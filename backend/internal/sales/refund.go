package sales

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/loyalty"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// A return is nine things happening at once, and blueprint C14 lists them
// because the ones that get forgotten are always the same ones.
//
//	1. inventory restored — quantity AND value
//	2. revenue reversed
//	3. output tax reversed
//	4. cost of sale reversed
//	5. the refund settled
//	6. loyalty reversed
//	7. commission reversed
//	8. a credit note issued, linked to the original
//	9. the journal posted, with its audit record
//
// Effects 6 and 7 are the ones C14 calls easily forgotten, and they are:
// nothing in the sale itself points at them, so a return written from the
// invoice alone silently leaves a customer holding points for goods they
// handed back. They are reported here as outstanding rather than quietly
// omitted — loyalty and commission are Phase 2 modules, and a return must say
// what it has not done.

// Refund is one way money went back to the customer.
type Refund struct {
	Method    string
	Amount    decimal.Decimal
	Reference string

	// ReversesTenderID points at the payment being refunded. Card money must go
	// back the way it came — refunding a card sale in cash is how a shop is
	// used to launder money, and most acquirer agreements forbid it.
	ReversesTenderID *uuid.UUID
}

// Return is a return awaiting processing.
type Return struct {
	// CreditNoteUUID is assigned on the device, like an invoice's, so a synced
	// retry is recognised rather than refunding the customer twice.
	CreditNoteUUID uuid.UUID

	OriginalInvoiceID uuid.UUID
	Requests          []ReturnRequest
	Refunds           []Refund

	IssuedAt time.Time
	Reason   string

	CashierID *uuid.UUID

	// ApprovedBy is required where the company's rules demand it. Blueprint C14
	// leaves the threshold to configuration; what is not optional is recording
	// who allowed it.
	ApprovedBy *uuid.UUID
}

// Refunded is what the till gets back.
type Refunded struct {
	CreditNoteID uuid.UUID
	// CreditNoteNumber is what the note is CALLED — "CN-MAIN-2026-000004".
	//
	// It was claimed and written and then thrown away, so the till told the
	// cashier "Refunded. Credit note 7d1ddd35-7151-42c9-8160-33de7e99bf05",
	// which is the primary key. Nobody reads that to a customer, nobody finds
	// it on a statement, and it is not the number printed on the note itself.
	// Found by photographing the screen after a refund.
	CreditNoteNumber string
	Link             zatca.Link
	Computed         ComputedReturn

	Reversal accounting.Result
	COGS     accounting.Result

	// PointsReversed is what the customer lost by handing the goods back, so a
	// till can say so rather than leaving them to find out next time.
	PointsReversed int

	// Effects records which of C14's nine this return carried out. Loyalty and
	// commission stay false until those modules exist, and Outstanding names
	// them, so nobody mistakes silence for completeness.
	Effects     ReturnEffects
	Outstanding []string

	AlreadyRefunded bool
}

