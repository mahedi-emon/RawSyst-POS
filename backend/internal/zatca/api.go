package zatca

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The shape of ZATCA's API, as the Developer Portal Manual documents it.
//
// # Provenance
//
// Read on 2026-08-23 from the Developer Portal Manual (see csr.go for the full
// citation). Two sources within it, because the manual splits the contract:
//
//   - §4.1's FAQ table carries the reporting and clearance contracts in the
//     TEXT layer, quoted at each declaration below.
//   - the API Integration Sandbox screenshots carry the base URL, the endpoint
//     paths and the CSID response body. These are images, so they were
//     extracted from the PDF and read.
//
// # What this file is, and what it is not
//
// It is the contract: paths, headers, request and response bodies, and a client
// that speaks them. Every field name below appears verbatim in the manual.
//
// It is NOT a licence to start submitting. UnverifiedSubmitter still refuses in
// every environment, and that is unchanged by this file, because the blocker
// was never the transport — it is that SA.ZATCA.UBL_FIELD_SET is unverified and
// nothing here can produce a document worth sending. Knowing the envelope does
// not mean knowing the letter.

// The three base URLs, one per environment.
//
// Core and simulation are carried verbatim by SA.ZATCA.ONBOARDING_ENDPOINTS,
// verified on 2026-08-19. The developer portal is from the "Servers" selector
// on the Integration Sandbox pages of the Developer Portal Manual.
//
// The environments are independent and onboard independently: a unit that
// onboards against the wrong one succeeds, and then reports invoices nowhere
// that counts. Note also that the sandbox lives on gazt.gov.sa — ZATCA's former
// name — while the other two are on zatca.gov.sa, which is not a typo in either
// direction.
const (
	BaseURLCore            = "https://gw-fatoora.zatca.gov.sa/e-invoicing/core"
	BaseURLSimulation      = "https://gw-fatoora.zatca.gov.sa/e-invoicing/simulation"
	BaseURLDeveloperPortal = "https://gw-apic-gov.gazt.gov.sa/e-invoicing/developer-portal"
)

// The endpoint paths, read from the Sandbox API listing.
const (
	// PathComplianceCSID issues a test CSID from a CSR. The Postman screenshot
	// on page 96 shows this called as a bare /compliance.
	PathComplianceCSID = "/compliance"

	// PathComplianceInvoice "performs compliance checks on einvoice documents".
	PathComplianceInvoice = "/compliance/invoices"

	// PathProductionCSID "issues an X509 Production Cryptographic Stamp
	// Identifier (PCSID/Certificate) (CSID) based on submitted CSR" on POST,
	// and renews one on PATCH.
	PathProductionCSID = "/production/csids"

	// PathReportingSingle takes simplified documents, which are reported after
	// issue. "The user will need to do a POST Method on endpoint
	// /invoices/reporting/single".
	PathReportingSingle = "/invoices/reporting/single"

	// PathClearanceSingle takes standard documents, which must be cleared
	// before issue. "a POST Method on endpoint /invoices/clearance/single".
	PathClearanceSingle = "/invoices/clearance/single"
)

// The headers the manual names.
const (
	// HeaderAcceptVersion: "An additional accept-version: v2 header must be
	// added to V2 API calls", and "V2 is currently the only valid version".
	HeaderAcceptVersion = "accept-version"
	AcceptVersionV2     = "v2"

	// HeaderAuthenticationCertificate and HeaderAcceptLanguage are passed "as a
	// parameter in the header" on reporting and clearance.
	HeaderAuthenticationCertificate = "authentication-certificate"
	HeaderAcceptLanguage            = "accept-language"

	// HeaderOTP carries the one-time password on the compliance CSID call.
	//
	// No published ZATCA document names this header. It was established from
	// the authority's own API on 2026-08-24: POST to the live core compliance
	// endpoint answers {"code":"Missing-OTP","message":"OTP is required field"}
	// with no such header and with "X-OTP" or "One-Time-Password", and moves on
	// to validating the CSR the moment a header named OTP is present. The
	// server states its own contract; that is evidence, not inference.
	HeaderOTP = "OTP"
)

// Clearance-Status appears in ZATCA integrations elsewhere and is deliberately
// NOT sent: the Developer Portal Manual never mentions it, and this package
// sends only what the manual documents. Noted so its absence reads as a
// decision rather than an oversight.

