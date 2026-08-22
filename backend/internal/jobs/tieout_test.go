package jobs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The alert is read by a shop owner at eight in the morning, not parsed.
//
// Worth its own test because the difference between "Customer balances out by
// 5.00, Stock valuation out by 0.01" with a trailing comma and a sentence a
// person can read is the difference between an alert that gets acted on and one
// that gets filed.
func TestADivergenceReadsLikeASentence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"nothing", nil, ""},
		{"one", []string{"A"}, "A"},
		{"two", []string{"A", "B"}, "A and B"},
		{"three", []string{"A", "B", "C"}, "A, B and C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinAnd(tc.parts); got != tc.want {
				t.Errorf("joinAnd(%v) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

// The three checks of QA gate M1, each with something an owner can read.
//
// The functions themselves are now compile-checked — they are the modules' own
// GLDifference, not SQL strings — so what is left to get wrong is the labelling
// and the count.
func TestTheTieOutChecksAreTheThreeOfGateM1(t *testing.T) {
	if len(tieOutChecks) != 3 {
		t.Fatalf("%d tie-out checks, want the 3 of QA gate M1", len(tieOutChecks))
	}
	seen := map[string]bool{}
	for _, c := range tieOutChecks {
		if c.label == "" {
			t.Error("a tie-out check has no label; its alert would name nothing")
		}
		if c.ask == nil {
			t.Errorf("the %q check asks nothing", c.label)
		}
		if seen[c.label] {
			t.Errorf("two checks are both labelled %q, so an alert naming it "+
				"would not say which moved", c.label)
		}
		seen[c.label] = true
	}
}

// Every job kind the package defines must be registered by the worker.
//
// This exists because of what an audit found: `accounting.tie_out` was named in
// design 08, its invariants were proved by the suite, and no worker ran it — so
// C13's "any divergence is flagged as an exception" was true at build time and
// false in production. The same shape as the shift service that was complete
// and mounted nowhere, and the scope checks that compiled and gated nothing.
//
// A job kind that exists and is not registered is not a half-built feature. It
// is a promise the system does not keep, and nothing else in the suite notices:
// every test of a sweeper calls it directly.
func TestEveryJobKindIsRegisteredByTheWorker(t *testing.T) {
	kinds := map[string]string{} // constant name -> kind string

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package: %v", err)
	}
	decl := regexp.MustCompile(`const (Kind\w+) = "([^"]+)"`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			kinds[m[1]] = m[2]
		}
	}
	if len(kinds) == 0 {
		t.Fatal("no job kinds found; this guard is not looking where it thinks")
	}

	main, err := os.ReadFile(filepath.Join("..", "..", "cmd", "worker", "main.go"))
	if err != nil {
		t.Fatalf("read the worker: %v", err)
	}

	for name, kind := range kinds {
		if !strings.Contains(string(main), "jobs."+name) {
			t.Errorf("jobs.%s (%q) is defined and the worker never registers it.\n"+
				"Nothing will ever run it, and every test of its handler passes "+
				"because they call the handler directly. Register it in "+
				"cmd/worker, or delete the kind.", name, kind)
		}
	}
}
