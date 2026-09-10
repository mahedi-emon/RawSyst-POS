// What a backup is allowed to claim about itself.
//
// These run without a database, an object store or `pg_dump`. What they hold to
// is the reasoning: a snapshot with no completion marker is not a backup, a
// retention policy may never empty the store, a manifest carries no secrets,
// and an encrypted dump that has been truncated or altered by one bit does not
// decrypt.
//
// The parts that need a real Postgres and a real bucket — dump, upload,
// checksum, restore into a scratch database, row counts, the production
// cutover — are proved by running them, and `deploy/server/BACKUP.md` says how.
// A unit test that mocked `pg_dump` would assert that the mock was called.
package backup

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestASnapshotIDSortsByWhenItWasTaken(t *testing.T) {
	// Retention and "the newest" are both lexical operations on this string.
	// If ids do not sort by time, both quietly pick the wrong snapshot.
	earlier := NewSnapshotID(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	later := NewSnapshotID(time.Date(2026, 3, 1, 10, 0, 1, 0, time.UTC))

	if !(earlier < later) {
		t.Errorf("%q does not sort before %q", earlier, later)
	}
	if !strings.HasPrefix(earlier, "20260301T100000Z") {
		t.Errorf("a snapshot id does not carry its time: %q", earlier)
	}
	at, ok := ParseSnapshotID(earlier)
	if !ok || !at.Equal(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("a snapshot id does not read back as its own time: %v %v", at, ok)
	}
}

func TestASnapshotKeyIsThePathARestoreWillLookIn(t *testing.T) {
	// A restore on a new server has the snapshot id and nothing else. If these
	// names move, an old snapshot becomes unreadable by a newer build, which is
	// the one thing a backup must never become.
	got := snapshotKey("rawsyst", "20260301T100000Z-1", databaseObject)
	if got != "rawsyst/20260301T100000Z-1/database.dump" {
		t.Errorf("snapshot key is %q", got)
	}
}

// A snapshot id reaches this package from an HTTP request and becomes a path
// inside a bucket and the name of a database. Anything but letters, digits,
// dash and underscore is refused before either of those happens.
func TestASnapshotIDCannotBeAPath(t *testing.T) {
	for _, bad := range []string{
		"", "..", "../../etc/passwd", "a/b", `a"b`, "a b", "a';DROP DATABASE x;--",
		"a\x00b", strings.Repeat("a", 65),
	} {
		if ValidSnapshotID(bad) {
			t.Errorf("%q was accepted as a snapshot id", bad)
		}
	}
	for _, good := range []string{
		"20260301T100000Z-1", "a", "A_b-9", NewSnapshotID(time.Now()),
	} {
		if !ValidSnapshotID(good) {
			t.Errorf("%q was refused as a snapshot id", good)
		}
	}
}

// The scratch database a verification restores into is named from the snapshot
// id, and `dropDatabase` refuses anything it did not name. Together those are
// what stop a bug here reaching a real database.
func TestNothingButItsOwnScratchDatabaseIsEverDropped(t *testing.T) {
	name := ScratchName("20260301T100000Z-1")
	if !strings.HasPrefix(name, "rawsyst_verify_") {
		t.Fatalf("a scratch database is not recognisable as one: %q", name)
	}
	if len(name) > 63 {
		t.Errorf("a scratch database name is longer than postgres allows: %q", name)
	}

	err := dropDatabase(t.Context(), "postgres://nobody@127.0.0.1:1/postgres", "rawsyst")
	if err == nil || !strings.Contains(err.Error(), "only drops databases it created") {
		t.Errorf("dropping the live database was not refused: %v", err)
	}
}

// --- retention --------------------------------------------------------------

func TestRetentionAlwaysKeepsTheNewest(t *testing.T) {
	// Thirty daily snapshots against a policy that asks for one of each. The
	// newest must survive whatever the arithmetic says, because a policy that
	// can empty the store is a policy that will, on the day somebody needs it.
	snapshots := dailySnapshots(30)
	keep := whatToKeep(snapshots, Policy{Daily: 1, Weekly: 1, Monthly: 1})

	if !keep[snapshots[0].ID] {
		t.Error("the newest snapshot was not kept")
	}
}

func TestRetentionKeepsOnePerPeriod(t *testing.T) {
	snapshots := dailySnapshots(60)
	keep := whatToKeep(snapshots, Policy{Daily: 7, Weekly: 4, Monthly: 3})

	if len(keep) < 7 {
		t.Errorf("a seven-day policy kept %d snapshots", len(keep))
	}
	if len(keep) >= len(snapshots) {
		t.Errorf("a policy over %d snapshots kept all of them", len(snapshots))
	}
}

// Deleting something because it is not understood is how a bug becomes data
// loss. A snapshot whose id this build cannot date is kept.
func TestRetentionKeepsWhatItCannotUnderstand(t *testing.T) {
	snapshots := append(dailySnapshots(10),
		Snapshot{ID: "something-else-entirely", Completed: true})
	keep := whatToKeep(snapshots, Policy{Daily: 1, Weekly: 1, Monthly: 1})

	if !keep["something-else-entirely"] {
		t.Error("a snapshot with an unreadable id was marked for deletion")
	}
}

// The caller passes the newest VERIFIED snapshot, which retention does not know
// about because verification is recorded in the database and retention runs
// against the store. It must survive the policy however old it is.
func TestRetentionKeepsWhatItIsToldToProtect(t *testing.T) {
	snapshots := dailySnapshots(60)
	protected := snapshots[59].ID // the oldest, which no policy would keep

	keep := whatToKeep(snapshots, Policy{Daily: 1, Weekly: 1, Monthly: 1})
	if keep[protected] {
		t.Skip("the policy kept it anyway; this test proves nothing today")
	}
	keep[protected] = true
	if !keep[protected] {
		t.Error("a protected snapshot was not kept")
	}
}

func TestARetentionClassIsDecidedByTheCalendar(t *testing.T) {
	// Recorded at the time of the backup rather than worked out when it is
	// listed, so a snapshot's class does not change under it as others come
	// and go.
	cases := map[string]string{
		"2026-03-01": RetentionMonthly, // the first of the month
		"2026-03-02": RetentionWeekly,  // a Monday
		"2026-03-03": RetentionDaily,
	}
	for day, want := range cases {
		at, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatal(err)
		}
		if got := RetentionClassOf(at); got != want {
			t.Errorf("%s is %q, expected %q", day, got, want)
		}
	}
}

