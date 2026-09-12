// Records legal values from a file, so a deployment needs no browser.
//
// # What this is for
//
// A production process serving Saudi Arabia refuses to start while a
// release-blocking legal value is still a placeholder. That refusal is right.
// What was wrong is that the only way past it was a person signing in to Super
// Admin and filling a form — on a service that was refusing to start.
//
// So the figures somebody reads out of the official documents go into a FILE,
// once, and every environment after that consumes the same file:
//
//	go run ./cmd/regulatory -template -country sa > sa-labour-law.json
//	# somebody reads the articles the file cites and fills in the figures
//	go run ./cmd/regulatory -check -file sa-labour-law.json
//	go run ./cmd/regulatory -apply -file sa-labour-law.json
//
// `-check` writes nothing and exits non-zero while a release blocker is
// outstanding, which makes it a deployment gate as well as a proof-read. With
// no flags at all it reports what this installation is still waiting for.
//
// # What it will not do
//
// It carries no figures of its own and it cannot invent one. Every value is
// checked against the embedded source pack — a choice must be one of the
// choices, a fraction must be a fraction — and `read_by` must resolve to a
// platform operator who exists here, because verification is a named person's
// act and a name in a file is not one.
//
// See internal/registry/attest.go for the rest of the reasoning.
package regulatory

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/config"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/registry"
)

func Main() {
	var (
		template = flag.Bool("template", false,
			"write a file to fill in, for what this installation is waiting for")
		check = flag.Bool("check", false,
			"validate a file and report what it would record, writing nothing")
		apply = flag.Bool("apply", false, "record the figures in a file")
		file  = flag.String("file", os.Getenv("RAWSYST_REGULATORY_SOURCE"),
			"the regulatory source file; defaults to $RAWSYST_REGULATORY_SOURCE")
		country = flag.String("country", "",
			"limit to one market, as a two-letter code")
	)
	flag.Parse()

	code, err := run(*template, *check, *apply, *file, *country)
	if err != nil {
		fmt.Fprintf(os.Stderr, "regulatory: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(template, check, apply bool, file, country string) (int, error) {
	chosen := 0
	for _, b := range []bool{template, check, apply} {
		if b {
			chosen++
		}
	}
	if chosen > 1 {
		return 0, errors.New(
			"choose one of -template, -check or -apply")
	}

	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	// Never strict here. `requireVerified` decides whether RESOLVING an
	// unverified rule refuses, and this command exists to stop them being
	// unverified; refusing to read the registry in production would make it
	// useless in the one place it matters most.
	rules := registry.New(pool, false)

	switch {
	case template:
		out, e := rules.Template(ctx, country)
		if e != nil {
			return 0, e
		}
		fmt.Println(string(out))
		return 0, nil
	case check, apply:
		return applyFile(ctx, rules, file, apply)
	default:
		return report(ctx, rules, country)
	}
}

// report says what is outstanding, and exits non-zero if a release blocker is
// among it.
//
// The exit code is the point: this is what a deployment pipeline runs to find
// out whether the API it is about to start will refuse to start.
func report(ctx context.Context, rules *registry.Service, country string) (int, error) {
	open, err := rules.Outstanding(ctx, country)
	if err != nil {
		return 0, err
	}
	if len(open) == 0 {
		fmt.Printf("\n  Every legal value in the registry has been recorded.\n\n")
		return 0, nil
	}

	// Two counts, not one.
	//
	// `!` is a rule no shop in that market can trade without, and a deployment
	// serving that market genuinely refuses to start. `~` is a capability that
	// cannot be COMPUTED until somebody records the figure: the software is
	// built, tested and reachable, it refuses by name where it is used, and
	// nothing else is affected.
	//
	// This used to print one number and exit non-zero on both. A pipeline
	// therefore failed a release over an end-of-service band nobody had read
	// yet, and the message it printed — "a deployment will refuse to start" —
	// was false for that rule. Two conditions, two marks, one exit code that
	// means what it says.
	stops, awaiting := 0, 0
	fmt.Printf("\n  Legal values nobody has recorded yet:\n\n")
	for _, u := range open {
		mark := " "
		switch {
		case u.Blocker && u.Blocks == registry.BlocksOnboarding:
			mark = "!"
			stops++
		case u.Blocker:
			mark = "~"
			awaiting++
		}
		fmt.Printf("  %s %-34s %-3s %s\n", mark, u.Key, u.Country,
			strings.Join(u.Fields, ", "))
		if !u.Described {
			fmt.Printf("      (not in the source pack: record it in Super Admin)\n")
		}
	}

	fmt.Printf("\n  %d outstanding.\n", len(open))
	if stops > 0 {
		fmt.Printf("  ! %d stop a market trading at all.\n", stops)
	}
	if awaiting > 0 {
		fmt.Printf("  ~ %d hold back one capability each. The software for "+
			"every one of them is complete;\n"+
			"    each refuses by name where it is used, and a deployment "+
			"still starts.\n", awaiting)
	}

	// Named without a runner in front of it. This is the same binary in a
	// container, where `go` does not exist, and on a development machine, where
	// it is `go run ./cmd/regulatory` -- printing one of those two is printing
	// the wrong one half the time.
	fmt.Printf("\n  Produce a file to fill in, then record it:\n\n")
	fmt.Printf("      regulatory -template -country %s > sources.json\n",
		firstBlockerCountry(open))
	fmt.Printf("      regulatory -check  -file sources.json\n")
	fmt.Printf("      regulatory -apply  -file sources.json\n\n")

	if stops > 0 {
		fmt.Printf("  A deployment serving those markets will refuse to start.\n\n")
		return 1, nil
	}
	return 0, nil
}

func firstBlockerCountry(open []registry.Unrecorded) string {
	for _, u := range open {
		if u.Blocker {
			return u.Country
		}
	}
	// Nothing is blocking, so the command is for whatever IS outstanding —
	// a country with no blocker at all still has values to record, and
	// printing "sa" at somebody looking at a Bangladeshi rule is printing a
	// command that writes an empty file.
	if len(open) > 0 {
		return open[0].Country
	}
	return "sa"
}

func applyFile(
	ctx context.Context, rules *registry.Service, file string, write bool,
) (int, error) {
	if strings.TrimSpace(file) == "" {
		return 0, errors.New(
			"say which file: -file sources.json, or set " +
				"$RAWSYST_REGULATORY_SOURCE")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	att, err := registry.ParseAttestation(raw)
	if err != nil {
		return 0, err
	}

	outcomes, err := rules.ApplyAttestation(ctx, att, !write)
	if err != nil {
		return 0, err
	}

	fmt.Printf("\n  %s, read by %s on %s:\n\n", file, att.ReadBy, att.ReadOn)
	for _, o := range outcomes {
		fmt.Printf("    %-34s %-16s %s  %s\n",
			o.Key, o.Action, o.From, o.Detail)
	}

	if !write {
		fmt.Printf("\n  Nothing was written. Run again with -apply.\n\n")
		return 0, nil
	}
	fmt.Printf("\n  Recorded. Restart any API or worker already running:\n")
	fmt.Printf("  they cache resolved rules in process.\n\n")
	return 0, nil
}
