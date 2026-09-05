//go:build integration

// Journal entries written by hand (blueprint C10).
//
// The exception to this ledger's rule that nobody types it. C10 asks for
// "accounting adjustments / manual journal entries — permission-gated,
// reason-required, fully audit-logged", and these hold it to all three plus the
// arithmetic: an adjustment either balances or it is refused, and it lands in
// the period covering its date or not at all.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// twoAccounts finds two postable accounts to move money between.
func twoAccounts(t *testing.T, h *harness, f *shopFixture) (string, string) {
	t.Helper()
	var a, b string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			rows, e := tx.Query(context.Background(), `
				SELECT id::text FROM account
				WHERE company_id = $1 AND is_postable
				ORDER BY code LIMIT 2`, f.companyID)
			if e != nil {
				return e
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var id string
				if e := rows.Scan(&id); e != nil {
					return e
				}
				got = append(got, id)
			}
			if len(got) < 2 {
				t.Fatal("the company has fewer than two postable accounts")
			}
			a, b = got[0], got[1]
			return rows.Err()
		}); err != nil {
		t.Fatalf("read accounts: %v", err)
	}
	return a, b
}

func journalBody(debitAcc, creditAcc, debit, credit, reason string) map[string]any {
	return map[string]any{
		"uuid":       newUUID(),
		"entry_date": "2026-08-15",
		"reason":     reason,
		"lines": []any{
			map[string]any{"account_id": debitAcc, "debit": debit},
			map[string]any{"account_id": creditAcc, "credit": credit},
		},
	}
}

func postJournal(t *testing.T, h *harness, f *shopFixture, body map[string]any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/accounting/journals?company_id="+f.companyID.String(),
		f.token, body)
}

// An adjustment posts, balances, and reaches the ledger.
func TestAHandWrittenJournalPostsToTheLedger(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f, journalBody(debit, credit, "250.00", "250.00",
		"Accrue August rent; the invoice had not arrived at close"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post: %d %s", resp.StatusCode, readBody(t, resp))
	}
	out := decodeJSONFrom(t, resp)

	if no, _ := out["journal_no"].(string); no == "" {
		t.Error("the journal has no number")
	}
	if got, _ := out["total"].(string); !amountsEqual(got, "250.00") {
		t.Errorf("total is %s, want 250.00", got)
	}
	lines, _ := out["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("the journal has %d lines, want 2", len(lines))
	}

	// It is an ordinary entry, so the trial balance sees it.
	assertTrialBalanceBalances(t, h, f)

	// And the ledger carries exactly what was asked for.
	var debits, credits decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT coalesce(sum(l.debit), 0), coalesce(sum(l.credit), 0)
				FROM journal_line l
				JOIN journal_entry e ON e.id = l.entry_id
				WHERE e.source_type = 'manual_journal'`).Scan(&debits, &credits)
		}); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if !debits.Equal(credits) || !debits.Equal(decimal.RequireFromString("250")) {
		t.Errorf("the ledger holds %s debits and %s credits, want 250 each",
			debits, credits)
	}
}

// An adjustment that does not balance is refused, and says by how much.
func TestAnUnbalancedJournalIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f,
		journalBody(debit, credit, "250.00", "200.00", "Mistyped on purpose"))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("an unbalanced journal posted")
	}
	body := readBody(t, resp)
	if !containsFold(body, "balance") {
		t.Errorf("the refusal does not say it is unbalanced: %s", body)
	}
	// The number somebody has to go and find.
	if !containsFold(body, "50") {
		t.Errorf("the refusal does not name the difference: %s", body)
	}
}

// A reason is required, because C10 requires it and an auditor reads it.
func TestAJournalWithoutAReasonIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f,
		journalBody(debit, credit, "10.00", "10.00", "   "))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a journal with no reason posted")
	}
}

// Negative amounts, both sides on one line, and a single line are all refused.
func TestAJournalRefusesNonsenseLines(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	cases := []struct {
		name  string
		lines []any
	}{
		{"a negative amount", []any{
			map[string]any{"account_id": debit, "debit": "-5.00"},
			map[string]any{"account_id": credit, "credit": "-5.00"},
		}},
		{"both sides on one line", []any{
			map[string]any{"account_id": debit, "debit": "5.00", "credit": "5.00"},
			map[string]any{"account_id": credit, "credit": "5.00"},
		}},
		{"only one line", []any{
			map[string]any{"account_id": debit, "debit": "5.00"},
		}},
		{"nothing at all", []any{
			map[string]any{"account_id": debit, "debit": "0"},
			map[string]any{"account_id": credit, "credit": "0"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJournal(t, h, f, map[string]any{
				"uuid": newUUID(), "entry_date": "2026-08-15",
				"reason": "Testing what is refused", "lines": c.lines,
			})
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				t.Errorf("%s was accepted", c.name)
			}
		})
	}
}

// An account belonging to nobody in this company cannot be posted to.
func TestAJournalCannotNameAnUnknownAccount(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, _ := twoAccounts(t, h, f)

	resp := postJournal(t, h, f, map[string]any{
		"uuid": newUUID(), "entry_date": "2026-08-15",
		"reason": "Pointing at an account that is not ours",
		"lines": []any{
			map[string]any{"account_id": debit, "debit": "5.00"},
			map[string]any{"account_id": newUUID(), "credit": "5.00"},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a journal posted to an account this company does not have")
	}
}

// A retry posts one journal, not two.
func TestTheSameJournalArrivingTwiceIsPostedOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	body := journalBody(debit, credit, "75.00", "75.00", "Retried on purpose")
	first := postJournal(t, h, f, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first: %s", readBody(t, first))
	}
	firstID, _ := decodeJSONFrom(t, first)["id"].(string)

	second := postJournal(t, h, f, body)
	// 200, as every other idempotent path in the product answers a repeat.
	// This route answered 201 either way, which says "created" of an entry it
	// did not create -- and a caller branching on the status to tell "posted"
	// from "already posted" was told wrongly.
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry: %d %s, want 200", second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("a replayed journal did not say so")
	}
	replayed := decodeJSONFrom(t, second)
	secondID, _ := replayed["id"].(string)

	if firstID != secondID {
		t.Errorf("the retry made a second journal (%s then %s)",
			firstID, secondID)
	}
	if already, _ := replayed["already_recorded"].(bool); !already {
		t.Error("the replayed journal did not report itself as one")
	}

	var n int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT count(*) FROM manual_journal WHERE company_id = $1`,
				f.companyID).Scan(&n)
		}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d journals were written, want 1", n)
	}
	assertTrialBalanceBalances(t, h, f)
}

