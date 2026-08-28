package accounting

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// moneyScale is the precision a posted amount carries.
//
// Two decimals, and it cannot quietly become four: base_debit and base_credit
// are what every tie-out in the product compares against — inventory to the
// Inventory account (0020, 0055), the exchange clearing account to zero (0030),
// customer balances to receivables (0035, 0036), supplier balances to payables
// (0058) — and each of those reads the ledger at this scale.
const moneyScale int32 = 2

// minLedgerUnit is the smallest amount a posted line can carry: one unit at
// moneyScale.
//
// The schema does not merely prefer a line to be non-zero. journal_line's
// base_one_side CHECK wants base_debit > 0 on a debit line, and sides_agree
// wants (debit > 0) = (base_debit > 0), so a line with a real amount whose base
// share rounds away to nothing satisfies neither and cannot be written.
var minLedgerUnit = decimal.New(1, -moneyScale)

// Side is which column an amount lands in.
type Side string

const (
	Debit  Side = "debit"
	Credit Side = "credit"
)

// Line is one side of one leg of an entry.
//
// An account is named by ROLE, not by id. A chart of accounts differs per
// company — one tenant's Cash is 1100 and another's is 1010 — so a posting rule
// naming a concrete account could not be shared, and every company would need
// its own copy of every rule. The role is the stable name and account_role_map
// resolves it (migration 0015).
type Line struct {
	Role string

	// AccountID names the account DIRECTLY, for the one case where a role
	// cannot: an account the person recording the transaction chose.
	//
	// Design 02 rule 5 debits "Expense Account", meaning whichever head the
	// expense is for — and design 12 §1 offers Rent, Utilities, Salaries and
	// Marketing as separate accounts with no generic one among them. A fixed
	// role cannot name the one somebody picked, and minting a role per expense
	// head would put a shop's own categories into the vocabulary the posting
	// rules share. So the head names its account and the line carries it.
	//
	// It is checked against the company before it is written — see
	// resolveAccounts — because a caller-supplied account id is the one input
	// row-level security cannot vouch for on its own: another company's account
	// is in the same tenant, so RLS sees nothing wrong with it.
	//
	// Exactly one of Role and AccountID is set.
	AccountID *uuid.UUID

	Side   Side
	Amount decimal.Decimal

	StoreID       *uuid.UUID
	SubledgerType string
	SubledgerID   *uuid.UUID
	Memo          string
}

// Entry is a journal entry awaiting posting.
type Entry struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	Date      time.Time

	// SourceType, SourceID and RuleKey together are the idempotency key. A sale
	// replayed by a sync retry finds its entry already present and is not posted
	// twice — this is what makes Pillar 3's at-least-once delivery safe for
	// accounting.
	SourceType string
	SourceID   uuid.UUID
	RuleKey    string

	// RuleVersion records WHICH version of RuleKey produced this entry. Rules
	// are versioned and never edited, so an entry posted last March must stay
	// explainable by the rule that actually made it — today's rule may not be
	// the one that ran. Set by PostByRule; zero when lines were built by hand.
	RuleVersion int

	// Currency is the transaction currency; BaseCurrency is the company's.
	// One entry carries one rate: a single transaction settled at two different
	// rates is not a rate difference, it is two transactions.
	Currency     string
	BaseCurrency string
	FXRate       decimal.Decimal

	Memo     string
	PostedBy *uuid.UUID

	// ReversesID is set when this entry exists to put another one right.
	// Design 02 §2: corrections happen only by posting a reversing entry, never
	// by editing posted history. The original stays; this row is the audit trail.
	ReversesID *uuid.UUID

	// StoreID is the dimension the whole entry belongs to. Applied to every
	// line that does not name its own, because a dimension describes WHERE the
	// transaction happened rather than a property of one leg — and a rule,
	// which knows nothing about branches, cannot supply it.
	//
	// Losing this silently is how branch reporting stops working while every
	// total still adds up.
	StoreID *uuid.UUID

	Lines []Line
}

// Result says what happened.
type Result struct {
	EntryID uuid.UUID
	EntryNo int64

	// AlreadyPosted is true when this source had been posted before and nothing
	// new was written. Callers treat it as success: the books already say what
	// this caller wanted them to say.
	AlreadyPosted bool
}

