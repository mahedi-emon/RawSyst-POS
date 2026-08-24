//go:build integration

package zatca

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeFatoora answers the reporting and clearance endpoints.
type fakeFatoora struct {
	server *httptest.Server

	// What arrived.
	path        string
	authHeader  string
	invoiceB64  string
	invoiceHash string
	requests    int

	// What to answer with.
	status int
	body   string
}

func newFakeFatoora(t *testing.T) *fakeFatoora {
	t.Helper()
	z := &fakeFatoora{status: http.StatusOK}

	handler := func(w http.ResponseWriter, r *http.Request) {
		z.requests++
		z.path = r.URL.Path
		z.authHeader = r.Header.Get("Authorization")

		var body DocumentRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		z.invoiceB64 = body.Invoice
		z.invoiceHash = body.InvoiceHash

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(z.status)
		if z.body != "" {
			_, _ = w.Write([]byte(z.body))
			return
		}
		_ = json.NewEncoder(w).Encode(DocumentResponse{
			InvoiceHash: body.InvoiceHash,
			Status:      "Reported",
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc(PathReportingSingle, handler)
	mux.HandleFunc(PathClearanceSingle, handler)

	z.server = httptest.NewServer(mux)
	t.Cleanup(z.server.Close)
	return z
}

// submitterFor wires an APISubmitter at the fake, with a credential in place.
func submitterFor(t *testing.T, f *fixture, z *fakeFatoora) *APISubmitter {
	t.Helper()
	store := NewCredentialStore(f.pool, testCipher(t))
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"PROD-CSID", []byte("prod-secret"),
		ptr(time.Now().Add(365*24*time.Hour)))

	s := NewAPISubmitter(store, EnvironmentSimulation)
	s.endpointFor = func(Environment) string { return z.server.URL }
	s.httpClient = z.server.Client()
	return s
}

func ptr(t time.Time) *time.Time { return &t }

func aSubmission(f *fixture, route Route) Submission {
	return Submission{
		InvoiceUUID: uuid.New(),
		ICV:         42,
		Route:       route,
		EGSUnitID:   f.unitID,
		SignedXML:   []byte("<Invoice>signed</Invoice>"),
		InvoiceHash: "QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=",
		Stamp:       "stamp",
		QRTLV:       "qr",
	}
}

func TestAReportedInvoiceIsSentWhereTheManualSays(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	s := submitterFor(t, f, z)

	resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	if resp.Outcome != OutcomeAccepted {
		t.Errorf("outcome is %q, want accepted", resp.Outcome)
	}
	if z.path != PathReportingSingle {
		t.Errorf("posted to %s, want %s", z.path, PathReportingSingle)
	}

	// The DOCUMENT, base64, not the stamp.
	decoded, err := base64.StdEncoding.DecodeString(z.invoiceB64)
	if err != nil {
		t.Fatalf("the invoice field is not base64: %v", err)
	}
	if string(decoded) != "<Invoice>signed</Invoice>" {
		t.Errorf("the document that arrived was %q", decoded)
	}
	if z.invoiceHash == "" {
		t.Error("no invoice hash was sent; ZATCA checks the document against it")
	}
}

// Standard invoices are CLEARED before issue; simplified ones are REPORTED
// within 24 hours. Sending one down the other is rejected for reasons that
// look like a data problem.
func TestStandardInvoicesAreClearedAndSimplifiedOnesReported(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	s := submitterFor(t, f, z)

	for _, c := range []struct {
		route Route
		path  string
	}{
		{RouteClearance, PathClearanceSingle},
		{RouteReporting, PathReportingSingle},
	} {
		if _, err := s.Submit(f.asTenant(), aSubmission(f, c.route)); err != nil {
			t.Fatalf("submitting %s: %v", c.route, err)
		}
		if z.path != c.path {
			t.Errorf("%s went to %s, want %s", c.route, z.path, c.path)
		}
	}
}

// The submission authenticates with the stored credential.
func TestSubmissionAuthenticatesWithTheStoredCredential(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	s := submitterFor(t, f, z)

	if _, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting)); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	want := Credentials{
		BinarySecurityToken: "PROD-CSID", Secret: "prod-secret",
	}.Authorization()
	if z.authHeader != want {
		t.Error("the submission did not authenticate with the stored credential")
	}
}

