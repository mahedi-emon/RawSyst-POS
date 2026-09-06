//go:build integration

// The parts of a commission scheme that the engine read and nothing could
// write.
//
// `commissionFor` filters the month's takings by `category_id`, `brand_id` and
// `variant_id`, and stops paying at `effective_to` and on `is_active`. All five
// were columns with no way in: `POST /commission-rules` accepted a name, a
// basis, an employee, a store, a rate, tiers and a start date, and nothing
// else. So a shop could be told its scheme covered one department and had no
// way to say so, and a shop that configured the wrong rate on its first day had
// no way to stop it — the scheme had no end date and could not be switched off.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

// categoryHolding puts the fixture's product into a named department and
// answers its id.
//
// The seeded product is in no category, so the scope being tested has to be
// arranged rather than found. Naming the department is what makes the pair of
// tests below meaningful: one scheme names the department the goods ARE in,
// the other names one they are not, and the two must pay differently.
func categoryHolding(t *testing.T, h *harness, f *shopFixture, name string) string {
	t.Helper()
	var id string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO category (tenant_id, company_id, name)
			VALUES ($1, $2, $3) RETURNING id::text`,
			f.tenantID, f.companyID, name).Scan(&id); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			UPDATE product SET category_id = $1::uuid
			WHERE id = (SELECT product_id FROM variant WHERE id = $2)`,
			id, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("put the product in a department: %v", err)
	}
	return id
}

// emptyCategory is a department with nothing in it.
func emptyCategory(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	var id string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO category (tenant_id, company_id, name)
			VALUES ($1, $2, 'Nothing is in here') RETURNING id::text`,
			f.tenantID, f.companyID).Scan(&id)
	}); err != nil {
		t.Fatalf("make an empty department: %v", err)
	}
	return id
}

// A scheme scoped to the category the goods are in pays on them.
func TestASchemeScopedToTheCategorySoldPays(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Ten per cent on this department", "basis": "revenue",
		"rate": "0.10", "category_id": categoryHolding(t, h, f, "Menswear"),
	})

	sellAndReturn(t, h, f, false) // 115.00 gross, 100.00 net of VAT

	if got := commissionPaid(t, h, f); got != "10.00" {
		t.Errorf("commission = %s, want 10.00 — the scheme names the "+
			"department the goods are in", got)
	}
}

// A scheme scoped to a different category pays nothing.
//
// The assertion that makes the one above mean something: without it, a scope
// the engine ignored would pass both.
func TestASchemeScopedToAnotherCategoryPaysNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)

	categoryHolding(t, h, f, "Menswear")
	elsewhere := emptyCategory(t, h, f)

	commissionRule(t, h, f, map[string]any{
		"name":  "Ten per cent on a department with nothing in it",
		"basis": "revenue", "rate": "0.10", "category_id": elsewhere,
	})

	sellAndReturn(t, h, f, false)

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — the scheme names a department "+
			"the goods are not in", got)
	}
}

// A scheme that has ended pays nothing after it ends.
func TestASchemeThatHasEndedPaysNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "Last year's scheme", "basis": "revenue", "rate": "0.10",
		"effective_from": "2025-01-01", "effective_to": "2025-12-31",
	})

	sellAndReturn(t, h, f, false) // August 2026, after it ended

	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s, want 0.00 — the scheme ended in 2025", got)
	}
}

// A scheme cannot end before it starts.
func TestASchemeCannotEndBeforeItStarts(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost,
		"/api/v1/commission-rules?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"name": "Backwards", "basis": "revenue", "rate": "0.10",
			"effective_from": "2026-06-01", "effective_to": "2026-01-01",
		})
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a scheme ending before it starts answered %d, want 400: %s",
			resp.StatusCode, body)
	}
}

// A scheme can be switched off, which is the only way to stop one that has no
// end date.
func TestASchemeCanBeSwitchedOffAndOnAgain(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	commissionEarner(t, h, f)
	commissionRule(t, h, f, map[string]any{
		"name": "The rate somebody typed wrong", "basis": "revenue",
		"rate": "0.10",
	})

	var ruleID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id::text FROM commission_rule WHERE company_id = $1`,
			f.companyID).Scan(&ruleID)
	}); err != nil {
		t.Fatalf("find the scheme: %v", err)
	}

	sellAndReturn(t, h, f, false)
	if got := commissionPaid(t, h, f); got != "10.00" {
		t.Fatalf("commission = %s before switching off, want 10.00", got)
	}

	off := h.do(t, http.MethodPost,
		"/api/v1/commission-rules/"+ruleID+"/active?company_id="+
			f.companyID.String(), f.token, map[string]any{"is_active": false})
	if off.StatusCode != http.StatusNoContent {
		t.Fatalf("switching the scheme off: %d %s", off.StatusCode, readBody(t, off))
	}
	off.Body.Close()

	clearPayrollRuns(t, h, f)
	if got := commissionPaid(t, h, f); got != "0.00" {
		t.Errorf("commission = %s after switching the scheme off, want 0.00", got)
	}

	on := h.do(t, http.MethodPost,
		"/api/v1/commission-rules/"+ruleID+"/active?company_id="+
			f.companyID.String(), f.token, map[string]any{"is_active": true})
	if on.StatusCode != http.StatusNoContent {
		t.Fatalf("switching the scheme back on: %d %s", on.StatusCode, readBody(t, on))
	}
	on.Body.Close()

	clearPayrollRuns(t, h, f)
	if got := commissionPaid(t, h, f); got != "10.00" {
		t.Errorf("commission = %s after switching the scheme back on, want 10.00",
			got)
	}
}