// A reversal undoes exactly what was posted, and the pair nets to nothing.
func TestReversingAJournalUndoesWhatItPosted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f, journalBody(debit, credit, "120.00", "120.00",
		"Booked to the wrong head"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post: %s", readBody(t, resp))
	}
	id, _ := decodeJSONFrom(t, resp)["id"].(string)

	rev := h.do(t, http.MethodPost,
		"/api/v1/accounting/journals/"+id+"/reverse?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"reason": "Correcting the head it was booked to"})
	if rev.StatusCode != http.StatusCreated {
		t.Fatalf("reverse: %d %s", rev.StatusCode, readBody(t, rev))
	}
	rev.Body.Close()

	// Every account either entry touched, netted across both.
	type move struct{ code, net string }
	var moves []move
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			rows, e := tx.Query(context.Background(), `
				SELECT a.code, sum(l.debit - l.credit)::text
				FROM journal_line l
				JOIN journal_entry e ON e.id = l.entry_id
				JOIN account a ON a.id = l.account_id
				WHERE e.source_type = 'manual_journal'
				GROUP BY a.code`)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var m move
				if e := rows.Scan(&m.code, &m.net); e != nil {
					return e
				}
				moves = append(moves, m)
			}
			return rows.Err()
		}); err != nil {
		t.Fatalf("read the footprint: %v", err)
	}
	if len(moves) == 0 {
		t.Fatal("the journal and its reversal touched no accounts")
	}
	for _, m := range moves {
		if !amountsEqual(m.net, "0") {
			t.Errorf("account %s is left %s out after the reversal", m.code,
				m.net)
		}
	}
	assertTrialBalanceBalances(t, h, f)
}

// A journal is reversed once. A second attempt returns the reversal that exists.
func TestAJournalIsNotReversedTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f,
		journalBody(debit, credit, "40.00", "40.00", "To be reversed"))
	id, _ := decodeJSONFrom(t, resp)["id"].(string)

	path := "/api/v1/accounting/journals/" + id + "/reverse?company_id=" +
		f.companyID.String()
	body := map[string]any{"reason": "Reversing"}

	first := h.do(t, http.MethodPost, path, f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first reversal: %s", readBody(t, first))
	}
	firstID, _ := decodeJSONFrom(t, first)["id"].(string)

	second := h.do(t, http.MethodPost, path, f.token, body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second reversal: %d %s", second.StatusCode,
			readBody(t, second))
	}
	if got, _ := decodeJSONFrom(t, second)["id"].(string); got != firstID {
		t.Errorf("the journal was reversed twice (%s then %s)", firstID, got)
	}
	assertTrialBalanceBalances(t, h, f)
}

