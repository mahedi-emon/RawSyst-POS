//go:build integration

// Cash, bank and reconciliation, derived from C2 and C11 rather than from the
// code.
//
//	C2: "Fund transfers, fully tracked: Cash → Bank, Bank → Cash, Cash → Cash
//	 (branch to branch), Bank → Bank — every transfer creates its own audit
//	 entry and a printable transfer voucher."
//
//	C11: "Proves that what the software says is in the bank is actually what
//	 the bank says."
//
// The second sentence is the whole of this file's weight. A reconciliation
// screen that lets somebody sign off a difference nobody accounts for is worse
// than no reconciliation screen: it produces a signed piece of paper that an
// auditor will rely on, and the thing it attests to was never checked.
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

type treasuryFixture struct {
	*shopFixture
	token string
	// bankID is the money account made from the seeded Bank ledger account.
	bankID uuid.UUID
	cashID uuid.UUID
}

func seedTreasury(t *testing.T, h *harness) *treasuryFixture {
	t.Helper()
	f := h.seedShop(t, "owner")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed the chart: %v", err)
	}

	out := &treasuryFixture{shopFixture: f, token: f.token}
	// 0081 backfilled money accounts for every company that existed when it
	// ran; this company came afterwards, so it gets them the way any new one
	// does — from the chart it was just given.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		for _, m := range []struct {
			role, kind string
			into       *uuid.UUID
		}{
			{"cash", "cash", &out.cashID},
			{"bank", "bank", &out.bankID},
		} {
			if e := tx.QueryRow(t.Context(), `
				INSERT INTO money_account
				  (tenant_id, company_id, account_id, kind, name, currency)
				SELECT $1, $2, a.id, $4, a.name, c.base_currency
				FROM account_role_map r
				JOIN account a ON a.id = r.account_id
				JOIN company c ON c.id = a.company_id
				WHERE r.company_id = $2 AND r.role = $3
				ON CONFLICT (account_id) DO UPDATE SET name = excluded.name
				RETURNING id`,
				f.tenantID, f.companyID, m.role, m.kind).Scan(m.into); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed money accounts: %v", err)
	}
	return out
}

func (f *treasuryFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// --- C2: transfers --------------------------------------------------------

// Posting rule 9, called for the first time.
//
// It has been seeded since 0025 and never once produced an entry, because it
// names both ends from the transaction and nothing knew what the two ends were.
func TestMovingMoneyBetweenAccountsPostsRuleNine(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	beforeCash := f.ledger(t, h, "1100")
	beforeBank := f.ledger(t, h, "1110")

	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "500.00",
			"moved_on": "2026-08-15", "note": "Banked the week's takings",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("move money: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if got := body["transfer_no"]; got == "" || got == nil {
		t.Error("a transfer should carry a voucher number; C2 asks for a " +
			"printable transfer voucher and a voucher with no number is a note")
	}
	if moved := f.ledger(t, h, "1100").Sub(beforeCash); !moved.Equal(decimal.RequireFromString("-500")) {
		t.Errorf("Cash should fall by 500, not %s", moved.StringFixed(2))
	}
	if moved := f.ledger(t, h, "1110").Sub(beforeBank); !moved.Equal(decimal.RequireFromString("500")) {
		t.Errorf("Bank should rise by 500, not %s", moved.StringFixed(2))
	}
}

// "every transfer creates its own audit entry"
func TestATransferLeavesAnAuditEntry(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "120.00",
			"moved_on": "2026-08-15",
		})

	trail := h.do(t, http.MethodGet,
		"/api/v1/audit?action=money_transferred", f.token, nil)
	rows, _ := decodeJSON(t, trail)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("moving money left no audit entry, which C2 requires by name")
	}
}

