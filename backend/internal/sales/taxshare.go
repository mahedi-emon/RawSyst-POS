// Splitting an invoice's tax between the authorities that levied it.
//
// Where tax is set nationally this file does nothing: one rate, one authority,
// and `sales_invoice.tax_total` says everything a return needs. Where it is
// levied by a state and a city at once, the shop files with each of them
// separately, and what each is owed has to be recorded at the time of sale —
// see 0111 for why it cannot be worked out again at filing time.
package sales

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// recordTaxShares writes each authority's portion of the tax on this invoice.
//
// # Apportioned by rate, with the remainder going to the largest share
//
// The tax actually charged is the sum of the line amounts, which has already
// been rounded to the currency. Splitting it back out by rate will not divide
// evenly, so the shares are computed proportionally and the leftover penny is
// given to one of them. The parts therefore add to the whole exactly, and a
// return built from them agrees with the invoice it came from.
//
// Deriving each share as `net * shareRate` independently would be the obvious
// alternative and is wrong: three shares rounded independently can sum to a
// figure the customer was never charged.
//
// The remainder goes to the authority with the LARGEST rate rather than to the
// last one in the walk, which is where this codebase's usual
// last-part-takes-the-remainder rule would put it. That rule assumes the parts
// are interchangeable and here they are not: the walk ends at the country root,
// which in the United States levies zero, so the ordinary rule would hand a
// stray penny to an authority that charges nothing and is owed nothing. An
// authority that levies 0% must be apportioned exactly 0, or the shop would be
// filing a return for tax it never charged.
func recordTaxShares(
	ctx context.Context, tx pgx.Tx, tenantID, invoiceID uuid.UUID,
	sale Sale, computed ComputedSale,
) error {
	if len(sale.TaxShares) == 0 {
		return nil
	}

	for treatment, combined := range sale.TaxShares {
		if len(combined.Shares) == 0 || combined.Total.IsZero() {
			// Nothing to apportion. A chain of authorities that all levy zero
			// is a real and legal state, and it owes nobody anything.
			continue
		}

		// What this treatment actually collected on this invoice.
		charged := decimal.Zero
		for _, l := range computed.Lines {
			if l.TaxTreatment == treatment {
				charged = charged.Add(l.TaxAmount)
			}
		}
		if charged.IsZero() {
			continue
		}

		rates := make([]decimal.Decimal, len(combined.Shares))
		for i, sh := range combined.Shares {
			rates[i] = sh.Rate
		}
		amounts := apportion(charged, rates, combined.Total)

		for i, sh := range combined.Shares {
			amount := amounts[i]

			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_invoice_tax_share
				  (tenant_id, invoice_id, jurisdiction_id, treatment,
				   level, code, name, rate, tax_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				tenantID, invoiceID, sh.JurisdictionID, treatment,
				sh.Level, sh.Code, sh.Name, sh.Rate, amount); err != nil {
				return db.Translate(err,
					"The tax on that sale could not be attributed to the "+
						"authorities that levied it.")
			}
		}
	}
	return nil
}

// apportion splits an amount across shares in proportion to their rates.
//
// The leftover from rounding goes to the authority with the LARGEST rate, not
// to the last one as this codebase's usual last-part-takes-the-remainder rule
// would have it. That rule assumes the parts are interchangeable and here they
// are not: the walk ends at the country root, which in the United States levies
// zero, so the ordinary rule would hand a stray penny to an authority that
// charges nothing and is owed nothing. An authority that levies 0% must be
// apportioned exactly 0, or a shop would file a return for tax it never
// charged.
func apportion(total decimal.Decimal, rates []decimal.Decimal,
	rateTotal decimal.Decimal) []decimal.Decimal {

	out := make([]decimal.Decimal, len(rates))
	running, largest := decimal.Zero, 0
	for i, r := range rates {
		out[i] = total.Mul(r).Div(rateTotal).Round(moneyScale)
		running = running.Add(out[i])
		if r.GreaterThan(rates[largest]) {
			largest = i
		}
	}
	out[largest] = out[largest].Add(total.Sub(running))
	return out
}

// recordCreditNoteTaxShares credits a return against the authorities that were
// paid on the original sale.
//
// # At the ORIGINAL rates, not today's
//
// The shares are read from the invoice being corrected rather than resolved
// afresh from the rate table. A rate that changed between the sale and the
// return would otherwise credit the state for tax the customer never paid it
// and leave the city short — the return has to reverse what was actually
// charged, which is a fact recorded on the original invoice.
//
// # Positive, like the credit note itself
//
// `sales_invoice.tax_total` on a credit note is positive and the document type
// carries the direction, so these follow it. A remittance query nets invoices
// against credit notes by doc_type, exactly as it does for the invoice totals.
func recordCreditNoteTaxShares(
	ctx context.Context, tx pgx.Tx, tenantID, creditNoteID, originalID uuid.UUID,
	taxTotal decimal.Decimal,
) error {
	if taxTotal.IsZero() {
		return nil
	}

	type share struct {
		jurisdictionID         uuid.UUID
		treatment, level, code string
		name                   string
		rate                   decimal.Decimal
	}

	rows, err := tx.Query(ctx, `
		SELECT jurisdiction_id, treatment, level, code, name, rate
		  FROM sales_invoice_tax_share
		 WHERE invoice_id = $1
		 ORDER BY rate DESC, code`, originalID)
	if err != nil {
		return db.Translate(err, "")
	}
	defer rows.Close()

	var shares []share
	rateTotal := decimal.Zero
	for rows.Next() {
		var sh share
		if e := rows.Scan(&sh.jurisdictionID, &sh.treatment, &sh.level,
			&sh.code, &sh.name, &sh.rate); e != nil {
			return db.Translate(e, "")
		}
		shares = append(shares, sh)
		rateTotal = rateTotal.Add(sh.rate)
	}
	if err := rows.Err(); err != nil {
		return db.Translate(err, "")
	}
	// A sale taxed nationally has no shares to reverse.
	if len(shares) == 0 || rateTotal.IsZero() {
		return nil
	}

	rates := make([]decimal.Decimal, len(shares))
	for i, sh := range shares {
		rates[i] = sh.rate
	}
	amounts := apportion(taxTotal, rates, rateTotal)

	for i, sh := range shares {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_invoice_tax_share
			  (tenant_id, invoice_id, jurisdiction_id, treatment,
			   level, code, name, rate, tax_amount)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			tenantID, creditNoteID, sh.jurisdictionID, sh.treatment,
			sh.level, sh.code, sh.name, sh.rate, amounts[i]); err != nil {
			return db.Translate(err,
				"The tax on that return could not be attributed to the "+
					"authorities that levied it.")
		}
	}
	return nil
}
