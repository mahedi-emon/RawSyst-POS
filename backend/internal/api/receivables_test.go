//go:build integration

package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// Customers and receivables.
//
// The invariant every test here exists to protect, from design 02 §6.6 quoting
// C9.3:
//
//	SUM(customer open balances) == Accounts Receivable control balance
//
// And the rule from 11-pos-and-sales.md §5: customer_due "is refused when it
// would breach the customer's credit limit (B16)". Refused, not warned — so the
// tests assert a rejection, not a flag.

// --- fixture -------------------------------------------------------------

type arFixture struct {
	*shopFixture
	customerID string
}

// seedSelling gives a shop a customer with a credit account.
func seedSelling(t *testing.T, h *harness, limit string) *arFixture {
	t.Helper()
	f := h.seedShop(t, "owner")

	// The full chart, for its Accounts Receivable CONTROL account. Without one
	// there is nothing for C9.3 to tie back to.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed chart: %v", err)
	}

	body := map[string]any{
		"code": "CUST1", "name": "Al Noor Trading",
		"customer_type": "wholesale", "payment_terms_days": 30,
	}
	if limit != "" {
		body["credit_limit"] = limit
	}
	created := h.do(t, "POST",
		"/api/v1/customers?company_id="+f.companyID.String(), f.token, body)
	if created.StatusCode != 201 {
		t.Fatalf("customer: %s", readBody(t, created))
	}
	id, _ := decodeJSON(t, created)["id"].(string)

	return &arFixture{shopFixture: f, customerID: id}
}

func (a *arFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + a.companyID.String()
}

