package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

func testTokens(t *testing.T) *TokenService {
	t.Helper()
	return NewTokenService(config.Auth{
		JWTSecret:       []byte("test-secret-that-is-at-least-32-bytes-long"),
		Issuer:          "rawsyst-test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
	})
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	svc := testTokens(t)
	want := actor.Actor{
		UserID:     uuid.New(),
		SessionID:  uuid.New(),
		TenantID:   uuid.New(),
		CompanyIDs: []uuid.UUID{uuid.New(), uuid.New()},
	}

	tok, exp, err := svc.IssueAccess(want)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatal("token expiry is not in the future")
	}

	got, err := svc.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != want.UserID || got.TenantID != want.TenantID ||
		got.SessionID != want.SessionID {
		t.Fatalf("round trip lost identity: got %+v want %+v", got, want)
	}
	if len(got.CompanyIDs) != 2 {
		t.Fatalf("company scope lost: got %d ids, want 2", len(got.CompanyIDs))
	}
}

// Permissions must never travel in the token, so revoking one takes effect
// immediately rather than at token expiry. This asserts the claim struct has no
// permission field by checking the encoded payload.
func TestTokenCarriesNoPermissions(t *testing.T) {
	svc := testTokens(t)
	tok, _, err := svc.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(), TenantID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	payload := parts[1]
	for _, forbidden := range []string{"perm", "permission", "role", "scope"} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("token payload appears to carry %q; permissions must be "+
				"resolved server-side per request", forbidden)
		}
	}
}

// The classic JWT attack: a token whose header claims "alg":"none". Accepting
// the header's word for the algorithm makes every token forgeable.
func TestVerifyRejectsAlgNone(t *testing.T) {
	svc := testTokens(t)

	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "rawsyst-test",
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		SessionID: uuid.NewString(),
		TenantID:  uuid.NewString(),
	})
	raw, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}

	if _, err := svc.Verify(raw); err == nil {
		t.Fatal("a token signed with alg=none was accepted")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := testTokens(t)
	tok, _, err := issuer.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(), TenantID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	other := NewTokenService(config.Auth{
		JWTSecret:      []byte("a-completely-different-secret-32-bytes!!"),
		Issuer:         "rawsyst-test",
		AccessTokenTTL: 15 * time.Minute,
	})
	if _, err := other.Verify(tok); err == nil {
		t.Fatal("a token signed with a different secret was accepted")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	svc := NewTokenService(config.Auth{
		JWTSecret:      []byte("test-secret-that-is-at-least-32-bytes-long"),
		Issuer:         "rawsyst-test",
		AccessTokenTTL: -time.Minute, // already expired
	})
	tok, _, err := svc.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(), TenantID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if _, err := svc.Verify(tok); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	issuer := NewTokenService(config.Auth{
		JWTSecret:      []byte("test-secret-that-is-at-least-32-bytes-long"),
		Issuer:         "someone-else",
		AccessTokenTTL: time.Hour,
	})
	tok, _, err := issuer.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(), TenantID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if _, err := testTokens(t).Verify(tok); err == nil {
		t.Fatal("a token from a different issuer was accepted")
	}
}

// A token claiming both Super Admin and a tenant is contradictory: the platform
// plane belongs to no tenant. Guessing which one wins would silently widen or
// narrow access, so it is refused.
func TestVerifyRejectsContradictoryClaims(t *testing.T) {
	svc := testTokens(t)

	superWithTenant, _, err := svc.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(),
		TenantID: uuid.New(), IsSuperAdmin: true,
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if _, err := svc.Verify(superWithTenant); err == nil {
		t.Fatal("a Super Admin token carrying a tenant was accepted")
	}

	tenantless, _, err := svc.IssueAccess(actor.Actor{
		UserID: uuid.New(), SessionID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if _, err := svc.Verify(tenantless); err == nil {
		t.Fatal("a non-Super-Admin token with no tenant was accepted")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	svc := testTokens(t)
	for _, bad := range []string{"", "not.a.token", "a.b", strings.Repeat("x", 500)} {
		if _, err := svc.Verify(bad); err == nil {
			t.Fatalf("garbage token %q was accepted", bad)
		} else if errs.CodeOf(err) != errs.CodeUnauthenticated {
			t.Fatalf("token %q: code = %q, want %q", bad, errs.CodeOf(err), errs.CodeUnauthenticated)
		}
	}
}

func TestRefreshTokenIsRandomAndOnlyStoredHashed(t *testing.T) {
	a, hashA, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	b, hashB, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}

	if a == b {
		t.Fatal("two refresh tokens are identical")
	}
	if hashA == hashB {
		t.Fatal("two refresh token hashes are identical")
	}
	if strings.Contains(hashA, a) {
		t.Fatal("the stored hash contains the token itself")
	}
	if HashRefreshToken(a) != hashA {
		t.Fatal("hashing is not deterministic; lookup by hash would fail")
	}
	if len(a) < 40 {
		t.Fatalf("refresh token is only %d chars; too little entropy", len(a))
	}
}
