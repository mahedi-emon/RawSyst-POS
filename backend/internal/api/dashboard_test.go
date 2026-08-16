//go:build integration

package api

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// The Owner Dashboard.
//
// The property that matters is not that the tiles render — it is that they
// agree with the books. An owner who sees one revenue figure on the dashboard
// and another on the P&L has no reason to trust either, and the dashboard is
// usually the one they look at daily.

func overview(t *testing.T, h *harness, f *shopFixture, query string) map[string]any {
	t.Helper()
	path := "/api/v1/dashboard/overview?company_id=" + f.companyID.String() + query
	resp := h.do(t, "GET", path, f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("overview: %d %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// The figure an owner reads first must be the figure the journal holds.
func TestDashboardAgreesWithTheProfitAndLoss(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for i := 0; i < 3; i++ {
		created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		if created.StatusCode != 201 {
			t.Fatalf("sale: %s", readBody(t, created))
		}
	}

	body := overview(t, h, f, "&date=2026-08-15")
	sales, _ := body["sales"].(map[string]any)
	profit, _ := body["profit"].(map[string]any)

	// Three sales at 115 inclusive.
	if got, _ := sales["total"].(string); !amountsEqual(got, "345.00") {
		t.Errorf("today's sales = %q, want 345.00", got)
	}
	if n, _ := sales["invoice_count"].(float64); int(n) != 3 {
		t.Errorf("invoice count = %v, want 3", n)
	}

	// And the revenue the dashboard reports must be the revenue the P&L
	// reports, because both read the same journal.
	pl := decodeJSON(t, h.do(t, "GET",
		"/api/v1/reports/profit-and-loss?company_id="+f.companyID.String()+
			"&from=2026-08-15&to=2026-08-15", f.token, nil))

	plRevenue := decimal.Zero
	if lines, ok := pl["revenue"].([]any); ok {
		for _, raw := range lines {
			line, _ := raw.(map[string]any)
			amount, _ := line["amount"].(string)
			plRevenue = plRevenue.Add(decimal.RequireFromString(amount))
		}
	}
	dashRevenue, _ := profit["revenue"].(string)
	if !decimal.RequireFromString(dashRevenue).Equal(plRevenue) {
		t.Errorf("dashboard revenue %s but the P&L says %s — an owner has no "+
			"reason to trust either", dashRevenue, plRevenue)
	}
}

// Gross profit is revenue less cost, and the margin follows from it.
func TestDashboardReportsGrossProfitAndMargin(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	profit, _ := overview(t, h, f, "&date=2026-08-15")["profit"].(map[string]any)

	revenue := decimal.RequireFromString(profit["revenue"].(string))
	cost := decimal.RequireFromString(profit["cost"].(string))
	gross := decimal.RequireFromString(profit["gross"].(string))

	if !revenue.Sub(cost).Equal(gross) {
		t.Errorf("gross %s does not equal revenue %s less cost %s",
			gross, revenue, cost)
	}
	// A margin is only meaningful against revenue.
	if revenue.IsPositive() && profit["margin_pct"] == nil {
		t.Error("no margin reported against positive revenue")
	}
}

// A percentage change against nothing is a division artefact, not information.
func TestDashboardOmitsChangeWhenThereIsNothingToCompare(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sales, _ := overview(t, h, f, "&date=2026-08-15")["sales"].(map[string]any)

	if sales["change_pct"] != nil {
		t.Errorf("reported a change of %v against a day with no trading",
			sales["change_pct"])
	}
	// The figure itself is still present and zero — absent would render as a
	// broken tile.
	if got, _ := sales["total"].(string); !amountsEqual(got, "0.00") {
		t.Errorf("today's sales = %q, want 0.00", got)
	}
}

// Quiet days must appear in the trend as zero. A sparkline that silently closes
// over a closed Friday misreports the shape of the week.
func TestDashboardTrendCoversEveryDay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sales, _ := overview(t, h, f, "&date=2026-08-15")["sales"].(map[string]any)
	trend, _ := sales["trend"].([]any)

	if len(trend) != 14 {
		t.Fatalf("trend has %d points, want 14", len(trend))
	}
	first, _ := trend[0].(map[string]any)
	last, _ := trend[13].(map[string]any)
	if first["date"] != "2026-08-02" {
		t.Errorf("trend starts at %v, want 2026-08-02", first["date"])
	}
	if last["date"] != "2026-08-15" {
		t.Errorf("trend ends at %v, want the day asked for", last["date"])
	}
}

// Payment methods are never merged. Mada's fee is materially lower than a
// scheme card's, so folding them together misstates margin — E3.1.
func TestDashboardKeepsTendersSeparate(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{
		{"method": "cash", "amount": "50.00"},
		{"method": "mada", "amount": "65.00"},
	}
	if created := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale); created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	tenders, _ := overview(t, h, f, "&date=2026-08-15")["tenders"].([]any)
	seen := map[string]string{}
	for _, raw := range tenders {
		slice, _ := raw.(map[string]any)
		method, _ := slice["method"].(string)
		total, _ := slice["total"].(string)
		seen[method] = total
	}

	if len(seen) != 2 {
		t.Fatalf("tenders came back as %v, want cash and mada separately", seen)
	}
	if !amountsEqual(seen["cash"], "50.00") || !amountsEqual(seen["mada"], "65.00") {
		t.Errorf("tender split is %v, want cash 50.00 and mada 65.00", seen)
	}
}

// The exchange offset moves no money and must not appear beside real tenders.
func TestDashboardHidesTheExchangeOffset(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")
	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "230.00",
			[]map[string]any{{"method": "cash", "amount": "115.00"}}))
	if resp.StatusCode != 201 {
		t.Fatalf("exchange: %s", readBody(t, resp))
	}

	tenders, _ := overview(t, h, f, "&date=2026-08-15")["tenders"].([]any)
	for _, raw := range tenders {
		slice, _ := raw.(map[string]any)
		if slice["method"] == "exchange_clearing" {
			t.Error("the exchange offset is shown as a payment the shop took")
		}
	}
}