// A retry after a lost response returns the original rather than banking the
// takings twice.
func TestMovingTheSameMoneyTwiceMovesItOnce(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	body := map[string]any{
		"uuid": newUUID(), "from_account_id": f.cashID.String(),
		"to_account_id": f.bankID.String(), "amount": "300.00",
		"moved_on": "2026-08-15",
	}
	before := f.ledger(t, h, "1110")

	first := h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first: %s", readBody(t, first))
	}
	second := h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("a retry should return the original with 200, got %d — %s",
			second.StatusCode, readBody(t, second))
	}
	if decodeJSON(t, second)["already_recorded"] != true {
		t.Error("the retry should say it is a replay")
	}
	if moved := f.ledger(t, h, "1110").Sub(before); !moved.Equal(decimal.RequireFromString("300")) {
		t.Errorf("the money was banked twice: Bank moved by %s", moved.StringFixed(2))
	}
}

// A transfer is a record of money that moved, and what moved does not change.
func TestARecordedTransferCannotBeEdited(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "75.00",
			"moved_on": "2026-08-15",
		})
	id, _ := decodeJSON(t, resp)["id"].(string)

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE money_transfer SET amount = 7500 WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Fatal("a recorded transfer could be edited. Correcting one means " +
			"transferring back, which leaves both facts visible.")
	}
}

// --- C11: reconciliation --------------------------------------------------

// The statement's own arithmetic, checked before anything is stored.
//
// A statement whose lines do not add up to its own closing balance was mistyped
// or truncated. Reconciling against it would send somebody chasing a difference
// that is not in the books at all.
func TestAStatementThatDoesNotAddUpIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": "0.00", "closing_balance": "1000.00",
			"lines": []map[string]any{
				{"value_date": "2026-08-05", "description": "Deposit", "amount": "400.00"},
			},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a statement that does not add up should be refused, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// The whole point, asserted.
//
// One transfer into the bank, and a statement showing exactly that deposit.
// The auto-match pairs them, the difference is nil, and it signs off.
func TestAStatementThatAgreesWithTheBooksReconciles(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "500.00",
			"moved_on": "2026-08-15",
		})

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")
	closing := opening.Add(decimal.RequireFromString("500"))

	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": closing.StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-15", "description": "Cash deposit",
					"amount": "500.00"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import: %s", readBody(t, resp))
	}
	st := decodeJSON(t, resp)

	if st["difference"] != "0.00" {
		t.Fatalf("the statement matches the books exactly; the difference "+
			"should be 0.00, not %v", st["difference"])
	}
	lines, _ := st["lines"].([]any)
	line, _ := lines[0].(map[string]any)
	if line["match_kind"] != "automatic" {
		t.Errorf("the deposit should have matched automatically, not %v",
			line["match_kind"])
	}

	id, _ := st["id"].(string)
	done := h.do(t, http.MethodPost,
		f.path("/api/v1/treasury/statements/"+id+"/reconcile"), f.token, nil)
	if done.StatusCode != http.StatusOK {
		t.Fatalf("reconcile: %s", readBody(t, done))
	}
	final := decodeJSON(t, done)
	if final["status"] != "reconciled" {
		t.Errorf("status is %v after reconciling", final["status"])
	}
	if final["reconciled_by"] == "" || final["reconciled_by"] == nil {
		t.Error("a reconciliation with nobody's name on it is not an assertion " +
			"anybody made")
	}
}

