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
var saudiHRBlockers = []string{
	"SA.EOSB.ENTITLEMENT",
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

// A Saudi deployment is still refused, exactly as before.
//
// The other half of the rule, and the one that would fail silently if the
// filter were ever loosened: where the market IS served, an unverified
// release-blocker must still stop the process.
func TestASaudiDeploymentIsStillBlockedByItsOwnUnverifiedRules(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), []string{"sa"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	for _, key := range saudiHRBlockers {
		if !contains(rep.BlockingRelease, key) {
			t.Errorf("%s does not block a Saudi deployment; the gate has been "+
				"weakened for a market it is meant to protect", key)
		}
		if contains(rep.DeferredBlockers, key) {
			t.Errorf("%s is reported as deferred on a Saudi deployment", key)
		}
	}
}

// A deployment serving both markets is blocked by the Saudi rules.
//
// The mixed case is the one a naive implementation gets wrong, by treating the
// market set as a single value rather than a set: serving Bangladesh must not
// excuse the Saudi obligations of the Saudi tenants sitting beside it.
func TestAMixedMarketDeploymentIsBlockedByTheSaudiRules(t *testing.T) {
	s := newRegistry(t)

	rep, err := s.healthFor(context.Background(), []string{"bd", "sa"})
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	for _, key := range saudiHRBlockers {
		if !contains(rep.BlockingRelease, key) {
			t.Errorf("%s does not block a deployment that serves Saudi Arabia", key)
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
