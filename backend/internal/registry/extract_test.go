// Reading Articles 84 and 85 out of the document the Ministry publishes.
//
// These run without a database. What they exercise is the reading itself: that
// the right sentence produces the right figure, that a document saying
// something else is refused rather than bent to fit, and that nothing is
// supplied when the document does not supply it.
//
// The text below is the Labour Law as published by the Ministry of Human
// Resources and Social Development (Royal Decree No. M/51), reproduced here as
// a test fixture. It is a fixture in the ordinary sense — an input to a test —
// and it is not this product's copy of the law: the copy that matters is the
// one retrieved through the source-document workflow, with its hash and its
// retrieval date. Nothing here is loaded into a registry, seeded, or used by
// anything at run time.
package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// The articles as the Ministry's PDF renders them once normalised, including
// the running page header that lands in the middle of Article 84's sentence.
const officialArticles = `Basic Wage: All that is given to a worker for his ` +
	`work by virtue of a written or unwritten employment contract regardless ` +
	`of the kind of wage or its method of payment, in addition to periodic ` +
	`increments. Actual Wage: The basic wage plus all other due increments ` +
	`decided for a worker for the effort he exerts at work or for risks he ` +
	`encounters in the course of performing his work. Wage: actual wage. ` +
	`Month: 30 days, unless otherwise specified in the employment contract ` +
	`or the work organization regulation. ` +
	`Chapter 4: End-of-Service Award Article 84 Upon the end of the ` +
	`employment relation, the employer shall pay the worker an ` +
	`end-of-service award equivalent to the amount of a half-month wage for ` +
	`each of the first five years and a one-month wage for each of the ` +
	`following years. The end-of-service award shall be calculated on the ` +
	`basis of the last wage and the worker shall be entitled to an ` +
	`end-of-service award for the portions of the year in proportion to the ` +
	`time spent on the job. Article 85 If the employment relation ends due ` +
	`to the worker's resignation, he shall, in this case, be entitled to one ` +
	`third of the award after service of not less than two consecutive years ` +
	`and not more than five years, to two thirds if his service is in excess ` +
	`of five consecutive years but less than 10 years, and to the full award ` +
	`if his service amounts to 10 years or more. Article 86 As an exception ` +
	`to the provisions of Article 8 of this Law, it may be agreed that the ` +
	`wage used as a basis for calculating the end-of-service award does not ` +
	`include all or some of the commissions. Article 87 As an exception to ` +
	`the provisions of Article 85 of this Law, the worker shall be entitled ` +
	`to the full end-of-service award if he leaves the work due to a force ` +
	`majeure beyond his control.`

func fieldsOf(t *testing.T, e Extraction) map[string]Extracted {
	t.Helper()
	out := map[string]Extracted{}
	for _, f := range e.Fields {
		out[f.Field] = f
	}
	return out
}

// Every field of the rule is read, and every one of them is the article.
func TestTheEndOfServiceArticlesAreReadIntoTheRule(t *testing.T) {
	got, err := Extract("SA.EOSB.ENTITLEMENT", officialArticles)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	fields := fieldsOf(t, got)
	for name, want := range map[string]string{
		// Article 84: half a 30-day month, then a whole one.
		"days_per_year_first_five": "15",
		"days_per_year_after_five": "30",
		// Article 84 read through the law's own definition of "wage".
		"wage_basis": EOSBBasisAllAllowances,
		// Article 85, as fractions rather than as decimals that lose money.
		"resignation_fraction_under_two_years":   "0",
		"resignation_fraction_two_to_five_years": "1/3",
		"resignation_fraction_five_to_ten_years": "2/3",
		"resignation_fraction_over_ten_years":    "1",
	} {
		f, ok := fields[name]
		if !ok {
			t.Errorf("%s was not read out of the document", name)
			continue
		}
		if f.Value != want {
			t.Errorf("%s = %q, want %q", name, f.Value, want)
		}
	}

	// The rule has exactly seven fields and the reading fills all of them.
	src, _ := SourceFor("SA.EOSB.ENTITLEMENT")
	if len(fields) != len(src.Fields) {
		t.Errorf("the reading produced %d fields and the rule has %d",
			len(fields), len(src.Fields))
	}
}

