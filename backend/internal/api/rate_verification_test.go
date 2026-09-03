//go:build integration

// Getting an imported rate schedule into production.
//
// 0118 loaded CDTFA's schedule unverified, which is right, and there was then
// no way to ever verify it: every `verified_on` write in the registry is on an
// INSERT, and re-importing the same schedule does nothing because the rows
// already exist. 541 rates were stuck at "imported" and no Californian shop
// could trade. 0120 and registry.VerifyRates are the route out, and these tests
// hold its two refusals shut.
//
// The batches here are test-local. Reviewing and verifying the SHIPPED CDTFA
// rows would stamp them for every other test in this package, which is exactly
// what cdtfa_test.go asserts has not happened.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

// importedBatch loads a small schedule, unverified, and returns its document.
func importedBatch(t *testing.T, h *harness, admin, suffix string) string {
	t.Helper()
	doc := "Test schedule " + suffix
	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/import", admin, map[string]any{
			"country": "us", "treatment": "taxable",
			"effective_from":   "2026-04-01",
			"source_authority": "test",
			"source_document":  doc,
			"rows": []map[string]any{
				{"level": "country", "code": "C" + suffix, "name": "Testland",
					"rate": "0"},
				{"level": "state", "code": "S" + suffix, "name": "Test State",
					"parent_code": "C" + suffix, "rate": "0.0725"},
				{"level": "city", "code": "T" + suffix, "name": "Test City",
					"parent_code": "S" + suffix, "rate": "0.035"},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, readBody(t, resp))
	}
	return doc
}

func batchBody(doc, note string) map[string]any {
	body := map[string]any{
		"country": "us", "source_document": doc,
		"treatment": "taxable", "effective_from": "2026-04-01",
	}
	if note != "" {
		body["note"] = note
	}
	return body
}

func review(t *testing.T, h *harness, admin, doc, note string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/rates/review", admin,
		batchBody(doc, note))
}

func verify(t *testing.T, h *harness, admin, doc string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/rates/verify", admin,
		batchBody(doc, ""))
}

// batchOf finds one schedule in the listing.
func batchOf(t *testing.T, h *harness, admin, doc string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/jurisdictions/rates?country=us", admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list batches: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	for _, r := range rows {
		b, _ := r.(map[string]any)
		if s, _ := b["source_document"].(string); s == doc {
			return b
		}
	}
	t.Fatalf("the schedule %q is not in the listing of %d", doc, len(rows))
	return nil
}

// --- the workflow ---------------------------------------------------------

// A freshly imported schedule is listed as imported, and nothing is verified.
func TestAnImportedScheduleIsListedAsAwaitingReview(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := upperSuffix(5)
	doc := importedBatch(t, h, admin, suffix)

	b := batchOf(t, h, admin, doc)
	if got, _ := b["status"].(string); got != "imported" {
		t.Errorf("status is %q, want imported", got)
	}
	if got, _ := b["rates"].(float64); int(got) != 3 {
		t.Errorf("the schedule has %v rates, want 3", b["rates"])
	}
	if got, _ := b["verified"].(float64); int(got) != 0 {
		t.Errorf("%v rates are verified on a fresh import, want 0",
			b["verified"])
	}
}

// A review says what was checked. A review with no statement is a click.
func TestAReviewMustSayWhatWasChecked(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	doc := importedBatch(t, h, admin, upperSuffix(5))

	resp := review(t, h, admin, doc, "   ")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity &&
		resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a review with no note got %d, want a refusal",
			resp.StatusCode)
	}
}