// receivableTieOut is C9.3 as a number, through the same SQL function the
// nightly job and a support engineer would use.
func receivableTieOut(t *testing.T, h *harness, f *arFixture) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT receivable_gl_difference($1)`, f.companyID).Scan(&d)
	}); err != nil {
		t.Fatalf("receivable tie-out: %v", err)
	}
	return d
}

// sellOnAccount rings a sale where `onAccount` of `price` goes on the customer's
// account and the rest is cash.
func sellOnAccount(
	t *testing.T, h *harness, f *arFixture, price, onAccount, cash string,
) (string, int) {
	t.Helper()
	sale := oneItemSale(f.shopFixture, newUUID(), "1", price, cash)
	sale["customer_id"] = f.customerID

	tenders := []map[string]any{{"method": "customer_due", "amount": onAccount}}
	if cash != "0.00" && cash != "" {
		tenders = append(tenders, map[string]any{"method": "cash", "amount": cash})
	}
	sale["tenders"] = tenders

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != 201 {
		return readBody(t, resp), resp.StatusCode
	}
	id, _ := decodeJSON(t, resp)["invoice_id"].(string)
	return id, resp.StatusCode
}

// --- The invariant -------------------------------------------------------

// The whole point. It must hold at every step, not only at the end.
func TestTheReceivableTieOutHoldsThroughTheWholeCycle(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("a shop with no sales already owes itself %s", d)
	}

	// A sale entirely on account.
	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale on account: %s", invoiceID)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("after a sale on account the receivable is out by %s", d)
	}

	// A split sale: half cash, half on account. The receivable must be the
	// SECOND half only. Deriving it from the invoice total would show 230 here
	// against a control balance of 115.
	if _, s := sellOnAccount(t, h, f, "200.00", "100.00", "100.00"); s != 201 {
		t.Fatal("split sale was refused")
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("after a split payment the receivable is out by %s", d)
	}

	// A part payment.
	takePayment(t, h, f, invoiceID, "50.00", 201)
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("after a part payment the receivable is out by %s", d)
	}

	// And the balance the customer sees must be what is left.
	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "165.00") {
		t.Errorf("balance = %q, want 165.00 (115 + 100 - 50)", got)
	}
}

// A return taken off the account credits AR. If the derived receivable ignores
// it, C9.3 fails by the refunded amount — which is exactly what it did before
// migration 0036.
func TestAReturnOnAccountComesOffWhatTheCustomerOwes(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}

	lineID := firstLineOf(t, h, f, invoiceID)
	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T11:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds": []map[string]any{
			{"method": "customer_due", "amount": "115.00"},
		},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return to account: %s", readBody(t, resp))
	}

	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("after a return on account the receivable is out by %s", d)
	}

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["balance"].(string); !amountsEqual(got, "0.00") {
		t.Errorf("balance after a full return on account = %q, want 0.00", got)
	}
}

// Nobody's account is not an account. Crediting a cash sale to `customer_due`
// would credit the control with no balance behind it.
func TestAReturnCannotBeTakenOffAnAccountThatDoesNotExist(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	// A plain cash sale, no customer.
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f.shopFixture, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("cash sale: %s", readBody(t, created))
	}
	invoiceID, _ := decodeJSON(t, created)["invoice_id"].(string)
	lineID := firstLineOf(t, h, f, invoiceID)

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T11:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds": []map[string]any{
			{"method": "customer_due", "amount": "115.00"},
		},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("crediting a cash sale to an account returned %d, want 400: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Fatalf("the refused return still moved the receivable, by %s", d)
	}
}

// --- The credit limit ----------------------------------------------------

// B16, and 11-pos-and-sales.md §5: refused, not warned.
func TestASaleBreachingTheCreditLimitIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "200.00")

	if _, s := sellOnAccount(t, h, f, "150.00", "150.00", "0.00"); s != 201 {
		t.Fatal("a sale within the limit was refused")
	}

	// 150 owed, limit 200. Another 100 would reach 250.
	body, status := sellOnAccount(t, h, f, "100.00", "100.00", "0.00")
	if status != 409 {
		t.Fatalf("a sale past the limit returned %d, want 409: %s", status, body)
	}
	// The refusal has to say the numbers. A cashier standing in front of a
	// customer cannot act on "denied".
	for _, want := range []string{"150.00", "200.00", "250.00"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not mention %s: %s", want, body)
		}
	}

	// And the refused sale must have moved nothing.
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the refused sale moved the receivable by %s", d)
	}
	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := after["balance"].(string); !amountsEqual(got, "150.00") {
		t.Errorf("balance after a refused sale = %q, want 150.00", got)
	}
}

// A refused sale must not consume an ICV. The counter cannot be handed back and
// a gap in the ZATCA chain is permanent, so the credit check has to run before
// the chain is touched.
func TestARefusedSaleDoesNotConsumeAChainPosition(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100.00")

	before := chainCount(t, h, f)
	if _, s := sellOnAccount(t, h, f, "500.00", "500.00", "0.00"); s != 409 {
		t.Fatalf("a sale far past the limit was not refused (got %d)", s)
	}
	if after := chainCount(t, h, f); after != before {
		t.Errorf("a refused sale consumed %d chain positions", after-before)
	}
}

// No limit recorded means no credit at all. A customer record somebody typed in
// a hurry at the counter must not come with an unlimited account attached.
func TestACustomerWithNoLimitCannotBuyOnAccount(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "")

	body, status := sellOnAccount(t, h, f, "10.00", "10.00", "0.00")
	if status != 409 {
		t.Fatalf("a sale on account with no limit returned %d, want 409: %s",
			status, body)
	}
	if !strings.Contains(body, "no credit account") {
		t.Errorf("the refusal does not explain there is no account: %s", body)
	}
}

// A receivable nobody owes cannot be collected.
func TestASaleOnAccountMustNameACustomer(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	sale := oneItemSale(f.shopFixture, newUUID(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{
		{"method": "customer_due", "amount": "115.00"},
	}
	// No customer_id.
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != 400 {
		t.Fatalf("an anonymous sale on account returned %d, want 400: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- Receipts ------------------------------------------------------------

// The same receipt arriving twice takes the money once. Same discipline as a
// sale, a return and a supplier payment.
func TestARetriedReceiptTakesTheMoneyOnce(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}

	receiptUUID := newUUID().String()
	body := map[string]any{
		"uuid": receiptUUID, "customer_id": f.customerID, "method": "cash",
		"allocations": []map[string]any{
			{"invoice_id": invoiceID, "amount": "40.00"},
		},
	}

	first := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token, body)
	if first.StatusCode != 201 {
		t.Fatalf("first receipt: %s", readBody(t, first))
	}
	number, _ := decodeJSON(t, first)["receipt_number"].(string)

	second := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token, body)
	if second.StatusCode != 200 {
		t.Fatalf("the retry returned %d, want 200: %s",
			second.StatusCode, readBody(t, second))
	}
	replay := decodeJSON(t, second)
	if again, _ := replay["receipt_number"].(string); again != number {
		t.Errorf("the retry issued receipt %q, not the original %q", again, number)
	}
	if taken, _ := replay["already_taken"].(bool); !taken {
		t.Error("the retry did not say the payment had already been taken")
	}

	// One payment, not two.
	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := after["balance"].(string); !amountsEqual(got, "75.00") {
		t.Errorf("balance = %q, want 75.00 — the retry took the money twice", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("after a retried receipt the receivable is out by %s", d)
	}
}

// Allocating more than an invoice owes takes money against a document that does
// not justify it.
func TestAReceiptCannotBeAllocatedPastWhatAnInvoiceOwes(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	takePayment(t, h, f, invoiceID, "500.00", 400)

	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the refused receipt moved the receivable by %s", d)
	}
}

// A receipt posts through Rule 8, which had been seeded since 0025 with no
// caller. Debit where the money arrived, credit accounts receivable.
func TestAReceiptPostsThroughTheSeededRule(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	takePayment(t, h, f, invoiceID, "115.00", 201)

	var rule string
	var cash, receivable decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			SELECT e.rule_key
			FROM journal_entry e
			JOIN customer_receipt cr ON cr.journal_entry_id = e.id
			WHERE cr.company_id = $1`, f.companyID).Scan(&rule); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account_role_map m ON m.account_id = l.account_id
			WHERE e.source_type = 'customer_receipt' AND m.role = 'cash'
			  AND m.company_id = $1`, f.companyID).Scan(&cash); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account_role_map m ON m.account_id = l.account_id
			WHERE e.source_type = 'customer_receipt'
			  AND m.role = 'accounts_receivable' AND m.company_id = $1`,
			f.companyID).Scan(&receivable)
	}); err != nil {
		t.Fatalf("read the posting: %v", err)
	}

	if rule != "payment.customer" {
		t.Errorf("a receipt posted through %q, not payment.customer", rule)
	}
	if !cash.Equal(decimal.RequireFromString("115")) {
		t.Errorf("cash moved %s, want a debit of 115", cash)
	}
	if !receivable.Equal(decimal.RequireFromString("-115")) {
		t.Errorf("receivable moved %s, want a credit of 115", receivable)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("after settling in full the receivable is out by %s", d)
	}
}

