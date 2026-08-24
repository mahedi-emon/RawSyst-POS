//go:build integration

package zatca

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
)

// The credential store is tested against the real database because the things
// that could go wrong are all database facts: the partial unique index, the
// RLS predicate, the CHECK that an issued row is complete. A mock would agree
// with whatever this file asserted and prove nothing.

func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	material := make([]byte, secrets.KeyLength)
	for i := range material {
		material[i] = byte(i + 1)
	}
	c, err := secrets.New(secrets.Key{Version: 1, Material: material})
	if err != nil {
		t.Fatalf("building the cipher: %v", err)
	}
	return c
}

func (f *fixture) asTenant() context.Context {
	return actor.Into(context.Background(), actor.Actor{
		UserID: uuid.New(), TenantID: f.tenantID,
	})
}

// onboard runs a full BeginOnboarding → Issue against the fixture's unit.
func (f *fixture) onboard(
	t *testing.T, store *CredentialStore, env Environment, kind CredentialKind,
	csid string, secret []byte, expires *time.Time,
) uuid.UUID {
	t.Helper()
	ctx := f.asTenant()

	var id uuid.UUID
	err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = store.BeginOnboarding(ctx, tx,
			f.tenantID, f.companyID, f.unitID, env, kind, "-----BEGIN CERTIFICATE REQUEST-----")
		if err != nil {
			return err
		}
		return store.Issue(ctx, tx, id, csid, secret, []byte{0x30, 0x82, 0x01}, expires)
	})
	if err != nil {
		t.Fatalf("onboarding: %v", err)
	}
	return id
}

func TestACredentialSurvivesBeingStoredAndRead(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	expires := time.Now().Add(365 * 24 * time.Hour).UTC().Truncate(time.Second)
	id := f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte("the-secret-zatca-issued"), &expires)

	got, err := store.Find(f.asTenant(), f.unitID, EnvironmentSimulation, KindProduction)
	if err != nil {
		t.Fatalf("finding: %v", err)
	}
	if got.ID != id {
		t.Errorf("found %s, want %s", got.ID, id)
	}
	if got.CSID != "TESTCSID123" {
		t.Errorf("CSID is %q", got.CSID)
	}
	if got.Status != StatusIssued {
		t.Errorf("status is %q, want issued", got.Status)
	}
	if got.SecretKeyVersion != 1 {
		t.Errorf("key version is %d, want 1", got.SecretKeyVersion)
	}
	if !got.Usable(time.Now()) {
		t.Error("a freshly issued credential is not usable")
	}
}

