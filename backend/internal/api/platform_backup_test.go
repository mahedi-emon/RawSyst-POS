//go:build integration

// Who may reach a backup, and what a backup route refuses.
//
// # The one property this file exists to hold
//
// A full database dump is every business on this server at once — their
// customers, their staff's pay, their bank details. There is no tenant
// permission that could safely reach it: a permission a business owner can
// grant themselves would be a permission that lets one shop download another
// shop's books.
//
// So every route is `AccessSuperAdmin`, and these tests attempt every one of
// them as an Owner holding every permission their plan offers. All must answer
// 404 — not 403, which would confirm the route exists.
//
// # And what it refuses even to an operator who may reach it
//
// A snapshot id that is a path. A production restore with the wrong
// confirmation, or with no rehearsal behind it. A download of something nobody
// has proved restores. An upload with no manifest, or with a manifest that does
// not describe the file beside it. Each of those is a way a backup system tells
// a comfortable lie, and each has a test here.
package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// everyBackupRoute is the whole surface, as the router table declares it.
//
// Written out rather than derived from `s.Routes()`, so that adding a route
// without adding it here is a visible omission rather than a silently widened
// test.
var everyBackupRoute = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/platform/backups/health"},
	{http.MethodGet, "/api/v1/platform/backups"},
	{http.MethodPost, "/api/v1/platform/backups"},
	{http.MethodGet, "/api/v1/platform/backups/tasks"},
	{http.MethodGet, "/api/v1/platform/backups/tasks/00000000-0000-0000-0000-000000000001"},
	{http.MethodPost, "/api/v1/platform/backups/upload"},
	{http.MethodGet, "/api/v1/platform/backups/20260301T100000Z-1"},
	{http.MethodGet, "/api/v1/platform/backups/20260301T100000Z-1/download/dump"},
	{http.MethodGet, "/api/v1/platform/backups/20260301T100000Z-1/download/manifest"},
	{http.MethodGet, "/api/v1/platform/backups/20260301T100000Z-1/download/checksum"},
	{http.MethodPost, "/api/v1/platform/backups/20260301T100000Z-1/verify"},
	{http.MethodPost, "/api/v1/platform/backups/20260301T100000Z-1/validate-restore"},
	{http.MethodPost, "/api/v1/platform/backups/20260301T100000Z-1/restore-production"},
	{http.MethodPost, "/api/v1/platform/backups/prune"},
	{http.MethodGet, "/api/v1/platform/maintenance"},
	{http.MethodPut, "/api/v1/platform/maintenance"},
}

// The blueprint's own rule, applied to the most dangerous surface in the
// product: a business owner may not download the database.
func TestABusinessOwnerCannotReachAnyBackupRoute(t *testing.T) {
	h := newHarness(t)
	owner := h.login(t, h.seedUserWithRole(t, "owner"))

	for _, rt := range everyBackupRoute {
		res := h.do(t, rt.method, rt.path, owner, map[string]any{})
		body := readBody(t, res)
		res.Body.Close()

		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s answered %d to a business owner; every platform "+
				"backup route must answer 404. Body: %s",
				rt.method, rt.path, res.StatusCode, body)
		}
		// 404 rather than 403 on purpose. Confirming that a platform endpoint
		// exists tells somebody where to aim.
		if strings.Contains(strings.ToLower(body), "permission") {
			t.Errorf("%s %s told a business owner which permission it wants",
				rt.method, rt.path)
		}
	}
}

// A cashier is further from these routes than an owner, and gets the same
// answer. Included because the failure mode is a route gated on a permission
// somebody could be granted rather than on the workspace they are in.
func TestACashierCannotReachAnyBackupRoute(t *testing.T) {
	h := newHarness(t)
	cashier := h.login(t, h.seedUserWithRole(t, "cashier"))

	for _, rt := range everyBackupRoute {
		res := h.do(t, rt.method, rt.path, cashier, map[string]any{})
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s answered %d to a cashier", rt.method, rt.path,
				res.StatusCode)
		}
	}
}

