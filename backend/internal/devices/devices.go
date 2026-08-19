// Terminals: registering them, pairing them, and taking them away again.
//
// Blueprint H3. A POS terminal is its own Device, and every POS route resolves
// the till from the device in the caller's token rather than from anything the
// request body says — a cashier cannot name a different terminal by asking
// nicely. That only works if a terminal can obtain such a token, which is what
// this package is for.
//
// # Pairing, in three steps
//
//  1. An Owner creates the terminal here. It exists, in `pending`, attached to a
//     store, and can do nothing at all.
//  2. The back office shows a short single-use code that expires in minutes.
//  3. Somebody types it into the till, which claims the device and receives its
//     own long-lived secret. Nothing long-lived was ever shared or transmitted
//     twice: the code dies on first use.
//
// # The secret is not a token
//
// It is exchanged for a short-lived, device-bound access token, and the
// exchange re-reads the device's status every single time. That is the whole
// reason for the indirection: an Owner who revokes a stolen till at 10:00 has
// it locked out at 10:00. A long-lived bearer token would keep working until it
// expired, which for a till in somebody else's hands is the wrong answer.
//
// # Nothing here touches the CSID
//
// Pairing a terminal is not onboarding it for e-invoicing. E1.3 puts the
// signing key on the device and the P1 verification gate is still open, so the
// csid_* columns are read and reported and never written. Conflating the two
// would make an unverifiable ZATCA claim a side effect of ordinary setup.
//
// Registering a terminal DOES name the EGS unit it will sign under, which is a
// different thing: it decides which invoice sequence the till writes to, and
// asserts nothing about certification. It is required because the alternative
// was the state 0013 left behind — a terminal that paired, reported itself
// healthy, and was refused by the till on its first sale with nothing on the
// setup path having mentioned it.

package devices

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

type Service struct {
	pool    *db.Pool
	guesses *limiter
}

func NewService(pool *db.Pool) *Service {
	return &Service{pool: pool, guesses: newLimiter(maxAttempts, CodeLifetime)}
}

// attemptsUnused documents that the column is read, not written, by the redeem
// path. It stays in the schema because a per-enrolment count is the right home
// for a future targeted lockout; today the limiter below is what bounds
// guessing, because a miss cannot be attributed to the code it was aiming at.
var attemptsUnused = struct{}{}

// Scope is which books a terminal belongs to.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// CodeLifetime is how long an enrolment code is good for.
//
// Short on purpose. A code that lives for a day is a code somebody writes on a
// sticky note and leaves on the monitor; fifteen minutes is long enough to walk
// across a shop and no longer.
const CodeLifetime = 15 * time.Minute

// maxAttempts is how many wrong codes one enrolment tolerates before it is
// dead. The code is short enough to type, which is short enough to guess
// without this.
const maxAttempts = 5

// --- Registering a terminal ------------------------------------------------

type NewTerminal struct {
	StoreID uuid.UUID
	Label   string

	// The EGS unit that will sign for this terminal. Required, because a
	// terminal without one is refused at the till and nothing on the way in
	// said so — which is precisely what happened to every terminal registered
	// between 0013 and now.
	EGSUnitID uuid.UUID
}

