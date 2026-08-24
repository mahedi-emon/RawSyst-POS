package sales

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// Turning a finished sale into the UBL document ZATCA hashes.
//
// # Why this exists
//
// The chain records a hash of the invoice, and §3 of the Security Features
// standard defines that hash over the canonicalised UBL document. So a sale
// cannot take a position on the chain until its document exists — there is
// nothing else to hash.
//
// Until now the server allocated a chain position and hashed a placeholder,
// which was honest only because the placeholder said so in its own schema
// version. It is a real document now.
//
// # Why a sale can fail here
//
// The nine CSR fields on an EGS unit are optional at registration on purpose —
// a shop should be able to set a till up today and find its industry
// classification tomorrow. They are NOT optional for issuing an invoice: ZATCA
// needs the seller's legal name and VAT number on the face of it.
//
// So a unit that has not been completed cannot issue, and this says so in words
// naming the field rather than failing somewhere downstream. That is the
// blueprint's A4 rule working as intended: compliance capability is derived
// state, not a toggle.

// seller is the party details an invoice needs, read from the EGS unit.
type seller struct {
	Name      string
	VATNumber string
	Address   zatca.Address
	SchemeID  string
	Scheme    string
}

// readSeller loads the registered party for an EGS unit.
//
// From the unit rather than the company because the unit is what ZATCA
// registered and what its certificate names. A group with several businesses
// has a unit per business, and the invoice must carry the one that signed it.
func readSeller(ctx context.Context, tx pgx.Tx, egsUnitID uuid.UUID) (seller, error) {
	var s seller
	var name, vat, location, storeAddress, storeName, country *string

	err := tx.QueryRow(ctx, `
		SELECT u.csr_organization_name,
		       u.csr_organization_identifier,
		       u.csr_location,
		       st.address, st.name,
		       c.country
		FROM egs_unit u
		JOIN company c ON c.id = u.company_id
		LEFT JOIN store st ON st.id = u.store_id
		WHERE u.id = $1`, egsUnitID).
		Scan(&name, &vat, &location, &storeAddress, &storeName, &country)
	if errors.Is(err, pgx.ErrNoRows) {
		return seller{}, errs.New(errs.CodeNotFound,
			"That e-invoicing unit was not found, so no invoice could be built.")
	}
	if err != nil {
		// Distinguished from "not found" on purpose. Mapping every failure to a
		// missing row hides a broken query behind a plausible business error,
		// which is how a column that does not exist reads as an unregistered
		// till.
		return seller{}, db.Translate(err,
			"The seller details for that e-invoicing unit could not be read.")
	}

	text := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	s.Name = text(name)
	s.VATNumber = text(vat)

	// The unit's registered location first, the store's address second.
	//
	// Neither is STRUCTURED. ZATCA's BR-KSA-63 to BR-KSA-69 ask for street,
	// building number, district, city and postal code as separate fields, and
	// this product stores a single free-text address on a store and a single
	// free-text location on a unit. That is a real gap in the data model rather
	// than something to invent here: splitting a free-text address on commas
	// would put whatever happened to be typed into fields a tax authority
	// reads.
	line := text(location)
	if line == "" {
		line = text(storeAddress)
	}
	s.Address = zatca.Address{
		Street:      line,
		City:        text(storeName),
		CountryCode: "SA",
	}
	if c := text(country); c != "" {
		s.Address.CountryCode = upperTwo(c)
	}

	// Named individually, because "the unit is incomplete" sends somebody
	// hunting through nine fields.
	missing := errs.New(errs.CodeComplianceBlocked,
		"This e-invoicing unit is not complete, so it cannot issue an invoice yet.")
	if s.Name == "" {
		missing.WithField("csr.organization_name",
			"The registered business name is needed on every invoice.")
	}
	if s.VATNumber == "" {
		missing.WithField("csr.organization_identifier",
			"The VAT number is needed on every invoice.")
	}
	if len(missing.Fields) > 0 {
		return seller{}, missing
	}
	return s, nil
}

