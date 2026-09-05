//go:build integration

// The branch list three screens were built against and which did not exist.
//
// Three routes already returned branches and every one belonged to somebody
// else: `/devices/stores` is the branches a TERMINAL can be registered in and is
// gated on `devices.view`; `/stock/locations` carries them as a side payload
// behind an inventory permission; `/onboarding/stores` creates them during
// setup. There was no general answer to "which branches does this business
// have".
//
// So the employee form, the employee record and the shift register were each
// written with a branch dropdown pointing at `/stores`, which answered 404. All
// three would have rendered an empty select and a failed query, in a product
// where assigning somebody to a branch is most of what those screens are for.
//
// Found by driving the settings routes before building item 17.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestABusinessCanBeAskedWhichBranchesItHas(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/stores?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asking for the branches answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	rows := decodeJSONFrom(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("a shop with a store reports no branches")
	}

	row := rows[0].(map[string]any)
	for _, field := range []string{"id", "code", "name", "is_active"} {
		if _, ok := row[field]; !ok {
			t.Errorf("a branch row says nothing about %q", field)
		}
	}
}

func TestAnybodySignedInCanNameABranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Merely authenticated, like GET /companies. A branch's name is not a
	// secret from somebody already signed into that company, and gating it
	// would mean picking a permission that is wrong for somebody: identity.view
	// is held by the Owner and the Auditor alone in the base seed, so an HR
	// Manager could not fill a branch dropdown on the screen where they assign
	// somebody to one.
	resp := h.do(t, http.MethodGet,
		"/api/v1/stores?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a signed-in cashier asking for branches answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestAnotherBusinessesBranchesAreNotFound(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	// Merely authenticated is not the same as unscoped. Across TENANTS the guard
	// is row-level security rather than the token's company scope: the id
	// passes the scope check, because an actor with no company restriction may
	// name any company in their OWN tenant, and the query then matches nothing.
	//
	// So the assertion is the security property that actually holds — no branch
	// of theirs comes back — rather than a status code. Asserting 404 here
	// would have been asserting a mechanism that is not the one protecting it.
	resp := h.do(t, http.MethodGet,
		"/api/v1/stores?company_id="+theirs.companyID.String(), mine.token, nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reading another business's branches answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	if resp.StatusCode == http.StatusOK {
		leaked := decodeJSONFrom(t, resp)["data"].([]any)
		if len(leaked) > 0 {
			t.Fatalf("another business's branches leaked: %d rows", len(leaked))
		}
	}
}

func TestAClosedBranchIsStillListed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	closed := closedBranch(t, h, f)

	resp := h.do(t, http.MethodGet,
		"/api/v1/stores?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asking for the branches answered %d", resp.StatusCode)
	}
	rows := decodeJSONFrom(t, resp)["data"].([]any)

	var found map[string]any
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["id"] == closed.String() {
			found = row
		}
	}
	// A shift worked in a branch that has since closed still happened there,
	// and dropping it would show that shift with no branch at all.
	if found == nil {
		t.Fatal("a closed branch is left out, so a record made in it would " +
			"have no branch to name")
	}
	if found["is_active"] != false {
		t.Errorf("a closed branch reports is_active=%v", found["is_active"])
	}

	// Open ones first, so a dropdown offers what is trading before what is not.
	if rows[0].(map[string]any)["is_active"] != true {
		t.Error("a closed branch sorts above an open one")
	}
}

// closedBranch adds a branch that is no longer trading.
func closedBranch(t *testing.T, h *harness, f *shopFixture) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO store (tenant_id, company_id, code, name, is_active)
			VALUES ($1, $2, $3, 'Closed Branch', false)
			RETURNING id`,
			f.tenantID, f.companyID, strings.ToUpper("CL"+uuid.NewString()[:6])).Scan(&id)
	}); err != nil {
		t.Fatalf("add a closed branch: %v", err)
	}
	return id
}
