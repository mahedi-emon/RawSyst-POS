//go:build integration

// The setup wizard, over the routes the Back Office screen actually calls.
//
// TestProvisionTenantThenOwnerCompletesOnboarding already walks the happy path
// from Super Admin to a committed company. These cover what the wizard added on
// top of it: the ZATCA obligation the Tax step captures, who may change setup
// and who may only watch, and that one tenant's answers are invisible to
// another.
//
// The ZATCA half matters most. Blueprint E1.0 is emphatic that the software
// must never assume or assert a taxpayer's wave — it comes from ZATCA to the
// taxpayer directly — so the wizard asks for it and the server records exactly
// what it was told. `zatca_deadline` was asked for by UI spec §6 and dropped on
// the floor until this milestone: the column has existed since 0002 and
// CommitBusinessInfo never wrote it, which is worse than not asking, because a
// shop would believe the product knew their date.
package api

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// setupFixture is a tenant whose Owner is standing at step one.
type setupFixture struct {
	tenantID uuid.UUID
	token    string
	email    string
}

func (h *harness) seedSetup(t *testing.T, roleKey string) *setupFixture {
	t.Helper()
	email := h.seedUserWithRole(t, roleKey)

	var tenantID uuid.UUID
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT tenant_id FROM app_user WHERE email = $1`, email).Scan(&tenantID); e != nil {
			return e
		}
		// seedUserWithRole creates neither of these; provisioning does. The
		// plan ceiling matters because committing a company reads it rather
		// than trusting the client with it.
		if _, e := tx.Exec(t.Context(),
			`INSERT INTO onboarding_progress (tenant_id) VALUES ($1)
			 ON CONFLICT DO NOTHING`, tenantID); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			INSERT INTO tenant_limit
			  (tenant_id, max_companies, max_stores, max_users, max_terminals,
			   max_skus, max_held_carts, max_custom_roles, max_storage_mb, sms_credits)
			SELECT $1, max_companies, max_stores, max_users, max_terminals,
			       max_skus, max_held_carts, max_custom_roles, max_storage_mb, sms_credits
			FROM plan_tier_default WHERE tier = 'professional'::plan_tier
			ON CONFLICT DO NOTHING`, tenantID)
		return e
	}); err != nil {
		t.Fatalf("seed setup for %s: %v", roleKey, err)
	}

	return &setupFixture{tenantID: tenantID, token: h.login(t, email), email: email}
}

// saveStep and completeStep mirror exactly what the wizard sends.
func (h *harness) saveStep(t *testing.T, f *setupFixture, step string, answers any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPut, "/api/v1/onboarding/steps/"+step, f.token, answers)
}

func (h *harness) completeStep(t *testing.T, f *setupFixture, step string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/onboarding/steps/"+step+"/complete", f.token, nil)
}

// businessAnswers is a complete, valid business. The VAT number is unique per
// call because it is unique across the platform — two tenants cannot both claim
// the same registration, which is the point of the constraint and would
// otherwise make these tests collide with each other.
func businessAnswers() map[string]any {
	return map[string]any{
		"legal_name":     "Olaya Trading Company",
		"legal_name_ar":  "شركة العليا التجارية",
		"trade_name":     "Olaya",
		"country":        "sa",
		"base_currency":  "SAR",
		"cr_number":      "1010101010",
		"vat_registered": true,
		"vat_number":     uniqueSaudiVAT(),
	}
}

// A Saudi VAT number is 15 digits starting and ending with 3.
//
// The thirteen in between come from the uuid's BITS, not from hunting its hex
// text for digit characters. The first version did the latter and walked off
// the end of the string whenever a uuid happened to contain fewer than
// thirteen digits among its thirty-two hex chars — which is 0.24% of uuids,
// and so roughly one full integration run in seventy died on an index-out-of-
// range panic a long way from anything that explained it.
func uniqueSaudiVAT() string {
	id := uuid.New()
	// 10^13 is the number of thirteen-digit values; the modulo keeps the
	// padding below honest rather than truncating a longer number.
	n := binary.BigEndian.Uint64(id[:8]) % 10_000_000_000_000
	return fmt.Sprintf("3%013d3", n)
}

