//go:build integration

// Per-terminal configuration (blueprint I5).
//
// Migration 0009 built `terminal_setting` — row-level security, a touch
// trigger, and the eight settings I5 names — and nothing in the product ever
// read or wrote it. A jeweller who wanted the customer recorded on every sale
// had a column describing their situation and no way to reach it. The full
// Blueprint reconciliation found it by looking for schema no Go file names,
// which is the same shape as the `price_dealer` gap found earlier.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func settingsPath(f *shopFixture, deviceID string) string {
	return "/api/v1/devices/" + deviceID + "/settings?company_id=" +
		f.companyID.String()
}

// A terminal nobody has configured answers with the defaults it runs on.
//
// Not a 404: "never configured" and "configured as standard" are the same thing
// to the person at the counter, and a screen should not have to tell them apart.
func TestAnUnconfiguredTerminalAnswersWithItsDefaults(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		settingsPath(f, f.deviceID.String()), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read settings: %d %s", resp.StatusCode, readBody(t, resp))
	}
	out := decodeJSON(t, resp)

	if on, _ := out["drawer_enabled"].(bool); !on {
		t.Error("the drawer defaults to off; 0009 defaults it on")
	}
	if req, _ := out["require_customer"].(bool); req {
		t.Error("a customer is required by default; a grocery would stall")
	}
	if n, _ := out["max_held_carts"].(float64); int(n) != 10 {
		t.Errorf("held carts default to %v, want 10", out["max_held_carts"])
	}
}

// The settings I5 names can be set and read back.
func TestATerminalCanBeConfigured(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut,
		settingsPath(f, f.deviceID.String()), f.token, map[string]any{
			"printer_name": "Counter 1 thermal", "scanner_prefix": "*",
			"drawer_enabled": false, "require_customer": true,
			"max_held_carts": 4, "receipt_template": "jewellery",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, readBody(t, resp))
	}
	out := decodeJSONFrom(t, resp)

	for _, c := range []struct {
		field string
		want  any
	}{
		{"printer_name", "Counter 1 thermal"},
		{"scanner_prefix", "*"},
		{"drawer_enabled", false},
		{"require_customer", true},
		{"receipt_template", "jewellery"},
	} {
		if got := out[c.field]; got != c.want {
			t.Errorf("%s = %v, want %v", c.field, got, c.want)
		}
	}
	if n, _ := out["max_held_carts"].(float64); int(n) != 4 {
		t.Errorf("max_held_carts = %v, want 4", out["max_held_carts"])
	}

	// And it is still there on the next read.
	again := h.do(t, http.MethodGet,
		settingsPath(f, f.deviceID.String()), f.token, nil)
	defer again.Body.Close()
	back := decodeJSON(t, again)
	if p, _ := back["printer_name"].(string); p != "Counter 1 thermal" {
		t.Errorf("the printer did not persist: %q", p)
	}
}

// Changing one setting leaves the others alone.
//
// The property that makes a settings screen safe: a form that saves the printer
// must not blank the discount rule it never asked about.
func TestChangingOneTerminalSettingLeavesTheRest(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	path := settingsPath(f, f.deviceID.String())

	h.do(t, http.MethodPut, path, f.token, map[string]any{
		"printer_name": "Original", "require_customer": true,
		"max_held_carts": 7,
	}).Body.Close()

	resp := h.do(t, http.MethodPut, path, f.token,
		map[string]any{"printer_name": "Replaced"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second save: %s", readBody(t, resp))
	}
	out := decodeJSONFrom(t, resp)

	if p, _ := out["printer_name"].(string); p != "Replaced" {
		t.Errorf("printer = %q, want Replaced", p)
	}
	if req, _ := out["require_customer"].(bool); !req {
		t.Error("require_customer was reset by a save that did not mention it")
	}
	if n, _ := out["max_held_carts"].(float64); int(n) != 7 {
		t.Errorf("max_held_carts became %v; it was not mentioned", n)
	}
}

// A terminal cannot be told to hold no carts.
func TestATerminalMustHoldAtLeastOneCart(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut,
		settingsPath(f, f.deviceID.String()), f.token,
		map[string]any{"max_held_carts": 0})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a terminal was set to hold no carts")
	}
}

// A default warehouse must belong to this company.
//
// Caller-supplied, and another company's warehouse sits in the same tenant
// where row-level security sees nothing wrong with it.
func TestATerminalCannotPointAtAnotherCompanysWarehouse(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut,
		settingsPath(mine, mine.deviceID.String()), mine.token,
		map[string]any{
			"default_warehouse_id": theirs.warehouseID.String()})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a terminal was pointed at another company's stock location")
	}
}

// One business cannot read or configure another's counter.
func TestATerminalInAnotherCompanyIsNotFound(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		resp := h.do(t, method,
			settingsPath(mine, theirs.deviceID.String()), mine.token,
			map[string]any{"printer_name": "Theirs"})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s on another company's terminal got %d, want 404",
				method, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Configuring a counter needs the permission to manage devices.
//
// How a counter is set up decides whether a customer is recorded on a sale and
// which warehouse the stock leaves from, so it is not a cashier's decision.
func TestACashierCannotConfigureATerminal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPut,
		settingsPath(f, f.deviceID.String()), f.token,
		map[string]any{"require_customer": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier configuring a terminal got %d, want 403",
			resp.StatusCode)
	}
}

// Changing a counter's configuration is audited.
func TestConfiguringATerminalIsAudited(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.do(t, http.MethodPut, settingsPath(f, f.deviceID.String()), f.token,
		map[string]any{"require_customer": true}).Body.Close()

	var n int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT count(*) FROM audit_log
				WHERE action = 'terminal_settings_changed'
				  AND entity_type = 'terminal_setting'`).Scan(&n)
		}); err != nil {
		t.Fatalf("read the audit log: %v", err)
	}
	if n != 1 {
		t.Errorf("%d audit records were written, want 1", n)
	}
}