type Terminal struct {
	ID      uuid.UUID `json:"id"`
	StoreID uuid.UUID `json:"store_id"`
	Store   string    `json:"store"`
	Label   string    `json:"terminal_label"`
	Status  string    `json:"status"`

	OS         string `json:"os,omitempty"`
	AppVersion string `json:"app_version,omitempty"`

	LastSyncAt   string `json:"last_sync_at,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
	EnrolledAt   string `json:"enrolled_at,omitempty"`

	RevokedAt     string `json:"revoked_at,omitempty"`
	RevokedReason string `json:"revoked_reason,omitempty"`

	// The EGS unit this terminal signs under, and therefore which invoice chain
	// its sales join. Empty means the terminal cannot sell at all: the till
	// resolves its unit on every sale and refuses when there is none.
	EGSUnitID string `json:"egs_unit_id,omitempty"`
	EGSUnit   string `json:"egs_unit,omitempty"`

	// Read from the EGS unit, never from the deprecated columns on `device`,
	// and never written here. Pairing a terminal is not onboarding it for
	// e-invoicing; the two are separate acts and the second is behind the P1
	// gate.
	CSIDStatus    string `json:"csid_status,omitempty"`
	CSIDSerial    string `json:"csid_serial,omitempty"`
	CSIDExpiresAt string `json:"csid_expires_at,omitempty"`

	// PendingCode is true when a code has been issued and not yet used, so the
	// screen can say "waiting to be paired" rather than only "pending".
	PendingCode bool `json:"pending_code"`
	// CodeExpiresAt lets the screen count down rather than leaving somebody to
	// discover a code died while they walked to the till.
	CodeExpiresAt string `json:"code_expires_at,omitempty"`
}

// Register creates a terminal, ready to be paired.
//
// It starts in `pending` and can do nothing until a code is redeemed against
// it. Deliberately two steps rather than one: a terminal that arrived already
// active would be a credential waiting to be claimed by whoever reached it
// first.
func (s *Service) Register(
	ctx context.Context, scope Scope, in NewTerminal,
) (Terminal, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		return Terminal{}, errs.New(errs.CodeInvalidInput,
			"Give the terminal a name you will recognise, like \"Till 2\".")
	}
	if in.StoreID == uuid.Nil {
		return Terminal{}, errs.New(errs.CodeInvalidInput,
			"Say which store this terminal is in.")
	}
	if in.EGSUnitID == uuid.Nil {
		return Terminal{}, errs.New(errs.CodeInvalidInput,
			"Choose the e-invoicing unit this terminal will sign under. "+
				"Every sale joins that unit's invoice sequence, so a terminal "+
				"without one cannot ring anything up.").
			WithField("egs_unit_id", "Choose an e-invoicing unit.")
	}

	var out Terminal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The store has to belong to the company being administered, or a
		// caller could attach a till to a company they cannot otherwise see.
		var exists bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM store WHERE id = $1 AND company_id = $2
			)`, in.StoreID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return errs.New(errs.CodeNotFound, "That store was not found.")
		}

		if e := bindable(ctx, tx, scope, in.EGSUnitID, in.StoreID); e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO device
			  (tenant_id, company_id, store_id, terminal_label, status, egs_unit_id)
			VALUES ($1,$2,$3,$4,'pending',$5) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.StoreID, label,
			in.EGSUnitID).Scan(&id); e != nil {
			return db.Translate(e, "That terminal could not be registered.")
		}

		read, e := s.read(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// --- The enrolment code ----------------------------------------------------

// IssuedCode is the one moment the code exists in readable form.
type IssuedCode struct {
	// Code is returned ONCE, to the caller who asked for it. It is never stored
	// and never readable again — only its hash is kept, for the same reason a
	// password is.
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	DeviceID  uuid.UUID `json:"device_id"`
	Label     string    `json:"terminal_label"`
}

// IssueCode produces a fresh enrolment code for a terminal.
//
// Supersedes any code already outstanding. Two live codes for one terminal
// would mean cancelling one achieved nothing, which is exactly what somebody
// re-issuing a code after reading it aloud on the phone is trying to do.
//
// Refused for a revoked terminal. Revocation is the one lifecycle step that
// does not reverse — 01 §7 pairs it with destroying the CSID key — so a revoked
// till is replaced, never resurrected.
func (s *Service) IssueCode(
	ctx context.Context, scope Scope, deviceID uuid.UUID,
) (IssuedCode, error) {
	var out IssuedCode

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, label string
		if e := tx.QueryRow(ctx, `
			SELECT status::text, terminal_label FROM device
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			deviceID, scope.CompanyID).Scan(&status, &label); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That terminal was not found.")
			}
			return e
		}
		if status == "revoked" {
			return errs.New(errs.CodeConflict,
				"That terminal was revoked and cannot be paired again. "+
					"Register a new terminal instead.")
		}

		code, e := newCode()
		if e != nil {
			return e
		}
		// Only the VERIFIER half is hashed; the selector is stored in the
		// clear so the redeem path can find this row with an index instead of
		// hashing every outstanding code. Both halves come from the normalised
		// form, because that is what the redeem path compares — hashing the
		// display form, dash and all, meant no code could ever match itself.
		selector, verifier, ok := splitCode(code)
		if !ok {
			return errs.New(errs.CodeInternal, "Could not issue an enrolment code.")
		}
		hash, e := identity.HashSecret(verifier)
		if e != nil {
			return e
		}

		// The old code goes first, so the partial unique index cannot be
		// violated and so an outstanding code is genuinely cancelled.
		if _, e := tx.Exec(ctx,
			`DELETE FROM device_enrolment WHERE device_id = $1 AND redeemed_at IS NULL`,
			deviceID); e != nil {
			return e
		}

		expires := time.Now().UTC().Add(CodeLifetime)
		if _, e := tx.Exec(ctx, `
			INSERT INTO device_enrolment
			  (tenant_id, device_id, code_hash, code_selector, expires_at, created_by)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			scope.TenantID, deviceID, hash, selector, expires,
			scope.UserID); e != nil {
			return e
		}

		// Re-pairing an already-paired terminal is allowed and puts it back to
		// pending: that is how a shop replaces a machine that died, keeping the
		// same terminal record — and with it, per §9 of 04-identity, the same
		// ZATCA chain. The old secret stops working the moment it is cleared.
		if status != "pending" {
			if _, e := tx.Exec(ctx, `
				UPDATE device
				SET status = 'pending', secret_hash = NULL, secret_selector = NULL,
				    enrolled_at = NULL
				WHERE id = $1`, deviceID); e != nil {
				return e
			}
		}

		out = IssuedCode{
			Code: code, ExpiresAt: expires, DeviceID: deviceID, Label: label,
		}
		return nil
	})
	return out, err
}

