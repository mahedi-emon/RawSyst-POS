// What a regulatory source file is allowed to say.
//
// No database: everything here is the check that stops a wrong figure reaching
// one. The unit matters more than the number — a resignation fraction typed as
// 33 instead of 0.3333 is arithmetically fine and would pay somebody
// thirty-three times their end-of-service award, and nothing downstream has any
// way to doubt it.
package registry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A rule of the test's own, so these do not depend on which figures the
// shipped pack happens to be waiting for.
var testPack = SourceRule{
	RuleKey:   "ZZ.TEST.ATTEST",
	Country:   "zz",
	Title:     "A rule for testing",
	Authority: "mhrsd",
	Document:  "Nothing real",
	URL:       "https://example.test",
	Articles:  []string{"1"},
	Reading:   "There is nothing to read.",
	Fields: []SourceField{
		{Name: "basis", Kind: "choice", Label: "Basis",
			Choices: []string{"basic", "gross"}, Help: "One of two."},
		{Name: "days", Kind: "decimal", Unit: "days", Label: "Days",
			Help: "Days of wage."},
		{Name: "share", Kind: "decimal", Unit: "fraction", Label: "Share",
			Help: "Between 0 and 1."},
		{Name: "window", Kind: "int", Unit: "days", Label: "Window",
			Help: "Whole days."},
	},
}

func withTestPack(t *testing.T) {
	t.Helper()
	prev := sourceFor
	sourceFor = func(key string) (SourceRule, bool) {
		if strings.EqualFold(key, testPack.RuleKey) {
			return testPack, true
		}
		return prev(key)
	}
	t.Cleanup(func() { sourceFor = prev })
}

func goodValues() map[string]string {
	return map[string]string{
		"basis": "gross", "days": "15", "share": "0.3333", "window": "10",
	}
}

func fileWith(values map[string]string) []byte {
	att := Attestation{
		Version: AttestationVersion,
		ReadBy:  "operator@example.test",
		ReadOn:  "2026-09-09",
		Statement: "Read the document named in the guidance and these are " +
			"the figures it gives.",
		Rules: []AttestedRule{{
			Key: "ZZ.TEST.ATTEST", From: "2026-09-09", Values: values,
		}},
	}
	b, _ := json.Marshal(att)
	return b
}

func TestAWellFormedSourceFileIsAccepted(t *testing.T) {
	withTestPack(t)

	att, err := ParseAttestation(fileWith(goodValues()))
	if err != nil {
		t.Fatalf("a complete file was refused: %v", err)
	}
	if len(att.Rules) != 1 || att.Rules[0].Key != "ZZ.TEST.ATTEST" {
		t.Fatalf("parsed %+v", att.Rules)
	}
}

// The figure that would be wrong by a factor of a hundred.
func TestAFractionAboveOneIsRefused(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["share"] = "33"
	_, err := ParseAttestation(fileWith(v))
	if err == nil {
		t.Fatal("a share of 33 was accepted; it is a fraction of an award, " +
			"and 33 pays thirty-three awards")
	}
	if !strings.Contains(err.Error(), "0.3333") {
		t.Errorf("the refusal does not say what right looks like: %v", err)
	}
}

func TestEveryFieldMustBeAnswered(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	delete(v, "days")
	_, err := ParseAttestation(fileWith(v))
	if err == nil {
		t.Fatal("a file missing a field was accepted; the payload would " +
			"still hold a placeholder and go on refusing")
	}
	if !strings.Contains(err.Error(), "days") {
		t.Errorf("the refusal does not name the missing field: %v", err)
	}
}

func TestABlankFigureIsRefused(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["days"] = "   "
	if _, err := ParseAttestation(fileWith(v)); err == nil {
		t.Fatal("an untouched template field was accepted as an answer")
	}
}

func TestAChoiceOutsideTheChoicesIsRefused(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["basis"] = "basic_plus_the_car"
	_, err := ParseAttestation(fileWith(v))
	if err == nil {
		t.Fatal("a wage basis the product cannot compute was accepted")
	}
	if !strings.Contains(err.Error(), "basic, gross") {
		t.Errorf("the refusal does not list what is understood: %v", err)
	}
}

func TestAFieldTheRuleDoesNotHaveIsRefused(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["days_per_yaer"] = "15"
	if _, err := ParseAttestation(fileWith(v)); err == nil {
		t.Fatal("a mistyped field name was accepted, and would have been " +
			"written into the payload beside the placeholder it was meant " +
			"to replace")
	}
}

