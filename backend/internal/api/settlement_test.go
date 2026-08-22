//go:build integration

// Card settlement over HTTP (P15, blueprint C12, design 02 §8).
//
// The blueprint's sentence is the whole test plan: "A customer pays SAR 1,000
// by card, but the bank deposits only SAR 985 two days later. Without this
// module the books never balance and the Owner never knows their real card
// cost." So: the clearing account must return to zero for the payments a
// deposit covered, the difference must land in an expense account somebody can
// point at, and a payment must never settle twice.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// settlingShop is a shop that can sell by card and reconcile a bank statement.
//
// The chart comes from provisioning.SeedChartOfAccounts rather than being
// listed here by hand. That is deliberate and is the lesson of 0048 and 0052: a
// test that maps `bank` and `bank_card_charges` itself proves the rule and says
// nothing about whether any company created through the product can post it,
// which is exactly how the cost_variance mapping stayed broken through a green
// suite.
func settlingShop(t *testing.T, h *harness) (*shopFixture, string) {
	t.Helper()
	f := h.seedShop(t, "cashier")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed the chart: %v", err)
	}
	return f, h.seedUserIn(t, f, "owner")
}

// sellByCard rings up one item paid by Mada and returns the invoice id.
func sellByCard(t *testing.T, h *harness, f *shopFixture, price string) string {
	t.Helper()
	body := oneItemSale(f, uuid.New(), "1", price, price)
	body["tenders"] = []map[string]any{{"method": "mada", "amount": price}}

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("card sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["invoice_id"].(string)
	return id
}

func settlementPath(f *shopFixture, base string) string {
	return base + "?company_id=" + f.companyID.String()
}

// pendingTenders reads what the shop is owed by its acquirer.
func pendingTenders(t *testing.T, h *harness, f *shopFixture, token string) []any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/pending"), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pending settlement: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	return rows
}

// The whole point of the module, in one test.
func TestADepositClearsTheCardAccountAndBooksTheFee(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	sellByCard(t, h, f, "1000.00")

	// Taken, and not yet the shop's to spend.
	if got := roleBalance(t, h, f, "card_clearing"); !got.Equal(decimal.RequireFromString("1000")) {
		t.Fatalf("card clearing after the sale = %s, want 1000", got)
	}

	pending := pendingTenders(t, h, f, owner)
	if len(pending) != 1 {
		t.Fatalf("%d payments awaiting settlement, want 1", len(pending))
	}
	tenderID, _ := pending[0].(map[string]any)["tender_id"].(string)

	// The bank deposits 985 two days later. The blueprint's own numbers.
	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "MADA-20260817-001",
			"deposited_on": "2026-08-17",
			"net_amount":   "985.00",
			"tender_ids":   []string{tenderID},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	batch := decodeJSON(t, resp)

	if batch["gross_amount"] != "1000.00" || batch["fee_amount"] != "15.00" ||
		batch["net_amount"] != "985.00" {
		t.Errorf("batch = gross %v, fee %v, net %v; want 1000.00 / 15.00 / 985.00",
			batch["gross_amount"], batch["fee_amount"], batch["net_amount"])
	}

	// The three balances that make the blueprint's sentence false.
	if got := roleBalance(t, h, f, "card_clearing"); !got.IsZero() {
		t.Errorf("card clearing after settlement = %s, want 0 — money the shop "+
			"has been paid must not sit in a clearing account forever", got)
	}
	if got := roleBalance(t, h, f, "bank"); !got.Equal(decimal.RequireFromString("985")) {
		t.Errorf("bank = %s, want 985", got)
	}
	if got := roleBalance(t, h, f, "bank_card_charges"); !got.Equal(decimal.RequireFromString("15")) {
		t.Errorf("card charges = %s, want 15 — the owner's real card cost", got)
	}

	// And the payment itself is marked, with its share of the fee, so a
	// margin-per-method report has something to read.
	var status, fee string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT settlement_status, coalesce(fee_amount, 0)::text
			FROM sales_tender WHERE id = $1`, tenderID).Scan(&status, &fee)
	}); err != nil {
		t.Fatalf("read the tender: %v", err)
	}
	if status != "settled" {
		t.Errorf("tender status = %q, want settled", status)
	}
	if !decimal.RequireFromString(fee).Equal(decimal.RequireFromString("15")) {
		t.Errorf("tender fee = %s, want 15", fee)
	}
}

// Recording the same deposit twice records it once.
//
// The bank statement is keyed by hand or imported by a job that may run twice;
// either way a lost response must not clear the same payments into a second
// journal entry, which would take the clearing account negative.
func TestRecordingADepositTwiceRecordsItOnce(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "500.00")

	tenderID, _ := pendingTenders(t, h, f, owner)[0].(map[string]any)["tender_id"].(string)
	body := map[string]any{
		"uuid":         uuid.NewString(),
		"reference":    "MADA-20260817-002",
		"deposited_on": "2026-08-17",
		"net_amount":   "492.50",
		"tender_ids":   []string{tenderID},
	}

	first := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first: status %d — %s", first.StatusCode, readBody(t, first))
	}
	firstID, _ := decodeJSON(t, first)["id"].(string)

	second := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry: status %d, want 200 — %s", second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("a retried deposit was not marked as a replay")
	}
	again := decodeJSON(t, second)
	if again["id"] != firstID {
		t.Errorf("retry produced a different batch: %v then %v", firstID, again["id"])
	}

	var entries int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM journal_entry
			WHERE source_type = 'settlement_batch'`).Scan(&entries)
	}); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if entries != 1 {
		t.Errorf("%d settlement journal entries, want 1", entries)
	}
	if got := roleBalance(t, h, f, "card_clearing"); !got.IsZero() {
		t.Errorf("card clearing = %s after a retried deposit, want 0", got)
	}
}

