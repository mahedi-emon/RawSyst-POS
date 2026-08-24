package zatca

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The real submitter.
//
// # Why this can exist now, when UnverifiedSubmitter said it could not
//
// UnverifiedSubmitter refused in every environment, and its reason was
// specific rather than general: "the byte-level format is still unverified
// against ZATCA's published standard". That was true and it is no longer.
// The UBL, the canonicalisation, the XAdES signature and the QR are checked
// against ZATCA's own validator in ubl_test.go and xades_test.go, which return
// valid=true with zero warnings including for the seller address rules.
//
// So the stated blocker is discharged, and the honest thing is to submit.
//
// # What is still NOT verified, and is not pretended to be
//
// That the LIVE service accepts these documents from a REAL credential. No
// test here can establish that, because it needs an OTP only the taxpayer can
// read from their own Fatoora portal. That is the one genuinely external step,
// and nothing in this file claims otherwise: a successful test run proves this
// system speaks the published contract, not that ZATCA agreed.
//
// # Why there is still no "pretend it worked" mode
//
// The asymmetry UnverifiedSubmitter described still holds. A fake HASH is
// obviously fake in the database; a fake SUBMISSION marks an invoice as
// reported to a tax authority that never received it, and everything
// downstream then treats a legal obligation as discharged. So the sandbox
// environment posts to ZATCA's real developer portal rather than to nothing,
// and an installation with no credential reports NotAttempted and keeps the
// invoice queued.

// APISubmitter reports and clears documents using a stored credential.
type APISubmitter struct {
	creds *CredentialStore

	// env decides both the endpoint and which credential is loaded. One field,
	// because they must never disagree: authenticating with a sandbox
	// credential against the production host is an error that reads like a
	// broken integration rather than a misconfiguration.
	env Environment

	httpClient  *http.Client
	endpointFor func(Environment) string
	now         func() time.Time
}

// NewAPISubmitter builds a submitter for one environment.
func NewAPISubmitter(creds *CredentialStore, env Environment) *APISubmitter {
	return &APISubmitter{
		creds:       creds,
		env:         env,
		httpClient:  &http.Client{Timeout: 60 * time.Second},
		endpointFor: func(e Environment) string { return e.BaseURL() },
		now:         time.Now,
	}
}

// Available reports whether this installation could submit at all.
//
// A coarse check on purpose: whether a PARTICULAR till has a usable credential
// is per-invoice and answered in Submit, where the invoice can be left queued
// with a reason naming that till. Answering it here would need a unit and
// there isn't one.
func (s *APISubmitter) Available() bool {
	return s.creds != nil && s.creds.CanStoreSecrets() && s.env.Valid()
}

// Submit sends one signed document.
func (s *APISubmitter) Submit(ctx context.Context, sub Submission) (Response, error) {
	if sub.EGSUnitID.String() == "00000000-0000-0000-0000-000000000000" {
		return Response{Outcome: OutcomeNotAttempted}, errs.New(errs.CodeInternal,
			"This submission names no till, so no credential could be found for it.")
	}
	if len(sub.SignedXML) == 0 {
		return Response{Outcome: OutcomeNotAttempted}, errs.New(errs.CodeInternal,
			"This submission carries no document.")
	}
	if strings.TrimSpace(sub.InvoiceHash) == "" {
		// ZATCA's body carries the hash alongside the document and checks one
		// against the other. Sending an empty one produces a rejection whose
		// message is about the hash rather than about the missing field.
		return Response{Outcome: OutcomeNotAttempted}, errs.New(errs.CodeInternal,
			"This submission carries no invoice hash.")
	}

	credential, err := s.creds.Find(ctx, sub.EGSUnitID, s.env, KindProduction)
	if err != nil {
		// Not attempted rather than failed: the obligation stands and the
		// invoice stays queued until the till is onboarded.
		return Response{Outcome: OutcomeNotAttempted}, errs.Newf(errs.CodeComplianceBlocked,
			"This till is not onboarded with ZATCA for %s, so its invoices "+
				"cannot be reported yet. They remain queued.", s.env)
	}
	if !credential.Usable(s.now()) {
		reason := "is not usable"
		if credential.Expired(s.now()) {
			reason = "has expired"
		}
		return Response{Outcome: OutcomeNotAttempted}, errs.Newf(errs.CodeComplianceBlocked,
			"This till's ZATCA certificate %s, so its invoices cannot be "+
				"reported. They remain queued. Renew it to resume.", reason)
	}

	var out Response
	err = s.creds.withSecret(ctx, credential.ID, func(csid string, secret []byte) error {
		client, e := NewClient(Config{
			BaseURL:     s.endpointFor(s.env),
			Credentials: Credentials{BinarySecurityToken: csid, Secret: string(secret)},
			HTTPClient:  s.httpClient,
		})
		if e != nil {
			return e
		}

		req := DocumentRequest{
			InvoiceHash: sub.InvoiceHash,
			Invoice:     base64.StdEncoding.EncodeToString(sub.SignedXML),
		}

		var resp DocumentResponse
		var status int
		// Clearance for standard documents, reporting for simplified. Sending
		// one down the other is rejected for reasons that look like a data
		// problem.
		if sub.Route == RouteClearance {
			resp, status, e = client.ClearSingle(ctx, req)
		} else {
			resp, status, e = client.ReportSingle(ctx, req)
		}

		out = interpret(resp, status, e)
		return nil
	})
	if err != nil {
		return Response{Outcome: OutcomeNotAttempted}, err
	}
	return out, nil
}

