package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/workflow"
)

// The Approval Centre (D5, F1) and the notification centre (D3).
//
// # Watching and deciding are different verbs
//
// `approval.view` reaches the queue, one request, and the caller's own
// requests. `approval.decide` answers one. A requester has to be able to watch
// what they asked for without being able to grant it, which is the entire point
// of an approval, and a single permission covering both would make every
// Purchase Manager their own signatory.
//
// # Notifications carry no permission at all
//
// Every route here is scoped to the CALLER in its query — `user_id = $2` — and
// there is no parameter that could name somebody else. A permission would
// suggest that reading another person's inbox is a thing that can be granted,
// and it is not: there is no code path that would serve it.

func (s *Server) approvalScope(r *http.Request) (workflow.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return workflow.Scope{}, err
	}
	return workflow.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) notifyScope(r *http.Request) (notify.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return notify.Scope{}, err
	}
	return notify.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- the approval queue ---------------------------------------------------

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Pending(r.Context(), scope,
		strings.TrimSpace(r.URL.Query().Get("subject")))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleMyApprovals answers "what happened to the thing I asked for".
//
// A separate route from the queue rather than a filter on it, because a person
// checking on their own request should not have to read past everybody else's.
func (s *Server) handleMyApprovals(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Mine(r.Context(), scope,
		r.URL.Query().Get("include_settled") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "requestID"), "requestID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Request(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "requestID"), "requestID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Decide(r.Context(), scope, id, req.Approve, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleEscalateApprovals moves everything that has waited too long.
//
// Run on demand rather than by a timer, like the other sweeps in this product:
// a background job that silently escalated a request on a Saturday would be a
// change nobody could attribute.
func (s *Server) handleEscalateApprovals(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	moved, err := s.workflow.Escalate(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"escalated": moved})
}

// --- the rules ------------------------------------------------------------

func (s *Server) handleListApprovalRules(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Rules(r.Context(), scope,
		strings.TrimSpace(r.URL.Query().Get("subject")))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveApprovalRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Subject   string `json:"subject"`
		Condition any    `json:"condition"`
		Action    string `json:"action"`
		Steps     any    `json:"steps"`
		Escalate  *int   `json:"escalate_after_hours"`
		Priority  int    `json:"priority"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	condition, err := jsonText(req.Condition, "{}")
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"The rule's condition is not something this engine can read."))
		return
	}
	steps, err := jsonText(req.Steps, "[]")
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"The rule's steps are not something this engine can read."))
		return
	}

	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.SaveRule(r.Context(), scope, workflow.Rule{
		Name: req.Name, Subject: req.Subject, Condition: condition,
		Action: req.Action, Steps: steps, Escalate: req.Escalate,
		Priority: req.Priority,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSetApprovalRuleActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "ruleID"), "ruleID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.workflow.SetRuleActive(r.Context(), scope, id, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- cover ----------------------------------------------------------------

func (s *Server) handleListDelegations(w http.ResponseWriter, r *http.Request) {
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.workflow.Delegations(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleDelegate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromUserID string `json:"from_user_id"`
		ToUserID   string `json:"to_user_id"`
		StartsOn   string `json:"starts_on"`
		EndsOn     string `json:"ends_on"`
		Note       string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.approvalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Absent means the caller is arranging their own cover, which is the
	// common case: somebody going on leave hands their approvals on.
	from := scope.UserID
	if v := strings.TrimSpace(req.FromUserID); v != "" {
		id, e := parseUUID(v, "from_user_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		from = id
	}
	to, err := parseUUID(strings.TrimSpace(req.ToUserID), "to_user_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	starts, err := parseReportDate(strings.TrimSpace(req.StartsOn),
		"starts_on", time.Time{})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	ends, err := parseReportDate(strings.TrimSpace(req.EndsOn),
		"ends_on", time.Time{})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.workflow.Delegate(r.Context(), scope, from, to, starts, ends,
		req.Note); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- notifications --------------------------------------------------------

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.notify.Inbox(r.Context(), scope,
		r.URL.Query().Get("unread") == "true",
		atoiOr(r.URL.Query().Get("limit"), 0))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	unread, err := s.notify.Unread(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// The count travels with the list. A bell that needed a second request to
	// know its own number would show a stale one for as long as that request
	// took, which is exactly when somebody is looking at it.
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "unread": unread})
}

func (s *Server) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	unread, err := s.notify.Unread(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"unread": unread})
}

func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "notificationID"), "notificationID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.notify.MarkRead(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	cleared, err := s.notify.MarkAllRead(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cleared": cleared})
}

func (s *Server) handleGetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.notify.Preferences(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSetNotificationPreference(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		// Accepted and ignored. A screen reads the whole preference list and
		// puts one row back, so the object it sends carries in_app; refusing
		// the field would make the natural round trip fail, and silently
		// honouring it would let somebody switch off the record of a failed
		// submission. So it is read, and the service always stores true.
		InApp bool `json:"in_app"`
		Email bool `json:"email"`
		SMS   bool `json:"sms"`
		Push  bool `json:"push"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.notify.SetPreference(r.Context(), scope, notify.Preference{
		Kind: req.Kind, Email: req.Email, SMS: req.SMS, Push: req.Push,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// jsonText re-encodes a decoded JSON value back to text for a jsonb column.
//
// The rule engine stores its condition and steps as jsonb and reads them back
// as text, so the handler must not invent a shape for them: whatever the rule
// editor sent goes in unchanged, and the engine is the only thing that
// interprets it. Absent means the documented default rather than SQL null,
// because the column is NOT NULL and "no condition" means "matches always".
func jsonText(v any, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// handleAnnounce posts a notice to everybody in the company.
//
// The only write in this file that reaches an inbox other than the caller's,
// which is why it is the only one behind a permission. Addressed to nobody in
// particular — a company-wide notification — so it goes no further than the
// centre: there is no address list to email or text, and inventing one would
// message a shop's whole staff because a manager wrote a note.
func (s *Server) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Severity string `json:"severity"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A notice needs something to say."))
		return
	}

	scope, err := s.notifyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	severity := strings.TrimSpace(req.Severity)
	if severity == "" {
		severity = notify.SeverityInfo
	}
	if err := s.notify.Announce(r.Context(), scope, notify.Fact{
		Kind:     notify.KindAnnouncement,
		Severity: severity,
		Title:    req.Title,
		Body:     req.Body,
		Subject:  "announcement",
	}); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
