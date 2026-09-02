// Reading and recalling lots (blueprint B4).
//
// The lot layer itself lives in `inventory` because it is part of receiving and
// issuing stock. What a person does with it — look at what is in date, withdraw
// a bad lot, find out who bought from it — belongs here beside the other stock
// operations a shop performs.
package stockops

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Batch is one lot as a screen shows it.
type Batch struct {
	ID        uuid.UUID `json:"id"`
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`

	Warehouse   string `json:"warehouse"`
	WarehouseID string `json:"warehouse_id"`

	BatchNo        string `json:"batch_no"`
	ManufacturedOn string `json:"manufactured_on,omitempty"`
	ExpiresOn      string `json:"expires_on,omitempty"`

	QtyReceived  string `json:"qty_received"`
	QtyRemaining string `json:"qty_remaining"`
	UnitCost     string `json:"unit_cost,omitempty"`

	Supplier string `json:"supplier,omitempty"`

	// DaysLeft is negative once the lot is past its date. Computed here rather
	// than on the client so every reader agrees about what "today" is — a till
	// in a different timezone must not disagree with the back office about
	// whether a lot has expired.
	DaysLeft *int `json:"days_left,omitempty"`
	Expired  bool `json:"expired"`

	RecalledAt   string `json:"recalled_at,omitempty"`
	RecallReason string `json:"recall_reason,omitempty"`

	ReceivedAt string `json:"received_at"`
}

// BatchFilter narrows the list.
type BatchFilter struct {
	VariantID *uuid.UUID
	// ExpiringWithinDays lists only lots at or past that horizon. Zero means
	// no date filter.
	ExpiringWithinDays int
	// IncludeEmpty shows lots that have been fully issued. Off by default: a
	// shop asking what it holds does not mean what it once held.
	IncludeEmpty bool
}

const batchSelect = `
	SELECT b.id, b.variant_id, v.sku, coalesce(p.name, v.sku),
	       w.name, b.warehouse_id::text,
	       b.batch_no,
	       coalesce(to_char(b.manufactured_on, 'YYYY-MM-DD'), ''),
	       coalesce(to_char(b.expires_on, 'YYYY-MM-DD'), ''),
	       b.qty_received::text, b.qty_remaining::text,
	       coalesce(b.unit_cost::text, ''),
	       coalesce(s.name, ''),
	       CASE WHEN b.expires_on IS NULL THEN NULL
	            ELSE (b.expires_on - current_date)::int END,
	       coalesce(b.expires_on < current_date, false),
	       coalesce(to_char(b.recalled_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(b.recall_reason, ''),
	       to_char(b.received_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
	FROM stock_batch b
	JOIN variant v   ON v.id = b.variant_id
	JOIN warehouse w ON w.id = b.warehouse_id
	LEFT JOIN product p  ON p.id = v.product_id
	LEFT JOIN supplier s ON s.id = b.supplier_id`

func scanBatch(rows pgx.Rows) (Batch, error) {
	var b Batch
	err := rows.Scan(&b.ID, &b.VariantID, &b.SKU, &b.Product,
		&b.Warehouse, &b.WarehouseID, &b.BatchNo,
		&b.ManufacturedOn, &b.ExpiresOn,
		&b.QtyReceived, &b.QtyRemaining, &b.UnitCost, &b.Supplier,
		&b.DaysLeft, &b.Expired, &b.RecalledAt, &b.RecallReason, &b.ReceivedAt)
	return b, err
}

// Batches lists lots, soonest to expire first.
//
// Ordered by date rather than by name because the question a shop asks of this
// list is "what do I have to move", and undated lots are not part of that
// question — they sort last.
func (s *Service) Batches(
	ctx context.Context, scope Scope, f BatchFilter,
) ([]Batch, error) {
	out := []Batch{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, batchSelect+`
			WHERE b.company_id = $1
			  AND ($2::uuid IS NULL OR b.variant_id = $2::uuid)
			  AND ($3::int = 0 OR (b.expires_on IS NOT NULL
			       AND b.expires_on < current_date + ($3::int * INTERVAL '1 day')))
			  AND ($4::boolean OR b.qty_remaining > 0)
			ORDER BY b.expires_on NULLS LAST, b.batch_no
			LIMIT 500`,
			scope.CompanyID, f.VariantID, f.ExpiringWithinDays, f.IncludeEmpty)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			b, e := scanBatch(rows)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// RecallTrace is who received goods from a lot.
//
// The answer a recall needs. A lot number on a supplier's notice is worth
// nothing unless it can be turned into a list of people to telephone.
type RecallTrace struct {
	Batch Batch          `json:"batch"`
	Sales []RecalledSale `json:"sales"`
	// StillOnHand is what never left, so a shop knows what to pull off the
	// shelf as well as who to call.
	StillOnHand string `json:"still_on_hand"`
}

// RecalledSale is one document that took stock from the lot.
type RecalledSale struct {
	InvoiceID     string `json:"invoice_id"`
	InvoiceNumber string `json:"invoice_no,omitempty"`
	IssuedAt      string `json:"issued_at"`
	Qty           string `json:"qty"`
	CustomerID    string `json:"customer_id,omitempty"`
	Customer      string `json:"customer,omitempty"`
	Phone         string `json:"phone,omitempty"`
}

// Recall withdraws a lot from sale and returns who bought from it.
//
// # Withdrawn, not deleted
//
// The history of what was already sold from the lot is the entire reason to
// have recorded it. `recalled_at` stops the FEFO allocator choosing it — see
// inventory/batch.go — while everything it did remains readable.
//
// # A reason is required
//
// The database enforces it too. A recall with no stated cause cannot be
// explained to the customer being telephoned, and "we are recalling this" is
// not something a shop should be able to record without saying why.
func (s *Service) Recall(
	ctx context.Context, scope Scope, batchID uuid.UUID, reason string,
) (RecallTrace, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return RecallTrace{}, errs.Validation(
			"Say why this batch is being recalled.").
			WithField("reason",
				"The customers you telephone will ask, and it goes on the record.")
	}

	var out RecallTrace
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE stock_batch
			SET recalled_at = coalesce(recalled_at, now()),
			    recall_reason = $3
			WHERE id = $1 AND company_id = $2`,
			batchID, scope.CompanyID, reason)
		if e != nil {
			return db.Translate(e, "That batch could not be recalled.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That batch was not found.")
		}

		rows, e := tx.Query(ctx, batchSelect+`
			WHERE b.id = $1 AND b.company_id = $2`, batchID, scope.CompanyID)
		if e != nil {
			return e
		}
		if rows.Next() {
			b, se := scanBatch(rows)
			if se != nil {
				rows.Close()
				return se
			}
			out.Batch = b
			out.StillOnHand = b.QtyRemaining
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		// Who took it. `stock_batch_movement` records the split per movement,
		// so a sale that drew from two lots appears under both.
		sales, e := tx.Query(ctx, `
			SELECT m.source_id::text,
			       coalesce(i.human_number, ''),
			       to_char(i.issued_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
			       (-bm.qty)::text,
			       coalesce(i.customer_id::text, ''),
			       coalesce(c.name, ''), coalesce(c.phone, '')
			FROM stock_batch_movement bm
			JOIN stock_movement m ON m.id = bm.movement_id
			LEFT JOIN sales_invoice i ON i.id = m.source_id
			LEFT JOIN customer c      ON c.id = i.customer_id
			WHERE bm.batch_id = $1
			  AND bm.qty < 0
			  AND m.source_type = 'sales_invoice'
			ORDER BY i.issued_at DESC NULLS LAST`, batchID)
		if e != nil {
			return e
		}
		defer sales.Close()

		out.Sales = []RecalledSale{}
		for sales.Next() {
			var r RecalledSale
			if e := sales.Scan(&r.InvoiceID, &r.InvoiceNumber, &r.IssuedAt,
				&r.Qty, &r.CustomerID, &r.Customer, &r.Phone); e != nil {
				return e
			}
			out.Sales = append(out.Sales, r)
		}
		return sales.Err()
	})
	return out, err
}

// ExpiringSoon is the default horizon a screen opens on.
const ExpiringSoon = 30 * 24 * time.Hour
