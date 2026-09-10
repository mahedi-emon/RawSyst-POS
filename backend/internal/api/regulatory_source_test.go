//go:build integration

// The workflow that takes a published document and ends in a legal value.
//
//	upload or fetch -> read -> validate -> preview -> apply -> audit
//
// What these hold to is the shape of that, not the content of any statute. The
// document uploaded below is a fixture: the wording of Articles 84 and 85 as the
// Ministry prints them, used as an INPUT so the reading has something to read.
// It is not this product's copy of the law and nothing seeds it — the copy that
// matters is retrieved through this workflow, with its own hash and retrieval
// date, and `internal/registry` tests the reading itself.
package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The market these tests record into.
//
// Not `sa`, and the reason is a rule this repository already states in
// `internal/registry/attest_integration_test.go`: `regulatory_rule` is
// append-only by trigger and platform-wide by design, so a test that recorded
// `SA.EOSB.ENTITLEMENT` for Saudi Arabia would permanently change it in every
// database the suite touches — including the ones asserting that the boot gate
// still reports it as awaiting a figure. `zz` is ISO 3166's "unknown country",
// nobody trades in it, and the invariant that a verified rule carries a legal
// citation already excludes it.
//
// The country is the only thing this changes. The document, the reading, the
// validation and the apply path are the real ones.
const registryTestCountry = "zz"

// The articles, as a document to upload.
const labourLawFixture = `Saudi Labor Law (fixture)

Basic Wage: All that is given to a worker for his work by virtue of a written
or unwritten employment contract regardless of the kind of wage or its method
of payment, in addition to periodic increments.

Actual Wage: The basic wage plus all other due increments decided for a worker
for the effort he exerts at work or for risks he encounters in the course of
performing his work.

Wage: actual wage.

Month: 30 days, unless otherwise specified in the employment contract or the
work organization regulation.

Chapter 4: End-of-Service Award

Article 84
Upon the end of the employment relation, the employer shall pay the worker an
end-of-service award equivalent to the amount of a half-month wage for each of
the first five years and a one-month wage for each of the following years. The
end-of-service award shall be calculated on the basis of the last wage and the
worker shall be entitled to an end-of-service award for the portions of the
year in proportion to the time spent on the job.

Article 85
If the employment relation ends due to the worker's resignation, he shall, in
this case, be entitled to one third of the award after service of not less than
two consecutive years and not more than five years, to two thirds if his
service is in excess of five consecutive years but less than 10 years, and to
the full award if his service amounts to 10 years or more.

Article 86
As an exception to the provisions of Article 8 of this Law, it may be agreed
that the wage used as a basis for calculating the end-of-service award does not
include all or some of the commissions.
`

// uploadSource posts a document the way the screen does, marked so that this
// run's copy of the fixture is its own document.
//
// One row per distinct document is a property of the table, and a document can
// never be deleted — so without a marker the second test to upload this fixture
// is handed the first one's row in whatever state that test left it, and the
// second RUN is handed its own row from the first. Both were observed: "a
// freshly uploaded document is superseded".
//
// The marker is a comment line. It changes the bytes and therefore the hash,
// and it changes nothing the reading reads. The one test that must upload the
// SAME bytes twice marks the body once itself and calls `uploadExactly`.
func (h *harness) uploadSource(
	t *testing.T, token, ruleKey, country, body string,
) *http.Response {
	t.Helper()
	return h.uploadExactly(t, token, ruleKey, country, markFixture(t, body))
}

// markFixture makes a fixture this run's own.
func markFixture(t *testing.T, body string) string {
	t.Helper()
	return body + "\n\n[test fixture for " + t.Name() +
		" " + upperSuffix(8) + "]\n"
}

