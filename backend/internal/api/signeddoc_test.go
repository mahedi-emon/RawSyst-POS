//go:build integration

// The terminal hands back what it signed.
//
// Signing is local and locked (E1.3 RULE 1): the device holds its CSID private
// key and the server never sees it. But the server allocates the ICV and the
// PIH, so the signed document necessarily arrives afterwards — and until this
// upload existed, the server held chain positions with nothing attached and
// had nothing to submit.
//
// The XML and stamp below assert nothing about ZATCA's format and are obviously
// not real documents. The QR is real, because the upload now checks the TLV
// framing before storing it. What is under test is the handoff, the write-once
// guarantee, which of the three artefacts reaches the submitter, and that a
// malformed QR is turned away.
package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// Stand-ins for what a terminal produces. The document is deliberately not
// shaped like real UBL: inventing a plausible one is what the verification gate
// exists to prevent, and these tests do not need one.
const (
	placeholderXML   = "<placeholder-signed-document/>"
	placeholderStamp = "placeholder-terminal-stamp"
	// Not a placeholder: the upload path validates the TLV framing, so this is
	// ZATCA's own worked payload from Technical Guideline V2 p.64 — tags 1 to 5,
	// which are the ones required since 2021.
	placeholderQR = "ARVCb2JzIEJhc2VtZW50IFJlY29yZHMCDzEwMDAyNTkwNjcwMDAwMwMU" +
		"MjAyMi0wNC0yNVQxNTozMDowMFoECjIxMDAxMDAuOTkFCTMxNTAxNS4xNQ=="
)

func (h *harness) uploadDocument(
	t *testing.T, f *shopFixture, invoiceID, xml, stamp, qr string,
) *http.Response {
	t.Helper()
	return h.do(t, "PUT", "/api/v1/pos/sales/"+invoiceID+"/signed-document",
		f.token, map[string]any{"xml": xml, "stamp": stamp, "qr_tlv": qr})
}

func (h *harness) sellOne(t *testing.T, f *shopFixture) string {
	t.Helper()
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	return decodeJSON(t, resp)["invoice_id"].(string)
}

