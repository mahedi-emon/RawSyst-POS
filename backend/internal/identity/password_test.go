package identity

import (
	"strings"
	"testing"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash is not argon2id: %q", hash)
	}
	if strings.Contains(hash, pw) {
		t.Fatal("hash contains the plaintext password")
	}

	ok, err := VerifyPassword(hash, pw)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("correct password did not verify")
	}

	ok, err = VerifyPassword(hash, pw+"x")
	if err != nil {
		t.Fatalf("VerifyPassword (wrong): %v", err)
	}
	if ok {
		t.Fatal("wrong password verified")
	}
}

// Each hash must use a fresh salt, otherwise identical passwords produce
// identical hashes and a leaked table reveals which users share a password.
func TestHashUsesFreshSalt(t *testing.T) {
	const pw = "correct horse battery staple"

	a, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are identical: salt is not random")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"too short", "short", true},
		{"exactly minimum", strings.Repeat("a", MinPasswordLen), false},
		{"long passphrase", "a reasonably long passphrase that a person can recall", false},
		{"too long", strings.Repeat("a", MaxPasswordLen+1), true},
		{"known breached", "password123", true},
		{"known breached, mixed case", "PassWord123", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordStrength(tc.pw)
			if tc.wantErr && err == nil {
				t.Fatal("expected rejection, got none")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected acceptance, got %v", err)
			}
			if tc.wantErr && err != nil {
				if got := errs.CodeOf(err); got != errs.CodeInvalidInput {
					t.Fatalf("code = %q, want %q", got, errs.CodeInvalidInput)
				}
			}
		})
	}
}

// A weak password must be refused at hash time too, not only when validated
// separately — otherwise a caller that skips validation stores it anyway.
func TestHashRejectsWeakPassword(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("HashPassword accepted a password below the minimum length")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"", "not-a-hash", "$argon2id$broken",
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	} {
		if _, err := VerifyPassword(bad, "whatever"); err == nil {
			t.Fatalf("malformed hash %q was accepted", bad)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword("a reasonably long passphrase")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if NeedsRehash(current) {
		t.Fatal("a freshly created hash should not need rehashing")
	}
	// Parameters below current policy must be flagged for upgrade.
	weak := "$argon2id$v=19$m=4096,t=1,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaA"
	if !NeedsRehash(weak) {
		t.Fatal("a hash with weaker parameters should need rehashing")
	}
}
