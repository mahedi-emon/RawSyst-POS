//go:build integration

// The stationery a till pulls, so it can print with no network.
//
// The last of I2. The Back Office writes the words (P35) and this is how they
// reach the document a customer walks out with — which is printed at the
// counter, often offline, and cannot wait for a round trip.
package api

import (
	"net/http"
	"testing"
)

const stationeryPath = "/api/v1/pos/stationery"

// A till with a template gets the shop's words; one without gets the default.
func TestATillPullsTheShopStationery(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Before anybody writes anything: the seller, and blocks that are empty
	// rather than seeded with wording nobody chose.
	resp := h.do(t, http.MethodGet, stationeryPath, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull stationery: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	before := decodeJSON(t, resp)

	if before["store_name"] == "" {
		t.Error("a till was given no seller; a receipt with no name on it is not a document")
	}
	if before["return_policy"] != "" || before["footer_text"] != "" {
		t.Errorf("stationery arrived with wording nobody wrote: %v", before)
	}
	if before["show_tax_number"] != true {
		t.Error("the default does not show tax numbers")
	}

	// Now an owner writes the counter-sale template.
	owner := h.seedUserIn(t, f, "owner")
	resp = h.do(t, http.MethodPut, templatePath(f, "simplified"), owner, map[string]any{
		"header_text":      "Olaya Branch, King Fahd Road",
		"header_text_ar":   "فرع العليا",
		"footer_text":      "See you again soon.",
		"footer_text_ar":   "",
		"return_policy":    "Unworn items may be returned within 14 days.",
		"return_policy_ar": "",
		"payment_terms":    "Cash and card accepted.",
		"payment_terms_ar": "",
		"show_logo":        true,
		"show_tax_number":  false,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save template: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, stationeryPath, f.token, nil)
	after := decodeJSON(t, resp)

	if after["header_text"] != "Olaya Branch, King Fahd Road" {
		t.Errorf("header = %v", after["header_text"])
	}
	if after["header_text_ar"] != "فرع العليا" {
		t.Errorf("the Arabic header came back as %v", after["header_text_ar"])
	}
	if after["return_policy"] != "Unworn items may be returned within 14 days." {
		t.Errorf("return policy = %v", after["return_policy"])
	}
	if after["show_tax_number"] != false {
		t.Error("the till was not told to withhold the tax number")
	}

	// The counter sale is a SIMPLIFIED invoice, so that is the template a till
	// gets. Writing the standard-invoice template must not reach the receipt.
	resp = h.do(t, http.MethodPut, templatePath(f, "standard"), owner, map[string]any{
		"header_text": "BACK OFFICE ONLY", "header_text_ar": "",
		"footer_text": "", "footer_text_ar": "",
		"return_policy": "", "return_policy_ar": "",
		"payment_terms": "", "payment_terms_ar": "",
		"show_logo": true, "show_tax_number": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save standard template: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, stationeryPath, f.token, nil)
	if got := decodeJSON(t, resp)["header_text"]; got == "BACK OFFICE ONLY" {
		t.Fatal("a till was given the standard-invoice template; a counter sale " +
			"is a simplified invoice and prints those words")
	}
}

// No logo in the payload, and that is deliberate rather than an omission.
//
// The receipt is 42 columns of plain text so it prints on every counter
// printer, and text cannot hold an image. Sending bytes the till would then
// discard is worse than not sending them: a client would wonder why the logo
// they uploaded never appeared.
func TestTheTillIsNotSentALogoItCannotPrint(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	owner := h.seedUserIn(t, f, "owner")
	resp := h.do(t, http.MethodPut, logoPath(f), owner, logoBody(pngOf(t, 200, 100)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload logo: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, stationeryPath, f.token, nil)
	body := decodeJSON(t, resp)
	for _, absent := range []string{"logo", "logo_url", "logo_data", "show_logo"} {
		if _, present := body[absent]; present {
			t.Errorf("the till was sent %q, which a plain-text receipt cannot use", absent)
		}
	}
}

// The company comes from the device, never from the caller.
//
// A terminal that could name its own company could print another company's
// letterhead, and both belong to the same tenant so row-level security would
// not notice — the same argument every other till route rests on.
func TestATillCannotAskForAnotherCompanysStationery(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	other := h.seedShop(t, "cashier")

	owner := h.seedUserIn(t, other, "owner")
	resp := h.do(t, http.MethodPut, templatePath(other, "simplified"), owner,
		map[string]any{
			"header_text": "ANOTHER SHOP", "header_text_ar": "",
			"footer_text": "", "footer_text_ar": "",
			"return_policy": "", "return_policy_ar": "",
			"payment_terms": "", "payment_terms_ar": "",
			"show_logo": true, "show_tax_number": true,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed the other shop: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Naming it explicitly changes nothing: another tenant's company is not
	// this caller's to ask about.
	resp = h.do(t, http.MethodGet,
		stationeryPath+"?company_id="+other.companyID.String(), f.token, nil)
	if resp.StatusCode == http.StatusOK {
		if got := decodeJSON(t, resp)["header_text"]; got == "ANOTHER SHOP" {
			t.Fatal("a till printed another company's letterhead")
		}
	} else if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("naming another company: status %d, want 404 or its own — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M7: pulling stationery is part of selling.
func TestStationeryNeedsPermissionToSell(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// A cashier prints receipts, so they must be able to pull the words.
	resp := h.do(t, http.MethodGet, stationeryPath, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cashier pulling stationery: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// An auditor reads everything and sells nothing.
	auditor := h.seedUserIn(t, f, "auditor")
	resp = h.do(t, http.MethodGet, stationeryPath, auditor, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("auditor pulling till stationery: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}
