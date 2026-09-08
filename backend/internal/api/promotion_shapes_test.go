//go:build integration

// Every kind of campaign a shop can run, created and read back (B9).
//
// `promotion.value`, `buy_qty`, `get_qty` and `min_purchase` are all nullable,
// and each kind fills a different subset of them: a percentage has no buy
// quantity, a buy-three-get-one has no value. Both the list and the single read
// selected the four columns raw and scanned them into non-null decimals, so the
// moment a shop created the most ordinary promotion there is, creating it
// answered 500 and the campaigns list answered 500 from then on.
//
// It was never seen because every test and every fixture happened to use
// buy_x_get_y, which is the one kind that fills the columns the others leave
// empty.
package api

import (
	"net/http"
	"testing"
)

// Each kind is created, and then the list is read with it in place.
func TestEveryKindOfPromotionCanBeCreatedAndListed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"a percentage off", map[string]any{
			"code": "PCT10", "name": "Ten per cent off",
			"kind": "percentage", "value": "10",
		}},
		{"a flat amount off", map[string]any{
			"code": "AMT5", "name": "Five off",
			"kind": "amount", "value": "5",
		}},
		{"buy three get one", map[string]any{
			"code": "B3G1", "name": "Buy three, get one",
			"kind": "buy_x_get_y", "buy_qty": "3", "get_qty": "1",
		}},
		{"a bundle price", map[string]any{
			"code": "BUNDLE3", "name": "Three for a hundred",
			"kind": "bundle_price", "buy_qty": "3", "value": "100",
		}},
	} {
		created := h.do(t, http.MethodPost, "/api/v1/promotions"+company,
			f.token, c.body)
		if created.StatusCode != http.StatusCreated &&
			created.StatusCode != http.StatusOK {
			t.Fatalf("create %s: %d %s", c.name, created.StatusCode,
				readBody(t, created))
		}
		created.Body.Close()

		// Read after each, so a failure names the kind that broke the list
		// rather than only the last one added.
		listed := h.do(t, http.MethodGet, "/api/v1/promotions"+company,
			f.token, nil)
		if listed.StatusCode != http.StatusOK {
			t.Fatalf("list the campaigns with %s in them: %d %s",
				c.name, listed.StatusCode, readBody(t, listed))
		}
		listed.Body.Close()
	}
}
