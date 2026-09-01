//go:build integration

// P32 — the dormant rule that named an account the chart did not have.
//
// Rule 12 (0025) credits the role `owner_capital`. The seeded chart offered
// `owners_equity`, so in every company created through the product the engine
// could not resolve the role and a capital contribution would have failed on
// the first day the module owning it posted. Dormant is not the same as
// harmless: rule 11 was dormant in exactly this way for eight migrations, and
// the test covering it mapped the role by hand and so never saw it.
//
// These deliberately go through `provisioning.SeedChartOfAccounts` — the chart
// every real company gets — rather than the api fixture's hand-built one, which
// is the blind spot that hid the cost_variance mapping until 0048.
package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// provisionedCompany makes a company with the chart provisioning gives it.
func provisionedCompany(t *testing.T, h *harness, f *shopFixture, name string) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	var companyID uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,$2,'sa','SAR') RETURNING id`,
			f.tenantID, name).Scan(&companyID); e != nil {
			return e
		}
		// An open period, or nothing can post: closed-period protection refuses
		// a date no period covers, which is the correct refusal and not what
		// these tests are about.
		// Both the month the fixtures name and the month containing today.
		// See the note in pos_test.go: they used to be the same month.
		if _, e := tx.Exec(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,8,'2026-08-01','2026-08-31')
			ON CONFLICT DO NOTHING`,
			f.tenantID, companyID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			SELECT $1, $2,
			       extract(year  FROM current_date)::int,
			       extract(month FROM current_date)::int,
			       date_trunc('month', current_date)::date,
			       (date_trunc('month', current_date)
			         + interval '1 month - 1 day')::date
			ON CONFLICT DO NOTHING`,
			f.tenantID, companyID); e != nil {
			return e
		}
		return provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, companyID)
	}); err != nil {
		t.Fatalf("provision %s: %v", name, err)
	}
	return companyID
}

// The chart maps owner_capital, to the account design 12 §1 names.
func TestProvisioningMapsOwnerCapital(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	companyID := provisionedCompany(t, h, f, "Owner Capital Co")

	var code, name, kind string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT a.code, a.name, a.type FROM account_role_map m
			JOIN account a ON a.id = m.account_id
			WHERE m.company_id = $1 AND m.role = 'owner_capital'`,
			companyID).Scan(&code, &name, &kind)
	}); err != nil {
		t.Fatalf("a provisioned company has no owner_capital account: %v", err)
	}

	if code != "3100" {
		t.Errorf("owner_capital maps to %s, want 3100 as design 12 §1 lists it", code)
	}
	if name != "Owner Capital" {
		t.Errorf("account 3100 is called %q, want %q", name, "Owner Capital")
	}
	if kind != "equity" {
		t.Errorf("3100 is a %s account, want equity", kind)
	}

	// The old spelling is gone rather than sitting alongside the new one. Two
	// equity roles on one account would let a later caller pick either and
	// split the balance without the trial balance ever noticing.
	var stale int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM account_role_map WHERE role = 'owners_equity'`).
			Scan(&stale)
	}); err != nil {
		t.Fatalf("look for the old role: %v", err)
	}
	if stale != 0 {
		t.Errorf("%d companies still map the old owners_equity role", stale)
	}
}

// Rule 12 posts through a real chart.
//
// The point of P32: not that the mapping row exists, but that the engine can
// resolve the rule end to end and produce a balanced entry. A mapping that
// existed and a rule that still failed would satisfy the previous test alone.
func TestACapitalContributionPostsThroughARealChart(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()
	companyID := provisionedCompany(t, h, f, "Capital Injection Co")

	sourceID := uuid.New()
	var lines []postedLine

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		// The shape rule 12 expects: the destination the money landed in is a
		// group, because capital can arrive as cash or straight into a bank,
		// and each member names its own ROLE for the same reason a split
		// tender does — the engine resolves the account, the caller does not.
		if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: f.tenantID, CompanyID: companyID,
			Date:       day(15),
			SourceType: "capital_contribution", SourceID: sourceID,
			RuleKey: "equity.contribution", PostedBy: &f.userID,
			Memo: "Owner put 50,000 into the business",
		}, "sa", accounting.Transaction{
			Amounts: map[string]decimal.Decimal{
				"amount": decimal.RequireFromString("50000.00"),
			},
			Groups: map[string]accounting.Group{
				"destination": {{Role: "cash",
					Amount: decimal.RequireFromString("50000.00")}},
			},
		}); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT a.code, coalesce(m.role, ''), l.debit, l.credit, l.store_id
			FROM journal_line l
			JOIN journal_entry en ON en.id = l.entry_id
			JOIN account a ON a.id = l.account_id
			LEFT JOIN account_role_map m ON m.account_id = a.id
			WHERE en.source_type = 'capital_contribution' AND en.source_id = $1
			ORDER BY l.line_no`, sourceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p postedLine
			if e := rows.Scan(&p.code, &p.role, &p.debit, &p.credit, &p.storeID); e != nil {
				return e
			}
			lines = append(lines, p)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("post a capital contribution: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("posted %d lines, want 2: %+v", len(lines), lines)
	}

	// Blueprint C3.2: capital in is never revenue. Cash rises and equity rises
	// with it; nothing touches the P&L.
	assertLine(t, lines, "1100", "cash", "50000", "0")
	assertLine(t, lines, "3100", "owner_capital", "0", "50000")

	debits, credits := decimal.Zero, decimal.Zero
	for _, l := range lines {
		debits = debits.Add(l.debit)
		credits = credits.Add(l.credit)
	}
	if !debits.Equal(credits) {
		t.Fatalf("the contribution does not balance: %s against %s", debits, credits)
	}
}

