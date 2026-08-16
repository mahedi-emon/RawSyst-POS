//go:build integration

package api

import (
	"testing"

	"github.com/shopspring/decimal"
)

// The drill-through screens behind the dashboard.
//
// The property that matters throughout: each detail list must sum to the tile
// it sits behind. A detail screen filtered slightly differently from its
// summary is worse than no detail screen at all, because it makes an owner
// believe the summary is wrong.

func get(t *testing.T, h *harness, f *shopFixture, path string) map[string]any {
	t.Helper()
	sep := "?"
	if len(path) > 0 && containsQuery(path) {
		sep = "&"
	}
	resp := h.do(t, "GET", path+sep+"company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

func containsQuery(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '?' {
			return true
		}
	}
	return false
}

// --- Sales detail --------------------------------------------------------

func TestSalesDetailSumsToTheDashboardTile(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for i := 0; i < 3; i++ {
		if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != 201 {
			t.Fatalf("sale: %s", readBody(t, resp))
		}
	}

	detail := get(t, h, f, "/api/v1/dashboard/sales?date=2026-08-15")
	tile, _ := get(t, h, f, "/api/v1/dashboard/overview?date=2026-08-15")["sales"].(map[string]any)

	// The list and the tile are the same figure or the owner trusts neither.
	if !amountsEqual(detail["sales_total"].(string), tile["total"].(string)) {
		t.Errorf("sales detail says %v, the tile says %v",
			detail["sales_total"], tile["total"])
	}

	rows, _ := detail["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("listed %d invoices, want 3", len(rows))
	}

	// And the rows themselves add up to the stated total.
	sum := decimal.Zero
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		sum = sum.Add(decimal.RequireFromString(row["total_inclusive"].(string)))
	}
	if !sum.Equal(decimal.RequireFromString(detail["sales_total"].(string))) {
		t.Errorf("the rows come to %s against a stated total of %v",
			sum, detail["sales_total"])
	}
}

// Refunds are shown, not hidden, and kept apart from sales. Netting them into
// one figure hides a day where a lot was sold and a lot came back — exactly the
// day an owner needs to see.
func TestSalesDetailKeepsRefundsSeparateAndVisible(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	invoiceID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")
	if resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00+03:00",
		"reason":              "changed their mind",
		"lines":               []any{map[string]any{"line_id": lineID, "qty": "1"}},
		"refunds":             []any{map[string]any{"method": "cash", "amount": "115.00"}},
	}); resp.StatusCode != 201 {
		t.Fatalf("refund: %s", readBody(t, resp))
	}

	detail := get(t, h, f, "/api/v1/dashboard/sales?date=2026-08-15")

	if !amountsEqual(detail["sales_total"].(string), "230.00") {
		t.Errorf("sales total %v, want 230.00 — the refund must not net it down",
			detail["sales_total"])
	}
	if !amountsEqual(detail["refund_total"].(string), "115.00") {
		t.Errorf("refund total %v, want 115.00", detail["refund_total"])
	}
	if !amountsEqual(detail["net_total"].(string), "115.00") {
		t.Errorf("net total %v, want 115.00", detail["net_total"])
	}

	// The credit note is a row in the list, flagged so the screen can style it.
	rows, _ := detail["rows"].([]any)
	var credits int
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if flagged, _ := row["is_credit_note"].(bool); flagged {
			credits++
		}
	}
	if credits != 1 {
		t.Errorf("found %d credit notes in the list, want 1 — hiding reversals "+
			"would make the list disagree with the books", credits)
	}
}

func TestSalesDetailShowsHowEachSaleWasPaid(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{
		{"method": "cash", "amount": "50.00"},
		{"method": "mada", "amount": "65.00"},
	}
	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale); resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	rows, _ := get(t, h, f, "/api/v1/dashboard/sales?date=2026-08-15")["rows"].([]any)
	row, _ := rows[0].(map[string]any)

	tenders, _ := row["tenders"].(string)
	if tenders != "Cash + Mada" && tenders != "Mada + Cash" {
		t.Errorf("tender summary is %q, want both methods named", tenders)
	}
}

func TestSalesDetailIsEmptyRatherThanAbsentOnAQuietDay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	detail := get(t, h, f, "/api/v1/dashboard/sales?date=2020-01-01")

	// An empty ARRAY, never null: a screen mapping over null crashes, and the
	// distinction costs nothing to get right on this side.
	rows, ok := detail["rows"].([]any)
	if !ok {
		t.Fatalf("rows came back as %T, want an empty array", detail["rows"])
	}
	if len(rows) != 0 {
		t.Errorf("found %d rows on a day with no trading", len(rows))
	}
	if !amountsEqual(detail["sales_total"].(string), "0.00") {
		t.Errorf("sales total %v on a quiet day, want 0.00", detail["sales_total"])
	}
}

