package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// The POS surface.
//
// Money and quantities cross this boundary as STRINGS, never JSON numbers
// (`07-api-conventions.md` §2). JavaScript's number is a float64 and cannot
// hold 0.15 exactly; a till that parsed a rate and multiplied would eventually
// disagree with the server's numeric, and the disagreement would show up as a
// VAT return that is out by a few hallalas with no traceable cause.
//
// Note what these handlers do NOT accept: company, store, EGS unit. Those are
// resolved from the registered device, because a till that could name its own
// company could post into another company's books, and a till that could name
// its own EGS unit could take a position on another terminal's ZATCA chain.
// Row-level security would not catch either — the rows belong to the same
// tenant. See sales.resolveTerminal.

// --- request shapes -----------------------------------------------------

type posLineRequest struct {
	VariantID     string `json:"variant_id"`
	Description   string `json:"description"`
	DescriptionAr string `json:"description_ar"`
	Qty           string `json:"qty"`
	UnitPrice     string `json:"unit_price"`
	LineDiscount  string `json:"line_discount"`
	TaxTreatment  string `json:"tax_treatment"`
}

type posTenderRequest struct {
	Method    string `json:"method"`
	Amount    string `json:"amount"`
	Reference string `json:"reference"`
}

type createSaleRequest struct {
	// InvoiceUUID is assigned ON THE DEVICE before any network call, which is
	// what makes a retry after a lost response safe: the same sale carries the
	// same id and is recognised rather than rung up twice.
	InvoiceUUID string `json:"invoice_uuid"`

	DocType          string `json:"doc_type"`
	IssuedAt         string `json:"issued_at"`
	WarehouseID      string `json:"warehouse_id"`
	PricesIncludeTax *bool  `json:"prices_include_tax"`
	InvoiceDiscount  string `json:"invoice_discount"`

	Lines   []posLineRequest   `json:"lines"`
	Tenders []posTenderRequest `json:"tenders"`
}

// --- POST /api/v1/pos/sales ---------------------------------------------

func (s *Server) handleCreateSale(w http.ResponseWriter, r *http.Request) {
	var req createSaleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())

	invoiceUUID, err := parseUUID(req.InvoiceUUID, "invoice_uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseOptionalUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	issuedAt, err := parseIssuedAt(req.IssuedAt)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	sale, err := s.buildSale(r, req, invoiceUUID, issuedAt, a)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.sales.RingUp(r.Context(), a.TenantID, a.DeviceID, sale, warehouseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// A recognised retry returns the ORIGINAL outcome, so a till that lost the
	// first response gets the same invoice number and the same chain position
	// rather than a second sale.
	status := http.StatusCreated
	if out.AlreadyRung {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}

	httpx.JSON(w, status, saleResponse(out))
}

