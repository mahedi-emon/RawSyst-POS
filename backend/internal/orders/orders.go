// Package orders is the sales pipeline: quotations, orders, and the documents
// that go with a delivery (blueprint B11, and B12's wholesale rules).
//
// # A quotation is an order in a different state
//
// B11 asks for a quotation "convertible to a Sales Order or directly to an
// Invoice in one click". Two tables would mean a conversion that copies rows
// between them, and a copy is a place where a price can change without anybody
// meaning it to. So converting is a state change, the lines never move, and
// "the quote said 4,200" is a question with an answer.
//
// # An order is not an invoice, and this package never posts
//
// An order is a promise to sell. An invoice is the tax document that records
// the sale, is immutable, and sits in a hash chain E1 will not let anything
// edit. Conflating them is how a product ends up editing invoices.
//
// So an order can be re-priced and cancelled all the way up to the moment it is
// invoiced, at which point `sales.Finalize` does what it has always done —
// moves the stock, posts the journal, takes the chain position — and the order
// records which invoice it became. Nothing in this package touches the ledger
// or the stock ledger.
//
// # The states are a line, not a graph
//
//	quotation → confirmed → processing → packed → delivered → completed
//
// with `cancelled` reachable from anywhere before `completed`. Movement is
// forward only. An order that has been packed and is put back to `processing`
// tells a picker to pick it again, and the honest way to undo a mistake is to
// cancel and raise another — which leaves both facts visible, as everywhere
// else in this product.
package orders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The lifecycle B11 draws.
const (
	StateQuotation  = "quotation"
	StateConfirmed  = "confirmed"
	StateProcessing = "processing"
	StatePacked     = "packed"
	StateDelivered  = "delivered"
	StateCompleted  = "completed"
	StateCancelled  = "cancelled"
)

// forward is the one path through the states.
//
// A map rather than a switch so the whole graph is one thing a reader can see,
// and so `Advance` cannot grow a branch that quietly allows a step backwards.
var forward = map[string]string{
	StateQuotation:  StateConfirmed,
	StateConfirmed:  StateProcessing,
	StateProcessing: StatePacked,
	StatePacked:     StateDelivered,
	StateDelivered:  StateCompleted,
}

// Service manages orders.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Order is a quotation or a sales order.
type Order struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"order_no"`
	State    string    `json:"state"`
	Channel  string    `json:"channel"`
	Region   string    `json:"region,omitempty"`
	Currency string    `json:"currency"`

	CustomerID   *uuid.UUID `json:"customer_id,omitempty"`
	CustomerName string     `json:"customer,omitempty"`
	Store        string     `json:"store,omitempty"`

	ValidUntil   string `json:"valid_until,omitempty"`
	DeliverTo    string `json:"deliver_to,omitempty"`
	DeliverPhone string `json:"deliver_phone,omitempty"`
	Notes        string `json:"notes,omitempty"`

	Subtotal string `json:"subtotal"`
	Discount string `json:"discount"`
	Total    string `json:"total"`

	InvoiceID     *uuid.UUID `json:"invoice_id,omitempty"`
	InvoiceNumber string     `json:"invoice_no,omitempty"`

	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at"`

	CancelReason string `json:"cancel_reason,omitempty"`

	// Expired says a quotation's validity date has passed. Derived rather than
	// stored: a quote does not become a different row at midnight, and a stored
	// flag would need something to come along and set it.
	Expired bool `json:"expired,omitempty"`

	Lines []Line `json:"lines,omitempty"`
}

// Line is one product on an order.
type Line struct {
	ID          uuid.UUID `json:"id"`
	LineNo      int       `json:"line_no"`
	VariantID   uuid.UUID `json:"variant_id"`
	SKU         string    `json:"sku"`
	Product     string    `json:"product"`
	Description string    `json:"description,omitempty"`

	Qty       string `json:"qty"`
	UnitPrice string `json:"unit_price"`
	Discount  string `json:"discount"`
	LineTotal string `json:"line_total"`

	Picked    string `json:"qty_picked"`
	Delivered string `json:"qty_delivered"`
}

// NewOrder is a quotation being raised.
type NewOrder struct {
	CustomerID   *uuid.UUID
	StoreID      *uuid.UUID
	Channel      string
	Region       string
	ValidUntil   *time.Time
	DeliverTo    string
	DeliverPhone string
	Notes        string
	Lines        []NewLine
}

// NewLine is one product being put on one.
type NewLine struct {
	VariantID   uuid.UUID
	Description string
	Qty         decimal.Decimal
	UnitPrice   decimal.Decimal
	Discount    decimal.Decimal
}