// interpret turns an HTTP exchange into the outcome the chain understands.
//
// The distinction that matters is REJECTED versus TRANSPORT FAILURE, because
// they lead to opposite actions: a rejection is permanent and needs a credit
// note, a transport failure retries indefinitely. Getting it backwards either
// retries something that will never succeed, burying the alert, or abandons an
// invoice that would have gone through on the next attempt.
func interpret(resp DocumentResponse, status int, err error) Response {
	out := Response{HTTPStatus: status, Warnings: messagesIn(resp.Warnings)}

	if raw, e := json.Marshal(resp); e == nil {
		out.Body = raw
	}

	// A transport error: nothing was heard back, so nothing can be concluded
	// about whether ZATCA received it.
	if err != nil && status == 0 {
		out.Outcome = OutcomeTransportFailure
		out.Error = err.Error()
		return out
	}

	out.Outcome = DescribeStatus(status)
	if err != nil {
		out.Error = err.Error()
	} else if reasons := messagesIn(resp.Errors); len(reasons) > 0 {
		// ZATCA's own wording, kept. A shop correcting a rejection needs the
		// rule that was broken, and a paraphrase discards it.
		out.Error = strings.Join(reasons, "; ")
	}
	return out
}

// messagesIn pulls readable text out of ZATCA's warning and error arrays.
//
// The manual never gives their element shape, which is why the client carries
// them as raw JSON rather than guessing a struct and silently discarding
// whatever ZATCA actually said. So this tries the shapes that have been
// observed and, failing all of them, keeps the raw JSON verbatim. Losing a
// compliance message because it arrived in an unexpected shape is the one
// outcome that is never acceptable.
func messagesIn(raw json.RawMessage) []string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return nil
	}

	// ["...", "..."]
	var plain []string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}

	// [{"code": "...", "message": "...", "category": "..."}]
	var structured []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(raw, &structured); err == nil {
		out := make([]string, 0, len(structured))
		for _, m := range structured {
			switch {
			case m.Code != "" && m.Message != "":
				out = append(out, m.Code+": "+m.Message)
			case m.Message != "":
				out = append(out, m.Message)
			case m.Code != "":
				out = append(out, m.Code)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Unrecognised: keep it whole rather than dropping it.
	return []string{trimmed}
}

// SubmitterFrom builds the submitter an installation should use.
//
// Returns a refusal rather than an error when the deployment cannot hold
// credentials, so a misconfigured stack still starts, still queues invoices,
// and still escalates a growing backlog with a truthful reason — rather than
// failing to boot, which tends to be resolved by turning the feature off.
func SubmitterFrom(creds *CredentialStore, env Environment) Submitter {
	if !env.Valid() {
		return UnavailableSubmitter{Reason: "the configured ZATCA environment (" +
			string(env) + ") is not one ZATCA operates"}
	}
	if creds == nil || !creds.CanStoreSecrets() {
		return UnavailableSubmitter{Reason: "this installation has no data " +
			"encryption key, so it cannot hold the credential ZATCA issues " +
			"(set RAWSYST_DATA_ENCRYPTION_KEYS)"}
	}
	return NewAPISubmitter(creds, env)
}
