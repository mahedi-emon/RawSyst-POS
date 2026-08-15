//go:build integration

// C13's hard invariant, mechanised:
//
//	"Inventory valuation report must always tie exactly to the Inventory
//	 account balance in the General Ledger — any divergence is flagged as an
//	 exception."
//
// This is the check that catches a costing engine and an accounting engine
// drifting apart. Each can be internally consistent and still disagree, and
// when they do the balance sheet carries stock that does not exist or misses
// stock that does.
package inventory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

type books struct {
	pool        *db.Pool
	tenantID    uuid.UUID
	companyID   uuid.UUID
	warehouseID uuid.UUID
	variantID   uuid.UUID
	periodID    uuid.UUID

	// The company's configured method, held here rather than passed to each
	// sale. The valuation function reads it from the company row, so a sale
	// costed by a different method than the company is configured for would
	// break the tie-out by construction — that is not a case worth being able
	// to express.
	method Method

	inventory, cogs, payable, revenue uuid.UUID
	entryNo                           int64
}

func newBooks(t *testing.T, method Method) *books {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	b := &books{pool: pool, entryNo: 1, method: method}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Tieout') RETURNING id`).
			Scan(&b.tenantID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO company
			  (tenant_id, legal_name, country, base_currency, costing_method)
			VALUES ($1,'Tieout Co','sa','SAR',$2) RETURNING id`,
			b.tenantID, string(method)).Scan(&b.companyID)
	})
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, b.tenantID)
			return e
		})
	})

	err = pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		var storeID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'MAIN','Main') RETURNING id`,
			b.tenantID, b.companyID).Scan(&storeID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,'WH1','Main Store') RETURNING id`,
			b.tenantID, b.companyID, storeID).Scan(&b.warehouseID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,8,'2026-08-01','2026-08-31') RETURNING id`,
			b.tenantID, b.companyID).Scan(&b.periodID); e != nil {
			return e
		}

		// The Inventory account is a CONTROL account — that is what makes the
		// tie-out query find it.
		if e := tx.QueryRow(ctx, `
			INSERT INTO account (tenant_id, company_id, code, name, type, is_control, control_of)
			VALUES ($1,$2,'1400','Inventory','asset',true,'inventory') RETURNING id`,
			b.tenantID, b.companyID).Scan(&b.inventory); e != nil {
			return e
		}
		for _, a := range []struct {
			code, name, kind string
			dst              *uuid.UUID
		}{
			{"5100", "Cost of Goods Sold", "expense", &b.cogs},
			{"2100", "Accounts Payable", "liability", &b.payable},
			{"4100", "Sales Revenue", "revenue", &b.revenue},
		} {
			if e := tx.QueryRow(ctx, `
				INSERT INTO account (tenant_id, company_id, code, name, type)
				VALUES ($1,$2,$3,$4,$5) RETURNING id`,
				b.tenantID, b.companyID, a.code, a.name, a.kind).Scan(a.dst); e != nil {
				return e
			}
		}

		var productID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'SKU1','Test Product','standard') RETURNING id`,
			b.tenantID, b.companyID).Scan(&productID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO variant (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,'SKU1-A',115.00) RETURNING id`,
			b.tenantID, b.companyID, productID).Scan(&b.variantID)
	})
	if err != nil {
		t.Fatalf("seed books: %v", err)
	}
	return b
}

// receive books a purchase: stock in, a cost layer, and Dr Inventory / Cr AP.
func (b *books) receive(t *testing.T, qty, unitCost string) {
	t.Helper()
	ctx := context.Background()
	value := dec(qty).Mul(dec(unitCost))
	entryNo := b.entryNo
	b.entryNo++

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,$5,'grn',$6)`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID, dec(qty), value); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO cost_layer
			  (tenant_id, company_id, variant_id, warehouse_id,
			   qty_received, qty_remaining, unit_cost)
			VALUES ($1,$2,$3,$4,$5,$5,$6)`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID,
			dec(qty), dec(unitCost)); e != nil {
			return e
		}

		// Both stores are kept current on every receipt, whichever method is in
		// force. A company that switches from FIFO to weighted average must not
		// discover its new valuation is empty, and back-filling one from the
		// other after the fact cannot recover costs that layers no longer hold.
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_valuation
			  (tenant_id, company_id, variant_id, warehouse_id, qty_on_hand, total_value)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (variant_id, warehouse_id) DO UPDATE SET
			  qty_on_hand = stock_valuation.qty_on_hand + EXCLUDED.qty_on_hand,
			  total_value = stock_valuation.total_value + EXCLUDED.total_value`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID,
			dec(qty), value); e != nil {
			return e
		}

		var entryID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,$4,'2026-08-15','purchase') RETURNING id`,
			b.tenantID, b.companyID, b.periodID, entryNo).Scan(&entryID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'SAR',$4,0,$4,0)`,
			b.tenantID, entryID, b.inventory, value); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,2,$3,'SAR',0,$4,0,$4)`,
			b.tenantID, entryID, b.payable, value)
		return e
	})
	if err != nil {
		t.Fatalf("receive %s at %s: %v", qty, unitCost, err)
	}
}

