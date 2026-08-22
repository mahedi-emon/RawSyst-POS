//go:build integration

package api

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// C13's provisional cost, corrected end to end over HTTP.
//
// The unit and store-level proofs live in the inventory package. What these add
// is the wiring: a real till selling through /api/v1/pos/sales, a real delivery
// through /api/v1/purchasing/receipts, and the chart of accounts that
// provisioning actually seeds rather than one a test assembled to suit itself.
//
// That last part is the whole reason this file exists. The variance rules ask
// for the `cost_variance` role and the seeded chart mapped account 5150 to
// `inventory_variance`, so every variance the engine tried to post failed on an
// unresolvable role in any real company. The test covering the rule created its
// own account and mapped the role by hand, which proved the rule and the engine
// and stepped over the join between them. Migration 0048 renamed the mapping;
// these tests go through the seeded one so the two cannot drift apart again.
//
// The shop starts with ten units at 60 — seedShop's opening stock, ledger side
// included — so a sale of twelve leaves two uncovered at a provisional 60 each.

// allowSellingBelowZero switches the company to the policy C13 permits.
//
// Through the platform connection, because negative_stock_policy is a company
// setting a till may not name: a terminal that could choose its own policy
// could sell past empty on a company that had forbidden it.
func allowSellingBelowZero(t *testing.T, h *harness, f *buyingFixture) {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE company SET negative_stock_policy = 'allow_warn' WHERE id = $1`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("set the negative stock policy: %v", err)
	}
}

// varianceBalance reads the account the seeded chart maps `cost_variance` to,
// through the role rather than the account code. An owner is free to renumber
// their chart; the role is what the posting engine resolves, so it is what this
// has to follow.
func varianceBalance(t *testing.T, h *harness, f *buyingFixture) decimal.Decimal {
	t.Helper()
	return roleBalance(t, h, f.shopFixture, "cost_variance")
}

// sellShort rings up qty over HTTP, which with ten units on hand goes below zero.
func sellShort(t *testing.T, h *harness, f *buyingFixture, qty, paid string) {
	t.Helper()
	sale := oneItemSale(f.shopFixture, newUUID(), qty, "115.00", paid)
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != 201 {
		t.Fatalf("sale below zero: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	// The till is told at the counter, which is the warning half of allow_warn.
	if short, _ := body["stock_shortfalls"].([]any); len(short) == 0 {
		t.Fatalf("a sale of %s from ten units reported no shortfall", qty)
	}
}

// deliver receives qty at unitCost against a fresh order and returns the body.
func deliver(t *testing.T, h *harness, f *buyingFixture, qty, unitCost string) map[string]any {
	t.Helper()
	poID, lineID := raiseOrder(t, h, f, qty, unitCost)
	resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": qty}},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// A till sells past empty; the delivery that follows recognises what those units
// really cost.
//
// Two units were charged at 60 on the strength of the pool average and arrived
// at 100, so 80 of cost of goods sold had gone unrecognised and the margin
// already reported on that sale was too generous by the same amount.
func TestADeliveryCorrectsTheCostOfAnEarlierSaleBelowZero(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	allowSellingBelowZero(t, h, f)

	sellShort(t, h, f, "12", "1380.00")

	// Nothing is corrected until the goods arrive.
	if got := varianceBalance(t, h, f); !got.IsZero() {
		t.Fatalf("the variance account holds %s before any delivery", got)
	}
	if diff := tieOut(t, h, f); !diff.IsZero() {
		t.Fatalf("after selling two more than it had, the valuation is out by "+
			"%s; the balance sheet and the stock report disagree", diff)
	}

	body := deliver(t, h, f, "2", "100.00")

	if got, _ := body["cost_correction"].(string); got != "80.00" {
		t.Errorf("the receipt reports a cost correction of %q, want \"80.00\"", got)
	}
	if got, _ := body["units_recosted"].(string); got != "2" {
		t.Errorf("the receipt reports %q units re-costed, want \"2\"", got)
	}

	// The money, on the account the seeded chart maps the role to. Before 0048
	// this failed with an unresolvable role and took the delivery down with it.
	if got := varianceBalance(t, h, f); !got.Equal(decimal.RequireFromString("80")) {
		t.Errorf("the variance account holds %s, want 80 — two units charged at "+
			"60 that cost 100", got)
	}
	if diff := tieOut(t, h, f); !diff.IsZero() {
		t.Fatalf("after the correction the valuation is out by %s", diff)
	}
}

// Goods that turn out CHEAPER than the till guessed correct the other way, as a
// credit to the variance account rather than a negative debit.
//
// The direction is chosen in postCostCorrection from the sign, which is why it
// is worth proving here and not only in the costing package: a single rule
// taking a signed amount would write a negative debit where a credit belongs,
// and a trial balance carrying negative debits is one an accountant cannot read.
func TestAFavourableCorrectionIsPostedAsACredit(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	allowSellingBelowZero(t, h, f)

	sellShort(t, h, f, "12", "1380.00")
	body := deliver(t, h, f, "2", "30.00")

	if got, _ := body["cost_correction"].(string); got != "-60.00" {
		t.Errorf("the receipt reports a correction of %q, want \"-60.00\"", got)
	}
	if got := varianceBalance(t, h, f); !got.Equal(decimal.RequireFromString("-60")) {
		t.Errorf("the variance account holds %s, want −60 — two units charged "+
			"at 60 that cost 30", got)
	}
	if diff := tieOut(t, h, f); !diff.IsZero() {
		t.Fatalf("after a favourable correction the valuation is out by %s", diff)
	}
}

// The correction is its own entry, attributed to the rule that produced it and
// kept apart from the accrual for the same delivery.
//
// Two different facts — what this delivery cost, and what an earlier sale was
// mis-costed by — and an auditor asking why cost of goods sold moved has to be
// able to see the second on its own.
func TestTheCorrectionIsItsOwnEntryUnderItsOwnRule(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	allowSellingBelowZero(t, h, f)

	sellShort(t, h, f, "11", "1265.00")
	deliver(t, h, f, "5", "100.00")

	var rules []string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT coalesce(rule_key, '') FROM journal_entry
			WHERE company_id = $1 AND source_type = 'goods_receipt'
			ORDER BY entry_no`, f.companyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if e := rows.Scan(&k); e != nil {
				return e
			}
			rules = append(rules, k)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read entries: %v", err)
	}

	want := map[string]bool{"purchase.accrual": false, "inventory.variance": false}
	for _, k := range rules {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("the delivery posted no %s entry; it posted %v", key, rules)
		}
	}
	if len(rules) != 2 {
		t.Errorf("the delivery posted %d entries (%v), want the accrual and the "+
			"correction as separate entries", len(rules), rules)
	}
}

