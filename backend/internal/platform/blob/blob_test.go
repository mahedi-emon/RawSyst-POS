package blob

import (
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

// Signature Version 4, against the values AWS publishes.
//
// Hand-written signing is worth exactly as much as its test. A wrong signature
// does not look wrong: it is sixty-four hex characters, the request goes out,
// and the store answers 403 with a message that names none of the eleven
// canonical fields.
//
// The two published anchors are used, and they are the two places this goes
// wrong:
//
//   - the CANONICAL REQUEST for the presigned-URL example, whose SHA-256 the
//     documentation gives as 3bfa2928…; and
//   - the SIGNING KEY chain, whose result the documentation gives as f4780e2d…
//
// Everything else follows from those two by one HMAC, so pinning them pins the
// implementation.

const (
	// The canonical request AWS documents for a presigned GET of test.txt in
	// examplebucket at 20130524T000000Z, valid for 86400 seconds.
	publishedCanonicalHash = "3bfa292879f6447bbcda7001decf97f4a54dc650c894" +
		"2174ae0a9121cf58ad04"
	// The signing key AWS documents for 20120215/us-east-1/iam.
	publishedSigningKey = "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1" +
		"a9e090d414db404d"

	exampleSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

// TestAPresignedURLIsTheRequestAWSDocuments rebuilds the canonical request
// from the URL this package emits and checks its hash against the published
// one.
//
// Rebuilt from the OUTPUT rather than compared against a string typed here, so
// what is checked is the request a store would actually receive: the host it
// resolves, the path it looks up and the query it canonicalises.
func TestAPresignedURLIsTheRequestAWSDocuments(t *testing.T) {
	store := Open(config.Storage{
		// Virtual-host style, which is what the published example uses:
		// examplebucket.s3.amazonaws.com/test.txt.
		Endpoint:        "https://s3.amazonaws.com",
		Region:          "us-east-1",
		Bucket:          "examplebucket",
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: exampleSecret,
		PathStyle:       false,
	})
	if store == nil {
		t.Fatal("Open returned nil for a complete configuration")
	}

	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	link, err := store.presignAt("test.txt", 24*time.Hour, at)
	if err != nil {
		t.Fatalf("presigning: %v", err)
	}

	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("the presigned URL does not parse: %v", err)
	}
	if parsed.Host != "examplebucket.s3.amazonaws.com" {
		t.Fatalf("host = %q, want examplebucket.s3.amazonaws.com", parsed.Host)
	}
	if parsed.Path != "/test.txt" {
		t.Fatalf("path = %q, want /test.txt", parsed.Path)
	}

	query := parsed.Query()
	signature := query.Get("X-Amz-Signature")
	if len(signature) != 64 {
		t.Fatalf("signature = %q, want 64 hex characters", signature)
	}
	// The signature is not part of what is signed.
	query.Del("X-Amz-Signature")

	canonical := strings.Join([]string{
		"GET",
		parsed.EscapedPath(),
		query.Encode(),
		"host:" + parsed.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	if got := sha256Hex([]byte(canonical)); got != publishedCanonicalHash {
		t.Fatalf("canonical request hash = %s\nwant                    %s\n"+
			"the request signed was:\n%s",
			got, publishedCanonicalHash, canonical)
	}

	// And the signature is that hash carried through the chain. Recomputed
	// here so a correct canonical request with a mis-assembled string-to-sign
	// still fails.
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		"20130524T000000Z",
		"20130524/us-east-1/s3/aws4_request",
		publishedCanonicalHash,
	}, "\n")
	want := hex.EncodeToString(
		hmacSHA256(store.signingKey("20130524"), []byte(toSign)))
	if signature != want {
		t.Fatalf("signature = %s\nwant        %s", signature, want)
	}
}

// TestTheSigningKeyIsDerivedTheWayAWSDerivesIt checks the chained HMAC on its
// own, against the vector in "Deriving the signing key".
//
// Separately from the URL above because the two fail for different reasons: a
// wrong chain here means every request in every region is wrong, while a wrong
// canonical request leaves the chain correct and one request broken.
func TestTheSigningKeyIsDerivedTheWayAWSDerivesIt(t *testing.T) {
	// The published example is for the `iam` service; this package is fixed to
	// `s3`, so the chain is walked directly rather than through signingKey.
	k := hmacSHA256([]byte("AWS4"+exampleSecret), []byte("20120215"))
	k = hmacSHA256(k, []byte("us-east-1"))
	k = hmacSHA256(k, []byte("iam"))
	k = hmacSHA256(k, []byte("aws4_request"))

	if got := hex.EncodeToString(k); got != publishedSigningKey {
		t.Fatalf("signing key = %s\nwant          %s", got, publishedSigningKey)
	}
}

// TestAKeyWithSlashesAddressesTheObjectItNames is the escaping trap.
//
// Keys in this product are namespaced by tenant — `t/<id>/documents/<id>` —
// and escaping the key as one string would turn every separator into `%2F`,
// which names a single object with slashes in its name rather than a path.
func TestAKeyWithSlashesAddressesTheObjectItNames(t *testing.T) {
	store := Open(config.Storage{
		Endpoint:        "https://minio.example:9000",
		Region:          "us-east-1",
		Bucket:          "rawsyst",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		PathStyle:       true,
	})
	if store == nil {
		t.Fatal("Open returned nil")
	}

	got := store.path("t/abc/documents/a b.pdf")
	const want = "/rawsyst/t/abc/documents/a%20b.pdf"
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if strings.Contains(got, "%2F") {
		t.Fatal("the separators were escaped, so this names one object rather " +
			"than a path")
	}
	// A space must be %20 and never +. `+` is a literal plus in a path, so the
	// two address different objects and only one of them exists.
	if strings.Contains(got, "+") {
		t.Fatal("a space was escaped as + rather than %20")
	}
}

// TestNoStoreIsNotACrash: an installation without object storage is supported,
// and every method must say so rather than dereference nil.
func TestNoStoreIsNotACrash(t *testing.T) {
	store := Open(config.Storage{})
	if store != nil {
		t.Fatal("Open returned a store for an empty configuration")
	}
	if store.Configured() {
		t.Fatal("a nil store reports itself as configured")
	}
	if err := store.Put(t.Context(), "k", "text/plain", nil); err == nil {
		t.Fatal("storing into a nil store did not report a problem")
	}
	if _, err := store.Get(t.Context(), "k"); err == nil {
		t.Fatal("reading from a nil store did not report a problem")
	}
	if err := store.Delete(t.Context(), "k"); err == nil {
		t.Fatal("deleting from a nil store did not report a problem")
	}
	if _, err := store.Presign("k", time.Minute); err == nil {
		t.Fatal("presigning against a nil store did not report a problem")
	}
}
