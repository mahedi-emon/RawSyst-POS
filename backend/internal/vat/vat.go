// Package vat prepares a tax return from the ledger.
//
// # Preparation, not filing
//
// This produces the figures a return is built from and reconciles them against
// the books. It does NOT produce a filed form. The box layout of the ZATCA VAT
// return is a regulatory value like any other, and no verified rule for it
// exists in the registry — so mapping these totals onto numbered boxes would be
// inventing a legal artefact from assumption, which the blueprint forbids in
// exactly these words.
//
// What is here is defensible: totals grouped by the tax treatments the registry
// says the country recognises, resolved at the transaction date, and checked
// against the VAT control accounts.
//
// # The reconciliation is the point
//
// Every figure on this return is arrived at twice, by paths that share nothing.
// Output tax is added up from the tax on every invoice line, and read again from
// the Output VAT account the posting engine produced. Input tax is read from the
// Input VAT account, and again from the tax on the supplier bills behind it.
// Each pair must agree exactly. When one does not, something posted wrongly —
// and finding that before a return is filed is the difference between a
// correction and a penalty.
//
// # What it does not decide
//
// Two things are deliberately left to Outstanding rather than guessed at, and
// both are named on the return itself: how input tax is apportioned where a
// business makes exempt supplies as well as taxable ones, and which numbered box
// each total belongs in. Neither is recorded in the registry, so either one
// would be a legal rule invented from assumption.
package vat

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service prepares returns.
type Service struct {
	pool  *db.Pool
	rules *registry.Service
}

// moneyScale is the precision every figure on this return is stated at,
// mirrored from the posting engine.
//
// It is fixed rather than natural. A return is read by a filing tool and by a
// browser that parses these strings into minor units, and a natural
// representation hands them "0.5" for fifty hallalas one period and "0" for
// nothing the next — two decimals always, so the scale is never inferred from
// the value.
const moneyScale int32 = 2

func NewService(pool *db.Pool, rules *registry.Service) *Service {
	return &Service{pool: pool, rules: rules}
}

// TreatmentLine is one tax treatment's contribution.
type TreatmentLine struct {
	Treatment string `json:"treatment"`

	// NetAmount is the value of supplies, excluding tax.
	NetAmount string `json:"net_amount"`

	// TaxAmount is the tax charged on them. Zero-rated supplies have a net
	// amount and no tax — which is a different thing from exempt supplies, and
	// the reason they are reported separately rather than netted together.
	TaxAmount string `json:"tax_amount"`

	InvoiceCount int64 `json:"invoice_count"`
}

// Return is a prepared, unfiled return.
type Return struct {
	Country string `json:"country"`
	From    string `json:"from"`
	To      string `json:"to"`

	// Model is 'vat' or 'sales_tax'. The distinction is not cosmetic: a sales
	// tax has no input side at all, so a US company's "net payable" is simply
	// what it collected.
	Model string `json:"model"`

	Supplies       []TreatmentLine `json:"supplies"`
	TotalNet       string          `json:"total_net"`
	OutputTaxTotal string          `json:"output_tax_total"`

	// InputTaxTotal is the tax paid on purchases and recoverable against
	// output, read from the Input VAT control account. Nil for a sales tax,
	// which has no input side at all.
	//
	// The ACCOUNT is the source rather than the supplier bills, and that is a
	// deliberate choice. The posting rules only ever debit it with tax the
	// design has already judged recoverable: where recovery is restricted the
	// tax is absorbed into the expense or the stock value and never reaches
	// this account (migration 0025, rule 5). So the account balance is the
	// recoverable figure by construction, and reading the bills instead would
	// report tax the business may not reclaim.
	InputTaxTotal *string `json:"input_tax_total"`

	// BilledInputTax is the same total arrived at independently, from the tax
	// on the supplier bills that have posted. It is the input side's equivalent
	// of the output reconciliation below. Nil for a sales tax.
	BilledInputTax *string `json:"billed_input_tax"`

	// InputDifference is BilledInputTax less InputTaxTotal. Nil for a sales tax.
	InputDifference *string `json:"input_difference"`

	NetPayable *string `json:"net_payable"`

	// LedgerOutputTax is the Output VAT control account balance over the same
	// period, arrived at independently through the posting engine.
	LedgerOutputTax string `json:"ledger_output_tax"`

	// Difference is the OUTPUT difference: tax charged on invoices less the
	// Output VAT account. InputDifference is its counterpart on the input side.
	Difference string `json:"difference"`

	// Reconciled is true only when BOTH sides agree. Each check may clear it
	// and none may set it, so the order they run in cannot decide the answer.
	Reconciled bool `json:"reconciled"`

	// Outstanding names what this return does NOT include and why. A figure a
	// business would act on has to carry its own caveats: input tax stated
	// before an apportionment it may be subject to, or a bill held out of the
	// ledger, is not wrong so much as incomplete, and only the return itself is
	// in a position to say so.
	Outstanding []string `json:"outstanding"`

	// Filed is always false. Stated explicitly so no caller can mistake a
	// preparation for a submission.
	Filed bool `json:"filed"`
}

