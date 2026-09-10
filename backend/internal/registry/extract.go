// Reading a rule's figures out of the document that states them.
//
// # What this is and, more importantly, what it is not
//
// It is a deterministic reader for one specific document per rule: given the
// text of a statute, it locates the article that states each field of the
// payload and reports what that article says, with the sentence it read.
//
// It is NOT an authority on the law, and nothing here is allowed to behave as
// if it were. Three rules hold that line:
//
//  1. Every value is either quoted from the document or derived from something
//     quoted, and the derivation is recorded in words beside it. Nothing is
//     assumed, defaulted or filled in from what the software expected to find.
//  2. A pattern that does not match is a refusal, naming the field and what it
//     was looking for. It is never a zero, never a skip and never a fallback to
//     a value from somewhere else.
//  3. The output is a CANDIDATE. It is shown to a person with its evidence
//     before it is applied, and applying it is their act. A machine reading of
//     a statute is a reading; a person putting their name to it is what the
//     registry has always required and still requires.
//
// # Why patterns rather than a language model
//
// Because a regulatory trail has to be reproducible. The same document must
// produce the same figures on every run, on every machine, in a year's time,
// and the reason a figure came out must be inspectable — which is what a named
// pattern and a quoted sentence give you and a probability does not.
package registry

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The wage bases this product knows how to compute an award on.
//
// Declared here rather than in the payroll engine because they are the closed
// vocabulary of a REGISTRY field: `sources.json` offers exactly these three as
// the choices for `wage_basis`, the validator checks against them, and the
// reading above resolves a statute's definition of "wage" into one of them. The
// engine consumes the vocabulary; it does not own it.
const (
	EOSBBasisBasic         = "basic"
	EOSBBasisBasicHousing  = "basic_plus_housing"
	EOSBBasisAllAllowances = "basic_plus_all_allowances"
)

// Extracted is one field, as read out of the document.
type Extracted struct {
	Field string `json:"field"`

	// Value in the form the registry stores: a string, and for a fraction the
	// rational the law states rather than a decimal approximation of it.
	Value string `json:"value"`

	// Article is where in the document it was read. Free text, because a
	// regulation may be organised by article, section, schedule or paragraph.
	Article string `json:"article"`

	// Evidence is the sentence the value was read out of, normalised but not
	// paraphrased. It is what an operator checks the reading against.
	Evidence string `json:"evidence"`

	// Derived explains a value the document does not print literally: how the
	// figure follows from the sentence quoted. Empty when the document states
	// the value in the form recorded.
	Derived string `json:"derived,omitempty"`
}

// Extraction is everything read out of one document for one rule.
type Extraction struct {
	RuleKey string      `json:"rule_key"`
	Fields  []Extracted `json:"fields"`
}

// Payload renders the extraction as the rule payload the registry stores.
func (e Extraction) Payload() map[string]string {
	out := make(map[string]string, len(e.Fields))
	for _, f := range e.Fields {
		out[f.Field] = f.Value
	}
	return out
}

// extractors are the readings this product knows how to perform.
//
// A rule with no extractor is not a failure of this file. It means nobody has
// written a reading for that document yet, and the workflow says so and offers
// the guided form instead — which is the same workflow, minus the machine
// reading, and produces the same audited result.
var extractors = map[string]func(Scanned) (Extraction, error){
	"SA.EOSB.ENTITLEMENT": extractSaudiEndOfService,
}

// CanExtract says whether this product can read the given rule from a document.
func CanExtract(ruleKey string) bool {
	_, ok := extractors[strings.ToUpper(strings.TrimSpace(ruleKey))]
	return ok
}

// Extract reads a rule's figures out of a normalised document.
func Extract(ruleKey, normalised string) (Extraction, error) {
	key := strings.ToUpper(strings.TrimSpace(ruleKey))
	fn, ok := extractors[key]
	if !ok {
		return Extraction{}, errs.Newf(errs.CodeInvalidInput,
			"This product has no reading for %s, so a document cannot be "+
				"turned into its figures automatically. Record it through the "+
				"guided form instead: the source pack describes every field, "+
				"and the result is validated and audited the same way.", key)
	}
	out, err := fn(Scan(normalised))
	if err != nil {
		return Extraction{}, err
	}
	out.RuleKey = key
	return out, nil
}

