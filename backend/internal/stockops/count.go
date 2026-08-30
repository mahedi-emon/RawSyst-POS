package stockops

// The physical count.
//
// B4: "compares System Quantity vs Physical Quantity, auto-generates a signed
// Adjustment Voucher (with reason, user, approval, timestamp) for any variance."
//
// # Why it is three steps and not one
//
// Counting a shop takes hours. The sheet is opened in the morning, filled in
// down the aisles, and posted when somebody has checked the odd-looking lines.
// A single call taking a finished count would mean either holding a transaction
// open for an afternoon or losing the work when a browser closed.
//
// # Why the counter is not shown the expected figure
//
// `OpenCount` records the system quantity and does not return it. A person told
// that the system expects fourteen counts to fourteen — not dishonestly, but
// because the eye finds what it is looking for, and a count that confirms the
// records is worth nothing. The figure is there when the sheet is posted, which
// is when it becomes information rather than a hint.
//
// # Stock moves while you count
//
// The till keeps selling. So each line records the system quantity twice: what
// it was when the sheet was opened, and what it was at the instant of posting.
// The variance is measured against the SECOND, because the counted figure is
// the truth about the shelf and the shelf has since been sold from — measuring
// against the opening figure would silently absorb every sale made during the
// count into the variance and blame the counter for them.
//
// Where the two differ the line is flagged. That is B4's "discrepancies are
// auto-flagged", pointed at the discrepancy that is not one.

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

// CountScope is which products a count covers. B4 offers full, category, brand,
// location and spot counts; the first and the last are the two that differ
// structurally, and the middle three are the same query with a filter.
type CountScope struct {
	WarehouseID uuid.UUID

	// CategoryID narrows a full count to one part of the catalogue.
	CategoryID *uuid.UUID

	// VariantIDs is a spot count: these products and no others. Takes
	// precedence over CategoryID, because a person who has named the products
	// has already decided the scope.
	VariantIDs []uuid.UUID

	Note string
}

