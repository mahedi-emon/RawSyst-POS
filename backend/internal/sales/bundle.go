// Selling a combo package (blueprint B1).
//
// A bundle is one sellable SKU that holds no stock of its own. Selling "Suit +
// Shirt + Tie" leaves one fewer suit, one fewer shirt and one fewer tie, and
// the bundle's cost of sale is the sum of what those three cost when they left.
//
// # Why the components are costed rather than the bundle
//
// A bundle with its own stock level would be a fourth number nobody maintains,
// wrong the first time somebody sold a shirt on its own. So the bundle is never
// received, never counted and never valued: `inventory.Consume` runs once per
// COMPONENT, which is also what keeps C13's tie-out holding — the components'
// consumption is what moves the Inventory account, and the bundle contributes
// nothing of its own to reconcile.
package sales

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// bundleComponent is one item inside a combo, and how many of it.
type bundleComponent struct {
	VariantID uuid.UUID
	Qty       decimal.Decimal
	SKU       string
}

// componentsOf returns what a bundle contains, or nil if it is not a bundle.
//
// One query answering both questions, because "is this a bundle" and "what is
// in it" are asked together at every call site and asking separately would let
// them disagree — a variant flagged as a bundle with no components would
// otherwise sell as an ordinary item and deduct nothing at all.
func componentsOf(
	ctx context.Context, tx pgx.Tx, variantID uuid.UUID,
) ([]bundleComponent, error) {
	var isBundle bool
	if err := tx.QueryRow(ctx,
		`SELECT is_bundle FROM variant WHERE id = $1`, variantID).
		Scan(&isBundle); err != nil {
		if err == pgx.ErrNoRows {
			return nil, errs.New(errs.CodeNotFound, "That product was not found.")
		}
		return nil, err
	}
	if !isBundle {
		return nil, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT c.component_variant_id, c.qty, v.sku
		FROM bundle_component c
		JOIN variant v ON v.id = c.component_variant_id
		WHERE c.bundle_variant_id = $1
		ORDER BY v.sku`, variantID)
	if err != nil {
		return nil, db.Translate(err, "")
	}
	defer rows.Close()

	var out []bundleComponent
	for rows.Next() {
		var c bundleComponent
		if e := rows.Scan(&c.VariantID, &c.Qty, &c.SKU); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}

	if len(out) == 0 {
		// Refused rather than sold as an empty package. A bundle with nothing
		// in it takes no stock and costs nothing, so it would report pure
		// margin on every sale — a figure that looks like a very good day.
		return nil, errs.New(errs.CodeConflict,
			"This combo has nothing in it yet, so it cannot be sold. Add its "+
				"items first.")
	}
	return out, nil
}
