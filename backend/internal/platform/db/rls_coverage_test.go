//go:build integration

// Every tenant-scoped table is actually isolated.
//
// The other tests in this package prove isolation by doing it: they write as
// one tenant, read as another, and check nothing comes back. That is the right
// way to test the mechanism, and it tests the mechanism on the tables those
// tests happen to touch.
//
// What it cannot catch is the next table. A migration that adds `tenant_id` and
// forgets `ENABLE ROW LEVEL SECURITY` — or enables it and forgets `FORCE`, or
// forces it and writes no policy — produces a table that reads across every
// business on the platform, and every existing test still passes because none
// of them knows the table exists.
//
// So this asks the database directly, about every table, and needs no
// maintenance when a module is added.
package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// tenantTable is what the catalogue says about one table's isolation.
type tenantTable struct {
	name     string
	enabled  bool
	forced   bool
	policies int
}

// notTenantScoped are the tables that carry a `tenant_id` column and are
// deliberately NOT isolated by it.
//
// Each one has to earn its place. A table here is a table any tenant's
// connection can read in full, so the reason must be that reading it in full is
// harmless or is the point.
var notTenantScoped = map[string]string{
	// 0027 says so in the table's own comment, and the code bears it out:
	// every access in internal/jobs goes through TxAsPlatform, including
	// EnqueueIn, so no tenant connection reads or writes this table at all.
	// The worker drains every tenant's queue, and a job row carries ids and a
	// kind — an invoice id, a device id — never business content.
	"job": "the worker drains every tenant's queue; every access is " +
		"TxAsPlatform and a row carries only ids and a kind",
}

func TestEveryTenantScopedTableIsIsolated(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var tables []tenantTable
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
			       (SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND c.relkind = 'r'
			  AND EXISTS (
			    SELECT 1 FROM pg_attribute a
			    WHERE a.attrelid = c.oid
			      AND a.attname = 'tenant_id'
			      AND NOT a.attisdropped)
			ORDER BY c.relname`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var tt tenantTable
			if e := rows.Scan(&tt.name, &tt.enabled, &tt.forced,
				&tt.policies); e != nil {
				return e
			}
			tables = append(tables, tt)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read the table catalogue: %v", err)
	}

	if len(tables) < 20 {
		t.Fatalf("only %d tenant-scoped tables were found; the catalogue "+
			"query is wrong and this test is proving nothing", len(tables))
	}
	t.Logf("%d tables carry a tenant_id", len(tables))

	for _, tt := range tables {
		if why, ok := notTenantScoped[tt.name]; ok {
			t.Logf("%-34s not isolated: %s", tt.name, why)
			continue
		}
		if !tt.enabled {
			t.Errorf("%s carries a tenant_id and row level security is OFF, "+
				"so every business on the platform can read it", tt.name)
			continue
		}
		// Without FORCE, the role that OWNS the table bypasses every policy on
		// it. The application role is deliberately NOBYPASSRLS, but the owner
		// is what migrations and any future maintenance connection run as.
		if !tt.forced {
			t.Errorf("%s has row level security enabled but not FORCED, so "+
				"the owning role reads every tenant's rows", tt.name)
		}
		if tt.policies == 0 {
			// RLS with no policy denies everything, which fails closed rather
			// than open — but it means the table is unusable, which is a bug
			// of its own and one that only shows up when somebody uses it.
			t.Errorf("%s has row level security and no policy at all, so no "+
				"tenant can read or write it", tt.name)
		}
	}
}

// The application role cannot turn isolation off.
//
// FORCE protects against the owner; this is the other half — the role the API
// actually connects as must not be able to disable a policy, drop one, or set
// itself superuser. Without it every guarantee above is advisory.
func TestTheApplicationRoleCannotDisableIsolation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var super, bypass bool
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT rolsuper, rolbypassrls
			FROM pg_roles WHERE rolname = current_user`).
			Scan(&super, &bypass)
	}); err != nil {
		t.Fatalf("read the current role: %v", err)
	}
	if super {
		t.Error("the application connects as a superuser, which ignores every " +
			"row level security policy on every table")
	}
	if bypass {
		t.Error("the application role holds BYPASSRLS, so tenant isolation " +
			"is not enforced for it at all")
	}
}