// --- The ledger and ageing -----------------------------------------------

// The khata: every charge and every receipt, in order, with a running balance a
// customer can follow.
func TestTheLedgerRunsChargesAndReceiptsInOneColumn(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}
	takePayment(t, h, f, invoiceID, "40.00", 201)

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID+"/ledger"), f.token, nil))

	rows, _ := body["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("the ledger has %d rows, want 2 (a sale and a receipt)", len(rows))
	}

	sale, _ := rows[0].(map[string]any)
	if kind, _ := sale["kind"].(string); kind != "sale" {
		t.Errorf("the first row is %q, want the sale", kind)
	}
	if got, _ := sale["balance"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("running balance after the sale = %q, want 115.00", got)
	}

	receipt, _ := rows[1].(map[string]any)
	if kind, _ := receipt["kind"].(string); kind != "receipt" {
		t.Errorf("the second row is %q, want the receipt", kind)
	}
	if got, _ := receipt["balance"].(string); !amountsEqual(got, "75.00") {
		t.Errorf("running balance after the receipt = %q, want 75.00", got)
	}

	if got, _ := body["closing"].(string); !amountsEqual(got, "75.00") {
		t.Errorf("closing = %q, want 75.00", got)
	}
}

// Ageing runs from the DUE date, not the issue date. On 30-day terms an invoice
// raised today is not overdue, and ageing from issue would put it in a chasing
// queue it does not belong in.
func TestAgeingRunsFromTheDueDateNotTheIssueDate(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	if _, s := sellOnAccount(t, h, f, "115.00", "115.00", "0.00"); s != 201 {
		t.Fatal("sale was refused")
	}

	// The sale is issued 2026-08-15 on 30-day terms, so it falls due 2026-09-14.
	notYet := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/receivables/ageing")+"&as_of=2026-09-01", f.token, nil))
	row := firstAgeingRow(t, notYet)
	if got, _ := row["not_due"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("on 2026-09-01 not_due = %q, want the whole 115.00", got)
	}
	if got, _ := row["days_0_30"].(string); !amountsEqual(got, "0.00") {
		t.Errorf("an invoice not yet due is aged into 0-30: %q", got)
	}

	// Ten days past due.
	overdue := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/receivables/ageing")+"&as_of=2026-09-24", f.token, nil))
	row = firstAgeingRow(t, overdue)
	if got, _ := row["days_0_30"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("ten days past due, days_0_30 = %q, want 115.00", got)
	}

	// And well past.
	late := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/receivables/ageing")+"&as_of=2026-12-31", f.token, nil))
	row = firstAgeingRow(t, late)
	if got, _ := row["days_90_plus"].(string); !amountsEqual(got, "115.00") {
		t.Errorf("108 days past due, days_90_plus = %q, want 115.00", got)
	}
}

