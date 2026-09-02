// Package blob is S3-compatible object storage.
//
// # Why the bytes belong somewhere other than the database
//
// A logo, a stored contract, a generated report, a signed invoice XML. Today
// they live in Postgres as `bytea`, which is correct and does not scale: a
// million receipt PDFs in a table is a backup nobody can restore in an
// afternoon, a replication stream carrying blobs nobody reads, and a `pg_dump`
// that grows without bound. With an object store the database holds a
// reference and the bytes live where bytes belong.
//
// # S3-compatible, not S3
//
// The endpoint is configuration, so this works against Amazon, MinIO on the
// shop's own server, Cloudflare R2, Wasabi, DigitalOcean Spaces, Ceph — any of
// them. That is not a nicety. A Saudi deployment under PDPL may be required to
// keep records inside the Kingdom, and a product that only spoke to one
// vendor's regions would be making that decision on the shop's behalf.
//
// # Written against the protocol rather than a vendor SDK
//
// Four verbs — PUT, GET, DELETE, and a presigned URL — signed with AWS
// Signature Version 4. That is about two hundred lines here against a tree of
// dependencies that brings credential chains, region resolvers, retry policies
// and an XML layer this product uses none of.
//
// The signing is the part worth being careful about, and it is worth being
// careful about only once: get it wrong and every request comes back 403 with
// a message that does not say which of the eleven canonical fields was wrong.
// It is written out step by step below for exactly that reason.
//
// # Optional
//
// Without a configured store, `Open` returns nil and every caller keeps the
// database path it already had. Nothing in this product REQUIRES object
// storage; a single-server shop should not have to run one.
package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

const (
	service       = "s3"
	algorithm     = "AWS4-HMAC-SHA256"
	unsignedBody  = "UNSIGNED-PAYLOAD"
	isoLayout     = "20060102T150405Z"
	dateLayout    = "20060102"
	maxObjectSize = 64 << 20 // 64 MiB
)

// Store is an object store.
type Store struct {
	cfg    config.Storage
	client *http.Client
	// host and scheme are split out of the endpoint once, because they are
	// needed separately by every signature.
	scheme string
	host   string
}

// Open builds a store, or returns nil when none is configured.
//
// nil is a legitimate return and every caller must handle it: an installation
// with no object store keeps the database path. A nil *Store's methods report
// that plainly rather than panicking, so a caller that forgot to check gets a
// message instead of a crash.
func Open(cfg config.Storage) *Store {
	if !cfg.Configured() {
		return nil
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	scheme, host, ok := strings.Cut(endpoint, "://")
	if !ok {
		return nil
	}
	return &Store{
		cfg:    cfg,
		scheme: scheme,
		host:   host,
		client: &http.Client{
			// Generous compared with the acquirer calls: an upload is a
			// multi-megabyte body and a shop's server is often on a domestic
			// line. Still bounded, because a request with no timeout is a
			// goroutine that never returns.
			Timeout: 2 * time.Minute,
		},
	}
}

// Configured reports whether there is a store to use.
func (s *Store) Configured() bool { return s != nil }

// Bucket is the bucket in use, for a health readout.
func (s *Store) Bucket() string {
	if s == nil {
		return ""
	}
	return s.cfg.Bucket
}

func notConfigured() error {
	return errs.New(errs.CodeUnavailable,
		"This installation has no object storage configured.")
}

// Put stores an object.
//
// `key` is the path inside the bucket. Callers are expected to namespace by
// tenant — `t/<tenant>/documents/<id>` — so a store shared between tenants
// cannot hand one tenant's key to another by a guessable path.
func (s *Store) Put(
	ctx context.Context, key, contentType string, body []byte,
) error {
	if s == nil {
		return notConfigured()
	}
	if len(body) > maxObjectSize {
		return errs.Newf(errs.CodeInvalidInput,
			"That file is %d MB, and the limit is %d MB.",
			len(body)>>20, maxObjectSize>>20)
	}

	req, err := s.request(ctx, http.MethodPut, key, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(body))

	if err := s.sign(req, sha256Hex(body)); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The object store could not be reached.")
	}
	defer resp.Body.Close()
	return s.check(resp, "storing")
}

