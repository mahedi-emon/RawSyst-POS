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
	"github.com/jackc/pgx/v5"
)

// Onboarding is tested against an httptest server speaking ZATCA's documented
// contract, not against a mock of our own client.
//
// The difference matters. A mock would return whatever this file told it to,
// and would keep agreeing after a refactor broke the header, the body shape or
// the response parsing. Here the REAL client code makes a REAL HTTP request,
// and the server asserts what ZATCA's manual says it should receive -- the OTP
// header on the compliance call, Basic auth on the production call, the CSR in
// the documented field.
//
// What this does NOT do is claim the live service behaves this way. It proves
// this system holds up its end of the published contract; only a real
// credential can prove the other end agrees, and that is called out as the one
// genuinely external step.

// fakeZATCA records what it was sent and answers as the manual says.
type fakeZATCA struct {
	server *httptest.Server

	// What the last request carried, for assertions.
	otpHeader     string
	authHeader    string
	complianceCSR string
	productionCSR string

	// What to answer with.
	complianceStatus int
	productionStatus int
	failureBody      string
}

// certificateFor builds a DER certificate with a known expiry, so the stored
// expiry can be checked against a value the test chose.
func certificateFor(t *testing.T, life time.Duration) []byte {
	t.Helper()
	signer, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	cert, err := SelfSignedDevelopmentCertificate(signer, life)
	if err != nil {
		t.Fatalf("making a certificate: %v", err)
	}
	return cert.DER
}

func newFakeZATCA(t *testing.T, certDER []byte) *fakeZATCA {
	t.Helper()
	z := &fakeZATCA{
		complianceStatus: http.StatusOK,
		productionStatus: http.StatusOK,
	}

	mux := http.NewServeMux()

	reply := func(w http.ResponseWriter, status int, csid, secret string, body string) {
		if body != "" {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(CSIDResponse{
			RequestID:           123456,
			TokenType:           "urn:oasis:names:tc:SAML:1.0:assertion",
			DispositionMessage:  "ISSUED",
			BinarySecurityToken: csid,
			Secret:              secret,
		})
	}

	mux.HandleFunc(PathComplianceCSID, func(w http.ResponseWriter, r *http.Request) {
		z.otpHeader = r.Header.Get(HeaderOTP)
		z.authHeader = r.Header.Get("Authorization")

		var body struct {
			CSR string `json:"csr"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		z.complianceCSR = body.CSR

		if z.complianceStatus != http.StatusOK {
			reply(w, z.complianceStatus, "", "", z.failureBody)
			return
		}
		reply(w, http.StatusOK,
			base64.StdEncoding.EncodeToString(certDER), "compliance-secret", "")
	})

	mux.HandleFunc(PathProductionCSID, func(w http.ResponseWriter, r *http.Request) {
		z.authHeader = r.Header.Get("Authorization")
		z.otpHeader = r.Header.Get(HeaderOTP)

		var body struct {
			CSR string `json:"csr"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		z.productionCSR = body.CSR

		if z.productionStatus != http.StatusOK {
			reply(w, z.productionStatus, "", "", z.failureBody)
			return
		}
		reply(w, http.StatusOK,
			base64.StdEncoding.EncodeToString(certDER), "production-secret", "")
	})

	z.server = httptest.NewServer(mux)
	t.Cleanup(z.server.Close)
	return z
}

// onboardingFor wires the service at the fake ZATCA.
func onboardingFor(t *testing.T, f *fixture, z *fakeZATCA) *Onboarding {
	t.Helper()
	o := NewOnboarding(f.pool, NewCredentialStore(f.pool, testCipher(t)))
	o.endpointFor = func(Environment) string { return z.server.URL }
	o.httpClient = z.server.Client()
	return o
}

const testCSR = "-----BEGIN CERTIFICATE REQUEST-----\nMIIB\n-----END CERTIFICATE REQUEST-----"

func TestComplianceOnboardingStoresWhatZATCAReturned(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	result, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), "123456")
	if err != nil {
		t.Fatalf("onboarding: %v", err)
	}

	if result.RequestID != 123456 {
		t.Errorf("request id is %d", result.RequestID)
	}
	if result.Credential.Status != StatusIssued {
		t.Errorf("status is %q, want issued", result.Credential.Status)
	}
	if result.Credential.ExpiresAt == nil {
		t.Error("no expiry was recorded, so renewal has nothing to work from")
	}
	if len(result.Credential.Certificate) == 0 {
		t.Error("no certificate was stored")
	}
}