// Prepare draws the return for a company over a period, both dates inclusive.
func (s *Service) Prepare(
	ctx context.Context, tenantID, companyID uuid.UUID, from, to time.Time,
) (Return, error) {
	if to.Before(from) {
		return Return{}, errs.New(errs.CodeInvalidInput,
			"The end of the period comes before its start.")
	}

	out := Return{
		From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
		Supplies: []TreatmentLine{},

		// Both reconciliations may only CLEAR this and neither may set it, so
		// one cannot undo the other's verdict and the order they run in does
		// not decide the answer.
		Reconciled: true,
	}

	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, companyID).
			Scan(&out.Country); e != nil {
			return db.Translate(e, "That company was not found.")
		}

		// The treatment list and the tax model come from the registry at the
		// END of the period, because that is the law the return is filed under.
		model, recoverable, treatments, e := s.taxModel(ctx, out.Country, to, tenantID)
		if e != nil {
			return e
		}
		out.Model = model

		if e := s.readSupplies(ctx, tx, companyID, from, to, &out); e != nil {
			return e
		}
		if e := s.reconcile(ctx, tx, companyID, from, to, &out); e != nil {
			return e
		}
		noteUnrecognisedTreatments(&out, treatments)

		if recoverable {
			if e := s.readInputTax(ctx, tx, companyID, from, to, &out); e != nil {
				return e
			}
		} else {
			// A sales tax has no input side, so what was collected IS what is
			// payable, and that figure is complete.
			net := out.OutputTaxTotal
			out.NetPayable = &net
		}

		out.Outstanding = append(out.Outstanding,
			"the official return form layout has not been verified against the "+
				"tax authority, so these totals are not mapped to numbered boxes")
		return nil
	})
	return out, err
}

// taxModel reads whether this country runs a VAT with a recoverable input side
// or a sales tax without one, and which treatments its law recognises.
func (s *Service) taxModel(
	ctx context.Context, country string, asOf time.Time, tenantID uuid.UUID,
) (model string, inputRecoverable bool, treatments []string, err error) {
	var payload struct {
		Model               string   `json:"model"`
		InputTaxRecoverable bool     `json:"input_tax_recoverable"`
		Treatments          []string `json:"treatments"`
	}
	q := registry.Query{
		Key:      treatmentKey(country),
		Country:  country,
		AsOf:     asOf,
		TenantID: tenantID,
	}
	if err = s.rules.Into(ctx, q, &payload); err != nil {
		return "", false, nil, err
	}
	if payload.Model == "" {
		return "", false, nil, errs.Newf(errs.CodeUnverifiedRule,
			"The tax model for %s is not recorded in the regulatory registry, so "+
				"a return cannot be prepared.", country)
	}
	return payload.Model, payload.InputTaxRecoverable, payload.Treatments, nil
}