// --- the vocabulary a statute states quantities in -------------------------

// monthsOfWage maps the way an entitlement per year is written to a count of
// months of wage. Rationals, because a half is a half and 0.5 is a rendering
// of it that a later multiplication can round away.
var monthsOfWage = map[string]string{
	"half-month":          "1/2",
	"halfmonth":           "1/2",
	"one-month":           "1",
	"onemonth":            "1",
	"month":               "1",
	"two-month":           "2",
	"twomonth":            "2",
	"quarter-month":       "1/4",
	"three-quarter-month": "3/4",
}

// shareOfAward maps the way a share is written to the fraction it is.
var shareOfAward = map[string]string{
	"onethird":     "1/3",
	"athird":       "1/3",
	"one-third":    "1/3",
	"twothirds":    "2/3",
	"two-thirds":   "2/3",
	"onehalf":      "1/2",
	"ahalf":        "1/2",
	"one-half":     "1/2",
	"onequarter":   "1/4",
	"threequarter": "3/4",
	"thefull":      "1",
	"full":         "1",
}

// denseKey folds a captured phrase to the form the vocabularies are keyed by:
// lower case with the spaces gone, because a match against the spaceless text
// carries no spaces and a match against readable text may.
func denseKey(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}

// wordNumbers are the small numbers a statute writes out.
var wordNumbers = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "fifteen": 15, "twenty": 20,
}

// countOf reads a small number written as a word or as digits.
func countOf(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := wordNumbers[s]; ok {
		return n, true
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n, true
	}
	return 0, false
}

// ParseFraction reads a fraction stated either as a rational or a decimal.
//
// # Why a rational is accepted at all
//
// Article 85 says "one third". Recorded as 0.3333 it pays 9,999.00 on a 30,000
// award, and the person owed 10,000.00 is short a riyal because a figure that
// the law states exactly was written down inexactly. The registry stores what
// the law says — `1/3` — and this turns it into a decimal at a precision that
// rounds correctly at the point money is computed.
//
// A decimal is still accepted, because most regulations state a rate rather
// than a share and writing 0.15 as 15/100 helps nobody.
func ParseFraction(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	num, den, isRational := strings.Cut(s, "/")
	if !isRational {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
				"%q is not a number.", s)
		}
		return d, nil
	}

	n, err := decimal.NewFromString(strings.TrimSpace(num))
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"%q is not a fraction: %q is not a number.", s, num)
	}
	d, err := decimal.NewFromString(strings.TrimSpace(den))
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"%q is not a fraction: %q is not a number.", s, den)
	}
	if d.IsZero() {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"%q divides by zero.", s)
	}
	// Well beyond any money this will multiply: the product of a 28-digit
	// quotient and a wage rounds to the same halalah as the exact rational.
	return n.DivRound(d, 28), nil
}

// --- Saudi Arabia: the end-of-service award --------------------------------

// numberWords is the alternation a threshold may be written as: a small number
// spelled out, or digits. Bounded on purpose — a statute writes "ten years",
// not "ten thousand years", and an unbounded run of digits next to a literal
// would swallow the word after it in the spaceless text these match against.
const numberWords = `(one|two|three|four|five|six|seven|eight|nine|ten|` +
	`eleven|twelve|fifteen|twenty|\d{1,3})`

// quantityWords is how much wage a year of service earns, written the ways a
// statute writes it. An explicit alternation rather than a run of letters,
// because these match text with the spaces removed: `[a-z]+` before "wage" has
// no word boundary to stop at there and swallows the whole clause before it.
const quantityWords = `(half-month|halfmonth|one-month|onemonth|two-month|` +
	`twomonth|quarter-month|three-quarter-month|month)`

// The diagnostic twins. When the strict pattern above finds nothing, these say
// what the document put where the quantity should be, so a refusal can name
// the word this product did not recognise rather than only the field it could
// not fill. Bounded to a couple of dozen characters: without spaces to stop at,
// an unbounded class would report half the sentence.
var (
	saFirstBandLoose = DensePattern(
		`(?i) ([a-z-]{1,24}) wage for each of the first `)
	saAfterBandLoose = DensePattern(
		`(?i) ([a-z-]{1,24}) wage for each of the ` +
			`(?:following|subsequent|remaining) `)
)

