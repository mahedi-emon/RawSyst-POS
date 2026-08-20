package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
)

// The customers and receivables surface.
//
// Shaped exactly like the purchasing surface: the company is an explicit
// parameter because these are back-office routes with no terminal to resolve it
// from, and CanAccessCompany plus a not-found answer is what stops a caller
// naming a company they have no business in.
//
// One route is gated differently from the rest, and deliberately.
// `customers.set_credit_limit` is separate from `customers.manage`: deciding how
// much a customer may owe is a different act from recording their phone number,
// and a shop that lets a counter assistant do the first has no control over its
// receivable at all.

// customerScope resolves and authorises the company for a customer call.
//
// Named company OR registered device, because both kinds of caller are real:
// the back office names a company explicitly, and a till has no company to name
// — its authority comes from the device in its token. Sharing one resolver
// means the customer routes serve the counter and the office without a second
// parallel surface, which is how the two would drift.
//
// The named-company path checks CanAccessCompany and answers "not found"
// rather than returning an empty list, for the reason spelled out in
// purchaseScope: an empty CompanyIDs means "every company in MY tenant", so
// without the check another tenant's company id reads as a company with no
// customers rather than as one this caller has no business asking about.
func (s *Server) customerScope(r *http.Request) (receivables.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return receivables.Scope{}, err
	}

	return receivables.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- Customers -----------------------------------------------------------

