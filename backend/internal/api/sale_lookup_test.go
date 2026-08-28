//go:build integration

// Finding the sale a customer is holding a receipt for.
//
// # The bug these were written against
//
// Design 11 §7 opens with "the original invoice is always scanned or linked,
// never re-typed (B10)". The till obeyed the second half and had nothing to
// obey the first half with.
//
// A terminal generates the document UUID, queues the sale under it, and prints
// the first eight characters of it on the receipt. sales_invoice.id is a
// different UUID, minted inside Finalize, and it is that one every sales route
// takes. So the returns screen — which asks the cashier to scan the receipt and
// sent what it got straight to /returnable — could not find a single sale any
// till had ever made. The eight characters failed as a malformed UUID; the
// whole document UUID came back "That invoice was not found."; and the id that
// would have worked appears on no receipt and in no response the till sees.
//
// Found by driving the packaged Windows application under tauri-driver
// (e2e/tauri.mjs), which is the only place the two halves meet.
package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// lookUp calls the route the returns screen calls.
func lookUp(t *testing.T, h *harness, token, reference, extra string) *http.Response {
	t.Helper()
	path := "/api/v1/pos/sales/lookup?reference=" + url.QueryEscape(reference)
	if extra != "" {
		path += "&" + extra
	}
	return h.do(t, "GET", path, token, nil)
}

// Every identifier a receipt can carry finds the same sale.
//
// A cashier holding a receipt has no way to know which kind of reference they
// are looking at, so all four resolve or the screen is unusable for whichever
// one the shop's stationery happens to print.
func TestASaleIsFoundByEveryReferenceItsReceiptCanCarry(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// The document UUID the TILL generates. This is the identifier the terminal
	// has, prints and remembers, and it is not the invoice id.
	documentUUID := newUUID()
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, documentUUID, "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID, _ := decodeJSON(t, created)["invoice_id"].(string)
	if invoiceID == "" {
		t.Fatal("the sale response carried no invoice id")
	}
	if invoiceID == documentUUID.String() {
		t.Fatal("the invoice id and the document UUID are the same value, so " +
			"this test cannot tell the two apart and the bug it guards " +
			"against would pass unnoticed")
	}

	read := h.do(t, "GET", "/api/v1/pos/sales/"+invoiceID, f.token, nil)
	if read.StatusCode != 200 {
		t.Fatalf("read the invoice: %s", readBody(t, read))
	}
	humanNumber, _ := decodeJSON(t, read)["human_number"].(string)
	if humanNumber == "" {
		t.Fatal("the invoice has no human number, so a customer reading one " +
			"out over the telephone has nothing to read")
	}

	for _, c := range []struct{ what, reference string }{
		{"the document UUID the till generated", documentUUID.String()},
		{"the eight characters the receipt prints", documentUUID.String()[:8]},
		{"the invoice number a customer reads out", humanNumber},
		{"the invoice id, for a caller that already holds one", invoiceID},
	} {
		t.Run(c.what, func(t *testing.T) {
			resp := lookUp(t, h, f.token, c.reference, "")
			if resp.StatusCode != 200 {
				t.Fatalf("looking a sale up by %s answered %d: %s. A cashier "+
					"holding the receipt cannot return the goods.",
					c.what, resp.StatusCode, readBody(t, resp))
			}
			body := decodeJSON(t, resp)
			if got, _ := body["id"].(string); got != invoiceID {
				t.Errorf("resolved to invoice %q, want %q", got, invoiceID)
			}
			// The id is what the caller needs; the rest is what a cashier
			// checks against the paper in their hand before money moves.
			if got, _ := body["uuid"].(string); got != documentUUID.String() {
				t.Errorf("the match names document UUID %q, want %q",
					got, documentUUID)
			}
			if got, _ := body["total_inclusive"].(string); got == "" {
				t.Error("the match carries no total, so there is nothing to " +
					"check the receipt against")
			}
		})
	}
}

// And the id it resolves to is one the returns route accepts.
//
// The two halves were each correct and did not meet, which is the whole shape
// of the original bug. Asserting the lookup alone would reproduce it.
func TestWhatTheLookupResolvesIsWhatTheReturnRouteTakes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	documentUUID := newUUID()
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, documentUUID, "2", "115.00", "230.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	// Exactly what the till does: the reference off the receipt, then the
	// lines, using nothing the cashier typed for the second call.
	found := lookUp(t, h, f.token, documentUUID.String()[:8], "")
	if found.StatusCode != 200 {
		t.Fatalf("lookup: %d %s", found.StatusCode, readBody(t, found))
	}
	id, _ := decodeJSON(t, found)["id"].(string)

	lines := h.do(t, "GET", "/api/v1/pos/sales/"+id+"/returnable", f.token, nil)
	if lines.StatusCode != 200 {
		t.Fatalf("the id the lookup returned was refused by /returnable: "+
			"%d %s", lines.StatusCode, readBody(t, lines))
	}
	got, _ := decodeJSON(t, lines)["lines"].([]any)
	if len(got) != 1 {
		t.Fatalf("%d returnable lines, want 1", len(got))
	}
}