// A third is a third, not 0.3333.
//
// The award for someone leaving on 30,000 with the middle band is 10,000.00.
// Recorded as 0.3333 it is 9,999.00, and the riyal is missing because a figure
// the law states exactly was written down inexactly.
func TestAThirdIsRecordedAsAThird(t *testing.T) {
	got, err := Extract("SA.EOSB.ENTITLEMENT", officialArticles)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	payload := got.Payload()

	third, err := ParseFraction(payload["resignation_fraction_two_to_five_years"])
	if err != nil {
		t.Fatalf("parse the fraction: %v", err)
	}
	award := third.Mul(decimalFromString(t, "30000")).Round(2)
	if award.String() != "10000" {
		t.Errorf("a third of a 30,000 award computes as %s, want 10000",
			award.String())
	}
}

// Every value is either quoted or explained.
func TestEveryExtractedFigureCarriesItsEvidence(t *testing.T) {
	got, err := Extract("SA.EOSB.ENTITLEMENT", officialArticles)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, f := range got.Fields {
		if strings.TrimSpace(f.Evidence) == "" {
			t.Errorf("%s was read with no sentence behind it", f.Field)
		}
		if f.Article == "" {
			t.Errorf("%s does not say which article it came from", f.Field)
		}
		// A value the document does not print literally has to say how it
		// follows from one that it does.
		if f.Value == "0" && strings.TrimSpace(f.Derived) == "" {
			t.Errorf("%s is zero and nothing says why", f.Field)
		}
	}

	fields := fieldsOf(t, got)
	if ev := fields["resignation_fraction_two_to_five_years"].Evidence; !strings.Contains(
		ev, "not less than two consecutive years") {
		t.Errorf("the two-to-five band quotes: %q", ev)
	}
	if ev := fields["days_per_year_first_five"].Evidence; !strings.Contains(
		ev, "half-month wage for each of the first five years") {
		t.Errorf("the first band quotes: %q", ev)
	}
	// The wage basis is not printed in Article 84 at all; it is followed
	// through the definitions, and the trail has to be visible.
	if d := fields["wage_basis"].Derived; !strings.Contains(d, "actual wage") {
		t.Errorf("the wage basis does not show its derivation: %q", d)
	}
}

// A document that is not this law is refused, not squeezed into the fields.
func TestADocumentStatingDifferentBandsIsRefused(t *testing.T) {
	for _, c := range []struct{ name, swap, with string }{
		{
			"a different first band",
			"for each of the first five years",
			"for each of the first three years",
		},
		{
			"a different resignation threshold",
			"not less than two consecutive years",
			"not less than four consecutive years",
		},
		{
			"a different long-service threshold",
			"amounts to 10 years or more",
			"amounts to 15 years or more",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := strings.Replace(officialArticles, c.swap, c.with, 1)
			if _, err := Extract("SA.EOSB.ENTITLEMENT", doc); err == nil {
				t.Error("a document with different bands was read into this " +
					"rule's fields anyway")
			}
		})
	}
}

// A document missing an article is refused by name.
func TestADocumentWithoutTheArticleIsRefusedByName(t *testing.T) {
	doc := strings.Replace(officialArticles, "Article 85 If the employment",
		"Article 99 If the employment", 1)
	_, err := Extract("SA.EOSB.ENTITLEMENT", doc)
	if err == nil {
		t.Fatal("a document with no Article 85 produced resignation fractions")
	}
	if !strings.Contains(err.Error(), "Article 85") {
		t.Errorf("the refusal does not name the missing article: %v", err)
	}
}