// openLayers reads the layers oldest-first, exactly as the costing engine
// expects them.
func (b *books) openLayers(t *testing.T) []Layer {
	t.Helper()
	var out []Layer
	err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(context.Background(), `
			SELECT id, qty_remaining, unit_cost FROM cost_layer
			WHERE variant_id=$1 AND warehouse_id=$2 AND qty_remaining > 0
			ORDER BY received_at, id`, b.variantID, b.warehouseID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var l Layer
			if e := rows.Scan(&l.ID, &l.QtyRemaining, &l.UnitCost); e != nil {
				return e
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read layers: %v", err)
	}
	return out
}

// currentPool reads the weighted-average position.
func (b *books) currentPool(t *testing.T) Pool {
	t.Helper()
	var p Pool
	err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(context.Background(), `
			SELECT qty_on_hand, total_value FROM stock_valuation
			WHERE variant_id=$1 AND warehouse_id=$2`,
			b.variantID, b.warehouseID).Scan(&p.QtyOnHand, &p.TotalValue)
		if e == pgx.ErrNoRows {
			return nil // never received: an empty pool, not an error
		}
		return e
	})
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	return p
}

// sell consumes stock by the company's method and posts COGS.
func (b *books) sell(t *testing.T, qty string) CostResult {
	t.Helper()
	ctx := context.Background()

	result, err := Consume(Request{
		Method: b.method,
		Qty:    dec(qty),
		Layers: b.openLayers(t),
		Pool:   b.currentPool(t),
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}

	entryNo := b.entryNo
	b.entryNo++

	err = b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		// Draw the layers down by exactly what the engine said it took.
		for _, c := range result.Consumed {
			if _, e := tx.Exec(ctx,
				`UPDATE cost_layer SET qty_remaining = qty_remaining - $2 WHERE id = $1`,
				c.LayerID, c.Qty); e != nil {
				return e
			}
		}

		// Write the pool back as the engine computed it, rather than
		// subtracting here. Recomputing the remaining value at this point would
		// round a second time, and that second rounding is exactly the drift
		// this test exists to catch.
		if b.method == MethodWAC {
			if _, e := tx.Exec(ctx, `
				UPDATE stock_valuation SET qty_on_hand = $3, total_value = $4
				WHERE variant_id = $1 AND warehouse_id = $2`,
				b.variantID, b.warehouseID,
				result.PoolQtyAfter, result.PoolValueAfter); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,$5,'sale',$6)`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID,
			dec(qty).Neg(), result.TotalCost.Neg()); e != nil {
			return e
		}

		// Rule 2: COGS posts simultaneously with the sale.
		var entryID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,$4,'2026-08-15','sale') RETURNING id`,
			b.tenantID, b.companyID, b.periodID, entryNo).Scan(&entryID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'SAR',$4,0,$4,0)`,
			b.tenantID, entryID, b.cogs, result.TotalCost); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,2,$3,'SAR',0,$4,0,$4)`,
			b.tenantID, entryID, b.inventory, result.TotalCost)
		return e
	})
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}
	return result
}

