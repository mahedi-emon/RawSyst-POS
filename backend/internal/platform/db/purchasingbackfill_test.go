//go:build integration

// Migration 0051 — the purchasing permission backfill.
//
// 0032 and 0033 granted the purchasing verbs to the platform role TEMPLATES and
// never touched the copies tenants already held. A tenant provisioned before
// those migrations ran therefore carries a clone taken from a template that did
// not yet have purchasing.*, and opening Purchasing tells its Owner they lack a
// permission the module assumes every Owner has.
//
// These tests reproduce that tenant, replay the real migration body — loaded
// from the embedded migration set rather than restated here, so the thing under
// test is the SQL that ships — and prove the properties that matter: the old
// tenant ends up with the verbs, an unrelated role gains nothing, and the loop
// cannot reach across tenants even with the platform flag set.
package db

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const backfillMigrationName = "backfill_purchasing_permissions"

// backfillSQL returns migration 0051's body. Matched by name rather than by
// number so that renumbering the file makes this fail loudly instead of
// silently testing a different migration.
func backfillSQL(t *testing.T) string {
	t.Helper()
	all, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	for _, m := range all {
		if m.Name == backfillMigrationName {
			return m.SQL
		}
	}
	t.Fatalf("migration %q not found in the embedded set", backfillMigrationName)
	return ""
}

// runBackfill replays the migration exactly as the migrator would: one
// transaction, on the platform plane, with no tenant context of its own. The DO
// block sets and clears the tenant per iteration, which is the behaviour under
// test.
func runBackfill(t *testing.T, pool *Pool) {
	t.Helper()
	sql := backfillSQL(t)
	if err := pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql)
		return err
	}); err != nil {
		t.Fatalf("replay backfill migration: %v", err)
	}
}

// seedPre0032Clone builds the role a tenant provisioned before 0032 would hold:
// the template cloned as it stood then, which is the template as it stands now
// minus everything 0032 and 0033 added.
//
// Stripping after the clone rather than reconstructing the old template by hand
// keeps the fixture honest — it stays correct as the seed grows, and it cannot
// drift into asserting a permission set no release ever shipped.
func seedPre0032Clone(t *testing.T, pool *Pool, tenantID uuid.UUID, roleKey string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var roleID uuid.UUID
	err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name, is_system, cloned_from)
			SELECT $1, key, name, true, id FROM role
			WHERE tenant_id IS NULL AND key = $2
			RETURNING id`, tenantID, roleKey).Scan(&roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT $1, rp.permission FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = $2`, roleID, roleKey); err != nil {
			return err
		}
		// 0032's grants, gone. 0033's too, but only for the Purchase Manager:
		// 0005 already gave the Store Manager catalog.view, so removing it here
		// would invent a state no tenant was ever in.
		_, err := tx.Exec(ctx, `
			DELETE FROM role_permission
			WHERE role_id = $1
			  AND (permission LIKE 'purchasing.%'
			       OR (permission = 'catalog.view' AND $2 = 'purchase_manager'))`,
			roleID, roleKey)
		return err
	})
	if err != nil {
		t.Fatalf("seed pre-0032 %s clone: %v", roleKey, err)
	}
	return roleID
}

// permissionsOf reads a role's permissions in its own tenant's context, sorted
// so comparisons read cleanly in a failure message.
func permissionsOf(t *testing.T, pool *Pool, tenantID, roleID uuid.UUID) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT permission FROM role_permission WHERE role_id = $1`, roleID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read permissions of role %s: %v", roleID, err)
	}
	sort.Strings(out)
	return out
}

// templatePermissions reads what a fresh tenant's clone of roleKey would carry.
func templatePermissions(t *testing.T, pool *Pool, tenantID uuid.UUID, roleKey string) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT rp.permission FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = $1`, roleKey)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read %s template permissions: %v", roleKey, err)
	}
	sort.Strings(out)
	return out
}

func hasPermission(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}

// missingFrom returns the members of want that perms does not contain.
func missingFrom(perms, want []string) []string {
	var out []string
	for _, w := range want {
		if !hasPermission(perms, w) {
			out = append(out, w)
		}
	}
	return out
}

