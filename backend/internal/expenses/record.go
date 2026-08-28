package expenses

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// NewExpense is a payment being recorded.
type NewExpense struct {
	// UUID is assigned by the caller, so recording the same receipt twice
	// because a response was lost gives back the first one rather than paying
	// the electricity bill into the books a second time.
	UUID uuid.UUID

	Date        time.Time
	StoreID     *uuid.UUID
	SupplierID  *uuid.UUID
	Reference   string
	Description string

	// PaidFrom is 'cash' or 'bank' — the two rule 5 names. A role rather than
	// an account id because these two ARE configuration: every company has
	// exactly one of each and the chart already maps them.
	PaidFrom string

	Lines []NewLine
}

// NewLine is one head and what was spent on it.
type NewLine struct {
	HeadID      uuid.UUID
	Description string

	// Net is the amount before tax. The till convention throughout this
	// product is that the CALLER states net and the server computes the tax,
	// so a client cannot quietly decide what the VAT return claims.
	Net decimal.Decimal

	// TaxTreatment is validated against the country's rules for the expense
	// date. A shop paying a zero-rated supplier states so; it does not get to
	// state the rate.
	TaxTreatment string
}

// Expense is a recorded payment.
type Expense struct {
	ID          uuid.UUID `json:"id"`
	ExpenseNo   string    `json:"expense_no"`
	Date        string    `json:"expense_date"`
	Reference   string    `json:"reference,omitempty"`
	Description string    `json:"description,omitempty"`
	PaidFrom    string    `json:"paid_from"`
	Store       string    `json:"store,omitempty"`
	Supplier    string    `json:"supplier,omitempty"`
	Currency    string    `json:"currency"`

	SubtotalNet    string `json:"subtotal_net"`
	TaxTotal       string `json:"tax_total"`
	TaxRecoverable string `json:"tax_recoverable"`
	TaxAbsorbed    string `json:"tax_absorbed"`
	Total          string `json:"total_inclusive"`

	Lines []Line `json:"lines,omitempty"`

	// AlreadyRecorded marks a replay: the caller sent a receipt that had been
	// recorded before, and this is the original.
	AlreadyRecorded bool `json:"already_recorded,omitempty"`
}

// Line is one head's share of an expense.
type Line struct {
	HeadID      uuid.UUID `json:"head_id"`
	Head        string    `json:"head"`
	Description string    `json:"description,omitempty"`

	Net          string `json:"net_amount"`
	TaxTreatment string `json:"tax_treatment"`
	TaxRate      string `json:"tax_rate"`
	Tax          string `json:"tax_amount"`

	TaxRecoverable string `json:"tax_recoverable"`
	// TaxAbsorbed is the VAT this head cannot reclaim, which is charged to the
	// expense instead. Reported separately because "why is this 115 when the
	// invoice says 100" has to have an answer on the face of the document.
	TaxAbsorbed string `json:"tax_absorbed"`
	Charge      string `json:"charge_amount"`
}

