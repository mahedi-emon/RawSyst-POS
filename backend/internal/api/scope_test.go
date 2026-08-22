//go:build integration

// The scope dimensions of design 04 §4.3, enforced over HTTP.
//
// A permission verb says WHAT an actor may do. Scope says WHERE, and up to how
// much. Both halves have existed in the schema and in `Grants` since 0003;
// until now only the verb was ever consulted, so a branch manager held
// `devices.manage` everywhere and a cashier with a SAR 50 ceiling could grant
// any discount at all. Every test here fails against that state.
//
// Row-level security does not help with any of this and is not meant to: two
// branches of one shop are the same tenant, so the rows are legitimately
// visible. Scope is the layer above it.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// scopeUser narrows a fresh assignment in the shop and signs that user in.
//
// The narrowing happens BEFORE the sign-in, and the cached grants are dropped,
// because grants are resolved once per user and held for a TTL — a limit
// written afterwards would not be seen by the request that tests it.
func (h *harness) scopeUser(
	t *testing.T, f *shopFixture, roleKey string,
	stores, warehouses []uuid.UUID, amountLimit string,
) string {
	t.Helper()
	email, userID := h.newUserInTenant(t, f.tenantID, roleKey)
	h.narrowAssignment(t, f.tenantID, userID, stores, warehouses, amountLimit)
	return h.login(t, email)
}

// limitUser puts a ceiling on the shop fixture's own cashier, who already holds
// the device-bound token the POS routes need.
func (h *harness) limitUser(t *testing.T, f *shopFixture, amountLimit string) {
	t.Helper()
	h.narrowAssignment(t, f.tenantID, f.userID, nil, nil, amountLimit)
}

func (h *harness) narrowAssignment(
	t *testing.T, tenantID, userID uuid.UUID,
	stores, warehouses []uuid.UUID, amountLimit string,
) {
	t.Helper()
	err := h.pool.TxAsTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		var limit *string
		if amountLimit != "" {
			limit = &amountLimit
		}
		if stores == nil {
			stores = []uuid.UUID{}
		}
		if warehouses == nil {
			warehouses = []uuid.UUID{}
		}
		_, e := tx.Exec(t.Context(), `
			UPDATE user_role_assignment
			SET store_ids     = $2,
			    warehouse_ids = $3,
			    amount_limit  = $4::numeric
			WHERE user_id = $1`,
			userID, stores, warehouses, limit)
		return e
	})
	if err != nil {
		t.Fatalf("narrow the assignment: %v", err)
	}
	h.authz.Invalidate(userID)
}

// secondBranch adds another store, with a warehouse of its own, to the shop.
func (h *harness) secondBranch(t *testing.T, f *shopFixture) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var storeID, warehouseID uuid.UUID
	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		// store_code_format allows [A-Z0-9-] only, so the hex has to be raised.
		code := "B" + strings.ToUpper(uuid.NewString()[:6])
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,$3,'Second Branch') RETURNING id`,
			f.tenantID, f.companyID, code).Scan(&storeID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,$4,'Second Store Room') RETURNING id`,
			f.tenantID, f.companyID, storeID, "W"+code).Scan(&warehouseID)
	})
	if err != nil {
		t.Fatalf("open a second branch: %v", err)
	}
	return storeID, warehouseID
}

// --- store scope ---------------------------------------------------------