// Get reads an object back.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil {
		return nil, notConfigured()
	}
	req, err := s.request(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeUnavailable,
			"The object store could not be reached.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errs.New(errs.CodeNotFound, "That file was not found.")
	}
	if err := s.check(resp, "reading"); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxObjectSize+1))
}

// Delete removes an object.
func (s *Store) Delete(ctx context.Context, key string) error {
	if s == nil {
		return notConfigured()
	}
	req, err := s.request(ctx, http.MethodDelete, key, nil)
	if err != nil {
		return err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The object store could not be reached.")
	}
	defer resp.Body.Close()
	// 404 on a delete is success: the object is not there, which is what was
	// asked for. Treating it as an error makes every retry fail.
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return s.check(resp, "removing")
}

// Presign builds a URL that lets a browser fetch an object directly, for a
// limited time and without a credential.
//
// This is the reason to have an object store at all on the read path: a 30MB
// report streamed through the API holds a connection, a goroutine and a
// database handle for the length of the download. A presigned URL hands the
// browser a link to the store and the API is done in a millisecond.
//
// The lifetime is short on purpose. A presigned URL is a bearer token in a
// query string: it will end up in a browser history, a proxy log and a chat
// message, and the mitigation is that it stops working.
func (s *Store) Presign(key string, expires time.Duration) (string, error) {
	return s.presignAt(key, expires, time.Now())
}

// presignAt is Presign with the clock passed in.
//
// Split out so a test can pin the timestamp and check the result against the
// signature AWS publishes for that exact request. Without a fixed clock the
// only thing a test could assert about a signature is that it is sixty-four
// hex characters, which is true of the wrong answer too.
func (s *Store) presignAt(
	key string, expires time.Duration, at time.Time,
) (string, error) {
	if s == nil {
		return "", notConfigured()
	}
	if expires <= 0 || expires > 7*24*time.Hour {
		// Seven days is S3's own ceiling for SigV4; past it the store refuses
		// with a message about the expiry rather than about the signature.
		expires = 15 * time.Minute
	}

	now := at.UTC()
	stamp := now.Format(isoLayout)
	day := now.Format(dateLayout)
	scope := strings.Join(
		[]string{day, s.cfg.Region, service, "aws4_request"}, "/")

	query := url.Values{}
	query.Set("X-Amz-Algorithm", algorithm)
	query.Set("X-Amz-Credential", s.cfg.AccessKeyID+"/"+scope)
	query.Set("X-Amz-Date", stamp)
	query.Set("X-Amz-Expires", fmt.Sprintf("%d", int(expires.Seconds())))
	query.Set("X-Amz-SignedHeaders", "host")

	// The same host the signed requests use, so a virtual-host-style bucket
	// signs against the name the browser will actually resolve.
	host := s.host_()
	path := s.path(key)
	canonical := strings.Join([]string{
		http.MethodGet,
		path,
		query.Encode(),
		"host:" + host + "\n",
		"host",
		// The body is not signed for a presigned GET; there isn't one.
		unsignedBody,
	}, "\n")

	toSign := strings.Join([]string{
		algorithm, stamp, scope, sha256Hex([]byte(canonical)),
	}, "\n")

	query.Set("X-Amz-Signature", hex.EncodeToString(
		hmacSHA256(s.signingKey(day), []byte(toSign))))

	return s.scheme + "://" + host + path + "?" + query.Encode(), nil
}

// --- the plumbing ---------------------------------------------------------

var emptyHash = sha256Hex(nil)

// path is the object's path within the endpoint, bucket included.
//
// Each SEGMENT is escaped separately: a key legitimately contains slashes
// (`t/<tenant>/documents/<id>.pdf`) and escaping the whole string would turn
// them into `%2F`, which addresses a differently named object.
func (s *Store) path(key string) string {
	segments := strings.Split(strings.TrimPrefix(key, "/"), "/")
	for i, seg := range segments {
		segments[i] = escape(seg)
	}
	joined := "/" + strings.Join(segments, "/")
	if s.cfg.PathStyle {
		return "/" + escape(s.cfg.Bucket) + joined
	}
	return joined
}

