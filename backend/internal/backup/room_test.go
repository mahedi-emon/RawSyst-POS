// Whether there is room, and what happens when there is not.
//
// The arithmetic is tested directly because the failure it prevents is not a
// failed backup — it is a dump filling the volume Postgres writes into, which
// stops the database accepting a sale. A guard that is off by a factor of a
// hundred in the generous direction is worse than no guard, because it reads
// as protection.
package backup

import (
	"os"
	"strings"
	"testing"
)

func TestRequiredFreeIsPessimistic(t *testing.T) {
	const gib = uint64(1) << 30

	// At the default, a dump is asked to have as much room as the database
	// measures, even though a compressed dump with no indexes is reliably
	// smaller. Being wrong in this direction costs a message.
	if got := requiredFree(4*gib, 100); got != 4*gib {
		t.Errorf("100%% of 4GiB: got %d, want %d", got, 4*gib)
	}
	if got := requiredFree(10*gib, 50); got != 5*gib {
		t.Errorf("50%% of 10GiB: got %d, want %d", got, 5*gib)
	}
	if got := requiredFree(10*gib, 150); got != 15*gib {
		t.Errorf("150%% of 10GiB: got %d, want %d", got, 15*gib)
	}

	// A nonsense percentage falls back to the default rather than to zero. A
	// misconfiguration must not silently disable the check.
	if got := requiredFree(4*gib, 0); got != 4*gib {
		t.Errorf("0%% must mean the default: got %d", got)
	}
	if got := requiredFree(4*gib, -20); got != 4*gib {
		t.Errorf("a negative percentage must mean the default: got %d", got)
	}
}

func TestThereIsAlwaysAFloor(t *testing.T) {
	// A nearly-empty database still needs somewhere to write, and a volume
	// with a few megabytes left is about to cause a different problem.
	if got := requiredFree(1<<20, 100); got != MinFreeBytes {
		t.Errorf("a 1MiB database: got %d, want the %d floor", got, MinFreeBytes)
	}
	if got := requiredFree(0, 100); got != MinFreeBytes {
		t.Errorf("an empty database: got %d, want the %d floor", got, MinFreeBytes)
	}
}

func TestFreeSpaceCanBeMeasuredHere(t *testing.T) {
	// The platform files behind this are the reason the check is not dead code
	// on the machine somebody is developing on.
	dir := t.TempDir()
	free, err := freeBytes(dir)
	if err != nil {
		t.Fatalf("free space on %s could not be measured: %v", dir, err)
	}
	if free == 0 {
		t.Errorf("free space on %s reported as zero, which cannot be right", dir)
	}
}

func TestASizeIsReadableInAMessage(t *testing.T) {
	cases := map[uint64]string{
		512:             "512 B",
		1024:            "1.0 KiB",
		1536:            "1.5 KiB",
		1 << 20:         "1.0 MiB",
		uint64(1) << 30: "1.0 GiB",
		uint64(3) << 40: "3.0 TiB",
	}
	for in, want := range cases {
		if got := human(in); got != want {
			t.Errorf("human(%d): got %q, want %q", in, got, want)
		}
	}
}

// The refusal itself, driven by asking for more room than any disk has rather
// than by filling one.
func TestTheRefusalSaysWhatToDo(t *testing.T) {
	dir := t.TempDir()
	free, err := freeBytes(dir)
	if err != nil {
		t.Skipf("free space cannot be measured here: %v", err)
	}

	// A database claimed to be larger than the volume, at the default margin.
	rep := &roomReport{
		DatabaseBytes: free + (1 << 30),
		FreeBytes:     free,
		Dir:           dir,
	}
	rep.NeedBytes = requiredFree(rep.DatabaseBytes, 100)
	if rep.FreeBytes >= rep.NeedBytes {
		t.Fatalf("the fixture is wrong: %d free is not less than %d needed",
			rep.FreeBytes, rep.NeedBytes)
	}

	// And the message a person would act on. Checked by content rather than
	// by exact wording: the point is that it names the directory, both
	// numbers, and the setting that changes the answer.
	msg := refusalMessage(rep)
	for _, want := range []string{
		dir, "RAWSYST_BACKUP_TEMP_DIR", "RAWSYST_BACKUP_MIN_FREE_PERCENT",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "password") || strings.Contains(msg, "@") {
		t.Errorf("the refusal leaks something it should not: %s", msg)
	}
}

func TestStagingDirectoryDefaultsToSomewhereReal(t *testing.T) {
	// An empty TempDir means the process's working directory, which exists.
	// The check must not blow up on it.
	if _, err := freeBytes("."); err != nil {
		t.Errorf("the default staging directory cannot be measured: %v", err)
	}
	if _, err := os.Stat("."); err != nil {
		t.Errorf("the default staging directory does not exist: %v", err)
	}
}
