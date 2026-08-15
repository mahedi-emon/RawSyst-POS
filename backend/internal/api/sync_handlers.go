package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
)

// The sync surface.
//
// One route. A terminal that has been trading offline pushes everything it
// queued, in the order it happened, and gets back a per-item verdict plus the
// cursor it may discard up to.

type pushRequest struct {
	IdempotencyKey string      `json:"idempotency_key"`
	Items          []sync.Item `json:"items"`
}

// handleSyncPush replays a terminal's offline queue.
//
// The device is taken from the TOKEN, never from the body. A terminal that
// could name its own device id would push another till's sales onto another
// till's ZATCA chain — and because both tills belong to the same tenant,
// row-level security would not notice.
func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if a.DeviceID == uuid.Nil {
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"Only a registered terminal can push a sync batch. This session is "+
				"not bound to one."))
		return
	}

	if req.IdempotencyKey == "" {
		// Without it a retried push is indistinguishable from a second one. The
		// per-sale invoice UUID would still stop duplicate invoices, but the
		// batch bookkeeping would double-count and the device would be told
		// its takings landed twice.
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A sync batch must carry an idempotency key so a retry can be "+
				"recognised."))
		return
	}

	out, err := s.sync.Push(r.Context(), a.TenantID, sync.Batch{
		DeviceID:       a.DeviceID,
		IdempotencyKey: req.IdempotencyKey,
		Items:          req.Items,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// 200 even when some items failed. The batch itself was processed; the
	// per-item verdicts say what landed and what did not, and a device that saw
	// a 4xx would retry the whole batch including the sales that succeeded.
	httpx.JSON(w, http.StatusOK, out)
}

// handleSyncHealth reports what a terminal still has outstanding.
//
// A cashier closing a till needs to know whether the day's takings have
// reached the server. "23 invoices still queued" is the difference between
// going home and staying to investigate.
func (s *Server) handleSyncHealth(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	if a.DeviceID == uuid.Nil {
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"This session is not bound to a terminal."))
		return
	}

	out, err := s.sync.HealthFor(r.Context(), a.TenantID, a.DeviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
