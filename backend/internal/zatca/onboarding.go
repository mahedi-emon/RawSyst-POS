package zatca

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Onboarding: turning a CSR and a one-time password into a usable credential.
//
// # The shape of the flow, and why it is two calls and not one
//
// ZATCA issues two credentials in sequence, and the Technical Guideline is
// explicit that they are not interchangeable:
//
//  1. COMPLIANCE CSID. Requested with the CSR and the OTP the taxpayer reads
//     from their own Fatoora portal. It can only ask ZATCA to CHECK invoices.
//  2. PRODUCTION CSID. Requested with the compliance credential -- no OTP,
//     because the compliance CSID is itself the proof that one was presented.
//     This is what reports and clears real invoices.
//
// Between them a solution is expected to pass compliance checks. That step is
// driven from here too, so a shop cannot promote a unit that has never
// successfully produced a document ZATCA accepted.
//
// # Where the private key is, and why this function never sees it
//
// It takes a CSR, already made. It does not generate one, and it has no
// parameter that could carry a private key.
//
// docs/system-design/01-invoice-zatca-engine.md §7 settles the custody
// question and marks the rule locked: the key pair is generated ON the
// terminal, kept in Windows DPAPI through Tauri's native layer, and never
// leaves the device. The same table assigns the cloud "onboarding credentials
// and the compliance-CSID request flow only".
//
// That is exactly this file. The terminal makes a key, builds a CSR -- which
// is public, and carries only the public half -- and hands the CSR up. The
// server performs the OTP exchange, stores the credential ZATCA returns, and
// never holds anything that could stamp an invoice.
//
// # Why the OTP is never stored
//
// It is single-use and short-lived, and a stored one is a liability with no
// corresponding benefit: by the time anybody could read it, it has expired. It
// arrives as an argument, travels in one header, and is never written to the
// database, the audit trail or a log line.

// OnboardingResult is what a completed step produced.
type OnboardingResult struct {
	Credential Credential

	// RequestID is ZATCA's own reference. Quoted verbatim to support, because
	// it is what ZATCA's own team asks for first.
	RequestID int64
}

// Onboarding runs the CSID flow.
type Onboarding struct {
	pool  *db.Pool
	creds *CredentialStore

	// endpointFor resolves the base URL for an environment.
	//
	// A field rather than a direct call to Environment.BaseURL so tests can
	// point it at an httptest server speaking ZATCA's documented contract. The
	// REAL client code then runs against it -- the transport, the headers, the
	// body shape, the response parsing -- rather than being replaced by a mock
	// that would agree with whatever the test asserted.
	endpointFor func(Environment) string

	httpClient *http.Client

	// now is injectable so expiry behaviour is testable without waiting a year.
	now func() time.Time
}

// NewOnboarding builds the service.
func NewOnboarding(pool *db.Pool, creds *CredentialStore) *Onboarding {
	return &Onboarding{
		pool:        pool,
		creds:       creds,
		endpointFor: func(e Environment) string { return e.BaseURL() },
		httpClient:  &http.Client{Timeout: 60 * time.Second},
		now:         time.Now,
	}
}

// unitContext is the ownership a credential row needs, read from the unit.
type unitContext struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
}

