//go:build integration

package api

import (
	"errors"
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
		"loyalty_liability", "loyalty_expense",
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
	// `owner_capital` is no longer among them. It was never a design decision
	// nobody had taken — design 12 §1 names account 3100 "Owner Capital" and
	// the chart had simply drifted to `owners_equity`, so 0053 renamed the
	// mapping and the rule now resolves.
	//
	// `expense` stays, and for a different reason than "the module is not
	// built". Design 02 rule 5 says "Dr Expense Account", meaning whichever
	// head the transaction is for, and design 12 §1 offers Rent, Utilities,
	// Salaries, Marketing and Bank Charges as separate heads with no generic
	// account among them. A fixed role cannot name the one a user picked, so
	// this is a rule that needs a `for_each` rather than a mapping that needs
	// an account — and choosing one account for every cash expense would be
	// inventing an accounting rule rather than recording one.
	// Empty, and kept rather than deleted.
	//
	// It held one entry: the `expense` role, which design 02 rule 5 named and
	// no chart could map, because the rule debits the expense HEAD a
	// transaction is for rather than a fixed account. 0071 built the head model
	// and rule 5 version 2 replaced that line with a for_each naming each
	// head's own account, so the role is gone from every version that can
	// resolve and there is nothing left to defer.
	//
	// The map stays as the place a future exception is written down WITH its
	// reason, which is what stopped this one being mistaken for an oversight
	// for as long as it stood.
	deferred := map[string]string{}

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		// posting_rule is global rather than per tenant, so it carries the rules
		// other tests insert too. Those use a `test.` prefix and are not part of
		// the seeded C9.2 set.
		// The roles of the versions that WOULD RESOLVE, at every date any rule
		// changes shape. See the longer note on the same query in
		// equitycontribution_test.go: a superseded version can never be
		// resolved and therefore can never post, so a role it names is not a
		// rule the chart has to answer for.
		rows, e := tx.Query(ctx, `
			WITH switchover AS (
				SELECT DISTINCT rule_key, effective_from AS on
				FROM posting_rule WHERE rule_key NOT LIKE 'test.%'
			),
			resolved AS (
				SELECT DISTINCT ON (s.rule_key, s.on) r.lines
				FROM switchover s
				JOIN posting_rule r ON r.rule_key = s.rule_key
				  AND r.effective_from <= s.on
				  AND (r.effective_to IS NULL OR r.effective_to > s.on)
				ORDER BY s.rule_key, s.on,
				         (r.country IS NOT NULL) DESC, r.version DESC
			)
			SELECT DISTINCT line->>'role'
			FROM resolved, jsonb_array_elements(lines) AS line
			WHERE line->>'role' IS NOT NULL
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

// No two roles may resolve to the same account.
//
// THE DEFECT THIS TEST WOULD HAVE CAUGHT.
//
// 0071 relabelled Stock Write-off from 5200 to 5400 so Rent could have the code
// design 12 §1 gives it — with a bare UPDATE, outside any tenant context, on a
// table with FORCE ROW LEVEL SECURITY. The predicate was false for every row,
// so it changed nothing and reported no error, because an UPDATE that matches
// nothing is not a failure. The insert that followed then found 5200 occupied,
// returned the EXISTING row, and mapped `expense_rent` to Stock Write-off.
//
// The result was two roles sharing one account. Every rent a shop recorded
// debited an account an accountant reads as damaged stock, and nothing anywhere
// said so: the entry balanced, the trial balance tied, and the expense screen
// showed a sensible-looking figure under a category called Rent.
//
// A shared account is what that failure looks like from the outside, whatever
// caused it — a seed that drifted, a migration that missed its scope, a mapping
// someone repointed by hand. The seeded chart maps one role to one account by
// construction, so there is nothing here to allow deliberately, and a future
// chart that genuinely wants to merge two roles should have to say so here.
func TestNoTwoAccountRolesShareAnAccount(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()
	companyID := provisionedCompany(t, h, f, "Role Collision Co")

	type clash struct {
		code, name string
		roles      []string
	}
	var clashes []clash

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT a.code, a.name, array_agg(m.role ORDER BY m.role)
			FROM account_role_map m
			JOIN account a ON a.id = m.account_id
			WHERE m.company_id = $1
			GROUP BY a.id, a.code, a.name
			HAVING count(*) > 1
			ORDER BY a.code`, companyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var c clash
			if e := rows.Scan(&c.code, &c.name, &c.roles); e != nil {
				return e
			}
			clashes = append(clashes, c)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("look for shared accounts: %v", err)
	}

	for _, c := range clashes {
		t.Errorf("account %s %q is mapped to %v. Two roles resolving to one "+
			"account means at least one of them posts somewhere nobody chose, "+
			"and every entry it writes will balance while landing in the wrong "+
			"place.", c.code, c.name, c.roles)
	}
}

// Every expense head a new company gets must post to an expense account of its
// own, and to the account its NAME implies.
//
// The second half is what makes this more than a repeat of the trigger's own
// check: a head pointing at any expense account passes the trigger, and a Rent
// head pointing at Stock Write-off is still wrong.
func TestSeededExpenseHeadsPostWhereTheirNamesSay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()
	companyID := provisionedCompany(t, h, f, "Expense Head Co")

	// The head code and the account NAME its seed intends. Written out rather
	// than derived from the seed, so a change to the seed has to be a change
	// here too — which is the point: the pairing is a decision, not a lookup.
	want := map[string]string{
		"RENT":      "Rent",
		"UTILITIES": "Utilities",
		"SALARIES":  "Salaries",
		"MARKETING": "Marketing",
		"BANKFEES":  "Bank & Card Charges",
	}

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		for code, accountName := range want {
			var got, kind string
			var postable bool
			e := tx.QueryRow(ctx, `
				SELECT a.name, a.type, a.is_postable
				FROM expense_head h
				JOIN account a ON a.id = h.account_id
				WHERE h.company_id = $1 AND h.code = $2`,
				companyID, code).Scan(&got, &kind, &postable)
			if errors.Is(e, pgx.ErrNoRows) {
				t.Errorf("a provisioned company has no %q expense head, so a "+
					"shop cannot record that cost on the day it installs the "+
					"product", code)
				continue
			}
			if e != nil {
				return e
			}
			if got != accountName {
				t.Errorf("the %q head posts to %q, and it should post to %q. An "+
					"entry to the wrong expense account balances perfectly and "+
					"is read by an accountant as something else entirely.",
					code, got, accountName)
			}
			if kind != "expense" {
				t.Errorf("the %q head posts to a %s account", code, kind)
			}
			if !postable {
				t.Errorf("the %q head posts to a heading rather than an account "+
					"entries can be written to", code)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("check the seeded heads: %v", err)
	}
}
