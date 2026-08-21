//go:build integration

// P35 — document templates, over the routes the Back Office calls.
//
// The half of I2 that 0054 could not build: what a client writes on their own
// documents, now that UI spec §5 has given them a surface that can carry it.
//
// The property these exist to protect is the one that makes the feature safe at
// all: a template carries presentation and nothing else, so changing it can
// never alter what a document said about the transaction it recorded.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func templatePath(f *shopFixture, docType string) string {
	return "/api/v1/companies/" + f.companyID.String() + "/templates/" + docType
}

func templatesPath(f *shopFixture) string {
	return "/api/v1/companies/" + f.companyID.String() + "/templates"
}

func fullTemplate() map[string]any {
	return map[string]any{
		"header_text":      "Olaya Branch, King Fahd Road",
		"header_text_ar":   "فرع العليا، طريق الملك فهد",
		"footer_text":      "Thank you for your custom.",
		"footer_text_ar":   "شكرا لتعاملكم معنا.",
		"return_policy":    "Unworn items may be returned within 14 days.",
		"return_policy_ar": "يمكن إرجاع القطع غير الملبوسة خلال ١٤ يوما.",
		"payment_terms":    "Net 30. Bank transfer preferred.",
		"payment_terms_ar": "صافي ٣٠ يوما.",
		"show_logo":        true,
		"show_tax_number":  true,
	}
}

// Every configurable type is listed, set or not.
//
// A screen showing only the configured ones would hide the types a client has
// not thought about, which are exactly the ones worth showing them.
func TestEveryDocumentTypeIsOfferedWhetherOrNotItIsSet(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet, templatesPath(f), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list templates: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := decodeJSON(t, resp)["data"].([]any)

	if len(raw) != 4 {
		t.Fatalf("offered %d types, want the four this product issues", len(raw))
	}

	seen := map[string]bool{}
	for _, r := range raw {
		item, _ := r.(map[string]any)
		docType, _ := item["doc_type"].(string)
		seen[docType] = true

		// Untouched, so it reads as the default rather than as an empty form.
		if item["configured"] != false {
			t.Errorf("%s reports configured before anybody set it", docType)
		}
		// The RawSyst fallback: mark and tax numbers on, blocks empty. Empty
		// rather than seeded with suggested wording — a footer nobody wrote is
		// a footer nobody meant.
		if item["show_logo"] != true || item["show_tax_number"] != true {
			t.Errorf("%s does not default to showing the logo and tax numbers", docType)
		}
		if item["footer_text"] != "" || item["return_policy"] != "" {
			t.Errorf("%s arrived with wording nobody wrote: %v", docType, item)
		}
	}

	for _, want := range []string{"standard", "simplified", "credit_note", "debit_note"} {
		if !seen[want] {
			t.Errorf("%s was not offered", want)
		}
	}
}