// uploadExactly posts the bytes it is given, unmarked.
func (h *harness) uploadExactly(
	t *testing.T, token, ruleKey, country, body string,
) *http.Response {
	t.Helper()

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for k, v := range map[string]string{
		"rule_key": ruleKey, "country": country,
		"title": "Labor Law (test fixture)",
	} {
		if err := form.WriteField(k, v); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
	part, err := form.CreateFormFile("file", "labor.txt")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatalf("write the document: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close the form: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/platform/regulatory-sources/upload", &buf)
	if err != nil {
		t.Fatalf("build the upload: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

// nextEffectiveFrom is a date past everything this rule already has.
//
// `regulatory_rule` refuses overlapping ranges and a recorded figure can never
// be deleted, so a fixed effective date works exactly once: the second run of
// the suite collides with the first run's row. Deriving it from what is already
// there makes these re-runnable, and the assertion that the recorded date is
// the date that was asked for is the stronger one anyway — a hard-coded date
// asserts only that a constant survived a round trip.
func nextEffectiveFrom(t *testing.T, h *harness, key, country string) string {
	t.Helper()
	var from string
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT to_char(
			  coalesce(max(effective_from), date '1900-01-01')
			    + interval '10 years', 'YYYY-MM-DD')
			FROM regulatory_rule WHERE rule_key = $1 AND country = $2`,
			key, country).Scan(&from)
	}); err != nil {
		t.Fatalf("read the rule's dates: %v", err)
	}
	return from
}

func documentOf(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	doc, _ := decodeJSON(t, resp)["document"].(map[string]any)
	if doc == nil {
		t.Fatalf("no document in the response")
	}
	return doc
}

// A business owner reaches none of this.
//
// The Saudi Labour Law is not one business's setting, and a tenant that could
// add a document behind a legal value would be choosing what it owes.
func TestOnlyThePlatformOwnerReachesRegulatorySources(t *testing.T) {
	h := newHarness(t)
	owner := h.seedShop(t, "owner")

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/platform/regulatory-sources"},
		{http.MethodPost, "/api/v1/platform/regulatory-sources/fetch"},
	} {
		resp := h.do(t, c.method, c.path, owner.token,
			map[string]any{"rule_key": "SA.EOSB.ENTITLEMENT"})
		if resp.StatusCode == http.StatusOK ||
			resp.StatusCode == http.StatusCreated {
			t.Errorf("a business owner reached %s %s", c.method, c.path)
		}
		resp.Body.Close()
	}

	resp := h.uploadSource(t, owner.token, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a business owner uploaded a regulatory source document")
	}
}

// An uploaded document is hashed, read and validated before anybody sees it.
func TestAnUploadedDocumentIsHashedAndRead(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", resp.StatusCode, readBody(t, resp))
	}
	doc := documentOf(t, resp)

	// Provenance, without which the reading is an assertion.
	if s, _ := doc["content_sha256"].(string); len(s) != 64 {
		t.Errorf("the document is stored with a %d-character checksum", len(s))
	}
	if doc["origin"] != "upload" {
		t.Errorf("origin is %v, want upload", doc["origin"])
	}
	if s, _ := doc["retrieved_on"].(string); s == "" {
		t.Error("nothing records when the document arrived")
	}
	if s, _ := doc["retrieved_by"].(string); s == "" {
		t.Error("nothing records who supplied it")
	}
	if doc["status"] != "candidate" {
		t.Errorf("a freshly uploaded document is %v; nothing should be "+
			"applied without somebody applying it", doc["status"])
	}

	// The reading, with its evidence.
	fields, _ := doc["extracted"].([]any)
	if len(fields) != 7 {
		t.Fatalf("%d fields were read out of the document, want 7", len(fields))
	}
	for _, raw := range fields {
		f, _ := raw.(map[string]any)
		if s, _ := f["evidence"].(string); strings.TrimSpace(s) == "" {
			t.Errorf("%v was read with no sentence behind it", f["field"])
		}
		if s, _ := f["article"].(string); s == "" {
			t.Errorf("%v does not say which article it came from", f["field"])
		}
	}
	if v, _ := doc["valid"].(bool); !v {
		t.Errorf("the reading does not pass validation: %v",
			doc["validation_error"])
	}
}

// Applying records the legal value, with the document behind it.
func TestApplyingADocumentRecordsTheRuleAndCitesIt(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	if upload.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", upload.StatusCode, readBody(t, upload))
	}
	id, _ := documentOf(t, upload)["id"].(string)

	from := nextEffectiveFrom(t, h, "SA.EOSB.ENTITLEMENT", registryTestCountry)
	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/regulatory-sources/"+id+"/apply", admin,
		map[string]any{"effective_from": from, "verified": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("apply: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rule, _ := decodeJSON(t, resp)["rule"].(map[string]any)

	if v, _ := rule["verified"].(bool); !v {
		t.Error("the rule was applied with verification asserted and came " +
			"back unverified")
	}
	if rule["effective_from"] != from {
		t.Errorf("in force from %v, want %s — the date that was asked for",
			rule["effective_from"], from)
	}
	payload, _ := rule["payload"].(map[string]any)
	for field, want := range map[string]string{
		"days_per_year_first_five":               "15",
		"days_per_year_after_five":               "30",
		"wage_basis":                             "basic_plus_all_allowances",
		"resignation_fraction_under_two_years":   "0",
		"resignation_fraction_two_to_five_years": "1/3",
		"resignation_fraction_five_to_ten_years": "2/3",
		"resignation_fraction_over_ten_years":    "1",
	} {
		if got, _ := payload[field].(string); got != want {
			t.Errorf("%s recorded as %q, want %q", field, got, want)
		}
	}

	// The rule points back at the artefact it was read out of. That link is
	// the difference between a citation somebody typed and a document anybody
	// can open and hash.
	//
	// Retiring the placeholder is the other half of applying, and it is proved
	// in `internal/registry` against a key of its own — not here, because the
	// placeholder this workflow retires in production is the Saudi one, and
	// these tests deliberately do not touch it.
	var linked *string
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT source_document_id::text FROM regulatory_rule
			WHERE id = $1`, rule["id"]).Scan(&linked)
	}); err != nil {
		t.Fatalf("read the rule back: %v", err)
	}
	if linked == nil || *linked != id {
		t.Errorf("the rule cites document %v, want %s", linked, id)
	}

	// And the document says it was applied.
	after := h.do(t, http.MethodGet,
		"/api/v1/platform/regulatory-sources/"+id, admin, nil)
	defer after.Body.Close()
	doc := documentOf(t, after)
	if doc["status"] != "applied" {
		t.Errorf("the document is %v after being applied", doc["status"])
	}
	if s, _ := doc["applied_at"].(string); s == "" {
		t.Error("nothing records when it was applied")
	}
}

// Applying the same document twice is refused.
//
// It would record the same reading as two separate assertions, and the second
// would supersede the first for no reason anybody could later explain.
func TestADocumentCannotBeAppliedTwice(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	id, _ := documentOf(t, upload)["id"].(string)

	// Its own effective date. Two rules cannot both be in force from the same
	// day — the registry refuses overlapping periods, which is what makes
	// "what did we believe the law was in March" answerable — and these tests
	// share a database, so each one records from a date of its own.
	from := nextEffectiveFrom(t, h, "SA.EOSB.ENTITLEMENT", registryTestCountry)
	apply := func() *http.Response {
		return h.do(t, http.MethodPost,
			"/api/v1/platform/regulatory-sources/"+id+"/apply", admin,
			map[string]any{"effective_from": from, "verified": true})
	}

	first := apply()
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("the first apply failed: %d", first.StatusCode)
	}

	second := apply()
	defer second.Body.Close()
	if second.StatusCode == http.StatusCreated {
		t.Error("the same document was applied twice")
	}
}