// newCode makes a code short enough to read aloud and type, and long enough not
// to be guessed inside its lifetime.
//
// Eight characters from a 32-symbol alphabet is 40 bits. With five attempts per
// code and a fifteen-minute window, guessing is not a threat worth more
// characters — and every extra character is one more chance to mistype at a
// counter with somebody waiting.
//
// The alphabet omits I, L, O, U and the digits 0 and 1: a code is read off a
// screen and typed on a different machine, so the pairs that get confused are
// removed rather than explained.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

func newCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate enrolment code: %w", err)
	}
	out := make([]byte, 8)
	for i, b := range raw {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	// Grouped, because a human is going to read this one character at a time.
	return string(out[:4]) + "-" + string(out[4:]), nil
}

// normaliseCode makes typing forgiving without making guessing easier.
//
// Case and the grouping dash are noise; a cashier who types k7qp4m2x has typed
// the right code. Nothing else is corrected, because silently mapping one
// character onto another would let two different codes be the same code.
func normaliseCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitCode separates the public first group from the secret second one.
//
// Operates on the NORMALISED form, so a code typed in lower case or with the
// dash omitted splits exactly as the issued one did.
func splitCode(code string) (selector, verifier string, ok bool) {
	flat := normaliseCode(code)
	if len(flat) != 8 {
		return "", "", false
	}
	return flat[:4], flat[4:], true
}

// --- Claiming a terminal ---------------------------------------------------

// Enrolled is what a terminal is told once it is paired.
type Enrolled struct {
	DeviceID uuid.UUID `json:"device_id"`
	// Secret is returned ONCE. The terminal puts it in the OS keystore
	// immediately; it is never retrievable from the server again.
	Secret    string    `json:"device_secret"`
	Label     string    `json:"terminal_label"`
	StoreID   uuid.UUID `json:"store_id"`
	Store     string    `json:"store"`
	CompanyID uuid.UUID `json:"company_id"`
	Company   string    `json:"company"`
}

type Claim struct {
	Code       string
	OS         string
	AppVersion string
	IP         string
}