// Credentials authenticate a call.
//
// §3 "Security Requirements": "The solution will include a Basic Authentication
// header with the CSID as the Username and a Secret Value as the Password."
// §2.3.10.2 is more specific about which value plays the username: "run the
// Compliance CSID API to obtain the 'binarySecurityToken' to be used as the
// Username and 'secret' as the Password."
type Credentials struct {
	BinarySecurityToken string
	Secret              string
}

// Authorization renders the header value.
//
// "{Base64 Encoded String} = A script containing the CSID, a Colon and the
// Secret encoded with Base64 (CSID:Secret)".
func (c Credentials) Authorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(c.BinarySecurityToken+":"+c.Secret))
}

// CSIDResponse is what the onboarding and renewal calls return.
//
// Field names and the token type are transcribed from the 200 response body
// printed on the Production CSID pages.
type CSIDResponse struct {
	RequestID int64 `json:"requestID"`

	// TokenType is the OASIS WSS X.509 token profile URI. ZATCA returns a
	// constant here; it is kept rather than dropped so an unexpected value is
	// visible instead of silently ignored.
	TokenType string `json:"tokenType"`

	// DispositionMessage is "ISSUED" on success.
	DispositionMessage string `json:"dispositionMessage"`

	// BinarySecurityToken is the certificate, base64-encoded. It becomes the
	// username on every later call.
	BinarySecurityToken string `json:"binarySecurityToken"`

	// Secret becomes the password. It is a credential: it must never be logged,
	// returned to a client, or written to the audit trail.
	Secret string `json:"secret"`
}

// DocumentRequest is the body of a reporting, clearance or compliance call.
//
// "The body object should Contain 2 Values: the first one is called
// 'invoiceHash' and the second one is called 'invoice'."
type DocumentRequest struct {
	// InvoiceHash is the base64 SHA-256 of the canonicalised document — the
	// text form, 44 characters, as the Fatoora SDK prints it ("INVOICE HASH =
	// QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=").
	//
	// The same encoding as QR tag 6, which also carries the base64 text — but
	// NOT the same as the value QRHash takes, which is the raw 32-byte digest
	// it encodes for you. Handing this string to QRHash gets it encoded twice.
	InvoiceHash string `json:"invoiceHash"`

	// Invoice is the UBL 2.1 XML, base64-encoded: clearance "accepts standard
	// invoice, credit note, or debit note encoded in base64".
	Invoice string `json:"invoice"`
}

// DocumentResponse is what reporting and clearance return.
//
// "The response will be 200 HTTP Ok with a Retrieved object containing 4
// values: 'invoiceHash', 'Status', 'Warnings', 'errors'."
//
// The manual capitalises Status and Warnings in the prose and lowercases them
// in the worked example directly beneath it. The example is what the server
// actually sends, so the tags follow the example.
type DocumentResponse struct {
	InvoiceHash string `json:"invoiceHash"`

	// Status is "Reported" in the manual's reporting example.
	Status string `json:"status"`

	// Warnings and Errors are null on a clean response. They are carried as raw
	// JSON because the manual never gives their element shape, and guessing a
	// struct would silently discard whatever ZATCA actually says — the one
	// thing a compliance warning must never do.
	Warnings json.RawMessage `json:"warnings"`
	Errors   json.RawMessage `json:"errors"`

	// ClearedInvoice is returned by clearance, which "applies a cryptographic
	// stamp from ZATCA side and generates a QR Code string. After that the XML
	// is returned back." The manual does not name this field; it is left empty
	// unless the server sends one under this name.
	ClearedInvoice string `json:"clearedInvoice,omitempty"`
}

// Config configures a Client.
type Config struct {
	// BaseURL is required. Use BaseURLDeveloperPortal for the sandbox.
	BaseURL string

	// Credentials are omitted on the compliance CSID call, which is how a
	// solution obtains them in the first place.
	Credentials Credentials

	// Language sets accept-language. The manual requires the header and never
	// states its permitted values, so this defaults to "en" and is settable.
	Language string

	HTTPClient *http.Client
}

// Client speaks the documented API.
//
// It performs no retries and interprets no failures. Deciding whether a
// rejection is permanent, and what that means for an invoice that has already
// consumed its ICV, belongs with the submission logic in submit.go, which knows
// about the chain; this type only carries bytes.
type Client struct {
	cfg Config
}

// NewClient returns a client, or an error if it could not be configured.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errs.New(errs.CodeInvalidInput,
			"The ZATCA endpoint is not configured on this installation.")
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &Client{cfg: cfg}, nil
}