// A till nobody onboarded must leave the invoice QUEUED, not failed. The
// obligation stands; it simply cannot be discharged yet.
func TestAnUnonboardedTillLeavesTheInvoiceQueued(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)

	store := NewCredentialStore(f.pool, testCipher(t))
	s := NewAPISubmitter(store, EnvironmentSimulation)
	s.endpointFor = func(Environment) string { return z.server.URL }
	s.httpClient = z.server.Client()

	resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if err == nil {
		t.Fatal("submitting from an unonboarded till reported success")
	}
	if resp.Outcome != OutcomeNotAttempted {
		t.Errorf("outcome is %q, want not_attempted", resp.Outcome)
	}
	if z.requests != 0 {
		t.Error("a request was sent to ZATCA with no credential to sign it")
	}
	if !strings.Contains(err.Error(), "remain queued") {
		t.Errorf("the error does not say the invoice is still queued: %v", err)
	}
}

// An expired certificate must do the same, and say so specifically -- "not
// onboarded" would send somebody to the wrong screen.
func TestAnExpiredCertificateLeavesTheInvoiceQueuedAndSaysSo(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)

	store := NewCredentialStore(f.pool, testCipher(t))
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"PROD-CSID", []byte("prod-secret"), ptr(time.Now().Add(-time.Hour)))

	s := NewAPISubmitter(store, EnvironmentSimulation)
	s.endpointFor = func(Environment) string { return z.server.URL }
	s.httpClient = z.server.Client()

	resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if err == nil {
		t.Fatal("an expired certificate submitted anyway")
	}
	if resp.Outcome != OutcomeNotAttempted {
		t.Errorf("outcome is %q, want not_attempted", resp.Outcome)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("the error does not name expiry as the cause: %v", err)
	}
	if z.requests != 0 {
		t.Error("an expired credential was still used to make a request")
	}
}