// A posted journal cannot be edited or deleted. Corrections are reversals.
func TestAPostedJournalCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	resp := postJournal(t, h, f,
		journalBody(debit, credit, "60.00", "60.00", "Immutable once written"))
	id, _ := decodeJSONFrom(t, resp)["id"].(string)

	for _, c := range []struct{ name, sql string }{
		{"the reason", `UPDATE manual_journal SET reason = 'something else' WHERE id = $1`},
		{"the date", `UPDATE manual_journal SET entry_date = '2020-01-01' WHERE id = $1`},
		{"the entry it posted", `UPDATE manual_journal SET journal_entry_id = gen_random_uuid() WHERE id = $1`},
		{"the row itself", `DELETE FROM manual_journal WHERE id = $1`},
	} {
		err := h.pool.TxAsTenant(context.Background(), f.tenantID,
			func(tx pgx.Tx) error {
				_, e := tx.Exec(context.Background(), c.sql, id)
				return e
			})
		if err == nil {
			t.Errorf("%s could be changed after posting", c.name)
		}
	}
}

// The adjustment register lists what was written.
func TestTheJournalRegisterListsWhatWasWritten(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	postJournal(t, h, f,
		journalBody(debit, credit, "15.00", "15.00", "One")).Body.Close()
	postJournal(t, h, f,
		journalBody(debit, credit, "25.00", "25.00", "Two")).Body.Close()

	resp := h.do(t, http.MethodGet,
		"/api/v1/accounting/journals?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %s", readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) != 2 {
		t.Errorf("the register holds %d journals, want 2", len(rows))
	}
}

// Somebody without the permission cannot post straight to the ledger.
//
// `accounting.create` is described in 0101 as posting "past every other
// screen", which is exactly why a cashier must not hold it.
func TestACashierCannotWriteAJournal(t *testing.T) {
	h := newHarness(t)
	owner := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, owner)
	cashier := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost,
		"/api/v1/accounting/journals?company_id="+cashier.companyID.String(),
		cashier.token,
		journalBody(debit, credit, "10.00", "10.00", "Trying it on"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier writing a journal got %d, want 403",
			resp.StatusCode)
	}
}

// One business cannot post into another's books.
func TestAJournalCannotBePostedIntoAnotherCompany(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, theirs)

	resp := h.do(t, http.MethodPost,
		"/api/v1/accounting/journals?company_id="+theirs.companyID.String(),
		mine.token,
		journalBody(debit, credit, "10.00", "10.00", "Into their books"))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a journal was posted into another business's ledger")
	}
	if resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-company posting got %d, want 404 or 403",
			resp.StatusCode)
	}
}

// Posting a journal is audited, because C10 says it is.
func TestWritingAJournalIsAudited(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)

	postJournal(t, h, f, journalBody(debit, credit, "90.00", "90.00",
		"Audited adjustment")).Body.Close()

	var n int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT count(*) FROM audit_log
				WHERE action = 'manual_journal_posted'
				  AND entity_type = 'manual_journal'
				  AND actor_id IS NOT NULL`).Scan(&n)
		}); err != nil {
		t.Fatalf("read the audit log: %v", err)
	}
	if n != 1 {
		t.Errorf("%d audit records were written, want 1", n)
	}
}

// A closed period refuses an adjustment dated inside it.
//
// The whole reason periods close: "once a period is closed, no transaction can
// be created, edited or deleted in that period — this is what makes financial
// statements trustworthy" (C10). A hand-written journal is the one entry a
// person could try to slip in afterwards, so it is the one that most needs the
// lock to hold.
func TestAJournalCannotBePostedIntoAClosedPeriod(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	debit, credit := twoAccounts(t, h, f)
	ctx := context.Background()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE fiscal_period
			SET state = 'closed', closed_at = now(), closed_by = $2
			WHERE company_id = $1 AND fiscal_year = 2026 AND period_no = 8`,
			f.companyID, f.userID)
		return e
	}); err != nil {
		t.Fatalf("close the period: %v", err)
	}

	resp := postJournal(t, h, f, journalBody(debit, credit, "30.00", "30.00",
		"Slipping one in after the close"))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a journal was posted into a closed period")
	}
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("posting into a closed period got %d, want 409",
			resp.StatusCode)
	}

	// Nothing landed: the entry and the journal row are written in one
	// transaction, so a refused entry must leave no journal behind.
	var n int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM manual_journal WHERE company_id = $1`,
			f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d journals survived a refused posting", n)
	}
}
