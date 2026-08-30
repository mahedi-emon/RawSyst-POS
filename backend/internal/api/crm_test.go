//go:build integration

// Loyalty, store credit and gift cards (blueprint B16).
//
// The test this file exists for is the first one. `store_credit` and
// `loyalty_points` have been accepted TENDERS since 0018 and there was never a
// balance behind either: a cashier could settle a sale with credit a customer
// had never been given, the sale posted, and account 2300 went negative with
// nobody told. Every other test here is about that money staying honest once it
// exists — spent once, not twice; earned on a sale, taken back on a return; and
// a gift card that is a liability rather than revenue.
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

type crmFixture struct {
	*shopFixture
	customerID uuid.UUID
}

func seedCRM(t *testing.T, h *harness) *crmFixture {
	t.Helper()
	f := h.seedShop(t, "owner")

	// The shop fixture builds its chart by hand, which is how account 2300 came
	// to be reachable as a tender with nothing mapped to explain it. The full
	// chart is idempotent over the hand-built one, so this adds what is missing
	// — Store Credit Issued, the two loyalty accounts, Sales Discounts — and
	// changes nothing that was already there.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(
			t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed the chart: %v", err)
	}

	// A real customer. `shopFixture.businessID` is declared and never filled,
	// so every foreign key pointed at the nil UUID and every write was refused
	// with "a referenced record does not exist".
	out := &crmFixture{shopFixture: f}
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO customer
			  (tenant_id, company_id, code, name, customer_type, vat_number)
			VALUES ($1,$2,'C-CRM','Layla Haddad','retail','310000000000003')
			RETURNING id`, f.tenantID, f.companyID).Scan(&out.customerID)
	}); err != nil {
		t.Fatalf("seed a customer: %v", err)
	}
	return out
}

func (f *crmFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

func (f *crmFixture) balance(
	t *testing.T, h *harness, code string,
) decimal.Decimal {
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

// ledger snapshots what a set of accounts holds now, so a test can assert what
// its own actions MOVED. The shop fixture already trades — it opens a till with
// a float and puts stock on the shelf — so an absolute balance would be a
// figure about the fixture rather than about the thing under test.
func (f *crmFixture) ledger(
	t *testing.T, h *harness, codes ...string,
) func(code string) decimal.Decimal {
	t.Helper()
	before := map[string]decimal.Decimal{}
	for _, c := range codes {
		before[c] = f.balance(t, h, c)
	}
	return func(code string) decimal.Decimal {
		return f.balance(t, h, code).Sub(before[code])
	}
}

// creditSale is a sale settled wholly out of stored value.
func creditSale(f *crmFixture, method, reference, amount string) map[string]any {
	sale := oneItemSale(f.shopFixture, uuid.New(), "1", amount, amount)
	sale["doc_type"] = "standard"
	sale["customer_id"] = f.customerID.String()
	tender := map[string]any{"method": method, "amount": amount}
	if reference != "" {
		tender["reference"] = reference
	}
	sale["tenders"] = []map[string]any{tender}
	return sale
}

// --- the hole this module closes ------------------------------------------

// The one that mattered.
//
// Before this, the sale went through: the journal debited Store Credit Issued
// because the posting rule said to, the liability went negative, and the only
// place it showed up was a balance sheet nobody reads daily.
func TestASaleCannotSpendStoreCreditNobodyHas(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	moved := f.ledger(t, h, "2300")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", "", "115.00"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a sale settled with credit nobody has returned %d, want 409. "+
			"Body: %s", resp.StatusCode, readBody(t, resp))
	}

	// And nothing was written. A refused sale that still moved the liability
	// would be the same bug wearing a different error message.
	if got := moved("2300"); !got.IsZero() {
		t.Errorf("Store Credit Issued moved by %s on a refused sale",
			got.StringFixed(2))
	}
}

// The message has to carry the numbers. A cashier standing in front of a
// customer cannot act on "insufficient funds".
func TestARefusedRedemptionSaysHowMuchThereIs(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	f.give(t, h, "40.00")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", "", "115.00"))
	body := readBody(t, resp)
	if !strings.Contains(body, "40.00") || !strings.Contains(body, "115.00") {
		t.Errorf("the refusal should name both figures, got: %s", body)
	}
}

// Store credit belongs to somebody. A sale that does not say who cannot spend
// it, and the old code would have posted it against nobody.
func TestAnAnonymousSaleCannotSpendStoreCredit(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)

	sale := oneItemSale(f.shopFixture, uuid.New(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{
		{"method": "store_credit", "amount": "115.00"},
	}
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != http.StatusUnprocessableEntity &&
		resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an anonymous store-credit sale returned %d, want a refusal: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- credit that does exist -----------------------------------------------

func TestCreditGivenCanBeSpentOnceAndIsThenGone(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	f.give(t, h, "115.00")

	first := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", "", "115.00"))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("spending credit that exists returned %d: %s",
			first.StatusCode, readBody(t, first))
	}

	if got := f.wallet(t, h); got != "0.00" {
		t.Errorf("the wallet holds %s after being spent in full, want 0.00", got)
	}

	// The second attempt is the whole point of a balance.
	second := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", "", "115.00"))
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("the same credit was spent twice: status %d — %s",
			second.StatusCode, readBody(t, second))
	}
}

// Credit given costs the shop and does not touch revenue: it is a discount the
// customer has not taken yet.
func TestGivingCreditCostsTheShopRatherThanEarningIt(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	moved := f.ledger(t, h, "2300", "4200", "4100")
	f.give(t, h, "50.00")

	if got := moved("2300"); !got.Equal(decimal.RequireFromString("-50")) {
		t.Errorf("Store Credit Issued should be credited 50, not %s",
			got.StringFixed(2))
	}
	if got := moved("4200"); !got.Equal(decimal.RequireFromString("50")) {
		t.Errorf("Sales Discounts should carry the cost, not %s", got.StringFixed(2))
	}
	if got := moved("4100"); !got.IsZero() {
		t.Errorf("giving credit is not a sale: Sales Revenue moved by %s",
			got.StringFixed(2))
	}
}

// --- gift cards -----------------------------------------------------------

// Selling a gift card takes money and owes goods. Booking it as revenue would
// overstate the month it was sold in, charge VAT twice, and leave the
// redemption with nothing to settle against.
func TestSellingAGiftCardIsMoneyOwed(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	moved := f.ledger(t, h, "1100", "2300", "4100", "2200")
	card := f.issueCard(t, h, "200.00", true)

	if card["balance"] != "200.00" {
		t.Errorf("a new card is worth %v, want 200.00", card["balance"])
	}
	if got := moved("1100"); !got.Equal(decimal.RequireFromString("200")) {
		t.Errorf("Cash should have taken 200, not %s", got.StringFixed(2))
	}
	if got := moved("2300"); !got.Equal(decimal.RequireFromString("-200")) {
		t.Errorf("Store Credit Issued should owe 200, not %s", got.StringFixed(2))
	}
	if got := moved("4100"); !got.IsZero() {
		t.Fatalf("selling a gift card booked %s of revenue. The month it was "+
			"sold in is overstated and the VAT on it is charged twice.",
			got.StringFixed(2))
	}
	if got := moved("2200"); !got.IsZero() {
		t.Errorf("no VAT is due on issuing a gift card, got %s", got.StringFixed(2))
	}
}

// A card is a piece of plastic that can be handed to two people.
func TestAGiftCardCannotBeSpentTwice(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	card := f.issueCard(t, h, "115.00", true)
	code, _ := card["code"].(string)

	first := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", code, "115.00"))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("spending a card returned %d: %s",
			first.StatusCode, readBody(t, first))
	}

	second := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "store_credit", code, "115.00"))
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("the same card was spent twice: status %d — %s",
			second.StatusCode, readBody(t, second))
	}
}

// A cancelled card's balance is money the shop no longer owes, and the ledger
// has to hear about it on the day it stopped owing it.
func TestCancellingACardWritesItsBalanceBack(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	moved := f.ledger(t, h, "2300")
	card := f.issueCard(t, h, "200.00", true)
	id, _ := card["id"].(string)

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/gift-cards/"+id+"/void"), f.token,
		map[string]any{"reason": "Reported lost by the customer"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("void: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["balance"]; got != "0.00" {
		t.Errorf("a cancelled card holds %v, want 0.00", got)
	}
	if got := moved("2300"); !got.IsZero() {
		t.Errorf("the liability should be back to nothing, not %s",
			got.StringFixed(2))
	}
}

// A cashier types the number off a card with a queue behind them.
func TestACardIsFoundByTheNumberOnIt(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	card := f.issueCard(t, h, "75.00", true)
	code, _ := card["code"].(string)

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/gift-cards/by-code/"+code), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("look up by code: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["balance"]; got != "75.00" {
		t.Errorf("looked up card holds %v, want 75.00", got)
	}
}

// --- points ---------------------------------------------------------------

// B16's example: 100 spent earns 1 point. A 115 sale earns one, not one and a
// sixth — the shop never owes a fraction of a point.
func TestPointsAreEarnedOnASaleAndRoundedDown(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	f.setProgram(t, h, "100", "1", nil)
	moved := f.ledger(t, h, "2400", "5800")

	sale := oneItemSale(f.shopFixture, uuid.New(), "1", "115.00", "115.00")
	sale["doc_type"] = "standard"
	sale["customer_id"] = f.customerID.String()
	if resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale); resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	if got := f.points(t, h); got != 1 {
		t.Errorf("a 115.00 sale earned %d points, want 1", got)
	}
	// A point is a real obligation, so it is on the balance sheet the moment it
	// is earned rather than the moment somebody spends it.
	if got := moved("2400"); !got.Equal(decimal.RequireFromString("-1")) {
		t.Errorf("Loyalty Points Liability should owe 1, not %s", got.StringFixed(2))
	}
	if got := moved("5800"); !got.Equal(decimal.RequireFromString("1")) {
		t.Errorf("Loyalty Points Cost should carry 1, not %s", got.StringFixed(2))
	}
}

func TestPointsCannotBeSpentBeyondWhatIsHeld(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	f.setProgram(t, h, "100", "1", nil)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		creditSale(f, "loyalty_points", "", "115.00"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("spending points nobody has returned %d, want 409: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Buy, earn, return, keep the points is free money. C14 lists loyalty as one of
// the two effects a return always forgets, and it was right until now.
func TestReturningGoodsTakesBackThePointsTheyEarned(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	f.setProgram(t, h, "10", "1", nil)

	invoiceUUID := uuid.New()
	sale := oneItemSale(f.shopFixture, invoiceUUID, "1", "115.00", "115.00")
	sale["doc_type"] = "standard"
	sale["customer_id"] = f.customerID.String()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read the line being returned: %v", err)
	}

	earned := f.points(t, h)
	if earned != 11 {
		t.Fatalf("115.00 at 10 a point earned %d, want 11", earned)
	}

	ret := h.do(t, http.MethodPost, "/api/v1/pos/returns", f.token,
		map[string]any{
			"credit_note_uuid":    uuid.New().String(),
			"original_invoice_id": invoiceID,
			"issued_at":           "2026-08-16T10:30:00Z",
			"reason":              "Did not fit",
			"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
			"refunds": []map[string]any{
				{"method": "cash", "amount": "115.00"},
			},
		})
	if ret.StatusCode != http.StatusCreated {
		t.Fatalf("return: status %d — %s", ret.StatusCode, readBody(t, ret))
	}

	if got := f.points(t, h); got != 0 {
		t.Fatalf("the customer kept %d points on goods they handed back", got)
	}
}

// --- fitting history ------------------------------------------------------

// Staff need to know what the customer is NOW. Two rows for shirts would make
// them do the reading.
func TestASizeIsCorrectedRatherThanAdded(t *testing.T) {
	h := newHarness(t)
	f := seedCRM(t, h)
	path := f.path("/api/v1/customers/" + f.customerID.String() + "/sizes")

	h.do(t, http.MethodPut, path, f.token,
		map[string]any{"garment": "Shirt", "size": "L"})
	resp := h.do(t, http.MethodPut, path, f.token,
		map[string]any{
			"garment":      "shirt",
			"size":         "XL",
			"measurements": map[string]string{"collar": "17", "unit": "in"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("record a size: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}

	sizes, _ := decodeJSON(t, resp)["data"].([]any)
	if len(sizes) != 1 {
		t.Fatalf("%d sizes recorded for one garment, want 1", len(sizes))
	}
	row, _ := sizes[0].(map[string]any)
	if row["size"] != "XL" {
		t.Errorf("the shirt size reads %v, want the corrected XL", row["size"])
	}
}

// --- helpers --------------------------------------------------------------

func (f *crmFixture) give(t *testing.T, h *harness, amount string) {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/wallets/"+f.customerID.String()+"/credit"), f.token,
		map[string]any{"amount": amount, "note": "Goodwill after a late delivery"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("give credit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
}

func (f *crmFixture) wallet(t *testing.T, h *harness) string {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/wallets/"+f.customerID.String()), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read wallet: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	balance, _ := decodeJSON(t, resp)["balance"].(string)
	return balance
}

func (f *crmFixture) issueCard(
	t *testing.T, h *harness, faceValue string, paid bool,
) map[string]any {
	t.Helper()
	body := map[string]any{"face_value": faceValue}
	if paid {
		body["proceeds"] = []map[string]any{
			{"role": "cash", "amount": faceValue},
		}
	}
	resp := h.do(t, http.MethodPost, f.path("/api/v1/gift-cards"), f.token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue a card: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

func (f *crmFixture) setProgram(
	t *testing.T, h *harness, spendPerPoint, pointValue string, tiers []map[string]any,
) {
	t.Helper()
	resp := h.do(t, http.MethodPut, f.path("/api/v1/loyalty/program"), f.token,
		map[string]any{
			"is_active":       true,
			"spend_per_point": spendPerPoint,
			"point_value":     pointValue,
			"tiers":           tiers,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set the scheme: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func (f *crmFixture) points(t *testing.T, h *harness) int {
	t.Helper()
	var points int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(points), 0)::int
			FROM loyalty_entry WHERE customer_id = $1`,
			f.customerID).Scan(&points)
	}); err != nil {
		t.Fatalf("read points: %v", err)
	}
	return points
}
