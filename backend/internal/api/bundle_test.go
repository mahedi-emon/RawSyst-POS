//go:build integration

// Combo packages (blueprint B1).
//
// "Product Bundles / Combo Packages: sell multiple SKUs as one package (e.g.
// 'Suit + Shirt + Tie Combo') with automatic proportional stock deduction of
// each component on sale."
//
// A `bundle_price` PROMOTION (0084) is a different thing: it prices several
// items together while each line stays its own product. A bundle is ONE
// sellable SKU that holds no stock of its own and takes what it needs from the
// things inside it.
package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
)

// newVariant adds a plain sellable variant with stock, and returns its id.
func newVariant(t *testing.T, h *harness, f *shopFixture, sku string, qty, cost string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		var productID uuid.UUID
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,$3,$4,'standard') RETURNING id`,
			f.tenantID, f.companyID, "P-"+sku, "Product "+sku).Scan(&productID); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,$4,100) RETURNING id`,
			f.tenantID, f.companyID, productID, sku).Scan(&id); e != nil {
			return e
		}
		return nil
	}); err != nil {
		t.Fatalf("create variant %s: %v", sku, err)
	}

	if qty != "" {
		if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			_, e := inventory.Receive(t.Context(), tx, inventory.Receipt{
				TenantID: f.tenantID, CompanyID: f.companyID,
				VariantID: id, WarehouseID: f.warehouseID,
				Qty:      decimal.RequireFromString(qty),
				UnitCost: decimal.RequireFromString(cost),
				Reason:   "grn",
			})
			return e
		}); err != nil {
			t.Fatalf("stock %s: %v", sku, err)
		}
	}
	return id
}

// makeBundle flags a variant as a combo and puts components in it.
func makeBundle(
	t *testing.T, h *harness, f *shopFixture, bundleID uuid.UUID,
	components map[uuid.UUID]string,
) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(),
			`UPDATE variant SET is_bundle = true WHERE id = $1`, bundleID); e != nil {
			return e
		}
		for componentID, qty := range components {
			if _, e := tx.Exec(t.Context(), `
				INSERT INTO bundle_component
				  (tenant_id, company_id, bundle_variant_id,
				   component_variant_id, qty)
				VALUES ($1,$2,$3,$4,$5::numeric)`,
				f.tenantID, f.companyID, bundleID, componentID, qty); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("build bundle: %v", err)
	}
}