// Every seeded rule that names a fixed role resolves, except the one that
// cannot.
//
// The companion to TestEveryRoleThePostingRulesNameIsInTheChart, which checks
// the mapping rows exist. This checks the engine agrees, by asking it to
// resolve each rule against a provisioned chart — a mapping pointing at a
// heading rather than a postable account would pass the first and fail here.
func TestEverySeededRuleResolvesAgainstAProvisionedChart(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()
	companyID := provisionedCompany(t, h, f, "Rule Resolution Co")

	// Nothing is expected to fail any more.
	//
	// `expense.cash` was pinned here as a KNOWN failure, with the reason: design
	// 02 rule 5 debits the expense HEAD the transaction is for, a fixed role
	// cannot name it, and the rule therefore needed a for_each over a model that
	// did not exist. The comment said "the day somebody fixes the rule, this
	// test tells them to update the expectation rather than silently passing",
	// and that is what happened — 0071 built the expense-head model and gave the
	// rule its second version, whose debit side is a for_each naming each head's
	// own account.
	//
	// The map is kept rather than deleted. It is how the next rule that cannot
	// resolve gets recorded as a decision instead of a skip.
	cannotResolve := map[string]bool{}

	var keys []string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT DISTINCT rule_key FROM posting_rule
			WHERE rule_key NOT LIKE 'test.%' ORDER BY 1`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if e := rows.Scan(&k); e != nil {
				return e
			}
			keys = append(keys, k)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("no posting rules are seeded, so this proves nothing")
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			// Resolution only: every role the rule names must map to a postable
			// account in this company. Building the lines needs figures each
			// rule defines for itself, which is each caller's own test.
			var unmapped []string
			if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
				// The roles of the version that WOULD RESOLVE, at every date the
				// rule changes shape — not the roles of every row ever stored.
				//
				// ResolveRule takes the highest version in force at the
				// transaction date. A version that a later one supersedes can
				// never be resolved, so it can never post, so a role it names is
				// not a defect in anything. Reading every row instead reported
				// exactly that as a failure, which is a test finding fault with
				// a rule the product will not use.
				//
				// Checking at each of the rule's own effective_from dates covers
				// today and every future switchover, which is more than reading
				// the rows ever did: it asks the question the posting engine
				// asks, on the days the answer changes.
				rows, e := tx.Query(ctx, `
					WITH switchover AS (
						SELECT DISTINCT effective_from AS on
						FROM posting_rule WHERE rule_key = $1
					),
					resolved AS (
						SELECT DISTINCT ON (s.on) r.lines
						FROM switchover s
						JOIN posting_rule r ON r.rule_key = $1
						  AND r.effective_from <= s.on
						  AND (r.effective_to IS NULL OR r.effective_to > s.on)
						ORDER BY s.on, (r.country IS NOT NULL) DESC, r.version DESC
					)
					SELECT DISTINCT line->>'role'
					FROM resolved, jsonb_array_elements(lines) AS line
					WHERE line->>'role' IS NOT NULL`, key)
				if e != nil {
					return e
				}
				defer rows.Close()

				var roles []string
				for rows.Next() {
					var r string
					if e := rows.Scan(&r); e != nil {
						return e
					}
					roles = append(roles, r)
				}
				if e := rows.Err(); e != nil {
					return e
				}

				for _, role := range roles {
					var postable bool
					e := tx.QueryRow(ctx, `
						SELECT NOT a.is_control OR a.control_of IS NOT NULL
						FROM account_role_map m JOIN account a ON a.id = m.account_id
						WHERE m.company_id = $1 AND m.role = $2`,
						companyID, role).Scan(&postable)
					if e != nil {
						unmapped = append(unmapped, role)
					}
				}
				return nil
			}); err != nil {
				t.Fatalf("resolve %s: %v", key, err)
			}

			if cannotResolve[key] {
				if len(unmapped) == 0 {
					t.Fatalf("%s resolves now; the rule was fixed, so remove it "+
						"from cannotResolve and say what it debits", key)
				}
				t.Logf("%s cannot resolve %v, as expected", key, unmapped)
				return
			}
			if len(unmapped) > 0 {
				t.Errorf("rule %s names %v, which a provisioned company does not "+
					"map, so the rule cannot post in any real company", key, unmapped)
			}
		})
	}
}
