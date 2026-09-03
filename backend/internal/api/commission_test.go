//go:build integration

// The sales commission engine (C6), and C14's seventh effect of a return.
//
// A commission scheme could be defined and a payslip could carry a commission
// figure, but nothing tied the two to what was actually sold correctly:
//
//   - A RETURN made the figure go UP. The period's takings were summed across
//     `sales_invoice` without regard to document type, and a credit note's
//     lines carry positive net amounts with the direction held in the doc type.
//     So refunding a sale in full paid the salesperson twice for it — the exact
//     opposite of C14 effect 7, "reverse any sales commission attributed to the
//     original sale".
//   - A rule scoped to one SHOP paid on every shop's sales. `store_id` is
//     settable through the API and was read only to order the rule search,
//     never to decide which takings the rule covers.
//   - A DRAFT or CANCELLED invoice counted, so a sale that legally does not
//     exist still earned commission.
package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// commissionEarner hires the fixture's own user as a commission-eligible
// employee, so the sales they ring up are attributed to them.
func commissionEarner(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	return h.hire(t, f, "Salesperson", "5000.00", map[string]any{
		"user_id":             f.userID.String(),
		"commission_eligible": true,
	})
}

// commissionRule creates a scheme through the route that defines them.
func commissionRule(t *testing.T, h *harness, f *shopFixture, body map[string]any) {
	t.Helper()
	if _, ok := body["effective_from"]; !ok {
		body["effective_from"] = "2020-01-01"
	}
	resp := h.do(t, http.MethodPost,
		"/api/v1/commission-rules?company_id="+f.companyID.String(),
		f.token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("set commission rule: %d %s", resp.StatusCode, readBody(t, resp))
	}
}

// commissionPaid runs payroll for August 2026 and returns the one payslip's
// commission line.
func commissionPaid(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), f.token,
		map[string]any{"period": "2026-08"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("payroll: %d %s", resp.StatusCode, readBody(t, resp))
	}
	slips, _ := decodeJSONFrom(t, resp)["payslips"].([]any)
	if len(slips) != 1 {
		t.Fatalf("the run has %d payslips, want 1", len(slips))
	}
	slip, _ := slips[0].(map[string]any)
	got, _ := slip["commission"].(string)
	return got
}

