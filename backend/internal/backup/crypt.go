// Encrypting a backup before it leaves the building.
//
// # Why this exists, and why it is optional
//
// A backup is every business on the platform: their customers, their staff's
// pay, their bank details, their tax returns. It is uploaded to somebody else's
// computer — Cloudflare, Amazon, whoever the bucket belongs to. Transport is
// TLS and a serious provider encrypts at rest, but both of those protect the
// bytes from everyone EXCEPT the provider, and the provider is a party this
// product cannot make promises about on a shop's behalf.
//
// So a key may be configured, and when it is, what leaves this server is
// ciphertext. When it is not, nothing here runs and the manifest says plainly
// that the dump is not encrypted. It is off by default because turning it on is
// a promise about key management that only the operator can keep: a lost key is
// a lost backup, permanently, and a product that switched this on by default
// would be arranging for that to happen to somebody who never chose it.
//
// # Chunked, because a dump does not fit in memory
//
// AES-256-GCM authenticates what it encrypts, which is the property worth
// having: a tampered backup fails to decrypt rather than restoring quietly
// wrong. But GCM is a single-shot construction over a whole message, and the
// whole message here is a database. So the stream is cut into fixed chunks,
// each sealed separately, and three things stop the obvious attacks on that:
//
//   - the chunk number is authenticated, so chunks cannot be reordered;
//   - a per-stream random prefix is authenticated, so a chunk cannot be moved
//     from one backup into another;
//   - the LAST chunk is marked as last in its authenticated data, so a truncated
//     stream fails instead of decrypting into a shorter, valid-looking dump.
//
// The third is the one people leave out, and it is the one that matters here:
// truncation is exactly what a half-finished upload looks like.
//
// # What is not in the ciphertext
//
// The key. The header carries the format, the chunk size, the stream prefix and
// a FINGERPRINT of the key — a hash, sixteen hex characters, from which the key
// cannot be recovered. Its whole job is to let a restore say "this snapshot was
// sealed with key a1b2c3… and you have given me d4e5f6…" instead of "decryption
// failed", which is the difference between a five-minute fix and an afternoon.
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// The wire format. Fixed for ever once a backup has been written with it: a
// snapshot outlives the build that made it, and a restore three years from now
// has only these bytes and the key.
const (
	// 8 bytes, so the file says what it is.
	//
	// Still the old product name, and it must stay that way: this is the magic
	// number at the head of every encrypted backup ever taken. `Open` refuses
	// a file that does not begin with it, so renaming it would make every
	// existing backup unreadable by the tool that wrote it -- the worst
	// possible outcome of a cosmetic change. It is also exactly eight bytes,
	// which "BIZ1COREB" is not.
	//
	// See docs/BRANDING.md, "Old identifiers deliberately kept".
	cryptMagic     = "RAWSYSTB"
	cryptFormat    = 1
	cryptChunk     = 4 << 20 // 4 MiB of plaintext per sealed chunk
	cryptPrefixLen = 16
	cryptKeyLen    = 32 // AES-256
)

// EncryptionInfo is what the manifest records about a sealed snapshot.
//
// Never the key, and never anything from which the key could be derived. A
// manifest is stored beside the dump and is readable by anybody who can read
// the bucket; a key in it would make the encryption theatre.
type EncryptionInfo struct {
	Algorithm string `json:"algorithm"`
	ChunkSize int    `json:"chunk_size_bytes"`

	// KeyFingerprint identifies WHICH key without being one. See the package
	// note: "you have the wrong key" is a far more useful sentence than
	// "decryption failed".
	KeyFingerprint string `json:"key_fingerprint"`
}

// Key is the material a backup is sealed with.
type Key struct {
	material []byte
	print    string
}