// A delivery for goods nobody sold short corrects nothing and posts nothing.
//
// The ordinary case, and worth holding: an empty correction entry on every
// receipt would balance, say nothing, and bury the real ones.
func TestAnOrdinaryDeliveryPostsNoCorrection(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	body := deliver(t, h, f, "10", "100.00")

	if got, _ := body["cost_correction"].(string); got != "0.00" {
		t.Errorf("an ordinary delivery reports a correction of %q", got)
	}
	if got, _ := body["units_recosted"].(string); got != "0" {
		t.Errorf("an ordinary delivery re-costed %q units", got)
	}
	if got := varianceBalance(t, h, f); !got.IsZero() {
		t.Errorf("the variance account holds %s after a delivery that corrected "+
			"nothing", got)
	}

	var entries int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM journal_entry
			WHERE company_id = $1 AND rule_key LIKE 'inventory.variance%'`,
			f.companyID).Scan(&entries)
	}); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if entries != 0 {
		t.Errorf("%d variance entries were posted for nothing", entries)
	}
}

// Receiving the same delivery twice must not correct the same shortfall twice.
//
// Receiving is idempotent on the delivery's own identifier and the correction
// has to inherit that. Without it a retried request — a client that lost its
// answer and asked again — would settle the hole a second time against stock
// that had already covered it, and cost of goods sold would drift by the
// adjustment on every retry.
func TestARetriedDeliveryDoesNotCorrectTwice(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	allowSellingBelowZero(t, h, f)

	sellShort(t, h, f, "12", "1380.00")

	poID, lineID := raiseOrder(t, h, f, "4", "110.00")
	delivery := map[string]any{
		"uuid": newUUID(), "po_id": poID,
		"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "4"}},
	}

	first := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token, delivery)
	if first.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, first))
	}
	after := varianceBalance(t, h, f)
	if !after.Equal(decimal.RequireFromString("100")) {
		t.Fatalf("the variance account holds %s, want 100 — two units charged "+
			"at 60 that cost 110", after)
	}

	second := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token, delivery)
	if second.StatusCode != 201 && second.StatusCode != 200 {
		t.Fatalf("retry: %s", readBody(t, second))
	}
	body := decodeJSON(t, second)
	if already, _ := body["already_received"].(bool); !already {
		t.Error("the retry was not recognised as the same delivery")
	}
	if got, _ := body["cost_correction"].(string); got != "0.00" {
		t.Errorf("the retry reports a correction of %q; the original made it", got)
	}

	if again := varianceBalance(t, h, f); !again.Equal(after) {
		t.Errorf("the variance account moved from %s to %s on a retried "+
			"delivery", after, again)
	}
	if diff := tieOut(t, h, f); !diff.IsZero() {
		t.Fatalf("valuation out by %s after the retry", diff)
	}
}