// Write, read back, and reset.
func TestAClientWritesTheirOwnDocumentTextAndCanUndoIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, templatePath(f, "standard"), f.token, fullTemplate())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	saved := decodeJSON(t, resp)

	if saved["configured"] != true {
		t.Error("a saved template does not report itself configured")
	}
	if saved["footer_text"] != "Thank you for your custom." {
		t.Errorf("footer = %v", saved["footer_text"])
	}
	// Arabic survives the round trip intact. A byte-level truncation somewhere
	// would show up here as mojibake rather than as an error.
	if saved["return_policy_ar"] != "يمكن إرجاع القطع غير الملبوسة خلال ١٤ يوما." {
		t.Errorf("the Arabic return policy came back as %v", saved["return_policy_ar"])
	}

	// Only the type that was written. A credit note keeps its own words —
	// I2 makes customization per type precisely because one is an apology and
	// the other is a demand.
	resp = h.do(t, http.MethodGet, templatesPath(f), f.token, nil)
	all, _ := decodeJSON(t, resp)["data"].([]any)
	for _, r := range all {
		item, _ := r.(map[string]any)
		if item["doc_type"] == "standard" {
			continue
		}
		if item["configured"] != false || item["footer_text"] != "" {
			t.Errorf("writing the invoice template changed %v", item["doc_type"])
		}
	}

	// Reset returns it to the default.
	resp = h.do(t, http.MethodDelete, templatePath(f, "standard"), f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, templatesPath(f), f.token, nil)
	all, _ = decodeJSON(t, resp)["data"].([]any)
	for _, r := range all {
		item, _ := r.(map[string]any)
		if item["doc_type"] != "standard" {
			continue
		}
		if item["configured"] != false || item["footer_text"] != "" {
			t.Errorf("after resetting, the template still reads %v", item)
		}
	}

	// Resetting one that was never customised succeeds: the client asked for
	// the default and has it.
	resp = h.do(t, http.MethodDelete, templatePath(f, "debit_note"), f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("resetting an untouched template: status %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
}

// The property that makes this safe: a template cannot reach a document.
//
// Blocks are presentation. The figures, the parties, the dates and the tax
// numbers come off the invoice row, which is immutable posted history — so a
// client changing a footer is not amending last quarter's invoices, and this
// proves the invoice is untouched by a template written after it was issued.
func TestATemplateCannotChangeADocumentAlreadyIssued(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Sell something, and record exactly what the invoice says.
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	before := h.do(t, http.MethodGet, "/api/v1/pos/sales/"+invoiceID, f.token, nil)
	if before.StatusCode != http.StatusOK {
		t.Fatalf("read invoice: status %d — %s", before.StatusCode, readBody(t, before))
	}
	original := decodeJSON(t, before)

	// Now write a template for that document type, as an owner would.
	owner := h.seedUserIn(t, f, "owner")
	resp = h.do(t, http.MethodPut, templatePath(f, "simplified"), owner, fullTemplate())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save template: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	after := h.do(t, http.MethodGet, "/api/v1/pos/sales/"+invoiceID, f.token, nil)
	if after.StatusCode != http.StatusOK {
		t.Fatalf("re-read invoice: status %d", after.StatusCode)
	}
	current := decodeJSON(t, after)

	// Every fact the document recorded is unchanged.
	for _, field := range []string{
		"id", "uuid", "doc_type", "human_number", "state", "issue_date",
		"currency", "subtotal_net", "discount_total", "tax_total",
		"total_inclusive",
	} {
		if current[field] != original[field] {
			t.Errorf("writing a template changed the invoice's %s: %v became %v",
				field, original[field], current[field])
		}
	}

	// And the template did not smuggle itself into the document's own payload:
	// the invoice carries no header, footer or policy field for one to land in.
	for _, absent := range []string{
		"header_text", "footer_text", "return_policy", "payment_terms",
	} {
		if _, present := current[absent]; present {
			t.Errorf("the invoice payload carries %q; a template block has "+
				"reached a document row", absent)
		}
	}
}

// What the server refuses, and why.
func TestATemplateIsRefusedWhenItCannotBeUsed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// A document type this product does not issue. I2 names nine; five of them
	// are documents that do not exist here, and a template for one would be
	// configuration for nothing.
	resp := h.do(t, http.MethodPut, templatePath(f, "quotation"), f.token, fullTemplate())
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a template for a quotation: status %d, want 400 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	msg, _ := decodeJSON(t, resp)["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "quotation") {
		t.Errorf("the refusal does not name the type: %q", msg)
	}

	// A block long enough to be a document in its own right. It would be
	// reprinted on every copy.
	tooLong := fullTemplate()
	tooLong["footer_text"] = strings.Repeat("x", 501)
	resp = h.do(t, http.MethodPut, templatePath(f, "standard"), f.token, tooLong)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an overlong footer: status %d, want a refusal — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Arabic is counted in characters, not bytes. A 400-character Arabic policy
	// is well inside the limit; counting its bytes would refuse it at half the
	// length an English one gets, for no reason a client could understand.
	arabic := fullTemplate()
	arabic["return_policy_ar"] = strings.Repeat("ش", 400)
	resp = h.do(t, http.MethodPut, templatePath(f, "standard"), f.token, arabic)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a 400-character Arabic policy: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M7: reading is for everyone who renders a document, writing is not.
func TestOnlySettingsPermissionWritesDocumentText(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// A cashier renders invoices at the counter, so they must be able to read
	// the stationery. They may not rewrite it.
	cashier := h.seedUserIn(t, f, "cashier")

	resp := h.do(t, http.MethodGet, templatesPath(f), cashier, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cashier reading templates: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	for _, c := range []struct{ name, method string }{
		{"write", http.MethodPut},
		{"reset", http.MethodDelete},
	} {
		var body any
		if c.method == http.MethodPut {
			body = fullTemplate()
		}
		resp := h.do(t, c.method, templatePath(f, "standard"), cashier, body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("cashier trying to %s document text: status %d, want 403 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

// QA gate M8: one shop's stationery is invisible to another.
//
// A return policy and payment terms are a business's own words, and bank
// details commonly sit in the payment block.
func TestOneShopCannotReadOrWriteAnothersDocumentText(t *testing.T) {
	h := newHarness(t)
	a := h.seedShop(t, "owner")
	b := h.seedShop(t, "owner")

	secret := fullTemplate()
	secret["payment_terms"] = "Alpha Confidential, IBAN SA0000000000000000"
	resp := h.do(t, http.MethodPut, templatePath(a, "standard"), a.token, secret)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant A save: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// B, naming A's company directly, learns nothing.
	resp = h.do(t, http.MethodGet,
		"/api/v1/companies/"+a.companyID.String()+"/templates", b.token, nil)
	if resp.StatusCode == http.StatusOK {
		body := readBody(t, resp)
		if strings.Contains(body, "Alpha Confidential") || strings.Contains(body, "IBAN") {
			t.Fatal("one shop read another shop's payment terms")
		}
	} else if resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()
	}

	// And cannot overwrite them.
	resp = h.do(t, http.MethodPut,
		"/api/v1/companies/"+a.companyID.String()+"/templates/standard",
		b.token, fullTemplate())
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant B writing tenant A's template: status %d, want 404",
			resp.StatusCode)
	}
	resp.Body.Close()

	// A's words are exactly as A left them.
	resp = h.do(t, http.MethodGet, templatesPath(a), a.token, nil)
	if !strings.Contains(readBody(t, resp), "Alpha Confidential") {
		t.Fatal("tenant A's payment terms were altered by tenant B")
	}

	// And the row is invisible at the database, not merely at the route.
	var visible int
	if err := h.pool.TxAsTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM document_template`).Scan(&visible)
	}); err != nil {
		t.Fatalf("query as tenant B: %v", err)
	}
	if visible != 0 {
		t.Fatalf("tenant B can see %d templates that are not its own", visible)
	}
}