// ProcessReturn reverses a sale, atomically.
//
// The same reasoning as a sale, in reverse. Stock, credit note, chain position
// and journal go together or not at all: a credit note with no accounting entry
// overstates revenue forever, and stock restored without a credit note is stock
// a shop can sell twice.
func (s *Service) ProcessReturn(
	ctx context.Context, tx pgx.Tx, term Terminal, ret Return,
) (Refunded, error) {
	if existing, found, err := s.alreadyRefunded(ctx, tx, term, ret.CreditNoteUUID); err != nil {
		return Refunded{}, err
	} else if found {
		return existing, nil
	}

	original, err := s.originalInvoice(ctx, tx, term, ret.OriginalInvoiceID)
	if err != nil {
		return Refunded{}, err
	}

	originals, err := s.returnableLines(ctx, tx, ret.OriginalInvoiceID)
	if err != nil {
		return Refunded{}, err
	}

	computed, err := ComputeReturn(originals, ret.Requests)
	if err != nil {
		return Refunded{}, err
	}

	// The refund must settle the credit note exactly. Refunding less leaves the
	// customer owed money nothing records; refunding more takes money out of the
	// till against a document that does not justify it.
	amounts := make([]decimal.Decimal, len(ret.Refunds))
	for i, r := range ret.Refunds {
		amounts[i] = r.Amount
	}
	if err := ValidateRefunds(computed.TotalInclusive, amounts); err != nil {
		return Refunded{}, err
	}

	// Taking a refund off the account only means something if there is an
	// account. Allowing it against a cash sale would credit the receivable
	// control with nobody's balance behind it, and C9.3's tie-out would fail by
	// exactly that amount.
	if original.customerID == nil {
		for _, r := range ret.Refunds {
			if r.Method == "customer_due" {
				return Refunded{}, errs.New(errs.CodeInvalidInput,
					"That sale was not on account, so nothing can be taken off "+
						"an account. Refund to cash, to the original card, or as store credit.")
			}
		}
	}

	// 1. Inventory restored — quantity AND value. The value is exactly the cost
	//    being reversed, so the valuation and the Inventory account move
	//    together.
	for _, l := range computed.Lines {
		if !l.Qty.IsPositive() {
			continue
		}
		if err := inventory.Restore(ctx, tx, inventory.Restoration{
			TenantID: term.TenantID, CompanyID: term.CompanyID,
			VariantID: l.VariantID, WarehouseID: term.WarehouseID,
			Qty: l.Qty, Value: l.COGSAmount,
			Reason: "return", SourceType: "sales_invoice",
			DeviceID: term.DeviceID, Note: ret.Reason,
		}); err != nil {
			return Refunded{}, err
		}
	}

	// 8. The credit note, linked to what it corrects.
	creditNoteID, creditNoteNumber, err := s.writeCreditNote(ctx, tx, term, ret, original, computed)
	if err != nil {
		return Refunded{}, err
	}

	// A credit note is an invoice in ZATCA's eyes and takes its own position on
	// the chain (E1). Skipping it would break the counter's continuity.
	//
	// Reserve, build, hash — the same three steps a sale takes, and for the same
	// reason: the document carries its own ICV and PIH, and the hash is over the
	// document.
	icv, pih, err := s.chain.Reserve(ctx, tx, term.EGSUnitID)
	if err != nil {
		return Refunded{}, err
	}
	document, err := buildCreditNoteDocument(ctx, tx, term, ret, original, computed,
		creditNoteNumber, icv, pih)
	if err != nil {
		return Refunded{}, err
	}
	link, err := s.chain.LinkFor(ctx, term.EGSUnitID, icv, pih, document)
	if err != nil {
		return Refunded{}, err
	}
	if err := s.chain.Record(ctx, tx, creditNoteID, term.TenantID, link); err != nil {
		return Refunded{}, err
	}

	// 5. The refund settled.
	for i, r := range ret.Refunds {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_refund
			  (tenant_id, credit_note_id, refund_no, method, amount, reference,
			   reverses_tender_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			term.TenantID, creditNoteID, i+1, r.Method, r.Amount,
			nullText(r.Reference), r.ReversesTenderID); err != nil {
			return Refunded{}, db.Translate(err,
				"That refund could not be recorded.")
		}
	}

	// 5, continued. A refund the customer took as store credit has to land on
	// their wallet, not only in account 2300: the control account and the
	// subsidiary ledger behind it are one figure, and crediting one without the
	// other is how a customer is told they have nothing.
	if err := s.creditRefunds(ctx, tx, term, ret, original, creditNoteID); err != nil {
		return Refunded{}, err
	}

	// 6. Loyalty reversed. C14 calls this one of the two that get forgotten,
	//    and until the scheme existed it genuinely was: a customer could buy,
	//    earn, return, and keep the points. Points come off in proportion to
	//    what was handed back.
	pointsReversed, err := s.reverseLoyalty(ctx, tx, term, ret, original, computed)
	if err != nil {
		return Refunded{}, err
	}

	// 2, 3, 4 and 9. The journal.
	reversal, cogs, err := s.postReturn(ctx, tx, term, ret, computed, creditNoteID)
	if err != nil {
		return Refunded{}, err
	}

	effects := ReturnEffects{
		InventoryRestored: true,
		RevenueReversed:   true,
		OutputTaxReversed: true,
		COGSReversed:      true,
		RefundSettled:     true,
		CreditNoteIssued:  true,
		JournalPosted:     true,
		LoyaltyReversed:   true,
		// Commission is still to come. Left false deliberately, and named in
		// Outstanding, so a return never claims to be complete when it is not.
	}

	return Refunded{
		CreditNoteID: creditNoteID, CreditNoteNumber: creditNoteNumber,
		Link: link, Computed: computed,
		Reversal: reversal, COGS: cogs,
		PointsReversed: pointsReversed,
		Effects:        effects, Outstanding: effects.Missing(),
	}, nil
}

