package orders

// Reading orders back, and the three documents B11 asks a warehouse to print.
//
// # Why the delivery note carries no prices
//
// B11 is explicit: the delivery challan is "itemized without pricing (for
// logistics/proof-of-delivery use)". A driver, a courier and whoever signs for
// the goods at the other end all handle that piece of paper, and none of them
// is entitled to know what the customer paid — least of all a competitor's
// warehouse staff at a shared loading bay.
//
// So `Document` for a delivery note simply has no price fields to fill in. Not
// blanked at render time: absent from the type, so no future screen can put
// them back by accident.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Filter narrows the order list.
type Filter struct {
	State      string
	Channel    string
	CustomerID *uuid.UUID
	// Open is the working view: everything somebody still has to act on.
	Open  bool
	Limit int
}

// List reads orders, newest first.
func (s *Service) List(
	ctx context.Context, scope Scope, f Filter,
) ([]Order, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	out := []Order{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, orderSelect+`
			WHERE o.company_id = $1
			  AND ($2 = '' OR o.state = $2)
			  AND ($3 = '' OR o.channel = $3)
			  AND ($4::uuid IS NULL OR o.customer_id = $4)
			  AND (NOT $5 OR o.state NOT IN ('completed', 'cancelled'))
			ORDER BY o.created_at DESC
			LIMIT $6`,
			scope.CompanyID, f.State, f.Channel, f.CustomerID, f.Open, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			o, e := scanOrder(rows)
			if e != nil {
				return e
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// Order reads one, with its lines.
func (s *Service) Order(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Order, error) {
	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		o, e := s.read(ctx, tx, scope, id)
		out = o
		return e
	})
	return out, err
}

// orderSelect is the one query the list and the single read share, so the two
// cannot drift into reporting different totals.
const orderSelect = `
	SELECT o.id, o.order_no, o.state, o.channel, coalesce(o.region, ''),
	       o.currency, o.customer_id, coalesce(c.name, ''),
	       coalesce(st.name, ''),
	       coalesce(to_char(o.valid_until, 'YYYY-MM-DD'), ''),
	       coalesce(o.deliver_to, ''), coalesce(o.deliver_phone, ''),
	       coalesce(o.notes, ''),
	       o.invoice_id, coalesce(i.human_number, ''),
	       coalesce(u.full_name, ''), o.created_at,
	       coalesce(o.cancel_reason, ''),
	       coalesce((SELECT sum(l.qty * l.unit_price) FROM sales_order_line l
	                 WHERE l.order_id = o.id), 0),
	       coalesce((SELECT sum(l.discount) FROM sales_order_line l
	                 WHERE l.order_id = o.id), 0),
	       (o.valid_until IS NOT NULL AND o.valid_until < current_date
	        AND o.state = 'quotation')
	FROM sales_order o
	LEFT JOIN customer c ON c.id = o.customer_id
	LEFT JOIN store st ON st.id = o.store_id
	LEFT JOIN sales_invoice i ON i.id = o.invoice_id
	LEFT JOIN app_user u ON u.id = o.created_by`

func scanOrder(rows pgx.Rows) (Order, error) {
	var o Order
	var createdAt time.Time
	var subtotal, discount decimal.Decimal
	if err := rows.Scan(&o.ID, &o.Number, &o.State, &o.Channel, &o.Region,
		&o.Currency, &o.CustomerID, &o.CustomerName, &o.Store, &o.ValidUntil,
		&o.DeliverTo, &o.DeliverPhone, &o.Notes, &o.InvoiceID,
		&o.InvoiceNumber, &o.CreatedBy, &createdAt, &o.CancelReason,
		&subtotal, &discount, &o.Expired); err != nil {
		return Order{}, err
	}
	o.CreatedAt = createdAt.Format(time.RFC3339)
	o.Subtotal = subtotal.StringFixed(2)
	o.Discount = discount.StringFixed(2)
	o.Total = subtotal.Sub(discount).StringFixed(2)
	return o, nil
}

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Order, error) {
	rows, err := tx.Query(ctx, orderSelect+`
		WHERE o.id = $1 AND o.company_id = $2`, id, scope.CompanyID)
	if err != nil {
		return Order{}, err
	}
	if !rows.Next() {
		rows.Close()
		return Order{}, errs.New(errs.CodeNotFound,
			"That order is not this business's.")
	}
	out, err := scanOrder(rows)
	rows.Close()
	if err != nil {
		return Order{}, err
	}

	lines, err := tx.Query(ctx, `
		SELECT l.id, l.line_no, l.variant_id, v.sku, p.name,
		       coalesce(l.description, ''),
		       l.qty, l.unit_price, l.discount, l.qty_picked, l.qty_delivered
		FROM sales_order_line l
		JOIN variant v ON v.id = l.variant_id
		JOIN product p ON p.id = v.product_id
		WHERE l.order_id = $1
		ORDER BY l.line_no`, id)
	if err != nil {
		return Order{}, err
	}
	defer lines.Close()

	for lines.Next() {
		var l Line
		var qty, price, discount, picked, delivered decimal.Decimal
		if err := lines.Scan(&l.ID, &l.LineNo, &l.VariantID, &l.SKU, &l.Product,
			&l.Description, &qty, &price, &discount, &picked,
			&delivered); err != nil {
			return Order{}, err
		}
		l.Qty = qty.String()
		l.UnitPrice = price.StringFixed(2)
		l.Discount = discount.StringFixed(2)
		l.LineTotal = qty.Mul(price).Sub(discount).StringFixed(2)
		l.Picked = picked.String()
		l.Delivered = delivered.String()
		out.Lines = append(out.Lines, l)
	}
	return out, lines.Err()
}

