//go:build integration

// The production boot gate, and which markets it speaks for.
//
// `cmd/api` refuses to start when an unverified release-blocking legal value
// applies to this deployment. That check used to consider every release-blocker
// in the registry regardless of country, and all three unverified ones are
// Saudi HR rules — SA.EOSB.ENTITLEMENT, SA.GOSI.RATES, SA.WPS.WAGE_FILE_FORMAT.
// A Bangladesh-only deployment could therefore be fully working at the counter
// and still unable to boot in production, held up by payroll rules for a
// country none of its clients trade in.
//
// These tests run against the REAL seeded registry rather than fixtures, so a
// migration that adds a release-blocker in a new country is caught here rather
// than in somebody's staging environment.
package registry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

func newRegistry(t *testing.T) *Service {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// requireVerified is irrelevant to Health — it gates rule RESOLUTION, not
	// reporting — but false is what a development process passes, and the gate
	// under test must behave the same either way.
	return New(pool, false)
}

// contains reports whether a rule key is in a report list.
func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// The Saudi HR rules are the ones this whole change is about, and they are
// asserted by name. A test that only counted would still pass if the three
// swapped places with three others.
// SA.GOSI.RATES and SA.WPS.WAGE_FILE_FORMAT used to head this list. 0117
// recorded GOSI's rates from GOSI's own employer guidance and 0116 recorded the
// Ministry's published wage-file layout, both verified, so neither is an
// unverified blocker any more and listing them here asserted the opposite.
//
// They kept passing for a while regardless, because two rows written by an
// earlier test run — a 12.75% GOSI rate and a "reported revision" of the wage
// file, both unverified, both open-ended from 2027 — had closed the verified
// versions and stood in their place. The tests were green because of
// fabricated data, which is worse than being red. Both rows are gone.
//
// End of service is the one genuinely outstanding: the entitlement is days of
// wage per year of service and nobody has confirmed the bands against the
// Labour Law.
//
// It is a FEATURE blocker, and that is the whole point of the rest of this
// file. The engine that consumes it is complete — both service bands, the wage
// basis, Article 85's resignation fractions, the accrual, the settlement, the
// audit entry — and what is missing is a figure in a statute. A deployment must
// therefore start, report the capability as awaiting its figure, and refuse
// only the calculation itself.
var saudiHRBlockers = []string{
	"SA.EOSB.ENTITLEMENT",
}

// attestOnboardingKey is a fixture blocker that DOES stop a market trading.
//
// Nothing real is unverified at onboarding level any more — every ZATCA format
// a Saudi invoice depends on is recorded and verified — so the half of the gate
// that still refuses has no production data left to prove itself against. A
// fixture under `zz`, ISO 3166's "unknown country", supplies one.
//
// `regulatory_rule` is append-only by trigger, so this row cannot be cleaned up
// and stays in whatever database the suite ran against. That is why `zz` is the
// country: the invariant that every verified rule carries a legal citation
// already excludes it, because demanding evidence for a rule whose country is
// "unknown" is demanding evidence for something no authority published.
const attestOnboardingKey = "ZZ.TEST.ONBOARDING_BLOCKER"

// seedOnboardingBlocker records the fixture, once per database.
func seedOnboardingBlocker(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Raw().Exec(ctx, `
		INSERT INTO regulatory_rule
		  (rule_key, country, payload, effective_from, source_authority,
		   source_document, release_blocker, blocks, notes)
		VALUES ($1, 'zz', '{"format":"__VERIFY__"}'::jsonb, '2020-01-01',
		        'mhrsd', 'Nothing real', true, 'onboarding',
		        'Seeded by the boot-gate tests. Nothing in the product reads '
		        'it; it exists so the half of the gate that still refuses a '
		        'production start can be proved against a rule that genuinely '
		        'stops a market trading.')
		ON CONFLICT DO NOTHING`, attestOnboardingKey); err != nil {
		t.Fatalf("seed the onboarding blocker: %v", err)
	}
}

// A Bangladesh-only deployment starts.
//
// The Saudi blockers are still unverified and still reported — as deferred, not
// as resolved. Nothing about them has been decided; they simply do not apply to
// anybody this deployment serves.
func TestABangladeshOnlyDeploymentIsNotBlockedBySaudiRules(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), []string{"bd"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	for _, key := range saudiHRBlockers {
		if contains(rep.BlockingRelease, key) {
			t.Errorf("%s blocks a Bangladesh-only deployment", key)
		}
		if !contains(rep.DeferredBlockers, key) {
			t.Errorf("%s is not reported as deferred; an unverified rule must "+
				"stay visible even when it is not blocking", key)
		}
	}
	if len(rep.BlockingRelease) != 0 {
		t.Errorf("a Bangladesh-only deployment is blocked by %v", rep.BlockingRelease)
	}
}

