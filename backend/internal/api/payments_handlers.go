package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/payments"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Card providers (E3.3) and the machine on the counter (E3.4).
//
// # No secret ever comes back out
//
// There is no route here that returns a key, and the `Gateway` type has no
// field for one. A screen learns that a key is stored and can replace it; it
// cannot read it. That is deliberate, and it is why the settings screen shows
// an empty box on an edit rather than dots.
//
// # The provider list is served, not hard-coded in the browser
//
// `GET /payment-providers` returns every acquirer with the fields it needs, so
// the settings screen renders a box per field from data. Adding an acquirer is
// an adapter and a table entry, not a change in the frontend.

func paymentScope(r *http.Request) (payments.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return payments.Scope{}, err
	}
	if !a.CanAccessCompany(companyID) {
		return payments.Scope{}, errs.New(errs.CodeNotFound,
			"That company was not found.")
	}
	return payments.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// paymentsReady reports the honest failure when no data encryption key is
// configured.
//
// A gateway key has to be sealed, and sealing needs a keyring. An installation
// without one should say so rather than storing a live acquirer credential in
// the clear.
func (s *Server) paymentsReady() error {
	if s.payments == nil {
		return errs.New(errs.CodeUnavailable,
			"Card providers need a data encryption key, and this "+
				"installation has none configured.")
	}
	return nil
}

// handleListPaymentProviders serves the catalogue the settings screen renders.
func (s *Server) handleListPaymentProviders(
	w http.ResponseWriter, r *http.Request,
) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"providers": payments.Providers(),
	})
}

func (s *Server) handleListGateways(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.payments.Gateways(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"gateways": out})
}

type gatewayRequest struct {
	Provider string            `json:"provider"`
	Label    string            `json:"label"`
	Mode     string            `json:"mode"`
	Settings map[string]string `json:"settings"`
	// Secret is the sealed half. Empty on an edit means "leave what is there",
	// because a screen that cannot read a key back cannot send it again.
	Secret   string   `json:"secret"`
	Methods  []string `json:"methods"`
	IsActive bool     `json:"is_active"`
}

func (s *Server) handleSaveGateway(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req gatewayRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := payments.NewGateway{
		Provider: req.Provider,
		Label:    req.Label,
		Mode:     req.Mode,
		Settings: req.Settings,
		Secret:   req.Secret,
		Methods:  req.Methods,
		IsActive: req.IsActive,
	}
	// An edit carries the id in the path; a create has none.
	if raw := chi.URLParam(r, "gatewayID"); raw != "" {
		id, e := parseUUID(raw, "gateway id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.ID = id
	}

	out, err := s.payments.SaveGateway(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	status := http.StatusOK
	if in.ID == uuid.Nil {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, map[string]any{"gateway": out})
}

func (s *Server) handleReadGateway(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "gatewayID"), "gateway id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.payments.Gateway(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"gateway": out})
}

func (s *Server) handleRemoveGateway(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "gatewayID"), "gateway id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.payments.RemoveGateway(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckGateway is the Test button.
//
// It talks to the acquirer with the stored credentials and writes down what
// came back, so a connection that stopped working is visible on the settings
// screen rather than at the counter. It never moves money.
func (s *Server) handleCheckGateway(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "gatewayID"), "gateway id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.payments.Check(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"gateway": out})
}

// --- taking the money -----------------------------------------------------

type chargeRequest struct {
	// Idempotency is the caller's own uuid. A till that retries after a
	// timeout sends the same one and gets the same attempt back rather than
	// charging the customer twice.
	Idempotency string `json:"idempotency_key"`
	Method      string `json:"method"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	InvoiceID   string `json:"invoice_id"`
	ReturnURL   string `json:"return_url"`
}

func (s *Server) handleCharge(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	gatewayID, err := parseUUID(chi.URLParam(r, "gatewayID"), "gateway id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req chargeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	idem, err := parseUUID(req.Idempotency, "idempotency key")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := parseAmount(req.Amount, "amount", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var invoiceID *uuid.UUID
	if req.InvoiceID != "" {
		id, e := parseUUID(req.InvoiceID, "invoice id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		invoiceID = &id
	}

	out, err := s.payments.Charge(r.Context(), scope, gatewayID, idem,
		req.Method, amount, req.Currency, invoiceID, req.ReturnURL)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attempt": out})
}

func (s *Server) handleListAttempts(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.payments.Attempts(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attempts": out})
}

type gatewayRefundRequest struct {
	Amount string `json:"amount"`
}

func (s *Server) handleRefundAttempt(w http.ResponseWriter, r *http.Request) {
	if err := s.paymentsReady(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := paymentScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	attemptID, err := parseUUID(chi.URLParam(r, "attemptID"), "attempt id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req gatewayRefundRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := parseAmount(req.Amount, "amount", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !amount.IsPositive() {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A refund of nothing is not a refund."))
		return
	}
	out, err := s.payments.Refund(r.Context(), scope, attemptID, amount)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attempt": out})
}
