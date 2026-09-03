// Selling from the back office rather than from a till.
//
// `Sell` resolves a terminal from a device, because a POS sale is rung on one.
// An order invoiced after delivery has no device, no drawer and no e-invoicing
// unit — but it is the same sale in every other respect, and it must be priced,
// taxed, costed and posted by the same code. Anything else would be a second
// definition of what a sale is worth.
package sales

import (
	"context"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// FinalizeForCompany prices and finalises a sale that was not rung on a till.
//
// The caller supplies the terminal — company, store, warehouse — because it
// knows where the goods came from; everything the till is NOT allowed to choose
// is still read from the company here, exactly as `Sell` does it: the tax
// rules, the rates, the base currency and the negative-stock policy.
//
// A terminal with no EGS unit is not on the e-invoicing chain, which is what
// keeps a back-office invoice out of a chain it was never part of.
func (s *Service) FinalizeForCompany(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale,
) (Finalized, error) {
	if term.CompanyID == uuid.Nil || term.StoreID == uuid.Nil {
		return Finalized{}, errs.New(errs.CodeInternal,
			"A back-office sale must name the company and the shop it is sold "+
				"from.")
	}

	var profile companyProfile
	profile.storeID = term.StoreID
	if err := tx.QueryRow(ctx, `
		SELECT country, base_currency, negative_stock_policy::text
		FROM company WHERE id = $1`, term.CompanyID).
		Scan(&profile.country, &profile.baseCurrency,
			&profile.stockPolicy); err != nil {
		if err == pgx.ErrNoRows {
			return Finalized{}, errs.New(errs.CodeNotFound,
				"That company was not found.")
		}
		return Finalized{}, err
	}
	term.Country = profile.country

	if err := s.applyTaxProfile(ctx, tx, &sale, profile, term.TenantID); err != nil {
		return Finalized{}, err
	}
	return s.Finalize(ctx, tx, term, sale)
}
