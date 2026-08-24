package zatca

import (
	"testing"
	"time"
)

// The expiry read back out of a certificate must be the one that was put in.
// Renewal depends on this date, and a wrong one either renews constantly or
// never renews at all.
func TestTheCertificateExpiryIsReadBack(t *testing.T) {
	signer, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	const life = 365 * 24 * time.Hour
	before := time.Now().UTC()
	cert, err := SelfSignedDevelopmentCertificate(signer, life)
	if err != nil {
		t.Fatalf("making a certificate: %v", err)
	}

	// Round-tripped through DER, so this reads what a verifier would read
	// rather than what the builder happened to keep in memory.
	parsed, err := ParseCertificate(cert.DER, signer)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if parsed.NotAfter.IsZero() {
		t.Fatal("no expiry was read from the certificate")
	}

	want := before.Add(life)
	if diff := parsed.NotAfter.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("expiry is %s, want about %s (out by %s)",
			parsed.NotAfter, want, diff)
	}
}

// RFC 5280 pins the two-digit-year pivot: 50-99 is 19xx, 00-49 is 20xx.
// Reading it the other way turns a 2049 expiry into 1949 and treats a
// perfectly valid certificate as decades dead.
func TestTheTwoDigitYearPivotFollowsRFC5280(t *testing.T) {
	for _, c := range []struct {
		encoded string
		want    int
	}{
		{"490101000000Z", 2049},
		{"000101000000Z", 2000},
		{"500101000000Z", 1950},
		{"990101000000Z", 1999},
	} {
		got := parseCertTime(derUTCTime, []byte(c.encoded))
		if got.IsZero() {
			t.Errorf("%s did not parse", c.encoded)
			continue
		}
		if got.Year() != c.want {
			t.Errorf("%s read as year %d, want %d", c.encoded, got.Year(), c.want)
		}
	}
}

// GeneralizedTime carries a four-digit year and is what certificates past 2049
// must use.
func TestGeneralizedTimeIsUnderstood(t *testing.T) {
	got := parseCertTime(derGeneralizedTime, []byte("20500101000000Z"))
	if got.Year() != 2050 {
		t.Errorf("read year %d, want 2050", got.Year())
	}
}

// An unparseable time must read as zero -- "unknown" -- rather than as the
// epoch, which would look like an expiry in 1970 and block every till.
func TestAnUnreadableTimeIsUnknownRatherThanExpired(t *testing.T) {
	if got := parseCertTime(derUTCTime, []byte("nonsense")); !got.IsZero() {
		t.Errorf("unparseable time read as %s, want zero", got)
	}
	if got := parseCertTime(0x99, []byte("20500101000000Z")); !got.IsZero() {
		t.Errorf("unknown tag read as %s, want zero", got)
	}
}
