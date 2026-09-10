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
	"encoding/json"
	"net/http"
	"sort"
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
	// Fractions that are unmistakably not the statute, for the same reason the
	// bands are: a settlement test asserting the real figures would pass
	// against a build that ignored the rule and hard-coded them. A half, a
	// quarter, a fifth and a tenth, descending — so a settlement that reads the
	// wrong band is a different number rather than a coincidence.
	h.stageEOSBRuleWithFractions(t, f, basis, firstFive, afterFive,
		"0.5", "0.25", "0.2", "0.1")
}

// stageEOSBRuleWithFractions is the same, naming Article 85's four bands.
func (h *harness) stageEOSBRuleWithFractions(
	t *testing.T, f *shopFixture, basis, firstFive, afterFive,
	underTwo, twoToFive, fiveToTen, overTen string,
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
				`"resignation_fraction_under_two_years":"`+underTwo+`",`+
				`"resignation_fraction_two_to_five_years":"`+twoToFive+`",`+
				`"resignation_fraction_five_to_ten_years":"`+fiveToTen+`",`+
				`"resignation_fraction_over_ten_years":"`+overTen+`"}`,
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

// --- The award on leaving (Articles 84 and 85) ------------------------------
//
// Everything above is the monthly ACCRUAL, which is what the business provides
// for. What follows is the SETTLEMENT, which is what it owes — a different
// question with a different formula, and until now one this product could not
// answer at all.
//
// The three resignation fractions had been required by the rule, validated on
// import, carried in the payload and read by nothing. A person who resigned
// after three years was owed a fraction of the award and there was no way to
// find out what.

// settlement asks for one person's award and returns the decoded answer.
func (h *harness) settlement(
	t *testing.T, f *shopFixture, employeeID, on, reason string,
) map[string]any {
	t.Helper()
	url := "/api/v1/eosb/settlement/" + employeeID +
		"?company_id=" + f.companyID.String() + "&reason=" + reason
	if on != "" {
		url += "&on=" + on
	}
	resp := h.do(t, http.MethodGet, url, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settlement: %s", readBody(t, resp))
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the settlement: %v", err)
	}
	return body.Data
}

// settled reads one field of a settlement, failing rather than returning "" for
// a field that is not there. A missing field and a field holding nothing are
// different defects and an empty string reports them as the same one.
func settled(t *testing.T, m map[string]any, name string) string {
	t.Helper()
	v, ok := m[name]
	if !ok {
		t.Fatalf("the settlement carries no %q; it has %v", name, keysOf(m))
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%q is %T, not a string", name, v)
	}
	return s
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A resignation is settled at Article 85's fraction of the award.
//
// The gap this closes. Three fractions were required by the rule, validated on
// the way in and applied by nothing, so somebody who resigned after three years
// was owed a share of the award that this product could not state.
//
// Three years at thirty days a year on 3000 basic: 100 a day, ninety days,
// 9000 full award. The two-to-five band here is a quarter, so 2250.
func TestAResignationIsSettledAtArticle85sFraction(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "30", "30")
	who := h.hire(t, f, "Nadia Farouk", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-3, 0, 0).Format("2006-01-02"),
	})

	got := h.settlement(t, f, who, "", "resignation")

	if v := settled(t, got, "full_award"); v != "9000.00" {
		t.Errorf("the Article 84 award is %s, want 9000.00", v)
	}
	if v := settled(t, got, "resignation_fraction"); v != "0.25" {
		t.Errorf("the fraction applied is %s, want 0.25 — the two-to-five "+
			"band. A settlement that reads the wrong band pays the wrong "+
			"person the wrong money", v)
	}
	if v := settled(t, got, "award"); v != "2250.00" {
		t.Errorf("the award on resignation is %s, want 2250.00", v)
	}
}

// A dismissal is settled at the whole award, and says so.
//
// Article 85 reduces what a person who RESIGNS receives. It does not reduce a
// dismissal, and the settlement states the fraction as 1 rather than omitting
// it — an absent field reads as a fraction somebody forgot to apply.
func TestATerminationIsSettledAtTheWholeAward(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "30", "30")
	who := h.hire(t, f, "Omar Rashid", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-3, 0, 0).Format("2006-01-02"),
	})

	got := h.settlement(t, f, who, "", "termination")

	if v := settled(t, got, "resignation_fraction"); v != "1" {
		t.Errorf("a dismissal applied a fraction of %s, want 1", v)
	}
	if v := settled(t, got, "award"); v != "9000.00" {
		t.Errorf("a dismissal settled at %s, want the whole 9000.00 award", v)
	}
}

