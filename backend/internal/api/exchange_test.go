//go:build integration

package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// An exchange end to end: a credit note and an invoice, one transaction, and
// only the difference moving.
//
// These go through HTTP rather than calling the service, because the things
// most likely to be wrong are at the seams — the tenders the handler builds,
// the chain positions the pair takes, and what the drawer is told.

// sellOne rings up a sale and returns its invoice id and first line id.
func sellOne(t *testing.T, h *harness, f *shopFixture, qty, price, paid string) (string, string) {
	t.Helper()

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), qty, price, paid))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID, _ := decodeJSON(t, created)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}
	return invoiceID, lineID
}

func exchangeBody(
	f *shopFixture, originalID, lineID, returnQty string,
	replacementQty, replacementPrice string, settlement []map[string]any,
) map[string]any {
	return map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"invoice_uuid":        newUUID().String(),
		"original_invoice_id": originalID,
		"issued_at":           "2026-08-16T11:00:00Z",
		"reason":              "wrong size",
		"returning": []map[string]any{
			{"line_id": lineID, "qty": returnQty},
		},
		"replacement": map[string]any{
			"doc_type": "simplified",
			"lines": []map[string]any{{
				"variant_id":    f.variantID.String(),
				"description":   "Abaya",
				"qty":           replacementQty,
				"unit_price":    replacementPrice,
				"tax_treatment": "standard",
			}},
		},
		"settlement": settlement,
	}
}

// The ordinary case. A 115 item swapped for a 230 one: the customer pays 115,
// and the drawer records 115 — not 230 in and 115 out.
func TestExchangeUpwardTakesOnlyTheDifference(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")

	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "230.00",
			[]map[string]any{{"method": "cash", "amount": "115.00"}}))
	if resp.StatusCode != 201 {
		t.Fatalf("exchange: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if got, _ := body["difference"].(string); !strings.HasPrefix(got, "115") {
		t.Errorf("difference = %q, want 115", got)
	}
	if paid, _ := body["customer_paid"].(bool); !paid {
		t.Error("customer_paid is false on an upward swap")
	}

	// Both documents exist and are linked.
	note, _ := body["credit_note"].(map[string]any)
	replacement, _ := body["replacement"].(map[string]any)
	if note["credit_note_id"] == nil || replacement["invoice_id"] == nil {
		t.Fatalf("an exchange did not produce both documents: %v", body)
	}

	// The drawer sees 115 from the original sale and 115 from the exchange.
	// Not 345, which is what recording the offset as cash would produce.
	if got := cashThroughDrawer(t, h, f); !got.Equal(decimal.RequireFromString("230.00")) {
		t.Errorf("cash through the drawer = %s, want 230.00 "+
			"(115 for the sale, 115 for the difference)", got)
	}
}

// Swapping down: the shop hands back the difference, and only that.
func TestExchangeDownwardPaysOutOnlyTheDifference(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "230.00", "230.00")

	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "115.00",
			[]map[string]any{{"method": "cash", "amount": "115.00"}}))
	if resp.StatusCode != 201 {
		t.Fatalf("exchange: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if got, _ := body["difference"].(string); !strings.HasPrefix(got, "-115") {
		t.Errorf("difference = %q, want -115", got)
	}
	if paid, _ := body["customer_paid"].(bool); paid {
		t.Error("customer_paid is true on a downward swap")
	}

	// 230 in, 115 back out.
	if got := cashThroughDrawer(t, h, f); !got.Equal(decimal.RequireFromString("115.00")) {
		t.Errorf("net cash = %s, want 115.00", got)
	}
}

// An even swap moves no money at all. The commonest exchange there is — wrong
// size, same price — and the one where recording gross movements would be most
// obviously wrong.
func TestEvenExchangeMovesNoCash(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")
	before := cashThroughDrawer(t, h, f)

	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "115.00", nil))
	if resp.StatusCode != 201 {
		t.Fatalf("exchange: %d %s", resp.StatusCode, readBody(t, resp))
	}

	if got := cashThroughDrawer(t, h, f); !got.Equal(before) {
		t.Errorf("the drawer moved from %s to %s on an even swap", before, got)
	}
}

