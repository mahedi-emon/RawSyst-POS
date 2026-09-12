// Who administers the platform (A4).
//
// Adding, correcting and disabling platform administrators. Everything here is
// Super Admin only, and every one of them writes an audit entry: these are the
// accounts that can see every tenant's billing and change what the product
// believes the law to be.
//
// See internal/identity/operators.go for why an operator's address is
// corrected in place rather than by creating a replacement.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/httpx"
)

func (s *Server) handleListOperators(w http.ResponseWriter, r *http.Request) {
	out, err := s.auth.Operators(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type addOperatorRequest struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
}

func (s *Server) handleAddOperator(w http.ResponseWriter, r *http.Request) {
	var req addOperatorRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.auth.AddOperator(r.Context(), req.Email, req.FullName)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"operator":           out.Operator,
		"temporary_password": out.TemporaryPassword,
		"detail": "Give this to them through a channel you have already verified. " +
			"They must change it when they sign in. It is shown once and is not " +
			"stored anywhere in readable form.",
	})
}

type operatorEmailRequest struct {
	Email string `json:"email"`
}

func (s *Server) handleChangeOperatorEmail(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput, "That is not a valid user id."))
		return
	}

	var req operatorEmailRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.auth.ChangeOperatorEmail(r.Context(), targetID, req.Email)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type operatorStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleSetOperatorStatus(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput, "That is not a valid user id."))
		return
	}

	var req operatorStatusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.auth.SetOperatorStatus(r.Context(), targetID, req.Status)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
