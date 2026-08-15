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
	Link         zatca.Link
	Computed     ComputedReturn

	Reversal accounting.Result
	COGS     accounting.Result

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
	creditNoteID, err := s.writeCreditNote(ctx, tx, term, ret, original, computed)
	if err != nil {
		return Refunded{}, err
	}

	// A credit note is an invoice in ZATCA's eyes and takes its own position on
	// the chain (E1). Skipping it would break the counter's continuity.
	link, err := s.chain.Allocate(ctx, tx, term.EGSUnitID,
		zatca.Document{InvoiceUUID: ret.CreditNoteUUID})
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
		// Loyalty and commission are Phase 2. Left false deliberately, and
		// named in Outstanding, so a return never claims to be complete when it
		// is not.
	}

	return Refunded{
		CreditNoteID: creditNoteID, Link: link, Computed: computed,
		Reversal: reversal, COGS: cogs,
		Effects: effects, Outstanding: effects.Missing(),
	}, nil
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
}

func (s *Service) originalInvoice(
	ctx context.Context, tx pgx.Tx, term Terminal, id uuid.UUID,
) (originalInvoice, error) {
	var out originalInvoice
	err := tx.QueryRow(ctx, `
		SELECT id, doc_type, currency, fx_rate FROM sales_invoice
		WHERE id = $1 AND tenant_id = $2`, id, term.TenantID).
		Scan(&out.id, &out.docType, &out.currency, &out.fxRate)

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
) (uuid.UUID, error) {
	var creditNoteID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO sales_invoice
		  (tenant_id, company_id, store_id, device_id, uuid, doc_type,
		   parent_invoice_id, issue_date, issued_at, currency, fx_rate,
		   subtotal_net, discount_total, tax_total, total_inclusive, state)
		VALUES ($1,$2,$3,$4,$5,'credit_note',$6,$7,$8,$9,$10,$11,$12,$13,$14,
		        'signed_pending_report')
		RETURNING id`,
		term.TenantID, term.CompanyID, term.StoreID, term.DeviceID,
		ret.CreditNoteUUID, original.id, ret.IssuedAt, ret.IssuedAt,
		original.currency, original.fxRate,
		computed.SubtotalNet, computed.DiscountTotal,
		computed.TaxTotal, computed.TotalInclusive).Scan(&creditNoteID)
	if err != nil {
		return uuid.Nil, db.Translate(err, "That credit note could not be issued.")
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
			return uuid.Nil, db.Translate(err,
				"A line on that credit note could not be recorded.")
		}
	}

	return creditNoteID, nil
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
		PostedBy: ret.CashierID,
	}

	// 2 and 3 — revenue and the output tax on it go back, against whatever the
	// money was refunded through.
	lines := make([]accounting.Line, 0, len(ret.Refunds)+2)
	lines = append(lines,
		accounting.Line{Role: "sales_revenue", Side: accounting.Debit,
			Amount: computed.SubtotalNet, StoreID: &term.StoreID},
		accounting.Line{Role: "output_vat", Side: accounting.Debit,
			Amount: computed.TaxTotal, StoreID: &term.StoreID},
	)
	for _, r := range ret.Refunds {
		lines = append(lines, accounting.Line{
			Role: tenderRole(r.Method), Side: accounting.Credit, Amount: r.Amount,
			StoreID: &term.StoreID, Memo: r.Method,
		})
	}

	reversalEntry := base
	reversalEntry.RuleKey = "return.reversal"
	reversalEntry.Memo = "Return: " + ret.Reason
	reversalEntry.Lines = lines

	reversal, err = accounting.Post(ctx, tx, reversalEntry)
	if err != nil {
		return reversal, cogs, err
	}

	// 4 — the cost of sale comes back into stock.
	if !computed.COGSTotal.IsPositive() {
		return reversal, cogs, nil
	}

	cogsEntry := base
	cogsEntry.RuleKey = "return.cogs"
	cogsEntry.Memo = "Cost of goods returned"
	cogsEntry.Lines = []accounting.Line{
		{Role: "inventory", Side: accounting.Debit, Amount: computed.COGSTotal,
			StoreID: &term.StoreID},
		{Role: "cogs", Side: accounting.Credit, Amount: computed.COGSTotal,
			StoreID: &term.StoreID},
	}

	cogs, err = accounting.Post(ctx, tx, cogsEntry)
	return reversal, cogs, err
}
