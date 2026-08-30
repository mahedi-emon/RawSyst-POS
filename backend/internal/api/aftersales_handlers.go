package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Delivery, serial/warranty, service and instalments (B13, B14, B15).

func afterScope(r *http.Request) (aftersales.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return aftersales.Scope{}, err
	}
	if !a.CanAccessCompany(companyID) {
		return aftersales.Scope{}, errs.New(errs.CodeNotFound,
			"That company was not found.")
	}
	return aftersales.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- Delivery ------------------------------------------------------------

type deliveryRequest struct {
	OrderID   string `json:"order_id"`
	Address   string `json:"address"`
	Phone     string `json:"phone"`
	Fee       string `json:"fee"`
	IsCOD     bool   `json:"is_cod"`
	CODAmount string `json:"cod_amount"`
	DriverID  string `json:"driver_id"`
	Note      string `json:"note"`
}

func (s *Server) handleBookDelivery(w http.ResponseWriter, r *http.Request) {
	var req deliveryRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	orderID, err := parseUUID(req.OrderID, "order_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := aftersales.NewDelivery{
		OrderID: orderID, Address: req.Address, Phone: req.Phone,
		IsCOD: req.IsCOD, Note: req.Note,
		Fee: decimal.Zero, CODAmount: decimal.Zero,
	}
	if req.Fee != "" {
		if in.Fee, err = parseAmount(req.Fee, "fee", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if req.CODAmount != "" {
		if in.CODAmount, err = parseAmount(req.CODAmount, "cod_amount", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if in.DriverID, err = optionalUUID(req.DriverID, "driver_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.aftersales.BookDelivery(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

type advanceRequest struct {
	Status       string `json:"status"`
	Note         string `json:"note"`
	DriverID     string `json:"driver_id"`
	Latitude     string `json:"latitude"`
	Longitude    string `json:"longitude"`
	CollectedCOD bool   `json:"collected_cod"`
}

func (s *Server) handleAdvanceDelivery(w http.ResponseWriter, r *http.Request) {
	var req advanceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "deliveryID"), "deliveryID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := aftersales.Advance{
		Status: req.Status, Note: req.Note, CollectedCOD: req.CollectedCOD,
	}
	if in.DriverID, err = optionalUUID(req.DriverID, "driver_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	for _, p := range []struct {
		raw   string
		field string
		dst   **decimal.Decimal
	}{{req.Latitude, "latitude", &in.Latitude},
		{req.Longitude, "longitude", &in.Longitude}} {
		if p.raw == "" {
			continue
		}
		v, e := parseAmount(p.raw, p.field, 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*p.dst = &v
	}

	out, err := s.aftersales.AdvanceDelivery(r.Context(), scope, id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleListDeliveries lists consignments.
//
// A driver sees only their own. A6.1 gives Delivery Staff "assigned delivery
// orders only", and a driver who could list every consignment would be reading
// the company's customer address book — so the narrowing is decided here from
// what the caller may do, never from a query parameter they could omit.
func (s *Server) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// A driver sees only their own runs. Decided from what the caller may do,
	// never from a query parameter they could simply omit.
	g := identity.GrantsFrom(r.Context())
	mineOnly := g == nil || !g.Can("delivery.manage")

	out, err := s.aftersales.Deliveries(
		r.Context(), scope, r.URL.Query().Get("status"), mineOnly)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadDelivery(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "deliveryID"), "deliveryID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.ReadDelivery(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// A driver may read only their own run, for the same reason they may list
	// only their own.
	a := actor.From(r.Context())
	g := identity.GrantsFrom(r.Context())
	if (g == nil || !g.Can("delivery.manage")) &&
		(out.DriverID == nil || *out.DriverID != a.UserID) {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That delivery was not found."))
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Reservation ---------------------------------------------------------

type reserveRequest struct {
	OrderID     string `json:"order_id"`
	VariantID   string `json:"variant_id"`
	WarehouseID string `json:"warehouse_id"`
	Qty         string `json:"qty"`
	ExpiresIn   int    `json:"expires_in_minutes"`
}

func (s *Server) handleReserveStock(w http.ResponseWriter, r *http.Request) {
	var req reserveRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	orderID, err := parseUUID(req.OrderID, "order_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := parseUUID(req.VariantID, "variant_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	qty, err := parseAmount(req.Qty, "qty", 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var expires *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresIn) * time.Minute)
		expires = &t
	}

	if err := s.aftersales.Reserve(r.Context(), scope, orderID, variantID,
		warehouseID, qty, expires); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReleaseReservation(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	orderID, err := parseUUID(chi.URLParam(r, "orderID"), "orderID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.aftersales.ReleaseOrder(r.Context(), scope, orderID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStockAvailability(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := parseUUID(r.URL.Query().Get("variant_id"), "variant_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseUUID(r.URL.Query().Get("warehouse_id"), "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.Available(r.Context(), scope, variantID, warehouseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Serials and warranty ------------------------------------------------

type receiveSerialsRequest struct {
	VariantID   string   `json:"variant_id"`
	WarehouseID string   `json:"warehouse_id"`
	GRNID       string   `json:"grn_id"`
	SupplierID  string   `json:"supplier_id"`
	Serials     []string `json:"serials"`
}

func (s *Server) handleReceiveSerials(w http.ResponseWriter, r *http.Request) {
	var req receiveSerialsRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := parseUUID(req.VariantID, "variant_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := identity.CheckWarehouseScope(r.Context(), warehouseID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	grnID, err := optionalUUID(req.GRNID, "grn_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, err := optionalUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.aftersales.ReceiveSerials(r.Context(), scope, variantID,
		warehouseID, grnID, supplierID, req.Serials)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (s *Server) handleListSerials(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := optionalUUID(r.URL.Query().Get("variant_id"), "variant_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.Serials(
		r.Context(), scope, r.URL.Query().Get("status"), variantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleLookupSerial is the warranty desk's question: what is this, whose is
// it, and is it still covered.
func (s *Server) handleLookupSerial(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.LookupSerial(
		r.Context(), scope, chi.URLParam(r, "serialNo"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Service jobs --------------------------------------------------------

type bookInRequest struct {
	CustomerID string `json:"customer_id"`
	StoreID    string `json:"store_id"`
	SerialNo   string `json:"serial_no"`
	VariantID  string `json:"variant_id"`
	Kind       string `json:"kind"`
	Fault      string `json:"fault_reported"`
	PromisedOn string `json:"promised_on"`
}

func (s *Server) handleBookInRepair(w http.ResponseWriter, r *http.Request) {
	var req bookInRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := aftersales.NewServiceOrder{
		SerialNo: req.SerialNo, Kind: req.Kind, Fault: req.Fault,
	}
	if in.CustomerID, err = optionalUUID(req.CustomerID, "customer_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.StoreID, err = optionalUUID(req.StoreID, "store_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.VariantID, err = optionalUUID(req.VariantID, "variant_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.PromisedOn, err = optionalDate(req.PromisedOn, "promised_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.aftersales.BookIn(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

type issuePartRequest struct {
	VariantID   string `json:"variant_id"`
	WarehouseID string `json:"warehouse_id"`
	Qty         string `json:"qty"`
}

func (s *Server) handleIssueServicePart(w http.ResponseWriter, r *http.Request) {
	var req issuePartRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	jobID, err := parseUUID(chi.URLParam(r, "jobID"), "jobID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := parseUUID(req.VariantID, "variant_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	warehouseID, err := parseUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	qty, err := parseAmount(req.Qty, "qty", 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.aftersales.IssuePart(r.Context(), scope, jobID, variantID,
		warehouseID, qty)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type jobUpdateRequest struct {
	Status            string `json:"status"`
	Diagnosis         string `json:"diagnosis"`
	WorkDone          string `json:"work_done"`
	LabourCost        string `json:"labour_cost"`
	Charged           string `json:"charged"`
	ReplacementSerial string `json:"replacement_serial"`
}

func (s *Server) handleUpdateRepair(w http.ResponseWriter, r *http.Request) {
	var req jobUpdateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	jobID, err := parseUUID(chi.URLParam(r, "jobID"), "jobID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := aftersales.JobUpdate{
		Status: req.Status, Diagnosis: req.Diagnosis, WorkDone: req.WorkDone,
		ReplacementSerial: req.ReplacementSerial,
	}
	for _, p := range []struct {
		raw   string
		field string
		dst   **decimal.Decimal
	}{{req.LabourCost, "labour_cost", &in.LabourCost},
		{req.Charged, "charged", &in.Charged}} {
		if p.raw == "" {
			continue
		}
		v, e := parseAmount(p.raw, p.field, 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*p.dst = &v
	}

	out, err := s.aftersales.UpdateJob(r.Context(), scope, jobID, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListRepairs(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.Jobs(r.Context(), scope, r.URL.Query().Get("status"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadRepair(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "jobID"), "jobID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.ReadJob(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- Instalments ---------------------------------------------------------

type planQuoteRequest struct {
	Principal   string `json:"principal"`
	DownPayment string `json:"down_payment"`
	MarkupRate  string `json:"markup_rate"`
	Tenure      int    `json:"tenure_months"`
}

// handleQuoteInstalments previews a schedule without committing anybody.
// B14's "EMI Plan Generator": a customer at the counter asks what twelve
// months would cost, and the answer must not create a plan.
func (s *Server) handleQuoteInstalments(w http.ResponseWriter, r *http.Request) {
	var req planQuoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if _, err := afterScope(r); err != nil {
		httpx.Error(w, r, err)
		return
	}

	principal, err := parseAmount(req.Principal, "principal", 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	down := decimal.Zero
	if req.DownPayment != "" {
		if down, err = parseAmount(req.DownPayment, "down_payment", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	rate := decimal.Zero
	if req.MarkupRate != "" {
		if rate, err = parseAmount(req.MarkupRate, "markup_rate", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	out, err := aftersales.QuotePlan(principal, down, rate, req.Tenure)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type openPlanRequest struct {
	CustomerID     string `json:"customer_id"`
	InvoiceID      string `json:"invoice_id"`
	DownPayment    string `json:"down_payment"`
	MarkupRate     string `json:"markup_rate"`
	Tenure         int    `json:"tenure_months"`
	StartsOn       string `json:"starts_on"`
	LateFeeFlat    string `json:"late_fee_flat"`
	LateFeeRate    string `json:"late_fee_rate"`
	GraceDays      int    `json:"grace_days"`
	GuarantorName  string `json:"guarantor_name"`
	GuarantorPhone string `json:"guarantor_phone"`
	GuarantorIDNo  string `json:"guarantor_id_no"`
	Verification   string `json:"verification_note"`
}

func (s *Server) handleOpenPlan(w http.ResponseWriter, r *http.Request) {
	var req openPlanRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(req.CustomerID, "customer_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	invoiceID, err := parseUUID(req.InvoiceID, "invoice_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := aftersales.NewPlan{
		CustomerID: customerID, InvoiceID: invoiceID, Tenure: req.Tenure,
		GraceDays: req.GraceDays, GuarantorName: req.GuarantorName,
		GuarantorPhone: req.GuarantorPhone, GuarantorIDNo: req.GuarantorIDNo,
		Verification: req.Verification,
		DownPayment:  decimal.Zero, MarkupRate: decimal.Zero,
		LateFeeFlat: decimal.Zero, LateFeeRate: decimal.Zero,
		StartsOn: time.Now().UTC(),
	}
	for _, p := range []struct {
		raw   string
		field string
		dst   *decimal.Decimal
	}{{req.DownPayment, "down_payment", &in.DownPayment},
		{req.MarkupRate, "markup_rate", &in.MarkupRate},
		{req.LateFeeFlat, "late_fee_flat", &in.LateFeeFlat},
		{req.LateFeeRate, "late_fee_rate", &in.LateFeeRate}} {
		if p.raw == "" {
			continue
		}
		v, e := parseAmount(p.raw, p.field, 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*p.dst = v
	}
	if req.StartsOn != "" {
		when, e := optionalDate(req.StartsOn, "starts_on")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StartsOn = *when
	}

	out, err := s.aftersales.OpenPlan(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

type collectRequest struct {
	ReceiptID string `json:"receipt_id"`
	Amount    string `json:"amount"`
}

func (s *Server) handleCollectInstalment(w http.ResponseWriter, r *http.Request) {
	var req collectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	planID, err := parseUUID(chi.URLParam(r, "planID"), "planID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	receiptID, err := parseUUID(req.ReceiptID, "receipt_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := parseAmount(req.Amount, "amount", 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.aftersales.CollectInstalment(r.Context(), scope, planID,
		receiptID, amount)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.Plans(r.Context(), scope, r.URL.Query().Get("status"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadPlan(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "planID"), "planID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.aftersales.ReadPlan(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type cancelPlanRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleCancelPlan(w http.ResponseWriter, r *http.Request) {
	var req cancelPlanRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "planID"), "planID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.aftersales.CancelPlan(r.Context(), scope, id, req.Reason); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAccrueFinanceIncome earns the finance charge on instalments now due.
func (s *Server) handleAccrueFinanceIncome(w http.ResponseWriter, r *http.Request) {
	scope, err := afterScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	accrued, err := s.aftersales.AccrueDue(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	charged, err := s.aftersales.ApplyLateFees(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"accrued": accrued, "late_fees_charged": charged,
	})
}