type customerRequest struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	NameAr    string `json:"name_ar"`
	Type      string `json:"customer_type"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	VATNumber string `json:"vat_number"`
	Address   string `json:"address"`
	TermsDays int    `json:"payment_terms_days"`
	Notes     string `json:"notes"`

	// CreditLimit is only read on create. Changing it afterwards goes through
	// its own route so it carries its own permission — see the file comment.
	CreditLimit string `json:"credit_limit"`
}

func (r customerRequest) toNew() receivables.NewCustomer {
	return receivables.NewCustomer{
		Code: r.Code, Name: r.Name, NameAr: r.NameAr, Type: r.Type,
		Phone: r.Phone, Email: r.Email, VATNumber: r.VATNumber,
		Address: r.Address, TermsDays: r.TermsDays, Notes: r.Notes,
	}
}

func (s *Server) handleCreateCustomer(w http.ResponseWriter, r *http.Request) {
	var req customerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := req.toNew()
	if req.CreditLimit != "" {
		// A limit set at creation still needs the authority to set one. Without
		// this check, `customers.manage` alone would be enough to open an account
		// with any ceiling on it, and the separate permission would mean nothing.
		if !identity.GrantsFrom(r.Context()).Can("customers.set_credit_limit") {
			httpx.Error(w, r, errs.New(errs.CodeForbidden,
				"Setting a credit limit is not something your role allows. "+
					"Create the customer without one and ask an owner to set it."))
			return
		}
		limit, e := parseAmount(req.CreditLimit, "credit_limit", -1)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CreditLimit = &limit
	}

	out, err := s.receivables.CreateCustomer(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req customerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.UpdateCustomer(r.Context(), scope, customerID, req.toNew())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type creditLimitRequest struct {
	// Empty means "no credit account". Distinct from "0", which means an account
	// exists with nothing available on it — the difference matters when a limit
	// is later restored.
	CreditLimit string `json:"credit_limit"`
}

func (s *Server) handleSetCreditLimit(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req creditLimitRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := receivables.NewCustomer{}
	if req.CreditLimit != "" {
		limit, e := parseAmount(req.CreditLimit, "credit_limit", -1)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CreditLimit = &limit
	}

	out, err := s.receivables.SetCreditLimit(r.Context(), scope, customerID, in.CreditLimit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type activeRequest struct {
	Active bool `json:"active"`
}

func (s *Server) handleSetCustomerActive(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req activeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.SetCustomerActive(r.Context(), scope, customerID, req.Active)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListCustomers(w http.ResponseWriter, r *http.Request) {
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.ListCustomers(r.Context(), scope,
		r.URL.Query().Get("search"),
		r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadCustomer(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.ReadCustomer(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCustomerLedger(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.LedgerFor(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCustomerOpenInvoices(w http.ResponseWriter, r *http.Request) {
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.OpenFor(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- Receipts ------------------------------------------------------------

type receiptRequest struct {
	// UUID is assigned by the client before the call. Same discipline as a sale,
	// a return and a supplier payment: a retry after a timeout must recognise
	// the payment rather than take it a second time.
	UUID        string                     `json:"uuid"`
	CustomerID  string                     `json:"customer_id"`
	Method      string                     `json:"method"`
	Reference   string                     `json:"reference"`
	ReceivedOn  string                     `json:"received_on"`
	Allocations []invoiceAllocationRequest `json:"allocations"`
}

type invoiceAllocationRequest struct {
	InvoiceID string `json:"invoice_id"`
	Amount    string `json:"amount"`
}

func (s *Server) handleTakeCustomerPayment(w http.ResponseWriter, r *http.Request) {
	var req receiptRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(req.CustomerID, "customer_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := receivables.NewReceipt{
		UUID: docUUID, CustomerID: customerID,
		Method: req.Method, Reference: req.Reference,
	}
	if req.ReceivedOn != "" {
		when, e := time.Parse("2006-01-02", req.ReceivedOn)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		in.ReceivedOn = when
	}

	for i, a := range req.Allocations {
		invoiceID, e := parseUUID(a.InvoiceID, "allocations.invoice_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		amount, e := parseAmount(a.Amount, "allocations.amount", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Allocations = append(in.Allocations, receivables.Allocation{
			InvoiceID: invoiceID, Amount: amount,
		})
	}

	out, err := s.receivables.TakePayment(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// A recognised retry is not a creation. 200 rather than 201 so a client can
	// tell the difference between "we took it" and "we already had it".
	status := http.StatusCreated
	if out.AlreadyTaken {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func (s *Server) handleReverseCustomerPayment(w http.ResponseWriter, r *http.Request) {
	receiptID, err := parseUUID(chi.URLParam(r, "receiptID"), "receiptID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.receivables.ReversePayment(r.Context(), scope, receivables.ReverseReceipt{
		UUID: docUUID, ReceiptID: receiptID,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.AlreadyTaken {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

// --- Ageing --------------------------------------------------------------

func (s *Server) handleCustomerAgeing(w http.ResponseWriter, r *http.Request) {
	scope, err := s.customerScope(r)
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

	out, err := s.receivables.AgeingAt(r.Context(), scope, asOf)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- GET /api/v1/customers/snapshot --------------------------------------

// handleCustomerSnapshot serves the customers a till caches.
//
// The mirror of handleCatalogSnapshot, cursored on (since, since_id) for the
// same reason: the till stores the last pair it saw and passes it back, so one
// route serves the first full download and every later delta. Paging by offset
// would skip or repeat rows when the book changes between pages, which at a
// counter means a regular customer who cannot be found.
func (s *Server) handleCustomerSnapshot(w http.ResponseWriter, r *http.Request) {
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var sinceID *uuid.UUID
	if raw := r.URL.Query().Get("since_id"); raw != "" {
		id, e := parseUUID(raw, "since_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		sinceID = &id
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, _ = strconv.Atoi(raw)
	}

	items, err := s.receivables.Snapshot(
		r.Context(), scope, r.URL.Query().Get("since"), sinceID, limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The cursor to send back next time. Absent when nothing came back, so a
	// caught-up till keeps the cursor it already has rather than resetting.
	out := map[string]any{"items": items}
	if n := len(items); n > 0 {
		out["next_since"] = items[n-1].UpdatedAt
		out["next_since_id"] = items[n-1].ID
	}
	httpx.JSON(w, http.StatusOK, out)
}
