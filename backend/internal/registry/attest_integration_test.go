//go:build integration

// The whole path, against a real registry: a placeholder goes in, a file is
// read, and the rule comes out verified, superseded rather than overwritten,
// with the person who read the document named on it.
//
// # Why it uses a rule of its own
//
// `regulatory_rule` is append-only by trigger and platform-wide by design, so a
// test that recorded SA.EOSB.ENTITLEMENT would permanently verify it in every
// database the suite touches — and the boot gate's own tests assert that a
// Saudi deployment is still refused. The test therefore seeds and retires its
// own key in a country nobody trades in, which is also the only way it can be
// re-run.
package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const attestTestKey = "ZZ.TEST.ATTEST"

// seedTestRule puts a placeholder in force and returns the day it starts.
//
// The window each run gets, so consecutive runs never collide: the rule is
// append-only, its ranges may not overlap, and nothing this test writes can
// ever be deleted.
func seedTestRule(t *testing.T, s *Service) (from time.Time) {
	t.Helper()
	ctx := context.Background()

	if err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Anything an earlier run left open, whether it finished or was
		// killed. Closed rather than deleted, which the trigger refuses.
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_rule
			SET effective_to = effective_from + 1
			WHERE rule_key = $1 AND effective_to IS NULL`,
			attestTestKey); e != nil {
			return e
		}

		// Start after everything this key has ever occupied. A fixed date
		// worked exactly once: the second run tried to seed a placeholder over
		// the range the first run's rows had closed, and the exclusion
		// constraint refused it.
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(
			         max(coalesce(effective_to, effective_from)) + 1,
			         current_date - 30)
			FROM regulatory_rule WHERE rule_key = $1`,
			attestTestKey).Scan(&from); e != nil {
			return e
		}

		_, e := tx.Exec(ctx, `
			INSERT INTO regulatory_rule
			  (rule_key, country, payload, effective_from, source_authority,
			   source_document, release_blocker, notes)
			VALUES ($1, 'zz',
			        '{"basis":"__VERIFY__","days":"__VERIFY__",
			          "share":"__VERIFY__","window":"__VERIFY__",
			          "already_known": 14}'::jsonb,
			        $2, 'mhrsd', 'Nothing real', false,
			        'Seeded by the source-file tests.')`,
			attestTestKey, from)
		return e
	}); err != nil {
		t.Fatalf("seed the test rule: %v", err)
	}

	t.Cleanup(func() {
		_ = s.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(), `
				UPDATE regulatory_rule
				SET effective_to = effective_from + 1
				WHERE rule_key = $1 AND effective_to IS NULL`, attestTestKey)
			return e
		})
		s.Invalidate()
	})
	return from
}