// A business rejection is PERMANENT and must be distinguishable from a
// transport failure -- they lead to opposite actions.
func TestARejectionIsPermanentAndKeepsZATCAsWording(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	z.status = http.StatusBadRequest
	z.body = `{"validationResults":{"errorMessages":[]},` +
		`"errors":[{"code":"BR-KSA-09","message":"Seller address is incomplete"}]}`
	s := submitterFor(t, f, z)

	resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if err != nil {
		t.Fatalf("a rejection should be reported through the response, not an error: %v", err)
	}
	if resp.Outcome == OutcomeTransportFailure {
		t.Error("a business rejection was classed as a transport failure; it " +
			"would retry forever and never succeed")
	}
	if !strings.Contains(resp.Error, "BR-KSA-09") {
		t.Errorf("ZATCA's rule reference was lost: %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "Seller address is incomplete") {
		t.Errorf("ZATCA's wording was lost: %q", resp.Error)
	}
}

// An unreachable ZATCA is a TRANSPORT failure, which retries.
func TestAnUnreachableZATCAIsATransportFailure(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	s := submitterFor(t, f, z)
	// Point at a closed port.
	z.server.Close()

	resp, _ := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if resp.Outcome != OutcomeTransportFailure {
		t.Errorf("outcome is %q, want transport_failure -- an unreachable "+
			"service must retry rather than abandon the invoice", resp.Outcome)
	}
}

// Warnings on an otherwise-good response must survive, whatever shape they
// arrive in. Losing a compliance warning is never acceptable.
func TestWarningsSurviveWhateverShapeTheyArriveIn(t *testing.T) {
	for name, body := range map[string]string{
		"plain strings": `{"status":"Reported","warnings":["BR-KSA-10 is advisory"]}`,
		"objects": `{"status":"Reported","warnings":` +
			`[{"code":"BR-KSA-10","message":"is advisory"}]}`,
		"unexpected shape": `{"status":"Reported","warnings":{"odd":"shape"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			z := newFakeFatoora(t)
			z.body = body
			s := submitterFor(t, f, z)

			resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
			if err != nil {
				t.Fatalf("submitting: %v", err)
			}
			if len(resp.Warnings) == 0 {
				t.Error("the warning was dropped")
			}
		})
	}
}

// A submission with nothing to send must not reach ZATCA.
func TestAnIncompleteSubmissionIsRefusedBeforeItIsSent(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)
	s := submitterFor(t, f, z)
	ctx := f.asTenant()

	base := aSubmission(f, RouteReporting)

	noDocument := base
	noDocument.SignedXML = nil
	if _, err := s.Submit(ctx, noDocument); err == nil {
		t.Error("a submission with no document was sent")
	}

	noHash := base
	noHash.InvoiceHash = ""
	if _, err := s.Submit(ctx, noHash); err == nil {
		t.Error("a submission with no invoice hash was sent")
	}

	noUnit := base
	noUnit.EGSUnitID = uuid.Nil
	if _, err := s.Submit(ctx, noUnit); err == nil {
		t.Error("a submission naming no till was sent")
	}

	if z.requests != 0 {
		t.Errorf("%d incomplete submissions still reached ZATCA", z.requests)
	}
}

// An installation with no encryption key cannot submit, and says what to set.
// It must NOT pretend to succeed.
func TestWithoutAKeyTheSubmitterRefusesRatherThanPretending(t *testing.T) {
	f := newFixture(t)
	s := SubmitterFrom(NewCredentialStore(f.pool, nil), EnvironmentSimulation)

	if s.Available() {
		t.Error("a submitter with no encryption key reports itself available")
	}
	resp, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting))
	if err == nil {
		t.Fatal("a submitter with no encryption key reported success")
	}
	if resp.Outcome != OutcomeNotAttempted {
		t.Errorf("outcome is %q, want not_attempted", resp.Outcome)
	}
	if !strings.Contains(err.Error(), "RAWSYST_DATA_ENCRYPTION_KEYS") {
		t.Errorf("the error does not say what to configure: %v", err)
	}
}

// A misconfigured environment must refuse rather than fall back to one, since
// a fallback could point real invoices at a sandbox or vice versa.
func TestAnUnknownEnvironmentRefusesToSubmit(t *testing.T) {
	f := newFixture(t)
	s := SubmitterFrom(NewCredentialStore(f.pool, testCipher(t)), Environment("live"))

	if s.Available() {
		t.Error("a submitter for an unknown environment reports itself available")
	}
	if _, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting)); err == nil {
		t.Error("a submitter for an unknown environment submitted anyway")
	}
}

// A credential stored for one environment must not authenticate against
// another. Reporting real invoices with a sandbox credential, or the reverse,
// is the failure this separation exists to prevent.
func TestACredentialFromAnotherEnvironmentIsNotUsed(t *testing.T) {
	f := newFixture(t)
	z := newFakeFatoora(t)

	store := NewCredentialStore(f.pool, testCipher(t))
	// Onboarded for SIMULATION only.
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"SIM-CSID", []byte("sim-secret"), ptr(time.Now().Add(time.Hour)))

	// But the submitter is configured for PRODUCTION.
	s := NewAPISubmitter(store, EnvironmentProduction)
	s.endpointFor = func(Environment) string { return z.server.URL }
	s.httpClient = z.server.Client()

	if _, err := s.Submit(f.asTenant(), aSubmission(f, RouteReporting)); err == nil {
		t.Fatal("a simulation credential was used to submit to production")
	}
	if z.requests != 0 {
		t.Error("a request was made with a credential from another environment")
	}
}
