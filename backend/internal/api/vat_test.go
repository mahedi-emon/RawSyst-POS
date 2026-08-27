//go:build integration

// VAT return preparation.
//
// The figure that matters is not the total — it is the reconciliation. Every
// figure on the return is reached twice, by paths that share nothing. Output tax
// is added up from what every invoice charged and read again from the Output VAT
// account the posting engine booked. Input tax is read from the Input VAT
// account and again from the tax on the supplier bills behind it. They must
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

	if ret.OutputTaxTotal != "45.00" {
		t.Errorf("output tax = %s, want 45.00", ret.OutputTaxTotal)
	}
	if ret.TotalNet != "300.00" {
		t.Errorf("net supplies = %s, want 300.00", ret.TotalNet)
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
	if ret.OutputTaxTotal != "15.00" {
		t.Errorf("output tax = %s, want 15.00 (30 charged less 15 refunded)",
			ret.OutputTaxTotal)
	}
	if ret.TotalNet != "100.00" {
		t.Errorf("net supplies = %s, want 100.00", ret.TotalNet)
	}
	if !ret.Reconciled {
		t.Errorf("the return stopped reconciling once a refund was involved: "+
			"invoices %s, ledger %s", ret.OutputTaxTotal, ret.LedgerOutputTax)
	}
}

