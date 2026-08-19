//go:build integration

package api

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Selling on account from a till.
//
// The AR milestone made the server refuse a sale that breaches a credit limit
// and refuse one that names nobody. This covers the other half: a till that can
// actually satisfy those rules, online through /api/v1/pos/sales and offline
// through the sync queue — and the invariant holding across both.

// --- The snapshot a till caches -------------------------------------------

// A till has no company to name; its authority is the device in its token.
func TestTheCustomerSnapshotResolvesTheCompanyFromTheTerminal(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "500.00")

	// f.token is device-bound, exactly as a POS carries it, and names no company.
	resp := h.do(t, "GET", "/api/v1/customers/snapshot", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot from a terminal: %d %s", resp.StatusCode, readBody(t, resp))
	}

	items, _ := decodeJSON(t, resp)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the snapshot returned %d customers, want 1", len(items))
	}
	row, _ := items[0].(map[string]any)

	// What a till needs to decide a credit sale, and nothing it does not.
	for _, want := range []string{"id", "code", "name", "credit_limit", "balance", "available"} {
		if _, present := row[want]; !present {
			t.Errorf("the snapshot omits %q, which a till needs", want)
		}
	}
	// A cache on every till must not carry more of a customer than selling needs.
	for _, absent := range []string{"address", "email", "notes"} {
		if _, present := row[absent]; present {
			t.Errorf("the snapshot carries %q onto every till", absent)
		}
	}
	if got, _ := row["available"].(string); !amountsEqual(got, "500.00") {
		t.Errorf("available = %q, want 500.00", got)
	}
}

// Cursored, so a till that has been off for a week pulls the difference.
func TestTheCustomerSnapshotIsADelta(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "500.00")

	first := decodeJSON(t, h.do(t, "GET", "/api/v1/customers/snapshot", f.token, nil))
	since, _ := first["next_since"].(string)
	sinceID, _ := first["next_since_id"].(string)
	if since == "" || sinceID == "" {
		t.Fatalf("the first pull returned no cursor: %v", first)
	}

	// QueryEscape, not Sprintf: the timestamp carries a +06:00 offset and a raw
	// "+" in a query string decodes as a space, which silently sends the server
	// an unparseable cursor and makes an empty delta look like a caught-up till.
	cursor := "?since=" + url.QueryEscape(since) +
		"&since_id=" + url.QueryEscape(sinceID)

	// Nothing has changed, so the delta is empty and the till keeps its cursor.
	caughtUp := decodeJSON(t, h.do(t, "GET",
		"/api/v1/customers/snapshot"+cursor, f.token, nil))
	if items, _ := caughtUp["items"].([]any); len(items) != 0 {
		t.Errorf("a caught-up till was sent %d customers again", len(items))
	}
	if _, present := caughtUp["next_since"]; present {
		t.Error("an empty delta reset the cursor, so the next pull would start over")
	}

	// Add one, and only that one comes back.
	created := h.do(t, "POST", f.path("/api/v1/customers"), f.token, map[string]any{
		"code": "SECOND", "name": "Bright Star Co", "payment_terms_days": 0,
	})
	if created.StatusCode != 201 {
		t.Fatalf("second customer: %s", readBody(t, created))
	}

	delta := decodeJSON(t, h.do(t, "GET",
		"/api/v1/customers/snapshot"+cursor, f.token, nil))
	items, _ := delta["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the delta returned %d customers, want just the new one", len(items))
	}
	row, _ := items[0].(map[string]any)
	if name, _ := row["name"].(string); name != "Bright Star Co" {
		t.Errorf("the delta returned %q, want the newly added customer", name)
	}
}

// A retired customer travels in the delta rather than being filtered out.
// Omitted, they would stay in the till's cache forever and the cashier would
// keep selling to somebody the shop has stopped dealing with.
func TestARetiredCustomerTravelsInTheDelta(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "500.00")

	if resp := h.do(t, "POST", f.path("/api/v1/customers/"+f.customerID+"/active"),
		f.token, map[string]any{"active": false}); resp.StatusCode != 200 {
		t.Fatalf("retire: %s", readBody(t, resp))
	}

	items, _ := decodeJSON(t, h.do(t, "GET",
		"/api/v1/customers/snapshot", f.token, nil))["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the retired customer was dropped from the snapshot")
	}
	row, _ := items[0].(map[string]any)
	if active, _ := row["is_active"].(bool); active {
		t.Error("the snapshot reports a retired customer as active")
	}
}

