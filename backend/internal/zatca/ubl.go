package zatca

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The UBL 2.1 invoice ZATCA reads.
//
// # Provenance
//
// Built against, and checked by, two things:
//
//	ZATCA, "Electronic Invoice XML Implementation Standard to the E-Invoicing
//	resolution", Version 1.2, dated 2023-05-19, 72 pages — the semantic model,
//	the element paths, and 119 business-rule families (BR, BR-CO, BR-S/Z/E/O,
//	BR-KSA, BR-KSA-DEC/CL/EN16931/F).
//
//	ZATCA's own validator, which is a public HTTP service and not, as this
//	registry recorded for months, available only inside the Fatoora SDK:
//	POST https://gw-fatoora.zatca.gov.sa/e-invoicing/developer-portal/
//	     validate-e-invoice/invoice/validate
//
// The validator was found by reading the Integration Sandbox's own runtime
// configuration at sandbox.zatca.gov.sa/env-config.js. It takes the invoice as
// BASE64 of the XML file with Content-Type text/plain and an accepted-language
// header — that shape is taken from the page's own upload code, which does
// readAsDataURL and posts the part after "data:text/xml;base64,". Sending raw
// XML instead returns 500, which is what made it look unusable.
//
// It answers with {"valid":…, "errors":[…], "warnings":[…]}, each entry naming
// a rule: BR_KSA_ERROR / BR-KSA-EN16931-06, QR_CODE_ERROR, SIGNATURE_ERROR.
// That makes it a real oracle rather than a yes/no, and it is what
// TestTheBuiltInvoicePassesZATCAsValidator drives.
//
// # What this builds, and what it deliberately leaves out
//
// BuildInvoiceXML produces the document in its PRE-SIGNATURE form: no
// ext:UBLExtensions, and no QR AdditionalDocumentReference. That is not an
// omission, it is the form the hash is taken over. canonical.go records the
// transform chain from Security Features v1.1 §2.3.3, and two of its three
// XPath removals delete exactly those subtrees before hashing — they are
// written into the document after the hash exists, so hashing them would be
// circular.
//
// So: this file ends where signing begins. Signing needs a CSID, and a CSID
// needs an onboarding call this project cannot yet make. Against the validator
// a document from here reports clean on every business rule and fails only
// SIGNATURE_ERROR and QR_CODE_ERROR, which is precisely the shape of "correct
// but not yet onboarded".

// Namespaces, fixed by the standard.
const (
	nsInvoice = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	nsCAC     = "urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	nsCBC     = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
	nsEXT     = "urn:oasis:names:specification:ubl:schema:xsd:CommonExtensionComponents-2"

	// ProfileIDReporting is BT-23. BR-KSA-EN16931-01: "Business process (BT-23)
	// must be 'reporting:1.0'."
	ProfileIDReporting = "reporting:1.0"

	// CurrencySAR is BT-6. BR-KSA-EN16931-02: the VAT accounting currency "must
	// be 'SAR'".
	CurrencySAR = "SAR"
)

// DocumentTypeCode is UBL's BT-3, from the standard's table at §5.
type DocumentTypeCode string

const (
	TypeTaxInvoice        DocumentTypeCode = "388"
	TypeDebitNote         DocumentTypeCode = "383"
	TypeCreditNote        DocumentTypeCode = "381"
	TypePrepaymentInvoice DocumentTypeCode = "386"
)

// TransactionCode is KSA-2, the seven-digit subtype on InvoiceTypeCode/@name.
//
// The standard's worked examples pin the first two digits — "For Simplified Tax
// Invoice, code is 388 and subtype is 02. ex. <cbc:InvoiceTypeCode
// name="0200000">388</cbc:InvoiceTypeCode>" — and BR-KSA-60 refers to
// "position 1 and 2 = 02" for simplified. The remaining five are flags.
type TransactionCode struct {
	// Simplified selects positions 1-2: "02" when true, "01" when false. A
	// standard invoice is cleared before issue; a simplified one is reported
	// after. Getting this wrong sends the document down the wrong route.
	Simplified bool

	ThirdParty bool // position 3
	Nominal    bool // position 4
	Exports    bool // position 5
	Summary    bool // position 6
	SelfBilled bool // position 7
}