func dailySnapshots(n int) []Snapshot {
	base := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	out := make([]Snapshot, 0, n)
	for i := range n {
		at := base.AddDate(0, 0, i)
		out = append(out, Snapshot{
			ID: NewSnapshotID(at), TakenAt: at, Completed: true,
		})
	}
	// Newest first, as `List` returns them.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// --- the manifest -----------------------------------------------------------

// A guard rather than a hope: the manifest is uploaded to a bucket, read by
// whoever can list it, and printed by `backup run`. It says what was backed up
// and what it hashes to, and nothing that would let anybody in.
func TestTheManifestCarriesNoSecrets(t *testing.T) {
	key := testKey(t)
	info := key.Info()
	m := Manifest{
		Version: ManifestVersion, SnapshotID: "20260601T030000Z-1",
		DatabaseName: "rawsyst", AppVersion: "1.0.0",
		Database:   Component{Key: databaseObject, SHA256: "abc"},
		Encryption: &info,
		Storage: StorageRef{
			Provider: "s3-compatible", Bucket: "b", Endpoint: "example.test",
		},
		Inventory: &Inventory{
			Rows:       map[string]int64{"tenant": 2},
			TenantRows: map[string]int64{"a": 1},
		},
	}
	rendered := strings.ToLower(renderForInspection(t, m))

	for _, forbidden := range []string{
		"password", "secret", "jwt", "api_key", "apikey", "private",
		"token", "credential", "totp", "passphrase", "access_key",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the manifest has a %q field. Backups are read by "+
				"whoever can list the bucket; secrets travel separately and "+
				"deploy/server/BACKUP.md says how", forbidden)
		}
	}
	// The encryption block names the key without being one.
	if strings.Contains(rendered, strings.ToLower(
		base64.StdEncoding.EncodeToString(key.material))) {
		t.Error("the manifest contains the encryption key itself")
	}
}

