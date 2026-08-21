package accounting

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// No test may pick its own entry number.
//
// P17. `accounting/posting_test.go`, `inventory/tieout_test.go` and
// `inventory/shortfall_test.go` each wrote `journal_entry` directly with a
// counter of their own — one starting at 1 and incrementing, others with
// literals like 500, 600 and 900. Nothing collided, but only because each ran
// against a company of its own: `journal_entry` is UNIQUE on
// (company_id, entry_no), so the first test to post into a company another
// harness had already touched would have failed on a unique-violation a long
// way from its cause.
//
// The fix is not to stop writing raw SQL. Several of those tests exist
// precisely to prove a DATABASE guarantee — an unbalanced entry refused by the
// deferred trigger, a closed period refused by another — and posting them
// through `accounting.Post` would have the Go layer refuse them first, leaving
// the test green and the trigger unexercised. This file's own header says why:
// a guarantee that only holds when the application cooperates is not a
// guarantee.
//
// So raw SQL stays and the NUMBER comes from `claim_entry_no()`, the same
// counter on the company row that production uses. Design 02 is explicit that
// entry numbers come from that counter rather than from `max()+1` or a
// sequence: `max()+1` collides under load, and a sequence is not transactional
// and leaves permanent gaps in numbered accounting records. A test that picks
// its own number is a third way, and the one that can collide.
func TestNoTestPicksItsOwnEntryNumber(t *testing.T) {
	root := repoInternalDir(t)

	// The column list, then the VALUES it is given. Matched together because
	// `entry_no` also appears in SELECTs and assertions, which are fine.
	insert := regexp.MustCompile(`(?s)INSERT INTO journal_entry.*?VALUES\s*\(([^)]*)\)`)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range insert.FindAllStringSubmatch(string(body), -1) {
			if !strings.Contains(m[1], "claim_entry_no(") {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("these tests write journal_entry without claim_entry_no(): %v\n"+
			"An entry number picked by hand collides with any other harness "+
			"sharing the company, on a unique index, far from the cause. Take "+
			"the number from the counter production takes it from — writing the "+
			"row in raw SQL is fine and often the point.", offenders)
	}
}

// repoInternalDir finds `backend/internal` from whichever package this runs in.
func repoInternalDir(t *testing.T) string {
	t.Helper()
	// Tests run with the package directory as their working directory, and
	// this package is internal/accounting.
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve the internal directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounting")); err != nil {
		t.Fatalf("expected to find the internal tree at %s: %v", dir, err)
	}
	return dir
}
