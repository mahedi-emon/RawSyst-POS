package zatca

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The document is checked two ways.
//
// Offline, against the structure the standard fixes — element order, the
// removals the hash depends on, the rules whose failure messages do not name a
// field. Online, against ZATCA's own validator, which is the only thing that
// can settle the other 119 rule families and is a public HTTP service.
//
// The online test is opt-in. It needs the network and it posts a synthetic
// invoice to a government endpoint, neither of which belongs in a default
// `go test ./...`. Set ZATCA_VALIDATOR=1 to run it.

func sampleInvoice() Invoice {
	return Invoice{
		Number:      "SME00021",
		UUID:        uuid.MustParse("3cf5ee18-ee25-44ea-a444-2c37ba7f28be"),
		IssuedAt:    time.Date(2022, 3, 13, 14, 40, 40, 0, time.UTC),
		Type:        TypeTaxInvoice,
		Transaction: TransactionCode{Simplified: true},
		ICV:         10,
		PIH:         "NWZlY2ViNjZmZmM4NmYzOGQ5NTI3ODZjNmQ2OTZjNzljMmRiYzIzOWRkNGU5MWI0NjcyOWQ3M2EyN2ZiNTdlOQ==",
		Supplier: Party{
			RegistrationName: "Ahmed Mohamed AL Ahmady",
			VATNumber:        "301121971500003",
			SchemeID:         "CRN",
			SchemeValue:      "1010010000",
			Address: Address{
				Street: "Prince Sultan", BuildingNo: "2322", PlotID: "2223",
				Subdivision: "Al-Murabba", City: "Riyadh", PostalZone: "23333",
				CountryCode: "SA",
			},
		},
		Customer: Party{
			RegistrationName: "Fatoora Store",
			Address: Address{
				Street: "Al Amir Mohammed", BuildingNo: "3893",
				Subdivision: "Al-Murabba", City: "Riyadh", PostalZone: "23333",
				CountryCode: "SA",
			},
		},
		DeliveryDate:     time.Date(2022, 3, 13, 0, 0, 0, 0, time.UTC),
		PaymentMeansCode: "10",
		Lines: []Line{{
			ID: "1", Name: "dates", Quantity: "44.000000", UnitCode: "PCE",
			UnitPrice: "21.9545454545", NetAmount: "966.00",
			VATCategory: "S", VATPercent: "15.00",
			VATAmount: "144.90", RoundingAmount: "1110.90",
		}},
		TaxSubtotals: []TaxSubtotal{{
			TaxableAmount: "966.00", TaxAmount: "144.90",
			Category: "S", Percent: "15.00",
		}},
		LineExtensionAmount: "966.00",
		TaxExclusiveAmount:  "966.00",
		TaxInclusiveAmount:  "1110.90",
		PayableAmount:       "1110.90",
		VATTotal:            "144.90",
	}
}

func standardInvoice() Invoice {
	in := sampleInvoice()
	in.Transaction = TransactionCode{Simplified: false}
	in.Customer.VATNumber = "311111111111113"
	in.Customer.SchemeID = "CRN"
	in.Customer.SchemeValue = "2050000000"
	return in
}

func TestTheDocumentIsWellFormedAndCarriesTheStandardsFixedValues(t *testing.T) {
	doc, err := BuildInvoiceXML(sampleInvoice())
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	var probe any
	if err := xml.Unmarshal(doc, &probe); err != nil {
		t.Fatalf("the document is not well-formed XML: %v", err)
	}

	for _, want := range []string{
		// BR-KSA-EN16931-01 and -02 fix these two outright.
		"<cbc:ProfileID>reporting:1.0</cbc:ProfileID>",
		"<cbc:TaxCurrencyCode>SAR</cbc:TaxCurrencyCode>",
		// KSA-2: simplified is positions 1 and 2 = 02.
		`<cbc:InvoiceTypeCode name="0200000">388</cbc:InvoiceTypeCode>`,
		"<cbc:ID>ICV</cbc:ID>",
		"<cbc:ID>PIH</cbc:ID>",
	} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("the document is missing %s", want)
		}
	}
}

// The hash is taken over the document WITHOUT these, so the builder must not
// produce them. canonical.go's transform chain removes ext:UBLExtensions and
// the QR AdditionalDocumentReference before hashing; emitting them here and
// stripping them later would mean two places had to agree forever.
func TestTheDocumentOmitsWhatTheHashTransformRemoves(t *testing.T) {
	doc, err := BuildInvoiceXML(sampleInvoice())
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	if bytes.Contains(doc, []byte("UBLExtensions")) {
		t.Error("the pre-signature document carries ext:UBLExtensions")
	}
	if bytes.Contains(doc, []byte("<cbc:ID>QR</cbc:ID>")) {
		t.Error("the pre-signature document carries the QR document reference")
	}
}