// The wave and the deadline reach the company record.
//
// Both are captured, never computed. The test asserts the exact strings the
// Owner typed come back off the row, because the only thing this product may
// claim about a taxpayer's obligation is what the taxpayer told it.
func TestTheZATCAObligationIsRecordedAsTheTaxpayerStatedIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	for _, s := range []struct {
		step    string
		answers any
	}{
		{"business_info", businessAnswers()},
		{"stores", map[string]any{
			"stores": []map[string]any{{"code": "RYD", "name": "Olaya branch", "street": "Prince Sultan Road", "building_number": "2322", "district": "Al-Murabba", "city": "Riyadh", "postal_code": "23333", "country_code": "SA"}},
		}},
		// UI spec §6 puts the ZATCA dates on the Tax step, and this is where
		// the wizard sends them.
		{"tax", map[string]any{
			"zatca_wave":     "Wave 12",
			"zatca_deadline": "2026-01-01",
		}},
		{"employees", map[string]any{}},
		{"hardware", map[string]any{}},
		{"opening_balances", map[string]any{}},
	} {
		save := h.saveStep(t, f, s.step, s.answers)
		if save.StatusCode != http.StatusNoContent && save.StatusCode != http.StatusOK {
			t.Fatalf("save %s: status %d — %s", s.step, save.StatusCode, readBody(t, save))
		}
		save.Body.Close()

		done := h.completeStep(t, f, s.step)
		if done.StatusCode != http.StatusOK {
			t.Fatalf("complete %s: status %d — %s", s.step, done.StatusCode, readBody(t, done))
		}
		done.Body.Close()
	}

	commit := h.do(t, http.MethodPost, "/api/v1/onboarding/company", f.token, nil)
	if commit.StatusCode != http.StatusCreated {
		t.Fatalf("commit: status %d — %s", commit.StatusCode, readBody(t, commit))
	}
	companyID, _ := decodeJSON(t, commit)["company_id"].(string)

	var wave, deadline, status string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(zatca_wave, ''),
			       coalesce(to_char(zatca_deadline, 'YYYY-MM-DD'), ''),
			       zatca_status::text
			FROM company WHERE id = $1`, companyID).Scan(&wave, &deadline, &status)
	}); err != nil {
		t.Fatalf("read company: %v", err)
	}

	if wave != "Wave 12" {
		t.Errorf("zatca_wave = %q, want the wave the taxpayer stated", wave)
	}
	if deadline != "2026-01-01" {
		t.Errorf("zatca_deadline = %q, want 2026-01-01 — the date was dropped", deadline)
	}

	// Creating a business does not advance a ZATCA position. Onboarding a unit
	// is a separate act behind a separate permission, and its request formats
	// are unverified release blockers.
	if status != "not_started" {
		t.Errorf("zatca_status = %q after setup, want not_started; the wizard "+
			"must not claim any ZATCA standing", status)
	}
}

// A business with no ZATCA notification yet commits with both fields empty.
//
// The commonest case for a new shop, and the one a product that guessed would
// get wrong by inventing a wave.
func TestABusinessWithNoZATCANotificationStillCommits(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	steps := []struct {
		step    string
		answers any
	}{
		{"business_info", businessAnswers()},
		{"stores", map[string]any{
			"stores": []map[string]any{{"code": "JED", "name": "Jeddah", "street": "Prince Sultan Road", "building_number": "2322", "district": "Al-Murabba", "city": "Riyadh", "postal_code": "23333", "country_code": "SA"}},
		}},
		{"tax", map[string]any{"zatca_wave": "", "zatca_deadline": ""}},
		{"employees", map[string]any{}},
		{"hardware", map[string]any{}},
		{"opening_balances", map[string]any{}},
	}
	for _, s := range steps {
		h.saveStep(t, f, s.step, s.answers).Body.Close()
		resp := h.completeStep(t, f, s.step)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("complete %s: status %d — %s", s.step, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	commit := h.do(t, http.MethodPost, "/api/v1/onboarding/company", f.token, nil)
	if commit.StatusCode != http.StatusCreated {
		t.Fatalf("commit: status %d — %s", commit.StatusCode, readBody(t, commit))
	}
	companyID, _ := decodeJSON(t, commit)["company_id"].(string)

	var waveNull, deadlineNull bool
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT zatca_wave IS NULL, zatca_deadline IS NULL
			FROM company WHERE id = $1`, companyID).Scan(&waveNull, &deadlineNull)
	}); err != nil {
		t.Fatalf("read company: %v", err)
	}
	if !waveNull || !deadlineNull {
		t.Errorf("a business with no notification recorded wave-null=%v deadline-null=%v; "+
			"blank must stay blank rather than becoming a guess", waveNull, deadlineNull)
	}
}

