package purchasing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Goods arriving.
//
// B5: "Only GRN increases stock — a PO alone never inflates inventory." This is
// the one place in purchasing that the inventory engine hears from, and it goes
// through inventory.Receive exactly as every other receipt does — the costing
// method, the cost layers and the WAC pool are all its business, not this
// package's.
//
// # A receipt is not a bill
//
// Stock arrives and is valued; nothing is posted to accounts payable here. The
// liability is created when the SUPPLIER BILLS, which may be days later and for
// a different quantity — and detecting that difference is the entire purpose of
// the three-way match. Posting on receipt would make the two indistinguishable.
//
// # It IS posted, as an accrual
//
// Stock arrives with a real value, so Dr Inventory / Cr GRNI goes in here; the
// payable and the input tax wait for the bill, which then discharges the
// accrual. Without the receipt-side entry the valuation runs ahead of the
// Inventory control account for the whole window between a delivery and its
// invoice — by the full value of the receipt — and design 02 §6.6 says that
// divergence must never exist.

// ReceivedLine is one line of a delivery.
type ReceivedLine struct {
	POLineID     uuid.UUID
	QtyReceived  decimal.Decimal
	QtyRejected  decimal.Decimal
	RejectReason string
}

// Delivery is a supplier's delivery awaiting recording.
type Delivery struct {
	// UUID is assigned by the client BEFORE the call. A network failure after
	// the server committed would otherwise have a storeman press Receive again
	// and put the same delivery into stock twice — the same reasoning, and the
	// same mechanism, as an invoice UUID.
	UUID uuid.UUID

	POID            uuid.UUID
	DeliveryNoteRef string
	Notes           string
	Lines           []ReceivedLine

	// LandedCost is freight, duty, handling and insurance — everything that
	// belongs in the cost of the stock. Allocated across the lines and included
	// in the cost layers, per 10-catalog-and-inventory.md.
	LandedCost decimal.Decimal
	// ImportVAT is recoverable and is NOT part of the cost of the stock. E2.5 is
	// explicit that duty goes to inventory cost while import VAT is reclaimed,
	// and it is a separate field so the two cannot be added together by
	// accident.
	ImportVAT decimal.Decimal
	// Basis is value or quantity. Value by default: quantity is wrong the
	// moment a carton of scarves and a carton of gold share a container.
	Basis string
}

type Receipt struct {
	ID         uuid.UUID     `json:"id"`
	GRNNumber  string        `json:"grn_number"`
	POID       uuid.UUID     `json:"po_id"`
	PONumber   string        `json:"po_number"`
	ReceivedOn string        `json:"received_on"`
	Lines      []ReceiptLine `json:"lines"`
	// AlreadyReceived is set when this UUID has been seen before. The original
	// receipt is returned unchanged rather than a second one being created.
	AlreadyReceived bool `json:"already_received"`
	// OrderStatus is what the PO became: receiving if more is expected,
	// received once every line is complete.
	OrderStatus string `json:"order_status"`

	// CostCorrection is what this delivery put right on earlier sales that went
	// below zero, signed: positive means those goods cost more than the till
	// estimated, so the margin already reported on them was too generous
	// (C13). Zero when nothing was owed, which is the ordinary case.
	CostCorrection string `json:"cost_correction"`

	// UnitsRecosted is how many previously uncovered units this delivery
	// settled. Reported alongside the money because a large correction over one
	// unit and a small one over hundreds are different problems.
	UnitsRecosted string `json:"units_recosted"`
}

type ReceiptLine struct {
	POLineID    uuid.UUID `json:"po_line_id"`
	VariantID   uuid.UUID `json:"variant_id"`
	Description string    `json:"description"`
	QtyReceived string    `json:"qty_received"`
	QtyRejected string    `json:"qty_rejected"`
	UnitCost    string    `json:"unit_cost"`
	Value       string    `json:"value"`
}

