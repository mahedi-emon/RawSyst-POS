//go:build integration

// UI spec §5 — the invoice detail screen's payload and its one action.
//
// §5 asks for header, lines, tenders, a ZATCA panel (state, ICV, QR, chain
// position) and an audit trail, plus a reprint that is logged. The read route
// already carried the first three; these cover what it did not, and the
// reprint that spec §5 requires be recorded rather than merely allowed.
//
// The screen shows the server's totals as they arrive rather than re-adding the
// lines, so these assert the server's arithmetic is the thing a customer would
// be holding.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func invoicePath(id string) string { return "/api/v1/pos/sales/" + id }

// sellOneFor rings up a sale and returns its invoice id.
func sellOneFor(t *testing.T, h *harness, f *shopFixture, price string) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", price, price))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["invoice_id"].(string)
	if id == "" {
		t.Fatal("the sale returned no invoice id")
	}
	return id
}

// Everything §5 puts on the page arrives in one read.
func TestTheInvoiceDetailPayloadCarriesWhatTheScreenShows(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := sellOneFor(t, h, f, "115.00")

	resp := h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read invoice: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	// Header.
	for _, field := range []string{
		"id", "uuid", "doc_type", "state", "issue_date", "currency",
		"subtotal_net", "discount_total", "tax_total", "total_inclusive",
	} {
		if body[field] == nil {
			t.Errorf("the payload has no %s; the header cannot render", field)
		}
	}

	// Lines and tenders.
	lines, _ := body["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	tenders, _ := body["tenders"].([]any)
	if len(tenders) != 1 {
		t.Fatalf("tenders = %d, want 1", len(tenders))
	}

	// The screen shows the server's totals rather than re-adding the lines, so
	// the server's arithmetic is what the customer is holding.
	if body["total_inclusive"] != "115" {
		t.Errorf("total = %v, want 115", body["total_inclusive"])
	}

	// The ZATCA panel: a chain position exists as soon as a legal document is
	// issued, and the QR does not until a terminal signs — which is gated. The
	// panel has to be able to tell those apart.
	zatca, ok := body["zatca"].(map[string]any)
	if !ok {
		t.Fatal("the payload carries no zatca block; §5's panel cannot render")
	}
	if zatca["icv"] == nil {
		t.Error("no ICV, so the chain position cannot be shown")
	}
	if zatca["pih"] == nil || zatca["invoice_hash"] == nil {
		t.Error("no hashes, so the chain cannot be shown")
	}
	if _, present := zatca["qr_tlv"]; !present {
		t.Error("the qr_tlv field is absent entirely; the screen cannot tell " +
			"'not signed yet' from 'signed'")
	}
	if zatca["qr_tlv"] != nil {
		t.Errorf("qr_tlv = %v on an unsigned invoice, want null — signing is "+
			"gated and the screen must not render a code that will not scan",
			zatca["qr_tlv"])
	}

	// The audit trail, always an array so the screen never guards for null.
	if _, ok := body["audit"].([]any); !ok {
		t.Errorf("audit = %v, want an array", body["audit"])
	}

	// A walk-in has no customer, and that is a state rather than a gap.
	if body["customer"] != nil {
		t.Errorf("customer = %v on a walk-in sale, want null", body["customer"])
	}
}

// A sale on account names its buyer, which is what "Billed to" reads.
func TestAnInvoiceOnAccountNamesItsCustomer(t *testing.T) {
	h := newHarness(t)
	// seedSelling already builds the full chart with its Accounts Receivable
	// control account and a customer holding credit. Rebuilding that here would
	// be a second fixture drifting from the first.
	f := seedSelling(t, h, "10000.00")

	sale := oneItemSale(f.shopFixture, newUUID(), "1", "115.00", "115.00")
	sale["customer_id"] = f.customerID
	sale["tenders"] = []map[string]any{
		{"method": "customer_due", "amount": "115.00"},
	}
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("credit sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	resp = h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read invoice: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	customer, ok := body["customer"].(map[string]any)
	if !ok {
		t.Fatal("a sale on account carried no customer; \"Billed to\" would read as a walk-in")
	}
	if customer["id"] != f.customerID {
		t.Errorf("customer id = %v, want %s", customer["id"], f.customerID)
	}
	if name, _ := customer["name"].(string); name == "" {
		t.Error("the customer has no name to show")
	}

	// The tender says how it was settled, which is what the payment block reads.
	tenders, _ := body["tenders"].([]any)
	if len(tenders) != 1 {
		t.Fatalf("tenders = %d, want 1", len(tenders))
	}
	if first, _ := tenders[0].(map[string]any); first["method"] != "customer_due" {
		t.Errorf("tender method = %v, want customer_due", first["method"])
	}
}

// Reprint is available and logged — spec §5, and the logging is the point.
//
// Reprinting is not reissuing: no new document, no new number, no new chain
// position. The only thing that changes is the trail.
func TestAReprintIsRecordedAndReissuesNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := sellOneFor(t, h, f, "115.00")

	before := decodeJSON(t, h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil))
	beforeAudit, _ := before["audit"].([]any)

	resp := h.do(t, http.MethodPost, invoicePath(invoiceID)+"/reprint", f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reprint: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	after := decodeJSON(t, h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil))
	afterAudit, _ := after["audit"].([]any)

	if len(afterAudit) != len(beforeAudit)+1 {
		t.Fatalf("audit entries went %d -> %d, want exactly one more",
			len(beforeAudit), len(afterAudit))
	}
	entry, _ := afterAudit[0].(map[string]any)
	if entry["action"] != "invoice_reprinted" {
		t.Errorf("newest audit action = %v, want invoice_reprinted", entry["action"])
	}
	// Denormalised server-side, so the trail survives the user being deleted.
	if entry["actor_label"] == nil || entry["actor_label"] == "" {
		t.Error("the reprint recorded no actor; the trail cannot answer who")
	}

	// Nothing about the document itself moved.
	for _, field := range []string{"uuid", "human_number", "state", "total_inclusive"} {
		if before[field] != after[field] {
			t.Errorf("%s changed on reprint: %v -> %v", field, before[field], after[field])
		}
	}
	beforeZ, _ := before["zatca"].(map[string]any)
	afterZ, _ := after["zatca"].(map[string]any)
	if beforeZ["icv"] != afterZ["icv"] {
		t.Errorf("the chain position moved on a reprint: %v -> %v",
			beforeZ["icv"], afterZ["icv"])
	}

	// A second reprint is another entry, not a refusal: handing the customer a
	// third copy is an ordinary thing to do and each one is recorded.
	resp = h.do(t, http.MethodPost, invoicePath(invoiceID)+"/reprint", f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second reprint: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	twice := decodeJSON(t, h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil))
	if entries, _ := twice["audit"].([]any); len(entries) != len(beforeAudit)+2 {
		t.Fatalf("after two reprints the trail has %d entries, want %d",
			len(entries), len(beforeAudit)+2)
	}
}

