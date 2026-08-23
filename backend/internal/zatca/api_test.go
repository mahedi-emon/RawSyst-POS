package zatca

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the wire format against the manual, not against ZATCA.
//
// Nothing here proves ZATCA accepts what we send — only the sandbox can do
// that, and reaching it needs credentials this project does not have. What they
// do prove is that the client sends exactly what the Developer Portal Manual
// describes, so that when credentials arrive the failure surface is ZATCA's
// behaviour rather than our transcription.

// capture records the one request a test server received.
type capture struct {
	method  string
	path    string
	headers http.Header
	body    []byte
}

func serve(t *testing.T, status int, response string) (*Client, *capture) {
	t.Helper()
	got := &capture{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.headers = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Config{
		BaseURL: srv.URL,
		Credentials: Credentials{
			BinarySecurityToken: "TUlJRDZq",
			Secret:              "f9YRhopN/G7x0TECOY6nKSCHLNY1b5riAHSFPICo4qw=",
		},
	})
	if err != nil {
		t.Fatalf("configuring the client: %v", err)
	}
	return client, got
}

func TestTheReportingCallGoesWhereTheManualSaysItDoes(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{"invoiceHash":"h","status":"Reported"}`)

	_, status, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=",
		Invoice:     "PEludm9pY2U+",
	})
	if err != nil {
		t.Fatalf("reporting: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	// "a POST Method on endpoint /invoices/reporting/single".
	if got.path != PathReportingSingle {
		t.Errorf("path = %s, want %s", got.path, PathReportingSingle)
	}
}

func TestTheClearanceCallGoesToItsOwnEndpoint(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{"invoiceHash":"h","status":"Cleared"}`)

	// Reporting and clearance are not interchangeable: a standard invoice must
	// be cleared BEFORE it may be issued, a simplified one is reported after.
	// Sending one down the other's endpoint fails for reasons that read like a
	// data problem.
	if _, _, err := client.ClearSingle(context.Background(), DocumentRequest{
		InvoiceHash: "h", Invoice: "PEludm9pY2U+",
	}); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if got.path != PathClearanceSingle {
		t.Errorf("path = %s, want %s", got.path, PathClearanceSingle)
	}
}

func TestTheCallCarriesTheHeadersTheManualRequires(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{}`)

	if _, _, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "h", Invoice: "PEludm9pY2U+",
	}); err != nil {
		t.Fatalf("reporting: %v", err)
	}

	// "An additional accept-version: v2 header must be added to V2 API calls."
	if v := got.headers.Get(HeaderAcceptVersion); v != AcceptVersionV2 {
		t.Errorf("%s = %q, want %q", HeaderAcceptVersion, v, AcceptVersionV2)
	}
	// "pass it on 'authentication-certificate' and accept-language as a
	// parameter in the header."
	if got.headers.Get(HeaderAuthenticationCertificate) == "" {
		t.Errorf("%s was not sent", HeaderAuthenticationCertificate)
	}
	if got.headers.Get(HeaderAcceptLanguage) == "" {
		t.Errorf("%s was not sent", HeaderAcceptLanguage)
	}
}

func TestTheAuthorizationHeaderIsTheTokenAndTheSecret(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{}`)

	if _, _, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "h", Invoice: "PEludm9pY2U+",
	}); err != nil {
		t.Fatalf("reporting: %v", err)
	}

	// "{Base64 Encoded String} = ... the CSID, a Colon and the Secret encoded
	// with Base64 (CSID:Secret)".
	value := got.headers.Get("Authorization")
	encoded, ok := strings.CutPrefix(value, "Basic ")
	if !ok {
		t.Fatalf("Authorization = %q, want a Basic credential", value)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the credential is not base64: %v", err)
	}
	want := "TUlJRDZq:f9YRhopN/G7x0TECOY6nKSCHLNY1b5riAHSFPICo4qw="
	if string(decoded) != want {
		t.Errorf("credential = %q, want %q", decoded, want)
	}
}

