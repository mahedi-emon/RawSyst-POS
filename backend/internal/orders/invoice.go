// Turning a delivered order into the invoice that finishes it (blueprint B11).
//
// B11's lifecycle is "Draft → Confirmed → Processing → Packed → Delivered →
// Completed", and the last arrow did not exist. `sales_order.invoice_id` was a
// column nothing ever wrote, there was no route, and `Advance` refused the
// final step with "Raise the invoice to complete it — an order is finished by
// being invoiced, not by being marked so". The product told an owner to do
// something it gave them no way to do, and an order could never leave
// `delivered`.
//
// Worse than an unreachable state: this package touches neither stock nor
// accounting. `Deliver` records qty_delivered and nothing else. So goods went
// out marked delivered, the shelf was never reduced, and no revenue, tax or
// cost of sale ever reached the ledger.
//
// # It reuses the till's own path
//
// `sales.Finalize` writes the invoice, moves the stock, costs it, posts revenue
// and tax and COGS, records loyalty and promotions, and writes the audit trail.
// Re-implementing any of that here would be a second sales engine that drifts
// from the first — which is how a business ends up with two definitions of what
// a sale is worth.
package orders

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
)

// InvoiceRequest is how an order is billed.
type InvoiceRequest struct {
	// UUID makes the call idempotent, exactly as a till's does: the same
	// request arriving twice produces one invoice, not two.
	UUID uuid.UUID

	// DocType is 'simplified' unless the caller asks for a standard (B2B)
	// invoice. Defaulted rather than derived from the customer, because a
	// standard invoice must name a VAT-registered buyer and quietly promoting
	// an order to one would fail at the worst moment.
	DocType string

	// PricesIncludeTax follows the till's convention so a price means the same
	// thing across the product.
	PricesIncludeTax bool

	// Tenders is how it was paid. Empty means on account, which needs a
	// customer to owe it.
	Tenders []sales.Tender
}

// Invoiced is what the caller gets back.
type Invoiced struct {
	OrderID   uuid.UUID `json:"order_id"`
	InvoiceID uuid.UUID `json:"invoice_id"`
	Number    string    `json:"order_no"`
	State     string    `json:"state"`
}

// Invoice bills a delivered order and completes it.
//
// # One transaction
//
// The invoice, the order's completion and the release of its stock hold all
// commit together or not at all. Any split leaves a state somebody has to
// reconcile by hand: an invoice with no order pointing at it can be raised
// twice, and an order marked completed with no invoice is a sale nobody can
// find.
func (s *Service) Invoice(
	ctx context.Context, scope Scope, orderID uuid.UUID, in InvoiceRequest,
) (Invoiced, error) {
	if s.sales == nil {
		return Invoiced{}, errs.New(errs.CodeInternal,
			"The orders service was built without the sales engine, so an "+
				"order cannot be invoiced.")
	}
	if in.UUID == uuid.Nil {
		return Invoiced{}, errs.New(errs.CodeInvalidInput,
			"An invoice must carry an identifier so a retry does not bill "+
				"the order twice.")
	}
	docType := in.DocType
	if docType == "" {
		docType = "simplified"
	}

	var out Invoiced
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var state, number, currency string
		var storeID *uuid.UUID
		var customerID *uuid.UUID
		var invoiceID *uuid.UUID
		if e := tx.QueryRow(ctx, `
			SELECT state, order_no, currency, store_id, customer_id, invoice_id
			FROM sales_order
			WHERE id = $1 AND company_id = $2
			FOR UPDATE`, orderID, scope.CompanyID).
			Scan(&state, &number, &currency, &storeID, &customerID,
				&invoiceID); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That order was not found.")
			}
			return e
		}

		// Already billed. Answered rather than refused, so a retry that lost
		// its response gets the same answer instead of a conflict.
		if invoiceID != nil {
			out = Invoiced{OrderID: orderID, InvoiceID: *invoiceID,
				Number: number, State: state}
			return nil
		}
		if state != StateDelivered {
			return errs.Newf(errs.CodeConflict,
				"%s is %s. An order is invoiced once it has been delivered, "+
					"because the invoice is what says the goods went out.",
				number, plainState(state))
		}

		lines, refs, err := s.orderLines(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return errs.Newf(errs.CodeConflict,
				"%s has no lines to invoice.", number)
		}

		tenders := in.Tenders
		if len(tenders) == 0 {
			if customerID == nil {
				return errs.Newf(errs.CodeInvalidInput,
					"%s has no customer, so it cannot be sold on account. "+
						"Say how it was paid.", number)
			}
			// On account: the receivable is the tender, and the customer
			// ledger is where it is collected from.
			total := decimal.Zero
			for _, l := range lines {
				total = total.Add(l.Qty.Mul(l.UnitPrice).Sub(l.LineDiscount))
			}
			tenders = []sales.Tender{{
				Method: "customer_due", Amount: total,
				Reference: number,
			}}
		}

		// An order need not name a shop: `sales_order.store_id` is nullable and
		// a single-branch business never fills it in. The invoice does need
		// one, because a sale belongs to somewhere.
		shop, err := s.storeFor(ctx, tx, scope.CompanyID, storeID)
		if err != nil {
			return err
		}
		warehouseID, err := s.warehouseFor(ctx, tx, scope.CompanyID, shop)
		if err != nil {
			return err
		}
		term := sales.Terminal{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			StoreID: shop, WarehouseID: warehouseID,
			// No device, no cash session and no EGS unit. An order is billed
			// from the back office rather than rung on a till, and
			// `Terminal.OnAChain` is false without an EGS unit — so this does
			// not touch the e-invoicing chain, which is deferred.
		}

		user := scope.UserID
		sale := sales.Sale{
			InvoiceUUID: in.UUID,
			DocType:     docType,
			IssuedAt:    time.Now().UTC(),
			CustomerID:  customerID,
			CashierID:   &user,
			Lines:       refs,
			Tenders:     tenders,
			Input: sales.SaleInput{
				Lines:            lines,
				PricesIncludeTax: in.PricesIncludeTax,
			},
		}

		done, err := s.sales.FinalizeForCompany(ctx, tx, term, sale)
		if err != nil {
			return err
		}

		if _, e := tx.Exec(ctx, `
			UPDATE sales_order
			SET state = $2, invoice_id = $3, completed_at = now()
			WHERE id = $1 AND company_id = $4`,
			orderID, StateCompleted, done.InvoiceID, scope.CompanyID); e != nil {
			return db.Translate(e, "That order could not be completed.")
		}

		// The goods have now actually left, so the claim on them ends. In the
		// same transaction, because a hold outliving the sale would make the
		// shelf look emptier than it is for as long as nobody noticed.
		if e := aftersales.ReleaseOrderInTx(ctx, tx, aftersales.Scope{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			UserID: scope.UserID,
		}, orderID, "consumed"); e != nil {
			return e
		}

		out = Invoiced{OrderID: orderID, InvoiceID: done.InvoiceID,
			Number: number, State: StateCompleted}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "order_invoiced",
			EntityType: "sales_order", EntityID: &orderID,
			Before: map[string]any{"state": state},
			After: map[string]any{
				"order": number, "invoice_id": done.InvoiceID.String(),
			},
		})
	})
	return out, db.Translate(err, "")
}

