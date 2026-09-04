//go:build integration

// Creating a business in a market whose legal values are still placeholders.
//
// The boot gate asks "may this process start given the tenants it has". That
// answer changes the moment a tenant is created in a new market, and the
// process does not re-run it — so a Bangladesh-only deployment could be handed a
// Saudi client at 10:00 and keep serving it on placeholder GOSI, EOSB and WPS
// values until somebody happened to restart. These tests cover the other end of
// that window.
package provisioning

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// newProvisioning builds the service against the real database and the real
// seeded registry. `strict` is what a production deployment passes.
func newProvisioning(t *testing.T, strict bool) (*Service, *db.Pool) {
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
	return NewService(pool).WithRules(registry.New(pool, strict)), pool
}

// superAdmin is the only actor allowed to create a tenant.
func superAdmin() context.Context {
	return actor.Into(context.Background(),
		actor.Actor{UserID: uuid.New(), IsSuperAdmin: true})
}

func newTenantIn(market string) NewTenant {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	return NewTenant{
		Name:       "Market Gate " + suffix,
		DataRegion: "sa",
		PlanTier:   "starter",
		Market:     market,
		OwnerEmail: "gate" + suffix + "@example.test",
		OwnerName:  "Gate Owner",
	}
}

// A Saudi business can be taken on, because the rules its first sale depends
// on are verified.
//
// This test used to assert the opposite, and the assertion was wrong in a way
// that cost a market. `requireMarketIsUsable` refused a business while ANY
// release-blocking rule was unverified, and the only one left was
// SA.EOSB.ENTITLEMENT — end of service, which is what an employer owes somebody
// who LEAVES. A coffee shop could be onboarded, trade for a year and hire
// nobody who resigns, and the entitlement bands would never come into it. The
// gate refused a sale today over a calculation that might never be performed.
//
// 0124 makes a blocker say what it blocks. The ZATCA rules are 'onboarding' —
// without them nothing can be issued at all, so selling a shop that till would
// be selling them a till that cannot trade. EOSB is 'feature', and is enforced
// where it is used rather than where a business is created.
func TestASaudiBusinessCanBeTakenOn(t *testing.T) {
	svc, _ := newProvisioning(t, true)

	out, err := svc.CreateTenant(superAdmin(), newTenantIn("sa"))
	if err != nil {
		t.Fatalf("a Saudi business could not be created: %v", err)
	}
	if out.TenantID == uuid.Nil {
		t.Error("no tenant came back")
	}
}

// The gate still refuses a market that cannot issue an invoice, and no longer
// refuses one that merely cannot compute a leaving payment.
//
// Both halves matter and they pull in opposite directions, which is why they
// are asserted together. Loosening "any unverified blocker" to "an unverified
// ONBOARDING blocker" must not loosen it to "nothing": a shop that cannot issue
// an invoice must not be sold a till.
//
// Staged inside a transaction that is rolled back, and the predicate is queried
// directly because UnverifiedBlockersFor opens its own transaction and would
// not see an uncommitted change.
func TestOnlyRulesTheFirstSaleNeedsBlockOnboarding(t *testing.T) {
	_, pool := newProvisioning(t, true)
	ctx := context.Background()

	// The query the gate runs.
	const blockers = `
		SELECT count(*) FROM regulatory_rule
		WHERE release_blocker AND blocks = 'onboarding'
		  AND verified_on IS NULL AND effective_to IS NULL
		  AND lower(country) = 'sa'`

	rollback := errors.New("rollback: this test must not unverify a rule")
	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// As things stand: end of service is unverified and Saudi onboarding
		// is open, because a leaving payment is not what a first sale needs.
		var n int
		if e := tx.QueryRow(ctx, blockers).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Errorf("%d onboarding blockers stand while only end of service "+
				"is unverified; a shop that can issue an invoice should be "+
				"able to trade", n)
		}

		// Take away a rule the invoice itself depends on, and the market
		// closes again.
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_rule SET verified_on = NULL, verified_by = NULL
			WHERE country = 'sa' AND blocks = 'onboarding'
			  AND effective_to IS NULL`); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, blockers).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			t.Error("no onboarding blocker stands for a market whose invoice " +
				"rules are unverified; a shop would be sold a till that " +
				"cannot issue anything")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("stage an unverified invoice rule: %v", err)
	}
}

// The same deployment still takes on a Bangladeshi business.
//
// This is the whole point: one market's unverified payroll rules must not stop
// the platform selling into another. Bangladesh has no release-blocking rule
// outstanding, so nothing here is being waived.
func TestABangladeshBusinessIsCreatedOnTheSameStrictDeployment(t *testing.T) {
	svc, pool := newProvisioning(t, true)
	ctx := superAdmin()

	out, err := svc.CreateTenant(ctx, newTenantIn("bd"))
	if err != nil {
		t.Fatalf("a Bangladeshi business was refused: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, out.TenantID)
			return e
		})
	})

	if out.TenantID == uuid.Nil {
		t.Error("no tenant id came back")
	}
	if out.TemporaryPassword == "" {
		t.Error("no temporary password came back; the owner cannot sign in")
	}
}

// A development deployment creates tenants in any market.
//
// Where unverified values are tolerated at the point of use, refusing them at
// provisioning would be a stricter rule than the one the deployment is
// configured with — and would make it impossible to develop the Saudi modules
// at all.
func TestADevelopmentDeploymentCreatesABusinessInAnyMarket(t *testing.T) {
	svc, pool := newProvisioning(t, false)
	ctx := superAdmin()

	out, err := svc.CreateTenant(ctx, newTenantIn("sa"))
	if err != nil {
		t.Fatalf("a development deployment refused a Saudi business: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, out.TenantID)
			return e
		})
	})
}

// A service with no registry wired does not silently enforce nothing.
//
// It enforces nothing — which is correct, because it was not asked — but the
// distinction matters enough to pin: if WithRules is ever dropped from the API
// server's wiring, this test still passes while the gate quietly stops running,
// so the wiring is asserted separately by the API-level tests rather than here.
func TestAServiceWithNoRegistryDoesNotBlockProvisioning(t *testing.T) {
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 2, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)

	svc := NewService(pool)
	if err := svc.requireMarketIsUsable(context.Background(), "sa"); err != nil {
		t.Errorf("a service with no registry refused a market: %v", err)
	}
}
