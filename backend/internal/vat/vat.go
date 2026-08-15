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
// against the Output VAT control account.
//
// # The reconciliation is the point
//
// Two independent paths arrive at output tax. One adds up the tax on every
// invoice line; the other reads the Output VAT account balance the posting
// engine produced. They must agree exactly. When they do not, something posted
// wrongly — and finding that before a return is filed is the difference between
// a correction and a penalty.
package vat

import (
	"context"
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
	// output. Nil when it cannot be known — see Outstanding.
	InputTaxTotal *string `json:"input_tax_total"`

	NetPayable *string `json:"net_payable"`

	// LedgerOutputTax is the Output VAT control account balance over the same
	// period, arrived at independently through the posting engine.
	LedgerOutputTax string `json:"ledger_output_tax"`
	Difference      string `json:"difference"`
	Reconciled      bool   `json:"reconciled"`

	// Outstanding names what this return does NOT include and why. A return
	// that silently reported zero input tax would look complete and understate
	// what the business can reclaim.
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
	}

	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, companyID).
			Scan(&out.Country); e != nil {
			return db.Translate(e, "That company was not found.")
		}

		// The treatment list and the tax model come from the registry at the
		// END of the period, because that is the law the return is filed under.
		model, recoverable, e := s.taxModel(ctx, out.Country, to, tenantID)
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

		// Input tax cannot be known: there is no purchasing module, so no
		// supplier invoice has ever been recorded. Reporting zero would look
		// like a business that reclaimed nothing rather than one whose
		// purchases were never entered.
		if recoverable {
			out.Outstanding = append(out.Outstanding,
				"input tax on purchases is not included: purchasing is not built, "+
					"so no supplier invoice has been recorded")
		}
		out.Outstanding = append(out.Outstanding,
			"the official return form layout has not been verified against the "+
				"tax authority, so these totals are not mapped to numbered boxes")

		if !recoverable {
			// A sales tax has no input side, so what was collected IS what is
			// payable, and that figure is complete.
			net := out.OutputTaxTotal
			out.NetPayable = &net
		}
		return nil
	})
	return out, err
}

// taxModel reads whether this country runs a VAT with a recoverable input side
// or a sales tax without one.
func (s *Service) taxModel(
	ctx context.Context, country string, asOf time.Time, tenantID uuid.UUID,
) (model string, inputRecoverable bool, err error) {
	var payload struct {
		Model               string `json:"model"`
		InputTaxRecoverable bool   `json:"input_tax_recoverable"`
	}
	q := registry.Query{
		Key:      treatmentKey(country),
		Country:  country,
		AsOf:     asOf,
		TenantID: tenantID,
	}
	if err = s.rules.Into(ctx, q, &payload); err != nil {
		return "", false, err
	}
	if payload.Model == "" {
		return "", false, errs.Newf(errs.CodeUnverifiedRule,
			"The tax model for %s is not recorded in the regulatory registry, so "+
				"a return cannot be prepared.", country)
	}
	return payload.Model, payload.InputTaxRecoverable, nil
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
		l.NetAmount, l.TaxAmount = net.String(), tax.String()
		totalNet = totalNet.Add(net)
		totalTax = totalTax.Add(tax)
		out.Supplies = append(out.Supplies, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out.TotalNet = totalNet.String()
	out.OutputTaxTotal = totalTax.String()
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
	out.LedgerOutputTax = ledger.String()
	out.Difference = diff.String()
	out.Reconciled = diff.IsZero()

	if !out.Reconciled {
		out.Outstanding = append(out.Outstanding,
			"the tax charged on invoices does not match the Output VAT account: "+
				"this return cannot be filed until they agree")
	}
	return nil
}
