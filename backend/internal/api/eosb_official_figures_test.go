//go:build integration

// The end-of-service award computed at the figures Articles 84 and 85 state.
//
// # How this differs from eosb_entitlement_test.go, and why both exist
//
// That file stages figures that are deliberately NOT the statute — ten days a
// year, a fifth of an award — because a test asserting the real numbers passes
// just as well against a build that ignored the registry and hard-coded them.
// It proves the engine reads the rule.
//
// This file proves something else: that the engine's arithmetic, driven by the
// figures the official document actually states, produces the amounts Articles
// 84 and 85 produce. Every expected value below is worked out from the articles
// in the comment above it, so a reader can check the assertion against the law
// rather than against this product.
//
// The figures are staged here as a per-tenant override, for the isolation
// reason the neighbouring file gives. Where they came from is not this file's
// claim: they are read out of the Ministry's published PDF by
// `internal/registry`, which has its own tests for the reading, and applied
// through the source-document workflow. What is asserted here is arithmetic.
//
// # The wage
//
// 12,000 a month throughout, because the law computes an award on a 30-day
// month and 12,000 gives a daily wage of exactly 400. Round numbers are chosen
// so that a wrong band is a visibly wrong amount rather than a near miss.
package api

import (
	"testing"
	"time"
)

// The award figures as the Ministry's document states them.
//
// Article 84: half a month's wage for each of the first five years, a whole
// month's wage for each year after that, on a 30-day month — so 15 days and 30
// days of wage per year.
//
// Article 85: nothing below two years' service, a third from two to five, two
// thirds above five and below ten, the whole award at ten or more. Written as
// the article writes them, because 0.3333 of a 30,000 award is 9,999.00.
const (
	officialFirstFive = "15"
	officialAfterFive = "30"
	officialBasis     = "basic_plus_all_allowances"

	officialUnderTwo  = "0"
	officialTwoToFive = "1/3"
	officialFiveToTen = "2/3"
	officialOverTen   = "1"
)

// stageOfficialEOSB states the award at the figures the articles state.
func (h *harness) stageOfficialEOSB(t *testing.T, f *shopFixture) {
	t.Helper()
	h.stageEOSBRuleWithFractions(t, f, officialBasis,
		officialFirstFive, officialAfterFive,
		officialUnderTwo, officialTwoToFive, officialFiveToTen,
		officialOverTen)
}

// hireFor puts somebody on 12,000 a month with the given service.
func (h *harness) hireFor(
	t *testing.T, f *shopFixture, name string, years, months int,
) string {
	t.Helper()
	return h.hire(t, f, name, "9000.00", map[string]any{
		// 9,000 basic plus 3,000 of allowances is 12,000 on the statutory
		// basis, and a build that quietly used the basic wage alone would
		// produce three quarters of every figure below.
		"housing_allowance":   "1800.00",
		"transport_allowance": "900.00",
		"other_allowance":     "300.00",
		"joined_on": time.Now().UTC().
			AddDate(-years, -months, 0).Format("2006-01-02"),
	})
}

// The whole award, for service ended by the employer.
//
// Article 84 alone: Article 85 reduces a RESIGNATION and nothing else, so a
// dismissal is the award itself. On a 400 daily wage:
//
//	A  1 year    15 days                        ->  6,000.00
//	B  5 years   15 x 5 = 75 days               -> 30,000.00
//	C  5y 6m     75 + 30 x 0.5 = 90 days        -> 36,000.00
//	D  6 years   75 + 30 = 105 days             -> 42,000.00
//	E  9 years   75 + 30 x 4 = 195 days         -> 78,000.00
//	F  10 years  75 + 30 x 5 = 225 days         -> 90,000.00
//
// The five-year boundary is the one that matters: a build charging every year
// at the first-five rate owes 150,000 for F's service at 30 days, and one
// charging every year at the later rate owes 300,000. Neither is 90,000.
func TestTheAwardAcrossArticle84sBands(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	for _, c := range []struct {
		name          string
		years, months int
		award         string
	}{
		{"A one year", 1, 0, "6000.00"},
		{"B five years", 5, 0, "30000.00"},
		{"C five years and six months", 5, 6, "36000.00"},
		{"D six years", 6, 0, "42000.00"},
		{"E nine years", 9, 0, "78000.00"},
		{"F ten years", 10, 0, "90000.00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			who := h.hireFor(t, f, "Award "+c.name, c.years, c.months)
			got := h.settlement(t, f, who, "", "termination")

			if v := settled(t, got, "award"); v != c.award {
				t.Errorf("%d years %d months settles at %s, want %s",
					c.years, c.months, v, c.award)
			}
			// A dismissal states the fraction as 1 rather than omitting it.
			if v := settled(t, got, "resignation_fraction"); v != "1" {
				t.Errorf("a dismissal applied a fraction of %s", v)
			}
			if v := settled(t, got, "wage"); v != "12000.00" {
				t.Errorf("the award was computed on a wage of %s, want "+
					"12000.00 — the basic wage plus all other due increments",
					v)
			}
		})
	}
}

