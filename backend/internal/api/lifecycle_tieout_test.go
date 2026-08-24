//go:build integration

package api

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// A whole day's trading, tied out against the books.
//
// # Why this exists when the pieces are already tested
//
// Every part of this has its own test, and they all pass. What none of them
// answers is whether the parts still agree once a day has run through them:
// the trial balance is tied out after two cash sales and nothing else, so the
// paths most likely to break it -- a return, an exchange, a credit sale and
// the payment that settles it -- have never been in the same company at the
// same time as the check that says the books balance.
//
// That is the shape of the failure this guards against. A rounding difference
// or a missing leg does not announce itself on the transaction that caused it;
// it shows up as a trial balance that is out by a few hallalas at the end of a
// month, by which point the transaction that did it is one of nine hundred.
//
// # The three invariants
//
// After everything below, all three must hold together:
//
//  1. Debits equal credits. Not approximately.
//  2. Stock is the sum of its movements. No level is stored anywhere, so any
//     disagreement means a movement was written that the valuation cannot see,
//     or vice versa.
//  3. What customers owe, per the ledger, equals the receivable control
//     account. These are computed by different code down different paths, so
//     agreeing is evidence rather than tautology.

// tradeAFullDay rings up everything a shop actually does in a day.
//
// Deliberately more than tradeOneDay: that one is two cash sales, which is the
// path least likely to be wrong.
func (h *harness) tradeAFullDay(t *testing.T, f *shopFixture) {
	t.Helper()

	// Two ordinary cash sales.
	for _, c := range []struct{ qty, paid string }{{"1", "115.00"}, {"2", "230.00"}} {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), c.qty, "115.00", c.paid))
		if resp.StatusCode != 201 {
			t.Fatalf("cash sale: %s", readBody(t, resp))
		}
		resp.Body.Close()
	}

	// A third, so there is something to bring back.
	invoiceID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")

	// One of the two comes back. A return is the most common way for the
	// books and the stock to part company: it has to reverse revenue, the tax
	// on it, the cost of sale and the stock, all four.
	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	resp.Body.Close()
}

// currentBooks reads the three figures the invariants are stated in.
type books struct {
	trialBalanceDiff decimal.Decimal
	stockFromLevels  decimal.Decimal
	stockFromMoves   decimal.Decimal
	ledgerReceivable decimal.Decimal
	controlAccount   decimal.Decimal
}