func stockOnHand(t *testing.T, h *harness, f *shopFixture, variantID uuid.UUID) string {
	t.Helper()
	var qty string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT stock_on_hand($1, $2)::text`, variantID, f.warehouseID).
			Scan(&qty)
	}); err != nil {
		t.Fatalf("on hand: %v", err)
	}
	return qty
}

// sellBundle rings up a bundle line and returns the invoice id.
func sellBundle(t *testing.T, h *harness, f *shopFixture, bundleID uuid.UUID, qty string) string {
	t.Helper()
	sale := map[string]any{
		"invoice_uuid": uuid.NewString(),
		"doc_type":     "simplified",
		"issued_at":    "2026-08-15T10:30:00Z",
		"lines": []map[string]any{{
			"variant_id": bundleID.String(), "description": "Combo",
			"qty": qty, "unit_price": "115.00", "tax_treatment": "standard",
		}},
		"tenders": []map[string]any{{
			"method": "cash",
			"amount": decimal.RequireFromString("115.00").
				Mul(decimal.RequireFromString(qty)).StringFixed(2),
		}},
	}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("sell bundle: %d %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["invoice_id"].(string)
	return id
}

// Selling a combo takes stock from its components, proportionally.
//
// B1's sentence, mechanised: "Suit + Shirt + Tie" leaves one fewer of each, and
// selling two takes two of each — or six, where the combo holds three.
func TestSellingACombPackageDeductsItsComponents(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	suit := newVariant(t, h, f, "SUIT", "10", "200.00")
	tie := newVariant(t, h, f, "TIE", "30", "20.00")
	combo := newVariant(t, h, f, "COMBO", "", "")
	makeBundle(t, h, f, combo, map[uuid.UUID]string{suit: "1", tie: "3"})

	sellBundle(t, h, f, combo, "2")

	// Two combos: two suits and six ties.
	if got := stockOnHand(t, h, f, suit); got != "8.0000" {
		t.Errorf("suits on hand = %s, want 8.0000", got)
	}
	if got := stockOnHand(t, h, f, tie); got != "24.0000" {
		t.Errorf("ties on hand = %s, want 24.0000 — the component quantity was "+
			"not applied proportionally", got)
	}
	// The bundle itself holds no stock and must not have moved. Compared
	// numerically: with no movements at all the sum is plain "0", not
	// "0.0000", and the point is the quantity rather than its formatting.
	if got := stockOnHand(t, h, f, combo); !decimal.RequireFromString(got).IsZero() {
		t.Errorf("the bundle itself moved stock: %s", got)
	}
}

// The combo's cost of sale is the sum of what its components cost.
//
// Gross profit has to be a measurement. A bundle valued at anything other than
// what left the shelf would report margin that never existed.
func TestACombPackageCostsWhatItsComponentsCost(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	suit := newVariant(t, h, f, "SUIT2", "10", "200.00")
	tie := newVariant(t, h, f, "TIE2", "30", "20.00")
	combo := newVariant(t, h, f, "COMBO2", "", "")
	makeBundle(t, h, f, combo, map[uuid.UUID]string{suit: "1", tie: "2"})

	invoiceID := sellBundle(t, h, f, combo, "1")

	// One suit at 200 plus two ties at 20 = 240.
	var cogs string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT coalesce(sum(cogs_amount),0)::text FROM sales_invoice_line
			 WHERE invoice_id = $1`, invoiceID).Scan(&cogs)
	}); err != nil {
		t.Fatalf("read cogs: %v", err)
	}
	if decimal.RequireFromString(cogs).
		Cmp(decimal.RequireFromString("240")) != 0 {
		t.Errorf("bundle cost of sale = %s, want 240 (one suit at 200 plus two "+
			"ties at 20)", cogs)
	}
}

// A combo short of one component cannot be sold.
//
// The stock policy applies to the components, because they are what moves. A
// combo that sold with no ties left would take the shop below zero on an item
// it never mentioned.
func TestACombPackageShortOfAComponentIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	suit := newVariant(t, h, f, "SUIT3", "10", "200.00")
	tie := newVariant(t, h, f, "TIE3", "1", "20.00") // only one tie
	combo := newVariant(t, h, f, "COMBO3", "", "")
	makeBundle(t, h, f, combo, map[uuid.UUID]string{suit: "1", tie: "3"})

	sale := map[string]any{
		"invoice_uuid": uuid.NewString(),
		"doc_type":     "simplified",
		"issued_at":    "2026-08-15T10:30:00Z",
		"lines": []map[string]any{{
			"variant_id": combo.String(), "description": "Combo",
			"qty": "1", "unit_price": "115.00", "tax_treatment": "standard",
		}},
		"tenders": []map[string]any{{"method": "cash", "amount": "115.00"}},
	}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	defer resp.Body.Close()
	if resp.StatusCode == 201 {
		t.Error("a combo sold with too few of one component")
	}
	// And nothing moved: a refused sale must leave every component whole.
	if got := stockOnHand(t, h, f, suit); got != "10.0000" {
		t.Errorf("suits moved on a refused sale: %s", got)
	}
}