// A document that does not define a month cannot state a half-month award.
func TestADocumentThatDoesNotDefineAMonthIsRefused(t *testing.T) {
	doc := strings.Replace(officialArticles, "Month: 30 days,", "", 1)
	_, err := Extract("SA.EOSB.ENTITLEMENT", doc)
	if err == nil {
		t.Fatal("a half-month wage was converted into days with no definition " +
			"of a month to convert it by")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "month") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// A wage the law defines differently produces a different basis, not the same
// one.
func TestTheWageBasisFollowsTheLawsOwnDefinition(t *testing.T) {
	doc := strings.Replace(officialArticles,
		"Wage: actual wage.", "Wage: basic wage.", 1)
	got, err := Extract("SA.EOSB.ENTITLEMENT", doc)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if v := fieldsOf(t, got)["wage_basis"].Value; v != EOSBBasisBasic {
		t.Errorf("wage_basis = %q against a law defining the wage as the "+
			"basic wage, want %q", v, EOSBBasisBasic)
	}
}

// A rule this product has no reading for says so, and says what to do instead.
func TestARuleWithNoReadingSaysSoRatherThanGuessing(t *testing.T) {
	_, err := Extract("SA.GOSI.RATES", officialArticles)
	if err == nil {
		t.Fatal("a document was read into a rule nothing knows how to read")
	}
	if !strings.Contains(err.Error(), "guided form") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
}

// What the reading produces passes the validator the registry already had.
//
// The two were written at different times for different reasons, and a reading
// that produced something `ValidatePayload` refuses would be a workflow with a
// wall in the middle of it.
func TestWhatIsReadOutIsWhatTheRegistryAccepts(t *testing.T) {
	got, err := Extract("SA.EOSB.ENTITLEMENT", officialArticles)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	payload, err := jsonPayload(got.Payload())
	if err != nil {
		t.Fatalf("render the payload: %v", err)
	}
	if err := ValidatePayload("SA.EOSB.ENTITLEMENT", payload); err != nil {
		t.Errorf("the registry refuses what the reading produced: %v", err)
	}
}

// --- normalisation ---------------------------------------------------------

// The PDF's line breaks, page headers and curly quotes do not survive.
func TestNormalisationRebuildsTheSentenceAPersonReads(t *testing.T) {
	raw := "an end\n-\nof\n-\nservice award equivalent to the\n" +
		"amount of a half\n-\nmonth wage for each of the first five years\n\n" +
		"Page 22\n\nand the worker’s entitlement."
	got := NormaliseSource(raw)
	want := "an end-of-service award equivalent to the amount of a " +
		"half-month wage for each of the first five years and the worker's " +
		"entitlement."
	if got != want {
		t.Errorf("normalised to:\n  %q\nwant:\n  %q", got, want)
	}
}

// The same document normalises identically every time, or a refresh would
// report a change that is really a whitespace difference.
func TestNormalisationIsStable(t *testing.T) {
	once := NormaliseSource(officialArticles)
	twice := NormaliseSource(once)
	if once != twice {
		t.Error("normalising twice changed the text, so a re-fetch of an " +
			"unchanged document would look like an amendment")
	}
}

// A cross-reference is not a heading.
//
// Article 87 says "as an exception to the provisions of Article 85 of this
// Law". Reading that as the start of Article 85 would scope the resignation
// bands to the wrong text.
func TestACrossReferenceIsNotMistakenForAHeading(t *testing.T) {
	body, err := ArticleText(Scan(officialArticles), 85, 86)
	if err != nil {
		t.Fatalf("article 85: %v", err)
	}
	if !strings.Contains(body.Text, "one third of the award") {
		t.Errorf("Article 85 scoped to: %q", body.Text)
	}
	if strings.Contains(body.Text, "force majeure") {
		t.Error("Article 85 ran on into Article 87")
	}
}

// --- helpers ---------------------------------------------------------------

func decimalFromString(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%q is not a number: %v", s, err)
	}
	return d
}

func jsonPayload(values map[string]string) ([]byte, error) {
	return json.Marshal(values)
}

