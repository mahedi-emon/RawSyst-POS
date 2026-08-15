//go:build integration

// VAT return preparation.
//
// The figure that matters is not the total — it is the reconciliation. Two
// independent paths reach output tax: adding up what every invoice charged, and
// reading what the posting engine booked to the Output VAT account. They must
// agree exactly, and finding a difference before a return is filed is the
// difference between a correction and a penalty.
package api

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
)

func (h *harness) vatService() *vat.Service {
	return vat.NewService(h.pool, registry.New(h.pool, false))
}

// Output tax on the return equals output tax in the ledger. Anything else means
// a sale charged tax that never reached the books, or the books hold tax no
// invoice supports.
func TestVATReturnReconcilesToTheLedger(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f) // three units at 115 gross

	ret, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if ret.OutputTaxTotal != "45" {
		t.Errorf("output tax = %s, want 45", ret.OutputTaxTotal)
	}
	if ret.TotalNet != "300" {
		t.Errorf("net supplies = %s, want 300", ret.TotalNet)
	}
	if !ret.Reconciled {
		t.Fatalf("the return does not reconcile: invoices say %s, the ledger "+
			"says %s, a difference of %s",
			ret.OutputTaxTotal, ret.LedgerOutputTax, ret.Difference)
	}
	if ret.Country != "sa" || ret.Model != "vat" {
		t.Errorf("country/model = %s/%s, want sa/vat", ret.Country, ret.Model)
	}
}

// A refund reduces what is owed. Credit note lines store POSITIVE tax with a
// negative quantity, so a return that summed the stored tax directly would add
// refunded tax to the liability instead of subtracting it — overstating what is
// owed by twice every refund.
func TestARefundReducesTheTaxOwed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "2", "115.00", "230.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}

	// One of the two comes back: 115 gross, so 15 of tax goes back.
	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "faulty stitching",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}

	ret, err := h.vatService().Prepare(ctx, f.tenantID, f.companyID, day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// 30 charged less 15 refunded.
	if ret.OutputTaxTotal != "15" {
		t.Errorf("output tax = %s, want 15 (30 charged less 15 refunded)",
			ret.OutputTaxTotal)
	}
	if ret.TotalNet != "100" {
		t.Errorf("net supplies = %s, want 100", ret.TotalNet)
	}
	if !ret.Reconciled {
		t.Errorf("the return stopped reconciling once a refund was involved: "+
			"invoices %s, ledger %s", ret.OutputTaxTotal, ret.LedgerOutputTax)
	}
}

// A return must say what it does not contain. Reporting zero input tax silently
// would look like a business that reclaimed nothing rather than one whose
// purchases were never entered.
func TestTheReturnDeclaresWhatItCannotInclude(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)

	ret, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if ret.Filed {
		t.Error("a prepared return reported itself as filed")
	}
	if ret.InputTaxTotal != nil {
		t.Errorf("input tax reported as %s when no purchase has ever been "+
			"recorded", *ret.InputTaxTotal)
	}
	if ret.NetPayable != nil {
		t.Error("a net payable was reported without an input side to net against")
	}

	joined := strings.Join(ret.Outstanding, " | ")
	if !strings.Contains(joined, "input tax") {
		t.Errorf("the return does not say input tax is missing: %v", ret.Outstanding)
	}
	if !strings.Contains(joined, "form layout") {
		t.Errorf("the return does not say the official form mapping is "+
			"unverified: %v", ret.Outstanding)
	}
}

// Zero-rated and exempt supplies are reported separately, not netted together.
// They are different things: a zero-rated supply is taxable at nil and keeps
// the right to reclaim input tax, an exempt one does not.
func TestTreatmentsAreReportedSeparately(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// A zero-rated variant alongside the standard-rated one.
	var zeroVariant string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var productID string
		if e := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'BOOK','Book','zero_rated') RETURNING id`,
			f.tenantID, f.companyID).Scan(&productID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO variant (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,'BOOK-1',50.00) RETURNING id`,
			f.tenantID, f.companyID, productID).Scan(&zeroVariant); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,5,'opening',100)`,
			f.tenantID, f.companyID, zeroVariant, f.warehouseID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO cost_layer
			  (tenant_id, company_id, variant_id, warehouse_id,
			   qty_received, qty_remaining, unit_cost)
			VALUES ($1,$2,$3,$4,5,5,20)`,
			f.tenantID, f.companyID, zeroVariant, f.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("seed zero-rated product: %v", err)
	}

	h.tradeOneDay(t, f) // 300 net standard-rated, 45 tax

	sale := oneItemSale(f, newUUID(), "1", "50.00", "50.00")
	lines := sale["lines"].([]map[string]any)
	lines[0]["variant_id"] = zeroVariant
	lines[0]["tax_treatment"] = "zero_rated"
	lines[0]["description"] = "Book"
	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale); resp.StatusCode != 201 {
		t.Fatalf("zero-rated sale: %s", readBody(t, resp))
	}

	ret, err := h.vatService().Prepare(ctx, f.tenantID, f.companyID, day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	byTreatment := map[string]string{}
	netByTreatment := map[string]string{}
	for _, s := range ret.Supplies {
		byTreatment[s.Treatment] = s.TaxAmount
		netByTreatment[s.Treatment] = s.NetAmount
	}

	if len(ret.Supplies) != 2 {
		t.Fatalf("%d treatments reported, want 2: %+v", len(ret.Supplies), ret.Supplies)
	}
	if byTreatment["standard"] != "45" {
		t.Errorf("standard-rated tax = %s, want 45", byTreatment["standard"])
	}
	if byTreatment["zero_rated"] != "0" {
		t.Errorf("zero-rated tax = %s, want 0", byTreatment["zero_rated"])
	}
	if netByTreatment["zero_rated"] != "50" {
		t.Errorf("zero-rated net = %s, want 50; a zero-rated supply has a value "+
			"even though it carries no tax", netByTreatment["zero_rated"])
	}
	if !ret.Reconciled {
		t.Errorf("the return does not reconcile with mixed treatments: %s vs %s",
			ret.OutputTaxTotal, ret.LedgerOutputTax)
	}
}

// The period is a period. Tax charged outside it belongs to another return.
func TestTheReturnCoversOnlyItsPeriod(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f) // dated the 15th

	ret, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(10))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if ret.OutputTaxTotal != "0" {
		t.Errorf("a period before any trading reported %s of output tax",
			ret.OutputTaxTotal)
	}
	if !ret.Reconciled {
		t.Error("an empty period failed to reconcile")
	}
}

// One tenant cannot prepare another's return.
func TestOneTenantCannotPrepareAnothersReturn(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")
	h.tradeOneDay(t, theirs)

	_, err := h.vatService().Prepare(t.Context(), mine.tenantID, theirs.companyID,
		day(1), day(31))
	if err == nil {
		t.Fatal("one tenant prepared a return over another tenant's company")
	}
}
