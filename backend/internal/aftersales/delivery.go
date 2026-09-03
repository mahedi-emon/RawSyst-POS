// Delivery and stock reservation (blueprint B13).
package aftersales

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// deliveryFlow is which status may follow which.
//
// A map rather than a chain of ifs, because the interesting part is what is
// ABSENT: nothing leads out of 'delivered', so a completed delivery cannot be
// reopened and quietly re-run, and 'failed' leads back to 'assigned' because a
// second attempt tomorrow is the normal case rather than an exception.
var deliveryFlow = map[string][]string{
	"pending":          {"assigned", "returned"},
	"assigned":         {"picked_up", "failed", "returned"},
	"picked_up":        {"out_for_delivery", "failed", "returned"},
	"out_for_delivery": {"delivered", "failed"},
	"failed":           {"assigned", "returned"},
	"delivered":        {},
	"returned":         {},
}

func canAdvance(from, to string) bool {
	for _, s := range deliveryFlow[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Delivery is one consignment.
type Delivery struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"delivery_no"`
	OrderID  uuid.UUID `json:"order_id"`
	OrderNo  string    `json:"order_no,omitempty"`
	Status   string    `json:"status"`
	Customer string    `json:"customer,omitempty"`

	DriverID   *uuid.UUID `json:"driver_id,omitempty"`
	DriverName string     `json:"driver_name,omitempty"`

	Address string `json:"address"`
	Phone   string `json:"phone,omitempty"`
	Fee     string `json:"fee"`

	IsCOD          bool   `json:"is_cod"`
	CODAmount      string `json:"cod_amount"`
	CODCollectedAt string `json:"cod_collected_at,omitempty"`

	AssignedAt    string `json:"assigned_at,omitempty"`
	PickedUpAt    string `json:"picked_up_at,omitempty"`
	DeliveredAt   string `json:"delivered_at,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	Attempts      int    `json:"attempt_count"`

	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`

	// The currency the fee and the cash-on-delivery figure are in. A
	// consignment is always priced in its company's base currency, but the
	// screen that shows a driver "180.00 to collect" has to say 180 of what.
	Currency string          `json:"currency"`
	Events   []DeliveryEvent `json:"events,omitempty"`
}

// DeliveryEvent is one step in the consignment's history.
type DeliveryEvent struct {
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
	RecordedBy string `json:"recorded_by,omitempty"`
	RecordedAt string `json:"recorded_at"`
}

// NewDelivery books a consignment against a confirmed order.
type NewDelivery struct {
	OrderID   uuid.UUID
	Address   string
	Phone     string
	Fee       decimal.Decimal
	IsCOD     bool
	CODAmount decimal.Decimal
	DriverID  *uuid.UUID
	Note      string
}

// BookDelivery creates the consignment.
//
// It does not post. The sale that produced the order posts its own revenue and
// VAT, and a delivery fee is a line on that invoice rather than a second
// document — booking it here would give the shop income twice for one delivery.
func (s *Service) BookDelivery(
	ctx context.Context, scope Scope, in NewDelivery,
) (Delivery, error) {
	if strings.TrimSpace(in.Address) == "" {
		return Delivery{}, errs.Validation("Say where it is going.").
			WithField("address", "A driver cannot deliver to a blank address.")
	}
	if in.Fee.IsNegative() || in.CODAmount.IsNegative() {
		return Delivery{}, errs.New(errs.CodeInvalidInput,
			"A delivery fee and a cash-on-delivery amount cannot be negative.")
	}
	if !in.IsCOD && in.CODAmount.IsPositive() {
		return Delivery{}, errs.Validation(
			"This delivery is not marked cash on delivery.").
			WithField("cod_amount",
				"Either mark it cash on delivery, or leave the amount at zero.")
	}

	var out Delivery
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var state string
		e := tx.QueryRow(ctx,
			`SELECT state FROM sales_order WHERE id = $1 AND company_id = $2`,
			in.OrderID, scope.CompanyID).Scan(&state)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That order was not found.")
		}
		if e != nil {
			return e
		}
		// A quotation is not a commitment and a cancelled order is not going
		// anywhere. Either would put a driver on the road for goods the shop
		// has not agreed to sell.
		if state == "quotation" || state == "cancelled" {
			return errs.Newf(errs.CodeConflict,
				"That order is a %s, so there is nothing to deliver yet.", state)
		}

		if in.DriverID != nil {
			if e := checkUserInTenant(ctx, tx, *in.DriverID); e != nil {
				return e
			}
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "delivery", "DEL")
		if e != nil {
			return e
		}

		status := "pending"
		var assignedAt *time.Time
		if in.DriverID != nil {
			status = "assigned"
			now := time.Now().UTC()
			assignedAt = &now
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO delivery
			  (tenant_id, company_id, delivery_no, order_id, status, driver_id,
			   address, phone, fee, is_cod, cod_amount, assigned_at, note,
			   created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.OrderID, status,
			in.DriverID, strings.TrimSpace(in.Address), nullText(in.Phone),
			in.Fee, in.IsCOD, in.CODAmount, assignedAt, nullText(in.Note),
			scope.UserID).Scan(&id); e != nil {
			return e
		}

		if e := recordEvent(ctx, tx, scope, id, status, "Booked"); e != nil {
			return e
		}

		read, e := s.readDelivery(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return Delivery{}, db.Translate(err, "")
	}
	return out, nil
}

// AdvanceDelivery moves a consignment along its pipeline.
//
// The transition is checked against deliveryFlow rather than simply written,
// because the statuses are not interchangeable labels: marking something
// delivered that was never picked up loses the fact that it never left, and a
// driver tapping the wrong row should be told rather than obeyed.
type Advance struct {
	Status   string
	Note     string
	DriverID *uuid.UUID
	// Latitude and Longitude are where the driver said they were. Optional: a
	// phone with no signal must still be able to close a delivery.
	Latitude  *decimal.Decimal
	Longitude *decimal.Decimal
	// CollectedCOD marks the cash as taken. Only meaningful on delivery.
	CollectedCOD bool
}

// AdvanceDelivery applies one step.
func (s *Service) AdvanceDelivery(
	ctx context.Context, scope Scope, id uuid.UUID, in Advance,
) (Delivery, error) {
	var out Delivery
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var current string
		var driverID *uuid.UUID
		var isCOD bool
		e := tx.QueryRow(ctx, `
			SELECT status, driver_id, is_cod FROM delivery
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			id, scope.CompanyID).Scan(&current, &driverID, &isCOD)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That delivery was not found.")
		}
		if e != nil {
			return e
		}

		if !canAdvance(current, in.Status) {
			return errs.Newf(errs.CodeConflict,
				"A delivery that is %s cannot become %s.", current, in.Status)
		}
		if in.Status == "failed" && strings.TrimSpace(in.Note) == "" {
			return errs.Validation("Say why it could not be delivered.").
				WithField("note",
					"Whether to try again tomorrow or ring the customer "+
						"depends on the reason.")
		}

		if in.DriverID != nil {
			if e := checkUserInTenant(ctx, tx, *in.DriverID); e != nil {
				return e
			}
			driverID = in.DriverID
		}
		if in.Status == "assigned" && driverID == nil {
			return errs.Validation("Say who is taking it.").
				WithField("driver_id", "An assigned delivery needs a driver.")
		}

		now := time.Now().UTC()
		set := map[string]any{"status": in.Status, "driver_id": driverID}

		switch in.Status {
		case "assigned":
			set["assigned_at"] = now
		case "picked_up":
			set["picked_up_at"] = now
		case "delivered":
			set["delivered_at"] = now
			if isCOD && in.CollectedCOD {
				set["cod_collected_at"] = now
			}
		case "failed":
			set["failure_reason"] = strings.TrimSpace(in.Note)
		}

		// A failed attempt is an attempt. The count is what tells a manager a
		// consignment has been out three times and is not getting there.
		bump := ""
		if in.Status == "failed" || in.Status == "out_for_delivery" {
			bump = ", attempt_count = attempt_count + 1"
		}

		if _, e := tx.Exec(ctx, `
			UPDATE delivery SET
			  status = $3, driver_id = $4,
			  assigned_at   = coalesce($5, assigned_at),
			  picked_up_at  = coalesce($6, picked_up_at),
			  delivered_at  = coalesce($7, delivered_at),
			  cod_collected_at = coalesce($8, cod_collected_at),
			  failure_reason = $9`+bump+`
			WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, in.Status, driverID,
			set["assigned_at"], set["picked_up_at"], set["delivered_at"],
			set["cod_collected_at"], set["failure_reason"]); e != nil {
			return db.Translate(e, "That delivery could not be updated.")
		}

		if e := recordEventAt(ctx, tx, scope, id, in.Status, in.Note,
			in.Latitude, in.Longitude); e != nil {
			return e
		}

		// A delivered consignment consumes the stock its order was holding:
		// the goods have physically gone, so the reservation that stood in for
		// them is released and the sale's own movement is what remains.
		if in.Status == "delivered" {
			if e := s.releaseForOrderInTx(ctx, tx, scope, id, "consumed"); e != nil {
				return e
			}
		}
		// One that came back releases its hold: the stock is available again.
		if in.Status == "returned" {
			if e := s.releaseForOrderInTx(ctx, tx, scope, id, "released"); e != nil {
				return e
			}
		}

		read, e := s.readDelivery(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return Delivery{}, db.Translate(err, "")
	}
	return out, nil
}

// releaseForOrderInTx closes out the reservations a delivery's order held.
func (s *Service) releaseForOrderInTx(
	ctx context.Context, tx pgx.Tx, scope Scope, deliveryID uuid.UUID,
	reason string,
) error {
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT order_id FROM delivery WHERE id = $1`,
		deliveryID).Scan(&orderID); err != nil {
		return err
	}
	return releaseOrderHolds(ctx, tx, scope, orderID, reason)
}

