// Package insight is global search (blueprint D7) and business analytics (D2).
//
// # One box, and it only finds what the caller could already open
//
// D7: "one search box finds anything the logged-in user is authorized to see".
// The permission is checked per BRANCH of the search, not once for the whole
// thing: a cashier holds `catalog.view` and not `people.view`, so their search
// for "Omar" returns the product and not the employee — and it returns the
// product rather than refusing the whole query because one branch was closed.
//
// That is why Search takes the caller's grants rather than a permission being
// declared on the route. A single permission would either be too narrow to find
// anything or wide enough to leak the staff list to the till.
//
// # Ranked, because a search box that returns forty rows has answered nothing
//
// An exact match on a code — a SKU, a barcode, an invoice number, a serial —
// outranks everything, because somebody who types one is holding the thing.
// Then a name that starts with the term, then one that merely contains it.
package insight

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Service answers search and analytics questions.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Hit is one thing the search found.
type Hit struct {
	// Kind is what it is: product, customer, supplier, invoice, order,
	// employee, serial. The screen uses it to decide where tapping goes.
	Kind string `json:"kind"`
	ID   string `json:"id"`

	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	// Amount and its currency, when the thing has one. An invoice without its
	// total is an invoice somebody has to open to recognise.
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`

	// rank orders the results and is not sent: a screen that could see it
	// would start second-guessing the order.
	rank int
}

// branch is one thing the search knows how to look for.
type branch struct {
	kind       string
	permission string
	sql        string
}

// The branches, in the order D7 lists them.
//
// Each returns (id, label, detail, amount, currency, rank) and takes the
// company and the term. Written out rather than generated, because each one
// knows what identifies its own subject: an invoice is found by its number, a
// serial by the serial itself, and a product by three different things.
var branches = []branch{
	{
		kind: "product", permission: "catalog.view",
		sql: `
			SELECT v.id::text, p.name,
			       v.sku || CASE WHEN v.barcode IS NULL THEN ''
			                     ELSE ' · ' || v.barcode END,
			       v.price_retail::text, co.base_currency,
			       CASE WHEN upper(v.sku) = upper($2)
			                 OR upper(coalesce(v.barcode, '')) = upper($2) THEN 0
			            WHEN p.name ILIKE $2 || '%' THEN 2
			            ELSE 3 END
			FROM variant v
			JOIN product p ON p.id = v.product_id
			JOIN company co ON co.id = v.company_id
			WHERE v.company_id = $1 AND v.is_active
			  AND (v.sku ILIKE '%' || $2 || '%'
			       OR v.barcode ILIKE '%' || $2 || '%'
			       OR p.name ILIKE '%' || $2 || '%'
			       OR p.translations->>'ar' ILIKE '%' || $2 || '%')
			LIMIT 8`,
	},
	{
		kind: "customer", permission: "customers.view",
		sql: `
			SELECT c.id::text, c.name,
			       c.code || CASE WHEN c.phone IS NULL THEN ''
			                      ELSE ' · ' || c.phone END,
			       '', '',
			       CASE WHEN upper(c.code) = upper($2) OR c.phone = $2 THEN 0
			            WHEN c.name ILIKE $2 || '%' THEN 2
			            ELSE 3 END
			FROM customer c
			WHERE c.company_id = $1 AND c.is_active
			  AND (c.name ILIKE '%' || $2 || '%'
			       OR c.code ILIKE '%' || $2 || '%'
			       OR c.phone ILIKE '%' || $2 || '%'
			       OR c.name_ar ILIKE '%' || $2 || '%')
			LIMIT 6`,
	},
	{
		kind: "supplier", permission: "purchasing.view",
		sql: `
			SELECT s.id::text, s.legal_name,
			       s.code || CASE WHEN s.phone IS NULL THEN ''
			                      ELSE ' · ' || s.phone END,
			       '', '',
			       CASE WHEN upper(s.code) = upper($2) THEN 0
			            WHEN s.legal_name ILIKE $2 || '%' THEN 2
			            ELSE 3 END
			FROM supplier s
			WHERE s.company_id = $1 AND s.is_active
			  AND (s.legal_name ILIKE '%' || $2 || '%'
			       OR s.code ILIKE '%' || $2 || '%'
			       OR s.phone ILIKE '%' || $2 || '%')
			LIMIT 6`,
	},
	{
		kind: "invoice", permission: "sales.view",
		sql: `
			SELECT i.id::text, coalesce(i.human_number, i.uuid::text),
			       to_char(i.issued_at, 'YYYY-MM-DD')
			         || coalesce(' · ' || c.name, ''),
			       i.total_inclusive::text, i.currency,
			       CASE WHEN upper(coalesce(i.human_number, '')) = upper($2)
			            THEN 0 ELSE 3 END
			FROM sales_invoice i
			LEFT JOIN customer c ON c.id = i.customer_id
			WHERE i.company_id = $1
			  AND (i.human_number ILIKE '%' || $2 || '%'
			       OR i.uuid::text ILIKE $2 || '%')
			ORDER BY i.issued_at DESC
			LIMIT 6`,
	},
	{
		kind: "order", permission: "order.view",
		sql: `
			SELECT o.id::text, o.order_no,
			       o.state || coalesce(' · ' || c.name, ''),
			       coalesce((SELECT sum(l.qty * l.unit_price - l.discount)
			                 FROM sales_order_line l
			                 WHERE l.order_id = o.id), 0)::text, o.currency,
			       CASE WHEN upper(o.order_no) = upper($2) THEN 0 ELSE 3 END
			FROM sales_order o
			LEFT JOIN customer c ON c.id = o.customer_id
			WHERE o.company_id = $1 AND o.order_no ILIKE '%' || $2 || '%'
			ORDER BY o.created_at DESC
			LIMIT 6`,
	},
	{
		kind: "employee", permission: "hr.view",
		sql: `
			SELECT e.id::text, e.full_name,
			       e.employee_no || coalesce(' · ' || e.position, ''),
			       '', '',
			       CASE WHEN upper(e.employee_no) = upper($2) THEN 0
			            WHEN e.full_name ILIKE $2 || '%' THEN 2
			            ELSE 3 END
			FROM employee e
			WHERE e.company_id = $1 AND e.left_on IS NULL
			  AND (e.full_name ILIKE '%' || $2 || '%'
			       OR e.employee_no ILIKE '%' || $2 || '%'
			       OR e.phone ILIKE '%' || $2 || '%')
			LIMIT 6`,
	},
	{
		kind: "serial", permission: "serial.view",
		sql: `
			SELECT s.id::text, s.serial_no,
			       s.status || coalesce(' · ' || p.name, ''),
			       '', '',
			       CASE WHEN upper(s.serial_no) = upper($2) THEN 0 ELSE 3 END
			FROM stock_serial s
			LEFT JOIN variant v ON v.id = s.variant_id
			LEFT JOIN product p ON p.id = v.product_id
			WHERE s.company_id = $1 AND s.serial_no ILIKE '%' || $2 || '%'
			LIMIT 6`,
	},
}

// Search answers D7's one box.
//
// `held` is what the caller can do. A branch whose permission is not held is
// not run at all — it is not run and its results filtered, which would still
// have read the rows.
func (s *Service) Search(
	ctx context.Context, scope Scope, term string, held func(string) bool,
) ([]Hit, error) {
	term = strings.TrimSpace(term)
	if len(term) < 2 {
		// One character matches most of a catalogue. Returning nothing is
		// honest and instant; returning four hundred rows is neither.
		return []Hit{}, nil
	}

	out := []Hit{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		for _, b := range branches {
			if !held(b.permission) {
				continue
			}
			rows, e := tx.Query(ctx, b.sql, scope.CompanyID, term)
			if e != nil {
				return e
			}
			for rows.Next() {
				h := Hit{Kind: b.kind}
				if e := rows.Scan(&h.ID, &h.Label, &h.Detail, &h.Amount,
					&h.Currency, &h.rank); e != nil {
					rows.Close()
					return e
				}
				out = append(out, h)
			}
			rows.Close()
			if e := rows.Err(); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}

	// Exact code matches first, then names that start with the term. Stable,
	// so two hits of the same rank keep the order their branch produced rather
	// than reshuffling between identical searches.
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank < out[j].rank })
	if len(out) > 30 {
		out = out[:30]
	}
	return out, nil
}