// orderLines turns an order's lines into what the sales engine prices.
//
// The prices are the ones the customer already agreed to on the order. They are
// not re-quoted: a shop that negotiated a price in March must not find the
// invoice charging April's.
func (s *Service) orderLines(
	ctx context.Context, tx pgx.Tx, orderID uuid.UUID,
) ([]sales.LineInput, []sales.SaleLineRef, error) {
	rows, err := tx.Query(ctx, `
		SELECT l.variant_id, coalesce(l.description, coalesce(p.name, v.sku)),
		       l.qty, l.unit_price, l.discount,
		       coalesce(p.tax_treatment, 'standard')
		FROM sales_order_line l
		JOIN variant v      ON v.id = l.variant_id
		LEFT JOIN product p ON p.id = v.product_id
		WHERE l.order_id = $1
		ORDER BY l.line_no`, orderID)
	if err != nil {
		return nil, nil, db.Translate(err, "")
	}
	defer rows.Close()

	var lines []sales.LineInput
	var refs []sales.SaleLineRef
	for rows.Next() {
		var variantID uuid.UUID
		var description, treatment string
		var qty, price, discount decimal.Decimal
		if e := rows.Scan(&variantID, &description, &qty, &price, &discount,
			&treatment); e != nil {
			return nil, nil, db.Translate(e, "")
		}
		lines = append(lines, sales.LineInput{
			VariantID:    variantID.String(),
			Description:  description,
			Qty:          qty,
			UnitPrice:    price,
			LineDiscount: discount,
			TaxTreatment: treatment,
		})
		refs = append(refs, sales.SaleLineRef{VariantID: variantID})
	}
	return lines, refs, db.Translate(rows.Err(), "")
}

// warehouseFor finds the stock an order's shop sells from.
//
// The shop's own warehouse first. `warehouse.store_id` is nullable, though, and
// a business with one stockroom serving every counter simply never fills it in
// — so a company with exactly one warehouse falls back to it rather than being
// told it has none.
//
// A company with several unlinked warehouses is refused instead of guessed at:
// picking one would take the goods off the wrong shelf, and the valuation would
// be wrong in two places at once.
func (s *Service) warehouseFor(
	ctx context.Context, tx pgx.Tx, companyID, storeID uuid.UUID,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM warehouse
		WHERE store_id = $1
		ORDER BY code
		LIMIT 1`, storeID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, db.Translate(err, "")
	}

	var n int
	if e := tx.QueryRow(ctx,
		`SELECT count(*) FROM warehouse WHERE company_id = $1`,
		companyID).Scan(&n); e != nil {
		return uuid.Nil, db.Translate(e, "")
	}
	switch {
	case n == 0:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"This business has no warehouse, so there is no stock to invoice "+
				"the order against.")
	case n > 1:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"That shop is not linked to a warehouse and this business has "+
				"several, so there is no way to tell which stock the order "+
				"came off. Link the shop to its warehouse first.")
	}
	err = tx.QueryRow(ctx,
		`SELECT id FROM warehouse WHERE company_id = $1`, companyID).Scan(&id)
	return id, db.Translate(err, "")
}

// storeFor decides which shop an order's invoice belongs to.
//
// The order's own shop when it names one. A business with a single branch
// never fills that in, so one store is taken as the answer rather than
// refusing an invoice over a field the shop had no reason to set.
//
// Several shops and no order-level choice is refused: guessing would file the
// sale under the wrong branch, and branch profitability is one of the things
// an owner reads most.
func (s *Service) storeFor(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, named *uuid.UUID,
) (uuid.UUID, error) {
	if named != nil && *named != uuid.Nil {
		return *named, nil
	}

	var n int
	if e := tx.QueryRow(ctx,
		`SELECT count(*) FROM store WHERE company_id = $1`, companyID).
		Scan(&n); e != nil {
		return uuid.Nil, db.Translate(e, "")
	}
	switch {
	case n == 0:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"This business has no shop, so there is nowhere to book the sale.")
	case n > 1:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"That order does not say which shop it belongs to and this "+
				"business has several. Set the order's shop before invoicing "+
				"it, or the sale lands under the wrong branch.")
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM store WHERE company_id = $1`, companyID).Scan(&id)
	return id, db.Translate(err, "")
}
