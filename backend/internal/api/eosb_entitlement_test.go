//go:build integration

// The end-of-service award, and the two things about it that are not one
// number: the service band and the wage it is computed on (blueprint E6).
//
// `SA.EOSB.ENTITLEMENT` carries six fields. The accrual read one of them —
// `days_per_year_first_five` — and applied it to every year of everybody's
// service, on a wage basis hard-coded in Go while the rule had a field for it.
// Both are legal parameters, and reading only the first band understates the
// liability of precisely the long-serving people whose award is largest. It
// would understate it silently for years and the error would surface on the
// day somebody with fifteen years resigned.
//
// The figures staged here are DELIBERATELY NOT the statute — ten days and
// forty, which no labour law says — because a test asserting the real numbers
// would pass just as well against a version that ignored the rule and hard-
// coded them. What is asserted is that the software computes from what the
// registry states.
package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// stageEOSBRule states an entitlement for ONE tenant.
//
// Written as a per-tenant override (E8.3) rather than as a new platform-wide
// row, and the reason is isolation rather than convenience: `regulatory_rule`
// is not tenant-scoped, so a test that superseded the Saudi entitlement would
// change it for every other test sharing the database — including the ones
// asserting that an unverified entitlement REFUSES. Those would then pass or
// fail according to what else happened to be running.
//
// `regulatory_rule_override` carries the tenant in its row-level security
// predicate, so this is visible to this shop and to nothing else.
func (h *harness) stageEOSBRule(
	t *testing.T, f *shopFixture, basis, firstFive, afterFive string,
) {
	t.Helper()
	ctx := t.Context()
	from := time.Now().UTC().AddDate(-40, 0, 0).Format("2006-01-02")

	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO regulatory_rule_override
			  (tenant_id, rule_key, country, payload, effective_from,
			   justification, approved_by)
			VALUES ($1, 'SA.EOSB.ENTITLEMENT', 'sa', $2::jsonb, $3::date,
			        'Test fixture: figures chosen to be unmistakably not the '
			        'statute, so the assertion is about reading the rule.',
			        $4)`,
			f.tenantID,
			`{"wage_basis":"`+basis+`",`+
				`"days_per_year_first_five":"`+firstFive+`",`+
				`"days_per_year_after_five":"`+afterFive+`",`+
				`"resignation_fraction_under_two_years":"0",`+
				`"resignation_fraction_two_to_five_years":"0.3333",`+
				`"resignation_fraction_five_to_ten_years":"0.6667"}`,
			from, f.userID)
		return e
	})
	if err != nil {
		t.Fatalf("stage the entitlement: %v", err)
	}
	h.rules.Invalidate()
}

// accrued is what one month charged for one person.
//
// Read as the tenant, not as the platform: `eosb_accrual` is FORCE row-level
// security on `current_tenant_id()`, so a platform connection sees no rows and
// the assertion would read a null rather than a figure.
func (h *harness) accrued(t *testing.T, f *shopFixture, employeeID string) string {
	t.Helper()
	ctx := t.Context()
	var amount string
	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT coalesce(to_char(sum(amount), 'FM9999999990.00'), 'nothing')
			FROM eosb_accrual WHERE employee_id = $1`, employeeID).Scan(&amount)
	})
	if err != nil {
		t.Fatalf("read the accrual for %s: %v", employeeID, err)
	}
	return amount
}

// Six years of service is charged at a different rate from six months.
//
// The bug this holds shut: one band applied to everybody. With ten days for
// each of the first five years and forty after them, a person past five years
// must accrue four times what a new joiner on the same wage does, and did not.
func TestEndOfServiceChargesTheBandTheMonthFallsIn(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.stageEOSBRule(t, f, "basic", "10", "40")

	newJoiner := h.hire(t, f, "Layla Nasser", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(0, -8, 0).Format("2006-01-02"),
	})
	longServer := h.hire(t, f, "Yusuf Idris", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-6, 0, 0).Format("2006-01-02"),
	})

	resp := h.do(t, http.MethodPost, "/api/v1/eosb/accrue"+company, f.token,
		map[string]any{"period": time.Now().UTC().Format("2006-01")})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accrue: %s", readBody(t, resp))
	}
	resp.Body.Close()

	// 3000 / 30 = 100 a day. Ten days a year is 1000 a year, so 83.33 a month.
	if got := h.accrued(t, f, newJoiner); got != "83.33" {
		t.Errorf("a new joiner accrued %s, want 83.33 — the first-five band", got)
	}
	// Forty days a year is 4000 a year, so 333.33 a month.
	if got := h.accrued(t, f, longServer); got != "333.33" {
		t.Errorf("six years of service accrued %s, want 333.33 — the "+
			"after-five band. Reading only the first band understates the "+
			"award of the people it is largest for", got)
	}
}

// The award is computed on the wage the rule names, not the one Go assumed.
//
// Basic alone and basic plus housing are different answers for the same person,
// and which is correct is a legal question. It was hard-coded as basic plus
// housing while the rule carried a field for it, so a jurisdiction stating
// anything else would have been computed wrongly with nothing to show for it.
func TestEndOfServiceComputesOnTheWageBasisTheRuleStates(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.stageEOSBRule(t, f, "basic", "30", "30")

	who := h.hire(t, f, "Hana Saleh", "3000.00", map[string]any{
		"joined_on":         time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02"),
		"housing_allowance": "1500.00",
	})

	resp := h.do(t, http.MethodPost, "/api/v1/eosb/accrue"+company, f.token,
		map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accrue: %s", readBody(t, resp))
	}
	resp.Body.Close()

	// Basic only: 3000 / 30 = 100 a day, thirty days a year, one twelfth.
	// Had the housing allowance been included it would be 375.00.
	if got := h.accrued(t, f, who); got != "250.00" {
		t.Errorf("accrued %s on a basic-only rule, want 250.00 (375.00 is "+
			"what basic plus housing would give)", got)
	}
}

// A wage basis this product cannot compute is refused, and named.
//
// The alternative is worse than an error: silently falling back to a basis the
// rule did not state would put a number nobody chose into a final settlement.
func TestAnUnknownWageBasisIsRefusedRatherThanApproximated(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.stageEOSBRule(t, f, "basic_plus_the_car", "30", "30")
	h.hire(t, f, "Mariam Zahra", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02"),
	})

	resp := h.do(t, http.MethodPost, "/api/v1/eosb/accrue"+company, f.token,
		map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a wage basis this product does not understand was accepted " +
			"and something was charged on it")
	}
	if body := readBody(t, resp); !containsText(body, "basic_plus_the_car") {
		t.Errorf("the refusal does not name the basis it could not use: %s", body)
	}
}
