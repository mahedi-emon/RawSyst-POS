package api

// Letting the report export take a tax return away as a file.
//
// The reports package draws the statements and writes the CSVs; the vat package
// prepares the return. Neither knows about the other, and neither should: a
// trial balance has nothing to do with a tax return, and making the reports
// service construct a tax service and a regulatory registry to draw one would
// be a dependency paid on every export.
//
// So `reports.VATPreparer` is a one-method interface with a shape of its own,
// and this is the conversion. It lives here because this is where both packages
// are already known — and putting it here means that when the tax return gains
// a field, this file is what fails to compile, which is the right place to be
// asked whether the export should carry it.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
)

// VATForExport adapts the tax service to what the report export asks for.
type VATForExport struct{ svc *vat.Service }

// NewVATForExport wires a tax service into the export.
func NewVATForExport(svc *vat.Service) *VATForExport {
	return &VATForExport{svc: svc}
}

// PrepareReturn draws the return and carries across what the file needs.
func (a *VATForExport) PrepareReturn(
	ctx context.Context, tenantID, companyID uuid.UUID, from, to time.Time,
) (reports.TaxReturn, error) {
	r, err := a.svc.Prepare(ctx, tenantID, companyID, from, to)
	if err != nil {
		return reports.TaxReturn{}, err
	}

	supplies := make([]reports.TaxSupply, 0, len(r.Supplies))
	for _, l := range r.Supplies {
		supplies = append(supplies, reports.TaxSupply{
			Treatment:    l.Treatment,
			NetAmount:    l.NetAmount,
			TaxAmount:    l.TaxAmount,
			InvoiceCount: l.InvoiceCount,
		})
	}

	return reports.TaxReturn{
		Country:      r.Country,
		From:         r.From,
		To:           r.To,
		BaseCurrency: r.BaseCurrency,
		Model:        r.Model,

		Supplies:       supplies,
		TotalNet:       r.TotalNet,
		OutputTaxTotal: r.OutputTaxTotal,
		// Carried as pointers, not flattened to "0.00". A sales tax has no
		// input side at all, and a nil that became a zero would state a
		// recoverable figure for a market that has none.
		InputTaxTotal: r.InputTaxTotal,
		NetPayable:    r.NetPayable,

		LedgerOutputTax: r.LedgerOutputTax,
		Difference:      r.Difference,
		Reconciled:      r.Reconciled,
		Outstanding:     r.Outstanding,
		Filed:           r.Filed,
	}, nil
}
