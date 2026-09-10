//go:build integration

// Recording a figure retires the placeholder that stood where it goes.
//
// # What this is for
//
// A `__VERIFY__` row is not a figure this product believed. It is the record
// that it had been told nothing, wearing a date because the table requires one.
// Nothing was ever computed from it — every use is refused by name at the point
// of use — so there is no period whose answer has to be preserved, and 0134
// permits deleting exactly that and nothing else.
//
// The case that made it necessary: articles are usually older than the
// placeholder standing in for them. `SA.EOSB.ENTITLEMENT` was seeded from 2026
// and Articles 84 and 85 have been in force since 2005, so the real figures
// could be recorded neither from the date the law took effect — that collides
// with the placeholder's range — nor from the placeholder's own date, because
// superseding only closes a row starting strictly earlier. The correction
// workflow could not correct the one row it exists for.
//
// # Why it uses a key of its own
//
// `regulatory_rule` is append-only by trigger and platform-wide by design, so a
// test that recorded the Saudi entitlement would permanently change it in every
// database the suite touches — including the boot gate's own tests, which
// assert it is still awaiting a figure. Same reasoning as
// `attest_integration_test.go`, same country: `zz`, which nobody trades in.
package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A key per test, so one test's permanent rows cannot crowd another's window.
//
// Every figure these tests record is undeletable, by the same trigger that
// makes the registry trustworthy. They therefore accumulate, and the seed below
// deals with that by taking a window past everything already there rather than
// by trying to clean up after itself — which it could not do and should not be
// able to.
const (
	retireEarlierKey = "ZZ.TEST.RETIRE_EARLIER"
	retireLaterKey   = "ZZ.TEST.RETIRE_LATER"
	retireDeleteKey  = "ZZ.TEST.RETIRE_DELETE"
)

