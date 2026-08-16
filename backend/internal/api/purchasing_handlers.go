package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
)

// The purchasing surface.
//
// Every route names its company explicitly, unlike the POS routes which resolve
// it from a registered device. Purchasing happens in a back office at a browser
// and there is no terminal to resolve from — so the company is a parameter, and
// CanAccessCompany is what stops a caller naming one they have no business in.
//
// Cost price is all over these payloads, deliberately and unavoidably: a
// purchase order IS a cost document. That is why the routes are gated on
// purchasing permissions rather than on catalog.view, and why a Cashier holding
// catalog.view but denied catalog.view_cost_price cannot reach any of them.

// purchaseScope resolves and authorises the company for a purchasing call.
func purchaseScope(r *http.Request) (purchasing.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return purchasing.Scope{}, err
	}

	// CanAccessCompany checks the token's own scope, and an empty CompanyIDs
	// means "every company in MY tenant" — so on its own it lets a company id
	// belonging to a different tenant through, and every query below then
	// returns an empty list under row-level security. Empty is a much worse
	// answer than "not found": it looks like a company with no purchase orders
	// rather than one this caller has no business asking about.
	if !a.CanAccessCompany(companyID) {
		return purchasing.Scope{}, errs.New(errs.CodeNotFound,
			"That company was not found.")
	}

	return purchasing.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- Suppliers -----------------------------------------------------------

