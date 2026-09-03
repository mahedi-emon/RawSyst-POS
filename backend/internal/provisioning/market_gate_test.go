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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
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

// A production deployment refuses a Saudi business while Saudi rules are
// placeholders.
//
// SA.EOSB.ENTITLEMENT is seeded unverified and is a release blocker: the
// entitlement is days of wage per year of service and nobody has confirmed the
// bands against the Labour Law. Taking on a Saudi client now would mean
// computing that client's end-of-service figures from a guess.
//
// GOSI and the WPS wage-file layout were the other two, and 0117 and 0116
// recorded both from their authorities' own publications, verified. The gate
// is unchanged; there is simply one rule left to confirm rather than three.
func TestASaudiBusinessIsRefusedWhileItsRulesAreUnverified(t *testing.T) {
	svc, _ := newProvisioning(t, true)

	_, err := svc.CreateTenant(superAdmin(), newTenantIn("sa"))
	if err == nil {
		t.Fatal("a Saudi business was created on placeholder legal values")
	}
	if errs.CodeOf(err) != errs.CodeUnverifiedRule {
		t.Errorf("code = %v, want %v: %v",
			errs.CodeOf(err), errs.CodeUnverifiedRule, err)
	}
	// The message has to name the rules. "Cannot create" without saying which
	// value is missing leaves the operator with nothing to act on.
	for _, key := range []string{"SA.EOSB.ENTITLEMENT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the refusal does not name %s: %v", key, err)
		}
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