// The reason the whole package exists: the secret must not be readable from
// the database by somebody who has the database.
func TestTheSecretIsNotStoredInPlaintext(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	const secret = "the-secret-zatca-issued"
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte(secret), nil)

	// Read the raw column, as an attacker with a psql prompt would.
	var sealed []byte
	ctx := f.asTenant()
	if err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT secret_sealed FROM zatca_credential WHERE egs_unit_id = $1`,
			f.unitID).Scan(&sealed)
	}); err != nil {
		t.Fatalf("reading the raw column: %v", err)
	}

	if bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("the secret is stored in plaintext in the database")
	}
	if len(sealed) == 0 {
		t.Fatal("nothing was stored at all")
	}
}

// And it must still be recoverable by the one path allowed to.
func TestTheSecretIsRecoverableOnlyThroughWithSecret(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	const want = "the-secret-zatca-issued"
	id := f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte(want), nil)

	var sawCSID, sawSecret string
	err := store.withSecret(f.asTenant(), id, func(csid string, secret []byte) error {
		sawCSID, sawSecret = csid, string(secret)
		return nil
	})
	if err != nil {
		t.Fatalf("opening the secret: %v", err)
	}
	if sawCSID != "TESTCSID123" {
		t.Errorf("CSID is %q", sawCSID)
	}
	if sawSecret != want {
		t.Errorf("secret is %q, want %q", sawSecret, want)
	}
}

// The secret buffer is wiped after the callback, so a later heap dump does not
// carry it for the rest of the process's life.
func TestTheSecretBufferIsClearedAfterUse(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	id := f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte("secret-value"), nil)

	var escaped []byte
	if err := store.withSecret(f.asTenant(), id, func(_ string, secret []byte) error {
		escaped = secret // deliberately keeping the slice, as buggy code would
		return nil
	}); err != nil {
		t.Fatalf("opening: %v", err)
	}

	for _, b := range escaped {
		if b != 0 {
			t.Fatalf("the secret buffer still holds %q after the callback returned", escaped)
		}
	}
}

// A credential belongs to one tenant, and RLS -- not application code -- is
// what keeps it there.
func TestAnotherTenantCannotReadACredential(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte("secret"), nil)

	other := actor.Into(context.Background(), actor.Actor{
		UserID: uuid.New(), TenantID: uuid.New(),
	})
	if _, err := store.Find(other, f.unitID, EnvironmentSimulation, KindProduction); err == nil {
		t.Fatal("another tenant read this tenant's ZATCA credential")
	}
}

// The deliberate asymmetry with every other table: a platform admin may see
// onboarding STATUS, but never the credential that authenticates as the tenant
// to their tax authority. The migration says so; this proves it.
func TestAPlatformAdminCannotReadACredential(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"TESTCSID123", []byte("secret"), nil)

	admin := actor.Into(context.Background(), actor.Actor{
		UserID: uuid.New(), IsSuperAdmin: true,
	})

	var n int
	err := f.pool.TxAsPlatform(admin, func(tx pgx.Tx) error {
		return tx.QueryRow(admin,
			`SELECT count(*) FROM zatca_credential WHERE egs_unit_id = $1`,
			f.unitID).Scan(&n)
	})
	if err != nil {
		t.Fatalf("querying as platform admin: %v", err)
	}
	if n != 0 {
		t.Errorf("a platform admin saw %d credential rows; the policy omits "+
			"is_platform_admin() precisely so this is zero", n)
	}
}

// Sandbox onboarding must not be able to clobber the production credential.
// The unique index is per (unit, environment, kind), which is what allows a
// unit to hold both at once.
func TestSandboxAndProductionCredentialsCoexist(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	f.onboard(t, store, EnvironmentProduction, KindProduction,
		"PROD-CSID", []byte("prod-secret"), nil)
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"SIM-CSID", []byte("sim-secret"), nil)

	prod, err := store.Find(f.asTenant(), f.unitID, EnvironmentProduction, KindProduction)
	if err != nil {
		t.Fatalf("finding the production credential: %v", err)
	}
	if prod.CSID != "PROD-CSID" {
		t.Errorf("the production CSID is %q -- simulation onboarding overwrote it", prod.CSID)
	}

	sim, err := store.Find(f.asTenant(), f.unitID, EnvironmentSimulation, KindProduction)
	if err != nil {
		t.Fatalf("finding the simulation credential: %v", err)
	}
	if sim.CSID != "SIM-CSID" {
		t.Errorf("the simulation CSID is %q", sim.CSID)
	}
}

// Compliance and production are different kinds and also coexist: the
// compliance one is kept after promotion so a renewal can be traced.
func TestComplianceAndProductionCredentialsCoexist(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))

	f.onboard(t, store, EnvironmentSimulation, KindCompliance,
		"COMP-CSID", []byte("comp-secret"), nil)
	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"PROD-CSID", []byte("prod-secret"), nil)

	for _, c := range []struct {
		kind CredentialKind
		want string
	}{{KindCompliance, "COMP-CSID"}, {KindProduction, "PROD-CSID"}} {
		got, err := store.Find(f.asTenant(), f.unitID, EnvironmentSimulation, c.kind)
		if err != nil {
			t.Fatalf("finding %s: %v", c.kind, err)
		}
		if got.CSID != c.want {
			t.Errorf("%s CSID is %q, want %q", c.kind, got.CSID, c.want)
		}
	}
}

// Retrying an onboarding must reuse the row rather than colliding on the
// unique index -- and must count the attempt, because a shop retrying five
// times is a support signal.
func TestRetryingAnOnboardingCountsTheAttempt(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))
	ctx := f.asTenant()

	var first, second uuid.UUID
	for _, id := range []*uuid.UUID{&first, &second} {
		err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
			var err error
			*id, err = store.BeginOnboarding(ctx, tx,
				f.tenantID, f.companyID, f.unitID,
				EnvironmentSimulation, KindProduction, "csr")
			return err
		})
		if err != nil {
			t.Fatalf("beginning onboarding: %v", err)
		}
	}

	if first != second {
		t.Errorf("a retry created a second row (%s then %s); the partial unique "+
			"index should have folded it into the first", first, second)
	}

	var attempts int
	if err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT attempts FROM zatca_credential WHERE id = $1`, first).Scan(&attempts)
	}); err != nil {
		t.Fatalf("reading attempts: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts is %d after one retry, want 1", attempts)
	}
}

