//go:build integration

// The Platform Owner's tax and legal-value screens (A4, E8).
//
// Every unverified value in this product — the Saudi GOSI schedule, the Mudad
// wage-file format, every US district rate — was described as "an operations
// task". It was not one: there was no route and no service method that could
// write a rule, add a tax jurisdiction, or record a rate, so the only way to
// perform the operation was a SQL client against production. These tests drive
// the path that closes that.
//
// What they must NOT do is weaken the refusals. A rate still cannot be invented,
// a placeholder still cannot be recorded as verified, and an unverified figure
// still refuses to price a sale.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func platformAdmin(t *testing.T, h *harness) string {
	t.Helper()
	return h.login(t, h.seedSuperAdmin(t))
}

// newJurisdiction creates a tax authority and returns its id.
func newJurisdiction(
	t *testing.T, h *harness, admin, country, level, code, name string,
	parent string,
) string {
	t.Helper()
	body := map[string]any{
		"country": country, "level": level, "code": code, "name": name,
	}
	if parent != "" {
		body["parent_id"] = parent
	}
	resp := h.do(t, http.MethodPost, "/api/v1/platform/jurisdictions", admin, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create %s %s: %d %s", level, code, resp.StatusCode,
			readBody(t, resp))
	}
	j, _ := decodeJSON(t, resp)["jurisdiction"].(map[string]any)
	id, _ := j["id"].(string)
	if id == "" {
		t.Fatal("the created jurisdiction has no id")
	}
	return id
}

// recordRate puts one authority's rate on file.
func recordRate(
	t *testing.T, h *harness, admin, jurisdictionID, rate, from string,
	verified bool,
) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/"+jurisdictionID+"/rates", admin,
		map[string]any{
			"treatment": "taxable", "rate": rate, "effective_from": from,
			"source_authority": "test",
			"source_document":  "State revenue department rate schedule",
			"source_url":       "https://example.invalid/rates",
			"verified":         verified,
		})
}

// --- authorization --------------------------------------------------------

// Only the Platform Owner may touch the legal values.
//
// A tenant that could edit a tax rate would be choosing what it owes, and one
// that could edit another market's rate would be choosing what somebody else
// owes. Both are Super Admin's alone.
func TestOnlyThePlatformOwnerMayRecordLegalValues(t *testing.T) {
	h := newHarness(t)
	owner := h.seedShop(t, "owner")

	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/api/v1/platform/rules", nil},
		{http.MethodPost, "/api/v1/platform/rules",
			map[string]any{"rule_key": "XX.TEST", "country": "sa"}},
		{http.MethodGet, "/api/v1/platform/jurisdictions", nil},
		{http.MethodPost, "/api/v1/platform/jurisdictions",
			map[string]any{"country": "us", "level": "state", "code": "ZZ",
				"name": "Nowhere"}},
	} {
		resp := h.do(t, c.method, c.path, owner.token, c.body)
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			t.Errorf("a business owner reached %s %s", c.method, c.path)
		}
		resp.Body.Close()
	}
}

// The registry is global, not a tenant's own settings.
//
// A rule recorded by the Platform Owner is the law for every business in that
// country, so it must be visible from the control plane rather than filed
// under whoever happened to type it.
func TestTheRegistryIsGlobalAndReadableByThePlatformOwner(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/platform/rules?country=sa",
		admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list rules: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the Saudi registry is empty; the seeded rules are not visible")
	}

	// The screen exists to show what is still outstanding, so the unverified
	// rows must be in it rather than filtered away.
	sawUnverified := false
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if v, ok := row["verified"].(bool); ok && !v {
			sawUnverified = true
		}
		if row["source_document"] == "" {
			t.Error("a rule is on file with no source document")
		}
	}
	if !sawUnverified {
		t.Error("no unverified rule is listed, yet GOSI and Mudad are " +
			"outstanding — the screen is hiding what it exists to surface")
	}
}

// --- recording a rule -----------------------------------------------------

