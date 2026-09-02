//go:build integration

// A campaign that gives money away has to record that it did.
//
// `promotions.Redeem` writes `promotion_redemption` and its comment has always
// said it is "called inside the transaction that finalises the sale". Nothing
// called it. Because `Quote` enforces `max_uses` and `max_uses_per_customer` by
// COUNTING rows in that table, every limit counted zero for ever: a coupon good
// for one use per customer could be used without limit, and every campaign
// reported as having cost nothing.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// livePromotion creates an active percentage campaign and returns its id.
//
// Written straight to the table rather than through the route because the
// subject here is what a SALE does with a campaign, not how one is created —
// `promotions` has its own tests for that.
// A cap needs a coupon: 0084 constrains max_uses/max_uses_per_customer to
// coupon promotions, because "an automatic promotion is not redeemed, it just
// applies". So a capped campaign here gets a code, and the till quotes it.
func livePromotion(t *testing.T, h *harness, f *shopFixture, maxPerCustomer *int) (uuid.UUID, string) {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	coupon := ""
	if maxPerCustomer != nil {
		coupon = "CPN" + uuid.NewString()[:6]
	}

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO promotion
			  (tenant_id, company_id, code, name, kind, value,
			   starts_on, ends_on, is_active, max_uses_per_customer, coupon_code)
			VALUES ($1,$2,$3,'Ten per cent off','percentage',10,
			        current_date - 1, current_date + 30, true, $4, nullif($5,''))
			RETURNING id`,
			f.tenantID, f.companyID, "P"+uuid.NewString()[:8], maxPerCustomer, coupon).
			Scan(&id)
	}); err != nil {
		t.Fatalf("create promotion: %v", err)
	}
	return id, coupon
}

// saleWithPromotion is a one-line sale whose discount names a campaign.
func saleWithPromotion(
	f *shopFixture, promotionID uuid.UUID, customerID *uuid.UUID,
) map[string]any {
	line := map[string]any{
		"variant_id":    f.variantID.String(),
		"description":   "Abaya",
		"qty":           "1",
		"unit_price":    "115.00",
		"line_discount": "15.00",
		"promotion_id":  promotionID.String(),
		"tax_treatment": "standard",
	}
	sale := map[string]any{
		"invoice_uuid": uuid.NewString(),
		"doc_type":     "simplified",
		"issued_at":    "2026-08-15T10:30:00Z",
		"lines":        []map[string]any{line},
		"tenders":      []map[string]any{{"method": "cash", "amount": "100.00"}},
	}
	if customerID != nil {
		sale["customer_id"] = customerID.String()
	}
	return sale
}

func redemptionCount(t *testing.T, h *harness, f *shopFixture, promotionID uuid.UUID) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM promotion_redemption WHERE promotion_id = $1`,
			promotionID).Scan(&n)
	}); err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	return n
}

// A sale that applies a campaign records the redemption.
//
// The defect this closes: it recorded nothing, so nobody could say what a
// campaign had cost and no usage limit could ever be reached.
func TestASaleRecordsTheCampaignItRedeemed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	promo, _ := livePromotion(t, h, f, nil)

	if n := redemptionCount(t, h, f, promo); n != 0 {
		t.Fatalf("the campaign already has %d redemptions", n)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		saleWithPromotion(f, promo, nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}

	if n := redemptionCount(t, h, f, promo); n != 1 {
		t.Errorf("redemptions = %d, want 1", n)
	}
}

// The redemption records what was given away, against the invoice that gave it.
//
// A count alone would not answer the question a campaign is judged on — what
// did it cost — and a redemption that could not be traced to its invoice could
// not be checked against one.
func TestARedemptionCarriesItsDiscountAndItsInvoice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	promo, _ := livePromotion(t, h, f, nil)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		saleWithPromotion(f, promo, nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	var discount string
	var gotInvoice *uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT discount::text, invoice_id FROM promotion_redemption
			WHERE promotion_id = $1`, promo).Scan(&discount, &gotInvoice)
	}); err != nil {
		t.Fatalf("read redemption: %v", err)
	}

	if gotInvoice == nil || gotInvoice.String() != invoiceID {
		t.Errorf("redemption points at %v, want invoice %s", gotInvoice, invoiceID)
	}
	// The line discount, not the invoice-level one: an invoice discount is the
	// shop's own reduction and belongs to no campaign.
	if discount != "15.0000" && discount != "15.00" && discount != "15" {
		t.Errorf("discount recorded as %q, want 15", discount)
	}
}

// A one-per-customer coupon is refused the second time that customer quotes it.
//
// This is the assertion the whole change exists for. `Quote` counts
// redemptions, so before this the count was always zero and the limit never
// bit.
func TestAOnePerCustomerCampaignStopsAfterTheFirstSale(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	once := 1
	promo, coupon := livePromotion(t, h, f, &once)
	customer := customerOfType(t, h, f, "retail")

	first := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		saleWithPromotion(f, promo, &customer))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first sale: %d %s", first.StatusCode, readBody(t, first))
	}
	if n := redemptionCount(t, h, f, promo); n != 1 {
		t.Fatalf("redemptions after the first sale = %d, want 1", n)
	}

	// The till asks what this customer may have now. The campaign is spent.
	quote := h.do(t, http.MethodPost, "/api/v1/promotions/quote", f.token,
		map[string]any{
			"customer_id": customer.String(),
			"coupon_code": coupon,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(),
				"qty":        "1",
				"unit_price": "115.00",
			}},
		})
	defer quote.Body.Close()
	if quote.StatusCode != http.StatusOK {
		t.Fatalf("quote: %d %s", quote.StatusCode, readBody(t, quote))
	}

	if body := readBody(t, quote); strings.Contains(body, promo.String()) {
		t.Errorf("a one-per-customer campaign was offered again after being "+
			"used: %s", body)
	}
}

// A sale with no campaign records nothing and needs no promotions service.
//
// The common case. It must stay free of the machinery above, and it must not
// start writing empty redemptions.
func TestAnOrdinarySaleRecordsNoRedemption(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}

	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM promotion_redemption WHERE tenant_id = $1`,
			f.tenantID).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("an ordinary sale wrote %d redemptions", n)
	}
}