// The three artefacts are stored, distinctly.
func TestATerminalUploadsItsSignedDocument(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	resp := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR)
	if resp.StatusCode != 200 {
		t.Fatalf("upload: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["stored"] != true {
		t.Errorf("the upload did not report the document as stored: %v", body)
	}
	if body["submittable"] != true {
		t.Errorf("the server does not consider the invoice submittable: %v", body)
	}
	// The gate is reported plainly, so a till never assumes its invoice has
	// been reported when the transport is still closed.
	if body["submission_available"] != false {
		t.Errorf("submission_available = %v; the transport is gated and must "+
			"say so", body["submission_available"])
	}

	var xml, stamp, qr *string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT xml, stamp, qr_tlv FROM zatca_invoice WHERE invoice_id = $1`,
			invoiceID).Scan(&xml, &stamp, &qr)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}

	// Three columns, three different values. Conflating them is the defect
	// this test exists to catch.
	if xml == nil || *xml != placeholderXML {
		t.Errorf("xml = %v, want the document", xml)
	}
	if stamp == nil || *stamp != placeholderStamp {
		t.Errorf("stamp = %v, want the signature", stamp)
	}
	if qr == nil || *qr != placeholderQR {
		t.Errorf("qr_tlv = %v, want the QR payload", qr)
	}
}

// The submitter receives the DOCUMENT, not the signature over it.
//
// An earlier cut sent the stamp as the payload, which would have posted a
// signature with nothing attached to it.
func TestTheSubmitterSendsTheDocumentNotTheStamp(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	if resp := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR); resp.StatusCode != 200 {
		t.Fatalf("upload: %s", readBody(t, resp))
	}

	client := &recordingZATCA{outcome: zatca.OutcomeAccepted, status: 200}
	w, _ := h.workerWith(t, f, client)
	if _, err := w.Drain(t.Context(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if len(client.seen) != 1 {
		t.Fatalf("%d submissions, want 1", len(client.seen))
	}
	got := client.seen[0]

	if string(got.SignedXML) != placeholderXML {
		t.Errorf("the submitter sent %q as the document; it must send the "+
			"signed XML, not the stamp", string(got.SignedXML))
	}
	if got.Stamp != placeholderStamp {
		t.Errorf("stamp = %q, want it carried alongside the document", got.Stamp)
	}
	if got.QRTLV != placeholderQR {
		t.Errorf("qr = %q", got.QRTLV)
	}
}

// Nothing is submitted while the document is missing, and the job stays
// queued rather than failing: the upload may still be on its way.
func TestAnInvoiceWithNoDocumentIsNotSubmitted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	client := &recordingZATCA{outcome: zatca.OutcomeAccepted, status: 200}
	w, _ := h.workerWith(t, f, client)
	if _, err := w.Drain(t.Context(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if len(client.seen) != 0 {
		t.Errorf("%d invoices submitted with no signed document", len(client.seen))
	}

	var jobState, invState string
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT state::text FROM job WHERE payload->>'invoice_id' = $1`,
			invoiceID).Scan(&jobState)
	}); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT state FROM sales_invoice WHERE id = $1`, invoiceID).Scan(&invState)
	}); err != nil {
		t.Fatalf("read invoice: %v", err)
	}

	if jobState != "pending" {
		t.Errorf("job state = %q, want pending; the upload may still arrive",
			jobState)
	}
	if invState != "signed_pending_report" {
		t.Errorf("invoice state = %q; nothing may settle without a document",
			invState)
	}
}

// Write-once. A document cannot be swapped underneath a signature that still
// looks valid — that is exactly the substitution a tamper-evident chain exists
// to catch.
func TestASignedDocumentCannotBeReplaced(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	if resp := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR); resp.StatusCode != 200 {
		t.Fatalf("first upload: %s", readBody(t, resp))
	}

	resp := h.uploadDocument(t, f, invoiceID,
		"<a-different-document/>", "a-different-stamp", placeholderQR)
	if resp.StatusCode != 409 {
		t.Fatalf("status = %d, want 409: %s", resp.StatusCode, readBody(t, resp))
	}
	if msg := readBody(t, resp); !strings.Contains(msg, "credit note") {
		t.Errorf("the refusal does not say what to do instead: %s", msg)
	}

	// And the original survives untouched.
	var xml string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT xml FROM zatca_invoice WHERE invoice_id = $1`, invoiceID).Scan(&xml)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if xml != placeholderXML {
		t.Errorf("the stored document changed to %q", xml)
	}
}

// The database enforces it too, not just the service. A support script and a
// migration must hit the same wall.
func TestTheDatabaseRefusesToRewriteASignedDocument(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	if resp := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR); resp.StatusCode != 200 {
		t.Fatalf("upload: %s", readBody(t, resp))
	}

	for _, column := range []string{"xml", "stamp", "qr_tlv"} {
		err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(t.Context(),
				`UPDATE zatca_invoice SET `+column+` = 'tampered' WHERE invoice_id = $1`,
				invoiceID)
			return e
		})
		if err == nil {
			t.Errorf("%s was rewritten by direct SQL", column)
		}
	}
}

// Re-uploading the SAME document is a retry, not a conflict. A terminal that
// lost the response must be able to send again.
func TestReuploadingTheSameDocumentIsARetry(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	first := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR)
	if first.StatusCode != 200 {
		t.Fatalf("first upload: %s", readBody(t, first))
	}

	second := h.uploadDocument(t, f, invoiceID,
		placeholderXML, placeholderStamp, placeholderQR)
	if second.StatusCode != 200 {
		t.Fatalf("retry: %d %s", second.StatusCode, readBody(t, second))
	}
	body := decodeJSON(t, second)
	if body["stored"] != false {
		t.Errorf("a retry reported the document as newly stored: %v", body)
	}
	if body["submittable"] != true {
		t.Errorf("a retry should still report the invoice as submittable: %v", body)
	}
}