// Two sales sharing a prefix are refused, never guessed between.
//
// The terminal chooses the document UUID, so a collision in the first eight
// characters is possible however unlikely — and picking one is a refund against
// a stranger's invoice. The cashier is told to type more of it.
func TestAnAmbiguousReferenceIsRefusedRatherThanGuessedAt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Two UUIDs that agree for the first eight characters and differ after.
	shared := uuid.NewString()
	first := uuid.MustParse(shared)
	second := uuid.MustParse(shared[:9] + flipHex(shared[9:10]) + shared[10:])
	if first == second || first.String()[:8] != second.String()[:8] {
		t.Fatalf("the fixture is wrong: %s and %s", first, second)
	}

	for _, id := range []uuid.UUID{first, second} {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, id, "1", "115.00", "115.00"))
		if resp.StatusCode != 201 {
			t.Fatalf("sale %s: %s", id, readBody(t, resp))
		}
	}

	resp := lookUp(t, h, f.token, shared[:8], "")
	if resp.StatusCode != 400 {
		t.Fatalf("an ambiguous reference answered %d, want 400. Resolving it "+
			"to one of the two would refund against whichever invoice the "+
			"database happened to return first: %s",
			resp.StatusCode, readBody(t, resp))
	}

	// And the full reference still works, because ambiguity is a property of
	// the prefix and not of the sale.
	full := lookUp(t, h, f.token, second.String(), "")
	if full.StatusCode != 200 {
		t.Fatalf("the whole document UUID answered %d: %s",
			full.StatusCode, readBody(t, full))
	}
}

func flipHex(s string) string {
	if s == "0" {
		return "1"
	}
	return "0"
}

// A reference that names nothing says so.
//
// Distinguished from "nothing left to return" on purpose. A cashier standing in
// front of a customer cannot tell those apart, and one of them is a
// conversation about a refund that is never going to happen.
func TestAnUnknownReferenceSaysNoSaleWasFound(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, c := range []struct {
		what, reference string
		status          int
	}{
		{"a well-formed UUID for no sale", uuid.NewString(), 404},
		{"an invoice number that was never issued", "INV-NOPE-9999", 404},
		{"a prefix matching nothing", "deadbeef", 404},
		{"a fragment too short to be a reference", "abc", 400},
		{"nothing at all", "", 400},
	} {
		t.Run(c.what, func(t *testing.T) {
			resp := lookUp(t, h, f.token, c.reference, "")
			if resp.StatusCode != c.status {
				t.Fatalf("%s answered %d, want %d: %s",
					c.what, resp.StatusCode, c.status, readBody(t, resp))
			}
			if msg := readBody(t, resp); msg == "" {
				t.Error("a refusal with no message; a cashier is told nothing")
			}
		})
	}
}

// A till may not widen its search by naming a company.
//
// The device's company comes from the device, exactly as it does for the
// catalogue and the stationery. A terminal that could name its own company
// could pull up a sister company's invoice and refund against it — and both
// companies belong to one tenant, so row-level security would not notice.
func TestATillsLookupIgnoresAnyCompanyItNames(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	documentUUID := newUUID()
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, documentUUID, "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID, _ := decodeJSON(t, created)["invoice_id"].(string)

	// Naming a company that is not the terminal's must change nothing: the
	// parameter is not consulted for a device at all.
	resp := lookUp(t, h, f.token, documentUUID.String(),
		"company_id="+uuid.NewString())
	if resp.StatusCode != 200 {
		t.Fatalf("a till naming another company answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if got, _ := decodeJSON(t, resp)["id"].(string); got != invoiceID {
		t.Errorf("resolved to %q, want the terminal's own sale %q",
			got, invoiceID)
	}
}

// Another shop's terminal cannot resolve this shop's receipt.
//
// The route is gated on sales.refund like /returnable, and for the same reason:
// a role that may sell and not reverse has no business finding the invoice to
// reverse. Beyond the permission, the tenant boundary has to hold — a reference
// is eight characters somebody could type by accident.
func TestAnotherTenantsTillCannotResolveThisShopsReceipt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	documentUUID := newUUID()
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, documentUUID, "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	stranger := h.seedShop(t, "cashier")
	resp := lookUp(t, h, stranger.token, documentUUID.String(), "")
	if resp.StatusCode == 200 {
		t.Fatalf("a terminal in another tenant resolved this shop's sale: %s",
			readBody(t, resp))
	}
	// 404 rather than 403: a 403 would confirm the record exists, which leaks
	// across the tenant boundary (07-api-conventions §3).
	if resp.StatusCode != 404 {
		t.Errorf("answered %d, want 404 so the refusal does not confirm the "+
			"invoice exists", resp.StatusCode)
	}
}

// A user confined to one company cannot search another's sales.
//
// Row-level security does not help: its predicate is the tenant, and both
// companies are inside it. The confinement lives in the handler, which is
// exactly where it gets forgotten — P65 found the dimension declared, stored,
// claimed, parsed and checked, and never fed.
//
// The two refusals are told apart by their MESSAGE. A missing check would
// answer 404 as well, having searched Beta and found nothing there, so a test
// reading only the status code would pass with the check deleted.
func TestAUserConfinedToOneCompanyCannotSearchAnothersSales(t *testing.T) {
	h := newHarness(t)
	s, beta := seedConfinedGroup(t, h)

	resp := lookUp(t, h, s.token, uuid.NewString(),
		"company_id="+beta.companyID.String())
	if resp.StatusCode != 404 {
		t.Fatalf("naming another company answered %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !strings.Contains(body, "company") {
		t.Errorf("the refusal is %q. It should come from the confinement check "+
			"and name the company; a refusal that names the sale means the "+
			"search ran against Beta and merely happened to find nothing.",
			truncate(body, 240))
	}

	// And their own company still works, or the check is a wall.
	own := lookUp(t, h, s.token, uuid.NewString(),
		"company_id="+s.companyA.String())
	if own.StatusCode != 404 {
		t.Fatalf("their own company answered %d, want the ordinary 404 for a "+
			"reference that names nothing: %s",
			own.StatusCode, readBody(t, own))
	}
	if body := readBody(t, own); strings.Contains(body, "company") {
		t.Errorf("a confined user searching their OWN company was refused by "+
			"the confinement check: %s", truncate(body, 240))
	}
}