// Post writes one balanced journal entry inside the caller's transaction.
//
// It takes a pgx.Tx rather than opening its own because C9.1 requires a sale
// and its journal entry to commit together or not at all. An eventually
// consistent posting pipeline would allow a sale to exist without its entry,
// and the trial balance would drift intermittently — which is worse than
// failing consistently, because it is only noticed at month end.
func Post(ctx context.Context, tx pgx.Tx, e Entry) (Result, error) {
	lines, err := usableLines(e.Lines)
	if err != nil {
		return Result{}, err
	}
	if e.StoreID != nil {
		for i := range lines {
			if lines[i].StoreID == nil {
				lines[i].StoreID = e.StoreID
			}
		}
	}

	debits, credits := sideTotals(lines)
	if !debits.Equal(credits) {
		// Refused here as well as by the deferred database trigger. The trigger
		// is the guarantee — it also stops a background job and a support
		// script — but it can only say which entry failed. This can say by how
		// much and in which direction, which is what a developer needs at
		// three in the morning.
		return Result{}, errs.Newf(errs.CodeInternal,
			"This entry does not balance: debits total %s against credits of %s, "+
				"a difference of %s.",
			debits, credits, debits.Sub(credits))
	}

	rate := e.FXRate
	if rate.IsZero() {
		rate = decimal.NewFromInt(1)
	}
	if rate.IsNegative() {
		return Result{}, errs.New(errs.CodeInvalidInput,
			"An exchange rate cannot be negative.")
	}

	periodID, err := resolvePeriod(ctx, tx, e.CompanyID, e.Date)
	if err != nil {
		return Result{}, err
	}

	entryID, entryNo, fresh, err := insertEntry(ctx, tx, e, periodID)
	if err != nil {
		return Result{}, err
	}
	if !fresh {
		return Result{EntryID: entryID, EntryNo: entryNo, AlreadyPosted: true}, nil
	}

	accounts, err := resolveAccounts(ctx, tx, e.CompanyID, lines)
	if err != nil {
		return Result{}, err
	}

	// Base amounts are allocated from the converted TOTAL, not converted line by
	// line. Converting each line independently and rounding it rounds many
	// times, and the sum of those roundings need not match on both sides — a
	// perfectly balanced entry in riyals would fail to balance in the base
	// currency, for no reason anyone could explain from the invoice.
	baseTotal := debits.Mul(rate).Round(moneyScale)
	baseDebits, err := allocate(baseTotal, amountsOn(lines, Debit))
	if err != nil {
		return Result{}, err
	}
	baseCredits, err := allocate(baseTotal, amountsOn(lines, Credit))
	if err != nil {
		return Result{}, err
	}

	var di, ci int
	for i, l := range lines {
		var debit, credit, baseDebit, baseCredit decimal.Decimal
		if l.Side == Debit {
			debit, baseDebit = l.Amount, baseDebits[di]
			di++
		} else {
			credit, baseCredit = l.Amount, baseCredits[ci]
			ci++
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, store_id, currency, fx_rate,
			   debit, credit, base_debit, base_credit,
			   subledger_type, subledger_id, memo)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			e.TenantID, entryID, i+1, accounts[accountKey(l)], l.StoreID,
			e.Currency, rate, debit, credit, baseDebit, baseCredit,
			nullText(l.SubledgerType), l.SubledgerID, nullText(l.Memo)); err != nil {
			return Result{}, db.Translate(err,
				"That accounting entry could not be written.")
		}
	}

	return Result{EntryID: entryID, EntryNo: entryNo}, nil
}

// usableLines drops zero-amount lines and checks what is left is postable.
//
// A zero line is dropped rather than refused because a rule that has a discount
// leg produces one on every sale, discounted or not. Making each caller filter
// them would put the same three lines of filtering in every module, and the one
// that forgets writes a line the database rejects for reasons unrelated to what
// the cashier did.
func usableLines(in []Line) ([]Line, error) {
	out := make([]Line, 0, len(in))
	for _, l := range in {
		if l.Amount.IsZero() {
			continue
		}
		if l.Amount.IsNegative() {
			// A negative debit is a credit, and writing it as a negative debit
			// makes every report that sums a column wrong. The caller states
			// which side it means.
			return nil, errs.Newf(errs.CodeInternal,
				"The %s line for %q is negative. State the other side instead.",
				l.Side, l.Role)
		}
		if l.Side != Debit && l.Side != Credit {
			return nil, errs.Newf(errs.CodeInternal,
				"%q is not a side of an accounting entry.", l.Side)
		}
		if l.Role == "" && l.AccountID == nil {
			return nil, errs.New(errs.CodeInternal,
				"An entry line must name the account role it posts to.")
		}
		if l.Role != "" && l.AccountID != nil {
			// Both would be ambiguous about which one decided where the money
			// went, and the answer matters: one is configuration the whole
			// tenant shares, the other is a choice somebody made once.
			return nil, errs.Newf(errs.CodeInternal,
				"An entry line names both the role %q and an account. It must "+
					"name one or the other.", l.Role)
		}
		out = append(out, l)
	}
	if len(out) < 2 {
		return nil, errs.New(errs.CodeInternal,
			"An accounting entry needs at least one debit and one credit.")
	}
	return out, nil
}

