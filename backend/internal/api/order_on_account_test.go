//go:build integration

// Billing an order to a customer's account, when its prices exclude tax (B11,
// B12).
//
// The defect: `orders.Invoice` built the `customer_due` tender itself by adding
// the line amounts up, which is the NET figure. The sale's total is the
// tax-inclusive one, computed inside the sales engine after the tax profile has
// been applied from the registry. So an order quoted net of tax and billed to
// an account was refused with
//
//	The payments come to 200 against a total of 230, a difference of -30
//
// naming payments the caller never sent, and short by exactly the VAT. Every
// wholesale order priced the way wholesale is normally priced hit it, and the
// message pointed nowhere near the cause. The amount is now stated by whoever
// computed the total.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// orderForCustomer raises an order that somebody can owe, and walks it to
// delivered.
func orderForCustomer(
	t *testing.T, h *harness, f *arFixture, qty, price string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders"), f.token,
		map[string]any{
			"channel":     "store",
			"customer_id": f.customerID,
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": qty,
					"unit_price": price},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("raise an order: %d %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSONFrom(t, resp)["id"].(string)
	if id == "" {
		t.Fatal("the raised order has no id")
	}
	for i := range 4 { // confirmed, processing, packed, delivered
		adv := h.do(t, http.MethodPost,
			f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
		if adv.StatusCode != http.StatusOK {
			t.Fatalf("advance %d: %d %s", i+1, adv.StatusCode, readBody(t, adv))
		}
		adv.Body.Close()
	}
	return id
}

// owed is what the customer's account carries, credits negative.
func owed(t *testing.T, h *harness, f *arFixture) decimal.Decimal {
	t.Helper()
	var out decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			  FROM journal_line l
			  JOIN account_role_map m ON m.account_id = l.account_id
			 WHERE m.company_id = $1 AND m.role = 'accounts_receivable'`,
			f.companyID).Scan(&out)
	}); err != nil {
		t.Fatalf("read the receivable: %v", err)
	}
	return out
}

// An order priced net of tax can be billed to an account.
//
// Two at a hundred, fifteen per cent: the customer owes 230, not 200, and the
// invoice is raised rather than refused.
func TestAnOrderPricedWithoutTaxCanBeBilledToAnAccount(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100000.00")
	id := orderForCustomer(t, h, f, "2", "100.00")

	before := owed(t, h, f)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/invoice"),
		f.token, map[string]any{
			"uuid":               newUUID().String(),
			"prices_include_tax": false,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("bill an order to an account: %d %s",
			resp.StatusCode, readBody(t, resp))
	}

	// The receivable is the tax-INCLUSIVE total. Booking the net figure would
	// leave the shop chasing 200 for an invoice it issued for 230, and the
	// ledger would not tie out.
	got := owed(t, h, f).Sub(before)
	if !got.Equal(decimal.RequireFromString("230")) {
		t.Errorf("the customer was put on the hook for %s, want 230 — the "+
			"total the invoice was issued for, tax and all", got)
	}
}

// The same order billed tax-inclusively still owes what it is worth.
//
// Guards the other direction: the fix must not double-count tax on the path
// that already worked, which is the one the till and the back office both take
// by default.
func TestAnOrderPricedWithTaxOwesTheSamePriceItQuoted(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100000.00")
	id := orderForCustomer(t, h, f, "2", "115.00")

	before := owed(t, h, f)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/invoice"),
		f.token, map[string]any{"uuid": newUUID().String()})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("bill an order to an account: %d %s",
			resp.StatusCode, readBody(t, resp))
	}

	got := owed(t, h, f).Sub(before)
	if !got.Equal(decimal.RequireFromString("230")) {
		t.Errorf("the customer owes %s, want 230 — the quoted price, which "+
			"already included the tax", got)
	}
}

// A payment stated by the caller is still the caller's to state.
//
// The engine fills in the amount only when nothing was tendered. A sale settled
// partly in cash and partly on account states both, and neither is guessed.
func TestAStatedPaymentIsNotOverriddenByTheOnAccountPath(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "100000.00")
	id := orderForCustomer(t, h, f, "2", "100.00")

	before := owed(t, h, f)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/invoice"),
		f.token, map[string]any{
			"uuid":               newUUID().String(),
			"prices_include_tax": false,
			"tenders": []map[string]any{
				{"method": "cash", "amount": "230.00"},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("bill an order paid in cash: %d %s",
			resp.StatusCode, readBody(t, resp))
	}

	if got := owed(t, h, f).Sub(before); !got.IsZero() {
		t.Errorf("a sale paid in cash moved the receivable by %s, want 0", got)
	}
}
