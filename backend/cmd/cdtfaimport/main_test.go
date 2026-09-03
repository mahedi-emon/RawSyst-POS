// The parts of the conversion that are easy to get quietly wrong.
//
// The arithmetic — each location's share plus the statewide rate equalling what
// CDTFA published — is asserted against the loaded data in
// internal/api/cdtfa_test.go. What is checked here is the reading: two bugs
// that produced plausible output rather than an error.
package main

import "testing"

// A cell is placed by its reference, not by its position in the row.
//
// A spreadsheet omits empty cells rather than writing them. CDTFA publishes
// five counties with an empty Rate, and reading those rows in document order
// put the county NAME in the rate column and the Notes in the type column —
// which parses as far as "this rate is not a number" and looks like malformed
// data rather than a shifted row.
func TestAColumnIsFoundByItsReference(t *testing.T) {
	for _, c := range []struct {
		ref  string
		want int
	}{
		{"A1", 0},
		{"B1", 1},
		{"C119", 2},
		{"E548", 4},
		{"Z9", 25},
		{"AA1", 26},
		{"AB2", 27},
		{"", 0},
	} {
		if got := columnOf(c.ref); got != c.want {
			t.Errorf("columnOf(%q) = %d, want %d", c.ref, got, c.want)
		}
	}
}

// A location's code survives next quarter's file unchanged.
//
// CDTFA publishes names, not codes. If the code moved, every location would be
// created afresh each quarter and the previous rates would never be superseded
// — the shop would end up with two open rates for the same authority.
func TestALocationCodeIsStableAndASCII(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Adelanto", "CA-ADELANTO"},
		{"Agoura Hills", "CA-AGOURA-HILLS"},
		{"Alameda County", "CA-ALAMEDA-COUNTY"},
		// The accent is folded in the CODE only; the name keeps it, because
		// that is how the authority spells it.
		{"La Cañada Flintridge", "CA-LA-CANADA-FLINTRIDGE"},
		{"Kern County Unincorporated Area", "CA-KERN-COUNTY-UNINCORPORATED-AREA"},
	} {
		if got := codeFor(c.name); got != c.want {
			t.Errorf("codeFor(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A city and the county of the same name are different authorities.
//
// CDTFA publishes Alameda at 10.75% and Alameda County at 10.25%. They levy
// differently, and collapsing them would charge one of the two wrongly.
func TestACityAndItsCountyDoNotCollide(t *testing.T) {
	if codeFor("Alameda") == codeFor("Alameda County") {
		t.Error("Alameda the city and Alameda County share a code")
	}
	if levelFor("City") != "city" || levelFor("County") != "county" {
		t.Errorf("levels are %q and %q, want city and county",
			levelFor("City"), levelFor("County"))
	}
	// CDTFA's Type column is the authority's own text, so it is matched
	// without regard to case or stray spacing.
	if got := levelFor(" county "); got != "county" {
		t.Errorf("levelFor(%q) = %q, want county", " county ", got)
	}
}
