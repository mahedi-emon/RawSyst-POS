//go:build integration

// Categories, brands and units — the three tables a product hangs off, and the
// three nothing in the product could reach.
//
// `POST /catalog/products` accepts `category_id`, `brand_id` and `unit_id`.
// Before these routes there was no way to list any of the three and no way to
// create one, so all three parameters took an id that no part of the product
// could produce. `devseed` writes ten categories, which is why development
// looked populated while a real business had none and no way to make one.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func taxonomyPath(f *shopFixture, base string) string {
	return base + "?company_id=" + f.companyID.String()
}

// createCategory posts one and answers its id.
func createCategory(
	t *testing.T, h *harness, f *shopFixture, body map[string]any,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		taxonomyPath(f, "/api/v1/catalog/categories"), f.token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating a category: %d %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSONFrom(t, resp)["id"].(string)
	return id
}

func listCategories(t *testing.T, h *harness, f *shopFixture, query string) []any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		taxonomyPath(f, "/api/v1/catalog/categories")+query, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing categories: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSONFrom(t, resp)["data"].([]any)
	return rows
}

func categoryByID(rows []any, id string) map[string]any {
	for _, row := range rows {
		r, _ := row.(map[string]any)
		if got, _ := r["id"].(string); got == id {
			return r
		}
	}
	return nil
}

// A department can be created, nested, and used on a product.
//
// The last part is the point: the whole reason these routes exist is that
// `category_id` on a product took an id nothing could produce.
func TestACategoryCanBeCreatedAndPutOnAProduct(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	parent := createCategory(t, h, f, map[string]any{
		"name": "Menswear", "name_ar": "ملابس رجالية",
	})
	child := createCategory(t, h, f, map[string]any{
		"name": "Shirts", "parent_id": parent,
	})

	rows := listCategories(t, h, f, "")
	got := categoryByID(rows, child)
	if got == nil {
		t.Fatalf("the child category is not in the list of %d", len(rows))
	}
	if depth, _ := got["depth"].(float64); depth != 1 {
		t.Errorf("the child's depth is %v, want 1", got["depth"])
	}
	if parentID, _ := got["parent_id"].(string); parentID != parent {
		t.Errorf("the child names parent %q, want %q", parentID, parent)
	}
	if arabic, _ := categoryByID(rows, parent)["name_ar"].(string); arabic != "ملابس رجالية" {
		t.Errorf("the Arabic name came back as %q", arabic)
	}

	// And a product can now be filed under it.
	made := h.do(t, http.MethodPost, "/api/v1/catalog/products", f.token,
		map[string]any{
			"company_id": f.companyID.String(),
			"sku":        "SHIRT-1", "name": "Oxford shirt",
			"category_id": child, "tax_treatment": "standard",
		})
	body := readBody(t, made)
	made.Body.Close()
	if made.StatusCode != http.StatusCreated {
		t.Fatalf("filing a product under a category: %d %s", made.StatusCode, body)
	}

	// The count on the list is what answers "may I retire this".
	if count, _ := categoryByID(listCategories(t, h, f, ""), child)["product_count"].(float64); count != 1 {
		t.Errorf("the category reports %v products, want 1", count)
	}
}