// A cashier is deliberately allowed to find the customer in front of them.
func TestACashierCanSearchCustomersButNotChangeThem(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "500.00")
	cashier := h.seedUserIn(t, f.shopFixture, "cashier")

	found := h.do(t, "GET", f.path("/api/v1/customers")+"&search=Noor", cashier, nil)
	if found.StatusCode != 200 {
		t.Fatalf("a cashier could not search customers: %d %s",
			found.StatusCode, readBody(t, found))
	}

	// But not open an account, and not decide what anybody may owe.
	blocked := h.do(t, "POST", f.path("/api/v1/customers"), cashier,
		map[string]any{"code": "NEW", "name": "Somebody"})
	if blocked.StatusCode != 403 {
		t.Errorf("a cashier created a customer (%d)", blocked.StatusCode)
	}
	raise := h.do(t, "POST",
		f.path("/api/v1/customers/"+f.customerID+"/credit-limit"), cashier,
		map[string]any{"credit_limit": "999999.00"})
	if raise.StatusCode != 403 {
		t.Errorf("a cashier set a credit limit (%d)", raise.StatusCode)
	}
}

// --- The offline path ------------------------------------------------------

// creditItem is what a till queues for a sale wholly on account.
func creditItem(
	f *arFixture, seq int64, invoiceUUID, customerID, price string,
) map[string]any {
	return map[string]any{
		"seq":         seq,
		"entity_uuid": invoiceUUID,
		"entity_type": "sales_invoice",
		"payload": map[string]any{
			"invoice_uuid": invoiceUUID,
			"doc_type":     "simplified",
			"issued_at":    fmt.Sprintf("2026-08-15T09:%02d:00Z", seq%60),
			"cashier_id":   f.userID.String(),
			"customer_id":  customerID,
			"lines": []map[string]any{{
				"variant_id":    f.variantID.String(),
				"description":   "Abaya",
				"qty":           "1",
				"unit_price":    price,
				"tax_treatment": "standard",
			}},
			"tenders": []map[string]any{{"method": "customer_due", "amount": price}},
		},
	}
}

// A sale rung up offline lands on the right customer's account.
func TestAnOfflineCreditSaleLandsOnTheCustomersAccount(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	out := h.push(t, f.shopFixture, "credit-batch-1",
		creditItem(f, 1, newUUID().String(), f.customerID, "115.00"),
		creditItem(f, 2, newUUID().String(), f.customerID, "230.00"))

	if applied, _ := out["applied"].(float64); int(applied) != 2 {
		t.Fatalf("applied %v of 2 queued credit sales: %v", applied, out)
	}

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "345.00") {
		t.Errorf("balance after two offline credit sales = %q, want 345.00", got)
	}

	// And the invariant holds across the replay, which is the point.
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("after replaying offline credit sales the receivable is out by %s", d)
	}
}

// The queue is idempotent for credit sales too: a batch sent twice must not
// double what the customer owes.
func TestAReplayedOfflineCreditSaleDoesNotDoubleTheDebt(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	item := creditItem(f, 1, newUUID().String(), f.customerID, "115.00")
	h.push(t, f.shopFixture, "credit-retry", item)
	again := h.push(t, f.shopFixture, "credit-retry-2", item)

	if dup, _ := again["duplicates"].(float64); int(dup) != 1 {
		t.Errorf("the resent sale was not recognised as a duplicate: %v", again)
	}

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("balance = %q, want 115.00 — the retry was charged twice", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("after a retried credit sale the receivable is out by %s", d)
	}
}

