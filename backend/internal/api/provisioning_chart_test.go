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
		"sales_revenue", "cogs", "cost_variance",
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

// The list above is written by hand, and that is exactly how the chart and the
// posting rules came to disagree without anybody noticing.
//
// Account 5150 was mapped to `inventory_variance` while rules 11 and 11a asked
// for `cost_variance`, so every variance the engine tried to post failed on an
// unresolvable role in any company whose chart came from provisioning — which is
// every real one. The test that covered rule 11 created its own variance account
// and mapped the role by hand before selling, proving the rule and the engine
// while stepping over the wiring between them. Migration 0048 renamed the
// mapping.
//
// So this asks the rules instead of a person. Every role any seeded rule names
// must be mapped, and the only accepted answer to "why is this one missing" is
// that the module owning it does not exist yet.
func TestEveryRoleThePostingRulesNameIsInTheChart(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Roles belonging to modules that are not built. A rule for them is seeded
	// and never called, so an unmapped role cannot fail anything today — but it
	// will on the first day that module posts, which is why they are named here
	// rather than skipped silently.
	//
	// `expense` and `owner_capital` also read as chart design decisions nobody
	// has taken: which account a generic cash expense hits is the accountant's
	// call, and the chart offers `owners_equity` where equity.contribution asks
	// for `owner_capital`. Both are recorded in PROJECT-STATUS.
	deferred := map[string]string{
		"expense":       "cash expenses are not built",
		"owner_capital": "equity contributions are not built",
	}

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		// posting_rule is global rather than per tenant, so it carries the rules
		// other tests insert too. Those use a `test.` prefix and are not part of
		// the seeded C9.2 set.
		rows, e := tx.Query(ctx, `
			SELECT DISTINCT line->>'role'
			FROM posting_rule, jsonb_array_elements(lines) AS line
			WHERE line->>'role' IS NOT NULL
			  AND rule_key NOT LIKE 'test.%'
			ORDER BY 1`)
		if e != nil {
			return e
		}
		defer rows.Close()

		var roles []string
		for rows.Next() {
			var role string
			if e := rows.Scan(&role); e != nil {
				return e
			}
			roles = append(roles, role)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(roles) == 0 {
			t.Fatal("no posting rules are seeded, so this proves nothing")
		}

		for _, role := range roles {
			var code string
			e := tx.QueryRow(ctx, `
				SELECT a.code FROM account_role_map m
				JOIN account a ON a.id = m.account_id
				WHERE m.company_id = $1 AND m.role = $2`,
				f.companyID, role).Scan(&code)
			if e == nil {
				continue
			}
			if reason, ok := deferred[role]; ok {
				t.Logf("role %q is unmapped, accepted: %s", role, reason)
				continue
			}
			t.Errorf("a seeded posting rule names role %q and the chart does "+
				"not map it, so that rule cannot post in a real company: %v",
				role, e)
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