// --- Expenses ------------------------------------------------------------

func TestExpensesDetailSumsToTheDashboardTile(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	detail := get(t, h, f, "/api/v1/dashboard/expenses?date=2026-08-15")
	tile, _ := get(t, h, f, "/api/v1/dashboard/overview?date=2026-08-15")["expenses"].(map[string]any)

	if !amountsEqual(detail["total"].(string), tile["total"].(string)) {
		t.Errorf("expenses detail says %v, the tile says %v",
			detail["total"], tile["total"])
	}
}

// Cost of sales is excluded here exactly as it is on the tile. It is already
// counted as the profit tile's cost, and showing it again under "expenses"
// would double it in the owner's head.
func TestExpensesDetailExcludesCostOfSales(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	detail := get(t, h, f, "/api/v1/dashboard/expenses?date=2026-08-15")
	byAccount, _ := detail["by_account"].([]any)

	for _, raw := range byAccount {
		line, _ := raw.(map[string]any)
		if code, _ := line["code"].(string); code == "5100" {
			t.Error("cost of goods sold appears under expenses; it is already " +
				"the profit tile's cost and would be counted twice")
		}
	}

	// Asserted rather than skipped past. If the sale posted no cost then this
	// test proves nothing, and a test that quietly proves nothing is worse than
	// a missing one — it reports green while the exclusion goes unchecked.
	profit, _ := get(t, h, f, "/api/v1/dashboard/overview?date=2026-08-15")["profit"].(map[string]any)
	if amountsEqual(profit["cost"].(string), "0.00") {
		t.Fatal("the sale posted no cost of sales, so the exclusion above was " +
			"never actually exercised")
	}
}

func TestExpensesDetailNarrowsToOneAccount(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	detail := get(t, h, f,
		"/api/v1/dashboard/expenses?date=2026-08-15&account_id="+f.companyID.String())

	// A company id is a valid uuid but no account, so the filter matches
	// nothing rather than being ignored — a filter that silently falls back to
	// everything is how a drill-through lies.
	entries, _ := detail["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("a filter matching no account returned %d entries", len(entries))
	}
	if got, _ := detail["account_id"].(string); got != f.companyID.String() {
		t.Errorf("the filter is not echoed back; the screen cannot show what it narrowed to")
	}
}

func TestExpensesDetailRefusesAMalformedAccount(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, "GET",
		"/api/v1/dashboard/expenses?company_id="+f.companyID.String()+
			"&account_id=not-a-uuid", f.token, nil)
	if resp.StatusCode != 400 {
		t.Errorf("got %d for a malformed account id, want 400", resp.StatusCode)
	}
}

// --- Compliance ----------------------------------------------------------

// The screen must never imply a retry will fix what a retry cannot fix. P1 is
// open: the terminal refuses to sign because the canonicalisation and QR TLV
// encoding are unverified, and every queued invoice waits on that rather than
// on a flaky network.
func TestComplianceQueueStatesTheSigningGateHonestly(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	queue := get(t, h, f, "/api/v1/dashboard/compliance")

	available, present := queue["signing_available"].(bool)
	if !present {
		t.Fatal("the queue does not report whether signing is available")
	}
	if available {
		t.Error("the compliance queue reports signing as available while the " +
			"P1 verification gate is open")
	}
}

func TestComplianceQueueListsUnreportedInvoices(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	queue := get(t, h, f, "/api/v1/dashboard/compliance")
	rows, _ := queue["rows"].([]any)

	if len(rows) == 0 {
		t.Fatal("a signed-pending-report invoice is not in the compliance queue")
	}

	row, _ := rows[0].(map[string]any)
	// The chain position, because a gap in the sequence is the exact signal
	// tamper detection looks for.
	if _, present := row["icv"]; !present {
		t.Error("no ICV reported; an owner chasing a stuck invoice cannot see " +
			"where in the chain it sits")
	}
	if _, present := row["age_hours"]; !present {
		t.Error("no age reported, so the 12/24/72-hour escalation cannot be shown")
	}
	if n, _ := queue["outstanding"].(float64); int(n) < 1 {
		t.Errorf("outstanding count is %v against a queue with rows", n)
	}
}

