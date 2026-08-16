//go:build integration

package api

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// A newly created company must be able to trade on its first day.
//
// This is the gap the exchange work exposed. Posting rules name account ROLES,
// resolved per company through account_role_map, and nothing was creating those
// mappings — tests seeded them by hand and real deployments were expected to.
// The first sale in a newly onboarded company would have failed on a missing
// role. The failure was at least loud and named the role, but loud is not the
// same as working.
func TestANewCompanyGetsAChartItCanPostTo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Every role the implemented modules post to. If one of these is missing,
	// the module that needs it fails at the first transaction.
	required := []string{
		"cash", "bank", "card_clearing", "accounts_receivable", "inventory",
		"output_vat", "store_credit_liability", "exchange_clearing",
		"sales_revenue", "cogs", "inventory_variance",
	}

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		for _, role := range required {
			var code string
			e := tx.QueryRow(ctx, `
				SELECT a.code FROM account_role_map m
				JOIN account a ON a.id = m.account_id
				WHERE m.company_id = $1 AND m.role = $2`,
				f.companyID, role).Scan(&code)
			if e != nil {
				t.Errorf("role %q is not mapped, so posting to it fails: %v", role, e)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("read roles: %v", err)
	}
}

// Seeding twice must not produce a second Cash account, and must never repoint
// an existing mapping — that would silently split a balance across two accounts
// mid-year, and the trial balance would still balance while both were wrong.
func TestSeedingTheChartTwiceChangesNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	seed := func() {
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			return provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, f.companyID)
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	count := func() (accounts, roles int) {
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			if e := tx.QueryRow(ctx,
				`SELECT count(*) FROM account WHERE company_id = $1`,
				f.companyID).Scan(&accounts); e != nil {
				return e
			}
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM account_role_map WHERE company_id = $1`,
				f.companyID).Scan(&roles)
		}); err != nil {
			t.Fatalf("count: %v", err)
		}
		return accounts, roles
	}

	seed()
	firstAccounts, firstRoles := count()
	seed()
	secondAccounts, secondRoles := count()

	if firstAccounts != secondAccounts {
		t.Errorf("accounts went from %d to %d on a repeated seed",
			firstAccounts, secondAccounts)
	}
	if firstRoles != secondRoles {
		t.Errorf("role mappings went from %d to %d on a repeated seed",
			firstRoles, secondRoles)
	}
}

// The seeded chart must actually work, not merely exist. A sale exercises
// cash, revenue, output VAT, COGS and inventory in one transaction.
func TestASaleWorksOnTheSeededChart(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("a sale failed on a freshly seeded chart: %s", readBody(t, created))
	}
}