// Raise creates a quotation.
//
// Always a quotation, never a confirmed order: confirming is a customer's
// decision and giving a route the power to skip it would put "the customer
// agreed" into the hands of whoever typed the order.
func (s *Service) Raise(
	ctx context.Context, scope Scope, in NewOrder,
) (Order, error) {
	if len(in.Lines) == 0 {
		return Order{}, errs.New(errs.CodeInvalidInput,
			"An order needs at least one line.")
	}
	for i, l := range in.Lines {
		if !l.Qty.IsPositive() {
			return Order{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many.", i+1)
		}
		if l.UnitPrice.IsNegative() {
			return Order{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has a price below nothing.", i+1)
		}
		if l.Discount.IsNegative() ||
			l.Discount.GreaterThan(l.Qty.Mul(l.UnitPrice)) {
			return Order{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has a discount larger than the line.", i+1)
		}
	}

	channel := strings.TrimSpace(in.Channel)
	if channel == "" {
		channel = "store"
	}

	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}

		number, e := claimOrderNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_order
			  (tenant_id, company_id, store_id, order_no, customer_id,
			   valid_until, channel, region, currency, notes,
			   deliver_to, deliver_phone, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.StoreID, number, in.CustomerID,
			in.ValidUntil, channel, nullIfBlank(in.Region), currency,
			nullIfBlank(in.Notes), nullIfBlank(in.DeliverTo),
			nullIfBlank(in.DeliverPhone), scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That order could not be raised.")
		}

		for i, l := range in.Lines {
			if _, e := tx.Exec(ctx, `
				INSERT INTO sales_order_line
				  (tenant_id, order_id, variant_id, line_no, description,
				   qty, unit_price, discount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				scope.TenantID, id, l.VariantID, i+1,
				nullIfBlank(l.Description), l.Qty, l.UnitPrice,
				l.Discount); e != nil {
				return db.Translate(e, "One of those lines could not be added.")
			}
		}

		read, e := s.read(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// Advance moves an order one step along the lifecycle.
//
// One step, and forward only. See the package note: putting a packed order back
// to processing tells a picker to pick it again, and the honest way to undo is
// to cancel and raise another.
func (s *Service) Advance(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Order, error) {
	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		current, number, e := s.stateOf(ctx, tx, scope, id)
		if e != nil {
			return e
		}

		next, ok := forward[current]
		if !ok {
			return errs.Newf(errs.CodeConflict,
				"%s is %s, and there is nowhere further for it to go.",
				number, plainState(current))
		}
		// Completing is what invoicing does, and invoicing is a different act
		// with a different permission. Letting this route set `completed`
		// would produce an order marked sold with no invoice behind it.
		if next == StateCompleted {
			return errs.Newf(errs.CodeConflict,
				"%s is delivered. Raise the invoice to complete it — an order "+
					"is finished by being invoiced, not by being marked so.",
				number)
		}

		if next == StateConfirmed {
			if e := s.checkWholesaleMinimums(ctx, tx, scope, id); e != nil {
				return e
			}
		}

		stamp := map[string]string{
			StateConfirmed: "confirmed_at",
			StateDelivered: "delivered_at",
		}[next]

		sql := `UPDATE sales_order SET state = $2 WHERE id = $1 AND company_id = $3`
		if stamp != "" {
			sql = fmt.Sprintf(
				`UPDATE sales_order SET state = $2, %s = now()
				 WHERE id = $1 AND company_id = $3`, stamp)
		}
		if _, e := tx.Exec(ctx, sql, id, next, scope.CompanyID); e != nil {
			return db.Translate(e, "That order could not be moved on.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "order_advanced",
			EntityType: "sales_order", EntityID: &id,
			Before: map[string]any{"state": current},
			After:  map[string]any{"state": next, "order": number},
		}); e != nil {
			return e
		}

		read, e := s.read(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// checkWholesaleMinimums enforces B12's minimum order quantity.
//
// At confirmation rather than when a line is added, deliberately. Somebody
// building a quote types a quantity, then a price, then changes the quantity —
// and a rule that refused every intermediate state would interrupt them three
// times before they finished one line.
func (s *Service) checkWholesaleMinimums(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) error {
	rows, err := tx.Query(ctx, `
		SELECT p.name, l.qty, v.min_wholesale_qty
		FROM sales_order_line l
		JOIN sales_order o ON o.id = l.order_id
		JOIN variant v ON v.id = l.variant_id
		JOIN product p ON p.id = v.product_id
		WHERE l.order_id = $1
		  AND o.channel = 'wholesale'
		  AND v.min_wholesale_qty IS NOT NULL
		  AND l.qty < v.min_wholesale_qty`, id)
	if err != nil {
		return err
	}
	defer rows.Close()

	var short []string
	for rows.Next() {
		var name string
		var qty, minimum decimal.Decimal
		if err := rows.Scan(&name, &qty, &minimum); err != nil {
			return err
		}
		short = append(short, fmt.Sprintf("%s (%s, minimum %s)",
			name, qty.String(), minimum.String()))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(short) > 0 {
		return errs.Newf(errs.CodeConflict,
			"These lines are below the wholesale minimum: %s.",
			strings.Join(short, "; "))
	}
	return nil
}

// Cancel abandons an order, with a reason.
func (s *Service) Cancel(
	ctx context.Context, scope Scope, id uuid.UUID, reason string,
) error {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return errs.Validation("Say why the order is being cancelled.").
			WithField("reason",
				"Somebody will ask why an order disappeared.")
	}
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		current, number, e := s.stateOf(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		if current == StateCompleted {
			return errs.Newf(errs.CodeConflict,
				"%s has been invoiced. An invoice is corrected by a credit "+
					"note, never by cancelling the order behind it.", number)
		}
		if current == StateCancelled {
			return errs.Newf(errs.CodeConflict, "%s is already cancelled.", number)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE sales_order
			SET state = 'cancelled', cancelled_at = now(), cancel_reason = $2
			WHERE id = $1 AND company_id = $3`,
			id, reason, scope.CompanyID); e != nil {
			return db.Translate(e, "That order could not be cancelled.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "order_cancelled",
			EntityType: "sales_order", EntityID: &id,
			Before: map[string]any{"state": current},
			After:  map[string]any{"order": number, "reason": reason},
		})
	})
}

// Pick records what a warehouse actually pulled.
//
// B11's picking slip tells staff what to pull; this is what came back. A short
// pick is an ordinary event — the shelf had four when the order wanted six —
// and it is recorded rather than refused, because the alternative is a picker
// unable to tell anybody what happened.
func (s *Service) Pick(
	ctx context.Context, scope Scope, id uuid.UUID, picked []LineQty,
) (Order, error) {
	return s.recordQuantities(ctx, scope, id, picked, "qty_picked", "order_picked")
}

// Deliver records what actually reached the customer.
func (s *Service) Deliver(
	ctx context.Context, scope Scope, id uuid.UUID, delivered []LineQty,
) (Order, error) {
	return s.recordQuantities(ctx, scope, id, delivered, "qty_delivered", "order_delivered")
}

// LineQty is one line and a quantity, used by picking and delivery.
type LineQty struct {
	LineID uuid.UUID
	Qty    decimal.Decimal
}

func (s *Service) recordQuantities(
	ctx context.Context, scope Scope, id uuid.UUID,
	quantities []LineQty, column, action string,
) (Order, error) {
	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		state, number, e := s.stateOf(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		if state == StateCompleted || state == StateCancelled {
			return errs.Newf(errs.CodeConflict,
				"%s is %s and cannot be changed.", number, plainState(state))
		}

		for _, q := range quantities {
			if q.Qty.IsNegative() {
				return errs.New(errs.CodeInvalidInput,
					"A quantity cannot be less than nothing.")
			}
			tag, e := tx.Exec(ctx, fmt.Sprintf(`
				UPDATE sales_order_line SET %s = $3
				WHERE id = $1 AND order_id = $2`, column),
				q.LineID, id, q.Qty)
			if e != nil {
				// The CHECK constraints refuse picked-above-ordered and
				// delivered-above-picked; this turns either into a sentence.
				return db.Translate(e,
					"That quantity is more than the line allows: a line cannot "+
						"be picked beyond what was ordered, or delivered "+
						"beyond what was picked.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"One of those lines is not on this order.")
			}
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     action,
			EntityType: "sales_order", EntityID: &id,
			After: map[string]any{"order": number, "lines": len(quantities)},
		}); e != nil {
			return e
		}

		read, e := s.read(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

func (s *Service) stateOf(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (string, string, error) {
	var state, number string
	err := tx.QueryRow(ctx, `
		SELECT state, order_no FROM sales_order
		WHERE id = $1 AND company_id = $2
		FOR UPDATE`, id, scope.CompanyID).Scan(&state, &number)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errs.New(errs.CodeNotFound,
			"That order is not this business's.")
	}
	return state, number, err
}

// plainState is a state as a person would say it.
func plainState(state string) string {
	switch state {
	case StateQuotation:
		return "still a quotation"
	case StateConfirmed:
		return "confirmed"
	case StateProcessing:
		return "being picked"
	case StatePacked:
		return "packed and ready"
	case StateDelivered:
		return "delivered"
	case StateCompleted:
		return "invoiced and finished"
	default:
		return "cancelled"
	}
}

func claimOrderNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n string
	err := tx.QueryRow(ctx, `SELECT claim_order_no($1)`, companyID).Scan(&n)
	return n, err
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
