package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portal"
)

// The customer self-service portal and the supplier portal (F2, F3).
//
// # Every portal route is AccessPublic, and that is not a hole
//
// AccessPublic in this product means "carries no STAFF token". A portal route
// carries a PORTAL token instead, resolved here against the portal's own
// session table, and a request without one is refused by the handler before it
// reaches a query. The route registry has no fourth access level because there
// is no fourth kind of staff access — a portal caller is not staff at all.
//
// The distinction matters and is worth stating plainly: `portalCaller` is the
// authorization on these routes. It is not optional, it is not skippable, and
// the two sign-in routes are the only ones that do not call it.
//
// # The shop is named in the query string, and the token is bound to it
//
// A portal is per company: the sign-in page belongs to a shop, and the customer
// of one branch of a group is not automatically the customer of another. The
// company id is therefore a parameter — but the session was issued against a
// company, and `Authenticate` only matches a session whose portal user belongs
// to the company being asked about. Naming a different company produces no
// session, not somebody else's data.

// portalScope reads the company a portal request is against.
func portalScope(r *http.Request) (portal.Scope, error) {
	raw := r.URL.Query().Get("company_id")
	companyID, err := uuid.Parse(raw)
	if err != nil {
		return portal.Scope{}, errs.New(errs.CodeInvalidInput,
			"That sign-in link is not complete.")
	}
	tenantID, err := uuid.Parse(r.URL.Query().Get("tenant_id"))
	if err != nil {
		return portal.Scope{}, errs.New(errs.CodeInvalidInput,
			"That sign-in link is not complete.")
	}
	return portal.Scope{TenantID: tenantID, CompanyID: companyID}, nil
}

// portalToken reads the bearer token.
func portalToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

// portalCaller resolves the signed-in portal user, or refuses.
//
// This is the authorization on every portal route but the two sign-in ones.
func (s *Server) portalCaller(r *http.Request) (portal.Caller, error) {
	scope, err := portalScope(r)
	if err != nil {
		return portal.Caller{}, err
	}
	return s.portal.Authenticate(r.Context(), scope, portalToken(r))
}

// --- signing in -----------------------------------------------------------

type portalCodeRequest struct {
	Phone string `json:"phone"`
}

type portalExchangeRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

type supplierSignInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handlePortalRequestCode sends a customer a one-time code.
//
// Always answers the same way. See the portal package note: an answer that
// differed for a number on file would turn this into a way of asking a shop
// whether a particular person shops there.
func (s *Server) handlePortalRequestCode(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := portalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req portalCodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The rate limiter that guards the staff recovery routes guards this one
	// too: it is the same shape of endpoint, sending the same shape of secret
	// to a phone somebody typed.
	if err := s.recoveryLimit.allow(r.Context(), clientIP(r)); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The code is issued and queued together inside the service. Nothing about
	// it comes back here, so nothing about it can reach the browser.
	if err := s.portal.RequestCode(
		r.Context(), scope, req.Phone); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"sent": true})
}

func (s *Server) handlePortalExchange(w http.ResponseWriter, r *http.Request) {
	scope, err := portalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req portalExchangeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.recoveryLimit.allow(r.Context(), clientIP(r)); err != nil {
		httpx.Error(w, r, err)
		return
	}

	token, caller, err := s.portal.Exchange(r.Context(), scope,
		req.Phone, req.Code, r.UserAgent(), clientIP(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token": token, "name": caller.Name,
	})
}

func (s *Server) handleSupplierSignIn(w http.ResponseWriter, r *http.Request) {
	scope, err := portalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req supplierSignInRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.recoveryLimit.allow(r.Context(), clientIP(r)); err != nil {
		httpx.Error(w, r, err)
		return
	}

	token, caller, err := s.portal.SupplierSignIn(r.Context(), scope,
		req.Email, req.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token": token, "name": caller.Name,
	})
}

func (s *Server) handlePortalSignOut(w http.ResponseWriter, r *http.Request) {
	scope, err := portalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.portal.SignOut(
		r.Context(), scope, portalToken(r)); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- what a customer sees --------------------------------------------------

func (s *Server) handlePortalMe(w http.ResponseWriter, r *http.Request) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.Me(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"me": out})
}

