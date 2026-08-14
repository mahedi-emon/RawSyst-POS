// Package errs defines the application error model.
//
// Errors carry a stable machine-readable code, an HTTP status, and a message
// written for the person who will read it. Blueprint principle: an error tells
// the user what happened, why, and what to do next — never a bare code.
//
// Internal detail (SQL text, driver errors, stack context) is attached via
// Wrap and logged, but never serialised to the client.
package errs

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable identifier a client may branch on. Codes never change once
// released; new situations get new codes.
type Code string

const (
	CodeInvalidInput    Code = "invalid_input"
	CodeUnauthenticated Code = "unauthenticated"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodeRateLimited     Code = "rate_limited"
	CodeInternal        Code = "internal"
	CodeUnavailable     Code = "unavailable"

	// Domain-specific codes. These exist because the client must be able to
	// react differently, not merely display a different string.

	// CodePeriodClosed is returned when a posting targets a closed or locked
	// fiscal period. Non-retryable: it needs a human decision.
	CodePeriodClosed Code = "period_closed"

	// CodeImmutable is returned when something attempts to modify a finalized
	// invoice, a posted journal entry, or an audit record. These are immutable
	// by law and by design; the correct path is a credit note or a reversing
	// entry.
	CodeImmutable Code = "immutable"

	// CodeAmountLimitExceeded is returned when an action exceeds the actor's
	// configured approval limit. The client should offer to request approval.
	CodeAmountLimitExceeded Code = "amount_limit_exceeded"

	// CodeComplianceBlocked is returned when an action cannot proceed for a
	// legal reason — for example finalizing a B2B standard tax invoice while
	// offline, which ZATCA requires to be cleared before it is issued.
	CodeComplianceBlocked Code = "compliance_blocked"

	// CodeUnverifiedRule is returned when a regulatory value required for the
	// operation has never been verified against its official source. Blocking
	// is deliberate: it is safer to refuse than to compute tax from a guess.
	CodeUnverifiedRule Code = "unverified_regulatory_rule"

	// CodeLimitReached is returned when a plan ceiling would be exceeded.
	CodeLimitReached Code = "plan_limit_reached"
)

// Error is the application error type.
type Error struct {
	Code    Code
	Message string            // safe to show a user
	Fields  map[string]string // per-field validation messages, optional
	cause   error             // internal detail, logged but never serialised
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// HTTPStatus maps a code to its transport status.
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case CodeInvalidInput:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden, CodeAmountLimitExceeded:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeImmutable, CodePeriodClosed, CodeLimitReached:
		return http.StatusConflict
	case CodeComplianceBlocked, CodeUnverifiedRule:
		return http.StatusUnprocessableEntity
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// New builds an error with a user-safe message.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf builds an error with a formatted user-safe message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap attaches internal detail to an error. The cause is logged, never sent.
func Wrap(cause error, code Code, message string) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// WithField attaches a per-field validation message.
func (e *Error) WithField(field, message string) *Error {
	if e.Fields == nil {
		e.Fields = make(map[string]string, 4)
	}
	e.Fields[field] = message
	return e
}

// Validation starts a field-level validation error.
func Validation(message string) *Error {
	return &Error{Code: CodeInvalidInput, Message: message}
}

// As extracts an *Error from an error chain, or nil.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// CodeOf returns the code for any error, defaulting to CodeInternal.
func CodeOf(err error) Code {
	if e := As(err); e != nil {
		return e.Code
	}
	return CodeInternal
}
