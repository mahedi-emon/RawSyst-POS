//go:build integration

// P33 — the drawer variance in the ledger.
//
// Design 11 §9 says a Z-report variance "posts to a Cash Over/Short account
// rather than being absorbed silently". Until 0052 it did not: the figure was
// written onto cash_session and went no further, so a shop could run short
// every day for a month while Cash carried a balance the drawer had never held
// and the loss appeared nowhere in the P&L.
//
// These check the entry itself rather than only that closing succeeds. A
// posting that ran and put the money on the wrong side would pass any test that
// merely asserted a 200, and would be discovered by an accountant months later.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// One line of a posted entry.
type postedLine struct {
	code    string
	role    string
	debit   decimal.Decimal
	credit  decimal.Decimal
	storeID *uuid.UUID
}

// varianceEntry reads back what closing a session posted, by the engine's own
// idempotency key rather than by guessing at the most recent entry.
func varianceEntry(
	t *testing.T, h *harness, f *shopFixture, sessionID uuid.UUID,
) (ruleKey string, memo string, lines []postedLine) {
	t.Helper()
	ctx := t.Context()

	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id, rule_key, coalesce(memo, '')
			FROM journal_entry
			WHERE source_type = 'cash_session' AND source_id = $1`, sessionID).
			Scan(&entryID, &ruleKey, &memo)
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT a.code, coalesce(m.role, ''), l.debit, l.credit, l.store_id
			FROM journal_line l
			JOIN account a ON a.id = l.account_id
			LEFT JOIN account_role_map m ON m.account_id = a.id
			WHERE l.entry_id = $1
			ORDER BY l.line_no`, entryID)
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
	})
	if err != nil {
		t.Fatalf("read the variance entry: %v", err)
	}
	return ruleKey, memo, lines
}