// String renders the seven digits.
func (t TransactionCode) String() string {
	digit := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	head := "01"
	if t.Simplified {
		head = "02"
	}
	return head + digit(t.ThirdParty) + digit(t.Nominal) +
		digit(t.Exports) + digit(t.Summary) + digit(t.SelfBilled)
}

// Address is a postal address. Every field below is required by BR-KSA-63 to
// BR-KSA-69 for the seller; the buyer's is conditional on the document type.
type Address struct {
	Street       string
	BuildingNo   string
	PlotID       string
	Subdivision  string
	City         string
	PostalZone   string
	CountryCode  string
	AdditionalNo string
}

// Party is a seller or buyer.
type Party struct {
	// RegistrationName is BT-27/BT-44, the legal name.
	RegistrationName string

	// VATNumber is BT-31/BT-48, fifteen digits. Mandatory for the seller;
	// mandatory for the buyer on a standard invoice (BR-KSA-14 territory) and
	// omitted on a simplified one.
	VATNumber string

	// SchemeID and SchemeValue carry the "other seller/buyer ID" — CRN, MOM,
	// MLS, SAG, OTH and the rest of the standard's scheme list.
	SchemeID    string
	SchemeValue string

	Address Address
}

// Line is one invoice line.
//
// Every amount is a STRING, and that is a project rule rather than a
// convenience: a float cannot hold 0.1 exactly, and an invoice that is a
// halalah out does not fail loudly, it fails at ZATCA weeks later.
type Line struct {
	ID       string
	Name     string
	Quantity string
	UnitCode string

	// UnitPrice is BT-146. The standard permits more than two decimals here,
	// which is how a per-unit price divides evenly into a rounded line total.
	UnitPrice string

	// NetAmount is BT-131, quantity times price less line allowances.
	NetAmount string

	// VATCategory is BT-151: S standard, Z zero-rated, E exempt, O outside
	// scope. VATPercent is BT-152.
	VATCategory string
	VATPercent  string

	// VATAmount is KSA-11, the tax on this line, and RoundingAmount is KSA-12,
	// the line total including it.
	VATAmount      string
	RoundingAmount string

	// ExemptionReasonCode and ExemptionReason are required by BR-KSA-EN16931-08
	// and its neighbours whenever the category is not S.
	ExemptionReasonCode string
	ExemptionReason     string

	// DiscountAmount is a line-level allowance. Charges at line level are
	// refused: BR-KSA-EN16931-06 states "Charge on price level (BG-29) is not
	// allowed. The value of Indicator should be 'false'."
	DiscountAmount string
	DiscountReason string
}

// TaxSubtotal is one VAT category's totals across the document.
type TaxSubtotal struct {
	TaxableAmount       string
	TaxAmount           string
	Category            string
	Percent             string
	ExemptionReasonCode string
	ExemptionReason     string
}

// Invoice is everything needed to build the document.
type Invoice struct {
	Number      string
	UUID        uuid.UUID
	IssuedAt    time.Time
	Type        DocumentTypeCode
	Transaction TransactionCode

	// ICV is the invoice counter value and PIH the previous invoice hash,
	// base64 SHA-256. BR-KSA-26: "If the invoice contains the previous invoice
	// hash (KSA-13), this hash must be base64 encoded SHA256."
	ICV int64
	PIH string

	Supplier Party
	Customer Party

	// BillingReference is the invoice a credit or debit note corrects, and is
	// mandatory on those (BR-KSA-56 and neighbours).
	BillingReference string

	// InstructionNote is the reason for a credit or debit note.
	InstructionNote string

	DeliveryDate     time.Time
	PaymentMeansCode string

	Lines        []Line
	TaxSubtotals []TaxSubtotal

	LineExtensionAmount string
	TaxExclusiveAmount  string
	TaxInclusiveAmount  string
	AllowanceTotal      string
	PrepaidAmount       string
	PayableAmount       string
	VATTotal            string
}