// Moving a department takes its subtree with it.
func TestMovingACategoryMovesWhatIsUnderIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	menswear := createCategory(t, h, f, map[string]any{"name": "Menswear"})
	clothing := createCategory(t, h, f, map[string]any{"name": "Clothing"})
	shirts := createCategory(t, h, f, map[string]any{
		"name": "Shirts", "parent_id": menswear,
	})
	oxford := createCategory(t, h, f, map[string]any{
		"name": "Oxford", "parent_id": shirts,
	})

	// Menswear moves under Clothing. Everything beneath it goes one deeper.
	moved := h.do(t, http.MethodPut,
		taxonomyPath(f, "/api/v1/catalog/categories/"+menswear), f.token,
		map[string]any{"name": "Menswear", "parent_id": clothing})
	if moved.StatusCode != http.StatusOK {
		t.Fatalf("moving a category: %d %s", moved.StatusCode, readBody(t, moved))
	}
	moved.Body.Close()

	rows := listCategories(t, h, f, "")
	for id, want := range map[string]float64{
		menswear: 1, shirts: 2, oxford: 3,
	} {
		row := categoryByID(rows, id)
		if row == nil {
			t.Fatalf("%s vanished from the list after the move", id)
		}
		if depth, _ := row["depth"].(float64); depth != want {
			t.Errorf("%s is at depth %v after the move, want %v",
				row["name"], depth, want)
		}
	}

	// And the stored ancestry agrees with the parents, which is the thing that
	// would silently rot if the subtree rewrite were wrong.
	var path []string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT array(SELECT unnest(path)::text) FROM category WHERE id = $1`,
			oxford).Scan(&path)
	}); err != nil {
		t.Fatalf("read the stored ancestry: %v", err)
	}
	if len(path) != 3 || path[0] != clothing || path[1] != menswear || path[2] != shirts {
		t.Errorf("the deepest category's stored ancestry is %v, want "+
			"[clothing menswear shirts]", path)
	}
}

// A department cannot be filed under itself, or under its own subtree.
//
// Either would make the tree a ring, and every path computation on it would
// then run forever.
func TestACategoryCannotBeFiledInsideItsOwnBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	parent := createCategory(t, h, f, map[string]any{"name": "Menswear"})
	child := createCategory(t, h, f, map[string]any{
		"name": "Shirts", "parent_id": parent,
	})

	for name, body := range map[string]map[string]any{
		"under itself":    {"name": "Menswear", "parent_id": parent},
		"under its child": {"name": "Menswear", "parent_id": child},
	} {
		resp := h.do(t, http.MethodPut,
			taxonomyPath(f, "/api/v1/catalog/categories/"+parent), f.token, body)
		got := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("filing a category %s answered %d, want 400: %s",
				name, resp.StatusCode, got)
		}
	}
}

// Retiring a department retires the branch under it.
//
// A subcategory of a department nobody may file under any more is not one
// anybody may file under either.
func TestRetiringACategoryRetiresItsBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	parent := createCategory(t, h, f, map[string]any{"name": "Menswear"})
	child := createCategory(t, h, f, map[string]any{
		"name": "Shirts", "parent_id": parent,
	})

	off := h.do(t, http.MethodPost,
		taxonomyPath(f, "/api/v1/catalog/categories/"+parent+"/active"), f.token,
		map[string]any{"is_active": false})
	if off.StatusCode != http.StatusNoContent {
		t.Fatalf("retiring a category: %d %s", off.StatusCode, readBody(t, off))
	}
	off.Body.Close()

	live := listCategories(t, h, f, "")
	if categoryByID(live, parent) != nil {
		t.Error("a retired category is still offered")
	}
	if categoryByID(live, child) != nil {
		t.Error("a subcategory of a retired category is still offered")
	}

	// Both are still readable, because a product filed under one has to stay
	// explicable.
	all := listCategories(t, h, f, "&include_retired=true")
	if categoryByID(all, parent) == nil || categoryByID(all, child) == nil {
		t.Error("include_retired does not bring the retired branch back")
	}
}

// Another business's departments are not listed here, and cannot be renamed
// from here.
func TestCategoriesAreScopedToTheBusiness(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	theirCategory := createCategory(t, h, theirs, map[string]any{"name": "Theirs"})

	if categoryByID(listCategories(t, h, mine, "&include_retired=true"), theirCategory) != nil {
		t.Error("another business's category is listed here")
	}

	refused := h.do(t, http.MethodPut,
		taxonomyPath(mine, "/api/v1/catalog/categories/"+theirCategory), mine.token,
		map[string]any{"name": "Renamed by somebody else"})
	body := readBody(t, refused)
	refused.Body.Close()
	if refused.StatusCode != http.StatusNotFound {
		t.Errorf("renaming another business's category answered %d, want 404: %s",
			refused.StatusCode, body)
	}
}

// A brand can be created, renamed and retired, and two cannot share a name.
func TestBrandsCanBeManaged(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	made := h.do(t, http.MethodPost, taxonomyPath(f, "/api/v1/catalog/brands"),
		f.token, map[string]any{"name": "Adidas", "name_ar": "أديداس"})
	if made.StatusCode != http.StatusCreated {
		t.Fatalf("creating a brand: %d %s", made.StatusCode, readBody(t, made))
	}
	id, _ := decodeJSONFrom(t, made)["id"].(string)
	made.Body.Close()

	// `brand_name_uq` is on lower(name), so the second one is refused with a
	// sentence rather than a constraint name.
	again := h.do(t, http.MethodPost, taxonomyPath(f, "/api/v1/catalog/brands"),
		f.token, map[string]any{"name": "adidas"})
	body := readBody(t, again)
	again.Body.Close()
	if again.StatusCode != http.StatusConflict && again.StatusCode != http.StatusBadRequest {
		t.Errorf("a second brand with the same name answered %d, want a "+
			"refusal: %s", again.StatusCode, body)
	}

	renamed := h.do(t, http.MethodPut,
		taxonomyPath(f, "/api/v1/catalog/brands/"+id), f.token,
		map[string]any{"name": "Adidas Originals"})
	if renamed.StatusCode != http.StatusOK {
		t.Fatalf("renaming a brand: %d %s", renamed.StatusCode, readBody(t, renamed))
	}
	renamed.Body.Close()

	off := h.do(t, http.MethodPost,
		taxonomyPath(f, "/api/v1/catalog/brands/"+id+"/active"), f.token,
		map[string]any{"is_active": false})
	if off.StatusCode != http.StatusNoContent {
		t.Fatalf("retiring a brand: %d %s", off.StatusCode, readBody(t, off))
	}
	off.Body.Close()

	live := h.do(t, http.MethodGet, taxonomyPath(f, "/api/v1/catalog/brands"),
		f.token, nil)
	rows, _ := decodeJSONFrom(t, live)["data"].([]any)
	live.Body.Close()
	if len(rows) != 0 {
		t.Errorf("a retired brand is still offered (%d rows)", len(rows))
	}
}

// A unit can be created and used, and its code does not move.
func TestAUnitCanBeCreatedAndItsCodeIsFixed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	made := h.do(t, http.MethodPost, taxonomyPath(f, "/api/v1/catalog/units"),
		f.token, map[string]any{
			"code": "kg", "name": "Kilogram", "allows_fraction": true,
		})
	if made.StatusCode != http.StatusCreated {
		t.Fatalf("creating a unit: %d %s", made.StatusCode, readBody(t, made))
	}
	created := decodeJSONFrom(t, made)
	made.Body.Close()
	id, _ := created["id"].(string)
	if code, _ := created["code"].(string); code != "KG" {
		t.Errorf("the code was stored as %q, want KG — it is upper-cased so "+
			"an invoice line reads consistently", code)
	}
	if !created["allows_fraction"].(bool) {
		t.Error("a kilogram cannot be sold in fractions, which is the point of it")
	}

	// Amending changes the name and not the code: the code is on every invoice
	// line already issued.
	amended := h.do(t, http.MethodPut,
		taxonomyPath(f, "/api/v1/catalog/units/"+id), f.token,
		map[string]any{"code": "GRAM", "name": "Kilogramme", "allows_fraction": true})
	if amended.StatusCode != http.StatusOK {
		t.Fatalf("amending a unit: %d %s", amended.StatusCode, readBody(t, amended))
	}
	after := decodeJSONFrom(t, amended)
	amended.Body.Close()
	if code, _ := after["code"].(string); code != "KG" {
		t.Errorf("the code moved to %q; it must stay KG", code)
	}
	if name, _ := after["name"].(string); name != "Kilogramme" {
		t.Errorf("the name is %q, want Kilogramme", name)
	}
}

// A cashier may read the arrangement and may not change it.
//
// Reading is `catalog.view`, which a cashier holds: a till showing a menu of
// departments needs it. Changing is `catalog.edit`, which they do not.
func TestACashierCanReadTheArrangementAndNotChangeIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")

	for _, path := range []string{
		"/api/v1/catalog/categories", "/api/v1/catalog/brands",
		"/api/v1/catalog/units",
	} {
		resp := h.do(t, http.MethodGet, taxonomyPath(f, path), cashier, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("a cashier reading %s: %d, want 200", path, resp.StatusCode)
		}
	}

	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/v1/catalog/categories", map[string]any{"name": "Mine now"}},
		{"/api/v1/catalog/brands", map[string]any{"name": "Mine now"}},
		{"/api/v1/catalog/units", map[string]any{"code": "X", "name": "Mine now"}},
	} {
		resp := h.do(t, http.MethodPost, taxonomyPath(f, c.path), cashier, c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a cashier writing %s: %d, want 403", c.path, resp.StatusCode)
		}
	}
}