func (s *Server) buildSale(
	r *http.Request, req createSaleRequest,
	invoiceUUID uuid.UUID, issuedAt time.Time, a actor.Actor,
) (sales.Sale, error) {
	if len(req.Lines) == 0 {
		return sales.Sale{}, errs.New(errs.CodeInvalidInput,
			"A sale needs at least one item.")
	}

	docType := req.DocType
	if docType == "" {
		// The Saudi retail norm. A B2B sale must say so, because it follows the
		// clearance route rather than reporting and cannot be defaulted into.
		docType = "simplified"
	}

	// TaxRate and Rules are deliberately left unset. The service resolves them
	// from the Regulatory Rule Registry against the company's country at the
	// transaction date — a till does not get to state a legal value, and a
	// terminal running last year's build must not keep charging last year's
	// rate.
	in := sales.SaleInput{
		PricesIncludeTax: req.PricesIncludeTax == nil || *req.PricesIncludeTax,
	}
	refs := make([]sales.SaleLineRef, 0, len(req.Lines))

	for i, l := range req.Lines {
		variantID, err := parseUUID(l.VariantID, "lines[].variant_id")
		if err != nil {
			return sales.Sale{}, err
		}
		qty, err := parseAmount(l.Qty, "lines[].qty", i)
		if err != nil {
			return sales.Sale{}, err
		}
		unitPrice, err := parseAmount(l.UnitPrice, "lines[].unit_price", i)
		if err != nil {
			return sales.Sale{}, err
		}
		lineDiscount, err := parseOptionalAmount(l.LineDiscount, "lines[].line_discount", i)
		if err != nil {
			return sales.Sale{}, err
		}

		in.Lines = append(in.Lines, sales.LineInput{
			VariantID:     l.VariantID,
			Description:   l.Description,
			DescriptionAr: l.DescriptionAr,
			Qty:           qty,
			UnitPrice:     unitPrice,
			LineDiscount:  lineDiscount,
			TaxTreatment:  l.TaxTreatment,
		})
		refs = append(refs, sales.SaleLineRef{VariantID: variantID})
	}

	invoiceDiscount, err := parseOptionalAmount(req.InvoiceDiscount, "invoice_discount", -1)
	if err != nil {
		return sales.Sale{}, err
	}
	in.InvoiceDiscount = invoiceDiscount

	tenders := make([]sales.Tender, 0, len(req.Tenders))
	for i, t := range req.Tenders {
		amount, err := parseAmount(t.Amount, "tenders[].amount", i)
		if err != nil {
			return sales.Sale{}, err
		}
		tenders = append(tenders, sales.Tender{
			Method: t.Method, Amount: amount, Reference: t.Reference,
		})
	}

	userID := a.UserID
	return sales.Sale{
		InvoiceUUID: invoiceUUID,
		DocType:     docType,
		IssuedAt:    issuedAt,
		// Currency is left unset for the same reason as the tax rate: it is the
		// company's base currency, not the till's choice.
		Input:     in,
		Lines:     refs,
		Tenders:   tenders,
		CashierID: &userID,
		// StockPolicy is deliberately left unset. Whether a till may sell past
		// what is on hand belongs to the company (C13), and the service reads
		// it from there — a handler that supplied a default would let a request
		// decide, and the safe default the column already carries would never
		// take effect.
	}, nil
}

// --- POST /api/v1/pos/returns -------------------------------------------

type returnLineRequest struct {
	LineID string `json:"line_id"`
	Qty    string `json:"qty"`
}

type refundRequest struct {
	Method           string `json:"method"`
	Amount           string `json:"amount"`
	Reference        string `json:"reference"`
	ReversesTenderID string `json:"reverses_tender_id"`
}

type createReturnRequest struct {
	CreditNoteUUID    string              `json:"credit_note_uuid"`
	OriginalInvoiceID string              `json:"original_invoice_id"`
	IssuedAt          string              `json:"issued_at"`
	WarehouseID       string              `json:"warehouse_id"`
	Reason            string              `json:"reason"`
	Lines             []returnLineRequest `json:"lines"`
	Refunds           []refundRequest     `json:"refunds"`
}