// FlipSides swaps every debit for a credit and every credit for a debit.
//
// That is what a reversing entry is: the same rule, the same amounts, the
// opposite sign. A negative debit is not a credit — journal_line_one_side
// refuses it — so the sides move, not the amounts.
func FlipSides(lines []Line) []Line {
	out := make([]Line, len(lines))
	for i, l := range lines {
		out[i] = l
		if l.Side == Debit {
			out[i].Side = Credit
		} else {
			out[i].Side = Debit
		}
	}
	return out
}

func sideTotals(lines []Line) (debits, credits decimal.Decimal) {
	debits, credits = decimal.Zero, decimal.Zero
	for _, l := range lines {
		if l.Side == Debit {
			debits = debits.Add(l.Amount)
		} else {
			credits = credits.Add(l.Amount)
		}
	}
	return debits, credits
}

func amountsOn(lines []Line, side Side) []decimal.Decimal {
	var out []decimal.Decimal
	for _, l := range lines {
		if l.Side == side {
			out = append(out, l.Amount)
		}
	}
	return out
}

// allocate splits total across parts in proportion, at ledger precision.
//
// Three things must hold of the result, and journal_line enforces every one of
// them with a CHECK rather than trusting the caller:
//
//   - The shares sum to exactly total. The same figure is allocated to both
//     sides, so an entry that balances in the transaction currency balances in
//     the base currency too — which is what assert_entry_balanced tests, on the
//     base columns, at commit.
//   - No share is negative (journal_line_amounts_non_negative).
//   - No share is zero. base_one_side wants base_debit > 0 on a debit line and
//     sides_agree wants (debit > 0) = (base_debit > 0); a line with a positive
//     amount and a zero base amount fails both.
//
// A CHECK does not misstate a figure. It aborts the transaction — so the sale,
// the receipt or the payment being recorded does not happen, and it will not
// happen on the retry either.
//
// # Cumulative targets, not a remainder on the last part
//
// Rounding each part's own share and handing the difference to the last part
// makes the shares sum to the whole, but the last one can come out NEGATIVE:
// several legs that each round up by a fraction take more than the whole between
// them, and the last is handed what is left, which is less than nothing.
//
// Seven tenders on a foreign-currency sale is enough. Six legs of equal value
// and one of a hallala, at a rate that puts the converted total at 9.05 against
// parts summing to 6.01: each of the six takes round(9.05 x 1 / 6.01, 2) = 1.51,
// which is 9.06 between them, and the seventh is handed 9.05 - 9.06 = -0.01.
//
// Taking each share as the difference between successive CUMULATIVE targets
// cannot go negative, because the targets only ever rise, and still sums exactly
// because the final target IS the total.
//
// This is the same defect and the same fix as allocateFee in settlement, the
// invoice-discount allocation in sales/pricing.go and allocateLandedCost in
// purchasing. Four sites, which is the argument for each of them saying so.
//
// # The floor, which proportion alone cannot give
//
// Proportion cannot keep a share off zero. A tender of one hallala on a sale in
// a currency worth less than half the base currency converts to under half a
// base hallala and rounds to nothing: the arithmetic is right and the entry is
// still unwritable. Any rate below 0.5 does it, which is every weaker currency
// a shop might take — and this product is built for companies trading across
// three countries at once.
//
// Every share is therefore floored at one ledger unit, paid for out of the
// largest shares so the sum stays exact. It moves a share by less than a
// hallala, and it is the only allocation the schema's own invariant permits.
func allocate(
	total decimal.Decimal, parts []decimal.Decimal,
) ([]decimal.Decimal, error) {
	out := make([]decimal.Decimal, len(parts))
	if len(parts) == 0 {
		return out, nil
	}

	sum := decimal.Zero
	for _, p := range parts {
		if !p.IsPositive() {
			// usableLines has already dropped the zeroes and refused the
			// negatives, so nothing reaches this today. It is refused rather
			// than worked around because the obvious workaround — the whole
			// total on one part and nothing on the rest — writes precisely the
			// zero share the floor below exists to prevent.
			return nil, errs.New(errs.CodeInternal,
				"An entry line has no amount to allocate the base currency by.")
		}
		sum = sum.Add(p)
	}

	// Below one unit per line there is no valid allocation at all, whatever the
	// proportions. Saying so beats writing an entry the database will reject:
	// the CHECK can only report which constraint failed, and "that accounting
	// entry could not be written" is not something a cashier can act on.
	floor := minLedgerUnit.Mul(decimal.NewFromInt(int64(len(parts))))
	if total.LessThan(floor) {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"This comes to %s in the company's own currency, which will not divide "+
				"across %d lines: the ledger records amounts down to %s and every "+
				"line needs at least that much. Record it in the company's currency.",
			total.StringFixed(moneyScale), len(parts),
			minLedgerUnit.StringFixed(moneyScale))
	}

	allocated := decimal.Zero
	cumulative := decimal.Zero
	for i, p := range parts {
		cumulative = cumulative.Add(p)
		target := total.Mul(cumulative).Div(sum).Round(moneyScale)
		out[i] = target.Sub(allocated)
		allocated = allocated.Add(out[i])
	}

	raiseToFloor(out)
	return out, nil
}