// A manager confined to one branch registers tills in that branch only.
func TestABranchManagerRegistersTillsOnlyInTheirOwnBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	otherStore, _ := h.secondBranch(t, f)

	manager := h.scopeUser(t, f, "owner", []uuid.UUID{f.storeID}, nil, "")

	// Their own branch: ordinary work, and it must keep working. A scope check
	// that refused everything would pass a test that only looked for refusals.
	resp := h.do(t, http.MethodPost, devicesPath(f), manager, map[string]any{
		"store_id":       f.storeID.String(),
		"terminal_label": "Till 2",
		"egs_unit_id":    f.egsUnitID.String(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register a till in their own branch: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// The branch next door. Not found rather than forbidden: a manager should
	// not be able to enumerate the estate by probing store ids.
	resp = h.do(t, http.MethodPost, devicesPath(f), manager, map[string]any{
		"store_id":       otherStore.String(),
		"terminal_label": "Till 3",
		"egs_unit_id":    f.egsUnitID.String(),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("register a till in a branch they are not scoped to: status %d, "+
			"want 404 — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// Nor can they move an existing till out to a branch that is not theirs.
func TestABranchManagerCannotMoveATillToAnotherBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	otherStore, _ := h.secondBranch(t, f)

	manager := h.scopeUser(t, f, "owner", []uuid.UUID{f.storeID}, nil, "")

	resp := h.do(t, http.MethodPut, devicePath(f), manager,
		map[string]any{"terminal_label": "Till 1", "store_id": otherStore.String()})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("move a till into a branch they are not scoped to: status %d, "+
			"want 404 — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Renaming in place still works: the scope is on the branch, not on the
	// terminal, and a manager who cannot rename their own till is broken.
	resp = h.do(t, http.MethodPut, devicePath(f), manager,
		map[string]any{"terminal_label": "Front Till"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("rename their own till: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// A branch manager cannot read the branch next door's takings.
func TestABranchManagerCannotReportOnAnotherBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	otherStore, _ := h.secondBranch(t, f)

	manager := h.scopeUser(t, f, "owner", []uuid.UUID{f.storeID}, nil, "")
	base := "/api/v1/reports/trial-balance?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodGet, base+"&store_id="+f.storeID.String(), manager, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("their own branch's trial balance: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, base+"&store_id="+otherStore.String(), manager, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("another branch's trial balance: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// An unscoped user is confined to nothing, which means every branch.
//
// The common case, and the one that would break the whole product if the check
// had the sense of the comparison backwards.
func TestAnUnscopedUserReachesEveryBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	otherStore, _ := h.secondBranch(t, f)

	owner := h.seedUserIn(t, f, "owner")
	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/trial-balance?company_id="+f.companyID.String()+
			"&store_id="+otherStore.String(), owner, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("an unscoped owner reading a branch: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// --- warehouse scope -----------------------------------------------------

// Inventory staff scoped to one warehouse order goods into that one only.
func TestAWarehouseScopedBuyerOrdersIntoTheirOwnWarehouse(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	_, otherWarehouse := h.secondBranch(t, f.shopFixture)

	// The buying fixture's own owner is narrowed, rather than a second one
	// added: a system role is cloned into a tenant once, so two users cannot
	// both hold "owner" here.
	own := uuid.MustParse(f.warehouseID)
	h.narrowAssignment(t, f.tenantID, f.userID, nil, []uuid.UUID{own}, "")
	buyer := f.token

	order := func(warehouseID uuid.UUID) *http.Response {
		return h.do(t, http.MethodPost, f.path("/api/v1/purchasing/orders"), buyer,
			map[string]any{
				"supplier_id":  f.supplierID,
				"warehouse_id": warehouseID.String(),
				"lines": []map[string]any{{
					"variant_id": f.variantID.String(),
					"qty":        "10",
					"unit_cost":  "50.00",
				}},
			})
	}

	resp := order(own)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("order into their own warehouse: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = order(otherWarehouse)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("order into a warehouse they are not scoped to: status %d, "+
			"want 404 — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// --- amount ceiling ------------------------------------------------------

// A cashier's discount ceiling is weighed on the document, not per line.
//
// The per-line reading is the one worth testing against: a ceiling that looked
// at each line separately would be sidestepped by anyone who could press the
// discount key twice.
func TestACashiersDiscountCeilingHoldsAcrossTheWholeSale(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Fifty riyals, the blueprint's own example.
	h.limitUser(t, f, "50.00")

	within := oneItemSale(f, uuid.New(), "1", "500.00", "460.00")
	within["invoice_discount"] = "40.00"
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, within)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a discount inside the ceiling: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	over := oneItemSale(f, uuid.New(), "1", "500.00", "440.00")
	over["invoice_discount"] = "60.00"
	resp = h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, over)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a discount above the ceiling: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got := errorCodeOf(body); got != "amount_limit_exceeded" {
		t.Errorf("error code = %q, want amount_limit_exceeded — %v", got, body)
	}

	// Split in two, each half under the ceiling, together over it. The whole
	// point of weighing the document rather than the line.
	split := oneItemSale(f, uuid.New(), "1", "500.00", "440.00")
	split["invoice_discount"] = "30.00"
	lines, _ := split["lines"].([]map[string]any)
	lines[0]["line_discount"] = "30.00"
	resp = h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, split)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a discount split across the document to stay under the "+
			"ceiling: status %d, want 403 — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// The same ceiling on a refund, which is design 04 §4.5's own example.
func TestARefundAboveTheCashiersCeilingIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	invoiceID, lineID := sellOne(t, h, f, "1", "500.00", "500.00")
	h.limitUser(t, f, "50.00")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    uuid.NewString(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-16T10:30:00Z",
		"reason":              "Customer changed their mind",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "500.00"}},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a refund ten times the ceiling: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	if got := errorCodeOf(decodeJSON(t, resp)); got != "amount_limit_exceeded" {
		t.Errorf("error code = %q, want amount_limit_exceeded", got)
	}
}

// --- what the client is told ---------------------------------------------

// /auth/me reports the confinement, so the client can shape itself around it.
func TestMeReportsTheBranchesAUserIsConfinedTo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Unscoped: no list at all. An empty list would read as "no branches",
	// which is the opposite of what it means.
	unscoped := h.seedUserIn(t, f, "owner")
	body := decodeJSON(t, h.do(t, http.MethodGet, "/api/v1/auth/me", unscoped, nil))
	if _, present := body["store_scope"]; present {
		t.Errorf("an unscoped user was sent a store_scope: %v", body["store_scope"])
	}
	if _, present := body["amount_limit"]; present {
		t.Errorf("an unlimited user was sent an amount_limit: %v", body["amount_limit"])
	}

	// An auditor, because the owner clone above is this tenant's only one.
	manager := h.scopeUser(t, f, "auditor", []uuid.UUID{f.storeID}, nil, "500.00")
	body = decodeJSON(t, h.do(t, http.MethodGet, "/api/v1/auth/me", manager, nil))

	scope, _ := body["store_scope"].([]any)
	if len(scope) != 1 || scope[0] != f.storeID.String() {
		t.Errorf("store_scope = %v, want just %s", body["store_scope"], f.storeID)
	}
	if body["amount_limit"] != "500.00" {
		t.Errorf("amount_limit = %v, want the string 500.00", body["amount_limit"])
	}
}

// errorCodeOf reads the machine-readable code out of the error envelope. The
// message is for a person; a test asserting on prose breaks when the prose is
// improved, which is the wrong thing to make expensive.
func errorCodeOf(body map[string]any) string {
	envelope, _ := body["error"].(map[string]any)
	code, _ := envelope["code"].(string)
	return code
}

// The terminal routes resolve the company from the request or from a registered
// device, and a back-office caller has no device — so they must name it.
func devicesPath(f *shopFixture) string {
	return "/api/v1/devices?company_id=" + f.companyID.String()
}

func devicePath(f *shopFixture) string {
	return "/api/v1/devices/" + f.deviceID.String() +
		"?company_id=" + f.companyID.String()
}