// A terminal cannot attach its signature to another till's chain position.
// Both tills belong to the same tenant, so row-level security would not notice.
func TestATerminalCannotSignAnotherTillsInvoice(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	theirInvoice := h.sellOne(t, theirs)

	resp := h.do(t, "PUT",
		"/api/v1/pos/sales/"+theirInvoice+"/signed-document", mine.token,
		map[string]any{"xml": placeholderXML, "stamp": placeholderStamp})
	defer resp.Body.Close()

	// 404 across the tenant boundary: their invoice simply is not there.
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// An upload with no terminal behind it is refused. A browser session holds no
// CSID and nothing it produced could be a signed document.
func TestAnUploadWithoutATerminalIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	token, _, err := h.tokens.IssueAccess(actorWithoutDevice(f))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	resp := h.do(t, "PUT", "/api/v1/pos/sales/"+invoiceID+"/signed-document",
		token, map[string]any{"xml": placeholderXML, "stamp": placeholderStamp})
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// A document with no stamp is not a signed document.
func TestAnUploadMissingItsStampIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	invoiceID := h.sellOne(t, f)

	for _, tc := range []struct{ name, xml, stamp string }{
		{"no document", "", placeholderStamp},
		{"no stamp", placeholderXML, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.uploadDocument(t, f, invoiceID, tc.xml, tc.stamp, "")
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

// A malformed QR is refused at the upload rather than stored. The payload ends
// up on a customer's receipt and in a submission to ZATCA, so the boundary is
// the right place to stop it.
func TestAnUploadWithAMalformedQRIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, tc := range []struct{ name, qr string }{
		{"not base64", "placeholder-qr-tlv"},
		{"truncated field", base64.StdEncoding.EncodeToString([]byte{0x01, 0x09, 0x41})},
		{"tag outside the nine", base64.StdEncoding.EncodeToString([]byte{0x0a, 0x01, 0x41})},
		{"missing the VAT total", func() string {
			qr, err := zatca.EncodeQR(
				zatca.QRText(zatca.QRSellerName, "Bobs Basement Records"),
				zatca.QRText(zatca.QRSellerVAT, "100025906700003"),
				zatca.QRText(zatca.QRTimestamp, "2022-04-25T15:30:00Z"),
				zatca.QRText(zatca.QRInvoiceTotal, "2100100.99"),
			)
			if err != nil {
				t.Fatalf("build the short payload: %v", err)
			}
			return qr
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh invoice each time: a refused upload must leave the invoice
			// free to receive a good document afterwards.
			invoiceID := h.sellOne(t, f)

			resp := h.uploadDocument(t, f, invoiceID,
				placeholderXML, placeholderStamp, tc.qr)
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
			}

			// Nothing was written, so the good payload still goes in.
			good := h.uploadDocument(t, f, invoiceID,
				placeholderXML, placeholderStamp, placeholderQR)
			defer good.Body.Close()
			if good.StatusCode != 200 {
				t.Errorf("the refused upload left the invoice unusable: %d %s",
					good.StatusCode, readBody(t, good))
			}
		})
	}
}

// recordingZATCA keeps what it was asked to submit, so a test can assert which
// artefact arrived. A transport double only — it signs nothing.
type recordingZATCA struct {
	outcome zatca.Outcome
	status  int
	seen    []zatca.Submission
}

func (*recordingZATCA) Available() bool { return true }

func (r *recordingZATCA) Submit(_ context.Context, s zatca.Submission) (zatca.Response, error) {
	r.seen = append(r.seen, s)
	return zatca.Response{Outcome: r.outcome, HTTPStatus: r.status}, nil
}