// An owner who has given themselves the tenant-level backup permissions still
// cannot reach the platform's backups. This is the test that says the boundary
// is the workspace and not a permission.
func TestBackupPermissionsDoNotReachThePlatformsBackups(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")
	h.grantPermissions(t, email, "backup.view", "backup.run")
	owner := h.login(t, email)

	// Their own tenant's record is theirs to read: that route still works.
	res := h.do(t, http.MethodGet, "/api/v1/backups/health", owner, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("a tenant's own backup health answered %d", res.StatusCode)
	}

	// The platform's is not.
	res = h.do(t, http.MethodGet, "/api/v1/platform/backups", owner, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("backup.view reached the platform's backups: %d", res.StatusCode)
	}
	res = h.do(t, http.MethodGet,
		"/api/v1/platform/backups/20260301T100000Z-1/download/dump", owner, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("backup.run reached a backup download: %d", res.StatusCode)
	}
}

func TestSignedOutReachesNoBackupRoute(t *testing.T) {
	h := newHarness(t)
	for _, rt := range everyBackupRoute {
		res := h.do(t, rt.method, rt.path, "", map[string]any{})
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d with no token", rt.method, rt.path,
				res.StatusCode)
		}
	}
}

// --- what an operator is refused --------------------------------------------

// A snapshot id becomes a path inside a bucket and the name of a database.
// Nothing that could be either reaches the store.
func TestASnapshotIDThatIsAPathIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	for _, bad := range []string{
		"..", "..%2f..%2fetc%2fpasswd", "a%20b", "a%00b",
		"%2e%2e%2f%2e%2e%2fetc", "a'b", "a%22b",
	} {
		path := "/api/v1/platform/backups/" + bad
		res := h.do(t, http.MethodGet, path, admin, nil)
		res.Body.Close()
		// 404 either from the router not matching, or from the handler
		// refusing the id. Both are the right answer; a 200 or a 500 is not.
		if res.StatusCode != http.StatusNotFound &&
			res.StatusCode != http.StatusBadRequest &&
			res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("snapshot id %q answered %d", bad, res.StatusCode)
		}
	}
}

// The download of a part this product does not have is refused by name rather
// than turned into an object key.
func TestOnlyTheThreePartsOfABackupCanBeAskedFor(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	res := h.do(t, http.MethodGet,
		"/api/v1/platform/backups/20260301T100000Z-1/download/etc", admin, nil)
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Fatalf("an unknown part was served: %s", body)
	}
}

// The confirmation is the snapshot id typed out. A checkbox is a thing people
// click; the name of the thing about to replace their database is a thing they
// have to look at.
func TestAProductionRestoreNeedsTheSnapshotIDTypedOut(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	for _, confirm := range []string{"", "yes", "YES", "confirm", "20260301T100000Z-2"} {
		res := h.do(t, http.MethodPost,
			"/api/v1/platform/backups/20260301T100000Z-1/restore-production",
			admin, map[string]any{"confirm": confirm})
		body := readBody(t, res)
		res.Body.Close()

		if res.StatusCode == http.StatusAccepted {
			t.Fatalf("a production restore was accepted with confirmation %q", confirm)
		}
		if !strings.Contains(body, "20260301T100000Z-1") {
			t.Errorf("the refusal for %q does not say what to type: %s", confirm, body)
		}
	}
}

// --- uploads ----------------------------------------------------------------

// A dump with no manifest cannot be checked against anything, so it is not
// kept. This is the whole reason the manifest is required rather than optional.
func TestAnUploadWithNoManifestIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	body, contentType := multipartUpload(t, map[string][]byte{
		"dump": []byte("PGDMP\x01\x0e\x00 and then some bytes"),
	})
	res := h.upload(t, "/api/v1/platform/backups/upload", admin, contentType, body)
	text := readBody(t, res)
	res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("a dump with no manifest was accepted: %s", text)
	}
	if !strings.Contains(text, "manifest") {
		t.Errorf("the refusal does not say what is missing: %s", text)
	}
}