func (b *books) tieOutDifference(t *testing.T) decimal.Decimal {
	t.Helper()
	var diff decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT inventory_gl_difference($1)`, b.companyID).Scan(&diff)
	}); err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	return diff
}

// The invariant, under each costing method: after receipts and sales, the
// valuation equals the Inventory control account exactly.
func TestValuationTiesToTheLedger(t *testing.T) {
	for _, method := range []Method{MethodFIFO, MethodWAC} {
		t.Run(string(method), func(t *testing.T) {
			b := newBooks(t, method)

			b.receive(t, "10", "50.00")
			b.receive(t, "10", "60.00")

			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("after two receipts the valuation is out by %s", diff)
			}

			b.sell(t, "15")

			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("after selling 15 under %s the valuation is out by %s; "+
					"the balance sheet and the stock report disagree", method, diff)
			}
		})
	}
}

// Awkward costs are the realistic case, and where a naive implementation
// drifts. Three receipts at prices that do not divide evenly, then repeated
// partial sales.
func TestTieOutHoldsThroughAwkwardArithmetic(t *testing.T) {
	b := newBooks(t, MethodWAC)

	b.receive(t, "3", "33.3333")
	b.receive(t, "7", "16.6667")
	b.receive(t, "5", "99.9999")

	for i := 0; i < 5; i++ {
		b.sell(t, "2")
		if diff := b.tieOutDifference(t); !diff.IsZero() {
			t.Fatalf("after sale %d the valuation is out by %s", i+1, diff)
		}
	}
}

// Selling everything must leave both at zero. Stock that no longer exists
// cannot keep a value on the balance sheet — and a residue left here is
// permanent, because there is nothing left to sell that would ever clear it.
//
// The receipts are chosen so the weighted average recurs: 10 units holding
// 100 averages 33.3333, and charging that out three times would leave a
// hallala behind.
func TestSellingAllStockLeavesNothingOnEitherSide(t *testing.T) {
	for _, method := range []Method{MethodFIFO, MethodWAC} {
		t.Run(string(method), func(t *testing.T) {
			b := newBooks(t, method)

			b.receive(t, "4", "25.00")
			b.receive(t, "6", "35.00")

			// Piecemeal, not in one go: each sale must round on its own and the
			// last one must still land exactly on zero.
			b.sell(t, "3")
			b.sell(t, "3")
			b.sell(t, "4")

			var valuation decimal.Decimal
			if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(context.Background(),
					`SELECT inventory_valuation($1)`, b.companyID).Scan(&valuation)
			}); err != nil {
				t.Fatalf("valuation: %v", err)
			}
			if !valuation.IsZero() {
				t.Fatalf("valuation = %s after selling everything under %s; that "+
					"value can never be cleared, because there is no stock left "+
					"to sell", valuation, method)
			}
			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("tie-out is out by %s after selling everything", diff)
			}
		})
	}
}

// Stock is the sum of its movements, and a return puts it back.
func TestStockIsTheSumOfItsMovements(t *testing.T) {
	b := newBooks(t, MethodFIFO)
	ctx := context.Background()

	b.receive(t, "10", "50.00")
	b.sell(t, "4")

	var onHand decimal.Decimal
	read := func() decimal.Decimal {
		if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
				b.variantID, b.warehouseID).Scan(&onHand)
		}); err != nil {
			t.Fatalf("stock_on_hand: %v", err)
		}
		return onHand
	}

	if got := read(); !got.Equal(dec("6")) {
		t.Fatalf("on hand = %s, want 6", got)
	}

	// A customer returns one.
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,1,'return',50.00)`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("return movement: %v", err)
	}

	if got := read(); !got.Equal(dec("7")) {
		t.Fatalf("on hand = %s after a return, want 7", got)
	}
}

// A movement is a fact. Correcting one means an opposite movement, never an
// edit — the same rule as a journal entry.
func TestStockMovementsAreImmutable(t *testing.T) {
	b := newBooks(t, MethodFIFO)
	ctx := context.Background()
	b.receive(t, "5", "50.00")

	for _, stmt := range []string{
		`UPDATE stock_movement SET delta = 999 WHERE company_id = $1`,
		`DELETE FROM stock_movement WHERE company_id = $1`,
	} {
		err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, stmt, b.companyID)
			return e
		})
		if err == nil {
			t.Errorf("succeeded but should not have: %s", stmt)
		}
	}
}

// Wastage and adjustments demand an explanation, because unexplained shrinkage
// is exactly what gets buried (blueprint B4).
func TestWastageAndAdjustmentRequireAReason(t *testing.T) {
	b := newBooks(t, MethodFIFO)
	ctx := context.Background()
	b.receive(t, "10", "50.00")

	for _, reason := range []string{"wastage", "adjustment"} {
		err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `
				INSERT INTO stock_movement
				  (tenant_id, company_id, variant_id, warehouse_id, delta, reason)
				VALUES ($1,$2,$3,$4,-1,$5)`,
				b.tenantID, b.companyID, b.variantID, b.warehouseID, reason)
			return e
		})
		if err == nil {
			t.Errorf("a %s movement was recorded with no explanation", reason)
		}
	}

	// With a note it goes through.
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, note)
			VALUES ($1,$2,$3,$4,-1,'wastage','water damage in the stockroom')`,
			b.tenantID, b.companyID, b.variantID, b.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("an explained wastage movement was refused: %v", err)
	}
}

// A cost layer's receipt facts are frozen; only what remains of it moves.
func TestCostLayerReceiptIsFrozen(t *testing.T) {
	b := newBooks(t, MethodFIFO)
	ctx := context.Background()
	b.receive(t, "10", "50.00")

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE cost_layer SET unit_cost = 1 WHERE company_id = $1`, b.companyID)
		return e
	})
	if err == nil {
		t.Fatal("a cost layer's unit cost was rewritten after the fact")
	}

	// Drawing it down is the one thing that may change.
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE cost_layer SET qty_remaining = qty_remaining - 1 WHERE company_id = $1`,
			b.companyID)
		return e
	}); err != nil {
		t.Fatalf("consuming a layer was refused: %v", err)
	}
}