// ParseKey reads a key from configuration.
//
// Base64 of exactly 32 bytes. Not a passphrase: deriving a key from a
// passphrase needs a KDF, a work factor and a salt, all of which are decisions
// somebody has to get right, and the failure mode of getting them wrong is a
// backup that looks encrypted. `openssl rand -base64 32` produces one, and
// `deploy/server/BACKUP.md` says where to keep it.
func ParseKey(encoded string) (Key, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return Key{}, errs.New(errs.CodeInvalidInput, "No encryption key was given.")
	}
	material, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Key{}, errs.New(errs.CodeInvalidInput,
			"The backup encryption key is not valid base64. It must be "+
				"base64 of exactly 32 random bytes — `openssl rand -base64 32`.")
	}
	if len(material) != cryptKeyLen {
		return Key{}, errs.Newf(errs.CodeInvalidInput,
			"The backup encryption key is %d bytes and must be %d. Generate "+
				"one with `openssl rand -base64 32`.", len(material), cryptKeyLen)
	}
	return Key{material: material, print: fingerprint(material)}, nil
}

// Fingerprint identifies the key without revealing it.
func (k Key) Fingerprint() string { return k.print }

// Set reports whether there is a key at all.
func (k Key) Set() bool { return len(k.material) == cryptKeyLen }

// fingerprint is a hash of a hash, truncated.
//
// Domain-separated so it cannot collide with any other use of the same key
// material, and truncated to 16 hex characters because it is an identifier for
// a human to compare, not a signature.
//
// The separator is still the old product name and must stay that way. It is
// mixed into the hash, so changing the string changes the fingerprint derived
// from the SAME key -- and the fingerprint is what an operator compares
// against the one recorded beside a stored backup to decide whether they hold
// the right key. Renaming it would report the correct key as the wrong one for
// every backup already taken.
//
// See docs/BRANDING.md, "Old identifiers deliberately kept".
func fingerprint(material []byte) string {
	sum := sha256.Sum256(append([]byte("rawsyst-backup-key\x00"), material...))
	second := sha256.Sum256(sum[:])
	return hex.EncodeToString(second[:8])
}

// Info describes how a stream sealed with this key is written.
func (k Key) Info() EncryptionInfo {
	return EncryptionInfo{
		Algorithm:      "AES-256-GCM, chunked",
		ChunkSize:      cryptChunk,
		KeyFingerprint: k.print,
	}
}

// header is what every sealed stream starts with.
//
//	magic(8) | format(1) | chunkSize(4, big-endian) | prefix(16) | print(8)
//
// Forty-five bytes, all of it authenticated as associated data on every chunk,
// so a header edited to claim a different chunk size fails at the first chunk
// rather than producing garbage.
func (k Key) header(prefix []byte) []byte {
	out := make([]byte, 0, 8+1+4+cryptPrefixLen+8)
	out = append(out, cryptMagic...)
	out = append(out, cryptFormat)
	out = binary.BigEndian.AppendUint32(out, uint32(cryptChunk))
	out = append(out, prefix...)
	printBytes, _ := hex.DecodeString(k.print)
	return append(out, printBytes...)
}

// Seal streams plaintext from r to w, encrypted.
//
// Returns the number of CIPHERTEXT bytes written, which is what the upload
// needs for its Content-Length and what the store will report back.
func (k Key) Seal(w io.Writer, r io.Reader) (int64, error) {
	if !k.Set() {
		return 0, errs.New(errs.CodeInternal, "No backup encryption key is set.")
	}
	aead, err := k.aead()
	if err != nil {
		return 0, err
	}

	prefix := make([]byte, cryptPrefixLen)
	if _, err := io.ReadFull(rand.Reader, prefix); err != nil {
		return 0, errs.Wrap(err, errs.CodeInternal,
			"The system random source could not be read to seal the backup.")
	}
	head := k.header(prefix)
	written, err := w.Write(head)
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternal,
			"The encrypted backup could not be written.")
	}
	total := int64(written)

	plain := make([]byte, cryptChunk)
	sealed := make([]byte, 0, cryptChunk+aead.Overhead())
	nonce := make([]byte, aead.NonceSize())

	var chunk uint64
	for {
		n, readErr := io.ReadFull(r, plain)
		last := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
		if readErr != nil && !last {
			return total, errs.Wrap(readErr, errs.CodeInternal,
				"The dump could not be read while sealing it.")
		}
		// A zero-length final read still gets a chunk: an empty stream, and a
		// stream whose length is an exact multiple of the chunk size, both end
		// with a sealed empty chunk marked last. Without it a complete stream
		// and a truncated one would be indistinguishable, which is the whole
		// failure this marker exists for.

		fillNonce(nonce, prefix, chunk)
		sealed = aead.Seal(sealed[:0], nonce, plain[:n], aad(head, chunk, last))
		if _, err := w.Write(sealed); err != nil {
			return total, errs.Wrap(err, errs.CodeInternal,
				"The encrypted backup could not be written.")
		}
		total += int64(len(sealed))
		chunk++

		if last {
			return total, nil
		}
	}
}

