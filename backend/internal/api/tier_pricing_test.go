//go:build integration

// Which of a product's prices a customer actually pays.
//
// B1 gives a product three sellable prices -- retail, wholesale and
// dealer/corporate -- and B16 gives a customer a type. The two were never
// joined: the till's scan returned `price_retail` unconditionally, so a
// wholesale customer was charged the retail price. `price_wholesale` was written
// only by the importer and read by nothing; `price_dealer` was read and written
// by nothing at all, while sitting in the schema implying a behaviour the
// product did not have.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pricedVariant gives the fixture's variant a barcode and a wholesale price.
func pricedVariant(t *testing.T, h *harness, f *shopFixture, retail, wholesale string) string {
	t.Helper()
	barcode := "628" + uuid.NewString()[:10]
	ctx := t.Context()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE variant
			SET barcode = $2, price_retail = $3::numeric,
			    price_wholesale = nullif($4,'')::numeric
			WHERE id = $1`, f.variantID, barcode, retail, wholesale)
		return e
	}); err != nil {
		t.Fatalf("price the variant: %v", err)
	}
	return barcode
}

// customerOfType creates a customer and returns its id.
func customerOfType(t *testing.T, h *harness, f *shopFixture, kind string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO customer (tenant_id, company_id, code, name, customer_type)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			f.tenantID, f.companyID, "C"+uuid.NewString()[:8],
			kind+" customer", kind).Scan(&id)
	}); err != nil {
		t.Fatalf("create %s customer: %v", kind, err)
	}
	return id
}

func scanPrice(t *testing.T, h *harness, f *shopFixture, barcode string, customer *uuid.UUID) string {
	t.Helper()
	url := "/api/v1/catalog/scan?barcode=" + barcode
	if customer != nil {
		url += "&customer_id=" + customer.String()
	}
	resp := h.do(t, http.MethodGet, url, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan: %d %s", resp.StatusCode, readBody(t, resp))
	}
	price, _ := decodeJSON(t, resp)["price"].(string)
	return price
}

// A wholesale customer is charged the wholesale price.
//
// The defect this closes: they were charged retail, and nothing anywhere said
// so -- the shop had entered a wholesale price and the product ignored it.
func TestAWholesaleCustomerIsChargedTheWholesalePrice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	barcode := pricedVariant(t, h, f, "100.0000", "80.0000")
	wholesale := customerOfType(t, h, f, "wholesale")

	if got := scanPrice(t, h, f, barcode, &wholesale); got != "80.0000" {
		t.Errorf("wholesale customer pays %s, want 80.0000", got)
	}
}

// A retail customer, and a walk-in with no customer at all, pay retail.
//
// The second case is the one that must not regress: most sales have no customer
// on them, and they behaved correctly before this change.
func TestRetailAndWalkInCustomersPayRetail(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	barcode := pricedVariant(t, h, f, "100.0000", "80.0000")
	retail := customerOfType(t, h, f, "retail")

	if got := scanPrice(t, h, f, barcode, &retail); got != "100.0000" {
		t.Errorf("retail customer pays %s, want 100.0000", got)
	}
	if got := scanPrice(t, h, f, barcode, nil); got != "100.0000" {
		t.Errorf("walk-in pays %s, want 100.0000", got)
	}
}

// A wholesale customer at a shop with no wholesale price set pays retail.
//
// `price_wholesale` is nullable and most shops will never fill it in. A null
// there means "not set" -- not free, and not unsellable.
func TestAWholesaleCustomerFallsBackToRetailWhenNoWholesalePriceIsSet(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	barcode := pricedVariant(t, h, f, "100.0000", "") // no wholesale price
	wholesale := customerOfType(t, h, f, "wholesale")

	if got := scanPrice(t, h, f, barcode, &wholesale); got != "100.0000" {
		t.Errorf("price = %s, want the retail 100.0000 as a fallback", got)
	}
}

// A VIP customer pays retail.
//
// Pinned deliberately, because it is a decision rather than an oversight. B1's
// price tiers are Retail / Wholesale / Dealer-Corporate and VIP is not among
// them -- it appears in B16 as a LOYALTY tier (Bronze/Silver/Gold/VIP) with
// "tier-based perks", which the loyalty and promotions engines already own.
// Charging a VIP the dealer price would be inventing a rule the Blueprint does
// not state.
func TestAVIPCustomerPaysRetailBecauseVIPIsNotAPriceTier(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	barcode := pricedVariant(t, h, f, "100.0000", "80.0000")
	vip := customerOfType(t, h, f, "vip")

	if got := scanPrice(t, h, f, barcode, &vip); got != "100.0000" {
		t.Errorf("VIP customer pays %s, want the retail 100.0000", got)
	}
}

// Naming another company's customer does not reprice the scan.
//
// The join is scoped to the variant's own company, so a customer id from
// elsewhere selects no row and the price stays retail. Without that scoping a
// caller could reach across a company boundary to price a sale -- inside their
// own tenant, where row-level security has no reason to object.
func TestACustomerFromAnotherCompanyDoesNotRepriceTheScan(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	other := h.seedShop(t, "cashier")

	barcode := pricedVariant(t, h, f, "100.0000", "80.0000")
	// A wholesale customer belonging to a DIFFERENT company.
	foreign := customerOfType(t, h, other, "wholesale")

	if got := scanPrice(t, h, f, barcode, &foreign); got != "100.0000" {
		t.Errorf("price = %s; another company's customer changed the price", got)
	}
}