// The permissions 0032 and 0033 gave each role, restated here on purpose: if
// the migration and this list ever disagree, one of them is wrong and the test
// should say so rather than agree with whatever the migration happens to do.
var backfilledVerbs = map[string][]string{
	"owner": {
		"purchasing.view", "purchasing.manage_suppliers", "purchasing.create_order",
		"purchasing.issue_order", "purchasing.receive_goods", "purchasing.record_bill",
		"purchasing.approve_bill", "purchasing.pay_supplier",
	},
	"purchase_manager": {
		"purchasing.view", "purchasing.manage_suppliers", "purchasing.create_order",
		"purchasing.issue_order", "purchasing.receive_goods", "purchasing.record_bill",
		"catalog.view",
	},
	"store_manager": {"purchasing.view", "purchasing.receive_goods"},
	"accountant": {
		"purchasing.view", "purchasing.record_bill",
		"purchasing.approve_bill", "purchasing.pay_supplier",
	},
	"auditor": {"purchasing.view"},
}

// P25 — a tenant that predates 0032 gets the purchasing verbs.
//
// The assertion is not merely "it has purchasing.view" but the stronger one the
// migration exists to deliver: after the backfill, an old tenant's clone holds
// exactly what a tenant provisioned today would clone from the template. Any
// weaker check would pass while leaving an Owner one verb short of running the
// module.
func TestAnOldTenantReceivesThePurchasingVerbs(t *testing.T) {
	pool := testPool(t)
	tenantID, _ := seedTenant(t, pool, "Pre-0032 Trading")

	for _, roleKey := range []string{
		"owner", "purchase_manager", "store_manager", "accountant", "auditor",
	} {
		t.Run(roleKey, func(t *testing.T) {
			want := backfilledVerbs[roleKey]
			roleID := seedPre0032Clone(t, pool, tenantID, roleKey)

			// The fixture is only meaningful if it really is short of every
			// verb the backfill grants.
			if got := missingFrom(permissionsOf(t, pool, tenantID, roleID), want); len(got) != len(want) {
				t.Fatalf("fixture is not a pre-0032 clone: it is missing only %v of %v", got, want)
			}

			runBackfill(t, pool)

			after := permissionsOf(t, pool, tenantID, roleID)
			if got := missingFrom(after, want); len(got) > 0 {
				t.Fatalf("after the backfill %s still lacks %v", roleKey, got)
			}

			// The whole point: old and new tenants end up identical.
			fresh := templatePermissions(t, pool, tenantID, roleKey)
			if strings.Join(after, ",") != strings.Join(fresh, ",") {
				t.Fatalf("backfilled %s does not match what a new tenant clones.\n got: %v\nwant: %v",
					roleKey, after, fresh)
			}
		})
	}
}

// The backfill must not become a general reconciliation of clones against
// templates. A Cashier is the sharpest case: the role is deliberately kept away
// from anything with money or cost in it, and a migration that quietly widened
// it would defeat the separation 0032's own comment is built around.
//
// The Cashier also already holds catalog.view from 0005, so this doubles as a
// check that the catalogue half of the backfill is keyed on the role rather
// than applied to whatever clone the loop happens to find.
func TestTheBackfillGrantsNothingToAnUnrelatedRole(t *testing.T) {
	pool := testPool(t)
	tenantID, _ := seedTenant(t, pool, "Unrelated Role Co")

	for _, roleKey := range []string{"cashier", "hr_manager", "delivery_staff"} {
		t.Run(roleKey, func(t *testing.T) {
			roleID := seedPre0032Clone(t, pool, tenantID, roleKey)
			before := permissionsOf(t, pool, tenantID, roleID)

			runBackfill(t, pool)

			after := permissionsOf(t, pool, tenantID, roleID)
			if strings.Join(before, ",") != strings.Join(after, ",") {
				t.Fatalf("the backfill changed %s.\nbefore: %v\n after: %v",
					roleKey, before, after)
			}
			for _, p := range after {
				if strings.HasPrefix(p, "purchasing.") {
					t.Fatalf("%s was granted %s", roleKey, p)
				}
			}
		})
	}
}

