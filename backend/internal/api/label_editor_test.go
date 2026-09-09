//go:build integration

// Editing a label, and overriding a barcode by hand.
//
// `PUT` and `DELETE /labels/templates/{id}` were live and unreachable: the
// studio could create a layout and list it, and a shop that got the height
// wrong on its thermal roll had to live with it. `PUT
// /labels/barcodes/{variantID}` was reachable and unaudited, which is the more
// serious of the two — it is the one act on this module that changes what a
// single garment scans as, and it is the one that is done quietly.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

// aTemplate posts a layout and returns the response.
func aTemplate(
	t *testing.T, h *harness, f *shopFixture, token string, body map[string]any,
) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/templates"),
		token, body)
}

func thermalBody(name string) map[string]any {
	return map[string]any{
		"name": name, "kind": "thermal",
		"width_mm": "50", "height_mm": "25", "margin_mm": "1", "gap_mm": "0",
		"fields": []map[string]any{
			{"field": "name", "size": 7},
			{"field": "price", "size": 9, "bold": true},
			{"field": "barcode", "height": 10},
		},
	}
}

// A layout can be created, changed and removed — the whole editor, end to end.
func TestALabelTemplateCanBeChangedAndRemoved(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	created := aTemplate(t, h, f, f.token, thermalBody("Shelf ticket"))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d — %s", created.StatusCode, readBody(t, created))
	}
	id, _ := decodeJSON(t, created)["id"].(string)

	// A roll that turned out to be 40mm wide, not 50.
	changed := h.do(t, http.MethodPut,
		adminPath(f, "/api/v1/labels/templates/"+id), f.token, map[string]any{
			"name": "Shelf ticket", "kind": "thermal",
			"width_mm": "40", "height_mm": "25", "margin_mm": "1", "gap_mm": "0",
			"is_default": true,
			"fields": []map[string]any{
				{"field": "name", "size": 6},
				{"field": "price", "size": 9, "bold": true},
				{"field": "barcode", "height": 10},
			},
		})
	if changed.StatusCode != http.StatusOK {
		t.Fatalf("edit: status %d — %s", changed.StatusCode, readBody(t, changed))
	}
	after := decodeJSON(t, changed)
	if after["width_mm"] != "40.00" && after["width_mm"] != "40" {
		t.Errorf("the edited width reads %v, want 40", after["width_mm"])
	}
	if after["is_default"] != true {
		t.Error("the layout was made the default for its kind and did not become one")
	}

	removed := h.do(t, http.MethodDelete,
		adminPath(f, "/api/v1/labels/templates/"+id), f.token, nil)
	removed.Body.Close()
	if removed.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", removed.StatusCode)
	}

	gone := h.do(t, http.MethodDelete,
		adminPath(f, "/api/v1/labels/templates/"+id), f.token, nil)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("removing a layout twice answered %d, want 404", gone.StatusCode)
	}
}

// A label cannot ask for something the printer has no data for.
//
// The failure this refuses is quiet and expensive: the layout is right, one
// line is silently blank, and nobody discovers it until nine hundred tags are
// on a rail.
func TestALabelCannotAskForSomethingItCannotPrint(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	body := thermalBody("Invented")
	body["fields"] = []map[string]any{
		{"field": "name", "size": 7},
		{"field": "supplier_telephone", "size": 7},
	}
	resp := aTemplate(t, h, f, f.token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a label naming a field the printer cannot fill was accepted: "+
			"%d — %s", resp.StatusCode, readBody(t, resp))
	}

	// And a label with nothing on it is a blank sticker.
	empty := thermalBody("Blank")
	empty["fields"] = []map[string]any{}
	blank := aTemplate(t, h, f, f.token, empty)
	blank.Body.Close()
	if blank.StatusCode == http.StatusCreated {
		t.Error("a layout with no lines on it was accepted")
	}
}

// Every layout the product seeds is one the product will accept back.
//
// The check above is worth nothing if a shop cannot save its own starting
// templates unchanged — which is exactly what happened to the loyalty card,
// whose seeded `tier` line the renderer has no case for. Migration 0131
// removes it; this holds the two in step.
func TestEverySeededLabelCanBeSavedAgainUnchanged(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedStudio(t, h, f)

	listed := h.do(t, http.MethodGet, adminPath(f, "/api/v1/labels/templates"),
		f.token, nil)
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d", listed.StatusCode)
	}
	rows, _ := decodeJSON(t, listed)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the studio seeded no layouts, so this proves nothing")
	}

	for _, raw := range rows {
		tpl, _ := raw.(map[string]any)
		id, _ := tpl["id"].(string)
		body := map[string]any{
			"name": tpl["name"], "kind": tpl["kind"],
			"width_mm": tpl["width_mm"], "height_mm": tpl["height_mm"],
			"margin_mm": tpl["margin_mm"], "gap_mm": tpl["gap_mm"],
			"columns": tpl["columns"], "rows": tpl["rows"],
			"is_default": tpl["is_default"], "fields": tpl["fields"],
		}
		resp := h.do(t, http.MethodPut,
			adminPath(f, "/api/v1/labels/templates/"+id), f.token, body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("the seeded %v layout cannot be saved back unchanged: "+
				"%d — %s", tpl["name"], resp.StatusCode, readBody(t, resp))
			continue
		}
		resp.Body.Close()
	}
}