// A legal value can be recorded with its source and its verifier.
func TestALegalValueCanBeRecordedAndVerified(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	key := "BD.TEST.RATE_" + upperSuffix(8)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": key, "country": "bd",
			"payload":          map[string]any{"rate": "0.15"},
			"effective_from":   "2020-01-01",
			"source_authority": "nbr",
			"source_document":  "VAT and Supplementary Duty Act 2012",
			"source_url":       "https://nbr.gov.bd",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record rule: %d %s", resp.StatusCode, readBody(t, resp))
	}

	rule, _ := decodeJSON(t, resp)["rule"].(map[string]any)
	if v, _ := rule["verified"].(bool); !v {
		t.Error("a rule recorded as verified came back unverified")
	}
	if rule["source_document"] == "" {
		t.Error("the recorded rule carries no source document")
	}

	// And the verifier is named, because verification is a person's assertion
	// rather than a flag.
	var verifiedBy *uuid.UUID
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT verified_by FROM regulatory_rule WHERE rule_key = $1`,
			key).Scan(&verifiedBy)
	}); err != nil {
		t.Fatalf("read the rule back: %v", err)
	}
	if verifiedBy == nil {
		t.Error("the rule is verified and nobody is named as having checked it")
	}
}

// A value recorded without asserting verification stays unverified.
//
// Staging a figure for somebody else to confirm is a real workflow, and it must
// not quietly become an assertion.
func TestARuleRecordedWithoutVerificationStaysUnverified(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	key := "BD.TEST.STAGED_" + upperSuffix(8)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": key, "country": "bd",
			"payload":          map[string]any{"rate": "0.10"},
			"effective_from":   "2020-01-01",
			"source_authority": "nbr",
			"source_document":  "Draft circular pending confirmation",
			"notes":            "Staged for confirmation against the NBR.",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record rule: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rule, _ := decodeJSON(t, resp)["rule"].(map[string]any)
	if v, _ := rule["verified"].(bool); v {
		t.Error("a rule recorded without verification came back verified")
	}
}

// A rule must name the document it came from.
func TestARuleWithoutItsSourceDocumentIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "BD.TEST.NOSOURCE", "country": "bd",
			"payload":          map[string]any{"rate": "0.15"},
			"effective_from":   "2020-01-01",
			"source_authority": "nbr",
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a legal value was recorded with no document behind it")
	}
}

// A placeholder cannot be recorded.
//
// `__VERIFY__` is what makes an unfilled rule refuse loudly. Writing one back
// in — especially as verified — would turn that refusal into a lie.
func TestAPlaceholderPayloadIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.TEST.PLACEHOLDER", "country": "sa",
			"payload":          map[string]any{"employer": "__VERIFY__"},
			"effective_from":   "2024-07-01",
			"source_authority": "gosi",
			"source_document":  "Social Insurance Law",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a placeholder was recorded as a legal value")
	}
	if body := readBody(t, resp); !containsFold(body, "__VERIFY__") {
		t.Errorf("the refusal does not say what is wrong: %s", body)
	}
}

// A rule needs a date, because the product resolves it at the document's date.
func TestARuleWithoutAnEffectiveDateIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "BD.TEST.NODATE", "country": "bd",
			"payload":          map[string]any{"rate": "0.15"},
			"source_authority": "nbr",
			"source_document":  "VAT Act",
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a legal value was recorded with no date it applies from")
	}
}

// A correction supersedes by date rather than overwriting.
//
// Payroll and tax resolve at the date of the document being processed, so
// editing a rule in place would silently restate every period the old figure
// governed. Re-running last year must still give last year's answer.
func TestACorrectionSupersedesRatherThanOverwrites(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	key := "BD.TEST.SUPERSEDE_" + upperSuffix(8)

	record := func(rate, from string) {
		resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
			map[string]any{
				"rule_key": key, "country": "bd",
				"payload":          map[string]any{"rate": rate},
				"effective_from":   from,
				"source_authority": "nbr",
				"source_document":  "VAT Act, rate schedule",
				"verified":         true,
			})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("record %s from %s: %d %s", rate, from, resp.StatusCode,
				readBody(t, resp))
		}
	}

	record("0.15", "2020-01-01")
	record("0.10", "2026-01-01")

	var rows, closed int
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT count(*) FROM regulatory_rule WHERE rule_key = $1`,
			key).Scan(&rows); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM regulatory_rule
			WHERE rule_key = $1 AND effective_to IS NOT NULL`, key).Scan(&closed)
	}); err != nil {
		t.Fatalf("read the rules back: %v", err)
	}

	if rows != 2 {
		t.Errorf("the registry holds %d rows for %s, want 2 — the correction "+
			"overwrote history instead of superseding it", rows, key)
	}
	if closed != 1 {
		t.Errorf("%d superseded rows are closed off, want 1 — the old figure "+
			"is still open-ended and would collide with the new one", closed)
	}
}

// --- jurisdictions and rates ----------------------------------------------

// A tax authority can be added and listed.
func TestATaxAuthorityCanBeAddedAndListed(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	id := stateUnderACountry(t, h, admin, "T")

	resp := h.do(t, http.MethodGet, "/api/v1/platform/jurisdictions?country=us",
		admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list jurisdictions: %d", resp.StatusCode)
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	found := false
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["id"] == id {
			found = true
		}
	}
	if !found {
		t.Error("the created authority is not in the country's list")
	}
}

// A rate can be put on file, and it must name its source.
func TestAJurisdictionRateNeedsItsSource(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	id := stateUnderACountry(t, h, admin, "S")

	bad := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/"+id+"/rates", admin,
		map[string]any{
			"treatment": "taxable", "rate": "0.0625",
			"effective_from": "2020-01-01", "source_authority": "test",
		})
	defer bad.Body.Close()
	if bad.StatusCode == http.StatusNoContent || bad.StatusCode == http.StatusOK {
		t.Error("a tax rate was recorded with no document behind it")
	}

	good := recordRate(t, h, admin, id, "0.0625", "2020-01-01", true)
	defer good.Body.Close()
	if good.StatusCode != http.StatusNoContent {
		t.Errorf("recording a sourced rate: %d %s", good.StatusCode,
			readBody(t, good))
	}
}

// A negative rate is refused.
func TestANegativeJurisdictionRateIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	id := stateUnderACountry(t, h, admin, "N")

	resp := recordRate(t, h, admin, id, "-0.05", "2020-01-01", true)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Error("a negative tax rate was recorded")
	}
}

// A later rate closes the one before it rather than overlapping.
//
// `tax_jurisdiction_rate` forbids overlapping ranges for a jurisdiction and
// treatment, so a correction that did not close the old row would simply be
// rejected by the database. Closing it is also what keeps last quarter's
// invoices explainable by last quarter's rate.
func TestALaterRateClosesTheOneBeforeIt(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	id := stateUnderACountry(t, h, admin, "O")

	first := recordRate(t, h, admin, id, "0.0600", "2020-01-01", true)
	first.Body.Close()
	if first.StatusCode != http.StatusNoContent {
		t.Fatalf("first rate: %d", first.StatusCode)
	}

	second := recordRate(t, h, admin, id, "0.0700", "2026-01-01", true)
	defer second.Body.Close()
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("a later rate was rejected, so a correction is impossible: "+
			"%d %s", second.StatusCode, readBody(t, second))
	}

	var open int
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM tax_jurisdiction_rate
			WHERE jurisdiction_id = $1 AND effective_to IS NULL`,
			id).Scan(&open)
	}); err != nil {
		t.Fatalf("read the rates back: %v", err)
	}
	if open != 1 {
		t.Errorf("%d rates are open-ended for one authority, want 1", open)
	}
}

