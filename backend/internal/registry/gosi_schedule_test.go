//go:build integration

// Whether GOSI's next schedule needs a code change.
//
// The Council of Ministers approved a new Social Insurance Law in July 2024 for
// employees with no prior contribution periods. GOSI's own pages state the
// Annuities Branch flatly at 18% with no hire-date distinction and publish no
// year-by-year escalation, so 0117 records one rate for both Saudi bands and
// says plainly that it is not claiming no escalation exists.
//
// The question that leaves open is the one these tests answer: WHEN GOSI
// publishes a dated schedule, can it be put into this product without changing
// code? If the answer were no, "no official schedule is available" would be
// hiding a second problem behind the first.
//
// Everything here runs inside a transaction that is rolled back. A new version
// of a global rule is global, and leaving one behind would restate GOSI for
// every other test in this package.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// stageGOSIVersion closes the rule in force and inserts a successor, exactly as
// registry.RecordRule does, inside the caller's transaction.
func stageGOSIVersion(
	ctx context.Context, tx pgx.Tx, from string, payload map[string]any,
) error {
	// Close whatever version COVERS the new date, rather than whichever one
	// happens to be open: a database carrying a later version already would
	// otherwise have that one closed to a date before it starts, which the
	// period-ordering constraint rejects.
	if _, err := tx.Exec(ctx, `
		UPDATE regulatory_rule SET effective_to = $1::date
		WHERE rule_key = 'SA.GOSI.RATES' AND country = 'sa'
		  AND effective_from < $1::date
		  AND (effective_to IS NULL OR effective_to > $1::date)`,
		from); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO regulatory_rule
		  (rule_key, country, payload, effective_from, source_authority,
		   source_document, release_blocker, notes, verified_on)
		VALUES ('SA.GOSI.RATES','sa',$1,$2::date,'gosi',
		        'A dated schedule published by GOSI', true,
		        'Staged by a test and rolled back.', $2::date)`, body, from)
	return err
}

// A published escalation goes in as dated versions, with no code change.
//
// An escalation IS a series of dated rules: "9% now, 9.5% from 2027, 10% from
// 2028" is three versions of one rule, and the registry resolves the one in
// force on the month being paid. That is the same mechanism that already makes
// January 2026 resolve the placeholder and February resolve 0117's figures.
//
// So the shape GOSI would publish is expressible today. What is missing is the
// schedule itself, not a place to put it.
func TestAPublishedGOSIEscalationNeedsNoCodeChange(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	rollback := errors.New("rollback: this test must not restate GOSI")
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// A hypothetical published step, with the shape 0117 already stores:
		// the post-July-2024 cohort moving while the earlier cohort stays.
		if e := stageGOSIVersion(ctx, tx, "2027-01-01", map[string]any{
			"saudi_post_jul2024": map[string]string{
				"employer": "0.1225", "employee": "0.1025"},
			"saudi_pre_jul2024": map[string]string{
				"employer": "0.1175", "employee": "0.0975"},
			"expatriate": map[string]string{
				"employer": "0.02", "employee": "0"},
			"wage_cap": "45000",
		}); e != nil {
			return e
		}

		// Before the step, the rule in force is still 0117's.
		var before, after struct {
			Post struct{ Employer, Employee string } `json:"saudi_post_jul2024"`
			Pre  struct{ Employer, Employee string } `json:"saudi_pre_jul2024"`
			Cap  string                              `json:"wage_cap"`
		}
		if e := s.Into(ctx, Query{
			Key: "SA.GOSI.RATES", Country: "sa", Tx: tx,
			AsOf: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		}, &before); e != nil {
			return e
		}
		if before.Post.Employer != "0.1175" {
			t.Errorf("August 2026 resolves %s for the post-2024 cohort, want "+
				"0.1175 — staging a future version changed the past",
				before.Post.Employer)
		}

		// After it, the new schedule applies, resolved from the month being
		// paid rather than from today.
		if e := s.Into(ctx, Query{
			Key: "SA.GOSI.RATES", Country: "sa", Tx: tx,
			AsOf: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
		}, &after); e != nil {
			return e
		}
		if after.Post.Employer != "0.1225" || after.Post.Employee != "0.1025" {
			t.Errorf("March 2027 resolves %s/%s, want 0.1225/0.1025 — a "+
				"published escalation would not take effect",
				after.Post.Employer, after.Post.Employee)
		}
		// The cohort that the July 2024 law did not move stays where it was.
		if after.Pre.Employer != "0.1175" {
			t.Errorf("the pre-July-2024 cohort moved to %s; a schedule for "+
				"new entrants must not restate everybody else",
				after.Pre.Employer)
		}
		if after.Cap != "45000" {
			t.Errorf("the wage ceiling became %s", after.Cap)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("stage a GOSI schedule: %v", err)
	}
}

// The staging left nothing behind.
//
// The check that makes the test above safe to keep: a rolled-back version of a
// GLOBAL rule must not be visible afterwards, or every later payroll test in
// this package would be computing against a schedule nobody published.
func TestStagingAGOSIScheduleLeavesNothingBehind(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	var rates struct {
		Post struct{ Employer string } `json:"saudi_post_jul2024"`
	}
	if err := s.Into(ctx, Query{
		Key: "SA.GOSI.RATES", Country: "sa",
		AsOf: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	}, &rates); err != nil {
		t.Fatalf("resolve GOSI: %v", err)
	}
	if rates.Post.Employer != "0.1175" {
		t.Errorf("March 2027 resolves %s; 0117's figure is 0.1175, so a "+
			"staged schedule was committed", rates.Post.Employer)
	}
}

// The wage ceiling is a figure, not a constant in the code.
//
// GOSI publishes SR 45,000 today and has moved it before. Payroll reads it from
// the rule, so a change is a new version rather than a release.
func TestTheGOSIWageCeilingComesFromTheRule(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	rollback := errors.New("rollback")
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := stageGOSIVersion(ctx, tx, "2028-01-01", map[string]any{
			"saudi_post_jul2024": map[string]string{
				"employer": "0.1175", "employee": "0.0975"},
			"saudi_pre_jul2024": map[string]string{
				"employer": "0.1175", "employee": "0.0975"},
			"expatriate": map[string]string{
				"employer": "0.02", "employee": "0"},
			"wage_cap": "50000",
		}); e != nil {
			return e
		}
		var got struct {
			Cap string `json:"wage_cap"`
		}
		if e := s.Into(ctx, Query{
			Key: "SA.GOSI.RATES", Country: "sa", Tx: tx,
			AsOf: time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC),
		}, &got); e != nil {
			return e
		}
		if got.Cap != "50000" {
			t.Errorf("the ceiling resolves to %s, want 50000 — it is not "+
				"being read from the rule", got.Cap)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("stage a ceiling change: %v", err)
	}
}

// End of service still cannot be computed from a placeholder.
//
// This is the property that makes 0124's change safe. The provisioning gate
// used to refuse a Saudi business outright while SA.EOSB.ENTITLEMENT was
// unverified — a coffee shop turned away over a leaving payment it might never
// have to make. 0124 lets the business be created and leaves the rule enforced
// where it is actually used.
//
// If this ever passes, the loosening became a hole: a shop would be told what
// it owes a departing employee on the strength of a number nobody confirmed.
func TestEndOfServiceStillRefusesWhileItsRuleIsUnverified(t *testing.T) {
	s := newRegistry(t)
	s.requireVerified = true

	_, err := s.Decimal(context.Background(), Query{
		Key:     "SA.EOSB.ENTITLEMENT",
		Country: "sa",
		AsOf:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}, "days_per_year_first_five")

	if err == nil {
		t.Fatal("an end-of-service entitlement was resolved from a rule " +
			"nobody has verified against the Labour Law")
	}
	if errs.CodeOf(err) != errs.CodeUnverifiedRule {
		t.Errorf("code = %v, want %v: %v", errs.CodeOf(err),
			errs.CodeUnverifiedRule, err)
	}
}