// --- the marshalled shape ---------------------------------------------------
//
// Written as explicit structs rather than assembled from strings, because UBL
// is order-sensitive: the sequence below IS the schema, and a field in the
// wrong place is an XSD failure rather than a cosmetic difference. Go's
// encoding/xml emits struct fields in declaration order, so the declaration
// order is the specification.
//
// Prefixes are written into the tag names. Go's namespace support would emit
// its own prefixes and redeclare them per element, which produces a valid but
// differently-shaped document — and this document is about to be canonicalised
// and hashed, so its exact bytes matter.

type xAmount struct {
	Currency string `xml:"currencyID,attr"`
	Value    string `xml:",chardata"`
}

type xQuantity struct {
	UnitCode string `xml:"unitCode,attr"`
	Value    string `xml:",chardata"`
}

type xTypeCode struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

type xIDScheme struct {
	SchemeID string `xml:"schemeID,attr,omitempty"`
	Value    string `xml:",chardata"`
}

type xBinaryObject struct {
	MimeCode string `xml:"mimeCode,attr"`
	Value    string `xml:",chardata"`
}

type xCountry struct {
	Code string `xml:"cbc:IdentificationCode"`
}

type xPostalAddress struct {
	Street       string   `xml:"cbc:StreetName,omitempty"`
	AdditionalNo string   `xml:"cbc:AdditionalStreetName,omitempty"`
	BuildingNo   string   `xml:"cbc:BuildingNumber,omitempty"`
	PlotID       string   `xml:"cbc:PlotIdentification,omitempty"`
	Subdivision  string   `xml:"cbc:CitySubdivisionName,omitempty"`
	City         string   `xml:"cbc:CityName,omitempty"`
	PostalZone   string   `xml:"cbc:PostalZone,omitempty"`
	Country      xCountry `xml:"cac:Country"`
}

type xTaxScheme struct {
	ID string `xml:"cbc:ID"`
}

type xPartyTaxScheme struct {
	CompanyID string     `xml:"cbc:CompanyID,omitempty"`
	TaxScheme xTaxScheme `xml:"cac:TaxScheme"`
}

type xPartyLegalEntity struct {
	RegistrationName string `xml:"cbc:RegistrationName"`
}

type xPartyIdentification struct {
	ID xIDScheme `xml:"cbc:ID"`
}

type xParty struct {
	Identification *xPartyIdentification `xml:"cac:PartyIdentification,omitempty"`
	PostalAddress  xPostalAddress        `xml:"cac:PostalAddress"`
	TaxScheme      *xPartyTaxScheme      `xml:"cac:PartyTaxScheme,omitempty"`
	LegalEntity    xPartyLegalEntity     `xml:"cac:PartyLegalEntity"`
}

type xSupplierParty struct {
	Party xParty `xml:"cac:Party"`
}

type xAttachment struct {
	Object xBinaryObject `xml:"cbc:EmbeddedDocumentBinaryObject"`
}

type xAdditionalDocumentReference struct {
	ID         string       `xml:"cbc:ID"`
	UUID       string       `xml:"cbc:UUID,omitempty"`
	Attachment *xAttachment `xml:"cac:Attachment,omitempty"`
}

type xBillingReferenceLine struct {
	ID string `xml:"cbc:ID"`
}

type xBillingReference struct {
	InvoiceRef xBillingReferenceLine `xml:"cac:InvoiceDocumentReference"`
}

type xDelivery struct {
	ActualDeliveryDate string `xml:"cbc:ActualDeliveryDate,omitempty"`
}

type xPaymentMeans struct {
	Code            string `xml:"cbc:PaymentMeansCode"`
	InstructionNote string `xml:"cbc:InstructionNote,omitempty"`
}

type xTaxCategory struct {
	ID                  string     `xml:"cbc:ID"`
	Percent             string     `xml:"cbc:Percent"`
	ExemptionReasonCode string     `xml:"cbc:TaxExemptionReasonCode,omitempty"`
	ExemptionReason     string     `xml:"cbc:TaxExemptionReason,omitempty"`
	TaxScheme           xTaxScheme `xml:"cac:TaxScheme"`
}