// The server is the authority, not the till.
//
// A terminal checks against the balance it last pulled. When that is stale —
// another till sold to the same customer in the meantime — the sale arrives
// over the limit and must be REFUSED on replay rather than quietly applied,
// because the limit is the shop's real exposure and the cached figure was only
// ever an estimate.
func TestAnOfflineCreditSaleOverTheLimitIsRefusedOnReplay(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "200.00")

	// Sold up to the limit while the offline till knew nothing about it.
	if _, s := sellOnAccount(t, h, f, "200.00", "200.00", "0.00"); s != 201 {
		t.Fatal("the first sale was refused")
	}

	out := h.push(t, f.shopFixture, "stale-credit",
		creditItem(f, 1, newUUID().String(), f.customerID, "150.00"))

	if failed, _ := out["failed"].(float64); int(failed) != 1 {
		t.Fatalf("a sale past the limit was applied on replay: %v", out)
	}

	// And it says why, so the exception queue has something a person can act on
	// rather than a code.
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	reason, _ := first["error"].(string)
	if !strings.Contains(reason, "200.00") || !strings.Contains(reason, "limit") {
		t.Errorf("the refusal does not explain the limit: %q", reason)
	}

	// Nothing moved.
	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "200.00") {
		t.Errorf("balance = %q, want 200.00 — the refused sale still landed", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("a refused replay moved the receivable by %s", d)
	}
}

// A refused replay must not consume a chain position either. An ICV cannot be
// handed back and a gap is permanent.
func TestARefusedReplayConsumesNoChainPosition(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100.00")

	before := chainCount(t, h, f)
	out := h.push(t, f.shopFixture, "no-icv",
		creditItem(f, 1, newUUID().String(), f.customerID, "5000.00"))
	if failed, _ := out["failed"].(float64); int(failed) != 1 {
		t.Fatalf("the sale was not refused: %v", out)
	}
	if after := chainCount(t, h, f); after != before {
		t.Errorf("a refused replay consumed %d chain positions", after-before)
	}
}

// An offline sale that names nobody but pays on account cannot be applied.
func TestAnOfflineSaleOnAccountWithNoCustomerIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	item := creditItem(f, 1, newUUID().String(), f.customerID, "115.00")
	payload, _ := item["payload"].(map[string]any)
	delete(payload, "customer_id")

	out := h.push(t, f.shopFixture, "anonymous-credit", item)
	if failed, _ := out["failed"].(float64); int(failed) != 1 {
		t.Fatalf("an anonymous sale on account was applied: %v", out)
	}
}

// A malformed customer id is refused rather than silently dropped, which would
// turn a credit sale into a cash sale nobody agreed to.
func TestAnOfflineSaleWithAnUnreadableCustomerIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	item := creditItem(f, 1, newUUID().String(), "not-a-uuid", "115.00")
	out := h.push(t, f.shopFixture, "bad-customer", item)
	if failed, _ := out["failed"].(float64); int(failed) != 1 {
		t.Fatalf("a sale naming an unreadable customer was applied: %v", out)
	}
}

// --- Online, and the whole journey ----------------------------------------

// Sale on account → receivable → the customer's own ledger, in one pass.
func TestACreditSaleReachesTheLedgerAndTheAgeing(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("credit sale: %s", invoiceID)
	}

	// The invoice knows who owes it.
	var owner string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT customer_id::text FROM sales_invoice WHERE id = $1`,
			invoiceID).Scan(&owner)
	}); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if owner != f.customerID {
		t.Errorf("the invoice names customer %q, want %q", owner, f.customerID)
	}

	// It is collectable: it appears as an open invoice a receipt can settle.
	open, _ := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID+"/open-invoices"),
		f.token, nil))["data"].([]any)
	if len(open) != 1 {
		t.Fatalf("the credit sale produced %d open invoices, want 1", len(open))
	}

	// And it is on the ledger and in the ageing.
	rows, _ := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID+"/ledger"),
		f.token, nil))["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the ledger shows %d rows, want the sale", len(rows))
	}
	ageing := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/receivables/ageing"), f.token, nil))
	if got, _ := ageing["total"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("ageing total = %q, want 115.00", got)
	}
}

// A split sale draws down only the part that went on account.
func TestASplitSaleOnlyDrawsDownWhatWentOnAccount(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100.00")

	// 300 total, 250 in cash and 50 on account. The 50 fits inside a 100 limit
	// even though the total is three times it.
	if _, s := sellOnAccount(t, h, f, "300.00", "50.00", "250.00"); s != 201 {
		t.Fatal("a split sale inside the limit was refused")
	}

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "50.00") {
		t.Errorf("balance = %q, want 50.00 — the whole sale was put on account", got)
	}
	if got, _ := body["available"].(string); !amountsEqual(got, "50.00") {
		t.Errorf("available = %q, want 50.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("after a split credit sale the receivable is out by %s", d)
	}
}