// The patterns are written with spaces for readability and compiled without
// them: see `DensePattern`. The Ministry's PDF breaks words mid-word — it
// renders "the full award" as "the ful l award" — and a pattern written
// against the spaces would find six of Article 85's figures and miss the
// seventh, which is exactly what it did before this changed.
var (
	// Article 84, first band. "the amount of a half-month wage for each of the
	// first five years".
	saFirstBand = DensePattern(
		`(?i) a? ` + quantityWords + ` wage for each of the first ` +
			numberWords + ` years `)

	// Article 84, second band. "a one-month wage for each of the following
	// years".
	saAfterBand = DensePattern(
		`(?i) a? ` + quantityWords + ` wage for each of the ` +
			`(?:following|subsequent|remaining) years `)

	// Article 84, the wage the award is computed on. "shall be calculated on
	// the basis of the last wage".
	saWageBasis = DensePattern(
		`(?i) calculated on the basis of the (last|final) wage `)

	// Article 84, the part-year. "entitled to an end-of-service award for the
	// portions of the year in proportion to the time spent on the job".
	saProportional = DensePattern(
		`(?i) for the portions? of the year in proportion to the time spent `)

	// Article 85, the resignation bands.
	saResignLower = DensePattern(
		`(?i) entitled to (one third|two thirds|one half|a third|a half|the full) ` +
			`(?:award)? of the award after (?:a )? service of not less than ` +
			numberWords + ` consecutive years and not more than ` +
			numberWords + ` years `)

	saResignMiddle = DensePattern(
		`(?i) to (one third|two thirds|one half|a third|a half|the full) ` +
			`(?:award)? if his service is in excess of ` + numberWords +
			` (?:consecutive|successive) years but less than ` + numberWords +
			` years `)

	saResignFull = DensePattern(
		`(?i) to (the full|one third|two thirds|one half) (?:award)? ` +
			`if his service amounts to ` + numberWords + ` years `)

	// The statutory definition chain that says what "wage" means.
	saWageIsActual = DensePattern(`(?i) wage: (actualwage|basicwage) `)
	saActualWage   = DensePattern(
		`(?i) actual wage: the basic wage plus all other due increments `)
)

// The bands this product's payload is shaped around, in years.
//
// The rule has four resignation fields and they are named for these thresholds.
// A document stating different ones is a different rule, and mapping it onto
// these fields would file one law's figures under another law's bands — so the
// thresholds are read out of the document and CHECKED, not assumed.
const (
	saResignFromYears = 2
	saResignMidYears  = 5
	saResignFullYears = 10
	saFirstBandYears  = 5
)

