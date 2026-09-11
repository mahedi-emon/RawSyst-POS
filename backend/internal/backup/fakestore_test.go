// An object store that lives inside the test.
//
// # Why not a mock
//
// Because the thing worth testing is the behaviour of the real `blob.Store`
// against a real HTTP server: the SigV4 signing, the streaming upload with a
// pre-computed checksum, the paged listing, the 404 that a missing segment has
// to come back as. A mock of `blob.Store` would assert that this product calls
// its own interface, which is a test of nothing.
//
// So this is a real HTTP server implementing the five verbs this product uses,
// backed by a map. The signature is not verified — that is the one thing a fake
// cannot usefully check, since it would only be checking this code against
// itself — but everything else about the exchange is real.
//
// # What it can be told to do
//
// Fail. The failure modes point-in-time recovery has to survive are almost all
// storage failures: the bucket unreachable, an object that comes back
// truncated, an object that was deleted from under a manifest, bytes that have
// been altered. Each of those is one field here, and the tests set them
// directly rather than by taking a real bucket apart.
package backup

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/blob"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	written map[string]time.Time

	server *httptest.Server
	bucket string

	// down makes every request fail, as an unreachable store does.
	down bool

	// failPutsMatching makes writes to keys containing this substring fail, so
	// a test can break the archive in one specific place rather than all of it.
	failPutsMatching string

	// puts and gets are counted so a test can assert that a check which claims
	// to be cheap actually is.
	puts, gets, lists, deletes int
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{
		objects: map[string][]byte{},
		written: map[string]time.Time{},
		bucket:  "rawsyst-test",
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// store is the real blob.Store, pointed at the fake.
func (f *fakeStore) store() *blob.Store {
	return blob.Open(config.Storage{
		Endpoint:        f.server.URL,
		Region:          "us-east-1",
		Bucket:          f.bucket,
		AccessKeyID:     "test",
		SecretAccessKey: "test",
		PathStyle:       true,
	})
}

func (f *fakeStore) walOptions() WALOptions {
	return WALOptions{
		Store:   f.store(),
		Prefix:  "rawsyst",
		Layout:  DefaultWALLayout(),
		Timeout: 30 * time.Second,
		Retries: 1,
	}
}

func (f *fakeStore) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.down {
		http.Error(w, "the store is down", http.StatusServiceUnavailable)
		return
	}

	// Path style: /<bucket>/<key>.
	path := strings.TrimPrefix(r.URL.Path, "/")
	key := strings.TrimPrefix(strings.TrimPrefix(path, f.bucket), "/")

	switch r.Method {
	case http.MethodPut:
		f.puts++
		if f.failPutsMatching != "" && strings.Contains(key, f.failPutsMatching) {
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = body
		f.written[key] = time.Now().UTC()
		w.WriteHeader(http.StatusOK)

	case http.MethodHead:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)

	case http.MethodGet:
		if r.URL.Query().Get("list-type") == "2" {
			f.lists++
			f.list(w, r.URL.Query().Get("prefix"))
			return
		}
		f.gets++
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)

	case http.MethodDelete:
		f.deletes++
		delete(f.objects, key)
		delete(f.written, key)
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeStore) list(w http.ResponseWriter, prefix string) {
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><ListBucketResult>`)
	for _, k := range keys {
		b.WriteString("<Contents><Key>")
		b.WriteString(escapeXML(k))
		b.WriteString("</Key><Size>")
		b.WriteString(strconv.Itoa(len(f.objects[k])))
		b.WriteString("</Size><LastModified>")
		b.WriteString(f.written[k].Format(time.RFC3339))
		b.WriteString("</LastModified></Contents>")
	}
	b.WriteString("<IsTruncated>false</IsTruncated></ListBucketResult>")

	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, b.String())
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// --- what a test does to it -------------------------------------------------

func (f *fakeStore) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeStore) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

func (f *fakeStore) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
}

// corrupt alters one byte in the middle of an object, which is what silent
// corruption in a store looks like: the right length, the wrong contents.
func (f *fakeStore) corrupt(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := f.objects[key]
	if len(body) == 0 {
		return
	}
	altered := append([]byte(nil), body...)
	altered[len(altered)/2] ^= 0xFF
	f.objects[key] = altered
}

// truncate cuts an object short, which is what a half-finished upload looks
// like to anything that did not check the length.
func (f *fakeStore) truncate(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := f.objects[key]
	if len(body) < 4 {
		return
	}
	f.objects[key] = body[:len(body)/2]
}

func (f *fakeStore) keysUnder(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (f *fakeStore) age(key string, by time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if at, ok := f.written[key]; ok {
		f.written[key] = at.Add(-by)
	}
}

// --- small helpers the tests share ------------------------------------------

func asBlobObjects(listing []storeListing) []blob.Object {
	out := make([]blob.Object, 0, len(listing))
	for _, l := range listing {
		out = append(out, blob.Object{
			Key: l.Key, Size: l.Size,
			LastModified: time.Now().Add(-30 * 24 * time.Hour),
		})
	}
	return out
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// errUnreachable is what a store that will not answer looks like.
func errUnreachable() error {
	return errs.New(errs.CodeUnavailable, "The object store could not be reached.")
}

// errWithURL is what a store error looks like when it carries a signed URL,
// which is the thing that must never reach a row an operator can read.
func errWithURL() error {
	return errs.New(errs.CodeForbidden,
		"403 from https://bucket.example/rawsyst/wal/00000001/x"+
			"?X-Amz-Signature=deadbeefsignature&X-Amz-Expires=900")
}