// The wizard is resumable, which is the whole reason saving and completing are
// two calls.
//
// A half-filled step must persist. If saving validated, an Owner who stopped
// mid-form would come back to an empty one and conclude the product lost their
// work.
func TestAHalfFilledStepSurvivesAndCanBeResumed(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	// Nothing but a name — not enough to complete the step.
	partial := map[string]any{"legal_name": "Half Written Co"}
	save := h.saveStep(t, f, "business_info", partial)
	if save.StatusCode != http.StatusNoContent && save.StatusCode != http.StatusOK {
		t.Fatalf("save a partial step: status %d — %s", save.StatusCode, readBody(t, save))
	}
	save.Body.Close()

	// Completing it is refused, and the refusal names the fields.
	resp := h.completeStep(t, f, "business_info")
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("completing a half-filled step: status %d, want a refusal — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// And what was typed is still there when the Owner comes back.
	resp = h.do(t, http.MethodGet, "/api/v1/onboarding", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read progress: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if body["current_step"] != "business_info" {
		t.Errorf("current step = %v, want business_info", body["current_step"])
	}

	data, _ := json.Marshal(body["step_data"])
	var saved struct {
		BusinessInfo struct {
			LegalName string `json:"legal_name"`
		} `json:"business_info"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode step data: %v", err)
	}
	if saved.BusinessInfo.LegalName != "Half Written Co" {
		t.Errorf("the half-filled answer was lost: %q", saved.BusinessInfo.LegalName)
	}
}

// QA gate M7 on the setup routes.
//
// The wizard shows a read-only banner to a login holding identity.view without
// identity.edit. That is a courtesy; this is the gate behind it.
func TestSetupCanBeReadWithoutBeingChanged(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	// An auditor: reads everything, edits nothing.
	auditor := h.seedUserInTenant(t, f.tenantID, "auditor")

	read := h.do(t, http.MethodGet, "/api/v1/onboarding", auditor, nil)
	if read.StatusCode != http.StatusOK {
		t.Fatalf("auditor reading setup: status %d, want 200 — %s",
			read.StatusCode, readBody(t, read))
	}
	read.Body.Close()

	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"save a step", http.MethodPut, "/api/v1/onboarding/steps/business_info",
			businessAnswers()},
		{"complete a step", http.MethodPost,
			"/api/v1/onboarding/steps/business_info/complete", nil},
		{"commit the company", http.MethodPost, "/api/v1/onboarding/company", nil},
	} {
		resp := h.do(t, c.method, c.path, auditor, c.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("auditor trying to %s: status %d, want 403 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

// A cashier cannot reach setup at all: they hold neither identity.view nor
// identity.edit, so the section does not appear and the routes refuse them.
func TestACashierCannotReachSetup(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	cashier := h.seedUserInTenant(t, f.tenantID, "cashier")

	resp := h.do(t, http.MethodGet, "/api/v1/onboarding", cashier, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cashier reading setup: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M8: one business's setup answers are invisible to another.
//
// Setup carries a legal name, a commercial registration and a VAT number
// before any company row exists, so this is the window where a leak would be
// easiest to miss.
func TestOneTenantCannotSeeAnothersSetupAnswers(t *testing.T) {
	h := newHarness(t)
	a := h.seedSetup(t, "owner")
	b := h.seedSetup(t, "owner")

	secret := businessAnswers()
	secret["legal_name"] = "Alpha Confidential Holdings"
	secret["vat_number"] = "300000000000003"
	h.saveStep(t, a, "business_info", secret).Body.Close()

	// Tenant B reads its own progress and sees nothing of A's.
	resp := h.do(t, http.MethodGet, "/api/v1/onboarding", b.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant B progress: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := json.Marshal(decodeJSON(t, resp))
	if containsText(string(raw), "Alpha Confidential") ||
		containsText(string(raw), "300000000000003") {
		t.Fatal("one tenant's setup answers leaked into another's progress")
	}

	// And the row itself is unreachable in B's context.
	var visible int
	if err := h.pool.TxAsTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM onboarding_progress WHERE tenant_id = $1`,
			a.tenantID).Scan(&visible)
	}); err != nil {
		t.Fatalf("query as tenant B: %v", err)
	}
	if visible != 0 {
		t.Fatal("tenant B can read tenant A's onboarding row")
	}
}