func TestAWholeNumberFieldRefusesAFraction(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["window"] = "10.5"
	if _, err := ParseAttestation(fileWith(v)); err == nil {
		t.Fatal("half a day was accepted as a count of days")
	}
}

func TestANegativeFigureIsRefused(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["days"] = "-15"
	if _, err := ParseAttestation(fileWith(v)); err == nil {
		t.Fatal("a negative entitlement was accepted")
	}
}

func TestThePlaceholderCannotBeWrittenBack(t *testing.T) {
	withTestPack(t)

	v := goodValues()
	v["days"] = Placeholder
	_, err := ParseAttestation(fileWith(v))
	if err == nil {
		t.Fatal("__VERIFY__ was accepted as a figure, which would record " +
			"the placeholder as verified")
	}
}

func TestARuleThePackDoesNotDescribeIsRefused(t *testing.T) {
	withTestPack(t)

	raw := []byte(`{"version":"1","read_by":"a@b.test","read_on":"2026-09-09",
	  "statement":"Something long enough to be a statement.",
	  "rules":[{"rule_key":"SA.VAT.STANDARD_RATE","effective_from":"2026-09-09",
	            "values":{"rate":"0.15"}}]}`)
	_, err := ParseAttestation(raw)
	if err == nil {
		t.Fatal("this file wrote a rule the source pack does not describe; " +
			"it is not a general writer for the registry")
	}
}