// The manual puts the OTP in its own header and sends NO credentials on this
// call -- it is how a solution obtains them in the first place.
func TestTheComplianceCallCarriesTheOTPAndNoAuthorization(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	if _, err := o.RequestComplianceCSID(f.asTenant(), f.unitID,
		EnvironmentSimulation, []byte(testCSR), "654321"); err != nil {
		t.Fatalf("onboarding: %v", err)
	}

	if z.otpHeader != "654321" {
		t.Errorf("the OTP header carried %q, want 654321", z.otpHeader)
	}
	if z.authHeader != "" {
		t.Errorf("the compliance call sent an Authorization header (%q); it has "+
			"no credentials yet, which is the point of the OTP", z.authHeader)
	}
	if z.complianceCSR == "" {
		t.Error("no CSR reached ZATCA")
	}
}

// Promotion authenticates with the compliance credential and sends NO OTP:
// the compliance CSID is itself the proof one was presented.
func TestPromotionAuthenticatesWithTheComplianceCredential(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	compliance, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), "123456")
	if err != nil {
		t.Fatalf("compliance onboarding: %v", err)
	}

	if _, err := o.RequestProductionCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR)); err != nil {
		t.Fatalf("promotion: %v", err)
	}

	if z.otpHeader != "" {
		t.Errorf("the production call sent an OTP header (%q); it authenticates "+
			"with the compliance credential instead", z.otpHeader)
	}
	want := Credentials{
		BinarySecurityToken: compliance.Credential.CSID,
		Secret:              "compliance-secret",
	}.Authorization()
	if z.authHeader != want {
		t.Error("the production call did not authenticate with the compliance " +
			"credential")
	}
}

// A unit that never completed compliance must not be promotable.
func TestPromotionIsRefusedBeforeCompliance(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	_, err := o.RequestProductionCSID(f.asTenant(), f.unitID,
		EnvironmentSimulation, []byte(testCSR))
	if err == nil {
		t.Fatal("a till with no compliance credential was promoted to production")
	}
	if !strings.Contains(err.Error(), "one-time password") {
		t.Errorf("the error does not say what is needed: %v", err)
	}
	if z.productionCSR != "" {
		t.Error("a production request was sent to ZATCA anyway")
	}
}

// A mistyped OTP must cost nothing: no request, no attempt counted.
func TestAMistypedOTPIsCaughtBeforeAnythingIsSent(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	for _, bad := range []string{"", "12345", "1234567", "12345a", "abcdef"} {
		_, err := o.RequestComplianceCSID(f.asTenant(), f.unitID,
			EnvironmentSimulation, []byte(testCSR), bad)
		if err == nil {
			t.Errorf("the one-time password %q was accepted", bad)
		}
	}
	if z.complianceCSR != "" {
		t.Error("a malformed one-time password still produced a request to ZATCA")
	}
}

// When ZATCA refuses, its wording is kept -- the detail that names which of
// the nine CSR fields was wrong is exactly what a paraphrase discards.
func TestARefusalKeepsWhatZATCASaid(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	z.complianceStatus = http.StatusBadRequest
	z.failureBody = `{"errors":[{"code":"CSR-001","message":` +
		`"organizationIdentifier must be 15 digits starting and ending with 3"}]}`
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	if _, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), "123456"); err == nil {
		t.Fatal("a refused onboarding reported success")
	}

	status, err := o.Status(ctx, f.unitID, EnvironmentSimulation, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	if status.Compliance == nil {
		t.Fatal("the failed attempt was not recorded")
	}
	if status.Compliance.Status != StatusFailed {
		t.Errorf("status is %q, want failed", status.Compliance.Status)
	}
	if !strings.Contains(status.Compliance.LastError, "organizationIdentifier") {
		t.Errorf("ZATCA's reason was not kept: %q", status.Compliance.LastError)
	}
	if status.Connected {
		t.Error("a till whose onboarding was refused reports itself connected")
	}
}