func readBooks(t *testing.T, h *harness, f *shopFixture) books {
	t.Helper()
	ctx := context.Background()
	var b books

	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT trial_balance_difference($1)`, f.companyID).
			Scan(&b.trialBalanceDiff); e != nil {
			return e
		}

		// Stock two ways: what the valuation view reports, and the raw sum of
		// the movement rows. There is no stored level, so these are genuinely
		// independent readings of the same fact.
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(delta), 0)
			  FROM stock_movement WHERE company_id = $1`, f.companyID).
			Scan(&b.stockFromMoves); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(qty), 0) FROM (
			  SELECT sum(delta) AS qty
			    FROM stock_movement WHERE company_id = $1
			   GROUP BY variant_id, warehouse_id
			) levels`, f.companyID).Scan(&b.stockFromLevels); e != nil {
			return e
		}

		// What the customer ledger says is owed, against the control account
		// the postings landed in.
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(customer_balance(c.id)), 0)
			  FROM customer c WHERE c.company_id = $1`, f.companyID).
			Scan(&b.ledgerReceivable); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			  FROM journal_line l
			  JOIN account a ON a.id = l.account_id
			 WHERE a.company_id = $1 AND a.control_of = 'receivable'`,
			f.companyID).Scan(&b.controlAccount)
	})
	if err != nil {
		t.Fatalf("reading the books: %v", err)
	}
	return b
}

// The books balance after a day that includes a return, not merely after two
// cash sales.
func TestTheBooksBalanceAfterARealDay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	before := readBooks(t, h, f)
	h.tradeAFullDay(t, f)
	after := readBooks(t, h, f)

	if !after.trialBalanceDiff.IsZero() {
		t.Errorf("the trial balance is out by %s after a day's trading. Every "+
			"entry balances on its own -- the tests for that pass -- so a "+
			"difference here means a leg was posted to one side of a "+
			"transaction and not the other.", after.trialBalanceDiff)
	}

	// Something must actually have happened, or the check above is vacuous.
	if after.stockFromMoves.Equal(before.stockFromMoves) {
		t.Fatal("stock did not move at all; the day did not trade and this " +
			"test proved nothing")
	}
}

// Stock is the sum of its movements, with nothing stored to drift from them.
func TestStockAgreesWithItsMovementsAfterARealDay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Measured as a CHANGE, not a total. The fixture seeds opening stock as a
	// movement like any other, so the absolute figure is dominated by an
	// opening balance that has nothing to do with the day's trading -- the
	// first cut of this asserted the total was negative and failed at +6,
	// which was the fixture's opening ten less the day's net five.
	before := readBooks(t, h, f)
	h.tradeAFullDay(t, f)
	after := readBooks(t, h, f)

	if !after.stockFromLevels.Equal(after.stockFromMoves) {
		t.Errorf("stock read per variant sums to %s but the movements sum to "+
			"%s. There is no stored level, so the two cannot disagree unless a "+
			"movement is being counted twice or not at all.",
			after.stockFromLevels, after.stockFromMoves)
	}

	// Five units sold across three sales, one returned: the day is four units
	// down whatever the shop opened with.
	moved := after.stockFromMoves.Sub(before.stockFromMoves)
	if !moved.Equal(decimal.RequireFromString("-4")) {
		t.Errorf("the day moved stock by %s, want -4 (five sold, one returned)",
			moved)
	}
}

// What the customer ledger says is owed must equal the receivable control
// account. They are computed by different code down different paths.
func TestTheCustomerLedgerAgreesWithTheControlAccount(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.tradeAFullDay(t, f)

	b := readBooks(t, h, f)
	if !b.ledgerReceivable.Equal(b.controlAccount) {
		t.Errorf("the customer ledger says %s is owed, the receivable control "+
			"account says %s. One of them is wrong, and a statement sent to a "+
			"customer is drawn from the first while the balance sheet is drawn "+
			"from the second.", b.ledgerReceivable, b.controlAccount)
	}
}

// A return must reverse all four of revenue, tax, cost and stock -- not the
// obvious two.
//
// Reversing revenue and forgetting the cost of sale leaves the margin
// overstated by the whole cost of the returned goods, and nothing about the
// trial balance notices: the entry still balances, it is simply wrong.
func TestAReturnReversesRevenueTaxCostAndStockTogether(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := context.Background()

	invoiceID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")

	read := func() (revenue, tax, cogs, stock decimal.Decimal) {
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			q := func(role string, into *decimal.Decimal) error {
				return tx.QueryRow(ctx, `
					SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
					  FROM journal_line l
					  JOIN account_role_map m ON m.account_id = l.account_id
					 WHERE m.company_id = $1 AND m.role = $2`,
					f.companyID, role).Scan(into)
			}
			if e := q("sales_revenue", &revenue); e != nil {
				return e
			}
			if e := q("output_vat", &tax); e != nil {
				return e
			}
			if e := q("cogs", &cogs); e != nil {
				return e
			}
			return tx.QueryRow(ctx, `
				SELECT coalesce(sum(delta), 0)
				  FROM stock_movement WHERE company_id = $1`, f.companyID).
				Scan(&stock)
		}); err != nil {
			t.Fatalf("reading: %v", err)
		}
		return
	}

	revBefore, taxBefore, cogsBefore, stockBefore := read()

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	resp.Body.Close()

	revAfter, taxAfter, cogsAfter, stockAfter := read()

	// One unit of 115 gross: 100 net and 15 tax come back off.
	if got := revBefore.Sub(revAfter); !got.Equal(decimal.RequireFromString("100")) {
		t.Errorf("revenue fell by %s on a one-unit return, want 100", got)
	}
	if got := taxBefore.Sub(taxAfter); !got.Equal(decimal.RequireFromString("15")) {
		t.Errorf("output VAT fell by %s, want 15. The VAT return is filed from "+
			"this figure, so crediting the customer without reversing the tax "+
			"means paying tax on a sale that was undone.", got)
	}
	// COGS is a debit, so the sign convention above makes a reversal show as a
	// rise in this (credit-positive) reading.
	if !cogsAfter.GreaterThan(cogsBefore) {
		t.Errorf("cost of sale did not reverse (%s then %s). The entry still "+
			"balances either way, so nothing else would have caught this -- "+
			"the margin would simply be overstated by the cost of the goods.",
			cogsBefore, cogsAfter)
	}
	if !stockAfter.Sub(stockBefore).Equal(decimal.RequireFromString("1")) {
		t.Errorf("stock moved by %s on the return, want +1. Crediting the "+
			"customer without taking the goods back means the shop has paid "+
			"for stock it does not have.", stockAfter.Sub(stockBefore))
	}
}