// Cleared and reported invoices have finished. Listing them would bury the
// ones that need attention.
func TestComplianceQueueExcludesFinishedInvoices(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	rows, _ := get(t, h, f, "/api/v1/dashboard/compliance")["rows"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		switch row["state"] {
		case "cleared", "reported", "draft", "cancelled":
			t.Errorf("a %v invoice is in the outstanding queue", row["state"])
		}
	}
}

// --- Stock ---------------------------------------------------------------

func TestStockDetailRefusesAnUnknownFilter(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, "GET",
		"/api/v1/dashboard/stock?company_id="+f.companyID.String()+
			"&filter=everything", f.token, nil)
	if resp.StatusCode != 400 {
		t.Errorf("got %d for an unknown filter, want 400", resp.StatusCode)
	}
}

func TestStockDetailAgreesWithTheDashboardCounts(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	inventory, _ := get(t, h, f, "/api/v1/dashboard/overview")["inventory"].(map[string]any)

	for _, pair := range []struct {
		filter string
		field  string
	}{
		{"low", "low_stock"},
		{"out", "out_of_stock"},
	} {
		detail := get(t, h, f, "/api/v1/dashboard/stock?filter="+pair.filter)
		listed, _ := detail["count"].(float64)
		counted, _ := inventory[pair.field].(float64)
		if listed != counted {
			t.Errorf("%s: the list has %v rows but the dashboard counted %v",
				pair.filter, listed, counted)
		}
	}
}

// --- RBAC and tenancy ----------------------------------------------------

// Each detail screen is gated on the permission covering the RECORDS it shows,
// not on the dashboard's accounting.view. A role holding one and not the other
// is an ordinary arrangement.
func TestDrillThroughRoutesAreGatedPerSurface(t *testing.T) {
	want := map[string]string{
		"/api/v1/dashboard/sales":      "sales.view",
		"/api/v1/dashboard/expenses":   "accounting.view",
		"/api/v1/dashboard/compliance": "accounting.view",
		"/api/v1/dashboard/stock":      "inventory.view",
	}

	found := map[string]bool{}
	for _, rt := range (&Server{}).Routes() {
		expected, watched := want[rt.Pattern]
		if !watched || rt.Method != "GET" {
			continue
		}
		found[rt.Pattern] = true
		if rt.Permission != expected {
			t.Errorf("%s is gated on %q, want %q", rt.Pattern, rt.Permission, expected)
		}
	}
	for pattern := range want {
		if !found[pattern] {
			t.Errorf("%s is not registered", pattern)
		}
	}
}

// M7, on every drill-through. Frontend hiding is never the control.
func TestCashierCannotDrillIntoTheFigures(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// A cashier holds sales.view, so the sales list is legitimately theirs.
	// The accounting surfaces are not.
	for _, path := range []string{
		"/api/v1/dashboard/expenses",
		"/api/v1/dashboard/compliance",
	} {
		resp := h.do(t, "GET", path+"?company_id="+f.companyID.String(), f.token, nil)
		if resp.StatusCode != 403 {
			t.Errorf("a cashier got %d from %s, want 403", resp.StatusCode, path)
		}
	}
}

// M8, on every drill-through. Another tenant's company is not found.
func TestDrillThroughCannotCrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	for _, path := range []string{
		"/api/v1/dashboard/sales",
		"/api/v1/dashboard/expenses",
		"/api/v1/dashboard/compliance",
		"/api/v1/dashboard/stock",
	} {
		resp := h.do(t, "GET",
			path+"?company_id="+theirs.companyID.String(), mine.token, nil)
		if resp.StatusCode == 200 {
			t.Errorf("%s returned another tenant's data", path)
			continue
		}
		if resp.StatusCode != 404 && resp.StatusCode != 403 {
			t.Errorf("%s returned %d, want 404 or 403", path, resp.StatusCode)
		}
	}
}

// Money crosses as a string on every drill-through, as it does everywhere.
func TestDrillThroughMoneyIsAlwaysAString(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	sales := get(t, h, f, "/api/v1/dashboard/sales?date=2026-08-15")
	for _, field := range []string{"sales_total", "refund_total", "net_total", "tax_total"} {
		if _, ok := sales[field].(string); !ok {
			t.Errorf("sales.%s came back as %T, not a string — float64 cannot "+
				"hold 0.15", field, sales[field])
		}
	}

	expenses := get(t, h, f, "/api/v1/dashboard/expenses?date=2026-08-15")
	if _, ok := expenses["total"].(string); !ok {
		t.Errorf("expenses.total came back as %T, not a string", expenses["total"])
	}
}
