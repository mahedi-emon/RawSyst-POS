// Retrieve the document a legal value is published in, read it, and record it.
//
// The same three service calls the Super Admin screen makes, on a command line,
// so that bringing a development machine to a known state is one command rather
// than a sequence of clicks:
//
//	go run ./cmd/ingest -rule SA.EOSB.ENTITLEMENT -from 2005-09-27 -apply
//
// # Why this exists when the screen already does it
//
// Determinism. A developer, a CI job and a fresh checkout all need the same
// registry contents without a browser, and "click through Super Admin" is not a
// reproducible step. It is not a second implementation: every flag below calls
// the same `registry` method the HTTP handler calls, so the two cannot drift.
//
// # What it will not do
//
// It will not apply without `-apply`, and it will not mark a figure verified
// without `-verified` naming the person asserting it. Recording a legal value
// is somebody's assertion, and a command line does not change that — it only
// changes where the assertion is typed. `-verified` takes the sign-in address
// of the platform operator making it, resolved against this installation, so
// the trail names somebody who exists here.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

func main() {
	var (
		rule = flag.String("rule", "",
			"the rule key to retrieve, for example SA.EOSB.ENTITLEMENT")
		file = flag.String("file", "",
			"read the document from this path instead of retrieving it")
		from = flag.String("from", "",
			"the date the figures take effect, YYYY-MM-DD. For a statute "+
				"this is when the article came into force")
		apply = flag.Bool("apply", false,
			"record the figures. Without it the reading is printed and "+
				"nothing is written")
		verified = flag.String("verified", "",
			"the sign-in address of the platform operator asserting they "+
				"have checked the reading against the document")
		refresh = flag.Bool("refresh", false,
			"re-check every applied source and file a candidate where one "+
				"has changed; applies nothing")
	)
	flag.Parse()

	if err := run(*rule, *file, *from, *apply, *verified, *refresh); err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
}

func run(rule, file, from string, apply bool, verified string, refresh bool) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Verification not required of this process itself: it records legal
	// values, it does not compute with them.
	rules := registry.New(pool, false)

	if refresh {
		return runRefresh(ctx, rules)
	}

	rule = strings.ToUpper(strings.TrimSpace(rule))
	if rule == "" {
		return fmt.Errorf("name the rule with -rule, for example " +
			"-rule SA.EOSB.ENTITLEMENT")
	}

	actor, err := resolveOperator(ctx, pool, verified)
	if err != nil {
		return err
	}

	doc, err := retrieve(ctx, rules, rule, file, actor)
	if err != nil {
		return err
	}
	report(doc)

	if !apply {
		fmt.Printf("\nNothing was written. Add -apply -from YYYY-MM-DD to " +
			"record these figures.\n")
		return nil
	}
	if doc.ExtractionError != "" {
		return fmt.Errorf("nothing was read out of that document, so there is "+
			"nothing to apply: %s", doc.ExtractionError)
	}
	if !doc.Valid {
		return fmt.Errorf("what was read does not pass validation: %s",
			doc.Validation)
	}
	if from == "" {
		return fmt.Errorf("say when these figures take effect with -from " +
			"YYYY-MM-DD. For a statute this is when the article came into " +
			"force, not when it was read")
	}
	effective, err := time.Parse("2006-01-02", from)
	if err != nil {
		return fmt.Errorf("-from must be YYYY-MM-DD: %w", err)
	}

	recorded, err := rules.ApplySource(ctx, doc.ID, effective,
		actor != uuid.Nil, "", actor)
	if err != nil {
		return err
	}

	fmt.Printf("\nrecorded %s for %s from %s\n",
		recorded.Key, strings.ToUpper(recorded.Country), recorded.From)
	if recorded.Verified {
		fmt.Printf("  verified on %s by %s\n", recorded.VerifiedOn, verified)
	} else {
		fmt.Printf("  recorded UNVERIFIED. Every use of it is refused where "+
			"verification is required. Add -verified <email> once somebody "+
			"has checked the reading above against %s.\n", doc.Title)
	}
	return nil
}