// ReportSingle reports one simplified document.
func (c *Client) ReportSingle(ctx context.Context, req DocumentRequest) (DocumentResponse, int, error) {
	return c.document(ctx, PathReportingSingle, req)
}

// ClearSingle clears one standard document.
func (c *Client) ClearSingle(ctx context.Context, req DocumentRequest) (DocumentResponse, int, error) {
	return c.document(ctx, PathClearanceSingle, req)
}

// CheckCompliance runs a document through the compliance checks.
func (c *Client) CheckCompliance(ctx context.Context, req DocumentRequest) (DocumentResponse, int, error) {
	return c.document(ctx, PathComplianceInvoice, req)
}

func (c *Client) document(ctx context.Context, path string, body DocumentRequest) (DocumentResponse, int, error) {
	if body.InvoiceHash == "" || body.Invoice == "" {
		return DocumentResponse{}, 0, errs.New(errs.CodeInvalidInput,
			"A document submission needs both the invoice and its hash.")
	}

	status, raw, err := c.send(ctx, http.MethodPost, path, body, true, nil)
	if err != nil {
		return DocumentResponse{}, status, err
	}

	var out DocumentResponse
	if len(raw) > 0 {
		if e := json.Unmarshal(raw, &out); e != nil {
			// A body we cannot parse is reported as such rather than as an
			// empty success: an unreadable clearance response may still mean
			// the document was cleared.
			return DocumentResponse{}, status, errs.New(errs.CodeUnavailable,
				"ZATCA returned a response this installation could not read.")
		}
	}
	return out, status, nil
}

// RequestComplianceCSID exchanges a CSR and an OTP for a compliance CSID.
//
// This is the first call of onboarding and the only one that is not
// authenticated: the credentials it returns are what authenticate everything
// after it.
//
// The OTP cannot be obtained programmatically, and that is ZATCA's design
// rather than a gap here. SA.ZATCA.ONBOARDING_OTP records the FAQ verbatim:
// "OTP generation is managed by the FATOORA portal and must be taken from the
// portal itself, no need for any API" and "There is no API for OTP". A human
// reads a six-digit code off the portal and it is valid for one hour. So
// onboarding can never be a background job, and this method takes the code as
// an argument rather than pretending it could fetch one.
func (c *Client) RequestComplianceCSID(
	ctx context.Context, csrPEM []byte, otp string,
) (CSIDResponse, int, error) {
	if strings.TrimSpace(otp) == "" {
		return CSIDResponse{}, 0, errs.New(errs.CodeInvalidInput,
			"Onboarding needs the one-time password from the Fatoora portal.")
	}
	// Six numeric digits, per SA.ZATCA.ONBOARDING_OTP, whose failure list begins
	// "Invalid OTP/OTC (not exactly six digits, not numeric)".
	if len(otp) != 6 || !isDigits(otp) {
		return CSIDResponse{}, 0, errs.New(errs.CodeInvalidInput,
			"The one-time password is six digits.")
	}

	body := struct {
		CSR string `json:"csr"`
	}{CSR: EncodeCSRForAPI(csrPEM)}

	status, raw, err := c.send(ctx, http.MethodPost, PathComplianceCSID, body,
		false, map[string]string{HeaderOTP: otp})
	if err != nil {
		return CSIDResponse{}, status, err
	}
	return readCSID(raw, status)
}

// RequestProductionCSID exchanges a compliance CSID for a production one.
//
// Authenticated with the compliance credentials, which is why it carries no
// OTP: the compliance CSID is itself the proof that an OTP was presented.
func (c *Client) RequestProductionCSID(ctx context.Context, csrPEM []byte) (CSIDResponse, int, error) {
	body := struct {
		CSR string `json:"csr"`
	}{CSR: EncodeCSRForAPI(csrPEM)}

	status, raw, err := c.send(ctx, http.MethodPost, PathProductionCSID, body, true, nil)
	if err != nil {
		return CSIDResponse{}, status, err
	}
	return readCSID(raw, status)
}

