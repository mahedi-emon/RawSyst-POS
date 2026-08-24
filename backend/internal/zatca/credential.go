package zatca

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
)

// Storage for the credentials ZATCA issues at onboarding.
//
// # The one rule this file exists to enforce
//
// The secret never leaves this package as plaintext except into an
// Authorization header. There is no getter that returns it, no field on a
// response struct that carries it, and no String method that could put it in a
// log line by accident. Callers get a Credential with the secret already
// sealed, and the only thing that opens it is the submitter, immediately
// before use.
//
// That is why Credential has no Secret field. It would be a small convenience
// and a permanent hazard: once a plaintext secret is reachable from an ordinary
// struct, it reaches a log through some %+v two refactors later.

// Environment names which ZATCA stack a credential belongs to.
//
// Not a bool. There are three, and "not production" would put simulation and
// sandbox in the same bucket when they have different endpoints and issue
// credentials that do not work against each other.
type Environment string

const (
	// EnvironmentSandbox is the developer portal: no real onboarding, fixed
	// test credentials, used to exercise the flow.
	EnvironmentSandbox Environment = "sandbox"

	// EnvironmentSimulation is ZATCA's pre-production stack. Real onboarding
	// with a real OTP, against invoices that carry no legal effect.
	EnvironmentSimulation Environment = "simulation"

	// EnvironmentProduction is the live tax authority.
	EnvironmentProduction Environment = "production"
)

// Valid reports whether e is one of the three ZATCA operates.
func (e Environment) Valid() bool {
	switch e {
	case EnvironmentSandbox, EnvironmentSimulation, EnvironmentProduction:
		return true
	}
	return false
}

// BaseURL is the endpoint for this environment.
//
// Resolved here rather than configured, because an environment and its URL are
// not independent facts: pointing "production" at the sandbox host would report
// real invoices into a test system and look like it worked.
func (e Environment) BaseURL() string {
	switch e {
	case EnvironmentProduction:
		return BaseURLCore
	case EnvironmentSimulation:
		return BaseURLSimulation
	default:
		return BaseURLDeveloperPortal
	}
}

// CredentialKind distinguishes the two ZATCA issues.
type CredentialKind string

const (
	// KindCompliance is issued for a CSR and an OTP. It can only be used to
	// have ZATCA CHECK invoices, never to report them.
	KindCompliance CredentialKind = "compliance"

	// KindProduction is issued once compliance checks have passed, and is what
	// reports and clears real invoices.
	KindProduction CredentialKind = "production"
)

// CredentialStatus is where onboarding got to.
type CredentialStatus string

const (
	StatusRequested  CredentialStatus = "requested"
	StatusIssued     CredentialStatus = "issued"
	StatusFailed     CredentialStatus = "failed"
	StatusRevoked    CredentialStatus = "revoked"
	StatusSuperseded CredentialStatus = "superseded"
)

// Credential is a stored ZATCA credential, WITHOUT its secret.
//
// Safe to return from an API, log, or render. The secret is deliberately
// absent — see the note at the top of this file.
type Credential struct {
	ID          uuid.UUID
	EGSUnitID   uuid.UUID
	Environment Environment
	Kind        CredentialKind
	Status      CredentialStatus

	// CSID is the username. Not secret, and the shop needs to see it to
	// recognise their own registration in ZATCA's portal.
	CSID string

	// Certificate is the DER ZATCA issued. Public by construction.
	Certificate []byte

	RequestedAt time.Time
	IssuedAt    *time.Time
	ExpiresAt   *time.Time

	// LastError is what ZATCA said, verbatim.
	LastError     string
	Attempts      int
	LastAttemptAt *time.Time

	// SecretKeyVersion says which encryption key sealed the secret, so an
	// operator can see what a rotation still has to cover. Knowing the version
	// reveals nothing about the value.
	SecretKeyVersion int
}

// Expired reports whether the certificate's own NotAfter has passed.
func (c Credential) Expired(now time.Time) bool {
	return c.ExpiresAt != nil && now.After(*c.ExpiresAt)
}

// ExpiresWithin reports whether renewal should start.
//
// ZATCA does not publish a required renewal lead time, so this takes a window
// from the caller rather than inventing one and presenting it as a rule.
func (c Credential) ExpiresWithin(now time.Time, window time.Duration) bool {
	return c.ExpiresAt != nil && now.Add(window).After(*c.ExpiresAt)
}

// Usable reports whether this credential could authenticate right now.
func (c Credential) Usable(now time.Time) bool {
	return c.Status == StatusIssued && c.CSID != "" && !c.Expired(now)
}

// CredentialStore reads and writes credentials.
//
// The cipher is held rather than passed per call so there is exactly one place
// that decides how a secret is protected.
type CredentialStore struct {
	pool   *db.Pool
	cipher *secrets.Cipher
}

// NewCredentialStore builds a store.
//
// A nil cipher is permitted and means "this deployment cannot store secrets" —
// which is the development default. It fails at the point of STORING, with a
// message saying what to configure, rather than at startup: a developer running
// the test suite has no ZATCA credentials to protect and should not need a key
// to run it.
func NewCredentialStore(pool *db.Pool, cipher *secrets.Cipher) *CredentialStore {
	return &CredentialStore{pool: pool, cipher: cipher}
}