// A payment settles once, and the second attempt says so plainly.
func TestAPaymentCannotBeInTwoDeposits(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "300.00")

	tenderID, _ := pendingTenders(t, h, f, owner)[0].(map[string]any)["tender_id"].(string)
	deposit := func(net string) *http.Response {
		return h.do(t, http.MethodPost,
			settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
				"uuid":         uuid.NewString(),
				"reference":    "MADA-" + uuid.NewString()[:8],
				"deposited_on": "2026-08-17",
				"net_amount":   net,
				"tender_ids":   []string{tenderID},
			})
	}

	if resp := deposit("295.00"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first deposit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	resp := deposit("295.00")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("the same payment in a second deposit: status %d, want 409 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// It is also gone from the pending list, which is what stops somebody
	// picking it a second time in the first place.
	if rows := pendingTenders(t, h, f, owner); len(rows) != 0 {
		t.Errorf("%d payments still awaiting settlement after all were deposited", len(rows))
	}
}

// A deposit larger than the payments it covers is refused, not absorbed.
func TestADepositCannotExceedThePaymentsItCovers(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "100.00")

	tenderID, _ := pendingTenders(t, h, f, owner)[0].(map[string]any)["tender_id"].(string)
	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "MADA-OVER",
			"deposited_on": "2026-08-17",
			"net_amount":   "120.00",
			"tender_ids":   []string{tenderID},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an over-large deposit: status %d, want 400 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Nothing was written. An acquirer paying more than was taken is a
	// different event, and half-recording it would leave a negative charge
	// buried in an expense account.
	if got := roleBalance(t, h, f, "card_clearing"); !got.Equal(decimal.RequireFromString("100")) {
		t.Errorf("card clearing = %s after a refused deposit, want 100 untouched", got)
	}
}