// UBL is order-sensitive: the sequence is the schema.
func TestTheElementsRunInTheOrderTheSchemaRequires(t *testing.T) {
	doc, err := BuildInvoiceXML(standardInvoice())
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	text := string(doc)

	order := []string{
		"cbc:ProfileID", "cbc:ID", "cbc:UUID", "cbc:IssueDate", "cbc:IssueTime",
		"cbc:InvoiceTypeCode", "cbc:DocumentCurrencyCode", "cbc:TaxCurrencyCode",
		"cac:AdditionalDocumentReference", "cac:AccountingSupplierParty",
		"cac:AccountingCustomerParty", "cac:Delivery", "cac:PaymentMeans",
		"cac:TaxTotal", "cac:LegalMonetaryTotal", "cac:InvoiceLine",
	}
	at := -1
	for _, element := range order {
		i := strings.Index(text, "<"+element)
		if i < 0 {
			t.Fatalf("%s is missing entirely", element)
		}
		if i < at {
			t.Errorf("%s appears out of sequence", element)
		}
		at = i
	}
}

// BR-KSA-EN16931-06: "Charge on price level (BG-29) is not allowed. The value
// of Indicator should be 'false'." The validator rejected a true here, so a
// line discount must never be emitted as a charge.
func TestALineDiscountIsNeverEmittedAsACharge(t *testing.T) {
	in := sampleInvoice()
	in.Lines[0].DiscountAmount = "10.00"
	in.Lines[0].DiscountReason = "loyalty"

	doc, err := BuildInvoiceXML(in)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if !bytes.Contains(doc, []byte("<cbc:ChargeIndicator>false</cbc:ChargeIndicator>")) {
		t.Error("the line allowance is not marked as an allowance")
	}
	if bytes.Contains(doc, []byte("<cbc:ChargeIndicator>true</cbc:ChargeIndicator>")) {
		t.Error("a charge indicator of true would be refused by BR-KSA-EN16931-06")
	}
}

func TestTheTransactionCodeFollowsKSA2(t *testing.T) {
	cases := []struct {
		code TransactionCode
		want string
	}{
		{TransactionCode{Simplified: false}, "0100000"},
		{TransactionCode{Simplified: true}, "0200000"},
		{TransactionCode{Simplified: true, ThirdParty: true}, "0210000"},
		{TransactionCode{Simplified: false, Nominal: true}, "0101000"},
		{TransactionCode{Simplified: false, Exports: true}, "0100100"},
		{TransactionCode{Simplified: true, Summary: true}, "0200010"},
		{TransactionCode{Simplified: false, SelfBilled: true}, "0100001"},
	}
	for _, c := range cases {
		if got := c.code.String(); got != c.want {
			t.Errorf("%+v rendered %q, want %q", c.code, got, c.want)
		}
	}
}

func TestTheBuilderRefusesDocumentsZATCAWouldReject(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Invoice)
	}{
		{"no invoice number", func(i *Invoice) { i.Number = "" }},
		{"no UUID", func(i *Invoice) { i.UUID = uuid.Nil }},
		{"an ICV of zero", func(i *Invoice) { i.ICV = 0 }},
		{"no previous invoice hash", func(i *Invoice) { i.PIH = "" }},
		{"a seller VAT number that is not 15 digits", func(i *Invoice) { i.Supplier.VATNumber = "30112197150000" }},
		{"a seller VAT number not starting with 3", func(i *Invoice) { i.Supplier.VATNumber = "401121971500003" }},
		{"no seller name", func(i *Invoice) { i.Supplier.RegistrationName = "" }},
		{"no lines", func(i *Invoice) { i.Lines = nil }},
		{"no VAT breakdown", func(i *Invoice) { i.TaxSubtotals = nil }},
		{"an unknown document type", func(i *Invoice) { i.Type = "999" }},
		{"a VAT category outside S/Z/E/O", func(i *Invoice) { i.Lines[0].VATCategory = "X" }},
		{"a zero-rated line with no reason", func(i *Invoice) { i.Lines[0].VATCategory = "Z" }},
		{"an amount that is not a decimal", func(i *Invoice) { i.PayableAmount = "eleven" }},
		{"a line amount that is not a decimal", func(i *Invoice) { i.Lines[0].NetAmount = "" }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := sampleInvoice()
			c.mutate(&in)
			if _, err := BuildInvoiceXML(in); err == nil {
				t.Errorf("%s was accepted", c.name)
			}
		})
	}
}