func upperTwo(s string) string {
	if len(s) < 2 {
		return "SA"
	}
	out := []byte(s[:2])
	for i, b := range out {
		if b >= 'a' && b <= 'z' {
			out[i] = b - 32
		}
	}
	return string(out)
}

// money renders a decimal the way UBL wants it: a plain string, two decimals.
//
// A string end to end. Every amount on an invoice has already been rounded by
// the pricing engine under the documented half-up rule, and re-deriving one
// here from a float would be a second opinion about a number the books have
// already committed to.
func money(d decimal.Decimal) string { return d.StringFixed(2) }

// buildSaleDocument renders a finalised sale as UBL.
func buildSaleDocument(
	ctx context.Context, tx pgx.Tx,
	term Terminal, sale Sale, computed ComputedSale,
	invoiceNumber string, icv int64, pih string,
) ([]byte, error) {
	party, err := readSeller(ctx, tx, term.EGSUnitID)
	if err != nil {
		return nil, err
	}

	// Finalise issues tax invoices. A credit note is a different document with
	// its own required fields — what it corrects, and why — and is raised on
	// the refund path, which knows both.
	docType := zatca.TypeTaxInvoice

	inv := zatca.Invoice{
		Number:   invoiceNumber,
		UUID:     sale.InvoiceUUID,
		IssuedAt: sale.IssuedAt,
		Type:     docType,
		// A till sale is a simplified invoice: KSA-2 positions 1 and 2 = 02.
		// Standard invoices are cleared before issue and are raised from the
		// back office against an identified buyer, not rung up at a counter.
		Transaction: zatca.TransactionCode{Simplified: true},
		ICV:         icv,
		PIH:         pih,

		Supplier: zatca.Party{
			RegistrationName: party.Name,
			VATNumber:        party.VATNumber,
			Address:          party.Address,
		},

		DeliveryDate:     sale.IssuedAt,
		PaymentMeansCode: "10",

		LineExtensionAmount: money(computed.SubtotalNet),
		TaxExclusiveAmount:  money(computed.SubtotalNet),
		TaxInclusiveAmount:  money(computed.TotalInclusive),
		AllowanceTotal:      money(computed.DiscountTotal),
		PayableAmount:       money(computed.TotalInclusive),
		VATTotal:            money(computed.TaxTotal),
	}

	// Lines, and the VAT breakdown, in one pass. The breakdown is grouped by
	// rate because that is what BR-KSA expects: one TaxSubtotal per category
	// and rate, not one per line.
	type bucket struct {
		taxable decimal.Decimal
		tax     decimal.Decimal
		rate    decimal.Decimal
	}
	buckets := map[string]*bucket{}
	order := []string{}

	for _, l := range computed.Lines {
		category := vatCategoryFor(l.TaxTreatment)
		percent := l.TaxRate.Mul(decimal.NewFromInt(100))

		line := zatca.Line{
			ID:             fmt.Sprintf("%d", l.LineNo),
			Name:           l.Description,
			Quantity:       l.Qty.String(),
			UnitCode:       "PCE",
			UnitPrice:      l.UnitPrice.String(),
			NetAmount:      money(l.NetAmount),
			VATCategory:    category,
			VATPercent:     percent.StringFixed(2),
			VATAmount:      money(l.TaxAmount),
			RoundingAmount: money(l.NetAmount.Add(l.TaxAmount)),
		}
		if category != "S" {
			// Anything not standard-rated must say why, or ZATCA refuses it
			// with a rule code a cashier cannot act on.
			line.ExemptionReasonCode = exemptionCodeFor(l.TaxTreatment)
			line.ExemptionReason = exemptionReasonFor(l.TaxTreatment)
		}
		if l.LineDiscount.IsPositive() {
			line.DiscountAmount = money(l.LineDiscount)
			line.DiscountReason = "discount"
		}
		inv.Lines = append(inv.Lines, line)

		key := category + "|" + percent.StringFixed(2)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{rate: percent}
			buckets[key] = b
			order = append(order, key)
		}
		b.taxable = b.taxable.Add(l.NetAmount)
		b.tax = b.tax.Add(l.TaxAmount)
	}

	for _, key := range order {
		b := buckets[key]
		category := key[:1]
		sub := zatca.TaxSubtotal{
			TaxableAmount: money(b.taxable),
			TaxAmount:     money(b.tax),
			Category:      category,
			Percent:       b.rate.StringFixed(2),
		}
		if category != "S" {
			sub.ExemptionReasonCode = exemptionCodeFor(treatmentForCategory(category))
			sub.ExemptionReason = exemptionReasonFor(treatmentForCategory(category))
		}
		inv.TaxSubtotals = append(inv.TaxSubtotals, sub)
	}

	return zatca.BuildInvoiceXML(inv)
}

