package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func keyOf(version byte, fill byte) Key {
	m := make([]byte, KeyLength)
	for i := range m {
		m[i] = fill
	}
	return Key{Version: version, Material: m}
}

func TestASealedValueComesBackUnchanged(t *testing.T) {
	c, err := New(keyOf(1, 0xA1))
	if err != nil {
		t.Fatalf("building the cipher: %v", err)
	}

	want := []byte("the CSID secret ZATCA issued")
	sealed, err := c.Seal(want)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	got, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The point of encrypting at rest: somebody reading the column sees nothing.
func TestTheSealedValueDoesNotContainThePlaintext(t *testing.T) {
	c, _ := New(keyOf(1, 0xA1))
	secret := []byte("311111111111113-supersecret")

	sealed, err := c.Seal(secret)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	if bytes.Contains(sealed, secret) {
		t.Error("the sealed value contains the plaintext verbatim")
	}
}

// Two seals of the same value must differ, or an observer learns which rows
// hold the same secret without decrypting any of them.
func TestSealingTheSameValueTwiceProducesDifferentBytes(t *testing.T) {
	c, _ := New(keyOf(1, 0xA1))

	a, _ := c.Seal([]byte("same"))
	b, _ := c.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Error("two seals of the same value are byte-identical, so the nonce " +
			"is not random and equal secrets are visible as equal ciphertexts")
	}
}

// A tampered ciphertext must fail rather than decrypt to something.
func TestATamperedValueIsRefused(t *testing.T) {
	c, _ := New(keyOf(1, 0xA1))

	sealed, _ := c.Seal([]byte("the CSID secret"))
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01

	if _, err := c.Open(tampered); err == nil {
		t.Error("a modified ciphertext was accepted, so the value is not authenticated")
	}
}

// Relabelling the version byte must fail too. It is bound as additional data
// precisely so an attacker cannot point a ciphertext at a key of their
// choosing.
func TestTheVersionByteCannotBeRelabelled(t *testing.T) {
	c, err := New(keyOf(1, 0xA1), keyOf(2, 0xB2))
	if err != nil {
		t.Fatalf("building the cipher: %v", err)
	}

	sealed, _ := c.Seal([]byte("secret"))
	if sealed[0] != 1 {
		t.Fatalf("sealed under version %d, want 1", sealed[0])
	}

	relabelled := append([]byte(nil), sealed...)
	relabelled[0] = 2

	if _, err := c.Open(relabelled); err == nil {
		t.Error("a ciphertext relabelled to another key version was accepted")
	}
}

// Rotation: a value sealed under the old key still opens after a new key is
// put in front of it. Without this, rotating would make every stored
// credential unreadable and force every till to re-onboard.
func TestAValueSurvivesKeyRotation(t *testing.T) {
	old, _ := New(keyOf(1, 0xA1))
	sealed, err := old.Seal([]byte("issued before the rotation"))
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	// The new key goes FIRST, so new writes use it; the old one stays so old
	// reads still work.
	rotated, err := New(keyOf(2, 0xB2), keyOf(1, 0xA1))
	if err != nil {
		t.Fatalf("building the rotated cipher: %v", err)
	}

	got, err := rotated.Open(sealed)
	if err != nil {
		t.Fatalf("a value sealed before rotation could not be read after it: %v", err)
	}
	if string(got) != "issued before the rotation" {
		t.Errorf("got %q", got)
	}
	if rotated.CurrentVersion() != 2 {
		t.Errorf("new values seal under v%d, want v2", rotated.CurrentVersion())
	}
}

// A deployment missing the key that wrote a value must say so in a way that
// stops somebody "fixing" it by re-onboarding, which would orphan the CSID.
func TestAMissingKeyVersionSaysSoWithoutSuggestingReonboarding(t *testing.T) {
	old, _ := New(keyOf(1, 0xA1))
	sealed, _ := old.Seal([]byte("secret"))

	other, _ := New(keyOf(2, 0xB2))
	_, err := other.Open(sealed)
	if err == nil {
		t.Fatal("a value was opened without the key that sealed it")
	}
	if !strings.Contains(err.Error(), "key version 1") {
		t.Errorf("the error does not name the missing version: %v", err)
	}
	if !strings.Contains(err.Error(), "do not re-onboard") {
		t.Errorf("the error does not warn against re-onboarding: %v", err)
	}
}

// Configuration mistakes must fail at startup, not at the first invoice.
func TestBadConfigurationIsRefusedAtStartup(t *testing.T) {
	short := Key{Version: 1, Material: []byte("too short")}
	if _, err := New(short); err == nil {
		t.Error("a key of the wrong length was accepted")
	}
	if _, err := New(); err == nil {
		t.Error("a cipher with no keys at all was accepted")
	}
	if _, err := New(keyOf(0, 0xA1)); err == nil {
		t.Error("version 0 was accepted, but it is reserved")
	}
	if _, err := New(keyOf(1, 0xA1), keyOf(1, 0xB2)); err == nil {
		t.Error("two keys claiming the same version were accepted")
	}
}

func TestParseKeyReadsWhatTheEnvironmentCarries(t *testing.T) {
	material := make([]byte, KeyLength)
	for i := range material {
		material[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(material)

	k, err := ParseKey(1, encoded)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if !bytes.Equal(k.Material, material) {
		t.Error("the parsed key does not match what was encoded")
	}
	if _, err := ParseKey(1, "not base64!!"); err == nil {
		t.Error("a malformed key was accepted")
	}
}

// An empty value is still a value: it must round-trip rather than being
// confused with "no credential stored".
func TestAnEmptyValueRoundTrips(t *testing.T) {
	c, _ := New(keyOf(1, 0xA1))
	sealed, err := c.Seal(nil)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	got, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %q, want empty", got)
	}
}

// Truncation must be refused rather than panicking.
func TestATruncatedValueIsRefused(t *testing.T) {
	c, _ := New(keyOf(1, 0xA1))
	sealed, _ := c.Seal([]byte("secret"))

	for _, n := range []int{0, 1, 5, len(sealed) - 1} {
		if _, err := c.Open(sealed[:n]); err == nil {
			t.Errorf("a value truncated to %d bytes was accepted", n)
		}
	}
}