// The filename is never used. The three names are composed from the snapshot id
// inside the manifest, so nothing from the request becomes a path.
func TestAnUploadedFilenameIsNeverUsedAsAPath(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	dump := []byte("PGDMP\x01\x0e\x00 pretend dump")
	manifest := manifestFor(t, "../../../../etc/passwd", dump)

	body, contentType := multipartNamed(t, []part{
		{name: "dump", filename: "../../../../etc/cron.d/evil", body: dump},
		{name: "manifest", filename: "../../manifest.json", body: manifest},
	})
	res := h.upload(t, "/api/v1/platform/backups/upload", admin, contentType, body)
	text := readBody(t, res)
	res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("a manifest naming a path as its snapshot was accepted: %s", text)
	}
	if !strings.Contains(strings.ToLower(text), "snapshot") {
		t.Errorf("the refusal does not name the problem: %s", text)
	}
}

// A dump that does not hash to what its manifest says is not the file the
// manifest describes, whatever else it is.
func TestAnUploadThatDoesNotMatchItsManifestIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	dump := []byte("PGDMP\x01\x0e\x00 the real bytes")
	manifest := manifestFor(t, "20260301T100000Z-9", dump)

	// One byte different, same length. A size check alone would pass this.
	altered := append([]byte(nil), dump...)
	altered[len(altered)-1] ^= 0x01

	body, contentType := multipartUpload(t, map[string][]byte{
		"dump": altered, "manifest": manifest,
	})
	res := h.upload(t, "/api/v1/platform/backups/upload", admin, contentType, body)
	text := readBody(t, res)
	res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("an altered dump was accepted: %s", text)
	}
	if !strings.Contains(text, "hashes to") {
		t.Errorf("the refusal does not say the checksum disagreed: %s", text)
	}
}

// A file that is neither a dump nor a sealed backup is not something
// `pg_restore` will read, and finding that out now beats finding it out during
// a recovery.
func TestAnUploadThatIsNotADumpIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	notADump := []byte("#!/bin/sh\necho this is not a database\n")
	manifest := manifestFor(t, "20260301T100000Z-8", notADump)

	body, contentType := multipartUpload(t, map[string][]byte{
		"dump": notADump, "manifest": manifest,
	})
	res := h.upload(t, "/api/v1/platform/backups/upload", admin, contentType, body)
	text := readBody(t, res)
	res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("a shell script was accepted as a backup: %s", text)
	}
	if !strings.Contains(text, "does not begin like") {
		t.Errorf("the refusal does not say what the file is not: %s", text)
	}
}

// A .sha256 file that disagrees with the manifest means somebody edited one of
// them. Both describe the same dump and there is no version of this in which
// proceeding is right.
func TestAChecksumFileThatDisagreesWithTheManifestIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	dump := []byte("PGDMP\x01\x0e\x00 bytes")
	manifest := manifestFor(t, "20260301T100000Z-7", dump)

	body, contentType := multipartUpload(t, map[string][]byte{
		"dump":     dump,
		"manifest": manifest,
		"checksum": []byte(strings.Repeat("0", 64) + "  RawSyst_Backup_x.dump\n"),
	})
	res := h.upload(t, "/api/v1/platform/backups/upload", admin, contentType, body)
	text := readBody(t, res)
	res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("a mismatched checksum file was accepted: %s", text)
	}
}

// --- maintenance ------------------------------------------------------------