// anOperator is somebody who can put their name to a legal value here.
func anOperator(t *testing.T, s *Service) string {
	t.Helper()
	ctx := context.Background()

	var email string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT email FROM app_user
			WHERE tenant_id IS NULL AND status = 'active'
			ORDER BY created_at LIMIT 1`).Scan(&email)
		if e == pgx.ErrNoRows {
			// Create one rather than skip: a database with no platform
			// operator is a database this path has never been exercised
			// against, which is the case worth covering.
			id := uuid.New()
			_, e = tx.Exec(ctx, `
				INSERT INTO app_user
				  (id, tenant_id, email, full_name, password_hash, status)
				VALUES ($1, NULL, $2, 'Test Operator', 'x', 'active')`,
				id, "attest-"+id.String()[:8]+"@example.test")
			if e != nil {
				return e
			}
			return tx.QueryRow(ctx,
				`SELECT email FROM app_user WHERE id = $1`, id).Scan(&email)
		}
		return e
	})
	if err != nil {
		t.Fatalf("find somebody to attest: %v", err)
	}
	return email
}

func attestationFor(reader string, from time.Time, values map[string]string) Attestation {
	return Attestation{
		Version: AttestationVersion,
		ReadBy:  reader,
		ReadOn:  time.Now().UTC().Format("2006-01-02"),
		Statement: "Read the document named in the guidance and recorded " +
			"the figures it gives. Not a real document and not the law.",
		Rules: []AttestedRule{{
			Key:    attestTestKey,
			From:   from.Format("2006-01-02"),
			Values: values,
		}},
	}
}

// The figures are deliberately not anybody's law: this test proves the
// PLUMBING carries a figure from a file to a computation, and a plausible
// number here is a number somebody could screenshot and quote.
func nonsenseFigures() map[string]string {
	return map[string]string{
		"basis": "gross", "days": "7", "share": "0.5", "window": "3",
	}
}

func TestRecordingFromASourceFile(t *testing.T) {
	s := newRegistry(t)
	withTestPack(t)

	placeholderFrom := seedTestRule(t, s)
	reader := anOperator(t, s)
	ctx := context.Background()

	// It shows up as outstanding first, or the report a deployment reads is
	// not telling it what it is waiting for.
	//
	// Looked up by key rather than by asserting the whole list. `zz` is this
	// suite's fixture market and `regulatory_rule` is append-only by trigger,
	// so every fixture any test in this package has ever seeded is still there
	// — `health_test.go` adds an onboarding blocker for the boot gate — and a
	// test that demands `zz` contain exactly one rule is asserting something
	// append-only data cannot promise.
	mine, err := outstandingFor(s, ctx, attestTestKey)
	if err != nil {
		t.Fatalf("outstanding: %v", err)
	}
	if mine == nil {
		t.Fatalf("%s is not reported as outstanding", attestTestKey)
	}
	if len(mine.Fields) != 4 {
		t.Errorf("reported %v as unfilled; `already_known` holds a figure "+
			"and is not one of them", mine.Fields)
	}

	from := placeholderFrom.AddDate(0, 0, 1)
	att := attestationFor(reader, from, nonsenseFigures())

	// A dry run writes nothing. This is what a deployment gate runs, and if it
	// wrote anything the gate would be the thing that changed the answer.
	outcomes, err := s.ApplyAttestation(ctx, att, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Action != "would record" {
		t.Fatalf("dry run reported %+v", outcomes)
	}
	if still, _ := outstandingFor(s, ctx, attestTestKey); still == nil {
		t.Fatal("a dry run recorded the rule")
	}

	// Now for real.
	outcomes, err = s.ApplyAttestation(ctx, att, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcomes[0].Action != "recorded" {
		t.Fatalf("apply reported %+v", outcomes)
	}

	// Not outstanding any more, and the value resolves.
	if still, _ := outstandingFor(s, ctx, attestTestKey); still != nil {
		t.Errorf("still outstanding after recording: %+v", still)
	}

	rule, err := s.Resolve(ctx, Query{
		Key: attestTestKey, Country: "zz", AsOf: from,
	})
	if err != nil {
		t.Fatalf("the recorded rule does not resolve: %v", err)
	}
	if !rule.Verified() {
		t.Error("the recorded rule resolves unverified, so every use of it " +
			"would go on refusing")
	}
	var payload map[string]any
	if err := json.Unmarshal(rule.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["days"] != "7" {
		t.Errorf("days resolved as %v", payload["days"])
	}
	if payload["already_known"] != float64(14) {
		t.Errorf("the figure that was already recorded came back as %v; "+
			"filling in the unrecorded fields disturbed it",
			payload["already_known"])
	}

	// The day before it took effect still refuses, because on that day the
	// product had no verified figure and a report re-run must say so.
	before, err := s.Resolve(ctx, Query{
		Key: attestTestKey, Country: "zz", AsOf: from.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("resolve before the effective date: %v", err)
	}
	if before.Verified() {
		t.Error("the day before the figure was recorded resolves verified; " +
			"the placeholder period was restated")
	}

	// Who recorded it, in the trail.
	assertAudited(t, s, "regulatory_rule_recorded")
}

func TestApplyingTheSameSourceFileTwiceRecordsOnce(t *testing.T) {
	s := newRegistry(t)
	withTestPack(t)

	placeholderFrom := seedTestRule(t, s)
	reader := anOperator(t, s)
	ctx := context.Background()

	from := placeholderFrom.AddDate(0, 0, 1)
	att := attestationFor(reader, from, nonsenseFigures())

	if _, err := s.ApplyAttestation(ctx, att, false); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	outcomes, err := s.ApplyAttestation(ctx, att, false)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if outcomes[0].Action != "already recorded" {
		t.Fatalf("the second run reported %+v; a deployment that runs this "+
			"on every boot would fill the registry with a history of "+
			"nothing happening", outcomes)
	}

	// Exactly one row in force, and one superseded.
	var open, closed int
	if err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE effective_to IS NULL),
			       count(*) FILTER (WHERE effective_to IS NOT NULL)
			FROM regulatory_rule WHERE rule_key = $1`,
			attestTestKey).Scan(&open, &closed)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if open != 1 {
		t.Errorf("%d rows in force, want 1", open)
	}
}

