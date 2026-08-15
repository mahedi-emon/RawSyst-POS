//go:build integration

// Posting rules as data.
//
// C9.2 requires each transaction type to have its own defined, CONFIGURABLE
// posting rule. These tests hold the engine to the properties that makes it
// worth having: that a rule is resolved at the TRANSACTION date so history
// stays explainable, that the version which produced an entry is recorded, and
// that a rule asking for a figure nobody supplied fails loudly rather than
// posting a silently wrong entry.
package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func amount(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// saleTransaction is the shape a till hands the engine.
func saleTransaction(total, net, vat string) Transaction {
	return Transaction{
		Amounts: Amounts{
			"subtotal_net":    amount(net),
			"tax_total":       amount(vat),
			"total_inclusive": amount(total),
		},
		Groups: map[string]Group{
			"tenders": {{Role: "cash", Amount: amount(total), Memo: "cash"}},
		},
	}
}

func (b *books) postByRule(t *testing.T, e Entry, txn Transaction) (Result, error) {
	t.Helper()
	var res Result
	err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		var e2 error
		res, e2 = PostByRule(context.Background(), tx, e, "sa", txn)
		return e2
	})
	return res, err
}

func (b *books) ruleEntry(key string) Entry {
	return Entry{
		TenantID: b.tenantID, CompanyID: b.companyID, Date: aug(15),
		SourceType: "sale", SourceID: uuid.New(), RuleKey: key,
		Currency: "SAR", BaseCurrency: "SAR",
	}
}

// The seeded rule produces the entry the hard-coded version used to.
func TestASalePostsThroughItsRule(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	got, err := b.postByRule(t, b.ruleEntry("sale.revenue"),
		saleTransaction("115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("PostByRule: %v", err)
	}

	var debits, credits decimal.Decimal
	var lines int
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(base_debit),0), coalesce(sum(base_credit),0), count(*)
			FROM journal_line WHERE entry_id = $1`, got.EntryID).
			Scan(&debits, &credits, &lines)
	}); err != nil {
		t.Fatalf("read entry: %v", err)
	}

	if !debits.Equal(amount("115")) || !debits.Equal(credits) {
		t.Errorf("entry is %s debit against %s credit, want 115 both", debits, credits)
	}
	if lines != 3 {
		t.Errorf("%d lines written, want 3 (cash, revenue, VAT)", lines)
	}
}

// The version that produced an entry is recorded. Rules are versioned and never
// edited, so an entry posted last March must stay explainable by the rule that
// actually made it — today's rule may not be the one that ran.
func TestTheRuleVersionIsRecordedOnTheEntry(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	got, err := b.postByRule(t, b.ruleEntry("sale.revenue"),
		saleTransaction("115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("PostByRule: %v", err)
	}

	var key string
	var version *int
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT rule_key, rule_version FROM journal_entry WHERE id = $1`,
			got.EntryID).Scan(&key, &version)
	}); err != nil {
		t.Fatalf("read entry: %v", err)
	}

	if key != "sale.revenue" {
		t.Errorf("rule key = %s", key)
	}
	if version == nil {
		t.Fatal("no rule version recorded; the entry cannot be explained by the " +
			"rule that made it once the rule is superseded")
	}
	if *version != 1 {
		t.Errorf("rule version = %d, want 1", *version)
	}
}

// A rule wanting a figure nobody supplied must FAIL, naming the figure.
//
// This is the property worth most. A missing amount silently becoming zero
// would post an entry that is wrong rather than one that fails, and the
// downstream complaint would be "debits do not equal credits" — which says
// nothing about the actual mistake.
func TestAMissingAmountFailsAndNamesItself(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	incomplete := saleTransaction("115.00", "100.00", "15.00")
	delete(incomplete.Amounts, "tax_total")

	_, err := b.postByRule(t, b.ruleEntry("sale.revenue"), incomplete)
	if err == nil {
		t.Fatal("an entry posted with a figure the rule asked for and nobody gave")
	}
	if !strings.Contains(err.Error(), "tax_total") {
		t.Errorf("the refusal does not name the missing figure: %v", err)
	}
}

// The same, for a repeating group.
func TestAMissingGroupFailsAndNamesItself(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	incomplete := saleTransaction("115.00", "100.00", "15.00")
	incomplete.Groups = map[string]Group{}

	_, err := b.postByRule(t, b.ruleEntry("sale.revenue"), incomplete)
	if err == nil {
		t.Fatal("a sale posted with no tenders at all")
	}
	if !strings.Contains(err.Error(), "tenders") {
		t.Errorf("the refusal does not name the missing group: %v", err)
	}
}

// A rule that does not exist on the date is refused, in words that say what is
// missing rather than surfacing an empty result set.
func TestAnUnknownRuleIsRefused(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	_, err := b.postByRule(t, b.ruleEntry("sale.imaginary"),
		saleTransaction("115.00", "100.00", "15.00"))
	if err == nil {
		t.Fatal("an entry posted under a rule that does not exist")
	}
	if !strings.Contains(err.Error(), "sale.imaginary") {
		t.Errorf("the refusal does not name the rule: %v", err)
	}
}