// Article 85's share, for service ended by the worker.
//
// The award is the Article 84 figure above; the share is the article's:
//
//	G  2 years   12,000 award, a third        ->  4,000.00
//	H  5 years   30,000 award, a third        -> 10,000.00
//	I  6 years   42,000 award, two thirds     -> 28,000.00
//	J  10 years  90,000 award, the whole      -> 90,000.00
//
// H is the boundary the article words differently from the other two. A third
// is due "after service of not less than two consecutive years and NOT MORE
// THAN FIVE YEARS"; two thirds only "IN EXCESS OF five". So exactly five years
// takes the lower band, and a build reading the boundary the other way pays
// 20,000.00 — twice what is owed, on the anniversary and on no other day.
//
// And G is the one that shows a third is a third: 12,000 x 0.3333 is 3,999.60.
func TestTheShareOnResignationAcrossArticle85sBands(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	for _, c := range []struct {
		name             string
		years, months    int
		fullAward, share string
		award            string
	}{
		{"G two years", 2, 0, "12000.00", "1/3", "4000.00"},
		{"H five years exactly", 5, 0, "30000.00", "1/3", "10000.00"},
		{"I six years", 6, 0, "42000.00", "2/3", "28000.00"},
		{"J ten years", 10, 0, "90000.00", "1", "90000.00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			who := h.hireFor(t, f, "Resign "+c.name, c.years, c.months)
			got := h.settlement(t, f, who, "", "resignation")

			if v := settled(t, got, "full_award"); v != c.fullAward {
				t.Errorf("the Article 84 award is %s, want %s", v, c.fullAward)
			}
			// Quoted as the article states it, so a settlement document can be
			// checked against Article 85 rather than against a long decimal.
			if v := settled(t, got, "resignation_fraction"); v != c.share {
				t.Errorf("the share applied is %s, want %s", v, c.share)
			}
			if v := settled(t, got, "award"); v != c.award {
				t.Errorf("%d years' service on resignation settles at %s, "+
					"want %s", c.years, v, c.award)
			}
		})
	}
}

// Below two years a resignation is owed nothing under Article 85.
//
// The article confers a share only "after service of not less than two
// consecutive years". Eighteen months is below that, and the award is not
// reduced but absent.
func TestAResignationBelowTwoYearsIsOwedNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	who := h.hireFor(t, f, "Under two", 1, 6)
	got := h.settlement(t, f, who, "", "resignation")

	// The Article 84 award exists — 18 months at 15 days a year is 22.5 days,
	// 9,000.00 — and Article 85 confers none of it.
	if v := settled(t, got, "full_award"); v != "9000.00" {
		t.Errorf("the Article 84 award for 18 months is %s, want 9000.00", v)
	}
	if v := settled(t, got, "resignation_fraction"); v != "0" {
		t.Errorf("the share below two years is %s, want 0", v)
	}
	if v := settled(t, got, "award"); v != "0.00" {
		t.Errorf("a resignation at 18 months settles at %s, want 0.00", v)
	}

	// And the same person dismissed is owed the whole award, which is the
	// difference Article 85 makes.
	dismissed := h.settlement(t, f, who, "", "termination")
	if v := settled(t, dismissed, "award"); v != "9000.00" {
		t.Errorf("dismissed at 18 months settles at %s, want 9000.00", v)
	}
}

// A part-year is entitled in proportion to the time served.
//
// Article 84: "the worker shall be entitled to an end-of-service award for the
// portions of the year in proportion to the time spent on the job." Twenty-
// seven months is 2.25 years in the first band: 15 x 27 / 12 = 33.75 days,
// which on a 400 daily wage is 13,500.00. Not 2 years' worth, and not 3.
func TestAPartYearIsEntitledInProportion(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	who := h.hireFor(t, f, "Part year", 2, 3)
	got := h.settlement(t, f, who, "", "termination")

	if v := settled(t, got, "award"); v != "13500.00" {
		t.Errorf("27 months settles at %s, want 13500.00 — 33.75 days of "+
			"wage, neither two years' worth (12,000.00) nor three (18,000.00)",
			v)
	}
	if v := settled(t, got, "months_of_service"); v != "27" {
		t.Errorf("service is recorded as %s months, want 27", v)
	}
}