// The refusal that is the feature.
//
// Two statements with a gap between them: the second opens 40 lower than the
// first closed, which means a page of the bank's own record was never
// imported. Nothing in the books explains the 40 and nothing on either
// exception list does either, so it cannot be signed off.
//
// This is the case a reconciliation exists to catch and the one a screen that
// simply displayed both totals would let through.
func TestAGapBetweenStatementsCannotBeSignedOff(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")

	// August: one deposit the books also have, so August reconciles.
	h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "500.00",
			"moved_on": "2026-08-15",
		})
	augClosing := opening.Add(decimal.RequireFromString("500"))

	first := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": augClosing.StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-15", "description": "Cash deposit",
					"amount": "500.00"},
			},
		})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("import August: %s", readBody(t, first))
	}
	if got := decodeJSON(t, first)["difference"]; got != "0.00" {
		t.Fatalf("August should reconcile on its own; difference is %v", got)
	}

	// September opens 40 BELOW where August closed. The bank took 40 out on a
	// page nobody imported.
	sepOpening := augClosing.Sub(decimal.RequireFromString("40"))
	second := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-09-01", "ends_on": "2026-09-30",
			"opening_balance": sepOpening.StringFixed(2),
			"closing_balance": sepOpening.StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-09-10", "description": "In", "amount": "5.00"},
				{"value_date": "2026-09-11", "description": "Out", "amount": "-5.00"},
			},
		})
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("import September: %s", readBody(t, second))
	}
	st := decodeJSON(t, second)
	if st["difference"] == "0.00" {
		t.Fatal("forty riyals left the account on a statement nobody imported " +
			"and September still reconciled. That is the exact failure this " +
			"module exists to prevent.")
	}

	id, _ := st["id"].(string)
	refused := h.do(t, http.MethodPost,
		f.path("/api/v1/treasury/statements/"+id+"/reconcile"), f.token, nil)
	if refused.StatusCode != http.StatusConflict {
		t.Fatalf("signing off an unexplained difference should conflict, got "+
			"%d — %s", refused.StatusCode, readBody(t, refused))
	}
}

// A bank charge the books have never heard of appears on the exception list
// rather than matching something.
func TestABankChargeNobodyKeyedIsReported(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")
	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": opening.Sub(decimal.RequireFromString("25")).StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-20",
					"description": "Account maintenance fee", "amount": "-25.00"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import: %s", readBody(t, resp))
	}

	lines, _ := decodeJSON(t, resp)["lines"].([]any)
	line, _ := lines[0].(map[string]any)
	if line["match_kind"] != nil && line["match_kind"] != "" {
		t.Errorf("a fee the books have never heard of matched %v; the auto-"+
			"matcher must not invent a pair", line["match_kind"])
	}
}