// A rule is resolved at the TRANSACTION date, not by taking the newest version.
//
// This is what keeps history explainable. An offline sale that syncs a week
// after a rule changed must post the way it would have when it was rung up, or
// two identical sales minutes apart land in different accounts because of when
// the network came back.
//
// The rule is given a throwaway key rather than adding a version to a seeded
// one. posting_rule is platform-global and immutable by design — there is no
// delete — so a test that edited a real rule would leave it edited for every
// later run and for every other test.
func TestARuleIsResolvedAtTheTransactionDate(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	// The schema requires the part after the dot to START with a letter, and a
	// bare hex string begins with a digit about a third of the time — which is
	// a flaky test rather than a rare one.
	key := "test.v" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]

	if err := b.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO posting_rule
			  (rule_key, country, version, lines, description, effective_from)
			VALUES
			  ($1, NULL, 1,
			   '[{"role": "early_role", "side": "debit",  "amount": "amount"},
			     {"role": "cash",       "side": "credit", "amount": "amount"}]'::jsonb,
			   'in force from August', '2026-08-01'),
			  ($1, NULL, 2,
			   '[{"role": "later_role", "side": "debit",  "amount": "amount"},
			     {"role": "cash",       "side": "credit", "amount": "amount"}]'::jsonb,
			   'in force from September', '2026-09-01')`, key)
		return e
	}); err != nil {
		t.Fatalf("seed rule versions: %v", err)
	}

	for _, tc := range []struct {
		name     string
		on       time.Time
		wantRole string
		wantVer  int
	}{
		{"August uses the August rule", aug(15), "early_role", 1},
		{"September uses the September rule", aug(15).AddDate(0, 1, 0), "later_role", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rule Rule
			if err := b.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
				var e error
				rule, e = ResolveRule(ctx, tx, key, "sa", tc.on)
				return e
			}); err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if rule.Version != tc.wantVer {
				t.Errorf("version = %d, want %d", rule.Version, tc.wantVer)
			}
			if rule.Lines[0].Role != tc.wantRole {
				t.Errorf("first line posts to %q, want %q",
					rule.Lines[0].Role, tc.wantRole)
			}
		})
	}
}

// Rules are data, so a tenant can be given a different chart of accounts
// without a code change — which is the whole point of C9.2. Proved by mapping
// the same role to a different account and watching the entry follow.
func TestChangingTheRoleMapMovesTheEntryWithoutACodeChange(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)
	ctx := context.Background()

	var alternative uuid.UUID
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO account (tenant_id, company_id, code, name, type)
			VALUES ($1,$2,'4200','Online Sales','revenue') RETURNING id`,
			b.tenantID, b.companyID).Scan(&alternative); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			UPDATE account_role_map SET account_id = $3
			WHERE company_id = $1 AND role = $2`,
			b.companyID, "sales_revenue", alternative)
		return e
	}); err != nil {
		t.Fatalf("remap the role: %v", err)
	}

	got, err := b.postByRule(t, b.ruleEntry("sale.revenue"),
		saleTransaction("115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("PostByRule: %v", err)
	}

	var landed uuid.UUID
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT account_id FROM journal_line
			WHERE entry_id = $1 AND base_credit = 100`, got.EntryID).Scan(&landed)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}
	if landed != alternative {
		t.Error("revenue did not follow the role mapping; the rule is still " +
			"bound to a specific account")
	}
}

// Every seeded rule must be readable and produce at least two lines. A rule
// that cannot balance is not a rule.
func TestEverySeededRuleIsWellFormed(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	err := b.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT DISTINCT rule_key FROM posting_rule ORDER BY rule_key`)
		if e != nil {
			return e
		}
		defer rows.Close()

		var keys []string
		for rows.Next() {
			var k string
			if e := rows.Scan(&k); e != nil {
				return e
			}
			keys = append(keys, k)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(keys) < 12 {
			t.Errorf("%d posting rules seeded, want at least the 12 C9.2 names", len(keys))
		}

		for _, k := range keys {
			rule, e := ResolveRule(ctx, tx, k, "sa", aug(15))
			if e != nil {
				t.Errorf("rule %q does not resolve: %v", k, e)
				continue
			}
			if len(rule.Lines) < 2 {
				t.Errorf("rule %q has %d lines; it cannot balance", k, len(rule.Lines))
			}
			var debits, credits int
			for _, l := range rule.Lines {
				switch Side(l.Side) {
				case Debit:
					debits++
				case Credit:
					credits++
				default:
					t.Errorf("rule %q has a line with side %q", k, l.Side)
				}
			}
			if debits == 0 || credits == 0 {
				t.Errorf("rule %q has %d debit and %d credit lines; every entry "+
					"needs both", k, debits, credits)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
}
