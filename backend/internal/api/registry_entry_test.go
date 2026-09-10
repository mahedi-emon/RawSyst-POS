//go:build integration

// The two doors into the regulatory registry, and what they used to disagree
// about.
//
// A legal value reaches this product two ways: an attestation file applied by
// `cmd/regulatory`, and the Super Admin screen behind `POST /platform/rules`.
// The file has validated every figure against the source pack since it was
// written — the field names, the units, the choices, the signs, and the rule
// that a fraction of an award lies between 0 and 1. The screen validated an
// empty payload and a payload still containing the placeholder, and wrote
// anything else.
//
// So the door a platform operator actually uses was the permissive one. These
// hold both doors to the same standard, and hold the screen to saying what an
// unverified blocker actually blocks.
package api

import (
	"net/http"
	"testing"
)

// A figure the attestation file would refuse is refused on the screen too.
//
// `days_per_year_first_five` is a decimal in the source pack. Recording it as
// prose put a value in the registry that no payroll run can compute with, and
// what surfaced later was an accrual failing rather than a form field saying
// the box holds words where it wants a number.
func TestTheScreenRefusesAFigureTheAttestationFileWouldRefuse(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.EOSB.ENTITLEMENT", "country": "sa",
			"payload": map[string]any{
				"wage_basis":                             "basic",
				"days_per_year_first_five":               "fifteen",
				"days_per_year_after_five":               "30",
				"resignation_fraction_under_two_years":   "0",
				"resignation_fraction_two_to_five_years": "0.3333",
				"resignation_fraction_five_to_ten_years": "0.6667",
				"resignation_fraction_over_ten_years":    "1",
			},
			"effective_from":   "2026-01-01",
			"source_authority": "mhrsd",
			"source_document":  "Saudi Labour Law, Articles 84 and 85",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a wage-day count of \"fifteen\" was recorded as the law")
	}
	body := readBody(t, resp)
	if !containsFold(body, "days_per_year_first_five") {
		t.Errorf("the refusal does not name the field that is wrong: %s", body)
	}
}

// A fraction of an award cannot exceed the award.
//
// Article 85 states a share of the Article 84 award. Typing 33 where the
// article says a third is the single most plausible mistake on this form, and
// it pays somebody thirty-three times what they are owed. The engine has
// refused it at the point of use for as long as it has read the rule; that is
// the wrong place to find out, because by then the figure is on record as the
// law and somebody's name is against it.
func TestAFractionGreaterThanTheAwardIsRefusedWhereItIsTyped(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.EOSB.ENTITLEMENT", "country": "sa",
			"payload": map[string]any{
				"wage_basis":                             "basic",
				"days_per_year_first_five":               "15",
				"days_per_year_after_five":               "30",
				"resignation_fraction_under_two_years":   "0",
				"resignation_fraction_two_to_five_years": "33",
				"resignation_fraction_five_to_ten_years": "0.6667",
				"resignation_fraction_over_ten_years":    "1",
			},
			"effective_from":   "2026-01-01",
			"source_authority": "mhrsd",
			"source_document":  "Saudi Labour Law, Articles 84 and 85",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a resignation fraction of 33 was recorded as the law")
	}
	if body := readBody(t, resp); !containsFold(body, "0.3333") {
		t.Errorf("the refusal does not say what the figure should look "+
			"like: %s", body)
	}
}

// A wage basis this product cannot compute is refused rather than stored.
//
// `wage_basis` is a closed vocabulary: three ways of reading "the last wage",
// each of which the payroll engine knows how to assemble. A fourth is not a
// figure this product can be told, and storing one produces a rule that
// resolves and then fails.
func TestAWageBasisThisProductCannotComputeIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.EOSB.ENTITLEMENT", "country": "sa",
			"payload": map[string]any{
				"wage_basis":                             "gross_including_overtime",
				"days_per_year_first_five":               "15",
				"days_per_year_after_five":               "30",
				"resignation_fraction_under_two_years":   "0",
				"resignation_fraction_two_to_five_years": "0.3333",
				"resignation_fraction_five_to_ten_years": "0.6667",
				"resignation_fraction_over_ten_years":    "1",
			},
			"effective_from":   "2026-01-01",
			"source_authority": "mhrsd",
			"source_document":  "Saudi Labour Law, Articles 84 and 85",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a wage basis this product cannot assemble was recorded")
	}
	if body := readBody(t, resp); !containsFold(body, "basic_plus_housing") {
		t.Errorf("the refusal does not list what is understood: %s", body)
	}
}