// The invariant the mechanism rests on. Whatever shape the exchange took, the
// clearing account is empty afterwards.
func TestExchangeLeavesTheClearingAccountEmpty(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, c := range []struct {
		name              string
		soldAt, replaceAt string
		settle            string
	}{
		{"upward", "115.00", "230.00", "115.00"},
		{"downward", "230.00", "115.00", "115.00"},
		{"even", "115.00", "115.00", ""},
	} {
		originalID, lineID := sellOne(t, h, f, "1", c.soldAt, c.soldAt)

		var settlement []map[string]any
		if c.settle != "" {
			settlement = []map[string]any{{"method": "cash", "amount": c.settle}}
		}
		resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
			exchangeBody(f, originalID, lineID, "1", "1", c.replaceAt, settlement))
		if resp.StatusCode != 201 {
			t.Fatalf("%s: %s", c.name, readBody(t, resp))
		}

		if bal := clearingBalance(t, h, f); !bal.IsZero() {
			t.Errorf("%s exchange left %s in the clearing account", c.name, bal)
		}
	}
}

// Both halves take their own position on the terminal's ZATCA chain, in order.
// A credit note is an invoice in ZATCA's eyes (E1) and skipping it would break
// the counter's continuity — which is exactly what tamper detection looks for.
func TestExchangeAdvancesTheChainTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")
	before := lastICV(t, h, f)

	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "230.00",
			[]map[string]any{{"method": "cash", "amount": "115.00"}}))
	if resp.StatusCode != 201 {
		t.Fatalf("exchange: %s", readBody(t, resp))
	}

	if after := lastICV(t, h, f); after != before+2 {
		t.Errorf("the chain went from %d to %d, want two positions "+
			"(the credit note and the replacement)", before, after)
	}
}

// Idempotency covers the PAIR. A retry must not sell the replacement a second
// time, which is what would happen if each half were deduplicated alone.
func TestRetryingAnExchangeDoesNotSellTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")
	body := exchangeBody(f, originalID, lineID, "1", "1", "230.00",
		[]map[string]any{{"method": "cash", "amount": "115.00"}})

	first := h.do(t, "POST", "/api/v1/pos/exchanges", f.token, body)
	if first.StatusCode != 201 {
		t.Fatalf("exchange: %s", readBody(t, first))
	}
	firstBody := decodeJSON(t, first)

	second := h.do(t, "POST", "/api/v1/pos/exchanges", f.token, body)
	if second.StatusCode != 200 {
		t.Fatalf("retry returned %d, want 200 for a recognised repeat: %s",
			second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("a replayed exchange did not say so")
	}
	secondBody := decodeJSON(t, second)

	// The SAME documents, not new ones.
	firstNote, _ := firstBody["credit_note"].(map[string]any)
	secondNote, _ := secondBody["credit_note"].(map[string]any)
	if firstNote["credit_note_id"] != secondNote["credit_note_id"] {
		t.Error("the retry issued a second credit note")
	}
	firstSale, _ := firstBody["replacement"].(map[string]any)
	secondSale, _ := secondBody["replacement"].(map[string]any)
	if firstSale["invoice_id"] != secondSale["invoice_id"] {
		t.Error("the retry sold the replacement a second time")
	}

	// And the customer was charged once.
	if got := cashThroughDrawer(t, h, f); !got.Equal(decimal.RequireFromString("345.00")) {
		t.Errorf("cash = %s, want 345.00 (230 sale + 115 difference, once)", got)
	}
}

// The server states the difference. A till that could name it could quietly
// undercharge, and nothing downstream would notice.
func TestExchangeRefusesATillsOwnSettlement(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")

	// The real difference is 115. Offering 5 must not buy a 230 abaya.
	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token,
		exchangeBody(f, originalID, lineID, "1", "1", "230.00",
			[]map[string]any{{"method": "cash", "amount": "5.00"}}))
	if resp.StatusCode == 201 {
		t.Fatal("an exchange settled 115.00 of goods for 5.00")
	}
	if body := readBody(t, resp); !strings.Contains(body, "115") {
		t.Errorf("the refusal does not name the real difference: %s", body)
	}
}