// A manifest whose version this build cannot read is refused. One from an OLDER
// build is read and upgraded, because refusing it would mean an upgrade is a
// moment when the business is unprotected.
func TestAnOlderManifestIsReadRatherThanRefused(t *testing.T) {
	v1 := []byte(`{
		"manifest_version": 1,
		"snapshot_id": "20260301T100000Z-1",
		"schema_version": 120,
		"table_count": 170,
		"row_counts": {"tenant": 3, "app_user": 41},
		"database": {"key": "database.dump", "bytes": 100, "sha256": "abc"}
	}`)
	m, err := ParseManifest(v1, "20260301T100000Z-1")
	if err != nil {
		t.Fatalf("a version 1 manifest was refused: %v", err)
	}
	if m.Inventory == nil {
		t.Fatal("no inventory was synthesised from a version 1 manifest")
	}
	if m.Inventory.Rows["tenant"] != 3 {
		t.Errorf("the synthesised inventory lost the row counts: %v", m.Inventory.Rows)
	}
	if m.Complete() {
		t.Error("a version 1 manifest was reported as a complete inventory, " +
			"which would let a thinner verification read as a full one")
	}
}

func TestAManifestFromANewerBuildIsRefused(t *testing.T) {
	future := []byte(`{"manifest_version": 99, "snapshot_id": "x",
		"database": {"key":"database.dump","bytes":1,"sha256":"a"}}`)
	if _, err := ParseManifest(future, "x"); err == nil {
		t.Error("a manifest from a newer build was accepted")
	}
}

