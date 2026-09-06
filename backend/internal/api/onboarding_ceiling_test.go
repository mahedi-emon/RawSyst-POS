//go:build integration

// The plan ceiling on the wizard's branch step, counted as additions.
//
// `POST /onboarding/stores` upserts on (company_id, code) and reads the answers
// the wizard saved, so calling it twice is the same act twice — which is what a
// retry after a timeout is, and what the client does when the first response is
// lost. The ceiling check summed `existing + len(payload)` regardless, so a
// tenant whose branches exactly fill its plan was refused on the second call,
// and the refusal said its plan was full when the submission added nothing.
//
// The per-branch route added later compares rather than sums and has its own
// test for amending at the ceiling. This is the same guarantee for the wizard
// path, which kept the old arithmetic.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// storeAnswer is one branch as the wizard sends it.
func storeAnswer(code, name string) map[string]any {
	return map[string]any{
		"code": code, "name": name,
		"street": "Prince Sultan Road", "building_number": "2322",
		"district": "Al-Murabba", "city": "Riyadh",
		"postal_code": "23333", "country_code": "SA",
	}
}

func setStoreCeiling(t *testing.T, h *harness, f *setupFixture, ceiling int) {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE tenant_limit SET max_stores = $2 WHERE tenant_id = $1`,
			f.tenantID, ceiling)
		return e
	}); err != nil {
		t.Fatalf("set the ceiling to %d: %v", ceiling, err)
	}
}

// commitCompanyWithStores runs the wizard to a committed company and answers
// its id.
func commitCompanyWithStores(
	t *testing.T, h *harness, f *setupFixture, stores []map[string]any,
) string {
	t.Helper()
	for _, s := range []struct {
		step    string
		answers any
	}{
		{"business_info", businessAnswers()},
		{"stores", map[string]any{"stores": stores}},
		{"tax", map[string]any{}},
		{"employees", map[string]any{}},
		{"hardware", map[string]any{}},
		{"opening_balances", map[string]any{}},
	} {
		save := h.saveStep(t, f, s.step, s.answers)
		if save.StatusCode != http.StatusNoContent && save.StatusCode != http.StatusOK {
			t.Fatalf("save %s: %d — %s", s.step, save.StatusCode, readBody(t, save))
		}
		save.Body.Close()
		done := h.completeStep(t, f, s.step)
		if done.StatusCode != http.StatusOK {
			t.Fatalf("complete %s: %d — %s", s.step, done.StatusCode, readBody(t, done))
		}
		done.Body.Close()
	}

	commit := h.do(t, http.MethodPost, "/api/v1/onboarding/company", f.token, nil)
	defer commit.Body.Close()
	if commit.StatusCode != http.StatusCreated {
		t.Fatalf("commit: %d — %s", commit.StatusCode, readBody(t, commit))
	}
	id, _ := decodeJSONFrom(t, commit)["company_id"].(string)
	return id
}

func createStores(t *testing.T, h *harness, f *setupFixture, companyID string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost, "/api/v1/onboarding/stores", f.token,
		map[string]any{"company_id": companyID})
}

func storeCount(t *testing.T, h *harness, f *setupFixture, companyID string) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM store WHERE company_id = $1`, companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count the branches: %v", err)
	}
	return n
}

// A shop whose branches exactly fill its plan can run the step again.
func TestResubmittingTheBranchesAShopAlreadyHasIsNotAnAddition(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")
	setStoreCeiling(t, h, f, 2)

	companyID := commitCompanyWithStores(t, h, f, []map[string]any{
		storeAnswer("RYD", "Olaya branch"),
		storeAnswer("JED", "Jeddah branch"),
	})

	first := createStores(t, h, f, companyID)
	if first.StatusCode != http.StatusCreated && first.StatusCode != http.StatusOK {
		t.Fatalf("creating the two branches: %d — %s",
			first.StatusCode, readBody(t, first))
	}
	first.Body.Close()
	if n := storeCount(t, h, f, companyID); n != 2 {
		t.Fatalf("the first call created %d branches, want 2", n)
	}

	// The same call again. Two branches in, two branches already here, so it
	// adds nothing — and a submission that adds nothing cannot exceed a limit.
	again := createStores(t, h, f, companyID)
	body := readBody(t, again)
	again.Body.Close()
	if again.StatusCode != http.StatusCreated && again.StatusCode != http.StatusOK {
		t.Fatalf("re-running the branch step at the ceiling answered %d, "+
			"want success — it adds nothing: %s", again.StatusCode, body)
	}
	if n := storeCount(t, h, f, companyID); n != 2 {
		t.Errorf("the re-run left %d branches, want 2", n)
	}
}

// A genuine addition past the ceiling is still refused, and adds nothing.
//
// The point of the fix is arithmetic, not permissiveness.
func TestBranchesBeyondThePlanCeilingAreStillRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")
	setStoreCeiling(t, h, f, 1)

	companyID := commitCompanyWithStores(t, h, f, []map[string]any{
		storeAnswer("RYD", "Olaya branch"),
		storeAnswer("JED", "Jeddah branch"),
	})

	refused := createStores(t, h, f, companyID)
	body := readBody(t, refused)
	refused.Body.Close()
	if refused.StatusCode != http.StatusConflict {
		t.Fatalf("two branches on a ceiling of one answered %d, want 409: %s",
			refused.StatusCode, body)
	}
	if n := storeCount(t, h, f, companyID); n != 0 {
		t.Errorf("the refused call left %d branches, want none — the whole "+
			"submission is one transaction", n)
	}
}

// One code named twice never reaches the ceiling check.
//
// Recorded because the ceiling now counts DISTINCT codes, and a reader could
// reasonably ask whether that de-duplication is load-bearing. It is not: the
// step refuses the duplicate first, with a better sentence than a limit error
// would be. The de-duplication stays as the arithmetic being correct rather
// than accidentally correct, and this test pins where the real guard lives so
// that moving it is a visible decision.
func TestTwoBranchesSharingACodeAreRefusedAtTheStep(t *testing.T) {
	h := newHarness(t)
	f := h.seedSetup(t, "owner")
	setStoreCeiling(t, h, f, 5)

	// Steps complete in order, so the business comes first.
	info := h.saveStep(t, f, "business_info", businessAnswers())
	info.Body.Close()
	firstDone := h.completeStep(t, f, "business_info")
	firstDone.Body.Close()

	save := h.saveStep(t, f, "stores", map[string]any{
		"stores": []map[string]any{
			storeAnswer("RYD", "Olaya branch"),
			storeAnswer("RYD", "Olaya branch, corrected"),
		},
	})
	save.Body.Close()

	done := h.completeStep(t, f, "stores")
	body := readBody(t, done)
	done.Body.Close()
	if done.StatusCode != http.StatusBadRequest {
		t.Fatalf("two branches sharing a code answered %d, want 400: %s",
			done.StatusCode, body)
	}
	if !strings.Contains(body, "share the code") {
		t.Errorf("the refusal does not say which code is duplicated: %s", body)
	}
}
