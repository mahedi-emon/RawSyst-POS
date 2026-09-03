//go:build integration

// G2's realised currency gain and loss.
//
// A shop buys stock for 1,000 USD when a dollar is worth 3.70 riyals. The
// payable is 3,700 riyals — what the books say it owes, and what the stock
// cost. Two months later it pays the 1,000 USD when a dollar costs 3.80, and
// 3,800 riyals leave the bank.
//
// Debit payable 3,700, credit bank 3,800, and the entry does not balance. The
// missing 100 is neither a rounding error nor a new cost of goods: the stock
// cost what it cost. It is a loss taken by owing money in a currency that
// moved, and 0114 gives it its own account so an owner sees it rather than
// finding their margins mysteriously worse.
//
// Before 0113 and 0114 none of this could happen at all: every bill was forced
// to the company's own currency and every rate in the repository was 1.
package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// usdRate records a USD→SAR rate for a day.
func usdRate(t *testing.T, h *harness, f *buyingFixture, rate, day string) {
	t.Helper()
	resp := h.do(t, http.MethodPut, "/api/v1/exchange-rates", f.token,
		map[string]any{
			"from_currency": "USD", "to_currency": "SAR", "rate": rate,
			"as_of": day, "source": "Test Bank",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("record rate %s on %s: %d %s", rate, day, resp.StatusCode,
			readBody(t, resp))
	}
}

// dollarBill raises and bills 10 units at 100 USD, on the given bill date.
func dollarBill(t *testing.T, h *harness, f *buyingFixture, billDate string) string {
	t.Helper()
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		}).Body.Close()

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"currency":     "USD", "bill_date": billDate,
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0",
			}},
		})
	defer billed.Body.Close()
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %d %s", billed.StatusCode, readBody(t, billed))
	}
	id, _ := decodeJSON(t, billed)["id"].(string)
	return id
}

// payBill settles an amount of the bill's currency on a day.
func payBill(
	t *testing.T, h *harness, f *buyingFixture, billID, amount, paidOn string,
) *http.Response {
	t.Helper()
	return h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"method": "bank_transfer", "paid_on": paidOn,
			"allocations": []map[string]any{
				{"bill_id": billID, "amount": amount},
			},
		})
}

// Paying a foreign bill when the currency has moved against you is a loss.
func TestPayingAForeignBillAtAWorseRateRealisesALoss(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.70", "2026-08-01")
	usdRate(t, h, f, "3.80", "2026-08-20")

	billID := dollarBill(t, h, f, "2026-08-01") // 1,000 USD at 3.70 = 3,700

	paid := payBill(t, h, f, billID, "1000.00", "2026-08-25")
	defer paid.Body.Close()
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %d %s", paid.StatusCode, readBody(t, paid))
	}

	// 1,000 USD cost 3,800 riyals to buy; the payable was carried at 3,700.
	if got := roleBalance(t, h, f.shopFixture, "fx_loss"); !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("realised loss = %s, want 100 — the payable was carried at "+
			"3,700 and 3,800 left the bank", got)
	}
	if got := roleBalance(t, h, f.shopFixture, "fx_gain"); !got.IsZero() {
		t.Errorf("a gain of %s was recognised on a loss-making settlement", got)
	}
}

// And a favourable move is a gain, kept out of sales revenue.
func TestPayingAForeignBillAtABetterRateRealisesAGain(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.80", "2026-08-01")
	usdRate(t, h, f, "3.70", "2026-08-20")

	billID := dollarBill(t, h, f, "2026-08-01") // 1,000 USD at 3.80 = 3,800

	paid := payBill(t, h, f, billID, "1000.00", "2026-08-25")
	defer paid.Body.Close()
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %d %s", paid.StatusCode, readBody(t, paid))
	}

	// A credit balance on a revenue account reads negative here.
	if got := roleBalance(t, h, f.shopFixture, "fx_gain"); !got.Equal(decimal.NewFromInt(-100)) {
		t.Errorf("realised gain = %s, want -100 (a credit)", got)
	}
	// A favourable exchange rate is not trading and must not swell turnover.
	if got := roleBalance(t, h, f.shopFixture, "sales_revenue"); !got.IsZero() {
		t.Errorf("an exchange gain reached sales revenue: %s", got)
	}
}

// Settling at exactly the rate the bill was booked at realises nothing.
//
// The control: an engine that always posted something would satisfy both tests
// above and be wrong every day the rate held.
func TestSettlingAtTheBookedRateRealisesNoFX(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.75", "2026-08-01")
	billID := dollarBill(t, h, f, "2026-08-01")

	paid := payBill(t, h, f, billID, "1000.00", "2026-08-25")
	defer paid.Body.Close()
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %d %s", paid.StatusCode, readBody(t, paid))
	}

	if got := roleBalance(t, h, f.shopFixture, "fx_loss"); !got.IsZero() {
		t.Errorf("a loss of %s on a settlement at the booked rate", got)
	}
	if got := roleBalance(t, h, f.shopFixture, "fx_gain"); !got.IsZero() {
		t.Errorf("a gain of %s on a settlement at the booked rate", got)
	}
}