// The one-time password must never reach the database.
func TestTheOneTimePasswordIsNeverStored(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	const otp = "987654"
	if _, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), otp); err != nil {
		t.Fatalf("onboarding: %v", err)
	}

	// Every text column on the row, concatenated, must not contain it.
	var haystack string
	if err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT coalesce(csr,'') || ' ' || coalesce(csid,'') || ' ' ||
			       coalesce(last_error,'') || ' ' || coalesce(status,'')
			  FROM zatca_credential WHERE egs_unit_id = $1`, f.unitID).Scan(&haystack)
	}); err != nil {
		t.Fatalf("reading the row: %v", err)
	}
	if strings.Contains(haystack, otp) {
		t.Error("the one-time password was written to the database; it is " +
			"single-use and short-lived, and storing it is pure liability")
	}
}

// Sandbox onboarding must not touch the production credential.
func TestOnboardingOneEnvironmentLeavesTheOtherAlone(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	for _, env := range []Environment{EnvironmentProduction, EnvironmentSimulation} {
		if _, err := o.RequestComplianceCSID(ctx, f.unitID, env,
			[]byte(testCSR), "123456"); err != nil {
			t.Fatalf("onboarding %s: %v", env, err)
		}
	}

	for _, env := range []Environment{EnvironmentProduction, EnvironmentSimulation} {
		s, err := o.Status(ctx, f.unitID, env, 30*24*time.Hour)
		if err != nil {
			t.Fatalf("status for %s: %v", env, err)
		}
		if s.Compliance == nil || s.Compliance.Status != StatusIssued {
			t.Errorf("%s lost its compliance credential when the other was onboarded", env)
		}
	}
}

// "Connected" must mean production, not compliance. Saying it for a compliance
// CSID would be a lie a shop acts on.
func TestConnectedMeansProductionNotCompliance(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	if _, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), "123456"); err != nil {
		t.Fatalf("compliance: %v", err)
	}

	s, err := o.Status(ctx, f.unitID, EnvironmentSimulation, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s.Connected {
		t.Error("a till with only a compliance credential reports itself connected")
	}
	if !strings.Contains(s.NextAction, "Promote") {
		t.Errorf("the next action does not point at promotion: %q", s.NextAction)
	}

	if _, err := o.RequestProductionCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR)); err != nil {
		t.Fatalf("promotion: %v", err)
	}

	s, err = o.Status(ctx, f.unitID, EnvironmentSimulation, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !s.Connected {
		t.Error("a till with a usable production credential is not connected")
	}
}

// A certificate near expiry must be flagged before it lapses, not after.
func TestRenewalIsFlaggedBeforeExpiry(t *testing.T) {
	f := newFixture(t)
	// Ten days of life, asked about with a thirty-day window.
	z := newFakeZATCA(t, certificateFor(t, 10*24*time.Hour))
	o := onboardingFor(t, f, z)
	ctx := f.asTenant()

	if _, err := o.RequestComplianceCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR), "123456"); err != nil {
		t.Fatalf("compliance: %v", err)
	}
	if _, err := o.RequestProductionCSID(ctx, f.unitID,
		EnvironmentSimulation, []byte(testCSR)); err != nil {
		t.Fatalf("promotion: %v", err)
	}

	s, err := o.Status(ctx, f.unitID, EnvironmentSimulation, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !s.NeedsRenewal {
		t.Error("a certificate with ten days left is not flagged for renewal at thirty")
	}
	if !s.Connected {
		t.Error("a certificate that has not expired yet is already disconnected")
	}
	if !strings.Contains(s.NextAction, "expires soon") {
		t.Errorf("the next action does not mention the coming expiry: %q", s.NextAction)
	}

	// And with a one-day window it is not yet urgent.
	s, err = o.Status(ctx, f.unitID, EnvironmentSimulation, 24*time.Hour)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s.NeedsRenewal {
		t.Error("a certificate with ten days left is flagged for renewal at one day")
	}
}

// A till nobody has onboarded reads as not-onboarded and says what to do.
func TestAnUnonboardedTillSaysWhatToDo(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	s, err := o.Status(f.asTenant(), f.unitID, EnvironmentSimulation, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s.Compliance != nil || s.Production != nil {
		t.Error("a till nobody onboarded has credentials")
	}
	if s.Connected {
		t.Error("a till nobody onboarded reports itself connected")
	}
	if !strings.Contains(s.NextAction, "one-time password") {
		t.Errorf("the next action does not mention the OTP: %q", s.NextAction)
	}
}

// An unknown environment is refused rather than defaulting to one, because
// defaulting could point production traffic at a sandbox.
func TestAnUnknownEnvironmentIsRefused(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	if _, err := o.RequestComplianceCSID(f.asTenant(), f.unitID,
		Environment("live"), []byte(testCSR), "123456"); err == nil {
		t.Error("an unknown environment was accepted")
	}
	if _, err := o.Status(f.asTenant(), f.unitID, Environment("live"), time.Hour); err == nil {
		t.Error("status was reported for an unknown environment")
	}
}

// Onboarding a till that does not exist must not create a credential for it.
func TestOnboardingAnUnknownTillIsRefused(t *testing.T) {
	f := newFixture(t)
	z := newFakeZATCA(t, certificateFor(t, 365*24*time.Hour))
	o := onboardingFor(t, f, z)

	_, err := o.RequestComplianceCSID(f.asTenant(), uuid.New(),
		EnvironmentSimulation, []byte(testCSR), "123456")
	if err == nil {
		t.Fatal("a credential was created for a till that does not exist")
	}
	if z.complianceCSR != "" {
		t.Error("a request was sent to ZATCA for a till that does not exist")
	}
}