// Record enters an expense and posts it.
//
//	Dr  each head's account      its net, plus any VAT it cannot reclaim
//	Dr  Input VAT Receivable     only the part that can be reclaimed
//	    Cr  Cash / Bank          the whole gross, which is what left the bank
//
// Design 02 rule 5, and the rule is resolved from the registry at the expense
// DATE like every other posting in this product: a receipt keyed a week late
// posts the way it would have posted on the day it was incurred.
func (s *Service) Record(
	ctx context.Context, scope Scope, in NewExpense,
) (Expense, error) {
	if in.UUID == uuid.Nil {
		return Expense{}, errs.New(errs.CodeInvalidInput,
			"An expense must carry an identifier so a retry does not record it twice.")
	}
	if len(in.Lines) == 0 {
		return Expense{}, errs.New(errs.CodeInvalidInput,
			"Say what the money was spent on.")
	}
	if in.PaidFrom != "cash" && in.PaidFrom != "bank" {
		return Expense{}, errs.Validation(
			"Say whether this was paid from cash or from the bank.").
			WithField("paid_from", "Choose cash or bank.")
	}
	if in.Date.IsZero() {
		return Expense{}, errs.Validation(
			"Say the date this was spent. It posts on that date, not on today.").
			WithField("expense_date", "A date is required.")
	}
	for i, l := range in.Lines {
		if l.HeadID == uuid.Nil {
			return Expense{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say which category it belongs to.", i+1)
		}
		if !l.Net.IsPositive() {
			return Expense{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no amount. An expense of nothing is not an expense.", i+1)
		}
	}

	var out Expense
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyRecorded(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyRecorded = true
			return nil
		}

		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		priced, e := s.price(ctx, tx, scope, country, in)
		if e != nil {
			return e
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var expenseID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO expense
			  (tenant_id, company_id, uuid, expense_no, expense_date, store_id,
			   supplier_id, reference, description, paid_from, currency,
			   subtotal_net, tax_total, tax_recoverable, tax_absorbed,
			   total_inclusive, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,nullif(btrim($8),''),nullif(btrim($9),''),
			        $10,$11,$12,$13,$14,$15,$16,$17)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.UUID, number, in.Date,
			in.StoreID, in.SupplierID, in.Reference, in.Description,
			in.PaidFrom, currency,
			priced.net, priced.tax, priced.recoverable, priced.absorbed,
			priced.total, scope.UserID).Scan(&expenseID); e != nil {
			return db.Translate(e, "That expense has already been recorded.")
		}

		for i, l := range priced.lines {
			if _, e := tx.Exec(ctx, `
				INSERT INTO expense_line
				  (tenant_id, expense_id, line_no, head_id, description,
				   net_amount, tax_treatment, tax_rate, tax_amount,
				   tax_recoverable, tax_absorbed, charge_amount)
				VALUES ($1,$2,$3,$4,nullif(btrim($5),''),$6,$7,$8,$9,$10,$11,$12)`,
				scope.TenantID, expenseID, i+1, l.headID, l.description,
				l.net, l.treatment, l.rate, l.tax,
				l.recoverable, l.absorbed, l.charge); e != nil {
				return db.Translate(e, "That expense line could not be recorded.")
			}
		}

		if e := s.post(ctx, tx, scope, expenseID, country, in, priced); e != nil {
			return e
		}

		read, e := s.read(ctx, tx, scope, expenseID)
		if e != nil {
			return e
		}
		out = read
		return nil
	})
	return out, err
}

// pricedLine is one line after the tax and the recoverability split.
type pricedLine struct {
	headID      uuid.UUID
	accountID   uuid.UUID
	headName    string
	description string
	treatment   string

	net, rate, tax            decimal.Decimal
	recoverable, absorbed     decimal.Decimal
	charge                    decimal.Decimal
	inputVATRecoverableAtTime bool
}

type priced struct {
	lines                           []pricedLine
	net, tax, recoverable, absorbed decimal.Decimal
	total                           decimal.Decimal
}

// price computes the tax on every line and splits it by the head's flag.
//
// The RATE comes from the registry at the expense date, never from the caller:
// a client that could state its own VAT rate could state what the return
// claims. The TREATMENT comes from the caller, because only they know whether
// the supplier charged VAT — but it is checked against the treatments the
// country allows on that date.
func (s *Service) price(
	ctx context.Context, tx pgx.Tx, scope Scope, country string, in NewExpense,
) (priced, error) {
	var out priced
	out.net, out.tax = decimal.Zero, decimal.Zero
	out.recoverable, out.absorbed = decimal.Zero, decimal.Zero

	if s.rules == nil {
		return priced{}, errs.New(errs.CodeInternal,
			"The expense service was built without the regulatory rule registry.")
	}

	rules, err := catalog.TaxRulesFor(ctx, s.rules, country, in.Date, scope.TenantID)
	if err != nil {
		return priced{}, err
	}
	standardRate, err := s.rules.VATRate(ctx, country, in.Date, scope.TenantID)
	if err != nil {
		return priced{}, err
	}

	for i, l := range in.Lines {
		head, e := s.headForPosting(ctx, tx, scope, l.HeadID)
		if e != nil {
			return priced{}, e
		}

		treatment := strings.TrimSpace(l.TaxTreatment)
		if treatment == "" {
			treatment = "standard"
		}
		if !rules.Allows(treatment) {
			return priced{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d is marked %q, which is not a tax treatment %s uses.",
				i+1, treatment, strings.ToUpper(country))
		}

		rate := decimal.Zero
		if treatment == "standard" {
			rate = standardRate
		}

		net := l.Net.Round(lineScale)
		tax := net.Mul(rate).Round(lineScale)

		// The split E2.3 asks for. All of it or none of it: VAT recovery is
		// restricted by CATEGORY, not apportioned within one.
		recoverable, absorbed := tax, decimal.Zero
		if !head.recoverable {
			recoverable, absorbed = decimal.Zero, tax
		}
		// A country with no input tax to reclaim at all — the USA, where sales
		// tax is charged once at retail and there is nothing to offset — has no
		// recoverable half whatever the head says. Claiming one would produce a
		// receivable against a tax authority that has no such mechanism.
		if !rules.InputTaxRecoverable {
			recoverable, absorbed = decimal.Zero, tax
		}

		out.lines = append(out.lines, pricedLine{
			headID: l.HeadID, accountID: head.accountID, headName: head.name,
			description: l.Description, treatment: treatment,
			net: net, rate: rate, tax: tax,
			recoverable: recoverable, absorbed: absorbed,
			charge:                    net.Add(absorbed),
			inputVATRecoverableAtTime: head.recoverable,
		})

		out.net = out.net.Add(net)
		out.tax = out.tax.Add(tax)
		out.recoverable = out.recoverable.Add(recoverable)
		out.absorbed = out.absorbed.Add(absorbed)
	}

	out.total = out.net.Add(out.tax)
	return out, nil
}