// Service is split across the two Article 84 bands, not charged at one.
//
// Eight years at ten days for the first five and forty after them: sixty months
// in the lower band and thirty-six in the upper. On 3000 basic that is
// 100 * (10 * 60 + 40 * 36) / 12 = 17,000 — neither 8 * 400 nor 8 * 100.
func TestASettlementSplitsServiceAcrossBothBands(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "10", "40")
	who := h.hire(t, f, "Yusuf Idris", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-8, 0, 0).Format("2006-01-02"),
	})

	got := h.settlement(t, f, who, "", "termination")

	if v := settled(t, got, "first_band_months"); v != "60" {
		t.Errorf("%s months fell in the first band, want 60", v)
	}
	if v := settled(t, got, "after_band_months"); v != "36" {
		t.Errorf("%s months fell in the after-five band, want 36", v)
	}
	if v := settled(t, got, "full_award"); v != "17000.00" {
		t.Errorf("eight years settled at %s, want 17000.00. Applying one band "+
			"to the whole of a long service is the error this splits", v)
	}
}

// Eleven years takes the fourth fraction, which used not to exist.
//
// 0092 recorded three resignation bands and stopped at ten years, so service
// beyond that had no fraction to apply and the software would have had to
// assume one. 0132 adds the fourth. This asserts it is READ rather than
// defaulted: with a tenth in that band and a fifth in the one below, a
// settlement that fell back on five-to-ten would be twice the right figure,
// and one that fell back on the whole award would be ten times it.
func TestServiceBeyondTenYearsTakesItsOwnFraction(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "30", "30")
	who := h.hire(t, f, "Salma Idrissi", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-11, 0, 0).Format("2006-01-02"),
	})

	got := h.settlement(t, f, who, "", "resignation")

	if v := settled(t, got, "resignation_fraction"); v != "0.1" {
		t.Errorf("eleven years applied a fraction of %s, want 0.1 — the band "+
			"above ten years, which is a figure in the rule and not a "+
			"fallback", v)
	}
}

// The band boundary falls on the anniversary, not the day before it.
//
// Exactly twenty-four completed months is into the third year, so it takes the
// two-to-five fraction. Reading it the other way round moves somebody DOWN a
// band on the day their entitlement rises, which is the wrong direction and
// invisible in every test that does not sit on the boundary.
func TestAnAnniversaryTakesTheHigherBand(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRuleWithFractions(t, f, "basic", "30", "30",
		"0.5", "0.25", "0.2", "0.1")
	joined := time.Now().UTC().AddDate(-3, 0, 0)
	who := h.hire(t, f, "Faisal Noor", "3000.00", map[string]any{
		"joined_on": joined.Format("2006-01-02"),
	})

	on := h.settlement(t, f, who, joined.AddDate(2, 0, 0).Format("2006-01-02"),
		"resignation")
	if v := settled(t, on, "resignation_fraction"); v != "0.25" {
		t.Errorf("on the second anniversary the fraction is %s, want 0.25", v)
	}

	before := h.settlement(t, f, who,
		joined.AddDate(2, 0, -1).Format("2006-01-02"), "resignation")
	if v := settled(t, before, "resignation_fraction"); v != "0.5" {
		t.Errorf("the day before the second anniversary the fraction is %s, "+
			"want 0.5", v)
	}
}