// --- Permissions and isolation -------------------------------------------

// Setting the ceiling on what somebody may owe is a different act from taking
// their money or recording their phone number.
func TestSettingACreditLimitNeedsItsOwnPermission(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "1000.00")

	// A store manager holds customers.manage but not customers.set_credit_limit.
	manager := h.seedUserIn(t, f.shopFixture, "store_manager")

	// They can edit the details.
	edit := h.do(t, "PUT", f.path("/api/v1/customers/"+f.customerID), manager,
		map[string]any{
			"name": "Al Noor Trading LLC", "customer_type": "wholesale",
			"payment_terms_days": 30,
		})
	if edit.StatusCode != 200 {
		t.Fatalf("a store manager could not edit a customer: %s", readBody(t, edit))
	}

	// They cannot move the ceiling.
	raise := h.do(t, "POST",
		f.path("/api/v1/customers/"+f.customerID+"/credit-limit"), manager,
		map[string]any{"credit_limit": "999999.00"})
	if raise.StatusCode != 403 {
		t.Fatalf("a store manager raised a credit limit (%d): %s",
			raise.StatusCode, readBody(t, raise))
	}

	// Nor sneak one in through creation.
	create := h.do(t, "POST", f.path("/api/v1/customers"), manager,
		map[string]any{
			"code": "SNEAK", "name": "Back Door", "credit_limit": "999999.00",
		})
	if create.StatusCode != 403 {
		t.Fatalf("a store manager created a customer with a limit (%d): %s",
			create.StatusCode, readBody(t, create))
	}

	// And the limit is unchanged.
	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))
	if got, _ := body["credit_limit"].(string); !amountsEqual(got, "1000.00") {
		t.Errorf("credit limit = %q, want the original 1000.00", got)
	}
}