// retrieve gets the document, from the authority or from a path.
func retrieve(
	ctx context.Context, rules *registry.Service, rule, file string,
	actor uuid.UUID,
) (registry.SourceDocument, error) {
	if file == "" {
		fmt.Printf("retrieving the document %s is published in…\n", rule)
		return rules.FetchSource(ctx, rule, "", actor)
	}

	content, err := os.ReadFile(file)
	if err != nil {
		return registry.SourceDocument{}, err
	}
	src, described := registry.SourceFor(rule)
	if !described {
		return registry.SourceDocument{}, fmt.Errorf(
			"the source pack does not describe %s, so this command does not "+
				"know which market or authority the document belongs to", rule)
	}
	fmt.Printf("reading %s as the document for %s…\n", file, rule)
	return rules.RecordSource(ctx, registry.NewSourceDocument{
		RuleKey: rule, Country: src.Country, Title: src.Document,
		Origin: "upload", Content: content,
		Notes: "Supplied from " + file + " on this machine.",
	}, actor)
}

// report prints the provenance and the reading.
func report(doc registry.SourceDocument) {
	fmt.Printf("\n  document   %s\n", doc.Title)
	if doc.URL != "" {
		fmt.Printf("  url        %s\n", doc.URL)
	}
	fmt.Printf("  retrieved  %s (%s)\n", doc.RetrievedOn, doc.Origin)
	fmt.Printf("  sha-256    %s\n", doc.SHA256)
	fmt.Printf("  size       %d bytes, %s\n", doc.ByteSize, doc.MediaType)

	if doc.ExtractionError != "" {
		fmt.Printf("\n  NOTHING WAS READ: %s\n", doc.ExtractionError)
		return
	}

	fmt.Printf("\n  read out of it:\n")
	for _, f := range doc.Extracted {
		fmt.Printf("    %-40s %s   (Article %s)\n", f.Field, f.Value, f.Article)
		fmt.Printf("      %q\n", trim(f.Evidence, 160))
		if f.Derived != "" {
			fmt.Printf("      %s\n", trim(f.Derived, 200))
		}
	}
	if doc.Valid {
		fmt.Printf("\n  validation PASSED\n")
	} else {
		fmt.Printf("\n  validation FAILED: %s\n", doc.Validation)
	}
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func runRefresh(ctx context.Context, rules *registry.Service) error {
	outcomes, err := rules.RefreshSources(ctx, uuid.Nil)
	if err != nil {
		return err
	}
	if len(outcomes) == 0 {
		fmt.Println("no applied source has an address to re-check")
		return nil
	}
	for _, o := range outcomes {
		switch {
		case o.Err != "":
			fmt.Printf("%-24s could not be re-checked: %s\n", o.RuleKey, o.Err)
		case o.Changed:
			fmt.Printf("%-24s CHANGED\n  was %s\n  now %s\n"+
				"  filed as candidate %s and NOT applied\n",
				o.RuleKey, o.PreviousSHA256, o.SHA256, o.DocumentID)
		default:
			fmt.Printf("%-24s unchanged (%s)\n", o.RuleKey, o.SHA256)
		}
	}
	return nil
}

// resolveOperator turns a sign-in address into the id the trail records.
//
// Resolved against this installation before anything is written, so a recorded
// assertion names somebody who exists here rather than an address somebody
// typed. Empty means nobody is asserting anything, and the figures are recorded
// unverified.
func resolveOperator(
	ctx context.Context, pool *db.Pool, email string,
) (uuid.UUID, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return uuid.Nil, nil
	}
	var id uuid.UUID
	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM app_user WHERE lower(email) = $1`, email).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf(
			"no account on this installation signs in as %q, so it cannot be "+
				"recorded as having checked anything", email)
	}
	return id, nil
}