// A field the rule does not have is refused rather than stored beside it.
//
// A payload carrying `days_per_year` where the rule wants
// `days_per_year_first_five` looks filled in and computes nothing. Refusing it
// at the point of entry is the difference between a form saying "that is not a
// field of this rule" and a payroll run months later saying a field is
// missing.
func TestAFieldTheRuleDoesNotHaveIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.EOSB.ENTITLEMENT", "country": "sa",
			"payload":          map[string]any{"days_per_year": "15"},
			"effective_from":   "2026-01-01",
			"source_authority": "mhrsd",
			"source_document":  "Saudi Labour Law, Articles 84 and 85",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a payload with an invented field name was recorded as the law")
	}
	if body := readBody(t, resp); !containsFold(body, "days_per_year_first_five") {
		t.Errorf("the refusal does not say what the rule takes: %s", body)
	}
}

// Every figure of a rule is recorded together.
//
// Half a rule is not half useful. A payload with one band filled in and the
// rest absent refuses at the point of use exactly as a placeholder does, so
// accepting it stores something that looks like progress and is not.
func TestAPartlyFilledRuleIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": "SA.EOSB.ENTITLEMENT", "country": "sa",
			"payload": map[string]any{
				"wage_basis":               "basic",
				"days_per_year_first_five": "15",
			},
			"effective_from":   "2026-01-01",
			"source_authority": "mhrsd",
			"source_document":  "Saudi Labour Law, Articles 84 and 85",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a rule missing five of its seven figures was recorded")
	}
}

// A rule the source pack does not describe is still recordable.
//
// The pack describes the values somebody has to go and read out of a published
// document. Plenty of rules are not like that, and refusing a payload merely
// because the pack has not caught up with it would close the registry to
// anything new.
func TestARuleTheSourcePackDoesNotDescribeIsStillRecordable(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	key := "BD.TEST.UNDESCRIBED_" + upperSuffix(8)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
		map[string]any{
			"rule_key": key, "country": "bd",
			"payload":          map[string]any{"rate": "0.15", "shape": "whatever"},
			"effective_from":   "2020-01-01",
			"source_authority": "nbr",
			"source_document":  "VAT and Supplementary Duty Act 2012",
			"verified":         true,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a rule outside the source pack was refused: %d %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- what a blocker blocks ------------------------------------------------

// A blocker recorded through the screen says what it blocks, and defaults to
// the narrower answer.
//
// 0124 taught the database the difference between a value that closes a market
// to new business and one that refuses a single capability. The screen could
// not say which, so every rule it recorded became a feature blocker by the
// column's default — correct by luck rather than by anybody's decision, and
// unable to record an onboarding blocker at all.
//
// Defaulting to `feature` is deliberate. Mistaking a capability blocker for an
// onboarding one shuts a market that could trade.
func TestARuleRecordedThroughTheScreenSaysWhatItBlocks(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	record := func(t *testing.T, blocks any) map[string]any {
		t.Helper()
		body := map[string]any{
			"rule_key": "BD.TEST.BLOCKS_" + upperSuffix(8), "country": "bd",
			"payload":          map[string]any{"rate": "0.15"},
			"effective_from":   "2020-01-01",
			"source_authority": "nbr",
			"source_document":  "VAT and Supplementary Duty Act 2012",
			"release_blocker":  true,
			"verified":         true,
		}
		if blocks != nil {
			body["blocks"] = blocks
		}
		resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin, body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("record: %d %s", resp.StatusCode, readBody(t, resp))
		}
		rule, _ := decodeJSON(t, resp)["rule"].(map[string]any)
		return rule
	}

	t.Run("omitted means the narrower one", func(t *testing.T) {
		if got, _ := record(t, nil)["blocks"].(string); got != "feature" {
			t.Errorf("blocks = %q with nothing said, want feature", got)
		}
	})

	t.Run("onboarding can be recorded", func(t *testing.T) {
		if got, _ := record(t, "onboarding")["blocks"].(string); got != "onboarding" {
			t.Errorf("blocks = %q, want onboarding", got)
		}
	})

	t.Run("anything else is refused", func(t *testing.T) {
		resp := h.do(t, http.MethodPost, "/api/v1/platform/rules", admin,
			map[string]any{
				"rule_key": "BD.TEST.BLOCKS_" + upperSuffix(8), "country": "bd",
				"payload":          map[string]any{"rate": "0.15"},
				"effective_from":   "2020-01-01",
				"source_authority": "nbr",
				"source_document":  "VAT and Supplementary Duty Act 2012",
				"release_blocker":  true,
				"blocks":           "everything",
				"verified":         true,
			})
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			t.Error("a rule was recorded as blocking something nothing reads")
		}
	})
}

