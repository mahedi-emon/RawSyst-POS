package stockops

// Reading back what happened to stock.
//
// Two audiences, and they want opposite things. A storeman wants to know what
// is on the shelf right now; an owner or an auditor wants to know why it
// changed. So there is a level view and a document view, and neither is derived
// from the other.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// readAdjustment reads one voucher inside a transaction that already holds it.
func (s *Service) readAdjustment(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Adjustment, error) {
	var a Adjustment
	var note, createdBy *string
	var postedAt *time.Time
	var createdAt time.Time

	err := tx.QueryRow(ctx, `
		SELECT adj.id, adj.adjustment_no, adj.kind, adj.reason, adj.note,
		       adj.status, w.name, c.base_currency,
		       u.full_name, adj.created_at, adj.posted_at
		FROM stock_adjustment adj
		JOIN warehouse w ON w.id = adj.warehouse_id
		JOIN company   c ON c.id = adj.company_id
		LEFT JOIN app_user u ON u.id = adj.created_by
		WHERE adj.id = $1 AND adj.company_id = $2`,
		id, scope.CompanyID).
		Scan(&a.ID, &a.Number, &a.Kind, &a.Reason, &note, &a.Status,
			&a.Location, &a.Currency, &createdBy, &createdAt, &postedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Adjustment{}, errs.New(errs.CodeNotFound,
			"That stock voucher is not this business's.")
	}
	if err != nil {
		return Adjustment{}, err
	}
	if note != nil {
		a.Note = *note
	}
	if createdBy != nil {
		a.CreatedBy = *createdBy
	}
	a.CreatedAt = createdAt.Format(time.RFC3339)
	if postedAt != nil {
		a.PostedAt = postedAt.Format(time.RFC3339)
	}

	rows, err := tx.Query(ctx, `
		SELECT l.variant_id, v.sku, p.name,
		       coalesce(l.system_qty_posted, l.system_qty_open),
		       l.counted_qty, l.delta, l.value,
		       l.system_qty_posted IS NOT NULL
		         AND l.system_qty_posted <> l.system_qty_open
		FROM stock_adjustment_line l
		JOIN variant v ON v.id = l.variant_id
		JOIN product p ON p.id = v.product_id
		WHERE l.adjustment_id = $1
		ORDER BY p.name, v.sku`, id)
	if err != nil {
		return Adjustment{}, err
	}
	defer rows.Close()

	total := decimal.Zero
	for rows.Next() {
		var l AdjustmentLine
		var system decimal.Decimal
		var counted, delta, value *decimal.Decimal
		if err := rows.Scan(&l.VariantID, &l.SKU, &l.Product,
			&system, &counted, &delta, &value, &l.MovedWhileCounting); err != nil {
			return Adjustment{}, err
		}
		l.SystemQty = system.String()
		if counted != nil {
			l.CountedQty = counted.String()
		}
		if delta != nil {
			l.Delta = delta.String()
		}
		if value != nil {
			l.Value = value.StringFixed(2)
			total = total.Add(*value)
		}
		a.Lines = append(a.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return Adjustment{}, err
	}
	a.Value = total.StringFixed(2)
	return a, nil
}

// Adjustment reads one voucher.
func (s *Service) Adjustment(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Adjustment, error) {
	var out Adjustment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		a, e := s.readAdjustment(ctx, tx, scope, id)
		out = a
		return e
	})
	return out, err
}

// AdjustmentFilter narrows the voucher list.
type AdjustmentFilter struct {
	Kind        string
	Status      string
	WarehouseID *uuid.UUID
	Limit       int
}