// A custom role a tenant wrote themselves has no template behind it, so
// cloned_from is NULL and the join drops it. That is the intended behaviour and
// worth pinning: a shop's own role must not acquire the right to pay suppliers
// because its key happens to be one the platform also ships.
func TestACustomRoleWithNoTemplateIsUntouched(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, _ := seedTenant(t, pool, "Custom Role Co")

	var roleID uuid.UUID
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Deliberately keyed 'owner' with no cloned_from: the strongest form of
		// the mistake, since the key alone matches eight lines of the backfill.
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name)
			VALUES ($1, 'owner', 'Hand-written Owner') RETURNING id`,
			tenantID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO role_permission (role_id, permission) VALUES ($1, 'reports.view')`,
			roleID)
		return err
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}

	runBackfill(t, pool)

	if after := permissionsOf(t, pool, tenantID, roleID); strings.Join(after, ",") != "reports.view" {
		t.Fatalf("a role with no template was modified: %v", after)
	}
}

// Running the migration twice must leave the same rows. ON CONFLICT DO NOTHING
// carries this, but the guarantee is what lets the migration be re-run against
// a database whose state is unknown, so it is asserted rather than assumed.
func TestTheBackfillIsIdempotent(t *testing.T) {
	pool := testPool(t)
	tenantID, _ := seedTenant(t, pool, "Idempotent Co")
	roleID := seedPre0032Clone(t, pool, tenantID, "owner")

	runBackfill(t, pool)
	first := permissionsOf(t, pool, tenantID, roleID)

	runBackfill(t, pool)
	second := permissionsOf(t, pool, tenantID, roleID)

	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("a second run changed the result.\nfirst:  %v\nsecond: %v", first, second)
	}
}

// QA gate M8, applied to the migration itself.
//
// The loop sets one tenant per iteration and filters on it. That filter is not
// the only thing standing between tenant A and tenant B, and this proves it:
// with the platform flag on and tenant A's context set, the same INSERT with
// the tenant filter REMOVED still cannot reach tenant B, because migration 0006
// deliberately kept `role` off the platform plane and role_isolation therefore
// still applies.
//
// Written as the careless form on purpose. If a future migration drops the
// WHERE clause, isolation must hold on the policy rather than on the author
// having remembered.
func TestTheBackfillCannotReachAnotherTenant(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tenantA, _ := seedTenant(t, pool, "Alpha Backfill")
	tenantB, _ := seedTenant(t, pool, "Beta Backfill")

	roleA := seedPre0032Clone(t, pool, tenantA, "owner")
	roleB := seedPre0032Clone(t, pool, tenantB, "owner")

	// One transaction, platform flag on, tenant A selected — and no tenant
	// filter on the insert at all.
	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.tenant_id', $1, true)`, tenantA.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT r.id, 'purchasing.pay_supplier'
			FROM role r
			JOIN role template ON template.id = r.cloned_from
			WHERE template.key = 'owner'
			ON CONFLICT DO NOTHING`)
		return err
	})
	if err != nil {
		t.Fatalf("unfiltered insert as tenant A: %v", err)
	}

	if !hasPermission(permissionsOf(t, pool, tenantA, roleA), "purchasing.pay_supplier") {
		t.Fatal("tenant A did not receive its own grant; the fixture proves nothing")
	}
	if hasPermission(permissionsOf(t, pool, tenantB, roleB), "purchasing.pay_supplier") {
		t.Fatal("a backfill running in tenant A's context wrote to tenant B; " +
			"role_isolation is not holding and every per-tenant migration is unsafe")
	}

	// And the real migration, which does filter, fixes both.
	runBackfill(t, pool)
	for _, c := range []struct {
		name   string
		tenant uuid.UUID
		role   uuid.UUID
	}{{"A", tenantA, roleA}, {"B", tenantB, roleB}} {
		if got := missingFrom(permissionsOf(t, pool, c.tenant, c.role),
			backfilledVerbs["owner"]); len(got) > 0 {
			t.Fatalf("tenant %s still lacks %v after the migration", c.name, got)
		}
	}
}