// --- the warehouse documents (B11) ----------------------------------------

// DocumentLine is one line of a picking, packing or delivery note.
//
// No price fields, on any of the three. See the note at the top of this file:
// a delivery note is handled by drivers and by whoever signs for the goods, and
// B11 says itemised without pricing. Leaving the fields off the type rather
// than blanking them at render time means no future screen can put them back.
type DocumentLine struct {
	LineNo      int    `json:"line_no"`
	SKU         string `json:"sku"`
	Barcode     string `json:"barcode,omitempty"`
	Product     string `json:"product"`
	Description string `json:"description,omitempty"`

	// Location is where a picker will find it. B11 asks for the picking slip
	// to be "organized by warehouse/aisle/shelf"; this product tracks stock to
	// a location and no further, so that is what it says.
	Location string `json:"location,omitempty"`

	Qty string `json:"qty"`
}

// Document is a picking slip, a packing slip or a delivery note.
type Document struct {
	Kind   string `json:"kind"`
	Number string `json:"order_no"`

	Customer     string `json:"customer,omitempty"`
	DeliverTo    string `json:"deliver_to,omitempty"`
	DeliverPhone string `json:"deliver_phone,omitempty"`
	Store        string `json:"store,omitempty"`
	PrintedAt    string `json:"printed_at"`

	Lines []DocumentLine `json:"lines"`

	// Note is what the document says about itself — that a delivery note
	// carries no prices, most importantly, so that somebody comparing it with
	// an invoice is not left wondering which is wrong.
	Note string `json:"note,omitempty"`
}

// The three B11 names.
const (
	DocPicking  = "picking"
	DocPacking  = "packing"
	DocDelivery = "delivery"
)

// Documentation draws one of B11's three warehouse documents.
func (s *Service) Documentation(
	ctx context.Context, scope Scope, id uuid.UUID, kind string,
) (Document, error) {
	switch kind {
	case DocPicking, DocPacking, DocDelivery:
	default:
		return Document{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a document this product prints for an order.", kind)
	}

	var out Document
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		out = Document{Kind: kind, PrintedAt: time.Now().UTC().Format(time.RFC3339)}

		e := tx.QueryRow(ctx, `
			SELECT o.order_no, coalesce(c.name, ''), coalesce(o.deliver_to, ''),
			       coalesce(o.deliver_phone, ''), coalesce(st.name, '')
			FROM sales_order o
			LEFT JOIN customer c ON c.id = o.customer_id
			LEFT JOIN store st ON st.id = o.store_id
			WHERE o.id = $1 AND o.company_id = $2`,
			id, scope.CompanyID).
			Scan(&out.Number, &out.Customer, &out.DeliverTo,
				&out.DeliverPhone, &out.Store)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That order is not this business's.")
		}
		if e != nil {
			return e
		}

		// Which quantity each document is about.
		//
		//   picking  — what to pull, so the ordered quantity
		//   packing  — what was pulled, so the picked quantity
		//   delivery — what is going, which is also the picked quantity: the
		//              delivered figure is filled in when it arrives, and a
		//              note printed from it would always be empty.
		column := "l.qty"
		if kind != DocPicking {
			column = "l.qty_picked"
		}

		rows, e := tx.Query(ctx, `
			SELECT l.line_no, v.sku, coalesce(v.barcode, ''), p.name,
			       coalesce(l.description, ''),
			       coalesce((SELECT w.name FROM stock_movement m
			                 JOIN warehouse w ON w.id = m.warehouse_id
			                 WHERE m.variant_id = l.variant_id
			                   AND m.company_id = $2
			                 ORDER BY m.occurred_at DESC LIMIT 1), ''),
			       `+column+`
			FROM sales_order_line l
			JOIN variant v ON v.id = l.variant_id
			JOIN product p ON p.id = v.product_id
			WHERE l.order_id = $1 AND `+column+` > 0
			ORDER BY l.line_no`, id, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var l DocumentLine
			var qty decimal.Decimal
			if e := rows.Scan(&l.LineNo, &l.SKU, &l.Barcode, &l.Product,
				&l.Description, &l.Location, &qty); e != nil {
				return e
			}
			l.Qty = qty.String()
			out.Lines = append(out.Lines, l)
		}
		return rows.Err()
	})
	return out, err
}
