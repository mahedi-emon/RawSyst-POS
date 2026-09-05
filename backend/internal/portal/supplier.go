// What a supplier sees when they sign in (blueprint F3).
//
// Every query is filtered on the CALLER's supplier id, taken from the session.
// No route in this file accepts a supplier id, so a supplier cannot ask what
// the shop is buying from anybody else — which on a procurement portal is not
// a privacy nicety but a commercial one.

package portal

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// SupplierHome is what the portal opens on.
type SupplierHome struct {
	SupplierName string `json:"supplier_name"`
	ContactName  string `json:"contact_name"`
	Currency     string `json:"currency"`

	// Orders waiting for an answer. The number the portal exists to make
	// visible: F3's whole argument is that a purchase order sitting in an
	// inbox is a purchase order nobody has accepted.
	AwaitingResponse int `json:"awaiting_response"`
	OpenOrders       int `json:"open_orders"`

	// What the shop owes them, and how much of it is late. A supplier chasing
	// payment is the other half of why they sign in.
	Outstanding string `json:"outstanding"`
	Overdue     string `json:"overdue"`

	OpenRFQs int `json:"open_rfqs"`
}

// Home builds it.
func (s *Service) SupplierHome(
	ctx context.Context, c Caller,
) (SupplierHome, error) {
	if c.SupplierID == nil {
		return SupplierHome{}, errs.New(errs.CodeForbidden,
			"That is not your account.")
	}
	var out SupplierHome
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT s.legal_name, u.full_name, co.base_currency
			FROM supplier_portal_user u
			JOIN supplier s ON s.id = u.supplier_id
			JOIN company co ON co.id = u.company_id
			WHERE u.id = $1`, c.PortalUserID).Scan(
			&out.SupplierName, &out.ContactName, &out.Currency); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That account was not found.")
			}
			return e
		}

		if e := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (
			         WHERE o.status = 'issued'
			           AND NOT EXISTS (SELECT 1 FROM po_response r
			                            WHERE r.po_id = o.id)),
			       count(*) FILTER (WHERE o.status IN ('issued', 'partial'))
			FROM purchase_order o
			WHERE o.company_id = $1 AND o.supplier_id = $2`,
			c.CompanyID, *c.SupplierID).Scan(
			&out.AwaitingResponse, &out.OpenOrders); e != nil {
			return e
		}

		var outstanding, overdue decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(greatest(b.total_inclusive - b.amount_paid - b.amount_credited, 0)), 0),
			       coalesce(sum(greatest(b.total_inclusive - b.amount_paid - b.amount_credited, 0))
			                FILTER (WHERE b.due_date < current_date), 0)
			FROM purchase_bill b
			WHERE b.company_id = $1 AND b.supplier_id = $2
			  AND b.status <> 'cancelled'
			  AND b.total_inclusive > b.amount_paid + b.amount_credited`,
			c.CompanyID, *c.SupplierID).Scan(&outstanding, &overdue); e != nil {
			return e
		}
		out.Outstanding = outstanding.StringFixed(2)
		out.Overdue = overdue.StringFixed(2)

		return tx.QueryRow(ctx, `
			SELECT count(*)
			FROM rfq_invitation i
			JOIN rfq r ON r.id = i.rfq_id
			WHERE i.supplier_id = $1 AND r.company_id = $2
			  AND i.declined_at IS NULL
			  AND r.status IN ('issued', 'comparing')`,
			*c.SupplierID, c.CompanyID).Scan(&out.OpenRFQs)
	})
	return out, db.Translate(err, "")
}

// SupplierOrder is one purchase order as its supplier sees it.
type SupplierOrder struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"po_number"`
	Status     string    `json:"status"`
	OrderedOn  string    `json:"ordered_on,omitempty"`
	ExpectedOn string    `json:"expected_on,omitempty"`
	Currency   string    `json:"currency"`
	Total      string    `json:"total"`

	// Their own answer, when they have given one.
	Response    string `json:"response,omitempty"`
	Comment     string `json:"comment,omitempty"`
	PromisedOn  string `json:"promised_on,omitempty"`
	RespondedAt string `json:"responded_at,omitempty"`

	Lines []SupplierOrderLine `json:"lines,omitempty"`
}