// An unreviewed schedule cannot be verified.
func TestAnUnreviewedScheduleCannotBeVerified(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	doc := importedBatch(t, h, admin, upperSuffix(5))

	resp := verify(t, h, admin, doc)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("verifying an unreviewed schedule got %d, want 409: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !containsFold(body, "review") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
}

// The person who reviewed a schedule cannot be the one who verifies it.
//
// The control the whole workflow exists for. One person mistyping a decimal in
// a tax rate charges every customer of every shop in that jurisdiction the
// wrong amount, and the shop remits the wrong amount to the state.
func TestTheReviewerCannotAlsoVerify(t *testing.T) {
	h := newHarness(t)
	first := platformAdmin(t, h)
	doc := importedBatch(t, h, first, upperSuffix(5))

	if resp := review(t, h, first, doc,
		"Checked all three rates against the authority's page."); resp.StatusCode != http.StatusOK {
		t.Fatalf("review: %d %s", resp.StatusCode, readBody(t, resp))
	}

	resp := verify(t, h, first, doc)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the reviewer verifying their own schedule got %d, want 403: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Reviewed by one person, verified by another, and then it is production data.
func TestASecondAdminVerifiesAReviewedSchedule(t *testing.T) {
	h := newHarness(t)
	first := platformAdmin(t, h)
	second := platformAdmin(t, h)
	suffix := upperSuffix(5)
	doc := importedBatch(t, h, first, suffix)

	if resp := review(t, h, first, doc,
		"Compared every rate with the published schedule dated 2026-04-01."); resp.StatusCode != http.StatusOK {
		t.Fatalf("review: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if got, _ := batchOf(t, h, first, doc)["status"].(string); got != "reviewed" {
		t.Errorf("status after review is %q, want reviewed", got)
	}

	resp := verify(t, h, second, doc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if n, _ := decodeJSON(t, resp)["verified"].(float64); int(n) != 3 {
		t.Errorf("verified %v rates, want 3", n)
	}
	resp.Body.Close()

	b := batchOf(t, h, first, doc)
	if got, _ := b["status"].(string); got != "verified" {
		t.Errorf("status after verification is %q, want verified", got)
	}
	// Compared by identity, not by display name: every seeded super admin
	// carries the same label, so comparing labels would pass even if one
	// person had done both.
	var unverified, sameHand int
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT count(*) FILTER (WHERE verified_on IS NULL),
			       count(*) FILTER (WHERE reviewed_by = verified_by)
			FROM tax_jurisdiction_rate
			WHERE source_document = $1`, doc).Scan(&unverified, &sameHand)
	}); err != nil {
		t.Fatalf("read the rates: %v", err)
	}
	if unverified != 0 {
		t.Errorf("%d rates are still unverified after sign-off", unverified)
	}
	if sameHand != 0 {
		t.Errorf("%d rates were reviewed and verified by the same person",
			sameHand)
	}
}

// Verifying a schedule cannot change the figures being verified.
//
// Without this, "verification" is an UPDATE that could also move the rate, its
// dates or its provenance — and a later import could rewrite a historical rate
// that invoices were already issued against.
func TestVerificationCannotAlterTheRateItVerifies(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	doc := importedBatch(t, h, admin, upperSuffix(5))

	for _, c := range []struct{ column, value string }{
		{"rate", "0.99"},
		{"effective_from", "'2020-01-01'"},
		{"source_authority", "'somebody_else'"},
		{"source_document", "'A different publication'"},
	} {
		err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`UPDATE tax_jurisdiction_rate SET `+c.column+` = `+c.value+
					` WHERE source_document = $1`, doc)
			return e
		})
		if err == nil {
			t.Errorf("%s could be changed on a rate already on file", c.column)
		}
	}

	// Closing a row is still allowed: that is how a later schedule supersedes
	// an earlier one.
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(), `
			UPDATE tax_jurisdiction_rate SET effective_to = '2027-01-01'
			WHERE source_document = $1`, doc)
		return e
	}); err != nil {
		t.Errorf("a rate could not be superseded: %v", err)
	}
}

// A schedule nobody imported is refused, not reported as nothing to do.
//
// A typo in a document name silently reporting "verified, 0 rates" is how an
// operator believes a schedule is live when it is not.
func TestVerifyingAScheduleThatWasNeverImportedIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := verify(t, h, admin, "A schedule nobody ever imported")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("verifying an unknown schedule got %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Reviewing twice does not re-open a schedule somebody already signed off.
func TestAnAlreadyReviewedScheduleIsNotReviewedAgain(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	doc := importedBatch(t, h, admin, upperSuffix(5))

	if resp := review(t, h, admin, doc, "First pass."); resp.StatusCode != http.StatusOK {
		t.Fatalf("review: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp := review(t, h, admin, doc, "Second pass.")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("reviewing twice got %d, want 409", resp.StatusCode)
	}
}

// Only the Platform Owner may sign tax rates off.
//
// A business owner who could verify their own tax rates would be choosing what
// they owe.
func TestABusinessOwnerCannotVerifyTaxRates(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	doc := importedBatch(t, h, admin, upperSuffix(5))
	f := h.seedShop(t, "owner")

	for _, path := range []string{"review", "verify"} {
		resp := h.do(t, http.MethodPost,
			"/api/v1/platform/jurisdictions/rates/"+path, f.token,
			batchBody(doc, "Trying it on."))
		if resp.StatusCode != http.StatusForbidden &&
			resp.StatusCode != http.StatusNotFound {
			t.Errorf("a business owner reached %s and got %d", path,
				resp.StatusCode)
		}
		resp.Body.Close()
	}
}
