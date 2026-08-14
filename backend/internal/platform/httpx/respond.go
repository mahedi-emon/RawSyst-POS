// Package httpx holds HTTP transport concerns: response shaping, middleware
// and the authorization gate.
//
// The error envelope is stable and machine-readable, because clients branch on
// it. Internal detail never crosses this boundary — it is logged instead.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
)

// ErrorBody is the wire shape of a failure.
type ErrorBody struct {
	Code      errs.Code         `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// JSON writes a success response.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so the response cannot be corrected.
		// Log it so a serialisation bug is visible rather than silent.
		slog.Error("encode response body", slog.Any("error", err))
	}
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Error writes a failure response and logs the internal detail.
//
// Server-side faults are logged at ERROR with the full cause; client faults are
// logged at DEBUG, because a stream of 400s from a misbehaving integration
// should not drown out real incidents.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	log := logging.From(ctx)
	reqID := logging.RequestID(ctx)

	appErr := errs.As(err)
	if appErr == nil {
		appErr = errs.Wrap(err, errs.CodeInternal, "Something went wrong on our side.")
	}
	status := appErr.HTTPStatus()

	attrs := []any{
		slog.String("code", string(appErr.Code)),
		slog.Int("status", status),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	}
	if status >= http.StatusInternalServerError {
		log.Error("request failed", attrs...)
	} else {
		log.Debug("request rejected", attrs...)
	}

	JSON(w, status, errorEnvelope{Error: ErrorBody{
		Code:      appErr.Code,
		Message:   appErr.Message,
		Fields:    appErr.Fields,
		RequestID: reqID,
	}})
}

// Decode reads and validates a JSON request body.
//
// Unknown fields are rejected rather than ignored. A client sending `amout`
// instead of `amount` should be told, not silently charged zero — and on a
// financial API that distinction is the whole point.
func Decode(r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" &&
		ct != "application/json" && ct != "application/json; charset=utf-8" {
		return errs.New(errs.CodeInvalidInput, "This endpoint expects JSON.")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)) // 1 MiB
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errs.Wrap(err, errs.CodeInvalidInput,
			"The request body could not be read. Check that it is valid JSON and "+
				"contains only the documented fields.")
	}
	return nil
}