// Changing a layout names who changed it, and what it became.
func TestChangingALabelIsAudited(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	created := aTemplate(t, h, f, f.token, thermalBody("Audited ticket"))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d — %s", created.StatusCode, readBody(t, created))
	}
	id, _ := decodeJSON(t, created)["id"].(string)

	var label, name string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(actor_label,''), coalesce(after_value->>'name','')
			FROM audit_log
			WHERE action = 'label_template_created' AND entity_id = $1`,
			id).Scan(&label, &name)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if label == "" || name != "Audited ticket" {
		t.Errorf("the trail records actor %q and name %q; the tag carries the "+
			"shop's price and a change to it must name somebody", label, name)
	}

	removed := h.do(t, http.MethodDelete,
		adminPath(f, "/api/v1/labels/templates/"+id), f.token, nil)
	removed.Body.Close()

	var gone int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM audit_log
			WHERE action = 'label_template_removed' AND entity_id = $1
			  AND before_value->>'name' = 'Audited ticket'`, id).Scan(&gone)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if gone != 1 {
		t.Errorf("%d entries record the removal, want 1 naming the layout that "+
			"went; a row naming a uuid that no longer resolves says nothing", gone)
	}
}

// Overriding a barcode by hand records what it was, what it became, and why.
func TestOverridingABarcodeIsAudited(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	variantID := f.variantID.String()

	const why = "The manufacturer's own EAN is printed on the box."
	set := h.do(t, http.MethodPut,
		adminPath(f, "/api/v1/labels/barcodes/"+variantID), f.token,
		map[string]any{"barcode": "5901234123457", "reason": why})
	set.Body.Close()
	if set.StatusCode != http.StatusNoContent && set.StatusCode != http.StatusOK {
		t.Fatalf("set barcode: %d", set.StatusCode)
	}

	var label, before, after, reason string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(actor_label,''), coalesce(before_value->>'barcode',''),
			       coalesce(after_value->>'barcode',''), coalesce(after_value->>'reason','')
			FROM audit_log
			WHERE action = 'barcode_overridden' AND entity_id = $1
			ORDER BY occurred_at DESC LIMIT 1`, variantID).
			Scan(&label, &before, &after, &reason)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}

	if label == "" {
		t.Error("the override names nobody")
	}
	if after != "5901234123457" {
		t.Errorf("the trail records the new code as %q", after)
	}
	if reason != why {
		t.Errorf("the trail records the reason as %q, want %q", reason, why)
	}
	_ = before // may legitimately be empty: the variant had no code before.
}

// Setting the code it already carries is not a change, and records nothing.
//
// A retried request must not put a row in the trail saying somebody altered a
// barcode when nobody did.
func TestSettingTheSameBarcodeAgainRecordsNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	variantID := f.variantID.String()

	for i := 0; i < 2; i++ {
		resp := h.do(t, http.MethodPut,
			adminPath(f, "/api/v1/labels/barcodes/"+variantID), f.token,
			map[string]any{"barcode": "4006381333931", "reason": "same again"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d: status %d", i+1, resp.StatusCode)
		}
	}

	var entries int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM audit_log
			WHERE action = 'barcode_overridden' AND entity_id = $1`,
			variantID).Scan(&entries)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if entries != 1 {
		t.Errorf("%d override entries for two identical requests, want 1", entries)
	}
}

// Designing a label is `label.manage`; printing one is `label.print`.
//
// A cashier putting stock on a shelf may print tags all day and must not be
// able to redesign the tag that carries the shop's price.
func TestACashierCannotRedesignTheLabelOrMoveABarcode(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	created := aTemplate(t, h, f, f.token, thermalBody("Cashier's own"))
	created.Body.Close()
	if created.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier created a label layout: %d", created.StatusCode)
	}

	set := h.do(t, http.MethodPut,
		adminPath(f, "/api/v1/labels/barcodes/"+f.variantID.String()), f.token,
		map[string]any{"barcode": "1234567890128"})
	set.Body.Close()
	if set.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier moved a barcode: %d", set.StatusCode)
	}
}
