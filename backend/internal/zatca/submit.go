package zatca

import (
	"context"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Submission to ZATCA, behind a seam.
//
// The transport is not implemented, and that is deliberate rather than
// unfinished. Submitting requires the signed UBL 2.1 document, whose byte-level
// format is still unverified against ZATCA's published standard — the same
// blocker that keeps DocumentHasher stubbed. A client that posted something
// plausible would get invoices rejected at scale, and an invoice ZATCA rejected
// still consumed its ICV.
//
// So this file defines what submission looks like and gates the doing of it.
// When the verification pass completes, a real client implements Submitter and
// nothing else in the system changes.

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

	// SignedXML is the document as the TERMINAL signed it. The server relays
	// bytes it cannot produce: the stamp was made on the device with a key this
	// process has never held and never will (E1.3, H1).
	SignedXML []byte
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

// SubmitterFor returns the client appropriate to the environment.
//
// The same gate as HasherFor, and for the same reason. An unverified document
// format and an unimplemented transport are one problem, and they must fail the
// same way rather than one of them quietly succeeding.
func SubmitterFor(isProduction bool) Submitter {
	return UnverifiedSubmitter{production: isProduction}
}

// UnverifiedSubmitter refuses to submit, in every environment.
//
// There is no development variant that pretends to succeed, and that asymmetry
// with DocumentHasher is deliberate. A placeholder HASH lets the rest of the
// system be exercised locally and is obviously fake in the database. A
// placeholder SUBMISSION would mark an invoice as reported to a tax authority
// that never received it — the one state this system must never enter by
// accident, because everything downstream then treats a legal obligation as
// discharged.
//
// So an invoice queued for submission stays queued until a verified client
// exists. That is visible, alerting, and true.
type UnverifiedSubmitter struct{ production bool }

func (UnverifiedSubmitter) Available() bool { return false }

func (u UnverifiedSubmitter) Submit(context.Context, Submission) (Response, error) {
	return Response{Outcome: OutcomeNotAttempted}, errs.New(errs.CodeComplianceBlocked,
		"E-invoicing submission is not yet available on this installation: the "+
			"document format has not been verified against ZATCA's published "+
			"standard. Invoices remain queued and none has been reported.")
}