// sellAndReturn rings a sale up and, if asked, brings the whole thing back.
func sellAndReturn(t *testing.T, h *harness, f *shopFixture, bringBack bool) {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)
	resp.Body.Close()
	if !bringBack {
		return
	}

	var lineID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id::text FROM sales_invoice_line WHERE invoice_id = $1`,
			invoiceID).Scan(&lineID)
	}); err != nil {
		t.Fatalf("find the line: %v", err)
	}

	ret := h.do(t, http.MethodPost, "/api/v1/pos/returns", f.token,
		map[string]any{
			"credit_note_uuid":    newUUID(),
			"original_invoice_id": invoiceID,
			"issued_at":           "2026-08-15T12:00:00Z",
			"reason":              "changed their mind",
			"lines":               []any{map[string]any{"line_id": lineID, "qty": "1"}},
			"refunds":             []any{map[string]any{"method": "cash", "amount": "115.00"}},
		})
	defer ret.Body.Close()
	if ret.StatusCode != http.StatusCreated {
		t.Fatalf("return: %d %s", ret.StatusCode, readBody(t, ret))
	}
}

// A sale earns commission at the scheme's rate.
//
// The baseline: an engine that paid nothing would satisfy every reversal test
// below without being of any use to anybody.
func TestASaleEarnsCommissionAtTheSchemesRate(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent of takings", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, f, false) // 115.00 gross, 100.00 net of VAT

	if got := commissionPaid(t, h, f); got != "10.00" {
		t.Errorf("commission = %s, want 10.00 — a tenth of the 100.00 net", got)
	}
}

// A return takes back the commission it paid (C14 effect 7).
//
// This failed in the worst possible direction before the fix: the credit note's
// positive line amounts were ADDED to the period's takings, so a sale rung up
// and refunded in full paid 20.00 on takings of nothing. A shop running this
// would have discovered it through its own staff.
func TestAReturnTakesBackTheCommissionItPaid(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent of takings", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, f, true) // sold, then brought back in full

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — the sale it was earned on was "+
			"refunded in full, so there are no takings to pay a share of", got)
	}
}

// A scheme scoped to one shop does not pay on another shop's sales.
//
// `store_id` is settable through the commission-rules route and was read only
// to ORDER the search for a rule, never to decide which takings that rule
// covers. A chain that gave one branch a scheme paid it on the whole company.
func TestASchemeScopedToOneShopDoesNotPayOnAnothers(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)

	// A scheme belonging to some OTHER shop entirely.
	other := h.seedShop(t, "owner")
	commissionRule(t, h, f, map[string]any{
		"name": "Branch scheme", "basis": "revenue", "rate": "0.10",
		"store_id": other.storeID.String(),
	})

	sellAndReturn(t, h, f, false)

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — the scheme belongs to a "+
			"different shop than the one that made the sale", got)
	}
}

// A scheme scoped to the shop that made the sale does pay.
//
// The other half, so the scoping fix cannot be a blanket refusal.
func TestASchemeScopedToTheSellingShopPays(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Branch scheme", "basis": "revenue", "rate": "0.10",
		"store_id": f.storeID.String(),
	})

	sellAndReturn(t, h, f, false)

	if got := commissionPaid(t, h, f); got != "10.00" {
		t.Errorf("commission = %s, want 10.00 — the scheme is this shop's own",
			got)
	}
}

// Commission on profit is measured on profit, not on takings.
//
// `basis` has always accepted 'profit' and the cost is on the line, so this
// pins that the two bases actually differ rather than both reading revenue.
func TestCommissionOnProfitIsMeasuredOnProfit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Tenth of the margin", "basis": "profit", "rate": "0.10",
	})

	sellAndReturn(t, h, f, false)

	// Whatever the fixture's cost is, commission on profit must be strictly
	// less than commission on the full 100.00 of revenue — the goods were not
	// free.
	got := commissionPaid(t, h, f)
	if got == "10.00" {
		t.Errorf("commission on profit = %s, the same as on revenue — the "+
			"cost of the goods was not taken off", got)
	}
	if got == "0.00" {
		t.Errorf("commission on profit = 0.00; the sale had a positive margin")
	}
}

// One tenant's commission scheme does not pay another tenant's staff.
func TestOneTenantsSchemeDoesNotPayAnothers(t *testing.T) {
	h := newHarness(t)
	generous := h.seedShop(t, "owner")
	plain := h.seedShop(t, "owner")

	commissionEarner(t, h, generous)
	commissionEarner(t, h, plain)
	commissionRule(t, h, generous, map[string]any{
		"name": "Ten per cent", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, generous, false)
	sellAndReturn(t, h, plain, false)

	if got := commissionPaid(t, h, generous); got != "10.00" {
		t.Errorf("the scheme's own tenant was paid %s, want 10.00", got)
	}
	if got := commissionPaid(t, h, plain); got != "0.00" {
		t.Errorf("a tenant with no scheme was paid %s; another tenant's "+
			"scheme reached across the boundary", got)
	}
}

// The sale records who made it.
//
// The column this whole feature rests on. Without it there is no link between
// a sale and a person, and `commissionFor` was joining on `created_by`, which
// never existed.
func TestASaleRecordsWhoMadeIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sellAndReturn(t, h, f, false)

	var cashier *uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT cashier_id FROM sales_invoice
			  WHERE company_id = $1 AND doc_type <> 'credit_note'`,
			f.companyID).Scan(&cashier)
	}); err != nil {
		t.Fatalf("read the attribution: %v", err)
	}
	if cashier == nil {
		t.Fatal("the sale recorded no cashier; nothing can be attributed to " +
			"a person")
	}
	if *cashier != f.userID {
		t.Errorf("the sale is attributed to %s, want the signed-in user %s",
			*cashier, f.userID)
	}
}

// A credit note is attributed to whoever made the ORIGINAL sale.
//
// C14 effect 7 reverses the commission attributed to the original sale. If the
// reversal followed the till the refund was processed at instead, a colleague
// handling somebody else's return would be docked for it and the seller would
// keep commission on goods that came back.
//
// The return is rung up normally and its credit note is then re-attributed to a
// DIFFERENT user, which is the situation a second cashier creates. Commission
// must still net to nothing for the seller: the reversal follows the parent
// invoice, not the credit note's own cashier.
func TestAReturnReversesAgainstTheOriginalSeller(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, f, true)

	// Somebody else was at the till for the refund.
	_, colleague := h.newUserInTenant(t, f.tenantID, "store_manager")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(t.Context(),
			`UPDATE sales_invoice SET cashier_id = $2
			  WHERE company_id = $1 AND doc_type = 'credit_note'`,
			f.companyID, colleague)
		if e == nil && tag.RowsAffected() != 1 {
			t.Fatalf("re-attributed %d credit notes, want 1", tag.RowsAffected())
		}
		return e
	}); err != nil {
		t.Fatalf("re-attribute the credit note: %v", err)
	}

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — the return must come off the "+
			"person who made the sale, whoever processed it", got)
	}
}

// A draft invoice earns nothing.
//
// A draft has consumed no invoice number and is not a sale. Paying commission
// on one would pay for something that legally does not exist.
func TestADraftInvoiceEarnsNoCommission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, f, false)
	setInvoiceState(t, h, f, "draft")

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — a draft is not a sale", got)
	}
}