// seedRetirePlaceholder puts an unfilled rule in force, and says from when.
//
// The date is not chosen by the caller: it is ten years past the latest row
// this key already has. `regulatory_rule` refuses overlapping ranges and a
// figure can never be deleted, so a fixed date works exactly once — the second
// run collides with the first run's open-ended figure. Deriving the window from
// what is already there makes the test re-runnable for as long as anybody cares
// to run it, which is the same reason `attest_integration_test.go` gives for
// taking a window of its own.
func seedRetirePlaceholder(t *testing.T, s *Service, key string) time.Time {
	t.Helper()
	ctx := context.Background()

	var from time.Time
	if err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Anything an earlier run left open, whether it finished or was
		// killed. Closed rather than deleted, which the trigger refuses for a
		// figure and permits only for an untouched placeholder.
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_rule
			SET effective_to = effective_from + 1
			WHERE rule_key = $1 AND effective_to IS NULL`, key); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(max(effective_from), date '1900-01-01') + interval '10 years'
			FROM regulatory_rule WHERE rule_key = $1`, key).Scan(&from); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO regulatory_rule
			  (rule_key, country, payload, effective_from, source_authority,
			   source_document, release_blocker, blocks, notes)
			VALUES ($1, 'zz', $2::jsonb, $3::date, 'mhrsd',
			        'Test fixture: a rule nobody has read yet', true, 'feature',
			        'Fixture. Not a legal value and not a market anybody trades in.')`,
			key, `{"days":"`+Placeholder+`"}`, from)
		return e
	}); err != nil {
		t.Fatalf("seed the placeholder: %v", err)
	}
	s.Invalidate()
	return from
}

func rulesFor(t *testing.T, s *Service, key string) []RuleRow {
	t.Helper()
	all, err := s.Rules(context.Background(), "zz")
	if err != nil {
		t.Fatalf("read the registry: %v", err)
	}
	var out []RuleRow
	for _, r := range all {
		if r.Key == key {
			out = append(out, r)
		}
	}
	return out
}

// A figure recorded from BEFORE the placeholder retires it.
//
// This is the shape that could not be recorded at all before 0134, and it is
// the shape every statute has: the article is older than this product's note
// that nobody had read it.
func TestAFigureFromBeforeThePlaceholderRetiresIt(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	placeholderFrom := seedRetirePlaceholder(t, s, retireEarlierKey)
	if got := len(rulesFor(t, s, retireEarlierKey)); got == 0 {
		t.Fatal("the placeholder was not seeded")
	}

	// Five years before the note that nobody had read it, which is the shape
	// every statute has.
	from := placeholderFrom.AddDate(-5, 0, 0)

	if _, err := s.RecordRule(ctx, NewRule{
		Key: retireEarlierKey, Country: "zz",
		Payload:  json.RawMessage(`{"days":"7"}`),
		From:     from,
		Document: "Test fixture", Authority: "mhrsd",
		Blocker: true, Blocks: "feature",
		// Recorded unverified, with no actor. `verified_by` is a foreign key
		// to a real account and there is none here; what is under test is
		// retirement and deletion, neither of which turns on verification.
		// It also keeps the row undeletable — 0134 permits removing a
		// placeholder, and this is a figure.
		Notes: "Test fixture. Not a legal value; recorded unverified because " +
			"no account asserted it.",
	}, uuid.Nil); err != nil {
		t.Fatalf("record a figure from before the placeholder: %v", err)
	}

	for _, r := range rulesFor(t, s, retireEarlierKey) {
		payload, ok := r.Payload.(map[string]any)
		if ok && payload["days"] == Placeholder && r.To == "" {
			t.Errorf("a placeholder from %s is still in force beside the "+
				"figure; an absence and a figure cannot both be", r.From)
		}
	}

	// And the figure answers, from the date the article took effect.
	rule, err := s.Resolve(ctx, Query{
		Key: retireEarlierKey, Country: "zz", AsOf: from,
	})
	if err != nil {
		t.Fatalf("the recorded figure does not resolve: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(rule.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["days"] != "7" {
		t.Errorf("resolved %q, want the figure that was recorded", payload["days"])
	}
}

// A placeholder that starts EARLIER is superseded, not deleted.
//
// The period before the figure goes on refusing with "nobody has read this yet"
// rather than with "no such rule", which is the more useful of the two
// sentences and is also what was true. Deleting every placeholder regardless
// would throw that away, and it is why the retirement is scoped to the ones at
// or after the new date.
func TestAnEarlierPlaceholderIsSupersededRatherThanRemoved(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	placeholderFrom := seedRetirePlaceholder(t, s, retireLaterKey)
	from := placeholderFrom.AddDate(5, 0, 0)

	if _, err := s.RecordRule(ctx, NewRule{
		Key: retireLaterKey, Country: "zz",
		Payload:  json.RawMessage(`{"days":"9"}`),
		From:     from,
		Document: "Test fixture", Authority: "mhrsd",
		Blocker: true, Blocks: "feature",
		Notes: "Test fixture. Not a legal value; recorded unverified because " +
			"no account asserted it.",
	}, uuid.Nil); err != nil {
		t.Fatalf("record a figure after the placeholder: %v", err)
	}

	// The day before it took effect still resolves to the placeholder, and the
	// placeholder still refuses — which is what that period deserves.
	before, err := s.Resolve(ctx, Query{
		Key: retireLaterKey, Country: "zz", AsOf: from.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("resolve before the figure: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(before.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["days"] != Placeholder {
		t.Errorf("the day before the figure resolved %q; the period this "+
			"product knew nothing about was restated", payload["days"])
	}
}

// A figure is never deletable, however the placeholder rule is worded.
//
// 0134 widened a `RAISE EXCEPTION` into a condition, and a widened guard is
// worth a test that it did not widen past what it was meant to.
func TestAFigureCannotBeDeleted(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	from := seedRetirePlaceholder(t, s, retireDeleteKey).AddDate(5, 0, 0)
	if _, err := s.RecordRule(ctx, NewRule{
		Key: retireDeleteKey, Country: "zz",
		Payload:  json.RawMessage(`{"days":"11"}`),
		From:     from,
		Document: "Test fixture", Authority: "mhrsd",
		Blocker: true, Blocks: "feature",
		Notes: "Test fixture. Not a legal value; recorded unverified because " +
			"no account asserted it.",
	}, uuid.Nil); err != nil {
		t.Fatalf("record: %v", err)
	}

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			DELETE FROM regulatory_rule
			WHERE rule_key = $1 AND effective_from = $2::date`,
			retireDeleteKey, from)
		return e
	})
	if err == nil {
		t.Error("a recorded figure was deleted. A report re-run for an " +
			"earlier period must still give that period's answer, which it " +
			"cannot if the rule that governed it is gone")
	}
}