// Enrol redeems a code and hands the terminal its secret.
//
// UNAUTHENTICATED, and it has to be: a terminal being paired has no credential
// yet, which is the entire problem being solved. Everything that would normally
// come from a token is therefore derived from the code — which tenant, which
// company, which store, which terminal — and the code is single-use, expiring
// and attempt-limited to make that safe.
//
// The lookup deliberately runs on the PLATFORM plane. Row-level security keys
// off the tenant in the caller's context and an unpaired terminal has no
// tenant to offer; scoping by the code is what takes its place, and the code is
// the only thing the caller is trusted for.
func (s *Service) Enrol(ctx context.Context, in Claim) (Enrolled, error) {
	code := normaliseCode(in.Code)
	if code == "" {
		return Enrolled{}, errs.New(errs.CodeInvalidInput,
			"Enter the enrolment code shown in the back office.")
	}
	if err := s.guesses.allow(in.IP); err != nil {
		return Enrolled{}, err
	}
	selector, verifier, ok := splitCode(code)
	if !ok {
		s.guesses.miss(in.IP)
		return Enrolled{}, errs.New(errs.CodeUnauthenticated,
			"That enrolment code is not valid. Codes expire after 15 minutes "+
				"and can only be used once — ask for a new one in the back office.")
	}

	var out Enrolled
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// One indexed lookup on the public half, then one hash comparison
		// against the secret half. See 0041 for why this is not a scan: this
		// endpoint is unauthenticated, and argon2 per outstanding code would
		// make it an amplifier anybody could pull.
		var enrolmentID, deviceID uuid.UUID
		var storedHash string
		var enrolledBy *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id, device_id, code_hash, created_by
			FROM device_enrolment
			WHERE code_selector = $1
			  AND redeemed_at IS NULL
			  AND expires_at > now()
			FOR UPDATE`, selector).
			Scan(&enrolmentID, &deviceID, &storedHash, &enrolledBy)

		if errors.Is(e, pgx.ErrNoRows) || (e == nil && !passwordMatches(storedHash, verifier)) {
			// Guessing is bounded per CALLER, never per code. Counting misses
			// against the codes themselves was the first shape of this and it
			// was a denial of service: one till guessing wrong would have killed
			// every outstanding code on the platform, in every other tenant.
			s.guesses.miss(in.IP)

			// Deliberately says nothing about which part was wrong. "No such
			// code", "expired" and "used already" are one answer, because
			// distinguishing them tells an attacker which codes exist.
			return errs.New(errs.CodeUnauthenticated,
				"That enrolment code is not valid. Codes expire after 15 minutes "+
					"and can only be used once — ask for a new one in the back office.")
		}
		if e != nil {
			return e
		}

		secretSelector, secretVerifier, secret, e := newSecret()
		if e != nil {
			return e
		}
		hash, e := identity.HashSecret(secretVerifier)
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx,
			`UPDATE device_enrolment SET redeemed_at = now() WHERE id = $1`,
			enrolmentID); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE device
			SET status = 'active', secret_hash = $2, secret_selector = $7,
			    enrolled_at = now(),
			    enrolled_by = $3, enrolled_ip = $4::inet,
			    os = nullif($5, ''), app_version = nullif($6, ''),
			    last_active_at = now()
			WHERE id = $1`,
			deviceID, hash, enrolledBy, nullIP(in.IP),
			strings.TrimSpace(in.OS), strings.TrimSpace(in.AppVersion),
			secretSelector); e != nil {
			return e
		}

		if e := tx.QueryRow(ctx, `
			SELECT d.id, d.terminal_label, d.store_id, s.name,
			       d.company_id, c.legal_name
			FROM device d
			JOIN store s   ON s.id = d.store_id
			JOIN company c ON c.id = d.company_id
			WHERE d.id = $1`, deviceID).
			Scan(&out.DeviceID, &out.Label, &out.StoreID, &out.Store,
				&out.CompanyID, &out.Company); e != nil {
			return e
		}
		out.Secret = secret
		return nil
	})
	return out, err
}