// A cancelled invoice earns nothing.
func TestACancelledInvoiceEarnsNoCommission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent", "basis": "revenue", "rate": "0.10",
	})

	sellAndReturn(t, h, f, false)
	setInvoiceState(t, h, f, "cancelled")

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — a cancelled invoice never "+
			"became a sale", got)
	}
}

// setInvoiceState forces the fixture's one sale into a state.
func setInvoiceState(t *testing.T, h *harness, f *shopFixture, state string) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE sales_invoice SET state = $2
			  WHERE company_id = $1 AND doc_type <> 'credit_note'`,
			f.companyID, state)
		return e
	}); err != nil {
		t.Fatalf("set state %s: %v", state, err)
	}
}

// The Blueprint's own worked example: 2% once sales exceed SAR 50,000 a month.
//
// C6 states it in those words, and `rateFromTiers` reads it as the highest band
// reached applying to the WHOLE amount — which is how commission schemes are
// written and understood. This pins the example itself, so the reading cannot
// drift into a marginal calculation without somebody deciding to.
func TestTheTieredSchemeMatchesTheBlueprintsWorkedExample(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Tiered", "basis": "revenue", "rate": "0.01",
		"tiers": `[{"from":"0","rate":"0.01"},{"from":"50000","rate":"0.02"}]`,
	})

	topUpStock(t, h, f, 10)

	// Below the threshold: 400.00 net over four sales, at the lower band.
	for i := 0; i < 4; i++ {
		sellAndReturn(t, h, f, false)
	}
	if got := commissionPaid(t, h, f); got != "4.00" {
		t.Fatalf("under the threshold commission = %s, want 4.00 — one per "+
			"cent of 400.00", got)
	}
	clearPayrollRuns(t, h, f)

	// Push the month's takings past SAR 50,000 and the whole amount moves to
	// two per cent. 600 more units at 100.00 net, on top of the four above,
	// is 60,400.
	topUpStock(t, h, f, 700)
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "600", "115.00", "69000.00"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bulk sale: %d", resp.StatusCode)
	}

	if got := commissionPaid(t, h, f); got != "1208.00" {
		t.Errorf("over the threshold commission = %s, want 1208.00 — two per "+
			"cent of 60,400.00, the higher band applying to the whole", got)
	}
}

// Concurrent sales are each counted once.
//
// Commission is an aggregate over the period rather than a running total, so
// the risk is not a lost update but a sale that never lands or lands twice.
// Twenty tills ringing up at once must come to exactly twenty sales.
func TestConcurrentSalesAreEachCountedOnceForCommission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent", "basis": "revenue", "rate": "0.10",
	})

	topUpStock(t, h, f, 40)

	const tills = 20
	var wg sync.WaitGroup
	errCh := make(chan string, tills)
	for i := 0; i < tills; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
				oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				errCh <- readBody(t, resp)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatalf("a concurrent sale was refused: %s", msg)
	}

	// Twenty sales of 100.00 net at a tenth is 200.00 — no more, no less.
	if got := commissionPaid(t, h, f); got != "200.00" {
		t.Errorf("commission = %s, want 200.00 from %d concurrent sales; a "+
			"figure above it means a sale was counted twice and below it "+
			"means one was lost", got, tills)
	}
}

// topUpStock puts more goods on the shelf, with the valuation to match.
//
// The fixture ships ten units, which is plenty for a sale or two and nowhere
// near C6's SAR 50,000 threshold.
func topUpStock(t *testing.T, h *harness, f *shopFixture, units int) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		value := units * 60
		if _, e := tx.Exec(t.Context(), `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason,
			   value_delta)
			VALUES ($1,$2,$3,$4,$5,'opening',$6)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID, units, value); e != nil {
			return e
		}
		if _, e := tx.Exec(t.Context(), `
			INSERT INTO cost_layer
			  (tenant_id, company_id, variant_id, warehouse_id,
			   qty_received, qty_remaining, unit_cost)
			VALUES ($1,$2,$3,$4,$5,$5,60)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID, units); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			UPDATE stock_valuation
			   SET qty_on_hand = qty_on_hand + $4,
			       total_value = total_value + $5
			 WHERE company_id = $1 AND variant_id = $2 AND warehouse_id = $3`,
			f.companyID, f.variantID, f.warehouseID, units, value)
		return e
	}); err != nil {
		t.Fatalf("top up stock: %v", err)
	}
}

// clearPayrollRuns removes the period's draft run so it can be prepared again.
//
// A period may hold only one draft run, which is right for the product and
// awkward for a test that wants to see the same month recomputed after more
// sales land.
func clearPayrollRuns(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(), `
			DELETE FROM payslip WHERE run_id IN (
			  SELECT id FROM payroll_run WHERE company_id = $1)`,
			f.companyID); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(),
			`DELETE FROM payroll_run WHERE company_id = $1`, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("clear payroll runs: %v", err)
	}
}