// A reconciled statement is evidence, and evidence does not change.
func TestAReconciledStatementIsFrozen(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")
	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": opening.StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-05", "description": "In", "amount": "10.00"},
				{"value_date": "2026-08-06", "description": "Out", "amount": "-10.00"},
			},
		})
	st := decodeJSON(t, resp)
	id, _ := st["id"].(string)

	done := h.do(t, http.MethodPost,
		f.path("/api/v1/treasury/statements/"+id+"/reconcile"), f.token, nil)
	if done.StatusCode != http.StatusOK {
		t.Fatalf("reconcile: %s", readBody(t, done))
	}

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO bank_statement_line
			  (tenant_id, statement_id, value_date, description, amount)
			VALUES ($1,$2,'2026-08-09','Slipped in later',999)`,
			f.tenantID, id)
		return e
	})
	if err == nil {
		t.Fatal("a line could be added to a reconciled statement. The lines " +
			"are the evidence the sign-off was about.")
	}
}

// ...and the refusal has to reach an HTTP caller as a refusal.
//
// The test above inserts straight into the table, so it proves the trigger
// fires and says nothing about what a person is told. Driving the
// reconciliation screens against a running server found the other half: match
// and unmatch on a signed-off statement both answered 500.
//
// The trigger raises with `USING ERRCODE = 'restrict_violation'` rather than
// the default `P0001`, and `db.Translate` knew only the default — so the
// sentence written for the reader, "Reopen it first, which is recorded", was
// thrown away and replaced with "Something went wrong on our side".
//
// That is worse than an unhelpful message. A 500 says the fault is ours, it
// invites a retry that will fail identically, it pages whoever watches the
// error rate, and it hides a refusal working exactly as intended. Two other
// triggers raise the same way — a posted stock voucher and an invoiced order —
// so all three were reporting a deliberate refusal as a server fault.
func TestAFrozenStatementRefusesRatherThanFailing(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")
	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": opening.StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-05", "description": "In", "amount": "10.00"},
				{"value_date": "2026-08-06", "description": "Out", "amount": "-10.00"},
			},
		})
	st := decodeJSON(t, resp)
	id, _ := st["id"].(string)
	lines, _ := st["lines"].([]any)
	if len(lines) == 0 {
		t.Fatal("the statement came back with no lines")
	}
	first, _ := lines[0].(map[string]any)
	lineID, _ := first["id"].(string)

	if done := h.do(t, http.MethodPost,
		f.path("/api/v1/treasury/statements/"+id+"/reconcile"), f.token, nil); done.StatusCode != http.StatusOK {
		t.Fatalf("reconcile: %s", readBody(t, done))
	}

	// An empty journal line is how the route expresses "undo the match", so
	// this exercises Unmatch, which returned its driver error raw.
	undo := h.do(t, http.MethodPost,
		f.path("/api/v1/treasury/lines/"+lineID+"/match"), f.token,
		map[string]any{"journal_line_id": ""})
	body := readBody(t, undo)

	if undo.StatusCode >= 500 {
		t.Fatalf("unmatching on a reconciled statement answered %d. The "+
			"database refused on purpose; a 500 tells the caller it was our "+
			"fault and to try again. Body: %s", undo.StatusCode, body)
	}
	if undo.StatusCode == http.StatusOK || undo.StatusCode == http.StatusNoContent {
		t.Fatal("a reconciled statement's lines could be changed. They are " +
			"the evidence the sign-off was about.")
	}
	if !strings.Contains(body, "Reopen it first") {
		t.Fatalf("the refusal did not carry the reason a person needs. Body: %s", body)
	}
}

// One ledger entry cannot answer for two bank lines.
//
// A payment recorded twice in the books against a single bank debit is exactly
// the error this module exists to surface, and a matcher that allowed it would
// reconcile the duplicate away.
func TestOneLedgerEntryCannotMatchTwoBankLines(t *testing.T) {
	h := newHarness(t)
	f := seedTreasury(t, h)

	h.do(t, http.MethodPost, f.path("/api/v1/treasury/transfers"), f.token,
		map[string]any{
			"uuid": newUUID(), "from_account_id": f.cashID.String(),
			"to_account_id": f.bankID.String(), "amount": "60.00",
			"moved_on": "2026-08-15",
		})

	opening := f.ledgerAt(t, h, "1110", "2026-07-31")
	resp := h.do(t, http.MethodPost, f.path("/api/v1/treasury/statements"), f.token,
		map[string]any{
			"account_id": f.bankID.String(),
			"starts_on":  "2026-08-01", "ends_on": "2026-08-31",
			"opening_balance": opening.StringFixed(2),
			"closing_balance": opening.Add(decimal.RequireFromString("120")).StringFixed(2),
			"lines": []map[string]any{
				{"value_date": "2026-08-15", "description": "Deposit", "amount": "60.00"},
				{"value_date": "2026-08-15", "description": "Deposit", "amount": "60.00"},
			},
		})
	st := decodeJSON(t, resp)
	lines, _ := st["lines"].([]any)

	matched := 0
	for _, raw := range lines {
		l, _ := raw.(map[string]any)
		if l["match_kind"] != nil && l["match_kind"] != "" {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("%d of two identical bank lines matched the single ledger "+
			"entry. Exactly one may: the other is the duplicate this whole "+
			"module exists to surface.", matched)
	}
}

// --- helpers --------------------------------------------------------------

func (f *treasuryFixture) ledger(t *testing.T, h *harness, code string) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN account a ON a.id = l.account_id
			WHERE a.company_id = $1 AND a.code = $2`,
			f.companyID, code).Scan(&d)
	}); err != nil {
		t.Fatalf("balance of %s: %v", code, err)
	}
	return d
}

func (f *treasuryFixture) ledgerAt(
	t *testing.T, h *harness, code, on string,
) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account a ON a.id = l.account_id
			WHERE a.company_id = $1 AND a.code = $2 AND e.entry_date <= $3::date`,
			f.companyID, code, on).Scan(&d)
	}); err != nil {
		t.Fatalf("balance of %s at %s: %v", code, on, err)
	}
	return d
}