// M8: another tenant's customer must read as absent, not as forbidden and not as
// an empty result.
func TestAnotherTenantsCustomerIsNotFound(t *testing.T) {
	h := newHarness(t)
	mine := seedSelling(t, h, "1000.00")
	theirs := seedSelling(t, h, "1000.00")

	// Their customer id, my token, my company: not found.
	resp := h.do(t, "GET",
		mine.path("/api/v1/customers/"+theirs.customerID), mine.token, nil)
	if resp.StatusCode != 404 {
		t.Errorf("reading another tenant's customer returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}

	// Their company id, my token: also not found, rather than an empty list
	// that looks like a company with no customers.
	resp = h.do(t, "GET",
		"/api/v1/customers?company_id="+theirs.companyID.String(), mine.token, nil)
	if resp.StatusCode != 404 {
		t.Errorf("listing another tenant's customers returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// A customer who owes money cannot be hidden. Retiring them would take the
// receivable out of every picker while leaving it in the control account.
func TestACustomerWhoOwesMoneyCannotBeRetired(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != 201 {
		t.Fatalf("sale: %s", invoiceID)
	}

	resp := h.do(t, "POST", f.path("/api/v1/customers/"+f.customerID+"/active"),
		f.token, map[string]any{"active": false})
	if resp.StatusCode != 409 {
		t.Fatalf("retiring a customer who owes money returned %d, want 409: %s",
			resp.StatusCode, readBody(t, resp))
	}

	// Settled, and now they can be.
	takePayment(t, h, f, invoiceID, "115.00", 201)
	resp = h.do(t, "POST", f.path("/api/v1/customers/"+f.customerID+"/active"),
		f.token, map[string]any{"active": false})
	if resp.StatusCode != 200 {
		t.Fatalf("retiring a settled customer failed: %s", readBody(t, resp))
	}
}

// The code appears on invoices already issued and signed, so it is not editable.
func TestACustomerCodeIsNotEditable(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "1000.00")

	resp := h.do(t, "PUT", f.path("/api/v1/customers/"+f.customerID), f.token,
		map[string]any{
			"code": "CHANGED", "name": "Al Noor Trading",
			"payment_terms_days": 30,
		})
	if resp.StatusCode != 200 {
		t.Fatalf("edit: %s", readBody(t, resp))
	}
	if got, _ := decodeJSON(t, resp)["code"].(string); got != "CUST1" {
		t.Errorf("code = %q, want the original CUST1 — it is on signed invoices", got)
	}
}

// --- helpers -------------------------------------------------------------

func takePayment(
	t *testing.T, h *harness, f *arFixture, invoiceID, amount string, want int,
) {
	t.Helper()
	resp := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token,
		map[string]any{
			"uuid": newUUID().String(), "customer_id": f.customerID,
			"method": "cash",
			"allocations": []map[string]any{
				{"invoice_id": invoiceID, "amount": amount},
			},
		})
	if resp.StatusCode != want {
		t.Fatalf("receipt of %s returned %d, want %d: %s",
			amount, resp.StatusCode, want, readBody(t, resp))
	}
}

func firstLineOf(t *testing.T, h *harness, f *arFixture, invoiceID string) string {
	t.Helper()
	var id string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id::text FROM sales_invoice_line
			 WHERE invoice_id = $1 ORDER BY line_no LIMIT 1`,
			invoiceID).Scan(&id)
	}); err != nil {
		t.Fatalf("read the invoice line: %v", err)
	}
	return id
}

func chainCount(t *testing.T, h *harness, f *arFixture) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM zatca_invoice WHERE tenant_id = $1`,
			f.tenantID).Scan(&n)
	}); err != nil {
		t.Fatalf("count the chain: %v", err)
	}
	return n
}

// seedUserIn adds a second user, with a different role, to a shop that already
// exists. seedUserWithRole builds a whole new tenant, which is the wrong tool
// for testing that two roles in the SAME company see different things.
func (h *harness) seedUserIn(t *testing.T, f *shopFixture, roleKey string) string {
	t.Helper()
	ctx := t.Context()

	email := "u" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] + "@example.test"
	hash, err := identity.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var userID uuid.UUID
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO app_user (tenant_id, email, full_name, password_hash, status)
			VALUES ($1,$2,'Second User',$3,'active') RETURNING id`,
			f.tenantID, email, hash).Scan(&userID)
	}); err != nil {
		t.Fatalf("add a %s to the shop: %v", roleKey, err)
	}

	// The role clone runs in tenant context: roles belong to the tenant and are
	// deliberately unreachable from the platform plane.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var roleID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name, cloned_from)
			SELECT $1, key, name, id FROM role
			WHERE tenant_id IS NULL AND key = $2
			RETURNING id`, f.tenantID, roleKey).Scan(&roleID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT $1, rp.permission FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = $2`, roleID, roleKey); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id)
			VALUES ($1,$2,$3)`, f.tenantID, userID, roleID)
		return e
	}); err != nil {
		t.Fatalf("assign %s: %v", roleKey, err)
	}

	return h.login(t, email)
}

func firstAgeingRow(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	rows, _ := body["rows"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the ageing report is empty: %v", body)
	}
	row, _ := rows[0].(map[string]any)
	return row
}