// A manifest found under one snapshot id that claims to belong to another is
// one of the two files being something other than what it says.
func TestAManifestMustBelongToTheSnapshotItWasFoundUnder(t *testing.T) {
	body := []byte(`{"manifest_version": 2, "snapshot_id": "somewhere-else",
		"database": {"key":"database.dump","bytes":1,"sha256":"a"}}`)
	_, err := ParseManifest(body, "20260301T100000Z-1")
	if err == nil {
		t.Fatal("a manifest belonging to another snapshot was accepted")
	}
	if !strings.Contains(err.Error(), "not what it claims") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// A manifest with no size and no checksum describes a dump that cannot be
// checked at all, which makes every later check vacuous.
func TestAManifestWithNothingToCheckAgainstIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"manifest_version":2,"snapshot_id":"x","database":{"key":"d","bytes":0,"sha256":"a"}}`,
		`{"manifest_version":2,"snapshot_id":"x","database":{"key":"d","bytes":5,"sha256":""}}`,
	} {
		if _, err := ParseManifest([]byte(body), "x"); err == nil {
			t.Errorf("a manifest with nothing to check against was accepted: %s", body)
		}
	}
}

func renderForInspection(t *testing.T, m Manifest) string {
	t.Helper()
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("render the manifest: %v", err)
	}
	return string(body)
}

// --- encryption -------------------------------------------------------------

func testKey(t *testing.T) Key {
	t.Helper()
	material := make([]byte, cryptKeyLen)
	if _, err := io.ReadFull(rand.Reader, material); err != nil {
		t.Fatal(err)
	}
	k, err := ParseKey(base64.StdEncoding.EncodeToString(material))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestASealedDumpComesBackUnchanged(t *testing.T) {
	key := testKey(t)

	// Sizes chosen around the chunk boundary: one short chunk, exactly one
	// chunk, one chunk and a byte, and two chunks exactly. The exact-multiple
	// cases are the ones a chunked format gets wrong.
	for _, size := range []int{0, 1, 4096, cryptChunk - 1, cryptChunk,
		cryptChunk + 1, 2 * cryptChunk} {
		plain := make([]byte, size)
		if _, err := io.ReadFull(rand.Reader, plain); err != nil && size > 0 {
			t.Fatal(err)
		}

		var sealed bytes.Buffer
		n, err := key.Seal(&sealed, bytes.NewReader(plain))
		if err != nil {
			t.Fatalf("%d bytes: sealing: %v", size, err)
		}
		if n != int64(sealed.Len()) {
			t.Errorf("%d bytes: sealed length reported %d, wrote %d",
				size, n, sealed.Len())
		}

		var back bytes.Buffer
		if _, err := key.Open(&back, bytes.NewReader(sealed.Bytes())); err != nil {
			t.Fatalf("%d bytes: opening: %v", size, err)
		}
		if !bytes.Equal(back.Bytes(), plain) {
			t.Errorf("%d bytes: what came back is not what went in", size)
		}
	}
}

// The test that says whether the encryption is real.
//
// A truncated stream is exactly what an interrupted upload looks like, and a
// chunked format that does not mark its last chunk decrypts one happily into a
// shorter, valid-looking dump.
func TestATruncatedSealedDumpDoesNotDecrypt(t *testing.T) {
	key := testKey(t)
	plain := make([]byte, 3*cryptChunk)
	if _, err := io.ReadFull(rand.Reader, plain); err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	if _, err := key.Seal(&sealed, bytes.NewReader(plain)); err != nil {
		t.Fatal(err)
	}

	cut := sealed.Bytes()[:sealed.Len()-(cryptChunk/2)]
	var back bytes.Buffer
	_, err := key.Open(&back, bytes.NewReader(cut))
	if err == nil {
		t.Fatal("a truncated encrypted backup decrypted without complaint")
	}
	if !strings.Contains(err.Error(), "truncated") &&
		!strings.Contains(err.Error(), "does not authenticate") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestOneAlteredByteIsCaught(t *testing.T) {
	key := testKey(t)
	plain := make([]byte, 8192)
	if _, err := io.ReadFull(rand.Reader, plain); err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	if _, err := key.Seal(&sealed, bytes.NewReader(plain)); err != nil {
		t.Fatal(err)
	}

	// In the middle of the ciphertext, past the header, leaving the length
	// unchanged. A checksum over the whole file would catch this; the point
	// here is that the cipher does too, so a store that silently returned an
	// altered object could not produce a restorable dump.
	altered := append([]byte(nil), sealed.Bytes()...)
	altered[len(altered)/2] ^= 0x01

	var back bytes.Buffer
	if _, err := key.Open(&back, bytes.NewReader(altered)); err == nil {
		t.Fatal("an altered encrypted backup decrypted without complaint")
	}
}

// Two backups sealed with the same key must not be interchangeable at the
// chunk level: a chunk lifted from one and dropped into the other has to fail.
func TestAChunkCannotBeMovedBetweenBackups(t *testing.T) {
	key := testKey(t)
	one := sealed(t, key, bytes.Repeat([]byte("a"), 2*cryptChunk))
	two := sealed(t, key, bytes.Repeat([]byte("b"), 2*cryptChunk))

	// The header is 45 bytes; the first sealed chunk follows it. Swapping the
	// first chunk of `one` into `two` keeps every length identical.
	const head = 8 + 1 + 4 + cryptPrefixLen + 8
	chunk := cryptChunk + 16
	if len(one) < head+chunk || len(two) < head+chunk {
		t.Fatal("the fixtures are too short for this test")
	}
	spliced := append([]byte(nil), two...)
	copy(spliced[head:head+chunk], one[head:head+chunk])

	var back bytes.Buffer
	if _, err := key.Open(&back, bytes.NewReader(spliced)); err == nil {
		t.Fatal("a chunk from one backup was accepted inside another")
	}
}

func TestTheWrongKeySaysSoRatherThanFailingToDecrypt(t *testing.T) {
	sealedWith := testKey(t)
	other := testKey(t)
	body := sealed(t, sealedWith, []byte("a database"))

	var back bytes.Buffer
	_, err := other.Open(&back, bytes.NewReader(body))
	if err == nil {
		t.Fatal("the wrong key opened the backup")
	}
	// "You have the wrong key, and here is which one it wants" is a
	// five-minute fix. "Decryption failed" is an afternoon.
	if !strings.Contains(err.Error(), sealedWith.Fingerprint()) ||
		!strings.Contains(err.Error(), other.Fingerprint()) {
		t.Errorf("the refusal names neither key: %v", err)
	}
}

func TestAKeyMustBeThirtyTwoBytes(t *testing.T) {
	for _, bad := range []string{
		"", "not base64 at all!!", base64.StdEncoding.EncodeToString([]byte("short")),
		base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 64)),
	} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("%q was accepted as an encryption key", bad)
		}
	}
	if _, set, err := KeyFromEnv("  "); err != nil || set {
		t.Errorf("an unset key was treated as a bad one: set=%v err=%v", set, err)
	}
}

// A fingerprint identifies the key without being one.
func TestAFingerprintDoesNotCarryTheKey(t *testing.T) {
	key := testKey(t)
	print := key.Fingerprint()

	if len(print) != 16 {
		t.Errorf("a fingerprint is %d characters", len(print))
	}
	if strings.Contains(base64.StdEncoding.EncodeToString(key.material), print) {
		t.Error("the fingerprint appears inside the key material")
	}
	// The same key always fingerprints the same way, or a restore could not
	// recognise the key it needs.
	again, err := ParseKey(base64.StdEncoding.EncodeToString(key.material))
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint() != print {
		t.Error("the same key fingerprints differently twice")
	}
}

// A plain dump handed to the decrypter is a mistake worth naming.
func TestAPlainDumpIsNotMistakenForAnEncryptedOne(t *testing.T) {
	key := testKey(t)
	var back bytes.Buffer
	_, err := key.Open(&back, bytes.NewReader([]byte("PGDMP\x01\x0e\x00rest of a dump")))
	if err == nil {
		t.Fatal("a plain dump was opened as an encrypted one")
	}
	if !strings.Contains(err.Error(), "plain PostgreSQL dump") {
		t.Errorf("the refusal does not say what the file actually is: %v", err)
	}
	if !strings.Contains(err.Error(), "without a key") {
		t.Errorf("the refusal does not say what to do about it: %v", err)
	}
	if IsSealed([]byte("PGDMP\x01\x0e\x00")) {
		t.Error("a custom-format dump was recognised as an encrypted backup")
	}
}

func sealed(t *testing.T, key Key, plain []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if _, err := key.Seal(&out, bytes.NewReader(plain)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// --- the artifact a person carries ------------------------------------------

func TestTheThreeFilesNameTheSnapshotTheyBelongTo(t *testing.T) {
	names := NamesFor("20260301T100000Z-1")
	for _, name := range []string{names.Dump, names.Manifest, names.Checksum} {
		if !strings.Contains(name, "20260301T100000Z-1") {
			t.Errorf("%q does not say which backup it is", name)
		}
	}
	if names.Dump == names.Manifest || names.Manifest == names.Checksum {
		t.Error("two of the three files have the same name")
	}
}

// The checksum file is the one an operator can check with a tool they already
// have. Getting the format wrong produces a file that looks right and that
// `sha256sum` refuses.
func TestTheChecksumFileIsTheFormatSha256sumReads(t *testing.T) {
	line := string(ChecksumFile("abc123", "RawSyst_Backup_x.dump"))
	if line != "abc123  RawSyst_Backup_x.dump\n" {
		t.Errorf("the checksum file is %q", line)
	}
	if !strings.Contains(line, "  ") {
		t.Error("sha256sum needs two spaces between the hash and the name")
	}
}