// Cash is never awaiting settlement, and cannot be deposited as a batch.
//
// It never debited the clearing account, so crediting it would put the account
// negative by the value of every cash sale in the shop.
func TestCashIsNeverAwaitingSettlement(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	// A cash sale and a card sale, so the list has something to get wrong.
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, uuid.New(), "1", "50.00", "50.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("cash sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	sellByCard(t, h, f, "80.00")

	rows := pendingTenders(t, h, f, owner)
	if len(rows) != 1 {
		t.Fatalf("%d payments awaiting settlement, want only the card one", len(rows))
	}
	if method, _ := rows[0].(map[string]any)["method"].(string); method != "mada" {
		t.Errorf("the pending payment is a %s", method)
	}

	// And naming a cash tender explicitly is refused rather than obeyed.
	var cashTender string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT t.id::text FROM sales_tender t
			JOIN sales_invoice i ON i.id = t.invoice_id
			WHERE i.company_id = $1 AND t.method = 'cash' LIMIT 1`,
			f.companyID).Scan(&cashTender)
	}); err != nil {
		t.Fatalf("find the cash tender: %v", err)
	}

	resp = h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "NOT-A-DEPOSIT",
			"deposited_on": "2026-08-17",
			"net_amount":   "50.00",
			"tender_ids":   []string{cashTender},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("depositing cash: status %d, want 400 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// The fee split across a batch sums back to the fee on the statement.
//
// Three payments and a fee that does not divide cleanly, which is the ordinary
// case. Each share is rounded and the last takes the remainder — the same rule
// the invoice discount, the FIFO layer draw and the shortfall settlement use.
func TestTheFeeSplitSumsBackToTheFeeCharged(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	for _, price := range []string{"33.33", "66.67", "100.00"} {
		sellByCard(t, h, f, price)
	}

	pending := pendingTenders(t, h, f, owner)
	if len(pending) != 3 {
		t.Fatalf("%d payments awaiting settlement, want 3", len(pending))
	}
	ids := make([]string, 0, 3)
	for _, row := range pending {
		id, _ := row.(map[string]any)["tender_id"].(string)
		ids = append(ids, id)
	}

	// 200.00 taken, 196.99 deposited: a fee of 3.01, which no proportional
	// split of these three amounts lands on evenly.
	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "MADA-SPLIT",
			"deposited_on": "2026-08-17",
			"net_amount":   "196.99",
			"tender_ids":   ids,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	batch := decodeJSON(t, resp)
	if batch["fee_amount"] != "3.01" {
		t.Fatalf("fee = %v, want 3.01", batch["fee_amount"])
	}

	tenders, _ := batch["tenders"].([]any)
	shares := decimal.Zero
	for _, raw := range tenders {
		row, _ := raw.(map[string]any)
		fee, _ := row["fee_amount"].(string)
		shares = shares.Add(decimal.RequireFromString(fee))
	}
	if !shares.Equal(decimal.RequireFromString("3.01")) {
		t.Errorf("the per-payment fees come to %s against a charge of 3.01. "+
			"Rounding each share independently loses a hallala and the "+
			"margin-per-method report stops agreeing with the P&L", shares)
	}

	if got := roleBalance(t, h, f, "card_clearing"); !got.IsZero() {
		t.Errorf("card clearing = %s, want 0", got)
	}
}

// One company cannot deposit another's takings, even inside one tenant.
func TestADepositCannotReachAnotherCompanysPayments(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	other, otherOwner := settlingShop(t, h)

	sellByCard(t, h, other, "70.00")
	theirs, _ := pendingTenders(t, h, other, otherOwner)[0].(map[string]any)["tender_id"].(string)

	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "WRONG-COMPANY",
			"deposited_on": "2026-08-17",
			"net_amount":   "68.00",
			"tender_ids":   []string{theirs},
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("settling another company's payment: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if got := roleBalance(t, h, other, "card_clearing"); !got.Equal(decimal.RequireFromString("70")) {
		t.Errorf("the other company's clearing account moved to %s", got)
	}
}

// QA gate M7: reconciling a bank statement is not a counter job.
func TestSettlementNeedsAccountingPermissions(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "40.00")

	// A cashier takes the money and does not decide it has arrived.
	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/pending"), f.token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cashier reading pending settlement: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// An auditor reads everything and changes nothing.
	auditor := h.seedUserIn(t, f, "auditor")
	resp = h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), auditor, map[string]any{
			"uuid": uuid.NewString(), "reference": "X",
			"deposited_on": "2026-08-17", "net_amount": "38.00",
			"tender_ids": []string{uuid.NewString()},
		})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("auditor recording a deposit: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// The owner can do both, or the gate is just broken rather than strict.
	if rows := pendingTenders(t, h, f, owner); len(rows) != 1 {
		t.Errorf("%d payments awaiting settlement for the owner, want 1", len(rows))
	}
}

// --- reading a deposit back ----------------------------------------------

// A recorded deposit reads back with the sales it covered.
//
// This route existed, was mounted, was declared, and had never once been
// executed: coverage on settlement.Read was zero, and the only tests touching
// it proved that it declares a permission and refuses an anonymous caller.
// Both of those pass whether or not the handler works.
func TestARecordedDepositReadsBackWithItsSales(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	for _, price := range []string{"120.00", "80.00"} {
		sellByCard(t, h, f, price)
	}
	pending := pendingTenders(t, h, f, owner)
	if len(pending) != 2 {
		t.Fatalf("%d payments awaiting settlement, want 2", len(pending))
	}
	ids := make([]string, 0, 2)
	for _, row := range pending {
		id, _ := row.(map[string]any)["tender_id"].(string)
		ids = append(ids, id)
	}

	recorded := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "MADA-READBACK",
			"deposited_on": "2026-08-18",
			"net_amount":   "197.00",
			"tender_ids":   ids,
		})
	if recorded.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: status %d — %s",
			recorded.StatusCode, readBody(t, recorded))
	}
	batchID, _ := decodeJSON(t, recorded)["id"].(string)

	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches/"+batchID), owner, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read the deposit back: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	got := decodeJSON(t, resp)

	if got["id"] != batchID {
		t.Errorf("id = %v, want %s", got["id"], batchID)
	}
	if got["reference"] != "MADA-READBACK" {
		t.Errorf("reference = %v", got["reference"])
	}
	if got["deposited_on"] != "2026-08-18" {
		t.Errorf("deposited_on = %v, want the date it landed", got["deposited_on"])
	}
	// 200.00 taken, 197.00 deposited, so the acquirer kept 3.00.
	if got["gross_amount"] != "200.00" || got["fee_amount"] != "3.00" ||
		got["net_amount"] != "197.00" {
		t.Errorf("amounts = gross %v, fee %v, net %v; want 200.00 / 3.00 / 197.00",
			got["gross_amount"], got["fee_amount"], got["net_amount"])
	}

	// The sales it covered, each with the share of the fee it bore. Without
	// these the batch reconciles to nothing and QA gate M4's "the batch
	// reconciles back to individual sales" is a forensic exercise again.
	tenders, _ := got["tenders"].([]any)
	if len(tenders) != 2 {
		t.Fatalf("%d payments in the batch, want 2 — %v", len(tenders), got["tenders"])
	}
	shares := decimal.Zero
	covered := decimal.Zero
	for _, raw := range tenders {
		row, _ := raw.(map[string]any)
		amount, _ := row["amount"].(string)
		fee, _ := row["fee_amount"].(string)
		if row["invoice_number"] == nil {
			t.Error("a payment in the batch names no invoice")
		}
		if method, _ := row["method"].(string); method != "mada" {
			t.Errorf("payment method = %q, want mada", method)
		}
		covered = covered.Add(decimal.RequireFromString(amount))
		shares = shares.Add(decimal.RequireFromString(fee))
	}
	if !covered.Equal(decimal.RequireFromString("200")) {
		t.Errorf("the payments come to %s against a gross of 200.00", covered)
	}
	if !shares.Equal(decimal.RequireFromString("3")) {
		t.Errorf("the per-payment fees come to %s against a charge of 3.00", shares)
	}
}

// A deposit that does not exist is not found, rather than empty or a crash.
func TestReadingADepositThatDoesNotExist(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches/"+uuid.NewString()), owner, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown deposit: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// One company cannot read another's deposit, and is told it does not exist.
//
// Both companies here belong to DIFFERENT tenants, so row-level security is one
// of the two things being checked; the company predicate in the query is the
// other, and it is the one that matters when two companies share a tenant.
func TestADepositCannotBeReadByAnotherCompany(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	other, otherOwner := settlingShop(t, h)

	sellByCard(t, h, other, "60.00")
	theirs, _ := pendingTenders(t, h, other, otherOwner)[0].(map[string]any)["tender_id"].(string)

	recorded := h.do(t, http.MethodPost,
		settlementPath(other, "/api/v1/settlement/batches"), otherOwner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "THEIR-DEPOSIT",
			"deposited_on": "2026-08-18",
			"net_amount":   "58.00",
			"tender_ids":   []string{theirs},
		})
	if recorded.StatusCode != http.StatusCreated {
		t.Fatalf("seed the other company's deposit: status %d — %s",
			recorded.StatusCode, readBody(t, recorded))
	}
	theirBatch, _ := decodeJSON(t, recorded)["id"].(string)

	// Naming their batch id under our own company. Not found, not forbidden:
	// confirming it exists would tell an outsider what to look for next.
	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches/"+theirBatch), owner, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("reading another company's deposit: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// And it is still readable by the company it belongs to, or the check above
	// would pass simply because nothing is readable.
	resp = h.do(t, http.MethodGet,
		settlementPath(other, "/api/v1/settlement/batches/"+theirBatch), otherOwner, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the owning company reading its own deposit: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M7 on the read route as well as the write one.
func TestReadingADepositNeedsAccountingView(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "45.00")

	tenderID, _ := pendingTenders(t, h, f, owner)[0].(map[string]any)["tender_id"].(string)
	recorded := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    "MADA-GATED",
			"deposited_on": "2026-08-18",
			"net_amount":   "44.00",
			"tender_ids":   []string{tenderID},
		})
	if recorded.StatusCode != http.StatusCreated {
		t.Fatalf("record: status %d — %s", recorded.StatusCode, readBody(t, recorded))
	}
	batchID, _ := decodeJSON(t, recorded)["id"].(string)

	// A cashier takes the money and does not read the bank reconciliation.
	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches/"+batchID), f.token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cashier reading a deposit: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// An auditor reads everything and changes nothing, so this one they may.
	auditor := h.seedUserIn(t, f, "auditor")
	resp = h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches/"+batchID), auditor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auditor reading a deposit: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}