// Nothing partial survives a failure. The replacement here cannot be costed
// against stock that does not exist, and the credit note must go with it.
func TestAFailedExchangeIssuesNoCreditNote(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	originalID, lineID := sellOne(t, h, f, "1", "115.00", "115.00")
	notesBefore := creditNoteCount(t, h, f)

	body := exchangeBody(f, originalID, lineID, "1", "1", "230.00",
		[]map[string]any{{"method": "cash", "amount": "115.00"}})
	// A replacement line naming a variant that is not in this catalogue.
	repl, _ := body["replacement"].(map[string]any)
	lines, _ := repl["lines"].([]map[string]any)
	lines[0]["variant_id"] = uuid.NewString()

	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token, body)
	if resp.StatusCode == 201 {
		t.Fatal("an exchange sold a variant that does not exist")
	}

	// Refused for the RIGHT reason, in words a cashier can act on.
	//
	// It used to come back 500, "Something went wrong on our side": the
	// foreign key on stock_movement caught the unknown variant, and the raw
	// driver error travelled all the way out. Nothing is wrong on our side —
	// an item that is not in this catalogue is a barcode to rescan or a
	// product to add, and a cashier told the server is broken calls the shop's
	// IT instead of doing either.
	said := readBody(t, resp)
	if resp.StatusCode >= 500 {
		t.Errorf("scanning an item that is not in the catalogue answered %d, "+
			"which blames the server for the till's input: %s",
			resp.StatusCode, said)
	}
	if !strings.Contains(said, "catalogue") {
		t.Errorf("the refusal does not say what is wrong or what to do "+
			"about it: %s", said)
	}

	if after := creditNoteCount(t, h, f); after != notesBefore {
		t.Errorf("credit notes went from %d to %d after a FAILED exchange — "+
			"the goods were credited and never replaced", notesBefore, after)
	}
}

// Refunding is gated separately from selling, and exchanging separately again.
func TestExchangeIsGatedOnItsOwnPermission(t *testing.T) {
	var found bool
	for _, rt := range (&Server{}).Routes() {
		if rt.Method != "POST" || rt.Pattern != "/api/v1/pos/exchanges" {
			continue
		}
		found = true
		if rt.Permission != "sales.exchange" {
			t.Errorf("exchanges are gated on %q, want sales.exchange", rt.Permission)
		}
	}
	if !found {
		t.Fatal("the exchange route is not registered")
	}
}

// --- reading the shop's state -------------------------------------------

func cashThroughDrawer(t *testing.T, h *harness, f *shopFixture) decimal.Decimal {
	t.Helper()
	var in, out decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT
			  coalesce((SELECT sum(t.amount) FROM sales_tender t
			            JOIN sales_invoice i ON i.id = t.invoice_id
			            WHERE i.company_id = $1 AND i.doc_type <> 'credit_note'
			              AND t.method = 'cash'), 0),
			  coalesce((SELECT sum(r.amount) FROM sales_refund r
			            JOIN sales_invoice n ON n.id = r.credit_note_id
			            WHERE n.company_id = $1 AND r.method = 'cash'), 0)`,
			f.companyID).Scan(&in, &out)
	}); err != nil {
		t.Fatalf("read the drawer: %v", err)
	}
	return in.Sub(out)
}

func clearingBalance(t *testing.T, h *harness, f *shopFixture) decimal.Decimal {
	t.Helper()
	var bal decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT exchange_clearing_balance($1)`, f.companyID).Scan(&bal)
	}); err != nil {
		t.Fatalf("read the clearing account: %v", err)
	}
	return bal
}

func lastICV(t *testing.T, h *harness, f *shopFixture) int {
	t.Helper()
	var icv int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		// Reached through the device, because that is how a sale reaches it:
		// the terminal never names its own EGS unit.
		return tx.QueryRow(t.Context(), `
			SELECT u.last_icv FROM egs_unit u
			JOIN device d ON d.egs_unit_id = u.id
			WHERE d.id = $1`, f.deviceID).Scan(&icv)
	}); err != nil {
		t.Fatalf("read the chain: %v", err)
	}
	return icv
}

func creditNoteCount(t *testing.T, h *harness, f *shopFixture) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM sales_invoice
			 WHERE company_id = $1 AND doc_type = 'credit_note'`,
			f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count credit notes: %v", err)
	}
	return n
}
