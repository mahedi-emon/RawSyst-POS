package registry

// What a legal value that nobody has recorded yet actually needs.
//
// # The problem this solves
//
// A rule seeded with `__VERIFY__` refuses at the point of use and blocks a
// production deployment serving that market. Correct — but until now the only
// way past it was an operator typing a JSON object into a textarea, having
// worked out from the placeholder keys alone what six fields called things like
// `resignation_fraction_two_to_five_years` were supposed to contain, in what
// unit, from which article of which law.
//
// That is a software gap wearing a legal hat. The citation, the field names,
// the units and the meaning of each field are all things this product knows and
// can state. Only the FIGURES come from outside.
//
// # Why the pack carries no figures
//
// It would be easy to ship the numbers and be done. It would also be wrong.
//
// There is no machine-readable authoritative source for these values: the Saudi
// Labour Law is published as prose, and no ministry serves it as data this
// software could fetch and check. So a number in this file would be a number
// somebody typed from memory, presented to every deployment as though the
// product had established it. The registry's whole discipline is that a reader
// can tell a confirmed figure from a guess, and `verified_by` is a person's
// name because reading the law is a person's act.
//
// So the pack states everything software can legitimately establish — where to
// look, what to look for, and what shape the answer takes — and leaves the
// figures to whoever reads the article. That turns "compose a JSON payload"
// into "type six labelled numbers", which is the whole of the software's job
// here.
//
// # Development is not blocked by this
//
// A developer does not need the legal values to work on payroll. `make
// dev-regulatory` writes per-tenant overrides carrying obviously-not-statutory
// figures, so the calculation runs locally. Overrides are tenant-scoped and
// carry `verified_on = NULL`, so a deployment that requires verification
// refuses them exactly as it refuses a placeholder.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed sources/sources.json
var sourcePackJSON []byte

// SourceField is one figure an operator has to supply, and what it means.
type SourceField struct {
	Name  string `json:"name"`
	Label string `json:"label"`

	// Kind is how the screen should ask: decimal, int, text or choice.
	Kind    string   `json:"kind"`
	Choices []string `json:"choices,omitempty"`
	Unit    string   `json:"unit,omitempty"`

	// Article is the specific article this field comes from, where the law has
	// them. Empty for a regulation that is not organised that way.
	Article string `json:"article,omitempty"`
	Help    string `json:"help"`
}

// SourceRule is where one legal value comes from, and what it is made of.
type SourceRule struct {
	RuleKey   string        `json:"rule_key"`
	Country   string        `json:"country"`
	Title     string        `json:"title"`
	Authority string        `json:"authority"`
	Document  string        `json:"document"`
	URL       string        `json:"url"`
	Articles  []string      `json:"articles,omitempty"`
	Reading   string        `json:"reading"`
	Fields    []SourceField `json:"fields"`
}

// SourcePack is the whole thing, versioned so a screen can say which it read.
type SourcePack struct {
	Version string       `json:"version"`
	Rules   []SourceRule `json:"rules"`
}

// Sources returns the pack, parsed once per call.
//
// Not cached: it is read when an operator opens the registry screen, which is
// rare, and a cached copy is one more thing that can be stale in a process
// that has been up for a month.
func Sources() (SourcePack, error) {
	var pack SourcePack
	if err := json.Unmarshal(sourcePackJSON, &pack); err != nil {
		return SourcePack{}, fmt.Errorf("the regulatory source pack is unreadable: %w", err)
	}
	return pack, nil
}

// SourceFor returns the entry for one rule key, and whether there is one.
//
// A rule with no entry is not an error: the pack describes the values a person
// has to go and read, and most rules in the registry were recorded long ago.
func SourceFor(key string) (SourceRule, bool) {
	pack, err := Sources()
	if err != nil {
		return SourceRule{}, false
	}
	for _, r := range pack.Rules {
		if strings.EqualFold(r.RuleKey, key) {
			return r, true
		}
	}
	return SourceRule{}, false
}