// "Where is my money" must separate what the shop holds from what the acquirer
// still holds (C12).
func TestDashboardSeparatesUnsettledCardMoney(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// The fixture already has cash movement from receiving stock, so the
	// assertion is that a card sale does not CHANGE the drawer — not that the
	// drawer is empty.
	before, _ := overview(t, h, f, "&date=2026-08-16")["money"].(map[string]any)
	cashBefore, _ := before["cash"].(string)

	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{{"method": "mada", "amount": "115.00"}}
	if created := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale); created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	money, _ := overview(t, h, f, "&date=2026-08-16")["money"].(map[string]any)

	// Taken today, not yet in the bank.
	if got, _ := money["unsettled"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("unsettled = %q, want 115.00 sitting with the acquirer", got)
	}
	if got, _ := money["cash"].(string); !amountsEqual(got, cashBefore) {
		t.Errorf("cash moved from %q to %q on a card sale", cashBefore, got)
	}
}

// Absent is not zero. An owner shown "Payables: 0.00" would reasonably conclude
// they owe nobody, which is a very different statement from "not yet built".
func TestDashboardNamesWhatIsNotBuiltRatherThanShowingZero(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	body := overview(t, h, f, "")
	unbuilt, _ := body["unbuilt"].([]any)
	if len(unbuilt) == 0 {
		t.Fatal("nothing declared unbuilt; the screen cannot tell an owner why a panel is empty")
	}

	named := map[string]bool{}
	for _, raw := range unbuilt {
		s, _ := raw.(string)
		named[s] = true
	}
	for _, want := range []string{"customers", "employees"} {
		if !named[want] {
			t.Errorf("%q is not declared unbuilt, so the screen would show it as zero", want)
		}
	}

	// Purchases, suppliers and payables are built now, so they must NOT be
	// declared unbuilt — a screen saying "coming later" about a module the user
	// can already open is worse than saying nothing.
	for _, built := range []string{"purchases", "suppliers", "payables"} {
		if named[built] {
			t.Errorf("%q is declared unbuilt but the module ships", built)
		}
	}

	// And no payables figure is invented.
	money, _ := body["money"].(map[string]any)
	if _, present := money["payable"]; present {
		t.Error("a payables figure is reported for a module that does not exist")
	}
}

// The dashboard shows margin and cash position, which is exactly what
// accounting.view protects. A cashier must not learn the shop's margin.
func TestDashboardIsGatedOnAccountingView(t *testing.T) {
	var found bool
	for _, rt := range (&Server{}).Routes() {
		if rt.Method != "GET" || rt.Pattern != "/api/v1/dashboard/overview" {
			continue
		}
		found = true
		if rt.Permission != "accounting.view" {
			t.Errorf("the dashboard is gated on %q, want accounting.view", rt.Permission)
		}
	}
	if !found {
		t.Fatal("the dashboard route is not registered")
	}
}

// M7, on the surface an owner cares about most. Frontend hiding is never the
// control; the server refuses.
func TestCashierCannotReadTheDashboard(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "GET",
		"/api/v1/dashboard/overview?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != 403 {
		t.Errorf("a cashier got %d from the dashboard, want 403", resp.StatusCode)
	}
}

// M8. Another tenant's company is not found, not merely empty.
func TestDashboardCannotReadAnotherTenantsCompany(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, "GET",
		"/api/v1/dashboard/overview?company_id="+theirs.companyID.String(),
		mine.token, nil)
	if resp.StatusCode == 200 {
		t.Fatal("an owner read another tenant's dashboard")
	}
	if resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Errorf("got %d, want 404 or 403", resp.StatusCode)
	}
}

func TestDashboardRefusesAMalformedDate(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, "GET",
		"/api/v1/dashboard/overview?company_id="+f.companyID.String()+
			"&date=last%20tuesday", f.token, nil)
	if resp.StatusCode != 400 {
		t.Errorf("got %d for a malformed date, want 400", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "2026") {
		t.Errorf("the refusal does not show the expected shape: %s", body)
	}
}

// Money crosses as a string here as everywhere. float64 cannot hold 0.15.
func TestDashboardMoneyIsAlwaysAString(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	body := overview(t, h, f, "")

	// Counts are genuinely numbers — it is the AMOUNTS that must never be, so
	// they are named rather than swept up by type.
	for _, field := range []struct{ block, name string }{
		{"money", "cash"}, {"money", "bank"}, {"money", "unsettled"},
		{"money", "receivable"}, {"money", "store_credit"}, {"money", "total"},
		{"profit", "revenue"}, {"profit", "cost"}, {"profit", "gross"},
		{"inventory", "value"},
		{"sales", "total"}, {"sales", "yesterday"},
		{"expenses", "total"},
	} {
		block, _ := body[field.block].(map[string]any)
		value, present := block[field.name]
		if !present {
			t.Errorf("%s.%s is missing", field.block, field.name)
			continue
		}
		if _, isString := value.(string); !isString {
			t.Errorf("%s.%s came back as %T, not a string — float64 cannot "+
				"hold 0.15", field.block, field.name, value)
		}
	}
}

func amountsEqual(got, want string) bool {
	if got == "" {
		return false
	}
	g, err := decimal.NewFromString(got)
	if err != nil {
		return false
	}
	return g.Equal(decimal.RequireFromString(want))
}