// A return must say what it does not contain — and it must not claim to be
// missing something it already has.
//
// THE DEFECT THIS TEST WAS REWRITTEN OVER, and it was the expensive kind: a
// module reporting what it could not include, for a reason that had since
// become false.
//
// This package used to report no input tax and no net payable for every VAT
// country, explaining that "purchasing is not built, so no supplier invoice has
// been recorded". Purchasing IS built. A supplier bill's tax reaches the Input
// VAT account through purchase.credit or purchase.clear_accrual, and has since
// migrations 0031 and 0034.
//
// So a Saudi retailer filing from this over-paid by the whole of its input tax —
// usually the larger part of what it owes — or could not file at all, because
// the net payable was nil. The stated reason was worse than the omission: it
// sent whoever read it looking for a module that was already there.
//
// This shop has traded and bought nothing, so its input tax is a true zero.
// That is a figure, not an absence — a business with no purchases reclaims
// nothing — and the return has to say so and net to what it collected.
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

	// Reported, and reported as zero rather than withheld.
	if ret.InputTaxTotal == nil {
		t.Fatal("a VAT return reported no input tax at all, so there is nothing " +
			"to net against and the business over-pays by whatever it could " +
			"have reclaimed")
	}
	if *ret.InputTaxTotal != "0.00" {
		t.Errorf("input tax = %s at a shop that has bought nothing",
			*ret.InputTaxTotal)
	}

	// The second, independent path to the same figure.
	if ret.BilledInputTax == nil || *ret.BilledInputTax != "0.00" {
		t.Errorf("the bills give no input figure to check the account against: %v",
			ret.BilledInputTax)
	}
	if !ret.Reconciled {
		t.Errorf("the return does not reconcile: output %s against %s, "+
			"input %v against %v", ret.OutputTaxTotal, ret.LedgerOutputTax,
			ret.BilledInputTax, ret.InputTaxTotal)
	}

	// Collected less reclaimed — which, with nothing reclaimed, is what it
	// collected.
	if ret.NetPayable == nil {
		t.Fatal("no net payable was reported, so there is no figure to file")
	}
	if *ret.NetPayable != "45.00" {
		t.Errorf("net payable = %s, want 45.00 (45 collected, nothing reclaimed)",
			*ret.NetPayable)
	}

	joined := strings.Join(ret.Outstanding, " | ")

	// The stale claim, pinned so it cannot return.
	if strings.Contains(joined, "purchasing is not built") {
		t.Errorf("the return still says purchasing does not exist: %v",
			ret.Outstanding)
	}

	// What genuinely is outstanding.
	if !strings.Contains(joined, "form layout") {
		t.Errorf("the return does not say the official form mapping is "+
			"unverified: %v", ret.Outstanding)
	}

	// And nothing else, because this shop made no exempt supplies and has no
	// bills at all. A caveat that fires unconditionally teaches its reader to
	// skip the list, which costs the caveats that matter.
	for _, phrase := range []string{"exempt supplies", "three-way match"} {
		if strings.Contains(joined, phrase) {
			t.Errorf("the return raised %q against a shop with no purchases and "+
				"no exempt supplies: %v", phrase, ret.Outstanding)
		}
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
		if _, e := tx.Exec(ctx, `
			INSERT INTO cost_layer
			  (tenant_id, company_id, variant_id, warehouse_id,
			   qty_received, qty_remaining, unit_cost)
			VALUES ($1,$2,$3,$4,5,5,20)`,
			f.tenantID, f.companyID, zeroVariant, f.warehouseID); e != nil {
			return e
		}
		// The pool as well as the layer. This company costs at weighted
		// average, so the pool is what a sale reads — seeding only the layer
		// leaves the shelf empty as far as costing is concerned.
		_, e := tx.Exec(ctx, `
			INSERT INTO stock_valuation
			  (tenant_id, company_id, variant_id, warehouse_id, qty_on_hand, total_value)
			VALUES ($1,$2,$3,$4,5,100)`,
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
	if byTreatment["standard"] != "45.00" {
		t.Errorf("standard-rated tax = %s, want 45.00", byTreatment["standard"])
	}
	if byTreatment["zero_rated"] != "0.00" {
		t.Errorf("zero-rated tax = %s, want 0.00", byTreatment["zero_rated"])
	}
	if netByTreatment["zero_rated"] != "50.00" {
		t.Errorf("zero-rated net = %s, want 50.00; a zero-rated supply has a "+
			"value even though it carries no tax", netByTreatment["zero_rated"])
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
	if ret.OutputTaxTotal != "0.00" {
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

// --- the input side ------------------------------------------------------

// THE REGRESSION TEST FOR THE DEFECT ABOVE: a purchase's input tax must reach
// the return and net against what was collected.
//
// Ten abayas at 100.00 plus 15% is 150.00 of recoverable input tax. The shop
// also sold three at 115.00 gross, collecting 45.00. So it owes 45.00, may
// reclaim 150.00, and is owed 105.00 — where before this was fixed the return
// said it owed 45.00 with no input side at all. One month, one supplier, one
// order: 150.00 over-paid.
func TestAPurchaseReachesTheReturnAndNetsAgainstOutputTax(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	h.tradeOneDay(t, f.shopFixture) // 300 net sold, 45 output tax

	poID, lineID := raiseOrder(t, h, f, "10", "100.00")
	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})
	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-VAT-1", "bill_date": "2026-08-15",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}

	ret, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if ret.InputTaxTotal == nil {
		t.Fatal("a posted supplier bill left no input tax on the return")
	}
	if *ret.InputTaxTotal != "150.00" {
		t.Errorf("input tax = %s, want 150.00 (ten at 100.00 plus 15%%)",
			*ret.InputTaxTotal)
	}

	// The second path — the bills themselves — which must agree exactly.
	if ret.BilledInputTax == nil || *ret.BilledInputTax != "150.00" {
		t.Errorf("the bills give %v of input tax against the account's %s",
			ret.BilledInputTax, *ret.InputTaxTotal)
	}
	if ret.InputDifference == nil || *ret.InputDifference != "0.00" {
		t.Errorf("the two paths to input tax differ by %v", ret.InputDifference)
	}
	if !ret.Reconciled {
		t.Errorf("the return does not reconcile: output %s against %s, input %v "+
			"against %v", ret.OutputTaxTotal, ret.LedgerOutputTax,
			ret.BilledInputTax, ret.InputTaxTotal)
	}

	// Owed 45, reclaiming 150, so the authority owes the shop 105. A negative
	// net payable is ordinary in a month with more buying than selling, and
	// clamping it to zero would quietly forfeit the refund.
	if ret.NetPayable == nil {
		t.Fatal("no net payable was reported, so there is no figure to file")
	}
	if *ret.NetPayable != "-105.00" {
		t.Errorf("net payable = %s, want -105.00 (45 collected less 150 "+
			"reclaimable)", *ret.NetPayable)
	}
}

// A bill the three-way match held is deliberately outside the ledger, so its
// input tax is not reclaimable yet — and the return has to say so rather than
// leave a shop wondering why the figure is short.
//
// Then approving it brings the tax in under the BILL's own date, not the
// approval date, so it lands back in THIS period after this return was already
// prepared. That is what makes the caveat worth a line instead of leaving it
// implicit, and asserting it here is what stops the two halves drifting apart.
func TestInputTaxOnAHeldBillIsExcludedAndDeclaredThenArrivesOnApproval(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	// Eight arrived.
	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "8"}},
		})
	// Ten billed — the case B5.2 exists to catch.
	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-VAT-2", "bill_date": "2026-08-15",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	bill := decodeJSON(t, billed)
	if bill["status"] != "blocked" {
		t.Fatalf("the bill is %v, want blocked — this test needs a held bill",
			bill["status"])
	}
	billID, _ := bill["id"].(string)

	held, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(31))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Nothing reclaimable on either path. The account has no movement because
	// no entry was written, and the cross-check excludes the bill for exactly
	// that reason rather than by looking at its status.
	if held.InputTaxTotal == nil || *held.InputTaxTotal != "0.00" {
		t.Errorf("input tax = %v against a bill that was never posted",
			held.InputTaxTotal)
	}
	if held.BilledInputTax == nil || *held.BilledInputTax != "0.00" {
		t.Errorf("a held bill was counted as reclaimable: %v",
			held.BilledInputTax)
	}
	if !held.Reconciled {
		t.Errorf("a held bill broke the input reconciliation: bills say %v, the "+
			"account says %v", held.BilledInputTax, held.InputTaxTotal)
	}
	if joined := strings.Join(held.Outstanding, " | "); !strings.Contains(
		joined, "three-way match") {
		t.Errorf("the return does not say a held bill's input tax is missing: %v",
			held.Outstanding)
	}

	// Somebody accepts the short delivery.
	ok := h.do(t, "POST",
		f.path("/api/v1/purchasing/bills/"+billID+"/approve"), f.token,
		map[string]any{"reason": "short delivery agreed by phone with Acme"})
	if ok.StatusCode != 200 {
		t.Fatalf("approve: %s", readBody(t, ok))
	}

	after, err := h.vatService().Prepare(t.Context(), f.tenantID, f.companyID,
		day(1), day(31))
	if err != nil {
		t.Fatalf("prepare after approval: %v", err)
	}

	// Back-dated to the bill, so it falls in the period the return already
	// covered. Approving in a later month does not move it to that month.
	if after.InputTaxTotal == nil || *after.InputTaxTotal != "150.00" {
		t.Errorf("input tax = %v after approval, want 150.00 posted under the "+
			"bill's own date", after.InputTaxTotal)
	}
	if after.BilledInputTax == nil || *after.BilledInputTax != "150.00" {
		t.Errorf("the approved bill is not in the cross-check: %v",
			after.BilledInputTax)
	}
	if !after.Reconciled {
		t.Errorf("the return stopped reconciling after an approval: bills say "+
			"%v, the account says %v", after.BilledInputTax, after.InputTaxTotal)
	}

	// And the caveat retires itself, because there is no longer a held bill.
	if joined := strings.Join(after.Outstanding, " | "); strings.Contains(
		joined, "three-way match") {
		t.Errorf("the return still warns about a held bill after the only one "+
			"was approved: %v", after.Outstanding)
	}
}
