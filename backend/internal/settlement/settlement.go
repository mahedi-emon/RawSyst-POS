// Package settlement matches what the acquirer deposited against what the till
// took (blueprint C12, design 02 §8).
//
// # What was wrong
//
// A card sale debits Card Settlement Clearing for the gross and credits revenue
// and VAT. Two days later the bank deposits the gross less its fee. Until this
// module existed nothing ever moved a tender out of `pending`, so the clearing
// account only grew: an asset on the balance sheet that was real money the shop
// had taken, could not spend, and could not tie to a bank statement. The
// blueprint's own words — "without this module the books never balance and the
// Owner never knows their real card cost".
//
// # The fee is a fact, not a calculation
//
// The fee is not derived from a configured rate. It is the difference between
// what was taken and what arrived. A rate would be a forecast, and a forecast
// posted into the ledger disagrees with the bank the first time the contract,
// the scheme mix or a mid-month change says otherwise — and the disagreement
// would land in the one account whose whole job is to reach zero.
//
// So a batch records what the bank did: these tenders, this deposit, this date.
// Gross comes from the tenders, net comes from the statement, and the fee is
// what is left over.
package settlement

import (
	"context"
	"errors"
	"time"

	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// moneyScale is two decimal places, the scale every posted amount carries.
// Tenders are stored at numeric(18,4) like all money in this schema, but what
// reaches the ledger is rounded, and the parts of a split fee must sum back to
// the figure on the bank statement.
const moneyScale = 2

// Service records settlements.
type Service struct{ pool *db.Pool }

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// PendingTender is one payment taken and not yet deposited.
type PendingTender struct {
	TenderID    uuid.UUID `json:"tender_id"`
	InvoiceID   uuid.UUID `json:"invoice_id"`
	HumanNumber string    `json:"invoice_number"`
	IssuedAt    string    `json:"issued_at"`
	Method      string    `json:"method"`
	Reference   string    `json:"reference,omitempty"`
	Amount      string    `json:"amount"`
}

// NewBatch is a deposit as it appears on a bank statement.
type NewBatch struct {
	// UUID is assigned by the caller, so recording the same deposit twice
	// because a response was lost returns the first batch rather than clearing
	// the same tenders into a second journal entry.
	UUID uuid.UUID

	Reference   string
	DepositedOn time.Time

	// NetAmount is what actually landed in the bank. The one figure that comes
	// from outside this system, and the reason nothing here needs a fee rate.
	NetAmount decimal.Decimal

	TenderIDs []uuid.UUID
}

// Batch is a recorded deposit.
type Batch struct {
	ID          uuid.UUID       `json:"id"`
	Reference   string          `json:"reference"`
	DepositedOn string          `json:"deposited_on"`
	Gross       string          `json:"gross_amount"`
	Fee         string          `json:"fee_amount"`
	Net         string          `json:"net_amount"`
	Tenders     []SettledTender `json:"tenders"`

	// AlreadyRecorded marks a replay: the caller sent a deposit that had been
	// recorded before, and this is the original.
	AlreadyRecorded bool `json:"already_recorded,omitempty"`
}

// SettledTender is one payment inside a deposit, with the share of the fee it
// carried.
type SettledTender struct {
	TenderID    uuid.UUID `json:"tender_id"`
	InvoiceID   uuid.UUID `json:"invoice_id"`
	HumanNumber string    `json:"invoice_number"`
	Method      string    `json:"method"`
	Amount      string    `json:"amount"`
	Fee         string    `json:"fee_amount"`
}

// Pending lists card money taken and not yet deposited.
//
// Cash is deliberately absent: it never clears through an acquirer, it goes
// into a drawer and is counted at the end of a shift, which is what the Z
// report and the cash-over/short posting are for. Listing it here would invite
// somebody to "settle" it and credit a clearing account it never debited.
func (s *Service) Pending(ctx context.Context, scope Scope) ([]PendingTender, error) {
	out := []PendingTender{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.id, i.id, coalesce(i.human_number, ''), i.issued_at,
			       t.method, coalesce(t.reference, ''), t.amount
			FROM sales_tender t
			JOIN sales_invoice i ON i.id = t.invoice_id
			WHERE i.company_id = $1
			  AND t.settlement_status = 'pending'
			  AND t.method <> ALL($2::text[])
			ORDER BY i.issued_at, t.tender_no`,
			scope.CompanyID, nonClearingMethods)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var p PendingTender
			var issuedAt time.Time
			var amount decimal.Decimal
			if e := rows.Scan(&p.TenderID, &p.InvoiceID, &p.HumanNumber, &issuedAt,
				&p.Method, &p.Reference, &amount); e != nil {
				return e
			}
			p.IssuedAt = issuedAt.Format(time.RFC3339)
			p.Amount = amount.StringFixed(moneyScale)
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// nonClearingMethods never pass through an acquirer, so they are never
// deposited as a batch and must not appear as pending settlement.
//
// Kept in step with sales.accountRoleFor: everything it sends to
// `card_clearing` is settleable, and everything it sends elsewhere is not.
var nonClearingMethods = []string{
	"cash", "customer_due", "store_credit", "loyalty_points",
	"bank_transfer", "cheque", "sadad", "exchange_clearing",
}

// Record enters a deposit and clears the tenders it covered.
//
//	Dr  Bank                     what actually arrived
//	Dr  Bank & Card Charges      what it cost to receive it
//	    Cr  Card Clearing        what was taken at the counter
func (s *Service) Record(ctx context.Context, scope Scope, in NewBatch) (Batch, error) {
	if in.UUID == uuid.Nil {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"A deposit must carry an identifier so a retry does not record it twice.")
	}
	if strings.TrimSpace(in.Reference) == "" {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"Say which line on the bank statement this deposit is. Matching a "+
				"deposit to the sales it covers is the point of recording it.")
	}
	if len(in.TenderIDs) == 0 {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"Say which payments this deposit covered.")
	}
	if !in.NetAmount.IsPositive() {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"A deposit of nothing is not a deposit. Money taken back by the "+
				"acquirer is a chargeback, which is recorded on its own.")
	}
	if in.DepositedOn.IsZero() {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"Say the date the money landed. It posts on that date, not on today.")
	}

	var out Batch
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyRecorded(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyRecorded = true
			return nil
		}

		var country string
		if e := tx.QueryRow(ctx, `SELECT country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		covered, gross, e := lockTenders(ctx, tx, scope, in.TenderIDs)
		if e != nil {
			return e
		}

		fee := gross.Sub(in.NetAmount)
		if fee.IsNegative() {
			return errs.Newf(errs.CodeInvalidInput,
				"This deposit of %s is more than the %s of payments it covers. "+
					"An acquirer paying more than was taken is a separate event, "+
					"not a fee.",
				in.NetAmount.StringFixed(moneyScale), gross.StringFixed(moneyScale))
		}

		var batchID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO settlement_batch
			  (tenant_id, company_id, uuid, reference, deposited_on,
			   gross_amount, fee_amount, net_amount, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.UUID, strings.TrimSpace(in.Reference),
			in.DepositedOn, gross, fee, in.NetAmount, scope.UserID,
		).Scan(&batchID); e != nil {
			return db.Translate(e, "That deposit has already been recorded.")
		}

		// One statement each rather than two per payment.
		//
		// A day's card takings is one deposit, and a busy shop can put several
		// hundred payments in it. At two round trips apiece that was a thousand
		// statements inside one transaction, holding row locks on every tender
		// for the duration. unnest expands the arrays server-side, so the cost
		// is one parse and one plan regardless of how large the batch is.
		shares := allocateFee(fee, gross, covered)
		tenderIDs := make([]uuid.UUID, len(covered))
		amounts := make([]decimal.Decimal, len(covered))
		for i, c := range covered {
			tenderIDs[i] = c.TenderID
			amounts[i] = c.amount
			covered[i].Fee = shares[i].StringFixed(moneyScale)
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO settlement_batch_tender (tenant_id, batch_id, tender_id, amount)
			SELECT $1, $2, t.id, t.amount
			FROM unnest($3::uuid[], $4::numeric[]) AS t(id, amount)`,
			scope.TenantID, batchID, tenderIDs, amounts); e != nil {
			return db.Translate(e,
				"One of those payments is already in another deposit.")
		}

		if _, e := tx.Exec(ctx, `
			UPDATE sales_tender t
			SET settlement_status = 'settled',
			    settled_at        = $2,
			    fee_amount        = s.fee
			FROM unnest($1::uuid[], $3::numeric[]) AS s(id, fee)
			WHERE t.id = s.id`,
			tenderIDs, in.DepositedOn, shares); e != nil {
			return e
		}

		result, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: in.DepositedOn, SourceType: "settlement_batch", SourceID: batchID,
			PostedBy: &scope.UserID, RuleKey: "payment.settlement",
			Memo: "Card settlement " + strings.TrimSpace(in.Reference),
		}, country, accounting.Transaction{
			Amounts: map[string]decimal.Decimal{
				"gross": gross,
				"fee":   fee,
				"net":   in.NetAmount,
			},
		})
		if e != nil {
			return e
		}
		if _, e := tx.Exec(ctx,
			`UPDATE settlement_batch SET journal_entry_id = $2 WHERE id = $1`,
			batchID, result.EntryID); e != nil {
			return e
		}

		out = Batch{
			ID: batchID, Reference: strings.TrimSpace(in.Reference),
			DepositedOn: in.DepositedOn.Format("2006-01-02"),
			Gross:       gross.StringFixed(moneyScale),
			Fee:         fee.StringFixed(moneyScale),
			Net:         in.NetAmount.StringFixed(moneyScale),
			Tenders:     settledFrom(covered),
		}
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return out, nil
}

// coveredTender is a tender inside a batch while it is being built: what the
// caller will be shown, plus the unrounded amount the allocation works on.
type coveredTender struct {
	SettledTender
	amount decimal.Decimal
}

// lockTenders reads the named payments for update and refuses any that are not
// this company's, not pending, or not the kind that clears through an acquirer.
//
// FOR UPDATE because two deposits naming the same payment at the same moment
// would otherwise both see it pending and both credit the clearing account for
// it, taking the account negative — the one outcome this module exists to
// prevent. The unique key on `settlement_batch_tender` is the backstop; the
// lock is what turns a confusing constraint violation into a clear refusal.
func lockTenders(
	ctx context.Context, tx pgx.Tx, scope Scope, ids []uuid.UUID,
) ([]coveredTender, decimal.Decimal, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id, i.id, coalesce(i.human_number, ''), t.method, t.amount,
		       t.settlement_status
		FROM sales_tender t
		JOIN sales_invoice i ON i.id = t.invoice_id
		WHERE t.id = ANY($1) AND i.company_id = $2
		ORDER BY i.issued_at, t.tender_no
		FOR UPDATE OF t`,
		ids, scope.CompanyID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	defer rows.Close()

	covered := make([]coveredTender, 0, len(ids))
	gross := decimal.Zero
	for rows.Next() {
		var c coveredTender
		var amount decimal.Decimal
		var status string
		if e := rows.Scan(&c.TenderID, &c.InvoiceID, &c.HumanNumber, &c.Method,
			&amount, &status); e != nil {
			return nil, decimal.Zero, e
		}
		if status != "pending" {
			return nil, decimal.Zero, errs.Newf(errs.CodeConflict,
				"A payment in this deposit is already marked %s. A payment "+
					"settles once; a later correction is its own entry.", status)
		}
		if !settleable(c.Method) {
			return nil, decimal.Zero, errs.Newf(errs.CodeInvalidInput,
				"A %s payment does not clear through an acquirer, so it is "+
					"never deposited as a batch.", c.Method)
		}
		c.Amount = amount.StringFixed(moneyScale)
		c.amount = amount
		covered = append(covered, c)
		gross = gross.Add(amount)
	}
	if e := rows.Err(); e != nil {
		return nil, decimal.Zero, e
	}

	// Reported as not found rather than as a partial success. Silently settling
	// the subset that happened to be visible would clear the account by the
	// wrong amount and leave the caller believing the whole deposit was
	// recorded.
	if len(covered) != len(ids) {
		return nil, decimal.Zero, errs.New(errs.CodeNotFound,
			"One of those payments was not found in this company.")
	}
	return covered, gross, nil
}

// allocateFee splits the acquirer's fee across the payments it was charged on.
//
// Proportionally, and the shares sum back to the fee on the bank statement
// EXACTLY — the same rounding-remainder rule the invoice discount, the FIFO
// layer draw, the base-currency allocation and the shortfall settlement all
// follow. The fee posts as one figure regardless, so this only decides what
// each sale is shown to have cost; being out by a hallala there is how a
// margin-per-method report stops agreeing with the P&L.
//
// Three constraints hold at once, and each of them broke the other two when it
// was added on its own:
//
//	sum(shares) == fee     the bank statement is the authority
//	0 <= share             a negative fee credits a sale it should charge
//	share <= its tender    a tender cannot cost more to receive than it was
func allocateFee(
	fee, gross decimal.Decimal, covered []coveredTender,
) []decimal.Decimal {
	shares := make([]decimal.Decimal, len(covered))
	if !fee.IsPositive() || !gross.IsPositive() {
		for i := range shares {
			shares[i] = decimal.Zero
		}
		return shares
	}

	// Allocated against the RUNNING TOTAL, not tender by tender.
	//
	// Rounding each share on its own and handing the remainder to the last
	// tender accumulates: twenty tenders of 3.33 and one of 0.01, with a fee
	// of 7.77, round up to 7.80 between them and leave the last tender a share
	// of MINUS 0.03. A negative fee share credits that sale with more than it
	// was worth, and the settlement no longer describes what the acquirer did.
	//
	// Taking each share as the difference between successive cumulative
	// targets bounds the error at one hallala for the whole batch rather than
	// one per tender, and because the final target is the fee itself the
	// shares still sum back to it exactly.
	//
	// This is the same defect, and the same fix, as the invoice-discount
	// allocation in sales/pricing.go. It was written twice because the shape
	// of the problem -- split an amount across lines, make the parts sum to
	// the whole -- turns up in both places and the obvious approach is wrong
	// in both.
	allocated := decimal.Zero
	cumulative := decimal.Zero
	for i, c := range covered {
		cumulative = cumulative.Add(c.amount)

		target := fee.Mul(cumulative).Div(gross).Round(moneyScale)
		share := target.Sub(allocated)

		// A tender cannot carry more fee than it is worth. Reachable when the
		// fee approaches the gross, where a share's exact value IS the tender.
		// The excess is left for the next tender, which picks it up because
		// the next share is measured against the cumulative target.
		if share.GreaterThan(c.amount) {
			share = c.amount
		}
		if share.IsNegative() {
			share = decimal.Zero
		}

		shares[i] = share
		allocated = allocated.Add(share)
	}

	// The clamp above is correct per tender and breaks the sum: whatever it
	// held back is passed to the NEXT tender, and the last tender has no next
	// one. A batch that ends on a clamped share allocates less than the fee,
	// and then the per-sale card cost no longer adds up to the figure on the
	// bank statement — which is the one number a shop reconciles against.
	//
	// So whatever is left goes back onto the tenders with room for it, in the
	// order they were taken. There is always room: the fee is gross minus a
	// deposit that must be positive, so it is strictly less than the gross,
	// and the gross is the sum of the very headroom being counted here.
	remainder := fee.Sub(allocated)
	for i := range shares {
		if !remainder.IsPositive() {
			break
		}
		headroom := covered[i].amount.Sub(shares[i])
		if !headroom.IsPositive() {
			continue
		}
		if headroom.GreaterThan(remainder) {
			headroom = remainder
		}
		shares[i] = shares[i].Add(headroom)
		remainder = remainder.Sub(headroom)
	}

	return shares
}

// Read returns a recorded deposit and the payments it covered.
func (s *Service) Read(ctx context.Context, scope Scope, id uuid.UUID) (Batch, error) {
	var out Batch
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		b, found, e := s.readBatch(ctx, tx, scope, "id = $1", id)
		if e != nil {
			return e
		}
		if !found {
			return errs.New(errs.CodeNotFound, "That deposit was not found.")
		}
		out = b
		return nil
	})
	return out, err
}

func (s *Service) alreadyRecorded(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Batch, bool, error) {
	return s.readBatch(ctx, tx, scope, "uuid = $1", docUUID)
}

func (s *Service) readBatch(
	ctx context.Context, tx pgx.Tx, scope Scope, where string, arg any,
) (Batch, bool, error) {
	var (
		b               Batch
		depositedOn     time.Time
		gross, fee, net decimal.Decimal
		batchID         uuid.UUID
	)
	err := tx.QueryRow(ctx, `
		SELECT id, reference, deposited_on, gross_amount, fee_amount, net_amount
		FROM settlement_batch
		WHERE `+where+` AND company_id = $2`, arg, scope.CompanyID).
		Scan(&batchID, &b.Reference, &depositedOn, &gross, &fee, &net)
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, false, nil
	}
	if err != nil {
		return Batch{}, false, err
	}

	b.ID = batchID
	b.DepositedOn = depositedOn.Format("2006-01-02")
	b.Gross = gross.StringFixed(moneyScale)
	b.Fee = fee.StringFixed(moneyScale)
	b.Net = net.StringFixed(moneyScale)
	b.Tenders = []SettledTender{}

	rows, err := tx.Query(ctx, `
		SELECT t.id, i.id, coalesce(i.human_number, ''), t.method,
		       l.amount, coalesce(t.fee_amount, 0)
		FROM settlement_batch_tender l
		JOIN sales_tender t   ON t.id = l.tender_id
		JOIN sales_invoice i  ON i.id = t.invoice_id
		WHERE l.batch_id = $1
		ORDER BY i.issued_at, t.tender_no`, batchID)
	if err != nil {
		return Batch{}, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var st SettledTender
		var amount, tenderFee decimal.Decimal
		if e := rows.Scan(&st.TenderID, &st.InvoiceID, &st.HumanNumber, &st.Method,
			&amount, &tenderFee); e != nil {
			return Batch{}, false, e
		}
		st.Amount = amount.StringFixed(moneyScale)
		st.Fee = tenderFee.StringFixed(moneyScale)
		b.Tenders = append(b.Tenders, st)
	}
	return b, true, rows.Err()
}

func settleable(method string) bool {
	for _, m := range nonClearingMethods {
		if m == method {
			return false
		}
	}
	return true
}

// settledFrom drops the working amount, leaving what the caller is shown.
func settledFrom(covered []coveredTender) []SettledTender {
	out := make([]SettledTender, 0, len(covered))
	for _, c := range covered {
		out = append(out, c.SettledTender)
	}
	return out
}