// extractSaudiEndOfService reads Articles 84 and 85.
func extractSaudiEndOfService(doc Scanned) (Extraction, error) {
	art84, err := ArticleText(doc, 84, 85)
	if err != nil {
		return Extraction{}, err
	}
	art85, err := ArticleText(doc, 85, 86)
	if err != nil {
		return Extraction{}, err
	}

	monthDays, monthEvidence, err := MonthDays(doc)
	if err != nil {
		return Extraction{}, err
	}
	days := decimal.NewFromInt(int64(monthDays))

	var out Extraction

	// --- Article 84: the two service bands ---------------------------------

	first := art84.Find(saFirstBand)
	if first == nil {
		if loose := art84.Find(saFirstBandLoose); loose != nil {
			return Extraction{}, unknownQuantity("days_per_year_first_five",
				84, art84.At(loose[2], loose[3]))
		}
		return Extraction{}, missing("days_per_year_first_five", 84,
			"an entitlement per year for the first years of service, of the "+
				"form \"a half-month wage for each of the first five years\"")
	}
	firstWords := art84.At(first[2], first[3])
	firstYears := art84.At(first[4], first[5])
	if years, ok := countOf(firstYears); !ok || years != saFirstBandYears {
		return Extraction{}, errs.Newf(errs.CodeInvalidInput,
			"Article 84 of this document bands the award at %q years. This "+
				"rule's fields are named for a five-year band, so the two do "+
				"not describe the same entitlement and mapping one onto the "+
				"other would file the wrong figures.", firstYears)
	}
	firstDays, err := wageMonthsToDays(firstWords, days,
		"days_per_year_first_five", 84)
	if err != nil {
		return Extraction{}, err
	}
	out.Fields = append(out.Fields, Extracted{
		Field:    "days_per_year_first_five",
		Value:    firstDays,
		Article:  "84",
		Evidence: art84.Sentence(first[0], first[1]),
		Derived: fmt.Sprintf(
			"%q per year, and this law defines a month as %d days (%q), so "+
				"the entitlement is %s days of wage per year.",
			firstWords, monthDays, monthEvidence, firstDays),
	})

	after := art84.Find(saAfterBand)
	if after == nil {
		if loose := art84.Find(saAfterBandLoose); loose != nil {
			return Extraction{}, unknownQuantity("days_per_year_after_five",
				84, art84.At(loose[2], loose[3]))
		}
		return Extraction{}, missing("days_per_year_after_five", 84,
			"an entitlement per year for later service, of the form \"a "+
				"one-month wage for each of the following years\"")
	}
	afterWords := art84.At(after[2], after[3])
	afterDays, err := wageMonthsToDays(afterWords, days,
		"days_per_year_after_five", 84)
	if err != nil {
		return Extraction{}, err
	}
	out.Fields = append(out.Fields, Extracted{
		Field:    "days_per_year_after_five",
		Value:    afterDays,
		Article:  "84",
		Evidence: art84.Sentence(after[0], after[1]),
		Derived: fmt.Sprintf(
			"%q per year, on the same %d-day month, so the entitlement is %s "+
				"days of wage per year.", afterWords, monthDays, afterDays),
	})

	// --- Article 84: which wage ---------------------------------------------

	basis, basisEvidence, basisDerived, err := saudiWageBasis(doc, art84)
	if err != nil {
		return Extraction{}, err
	}
	out.Fields = append(out.Fields, Extracted{
		Field: "wage_basis", Value: basis, Article: "84",
		Evidence: basisEvidence, Derived: basisDerived,
	})

	// The part-year rule is not a field of this payload — the engine prorates
	// by completed months either way — but its presence is what makes that
	// engine's behaviour the document's behaviour rather than a convention. A
	// document without it is not the article this reading was written for.
	if art84.Find(saProportional) == nil {
		return Extraction{}, errs.New(errs.CodeInvalidInput,
			"Article 84 of this document does not say that a part-year is "+
				"entitled in proportion to the time served. This product "+
				"prorates part-years, so a document that does not state that "+
				"rule is not the article it was written to read.")
	}

	// --- Article 85: the resignation bands ---------------------------------

	lower := art85.Find(saResignLower)
	if lower == nil {
		return Extraction{}, missing("resignation_fraction_two_to_five_years", 85,
			"a share of the award on resignation after a minimum service, of "+
				"the form \"one third of the award after service of not less "+
				"than two consecutive years and not more than five years\"")
	}
	lowerFromWord := art85.At(lower[4], lower[5])
	lowerToWord := art85.At(lower[6], lower[7])
	lowerFrom, okFrom := countOf(lowerFromWord)
	lowerTo, okTo := countOf(lowerToWord)
	if !okFrom || !okTo ||
		lowerFrom != saResignFromYears || lowerTo != saResignMidYears {
		return Extraction{}, errs.Newf(errs.CodeInvalidInput,
			"Article 85 of this document bands resignation from %q to %q "+
				"years. This rule's fields are named for two to five, so the "+
				"two do not describe the same bands.",
			lowerFromWord, lowerToWord)
	}
	lowerShare, err := shareValue(art85.At(lower[2], lower[3]),
		"resignation_fraction_two_to_five_years", 85)
	if err != nil {
		return Extraction{}, err
	}
	lowerEvidence := art85.Sentence(lower[0], lower[1])
	out.Fields = append(out.Fields, Extracted{
		Field: "resignation_fraction_two_to_five_years", Value: lowerShare,
		Article: "85", Evidence: lowerEvidence,
	})

	// Below the threshold the article confers nothing. That is a reading of the
	// article's silence, and it is labelled as one rather than presented as a
	// figure the document prints.
	out.Fields = append(out.Fields, Extracted{
		Field: "resignation_fraction_under_two_years", Value: "0",
		Article: "85", Evidence: lowerEvidence,
		Derived: fmt.Sprintf(
			"The article confers a share on resignation only after service of "+
				"not less than %d consecutive years. Below that it confers "+
				"none, so the share is zero. This is the article's silence "+
				"below the threshold, not a figure it prints.", lowerFrom),
	})

	middle := art85.Find(saResignMiddle)
	if middle == nil {
		return Extraction{}, missing("resignation_fraction_five_to_ten_years", 85,
			"a share for service beyond the middle threshold, of the form "+
				"\"two thirds if his service is in excess of five consecutive "+
				"years but less than 10 years\"")
	}
	midFromWord := art85.At(middle[4], middle[5])
	midToWord := art85.At(middle[6], middle[7])
	midFrom, okMidFrom := countOf(midFromWord)
	midTo, okMidTo := countOf(midToWord)
	if !okMidFrom || !okMidTo ||
		midFrom != saResignMidYears || midTo != saResignFullYears {
		return Extraction{}, errs.Newf(errs.CodeInvalidInput,
			"Article 85 of this document bands the middle share from %q to "+
				"%q years. This rule's fields are named for five to ten.",
			midFromWord, midToWord)
	}
	midShare, err := shareValue(art85.At(middle[2], middle[3]),
		"resignation_fraction_five_to_ten_years", 85)
	if err != nil {
		return Extraction{}, err
	}
	out.Fields = append(out.Fields, Extracted{
		Field: "resignation_fraction_five_to_ten_years", Value: midShare,
		Article: "85", Evidence: art85.Sentence(middle[0], middle[1]),
	})

	full := art85.Find(saResignFull)
	if full == nil {
		return Extraction{}, missing("resignation_fraction_over_ten_years", 85,
			"a share for long service, of the form \"the full award if his "+
				"service amounts to 10 years or more\"")
	}
	fullFromWord := art85.At(full[4], full[5])
	fullFrom, okFull := countOf(fullFromWord)
	if !okFull || fullFrom != saResignFullYears {
		return Extraction{}, errs.Newf(errs.CodeInvalidInput,
			"Article 85 of this document grants the whole award at %q years. "+
				"This rule's field is named for ten.", fullFromWord)
	}
	fullShare, err := shareValue(art85.At(full[2], full[3]),
		"resignation_fraction_over_ten_years", 85)
	if err != nil {
		return Extraction{}, err
	}
	out.Fields = append(out.Fields, Extracted{
		Field: "resignation_fraction_over_ten_years", Value: fullShare,
		Article: "85", Evidence: art85.Sentence(full[0], full[1]),
	})

	return out, nil
}