// A partial settlement realises only its own share.
func TestAPartialSettlementRealisesOnlyItsShare(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.70", "2026-08-01")
	usdRate(t, h, f, "3.80", "2026-08-20")
	billID := dollarBill(t, h, f, "2026-08-01")

	// A quarter of the bill.
	paid := payBill(t, h, f, billID, "250.00", "2026-08-25")
	defer paid.Body.Close()
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %d %s", paid.StatusCode, readBody(t, paid))
	}

	if got := roleBalance(t, h, f.shopFixture, "fx_loss"); !got.Equal(decimal.NewFromInt(25)) {
		t.Errorf("realised loss = %s, want 25 — a tenth of a riyal on each of "+
			"250 dollars, not the whole bill's 100", got)
	}
}

// Several settlements of one bill realise the difference once each, and no
// more.
//
// The double-recognition test. Four quarter payments at the same worse rate
// must come to exactly the loss a single full payment would have realised.
func TestMultipleSettlementsDoNotDoubleRecogniseFX(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.70", "2026-08-01")
	usdRate(t, h, f, "3.80", "2026-08-20")
	billID := dollarBill(t, h, f, "2026-08-01")

	for i := 0; i < 4; i++ {
		paid := payBill(t, h, f, billID, "250.00", "2026-08-25")
		if paid.StatusCode != 201 {
			t.Fatalf("payment %d: %d %s", i+1, paid.StatusCode,
				readBody(t, paid))
		}
		paid.Body.Close()
	}

	if got := roleBalance(t, h, f.shopFixture, "fx_loss"); !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("realised loss over four settlements = %s, want exactly 100 "+
			"— the same as one settlement of the whole bill", got)
	}
}

// The journal balances, in base currency, on a settlement that realised FX.
//
// The invariant the whole mechanism rests on. A three-legged entry whose legs
// were computed from two different rates is exactly where an imbalance would
// hide, and the ledger must never carry one.
func TestAnFXSettlementLeavesABalancedJournal(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.70", "2026-08-01")
	usdRate(t, h, f, "3.80", "2026-08-20")
	billID := dollarBill(t, h, f, "2026-08-01")

	paid := payBill(t, h, f, billID, "1000.00", "2026-08-25")
	defer paid.Body.Close()
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %d %s", paid.StatusCode, readBody(t, paid))
	}

	var debits, credits decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit), 0), coalesce(sum(l.base_credit), 0)
			  FROM journal_line l
			  JOIN journal_entry e ON e.id = l.entry_id
			 WHERE e.company_id = $1 AND e.source_type = 'supplier_payment'`,
			f.companyID).Scan(&debits, &credits)
	}); err != nil {
		t.Fatalf("read the entry: %v", err)
	}

	if !debits.Equal(credits) {
		t.Errorf("the settlement entry is out of balance: %s debits against "+
			"%s credits", debits, credits)
	}
	// And it is the right size: 3,700 payable plus 100 loss against 3,800 out.
	if !debits.Equal(decimal.NewFromInt(3800)) {
		t.Errorf("debits = %s, want 3800", debits)
	}
}

// Concurrent settlements of one bill cannot overdraw it.
//
// The bill is locked FOR UPDATE while its outstanding balance is read and
// reduced, so eight tills paying the last 250 dollars at once settle it once.
func TestConcurrentSettlementsCannotOverpayABill(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	usdRate(t, h, f, "3.70", "2026-08-01")
	usdRate(t, h, f, "3.80", "2026-08-20")
	billID := dollarBill(t, h, f, "2026-08-01")

	const payers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < payers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := payBill(t, h, f, billID, "1000.00", "2026-08-25")
			defer resp.Body.Close()
			if resp.StatusCode == 201 {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if accepted != 1 {
		t.Errorf("%d of %d concurrent payments of the whole bill were "+
			"accepted, want 1", accepted, payers)
	}
	if got := roleBalance(t, h, f.shopFixture, "fx_loss"); !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("realised loss = %s, want 100 — a second settlement would "+
			"have recognised the difference twice", got)
	}
}

// One tenant's rates do not price another tenant's bill.
func TestAForeignBillNeedsItsOwnTenantsRate(t *testing.T) {
	h := newHarness(t)
	mine := seedBuying(t, h)
	theirs := seedBuying(t, h)

	// Only the first tenant records a rate.
	usdRate(t, h, mine, "3.75", "2026-08-01")

	poID, lineID := raiseOrder(t, h, theirs, "10", "100.00")
	h.do(t, "POST", theirs.path("/api/v1/purchasing/receipts"), theirs.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		}).Body.Close()

	billed := h.do(t, "POST", theirs.path("/api/v1/purchasing/bills"),
		theirs.token, map[string]any{
			"uuid": newUUID(), "supplier_id": theirs.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"currency":     "USD", "bill_date": "2026-08-01",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0",
			}},
		})
	defer billed.Body.Close()
	if billed.StatusCode == 201 {
		t.Error("a foreign bill was booked using another tenant's exchange " +
			"rate, or at par")
	}
}

// A bill in a currency with no rate on file is refused, not booked at par.
func TestAForeignBillWithNoRateIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	poID, lineID := raiseOrder(t, h, f, "10", "100.00")
	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		}).Body.Close()

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"currency":     "EUR", "bill_date": "2026-08-01",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0",
			}},
		})
	defer billed.Body.Close()
	if billed.StatusCode == 201 {
		t.Fatal("a bill in a currency with no rate on file was booked anyway")
	}
	if body := readBody(t, billed); !containsFold(body, "exchange rate") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
}

// containsFold is a case-insensitive substring check.
func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		len(needle) > 0 &&
		indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

var _ = pgx.ErrNoRows