// A standard invoice must identify its buyer; a simplified one need not.
func TestOnlyAStandardInvoiceMustNameItsBuyer(t *testing.T) {
	simplified := sampleInvoice()
	if _, err := BuildInvoiceXML(simplified); err != nil {
		t.Errorf("a simplified invoice with an unnamed buyer was refused: %v", err)
	}

	standard := standardInvoice()
	standard.Customer.VATNumber = ""
	if _, err := BuildInvoiceXML(standard); err == nil {
		t.Error("a standard invoice with no buyer VAT number was accepted")
	}
}

// A credit or debit note must say what it corrects and why.
func TestANoteMustReferenceWhatItCorrects(t *testing.T) {
	for _, kind := range []DocumentTypeCode{TypeCreditNote, TypeDebitNote} {
		in := sampleInvoice()
		in.Type = kind
		if _, err := BuildInvoiceXML(in); err == nil {
			t.Errorf("a %s with no billing reference was accepted", kind)
		}

		in.BillingReference = "SME00020"
		in.InstructionNote = "goods returned"
		doc, err := BuildInvoiceXML(in)
		if err != nil {
			t.Fatalf("a complete %s was refused: %v", kind, err)
		}
		if !bytes.Contains(doc, []byte("<cbc:ID>SME00020</cbc:ID>")) {
			t.Errorf("the %s does not reference the invoice it corrects", kind)
		}
	}
}

// --- ZATCA's own validator --------------------------------------------------

type validatorResult struct {
	Valid  bool `json:"valid"`
	Errors []struct {
		Category string `json:"category"`
		Code     string `json:"code"`
		Message  string `json:"message"`
	} `json:"errors"`
	Warnings []struct {
		Category string `json:"category"`
		Code     string `json:"code"`
		Message  string `json:"message"`
	} `json:"warnings"`
}

// ValidatorURL is ZATCA's public invoice validator.
//
// Found in the Integration Sandbox's own runtime configuration at
// sandbox.zatca.gov.sa/env-config.js, as REACT_APP_API_URL. It is not behind
// the Fatoora SDK's login, which is what this project believed for months.
const ValidatorURL = "https://gw-fatoora.zatca.gov.sa/e-invoicing/developer-portal/" +
	"validate-e-invoice/invoice/validate"

func validateWithZATCA(t *testing.T, doc []byte) validatorResult {
	t.Helper()

	// The body is BASE64 of the file, posted as text/plain. That is what the
	// sandbox page itself sends: it reads the upload with readAsDataURL and
	// posts the part after "data:text/xml;base64,". Raw XML returns 500.
	body := strings.NewReader(base64.StdEncoding.EncodeToString(doc))

	req, err := http.NewRequest(http.MethodPost, ValidatorURL, body)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("accepted-language", "en")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("ZATCA's validator could not be reached: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the validator answered %d: %s", resp.StatusCode, raw)
	}

	var out validatorResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the validator's answer could not be read: %v\n%s", err, raw)
	}
	return out
}

// A standard invoice is complete without a stamp: ZATCA applies its own on
// clearance. So this one must come back valid with nothing against it.
func TestTheStandardInvoicePassesZATCAsValidator(t *testing.T) {
	if os.Getenv("ZATCA_VALIDATOR") == "" {
		t.Skip("set ZATCA_VALIDATOR=1 to check against ZATCA's live validator")
	}

	doc, err := BuildInvoiceXML(standardInvoice())
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	result := validateWithZATCA(t, doc)

	for _, e := range result.Errors {
		t.Errorf("validator error: %s %s — %s", e.Category, e.Code, e.Message)
	}
	for _, w := range result.Warnings {
		t.Errorf("validator warning: %s %s — %s", w.Category, w.Code, w.Message)
	}
	if !result.Valid {
		t.Error("ZATCA's validator refused a standard invoice this package built")
	}
}

// A simplified invoice is signed and carries a QR before it is reported, and
// neither is possible without a CSID. So it must come back clean on every
// BUSINESS rule and fail only on the two things onboarding would supply.
func TestTheSimplifiedInvoiceFailsZATCAsValidatorOnlyForWantOfACertificate(t *testing.T) {
	if os.Getenv("ZATCA_VALIDATOR") == "" {
		t.Skip("set ZATCA_VALIDATOR=1 to check against ZATCA's live validator")
	}

	doc, err := BuildInvoiceXML(sampleInvoice())
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	result := validateWithZATCA(t, doc)

	// Anything outside these two categories is a real defect in what we build.
	for _, e := range result.Errors {
		switch e.Category {
		case "SIGNATURE_ERROR", "QR_CODE_ERROR":
		default:
			t.Errorf("business-rule failure: %s %s — %s", e.Category, e.Code, e.Message)
		}
	}
	for _, w := range result.Warnings {
		switch w.Code {
		case "BR-KSA-27", "BR-KSA-60": // "must contain a QR", "must be stamped"
		default:
			t.Errorf("unexpected warning: %s %s — %s", w.Category, w.Code, w.Message)
		}
	}
}