// --- the engine actually consumes what was recorded -----------------------

// A shop can trade once the rates behind it are on file.
//
// The whole point. A US shop refuses to sell until every authority above it has
// a rate, and before this path existed the only way to give it one was a SQL
// client. This drives the real route and then rings up a real sale.
func TestRecordingRatesThroughThePlatformLetsAShopTrade(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := uuid.NewString()[:6]

	country := newJurisdiction(t, h, admin, "us", "country",
		"C"+suffix, "Testland", "")
	state := newJurisdiction(t, h, admin, "us", "state",
		"S"+suffix, "Test State", country)
	city := newJurisdiction(t, h, admin, "us", "city",
		"T"+suffix, "Test City", state)

	// The country levies nothing and says so; the state and city levy.
	for _, r := range []struct{ id, rate string }{
		{country, "0"}, {state, "0.0625"}, {city, "0.0200"},
	} {
		resp := recordRate(t, h, admin, r.id, r.rate, "2020-01-01", true)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("record %s: %d", r.rate, resp.StatusCode)
		}
	}

	cityID, _ := uuid.Parse(city)
	f := usShop(t, h, &cityID)

	if got := taxOf(t, h, f, usSale(f, "100.00")); got != "8.25" && got != "8.2500" {
		t.Errorf("tax = %s, want 8.25 — the rates recorded through the "+
			"platform are not the ones the till charged", got)
	}
}