// A document nothing could be read out of cannot be applied.
func TestADocumentThatReadsNothingCannotBeApplied(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		"This is a leaflet about workplace safety. It states no entitlement.")
	defer upload.Body.Close()
	if upload.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", upload.StatusCode, readBody(t, upload))
	}
	doc := documentOf(t, upload)

	// Kept as evidence, and honest about what happened.
	if s, _ := doc["extraction_error"].(string); s == "" {
		t.Error("a document nothing could be read out of records no reason")
	}
	if v, _ := doc["valid"].(bool); v {
		t.Error("a document with no reading is reported as valid")
	}

	id, _ := doc["id"].(string)
	from := nextEffectiveFrom(t, h, "SA.EOSB.ENTITLEMENT", registryTestCountry)
	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/regulatory-sources/"+id+"/apply", admin,
		map[string]any{"effective_from": from, "verified": true})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a legal value was recorded from a document that states none")
	}
}

// A rejected candidate keeps its reason and is not deleted.
func TestRejectingACandidateKeepsItAndItsReason(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	id, _ := documentOf(t, upload)["id"].(string)

	// A reason is required: somebody finds this later and needs to know
	// whether to try again.
	blank := h.do(t, http.MethodPost,
		"/api/v1/platform/regulatory-sources/"+id+"/reject", admin,
		map[string]any{"reason": ""})
	blank.Body.Close()
	if blank.StatusCode == http.StatusOK {
		t.Error("a candidate was rejected with no reason")
	}

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/regulatory-sources/"+id+"/reject", admin,
		map[string]any{"reason": "Superseded by the 2026 consolidated text."})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: %d", resp.StatusCode)
	}

	after := h.do(t, http.MethodGet,
		"/api/v1/platform/regulatory-sources/"+id, admin, nil)
	defer after.Body.Close()
	doc := documentOf(t, after)
	if doc["status"] != "rejected" {
		t.Errorf("status is %v after rejection", doc["status"])
	}
	if s, _ := doc["rejected_reason"].(string); !strings.Contains(s, "2026") {
		t.Errorf("the reason was not kept: %q", s)
	}
}

