//go:build integration

// H8 — the platform control plane, over the routes the operator's screens call.
//
// These cover two things the platform could not do, both found by driving the
// live control plane rather than by reading it.
//
// The tenant list was `ORDER BY created_at DESC LIMIT 500` with no arguments,
// and the screen above it filtered in the browser what it had been sent. The
// development database holds nine and a half thousand tenants, of which one
// hundred and thirty are named "Tieout" and not one is among the newest five
// hundred — so an operator searching for that client was told there were no
// matches. "No matches" and "not in the half of the table I sent you" are
// different answers and only one of them was true, which is the worst shape a
// defect can take: it looks like an answer.
//
// And the sub-processor register could be written by the platform and read only
// by a tenant, so the one person allowed to maintain it could not see it.
package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// randomSuffix keeps seeded names distinct across runs against a database that
// is not torn down between them.
func randomSuffix() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
}

// seedTenantsNamed creates n tenants with a shared distinctive name, older than
// everything already in the table, and returns that name.
//
// Older on purpose: a tenant created just now would sit at the top of a
// created_at DESC list and would be found by a truncating query as easily as by
// a searching one, which would prove nothing.
func (h *harness) seedTenantsNamed(t *testing.T, n int) string {
	t.Helper()
	name := "Backfill " + fmt.Sprintf("%d", n) + "x" + randomSuffix()

	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO tenant (name, market, plan_tier, status, created_at)
			SELECT $1, 'bd', 'starter', 'active',
			       now() - interval '10 years' - (g || ' minutes')::interval
			FROM generate_series(1, $2) g`, name, n)
		return e
	}); err != nil {
		t.Fatalf("seed tenants: %v", err)
	}
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
			_, e := tx.Exec(t.Context(), `DELETE FROM tenant WHERE name = $1`, name)
			return e
		})
	})
	return name
}

// An operator finds a client that is not among the newest accounts.
func TestTheTenantSearchReachesPastTheFirstPage(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	const seeded = 12
	name := h.seedTenantsNamed(t, seeded)

	// Unsearched, the newest page does not contain them: they are ten years
	// old and the list is newest-first. This is the state the old code left an
	// operator in permanently.
	resp := h.do(t, http.MethodGet, "/api/v1/platform/tenants?limit=50", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	for _, row := range decodeJSON(t, resp)["data"].([]any) {
		if row.(map[string]any)["name"] == name {
			t.Fatalf("the seeded tenants are on the newest page, so this test "+
				"would pass without a search reaching them; name %q", name)
		}
	}

	// Searched, every one of them comes back.
	found := 0
	cursor := ""
	for page := 0; page < 10; page++ {
		path := "/api/v1/platform/tenants?limit=5&search=" + url.QueryEscape(name)
		if cursor != "" {
			path += "&after=" + cursor
		}
		resp := h.do(t, http.MethodGet, path, admin, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search: status %d — %s", resp.StatusCode, readBody(t, resp))
		}
		body := decodeJSON(t, resp)
		rows, _ := body["data"].([]any)
		for _, row := range rows {
			if row.(map[string]any)["name"] != name {
				t.Errorf("search returned %v, which does not match the term",
					row.(map[string]any)["name"])
			}
			found++
		}
		meta, _ := body["page"].(map[string]any)
		if more, _ := meta["has_more"].(bool); !more {
			break
		}
		cursor, _ = meta["cursor"].(string)
	}
	if found != seeded {
		t.Errorf("search found %d of %d seeded tenants; a client the operator "+
			"cannot find is a client they cannot support", found, seeded)
	}
}

// Paging walks the list without repeating or skipping a row.
func TestTenantPagingNeitherRepeatsNorSkips(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	const seeded = 13
	name := h.seedTenantsNamed(t, seeded)

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 12; page++ {
		path := "/api/v1/platform/tenants?limit=4&search=" + url.QueryEscape(name)
		if cursor != "" {
			path += "&after=" + cursor
		}
		resp := h.do(t, http.MethodGet, path, admin, nil)
		body := decodeJSON(t, resp)
		rows, _ := body["data"].([]any)
		for _, row := range rows {
			id, _ := row.(map[string]any)["id"].(string)
			if seen[id] {
				t.Errorf("tenant %s came back on two pages; a keyset that "+
					"repeats is one an operator cannot trust to be complete", id)
			}
			seen[id] = true
		}
		meta, _ := body["page"].(map[string]any)
		if more, _ := meta["has_more"].(bool); !more {
			break
		}
		cursor, _ = meta["cursor"].(string)
	}
	if len(seen) != seeded {
		t.Errorf("paging saw %d distinct tenants, seeded %d", len(seen), seeded)
	}
}

// The filters narrow rather than decorate.
func TestTenantFiltersActuallyFilter(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	name := h.seedTenantsNamed(t, 4) // seeded into the 'bd' market

	// The market filter and the search agree: all four are Bangladeshi.
	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants?limit=50&market=bd&search="+url.QueryEscape(name), admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filtered: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := len(decodeJSON(t, resp)["data"].([]any)); got != 4 {
		t.Errorf("market=bd returned %d of the 4 seeded Bangladeshi tenants", got)
	}

	// And a market they are not in excludes them, rather than being ignored.
	resp = h.do(t, http.MethodGet,
		"/api/v1/platform/tenants?limit=50&market=sa&search="+url.QueryEscape(name), admin, nil)
	if got := len(decodeJSON(t, resp)["data"].([]any)); got != 0 {
		t.Errorf("market=sa returned %d Bangladeshi tenants; the filter is "+
			"being accepted and ignored", got)
	}
}

// The operator can read the register only they may write.
func TestThePlatformCanReadTheSubprocessorRegisterItKeeps(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	name := "Test Processor " + randomSuffix()
	resp := h.do(t, http.MethodPut, "/api/v1/platform/subprocessors", admin,
		map[string]any{
			"name": name, "purpose": "Card processing", "country": "sa",
			"data_categories": "payment", "is_active": true,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	// Reading it back is the whole point: before this route the platform could
	// write this list and had no way to see it, because the only read was
	// scoped to a tenant and an operator has none.
	resp = h.do(t, http.MethodGet, "/api/v1/platform/subprocessors", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	var found map[string]any
	for _, row := range rows {
		if row.(map[string]any)["name"] == name {
			found = row.(map[string]any)
		}
	}
	if found == nil {
		t.Fatalf("the sub-processor just written is not in the register: %v", rows)
	}

	// Retiring one keeps it visible to the operator. A retired row that
	// vanished would be indistinguishable from a deleted one, and could never
	// be brought back.
	id, _ := found["id"].(string)
	resp = h.do(t, http.MethodPut, "/api/v1/platform/subprocessors", admin,
		map[string]any{
			"id": id, "name": name, "purpose": "Card processing", "country": "sa",
			"data_categories": "payment", "is_active": false,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retire: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	resp = h.do(t, http.MethodGet, "/api/v1/platform/subprocessors", admin, nil)
	rows, _ = decodeJSON(t, resp)["data"].([]any)
	var stillThere bool
	for _, row := range rows {
		if row.(map[string]any)["id"] == id {
			stillThere = true
			if row.(map[string]any)["is_active"] != false {
				t.Error("the retired sub-processor still reads as active")
			}
		}
	}
	if !stillThere {
		t.Error("a retired sub-processor disappeared from the operator's own " +
			"register, so retiring one cannot be undone")
	}
}

// A tenant's view of the register stays current-only, which is what belongs in
// their processing record.
func TestATenantSeesOnlyCurrentSubprocessors(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	f := h.seedShop(t, "owner")

	name := "Retired Processor " + randomSuffix()
	resp := h.do(t, http.MethodPut, "/api/v1/platform/subprocessors", admin,
		map[string]any{
			"name": name, "purpose": "Archived", "country": "sa",
			"data_categories": "payment", "is_active": false,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write retired: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	resp = h.do(t, http.MethodGet,
		"/api/v1/privacy/subprocessors?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant read: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	for _, row := range decodeJSON(t, resp)["data"].([]any) {
		if row.(map[string]any)["name"] == name {
			t.Error("a tenant can see a sub-processor the platform has retired; " +
				"it does not belong in their processing record")
		}
	}
}