type xTaxSubtotal struct {
	TaxableAmount xAmount      `xml:"cbc:TaxableAmount"`
	TaxAmount     xAmount      `xml:"cbc:TaxAmount"`
	TaxCategory   xTaxCategory `xml:"cac:TaxCategory"`
}

type xTaxTotal struct {
	TaxAmount xAmount        `xml:"cbc:TaxAmount"`
	Subtotals []xTaxSubtotal `xml:"cac:TaxSubtotal,omitempty"`
}

type xLineTaxTotal struct {
	TaxAmount      xAmount `xml:"cbc:TaxAmount"`
	RoundingAmount xAmount `xml:"cbc:RoundingAmount"`
}

type xLegalMonetaryTotal struct {
	LineExtensionAmount xAmount `xml:"cbc:LineExtensionAmount"`
	TaxExclusiveAmount  xAmount `xml:"cbc:TaxExclusiveAmount"`
	TaxInclusiveAmount  xAmount `xml:"cbc:TaxInclusiveAmount"`
	AllowanceTotal      xAmount `xml:"cbc:AllowanceTotalAmount"`
	PrepaidAmount       xAmount `xml:"cbc:PrepaidAmount"`
	PayableAmount       xAmount `xml:"cbc:PayableAmount"`
}

type xClassifiedTaxCategory struct {
	ID        string     `xml:"cbc:ID"`
	Percent   string     `xml:"cbc:Percent"`
	TaxScheme xTaxScheme `xml:"cac:TaxScheme"`
}

type xItem struct {
	Name        string                 `xml:"cbc:Name"`
	TaxCategory xClassifiedTaxCategory `xml:"cac:ClassifiedTaxCategory"`
}

type xAllowanceCharge struct {
	ChargeIndicator bool    `xml:"cbc:ChargeIndicator"`
	Reason          string  `xml:"cbc:AllowanceChargeReason,omitempty"`
	Amount          xAmount `xml:"cbc:Amount"`
}

type xPrice struct {
	PriceAmount    xAmount           `xml:"cbc:PriceAmount"`
	AllowanceCharg *xAllowanceCharge `xml:"cac:AllowanceCharge,omitempty"`
}

type xInvoiceLine struct {
	ID                  string        `xml:"cbc:ID"`
	InvoicedQuantity    xQuantity     `xml:"cbc:InvoicedQuantity"`
	LineExtensionAmount xAmount       `xml:"cbc:LineExtensionAmount"`
	TaxTotal            xLineTaxTotal `xml:"cac:TaxTotal"`
	Item                xItem         `xml:"cac:Item"`
	Price               xPrice        `xml:"cac:Price"`
}

type xInvoice struct {
	XMLName xml.Name `xml:"Invoice"`

	Xmlns    string `xml:"xmlns,attr"`
	XmlnsCAC string `xml:"xmlns:cac,attr"`
	XmlnsCBC string `xml:"xmlns:cbc,attr"`
	XmlnsEXT string `xml:"xmlns:ext,attr"`

	ProfileID            string    `xml:"cbc:ProfileID"`
	ID                   string    `xml:"cbc:ID"`
	UUID                 string    `xml:"cbc:UUID"`
	IssueDate            string    `xml:"cbc:IssueDate"`
	IssueTime            string    `xml:"cbc:IssueTime"`
	InvoiceTypeCode      xTypeCode `xml:"cbc:InvoiceTypeCode"`
	DocumentCurrencyCode string    `xml:"cbc:DocumentCurrencyCode"`
	TaxCurrencyCode      string    `xml:"cbc:TaxCurrencyCode"`

	BillingReference *xBillingReference             `xml:"cac:BillingReference,omitempty"`
	AdditionalDocs   []xAdditionalDocumentReference `xml:"cac:AdditionalDocumentReference"`

	Supplier xSupplierParty `xml:"cac:AccountingSupplierParty"`
	Customer xSupplierParty `xml:"cac:AccountingCustomerParty"`

	Delivery     *xDelivery     `xml:"cac:Delivery,omitempty"`
	PaymentMeans *xPaymentMeans `xml:"cac:PaymentMeans,omitempty"`

	TaxTotals   []xTaxTotal         `xml:"cac:TaxTotal"`
	LegalTotal  xLegalMonetaryTotal `xml:"cac:LegalMonetaryTotal"`
	InvoiceLine []xInvoiceLine      `xml:"cac:InvoiceLine"`
}