// Open streams ciphertext from r to w, decrypted.
//
// Refuses on the first chunk that does not authenticate, and refuses a stream
// that ends without a chunk marked last. Both are the same finding stated
// differently: this is not the backup it claims to be.
func (k Key) Open(w io.Writer, r io.Reader) (int64, error) {
	if !k.Set() {
		return 0, errs.New(errs.CodeInvalidInput,
			"This snapshot is encrypted and no key is configured. Set "+
				"RAWSYST_BACKUP_ENCRYPTION_KEY to the key it was sealed with; "+
				"without it the backup cannot be read by anybody, including "+
				"this product.")
	}
	aead, err := k.aead()
	if err != nil {
		return 0, err
	}

	head := make([]byte, 8+1+4+cryptPrefixLen+8)
	n, readErr := io.ReadFull(r, head)

	// What the file IS comes before how long it is. A plain dump handed to the
	// decrypter is an ordinary mistake — somebody restoring an unencrypted
	// backup with a key configured — and "this is a plain dump" is the sentence
	// that fixes it. Reporting "too short to be encrypted" first would send
	// them looking for a truncated download that does not exist, and a short
	// plain dump would get that message rather than the useful one.
	if n >= 5 && string(head[:5]) == "PGDMP" {
		return 0, errs.New(errs.CodeInvalidInput,
			"This file is a plain PostgreSQL dump, not an encrypted Biz1core "+
				"backup. Restore it without a key.")
	}
	if readErr != nil {
		return 0, errs.New(errs.CodeInvalidInput,
			"This file is too short to be an encrypted Biz1core backup.")
	}
	if string(head[:8]) != cryptMagic {
		return 0, errs.New(errs.CodeInvalidInput,
			"This file does not begin like an encrypted Biz1core backup, and "+
				"it does not begin like a PostgreSQL dump either. Whatever it "+
				"is, it is not something this product wrote.")
	}
	if head[8] != cryptFormat {
		return 0, errs.Newf(errs.CodeInvalidInput,
			"This backup is in encryption format %d and this build reads "+
				"format %d.", head[8], cryptFormat)
	}
	size := int(binary.BigEndian.Uint32(head[9:13]))
	if size <= 0 || size > 64<<20 {
		return 0, errs.New(errs.CodeInvalidInput,
			"This backup declares a chunk size that cannot be right.")
	}
	prefix := head[13 : 13+cryptPrefixLen]
	if got := hex.EncodeToString(head[13+cryptPrefixLen:]); got != k.print {
		return 0, errs.Newf(errs.CodeInvalidInput,
			"This backup was sealed with key %s and the configured key is %s. "+
				"It cannot be read with this one. The key it needs is the one "+
				"that was in RAWSYST_BACKUP_ENCRYPTION_KEY when it was taken.",
			got, k.print)
	}

	sealed := make([]byte, size+aead.Overhead())
	// Reused across chunks. A fresh 4 MiB allocation per chunk would be a
	// gigabyte of garbage per gigabyte of backup, on a container with 1.6 GiB
	// to spend.
	plainBuf := make([]byte, 0, size)
	nonce := make([]byte, aead.NonceSize())
	var chunk uint64
	var total int64

	for {
		n, readErr := io.ReadFull(r, sealed)
		atEnd := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
		if readErr != nil && !atEnd {
			return total, errs.Wrap(readErr, errs.CodeUnavailable,
				"The encrypted backup could not be read in full.")
		}
		if n == 0 && atEnd {
			// Every stream this code writes ends with a chunk marked last, so
			// reaching the end without having seen one means the bytes stop
			// early. An upload that did not finish looks exactly like this.
			return total, errs.New(errs.CodeInvalidInput,
				"The encrypted backup ends without its final chunk. It was "+
					"truncated and will not be restored.")
		}
		if n < aead.Overhead() {
			return total, errs.New(errs.CodeInvalidInput,
				"The encrypted backup ends in the middle of a chunk. It is "+
					"incomplete and will not be restored.")
		}

		fillNonce(nonce, prefix, chunk)
		// Tried as an ordinary chunk first and as the last chunk second. The
		// alternative — a length field saying which — would be a number an
		// attacker controls, and the whole point of the marker is that it is
		// authenticated rather than declared.
		plain, err := aead.Open(plainBuf[:0], nonce, sealed[:n],
			aad(head, chunk, false))
		last := false
		if err != nil {
			plain, err = aead.Open(plainBuf[:0], nonce, sealed[:n],
				aad(head, chunk, true))
			last = true
		}
		if err != nil {
			return total, errs.Newf(errs.CodeInvalidInput,
				"Chunk %d of this backup does not authenticate. It has been "+
					"altered or corrupted since it was sealed, and it will "+
					"not be restored.", chunk)
		}
		if _, err := w.Write(plain); err != nil {
			return total, errs.Wrap(err, errs.CodeInternal,
				"The decrypted dump could not be written.")
		}
		total += int64(len(plain))
		chunk++

		if last {
			return total, nil
		}
		if atEnd {
			return total, errs.New(errs.CodeInvalidInput,
				"The encrypted backup ends without its final chunk. It was "+
					"truncated — an upload that did not finish looks exactly "+
					"like this — and it will not be restored.")
		}
	}
}