// A scheme belonging to another business cannot be switched off from here.
//
// A 404 rather than a 403: a 403 would confirm the scheme exists, which is what
// a cross-tenant probe is looking for.
func TestASchemeInAnotherBusinessCannotBeSwitchedOff(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	commissionRule(t, h, theirs, map[string]any{
		"name": "Theirs", "basis": "revenue", "rate": "0.10",
	})
	var theirRule string
	if err := h.pool.TxAsTenant(t.Context(), theirs.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id::text FROM commission_rule WHERE company_id = $1`,
			theirs.companyID).Scan(&theirRule)
	}); err != nil {
		t.Fatalf("find their scheme: %v", err)
	}

	refused := h.do(t, http.MethodPost,
		"/api/v1/commission-rules/"+theirRule+"/active?company_id="+
			mine.companyID.String(), mine.token,
		map[string]any{"is_active": false})
	body := readBody(t, refused)
	refused.Body.Close()
	if refused.StatusCode != http.StatusNotFound {
		t.Errorf("switching off another business's scheme answered %d, "+
			"want 404: %s", refused.StatusCode, body)
	}

	var stillOn bool
	if err := h.pool.TxAsTenant(t.Context(), theirs.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT is_active FROM commission_rule WHERE id = $1`,
			theirRule).Scan(&stillOn)
	}); err != nil {
		t.Fatalf("read their scheme back: %v", err)
	}
	if !stillOn {
		t.Error("the refused call switched their scheme off anyway")
	}
}

// The scope a scheme was saved with comes back on the list.
//
// A list that omits it shows two schemes as identical when they pay different
// money, which is the mistake somebody makes at the moment they are trying to
// work out why a salesperson was paid what they were.
func TestASchemesScopeIsReadBack(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	category := categoryHolding(t, h, f, "Menswear")

	commissionRule(t, h, f, map[string]any{
		"name": "Scoped", "basis": "profit", "rate": "0.05",
		"store_id": f.storeID.String(), "category_id": category,
		"effective_from": "2026-01-01", "effective_to": "2026-12-31",
	})

	resp := h.do(t, http.MethodGet,
		"/api/v1/commission-rules?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing schemes: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSONFrom(t, resp)["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("saved one scheme, listed %d", len(rows))
	}
	got, _ := rows[0].(map[string]any)

	for field, want := range map[string]string{
		"category_id":    category,
		"store_id":       f.storeID.String(),
		"basis":          "profit",
		"effective_from": "2026-01-01",
		"effective_to":   "2026-12-31",
	} {
		if have, _ := got[field].(string); have != want {
			t.Errorf("%s = %q, want %q", field, have, want)
		}
	}
}