func TestTheBodyCarriesTheTwoFieldsTheManualNames(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{}`)

	if _, _, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=",
		Invoice:     "PEludm9pY2U+",
	}); err != nil {
		t.Fatalf("reporting: %v", err)
	}

	// "The body object should Contain 2 Values: the first one is called
	// 'invoiceHash' and the second one is called 'invoice'."
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	if len(body) != 2 {
		t.Errorf("the body has %d fields, want exactly 2: %s", len(body), got.body)
	}
	if body["invoiceHash"] != "QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=" {
		t.Errorf("invoiceHash = %v", body["invoiceHash"])
	}
	if body["invoice"] != "PEludm9pY2U+" {
		t.Errorf("invoice = %v", body["invoice"])
	}
}

func TestAWarningIsKeptExactlyAsZATCAPhrasedIt(t *testing.T) {
	// A compliance warning must reach the Owner unaltered. Parsing it into a
	// struct we invented would drop whichever fields we failed to guess.
	const warning = `[{"category":"XSD validation","code":"BR-KSA-10","message":"Something ZATCA said"}]`
	client, _ := serve(t, http.StatusOK,
		`{"invoiceHash":"h","status":"Reported","warnings":`+warning+`,"errors":null}`)

	out, _, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "h", Invoice: "PEludm9pY2U+",
	})
	if err != nil {
		t.Fatalf("reporting: %v", err)
	}
	if out.Status != "Reported" {
		t.Errorf("status = %q", out.Status)
	}
	var round any
	if err := json.Unmarshal(out.Warnings, &round); err != nil {
		t.Fatalf("the warning did not survive: %v", err)
	}
	if !strings.Contains(string(out.Warnings), "BR-KSA-10") {
		t.Errorf("the warning lost its rule code: %s", out.Warnings)
	}
}

func TestTheCertificateResponseIsReadUsingZATCAsFieldNames(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{
		"requestID": 30368,
		"tokenType": "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-x509-token-profile-1.0#X509v3",
		"dispositionMessage": "ISSUED",
		"binarySecurityToken": "TUlJRDZq",
		"secret": "f9YRhopN"
	}`)

	out, _, err := client.RequestProductionCSID(context.Background(),
		[]byte("-----BEGIN CERTIFICATE REQUEST-----\nMII\n-----END CERTIFICATE REQUEST-----\n"))
	if err != nil {
		t.Fatalf("requesting a certificate: %v", err)
	}

	if got.path != PathProductionCSID {
		t.Errorf("path = %s, want %s", got.path, PathProductionCSID)
	}
	if out.RequestID != 30368 {
		t.Errorf("requestID = %d", out.RequestID)
	}
	if out.DispositionMessage != "ISSUED" {
		t.Errorf("dispositionMessage = %q", out.DispositionMessage)
	}
	if out.BinarySecurityToken != "TUlJRDZq" || out.Secret != "f9YRhopN" {
		t.Errorf("the credentials were not read back")
	}

	// The CSR travels base64-encoded, under "csr".
	var body map[string]string
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body["csr"])
	if err != nil {
		t.Fatalf("the csr field is not base64: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "-----BEGIN CERTIFICATE REQUEST-----") {
		t.Errorf("the csr field is not the PEM block: %.40q", decoded)
	}
}

func TestTheSecretIsNeverPrinted(t *testing.T) {
	// One %v in a log line is all it takes to write a signing credential
	// somewhere it outlives the certificate.
	r := CSIDResponse{BinarySecurityToken: "TUlJRDZq", Secret: "f9YRhopN"}
	if strings.Contains(r.String(), "f9YRhopN") {
		t.Errorf("the secret appears in %s", r.String())
	}
	if !strings.Contains(r.String(), "redacted") {
		t.Errorf("the redaction is not visible in %s", r.String())
	}
}

func TestTheClientRefusesToStartWithoutAnEndpoint(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("a client was configured with no base URL")
	}
}

func TestASubmissionNeedsBothTheDocumentAndItsHash(t *testing.T) {
	client, _ := serve(t, http.StatusOK, `{}`)

	if _, _, err := client.ReportSingle(context.Background(),
		DocumentRequest{Invoice: "PEludm9pY2U+"}); err == nil {
		t.Error("a submission with no hash was sent")
	}
	if _, _, err := client.ReportSingle(context.Background(),
		DocumentRequest{InvoiceHash: "h"}); err == nil {
		t.Error("a submission with no document was sent")
	}
}