func (k Key) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.material)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal, "The cipher could not be built.")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal, "The cipher could not be built.")
	}
	return aead, nil
}

// fillNonce is the stream prefix's first four bytes and the chunk counter.
//
// GCM's nonce is 12 bytes and must never repeat under one key. The counter
// alone would repeat across two backups sealed with the same key, which is the
// ordinary case here — so four bytes of the per-stream random prefix go in
// front of it.
func fillNonce(nonce, prefix []byte, chunk uint64) {
	copy(nonce[:4], prefix[:4])
	binary.BigEndian.PutUint64(nonce[4:], chunk)
}

// aad is what each chunk authenticates besides itself.
func aad(head []byte, chunk uint64, last bool) []byte {
	out := make([]byte, 0, len(head)+9)
	out = append(out, head...)
	out = binary.BigEndian.AppendUint64(out, chunk)
	if last {
		return append(out, 1)
	}
	return append(out, 0)
}

// IsSealed reports whether a file begins like an encrypted backup.
//
// Used to give a plain "this is encrypted and you have no key" message instead
// of handing a stream of AES to `pg_restore` and reporting whatever it says
// about a corrupt archive.
func IsSealed(head []byte) bool {
	return len(head) >= 8 && string(head[:8]) == cryptMagic
}

// KeyFromEnv builds a key from a configuration value.
//
// An empty value is "no key configured", which is the default and is not an
// error: the second return says whether there is a key at all, so a caller
// cannot mistake "not configured" for "configured badly".
func KeyFromEnv(v string) (Key, bool, error) {
	if strings.TrimSpace(v) == "" {
		return Key{}, false, nil
	}
	k, err := ParseKey(v)
	if err != nil {
		return Key{}, false, err
	}
	return k, true, nil
}