// OpenCount starts a count sheet.
func (s *Service) OpenCount(
	ctx context.Context, scope Scope, in CountScope,
) (Adjustment, error) {
	var out Adjustment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if _, e := locationForWrite(ctx, tx, scope.CompanyID, in.WarehouseID); e != nil {
			return e
		}

		// One open count per location. Two sheets against the same shelves
		// would each post a variance measured against a system figure the other
		// had already changed, and the second would undo the first.
		var open string
		e := tx.QueryRow(ctx, `
			SELECT adjustment_no FROM stock_adjustment
			WHERE company_id = $1 AND warehouse_id = $2
			  AND kind = 'count' AND status = 'draft'
			LIMIT 1`, scope.CompanyID, in.WarehouseID).Scan(&open)
		if e == nil {
			return errs.Newf(errs.CodeConflict,
				"Count %s is already open for that location. Post it or cancel "+
					"it before starting another.", open)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		number, e := claimAdjustmentNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		id := uuid.New()
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_adjustment
			  (id, tenant_id, company_id, warehouse_id, adjustment_no,
			   kind, reason, note, status, created_by)
			VALUES ($1,$2,$3,$4,$5,'count','count',$6,'draft',$7)`,
			id, scope.TenantID, scope.CompanyID, in.WarehouseID, number,
			nullIfBlank(in.Note), scope.UserID); e != nil {
			return db.Translate(e, "That count could not be opened.")
		}

		// The sheet, with the system quantity frozen onto every line.
		//
		// Products with nothing on hand are included deliberately. A count that
		// only listed what the system believes it has can never find the box of
		// stock nobody ever keyed in, which is one of the two things a count
		// exists to catch.
		var rows pgx.Rows
		switch {
		case len(in.VariantIDs) > 0:
			rows, e = tx.Query(ctx, `
				SELECT v.id FROM variant v
				WHERE v.company_id = $1 AND v.id = ANY($2) AND v.is_active`,
				scope.CompanyID, in.VariantIDs)
		case in.CategoryID != nil:
			rows, e = tx.Query(ctx, `
				SELECT v.id FROM variant v
				JOIN product p ON p.id = v.product_id
				WHERE v.company_id = $1 AND p.category_id = $2 AND v.is_active`,
				scope.CompanyID, *in.CategoryID)
		default:
			rows, e = tx.Query(ctx, `
				SELECT v.id FROM variant v
				WHERE v.company_id = $1 AND v.is_active`, scope.CompanyID)
		}
		if e != nil {
			return e
		}
		var variants []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			variants = append(variants, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		if len(variants) == 0 {
			return errs.New(errs.CodeInvalidInput,
				"There is nothing to count. Add products to the catalogue first.")
		}

		for _, v := range variants {
			onHand, e := inventory.OnHandAt(ctx, tx, v, in.WarehouseID)
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO stock_adjustment_line
				  (tenant_id, adjustment_id, variant_id, system_qty_open)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, id, v, onHand); e != nil {
				return e
			}
		}

		read, e := s.readAdjustment(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		out = read
		return nil
	})
	return out, err
}

// CountedLine is one shelf, counted.
type CountedLine struct {
	VariantID uuid.UUID
	Qty       decimal.Decimal
}

// SaveCount records what was counted. Called as often as the sheet is saved.
func (s *Service) SaveCount(
	ctx context.Context, scope Scope, id uuid.UUID, lines []CountedLine,
) error {
	for _, l := range lines {
		if l.Qty.IsNegative() {
			return errs.New(errs.CodeInvalidInput,
				"A shelf cannot hold less than nothing. Enter the number of "+
					"units you found, or zero.")
		}
	}
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if _, e := draftCount(ctx, tx, scope, id); e != nil {
			return e
		}
		for _, l := range lines {
			tag, e := tx.Exec(ctx, `
				UPDATE stock_adjustment_line SET counted_qty = $3
				WHERE adjustment_id = $1 AND variant_id = $2`,
				id, l.VariantID, l.Qty)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"One of those products is not on this count sheet.")
			}
		}
		return nil
	})
}

// PostCount turns a filled-in sheet into movements and a journal entry.
//
// Uncounted lines are left alone rather than treated as zero. A sheet where
// somebody counted three aisles and went home says nothing about the fourth,
// and reading silence as "none" would write off the entire aisle.
func (s *Service) PostCount(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Adjustment, error) {
	var out Adjustment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		warehouseID, e := draftCount(ctx, tx, scope, id)
		if e != nil {
			return e
		}

		var counted int
		if e := tx.QueryRow(ctx, `
			SELECT count(*) FROM stock_adjustment_line
			WHERE adjustment_id = $1 AND counted_qty IS NOT NULL`, id).
			Scan(&counted); e != nil {
			return e
		}
		if counted == 0 {
			return errs.New(errs.CodeConflict,
				"Nothing on this sheet has been counted yet.")
		}

		// The second reading, and the one the variance is measured against.
		// Taken here, inside the posting transaction, so nothing can move
		// between the measurement and the movement.
		rows, e := tx.Query(ctx, `
			SELECT variant_id FROM stock_adjustment_line
			WHERE adjustment_id = $1 AND counted_qty IS NOT NULL
			ORDER BY variant_id`, id)
		if e != nil {
			return e
		}
		var variants []uuid.UUID
		for rows.Next() {
			var v uuid.UUID
			if e := rows.Scan(&v); e != nil {
				rows.Close()
				return e
			}
			variants = append(variants, v)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		if e := inventory.LockStock(ctx, tx, warehouseID, variants); e != nil {
			return e
		}

		for _, v := range variants {
			onHand, e := inventory.OnHandAt(ctx, tx, v, warehouseID)
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				UPDATE stock_adjustment_line
				SET system_qty_posted = $3, delta = counted_qty - $3
				WHERE adjustment_id = $1 AND variant_id = $2`,
				id, v, onHand); e != nil {
				return e
			}
		}

		// The stock moves, the journal posts, and only then is the sheet
		// frozen. Freezing it first would make the trigger in 0079 refuse the
		// UPDATE that attaches the journal entry to it.
		now := time.Now().UTC()
		if e := s.applyAndPost(ctx, tx, scope, id, warehouseID, KindCount, now); e != nil {
			return e
		}
		if e := markPosted(ctx, tx, id, scope.UserID, now); e != nil {
			return db.Translate(e, "That count could not be posted.")
		}

		read, e := s.readAdjustment(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		out = read
		return nil
	})
	return out, err
}

// CancelCount abandons a sheet. Only a draft: a posted voucher is frozen by a
// trigger, and the way to undo one is another voucher the other way.
func (s *Service) CancelCount(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if _, e := draftCount(ctx, tx, scope, id); e != nil {
			return e
		}
		_, e := tx.Exec(ctx,
			`UPDATE stock_adjustment SET status = 'cancelled' WHERE id = $1`, id)
		return e
	})
}

// draftCount reads an open count sheet this company owns, and returns the
// location it is counting.
func draftCount(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (uuid.UUID, error) {
	var warehouseID uuid.UUID
	var status, kind, number string
	err := tx.QueryRow(ctx, `
		SELECT warehouse_id, status, kind, adjustment_no
		FROM stock_adjustment WHERE id = $1 AND company_id = $2`,
		id, scope.CompanyID).Scan(&warehouseID, &status, &kind, &number)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errs.New(errs.CodeNotFound,
			"That count is not this business's.")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if kind != KindCount {
		return uuid.Nil, errs.Newf(errs.CodeConflict,
			"%s is a stock %s, not a count.", number, kind)
	}
	switch status {
	case "draft":
		return warehouseID, nil
	case "posted":
		return uuid.Nil, errs.Newf(errs.CodeConflict,
			"%s has already been posted. Raise an adjustment to correct it.", number)
	default:
		return uuid.Nil, errs.Newf(errs.CodeConflict,
			"%s was cancelled.", number)
	}
}

// countReasons is the reason vocabulary a screen offers, per kind.
func Reasons(kind string) []string {
	out := make([]string, 0, len(reasonsByKind[kind]))
	for r := range reasonsByKind[kind] {
		out = append(out, r)
	}
	return sorted(out)
}