func TestAnAttestationMustNameAPersonAndSayWhatTheyRead(t *testing.T) {
	withTestPack(t)

	for _, tc := range []struct{ name, mutate string }{
		{"no reader", `"read_by":""`},
		{"no date", `"read_on":""`},
		{"no statement", `"statement":"read it"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := string(fileWith(goodValues()))
			field := strings.SplitN(tc.mutate, ":", 2)[0]
			// Replace the whole field, whatever it held.
			start := strings.Index(raw, field)
			if start < 0 {
				t.Fatalf("could not find %s in the file", field)
			}
			end := strings.Index(raw[start:], ",") + start
			raw = raw[:start] + tc.mutate + raw[end:]

			if _, err := ParseAttestation([]byte(raw)); err == nil {
				t.Fatalf("a file with %s was accepted; the registry would "+
					"carry a figure nobody stood behind", tc.name)
			}
		})
	}
}

func TestTheSameRuleTwiceIsRefused(t *testing.T) {
	withTestPack(t)

	raw := []byte(`{"version":"1","read_by":"a@b.test","read_on":"2026-09-09",
	  "statement":"Something long enough to be a statement.",
	  "rules":[
	    {"rule_key":"ZZ.TEST.ATTEST","effective_from":"2026-09-09",
	     "values":{"basis":"gross","days":"15","share":"0.5","window":"10"}},
	    {"rule_key":"ZZ.TEST.ATTEST","effective_from":"2026-09-09",
	     "values":{"basis":"basic","days":"30","share":"0.5","window":"10"}}]}`)
	if _, err := ParseAttestation(raw); err == nil {
		t.Fatal("two answers to the same question were accepted")
	}
}

// A misspelt top-level key is a figure that silently does not arrive, so the
// decoder refuses what it does not recognise rather than ignoring it.
func TestAnUnknownFieldInTheFileIsRefused(t *testing.T) {
	withTestPack(t)

	raw := []byte(`{"version":"1","read_by":"a@b.test","read_on":"2026-09-09",
	  "statement":"Something long enough to be a statement.",
	  "verified":true,
	  "rules":[{"rule_key":"ZZ.TEST.ATTEST","effective_from":"2026-09-09",
	            "values":{"basis":"gross","days":"15","share":"0.5","window":"1"}}]}`)
	if _, err := ParseAttestation(raw); err == nil {
		t.Fatal("an unrecognised key was ignored rather than refused")
	}
}

// --- the effective date ----------------------------------------------------

func TestARecordedValueCannotStartOverThePlaceholderItReplaces(t *testing.T) {
	placeholderFrom := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cur := current{from: placeholderFrom}

	for _, when := range []string{"2025-12-31", "2026-01-01"} {
		if _, err := effectiveFrom(
			AttestedRule{Key: "ZZ.TEST.ATTEST", From: when}, cur); err == nil {
			t.Errorf("%s was accepted over a placeholder in force from "+
				"2026-01-01. A period the product refused to compute must "+
				"go on refusing when the report is re-run", when)
		}
	}

	got, err := effectiveFrom(
		AttestedRule{Key: "ZZ.TEST.ATTEST", From: "2026-01-02"}, cur)
	if err != nil {
		t.Fatalf("the day after was refused: %v", err)
	}
	if got.Format("2006-01-02") != "2026-01-02" {
		t.Errorf("effective from %s", got)
	}
}

func TestTheTemplateOffersADateThatWillBeAccepted(t *testing.T) {
	// The template's suggestion has to satisfy the rule the applier enforces,
	// or somebody fills a file in and is refused by the tool that wrote it.
	today := time.Date(2026, time.September, 9, 0, 0, 0, 0, time.UTC)

	for _, placeholderFrom := range []string{"2020-01-01", "2026-01-01", "2026-09-09", "2027-01-01"} {
		suggested := firstEffectiveDate(placeholderFrom, today)
		from, _ := time.Parse("2006-01-02", placeholderFrom)
		got, err := effectiveFrom(
			AttestedRule{Key: "ZZ.TEST.ATTEST", From: suggested},
			current{from: from})
		if err != nil {
			t.Errorf("the template suggested %s against a placeholder from "+
				"%s and the applier refused it: %v",
				suggested, placeholderFrom, err)
			continue
		}
		if !got.After(from) {
			t.Errorf("%s is not after %s", suggested, placeholderFrom)
		}
	}
}

// --- merging ---------------------------------------------------------------

// A payload can hold figures that are already recorded beside ones that are
// not: the e-commerce cooling-off rule carries a verified number of days and an
// unrecorded list of exemptions. Filling in the second must not disturb the
// first, and must not change its type.
func TestFillingOneFieldLeavesTheRestExactlyAsTheyWere(t *testing.T) {
	cur := map[string]json.RawMessage{
		"days":                json.RawMessage(`14`),
		"category_exemptions": json.RawMessage(`"__VERIFY__"`),
	}
	merged, err := mergePayload(cur, map[string]string{
		"category_exemptions": "none",
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("the merged payload is not an object: %v", err)
	}
	if string(got["days"]) != "14" {
		t.Errorf("a recorded figure changed to %s; it was the number 14 and "+
			"a string \"14\" is a different value to every reader of it",
			got["days"])
	}
	if string(got["category_exemptions"]) != `"none"` {
		t.Errorf("the filled field is %s", got["category_exemptions"])
	}
	if strings.Contains(string(merged), Placeholder) {
		t.Error("the merged payload still holds a placeholder")
	}
}

func TestPlaceholderFieldsNamesOnlyWhatIsUnfilled(t *testing.T) {
	got := placeholderFields([]byte(
		`{"a":"__VERIFY__","b":"done","c":{"d":"__VERIFY__"},"e":14}`))
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("named %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("named %v, want %v", got, want)
		}
	}
}

func TestApplyingTheSameFileTwiceIsRecognised(t *testing.T) {
	cur := map[string]json.RawMessage{
		"basis": json.RawMessage(`"gross"`),
		"days":  json.RawMessage(`"15"`),
	}
	if !samePayload(cur, map[string]string{"basis": "gross", "days": "15"}) {
		t.Error("a file already applied was not recognised as applied, so " +
			"every deployment would supersede the rule with a copy of itself")
	}
	if samePayload(cur, map[string]string{"basis": "gross", "days": "30"}) {
		t.Error("a different figure was read as the same one")
	}
}

// --- the resolver's cache --------------------------------------------------

// Two dates in one month are two questions.
//
// The cache keyed by month, on the reasoning that a rule changing mid-month is
// rare. Recording a legal value from a source file makes it ordinary: the
// effective date is the day somebody records it, which is almost never the
// first. A placeholder in force until the 9th and a verified figure from the
// 9th would then share a key, and whichever a process resolved first would
// govern the whole month for that process — silently, and differently in each
// running instance.
func TestTwoDatesInOneMonthDoNotShareAnAnswer(t *testing.T) {
	sep := func(day int) Query {
		return Query{
			Key:     "SA.EOSB.ENTITLEMENT",
			Country: "sa",
			AsOf:    time.Date(2026, time.September, day, 0, 0, 0, 0, time.UTC),
		}
	}
	if cacheKey(sep(8)) == cacheKey(sep(9)) {
		t.Fatal("the 8th and the 9th of the same month resolve from one " +
			"cache entry, so a value recorded on the 9th would govern the " +
			"8th as well, or not govern the 9th, depending only on which " +
			"was asked first")
	}
	if cacheKey(sep(9)) != cacheKey(sep(9)) {
		t.Error("the same date does not cache")
	}
}