// releaseOrderHolds writes the negative rows that let go of an order's stock.
//
// One row per (variant, warehouse) still held, for the net amount held. A
// release is never an UPDATE of the original: the ledger is append-only, and
// "held 5, released 5" is a history somebody can read while "qty = 0" is not.
func releaseOrderHolds(
	ctx context.Context, tx pgx.Tx, scope Scope, orderID uuid.UUID, reason string,
) error {
	rows, err := tx.Query(ctx, `
		SELECT variant_id, warehouse_id, sum(qty)
		FROM stock_reservation
		WHERE order_id = $1
		GROUP BY variant_id, warehouse_id
		HAVING sum(qty) > 0`, orderID)
	if err != nil {
		return err
	}
	type hold struct {
		variantID, warehouseID uuid.UUID
		qty                    decimal.Decimal
	}
	var holds []hold
	for rows.Next() {
		var h hold
		if e := rows.Scan(&h.variantID, &h.warehouseID, &h.qty); e != nil {
			rows.Close()
			return e
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Null rather than the zero uuid when nobody is behind this. The expiry
	// sweep releases a lapsed hold on its own initiative, and writing
	// 00000000-… would break the foreign key and, worse, name a person who
	// did not do it. `created_by` is nullable precisely so a release can say
	// "the system, on a deadline".
	var by *uuid.UUID
	if scope.UserID != uuid.Nil {
		id := scope.UserID
		by = &id
	}

	for _, h := range holds {
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_reservation
			  (tenant_id, company_id, variant_id, warehouse_id, qty, reason,
			   order_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			scope.TenantID, scope.CompanyID, h.variantID, h.warehouseID,
			h.qty.Neg(), reason, orderID, by); e != nil {
			return e
		}
	}
	return nil
}

// --- Reservations --------------------------------------------------------

// Reserve holds stock against an order so a second channel cannot sell it.
//
// B13: "Stock Reservation (so two channels can't both sell the last unit)".
// The check and the write happen under a lock on the variant's existing
// reservations, because two web orders arriving in the same instant would
// otherwise both read "one available" and both hold it.
func (s *Service) Reserve(
	ctx context.Context, scope Scope, orderID, variantID, warehouseID uuid.UUID,
	qty decimal.Decimal, expires *time.Time,
) error {
	if !qty.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"Say how much to hold.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Lock the variant's reservation history for this warehouse. Taking the
		// lock on a row that may not exist yet is why this is an advisory lock
		// rather than SELECT ... FOR UPDATE: there is nothing to lock before
		// the first reservation, and two callers would both find nothing.
		if _, e := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			variantID.String()+warehouseID.String()); e != nil {
			return e
		}

		var available decimal.Decimal
		if e := tx.QueryRow(ctx,
			`SELECT stock_available_to_sell($1, $2)`,
			variantID, warehouseID).Scan(&available); e != nil {
			return e
		}
		if available.LessThan(qty) {
			return errs.Newf(errs.CodeConflict,
				"Only %s of that is free to sell; the rest is already held for "+
					"another order.", available.String())
		}

		_, e := tx.Exec(ctx, `
			INSERT INTO stock_reservation
			  (tenant_id, company_id, variant_id, warehouse_id, qty, reason,
			   order_id, expires_at, created_by)
			VALUES ($1,$2,$3,$4,$5,'held',$6,$7,$8)`,
			scope.TenantID, scope.CompanyID, variantID, warehouseID, qty,
			orderID, expires, scope.UserID)
		return e
	})
	return db.Translate(err, "")
}