func (o *Onboarding) readUnit(ctx context.Context, unitID uuid.UUID) (unitContext, error) {
	var u unitContext
	err := o.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT tenant_id, company_id FROM egs_unit WHERE id = $1`, unitID).
			Scan(&u.TenantID, &u.CompanyID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return unitContext{}, errs.New(errs.CodeNotFound, "That till was not found.")
	}
	if err != nil {
		return unitContext{}, db.Translate(err, "That till could not be read.")
	}
	return u, nil
}

// RequestComplianceCSID performs step 1: CSR + OTP in, compliance CSID out.
//
// The OTP is validated for shape before anything is written or sent, so a
// mistyped code costs nothing and produces a message about the code rather
// than about ZATCA.
func (o *Onboarding) RequestComplianceCSID(
	ctx context.Context, unitID uuid.UUID, env Environment, csrPEM []byte, otp string,
) (OnboardingResult, error) {
	if !env.Valid() {
		return OnboardingResult{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a ZATCA environment.", env)
	}
	if len(csrPEM) == 0 {
		return OnboardingResult{}, errs.New(errs.CodeInvalidInput,
			"Onboarding needs the certificate request produced by this till.")
	}
	// Checked here as well as in the client so the shop is told about the code
	// before a row is written and an attempt counted against them.
	otp = strings.TrimSpace(otp)
	if len(otp) != 6 || !isDigits(otp) {
		return OnboardingResult{}, errs.New(errs.CodeInvalidInput,
			"The one-time password from the Fatoora portal is six digits.")
	}
	if !o.creds.CanStoreSecrets() {
		return OnboardingResult{}, errNoCipher()
	}

	unit, err := o.readUnit(ctx, unitID)
	if err != nil {
		return OnboardingResult{}, err
	}

	client, err := NewClient(Config{
		BaseURL:    o.endpointFor(env),
		HTTPClient: o.httpClient,
	})
	if err != nil {
		return OnboardingResult{}, err
	}

	// The attempt is recorded BEFORE the request leaves. If this process dies
	// mid-call, ZATCA may still have registered the unit -- and a shop that
	// then onboards again is refused by ZATCA for a unit this system has no
	// record of. The 'requested' row is what makes that diagnosable.
	var credentialID uuid.UUID
	if err := o.pool.Tx(ctx, func(tx pgx.Tx) error {
		var e error
		credentialID, e = o.creds.BeginOnboarding(ctx, tx,
			unit.TenantID, unit.CompanyID, unitID, env, KindCompliance, string(csrPEM))
		return e
	}); err != nil {
		return OnboardingResult{}, err
	}

	resp, status, err := client.RequestComplianceCSID(ctx, csrPEM, otp)
	if err != nil {
		o.recordFailure(ctx, credentialID, describeOnboardingFailure(status, err))
		return OnboardingResult{}, err
	}

	return o.store(ctx, credentialID, unitID, env, KindCompliance, resp)
}

// RequestProductionCSID performs step 2: compliance credential in, production
// CSID out.
//
// No OTP, and that is not an oversight. The Technical Guideline has the
// production call authenticated with the COMPLIANCE credential, which is
// itself the proof that an OTP was presented.
func (o *Onboarding) RequestProductionCSID(
	ctx context.Context, unitID uuid.UUID, env Environment, csrPEM []byte,
) (OnboardingResult, error) {
	if !env.Valid() {
		return OnboardingResult{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a ZATCA environment.", env)
	}
	if !o.creds.CanStoreSecrets() {
		return OnboardingResult{}, errNoCipher()
	}

	compliance, err := o.creds.Find(ctx, unitID, env, KindCompliance)
	if err != nil {
		return OnboardingResult{}, errs.New(errs.CodeComplianceBlocked,
			"This till has not completed compliance onboarding yet, so it cannot "+
				"be promoted to production. Enter the one-time password from the "+
				"Fatoora portal first.")
	}
	if !compliance.Usable(o.now()) {
		return OnboardingResult{}, errs.New(errs.CodeComplianceBlocked,
			"This till's compliance credential is not usable, so it cannot be "+
				"promoted. Onboard it again with a new one-time password.")
	}

	unit, err := o.readUnit(ctx, unitID)
	if err != nil {
		return OnboardingResult{}, err
	}

	var credentialID uuid.UUID
	if err := o.pool.Tx(ctx, func(tx pgx.Tx) error {
		var e error
		credentialID, e = o.creds.BeginOnboarding(ctx, tx,
			unit.TenantID, unit.CompanyID, unitID, env, KindProduction, string(csrPEM))
		return e
	}); err != nil {
		return OnboardingResult{}, err
	}

	// The compliance secret is opened only for the duration of this call.
	var result OnboardingResult
	err = o.creds.withSecret(ctx, compliance.ID, func(csid string, secret []byte) error {
		client, e := NewClient(Config{
			BaseURL:     o.endpointFor(env),
			Credentials: Credentials{BinarySecurityToken: csid, Secret: string(secret)},
			HTTPClient:  o.httpClient,
		})
		if e != nil {
			return e
		}

		resp, status, e := client.RequestProductionCSID(ctx, csrPEM)
		if e != nil {
			o.recordFailure(ctx, credentialID, describeOnboardingFailure(status, e))
			return e
		}

		result, e = o.store(ctx, credentialID, unitID, env, KindProduction, resp)
		return e
	})
	if err != nil {
		return OnboardingResult{}, err
	}
	return result, nil
}

// store writes what ZATCA returned and moves the unit's status along.
func (o *Onboarding) store(
	ctx context.Context, credentialID, unitID uuid.UUID,
	env Environment, kind CredentialKind, resp CSIDResponse,
) (OnboardingResult, error) {
	// The binarySecurityToken is the certificate, base64. Decoded so the DER is
	// stored as DER -- the digest in every signature is taken over "the entire
	// DER encoded certificate", so a re-encoding would break every stamp.
	der, err := base64.StdEncoding.DecodeString(resp.BinarySecurityToken)
	if err != nil {
		o.recordFailure(ctx, credentialID,
			"ZATCA returned a certificate that could not be decoded.")
		return OnboardingResult{}, errs.New(errs.CodeInternal,
			"ZATCA returned a certificate this system could not read. Nothing "+
				"was changed; try onboarding again.")
	}

	var expires *time.Time
	if cert, err := ParseCertificate(der, nil); err == nil && !cert.NotAfter.IsZero() {
		when := cert.NotAfter
		expires = &when
	}

	if err := o.pool.Tx(ctx, func(tx pgx.Tx) error {
		if err := o.creds.Issue(ctx, tx, credentialID,
			resp.BinarySecurityToken, []byte(resp.Secret), der, expires); err != nil {
			// Recorded outside this transaction, which is about to roll back.
			defer o.recordFailure(ctx, credentialID, err.Error())
			return err
		}
		// egs_unit carries the status the compliance watch reads, and it is
		// what a Super Admin can still see now that the credential itself is
		// tenant-only.
		status := "compliance_csid"
		if kind == KindProduction {
			status = "production_csid"
		}
		_, err := tx.Exec(ctx, `
			UPDATE egs_unit
			   SET csid_status     = $2,
			       csid_serial     = coalesce($3, csid_serial),
			       csid_issued_at  = now(),
			       csid_expires_at = coalesce($4, csid_expires_at)
			 WHERE id = $1`, unitID, status, serialOf(der), expires)
		return err
	}); err != nil {
		return OnboardingResult{}, err
	}

	stored, err := o.creds.Find(ctx, unitID, env, kind)
	if err != nil {
		return OnboardingResult{}, err
	}
	return OnboardingResult{Credential: stored, RequestID: resp.RequestID}, nil
}

// serialOf reads the certificate serial, or nil when it cannot be read.
func serialOf(der []byte) *string {
	cert, err := ParseCertificate(der, nil)
	if err != nil || cert.SerialNumber == "" {
		return nil
	}
	return &cert.SerialNumber
}

// recordFailure stores what ZATCA said. Best-effort by design: the caller is
// already returning the real error, and losing the audit note must not replace
// a useful message with a database one.
func (o *Onboarding) recordFailure(ctx context.Context, credentialID uuid.UUID, reason string) {
	_ = o.pool.Tx(ctx, func(tx pgx.Tx) error {
		return o.creds.Fail(ctx, tx, credentialID, reason)
	})
}

// describeOnboardingFailure renders a failure for the audit note.
//
// ZATCA's wording is kept rather than paraphrased. When onboarding is refused
// the reason is usually one of the nine CSR fields, and the detail that names
// which one is exactly what a paraphrase discards.
func describeOnboardingFailure(status int, err error) string {
	if err == nil {
		return ""
	}
	if status == 0 {
		return err.Error()
	}
	return "HTTP " + itoa(status) + ": " + err.Error()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// OnboardingStatus is what the settings screen shows.
//
// Everything here is safe to render: no secret, and no field derived from one.
type OnboardingStatus struct {
	EGSUnitID uuid.UUID `json:"egs_unit_id"`

	// Environment is the one this status describes. A unit can hold a
	// credential in more than one, and conflating them is how a shop believes
	// it is live when only its sandbox is.
	Environment Environment `json:"environment"`

	Compliance *CredentialSummary `json:"compliance"`
	Production *CredentialSummary `json:"production"`

	// Connected is true only when a PRODUCTION credential is usable. A
	// compliance CSID means onboarding has started, not that invoices can be
	// reported, and saying "connected" for it would be a lie a shop acts on.
	Connected bool `json:"connected"`

	// NeedsRenewal is set when the production certificate expires within the
	// window the caller asked about.
	NeedsRenewal bool `json:"needs_renewal"`

	// NextAction names, in words, what the shop does next.
	NextAction string `json:"next_action"`
}

// CredentialSummary is one credential, rendered for a screen.
type CredentialSummary struct {
	Status    CredentialStatus `json:"status"`
	CSID      string           `json:"csid"`
	IssuedAt  *time.Time       `json:"issued_at"`
	ExpiresAt *time.Time       `json:"expires_at"`
	LastError string           `json:"last_error"`
	Attempts  int              `json:"attempts"`

	// KeyVersion says which encryption key holds the secret. Useful to an
	// operator planning a rotation and useless to anybody else.
	KeyVersion int `json:"key_version"`
}

func summarise(c Credential) *CredentialSummary {
	return &CredentialSummary{
		Status:     c.Status,
		CSID:       c.CSID,
		IssuedAt:   c.IssuedAt,
		ExpiresAt:  c.ExpiresAt,
		LastError:  c.LastError,
		Attempts:   c.Attempts,
		KeyVersion: c.SecretKeyVersion,
	}
}

// Status describes where a unit has got to in one environment.
func (o *Onboarding) Status(
	ctx context.Context, unitID uuid.UUID, env Environment, renewWithin time.Duration,
) (OnboardingStatus, error) {
	if !env.Valid() {
		return OnboardingStatus{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a ZATCA environment.", env)
	}

	s := OnboardingStatus{EGSUnitID: unitID, Environment: env}
	now := o.now()

	// A missing credential is not an error here: "not onboarded" is a normal
	// state and the screen has to render it.
	// Latest rather than Find: a FAILED attempt is exactly what this screen
	// exists to show, and Find deliberately ignores one.
	if c, err := o.creds.Latest(ctx, unitID, env, KindCompliance); err == nil {
		s.Compliance = summarise(c)
	} else if errs.CodeOf(err) != errs.CodeNotFound {
		return OnboardingStatus{}, err
	}
	if c, err := o.creds.Latest(ctx, unitID, env, KindProduction); err == nil {
		s.Production = summarise(c)
		// Usable() already insists on StatusIssued, so a failed or superseded
		// row cannot make a till look connected.
		s.Connected = c.Usable(now)
		s.NeedsRenewal = c.Status == StatusIssued && c.ExpiresWithin(now, renewWithin)
	} else if errs.CodeOf(err) != errs.CodeNotFound {
		return OnboardingStatus{}, err
	}

	s.NextAction = nextAction(s, now)
	return s, nil
}

// nextAction says what to do, in the order a shop would do it.
func nextAction(s OnboardingStatus, now time.Time) string {
	switch {
	case s.Compliance == nil:
		return "Generate a certificate request on this till, then enter the " +
			"one-time password from your Fatoora portal."

	case s.Compliance.Status == StatusFailed:
		return "ZATCA refused the last request. Check the reason shown, correct " +
			"the till's registration details, and try again with a new one-time password."

	case s.Compliance.Status == StatusRequested:
		return "Onboarding was started but never completed. Try again with a " +
			"new one-time password from your Fatoora portal."

	case s.Production == nil:
		return "Compliance is set up. Promote this till to production to start " +
			"reporting real invoices."

	case s.Production.Status == StatusFailed:
		return "ZATCA refused to issue a production certificate. Check the " +
			"reason shown and try again."

	case s.Production.ExpiresAt != nil && now.After(*s.Production.ExpiresAt):
		return "This till's certificate has expired. Renew it to resume " +
			"reporting invoices."

	case s.NeedsRenewal:
		return "This till's certificate expires soon. Renew it before it does, " +
			"so reporting is never interrupted."

	case s.Connected:
		return "This till is connected to ZATCA and reporting invoices."

	default:
		return "This till's production credential is not usable. Onboard it again."
	}
}
