//go:build integration

// The role builder's list, and a refusal that told the truth about half of it.
//
// A6.2's role builder shows every role a business can assign and lets an owner
// build their own. Two things it needs, and neither was on the list route:
//
//   - WHICH ROLES ARE BUILT-IN. They cannot be edited or removed, and the
//     answer is "copy it and edit the copy". A screen that could not tell
//     would either ask for each role individually — thirteen requests to draw
//     one table — or offer Edit on everything and let the server refuse.
//   - HOW MANY PEOPLE HOLD ONE. A role cannot be removed while anybody does.
//
// And `DELETE /roles/{id}` on a built-in TEMPLATE answered 404 "That role was
// not found" for a role sitting in the list the caller had just read, because
// the lookup was scoped to the caller's tenant and a template belongs to none.
// The same id on PUT answered the accurate refusal. An owner told a visible
// role does not exist reloads the page; the truth is it cannot be deleted.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// theRoles reads the assignable-role list the way the screen does.
func theRoles(t *testing.T, h *harness, f *shopFixture) []map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/people/roles?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read the roles: %s", readBody(t, resp))
	}
	raw := decodeJSONFrom(t, resp)["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestTheRoleListSaysWhichRolesAreTheProductsOwn(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	roles := theRoles(t, h, f)
	if len(roles) == 0 {
		t.Fatal("a business with an owner can assign no roles at all")
	}

	// Every seeded role is one the product ships. A list that says so lets the
	// screen offer Copy rather than Edit, which is the only thing that works.
	builtin := 0
	for _, r := range roles {
		flag, ok := r["is_system"]
		if !ok {
			t.Fatalf("the role list does not say whether %q is built-in, so a "+
				"screen cannot know whether it may be edited", r["name"])
		}
		if flag == true {
			builtin++
		}
	}
	if builtin == 0 {
		t.Errorf("not one of the %d seeded roles is marked built-in", len(roles))
	}
}

func TestTheRoleListSaysHowManyPeopleHoldEachRole(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, r := range theRoles(t, h, f) {
		count, ok := r["in_use"]
		if !ok {
			t.Fatalf("the role list does not say how many people hold %q, so a "+
				"screen offering Remove cannot say whether it is available",
				r["name"])
		}
		if n, fine := count.(float64); !fine || n < 0 {
			t.Errorf("%q reports %v holders", r["name"], count)
		}
	}

	// The owner's own role is held by at least the owner, so the figure is
	// being counted rather than defaulted to nothing.
	held := false
	for _, r := range theRoles(t, h, f) {
		if n, fine := r["in_use"].(float64); fine && n > 0 {
			held = true
		}
	}
	if !held {
		t.Error("every role reports nobody holding it, and this business has " +
			"an owner who holds one")
	}
}

func TestRemovingABuiltInRoleSaysItIsBuiltInRatherThanMissing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	var builtin uuid.UUID
	for _, r := range theRoles(t, h, f) {
		if r["is_system"] == true {
			builtin = uuid.MustParse(r["id"].(string))
			break
		}
	}
	if builtin == uuid.Nil {
		t.Skip("no built-in role to try removing")
	}

	// It answered 404. The role is in the list the caller just read, and the
	// same id on PUT answers a precise 403.
	resp := h.do(t, http.MethodDelete,
		"/api/v1/roles/"+builtin.String()+"?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("removing a built-in role reports it missing, and it is in " +
			"the list: an owner reads that as a stale page rather than as a " +
			"role that cannot be deleted")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("removing a built-in role answered %d, want 403: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestARoleSomebodyElseOwnsIsStillNotFound(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	// A custom role in the other business. Its existence is not this caller's
	// to learn, so the truthful answer here IS "not found" — the fix above
	// must not have turned every miss into a 403.
	made := h.do(t, http.MethodPost,
		"/api/v1/roles?company_id="+theirs.companyID.String(), theirs.token,
		map[string]any{
			"name":        "Their Own Role",
			"permissions": []string{"sales.view"},
		})
	if made.StatusCode != http.StatusOK && made.StatusCode != http.StatusCreated {
		t.Fatalf("create a role in the other business: %s", readBody(t, made))
	}
	role := decodeJSONFrom(t, made)["role"].(map[string]any)

	resp := h.do(t, http.MethodDelete,
		"/api/v1/roles/"+role["id"].(string)+"?company_id="+mine.companyID.String(),
		mine.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting another business's role answered %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestARoleYouMadeCanBeRemoved(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// The other side of the refusal. A fix that made every delete a 403 would
	// pass the tests above and break the feature.
	made := h.do(t, http.MethodPost,
		"/api/v1/roles?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"name":        "Shop Floor Only",
			"description": "Sees the catalogue and nothing else",
			"permissions": []string{"catalog.view"},
		})
	if made.StatusCode != http.StatusOK && made.StatusCode != http.StatusCreated {
		t.Fatalf("create a role: %s", readBody(t, made))
	}
	role := decodeJSONFrom(t, made)["role"].(map[string]any)

	// And it appears on the list as the tenant's own, held by nobody.
	found := false
	for _, r := range theRoles(t, h, f) {
		if r["id"] == role["id"] {
			found = true
			if r["is_system"] == true {
				t.Error("a role the business made is marked as the product's own")
			}
			if n, fine := r["in_use"].(float64); !fine || n != 0 {
				t.Errorf("a brand new role reports %v holders", r["in_use"])
			}
		}
	}
	if !found {
		t.Fatal("a role that was just created is not on the assignable list")
	}

	resp := h.do(t, http.MethodDelete,
		"/api/v1/roles/"+role["id"].(string)+"?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("removing a role the business made answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
}