func (s *Server) handleCreateReturn(w http.ResponseWriter, r *http.Request) {
	var req createReturnRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())

	creditNoteUUID, err := parseUUID(req.CreditNoteUUID, "credit_note_uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	originalID, err := parseUUID(req.OriginalInvoiceID, "original_invoice_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseOptionalUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	issuedAt, err := parseIssuedAt(req.IssuedAt)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// C14 requires a reason on every return. Unexplained returns are how refund
	// fraud is concealed, and the reason is what makes an exception report
	// mean anything.
	if len(req.Reason) < 3 {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A return needs a reason. It appears on the credit note and in the "+
				"returns report."))
		return
	}

	requests := make([]sales.ReturnRequest, 0, len(req.Lines))
	for i, l := range req.Lines {
		lineID, err := parseUUID(l.LineID, "lines[].line_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		qty, err := parseAmount(l.Qty, "lines[].qty", i)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		requests = append(requests, sales.ReturnRequest{LineID: lineID, Qty: qty})
	}

	refunds := make([]sales.Refund, 0, len(req.Refunds))
	for i, f := range req.Refunds {
		amount, err := parseAmount(f.Amount, "refunds[].amount", i)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		reversesID, err := parseOptionalUUID(f.ReversesTenderID, "refunds[].reverses_tender_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		refund := sales.Refund{
			Method: f.Method, Amount: amount, Reference: f.Reference,
		}
		if reversesID != uuid.Nil {
			refund.ReversesTenderID = &reversesID
		}
		refunds = append(refunds, refund)
	}

	userID := a.UserID
	out, err := s.sales.Refund(r.Context(), a.TenantID, a.DeviceID, sales.Return{
		CreditNoteUUID:    creditNoteUUID,
		OriginalInvoiceID: originalID,
		IssuedAt:          issuedAt,
		Reason:            req.Reason,
		Requests:          requests,
		Refunds:           refunds,
		CashierID:         &userID,
	}, warehouseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	status := http.StatusCreated
	if out.AlreadyRefunded {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}

	httpx.JSON(w, status, refundResponse(out))
}

// --- GET /api/v1/pos/sales/{invoiceID} ----------------------------------

func (s *Server) handleGetSale(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := parseUUID(chi.URLParam(r, "invoiceID"), "invoiceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	out, err := s.sales.Read(r.Context(), a.TenantID, invoiceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleReturnableLines reports what is still owed back on an invoice.
//
// Gated on sales.refund, not sales.view: the numbers here exist to authorise a
// refund, and a Cashier who may look up a sale but not refund one has no
// business being told how much of it is still claimable.
//
// A till must never compute this for itself. How much of a line has already
// been returned lives in the credit notes against the invoice, which a
// terminal that was offline when they were raised has never seen — and the
// failure mode is refunding the same jacket twice.
func (s *Server) handleReturnableLines(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := parseUUID(chi.URLParam(r, "invoiceID"), "invoiceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	lines, err := s.sales.Returnable(r.Context(), a.TenantID, invoiceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// --- responses ----------------------------------------------------------

func saleResponse(f sales.Finalized) map[string]any {
	lines := make([]map[string]any, 0, len(f.Computed.Lines))
	for _, l := range f.Computed.Lines {
		lines = append(lines, map[string]any{
			"line_no":                l.LineNo,
			"description":            l.Description,
			"qty":                    l.Qty.String(),
			"unit_price":             l.UnitPrice.String(),
			"line_discount":          l.LineDiscount.String(),
			"invoice_discount_alloc": l.InvoiceDiscountAlloc.String(),
			"tax_rate":               l.TaxRate.String(),
			"tax_amount":             l.TaxAmount.String(),
			"net_amount":             l.NetAmount.String(),
			"gross_amount":           l.GrossAmount.String(),
		})
	}

	shortfalls := make([]map[string]any, 0, len(f.Shortfalls))
	for _, sf := range f.Shortfalls {
		shortfalls = append(shortfalls, map[string]any{
			"line_no": sf.LineNo, "variant_id": sf.VariantID, "short_by": sf.ShortBy.String(),
		})
	}

	return map[string]any{
		"invoice_id":      f.InvoiceID,
		"subtotal_net":    f.Computed.SubtotalNet.String(),
		"discount_total":  f.Computed.DiscountTotal.String(),
		"tax_total":       f.Computed.TaxTotal.String(),
		"total_inclusive": f.Computed.TotalInclusive.String(),
		"lines":           lines,

		// The chain position. The till needs the ICV and PIH to build and sign
		// the XML locally — the server never sees the signing key.
		"zatca": map[string]any{
			"icv":            f.Link.ICV,
			"pih":            f.Link.PIH,
			"schema_version": f.Link.SchemaVersion,
		},

		// COGS is deliberately absent. A cashier has no business seeing cost or
		// margin, and the till has no need for it — it is posted server-side.

		"stock_shortfalls": shortfalls,
	}
}

func refundResponse(f sales.Refunded) map[string]any {
	return map[string]any{
		"credit_note_id":  f.CreditNoteID,
		"subtotal_net":    f.Computed.SubtotalNet.String(),
		"tax_total":       f.Computed.TaxTotal.String(),
		"total_inclusive": f.Computed.TotalInclusive.String(),
		"zatca": map[string]any{
			"icv":            f.Link.ICV,
			"pih":            f.Link.PIH,
			"schema_version": f.Link.SchemaVersion,
		},

		// What this return has NOT done. C14 names nine effects and two of them
		// — loyalty and commission — are Phase 2. Reporting them keeps a till
		// from showing "return complete" when points are still outstanding.
		"effects_outstanding": f.Outstanding,
	}
}

// --- parsing ------------------------------------------------------------

func parseUUID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, errs.Newf(errs.CodeInvalidInput,
			"%s is not a valid identifier.", field)
	}
	return id, nil
}

func parseOptionalUUID(s, field string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	return parseUUID(s, field)
}

// parseAmount reads a decimal from a STRING. A JSON number would have been
// through a float64 by the time it arrived and 0.15 is not exactly
// representable in binary floating point.
func parseAmount(s, field string, index int) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"%s is required%s.", field, atIndex(index))
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"%s%s is not a number.", field, atIndex(index))
	}
	return d, nil
}

