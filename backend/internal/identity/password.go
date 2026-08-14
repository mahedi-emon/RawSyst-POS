package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Password hashing uses argon2id.
//
// Blueprint A4.2 requires that a password is stored as an irreversible hash —
// framed there as "a security requirement, not just a policy choice", because
// it is what makes it structurally impossible for a Super Admin to reveal an
// Owner's password during account recovery. There is no decryption function to
// call, so the recovery flow can only ever issue a new one-time password.
//
// Parameters follow the OWASP argon2id recommendation. They are encoded into
// every hash, so raising them later does not invalidate existing passwords:
// an old hash still verifies against its own parameters and can be rehashed on
// the next successful sign-in.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(plain string) (string, error) {
	if err := ValidatePasswordStrength(plain); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errs.Wrap(err, errs.CodeInternal, "Could not secure the password.")
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether plain matches encoded.
//
// Comparison is constant-time. A timing side channel here would let an
// attacker distinguish "wrong password" from "no such user", which is exactly
// the enumeration the generic failure message below is designed to prevent.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errs.New(errs.CodeInternal, "Stored credential is unreadable.")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, errs.Wrap(err, errs.CodeInternal, "Stored credential is unreadable.")
	}
	if version != argon2.Version {
		return false, errs.New(errs.CodeInternal, "Stored credential uses an unsupported version.")
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errs.Wrap(err, errs.CodeInternal, "Stored credential is unreadable.")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternal, "Stored credential is unreadable.")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternal, "Stored credential is unreadable.")
	}

	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether a stored hash was made with weaker parameters
// than current policy, so it can be upgraded after a successful sign-in.
func NeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return true
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return true
	}
	return memory < argonMemory || time < argonTime || threads < argonThreads
}

// ValidatePasswordStrength applies the platform password policy.
//
// The blueprint states no specific policy — A4 lists "global security policy
// (password rules, session timeout, lockout thresholds)" as Super Admin
// configuration. These are the defaults, following current NIST guidance:
// length matters, composition rules do not, and forced rotation is harmful
// because it drives predictable increments.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 256 // bounded so a huge input cannot become a CPU attack
)

func ValidatePasswordStrength(plain string) error {
	switch {
	case len(plain) < MinPasswordLen:
		return errs.Newf(errs.CodeInvalidInput,
			"Your password must be at least %d characters. A short phrase you can "+
				"remember is stronger than a short password with symbols.", MinPasswordLen)
	case len(plain) > MaxPasswordLen:
		return errs.Newf(errs.CodeInvalidInput,
			"Your password must be %d characters or fewer.", MaxPasswordLen)
	}
	if isCommonPassword(plain) {
		return errs.New(errs.CodeInvalidInput,
			"That password appears in known breach lists. Please choose a different one.")
	}
	return nil
}

// commonPasswords is a starter deny-list. In production this is backed by a
// breach-corpus check; the point of keeping a small inline list is that the
// obvious cases are refused even if that service is unavailable.
var commonPasswords = map[string]struct{}{
	"password":     {}, "password123": {}, "123456789012": {},
	"qwertyuiop":   {}, "administrator": {}, "letmein12345": {},
	"welcome12345": {}, "changeme1234": {}, "rawsystpos":  {},
}

func isCommonPassword(p string) bool {
	_, found := commonPasswords[strings.ToLower(p)]
	return found
}