// newSecret is the terminal's long-lived credential, in two halves.
//
// `selector.verifier`. The selector is public and indexed — it says WHICH
// terminal a credential claims to be, and knowing it gives no way to act as
// one. The verifier is the secret, and only its hash is stored.
//
// Split because the alternative is scanning every paired terminal and running
// argon2 against each, and argon2 is expensive on purpose. One indexed lookup
// and one hash comparison is the difference between a login that costs 64 MiB
// and one that costs 64 MiB per terminal on the platform.
//
// Never typed by anybody, so length costs nothing: 16 and 32 characters from
// the same unambiguous alphabet the enrolment code uses.
func newSecret() (selector, verifier, secret string, err error) {
	selector, err = randomString(16)
	if err != nil {
		return "", "", "", err
	}
	verifier, err = randomString(32)
	if err != nil {
		return "", "", "", err
	}
	return selector, verifier, selector + "." + verifier, nil
}

func randomString(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	out := make([]byte, n)
	for i, b := range raw {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}

// splitSecret separates the public half from the secret half.
func splitSecret(secret string) (selector, verifier string, ok bool) {
	i := strings.IndexByte(secret, '.')
	if i <= 0 || i == len(secret)-1 {
		return "", "", false
	}
	return secret[:i], secret[i+1:], true
}

// --- Proving a terminal ----------------------------------------------------

// Identity is a paired terminal, as resolved from its secret.
type Identity struct {
	DeviceID  uuid.UUID
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	StoreID   uuid.UUID
	Label     string
}

// Authenticate resolves a device secret to the terminal that holds it.
//
// Re-reads the status EVERY time rather than trusting anything issued earlier.
// That is the property the whole design turns on: revocation is immediate
// because there is nothing cached to outlive it.
//
// Runs on the platform plane for the same reason Enrol does — the caller has
// presented a secret, not a tenant, and the secret is what establishes which
// tenant they are in.
func (s *Service) Authenticate(ctx context.Context, secret string) (Identity, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Identity{}, errs.New(errs.CodeUnauthenticated,
			"This terminal is not paired.")
	}

	selector, verifier, ok := splitSecret(secret)
	if !ok {
		return Identity{}, errs.New(errs.CodeUnauthenticated,
			"This terminal is not recognised. It may have been revoked, or it "+
				"may need to be paired again.")
	}

	var out Identity
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var id, tenantID, companyID, storeID uuid.UUID
		var label, hash, status string
		e := tx.QueryRow(ctx, `
			SELECT id, tenant_id, company_id, store_id, terminal_label,
			       secret_hash, status::text
			FROM device
			WHERE secret_selector = $1 AND secret_hash IS NOT NULL`, selector).
			Scan(&id, &tenantID, &companyID, &storeID, &label, &hash, &status)

		if errors.Is(e, pgx.ErrNoRows) || (e == nil && !passwordMatches(hash, verifier)) {
			// One answer for "no such terminal" and "wrong secret". Telling them
			// apart would let somebody with a selector learn which terminals
			// exist.
			return errs.New(errs.CodeUnauthenticated,
				"This terminal is not recognised. It may have been revoked, or it "+
					"may need to be paired again.")
		}
		if e != nil {
			return e
		}

		if status != "active" {
			// Named, because "this till has been switched off" and "this secret
			// is wrong" need different actions from whoever is standing in
			// front of it.
			return errs.Newf(errs.CodeForbidden,
				"%s is %s, so it cannot be used. An owner can reactivate it "+
					"under Devices.", label, status)
		}

		out = Identity{
			DeviceID: id, TenantID: tenantID, CompanyID: companyID,
			StoreID: storeID, Label: label,
		}
		return nil
	})
	return out, err
}

// Touch records that a terminal was heard from.
//
// Best effort and deliberately not in the caller's error path: a till whose
// heartbeat failed to write must still be able to sell.
func (s *Service) Touch(ctx context.Context, id Identity) {
	_ = s.pool.TxAsTenant(ctx, id.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE device SET last_active_at = now() WHERE id = $1`, id.DeviceID)
		return e
	})
}

func nullIP(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return raw
}

// passwordMatches wraps the identity package's verifier, discarding the error.
//
// A malformed stored hash and a wrong secret are the same answer to a caller
// trying to prove a terminal: no. Distinguishing them would tell somebody
// probing which rows exist.
func passwordMatches(encoded, plain string) bool {
	ok, err := identity.VerifyPassword(encoded, plain)
	return err == nil && ok
}