// RenewProductionCSID replaces a certificate that is near expiry.
//
// PATCH rather than POST on the same path, per the Sandbox endpoint listing:
// "Renews an X509 Certificate (CSID) based on submitted CSR."
func (c *Client) RenewProductionCSID(
	ctx context.Context, csrPEM []byte, otp string,
) (CSIDResponse, int, error) {
	if len(otp) != 6 || !isDigits(otp) {
		return CSIDResponse{}, 0, errs.New(errs.CodeInvalidInput,
			"Renewal needs the six-digit one-time password from the Fatoora portal.")
	}

	body := struct {
		CSR string `json:"csr"`
	}{CSR: EncodeCSRForAPI(csrPEM)}

	status, raw, err := c.send(ctx, http.MethodPatch, PathProductionCSID, body,
		true, map[string]string{HeaderOTP: otp})
	if err != nil {
		return CSIDResponse{}, status, err
	}
	return readCSID(raw, status)
}

// readCSID parses a certificate response.
func readCSID(raw []byte, status int) (CSIDResponse, int, error) {
	// A non-2xx is a REFUSAL and must be an error.
	//
	// This used to fall through: the body parsed into an empty response, the
	// empty-token check was guarded on status == 200, and a 400 returned a nil
	// error. Onboarding then carried on as though it had a certificate, and the
	// only thing that stopped it was a later nil check with no idea what ZATCA
	// had actually objected to. The reason a shop needs -- which of the nine
	// CSR fields is wrong -- was in the body that got dropped.
	//
	// ZATCA's wording is carried verbatim for the same reason: onboarding
	// refusals are field-level, and a paraphrase discards the field name.
	if status < 200 || status > 299 {
		detail := strings.TrimSpace(string(raw))
		if detail == "" {
			detail = "no reason was given"
		}
		return CSIDResponse{}, status, errs.Newf(errs.CodeComplianceBlocked,
			"ZATCA refused this onboarding request (HTTP %d): %s", status, detail)
	}

	var out CSIDResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return CSIDResponse{}, status, errs.New(errs.CodeUnavailable,
			"ZATCA returned a certificate response this installation could not read.")
	}
	if out.BinarySecurityToken == "" {
		return out, status, errs.New(errs.CodeUnavailable,
			"ZATCA accepted the request but returned no certificate.")
	}
	return out, status, nil
}

// post sends one request with the documented headers.
func (c *Client) send(
	ctx context.Context, method, path string, body any,
	authenticate bool, extra map[string]string,
) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, errs.New(errs.CodeInternal, "The request could not be encoded.")
	}

	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, errs.New(errs.CodeInternal, "The request could not be built.")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HeaderAcceptVersion, AcceptVersionV2)
	req.Header.Set(HeaderAcceptLanguage, c.cfg.Language)
	if authenticate {
		req.Header.Set("Authorization", c.cfg.Credentials.Authorization())
		req.Header.Set(HeaderAuthenticationCertificate, c.cfg.Credentials.BinarySecurityToken)
	}
	for name, value := range extra {
		req.Header.Set(name, value)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, errs.New(errs.CodeUnavailable,
			"ZATCA could not be reached. The document remains queued and has not been reported.")
	}
	defer func() { _ = resp.Body.Close() }()

	// Capped: a malfunctioning endpoint should not be able to exhaust memory on
	// a terminal that is also running a till.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, errs.New(errs.CodeUnavailable,
			"The response from ZATCA was cut short.")
	}
	return resp.StatusCode, raw, nil
}

// DescribeStatus renders an HTTP status as the outcome the chain understands.
//
// The manual documents 200 for reporting and clearance and shows warnings
// carried inside a 200 body rather than by a 202. Anything else is left
// deliberately coarse: this maps what is documented and refuses to invent
// meanings for codes ZATCA has not described.
func DescribeStatus(code int) Outcome {
	switch {
	case code == http.StatusOK:
		return OutcomeAccepted
	case code == http.StatusAccepted:
		return OutcomeAcceptedWithWarnings
	case code == http.StatusBadRequest, code == http.StatusNotAcceptable,
		code == http.StatusConflict, code == http.StatusUnprocessableEntity:
		return OutcomeRejected
	case code >= 500:
		return OutcomeTransportFailure
	default:
		return OutcomeTransportFailure
	}
}

// String makes a response printable without exposing the secret.
//
// CSIDResponse carries a credential, and a struct that prints it is one %v away
// from writing it to a log that outlives the certificate.
func (r CSIDResponse) String() string {
	return fmt.Sprintf("CSIDResponse{requestID:%d disposition:%q token:%d bytes secret:redacted}",
		r.RequestID, r.DispositionMessage, len(r.BinarySecurityToken))
}