// vatCategoryFor maps the catalogue's tax treatment onto BT-151.
//
// S standard, Z zero-rated, E exempt, O outside scope. The names differ because
// the catalogue speaks about what a shop sells and ZATCA speaks about how it is
// taxed, and collapsing the two vocabularies would lose the distinction between
// "zero-rated" and "exempt" — which are different on a VAT return.
func vatCategoryFor(treatment string) string {
	switch treatment {
	case "zero_rated":
		return "Z"
	case "exempt":
		return "E"
	case "out_of_scope":
		return "O"
	default:
		return "S"
	}
}

func treatmentForCategory(category string) string {
	switch category {
	case "Z":
		return "zero_rated"
	case "E":
		return "exempt"
	case "O":
		return "out_of_scope"
	default:
		return "standard"
	}
}

// exemptionCodeFor returns the VATEX code ZATCA's list names.
func exemptionCodeFor(treatment string) string {
	switch treatment {
	case "zero_rated":
		return "VATEX-SA-32"
	case "exempt":
		return "VATEX-SA-HEA"
	case "out_of_scope":
		return "VATEX-SA-OOS"
	default:
		return ""
	}
}

func exemptionReasonFor(treatment string) string {
	switch treatment {
	case "zero_rated":
		return "Zero-rated supply"
	case "exempt":
		return "Exempt supply"
	case "out_of_scope":
		return "Outside the scope of VAT"
	default:
		return ""
	}
}

