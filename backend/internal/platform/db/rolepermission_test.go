//go:build integration

package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Migration 0003 protected `role` but left `role_permission` with no
// row-level security at all, so a plain SELECT returned every tenant's custom
// role configuration. Found while writing the API access tests.
//
// This is the regression guard. The leak is not customer data, but it is still
// one tenant learning about another through a manipulated query, which QA gate
// M8 forbids outright.
func TestRolePermissionIsTenantIsolated(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tenantA, _ := seedTenant(t, pool, "Alpha")
	tenantB, _ := seedTenant(t, pool, "Beta")

	// Tenant B defines a custom role with a distinctive permission.
	const secretPermission = "wholesale.negotiate_dealer_price"
	var roleB uuid.UUID
	if err := pool.Tx(ctxAs(tenantB), func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name)
			VALUES ($1, 'beta_secret_role', 'Beta Secret Role')
			RETURNING id`, tenantB).Scan(&roleB); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO role_permission (role_id, permission) VALUES ($1, $2)`,
			roleB, secretPermission)
		return err
	}); err != nil {
		t.Fatalf("seed tenant B role: %v", err)
	}

	// Tenant A runs the unfiltered query — the realistic probe.
	var visible int
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM role_permission WHERE permission = $1`,
			secretPermission).Scan(&visible)
	}); err != nil {
		t.Fatalf("query as tenant A: %v", err)
	}
	if visible != 0 {
		t.Fatal("tenant A can read tenant B's role permissions")
	}

	// Naming the role id directly must also reveal nothing.
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM role_permission WHERE role_id = $1`, roleB).
			Scan(&visible)
	}); err != nil {
		t.Fatalf("query by id as tenant A: %v", err)
	}
	if visible != 0 {
		t.Fatal("tenant A can read tenant B's role permissions by role id")
	}

	// Tenant B still sees its own, or the policy is too strict and the
	// authorizer would resolve nobody's permissions.
	if err := pool.Tx(ctxAs(tenantB), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM role_permission WHERE role_id = $1`, roleB).
			Scan(&visible)
	}); err != nil {
		t.Fatalf("query as tenant B: %v", err)
	}
	if visible != 1 {
		t.Fatalf("tenant B sees %d of its own role permissions, want 1", visible)
	}
}

// The twelve platform role templates must stay readable by every tenant, since
// the role builder offers them as a starting point. A policy that isolated them
// along with tenant roles would break role creation entirely.
func TestPlatformRoleTemplatesRemainReadable(t *testing.T) {
	pool := testPool(t)
	tenantA, _ := seedTenant(t, pool, "Alpha")

	var templates, permissions int
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM role WHERE tenant_id IS NULL`).Scan(&templates); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `
			SELECT count(*) FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL`).Scan(&permissions)
	}); err != nil {
		t.Fatalf("read templates as a tenant: %v", err)
	}

	if templates != 12 {
		t.Fatalf("visible role templates = %d, want the 12 from blueprint A6.1", templates)
	}
	if permissions == 0 {
		t.Fatal("role templates are visible but carry no permissions; " +
			"cloning one would produce an empty role")
	}
}