// An empty combo cannot be sold.
//
// It would take no stock and cost nothing, reporting pure margin on every sale
// — a figure that looks like a very good day.
func TestAnEmptyCombPackageCannotBeSold(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	combo := newVariant(t, h, f, "EMPTY", "", "")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET is_bundle = true WHERE id = $1`, combo)
		return e
	}); err != nil {
		t.Fatalf("flag bundle: %v", err)
	}

	sale := map[string]any{
		"invoice_uuid": uuid.NewString(),
		"doc_type":     "simplified",
		"issued_at":    "2026-08-15T10:30:00Z",
		"lines": []map[string]any{{
			"variant_id": combo.String(), "description": "Combo",
			"qty": "1", "unit_price": "115.00", "tax_treatment": "standard",
		}},
		"tenders": []map[string]any{{"method": "cash", "amount": "115.00"}},
	}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	defer resp.Body.Close()
	if resp.StatusCode == 201 {
		t.Error("an empty combo was sold")
	}
}

// A combo cannot contain another combo.
//
// Nesting would need recursive expansion, recursive costing and a cycle check,
// and B1's example is one level deep. Unrepresentable beats half-supported: a
// wrong answer computed confidently is worse than a refusal.
func TestACombPackageCannotContainAnotherCombPackage(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	inner := newVariant(t, h, f, "INNER", "5", "10.00")
	innerCombo := newVariant(t, h, f, "INNERCOMBO", "", "")
	makeBundle(t, h, f, innerCombo, map[uuid.UUID]string{inner: "1"})

	outer := newVariant(t, h, f, "OUTER", "", "")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET is_bundle = true WHERE id = $1`, outer)
		return e
	}); err != nil {
		t.Fatalf("flag outer: %v", err)
	}

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO bundle_component
			  (tenant_id, company_id, bundle_variant_id, component_variant_id, qty)
			VALUES ($1,$2,$3,$4,1)`,
			f.tenantID, f.companyID, outer, innerCombo)
		return e
	})
	if err == nil {
		t.Error("a combo was nested inside another combo")
	}
}

// Components can only hang off something flagged as a bundle.
//
// Otherwise an ordinary product could quietly acquire components and start
// deducting stock nobody expected it to touch.
func TestComponentsCannotHangOffAnOrdinaryProduct(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	plain := newVariant(t, h, f, "PLAIN", "5", "10.00")
	other := newVariant(t, h, f, "OTHER", "5", "10.00")

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO bundle_component
			  (tenant_id, company_id, bundle_variant_id, component_variant_id, qty)
			VALUES ($1,$2,$3,$4,1)`,
			f.tenantID, f.companyID, plain, other)
		return e
	})
	if err == nil {
		t.Error("an ordinary product accepted bundle components")
	}
}

// Two tills selling combos that share a component do not deadlock.
//
// The components are what move, so they are what must be locked. Locking only
// the bundle would leave two sales free to take a shared tie in opposite
// orders, which is the deadlock LockStock's deterministic ordering exists to
// prevent.
func TestConcurrentBundleSalesSharingAComponentDoNotDeadlock(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	shared := newVariant(t, h, f, "SHARED", "100", "5.00")
	a := newVariant(t, h, f, "AONLY", "100", "5.00")
	b := newVariant(t, h, f, "BONLY", "100", "5.00")

	comboA := newVariant(t, h, f, "COMBOA", "", "")
	comboB := newVariant(t, h, f, "COMBOB", "", "")
	makeBundle(t, h, f, comboA, map[uuid.UUID]string{shared: "1", a: "1"})
	makeBundle(t, h, f, comboB, map[uuid.UUID]string{b: "1", shared: "1"})

	// Ten sales, alternating between the two combos so the shared component is
	// reached from both orders at once.
	const tills = 10
	errs := concurrently(tills, func(i int) error {
		combo := comboA
		if i%2 == 1 {
			combo = comboB
		}
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, map[string]any{
			"invoice_uuid": uuid.NewString(),
			"doc_type":     "simplified",
			"issued_at":    "2026-08-15T10:30:00Z",
			"lines": []map[string]any{{
				"variant_id": combo.String(), "description": "Combo",
				"qty": "1", "unit_price": "115.00",
				"tax_treatment": "standard",
			}},
			"tenders": []map[string]any{{"method": "cash", "amount": "115.00"}},
		})
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			return errFromStatus(resp.StatusCode)
		}
		return nil
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent bundle sale %d failed: %v", i, err)
		}
	}

	// Ten combos, each taking one of the shared item.
	if got := stockOnHand(t, h, f, shared); got != "90.0000" {
		t.Errorf("shared component on hand = %s, want 90.0000 — a concurrent "+
			"sale lost or double-counted stock", got)
	}
}