// raiseToFloor lifts any share that rounded below one ledger unit, taking what
// that costs from the largest shares so the total is unchanged.
//
// Only the shares proportion could not express are moved. The largest are the
// ones that can spare a unit without falling below the floor themselves, and
// total being at least one unit per line is what guarantees one of them can.
func raiseToFloor(out []decimal.Decimal) {
	deficit := decimal.Zero
	for i, share := range out {
		if short := minLedgerUnit.Sub(share); short.IsPositive() {
			deficit = deficit.Add(short)
			out[i] = minLedgerUnit
		}
	}

	for deficit.IsPositive() {
		largest := 0
		for i, share := range out {
			if share.GreaterThan(out[largest]) {
				largest = i
			}
		}

		spare := out[largest].Sub(minLedgerUnit)
		if !spare.IsPositive() {
			// Every share already sits on the floor, so the total was exactly
			// one unit a line and there is nothing to take. Unreachable: the
			// caller refuses anything smaller than that.
			return
		}
		if spare.GreaterThan(deficit) {
			spare = deficit
		}
		out[largest] = out[largest].Sub(spare)
		deficit = deficit.Sub(spare)
	}
}

// resolvePeriod finds the fiscal period a date falls in.
//
// The period is resolved from the TRANSACTION date, never from now. A sale
// synced three days after it was rung up belongs to the day it happened, or
// every offline sale near a month end lands in the wrong month.
func resolvePeriod(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, on time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	var state string
	err := tx.QueryRow(ctx, `
		SELECT id, state FROM fiscal_period
		WHERE company_id = $1 AND $2::date BETWEEN starts_on AND ends_on`,
		companyID, on).Scan(&id, &state)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errs.Newf(errs.CodeConflict,
			"No accounting period covers %s, so this cannot be posted. Ask an "+
				"owner to open the period for that date.",
			on.Format("2 January 2006"))
	}
	if err != nil {
		return uuid.Nil, err
	}
	if state != "open" {
		// The database trigger refuses this too. Saying it here means the till
		// gets a sentence a person can act on rather than a constraint name.
		return uuid.Nil, errs.Newf(errs.CodeConflict,
			"The accounting period covering %s is %s. Reopening it needs an "+
				"owner and a written reason.",
			on.Format("2 January 2006"), state)
	}
	return id, nil
}

