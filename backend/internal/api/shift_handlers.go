package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The shift surface (blueprint C8, design 11 §9).
//
// These handlers carry no logic of their own. The reckoning lives in
// `internal/shift`, which had no route at all until now — a till could be
// paired, signed in and bound to an EGS unit and still not sell, because
// `sales.resolveTerminal` refuses a terminal with no open cash session and
// nothing exposed a way to open one. Every shift test passed because the
// harness called the service in-process, which is the same shape of gap that
// let rule 11 resolve an account role no chart mapped.
//
// Money crosses this boundary as a STRING, like everywhere else
// (`07-api-conventions.md` §2): an opening float or a counted drawer parsed
// into a float64 would be wrong by a hallala at exactly the moment the number
// matters, which is a variance nobody can explain.
//
// The device is never in the body. It comes from the token, for the reason the
// POS handlers give: a till that could name its own terminal could open a
// session on another till and take its takings into a drawer nobody counted.

// --- request shapes -----------------------------------------------------

type openShiftRequest struct {
	// OpeningFloat is what was counted INTO the drawer, declared rather than
	// assumed. A till that starts from an assumed float has no baseline to
	// reconcile against.
	OpeningFloat string `json:"opening_float"`

	// BlindClose withholds the expected figure from the cashier at close.
	// Whether a shop works this way is the shop's decision, taken per session
	// because a trainee and a supervisor may run the same till on the same day.
	BlindClose bool `json:"blind_close"`
}

type cashDropRequest struct {
	// Amount is signed: negative takes money out of the drawer to the vault,
	// positive puts a float back in. The service refuses zero.
	Amount string `json:"amount"`
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

type closeShiftRequest struct {
	CountedCash string `json:"counted_cash"`
	Note        string `json:"note"`
}

// --- POST /api/v1/shifts -------------------------------------------------

func (s *Server) handleOpenShift(w http.ResponseWriter, r *http.Request) {
	var req openShiftRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if a.DeviceID == uuid.Nil {
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"Only a registered till can open a session. Sign in on the terminal "+
				"itself rather than in a browser."))
		return
	}

	float, err := parseAmount(req.OpeningFloat, "opening_float", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	session, err := s.shift.Open(r.Context(), a.TenantID, a.DeviceID, a.UserID,
		float, req.BlindClose)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, session)
}

// --- GET /api/v1/shifts/current ------------------------------------------

// The till's own open session. No id is passed, so this route cannot be aimed
// at another terminal's drawer even by a caller who knows its id.
func (s *Server) handleCurrentShift(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	if a.DeviceID == uuid.Nil {
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"This session is not bound to a terminal, so it has no till to report on."))
		return
	}

	session, err := s.shift.Current(r.Context(), a.TenantID, a.DeviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, session)
}

// --- GET /api/v1/shifts/{sessionID} --------------------------------------

// What the CASHIER sees. On a blind-close till the expected figure is withheld
// until the count is committed; see the x-report route for the other half.
func (s *Server) handlePeekShift(w http.ResponseWriter, r *http.Request) {
	sessionID, err := parseUUID(chi.URLParam(r, "sessionID"), "session_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	report, err := s.shift.Peek(r.Context(), a.TenantID, sessionID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

// --- GET /api/v1/shifts/{sessionID}/x-report -----------------------------

// A mid-shift snapshot for a supervisor. Closes nothing and may be taken as
// often as they like, which is why it is a GET.
func (s *Server) handleXReport(w http.ResponseWriter, r *http.Request) {
	sessionID, err := parseUUID(chi.URLParam(r, "sessionID"), "session_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	report, err := s.shift.XReport(r.Context(), a.TenantID, sessionID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

// --- POST /api/v1/shifts/{sessionID}/cash-drop ---------------------------

func (s *Server) handleCashDrop(w http.ResponseWriter, r *http.Request) {
	var req cashDropRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	sessionID, err := parseUUID(chi.URLParam(r, "sessionID"), "session_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := parseAmount(req.Amount, "amount", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if err := s.shift.RecordMovement(r.Context(), a.TenantID, sessionID, a.UserID,
		amount, req.Reason, req.Note); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// --- POST /api/v1/shifts/{sessionID}/close -------------------------------

// The Z report. A POST rather than a GET because it closes the session, and it
// may happen exactly once — a second one would either double-count the takings
// or overwrite the first count.
func (s *Server) handleCloseShift(w http.ResponseWriter, r *http.Request) {
	var req closeShiftRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	sessionID, err := parseUUID(chi.URLParam(r, "sessionID"), "session_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	counted, err := parseAmount(req.CountedCash, "counted_cash", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	report, err := s.shift.Close(r.Context(), a.TenantID, sessionID, a.UserID,
		counted, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}
