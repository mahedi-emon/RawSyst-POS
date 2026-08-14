package catalog

import (
	"strings"
	"testing"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

var (
	saudi = TaxRules{
		Country: "sa", Model: TaxModelVAT, InputTaxRecoverable: true,
		Treatments: []string{"standard", "zero_rated", "exempt", "out_of_scope",
			"export", "reverse_charge", "import"},
	}
	bangladesh = TaxRules{
		Country: "bd", Model: TaxModelVAT, InputTaxRecoverable: true,
		Treatments: []string{"standard", "reduced", "zero_rated", "exempt",
			"out_of_scope", "export"},
	}
	usa = TaxRules{
		Country: "us", Model: TaxModelSalesTax, InputTaxRecoverable: false,
		Treatments: []string{"taxable", "non_taxable", "exempt"},
	}
)

// The three markets accept different treatment names. A value that is correct
// in one is meaningless in another, and accepting it silently would put a
// wrong treatment on a tax invoice.
func TestTreatmentsAreCountrySpecific(t *testing.T) {
	cases := []struct {
		name      string
		rules     TaxRules
		treatment string
		valid     bool
	}{
		{"saudi standard", saudi, "standard", true},
		{"saudi reverse charge", saudi, "reverse_charge", true},
		{"saudi does not use the US name", saudi, "taxable", false},
		{"saudi has no reduced rate", saudi, "reduced", false},

		{"bangladesh reduced", bangladesh, "reduced", true},
		{"bangladesh standard", bangladesh, "standard", true},
		{"bangladesh has no reverse charge in this list", bangladesh, "reverse_charge", false},

		{"usa taxable", usa, "taxable", true},
		{"usa non taxable", usa, "non_taxable", true},
		{"usa does not use VAT vocabulary", usa, "zero_rated", false},
		{"usa does not use standard", usa, "standard", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTreatment(tc.rules, tc.treatment)
			if tc.valid && err != nil {
				t.Fatalf("%q should be valid in %s: %v",
					tc.treatment, tc.rules.Country, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("%q was accepted in %s but is not a treatment there",
					tc.treatment, tc.rules.Country)
			}
		})
	}
}

// A rejection must name the allowed values. Someone who typed a Saudi term into
// a US catalogue needs to know what the US list actually is.
func TestRejectionListsWhatIsAllowed(t *testing.T) {
	err := ValidateTreatment(usa, "zero_rated")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	msg := err.Error()
	for _, allowed := range usa.Treatments {
		if !strings.Contains(msg, allowed) {
			t.Errorf("the rejection does not mention the allowed value %q: %s",
				allowed, msg)
		}
	}
	if errs.CodeOf(err) != errs.CodeInvalidInput {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeInvalidInput)
	}
}

// The distinction that breaks naive designs: US sales tax has no input tax to
// reclaim, so any "output minus input" calculation is meaningless there.
func TestInputTaxRecoverabilityDiffersByModel(t *testing.T) {
	if !saudi.InputTaxRecoverable {
		t.Error("Saudi VAT input tax is reclaimable")
	}
	if !bangladesh.InputTaxRecoverable {
		t.Error("Bangladesh VAT input tax is reclaimable")
	}
	if usa.InputTaxRecoverable {
		t.Error("US sales tax has no input tax to reclaim; treating it as " +
			"recoverable would invent a refund that does not exist")
	}
}

// A US rate depends on state, county and city. Resolving one from the country
// alone would be wrong almost everywhere.
func TestOnlySalesTaxNeedsAJurisdiction(t *testing.T) {
	if saudi.RequiresJurisdiction() {
		t.Error("Saudi VAT is set nationally")
	}
	if bangladesh.RequiresJurisdiction() {
		t.Error("Bangladesh VAT is set nationally")
	}
	if !usa.RequiresJurisdiction() {
		t.Error("US sales tax is set per jurisdiction; resolving a rate from the " +
			"country alone would be wrong almost everywhere")
	}
}

func TestExemptionReasonRequirement(t *testing.T) {
	cases := []struct {
		rules     TaxRules
		treatment string
		required  bool
	}{
		// ZATCA requires the reason for any non-standard treatment on the invoice.
		{saudi, "standard", false},
		{saudi, "zero_rated", true},
		{saudi, "exempt", true},
		{saudi, "export", true},

		{bangladesh, "standard", false},
		{bangladesh, "reduced", false},
		{bangladesh, "exempt", true},

		// A US exempt sale needs a certificate held against the customer.
		{usa, "taxable", false},
		{usa, "non_taxable", false},
		{usa, "exempt", true},
	}

	for _, tc := range cases {
		got := RequiresExemptionReason(tc.rules, tc.treatment)
		if got != tc.required {
			t.Errorf("%s/%s: reason required = %v, want %v",
				tc.rules.Country, tc.treatment, got, tc.required)
		}
	}
}

// An unknown model must fail loudly rather than defaulting. Guessing is the
// mistake the explicit field exists to prevent.
func TestUnknownModelRequiresAReason(t *testing.T) {
	unknown := TaxRules{Country: "xx", Model: "something_new", Treatments: []string{"a"}}
	if !RequiresExemptionReason(unknown, "a") {
		t.Fatal("an unrecognised tax model should require a reason rather than " +
			"assuming none is needed")
	}
}

func TestTreatmentKeyNamesTheRegime(t *testing.T) {
	cases := map[string]string{
		"sa": "SA.VAT.TAX_TREATMENTS",
		"bd": "BD.VAT.TAX_TREATMENTS",
		"us": "US.SALESTAX.TAX_TREATMENTS",
	}
	for country, want := range cases {
		if got := treatmentKeyFor(country); got != want {
			t.Errorf("treatmentKeyFor(%q) = %q, want %q", country, got, want)
		}
	}
}

func TestEmptyTreatmentIsRejected(t *testing.T) {
	if err := ValidateTreatment(saudi, ""); err == nil {
		t.Fatal("an empty tax treatment was accepted")
	}
}