// Superseding frees the slot so a renewal can take it, and keeps the old row.
func TestSupersedingFreesTheSlotAndKeepsTheHistory(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))
	ctx := f.asTenant()

	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"OLD-CSID", []byte("old"), nil)

	if err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		return store.Supersede(ctx, tx, f.unitID, EnvironmentSimulation, KindProduction)
	}); err != nil {
		t.Fatalf("superseding: %v", err)
	}

	f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"NEW-CSID", []byte("new"), nil)

	live, err := store.Find(ctx, f.unitID, EnvironmentSimulation, KindProduction)
	if err != nil {
		t.Fatalf("finding the live credential: %v", err)
	}
	if live.CSID != "NEW-CSID" {
		t.Errorf("the live CSID is %q, want NEW-CSID", live.CSID)
	}

	all, err := store.List(ctx, f.unitID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("history holds %d rows, want 2 -- the old credential is evidence "+
			"of how earlier invoices were stamped and must not be discarded", len(all))
	}
}

// A credential row is never deleted, for the same retention reason.
func TestACredentialCannotBeDeleted(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, testCipher(t))
	ctx := f.asTenant()
	f.onboard(t, store, EnvironmentSimulation, KindProduction, "CSID", []byte("s"), nil)

	err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM zatca_credential WHERE egs_unit_id = $1`, f.unitID)
		return e
	})
	if err == nil {
		t.Error("a ZATCA credential was deleted; it must only ever be superseded")
	}
}

// Without a configured key, storing must fail LOUDLY rather than writing the
// secret in the clear.
func TestWithoutAKeyStoringRefusesRatherThanWritingPlaintext(t *testing.T) {
	f := newFixture(t)
	store := NewCredentialStore(f.pool, nil) // no cipher: the development default
	ctx := f.asTenant()

	if store.CanStoreSecrets() {
		t.Error("a store with no cipher claims it can hold secrets")
	}

	err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		id, err := store.BeginOnboarding(ctx, tx, f.tenantID, f.companyID,
			f.unitID, EnvironmentSimulation, KindProduction, "csr")
		if err != nil {
			return err
		}
		return store.Issue(ctx, tx, id, "CSID", []byte("secret"), []byte{0x30}, nil)
	})
	if err == nil {
		t.Fatal("a credential was stored with no encryption key configured")
	}
	if !strings.Contains(err.Error(), "RAWSYST_DATA_ENCRYPTION_KEYS") {
		t.Errorf("the error does not say what to configure: %v", err)
	}
	// And it must say nothing reached ZATCA, so nobody re-onboards in a panic.
	if !strings.Contains(err.Error(), "Nothing was sent to ZATCA") {
		t.Errorf("the error does not reassure that no request was made: %v", err)
	}
}

// A rotation must not strand a stored credential.
func TestACredentialSurvivesKeyRotation(t *testing.T) {
	f := newFixture(t)

	oldKey := secrets.Key{Version: 1, Material: bytes.Repeat([]byte{0xA1}, secrets.KeyLength)}
	newKey := secrets.Key{Version: 2, Material: bytes.Repeat([]byte{0xB2}, secrets.KeyLength)}

	before, err := secrets.New(oldKey)
	if err != nil {
		t.Fatalf("building the old cipher: %v", err)
	}
	store := NewCredentialStore(f.pool, before)
	id := f.onboard(t, store, EnvironmentSimulation, KindProduction,
		"CSID", []byte("issued-before-rotation"), nil)

	// The deployment rotates: new key first, old key retained.
	after, err := secrets.New(newKey, oldKey)
	if err != nil {
		t.Fatalf("building the rotated cipher: %v", err)
	}
	rotated := NewCredentialStore(f.pool, after)

	var got string
	if err := rotated.withSecret(f.asTenant(), id, func(_ string, secret []byte) error {
		got = string(secret)
		return nil
	}); err != nil {
		t.Fatalf("a credential stored before rotation could not be read after it: %v", err)
	}
	if got != "issued-before-rotation" {
		t.Errorf("got %q", got)
	}
}

// Expiry is derived from the certificate's own NotAfter, and the helpers that
// drive renewal must agree with it.
func TestExpiryDrivesUsabilityAndRenewal(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	soon := now.Add(48 * time.Hour)
	far := now.Add(365 * 24 * time.Hour)

	expired := Credential{Status: StatusIssued, CSID: "x", ExpiresAt: &past}
	if !expired.Expired(now) {
		t.Error("an expired credential does not report itself expired")
	}
	if expired.Usable(now) {
		t.Error("an expired credential reports itself usable")
	}

	nearly := Credential{Status: StatusIssued, CSID: "x", ExpiresAt: &soon}
	if !nearly.ExpiresWithin(now, 7*24*time.Hour) {
		t.Error("a credential expiring in 48h is not flagged for renewal at 7 days")
	}
	if nearly.ExpiresWithin(now, time.Hour) {
		t.Error("a credential expiring in 48h is flagged for renewal at 1 hour")
	}
	if !nearly.Usable(now) {
		t.Error("a credential expiring soon is already unusable")
	}

	healthy := Credential{Status: StatusIssued, CSID: "x", ExpiresAt: &far}
	if healthy.ExpiresWithin(now, 7*24*time.Hour) {
		t.Error("a credential a year out is flagged for renewal")
	}

	// A credential with no expiry recorded is not treated as expired: absent is
	// not the same as past, and guessing would block a working till.
	unknown := Credential{Status: StatusIssued, CSID: "x"}
	if unknown.Expired(now) || !unknown.Usable(now) {
		t.Error("a credential with no recorded expiry is treated as expired")
	}
}

// The database refuses a half-written credential, so a partial failure cannot
// leave a row that looks issued but cannot authenticate.
func TestAnIssuedCredentialMustBeComplete(t *testing.T) {
	f := newFixture(t)
	ctx := f.asTenant()

	err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO zatca_credential
			  (tenant_id, company_id, egs_unit_id, environment, kind, status, csid)
			VALUES ($1,$2,$3,'simulation','production','issued','CSID-ONLY')`,
			f.tenantID, f.companyID, f.unitID)
		return e
	})
	if err == nil {
		t.Error("a credential marked issued was stored with no secret and no " +
			"certificate; it would authenticate nothing")
	}
}