// A figure already recorded is not this tool's to change.
func TestASourceFileWillNotOverwriteARecordedFigure(t *testing.T) {
	s := newRegistry(t)
	withTestPack(t)

	placeholderFrom := seedTestRule(t, s)
	reader := anOperator(t, s)
	ctx := context.Background()

	from := placeholderFrom.AddDate(0, 0, 1)
	if _, err := s.ApplyAttestation(ctx,
		attestationFor(reader, from, nonsenseFigures()), false); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	different := nonsenseFigures()
	different["days"] = "9"
	_, err := s.ApplyAttestation(ctx,
		attestationFor(reader, from.AddDate(0, 0, 1), different), false)
	if err == nil {
		t.Fatal("a source file silently replaced a figure already in force; " +
			"correcting a recorded value belongs on the screen where the " +
			"person doing it sees what they are replacing")
	}
}

// Verification is a named person's act, and the name has to be somebody here.
func TestASourceFileFromNobodyIsRefused(t *testing.T) {
	s := newRegistry(t)
	withTestPack(t)

	placeholderFrom := seedTestRule(t, s)
	ctx := context.Background()

	from := placeholderFrom.AddDate(0, 0, 1)
	att := attestationFor("nobody-at-all@example.invalid", from, nonsenseFigures())
	if _, err := s.ApplyAttestation(ctx, att, false); err == nil {
		t.Fatal("a figure was recorded as verified by an address that " +
			"matches nobody on this installation")
	}

	// And a business user is not a platform operator, even a real one.
	var tenantUser string
	_ = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT email FROM app_user
			WHERE tenant_id IS NOT NULL AND status = 'active' LIMIT 1`).
			Scan(&tenantUser)
	})
	if tenantUser == "" {
		t.Skip("this database has no business user to check against")
	}
	if _, err := s.ApplyAttestation(ctx,
		attestationFor(tenantUser, from, nonsenseFigures()), false); err == nil {
		t.Fatal("a business user recorded what every business in the " +
			"market computes from")
	}
}

// The template a deployment produces is a file the applier accepts once the
// figures are typed in. If it were not, the two halves would disagree and the
// person filling it in would find out last.
func TestTheTemplateRoundTrips(t *testing.T) {
	s := newRegistry(t)
	withTestPack(t)

	seedTestRule(t, s)
	reader := anOperator(t, s)
	ctx := context.Background()

	raw, err := s.Template(ctx, "zz")
	if err != nil {
		t.Fatalf("template: %v", err)
	}

	var att Attestation
	if err := json.Unmarshal(raw, &att); err != nil {
		t.Fatalf("the template is not readable as an attestation: %v", err)
	}
	if len(att.Rules) != 1 {
		t.Fatalf("the template holds %d rule(s)", len(att.Rules))
	}
	if len(att.Rules[0].Guidance) == 0 {
		t.Error("the template carries no guidance, so somebody filling it " +
			"in is back to guessing from the field names")
	}

	att.ReadBy = reader
	att.ReadOn = time.Now().UTC().Format("2006-01-02")
	att.Statement = "Read the document named in the guidance. Not real."
	att.Rules[0].Values = nonsenseFigures()

	filled, err := json.Marshal(att)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Through the same door the command uses, guidance block and all.
	parsed, err := ParseAttestation(filled)
	if err != nil {
		t.Fatalf("a filled-in template was refused: %v", err)
	}
	outcomes, err := s.ApplyAttestation(ctx, parsed, false)
	if err != nil {
		t.Fatalf("apply a filled-in template: %v", err)
	}
	if outcomes[0].Action != "recorded" {
		t.Fatalf("reported %+v", outcomes)
	}
}

func assertAudited(t *testing.T, s *Service, action string) {
	t.Helper()
	var n int
	if err := s.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT count(*) FROM audit_log
			WHERE action = $1 AND occurred_at > now() - INTERVAL '5 minutes'`,
			action).Scan(&n)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if n == 0 {
		t.Errorf("nothing in the trail says %s. Recording a legal value is "+
			"the most consequential act a platform operator performs and it "+
			"was not being recorded", action)
	}
}

// outstandingFor picks one rule out of the fixture market's report.
//
// Returns nil when it is not there, so a caller reads "recorded" and "not
// recorded" as presence rather than as a count of everything `zz` happens to
// hold.
func outstandingFor(
	s *Service, ctx context.Context, key string,
) (*Unrecorded, error) {
	open, err := s.Outstanding(ctx, "zz")
	if err != nil {
		return nil, err
	}
	for i := range open {
		if open[i].Key == key {
			return &open[i], nil
		}
	}
	return nil, nil
}