// A Saudi deployment starts, and says what it cannot compute.
//
// This is the change 0124 made possible and the gate had not taken up. End of
// service is what an employer owes somebody who LEAVES: a shop can be onboarded
// on Monday, trade for a year and have nobody resign, and the entitlement bands
// never come into it. Refusing to start the whole service over a calculation
// that may never be performed took a regulatory-data condition and reported it
// as a broken deployment.
//
// Nothing is loosened. The rule is still unverified, still named at every
// start, and `gate()` still refuses the calculation itself at the point of use
// — which is the protection that ever mattered.
func TestASaudiDeploymentStartsWithAFeatureBlockerAndNamesIt(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), []string{"sa"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	for _, key := range saudiHRBlockers {
		if contains(rep.BlockingRelease, key) {
			t.Errorf("%s refuses a Saudi production start. It blocks a "+
				"FEATURE, not onboarding: the software is complete and the "+
				"figure is not on record, and those are different failures",
				key)
		}
		if !contains(rep.AwaitingData, key) {
			t.Errorf("%s is not reported as awaiting its figure. An "+
				"unverified rule that no longer stops a boot must be named at "+
				"every start, or it becomes invisible", key)
		}
		if contains(rep.DeferredBlockers, key) {
			t.Errorf("%s is reported as deferred on a Saudi deployment; it "+
				"applies to a market this deployment serves", key)
		}
	}
}

// An onboarding blocker still refuses, exactly as before.
//
// The other half of the rule, and the one that would fail silently if the split
// were ever read the wrong way round: where the market IS served and the market
// cannot trade without the figure, an unverified rule must still stop the
// process.
func TestAnOnboardingBlockerStillRefusesAProductionStart(t *testing.T) {
	s := newRegistry(t)
	seedOnboardingBlocker(t, s)

	rep, err := s.healthFor(context.Background(), []string{"zz"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if !contains(rep.BlockingRelease, attestOnboardingKey) {
		t.Errorf("%s does not block a deployment serving its market; the gate "+
			"has been weakened for the case it exists to protect",
			attestOnboardingKey)
	}
	if contains(rep.AwaitingData, attestOnboardingKey) {
		t.Errorf("%s is reported as merely awaiting data. Nothing in its "+
			"market can trade without it, which is a refusal",
			attestOnboardingKey)
	}
}

// A market that is not served defers an onboarding blocker rather than
// refusing.
//
// The two filters are independent and this pins that they compose: blocking
// requires BOTH that the market is served and that the rule stops trading in
// it.
func TestAnOnboardingBlockerForAnUnservedMarketIsDeferred(t *testing.T) {
	s := newRegistry(t)
	seedOnboardingBlocker(t, s)

	rep, err := s.healthFor(context.Background(), []string{"bd"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if contains(rep.BlockingRelease, attestOnboardingKey) {
		t.Errorf("%s blocks a deployment that does not serve its market",
			attestOnboardingKey)
	}
	if !contains(rep.DeferredBlockers, attestOnboardingKey) {
		t.Errorf("%s is not reported as deferred", attestOnboardingKey)
	}
}

// A deployment serving both markets separates the two Saudi conditions.
//
// The mixed case is the one a naive implementation gets wrong, by treating the
// market set as a single value rather than a set: serving Bangladesh must not
// excuse the Saudi obligations of the Saudi tenants sitting beside it.
func TestAMixedMarketDeploymentStillOwesTheSaudiRules(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), []string{"bd", "sa"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	for _, key := range saudiHRBlockers {
		if !contains(rep.AwaitingData, key) {
			t.Errorf("%s is not owed by a deployment that serves Saudi Arabia",
				key)
		}
		if contains(rep.DeferredBlockers, key) {
			t.Errorf("%s is deferred although Saudi Arabia is served", key)
		}
	}
}

// A deployment with no tenants blocks on nothing, and says so.
//
// It serves no markets and has no legal figure to compute. This is deliberately
// permissive and it is not a bypass: `gate()` refuses EVERY unverified rule at
// the point of use when requireVerified is set, so the first Saudi payroll run
// still fails on SA.GOSI.RATES whatever happened at boot.
func TestADeploymentWithNoTenantsBlocksOnNothing(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if len(rep.BlockingRelease) != 0 {
		t.Errorf("a deployment serving no markets is blocked by %v", rep.BlockingRelease)
	}
	if len(rep.DeferredBlockers) == 0 {
		t.Error("no blockers were reported as deferred; the unverified Saudi " +
			"rules must still be visible to an operator")
	}
}

// The market set comes from tenant data, not from configuration.
//
// The half healthFor cannot cover. It also pins the thing most likely to break
// silently: `tenant` is FORCE row-level security, so reading it from an
// unscoped connection returns zero rows with no error — which would read as
// "this deployment serves no markets" and quietly disable the gate for
// everyone. If servedMarkets ever stops using the platform plane, this fails.
func TestServedMarketsAreReadFromTenantData(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	markets, err := s.servedMarkets(ctx)
	if err != nil {
		t.Fatalf("served markets: %v", err)
	}

	// The suite provisions tenants, so this database has some. Zero here means
	// the read could not see them rather than that none exist.
	if len(markets) == 0 {
		t.Fatal("no served markets found; the tenant read is returning nothing, " +
			"which is what an unscoped connection does under row-level security")
	}
	for _, m := range markets {
		if m == "" {
			t.Error("a served market is empty")
		}
		if m != "sa" && m != "bd" && m != "us" {
			t.Errorf("served market %q is not one the product supports", m)
		}
	}
}