// ReceiveGoods records a delivery and puts it into stock, atomically.
//
// Stock, cost layers and the receipt record go together or not at all. Stock
// increased without a receipt is stock a shop cannot explain; a receipt without
// stock is a delivery that vanished between the loading bay and the shelf.
func (s *Service) ReceiveGoods(
	ctx context.Context, scope Scope, in Delivery,
) (Receipt, error) {
	if len(in.Lines) == 0 {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"A delivery needs at least one line.")
	}
	if in.UUID == uuid.Nil {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"A delivery must carry an identifier so a retry is not received twice.")
	}

	basis := in.Basis
	if basis == "" {
		basis = "value"
	}
	if basis != "value" && basis != "quantity" {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"Landed cost is spread by value or by quantity.")
	}

	var out Receipt
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The retry check comes first, before anything is written. A
		// recognised delivery returns what it did the first time.
		if existing, found, e := s.alreadyReceived(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyReceived = true
			return nil
		}

		var poStatus string
		var warehouseID uuid.UUID
		var poNumber string
		if e := tx.QueryRow(ctx, `
			SELECT status, warehouse_id, po_number FROM purchase_order
			WHERE id = $1 AND company_id = $2`,
			in.POID, scope.CompanyID).Scan(&poStatus, &warehouseID, &poNumber); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That purchase order was not found.")
			}
			return e
		}

		// A draft has not been sent to anybody, so nothing can have arrived
		// against it. Receiving against one would let stock and cost enter the
		// books on an order nobody authorised.
		switch poStatus {
		case "issued", "receiving":
		case "draft":
			return errs.New(errs.CodeConflict,
				"That order has not been issued yet, so nothing can have arrived against it.")
		default:
			return errs.Newf(errs.CodeConflict,
				"That order is %s and cannot receive goods.", poStatus)
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "grn", "GRN")
		if e != nil {
			return e
		}

		var grnID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO goods_receipt
			  (tenant_id, company_id, po_id, warehouse_id, grn_number, uuid,
			   delivery_note_ref, notes, received_by,
			   landed_cost, import_vat, landed_cost_basis)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.POID, warehouseID, number,
			in.UUID, nullText(in.DeliveryNoteRef), nullText(in.Notes),
			scope.UserID, in.LandedCost, in.ImportVAT, basis).Scan(&grnID); e != nil {
			return db.Translate(e, "That delivery has already been recorded.")
		}

		out = Receipt{
			ID: grnID, GRNNumber: number, POID: in.POID, PONumber: poNumber,
			Lines: []ReceiptLine{},
		}

		// Resolved in full before anything is written, because allocating
		// freight by value needs every line's value first — and a line that
		// turns out not to belong to this order must fail before any stock has
		// moved rather than halfway through.
		type resolved struct {
			line        ReceivedLine
			variantID   uuid.UUID
			description string
			unitCost    decimal.Decimal
			kept        decimal.Decimal
		}
		items := make([]resolved, 0, len(in.Lines))

		for _, line := range in.Lines {
			if !line.QtyReceived.IsPositive() {
				return errs.New(errs.CodeInvalidInput,
					"A received line must have a positive quantity.")
			}
			rejected := line.QtyRejected
			if rejected.IsNegative() {
				return errs.New(errs.CodeInvalidInput,
					"A rejected quantity cannot be negative.")
			}
			if rejected.GreaterThan(line.QtyReceived) {
				return errs.New(errs.CodeInvalidInput,
					"More was rejected than arrived.")
			}

			// The PO line must belong to THIS order. Without the check a
			// caller could receive against another order's line and land the
			// stock, and the cost, on the wrong purchase entirely.
			var variantID uuid.UUID
			var description string
			var unitCost decimal.Decimal
			if e := tx.QueryRow(ctx, `
				SELECT variant_id, description, unit_cost
				FROM po_line WHERE id = $1 AND po_id = $2`,
				line.POLineID, in.POID).
				Scan(&variantID, &description, &unitCost); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.New(errs.CodeInvalidInput,
						"One of those lines is not on this purchase order.")
				}
				return e
			}

			// Only what was KEPT is costed. Rejected goods are recorded as
			// having arrived and gone straight back, which is a different fact
			// from never having come — and the supplier will argue about both.
			items = append(items, resolved{
				line: line, variantID: variantID, description: description,
				unitCost: unitCost, kept: line.QtyReceived.Sub(rejected),
			})
		}

		// Freight and duty, spread across what was kept.
		//
		// Weighted by value or by quantity as the receipt asked. Rejected goods
		// carry no share: the shop is not paying to warehouse something it sent
		// straight back, and loading their freight onto the units it did keep
		// would overstate those units' cost.
		weights := make([]decimal.Decimal, len(items))
		for i, item := range items {
			switch basis {
			case "quantity":
				weights[i] = item.kept
			default:
				weights[i] = item.kept.Mul(item.unitCost)
			}
		}
		allocation := allocateLandedCost(in.LandedCost, basis, weights)

		accrued := decimal.Zero
		correction, recosted := decimal.Zero, decimal.Zero
		for i, item := range items {
			share := allocation[i]

			// The landed cost raises the UNIT cost, which is what goes into the
			// cost layers — 10-catalog-and-inventory.md is explicit that
			// cost_layer.unit_cost includes the allocation. A shop that sold
			// these units at the pre-freight cost would report a margin it
			// never earned.
			unitCost := item.unitCost
			if item.kept.IsPositive() && share.IsPositive() {
				unitCost = item.unitCost.Add(share.Div(item.kept)).Round(4)
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO grn_line
				  (tenant_id, grn_id, po_line_id, variant_id,
				   qty_received, qty_rejected, reject_reason, unit_cost,
				   landed_cost_alloc)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				scope.TenantID, grnID, item.line.POLineID, item.variantID,
				item.line.QtyReceived, item.line.QtyRejected,
				nullText(item.line.RejectReason), unitCost, share); e != nil {
				return e
			}

			if item.kept.IsPositive() {
				posted, e := inventory.Receive(ctx, tx, inventory.Receipt{
					TenantID: scope.TenantID, CompanyID: scope.CompanyID,
					VariantID: item.variantID, WarehouseID: warehouseID,
					Qty: item.kept, UnitCost: unitCost,
					// The reason migration 0020 already reserved for exactly
					// this. Naming it anything else would put purchase
					// receipts outside every stock report that filters on it.
					Reason: "grn", SourceType: "goods_receipt",
					SourceID: &grnID,
				})
				if e != nil {
					return e
				}
				// What the valuation actually rose by, from the engine that
				// moved it — not `kept * unitCost` computed again here. A
				// caller doing its own arithmetic rounds a second time, and the
				// second rounding is what parted the stock report from the
				// balance sheet (P34).
				accrued = accrued.Add(posted)

				// The stock is on the shelf, so any earlier sale of this
				// variant that went below zero can stop guessing what it cost.
				// C13 requires this to happen on the NEXT receipt, which is
				// this one — a correction deferred to a month-end job would
				// leave the books wrong for the whole month, and the layers it
				// needs may have been consumed by then.
				settled, e := inventory.SettleShortfalls(ctx, tx,
					scope.CompanyID, item.variantID, warehouseID)
				if e != nil {
					return e
				}
				correction = correction.Add(settled.Adjustment)
				recosted = recosted.Add(settled.QtySettled)
			}

			out.Lines = append(out.Lines, ReceiptLine{
				POLineID: item.line.POLineID, VariantID: item.variantID,
				Description: item.description,
				QtyReceived: item.line.QtyReceived.String(),
				QtyRejected: item.line.QtyRejected.String(),
				UnitCost:    unitCost.String(),
				Value:       item.kept.Mul(unitCost).Round(4).String(),
			})
		}

		// The accrual, for exactly what went into stock. Posted from the value
		// the costing engine actually recorded rather than from the order, so
		// the ledger and the valuation cannot disagree.
		if e := s.postReceiptAccrual(ctx, tx, scope, grnID, time.Now().UTC(),
			accrued, "Goods received "+number); e != nil {
			return e
		}

		// The correction to earlier sales, as its own entry rather than folded
		// into the accrual. They are two different facts — what this delivery
		// cost, and what an earlier one was mis-costed by — and an auditor
		// asking why cost of goods sold moved needs to see the second on its
		// own, attributed to the rule that produced it.
		if e := s.postCostCorrection(ctx, tx, scope, grnID, time.Now().UTC(),
			correction, "Cost correction on goods received "+number); e != nil {
			return e
		}
		out.CostCorrection = correction.StringFixed(2)
		out.UnitsRecosted = recosted.String()

		status, e := advanceOrderStatus(ctx, tx, in.POID)
		out.OrderStatus = status
		return e
	})
	if err != nil {
		return Receipt{}, err
	}
	return out, nil
}

