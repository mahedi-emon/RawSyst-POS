package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords, RFC 6238.
//
// # Written here rather than taken from a library
//
// The algorithm is forty lines and completely specified: HMAC-SHA1 over the
// counter, dynamic truncation, modulo ten to the sixth. A dependency for that
// is a dependency to audit, update and trust with the second factor on every
// account in the product — and the thing it would save is forty lines that
// cannot drift, because the RFC does not change.
//
// SHA-1 is not a mistake here. RFC 6238 specifies HMAC-SHA1 and it is what
// every authenticator app implements; the collision attacks on SHA-1 do not
// apply to HMAC, and choosing SHA-256 would produce codes that Google
// Authenticator and its imitators cannot read.
//
// # The window is one step either side
//
// Thirty seconds is short and clocks drift. Accepting the previous and next
// step means somebody whose phone is twenty seconds fast still gets in, at the
// cost of widening the guess space from one code in a million to three — which
// the attempt limiter, not the window, is what protects.

const (
	// totpStep is the period each code is valid for, in seconds. Thirty is
	// what every authenticator app assumes and is not configurable for that
	// reason: a shop cannot know which app its staff installed.
	totpStep = 30

	// totpDigits is the length of the code. Six, for the same reason.
	totpDigits = 6

	// totpSkew is how many steps either side of now are accepted.
	totpSkew = 1

	// secretBytes is the length of a generated secret. Twenty bytes is the
	// RFC's recommendation for HMAC-SHA1 and encodes to a 32-character base32
	// string, which is what an app expects to scan or be typed.
	secretBytes = 20
)

// NewTOTPSecret generates a secret, base32-encoded as an app expects it.
func NewTOTPSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	// No padding. Authenticator apps reject the '=' characters standard
	// encoding would add, and the QR payload carries the same string.
	return base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString(raw), nil
}

// TOTPURI is what a QR code encodes, per the otpauth:// de facto standard.
//
// The issuer appears twice — once as a label prefix and once as a parameter —
// because different apps read different ones, and an account that shows up as
// a bare email address among fifteen others is one nobody can identify when
// they need it.
func TOTPURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpStep))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// VerifyTOTP checks a code against a secret at a moment in time.
//
// `at` is a parameter rather than time.Now() so the tests can pin it. Every
// caller in production passes the real clock.
func VerifyTOTP(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return false
	}

	counter := at.Unix() / totpStep
	for offset := -totpSkew; offset <= totpSkew; offset++ {
		want := totpAt(key, counter+int64(offset))
		// Constant time, so the number of leading digits a wrong guess got
		// right is not measurable. It matters less here than on a password —
		// the code dies in thirty seconds — and it costs nothing.
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// totpAt is the code for one counter value.
func totpAt(key []byte, counter int64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter))

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 §5.3: the low nibble of the last byte picks
	// where in the digest to read four bytes from, and the top bit is masked
	// off so the result is positive on every platform.
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod)
}

// NewRecoveryCode is one of the codes a person keeps for the day the phone is
// lost.
//
// Ten characters from an alphabet with no 0/O or 1/I/l, because these are read
// off paper and typed by somebody who is already having a bad morning. Grouped
// with a dash for the same reason.
func NewRecoveryCode() (string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, 0, 11)
	for i, b := range raw {
		if i == 5 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(b)%len(alphabet)])
	}
	return string(out), nil
}

// NormaliseRecoveryCode makes a typed code comparable to a stored one.
//
// People type these with the dash, without it, and in lower case. Refusing any
// of those would be refusing somebody who has the right code.
func NormaliseRecoveryCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(
		strings.TrimSpace(code), "-", ""))
}