// The settlement and the accrual are the same arithmetic.
//
// The property that keeps the two halves of end of service honest. A business
// provides monthly and settles once, and if the two formulas disagree the
// provision is wrong every month for years and nobody finds out until somebody
// leaves.
//
// One month of service, one month charged, on a wage that never moved — the
// only case where the two figures must be identical to the halala. Article 84
// settles on the LAST wage while each charge was made on the wage in force that
// month, so a pay rise legitimately separates them, and the shortfall is what
// reports that difference.
//
// One period rather than several because a seeded shop has one accounting
// period open, and an accrual posts a journal entry dated to the end of the
// month it charges.
func TestASettlementAgreesWithWhatWasAccruedOnAnUnchangedWage(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.stageEOSBRule(t, f, "basic", "30", "30")
	who := h.hire(t, f, "Rana Habib", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(0, -1, 0).Format("2006-01-02"),
	})

	resp := h.do(t, http.MethodPost, "/api/v1/eosb/accrue"+company, f.token,
		map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accrue: %s", readBody(t, resp))
	}
	resp.Body.Close()

	got := h.settlement(t, f, who, "", "termination")
	if v := settled(t, got, "months_of_service"); v != "1" {
		t.Fatalf("the settlement counts %s months of service, want 1", v)
	}
	provision := settled(t, got, "provision")
	award := settled(t, got, "full_award")
	if provision != award {
		t.Errorf("the provision is %s and the award is %s. One month provided "+
			"for and one month owed, on a wage that never moved, is the same "+
			"sum charged two ways — a difference means the accrual and the "+
			"settlement disagree about the entitlement itself",
			provision, award)
	}
	if v := settled(t, got, "shortfall"); v != "0.00" {
		t.Errorf("the shortfall is %s on a fully provided settlement, want "+
			"0.00", v)
	}
}

// A settlement will not guess how the service ended.
//
// `Leave` writes the reason into a free-text note, and Article 85 turns on
// whether somebody resigned. Reading that intent out of prose is a guess with a
// leaver's money on the end of it, so the caller states it and an unrecognised
// value is refused rather than defaulted to either side.
func TestASettlementRefusesToGuessHowServiceEnded(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "30", "30")
	who := h.hire(t, f, "Tariq Amin", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-3, 0, 0).Format("2006-01-02"),
	})

	for _, reason := range []string{"", "left", "retirement"} {
		url := "/api/v1/eosb/settlement/" + who +
			"?company_id=" + f.companyID.String() + "&reason=" + reason
		resp := h.do(t, http.MethodGet, url, f.token, nil)
		body := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("reason %q was accepted and settled: %s", reason, body)
			continue
		}
		if !containsText(body, "resignation") {
			t.Errorf("the refusal for %q does not name what it will accept: %s",
				reason, body)
		}
	}
}

// A leaving date before the joining date is refused.
//
// The arithmetic would produce zero months and a zero award, which is a plain
// answer to an impossible question — and a settlement of nothing is exactly
// what somebody typing 2025 for 2026 would then be paid.
func TestASettlementRefusesADateBeforeTheyJoined(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRule(t, f, "basic", "30", "30")
	joined := time.Now().UTC().AddDate(-1, 0, 0)
	who := h.hire(t, f, "Dalia Kamal", "3000.00", map[string]any{
		"joined_on": joined.Format("2006-01-02"),
	})

	url := "/api/v1/eosb/settlement/" + who +
		"?company_id=" + f.companyID.String() +
		"&reason=termination&on=" + joined.AddDate(0, 0, -1).Format("2006-01-02")
	resp := h.do(t, http.MethodGet, url, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a leaving date before the joining date was settled: %s",
			readBody(t, resp))
	}
}

// A fraction above one is refused wherever it was written.
//
// The source file already refuses it on the way in. This is the second door:
// the registry screen writes a payload directly and an override is written per
// tenant, and neither goes through the file's validation. A fraction of 33
// instead of 0.33 pays somebody thirty-three times their award, and the
// arithmetic would be valid.
func TestAFractionAboveOneIsRefusedAtTheSettlement(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.stageEOSBRuleWithFractions(t, f, "basic", "30", "30",
		"0.5", "33", "0.2", "0.1")
	who := h.hire(t, f, "Hisham Wali", "3000.00", map[string]any{
		"joined_on": time.Now().UTC().AddDate(-3, 0, 0).Format("2006-01-02"),
	})

	url := "/api/v1/eosb/settlement/" + who +
		"?company_id=" + f.companyID.String() + "&reason=resignation"
	resp := h.do(t, http.MethodGet, url, f.token, nil)
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a resignation fraction of 33 was applied: %s", body)
	}
	if !containsText(body, "0.3333") {
		t.Errorf("the refusal does not show what a fraction looks like: %s",
			body)
	}
}