// advanceOrderStatus moves the PO on once a delivery lands.
//
// received only when every line is complete. A partly delivered order stays
// open, because closing it would hide the outstanding balance from the buyer
// who has to chase it.
func advanceOrderStatus(
	ctx context.Context, tx pgx.Tx, poID uuid.UUID,
) (string, error) {
	var outstanding int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM po_outstanding($1) WHERE qty_outstanding > 0`,
		poID).Scan(&outstanding); err != nil {
		return "", err
	}

	status := "receiving"
	if outstanding == 0 {
		status = "received"
	}

	_, err := tx.Exec(ctx,
		`UPDATE purchase_order SET status = $2 WHERE id = $1`, poID, status)
	return status, err
}

func (s *Service) alreadyReceived(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Receipt, bool, error) {
	var out Receipt
	err := tx.QueryRow(ctx, `
		SELECT g.id, g.grn_number, g.po_id, p.po_number,
		       g.received_on::text, p.status
		FROM goods_receipt g
		JOIN purchase_order p ON p.id = g.po_id
		WHERE g.tenant_id = $1 AND g.uuid = $2`,
		scope.TenantID, docUUID,
	).Scan(&out.ID, &out.GRNNumber, &out.POID, &out.PONumber,
		&out.ReceivedOn, &out.OrderStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}

	rows, err := tx.Query(ctx, `
		SELECT l.po_line_id, l.variant_id, pl.description,
		       l.qty_received::text, l.qty_rejected::text, l.unit_cost::text,
		       ((l.qty_received - l.qty_rejected) * l.unit_cost)::text
		FROM grn_line l
		JOIN po_line pl ON pl.id = l.po_line_id
		WHERE l.grn_id = $1`, out.ID)
	if err != nil {
		return Receipt{}, false, err
	}
	defer rows.Close()

	// Any correction this delivery made was posted by the call that created it.
	// Reporting it again would read as a second one having happened.
	out.CostCorrection = "0.00"
	out.UnitsRecosted = "0"

	out.Lines = []ReceiptLine{}
	for rows.Next() {
		var l ReceiptLine
		if err := rows.Scan(&l.POLineID, &l.VariantID, &l.Description,
			&l.QtyReceived, &l.QtyRejected, &l.UnitCost, &l.Value); err != nil {
			return Receipt{}, false, err
		}
		out.Lines = append(out.Lines, l)
	}
	return out, true, rows.Err()
}

// ListReceipts returns the deliveries against one order.
func (s *Service) ListReceipts(
	ctx context.Context, scope Scope, poID uuid.UUID,
) ([]Receipt, error) {
	out := []Receipt{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT g.id, g.grn_number, g.po_id, p.po_number, g.received_on::text
			FROM goods_receipt g
			JOIN purchase_order p ON p.id = g.po_id
			WHERE g.po_id = $1 AND g.company_id = $2
			ORDER BY g.received_on DESC, g.grn_number DESC`,
			poID, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			// A correction is reported by the receipt that made it, not by a
			// later listing, which is a summary of what arrived rather than of
			// what it put right.
			r := Receipt{CostCorrection: "0.00", UnitsRecosted: "0"}
			if e := rows.Scan(&r.ID, &r.GRNNumber, &r.POID, &r.PONumber,
				&r.ReceivedOn); e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}