// The address this product can supply today, and what ZATCA thinks of it.
//
// `store` holds a single free-text `address` column and `egs_unit` a single
// `csr_location`. ZATCA wants five separate fields. Asked directly, the
// validator accepts the document — valid=true — and returns three warnings:
//
//	BR-KSA-09  seller address must contain street name (BT-35), building
//	           number (KSA-17), postal code (BT-38), city (BT-37),
//	           district (KSA-3), country code (BT-40)
//	BR-KSA-37  the seller address building number must contain 4 digits
//	BR-KSA-66  seller postal code (BT-38) must be 5 digits
//
// So this is a data-model gap rather than a mapping bug, and it is recorded
// here in the validator's own words instead of in a comment somebody has to
// take on trust. SA.ZATCA.API_RESPONSES already notes that ZATCA warns such
// warnings "might become rejections in the future", which is what makes this
// worth fixing rather than tolerating.
//
// Splitting the free text on commas is NOT the fix: it would put whatever
// happened to be typed into fields a tax authority reads.
func TestTheReducedAddressIsAcceptedButWarned(t *testing.T) {
	if os.Getenv("ZATCA_VALIDATOR") == "" {
		t.Skip("set ZATCA_VALIDATOR=1 to check against ZATCA's live validator")
	}

	in := standardInvoice()
	in.Supplier.Address = Address{
		Street:      "Prince Sultan Road, Al-Murabba",
		City:        "Main Branch",
		CountryCode: "SA",
	}
	doc, err := BuildInvoiceXML(in)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	result := validateWithZATCA(t, doc)

	// Accepted, so a shop can trade while the address model is completed.
	for _, e := range result.Errors {
		t.Errorf("unexpected error: %s %s — %s", e.Category, e.Code, e.Message)
	}
	if !result.Valid {
		t.Error("a reduced address made the document invalid, not merely warned")
	}

	// And every warning is about the address, not about anything else we build.
	known := map[string]bool{"BR-KSA-09": true, "BR-KSA-37": true, "BR-KSA-66": true}
	for _, w := range result.Warnings {
		if !known[w.Code] {
			t.Errorf("a warning that is not about the address: %s — %s", w.Code, w.Message)
		}
	}
	if len(result.Warnings) == 0 {
		t.Log("ZATCA no longer warns about the reduced address; the gap may be closed")
	}
}

// The complete Saudi National Address, and ZATCA's verdict on it.
//
// BR-KSA-09 names six fields; BR-KSA-37 wants the building number to be four
// digits and BR-KSA-66 wants the postal code to be five. This is the same
// document as the test above with those six filled in, and it must come back
// with NOTHING against it — not merely valid, but unwarned.
//
// That distinction is the whole point of migration 0063. A document that is
// valid-with-warnings is accepted today and, on ZATCA's own notice that
// warnings "might become rejections in the future", is a liability rather than
// a pass.
func TestTheCompleteAddressClearsEveryAddressWarning(t *testing.T) {
	if os.Getenv("ZATCA_VALIDATOR") == "" {
		t.Skip("set ZATCA_VALIDATOR=1 to check against ZATCA's live validator")
	}

	in := standardInvoice()
	in.Supplier.Address = Address{
		Street:       "Prince Sultan Road",
		BuildingNo:   "2322", // KSA-17, four digits
		AdditionalNo: "2223",
		Subdivision:  "Al-Murabba", // KSA-3, the district
		City:         "Riyadh",
		PostalZone:   "23333", // BT-38, five digits
		CountryCode:  "SA",
	}
	doc, err := BuildInvoiceXML(in)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	result := validateWithZATCA(t, doc)

	for _, e := range result.Errors {
		t.Errorf("error: %s %s — %s", e.Category, e.Code, e.Message)
	}
	for _, w := range result.Warnings {
		switch w.Code {
		case "BR-KSA-09", "BR-KSA-37", "BR-KSA-66":
			t.Errorf("the address warning migration 0063 exists to fix is still "+
				"raised: %s — %s", w.Code, w.Message)
		default:
			t.Errorf("an unexpected warning: %s — %s", w.Code, w.Message)
		}
	}
	if !result.Valid {
		t.Error("a complete address did not produce a valid document")
	}
}