// insertEntry claims a number and writes the header, or reports that this
// source was already posted.
func insertEntry(
	ctx context.Context, tx pgx.Tx, e Entry, periodID uuid.UUID,
) (id uuid.UUID, no int64, fresh bool, err error) {
	// Look first. Claiming a number and then hitting the conflict would burn it
	// and leave a permanent gap in the journal, which is exactly what an auditor
	// asks about — and a sync retry storm would burn one per attempt.
	err = tx.QueryRow(ctx, `
		SELECT id, entry_no FROM journal_entry
		WHERE source_type = $1 AND source_id = $2 AND coalesce(rule_key,'') = $3`,
		e.SourceType, e.SourceID, e.RuleKey).Scan(&id, &no)
	if err == nil {
		return id, no, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, false, err
	}

	if err = tx.QueryRow(ctx,
		`SELECT claim_entry_no($1)`, e.CompanyID).Scan(&no); err != nil {
		return uuid.Nil, 0, false, db.Translate(err,
			"That company was not found, so no entry number could be issued.")
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO journal_entry
		  (tenant_id, company_id, period_id, entry_no, entry_date,
		   source_type, source_id, rule_key, rule_version, memo, posted_by,
		   reverses_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		e.TenantID, e.CompanyID, periodID, no, e.Date,
		e.SourceType, e.SourceID, nullText(e.RuleKey), nullInt(e.RuleVersion),
		nullText(e.Memo), e.PostedBy, e.ReversesID).Scan(&id)
	if err != nil {
		return uuid.Nil, 0, false, db.Translate(err,
			"That accounting entry could not be posted.")
	}
	return id, no, true, nil
}

// accountKey is how a line is looked up in the resolved account map.
//
// A role is its own key. An account names itself, prefixed so a role and an
// account id can never collide in one map.
func accountKey(l Line) string {
	if l.AccountID != nil {
		return "id:" + l.AccountID.String()
	}
	return l.Role
}

// resolveAccounts maps every line to the account it posts to.
//
// Two kinds of line, checked in one place because they have to end up equally
// trustworthy:
//
//   - A ROLE is configuration. It resolves through account_role_map, and a
//     missing one names itself: an unmapped chart is a setup mistake, and the
//     message has to be specific enough for whoever configures it to fix it.
//
//   - An ACCOUNT ID came from the caller. It is verified to belong to THIS
//     company and to be postable, and refused if it is not. Row-level security
//     cannot do this job: another company inside the same tenant is visible to
//     it, so an id from a sister company's chart would pass every policy and
//     put an expense in the wrong set of books.
//
// Both checks are the same shape — resolve, then refuse anything that did not
// resolve — so a line can never reach the insert with a nil account and be
// written against whatever the zero UUID happens to be.
func resolveAccounts(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, lines []Line,
) (map[string]uuid.UUID, error) {
	roles := make([]string, 0, len(lines))
	ids := make([]uuid.UUID, 0, len(lines))
	seen := make(map[string]bool, len(lines))

	for _, l := range lines {
		key := accountKey(l)
		if seen[key] {
			continue
		}
		seen[key] = true
		if l.AccountID != nil {
			ids = append(ids, *l.AccountID)
		} else {
			roles = append(roles, l.Role)
		}
	}

	out := make(map[string]uuid.UUID, len(seen))

	if len(roles) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT m.role, m.account_id
			FROM account_role_map m
			JOIN account a ON a.id = m.account_id
			WHERE m.company_id = $1 AND m.role = ANY($2) AND a.is_postable`,
			companyID, roles)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var role string
			var id uuid.UUID
			if err := rows.Scan(&role, &id); err != nil {
				return nil, err
			}
			out[role] = id
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}

		for _, role := range roles {
			if _, ok := out[role]; !ok {
				return nil, errs.Newf(errs.CodeConflict,
					"This company has no account mapped to %q, or the one mapped to "+
						"it is a heading rather than an account that can be posted to. "+
						"Set it in the chart of accounts before trading.", role)
			}
		}
	}

	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT id FROM account
			WHERE id = ANY($1) AND company_id = $2 AND is_postable`,
			ids, companyID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out["id:"+id.String()] = id
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}

		for _, id := range ids {
			if _, ok := out["id:"+id.String()]; !ok {
				return nil, errs.New(errs.CodeInvalidInput,
					"One of those lines names an account that is not this "+
						"company's, or is a heading rather than an account that "+
						"can be posted to.")
			}
		}
	}

	return out, nil
}

// nullInt keeps an unset version out of the database as NULL, so "built by
// hand" and "version zero" stay distinguishable.
func nullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