// noteUnrecognisedTreatments reports supplies the law as of the period end does
// not classify.
//
// The package doc claims these totals are grouped by the treatments the registry
// says the country recognises. Nothing was checking it: the treatment travels on
// the invoice line, so a typo, a retired treatment, or a rate imported from
// another country's rules would appear as its own row and be filed unremarked.
//
// Reported rather than corrected. A posted invoice's treatment is what the
// customer was actually charged under, and reclassifying it here would make the
// return disagree with the document it came from — so the honest answer is to
// say the return contains something the registry cannot place.
//
// It does not clear Reconciled. That flag means the two paths to a figure agree,
// which is a different claim and still true: an unplaceable treatment is
// counted correctly on both sides.
func noteUnrecognisedTreatments(out *Return, recognised []string) {
	if len(recognised) == 0 {
		// The registry did not list any, so there is nothing to check against
		// and every treatment would look wrong. Silence beats a false alarm.
		return
	}
	known := make(map[string]bool, len(recognised))
	for _, t := range recognised {
		known[t] = true
	}

	var unknown []string
	for _, l := range out.Supplies {
		if !known[l.Treatment] {
			unknown = append(unknown, l.Treatment)
		}
	}
	if len(unknown) == 0 {
		return
	}
	out.Outstanding = append(out.Outstanding,
		"supplies are recorded under a tax treatment the registry does not list "+
			"for "+out.Country+" ("+strings.Join(unknown, ", ")+"), so they "+
			"cannot be placed on the return")
}

func treatmentKey(country string) string {
	switch country {
	case "us":
		return "US.SALESTAX.TAX_TREATMENTS"
	case "bd":
		return "BD.VAT.TAX_TREATMENTS"
	default:
		return "SA.VAT.TAX_TREATMENTS"
	}
}

