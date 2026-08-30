//go:build integration

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// P21: a receipt allocated to the wrong invoice is put right by a NEW
// document, never by editing the original. Design 02 §2 and C9.1.

func TestReversingAReceiptPutsTheInvoiceBackOnTheAccount(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	stockAfterSale := shopStockOnHand(t, h, f)
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")

	if got := customerBalance(t, h, f); !amountsEqual(got, "0.00") {
		t.Fatalf("balance after payment = %q, want 0.00", got)
	}

	orig := snapshotReceipt(t, h, f, receiptID)

	resp := reverseReceipt(t, h, f, receiptID, newUUID().String())
	if resp.StatusCode != 201 {
		t.Fatalf("reverse: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	reverses, _ := body["reverses_id"].(string)
	if reverses != receiptID {
		t.Errorf("reverses_id = %q, want the original %q", reverses, receiptID)
	}
	if amountsEqual(mustString(body["amount"]), "0.00") || mustString(body["amount"]) == "" {
		t.Errorf("the reversing document has amount %q; it must stay positive", body["amount"])
	}

	if got := customerBalance(t, h, f); !amountsEqual(got, "115.00") {
		t.Errorf("balance after reversal = %q, want 115.00", got)
	}
	if got := invoiceOutstanding(t, h, f, invoiceID); !amountsEqual(got, "115.00") {
		t.Errorf("outstanding after reversal = %q, want 115.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("C9.3 after reversal is out by %s", d)
	}
	if !shopStockOnHand(t, h, f).Equal(stockAfterSale) {
		t.Error("reversing a receipt moved stock; a payment does not restock")
	}

	after := snapshotReceipt(t, h, f, receiptID)
	if after != orig {
		t.Errorf("the original receipt was rewritten:\n  before %#v\n  after  %#v", orig, after)
	}
}

func TestAReversingReceiptFlipsTheOriginalJournal(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")

	resp := reverseReceipt(t, h, f, receiptID, newUUID().String())
	if resp.StatusCode != 201 {
		t.Fatalf("reverse: %s", readBody(t, resp))
	}

	var origRule, revRule string
	var origReverses *uuid.UUID
	var revReverses *uuid.UUID
	var origCash, origAR, revCash, revAR decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			SELECT e.rule_key, e.reverses_id
			FROM journal_entry e
			JOIN customer_receipt cr ON cr.journal_entry_id = e.id
			WHERE cr.id = $1`, receiptID).Scan(&origRule, &origReverses); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(), `
			SELECT e.rule_key, e.reverses_id
			FROM journal_entry e
			JOIN customer_receipt cr ON cr.journal_entry_id = e.id
			WHERE cr.reverses_id = $1`, receiptID).Scan(&revRule, &revReverses); e != nil {
			return e
		}
		origCash, origAR = receiptRoleMoves(t, tx, f.companyID, receiptID)
		var reversing uuid.UUID
		if e := tx.QueryRow(t.Context(),
			`SELECT id FROM customer_receipt WHERE reverses_id = $1`,
			receiptID).Scan(&reversing); e != nil {
			return e
		}
		revCash, revAR = receiptRoleMoves(t, tx, f.companyID, reversing.String())
		return nil
	}); err != nil {
		t.Fatalf("read the journals: %v", err)
	}

	if origRule != "payment.customer" || revRule != "payment.customer" {
		t.Errorf("rules = %q / %q, want payment.customer on both", origRule, revRule)
	}
	if origReverses != nil {
		t.Errorf("the original journal names a reversal %v", origReverses)
	}
	if revReverses == nil {
		t.Fatal("the reversing journal does not name the original entry")
	}
	if !origCash.Equal(decimal.RequireFromString("115")) {
		t.Errorf("original cash = %s, want a debit of 115", origCash)
	}
	if !origAR.Equal(decimal.RequireFromString("-115")) {
		t.Errorf("original AR = %s, want a credit of 115", origAR)
	}
	if !revCash.Equal(decimal.RequireFromString("-115")) {
		t.Errorf("reversal cash = %s, want a credit of 115", revCash)
	}
	if !revAR.Equal(decimal.RequireFromString("115")) {
		t.Errorf("reversal AR = %s, want a debit of 115", revAR)
	}
}

func TestTheStatementShowsTheReversalAndTheOriginalTogether(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	if reverseReceipt(t, h, f, receiptID, newUUID().String()).StatusCode != 201 {
		t.Fatal("reverse failed")
	}

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID+"/ledger"), f.token, nil))
	rows, _ := body["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("ledger has %d rows, want 3 (sale, receipt, reversal)", len(rows))
	}

	sale, _ := rows[0].(map[string]any)
	receipt, _ := rows[1].(map[string]any)
	reversal, _ := rows[2].(map[string]any)
	if sale["kind"] != "sale" || receipt["kind"] != "receipt" || reversal["kind"] != "reversal" {
		t.Errorf("kinds = %v / %v / %v", sale["kind"], receipt["kind"], reversal["kind"])
	}
	if reversed, _ := receipt["reversed"].(bool); !reversed {
		t.Error("the original payment is not marked reversed")
	}
	if mustString(reversal["reverses_id"]) != receiptID {
		t.Errorf("statement reversal points at %q, want %q", reversal["reverses_id"], receiptID)
	}
	if closing, _ := body["closing"].(string); !amountsEqual(closing, "115.00") {
		t.Errorf("closing = %q, want 115.00", closing)
	}
	if got := customerBalance(t, h, f); !amountsEqual(got, mustString(body["closing"])) {
		t.Errorf("balance %q disagrees with the statement closing %q", got, body["closing"])
	}
}

func TestARetriedReversalDoesNotReverseTwice(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	docUUID := newUUID().String()

	first := reverseReceipt(t, h, f, receiptID, docUUID)
	if first.StatusCode != 201 {
		t.Fatalf("first reverse: %s", readBody(t, first))
	}
	number, _ := decodeJSON(t, first)["receipt_number"].(string)

	second := reverseReceipt(t, h, f, receiptID, docUUID)
	if second.StatusCode != 200 {
		t.Fatalf("the retry returned %d, want 200: %s",
			second.StatusCode, readBody(t, second))
	}
	replay := decodeJSON(t, second)
	if again, _ := replay["receipt_number"].(string); again != number {
		t.Errorf("the retry issued %q, not the original %q", again, number)
	}
	if taken, _ := replay["already_taken"].(bool); !taken {
		t.Error("the retry did not say the reversal had already been taken")
	}
	if countReversals(t, h, f, receiptID) != 1 {
		t.Error("the retry posted a second reversing receipt")
	}
	if got := customerBalance(t, h, f); !amountsEqual(got, "115.00") {
		t.Errorf("balance = %q, want 115.00 — the retry reversed twice", got)
	}
}

func TestASecondReversalOfTheSameReceiptIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	if reverseReceipt(t, h, f, receiptID, newUUID().String()).StatusCode != 201 {
		t.Fatal("first reverse failed")
	}

	resp := reverseReceipt(t, h, f, receiptID, newUUID().String())
	if resp.StatusCode != 409 {
		t.Fatalf("a second reversal returned %d, want 409: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if countReversals(t, h, f, receiptID) != 1 {
		t.Error("the refused second reversal still wrote a document")
	}
}

func TestAReversalCannotItselfBeReversed(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	created := reverseReceipt(t, h, f, receiptID, newUUID().String())
	if created.StatusCode != 201 {
		t.Fatalf("reverse: %s", readBody(t, created))
	}
	reversingID, _ := decodeJSON(t, created)["id"].(string)

	resp := reverseReceipt(t, h, f, reversingID, newUUID().String())
	if resp.StatusCode != 400 {
		t.Fatalf("reversing a reversal returned %d, want 400: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestAnUnknownReceiptCannotBeReversed(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	resp := reverseReceipt(t, h, f, newUUID().String(), newUUID().String())
	if resp.StatusCode != 404 {
		t.Fatalf("reversing a missing receipt returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestAReversalWithoutAnIdentifierIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")

	resp := h.do(t, "POST",
		f.path("/api/v1/receivables/receipts/"+receiptID+"/reverse"),
		f.token, map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("a reversal with no uuid returned %d, want 400: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestAnotherTenantsReceiptCannotBeReversed(t *testing.T) {
	h := newHarness(t)
	mine := seedSelling(t, h, "5000.00")
	theirs := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, theirs, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("their sale: %s", invoiceID)
	}
	theirReceipt := takePaymentReceipt(t, h, theirs, invoiceID, "115.00")

	resp := h.do(t, "POST",
		mine.path("/api/v1/receivables/receipts/"+theirReceipt+"/reverse"),
		mine.token, map[string]any{"uuid": newUUID().String()})
	if resp.StatusCode != 404 {
		t.Errorf("reversing another tenant's receipt with my company returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}

	resp = h.do(t, "POST",
		theirs.path("/api/v1/receivables/receipts/"+theirReceipt+"/reverse"),
		mine.token, map[string]any{"uuid": newUUID().String()})
	if resp.StatusCode != 404 {
		t.Errorf("reversing another tenant's receipt naming their company returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}

	if countReversals(t, h, theirs, theirReceipt) != 0 {
		t.Error("another tenant's reversal still wrote a document")
	}
}

func TestAnAuditorCannotReverseAReceipt(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	auditor := h.seedUserIn(t, f.shopFixture, "auditor")

	resp := h.do(t, "POST",
		f.path("/api/v1/receivables/receipts/"+receiptID+"/reverse"),
		auditor, map[string]any{"uuid": newUUID().String()})
	if resp.StatusCode != 403 {
		t.Fatalf("an auditor reversed a receipt (%d): %s",
			resp.StatusCode, readBody(t, resp))
	}
	if countReversals(t, h, f, receiptID) != 0 {
		t.Error("the refused reversal still wrote a document")
	}
}

func TestAClosedPeriodRollsTheReversalBack(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	closeOpenPeriods(t, h, f)

	resp := reverseReceipt(t, h, f, receiptID, newUUID().String())
	if resp.StatusCode != 409 {
		t.Fatalf("reversing into a closed period returned %d, want 409: %s",
			resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(strings.ToLower(body), "closed") {
		t.Errorf("the refusal does not say the period is closed: %s", body)
	}
	if countReversals(t, h, f, receiptID) != 0 {
		t.Error("a failed reversal left a reversing receipt behind")
	}
	if got := customerBalance(t, h, f); !amountsEqual(got, "0.00") {
		t.Errorf("balance after the rolled-back reversal = %q, want 0.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the rolled-back reversal left C9.3 out by %s", d)
	}
}

func TestTheOriginalReceiptCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	id := uuid.MustParse(receiptID)

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE customer_receipt SET amount = amount + 1 WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Fatal("the original receipt's amount was edited")
	}

	err = h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `DELETE FROM customer_receipt WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Fatal("the original receipt was deleted")
	}
}

func TestAReversedReceiptCanBeTakenAgain(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takePaymentReceipt(t, h, f, invoiceID, "115.00")
	if reverseReceipt(t, h, f, receiptID, newUUID().String()).StatusCode != 201 {
		t.Fatal("reverse failed")
	}

	takePayment(t, h, f, invoiceID, "115.00", 201)
	if got := customerBalance(t, h, f); !amountsEqual(got, "0.00") {
		t.Errorf("balance after taking the payment again = %q, want 0.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("C9.3 after retaking is out by %s", d)
	}
}

func TestAReceiptUUIDCannotBeReusedAsAReversal(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}

	receiptUUID := newUUID().String()
	resp := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token,
		map[string]any{
			"uuid": receiptUUID, "customer_id": f.customerID, "method": "cash",
			"allocations": []map[string]any{
				{"invoice_id": invoiceID, "amount": "115.00"},
			},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("receipt: %s", readBody(t, resp))
	}
	receiptID, _ := decodeJSON(t, resp)["id"].(string)

	stolen := reverseReceipt(t, h, f, receiptID, receiptUUID)
	if stolen.StatusCode != 409 {
		t.Fatalf("reusing the receipt uuid as a reversal returned %d, want 409: %s",
			stolen.StatusCode, readBody(t, stolen))
	}
}

// --- helpers -------------------------------------------------------------

func takePaymentReceipt(
	t *testing.T, h *harness, f *arFixture, invoiceID, amount string,
) string {
	t.Helper()
	resp := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token,
		map[string]any{
			"uuid": newUUID().String(), "customer_id": f.customerID,
			"method": "cash",
			"allocations": []map[string]any{
				{"invoice_id": invoiceID, "amount": amount},
			},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("receipt of %s: %s", amount, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	if id == "" {
		t.Fatal("the receipt had no id")
	}
	return id
}

func reverseReceipt(
	t *testing.T, h *harness, f *arFixture, receiptID, docUUID string,
) *http.Response {
	t.Helper()
	return h.do(t, "POST",
		f.path("/api/v1/receivables/receipts/"+receiptID+"/reverse"),
		f.token, map[string]any{"uuid": docUUID})
}

func customerBalance(t *testing.T, h *harness, f *arFixture) string {
	t.Helper()
	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	got, _ := body["balance"].(string)
	return got
}

func invoiceOutstanding(t *testing.T, h *harness, f *arFixture, invoiceID string) string {
	t.Helper()
	var out string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT outstanding::text FROM customer_open_invoices($1)
			WHERE invoice_id = $2`, f.companyID, invoiceID).Scan(&out)
	}); err != nil {
		t.Fatalf("outstanding: %v", err)
	}
	return out
}

type receiptFacts struct {
	Number  string
	Method  string
	Amount  string
	UUID    string
	EntryID string
	Allocs  string
}

func snapshotReceipt(t *testing.T, h *harness, f *arFixture, receiptID string) receiptFacts {
	t.Helper()
	var facts receiptFacts
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			SELECT receipt_number, method, round(amount,2)::text, uuid::text,
			       journal_entry_id::text
			FROM customer_receipt WHERE id = $1`, receiptID).
			Scan(&facts.Number, &facts.Method, &facts.Amount, &facts.UUID, &facts.EntryID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(string_agg(invoice_id::text || ':' || round(amount,2)::text, ','
			                           ORDER BY invoice_id), '')
			FROM customer_receipt_allocation WHERE receipt_id = $1`,
			receiptID).Scan(&facts.Allocs)
	}); err != nil {
		t.Fatalf("snapshot the receipt: %v", err)
	}
	return facts
}

func countReversals(t *testing.T, h *harness, f *arFixture, receiptID string) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM customer_receipt WHERE reverses_id = $1`,
			receiptID).Scan(&n)
	}); err != nil {
		t.Fatalf("count reversals: %v", err)
	}
	return n
}

func shopStockOnHand(t *testing.T, h *harness, f *arFixture) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT stock_on_hand($1,$2)`,
			f.variantID, f.warehouseID).Scan(&qty)
	}); err != nil {
		t.Fatalf("stock_on_hand: %v", err)
	}
	return qty
}

func closeOpenPeriods(t *testing.T, h *harness, f *arFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE fiscal_period
			SET state = 'closed', closed_at = now(), closed_by = $2
			WHERE company_id = $1 AND state = 'open'`, f.companyID, f.userID)
		return e
	}); err != nil {
		t.Fatalf("close the period: %v", err)
	}
}

func receiptRoleMoves(
	t *testing.T, tx pgx.Tx, companyID uuid.UUID, receiptID string,
) (cash, ar decimal.Decimal) {
	t.Helper()
	if err := tx.QueryRow(t.Context(), `
		SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN customer_receipt cr ON cr.journal_entry_id = e.id
		JOIN account_role_map m ON m.account_id = l.account_id
		WHERE cr.id = $1 AND m.role = 'cash' AND m.company_id = $2`,
		receiptID, companyID).Scan(&cash); err != nil {
		t.Fatalf("cash move: %v", err)
	}
	if err := tx.QueryRow(t.Context(), `
		SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN customer_receipt cr ON cr.journal_entry_id = e.id
		JOIN account_role_map m ON m.account_id = l.account_id
		WHERE cr.id = $1 AND m.role = 'accounts_receivable' AND m.company_id = $2`,
		receiptID, companyID).Scan(&ar); err != nil {
		t.Fatalf("ar move: %v", err)
	}
	return cash, ar
}

func mustString(v any) string {
	s, _ := v.(string)
	return s
}