// saudiWageBasis follows the statute's own definition chain.
//
// Article 84 computes on "the last wage" and does not say what a wage is. The
// definitions do, in two steps: `Wage: actual wage`, and `Actual Wage: the
// basic wage plus all other due increments`. Following that chain is the
// difference between recording which wage the law names and choosing one.
func saudiWageBasis(
	doc, art84 Scanned,
) (basis, evidence, derived string, err error) {
	anchor := art84.Find(saWageBasis)
	if anchor == nil {
		return "", "", "", missing("wage_basis", 84,
			"which wage the award is computed on, of the form \"calculated "+
				"on the basis of the last wage\"")
	}
	which := art84.At(anchor[2], anchor[3])
	articleSentence := art84.Sentence(anchor[0], anchor[1])

	defined, defEvidence := statutoryWage(doc)
	switch defined {
	case "actualwage":
		actual := doc.Find(saActualWage)
		if actual == nil {
			return "", "", "", errs.New(errs.CodeInvalidInput,
				"This document defines the wage as the actual wage and does "+
					"not then say what the actual wage is, so which pay the "+
					"award is computed on cannot be established from it.")
		}
		return EOSBBasisAllAllowances,
			articleSentence + " " + defEvidence + " " +
				doc.Sentence(actual[0], actual[1]),
			fmt.Sprintf(
				"Article 84 computes on the %s wage. This law defines Wage as "+
					"the actual wage, and the actual wage as the basic wage "+
					"plus all other due increments, which is this product's "+
					"%q basis.", which, EOSBBasisAllAllowances),
			nil
	case "basicwage":
		return EOSBBasisBasic,
			articleSentence + " " + defEvidence,
			fmt.Sprintf(
				"Article 84 computes on the %s wage, and this law defines "+
					"Wage as the basic wage, which is this product's %q basis.",
				which, EOSBBasisBasic),
			nil
	default:
		return "", "", "", errs.New(errs.CodeInvalidInput,
			"This document does not define what \"wage\" means, so which pay "+
				"the end-of-service award is computed on cannot be read out "+
				"of it. The three bases this product understands are "+
				EOSBBasisBasic+", "+EOSBBasisBasicHousing+" and "+
				EOSBBasisAllAllowances+".")
	}
}