// A sealed secret must record which key sealed it, or a rotation makes it
// unreadable with no way to tell which key it needed.
func TestASealedSecretMustRecordItsKeyVersion(t *testing.T) {
	f := newFixture(t)
	ctx := f.asTenant()

	err := f.pool.Tx(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO zatca_credential
			  (tenant_id, company_id, egs_unit_id, environment, kind, secret_sealed)
			VALUES ($1,$2,$3,'simulation','production','\x0102')`,
			f.tenantID, f.companyID, f.unitID)
		return e
	})
	if err == nil {
		t.Error("a sealed secret was stored without its key version")
	}
}

// The environment decides the endpoint, and getting that wrong would report
// real invoices into a test system.
func TestEachEnvironmentResolvesToItsOwnEndpoint(t *testing.T) {
	seen := map[string]Environment{}
	for _, env := range []Environment{
		EnvironmentSandbox, EnvironmentSimulation, EnvironmentProduction,
	} {
		if !env.Valid() {
			t.Errorf("%s is not reported valid", env)
		}
		url := env.BaseURL()
		if url == "" {
			t.Errorf("%s has no endpoint", env)
		}
		if other, clash := seen[url]; clash {
			t.Errorf("%s and %s share the endpoint %s", env, other, url)
		}
		seen[url] = env
	}
	if Environment("live").Valid() {
		t.Error("an unknown environment reports itself valid")
	}
}
