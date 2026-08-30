package stockops

// Moving stock between the company's own rooms.
//
// B4's workflow, as four states:
//
//	Transfer Request -> Manager Approval -> Dispatch (in-transit lock)
//	                 -> Receiving Branch Confirms & Reconciles
//
// # Nothing posts, and that is the whole point
//
// A transfer moves stock from one room the company owns to another. The
// business is neither richer nor poorer, the Inventory control account does not
// move, and there is no journal entry. Anybody looking for one is looking for a
// bug.
//
// Which puts the entire weight of correctness on C13's tie-out: the inventory
// VALUATION must not move either, at any point in the journey, or it stops
// agreeing with a ledger that never moved.
//
// # Why in-transit stock lives in a real place
//
// Stock leaves Riyadh on Monday and arrives in Jeddah on Wednesday. On Tuesday
// it is real, owned, and in neither branch. If the dispatch simply removed it,
// the valuation would fall by a lorry-load for two days while the ledger stood
// still, and C13 would be broken for the duration of every transfer the company
// ever makes.
//
// So it goes into a system-owned `transit` location (0079), which the valuation
// counts like any other. Each leg is a Consume immediately followed by a
// Restore of EXACTLY the value the Consume reported:
//
//	dispatch:  Consume(source)  -> value V   Restore(transit, value: V)
//	receipt:   Consume(transit) -> value V'  Restore(destination, value: V')
//
// `Restore` takes a VALUE rather than a unit cost, and splits it into layers
// that multiply back to it exactly — the rounding-remainder rule this product
// applies in five other places. Receiving at a unit cost instead would multiply
// and round a second time, and a transfer would create or destroy a hallala of
// inventory value on every leg. Over a year of branch transfers that is a
// valuation that no longer ties, arrived at one hallala at a time.
//
// # Receiving less than was sent
//
// B4 wants discrepancies flagged. A branch may confirm less than was
// dispatched — a box went missing, a carton was crushed — and the difference
// stays in transit rather than vanishing. It is still the company's stock and
// it is still in the valuation; what has happened is that nobody knows where it
// is. Writing it off is a decision with a reason attached, which is what the
// wastage voucher is for, so the shortfall is left visible and unresolved
// rather than being quietly absorbed by the receiving branch.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The states of a transfer, matching 0079's CHECK.
const (
	TransferRequested  = "requested"
	TransferApproved   = "approved"
	TransferDispatched = "dispatched"
	TransferReceived   = "received"
	TransferCancelled  = "cancelled"
)

// NewTransfer is stock being asked for.
type NewTransfer struct {
	FromWarehouseID uuid.UUID
	ToWarehouseID   uuid.UUID
	Note            string
	Lines           []TransferQty
}

// TransferQty is one product and a quantity, used at every step.
type TransferQty struct {
	VariantID uuid.UUID
	Qty       decimal.Decimal
}

// Transfer is a movement between locations.
type Transfer struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"transfer_no"`
	Status string    `json:"status"`
	Note   string    `json:"note,omitempty"`

	From string `json:"from"`
	To   string `json:"to"`

	RequestedBy  string `json:"requested_by,omitempty"`
	RequestedAt  string `json:"requested_at"`
	ApprovedBy   string `json:"approved_by,omitempty"`
	ApprovedAt   string `json:"approved_at,omitempty"`
	DispatchedAt string `json:"dispatched_at,omitempty"`
	ReceivedBy   string `json:"received_by,omitempty"`
	ReceivedAt   string `json:"received_at,omitempty"`

	// Value is what is on the lorry, at cost. Only known once dispatched.
	Value    string `json:"value,omitempty"`
	Currency string `json:"currency"`

	// ShortBy is the total quantity dispatched that the receiving branch never
	// confirmed. Non-zero means stock is unaccounted for and still sitting in
	// transit — B4's flagged discrepancy.
	ShortBy string `json:"short_by,omitempty"`

	Lines []TransferLine `json:"lines,omitempty"`
}

// TransferLine is one product's part of a transfer.
type TransferLine struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`

	Requested  string `json:"qty_requested"`
	Dispatched string `json:"qty_dispatched,omitempty"`
	Received   string `json:"qty_received,omitempty"`
	Value      string `json:"value,omitempty"`

	// Short is dispatched less received, once the branch has confirmed.
	Short string `json:"short,omitempty"`
}