// creditRefunds puts a store-credit refund on the customer's wallet.
//
// Only the wallet: a refund is not put back on a gift card, because the card is
// in the customer's pocket and the shop has no way to know it is the same one.
// A refund to store credit against a sale with no customer is refused earlier
// than this — there is nowhere for it to go.
func (s *Service) creditRefunds(
	ctx context.Context, tx pgx.Tx, term Terminal, ret Return,
	original originalInvoice, creditNoteID uuid.UUID,
) error {
	for _, r := range ret.Refunds {
		if r.Method != "store_credit" || !r.Amount.IsPositive() {
			continue
		}
		if original.customerID == nil {
			return errs.New(errs.CodeInvalidInput,
				"Store credit belongs to somebody. That sale was anonymous, so "+
					"refund it the way it was paid.")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO store_credit_entry
			  (tenant_id, company_id, customer_id, amount, currency, reason,
			   invoice_id, note, created_by)
			VALUES ($1,$2,$3,$4,$5,'refunded',$6,$7,$8)`,
			term.TenantID, term.CompanyID, *original.customerID, r.Amount,
			original.currency, creditNoteID, nullText(ret.Reason),
			ret.CashierID); err != nil {
			return db.Translate(err, "That refund could not be credited.")
		}
	}
	return nil
}

// reverseLoyalty takes back the points a returned sale earned.
//
// In proportion to what came back, rounded UP: a customer who returns half a
// purchase loses half the points, and the half-point goes to the shop rather
// than to the customer. Never more than the points that sale actually earned,
// and never more than the customer still holds — a customer who has already
// spent them is not put into debt by handing goods back.
func (s *Service) reverseLoyalty(
	ctx context.Context, tx pgx.Tx, term Terminal, ret Return,
	original originalInvoice, computed ComputedReturn,
) (int, error) {
	if original.customerID == nil || !computed.TotalInclusive.IsPositive() {
		return 0, nil
	}

	var earned int
	var spend decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(points), 0)::int, coalesce(sum(spend), 0)
		FROM loyalty_entry
		WHERE invoice_id = $1 AND reason = 'earned'`, original.id).
		Scan(&earned, &spend)
	if err != nil {
		return 0, err
	}
	if earned <= 0 || !spend.IsPositive() {
		return 0, nil
	}

	share := computed.TotalInclusive.Div(spend)
	if share.GreaterThan(decimal.NewFromInt(1)) {
		share = decimal.NewFromInt(1)
	}
	take := share.Mul(decimal.NewFromInt(int64(earned))).Ceil().IntPart()
	if take <= 0 {
		return 0, nil
	}

	held, err := loyalty.Balance(ctx, tx, *original.customerID)
	if err != nil {
		return 0, err
	}
	if int64(held) < take {
		take = int64(held)
	}
	if take <= 0 {
		return 0, nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO loyalty_entry
		  (tenant_id, company_id, customer_id, points, reason, invoice_id,
		   created_by)
		VALUES ($1,$2,$3,$4,'reversed',$5,$6)`,
		term.TenantID, term.CompanyID, *original.customerID, -take,
		original.id, ret.CashierID); err != nil {
		return 0, db.Translate(err, "Those points could not be taken back.")
	}
	return int(take), nil
}

// alreadyRefunded recognises a return the device has sent before.
func (s *Service) alreadyRefunded(
	ctx context.Context, tx pgx.Tx, term Terminal, creditNoteUUID uuid.UUID,
) (Refunded, bool, error) {
	var out Refunded
	err := tx.QueryRow(ctx, `
		SELECT i.id, z.icv, z.pih, z.invoice_hash, z.schema_version
		FROM sales_invoice i
		JOIN zatca_invoice z ON z.invoice_id = i.id
		WHERE i.tenant_id = $1 AND i.uuid = $2`,
		term.TenantID, creditNoteUUID).
		Scan(&out.CreditNoteID, &out.Link.ICV, &out.Link.PIH,
			&out.Link.InvoiceHash, &out.Link.SchemaVersion)

	if errors.Is(err, pgx.ErrNoRows) {
		return Refunded{}, false, nil
	}
	if err != nil {
		return Refunded{}, false, err
	}

	out.Link.EGSUnitID = term.EGSUnitID
	out.AlreadyRefunded = true
	return out, true, nil
}

type originalInvoice struct {
	id       uuid.UUID
	docType  string
	currency string
	fxRate   decimal.Decimal

	// customerID is carried onto the credit note so a return appears in the
	// customer's history, and so a refund taken off the account reduces the
	// balance of the customer who owes it rather than nobody's.
	customerID *uuid.UUID
}

func (s *Service) originalInvoice(
	ctx context.Context, tx pgx.Tx, term Terminal, id uuid.UUID,
) (originalInvoice, error) {
	var out originalInvoice
	err := tx.QueryRow(ctx, `
		SELECT id, doc_type, currency, fx_rate, customer_id FROM sales_invoice
		WHERE id = $1 AND tenant_id = $2`, id, term.TenantID).
		Scan(&out.id, &out.docType, &out.currency, &out.fxRate, &out.customerID)

	if errors.Is(err, pgx.ErrNoRows) {
		// Under row-level security another tenant's invoice reads as absent,
		// which is the right answer: its existence is not this caller's
		// business.
		return originalInvoice{}, errs.New(errs.CodeNotFound,
			"That invoice was not found, so nothing can be returned against it.")
	}
	if err != nil {
		return originalInvoice{}, err
	}

	if out.docType != "standard" && out.docType != "simplified" {
		// Returning against a credit note would let a refund be refunded.
		return originalInvoice{}, errs.New(errs.CodeInvalidInput,
			"That document is not a sale, so nothing can be returned against it.")
	}
	return out, nil
}

// returnableLines reads what is left to return on the original.
//
// It reads through the returnable_lines() function so the application and the
// database trigger that enforces the same limit cannot disagree about what has
// already gone back.
func (s *Service) returnableLines(
	ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID,
) ([]OriginalLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT line_id, line_no, variant_id, description,
		       qty_sold, qty_returned, unit_price, tax_treatment, tax_rate,
		       net_amount, tax_amount, line_discount, invoice_discount_alloc,
		       cogs_amount,
		       net_returned, tax_returned, discount_alloc_returned, cogs_returned
		FROM returnable_lines($1)`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OriginalLine
	for rows.Next() {
		var o OriginalLine
		// A line can be free text with no variant behind it — a delivery charge,
		// a one-off adjustment — and those return without moving stock.
		var variantID *uuid.UUID
		if err := rows.Scan(
			&o.LineID, &o.LineNo, &variantID, &o.Description,
			&o.QtySold, &o.QtyReturned, &o.UnitPrice, &o.TaxTreatment, &o.TaxRate,
			&o.NetAmount, &o.TaxAmount, &o.LineDiscount, &o.InvoiceDiscountAlloc,
			&o.COGSAmount,
			&o.NetReturned, &o.TaxReturned, &o.DiscountAllocReturned,
			&o.COGSReturned); err != nil {
			return nil, err
		}
		if variantID != nil {
			o.VariantID = *variantID
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errs.New(errs.CodeNotFound,
			"That invoice has no lines to return.")
	}
	return out, nil
}

// writeCreditNote records the credit note and its lines.
//
// Lines carry NEGATIVE quantities and point at the line each reverses, which is
// what the database trigger uses to refuse a return larger than the original —
// including across several partial returns that individually look reasonable.
func (s *Service) writeCreditNote(
	ctx context.Context, tx pgx.Tx, term Terminal, ret Return,
	original originalInvoice, computed ComputedReturn,
) (uuid.UUID, string, error) {
	humanNumber, err := claimHumanNumber(ctx, tx, term.StoreID, ret.IssuedAt, "credit_note")
	if err != nil {
		return uuid.Nil, "", err
	}

	// A credit note follows the ROUTE OF THE INVOICE IT CORRECTS. A note
	// against a B2B invoice must be cleared before issue like the invoice was,
	// not merely reported — sending it down the B2C route would put it through
	// a process ZATCA does not accept for that document type.
	state := initialState(original.docType)

	var creditNoteID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO sales_invoice
		  (tenant_id, company_id, store_id, device_id, uuid, doc_type,
		   parent_invoice_id, issue_date, issued_at, currency, fx_rate,
		   subtotal_net, discount_total, tax_total, total_inclusive, state,
		   cash_session_id, human_number, customer_id)
		VALUES ($1,$2,$3,$4,$5,'credit_note',$6,$7,$8,$9,$10,$11,$12,$13,$14,
		        $15,$16,$17,$18)
		RETURNING id`,
		term.TenantID, term.CompanyID, term.StoreID, term.DeviceID,
		ret.CreditNoteUUID, original.id, ret.IssuedAt, ret.IssuedAt,
		original.currency, original.fxRate,
		computed.SubtotalNet, computed.DiscountTotal,
		computed.TaxTotal, computed.TotalInclusive, state,
		term.CashSessionID, humanNumber, original.customerID).Scan(&creditNoteID)
	if err != nil {
		return uuid.Nil, "", db.Translate(err, "That credit note could not be issued.")
	}

	for i, l := range computed.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_invoice_line
			  (tenant_id, invoice_id, line_no, variant_id, description, qty,
			   unit_price, line_discount, invoice_discount_alloc, tax_treatment,
			   tax_rate, tax_amount, net_amount, gross_amount, cogs_amount,
			   reverses_line_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$9,$10,$11,$12,$13,$14,$15)`,
			term.TenantID, creditNoteID, i+1, l.VariantID, l.Description,
			l.Qty.Neg(), l.UnitPrice, l.InvoiceDiscountAlloc, l.TaxTreatment,
			l.TaxRate, l.TaxAmount, l.NetAmount, l.GrossAmount, l.COGSAmount,
			l.OriginalLineID); err != nil {
			return uuid.Nil, "", db.Translate(err,
				"A line on that credit note could not be recorded.")
		}
	}

	return creditNoteID, humanNumber, nil
}

// postReturn writes the two reversing entries.
//
// Reversals are posted as their own entries with their own rule keys, not as
// edits to the sale's. Posted history is immutable: a correction is always a
// new entry pointing at what it undoes, so the original sale remains
// explainable exactly as it was reported.
func (s *Service) postReturn(
	ctx context.Context, tx pgx.Tx, term Terminal, ret Return,
	computed ComputedReturn, creditNoteID uuid.UUID,
) (reversal, cogs accounting.Result, err error) {
	base := accounting.Entry{
		TenantID: term.TenantID, CompanyID: term.CompanyID,
		Date: ret.IssuedAt, SourceType: "credit_note", SourceID: creditNoteID,
		PostedBy: ret.CashierID, StoreID: &term.StoreID,
	}

	// 2 and 3 — revenue and the output tax on it go back, against whatever the
	// money was refunded through. The shape is in posting_rule; this supplies
	// the figures and the split across refund methods.
	refunds := make(accounting.Group, 0, len(ret.Refunds))
	for _, r := range ret.Refunds {
		refunds = append(refunds, accounting.GroupMember{
			Role: tenderRole(r.Method), Amount: r.Amount, Memo: r.Method,
		})
	}

	reversalEntry := base
	reversalEntry.RuleKey = "return.reversal"
	reversalEntry.Memo = "Return: " + ret.Reason

	// A credit note can legitimately come to nothing, and an entry with nothing
	// in it is not an entry.
	//
	// Two ways it happens. A line sold at no charge coming back — pricing
	// refuses only a NEGATIVE unit price, so a free sample or a giveaway is an
	// ordinary line — and a partial return of a line so cheap that its
	// cumulative credit has not moved a hallala since the last one: a hundred
	// pieces at a net of 0.50 credit 0.01 on the first return and nothing on the
	// second.
	//
	// Posting it fails, because usableLines drops every zero line and what is
	// left is not the two an entry needs. That refusal used to take the whole
	// return down with it — everything above runs in the caller's transaction, so
	// the stock restoration rolled back as well, leaving goods physically in the
	// shop that the records still said were sold, and the retry failed
	// identically. Declining to write an empty journal entry is much the smaller
	// thing.
	//
	// The inclusive total covers the whole rule. Net and tax are each
	// non-negative and sum to it, so a zero total means both are zero; and the
	// refunds must settle the total exactly while each one must be positive, so
	// there are none. Every figure this rule reads is zero.
	if computed.TotalInclusive.IsPositive() {
		reversal, err = accounting.PostByRule(ctx, tx, reversalEntry, term.Country,
			accounting.Transaction{
				Amounts: accounting.Amounts{
					"subtotal_net":    computed.SubtotalNet,
					"tax_total":       computed.TaxTotal,
					"total_inclusive": computed.TotalInclusive,
				},
				Groups: map[string]accounting.Group{"refunds": refunds},
			})
		if err != nil {
			return reversal, cogs, err
		}
	}

	// 4 — the cost of sale comes back into stock. Reached even when nothing was
	// credited, because goods given away still cost the shop what they cost, and
	// value returning to stock has to appear in the ledger or the valuation parts
	// from the Inventory account.
	if !computed.COGSTotal.IsPositive() {
		return reversal, cogs, nil
	}

	cogsEntry := base
	cogsEntry.RuleKey = "return.cogs"
	cogsEntry.Memo = "Cost of goods returned"

	cogs, err = accounting.PostByRule(ctx, tx, cogsEntry, term.Country,
		accounting.Transaction{
			Amounts: accounting.Amounts{"cogs_total": computed.COGSTotal},
		})
	return reversal, cogs, err
}