func countVarianceEntries(t *testing.T, h *harness, f *shopFixture, sessionID uuid.UUID) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM journal_entry
			WHERE source_type = 'cash_session' AND source_id = $1`, sessionID).Scan(&n)
	}); err != nil {
		t.Fatalf("count variance entries: %v", err)
	}
	return n
}

// closeWith closes the fixture's session over the real HTTP route.
func closeWith(t *testing.T, h *harness, f *shopFixture, counted string) map[string]any {
	t.Helper()
	resp := h.do(t, "POST", "/api/v1/shifts/"+f.sessionID.String()+"/close", f.token,
		map[string]any{"counted_cash": counted, "note": "counted"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close with %s: status %d — %s", counted, resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// A drawer that reconciles posts nothing.
//
// Not an optimisation. A zero-value journal entry for every shift that went
// right would bury the ones that went wrong, and a Cash Over/Short account
// whose activity is mostly zeroes is one nobody reads.
func TestAnExactDrawerPostsNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // 200.00 float, nothing sold

	z := closeWith(t, h, f, "200.00")
	if z["variance"] != "0" {
		t.Fatalf("variance = %v, want 0", z["variance"])
	}

	if n := countVarianceEntries(t, h, f, f.sessionID); n != 0 {
		t.Fatalf("an exact drawer posted %d journal entries, want none", n)
	}
}

// A shortfall: the cash is not there, so the asset comes down and the shop
// wears the difference as an expense.
//
//	Dr 5500 Cash Over/Short   5.00
//	Cr 1100 Cash                    5.00
func TestAShortDrawerPostsToCashOverShort(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	z := closeWith(t, h, f, "195.00")
	if z["variance"] != "-5" {
		t.Fatalf("variance = %v, want -5", z["variance"])
	}

	rule, memo, lines := varianceEntry(t, h, f, f.sessionID)
	if rule != "cash.shortage" {
		t.Errorf("rule = %q, want cash.shortage", rule)
	}
	if len(lines) != 2 {
		t.Fatalf("posted %d lines, want 2: %+v", len(lines), lines)
	}

	assertLine(t, lines, "5500", "cash_over_short", "5", "0")
	assertLine(t, lines, "1100", "cash", "0", "5")

	// The memo says which shift and which way, because a Cash Over/Short
	// account with unexplained lines is the thing this exists to prevent.
	if !strings.Contains(memo, "short") || !strings.Contains(memo, "5.00") {
		t.Errorf("memo = %q; it should name the direction and the amount", memo)
	}

	// The store is carried onto the lines, so a group can see which branch is
	// losing money rather than only that the group is.
	for _, l := range lines {
		if l.storeID == nil || *l.storeID != f.storeID {
			t.Errorf("line %s carries store %v, want %s", l.code, l.storeID, f.storeID)
		}
	}
}

// An overage: cash the books did not know about. The asset goes up and the same
// account carries the other side — deliberately not Other Income, because an
// unexplained surplus is as much a control failure as a shortfall and posting
// it as income would flatter the month it happened in.
//
//	Dr 1100 Cash              7.50
//	Cr 5500 Cash Over/Short         7.50
func TestALongDrawerPostsToCashOverShort(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	z := closeWith(t, h, f, "207.50")
	if z["variance"] != "7.5" {
		t.Fatalf("variance = %v, want 7.5", z["variance"])
	}

	rule, memo, lines := varianceEntry(t, h, f, f.sessionID)
	if rule != "cash.overage" {
		t.Errorf("rule = %q, want cash.overage", rule)
	}
	if len(lines) != 2 {
		t.Fatalf("posted %d lines, want 2: %+v", len(lines), lines)
	}

	// The sides are swapped and the amount stays positive, which is the
	// arrangement 0025/0026 established for the costing variance. A single
	// signed rule would write a negative debit where a credit belongs.
	assertLine(t, lines, "1100", "cash", "7.5", "0")
	assertLine(t, lines, "5500", "cash_over_short", "0", "7.5")

	if !strings.Contains(memo, "over") {
		t.Errorf("memo = %q; it should say the drawer was over", memo)
	}
}

// The entry balances, and the whole trial balance still does.
//
// The deferred constraint trigger is the real guarantee, but a variance posted
// on one side only would satisfy nothing and this says so in the language an
// accountant would use.
func TestTheVarianceEntryBalancesAndTheCompanyStillDoes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Trade first, so the variance lands on top of real activity rather than
	// on an otherwise empty ledger.
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	closeWith(t, h, f, "310.00") // expected 315, so 5 short

	_, _, lines := varianceEntry(t, h, f, f.sessionID)
	debits, credits := decimal.Zero, decimal.Zero
	for _, l := range lines {
		debits = debits.Add(l.debit)
		credits = credits.Add(l.credit)
	}
	if !debits.Equal(credits) {
		t.Fatalf("the variance entry does not balance: %s against %s", debits, credits)
	}

	// And the company as a whole. C9.1's hard rule holds across every entry
	// this shift produced, not only the one under test.
	var totalDebit, totalCredit decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit), 0), coalesce(sum(l.base_credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE e.company_id = $1`, f.companyID).Scan(&totalDebit, &totalCredit)
	}); err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !totalDebit.Equal(totalCredit) {
		t.Fatalf("the company's trial balance is out: debits %s against credits %s",
			totalDebit, totalCredit)
	}
}

// The posting is inside the transaction that freezes the count.
//
// A closed period refuses the entry, and the whole close must go with it —
// otherwise a till would report a Z report that reconciled nothing and the
// session would be sealed with its difference missing from the books, which is
// the exact state P33 existed to end.
func TestAClosedPeriodRollsBackTheWholeClose(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// Close every period the company has, so the variance has nowhere to post.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE fiscal_period SET state = 'closed', closed_at = now(), closed_by = $2
			 WHERE company_id = $1`, f.companyID, f.userID)
		return e
	}); err != nil {
		t.Fatalf("close the periods: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/shifts/"+f.sessionID.String()+"/close", f.token,
		map[string]any{"counted_cash": "195.00", "note": "five short"})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("the till closed into a locked period; the variance went nowhere")
	}
	resp.Body.Close()

	// The session is still open, and can be closed once the period is reopened.
	// A half-applied close would leave a sealed session with no entry behind it.
	var state string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT state FROM cash_session WHERE id = $1`, f.sessionID).Scan(&state)
	}); err != nil {
		t.Fatalf("read the session: %v", err)
	}
	if state != "open" {
		t.Fatalf("session state = %q after a refused close, want open", state)
	}
	if n := countVarianceEntries(t, h, f, f.sessionID); n != 0 {
		t.Fatalf("a refused close left %d journal entries behind", n)
	}
}