// statutoryWage finds the definition of "Wage" itself.
//
// "Basic Wage:" and "Actual Wage:" are different definitions in the same list
// and both end in the same five characters, so a match has to be checked for
// what precedes it. Go's regexp is RE2 and has no lookbehind, and adding one
// for this would be a worse trade than four lines that say plainly what they
// exclude.
func statutoryWage(doc Scanned) (defined, evidence string) {
	for _, loc := range saWageIsActual.FindAllStringSubmatchIndex(doc.Dense, -1) {
		before := strings.ToLower(doc.At(max(0, loc[0]-6), loc[0]))
		if strings.HasSuffix(before, "basic") || strings.HasSuffix(before, "actual") {
			continue
		}
		return strings.ToLower(doc.At(loc[2], loc[3])),
			doc.Sentence(loc[0], loc[1])
	}
	return "", ""
}

// wageMonthsToDays turns "half-month" into days of wage per year.
func wageMonthsToDays(
	words string, monthDays decimal.Decimal, field string, article int,
) (string, error) {
	months, ok := monthsOfWage[denseKey(words)]
	if !ok {
		return "", errs.Newf(errs.CodeInvalidInput,
			"Article %d states the entitlement for %s as %q per year, and "+
				"this product does not know how many months of wage that is. "+
				"It reads: %s.", article, field, words,
			strings.Join(sortedKeys(monthsOfWage), ", "))
	}
	m, err := ParseFraction(months)
	if err != nil {
		return "", err
	}
	return m.Mul(monthDays).String(), nil
}

// shareValue turns "one third" into the fraction the law states.
func shareValue(words string, field string, article int) (string, error) {
	share, ok := shareOfAward[denseKey(words)]
	if !ok {
		return "", errs.Newf(errs.CodeInvalidInput,
			"Article %d states %s as %q of the award, and this product does "+
				"not know what fraction that is. It reads: %s.",
			article, field, words, strings.Join(sortedKeys(shareOfAward), ", "))
	}
	return share, nil
}

// unknownQuantity is the refusal when the sentence is there and the quantity
// in it is not one this product can convert into days of wage.
//
// Worth distinguishing from a missing article. "Article 84 does not state the
// entitlement" sends somebody looking for a different document; "it states it
// as a third-of-a-month wage and this product does not know that phrase" sends
// them to the right place, which is this file.
func unknownQuantity(field string, article int, found string) error {
	return errs.Newf(errs.CodeInvalidInput,
		"Article %d states the entitlement for %s in a quantity this product "+
			"does not recognise, near %q. It reads: %s. Nothing is assumed "+
			"in its place.", article, field, found,
		strings.Join(sortedKeys(monthsOfWage), ", ")).
		WithField(field, "An unrecognised quantity of wage per year.")
}

// missing is the refusal when a pattern finds nothing.
//
// It names the field and describes the sentence that was looked for, because
// "extraction failed" tells an operator holding the right document nothing
// about whether they have the wrong one, the wrong edition, or the right one
// worded differently.
func missing(field string, article int, looking string) error {
	return errs.Newf(errs.CodeInvalidInput,
		"Article %d of this document does not state %s. This reading looks "+
			"for %s. Nothing is assumed in its place: a figure this product "+
			"cannot find is a figure it will not supply.",
		article, field, looking).WithField(field, "Not found in Article "+
		fmt.Sprint(article)+".")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic, so a refusal reads the same way twice.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