func parseOptionalAmount(s, field string, index int) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return parseAmount(s, field, index)
}

func atIndex(i int) string {
	if i < 0 {
		return ""
	}
	return " on line " + decimal.NewFromInt(int64(i+1)).String()
}

// parseIssuedAt reads the time the sale was rung up ON THE DEVICE.
//
// The device's time is used, not the server's, because an offline sale may sync
// days later and it belongs to the day it happened. Getting this wrong would
// put every offline sale near a month end into the wrong fiscal period.
func parseIssuedAt(s string) (time.Time, error) {
	if s == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errs.New(errs.CodeInvalidInput,
			"issued_at must be an RFC 3339 timestamp, for example "+
				"2026-08-15T10:30:00Z.")
	}
	return t.UTC(), nil
}

// --- POST /api/v1/pos/sales/{invoiceID}/signed-document -----------------

type signedDocumentRequest struct {
	// XML is the canonical signed UBL 2.1 document the terminal built and
	// signed locally. This is what ZATCA receives.
	XML string `json:"xml"`

	// Stamp is the ECDSA signature over that document; QRTLV is the payload
	// derived from it for the receipt. Three distinct things, carried
	// distinctly.
	Stamp string `json:"stamp"`
	QRTLV string `json:"qr_tlv"`
}

// handleUploadSignedDocument stores what the terminal signed.
//
// The step that closes the loop on local signing. The server allocates the ICV
// and the PIH because they are a per-terminal sequence only one authority can
// arbitrate; the TERMINAL then builds the UBL, signs it with a CSID key this
// process has never held, and sends the result back here (E1.3 RULE 1).
//
// Nothing is validated against ZATCA's standard, because the standard is not
// yet verified. The document is stored as received and submission stays gated.
func (s *Server) handleUploadSignedDocument(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := parseUUID(chi.URLParam(r, "invoiceID"), "invoiceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var req signedDocumentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if a.DeviceID == uuid.Nil {
		// Only a registered terminal signs. A browser session has no CSID and
		// nothing it produced could be a signed document.
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"Only a registered terminal can upload a signed document."))
		return
	}

	out, err := s.sales.AttachSignedDocument(r.Context(), a.TenantID, a.DeviceID,
		invoiceID, zatca.SignedDocument{
			XML: req.XML, Stamp: req.Stamp, QRTLV: req.QRTLV,
		})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