// A second close is refused, so the variance cannot post twice.
//
// Two things stop it and both are checked: the session is locked and already
// closed, and the engine's key on (source_type, source_id, rule_key) would
// refuse a duplicate even if it were not.
func TestTheVariancePostsOnceOnly(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	path := "/api/v1/shifts/" + f.sessionID.String() + "/close"

	resp := h.do(t, "POST", path, f.token, map[string]any{"counted_cash": "195.00"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, "POST", path, f.token, map[string]any{"counted_cash": "100.00"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second close: status %d, want 409 — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if n := countVarianceEntries(t, h, f, f.sessionID); n != 1 {
		t.Fatalf("the shift posted %d variance entries, want exactly 1", n)
	}

	// And the first entry stands unedited: posted history is never rewritten,
	// so the refused second close changed nothing about it.
	_, _, lines := varianceEntry(t, h, f, f.sessionID)
	assertLine(t, lines, "5500", "cash_over_short", "5", "0")
}

// QA gate M8 on the new entry: one shop cannot see another's drawer losses.
//
// Worth its own test because the entry is posted by a service acting for a
// tenant rather than by a signed-in caller, and a posting path that lost its
// tenant scope would write rows another shop could read.
func TestOneShopCannotSeeAnothersDrawerLoss(t *testing.T) {
	h := newHarness(t)
	a := h.seedShop(t, "cashier")
	b := h.seedShop(t, "cashier")

	closeWith(t, h, a, "190.00") // shop A is 10 short

	// Shop A sees its own entry.
	if n := countVarianceEntries(t, h, a, a.sessionID); n != 1 {
		t.Fatalf("shop A sees %d of its own variance entries, want 1", n)
	}

	// Shop B, asking about shop A's session by id, sees nothing.
	var visible int
	if err := h.pool.TxAsTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM journal_entry
			WHERE source_type = 'cash_session' AND source_id = $1`, a.sessionID).
			Scan(&visible)
	}); err != nil {
		t.Fatalf("query as shop B: %v", err)
	}
	if visible != 0 {
		t.Fatal("one shop can read another shop's drawer variance")
	}

	// And the unfiltered probe — the realistic one — reveals nothing either.
	if err := h.pool.TxAsTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM journal_entry WHERE source_type = 'cash_session'`).
			Scan(&visible)
	}); err != nil {
		t.Fatalf("unfiltered query as shop B: %v", err)
	}
	if visible != 0 {
		t.Fatalf("shop B can see %d cash-session entries that are not its own", visible)
	}
}

// Every company created through the product can close a till.
//
// The 0048 lesson, applied to this role: a rule naming a role the chart does
// not map throws at the moment somebody uses it, and the test that covers the
// rule will not notice if it maps the role by hand. This asks provisioning's
// own chart, which is the one every real company gets.
func TestProvisioningMapsCashOverShort(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	var code, kind string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		// A second company, seeded the way provisioning seeds one.
		var companyID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,'Chart Check Co','sa','SAR') RETURNING id`,
			f.tenantID).Scan(&companyID); e != nil {
			return e
		}
		if e := provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, companyID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT a.code, a.type FROM account_role_map m
			JOIN account a ON a.id = m.account_id
			WHERE m.company_id = $1 AND m.role = 'cash_over_short'`,
			companyID).Scan(&code, &kind)
	}); err != nil {
		t.Fatalf("a company seeded by provisioning has no cash_over_short account: %v", err)
	}

	if code != "5500" {
		t.Errorf("cash_over_short maps to account %s, want 5500 as design 12 §1 lists it", code)
	}
	if kind != "expense" {
		t.Errorf("5500 is a %s account, want expense", kind)
	}
}

// --- helpers --------------------------------------------------------------

func assertLine(t *testing.T, lines []postedLine, code, role, debit, credit string) {
	t.Helper()
	for _, l := range lines {
		if l.code != code {
			continue
		}
		if l.role != role {
			t.Errorf("account %s maps to role %q, want %q", code, l.role, role)
		}
		if !l.debit.Equal(decimal.RequireFromString(debit)) {
			t.Errorf("account %s debit = %s, want %s", code, l.debit, debit)
		}
		if !l.credit.Equal(decimal.RequireFromString(credit)) {
			t.Errorf("account %s credit = %s, want %s", code, l.credit, credit)
		}
		return
	}
	t.Errorf("no line posted to account %s; got %+v", code, lines)
}
