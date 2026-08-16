package purchasing

import (
	"context"
	"errors"

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
// Which leaves the well-known gap: between receipt and bill, stock is on the
// shelf and nothing is owed for it in the ledger. That is Goods Received Not
// Invoiced, and it is a real accrual a full ERP posts. It is NOT posted here —
// see the note on the ledger effect below.

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
			   delivery_note_ref, notes, received_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.POID, warehouseID, number,
			in.UUID, nullText(in.DeliveryNoteRef), nullText(in.Notes),
			scope.UserID).Scan(&grnID); e != nil {
			return db.Translate(e, "That delivery has already been recorded.")
		}

		out = Receipt{
			ID: grnID, GRNNumber: number, POID: in.POID, PONumber: poNumber,
			Lines: []ReceiptLine{},
		}

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

			if _, e := tx.Exec(ctx, `
				INSERT INTO grn_line
				  (tenant_id, grn_id, po_line_id, variant_id,
				   qty_received, qty_rejected, reject_reason, unit_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				scope.TenantID, grnID, line.POLineID, variantID,
				line.QtyReceived, rejected, nullText(line.RejectReason),
				unitCost); e != nil {
				return e
			}

			// Only what was KEPT goes into stock. Rejected goods are recorded
			// as having arrived and gone straight back, which is a different
			// fact from never having come — and the supplier will argue about
			// both.
			kept := line.QtyReceived.Sub(rejected)
			if kept.IsPositive() {
				if e := inventory.Receive(ctx, tx, inventory.Receipt{
					TenantID: scope.TenantID, CompanyID: scope.CompanyID,
					VariantID: variantID, WarehouseID: warehouseID,
					Qty: kept, UnitCost: unitCost,
					// The reason migration 0020 already reserved for exactly
					// this. Naming it anything else would put purchase
					// receipts outside every stock report that filters on it.
					Reason: "grn", SourceType: "goods_receipt",
					SourceID: &grnID,
				}); e != nil {
					return e
				}
			}

			out.Lines = append(out.Lines, ReceiptLine{
				POLineID: line.POLineID, VariantID: variantID,
				Description: description,
				QtyReceived: line.QtyReceived.String(),
				QtyRejected: rejected.String(),
				UnitCost:    unitCost.String(),
				Value:       kept.Mul(unitCost).Round(4).String(),
			})
		}

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
			var r Receipt
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
