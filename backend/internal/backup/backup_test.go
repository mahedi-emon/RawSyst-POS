// What a backup is allowed to claim about itself.
//
// These run without a database, an object store or `pg_dump`. What they hold to
// is the reasoning: a snapshot with no completion marker is not a backup, a
// retention policy may never empty the store, and a manifest from a build that
// does not exist yet is not something to guess at.
//
// The parts that need a real Postgres and a real bucket — dump, upload,
// checksum, restore into a scratch database, row counts — are proved by running
// them, and `deploy/server/BACKUP.md` says how. A unit test that mocked
// `pg_dump` would assert that the mock was called.
package backup

import (
	"encoding/json"
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

// Retention never empties the store.
func TestRetentionAlwaysKeepsTheNewest(t *testing.T) {
	// Thirty daily snapshots against a policy that asks for one of each. The
	// newest must survive whatever the arithmetic says, because a policy that
	// can empty the store is a policy that will, on the day somebody needs it.
	var snapshots []Snapshot
	base := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	for i := range 30 {
		at := base.AddDate(0, 0, i)
		snapshots = append(snapshots, Snapshot{
			ID: NewSnapshotID(at), TakenAt: at, Completed: true,
		})
	}
	// Newest first, as `List` returns them.
	for i, j := 0, len(snapshots)-1; i < j; i, j = i+1, j-1 {
		snapshots[i], snapshots[j] = snapshots[j], snapshots[i]
	}

	keep := whatToKeep(snapshots, Policy{Daily: 1, Weekly: 1, Monthly: 1})
	if !keep[snapshots[0].ID] {
		t.Error("the newest snapshot was not kept")
	}
	if len(keep) == 0 {
		t.Fatal("a retention pass kept nothing at all")
	}
	if len(keep) == len(snapshots) {
		t.Error("a one-of-each policy kept all thirty, so nothing is pruned")
	}
}

// A policy keeps one snapshot per period, not one snapshot in total.
func TestRetentionKeepsOnePerPeriod(t *testing.T) {
	var snapshots []Snapshot
	base := time.Date(2026, 6, 30, 3, 0, 0, 0, time.UTC)
	for i := range 40 {
		at := base.AddDate(0, 0, -i)
		snapshots = append(snapshots, Snapshot{
			ID: NewSnapshotID(at), TakenAt: at, Completed: true,
		})
	}

	keep := whatToKeep(snapshots, Policy{Daily: 7, Weekly: 4, Monthly: 3})

	// Seven distinct days, at least. The weekly and monthly buckets overlap
	// them, so the total is somewhere between seven and fourteen — asserting
	// an exact number would assert the arithmetic rather than the intent.
	if len(keep) < 7 {
		t.Errorf("kept %d snapshots against a seven-day policy", len(keep))
	}
	if len(keep) >= len(snapshots) {
		t.Errorf("kept %d of %d; nothing would ever be pruned",
			len(keep), len(snapshots))
	}
}

// A snapshot this build cannot date is kept, not deleted.
func TestRetentionKeepsWhatItCannotUnderstand(t *testing.T) {
	snapshots := []Snapshot{
		{ID: "20260601T030000Z-1",
			TakenAt: time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC), Completed: true},
		// No parseable time: written by something else, or by a later build.
		{ID: "something-else-entirely", Completed: true},
	}
	keep := whatToKeep(snapshots, Policy{Daily: 1})
	if !keep["something-else-entirely"] {
		t.Error("a snapshot whose id could not be read was marked for " +
			"deletion. Deleting something because it is not understood is " +
			"how a bug becomes data loss")
	}
}

// The manifest carries nothing secret.
//
// A guard rather than a hope: the manifest is uploaded to a bucket, read by
// whoever can list it, and printed by `backup run`. It says what was backed up
// and what it hashes to, and nothing that would let anybody in.
func TestTheManifestCarriesNoSecrets(t *testing.T) {
	m := Manifest{
		Version: ManifestVersion, SnapshotID: "20260601T030000Z-1",
		DatabaseName: "rawsyst", AppVersion: "1.0.0",
		Database: Component{Key: databaseObject, SHA256: "abc"},
	}
	rendered := strings.ToLower(renderForInspection(t, m))

	for _, forbidden := range []string{
		"password", "secret", "jwt", "api_key", "apikey", "private",
		"token", "credential", "totp", "passphrase",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the manifest has a %q field. Backups are read by "+
				"whoever can list the bucket; secrets travel separately and "+
				"deploy/server/BACKUP.md says how", forbidden)
		}
	}
}

// renderForInspection is the manifest as it is actually stored: JSON, with the
// field names a reader of the bucket would see.
func renderForInspection(t *testing.T, m Manifest) string {
	t.Helper()
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("render the manifest: %v", err)
	}
	return string(body)
}
