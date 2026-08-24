// Package secrets encrypts small values at rest.
//
// # What this is for, and what it is NOT for
//
// This protects the credentials the SERVER legitimately holds: the CSID
// ZATCA issues, the secret that goes with it, and the compliance certificate.
// Those are needed to authenticate to ZATCA's reporting and clearance
// endpoints, so a process that cannot read them cannot submit an invoice.
//
// It is NOT for the device stamping key. docs/system-design/01-invoice-zatca-engine.md
// §7 settles that explicitly, and calls the rule LOCKED: the key pair is
// generated ON the terminal, stored in Windows DPAPI through Tauri's native
// layer, and "never leaves the device -- not to the cloud, not to a backup,
// not to a log". The same table assigns the cloud "onboarding credentials and
// the compliance-CSID request flow only".
//
// So there is deliberately no Seal() call anywhere near a stamping key, and
// adding one would break a decision the design took on purpose rather than
// filling a gap it left.
//
// # Why AES-GCM and not something with more knobs
//
// The values are short, the volume is low, and the threat is an attacker who
// has read the database -- a stolen dump, a mis-scoped backup, a support
// engineer with a psql prompt. AEAD with a random nonce answers exactly that
// threat and nothing about a bigger construction would answer it better.
//
// H1 requires the key itself to come from a secret manager rather than a file
// on disk. That is the deployment's job; this package only insists that a key
// is present and the right length, and refuses to start without one.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeyLength is the AES-256 key size, in bytes.
const KeyLength = 32

// Cipher seals and opens values with a versioned key.
//
// Versioned because a key that can never be rotated is a key that never is.
// The version travels in the first byte of every sealed value, so rotating
// means adding a new key and leaving the old one available to decrypt what was
// written under it -- no migration pass over the table, no downtime, and no
// moment where half the rows are unreadable.
type Cipher struct {
	byVersion map[byte]cipher.AEAD
	current   byte
}

// Key is one numbered encryption key.
type Key struct {
	// Version is what gets written into the sealed value. Must be non-zero:
	// zero is reserved so an all-zero byte slice cannot be mistaken for a
	// validly-versioned ciphertext.
	Version byte

	// Material is exactly KeyLength bytes.
	Material []byte
}

// New builds a Cipher. The FIRST key given is the one new values are sealed
// with; the rest are kept only so previously-sealed values still open.
func New(keys ...Key) (*Cipher, error) {
	if len(keys) == 0 {
		return nil, errors.New("no encryption key was configured")
	}

	c := &Cipher{byVersion: make(map[byte]cipher.AEAD, len(keys))}
	for i, k := range keys {
		if k.Version == 0 {
			return nil, errors.New("encryption key version 0 is reserved")
		}
		if len(k.Material) != KeyLength {
			return nil, fmt.Errorf(
				"encryption key v%d must be %d bytes, got %d",
				k.Version, KeyLength, len(k.Material))
		}
		if _, taken := c.byVersion[k.Version]; taken {
			return nil, fmt.Errorf("two encryption keys both claim version %d", k.Version)
		}

		block, err := aes.NewCipher(k.Material)
		if err != nil {
			return nil, fmt.Errorf("encryption key v%d: %w", k.Version, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("encryption key v%d: %w", k.Version, err)
		}
		c.byVersion[k.Version] = aead
		if i == 0 {
			c.current = k.Version
		}
	}
	return c, nil
}

// ParseKey reads a base64 key as it arrives from the environment.
func ParseKey(version byte, encoded string) (Key, error) {
	material, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Key{}, fmt.Errorf("encryption key v%d is not valid base64", version)
	}
	return Key{Version: version, Material: material}, nil
}

// Seal encrypts a value.
//
// The layout is version || nonce || ciphertext-with-tag. Self-describing, so
// Open needs nothing but the sealed bytes and the keyring.
//
// The plaintext is bound to its version byte as additional data, so a sealed
// value cannot be relabelled as having been written under a different key --
// the tag check fails if anybody edits that byte in the database.
func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	aead, ok := c.byVersion[c.current]
	if !ok {
		return nil, errors.New("no current encryption key")
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating a nonce: %w", err)
	}

	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+aead.Overhead())
	out = append(out, c.current)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, []byte{c.current}), nil
}

// Open decrypts a value sealed by Seal.
//
// The error deliberately says nothing about WHY it failed. A message that
// distinguished "wrong key" from "corrupt data" from "bad tag" would be a
// small oracle, and there is nothing a caller could usefully do differently.
func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < 1 {
		return nil, errors.New("the stored value could not be decrypted")
	}
	version := sealed[0]

	aead, ok := c.byVersion[version]
	if !ok {
		return nil, fmt.Errorf(
			"the stored value was encrypted with key version %d, which this "+
				"deployment does not have. It cannot be read until that key is "+
				"configured -- do not re-onboard, which would orphan it", version)
	}
	if len(sealed) < 1+aead.NonceSize() {
		return nil, errors.New("the stored value could not be decrypted")
	}

	nonce := sealed[1 : 1+aead.NonceSize()]
	body := sealed[1+aead.NonceSize():]

	plaintext, err := aead.Open(nil, nonce, body, []byte{version})
	if err != nil {
		return nil, errors.New("the stored value could not be decrypted")
	}
	return plaintext, nil
}

// CurrentVersion reports which key new values are sealed under. Recorded
// alongside the ciphertext so an operator can see what still needs rotating
// without decrypting anything.
func (c *Cipher) CurrentVersion() byte { return c.current }