// CanStoreSecrets reports whether this deployment is configured to hold them.
func (s *CredentialStore) CanStoreSecrets() bool { return s.cipher != nil }

// errNoCipher is the same message wherever the absence of a key is discovered.
func errNoCipher() error {
	return errs.New(errs.CodeComplianceBlocked,
		"This installation is not configured to store e-invoicing credentials "+
			"securely, so onboarding cannot be completed. Set "+
			"RAWSYST_DATA_ENCRYPTION_KEYS and try again. Nothing was sent to ZATCA.")
}

// BeginOnboarding records that a CSR has been sent, before the reply arrives.
//
// Written FIRST, on purpose. If the process dies between sending the CSR and
// receiving the CSID, an unrecorded attempt leaves a credential issued at
// ZATCA that this system has no idea exists — and the shop cannot onboard
// again because ZATCA already registered the unit. The 'requested' row is what
// makes that recoverable.
func (s *CredentialStore) BeginOnboarding(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID, unitID uuid.UUID,
	env Environment, kind CredentialKind, csr string,
) (uuid.UUID, error) {
	if !env.Valid() {
		return uuid.Nil, errs.Newf(errs.CodeInvalidInput,
			"%q is not a ZATCA environment.", env)
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO zatca_credential
		  (tenant_id, company_id, egs_unit_id, environment, kind, csr, status)
		VALUES ($1,$2,$3,$4,$5,$6,'requested')
		ON CONFLICT (egs_unit_id, environment, kind)
		  WHERE status IN ('requested','issued')
		DO UPDATE SET
		  csr             = excluded.csr,
		  attempts        = zatca_credential.attempts + 1,
		  last_attempt_at = now()
		RETURNING id`,
		tenantID, companyID, unitID, string(env), string(kind), csr).Scan(&id)
	if err != nil {
		return uuid.Nil, db.Translate(err, "That onboarding attempt could not be recorded.")
	}
	return id, nil
}

// Issue records the credential ZATCA returned.
//
// The secret is sealed here and nowhere else, so there is one place to audit.
func (s *CredentialStore) Issue(
	ctx context.Context, tx pgx.Tx, id uuid.UUID,
	csid string, secret []byte, certificate []byte, expiresAt *time.Time,
) error {
	if s.cipher == nil {
		return errNoCipher()
	}
	if csid == "" {
		return errs.New(errs.CodeInternal,
			"ZATCA returned no CSID, so there is nothing to authenticate with.")
	}
	if len(certificate) == 0 {
		return errs.New(errs.CodeInternal,
			"ZATCA returned no certificate, so invoices could not be stamped.")
	}

	sealed, err := s.cipher.Seal(secret)
	if err != nil {
		return errs.New(errs.CodeInternal,
			"The credential ZATCA issued could not be stored securely.")
	}

	ct, err := tx.Exec(ctx, `
		UPDATE zatca_credential
		   SET status             = 'issued',
		       csid               = $2,
		       secret_sealed      = $3,
		       secret_key_version = $4,
		       certificate        = $5,
		       expires_at         = $6,
		       issued_at          = now(),
		       last_error         = NULL
		 WHERE id = $1`,
		id, csid, sealed, int(s.cipher.CurrentVersion()), certificate, expiresAt)
	if err != nil {
		return db.Translate(err, "That credential could not be stored.")
	}
	if ct.RowsAffected() == 0 {
		return errs.New(errs.CodeNotFound, "That onboarding attempt was not found.")
	}
	return nil
}

// Fail records that ZATCA refused, keeping its wording.
func (s *CredentialStore) Fail(
	ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE zatca_credential
		   SET status          = 'failed',
		       last_error      = $2,
		       last_attempt_at = now()
		 WHERE id = $1`, id, reason)
	if err != nil {
		return db.Translate(err, "That failure could not be recorded.")
	}
	return nil
}

// Supersede retires a credential so a new one can take its place.
//
// Not a delete: the invoices it authenticated outlive it, and the row is the
// evidence of how they came to be stamped.
func (s *CredentialStore) Supersede(
	ctx context.Context, tx pgx.Tx, unitID uuid.UUID, env Environment, kind CredentialKind,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE zatca_credential
		   SET status = 'superseded'
		 WHERE egs_unit_id = $1 AND environment = $2 AND kind = $3
		   AND status IN ('requested','issued')`,
		unitID, string(env), string(kind))
	if err != nil {
		return db.Translate(err, "The previous credential could not be retired.")
	}
	return nil
}

// Find returns a credential without its secret.
func (s *CredentialStore) Find(
	ctx context.Context, unitID uuid.UUID, env Environment, kind CredentialKind,
) (Credential, error) {
	var c Credential
	var keyVersion *int
	var lastError *string

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, egs_unit_id, environment, kind, status,
			       coalesce(csid,''), certificate,
			       requested_at, issued_at, expires_at,
			       last_error, attempts, last_attempt_at, secret_key_version
			  FROM zatca_credential
			 WHERE egs_unit_id = $1 AND environment = $2 AND kind = $3
			   AND status IN ('requested','issued')`,
			unitID, string(env), string(kind)).
			Scan(&c.ID, &c.EGSUnitID, &c.Environment, &c.Kind, &c.Status,
				&c.CSID, &c.Certificate, &c.RequestedAt, &c.IssuedAt, &c.ExpiresAt,
				&lastError, &c.Attempts, &c.LastAttemptAt, &keyVersion)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, errs.New(errs.CodeNotFound,
			"This till has not been onboarded for e-invoicing in that environment yet.")
	}
	if err != nil {
		return Credential{}, db.Translate(err, "That credential could not be read.")
	}

	if lastError != nil {
		c.LastError = *lastError
	}
	if keyVersion != nil {
		c.SecretKeyVersion = *keyVersion
	}
	return c, nil
}