// The award follows the wage, and the wage follows the basis the rule states.
//
// The same service at three different pay packets, on the statutory basis:
// basic plus all other due increments. A build reading the basic wage alone
// gives the first column and is wrong by every allowance.
func TestTheAwardFollowsTheWageTheRuleNames(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	for _, c := range []struct {
		name                             string
		basic, housing, transport, other string
		wage, award                      string
	}{
		// One year of service: 15 days of wage, so the award is half a month.
		{"basic only", "6000.00", "0.00", "0.00", "0.00",
			"6000.00", "3000.00"},
		{"basic and housing", "6000.00", "1500.00", "0.00", "0.00",
			"7500.00", "3750.00"},
		{"every allowance", "5000.00", "1250.00", "500.00", "250.00",
			"7000.00", "3500.00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			who := h.hire(t, f, "Wage "+c.name, c.basic, map[string]any{
				"housing_allowance":   c.housing,
				"transport_allowance": c.transport,
				"other_allowance":     c.other,
				"joined_on": time.Now().UTC().AddDate(-1, 0, 0).
					Format("2006-01-02"),
			})
			got := h.settlement(t, f, who, "", "termination")

			if v := settled(t, got, "wage"); v != c.wage {
				t.Errorf("the wage is %s, want %s", v, c.wage)
			}
			if v := settled(t, got, "award"); v != c.award {
				t.Errorf("the award is %s, want %s", v, c.award)
			}
		})
	}
}

// A third of an award is a third, to the halalah.
//
// This is the reason Article 85's shares are recorded as `1/3` and `2/3` rather
// than as decimals. On a 30,000 award, 0.3333 pays 9,999.00 and the person is
// short a riyal; 0.33333333 pays 9,999.99 and they are short a halalah. There
// is no number of threes that is a third.
//
// Three wages chosen so the exact answer is not a round number, and the sum of
// the three shares is the whole award — which a rounded fraction cannot manage.
func TestAThirdOfAnAwardIsExactToTheHalalah(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	for _, c := range []struct{ name, basic, fullAward, award string }{
		// Two years' service: 15 x 2 = 30 days, so the award is one month's
		// wage and the share is a third of it.
		{"a wage that divides", "30000.00", "30000.00", "10000.00"},
		{"a wage that does not", "10000.00", "10000.00", "3333.33"},
		{"an awkward wage", "8888.00", "8888.00", "2962.67"},
	} {
		t.Run(c.name, func(t *testing.T) {
			who := h.hire(t, f, "Third "+c.name, c.basic, map[string]any{
				"joined_on": time.Now().UTC().AddDate(-2, 0, 0).
					Format("2006-01-02"),
			})
			got := h.settlement(t, f, who, "", "resignation")

			if v := settled(t, got, "full_award"); v != c.fullAward {
				t.Errorf("the award is %s, want %s", v, c.fullAward)
			}
			if v := settled(t, got, "award"); v != c.award {
				t.Errorf("a third of %s settled at %s, want %s",
					c.fullAward, v, c.award)
			}
		})
	}
}

// The day before the fifth anniversary and the day of it are different bands,
// and the day after is a third one.
//
// Article 85 hinges on "not more than five years" against "in excess of five",
// so the fifth anniversary belongs to the lower band and the day after it does
// not. This is the assertion that would have caught the boundary being read the
// wrong way round, which it was until Article 85 was ingested and the sentence
// sat next to the code.
func TestTheFifthAnniversaryBelongsToTheLowerBand(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	joined := time.Now().UTC().AddDate(-6, 0, 0)
	who := h.hire(t, f, "Fifth anniversary", "12000.00", map[string]any{
		"joined_on": joined.Format("2006-01-02"),
	})

	for _, c := range []struct {
		name  string
		on    time.Time
		share string
	}{
		{"the day before", joined.AddDate(5, 0, -1), "1/3"},
		{"the anniversary itself", joined.AddDate(5, 0, 0), "1/3"},
		{"a month after", joined.AddDate(5, 1, 0), "2/3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := h.settlement(t, f, who,
				c.on.Format("2006-01-02"), "resignation")
			if v := settled(t, got, "resignation_fraction"); v != c.share {
				t.Errorf("leaving on %s applied %s, want %s",
					c.on.Format("2006-01-02"), v, c.share)
			}
		})
	}
}

// What the accrual charged and what the settlement owes agree.
//
// The monthly charge and the final settlement read the same rule and must not
// drift: a provision built one way and a settlement computed another is a
// shortfall discovered on somebody's last day.
func TestTheAccrualAndTheSettlementAgreeOnTheOfficialFigures(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.stageOfficialEOSB(t, f)

	who := h.hireFor(t, f, "Agreement", 3, 0)
	got := h.settlement(t, f, who, "", "termination")

	// 36 months in the first band: 15 x 36 / 12 = 45 days, at 400 a day.
	if v := settled(t, got, "award"); v != "18000.00" {
		t.Errorf("three years settles at %s, want 18000.00", v)
	}
	if v := settled(t, got, "first_band_months"); v != "36" {
		t.Errorf("the first band covers %s months, want 36", v)
	}
	if v := settled(t, got, "after_band_months"); v != "0" {
		t.Errorf("the later band covers %s months for three years' service", v)
	}
	if v := settled(t, got, "first_band_days_per_year"); v != "15" {
		t.Errorf("the first band pays %s days a year, want 15", v)
	}
	if v := settled(t, got, "after_band_days_per_year"); v != "30" {
		t.Errorf("the later band pays %s days a year, want 30", v)
	}
}