// RequestTransfer raises a transfer for approval.
func (s *Service) RequestTransfer(
	ctx context.Context, scope Scope, in NewTransfer,
) (Transfer, error) {
	if in.FromWarehouseID == in.ToWarehouseID {
		return Transfer{}, errs.New(errs.CodeInvalidInput,
			"Stock cannot be moved to where it already is.")
	}
	if len(in.Lines) == 0 {
		return Transfer{}, errs.New(errs.CodeInvalidInput,
			"Say what is being moved.")
	}
	seen := map[uuid.UUID]bool{}
	for i, l := range in.Lines {
		if !l.Qty.IsPositive() {
			return Transfer{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d moves nothing. Say how many.", i+1)
		}
		if seen[l.VariantID] {
			return Transfer{}, errs.New(errs.CodeInvalidInput,
				"The same product appears twice. Put the whole quantity on one line.")
		}
		seen[l.VariantID] = true
	}

	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{in.FromWarehouseID, in.ToWarehouseID} {
			if _, e := locationForWrite(ctx, tx, scope.CompanyID, id); e != nil {
				return e
			}
		}

		number, e := claimTransferNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		id := uuid.New()
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_transfer
			  (id, tenant_id, company_id, transfer_no,
			   from_warehouse_id, to_warehouse_id, note, requested_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, scope.TenantID, scope.CompanyID, number,
			in.FromWarehouseID, in.ToWarehouseID,
			nullIfBlank(in.Note), scope.UserID); e != nil {
			return db.Translate(e, "That transfer could not be raised.")
		}

		for _, l := range in.Lines {
			if _, e := variantLabel(ctx, tx, l.VariantID); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO stock_transfer_line
				  (tenant_id, transfer_id, variant_id, qty_requested)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, id, l.VariantID, l.Qty); e != nil {
				return db.Translate(e, "That transfer could not be raised.")
			}
		}

		read, e := s.readTransfer(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// ApproveTransfer lets the stock go.
//
// A separate permission from raising one — `inventory.approve_transfer`, added
// in 0079 — because a control the doer can sign off is not a control. The route
// enforces that; this enforces that the approver is not the requester, which no
// permission can express.
func (s *Service) ApproveTransfer(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Transfer, error) {
	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := transferInState(ctx, tx, scope, id, TransferRequested)
		if e != nil {
			return e
		}
		if t.requestedBy != nil && *t.requestedBy == scope.UserID {
			return errs.Newf(errs.CodeForbidden,
				"You raised %s, so somebody else has to approve it.", t.number)
		}
		_, e = tx.Exec(ctx, `
			UPDATE stock_transfer
			SET status = 'approved', approved_by = $2, approved_at = now()
			WHERE id = $1`, id, scope.UserID)
		if e != nil {
			return e
		}
		read, e := s.readTransfer(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// DispatchTransfer takes the stock out of the source and puts it on the lorry.
//
// Quantities may be less than were approved — the storeman found four, not six
// — but never more, which the schema enforces.
func (s *Service) DispatchTransfer(
	ctx context.Context, scope Scope, id uuid.UUID, sent []TransferQty,
) (Transfer, error) {
	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := transferInState(ctx, tx, scope, id, TransferApproved)
		if e != nil {
			return e
		}

		transitID, e := transitLocation(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		// Everything actually going, defaulting to what was asked for.
		qty := map[uuid.UUID]decimal.Decimal{}
		rows, e := tx.Query(ctx,
			`SELECT variant_id, qty_requested FROM stock_transfer_line
			 WHERE transfer_id = $1 ORDER BY variant_id`, id)
		if e != nil {
			return e
		}
		var order []uuid.UUID
		for rows.Next() {
			var v uuid.UUID
			var q decimal.Decimal
			if e := rows.Scan(&v, &q); e != nil {
				rows.Close()
				return e
			}
			qty[v] = q
			order = append(order, v)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, s := range sent {
			if _, ok := qty[s.VariantID]; !ok {
				return errs.Newf(errs.CodeInvalidInput,
					"One of those products is not on %s.", t.number)
			}
			if s.Qty.IsNegative() {
				return errs.New(errs.CodeInvalidInput,
					"A dispatch cannot be for less than nothing.")
			}
			qty[s.VariantID] = s.Qty
		}

		// Both ends locked, in a stable order, before anything moves. The
		// source because it is being drawn down; transit because a second
		// transfer of the same product would otherwise interleave with this
		// one's Restore.
		if e := inventory.LockStock(ctx, tx, t.from, order); e != nil {
			return e
		}
		if e := inventory.LockStock(ctx, tx, transitID, order); e != nil {
			return e
		}

		any := false
		for _, v := range order {
			q := qty[v]
			if !q.IsPositive() {
				// Nothing of this line is going. The line stays on the document
				// with a dispatched quantity of nothing, which is the honest
				// record of a request that could not be filled.
				continue
			}
			any = true

			res, e := inventory.Consume(ctx, tx, inventory.Issue{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: v, WarehouseID: t.from,
				Qty:        q,
				Reason:     "transfer_out",
				SourceType: "stock_transfer", SourceID: &id,
				Note: "Dispatched on " + t.number,
			})
			if e != nil {
				return e
			}

			// Onto the lorry at EXACTLY what it left at. See the package note:
			// a unit cost here would round a second time.
			if e := inventory.Restore(ctx, tx, inventory.Restoration{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: v, WarehouseID: transitID,
				Qty: q, Value: res.TotalCost,
				Reason:     "transfer_in",
				SourceType: "stock_transfer", SourceID: &id,
				Note: "In transit on " + t.number,
			}); e != nil {
				return e
			}

			if _, e := tx.Exec(ctx, `
				UPDATE stock_transfer_line
				SET qty_dispatched = $3, value_dispatched = $4
				WHERE transfer_id = $1 AND variant_id = $2`,
				id, v, q, res.TotalCost); e != nil {
				return db.Translate(e, "That dispatch could not be recorded.")
			}
		}

		if !any {
			return errs.New(errs.CodeInvalidInput,
				"Nothing is being sent. Cancel the transfer instead.")
		}

		if _, e := tx.Exec(ctx, `
			UPDATE stock_transfer
			SET status = 'dispatched', dispatched_by = $2, dispatched_at = now()
			WHERE id = $1`, id, scope.UserID); e != nil {
			return e
		}

		read, e := s.readTransfer(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// ReceiveTransfer takes the stock off the lorry and into the destination.
//
// Quantities default to what was dispatched. A branch confirming less leaves
// the difference in transit — see the package note on why that is deliberate
// rather than an oversight.
func (s *Service) ReceiveTransfer(
	ctx context.Context, scope Scope, id uuid.UUID, got []TransferQty,
) (Transfer, error) {
	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := transferInState(ctx, tx, scope, id, TransferDispatched)
		if e != nil {
			return e
		}

		transitID, e := transitLocation(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		type dispatched struct {
			qty   decimal.Decimal
			value decimal.Decimal
		}
		sent := map[uuid.UUID]dispatched{}
		var order []uuid.UUID

		rows, e := tx.Query(ctx, `
			SELECT variant_id, qty_dispatched, value_dispatched
			FROM stock_transfer_line
			WHERE transfer_id = $1 AND qty_dispatched IS NOT NULL
			ORDER BY variant_id`, id)
		if e != nil {
			return e
		}
		for rows.Next() {
			var v uuid.UUID
			var d dispatched
			if e := rows.Scan(&v, &d.qty, &d.value); e != nil {
				rows.Close()
				return e
			}
			sent[v] = d
			order = append(order, v)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		received := map[uuid.UUID]decimal.Decimal{}
		for _, v := range order {
			received[v] = sent[v].qty
		}
		for _, g := range got {
			d, ok := sent[g.VariantID]
			if !ok {
				return errs.Newf(errs.CodeInvalidInput,
					"One of those products was not sent on %s.", t.number)
			}
			if g.Qty.IsNegative() {
				return errs.New(errs.CodeInvalidInput,
					"A branch cannot receive less than nothing.")
			}
			if g.Qty.GreaterThan(d.qty) {
				label, _ := variantLabel(ctx, tx, g.VariantID)
				return errs.Newf(errs.CodeInvalidInput,
					"%s says %s of %s was sent, and you have confirmed %s. A "+
						"lorry does not gain stock on the way.",
					t.number, d.qty.String(), label, g.Qty.String())
			}
			received[g.VariantID] = g.Qty
		}

		if e := inventory.LockStock(ctx, tx, transitID, order); e != nil {
			return e
		}
		if e := inventory.LockStock(ctx, tx, t.to, order); e != nil {
			return e
		}

		for _, v := range order {
			q := received[v]
			if _, e := tx.Exec(ctx, `
				UPDATE stock_transfer_line SET qty_received = $3
				WHERE transfer_id = $1 AND variant_id = $2`, id, v, q); e != nil {
				return db.Translate(e, "That receipt could not be recorded.")
			}
			if !q.IsPositive() {
				continue
			}

			res, e := inventory.Consume(ctx, tx, inventory.Issue{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: v, WarehouseID: transitID,
				Qty:        q,
				Reason:     "transfer_out",
				SourceType: "stock_transfer", SourceID: &id,
				Note: "Off the lorry on " + t.number,
			})
			if e != nil {
				return e
			}

			if e := inventory.Restore(ctx, tx, inventory.Restoration{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: v, WarehouseID: t.to,
				Qty: q, Value: res.TotalCost,
				Reason:     "transfer_in",
				SourceType: "stock_transfer", SourceID: &id,
				Note: "Received on " + t.number,
			}); e != nil {
				return e
			}

			// The arriving branch may have been selling this product below
			// zero. It has stock now, so those sales can stop guessing.
			settled, e := inventory.SettleShortfalls(ctx, tx,
				scope.CompanyID, v, t.to)
			if e != nil {
				return e
			}
			if settled.Posted() {
				var country string
				if e := tx.QueryRow(ctx,
					`SELECT country FROM company WHERE id = $1`,
					scope.CompanyID).Scan(&country); e != nil {
					return e
				}
				if e := s.postCostCorrection(ctx, tx, scope, id, country,
					time.Now().UTC(), settled.Adjustment,
					"Cost correction on "+t.number); e != nil {
					return e
				}
			}
		}

		if _, e := tx.Exec(ctx, `
			UPDATE stock_transfer
			SET status = 'received', received_by = $2, received_at = now()
			WHERE id = $1`, id, scope.UserID); e != nil {
			return e
		}

		read, e := s.readTransfer(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// CancelTransfer abandons one that has not left yet.
//
// Only before dispatch. Once stock is on the lorry it is somewhere, and the
// document that says where it went cannot be withdrawn — the way back is to
// receive it at the origin, or to write off what never arrived.
func (s *Service) CancelTransfer(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := readTransferRow(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		if t.status != TransferRequested && t.status != TransferApproved {
			return errs.Newf(errs.CodeConflict,
				"%s has already been dispatched, so it cannot be cancelled. "+
					"Receive it, or write off what never arrived.", t.number)
		}
		_, e = tx.Exec(ctx, `
			UPDATE stock_transfer
			SET status = 'cancelled', cancelled_by = $2, cancelled_at = now()
			WHERE id = $1`, id, scope.UserID)
		return e
	})
}

// --- reading --------------------------------------------------------------

type transferRow struct {
	number      string
	status      string
	from, to    uuid.UUID
	requestedBy *uuid.UUID
}

func readTransferRow(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (transferRow, error) {
	var t transferRow
	err := tx.QueryRow(ctx, `
		SELECT transfer_no, status, from_warehouse_id, to_warehouse_id, requested_by
		FROM stock_transfer WHERE id = $1 AND company_id = $2`,
		id, scope.CompanyID).
		Scan(&t.number, &t.status, &t.from, &t.to, &t.requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return transferRow{}, errs.New(errs.CodeNotFound,
			"That transfer is not this business's.")
	}
	return t, err
}

// transferInState reads a transfer and refuses it if the step being asked for
// is not the step it is at, naming the step it IS at.
func transferInState(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID, want string,
) (transferRow, error) {
	t, err := readTransferRow(ctx, tx, scope, id)
	if err != nil {
		return transferRow{}, err
	}
	if t.status != want {
		return transferRow{}, errs.Newf(errs.CodeConflict,
			"%s is %s, and this step is for a transfer that is %s.",
			t.number, plainState(t.status), plainState(want))
	}
	return t, nil
}

func plainState(s string) string {
	switch s {
	case TransferRequested:
		return "waiting for approval"
	case TransferApproved:
		return "approved and waiting to be sent"
	case TransferDispatched:
		return "on its way"
	case TransferReceived:
		return "already received"
	default:
		return "cancelled"
	}
}

// transitLocation is the company's one in-transit location.
//
// 0079 created one for every company that existed when it ran. This creates one
// for any company that has appeared since, on first use.
//
// A trigger on `company` was the first attempt and it does not work: the
// row-level policy on `warehouse` is `tenant_id = current_tenant_id()` with
// FORCE, and a company can be created on the platform plane where there is no
// current tenant, so the trigger's INSERT is refused and takes the company
// creation down with it. Widening the policy to let a trigger through would
// trade a real isolation boundary for one row.
//
// Here it always works, because every method on this service runs as the
// tenant. `ON CONFLICT DO NOTHING` and a re-read rather than a lock: two
// dispatches racing on a company's first-ever transfer both get the same row,
// whichever of them inserted it.
func transitLocation(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM warehouse WHERE company_id = $1 AND kind = 'transit'`,
		companyID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	var tenantID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT tenant_id FROM company WHERE id = $1`, companyID).
		Scan(&tenantID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, errs.New(errs.CodeNotFound,
				"That business is not one this account can act for.")
		}
		return uuid.Nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO warehouse (tenant_id, company_id, store_id, code, name, kind)
		VALUES ($1,$2,NULL,'TRANSIT','In transit','transit')
		ON CONFLICT (company_id, code) DO NOTHING`,
		tenantID, companyID); err != nil {
		return uuid.Nil, err
	}

	err = tx.QueryRow(ctx,
		`SELECT id FROM warehouse WHERE company_id = $1 AND kind = 'transit'`,
		companyID).Scan(&id)
	return id, err
}

func (s *Service) readTransfer(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Transfer, error) {
	var t Transfer
	var note, requestedBy, approvedBy, receivedBy *string
	var requestedAt time.Time
	var approvedAt, dispatchedAt, receivedAt *time.Time

	err := tx.QueryRow(ctx, `
		SELECT tr.id, tr.transfer_no, tr.status, tr.note,
		       src.name, dst.name, c.base_currency,
		       ru.full_name, tr.requested_at,
		       au.full_name, tr.approved_at, tr.dispatched_at,
		       vu.full_name, tr.received_at
		FROM stock_transfer tr
		JOIN warehouse src ON src.id = tr.from_warehouse_id
		JOIN warehouse dst ON dst.id = tr.to_warehouse_id
		JOIN company   c   ON c.id   = tr.company_id
		LEFT JOIN app_user ru ON ru.id = tr.requested_by
		LEFT JOIN app_user au ON au.id = tr.approved_by
		LEFT JOIN app_user vu ON vu.id = tr.received_by
		WHERE tr.id = $1 AND tr.company_id = $2`,
		id, scope.CompanyID).
		Scan(&t.ID, &t.Number, &t.Status, &note, &t.From, &t.To, &t.Currency,
			&requestedBy, &requestedAt, &approvedBy, &approvedAt,
			&dispatchedAt, &receivedBy, &receivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, errs.New(errs.CodeNotFound,
			"That transfer is not this business's.")
	}
	if err != nil {
		return Transfer{}, err
	}

	t.Note = deref(note)
	t.RequestedBy = deref(requestedBy)
	t.ApprovedBy = deref(approvedBy)
	t.ReceivedBy = deref(receivedBy)
	t.RequestedAt = requestedAt.Format(time.RFC3339)
	t.ApprovedAt = stamp(approvedAt)
	t.DispatchedAt = stamp(dispatchedAt)
	t.ReceivedAt = stamp(receivedAt)

	rows, err := tx.Query(ctx, `
		SELECT l.variant_id, v.sku, p.name,
		       l.qty_requested, l.qty_dispatched, l.qty_received, l.value_dispatched
		FROM stock_transfer_line l
		JOIN variant v ON v.id = l.variant_id
		JOIN product p ON p.id = v.product_id
		WHERE l.transfer_id = $1
		ORDER BY p.name, v.sku`, id)
	if err != nil {
		return Transfer{}, err
	}
	defer rows.Close()

	value, short := decimal.Zero, decimal.Zero
	for rows.Next() {
		var l TransferLine
		var requested decimal.Decimal
		var dispatched, receivedQty, lineValue *decimal.Decimal
		if err := rows.Scan(&l.VariantID, &l.SKU, &l.Product,
			&requested, &dispatched, &receivedQty, &lineValue); err != nil {
			return Transfer{}, err
		}
		l.Requested = requested.String()
		if dispatched != nil {
			l.Dispatched = dispatched.String()
		}
		if receivedQty != nil {
			l.Received = receivedQty.String()
		}
		if lineValue != nil {
			l.Value = lineValue.StringFixed(2)
			value = value.Add(*lineValue)
		}
		if dispatched != nil && receivedQty != nil {
			if gap := dispatched.Sub(*receivedQty); gap.IsPositive() {
				l.Short = gap.String()
				short = short.Add(gap)
			}
		}
		t.Lines = append(t.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return Transfer{}, err
	}

	if value.IsPositive() {
		t.Value = value.StringFixed(2)
	}
	if short.IsPositive() {
		t.ShortBy = short.String()
	}
	return t, nil
}

// Transfer reads one.
func (s *Service) Transfer(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Transfer, error) {
	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := s.readTransfer(ctx, tx, scope, id)
		out = t
		return e
	})
	return out, err
}

// Transfers lists them, newest first. `status=open` is the working view: every
// transfer somebody still has to do something about.
func (s *Service) Transfers(
	ctx context.Context, scope Scope, status string, limit int,
) ([]Transfer, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	open := strings.EqualFold(status, "open")
	if open {
		status = ""
	}

	out := []Transfer{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tr.id, tr.transfer_no, tr.status, coalesce(tr.note, ''),
			       src.name, dst.name, c.base_currency,
			       coalesce(ru.full_name, ''), tr.requested_at,
			       coalesce((SELECT sum(value_dispatched) FROM stock_transfer_line
			                 WHERE transfer_id = tr.id), 0),
			       coalesce((SELECT sum(qty_dispatched - coalesce(qty_received, 0))
			                 FROM stock_transfer_line
			                 WHERE transfer_id = tr.id AND qty_dispatched IS NOT NULL), 0)
			FROM stock_transfer tr
			JOIN warehouse src ON src.id = tr.from_warehouse_id
			JOIN warehouse dst ON dst.id = tr.to_warehouse_id
			JOIN company   c   ON c.id   = tr.company_id
			LEFT JOIN app_user ru ON ru.id = tr.requested_by
			WHERE tr.company_id = $1
			  AND ($2 = '' OR tr.status = $2)
			  AND (NOT $3 OR tr.status IN ('requested','approved','dispatched'))
			ORDER BY tr.requested_at DESC
			LIMIT $4`,
			scope.CompanyID, status, open, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var t Transfer
			var requestedAt time.Time
			var value, short decimal.Decimal
			if err := rows.Scan(&t.ID, &t.Number, &t.Status, &t.Note,
				&t.From, &t.To, &t.Currency, &t.RequestedBy, &requestedAt,
				&value, &short); err != nil {
				return err
			}
			t.RequestedAt = requestedAt.Format(time.RFC3339)
			if value.IsPositive() {
				t.Value = value.StringFixed(2)
			}
			if short.IsPositive() {
				t.ShortBy = short.String()
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func claimTransferNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n string
	err := tx.QueryRow(ctx, `SELECT claim_stock_transfer_no($1)`, companyID).Scan(&n)
	return n, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