// The stored artefact is served back, so nobody has to take the reading on
// trust.
func TestTheStoredDocumentCanBeReadBack(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	id, _ := documentOf(t, upload)["id"].(string)

	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/regulatory-sources/"+id+"/content", admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content: %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "one third of the award") {
		t.Error("the bytes served back are not the document that was stored")
	}
	// Served as an attachment rather than rendered: an arbitrary retrieved
	// document displayed inline on the platform's own origin is a script
	// running behind a Super Admin session.
	if d := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "attachment") {
		t.Errorf("Content-Disposition is %q, want an attachment", d)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the artefact is served without nosniff")
	}
}

// A document, once written, is evidence and cannot be edited.
func TestARetrievedDocumentCannotBeEdited(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	id, _ := documentOf(t, upload)["id"].(string)

	err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE regulatory_source_document
			SET content_sha256 = repeat('0', 64) WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Error("the checksum of a stored document was rewritten; evidence " +
			"that can be edited is an assertion with extra steps")
	}

	err = h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`DELETE FROM regulatory_source_document WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Error("a stored document was deleted; a rule citing it would " +
			"then cite nothing")
	}
}

// The same document, supplied twice, is one document.
//
// The daily re-check exists to run repeatedly and an unchanged source is its
// expected answer every time. Filing a second identical row per run would turn
// a courtesy into a table that grows for ever.
func TestTheSameDocumentTwiceIsOneDocument(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	// Marked once, uploaded twice. `uploadSource` marks every call separately,
	// which is right everywhere else and would defeat this test: the point is
	// that the SAME bytes are the same document.
	document := markFixture(t, labourLawFixture)

	first := h.uploadExactly(t, admin, "SA.EOSB.ENTITLEMENT",
		registryTestCountry, document)
	defer first.Body.Close()
	a := documentOf(t, first)

	second := h.uploadExactly(t, admin, "SA.EOSB.ENTITLEMENT",
		registryTestCountry, document)
	defer second.Body.Close()
	b := documentOf(t, second)

	if a["id"] != b["id"] {
		t.Errorf("the same bytes produced two documents, %v and %v",
			a["id"], b["id"])
	}
	if a["content_sha256"] != b["content_sha256"] {
		t.Error("the same bytes hashed differently")
	}
}

// A payroll settlement computes from the figures this workflow produced.
//
// The end of the chain: a document goes in one end and money comes out the
// other, and every step between is the product's own.
//
// # Why the last hop is a per-tenant rule
//
// The chain would be shorter if this recorded the Saudi entitlement globally
// and let a Saudi shop resolve it. It deliberately does not: `regulatory_rule`
// is append-only and platform-wide, so doing that would permanently restate
// `SA.EOSB.ENTITLEMENT` in every database the suite touches, and the boot
// gate's own tests assert it is still awaiting its figure. That is the rule
// `internal/registry/attest_integration_test.go` states and this file follows.
//
// So the document is applied under `zz`, its recorded payload is read back from
// the registry, and THAT payload — not a copy of it typed into this file — is
// what the Saudi shop computes from, through `regulatory_rule_override`, which
// is how the product supports a tenant-specific reading anyway.
//
// The one thing not exercised is the resolver preferring a global `sa` row,
// which every other test in `eosb_entitlement_test.go` covers. What IS
// exercised, and covered nowhere else, is that the figures a machine read out
// of the Ministry's own wording settle a real person at the right amount.
func TestASettlementComputesFromAnAppliedDocument(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	upload := h.uploadSource(t, admin, "SA.EOSB.ENTITLEMENT", registryTestCountry,
		labourLawFixture)
	defer upload.Body.Close()
	id, _ := documentOf(t, upload)["id"].(string)

	apply := h.do(t, http.MethodPost,
		"/api/v1/platform/regulatory-sources/"+id+"/apply", admin,
		map[string]any{
			"effective_from": nextEffectiveFrom(
				t, h, "SA.EOSB.ENTITLEMENT", registryTestCountry),
			"verified": true,
		})
	defer apply.Body.Close()
	if apply.StatusCode != http.StatusCreated {
		t.Fatalf("apply: %d %s", apply.StatusCode, readBody(t, apply))
	}
	payload, _ := decodeJSON(t, apply)["rule"].(map[string]any)["payload"].(map[string]any)
	if len(payload) == 0 {
		t.Fatal("the applied rule carries no payload")
	}

	field := func(name string) string {
		t.Helper()
		v, ok := payload[name].(string)
		if !ok {
			t.Fatalf("the recorded rule has no %s", name)
		}
		return v
	}

	// Read back out of the registry, not retyped here. A copy of the figures in
	// this file would let the reading drift and the test go on passing.
	f := h.seedShop(t, "owner")
	h.stageEOSBRuleWithFractions(t, f,
		field("wage_basis"),
		field("days_per_year_first_five"),
		field("days_per_year_after_five"),
		field("resignation_fraction_under_two_years"),
		field("resignation_fraction_two_to_five_years"),
		field("resignation_fraction_five_to_ten_years"),
		field("resignation_fraction_over_ten_years"))

	who := h.hire(t, f, "Layla Haddad", "12000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-6, 0, 0).Format("2006-01-02"),
	})

	got := h.settlement(t, f, who, "", "resignation")

	// Six years at 12,000, on the statutory 30-day month: 15 days a year for
	// the first five and 30 for the sixth is 105 days at 400 a day — 42,000.00
	// — and Article 85's two thirds of that on resignation is 28,000.00.
	if v := settled(t, got, "full_award"); v != "42000.00" {
		t.Errorf("the award is %s, want 42000.00", v)
	}
	if v := settled(t, got, "resignation_fraction"); v != "2/3" {
		t.Errorf("the share is %s, want 2/3 as Article 85 states it", v)
	}
	if v := settled(t, got, "award"); v != "28000.00" {
		t.Errorf("the settlement is %s, want 28000.00", v)
	}
	if v := settled(t, got, "wage_basis"); v != "basic_plus_all_allowances" {
		t.Errorf("computed on %q, want the wage the law names", v)
	}
}