// A word the PDF broke in half is still the word.
//
// The Ministry's own PDF renders "the full award" as "the ful l award" and
// "resignation" as "resignat ion": the typesetter kerned two letters apart and
// the text layer records the gap as a space. Six of Article 85's seven figures
// read correctly against a pattern written with spaces, and the seventh did
// not, which is a failure mode worth a test of its own because the document
// that triggers it is the actual official one.
func TestAWordThePDFBrokeInHalfIsStillTheWord(t *testing.T) {
	broken := strings.NewReplacer(
		"resignation", "resignat ion",
		"the full award", "the ful l award",
		"half-month", "half-mon th",
	).Replace(officialArticles)

	got, err := Extract("SA.EOSB.ENTITLEMENT", broken)
	if err != nil {
		t.Fatalf("extract from a document with broken words: %v", err)
	}
	fields := fieldsOf(t, got)
	if v := fields["resignation_fraction_over_ten_years"].Value; v != "1" {
		t.Errorf("the long-service share read as %q from a document that "+
			"prints \"the ful l award\"", v)
	}
	if v := fields["days_per_year_first_five"].Value; v != "15" {
		t.Errorf("the first band read as %q from a document that prints "+
			"\"half-mon th\"", v)
	}

	// And the evidence is still the sentence, not the spaceless form.
	if ev := fields["resignation_fraction_over_ten_years"].Evidence; !strings.Contains(
		ev, "ful l award") {
		t.Errorf("the evidence is not the document's own words: %q", ev)
	}
}

// A quantity this product cannot convert is named, not silently missed.
func TestAnUnrecognisedQuantityIsNamed(t *testing.T) {
	doc := strings.Replace(officialArticles,
		"a half-month wage for each of the first five years",
		"a fortnight wage for each of the first five years", 1)
	_, err := Extract("SA.EOSB.ENTITLEMENT", doc)
	if err == nil {
		t.Fatal("a fortnight was converted into days of wage")
	}
	if !strings.Contains(err.Error(), "fortnight") {
		t.Errorf("the refusal does not name what it did not understand: %v",
			err)
	}
}

// --- where a document may be fetched from ----------------------------------

// A regulatory importer that can be pointed anywhere is server-side request
// forgery with a ministry's name on it.
//
// The address is not the caller's to choose. The source pack records where each
// rule is published and a fetch must land on that authority's site — otherwise
// a Super Admin session, or anything that reaches one, could point "fetch the
// official source" at a cloud metadata endpoint, an internal admin port or a
// machine on the same network, and the response would be stored in the database
// and rendered back on a screen.
func TestAFetchIsBoundToTheAuthorityThatPublishes(t *testing.T) {
	const published = "https://www.hrsd.gov.sa/sites/default/files/2023-02/Labor.pdf"

	for _, c := range []struct {
		name, target string
		allowed      bool
	}{
		{"the published address", published, true},
		{"another path on the same site",
			"https://www.hrsd.gov.sa/sites/default/files/2026-01/Labor.pdf", true},
		{"the authority's bare domain", "https://hrsd.gov.sa/labour-law", true},
		{"a subdomain of it", "https://laws.hrsd.gov.sa/labour-law", true},

		// The mistake this kind of check is usually written with: a substring
		// test passes every one of these.
		{"a lookalike suffix", "https://www.hrsd.gov.sa.example.com/x", false},
		{"a lookalike prefix", "https://evil.com/www.hrsd.gov.sa/Labor.pdf", false},
		{"a different ministry", "https://gosi.gov.sa/Labor.pdf", false},

		// The addresses an importer gets pointed at when somebody is trying.
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/", false},
		{"a machine on this network", "https://10.0.0.5:8080/admin", false},
		{"localhost", "https://localhost:5432/", false},

		{"plain http", "http://www.hrsd.gov.sa/Labor.pdf", false},
		{"a file path", "file:///etc/passwd", false},
		{"nonsense", "not a url", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := sameAuthority(c.target, published)
			if c.allowed && err != nil {
				t.Errorf("%s was refused: %v", c.target, err)
			}
			if !c.allowed && err == nil {
				t.Errorf("%s was allowed", c.target)
			}
		})
	}
}

// A rule the pack does not describe cannot be fetched at all.
//
// There is no address to check against, and inventing one would be the whole
// problem in a single line.
func TestARuleWithNoPublishedAddressCannotBeFetched(t *testing.T) {
	if _, ok := SourceFor("XX.MADE.UP"); ok {
		t.Fatal("the source pack describes a rule that does not exist")
	}
}