// ReleaseOrder lets go of everything an order was holding, when it is cancelled.
func (s *Service) ReleaseOrder(
	ctx context.Context, scope Scope, orderID uuid.UUID,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return releaseOrderHolds(ctx, tx, scope, orderID, "released")
	})
	return db.Translate(err, "")
}

// ExpireHolds releases reservations whose deadline has passed.
//
// B13 reserves against UNPAID orders, so an abandoned basket would otherwise
// hold the last unit forever. Run from the job queue; returns how many were
// let go so a run that freed nothing is distinguishable from one that did not
// happen.
func (s *Service) ExpireHolds(ctx context.Context, scope Scope) (int, error) {
	var freed int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT DISTINCT order_id FROM stock_reservation
			WHERE company_id = $1 AND reason = 'held'
			  AND expires_at IS NOT NULL AND expires_at < now()
			  AND order_id IS NOT NULL`, scope.CompanyID)
		if e != nil {
			return e
		}
		var orders []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			orders = append(orders, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, id := range orders {
			if e := releaseOrderHolds(ctx, tx, scope, id, "released"); e != nil {
				return e
			}
			freed++
		}
		return nil
	})
	return freed, db.Translate(err, "")
}

// Availability is what a channel may sell right now.
type Availability struct {
	VariantID uuid.UUID `json:"variant_id"`
	OnHand    string    `json:"on_hand"`
	Reserved  string    `json:"reserved"`
	Available string    `json:"available_to_sell"`
}

// Available reports on hand, reserved and free to sell for one variant.
func (s *Service) Available(
	ctx context.Context, scope Scope, variantID, warehouseID uuid.UUID,
) (Availability, error) {
	out := Availability{VariantID: variantID}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var onHand, reserved, available decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT stock_on_hand($1,$2), stock_reserved($1,$2),
			       stock_available_to_sell($1,$2)`,
			variantID, warehouseID).Scan(&onHand, &reserved, &available); e != nil {
			return e
		}
		out.OnHand = onHand.String()
		out.Reserved = reserved.String()
		out.Available = available.String()
		return nil
	})
	return out, db.Translate(err, "")
}