// readSupplies totals net and tax by treatment over the period.
//
// A credit note's lines carry POSITIVE tax with a negative quantity, so the
// sign has to come from the document type. Summing the stored tax directly
// would add refunded tax to what is owed rather than subtracting it — and the
// return would overstate the liability by twice every refund.
func (s *Service) readSupplies(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	from, to time.Time, out *Return,
) error {
	rows, err := tx.Query(ctx, `
		SELECT l.tax_treatment,
		       sum(CASE WHEN i.doc_type = 'credit_note'
		                THEN -l.net_amount ELSE l.net_amount END),
		       sum(CASE WHEN i.doc_type = 'credit_note'
		                THEN -l.tax_amount ELSE l.tax_amount END),
		       count(DISTINCT i.id)
		FROM sales_invoice_line l
		JOIN sales_invoice i ON i.id = l.invoice_id
		WHERE i.company_id = $1
		  AND i.issue_date BETWEEN $2::date AND $3::date
		  AND i.state <> 'draft'
		GROUP BY l.tax_treatment
		ORDER BY l.tax_treatment`,
		companyID, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()

	totalNet, totalTax := decimal.Zero, decimal.Zero
	for rows.Next() {
		var l TreatmentLine
		var net, tax decimal.Decimal
		if err := rows.Scan(&l.Treatment, &net, &tax, &l.InvoiceCount); err != nil {
			return err
		}
		l.NetAmount = net.StringFixed(moneyScale)
		l.TaxAmount = tax.StringFixed(moneyScale)
		totalNet = totalNet.Add(net)
		totalTax = totalTax.Add(tax)
		out.Supplies = append(out.Supplies, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out.TotalNet = totalNet.StringFixed(moneyScale)
	out.OutputTaxTotal = totalTax.StringFixed(moneyScale)
	return nil
}

// reconcile checks the invoice-line total against the Output VAT account.
//
// Two independent paths to the same number. The invoice lines are what the
// customer was charged; the account balance is what the posting engine booked.
// A difference means a sale charged tax that never reached the ledger, or the
// ledger holds tax no invoice supports — and either way the return is wrong.
func (s *Service) reconcile(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	from, to time.Time, out *Return,
) error {
	var ledger decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN account_role_map m ON m.account_id = l.account_id
		WHERE e.company_id = $1
		  AND m.company_id = $1
		  AND m.role = 'output_vat'
		  AND e.entry_date BETWEEN $2::date AND $3::date`,
		companyID, from, to).Scan(&ledger)
	if err != nil {
		return err
	}

	charged, err := decimal.NewFromString(out.OutputTaxTotal)
	if err != nil {
		return err
	}

	diff := charged.Sub(ledger)
	out.LedgerOutputTax = ledger.StringFixed(moneyScale)
	out.Difference = diff.StringFixed(moneyScale)

	// Cleared, never set. readInputTax may have its own verdict and neither
	// check is entitled to overrule the other.
	if !diff.IsZero() {
		out.Reconciled = false
		out.Outstanding = append(out.Outstanding,
			"the tax charged on invoices does not match the Output VAT account: "+
				"this return cannot be filed until they agree")
	}
	return nil
}

// readInputTax fills in the recoverable input side and the net payable.
//
// # Why the account rather than the bills
//
// The figure comes from the Input VAT control account. That is not an
// implementation detail: the posting rules only ever debit that account with tax
// the design has already judged recoverable, because where recovery is
// restricted the tax is absorbed into the expense or the stock value and the tax
// line drops out of the entry entirely (migration 0025, rule 5). So the account
// holds the recoverable figure BY CONSTRUCTION, and adding up the bills instead
// would reclaim tax the business may not be entitled to.
//
// # Why the bills are read anyway
//
// As a second, independent path to the same number — the mirror image of the
// output reconciliation, and for the same reason. One is what suppliers charged;
// the other is what the engine booked as recoverable. A difference means a bill
// charged tax that never reached the ledger, or the ledger holds input tax no
// bill supports.
//
// They agree by construction while nothing is broken, and each step of that was
// checked rather than assumed. billing.go stores purchase_bill.tax_total and
// hands the same value to the rule; exactly one rule debits input_vat per bill,
// since purchase.credit and purchase.clear_accrual are chosen exclusively and
// the receipt rule has no tax leg at all (a supplier who has not invoiced has
// charged no tax); and both the matched path and the approval path post under
// the bill's OWN date, so a bill blocked in March and approved in June still
// lands in March. A difference is therefore evidence of a fault, not of a
// timing convention — including the day someone adds a genuinely
// non-recoverable path, which will announce itself here rather than quietly
// over-reclaiming.
func (s *Service) readInputTax(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	from, to time.Time, out *Return,
) error {
	// Debits less credits: Input VAT is an asset, the opposite convention to
	// the output account above. A credit here is a supplier credit note
	// reducing what may be reclaimed, which must lower the figure rather than
	// raise it.
	var ledger decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN account_role_map m ON m.account_id = l.account_id
		WHERE e.company_id = $1
		  AND m.company_id = $1
		  AND m.role = 'input_vat'
		  AND e.entry_date BETWEEN $2::date AND $3::date`,
		companyID, from, to).Scan(&ledger); err != nil {
		return err
	}

	// Posted is tested by the journal entry itself, not by a list of statuses.
	// postBill is the only thing that sets journal_entry_id, so the link exists
	// exactly when an entry does — whereas a status list has to be kept in step
	// with a CHECK that already carries six members and would fail silently
	// when a seventh arrived.
	var billed decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(b.tax_total), 0)
		FROM purchase_bill b
		WHERE b.company_id = $1
		  AND b.bill_date BETWEEN $2::date AND $3::date
		  AND b.journal_entry_id IS NOT NULL`,
		companyID, from, to).Scan(&billed); err != nil {
		return err
	}

	// Cash expenses reclaim input tax too, so the document side has to count
	// them or the reconciliation reports a difference on every return where a
	// shop paid its electricity bill.
	//
	// tax_RECOVERABLE, not tax_total. The two are the same figure for an
	// ordinary expense and deliberately different for a restricted one: E2.3
	// puts entertainment, some vehicles and fuel outside recovery, and 0071
	// absorbs that VAT into the expense rather than debiting Input VAT with it.
	// Summing tax_total here would claim it back on the return by the side
	// door, which is precisely what the head's flag exists to prevent.
	//
	// This is the case the comment above predicted — "the day someone adds a
	// genuinely non-recoverable path, which will announce itself here rather
	// than quietly over-reclaiming". It announced itself. The reconciliation
	// still compares two independent paths to one number; the document side
	// simply has two kinds of document in it now.
	var expensed decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(x.tax_recoverable), 0)
		FROM expense x
		WHERE x.company_id = $1
		  AND x.expense_date BETWEEN $2::date AND $3::date
		  AND x.journal_entry_id IS NOT NULL`,
		companyID, from, to).Scan(&expensed); err != nil {
		return err
	}
	billed = billed.Add(expensed)

	inputTax := ledger.StringFixed(moneyScale)
	billedTax := billed.StringFixed(moneyScale)
	inputDiff := billed.Sub(ledger)
	diffStr := inputDiff.StringFixed(moneyScale)

	out.InputTaxTotal = &inputTax
	out.BilledInputTax = &billedTax
	out.InputDifference = &diffStr

	if !inputDiff.IsZero() {
		out.Reconciled = false
		out.Outstanding = append(out.Outstanding,
			"the tax on supplier bills and expenses does not match the Input "+
				"VAT account: this return cannot be filed until they agree")
	}

	// What was collected, less what may be reclaimed. Negative is ordinary and
	// must not be clamped: a shop that bought more than it sold this period is
	// owed a refund, and reporting zero would forfeit it.
	output, err := decimal.NewFromString(out.OutputTaxTotal)
	if err != nil {
		return err
	}
	net := output.Sub(ledger).StringFixed(moneyScale)
	out.NetPayable = &net

	// Two things this figure genuinely does not account for, both named because
	// a return that looks complete is worse than one that admits a gap.
	//
	// Bills held by the three-way match are deliberately outside the ledger
	// until an approver puts their name to the discrepancy, so their input tax
	// is not here. It is deferred, not lost — and when it is approved it posts
	// under the BILL's date, so it lands in THIS period after the fact, which
	// is what makes saying so now worth the line.
	var blocked int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM purchase_bill
		WHERE company_id = $1
		  AND bill_date BETWEEN $2::date AND $3::date
		  AND status = 'blocked'`,
		companyID, from, to).Scan(&blocked); err != nil {
		return err
	}
	if blocked > 0 {
		out.Outstanding = append(out.Outstanding,
			"input tax on bills held by the three-way match is not included: "+
				"approving one posts it under the bill's own date, so it will "+
				"fall into this period after this return was prepared")
	}

	// And apportionment. Where a business makes exempt supplies alongside
	// taxable ones, input tax on what it bought to make BOTH is recoverable
	// only in proportion — and neither the proportion nor the method is in the
	// registry, so applying one would be inventing a rule. The figure above is
	// the unapportioned total, and this says so rather than letting a
	// part-exempt business over-reclaim without knowing it.
	for _, l := range out.Supplies {
		if l.Treatment != "exempt" {
			continue
		}
		if supplied, err := decimal.NewFromString(l.NetAmount); err == nil &&
			supplied.IsZero() {
			continue
		}
		out.Outstanding = append(out.Outstanding,
			"exempt supplies were made in this period, so input tax may be "+
				"recoverable only in proportion: no apportionment method is "+
				"recorded in the regulatory registry, and the figure above is "+
				"the full amount before any restriction")
		break
	}
	return nil
}