// buildCreditNoteDocument renders a return as a UBL credit note.
//
// A credit note is an invoice in ZATCA's eyes and takes its own position on the
// chain, so it needs a document of its own. Two fields distinguish it and both
// are mandatory: what it corrects, and why.
func buildCreditNoteDocument(
	ctx context.Context, tx pgx.Tx,
	term Terminal, ret Return, original originalInvoice, computed ComputedReturn,
	creditNoteNumber string, icv int64, pih string,
) ([]byte, error) {
	party, err := readSeller(ctx, tx, term.EGSUnitID)
	if err != nil {
		return nil, err
	}

	// The number of the invoice being corrected, which BR-KSA requires the note
	// to name. Read here rather than carried on originalInvoice because it is
	// needed only for this.
	var correctedNumber string
	if err := tx.QueryRow(ctx,
		`SELECT human_number FROM sales_invoice WHERE id = $1`, original.id).
		Scan(&correctedNumber); err != nil {
		return nil, errs.New(errs.CodeNotFound,
			"The invoice this note corrects could not be read.")
	}

	// A note against a STANDARD invoice is itself standard, and a standard
	// document must name its buyer — BR-KSA requires the legal name and the
	// 15-digit VAT number. A simplified note needs neither.
	standard := original.docType == "standard"
	var buyer zatca.Party
	if standard {
		if original.customerID == nil {
			return nil, errs.New(errs.CodeComplianceBlocked,
				"A credit note against a tax invoice must name the buyer, and "+
					"that invoice has no customer on it.")
		}
		var name, vat, address *string
		if err := tx.QueryRow(ctx,
			`SELECT name, vat_number, address FROM customer WHERE id = $1`,
			*original.customerID).Scan(&name, &vat, &address); err != nil {
			return nil, db.Translate(err,
				"The customer on the invoice this note corrects could not be read.")
		}
		text := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		buyer = zatca.Party{
			RegistrationName: text(name),
			VATNumber:        text(vat),
			Address: zatca.Address{
				Street:      text(address),
				CountryCode: party.Address.CountryCode,
			},
		}
	}

	reason := ret.Reason
	if reason == "" {
		// BR-KSA refuses a note with no reason, and "returned" is what happened
		// rather than an invention: the caller reached this function through
		// the returns path.
		reason = "Goods returned"
	}

	inv := zatca.Invoice{
		Number:      creditNoteNumber,
		UUID:        ret.CreditNoteUUID,
		IssuedAt:    ret.IssuedAt,
		Type:        zatca.TypeCreditNote,
		Transaction: zatca.TransactionCode{Simplified: !standard},
		ICV:         icv,
		PIH:         pih,

		BillingReference: correctedNumber,
		InstructionNote:  reason,

		Supplier: zatca.Party{
			RegistrationName: party.Name,
			VATNumber:        party.VATNumber,
			Address:          party.Address,
		},
		Customer: buyer,

		DeliveryDate:     ret.IssuedAt,
		PaymentMeansCode: "10",

		LineExtensionAmount: money(computed.SubtotalNet),
		TaxExclusiveAmount:  money(computed.SubtotalNet),
		TaxInclusiveAmount:  money(computed.TotalInclusive),
		AllowanceTotal:      money(computed.DiscountTotal),
		PayableAmount:       money(computed.TotalInclusive),
		VATTotal:            money(computed.TaxTotal),
	}

	type bucket struct {
		taxable decimal.Decimal
		tax     decimal.Decimal
		rate    decimal.Decimal
	}
	buckets := map[string]*bucket{}
	order := []string{}

	for _, l := range computed.Lines {
		category := vatCategoryFor(l.TaxTreatment)
		percent := l.TaxRate.Mul(decimal.NewFromInt(100))

		line := zatca.Line{
			ID:             fmt.Sprintf("%d", l.LineNo),
			Name:           l.Description,
			Quantity:       l.Qty.String(),
			UnitCode:       "PCE",
			UnitPrice:      l.UnitPrice.String(),
			NetAmount:      money(l.NetAmount),
			VATCategory:    category,
			VATPercent:     percent.StringFixed(2),
			VATAmount:      money(l.TaxAmount),
			RoundingAmount: money(l.NetAmount.Add(l.TaxAmount)),
		}
		if category != "S" {
			line.ExemptionReasonCode = exemptionCodeFor(l.TaxTreatment)
			line.ExemptionReason = exemptionReasonFor(l.TaxTreatment)
		}
		inv.Lines = append(inv.Lines, line)

		key := category + "|" + percent.StringFixed(2)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{rate: percent}
			buckets[key] = b
			order = append(order, key)
		}
		b.taxable = b.taxable.Add(l.NetAmount)
		b.tax = b.tax.Add(l.TaxAmount)
	}

	for _, key := range order {
		b := buckets[key]
		category := key[:1]
		sub := zatca.TaxSubtotal{
			TaxableAmount: money(b.taxable),
			TaxAmount:     money(b.tax),
			Category:      category,
			Percent:       b.rate.StringFixed(2),
		}
		if category != "S" {
			sub.ExemptionReasonCode = exemptionCodeFor(treatmentForCategory(category))
			sub.ExemptionReason = exemptionReasonFor(treatmentForCategory(category))
		}
		inv.TaxSubtotals = append(inv.TaxSubtotals, sub)
	}

	return zatca.BuildInvoiceXML(inv)
}