// BuildInvoiceXML renders the document in its pre-signature form.
func BuildInvoiceXML(in Invoice) ([]byte, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	doc := xInvoice{
		Xmlns:    nsInvoice,
		XmlnsCAC: nsCAC,
		XmlnsCBC: nsCBC,
		XmlnsEXT: nsEXT,

		ProfileID: ProfileIDReporting,
		ID:        in.Number,
		UUID:      in.UUID.String(),
		IssueDate: in.IssuedAt.UTC().Format("2006-01-02"),
		IssueTime: in.IssuedAt.UTC().Format("15:04:05"),
		InvoiceTypeCode: xTypeCode{
			Name:  in.Transaction.String(),
			Value: string(in.Type),
		},
		DocumentCurrencyCode: CurrencySAR,
		TaxCurrencyCode:      CurrencySAR,
	}

	if in.BillingReference != "" {
		doc.BillingReference = &xBillingReference{
			InvoiceRef: xBillingReferenceLine{ID: in.BillingReference},
		}
	}

	// ICV first, then PIH. The QR reference is absent by design — it is written
	// after signing and removed again before hashing.
	doc.AdditionalDocs = []xAdditionalDocumentReference{
		{ID: "ICV", UUID: fmt.Sprintf("%d", in.ICV)},
		{
			ID: "PIH",
			Attachment: &xAttachment{
				Object: xBinaryObject{MimeCode: "text/plain", Value: in.PIH},
			},
		},
	}

	doc.Supplier = xSupplierParty{Party: party(in.Supplier, true)}
	doc.Customer = xSupplierParty{Party: party(in.Customer, false)}

	if !in.DeliveryDate.IsZero() {
		doc.Delivery = &xDelivery{
			ActualDeliveryDate: in.DeliveryDate.UTC().Format("2006-01-02"),
		}
	}
	if in.PaymentMeansCode != "" {
		doc.PaymentMeans = &xPaymentMeans{
			Code:            in.PaymentMeansCode,
			InstructionNote: in.InstructionNote,
		}
	}

	// Two TaxTotal elements. The first carries only the total; the second
	// carries it again with the per-category breakdown. That duplication is the
	// standard's, not ours — BR-KSA-DEC-02 and the EN 16931 model both expect
	// the summary element to be present on its own.
	total := xAmount{Currency: CurrencySAR, Value: in.VATTotal}
	breakdown := xTaxTotal{TaxAmount: total}
	for _, s := range in.TaxSubtotals {
		breakdown.Subtotals = append(breakdown.Subtotals, xTaxSubtotal{
			TaxableAmount: xAmount{Currency: CurrencySAR, Value: s.TaxableAmount},
			TaxAmount:     xAmount{Currency: CurrencySAR, Value: s.TaxAmount},
			TaxCategory: xTaxCategory{
				ID:                  s.Category,
				Percent:             s.Percent,
				ExemptionReasonCode: s.ExemptionReasonCode,
				ExemptionReason:     s.ExemptionReason,
				TaxScheme:           xTaxScheme{ID: "VAT"},
			},
		})
	}
	doc.TaxTotals = []xTaxTotal{{TaxAmount: total}, breakdown}

	doc.LegalTotal = xLegalMonetaryTotal{
		LineExtensionAmount: xAmount{Currency: CurrencySAR, Value: in.LineExtensionAmount},
		TaxExclusiveAmount:  xAmount{Currency: CurrencySAR, Value: in.TaxExclusiveAmount},
		TaxInclusiveAmount:  xAmount{Currency: CurrencySAR, Value: in.TaxInclusiveAmount},
		AllowanceTotal:      xAmount{Currency: CurrencySAR, Value: orZero(in.AllowanceTotal)},
		PrepaidAmount:       xAmount{Currency: CurrencySAR, Value: orZero(in.PrepaidAmount)},
		PayableAmount:       xAmount{Currency: CurrencySAR, Value: in.PayableAmount},
	}

	for _, l := range in.Lines {
		line := xInvoiceLine{
			ID:                  l.ID,
			InvoicedQuantity:    xQuantity{UnitCode: l.UnitCode, Value: l.Quantity},
			LineExtensionAmount: xAmount{Currency: CurrencySAR, Value: l.NetAmount},
			TaxTotal: xLineTaxTotal{
				TaxAmount:      xAmount{Currency: CurrencySAR, Value: l.VATAmount},
				RoundingAmount: xAmount{Currency: CurrencySAR, Value: l.RoundingAmount},
			},
			Item: xItem{
				Name: l.Name,
				TaxCategory: xClassifiedTaxCategory{
					ID:        l.VATCategory,
					Percent:   l.VATPercent,
					TaxScheme: xTaxScheme{ID: "VAT"},
				},
			},
			Price: xPrice{
				PriceAmount: xAmount{Currency: CurrencySAR, Value: l.UnitPrice},
			},
		}

		// ChargeIndicator is always false. BR-KSA-EN16931-06: "Charge on price
		// level (BG-29) is not allowed. The value of Indicator should be
		// 'false'." A line discount is an allowance, never a charge.
		if l.DiscountAmount != "" {
			line.Price.AllowanceCharg = &xAllowanceCharge{
				ChargeIndicator: false,
				Reason:          l.DiscountReason,
				Amount:          xAmount{Currency: CurrencySAR, Value: l.DiscountAmount},
			}
		}

		doc.InvoiceLine = append(doc.InvoiceLine, line)
	}

	body, err := xml.MarshalIndent(doc, "", "    ")
	if err != nil {
		return nil, errs.New(errs.CodeInternal,
			"The invoice document could not be rendered.")
	}

	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.Write(body)
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func party(p Party, isSupplier bool) xParty {
	out := xParty{
		PostalAddress: xPostalAddress{
			Street:       p.Address.Street,
			AdditionalNo: p.Address.AdditionalNo,
			BuildingNo:   p.Address.BuildingNo,
			PlotID:       p.Address.PlotID,
			Subdivision:  p.Address.Subdivision,
			City:         p.Address.City,
			PostalZone:   p.Address.PostalZone,
			Country:      xCountry{Code: p.Address.CountryCode},
		},
		LegalEntity: xPartyLegalEntity{RegistrationName: p.RegistrationName},
	}
	if p.SchemeValue != "" {
		out.Identification = &xPartyIdentification{
			ID: xIDScheme{SchemeID: p.SchemeID, Value: p.SchemeValue},
		}
	}
	// The seller always carries a PartyTaxScheme; the buyer only when it has a
	// VAT number, which a simplified invoice's buyer does not.
	if p.VATNumber != "" || isSupplier {
		out.TaxScheme = &xPartyTaxScheme{
			CompanyID: p.VATNumber,
			TaxScheme: xTaxScheme{ID: "VAT"},
		}
	}
	return out
}

func orZero(v string) string {
	if strings.TrimSpace(v) == "" {
		return "0.00"
	}
	return v
}

var (
	decimalPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	vatPattern     = regexp.MustCompile(`^3\d{13}3$`)
)

// Validate refuses a document that would be rejected, where the rule is stated.
//
// This is not a reimplementation of all 119 rule families — the validator is
// the authority on those, and duplicating them here would create a second
// opinion that drifts. What it catches is the subset that is structural: a
// missing mandatory element, a malformed amount, a VAT number that is not a
// VAT number. Those produce failures whose messages do not say which field.
func (in Invoice) Validate() error {
	e := errs.New(errs.CodeInvalidInput, "This invoice cannot be built for ZATCA.")

	if strings.TrimSpace(in.Number) == "" {
		e.WithField("number", "The invoice needs its number.")
	}
	if in.UUID == uuid.Nil {
		e.WithField("uuid", "The invoice needs its UUID.")
	}
	if in.IssuedAt.IsZero() {
		e.WithField("issued_at", "The invoice needs its issue date and time.")
	}
	switch in.Type {
	case TypeTaxInvoice, TypeDebitNote, TypeCreditNote, TypePrepaymentInvoice:
	default:
		e.WithField("type", "That is not a document type ZATCA recognises.")
	}
	if in.ICV <= 0 {
		e.WithField("icv", "The invoice counter value starts at 1.")
	}
	if strings.TrimSpace(in.PIH) == "" {
		e.WithField("pih", "The previous invoice hash is required on every document.")
	}

	// BR-KSA-56 and neighbours: a note must say what it corrects.
	if in.Type == TypeCreditNote || in.Type == TypeDebitNote {
		if strings.TrimSpace(in.BillingReference) == "" {
			e.WithField("billing_reference",
				"A credit or debit note must reference the invoice it corrects.")
		}
		if strings.TrimSpace(in.InstructionNote) == "" {
			e.WithField("instruction_note",
				"A credit or debit note must give the reason for it.")
		}
	}

	if !vatPattern.MatchString(in.Supplier.VATNumber) {
		e.WithField("supplier.vat_number",
			"The seller's VAT number must be 15 digits starting and ending with 3.")
	}
	if strings.TrimSpace(in.Supplier.RegistrationName) == "" {
		e.WithField("supplier.registration_name", "The seller's legal name is required.")
	}
	if in.Supplier.Address.CountryCode == "" {
		e.WithField("supplier.address", "The seller's address is required.")
	}

	// A standard invoice identifies its buyer; a simplified one need not.
	if !in.Transaction.Simplified {
		if !vatPattern.MatchString(in.Customer.VATNumber) {
			e.WithField("customer.vat_number",
				"A standard tax invoice must carry the buyer's 15-digit VAT number.")
		}
		if strings.TrimSpace(in.Customer.RegistrationName) == "" {
			e.WithField("customer.registration_name",
				"A standard tax invoice must carry the buyer's legal name.")
		}
	}

	if len(in.Lines) == 0 {
		e.WithField("lines", "An invoice needs at least one line.")
	}
	if len(in.TaxSubtotals) == 0 {
		e.WithField("tax_subtotals", "An invoice needs at least one VAT breakdown.")
	}

	amounts := map[string]string{
		"line_extension_amount": in.LineExtensionAmount,
		"tax_exclusive_amount":  in.TaxExclusiveAmount,
		"tax_inclusive_amount":  in.TaxInclusiveAmount,
		"payable_amount":        in.PayableAmount,
		"vat_total":             in.VATTotal,
	}
	for name, v := range amounts {
		if !decimalPattern.MatchString(strings.TrimSpace(v)) {
			e.WithField(name, "This must be a decimal amount.")
		}
	}

	for i, l := range in.Lines {
		where := fmt.Sprintf("lines.%d", i)
		if strings.TrimSpace(l.Name) == "" {
			e.WithField(where+".name", "Every line needs the item name.")
		}
		if strings.TrimSpace(l.UnitCode) == "" {
			e.WithField(where+".unit_code", "Every line needs its unit of measure.")
		}
		switch l.VATCategory {
		case "S", "Z", "E", "O":
		default:
			e.WithField(where+".vat_category",
				"The VAT category must be S, Z, E or O.")
		}
		// BR-KSA-EN16931-08 and neighbours: anything but standard-rated has to
		// say why, and a blank reason is refused at ZATCA with a rule code the
		// cashier cannot act on.
		if l.VATCategory != "S" && strings.TrimSpace(l.ExemptionReason) == "" &&
			strings.TrimSpace(l.ExemptionReasonCode) == "" {
			e.WithField(where+".exemption_reason",
				"A line that is not standard-rated must give the exemption reason.")
		}
		for field, v := range map[string]string{
			".quantity":        l.Quantity,
			".unit_price":      l.UnitPrice,
			".net_amount":      l.NetAmount,
			".vat_amount":      l.VATAmount,
			".rounding_amount": l.RoundingAmount,
		} {
			if !decimalPattern.MatchString(strings.TrimSpace(v)) {
				e.WithField(where+field, "This must be a decimal amount.")
			}
		}
	}

	if len(e.Fields) > 0 {
		return e
	}
	return nil
}