// Reprinting something that does not exist puts no fiction in the trail.
func TestReprintingAnUnknownInvoiceIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost,
		invoicePath(uuid.NewString())+"/reprint", f.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reprint of a missing invoice: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	var stray int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM audit_log WHERE action = 'invoice_reprinted'`).
			Scan(&stray)
	}); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if stray != 0 {
		t.Fatalf("a refused reprint wrote %d audit rows; the trail is not evidence "+
			"if it can be written to about nothing", stray)
	}
}

// QA gate M8: one shop cannot read another's invoice, or log against it.
//
// 404 rather than 403, because a 403 would confirm the record exists — which is
// the leak, not the read.
func TestOneShopCannotOpenAnothersInvoice(t *testing.T) {
	h := newHarness(t)
	a := h.seedShop(t, "cashier")
	b := h.seedShop(t, "cashier")

	invoiceID := sellOneFor(t, h, a, "115.00")

	for _, c := range []struct{ name, method, path string }{
		{"read", http.MethodGet, invoicePath(invoiceID)},
		{"reprint", http.MethodPost, invoicePath(invoiceID) + "/reprint"},
	} {
		resp := h.do(t, c.method, c.path, b.token, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("shop B trying to %s shop A's invoice: status %d, want 404 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// And shop A's trail is untouched by the attempt.
	body := decodeJSON(t, h.do(t, http.MethodGet, invoicePath(invoiceID), a.token, nil))
	for _, e := range body["audit"].([]any) {
		if entry, _ := e.(map[string]any); entry["action"] == "invoice_reprinted" {
			t.Fatal("shop B's refused reprint reached shop A's audit trail")
		}
	}
}

// The document is read-only over HTTP: there is no route that edits or deletes
// one, which is what §5's panel promises the reader.
func TestAnInvoiceCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := sellOneFor(t, h, f, "115.00")

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		resp := h.do(t, method, invoicePath(invoiceID), f.token,
			map[string]any{"total_inclusive": "1.00"})
		// 405 from the router, or 404 because no such route is mounted at all.
		// Either is a refusal; what must never happen is a 2xx.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Errorf("%s on a finalised invoice returned %d; the document is "+
				"supposed to be immutable", method, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// And it still reads exactly as it did.
	body := decodeJSON(t, h.do(t, http.MethodGet, invoicePath(invoiceID), f.token, nil))
	if body["total_inclusive"] != "115" {
		t.Fatalf("the invoice total is now %v; something got through",
			body["total_inclusive"])
	}
}

// A cashier may look a sale up and take a copy of it; both are sales.view.
func TestReprintCarriesTheSamePermissionAsLookingASaleUp(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := sellOneFor(t, h, f, "115.00")

	// An inventory keeper holds neither sales.view nor sales.refund.
	keeper := h.seedUserIn(t, f, "inventory_keeper")

	for _, c := range []struct{ name, method, path string }{
		{"read", http.MethodGet, invoicePath(invoiceID)},
		{"reprint", http.MethodPost, invoicePath(invoiceID) + "/reprint"},
	} {
		resp := h.do(t, c.method, c.path, keeper, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a stock keeper trying to %s an invoice: status %d, want 403",
				c.name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