// Setup cannot be reopened once it is finished, so a committed business cannot
// have its identity rewritten through the wizard.
func TestFinishedSetupRefusesFurtherEdits(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	for _, s := range []struct {
		step    string
		answers any
	}{
		{"business_info", businessAnswers()},
		{"stores", map[string]any{
			"stores": []map[string]any{{"code": "RYD", "name": "Olaya", "street": "Prince Sultan Road", "building_number": "2322", "district": "Al-Murabba", "city": "Riyadh", "postal_code": "23333", "country_code": "SA"}},
		}},
		{"tax", map[string]any{}},
		{"employees", map[string]any{}},
		{"hardware", map[string]any{}},
		{"opening_balances", map[string]any{}},
	} {
		h.saveStep(t, f, s.step, s.answers).Body.Close()
		resp := h.completeStep(t, f, s.step)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("complete %s: status %d — %s", s.step, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// Completing the last real step is what finishes setup; there is no
	// separate act for the terminal 'finished' marker.
	read := h.do(t, http.MethodGet, "/api/v1/onboarding", f.token, nil)
	if read.StatusCode != http.StatusOK {
		t.Fatalf("read progress: status %d — %s", read.StatusCode, readBody(t, read))
	}
	if finished, _ := decodeJSON(t, read)["finished"].(bool); !finished {
		t.Fatal("completing every step did not mark setup finished")
	}

	// A save now is refused rather than silently ignored.
	resp := h.saveStep(t, f, "business_info", map[string]any{"legal_name": "Renamed Ltd"})
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		t.Fatal("a finished setup accepted a new answer; the business identity " +
			"every invoice carries would be rewritable after the fact")
	}
	resp.Body.Close()
}

func containsText(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// The blocker 0078 closed, asserted end to end.
//
// Before it, a tenant could complete every step of this wizard — business,
// branches, tax, staff, hardware, opening balances — and then be refused on the
// first sale with:
//
//	"This branch has no stock location set up, so there is nothing to sell
//	 from. An owner can add one under Settings > Stock locations."
//
// There was no Settings > Stock locations screen, no route behind one, and
// nothing in the entire product had ever inserted a `warehouse` row. The only
// INSERTs against that table lived in test fixtures, which is exactly why every
// test passed while no real shop could trade.
//
// So the assertion is not "the wizard wrote a row". It is `resolveWarehouse`'s
// own question, asked the way the sales path asks it: is there somewhere active
// this branch can sell from.
func TestAShopCanSellTheDayItsBranchIsCreated(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")

	for _, s := range []struct {
		step    string
		answers any
	}{
		{"business_info", businessAnswers()},
		{"stores", map[string]any{
			"stores": []map[string]any{{
				"code": "RYD", "name": "Olaya branch",
				"street": "Prince Sultan Road", "building_number": "2322",
				"district": "Al-Murabba", "city": "Riyadh",
				"postal_code": "23333", "country_code": "SA",
			}},
		}},
		{"tax", map[string]any{}},
		{"employees", map[string]any{}},
		{"hardware", map[string]any{}},
		{"opening_balances", map[string]any{}},
	} {
		save := h.saveStep(t, f, s.step, s.answers)
		if save.StatusCode != http.StatusNoContent && save.StatusCode != http.StatusOK {
			t.Fatalf("save %s: status %d — %s", s.step, save.StatusCode, readBody(t, save))
		}
		save.Body.Close()
		done := h.completeStep(t, f, s.step)
		if done.StatusCode != http.StatusOK {
			t.Fatalf("complete %s: status %d — %s", s.step, done.StatusCode, readBody(t, done))
		}
		done.Body.Close()
	}

	commit := h.do(t, http.MethodPost, "/api/v1/onboarding/company", f.token, nil)
	if commit.StatusCode != http.StatusCreated {
		t.Fatalf("commit: status %d — %s", commit.StatusCode, readBody(t, commit))
	}
	companyID, _ := decodeJSON(t, commit)["company_id"].(string)

	// The branches, which are a separate call the wizard makes once the company
	// exists — the answers collected at the `stores` step are only stored until
	// there is a company to hang them on.
	stores := h.do(t, http.MethodPost, "/api/v1/onboarding/stores", f.token,
		map[string]any{"company_id": companyID})
	if stores.StatusCode != http.StatusCreated && stores.StatusCode != http.StatusOK {
		t.Fatalf("create branches: status %d — %s",
			stores.StatusCode, readBody(t, stores))
	}
	stores.Body.Close()

	var sellableFrom int
	var locationName string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*), coalesce(min(w.name), '')
			FROM store s
			JOIN warehouse w
			  ON w.company_id = s.company_id
			 AND w.is_active
			 AND w.kind <> 'transit'
			 AND (w.store_id = s.id OR w.store_id IS NULL)
			WHERE s.company_id = $1`, companyID).Scan(&sellableFrom, &locationName)
	}); err != nil {
		t.Fatalf("look for somewhere to sell from: %v", err)
	}

	if sellableFrom == 0 {
		t.Fatal("the branch has nowhere to sell from, so the first sale would " +
			"be refused — which is the state every tenant was left in before " +
			"0078, after completing the whole wizard")
	}
	if locationName != "Olaya branch" {
		t.Errorf("the stock location should be named after its branch, not %q",
			locationName)
	}
}
