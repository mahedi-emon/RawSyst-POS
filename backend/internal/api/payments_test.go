//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// The four properties the gateway layer exists for.
//
//   - The credentials are the CLIENT'S, typed into a screen, and the product
//     talks to whichever acquirer they chose without a deployment.
//   - A stored key never comes back out.
//   - A retried charge does not charge the customer twice.
//   - A configuration cannot be switched to live until it has been tested and
//     passed.
//
// # Why there is a fake card machine here and no fake Moyasar
//
// The `terminal` adapter takes the ADDRESS as configuration, which is the whole
// point of the design: a shop types where its machine lives. That makes it the
// one adapter a test can point at an `httptest` server and drive end to end —
// through the real route, the real permission check, the real sealing, the real
// idempotency index and the real adapter — without inventing a way to redirect
// an acquirer's hard-coded hostname.
//
// The other seven differ from it in the endpoint and the field names and in
// nothing else that these four properties touch.

// fakeTerminal is a card machine on the counter that approves everything and
// counts how many times it was asked.
type fakeTerminal struct {
	*httptest.Server
	sales atomic.Int32
}

func newFakeTerminal(t *testing.T) *fakeTerminal {
	t.Helper()
	term := &fakeTerminal{}
	term.Server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/sale" {
				term.sales.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result":  "approved",
				"rrn":     "RRN" + uuid.NewString()[:8],
				"code":    "00",
				"message": "Approved",
			})
		}))
	t.Cleanup(term.Close)
	return term
}

// saveTerminalGateway configures the fixture's company to use one.
func saveTerminalGateway(
	t *testing.T, h *harness, f *shopFixture, term *fakeTerminal, active bool,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/payment-gateways?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"provider": "terminal",
			"label":    "Front counter",
			"mode":     "test",
			"settings": map[string]string{
				"address":     term.URL,
				"terminal_id": "T-001",
			},
			"methods":   []string{"mada", "visa"},
			"is_active": active,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("saving the gateway: %d %s", resp.StatusCode,
			readBody(t, resp))
	}
	gateway, _ := decodeJSON(t, resp)["gateway"].(map[string]any)
	id, _ := gateway["id"].(string)
	if id == "" {
		t.Fatal("saving the gateway returned no id")
	}
	return id
}

// TestACardProviderIsConfiguredNotDeployed is the user's own point, proved.
//
// Nothing about the acquirer is in this repository except the shape of its
// API. The address, the terminal id and the key are typed into a screen, and
// the product then takes money through whichever provider the shop chose.
func TestACardProviderIsConfiguredNotDeployed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	term := newFakeTerminal(t)

	// The screen asks the server what boxes to draw rather than knowing.
	providers := h.do(t, http.MethodGet, "/api/v1/payment-providers",
		f.token, nil)
	if providers.StatusCode != http.StatusOK {
		t.Fatalf("listing providers: %s", readBody(t, providers))
	}
	list, _ := decodeJSON(t, providers)["providers"].([]any)
	if len(list) < 8 {
		t.Fatalf("providers = %d, want every acquirer with an adapter",
			len(list))
	}

	id := saveTerminalGateway(t, h, f, term, true)

	// The Test button reaches the machine.
	check := h.do(t, http.MethodPost,
		"/api/v1/payment-gateways/"+id+"/check?company_id="+
			f.companyID.String(), f.token, nil)
	if check.StatusCode != http.StatusOK {
		t.Fatalf("checking the gateway: %s", readBody(t, check))
	}
	gateway, _ := decodeJSON(t, check)["gateway"].(map[string]any)
	if ok, _ := gateway["last_check_ok"].(bool); !ok {
		t.Fatalf("the check did not pass: %v", gateway["last_check_note"])
	}

	// And a charge goes through it and is captured.
	charge := h.do(t, http.MethodPost,
		"/api/v1/payment-gateways/"+id+"/charge?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"idempotency_key": uuid.NewString(),
			"method":          "mada",
			"amount":          "115.00",
			"currency":        "SAR",
		})
	if charge.StatusCode != http.StatusOK {
		t.Fatalf("charging: %d %s", charge.StatusCode, readBody(t, charge))
	}
	attempt, _ := decodeJSON(t, charge)["attempt"].(map[string]any)
	if got, _ := attempt["status"].(string); got != "captured" {
		t.Fatalf("status = %q, want captured", got)
	}
	if term.sales.Load() != 1 {
		t.Fatalf("the machine was asked %d times, want 1", term.sales.Load())
	}
}