// The list says what each rule blocks, so the screen can stop calling every
// unverified blocker a closed market.
//
// `SA.EOSB.ENTITLEMENT` is the case that made this matter. An end-of-service
// award is computed when somebody LEAVES: a shop can be onboarded, trade for a
// year and never compute one. Counting it among the reasons Saudi Arabia is
// closed to new business told an operator that a market could not be sold into
// over a calculation that market may never perform.
func TestTheRuleListSaysWhatEachBlockerBlocks(t *testing.T) {
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
		t.Fatal("the Saudi registry is empty")
	}

	sawEOSB := false
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		blocks, _ := row["blocks"].(string)
		if blocks != "onboarding" && blocks != "feature" {
			t.Errorf("%v says it blocks %q, which nothing reads",
				row["rule_key"], blocks)
		}
		if row["rule_key"] == "SA.EOSB.ENTITLEMENT" {
			sawEOSB = true
			if blocks != "feature" {
				t.Errorf("the end-of-service award blocks %q. It is computed "+
					"when somebody leaves, so it cannot stop a shop opening",
					blocks)
			}
		}
	}
	if !sawEOSB {
		t.Error("the end-of-service rule is not in the Saudi registry")
	}
}

// --- what this installation is waiting for --------------------------------

// The outstanding list is answerable from the application.
//
// `registry.Outstanding` has answered this since the attestation workflow was
// written, and its only caller was a binary run on the host with the database
// password. So the one report saying what this installation is waiting for
// reached a developer with a shell and nobody else — including the platform
// operator whose job it is to go and read the document.
func TestTheOutstandingListSaysWhatIsStillWaitedOn(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/rules/outstanding?country=sa", admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outstanding: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)

	var eosb map[string]any
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["rule_key"] == "SA.EOSB.ENTITLEMENT" {
			eosb = row
		}
	}
	if eosb == nil {
		t.Fatal("the end-of-service rule holds placeholders and the " +
			"outstanding list does not name it")
	}

	// Which fields, not merely that something is missing: naming them is
	// naming exactly what somebody has to go and look up.
	fields, _ := eosb["unfilled_fields"].([]any)
	if len(fields) == 0 {
		t.Error("the outstanding entry names no unfilled field")
	}

	// And whether the product knows where they come from, which decides
	// whether the operator gets labelled boxes or a JSON textarea.
	if described, _ := eosb["described"].(bool); !described {
		t.Error("the source pack describes the end-of-service award and the " +
			"outstanding list says it does not")
	}

	if blocks, _ := eosb["blocks"].(string); blocks != "feature" {
		t.Errorf("outstanding says the award blocks %q", blocks)
	}
}

// A business owner cannot read what the platform is waiting for.
func TestTheOutstandingListIsPlatformOnly(t *testing.T) {
	h := newHarness(t)
	owner := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet, "/api/v1/platform/rules/outstanding",
		owner.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a business owner read the platform's outstanding legal values")
	}
}