// escape is RFC 3986 unreserved-set escaping.
//
// `url.QueryEscape` is wrong here in two ways that both produce a 403: it
// encodes a space as `+` rather than `%20`, and it leaves `~` unescaped where
// S3's canonicalisation wants it escaped nowhere and `+` escaped everywhere.
// Written out because the failure is silent.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (s *Store) host_() string {
	if s.cfg.PathStyle {
		return s.host
	}
	return s.cfg.Bucket + "." + s.host
}

func (s *Store) request(
	ctx context.Context, method, key string, body io.Reader,
) (*http.Request, error) {
	target := s.scheme + "://" + s.host_() + s.path(key)
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInvalidInput,
			"That is not a usable object name.")
	}
	return req, nil
}

// sign applies AWS Signature Version 4 to a request.
//
// The four steps, in the order the specification gives them, because getting
// any of them wrong produces the same unhelpful 403:
//
//  1. Build a canonical request: method, path, sorted query, sorted signed
//     headers, the list of those header names, and the hash of the body.
//  2. Build a string to sign from the algorithm, the timestamp, the
//     credential scope and the hash of step 1.
//  3. Derive a signing key by chaining HMACs over date, region, service and
//     the literal "aws4_request".
//  4. HMAC step 2 with step 3.
func (s *Store) sign(req *http.Request, bodyHash string) error {
	now := time.Now().UTC()
	stamp := now.Format(isoLayout)
	day := now.Format(dateLayout)

	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)
	// Set explicitly rather than left to the transport: the signature covers
	// the Host header, and a value added after signing would not match.
	req.Host = s.host_()

	// Every header that is signed, lowercased and sorted by name. `host` is
	// not in req.Header at all — Go keeps it on the struct — so it is added
	// here by hand, which is the single most common way a hand-written SigV4
	// implementation fails.
	signed := map[string]string{"host": req.Host}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
			signed[lower] = strings.TrimSpace(strings.Join(values, ","))
		}
	}
	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sort.Strings(names)

	var headerBlock strings.Builder
	for _, name := range names {
		headerBlock.WriteString(name)
		headerBlock.WriteString(":")
		headerBlock.WriteString(signed[name])
		headerBlock.WriteString("\n")
	}
	signedNames := strings.Join(names, ";")

	// The canonical path is the request's own escaped path, not a second
	// construction from the key. Two constructions of one string that must
	// match byte for byte is how a signature ends up almost right.
	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		headerBlock.String(),
		signedNames,
		bodyHash,
	}, "\n")

	scope := strings.Join(
		[]string{day, s.cfg.Region, service, "aws4_request"}, "/")
	toSign := strings.Join([]string{
		algorithm, stamp, scope, sha256Hex([]byte(canonical)),
	}, "\n")

	signature := hex.EncodeToString(
		hmacSHA256(s.signingKey(day), []byte(toSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, s.cfg.AccessKeyID, scope, signedNames, signature))
	return nil
}

// signingKey derives the request key by chained HMAC, which is what makes a
// leaked signature useless tomorrow and in another region.
func (s *Store) signingKey(day string) []byte {
	k := hmacSHA256([]byte("AWS4"+s.cfg.SecretAccessKey), []byte(day))
	k = hmacSHA256(k, []byte(s.cfg.Region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// check turns a non-2xx response into an error carrying what the store said.
func (s *Store) check(resp *http.Response, what string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// Capped: an error page is not a document.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return errs.Newf(errs.CodeUnavailable,
			"The object store refused the credentials while %s a file.", what)
	case http.StatusNotFound:
		return errs.New(errs.CodeNotFound, "That file was not found.")
	}
	return errs.Newf(errs.CodeUnavailable,
		"The object store answered %d while %s a file: %s",
		resp.StatusCode, what, message)
}

// Ping proves the credentials and the bucket work, without writing anything.
//
// A HEAD on the bucket: 200 means it exists and the key may read it, 403 means
// the credentials are wrong, 404 means the bucket name is. All three are worth
// telling an operator apart at start-up rather than on the first upload.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil {
		return notConfigured()
	}
	target := s.scheme + "://" + s.host_() + "/"
	if s.cfg.PathStyle {
		target = s.scheme + "://" + s.host + "/" + escape(s.cfg.Bucket)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The object store could not be reached.")
	}
	defer resp.Body.Close()
	return s.check(resp, "checking")
}