// --- Reads ---------------------------------------------------------------

// Deliveries lists consignments.
//
// mineOnly restricts the list to the caller's own runs, which is what a driver
// holding delivery.view sees: A6.1 gives Delivery Staff "assigned delivery
// orders only", and a driver who could list every consignment in the company
// would be reading the customer address book.
func (s *Service) Deliveries(
	ctx context.Context, scope Scope, status string, mineOnly bool,
) ([]Delivery, error) {
	out := []Delivery{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, deliverySelect+`
			WHERE d.company_id = $1
			  AND ($2 = '' OR d.status = $2)
			  AND (NOT $3::boolean OR d.driver_id = $4)
			ORDER BY d.created_at DESC
			LIMIT 500`, scope.CompanyID, status, mineOnly, scope.UserID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			d, e := scanDelivery(rows)
			if e != nil {
				return e
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ReadDelivery returns one consignment with its history.
func (s *Service) ReadDelivery(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Delivery, error) {
	var out Delivery
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readDelivery(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

const deliverySelect = `
	SELECT d.id, d.delivery_no, d.order_id, coalesce(o.order_no, ''),
	       d.status, coalesce(c.name, ''), d.driver_id,
	       coalesce(u.full_name, ''), d.address, coalesce(d.phone, ''),
	       d.fee, d.is_cod, d.cod_amount, d.cod_collected_at,
	       d.assigned_at, d.picked_up_at, d.delivered_at,
	       coalesce(d.failure_reason, ''), d.attempt_count,
	       coalesce(d.note, ''), d.created_at, co.base_currency
	FROM delivery d
	JOIN company co         ON co.id = d.company_id
	LEFT JOIN sales_order o ON o.id = d.order_id
	LEFT JOIN customer c    ON c.id = o.customer_id
	LEFT JOIN app_user u    ON u.id = d.driver_id`

func (s *Service) readDelivery(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Delivery, error) {
	row := tx.QueryRow(ctx, deliverySelect+`
		WHERE d.id = $1 AND d.company_id = $2`, id, companyID)
	out, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, errs.New(errs.CodeNotFound,
			"That delivery was not found.")
	}
	if err != nil {
		return Delivery{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT e.status, coalesce(e.note, ''), coalesce(u.full_name, ''),
		       e.recorded_at
		FROM delivery_event e
		LEFT JOIN app_user u ON u.id = e.recorded_by
		WHERE e.delivery_id = $1 ORDER BY e.recorded_at`, id)
	if err != nil {
		return Delivery{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev DeliveryEvent
		var at time.Time
		if e := rows.Scan(&ev.Status, &ev.Note, &ev.RecordedBy, &at); e != nil {
			return Delivery{}, e
		}
		ev.RecordedAt = at.UTC().Format(time.RFC3339)
		out.Events = append(out.Events, ev)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanDelivery(row scanner) (Delivery, error) {
	var d Delivery
	var fee, cod decimal.Decimal
	var codAt, assigned, picked, delivered *time.Time
	var created time.Time
	if err := row.Scan(&d.ID, &d.Number, &d.OrderID, &d.OrderNo, &d.Status,
		&d.Customer, &d.DriverID, &d.DriverName, &d.Address, &d.Phone,
		&fee, &d.IsCOD, &cod, &codAt, &assigned, &picked, &delivered,
		&d.FailureReason, &d.Attempts, &d.Note, &created,
		&d.Currency); err != nil {
		return Delivery{}, err
	}
	d.Fee = fee.StringFixed(2)
	d.CODAmount = cod.StringFixed(2)
	d.CreatedAt = created.UTC().Format(time.RFC3339)
	for _, p := range []struct {
		t   *time.Time
		dst *string
	}{{codAt, &d.CODCollectedAt}, {assigned, &d.AssignedAt},
		{picked, &d.PickedUpAt}, {delivered, &d.DeliveredAt}} {
		if p.t != nil {
			*p.dst = p.t.UTC().Format(time.RFC3339)
		}
	}
	d.Events = []DeliveryEvent{}
	return d, nil
}

func recordEvent(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
	status, note string,
) error {
	return recordEventAt(ctx, tx, scope, id, status, note, nil, nil)
}

func recordEventAt(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
	status, note string, lat, lon *decimal.Decimal,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO delivery_event
		  (tenant_id, delivery_id, status, note, latitude, longitude,
		   recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		scope.TenantID, id, status, nullText(note), lat, lon, scope.UserID)
	return err
}

// checkUserInTenant refuses a driver who is not staff here.
//
// Row-level security proves the row belongs to the tenant, which is exactly
// what this needs — but a nil result must be reported as "not found" rather
// than passed through as a null driver, or a typo in an id would silently book
// an unassigned delivery.
func checkUserInTenant(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM app_user WHERE id = $1 AND status <> 'disabled'`,
		userID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That member of staff was not found.")
	}
	return err
}