// An unverified rate still refuses in a deployment that requires verification.
//
// The path that records rates must not become a way around the gate. What
// changed is that a figure can now be entered; what did not change is that an
// unchecked one cannot price a sale.
func TestRecordingAnUnverifiedRateDoesNotMarkItVerified(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	id := stateUnderACountry(t, h, admin, "U")

	resp := recordRate(t, h, admin, id, "0.0625", "2020-01-01", false)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("record rate: %d", resp.StatusCode)
	}

	var verified bool
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT verified_on IS NOT NULL FROM tax_jurisdiction_rate
			WHERE jurisdiction_id = $1`, id).Scan(&verified)
	}); err != nil {
		t.Fatalf("read the rate back: %v", err)
	}
	if verified {
		t.Error("a rate recorded without verification was stamped verified")
	}
}

// upperSuffix is a unique fragment a rule key may legally contain.
//
// `regulatory_rule_key_format` is ^[A-Z]{2}\.[A-Z0-9_]+\.[A-Z0-9_]+$, so the
// lowercase hex of a raw uuid is refused — correctly, because a registry whose
// keys differ only by case would resolve two rules for one thing.
func upperSuffix(n int) string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:n])
}

// stateUnderACountry creates a state with the country it belongs to.
//
// `tax_jurisdiction_root_is_country` makes a parent mandatory for everything
// below country level, which is what stops a city floating free of the state
// whose tax it is partly charging.
func stateUnderACountry(t *testing.T, h *harness, admin, prefix string) string {
	t.Helper()
	suffix := upperSuffix(5)
	country := newJurisdiction(t, h, admin, "us", "country",
		"C"+suffix, "Testland", "")
	return newJurisdiction(t, h, admin, "us", "state",
		prefix+suffix, "Test State", country)
}

// --- bulk ingestion -------------------------------------------------------

// A published schedule loads whole, and the shop it covers can then trade.
//
// California is the worked example because CDTFA publishes a real one: a 7.25%
// statewide rate plus district taxes of 0.10% to 2.00%, issued as a quarterly
// spreadsheet. The figures below are the SHAPE of that schedule with fixture
// codes, not California's actual districts — those are loaded from CDTFA's own
// file, and inventing them here would put made-up tax in a test that looks
// authoritative.
func TestAPublishedScheduleLoadsWholeAndLetsAShopTrade(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := upperSuffix(5)

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/import", admin, map[string]any{
			"country": "us", "treatment": "taxable",
			"effective_from":   "2020-01-01",
			"source_authority": "test",
			"source_document":  "State schedule, first quarter",
			"verified":         true,
			"rows": []map[string]any{
				{"level": "country", "code": "C" + suffix, "name": "Testland",
					"rate": "0"},
				{"level": "state", "code": "S" + suffix, "name": "Test State",
					"parent_code": "C" + suffix, "rate": "0.0625"},
				{"level": "city", "code": "T" + suffix, "name": "Test City",
					"parent_code": "S" + suffix, "rate": "0.0200"},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, readBody(t, resp))
	}
	result, _ := decodeJSON(t, resp)["result"].(map[string]any)
	if n, _ := result["rates"].(float64); int(n) != 3 {
		t.Errorf("the import recorded %v rates, want 3", result["rates"])
	}

	// The whole point: a shop in the loaded city can now be taxed.
	city := jurisdictionByCode(t, h, admin, "T"+suffix)
	f := usShop(t, h, &city)
	if got := taxOf(t, h, f, usSale(f, "100.00")); got != "8.25" && got != "8.2500" {
		t.Errorf("tax = %s, want 8.25 — the imported schedule is not what "+
			"the till charged", got)
	}
}

// A row whose parent is not on file is skipped and named, not reparented.
//
// A district silently attached to the country root would charge the state's
// tax and not the county's, and nothing downstream could tell.
func TestAnImportedRowWithAnUnknownParentIsSkippedAndNamed(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := upperSuffix(5)

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/import", admin, map[string]any{
			"country": "us", "effective_from": "2020-01-01",
			"source_authority": "test",
			"source_document":  "State schedule",
			"rows": []map[string]any{
				{"level": "country", "code": "C" + suffix, "name": "Testland",
					"rate": "0"},
				{"level": "city", "code": "X" + suffix, "name": "Orphan City",
					"parent_code": "NOSUCH" + suffix, "rate": "0.02"},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, readBody(t, resp))
	}
	result, _ := decodeJSON(t, resp)["result"].(map[string]any)
	skipped, _ := result["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("the import skipped %d rows, want 1: %v", len(skipped),
			result)
	}
	if s, _ := skipped[0].(string); !containsFold(s, "parent") {
		t.Errorf("the skip does not say why: %q", s)
	}
}

// A schedule must name the document it came from.
func TestAnImportWithoutItsSourceDocumentIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/import", admin, map[string]any{
			"country": "us", "effective_from": "2020-01-01",
			"source_authority": "test",
			"rows": []map[string]any{
				{"level": "country", "code": "C" + upperSuffix(5),
					"name": "Testland", "rate": "0"},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a rate schedule was loaded with no document behind it")
	}
}

// Loading next quarter's schedule closes the one before it.
//
// The exclusion constraint forbids overlapping ranges, so a reload that did not
// close the old rates would simply be rejected — and closing them is what keeps
// last quarter's invoices explainable by last quarter's rate.
func TestReloadingAScheduleSupersedesTheRatesBeforeIt(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := upperSuffix(5)

	load := func(rate, from string) *http.Response {
		return h.do(t, http.MethodPost,
			"/api/v1/platform/jurisdictions/import", admin, map[string]any{
				"country": "us", "effective_from": from,
				"source_authority": "test",
				"source_document":  "State schedule for " + from,
				"verified":         true,
				"rows": []map[string]any{
					{"level": "country", "code": "C" + suffix,
						"name": "Testland", "rate": "0"},
					{"level": "state", "code": "S" + suffix,
						"name": "Test State", "parent_code": "C" + suffix,
						"rate": rate},
				},
			})
	}

	first := load("0.0600", "2020-01-01")
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first load: %d", first.StatusCode)
	}
	second := load("0.0700", "2026-01-01")
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("reload was rejected, so a quarterly update is impossible: "+
			"%d %s", second.StatusCode, readBody(t, second))
	}

	state := jurisdictionByCode(t, h, admin, "S"+suffix)
	var open int
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM tax_jurisdiction_rate
			WHERE jurisdiction_id = $1 AND effective_to IS NULL`,
			state).Scan(&open)
	}); err != nil {
		t.Fatalf("read the rates: %v", err)
	}
	if open != 1 {
		t.Errorf("%d rates are open-ended after a reload, want 1", open)
	}
}

// Only the Platform Owner may load a schedule.
func TestOnlyThePlatformOwnerMayImportASchedule(t *testing.T) {
	h := newHarness(t)
	owner := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/jurisdictions/import", owner.token,
		map[string]any{"country": "us"})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a business owner loaded a tax schedule")
	}
}

// jurisdictionByCode finds a loaded authority's id.
func jurisdictionByCode(
	t *testing.T, h *harness, admin, code string,
) uuid.UUID {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/jurisdictions?country=us", admin, nil)
	defer resp.Body.Close()
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["code"] == code {
			id, _ := uuid.Parse(row["id"].(string))
			return id
		}
	}
	t.Fatalf("no jurisdiction with code %s", code)
	return uuid.Nil
}
