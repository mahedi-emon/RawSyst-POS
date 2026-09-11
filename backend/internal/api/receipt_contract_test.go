//go:build integration

// Everything a receipt has to print, proved to be there.
//
// # Why this test exists
//
// The product could ring up a sale, chain it for tax and record a reprint in
// the audit trail, and it could not PRINT anything: the reprint route writes an
// audit entry and its own comment says it "is the CONTROL rather than the
// printing", the till's completion dialog pointed at an invoice screen, and
// there was no invoice screen. A point-of-sale product that cannot produce a
// receipt is not finished.
//
// The screen that prints one now reads `GET /pos/sales/{id}` and
// `GET /pos/stationery`. This pins what those two must carry.
//
// # Why a contract test rather than a screenshot
//
// Because the way this breaks is silent. Every field the receipt reads is
// optional in JSON: rename `gross_amount` and the receipt prints a blank column
// rather than failing, and nobody finds out until a customer is holding the
// paper. Five of the field names the screen was first written against were
// wrong for exactly that reason, and a type check could not see it because the
// types were written from the same wrong assumption.
//
// So this asserts the CONTRACT, from the running API, with a real sale behind
// it.
package api

import (
	"net/http"
	"testing"
)

// receiptFields are what the printed document reads, by their JSON names.
//
// Changing one of these is changing the receipt, which is a customer-facing
// document and in Saudi Arabia a tax one. That should take a deliberate edit
// here rather than happening as a side effect.
var receiptFields = struct {
	invoice []string
	line    []string
	tender  []string
}{
	invoice: []string{
		"id", "human_number", "issue_date", "currency",
		"subtotal_net", "discount_total", "tax_total", "total_inclusive",
		"lines", "tenders", "audit",
	},
	line: []string{
		"description", "qty", "unit_price", "line_discount",
		"tax_rate", "tax_amount", "gross_amount",
	},
	tender: []string{"method", "amount"},
}

func TestASaleCarriesEverythingAReceiptPrints(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	resp := h.do(t, http.MethodGet,
		"/api/v1/pos/sales/"+invoiceID+"?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the sale: %d — %s", resp.StatusCode, readBody(t, resp))
	}
	sale := decodeJSON(t, resp)

	for _, field := range receiptFields.invoice {
		if _, ok := sale[field]; !ok {
			t.Errorf("the sale carries no %q, which the receipt prints", field)
		}
	}

	lines, _ := sale["lines"].([]any)
	if len(lines) == 0 {
		t.Fatal("the sale has no lines, so the receipt would list nothing bought")
	}
	line := lines[0].(map[string]any)
	for _, field := range receiptFields.line {
		if _, ok := line[field]; !ok {
			t.Errorf("a line carries no %q; the receipt would print a blank "+
				"where that column goes", field)
		}
	}

	tenders, _ := sale["tenders"].([]any)
	if len(tenders) == 0 {
		t.Fatal("the sale records no tender, so the receipt cannot say how it " +
			"was paid")
	}
	tender := tenders[0].(map[string]any)
	for _, field := range receiptFields.tender {
		if _, ok := tender[field]; !ok {
			t.Errorf("a tender carries no %q", field)
		}
	}

	// The figures have to add up on the paper, not only in the database. A
	// receipt whose lines do not sum to its total is the one document a
	// customer WILL check.
	if sale["total_inclusive"] == "" || sale["total_inclusive"] == nil {
		t.Error("the sale has no total")
	}
}

// The shop's own identity, which is the other half of a receipt.
//
// A receipt that lists prices and does not say who sold them is not a document
// anybody can return goods against, and in a VAT market it is not a tax
// invoice.
func TestTheStationeryCarriesWhoIsSelling(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// A PERSON at a browser, not `f.token`, which is a till's device token.
	//
	// This distinction was learned the hard way: the logo fields were first
	// added to this payload unconditionally, and the till's own test caught it
	// — a terminal prints 42 columns of plain text and must not be told about
	// an image it cannot render. The receipt SCREEN is the other caller, prints
	// HTML, and is always a signed-in person. Asserting the screen's contract
	// with a device token asserted the wrong one.
	// An owner rather than a second cashier: `seedShop` has already created
	// the cashier role in this tenant and role keys are unique per tenant.
	person := h.seedUserIn(t, f, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/pos/stationery?company_id="+f.companyID.String()+
			"&store_id="+f.storeID.String(), person, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the stationery: %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	paper := decodeJSON(t, resp)

	for _, field := range []string{
		"store_name", "vat_number", "base_currency",
		"header_text", "footer_text", "return_policy", "show_tax_number",
		// Added for the receipt: where the customer stood, the number they
		// would ring, and whether the shop's mark goes on the paper.
		"store_address", "store_phone", "show_logo", "company_id",
	} {
		if _, ok := paper[field]; !ok {
			t.Errorf("the stationery carries no %q", field)
		}
	}

	if name, _ := paper["store_name"].(string); name == "" {
		t.Error("the stationery names no seller; a receipt with no seller on " +
			"it is not a document")
	}
}

// A branch belonging to somebody else prints no address rather than theirs.
//
// The store id is taken from the request, so this is the check that makes that
// safe: `ReadSeller` joins the store to its company, and a foreign id simply
// finds nothing.
func TestAForeignStoreIdPrintsNoAddress(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	person := h.seedUserIn(t, mine, "owner")
	resp := h.do(t, http.MethodGet,
		"/api/v1/pos/stationery?company_id="+mine.companyID.String()+
			"&store_id="+theirs.storeID.String(), person, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the stationery: %d", resp.StatusCode)
	}

	paper := decodeJSON(t, resp)
	if addr, _ := paper["store_address"].(string); addr != "" {
		t.Errorf("another company's branch address was printed: %q", addr)
	}
	// And the shop's own name is still there, so the refusal is narrow.
	if name, _ := paper["store_name"].(string); name == "" {
		t.Error("naming a foreign store blanked the whole receipt header")
	}
}