// SupplierOrderLine is what was asked for and what has arrived.
type SupplierOrderLine struct {
	LineNo      int    `json:"line_no"`
	SKU         string `json:"sku,omitempty"`
	Description string `json:"description"`
	Ordered     string `json:"qty_ordered"`
	Received    string `json:"qty_received"`
	UnitCost    string `json:"unit_cost"`
	Gross       string `json:"gross_amount"`
}

// SupplierOrders lists them, newest first.
func (s *Service) SupplierOrders(
	ctx context.Context, c Caller,
) ([]SupplierOrder, error) {
	if c.SupplierID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []SupplierOrder{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT o.id, o.po_number, o.status, o.ordered_on, o.expected_on,
			       o.currency,
			       coalesce((SELECT sum(l.gross_amount) FROM po_line l
			                  WHERE l.po_id = o.id), 0),
			       coalesce(r.response, ''), coalesce(r.comment, ''),
			       r.promised_on, r.responded_at
			FROM purchase_order o
			LEFT JOIN po_response r ON r.po_id = o.id
			WHERE o.company_id = $1 AND o.supplier_id = $2
			  AND o.status <> 'draft'
			ORDER BY o.ordered_on DESC NULLS LAST, o.po_number DESC
			LIMIT 200`, c.CompanyID, *c.SupplierID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var o SupplierOrder
			var total decimal.Decimal
			var ordered, expected, promised *time.Time
			var responded *time.Time
			if e := rows.Scan(&o.ID, &o.Number, &o.Status, &ordered, &expected,
				&o.Currency, &total, &o.Response, &o.Comment, &promised,
				&responded); e != nil {
				return e
			}
			o.Total = total.StringFixed(2)
			for _, p := range []struct {
				t   *time.Time
				dst *string
			}{{ordered, &o.OrderedOn}, {expected, &o.ExpectedOn},
				{promised, &o.PromisedOn}} {
				if p.t != nil {
					*p.dst = p.t.Format("2006-01-02")
				}
			}
			if responded != nil {
				o.RespondedAt = responded.UTC().Format(time.RFC3339)
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SupplierOrder reads one, with its lines and what has been received.
//
// F3 asks for the goods-receipt view — "what was actually received and
// accepted" — and a supplier reading it beside what they sent is how a dispute
// gets settled without a phone call.
func (s *Service) SupplierOrder(
	ctx context.Context, c Caller, poID uuid.UUID,
) (SupplierOrder, error) {
	if c.SupplierID == nil {
		return SupplierOrder{}, errs.New(errs.CodeForbidden,
			"That is not your account.")
	}
	var out SupplierOrder
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		var total decimal.Decimal
		var ordered, expected, promised, responded *time.Time
		e := tx.QueryRow(ctx, `
			SELECT o.id, o.po_number, o.status, o.ordered_on, o.expected_on,
			       o.currency,
			       coalesce((SELECT sum(l.gross_amount) FROM po_line l
			                  WHERE l.po_id = o.id), 0),
			       coalesce(r.response, ''), coalesce(r.comment, ''),
			       r.promised_on, r.responded_at
			FROM purchase_order o
			LEFT JOIN po_response r ON r.po_id = o.id
			WHERE o.id = $1 AND o.company_id = $2 AND o.supplier_id = $3
			  AND o.status <> 'draft'`,
			poID, c.CompanyID, *c.SupplierID).Scan(
			&out.ID, &out.Number, &out.Status, &ordered, &expected,
			&out.Currency, &total, &out.Response, &out.Comment, &promised,
			&responded)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That order was not found.")
		}
		if e != nil {
			return e
		}
		out.Total = total.StringFixed(2)
		for _, p := range []struct {
			t   *time.Time
			dst *string
		}{{ordered, &out.OrderedOn}, {expected, &out.ExpectedOn},
			{promised, &out.PromisedOn}} {
			if p.t != nil {
				*p.dst = p.t.Format("2006-01-02")
			}
		}
		if responded != nil {
			out.RespondedAt = responded.UTC().Format(time.RFC3339)
		}

		rows, e := tx.Query(ctx, `
			SELECT l.line_no, coalesce(v.sku, ''), coalesce(l.description, ''),
			       l.qty_ordered,
			       coalesce((SELECT sum(g.qty_received - g.qty_rejected)
			                   FROM grn_line g
			                  WHERE g.po_line_id = l.id), 0),
			       l.unit_cost, l.gross_amount
			FROM po_line l
			LEFT JOIN variant v ON v.id = l.variant_id
			WHERE l.po_id = $1
			ORDER BY l.line_no`, poID)
		if e != nil {
			return e
		}
		defer rows.Close()
		out.Lines = []SupplierOrderLine{}
		for rows.Next() {
			var l SupplierOrderLine
			var qty, received, cost, gross decimal.Decimal
			if e := rows.Scan(&l.LineNo, &l.SKU, &l.Description, &qty,
				&received, &cost, &gross); e != nil {
				return e
			}
			l.Ordered = qty.String()
			l.Received = received.String()
			l.UnitCost = cost.StringFixed(2)
			l.Gross = gross.StringFixed(2)
			out.Lines = append(out.Lines, l)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// RespondToOrder is F3's accept or reject.
//
// It records the supplier's answer and does NOT move the order's own status.
// The order's status is the SHOP's view of it — issued, partly received,
// closed — and a supplier declining does not receive goods or close anything.
// The buyer reads the response and decides.
func (s *Service) RespondToOrder(
	ctx context.Context, c Caller, poID uuid.UUID,
	response, comment, promisedOn string,
) (SupplierOrder, error) {
	if c.SupplierID == nil {
		return SupplierOrder{}, errs.New(errs.CodeForbidden,
			"That is not your account.")
	}
	switch response {
	case "accepted", "rejected", "accepted_with_changes":
	default:
		return SupplierOrder{}, errs.New(errs.CodeInvalidInput,
			"Say whether you accept the order, accept it with changes, or "+
				"cannot fulfil it.")
	}
	if response == "rejected" && strings.TrimSpace(comment) == "" {
		return SupplierOrder{}, errs.New(errs.CodeInvalidInput,
			"Tell the buyer why you cannot fulfil it.")
	}

	var promised *time.Time
	if promisedOn != "" {
		d, err := time.Parse("2006-01-02", promisedOn)
		if err != nil {
			return SupplierOrder{}, errs.New(errs.CodeInvalidInput,
				"That delivery date is not a date.")
		}
		promised = &d
	}

	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		// The order has to be theirs and has to be issued. Answering a draft
		// would be answering something the buyer has not sent.
		var theirs bool
		e := tx.QueryRow(ctx, `
			SELECT true FROM purchase_order
			 WHERE id = $1 AND company_id = $2 AND supplier_id = $3
			   AND status <> 'draft'`,
			poID, c.CompanyID, *c.SupplierID).Scan(&theirs)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That order was not found.")
		}
		if e != nil {
			return e
		}

		_, e = tx.Exec(ctx, `
			INSERT INTO po_response (
			  po_id, tenant_id, company_id, portal_user_id, response,
			  comment, promised_on)
			VALUES ($1,$2,$3,$4,$5,nullif($6,''),$7)
			ON CONFLICT (po_id) DO UPDATE SET
			  portal_user_id = excluded.portal_user_id,
			  response = excluded.response,
			  comment = excluded.comment,
			  promised_on = excluded.promised_on,
			  responded_at = now()`,
			poID, c.TenantID, c.CompanyID, c.PortalUserID, response,
			strings.TrimSpace(comment), promised)
		return db.Translate(e, "That answer could not be recorded.")
	})
	if err != nil {
		return SupplierOrder{}, db.Translate(err, "")
	}
	return s.SupplierOrder(ctx, c, poID)
}

// SupplierBill is what the shop owes on one document.
type SupplierBill struct {
	ID          uuid.UUID `json:"id"`
	Number      string    `json:"supplier_ref,omitempty"`
	BillDate    string    `json:"bill_date"`
	DueOn       string    `json:"due_on,omitempty"`
	Currency    string    `json:"currency"`
	Gross       string    `json:"gross_total"`
	Paid        string    `json:"paid_total"`
	Outstanding string    `json:"outstanding"`
	Status      string    `json:"status"`
	Overdue     bool      `json:"overdue"`
}

// SupplierBills is F3's "payment status and payment history".
func (s *Service) SupplierBills(
	ctx context.Context, c Caller,
) ([]SupplierBill, error) {
	if c.SupplierID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []SupplierBill{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT b.id, coalesce(b.supplier_ref, ''), b.bill_date, b.due_date,
			       b.currency, b.total_inclusive, b.amount_paid, b.status
			FROM purchase_bill b
			WHERE b.company_id = $1 AND b.supplier_id = $2
			ORDER BY b.bill_date DESC
			LIMIT 200`, c.CompanyID, *c.SupplierID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var b SupplierBill
			var gross, paid decimal.Decimal
			var billDate time.Time
			var due *time.Time
			if e := rows.Scan(&b.ID, &b.Number, &billDate, &due, &b.Currency,
				&gross, &paid, &b.Status); e != nil {
				return e
			}
			b.BillDate = billDate.Format("2006-01-02")
			b.Gross = gross.StringFixed(2)
			b.Paid = paid.StringFixed(2)
			b.Outstanding = gross.Sub(paid).StringFixed(2)
			if due != nil {
				b.DueOn = due.Format("2006-01-02")
				b.Overdue = gross.GreaterThan(paid) &&
					due.Before(time.Now().UTC())
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SupplierRFQ is a request for quotation this supplier was asked to answer.
type SupplierRFQ struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"rfq_no"`
	Status     string    `json:"status"`
	ClosesOn   string    `json:"closes_on,omitempty"`
	Note       string    `json:"note,omitempty"`
	Quoted     bool      `json:"quoted"`
	QuotedOn   string    `json:"quoted_on,omitempty"`
	QuoteTotal string    `json:"quote_total,omitempty"`
}

// SupplierRFQs lists them.
func (s *Service) SupplierRFQs(
	ctx context.Context, c Caller,
) ([]SupplierRFQ, error) {
	if c.SupplierID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []SupplierRFQ{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT r.id, r.rfq_number, r.status, r.closes_on,
			       coalesce(r.notes, ''),
			       q.id IS NOT NULL, q.received_on,
			       coalesce((SELECT sum(l.gross_amount)
			                   FROM supplier_quote_line l
			                  WHERE l.quote_id = q.id), 0)
			FROM rfq_invitation i
			JOIN rfq r ON r.id = i.rfq_id
			LEFT JOIN supplier_quote q
			       ON q.rfq_id = r.id AND q.supplier_id = i.supplier_id
			      AND q.status <> 'superseded'
			WHERE i.supplier_id = $1 AND r.company_id = $2
			  AND r.status <> 'draft'
			ORDER BY r.closes_on DESC NULLS LAST, r.rfq_number DESC
			LIMIT 100`, *c.SupplierID, c.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var q SupplierRFQ
			var closes, quotedOn *time.Time
			var total decimal.Decimal
			if e := rows.Scan(&q.ID, &q.Number, &q.Status, &closes, &q.Note,
				&q.Quoted, &quotedOn, &total); e != nil {
				return e
			}
			if closes != nil {
				q.ClosesOn = closes.Format("2006-01-02")
			}
			if quotedOn != nil {
				q.QuotedOn = quotedOn.Format("2006-01-02")
				q.QuoteTotal = total.StringFixed(2)
			}
			out = append(out, q)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}