type supplierRequest struct {
	Code        string `json:"code"`
	LegalName   string `json:"legal_name"`
	NameAr      string `json:"name_ar"`
	Contact     string `json:"contact_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	VATNumber   string `json:"vat_number"`
	CRNumber    string `json:"cr_number"`
	Country     string `json:"country"`
	TermsDays   int    `json:"payment_terms_days"`
	CreditLimit string `json:"credit_limit"`
	Notes       string `json:"notes"`
}

func (s *Server) handleCreateSupplier(w http.ResponseWriter, r *http.Request) {
	var req supplierRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewSupplier{
		Code: req.Code, LegalName: req.LegalName, NameAr: req.NameAr,
		Contact: req.Contact, Email: req.Email, Phone: req.Phone,
		VATNumber: req.VATNumber, CRNumber: req.CRNumber,
		Country: req.Country, TermsDays: req.TermsDays, Notes: req.Notes,
	}
	if req.CreditLimit != "" {
		limit, e := parseAmount(req.CreditLimit, "credit_limit", -1)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CreditLimit = &limit
	}

	out, err := s.purchasing.CreateSupplier(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListSuppliers(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.ListSuppliers(r.Context(), scope,
		r.URL.Query().Get("search"),
		r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- Purchase orders -----------------------------------------------------

type orderLineRequest struct {
	VariantID    string `json:"variant_id"`
	Description  string `json:"description"`
	Qty          string `json:"qty"`
	UnitCost     string `json:"unit_cost"`
	TaxTreatment string `json:"tax_treatment"`
	TaxRate      string `json:"tax_rate"`
}

type createOrderRequest struct {
	SupplierID  string             `json:"supplier_id"`
	WarehouseID string             `json:"warehouse_id"`
	ExpectedOn  string             `json:"expected_on"`
	Notes       string             `json:"notes"`
	Lines       []orderLineRequest `json:"lines"`
}

func (s *Server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	supplierID, err := parseUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewOrder{
		SupplierID: supplierID, WarehouseID: warehouseID, Notes: req.Notes,
	}
	if req.ExpectedOn != "" {
		when, e := time.Parse("2006-01-02", req.ExpectedOn)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		in.ExpectedOn = &when
	}

	for i, line := range req.Lines {
		parsed, e := parseOrderLine(i, line)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Lines = append(in.Lines, parsed)
	}

	out, err := s.purchasing.CreateOrder(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func parseOrderLine(i int, line orderLineRequest) (purchasing.OrderLine, error) {
	variantID, err := parseUUID(line.VariantID, "variant_id")
	if err != nil {
		return purchasing.OrderLine{}, err
	}
	qty, err := parseAmount(line.Qty, "qty", i)
	if err != nil {
		return purchasing.OrderLine{}, err
	}
	cost, err := parseAmount(line.UnitCost, "unit_cost", i)
	if err != nil {
		return purchasing.OrderLine{}, err
	}

	rate := decimal.Zero
	if line.TaxRate != "" {
		rate, err = parseAmount(line.TaxRate, "tax_rate", i)
		if err != nil {
			return purchasing.OrderLine{}, err
		}
	}

	return purchasing.OrderLine{
		VariantID: variantID, Description: line.Description,
		Qty: qty, UnitCost: cost,
		TaxTreatment: line.TaxTreatment, TaxRate: rate,
	}, nil
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	out, err := s.purchasing.ListOrders(r.Context(), scope,
		r.URL.Query().Get("status"), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadOrder(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	poID, err := parseUUID(chi.URLParam(r, "poID"), "poID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.ReadOrder(r.Context(), scope, poID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleIssueOrder(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	poID, err := parseUUID(chi.URLParam(r, "poID"), "poID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.IssueOrder(r.Context(), scope, poID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Receiving -----------------------------------------------------------

type receiveLineRequest struct {
	POLineID     string `json:"po_line_id"`
	QtyReceived  string `json:"qty_received"`
	QtyRejected  string `json:"qty_rejected"`
	RejectReason string `json:"reject_reason"`
}

type receiveRequest struct {
	UUID            string               `json:"uuid"`
	POID            string               `json:"po_id"`
	DeliveryNoteRef string               `json:"delivery_note_ref"`
	Notes           string               `json:"notes"`
	Lines           []receiveLineRequest `json:"lines"`
}

// handleReceiveGoods records a delivery. The only route in the product that
// increases stock through a purchase — B5's rule that a PO alone never inflates
// inventory lives on the other side of this handler.
func (s *Server) handleReceiveGoods(w http.ResponseWriter, r *http.Request) {
	var req receiveRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	poID, err := parseUUID(req.POID, "po_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.Delivery{
		UUID: docUUID, POID: poID,
		DeliveryNoteRef: req.DeliveryNoteRef, Notes: req.Notes,
	}
	for i, line := range req.Lines {
		lineID, e := parseUUID(line.POLineID, "po_line_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		received, e := parseAmount(line.QtyReceived, "qty_received", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		rejected := decimal.Zero
		if line.QtyRejected != "" {
			rejected, e = parseAmount(line.QtyRejected, "qty_rejected", i)
			if e != nil {
				httpx.Error(w, r, e)
				return
			}
		}
		in.Lines = append(in.Lines, purchasing.ReceivedLine{
			POLineID: lineID, QtyReceived: received, QtyRejected: rejected,
			RejectReason: line.RejectReason,
		})
	}

	out, err := s.purchasing.ReceiveGoods(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// A recognised retry returns 200 with the original receipt rather than
	// 201, so a client can tell it did not create anything new.
	status := http.StatusCreated
	if out.AlreadyReceived {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

// --- Bills ---------------------------------------------------------------

type billLineRequest struct {
	POLineID     string `json:"po_line_id"`
	VariantID    string `json:"variant_id"`
	Description  string `json:"description"`
	Qty          string `json:"qty"`
	UnitCost     string `json:"unit_cost"`
	TaxTreatment string `json:"tax_treatment"`
	TaxRate      string `json:"tax_rate"`
}

type billRequest struct {
	UUID        string            `json:"uuid"`
	SupplierID  string            `json:"supplier_id"`
	POID        string            `json:"po_id"`
	SupplierRef string            `json:"supplier_ref"`
	BillDate    string            `json:"bill_date"`
	Lines       []billLineRequest `json:"lines"`
}

func (s *Server) handleRecordBill(w http.ResponseWriter, r *http.Request) {
	var req billRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, err := parseUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewBill{
		UUID: docUUID, SupplierID: supplierID, SupplierRef: req.SupplierRef,
	}
	if req.POID != "" {
		poID, e := parseUUID(req.POID, "po_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.POID = &poID
	}
	if req.BillDate != "" {
		when, e := time.Parse("2006-01-02", req.BillDate)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		in.BillDate = when
	}

	for i, line := range req.Lines {
		parsed, e := parseBillLine(i, line)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Lines = append(in.Lines, parsed)
	}

	out, err := s.purchasing.RecordBill(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	status := http.StatusCreated
	if out.AlreadyRecorded {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func parseBillLine(i int, line billLineRequest) (purchasing.BillLine, error) {
	qty, err := parseAmount(line.Qty, "qty", i)
	if err != nil {
		return purchasing.BillLine{}, err
	}
	cost, err := parseAmount(line.UnitCost, "unit_cost", i)
	if err != nil {
		return purchasing.BillLine{}, err
	}

	rate := decimal.Zero
	if line.TaxRate != "" {
		rate, err = parseAmount(line.TaxRate, "tax_rate", i)
		if err != nil {
			return purchasing.BillLine{}, err
		}
	}

	out := purchasing.BillLine{
		Description: line.Description, Qty: qty, UnitCost: cost,
		TaxTreatment: line.TaxTreatment, TaxRate: rate,
	}

	// Both optional: a bill can carry a line the order never had, which the
	// match reports as a breach rather than refusing outright.
	if line.POLineID != "" {
		id, e := parseUUID(line.POLineID, "po_line_id")
		if e != nil {
			return purchasing.BillLine{}, e
		}
		out.POLineID = &id
	}
	if line.VariantID != "" {
		id, e := parseUUID(line.VariantID, "variant_id")
		if e != nil {
			return purchasing.BillLine{}, e
		}
		out.VariantID = &id
	}
	return out, nil
}

func (s *Server) handleListBills(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	out, err := s.purchasing.ListBills(r.Context(), scope,
		r.URL.Query().Get("status"), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadBill(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	billID, err := parseUUID(chi.URLParam(r, "billID"), "billID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.ReadBill(r.Context(), scope, billID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type approveBillRequest struct {
	Reason string `json:"reason"`
}

// handleApproveBill lets a blocked bill through.
//
// Its own permission, separate from raising a bill. B5.2's control depends on
// the person who accepts a discrepancy not being the person who entered it, and
// a single permission covering both would make that separation impossible to
// configure.
func (s *Server) handleApproveBill(w http.ResponseWriter, r *http.Request) {
	var req approveBillRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	billID, err := parseUUID(chi.URLParam(r, "billID"), "billID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.ApproveBill(r.Context(), scope, billID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Payment -------------------------------------------------------------

type allocationRequest struct {
	BillID string `json:"bill_id"`
	Amount string `json:"amount"`
}

type paymentRequest struct {
	UUID        string              `json:"uuid"`
	SupplierID  string              `json:"supplier_id"`
	Method      string              `json:"method"`
	Reference   string              `json:"reference"`
	PaidOn      string              `json:"paid_on"`
	Allocations []allocationRequest `json:"allocations"`
}

func (s *Server) handlePaySupplier(w http.ResponseWriter, r *http.Request) {
	var req paymentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, err := parseUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewPayment{
		UUID: docUUID, SupplierID: supplierID,
		Method: req.Method, Reference: req.Reference,
	}
	if req.PaidOn != "" {
		when, e := time.Parse("2006-01-02", req.PaidOn)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		in.PaidOn = when
	}

	for i, a := range req.Allocations {
		billID, e := parseUUID(a.BillID, "bill_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		amount, e := parseAmount(a.Amount, "amount", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Allocations = append(in.Allocations, purchasing.Allocation{
			BillID: billID, Amount: amount,
		})
	}

	out, err := s.purchasing.PaySupplier(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	status := http.StatusCreated
	if out.AlreadyPaid {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func (s *Server) handleSupplierAgeing(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	asOf := time.Now().UTC()
	if raw := r.URL.Query().Get("as_of"); raw != "" {
		when, e := time.Parse("2006-01-02", raw)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		asOf = when
	}

	out, err := s.purchasing.AgeingAt(r.Context(), scope, asOf)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListReceipts(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	poID, err := parseUUID(chi.URLParam(r, "poID"), "poID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.ListReceipts(r.Context(), scope, poID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