// The write freeze is what stops a sale being rung up after the final backup of
// a migration and lost when the old server goes away. It has to actually stop
// one.
func TestTheWriteFreezeStopsWritesAndLeavesReadsAlone(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	f := h.seedShop(t, "owner")
	owner := h.tokenForUser(t, f)

	t.Cleanup(func() {
		res := h.do(t, http.MethodPut, "/api/v1/platform/maintenance", admin,
			map[string]any{"active": false})
		res.Body.Close()
	})

	res := h.do(t, http.MethodPut, "/api/v1/platform/maintenance", admin,
		map[string]any{"active": true, "reason": "Moving to another server."})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintenance could not be started: %d", res.StatusCode)
	}

	// The cache is two seconds wide by design; see internal/maintenance. A
	// freeze that took effect instantly would need a query in front of every
	// request, which is a worse trade for a flag that changes twice a year.
	categories := taxonomyPath(f, "/api/v1/catalog/categories")
	waitForFreeze(t, h, owner, categories, true)

	// A read still works. A cashier looking at yesterday's totals writes
	// nothing and loses nothing.
	res = h.do(t, http.MethodGet, categories, owner, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("a read was refused during a write freeze: %d", res.StatusCode)
	}

	// A write does not.
	res = h.do(t, http.MethodPost, categories, owner,
		map[string]any{"name": "Written during a freeze"})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a write was accepted during a freeze: %d %s", res.StatusCode, body)
	}
	// 503 rather than 403: this is temporary and a client that retries later
	// is doing the right thing.
	if !strings.Contains(body, "Moving to another server") {
		t.Errorf("the refusal does not carry the operator's own words: %s", body)
	}

	// And a platform operator keeps working, or the only way to end a freeze
	// would be a database client.
	res = h.do(t, http.MethodPost, "/api/v1/platform/backups", admin, map[string]any{})
	res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		t.Error("the platform operator performing the migration was frozen out")
	}
}

func waitForFreeze(t *testing.T, h *harness, token, path string, want bool) {
	t.Helper()
	var last int
	var lastBody string
	for range 40 {
		res := h.do(t, http.MethodPost, path, token,
			map[string]any{"name": "freeze probe"})
		frozen := res.StatusCode == http.StatusServiceUnavailable
		last = res.StatusCode
		lastBody = readBody(t, res)
		res.Body.Close()
		if frozen == want {
			return
		}
		sleepABit()
	}
	t.Fatalf("the write freeze did not reach the request path; the probe "+
		"answered %d: %s", last, lastBody)
}

// --- helpers ----------------------------------------------------------------

type part struct {
	name, filename string
	body           []byte
}

func multipartUpload(t *testing.T, fields map[string][]byte) (*bytes.Buffer, string) {
	t.Helper()
	parts := make([]part, 0, len(fields))
	for name, body := range fields {
		parts = append(parts, part{name: name, filename: name + ".bin", body: body})
	}
	return multipartNamed(t, parts)
}

func multipartNamed(t *testing.T, parts []part) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		f, err := w.CreateFormFile(p.name, p.filename)
		if err != nil {
			t.Fatalf("build multipart: %v", err)
		}
		if _, err := f.Write(p.body); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// manifestFor builds a manifest that correctly describes the bytes given, so a
// test that changes one thing changes only that thing.
func manifestFor(t *testing.T, snapshotID string, dump []byte) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"manifest_version": 2,
		"snapshot_id":      snapshotID,
		"taken_at":         "2026-03-01T10:00:00Z",
		"schema_version":   135,
		"table_count":      186,
		"database_name":    "rawsyst",
		"database": map[string]any{
			"key":    "database.dump",
			"bytes":  len(dump),
			"sha256": sha256Hex(dump),
		},
	})
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return body
}

// upload posts a multipart body, which is the one shape `h.do` cannot send.
func (h *harness) upload(
	t *testing.T, path, token, contentType string, body *bytes.Buffer,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload %s: %v", path, err)
	}
	return res
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// sleepABit is one cache window, so a freeze written a moment ago has reached
// the request path. See internal/maintenance for why the window exists.
func sleepABit() { time.Sleep(250 * time.Millisecond) }
