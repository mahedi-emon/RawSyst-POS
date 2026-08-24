package zatca

import (
	"context"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Submission to ZATCA, behind a seam.
//
// This file defines what submission looks like. APISubmitter in submit_api.go
// does it, using a credential stored by the onboarding flow.
//
// The seam is kept rather than collapsed because there are genuinely two
// implementations: the real client, and the refusal below for an installation
// that cannot submit at all. What there is NOT, and must never be, is a third
// that pretends to succeed — see UnavailableSubmitter for why.

// Route is which ZATCA endpoint a document goes to.
//
// Not interchangeable. B2C is REPORTED within 24 hours of issue; B2B must be
// CLEARED before it may be issued at all. Sending a document down the wrong one
// is rejected for reasons that look like a data problem.
type Route string

const (
	RouteReporting Route = "reporting"
	RouteClearance Route = "clearance"
)

// RouteFor picks the endpoint for a document type.
func RouteFor(docType string) Route {
	// A credit or debit note follows the route of the invoice it corrects, and
	// the caller resolves that before getting here — this only maps the
	// document type it is finally given.
	if docType == "standard" {
		return RouteClearance
	}
	return RouteReporting
}

// Outcome is what ZATCA said.
type Outcome string

const (
	OutcomeAccepted Outcome = "accepted"

	// A 202: stamped and valid, but with warnings that must reach the Owner
	// unaltered. Paraphrasing a compliance notice is how its meaning is lost.
	OutcomeAcceptedWithWarnings Outcome = "accepted_with_warnings"

	// A business rejection. Retrying would never succeed, so the invoice keeps
	// its ICV, moves to rejected, raises a critical alert and is corrected by
	// credit note (E1.2).
	OutcomeRejected Outcome = "rejected"

	// Anything that might succeed later: a timeout, a 5xx, DNS. Retries.
	OutcomeTransportFailure Outcome = "transport_failure"

	// Nothing was sent. Distinct from a transport failure because no request
	// left this machine — the integration is not verified.
	OutcomeNotAttempted Outcome = "not_attempted"
)

// Document being submitted.
type Submission struct {
	InvoiceUUID uuid.UUID
	ICV         int64
	Route       Route

	// EGSUnitID names the till whose credential authenticates this. Required:
	// the credential is per unit, and a submission that did not say which unit
	// could only guess.
	EGSUnitID uuid.UUID

	// InvoiceHash is the base64 SHA-256 of the canonical document. ZATCA's body
	// carries it alongside the document and checks one against the other, so an
	// empty one is rejected for a reason that reads as a hash problem rather
	// than a missing field.
	InvoiceHash string

	// SignedXML is the canonical signed UBL 2.1 DOCUMENT, as built and signed
	// on the terminal. This is what ZATCA receives.
	//
	// The server relays bytes it cannot produce: the document was signed on the
	// device with a key this process has never held and never will (E1.3, H1).
	SignedXML []byte

	// Stamp is the ECDSA signature over that document, and QRTLV the payload
	// derived from it for the receipt. Carried alongside rather than in place
	// of the document — they are three different things, and sending a
	// signature where a document belongs would post a stamp attached to
	// nothing.
	Stamp string
	QRTLV string
}

// Response is what came back.
type Response struct {
	Outcome    Outcome
	HTTPStatus int

	// Body is kept verbatim so a warning or a rejection reason reaches the
	// Owner exactly as ZATCA phrased it.
	Body []byte

	// Warnings on a 202. Surfaced, never swallowed.
	Warnings []string

	Error string
}

// Submitter sends a signed document to ZATCA.
//
// The seam. A real client implements this once the format is verified; nothing
// else in the system needs to change when it does.
type Submitter interface {
	Submit(ctx context.Context, s Submission) (Response, error)

	// Available reports whether this client can actually submit. False keeps
	// the invoice queued rather than failing it: the document is valid and the
	// obligation stands, it simply cannot be sent yet.
	Available() bool
}

// UnavailableSubmitter refuses to submit, and says why.
//
// Used when an installation is not configured to hold credentials at all, so
// there is nothing to authenticate with.
//
// There is deliberately no variant that pretends to succeed, and that
// asymmetry with DocumentHasher is the point. A placeholder HASH lets the rest
// of the system be exercised locally and is obviously fake in the database. A
// placeholder SUBMISSION would mark an invoice as reported to a tax authority
// that never received it — the one state this system must never enter by
// accident, because everything downstream then treats a legal obligation as
// discharged.
//
// So an invoice queued for submission stays queued. That is visible, alerting,
// and true.
type UnavailableSubmitter struct {
	// Reason is shown to the Owner, and should say what to configure.
	Reason string
}

func (UnavailableSubmitter) Available() bool { return false }

func (u UnavailableSubmitter) Submit(context.Context, Submission) (Response, error) {
	reason := u.Reason
	if reason == "" {
		reason = "this installation is not configured to report e-invoices"
	}
	return Response{Outcome: OutcomeNotAttempted}, errs.Newf(errs.CodeComplianceBlocked,
		"E-invoicing submission is not available: %s. Invoices remain queued "+
			"and none has been reported.", reason)
}