// Adjustments lists vouchers, newest first.
func (s *Service) Adjustments(
	ctx context.Context, scope Scope, f AdjustmentFilter,
) ([]Adjustment, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	out := []Adjustment{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT adj.id, adj.adjustment_no, adj.kind, adj.reason,
			       coalesce(adj.note, ''), adj.status, w.name, c.base_currency,
			       coalesce(u.full_name, ''), adj.created_at, adj.posted_at,
			       coalesce((SELECT sum(value) FROM stock_adjustment_line
			                 WHERE adjustment_id = adj.id), 0)
			FROM stock_adjustment adj
			JOIN warehouse w ON w.id = adj.warehouse_id
			JOIN company   c ON c.id = adj.company_id
			LEFT JOIN app_user u ON u.id = adj.created_by
			WHERE adj.company_id = $1
			  AND ($2 = '' OR adj.kind = $2)
			  AND ($3 = '' OR adj.status = $3)
			  AND ($4::uuid IS NULL OR adj.warehouse_id = $4)
			ORDER BY adj.created_at DESC
			LIMIT $5`,
			scope.CompanyID, f.Kind, f.Status, f.WarehouseID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var a Adjustment
			var createdAt time.Time
			var postedAt *time.Time
			var total decimal.Decimal
			if err := rows.Scan(&a.ID, &a.Number, &a.Kind, &a.Reason, &a.Note,
				&a.Status, &a.Location, &a.Currency, &a.CreatedBy,
				&createdAt, &postedAt, &total); err != nil {
				return err
			}
			a.CreatedAt = createdAt.Format(time.RFC3339)
			if postedAt != nil {
				a.PostedAt = postedAt.Format(time.RFC3339)
			}
			a.Value = total.StringFixed(2)
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// --- what is on the shelf -------------------------------------------------

// StockLine is one product's position at one location.
type StockLine struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`
	Barcode   string    `json:"barcode,omitempty"`

	Location string `json:"location"`
	OnHand   string `json:"on_hand"`

	// ReorderLevel is B4's minimum-stock alert, carried so the screen can mark
	// the line rather than the caller having to ask twice.
	ReorderLevel string `json:"reorder_level,omitempty"`
	BelowMinimum bool   `json:"below_minimum,omitempty"`
}

// StockFilter narrows the level view.
type StockFilter struct {
	WarehouseID *uuid.UUID
	Search      string
	OnlyLow     bool
	Limit       int
}

// OnHand reports what is where.
//
// In-transit is included when no location is named, and excluded when one is:
// an owner asking "what have I got" owns the stock on the lorry, while a
// storeman asking about the back room does not want a row for it.
func (s *Service) OnHand(
	ctx context.Context, scope Scope, f StockFilter,
) ([]StockLine, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	out := []StockLine{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT v.id, v.sku, p.name, coalesce(v.barcode, ''), w.name,
			       stock_on_hand(v.id, w.id),
			       v.reorder_level
			FROM variant v
			JOIN product p ON p.id = v.product_id
			JOIN warehouse w ON w.company_id = v.company_id AND w.is_active
			WHERE v.company_id = $1 AND v.is_active
			  AND ($2::uuid IS NULL OR w.id = $2)
			  AND ($3 = '' OR v.sku ILIKE '%' || $3 || '%'
			                OR p.name ILIKE '%' || $3 || '%'
			                OR coalesce(v.barcode, '') = $3)
			  AND (stock_on_hand(v.id, w.id) <> 0
			       OR w.kind <> 'transit')
			  AND (NOT $4 OR (v.reorder_level IS NOT NULL
			                  AND stock_on_hand(v.id, w.id) <= v.reorder_level))
			ORDER BY p.name, v.sku, w.code
			LIMIT $5`,
			scope.CompanyID, f.WarehouseID, f.Search, f.OnlyLow, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var l StockLine
			var onHand decimal.Decimal
			var reorder *decimal.Decimal
			if err := rows.Scan(&l.VariantID, &l.SKU, &l.Product, &l.Barcode,
				&l.Location, &onHand, &reorder); err != nil {
				return err
			}
			l.OnHand = onHand.String()
			if reorder != nil {
				l.ReorderLevel = reorder.String()
				l.BelowMinimum = onHand.LessThanOrEqual(*reorder)
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

// --- the movement ledger --------------------------------------------------

// Movement is one thing that happened to stock.
type Movement struct {
	At       string `json:"occurred_at"`
	Product  string `json:"product"`
	SKU      string `json:"sku"`
	Location string `json:"location"`
	Reason   string `json:"reason"`
	Delta    string `json:"delta"`
	Value    string `json:"value,omitempty"`
	Document string `json:"document,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Movements is B4's Stock-In / Stock-Out report: every movement, newest first,
// with what caused it.
func (s *Service) Movements(
	ctx context.Context, scope Scope, f StockFilter,
) ([]Movement, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	out := []Movement{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.occurred_at, p.name, v.sku, w.name, m.reason,
			       m.delta, m.value_delta,
			       coalesce(adj.adjustment_no, trf.transfer_no, ''),
			       coalesce(m.note, '')
			FROM stock_movement m
			JOIN variant   v ON v.id = m.variant_id
			JOIN product   p ON p.id = v.product_id
			JOIN warehouse w ON w.id = m.warehouse_id
			LEFT JOIN stock_adjustment adj
			       ON m.source_type = 'stock_adjustment' AND adj.id = m.source_id
			LEFT JOIN stock_transfer trf
			       ON m.source_type = 'stock_transfer' AND trf.id = m.source_id
			WHERE m.company_id = $1
			  AND ($2::uuid IS NULL OR m.warehouse_id = $2)
			  AND ($3 = '' OR v.sku ILIKE '%' || $3 || '%'
			                OR p.name ILIKE '%' || $3 || '%')
			ORDER BY m.occurred_at DESC
			LIMIT $4`,
			scope.CompanyID, f.WarehouseID, f.Search, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m Movement
			var at time.Time
			var delta decimal.Decimal
			var value *decimal.Decimal
			if err := rows.Scan(&at, &m.Product, &m.SKU, &m.Location, &m.Reason,
				&delta, &value, &m.Document, &m.Note); err != nil {
				return err
			}
			m.At = at.Format(time.RFC3339)
			m.Delta = delta.String()
			if value != nil {
				m.Value = value.StringFixed(2)
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}