type headForPosting struct {
	accountID   uuid.UUID
	name        string
	recoverable bool
}

// headForPosting reads the head a line names, refusing one that is not this
// company's or has been retired.
//
// Retired is refused rather than allowed-but-hidden: a head is retired because
// the shop stopped using that category, and a client holding a stale list
// should be told rather than quietly booking into it.
func (s *Service) headForPosting(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (headForPosting, error) {
	var h headForPosting
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT account_id, name, input_vat_recoverable, is_active
		FROM expense_head WHERE id = $1 AND company_id = $2`,
		id, scope.CompanyID).Scan(&h.accountID, &h.name, &h.recoverable, &active)

	if errors.Is(err, pgx.ErrNoRows) {
		return headForPosting{}, errs.New(errs.CodeInvalidInput,
			"One of those expense categories is not this business's.")
	}
	if err != nil {
		return headForPosting{}, err
	}
	if !active {
		return headForPosting{}, errs.Newf(errs.CodeInvalidInput,
			"The category %q has been retired, so nothing new can be booked to "+
				"it. Pick another, or restore it first.", h.name)
	}
	return h, nil
}

// post writes the journal entry through rule 5.
func (s *Service) post(
	ctx context.Context, tx pgx.Tx, scope Scope, expenseID uuid.UUID,
	country string, in NewExpense, p priced,
) error {
	// One group member per line, each naming the head's own ACCOUNT. This is
	// what rule 5's `for_each: expense_lines` expands into, and what a fixed
	// role could never express.
	heads := make(accounting.Group, 0, len(p.lines))
	for _, l := range p.lines {
		account := l.accountID
		heads = append(heads, accounting.GroupMember{
			AccountID: &account,
			Amount:    l.charge,
			Memo:      l.headName,
		})
	}

	result, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date:       in.Date,
		SourceType: "expense", SourceID: expenseID,
		StoreID: in.StoreID,
		RuleKey: "expense.cash", PostedBy: &scope.UserID,
		Memo: expenseMemo(in),
	}, country, accounting.Transaction{
		Amounts: accounting.Amounts{"recoverable_tax": p.recoverable},
		Groups: map[string]accounting.Group{
			"expense_lines": heads,
			// The whole gross left the account, including the VAT that was
			// absorbed rather than reclaimed. Crediting only the net would
			// leave the bank saying it still held money it had paid out.
			"payments": {{Role: in.PaidFrom, Amount: p.total, Memo: in.PaidFrom}},
		},
	})
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE expense SET journal_entry_id = $2 WHERE id = $1`,
		expenseID, result.EntryID)
	return err
}

func expenseMemo(in NewExpense) string {
	if d := strings.TrimSpace(in.Description); d != "" {
		return d
	}
	if r := strings.TrimSpace(in.Reference); r != "" {
		return "Expense " + r
	}
	return "Expense"
}

func claimNumber(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (string, error) {
	var n string
	err := tx.QueryRow(ctx, `SELECT claim_expense_no($1)`, companyID).Scan(&n)
	return n, err
}