// Latest returns the most recent attempt of a kind, whatever became of it.
//
// Distinct from Find, and the distinction is the point. Find answers "what can
// this till authenticate with", so it considers only live rows -- a submitter
// must never pick up a failed credential. Latest answers "what happened last",
// which is what a settings screen shows, and a FAILED attempt is precisely the
// thing a shop needs to see: it carries the reason ZATCA gave.
//
// Ordered newest first because a retry after a failure creates a new row --
// the partial unique index only reserves the slot for live ones.
func (s *CredentialStore) Latest(
	ctx context.Context, unitID uuid.UUID, env Environment, kind CredentialKind,
) (Credential, error) {
	var c Credential
	var keyVersion *int
	var lastError *string

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, egs_unit_id, environment, kind, status,
			       coalesce(csid,''), certificate,
			       requested_at, issued_at, expires_at,
			       last_error, attempts, last_attempt_at, secret_key_version
			  FROM zatca_credential
			 WHERE egs_unit_id = $1 AND environment = $2 AND kind = $3
			 ORDER BY requested_at DESC
			 LIMIT 1`,
			unitID, string(env), string(kind)).
			Scan(&c.ID, &c.EGSUnitID, &c.Environment, &c.Kind, &c.Status,
				&c.CSID, &c.Certificate, &c.RequestedAt, &c.IssuedAt, &c.ExpiresAt,
				&lastError, &c.Attempts, &c.LastAttemptAt, &keyVersion)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, errs.New(errs.CodeNotFound,
			"This till has not been onboarded for e-invoicing in that environment yet.")
	}
	if err != nil {
		return Credential{}, db.Translate(err, "That credential could not be read.")
	}

	if lastError != nil {
		c.LastError = *lastError
	}
	if keyVersion != nil {
		c.SecretKeyVersion = *keyVersion
	}
	return c, nil
}

// List returns every credential for a unit, newest first, without secrets.
func (s *CredentialStore) List(ctx context.Context, unitID uuid.UUID) ([]Credential, error) {
	var out []Credential
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, egs_unit_id, environment, kind, status,
			       coalesce(csid,''), certificate,
			       requested_at, issued_at, expires_at,
			       coalesce(last_error,''), attempts, last_attempt_at,
			       coalesce(secret_key_version, 0)
			  FROM zatca_credential
			 WHERE egs_unit_id = $1
			 ORDER BY requested_at DESC`, unitID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var c Credential
			if err := rows.Scan(&c.ID, &c.EGSUnitID, &c.Environment, &c.Kind,
				&c.Status, &c.CSID, &c.Certificate, &c.RequestedAt, &c.IssuedAt,
				&c.ExpiresAt, &c.LastError, &c.Attempts, &c.LastAttemptAt,
				&c.SecretKeyVersion); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "Those credentials could not be read.")
	}
	return out, nil
}

// withSecret runs fn with the plaintext secret, and is the ONLY way to get it.
//
// Unexported and callback-shaped on purpose. A function that RETURNED the
// secret would let it escape into a struct, a log, or an error; this way its
// lifetime is bounded by a call the compiler can see, and the only caller is
// the submitter building an Authorization header.
func (s *CredentialStore) withSecret(
	ctx context.Context, id uuid.UUID, fn func(csid string, secret []byte) error,
) error {
	if s.cipher == nil {
		return errNoCipher()
	}

	var csid string
	var sealed []byte
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT coalesce(csid,''), secret_sealed
			  FROM zatca_credential
			 WHERE id = $1 AND status = 'issued'`, id).Scan(&csid, &sealed)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound,
			"That e-invoicing credential was not found, or has not been issued.")
	}
	if err != nil {
		return db.Translate(err, "That credential could not be read.")
	}

	secret, err := s.cipher.Open(sealed)
	if err != nil {
		// The underlying error names the missing key version and warns against
		// re-onboarding; both matter enough to pass through.
		return errs.Newf(errs.CodeComplianceBlocked,
			"The stored e-invoicing credential could not be decrypted: %s", err.Error())
	}
	// Cleared as soon as fn returns, so it is not sitting in a heap dump for
	// the rest of the process's life.
	defer func() {
		for i := range secret {
			secret[i] = 0
		}
	}()

	return fn(csid, secret)
}