// TestAStoredPaymentKeyNeverComesBackOut is the property that makes it safe to
// let a shop type its own credentials in.
func TestAStoredPaymentKeyNeverComesBackOut(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	const secret = "sk_test_the_shops_own_moyasar_key"
	resp := h.do(t, http.MethodPost,
		"/api/v1/payment-gateways?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"provider": "moyasar",
			"label":    "Moyasar",
			"mode":     "test",
			"settings": map[string]string{
				"publishable_key": "pk_test_public_half",
			},
			"secret":  secret,
			"methods": []string{"mada"},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("saving: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); strings.Contains(body, secret) {
		t.Fatal("the save response handed the secret key back")
	}

	list := h.do(t, http.MethodGet,
		"/api/v1/payment-gateways?company_id="+f.companyID.String(),
		f.token, nil)
	body := readBody(t, list)
	if strings.Contains(body, secret) {
		t.Fatal("the list handed the secret key back")
	}
	// It says one is held, which is what a screen needs to offer to replace it.
	if !strings.Contains(body, `"has_secret":true`) {
		t.Fatalf("the list does not say a key is stored: %s", body)
	}
}

// TestARetriedChargeDoesNotChargeTheCustomerTwice is the single most important
// property here.
//
// A till whose network dropped after the request left has no way to know
// whether the money was taken. It retries with the same key, and gets the
// first attempt back rather than a second charge.
func TestARetriedChargeDoesNotChargeTheCustomerTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	term := newFakeTerminal(t)
	id := saveTerminalGateway(t, h, f, term, true)

	key := uuid.NewString()
	body := map[string]any{
		"idempotency_key": key,
		"method":          "mada",
		"amount":          "115.00",
		"currency":        "SAR",
	}
	path := "/api/v1/payment-gateways/" + id + "/charge?company_id=" +
		f.companyID.String()

	first := h.do(t, http.MethodPost, path, f.token, body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first charge: %s", readBody(t, first))
	}
	firstAttempt, _ := decodeJSON(t, first)["attempt"].(map[string]any)

	second := h.do(t, http.MethodPost, path, f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry: %s", readBody(t, second))
	}
	secondAttempt, _ := decodeJSON(t, second)["attempt"].(map[string]any)

	if firstAttempt["id"] != secondAttempt["id"] {
		t.Fatalf("the retry made a second attempt: %v then %v",
			firstAttempt["id"], secondAttempt["id"])
	}
	if n := term.sales.Load(); n != 1 {
		t.Fatalf("the machine was asked %d times, want 1 -- the customer "+
			"was charged twice", n)
	}
}

// TestALiveConnectionCannotBeSwitchedOnUntilItHasBeenTested keeps a typo in a
// key from being discovered by a cashier at the counter.
func TestALiveConnectionCannotBeSwitchedOnUntilItHasBeenTested(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost,
		"/api/v1/payment-gateways?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"provider": "moyasar",
			"label":    "Moyasar live",
			"mode":     "live",
			"settings": map[string]string{
				"publishable_key": "pk_live_public_half",
			},
			"secret":    "sk_live_never_tested",
			"methods":   []string{"mada"},
			"is_active": true,
		})
	if resp.StatusCode < 400 {
		t.Fatalf("an untested live connection was switched on: %d %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// TestOneShopsCardProviderIsInvisibleToAnother is the isolation this table
// needs more than most: a gateway row holds a key that moves money.
func TestOneShopsCardProviderIsInvisibleToAnother(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	term := newFakeTerminal(t)
	id := saveTerminalGateway(t, h, mine, term, true)

	// Asked for with the OTHER tenant's token and their own company id, which
	// is the shape a real attempt would take.
	resp := h.do(t, http.MethodGet,
		"/api/v1/payment-gateways/"+id+"?company_id="+
			theirs.companyID.String(), theirs.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode,
			readBody(t, resp))
	}
}