func (s *Server) handlePortalInvoices(w http.ResponseWriter, r *http.Request) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.Invoices(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePortalOrders(w http.ResponseWriter, r *http.Request) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.Orders(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePortalWarranty(w http.ResponseWriter, r *http.Request) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.CheckWarranty(
		r.Context(), caller, r.URL.Query().Get("serial_no"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"warranty": out})
}

func (s *Server) handlePortalAddresses(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if r.Method == http.MethodGet {
		out, err := s.portal.Addresses(r.Context(), caller)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
		return
	}

	var req portal.Address
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SaveAddress(r.Context(), caller, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePortalRemoveAddress(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "addressID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That address was not found."))
		return
	}
	if err := s.portal.RemoveAddress(r.Context(), caller, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type askToReturnRequest struct {
	InvoiceID string `json:"invoice_id"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Items     string `json:"items"`
}

func (s *Server) handlePortalReturns(w http.ResponseWriter, r *http.Request) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if r.Method == http.MethodGet {
		out, err := s.portal.MyReturnRequests(r.Context(), caller)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
		return
	}

	var req askToReturnRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	var invoiceID *uuid.UUID
	if req.InvoiceID != "" {
		parsed, uerr := uuid.Parse(req.InvoiceID)
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That receipt reference is not valid."))
			return
		}
		invoiceID = &parsed
	}
	out, err := s.portal.AskToReturn(r.Context(), caller, invoiceID,
		req.Kind, req.Reason, req.Items)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"request": out})
}

// --- what a supplier sees --------------------------------------------------

func (s *Server) handlePortalSupplierHome(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierHome(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"home": out})
}

func (s *Server) handlePortalSupplierOrders(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierOrders(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePortalSupplierOrder(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "orderID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That order was not found."))
		return
	}
	out, err := s.portal.SupplierOrder(r.Context(), caller, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"order": out})
}

type respondToOrderRequest struct {
	Response   string `json:"response"`
	Comment    string `json:"comment"`
	PromisedOn string `json:"promised_on"`
}

func (s *Server) handlePortalRespondToOrder(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "orderID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That order was not found."))
		return
	}
	var req respondToOrderRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.RespondToOrder(r.Context(), caller, id,
		req.Response, req.Comment, req.PromisedOn)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"order": out})
}

func (s *Server) handlePortalSupplierBills(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierBills(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePortalSupplierRFQs(
	w http.ResponseWriter, r *http.Request,
) {
	caller, err := s.portalCaller(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierRFQs(r.Context(), caller)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- the staff side --------------------------------------------------------

func (s *Server) staffPortalScope(r *http.Request) (portal.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return portal.Scope{}, err
	}
	return portal.Scope{TenantID: a.TenantID, CompanyID: companyID}, nil
}

type inviteSupplierRequest struct {
	SupplierID string `json:"supplier_id"`
	FullName   string `json:"full_name"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func (s *Server) handleListSupplierContacts(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.staffPortalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierContacts(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleInviteSupplier(w http.ResponseWriter, r *http.Request) {
	scope, err := s.staffPortalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req inviteSupplierRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, uerr := uuid.Parse(req.SupplierID)
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the supplier."))
		return
	}
	a := actor.From(r.Context())
	if err := s.portal.InviteSupplier(r.Context(), scope, a.UserID,
		supplierID, req.FullName, req.Email, req.Password); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.SupplierContacts(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRevokeSupplierContact(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.staffPortalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "contactID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That contact was not found."))
		return
	}
	if err := s.portal.RevokeSupplier(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type decideReturnRequest struct {
	Accept bool   `json:"accept"`
	Note   string `json:"note"`
}

func (s *Server) handleStaffReturnRequests(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.staffPortalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portal.PendingReturnRequests(r.Context(), scope,
		r.URL.Query().Get("open") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleDecideReturnRequest(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.staffPortalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "returnID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That request was not found."))
		return
	}
	var req decideReturnRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())
	if err := s.portal.DecideReturnRequest(r.Context(), scope, a.UserID, id,
		req.Accept, req.Note); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