func TestAnUnreadableResponseIsNotReportedAsSuccess(t *testing.T) {
	// An unparseable clearance response may still mean the document was
	// cleared. Treating it as an empty success would record the opposite.
	client, _ := serve(t, http.StatusOK, `<html>gateway error</html>`)

	if _, _, err := client.ReportSingle(context.Background(), DocumentRequest{
		InvoiceHash: "h", Invoice: "PEludm9pY2U+",
	}); err == nil {
		t.Fatal("an unreadable body was accepted as a successful report")
	}
}

func TestStatusCodesMapOnlyWhereTheManualDescribesThem(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{http.StatusOK, OutcomeAccepted},
		{http.StatusAccepted, OutcomeAcceptedWithWarnings},
		{http.StatusBadRequest, OutcomeRejected},
		{http.StatusInternalServerError, OutcomeTransportFailure},
		{http.StatusBadGateway, OutcomeTransportFailure},
	}
	for _, c := range cases {
		if got := DescribeStatus(c.code); got != c.want {
			t.Errorf("DescribeStatus(%d) = %s, want %s", c.code, got, c.want)
		}
	}
}

// The compliance CSID call is the one unauthenticated call in the flow, and
// the only one carrying an OTP.
func TestTheComplianceCSIDCallCarriesTheOTPHeaderAndNoCredentials(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{
		"requestID": 1, "dispositionMessage": "ISSUED",
		"binarySecurityToken": "TUlJRDZq", "secret": "s"
	}`)

	if _, _, err := client.RequestComplianceCSID(context.Background(),
		[]byte("-----BEGIN CERTIFICATE REQUEST-----\nMII\n-----END CERTIFICATE REQUEST-----\n"),
		"123456"); err != nil {
		t.Fatalf("onboarding: %v", err)
	}

	if got.path != PathComplianceCSID {
		t.Errorf("path = %s, want %s", got.path, PathComplianceCSID)
	}
	// Established from ZATCA's own API: without a header named OTP the service
	// answers Missing-OTP, and with one it moves on to validating the CSR.
	if v := got.headers.Get(HeaderOTP); v != "123456" {
		t.Errorf("%s = %q, want the one-time password", HeaderOTP, v)
	}
	// This call is what OBTAINS the credentials, so it cannot present them.
	if got.headers.Get("Authorization") != "" {
		t.Error("the compliance CSID call presented credentials it does not yet have")
	}
}

func TestOnboardingRefusesAnOTPThatIsNotSixDigits(t *testing.T) {
	client, _ := serve(t, http.StatusOK, `{}`)
	csr := []byte("-----BEGIN CERTIFICATE REQUEST-----\nMII\n-----END CERTIFICATE REQUEST-----\n")

	for _, bad := range []string{"", "12345", "1234567", "12345a", "abcdef"} {
		if _, _, err := client.RequestComplianceCSID(context.Background(), csr, bad); err == nil {
			t.Errorf("the one-time password %q was accepted", bad)
		}
	}
}

// Renewal is a PATCH on the production path and carries an OTP of its own.
func TestRenewalPatchesTheProductionCertificate(t *testing.T) {
	client, got := serve(t, http.StatusOK, `{
		"requestID": 2, "dispositionMessage": "ISSUED",
		"binarySecurityToken": "TUlJRDZq", "secret": "s"
	}`)

	if _, _, err := client.RenewProductionCSID(context.Background(),
		[]byte("-----BEGIN CERTIFICATE REQUEST-----\nMII\n-----END CERTIFICATE REQUEST-----\n"),
		"654321"); err != nil {
		t.Fatalf("renewal: %v", err)
	}
	if got.method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", got.method)
	}
	if got.path != PathProductionCSID {
		t.Errorf("path = %s, want %s", got.path, PathProductionCSID)
	}
	if got.headers.Get(HeaderOTP) != "654321" {
		t.Error("renewal did not carry its one-time password")
	}
}

// A 200 with no certificate in it is not a success.
func TestACertificateResponseWithNoCertificateIsAnError(t *testing.T) {
	client, _ := serve(t, http.StatusOK, `{"requestID": 3, "dispositionMessage": "PENDING"}`)

	if _, _, err := client.RequestProductionCSID(context.Background(),
		[]byte("-----BEGIN CERTIFICATE REQUEST-----\nMII\n-----END CERTIFICATE REQUEST-----\n")); err == nil {
		t.Fatal("a response carrying no certificate was treated as success")
	}
}
