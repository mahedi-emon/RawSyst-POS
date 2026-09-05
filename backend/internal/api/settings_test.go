//go:build integration

// I1 — business settings after setup, over the routes the Back Office calls.
//
// What these cover is the gap the feature closed. Before it, a company could be
// created and never amended, and a branch could be created only by the setup
// wizard reading its own scratch answers. `UPDATE company` appeared nowhere in
// the product outside a receipt-number counter, and `UPDATE store` nowhere at
// all. So the tests here are less about validation than about the three things
// a business could not previously do: correct its own record, open a second
// branch, and fix the address its invoices are refused without.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func businessPath(f *shopFixture) string {
	return "/api/v1/companies/" + f.companyID.String()
}

func branchesPath(f *shopFixture) string {
	return businessPath(f) + "/branches"
}

// The record comes back with its branches, and says what may still be changed.
func TestTheBusinessRecordSaysWhatMayStillBeChanged(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet, businessPath(f), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read the business: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	business, ok := body["business"].(map[string]any)
	if !ok {
		t.Fatalf("no business in %v", body)
	}
	if business["legal_name"] == "" || business["legal_name"] == nil {
		t.Errorf("legal_name came back %v; a company always has one", business["legal_name"])
	}
	// The market is the tenant's and the country the company's. A screen shows
	// them together to explain why the country cannot be edited, so both must
	// arrive.
	if business["market"] != business["country"] {
		t.Errorf("market %v and country %v disagree; CommitBusinessInfo requires them equal",
			business["market"], business["country"])
	}
	if business["market_name"] == "" || business["market_name"] == nil {
		t.Errorf("market_name is %v; the screen names the market in prose",
			business["market_name"])
	}

	// Tolerances are strings. A tolerance decides whether a bill matches, and
	// JSON numbers are float64 by the time they reach a screen.
	for _, field := range []string{"match_tolerance_pct", "match_tolerance_amount"} {
		if _, isString := business[field].(string); !isString {
			t.Errorf("%s arrived as %T, want a string — money and tolerances never cross as floats",
				field, business[field])
		}
	}

	settled, ok := business["settled"].(map[string]any)
	if !ok {
		t.Fatalf("no settled map in %v", business)
	}
	// The country is settled for every company, always: the market is the
	// platform operator's decision and the tax engine reads it on every sale.
	if _, fixed := settled["country"]; !fixed {
		t.Error("country is not reported settled; it can never be changed by a client")
	}
	// This shop has sold something, so its books have entries and its stock has
	// moved. Both consequences must be reported.
	for _, field := range []string{"base_currency", "fiscal_year_start_month", "costing_method"} {
		why, fixed := settled[field].(string)
		if !fixed {
			t.Errorf("%s is not settled, but this shop has already posted and sold", field)
			continue
		}
		if why == "" {
			t.Errorf("%s is settled with no reason; the screen renders the reason", field)
		}
	}

	branches, ok := body["branches"].([]any)
	if !ok || len(branches) == 0 {
		t.Fatalf("no branches in %v; the settings screen shows them with the company", body)
	}
}

// The three fields a shop most often has to correct, and could not.
func TestABusinessCorrectsItsOwnRecord(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, businessPath(f), f.token, map[string]any{
		"legal_name":    "Corrected Trading Company",
		"legal_name_ar": "شركة التجارة المصححة",
		"cr_number":     "1010101010",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("amend: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	saved := decodeJSON(t, resp)["business"].(map[string]any)
	if saved["legal_name"] != "Corrected Trading Company" {
		t.Errorf("legal_name = %v, want the corrected one", saved["legal_name"])
	}
	if saved["cr_number"] != "1010101010" {
		t.Errorf("cr_number = %v; the storefront disclosure reports this missing "+
			"and had no way to be given one", saved["cr_number"])
	}

	// A partial amendment leaves everything it did not name alone. This is the
	// property a tabbed settings screen depends on: saving one tab must not
	// blank the fields on the others.
	resp = h.do(t, http.MethodPut, businessPath(f), f.token, map[string]any{
		"trade_name": "Corner Shop",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second amend: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	after := decodeJSON(t, resp)["business"].(map[string]any)
	if after["cr_number"] != "1010101010" {
		t.Errorf("cr_number = %v after amending only the trade name; "+
			"an absent field must not be treated as a blank one", after["cr_number"])
	}
	if after["legal_name"] != "Corrected Trading Company" {
		t.Errorf("legal_name = %v after a partial save", after["legal_name"])
	}

	// A blank string IS an answer, and clears the field. That is the other half
	// of the same distinction.
	resp = h.do(t, http.MethodPut, businessPath(f), f.token, map[string]any{
		"trade_name": "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear the trade name: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	cleared := decodeJSON(t, resp)["business"].(map[string]any)
	if cleared["trade_name"] != "" {
		t.Errorf("trade_name = %v after being sent blank; blank is a value, not an absence",
			cleared["trade_name"])
	}
}

// Naming a settled field is refused with the reason, not ignored.
func TestASettledFieldIsRefusedWithItsReason(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, businessPath(f), f.token, map[string]any{
		"country": "bd",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("changing the country: status %d — %s, want 409",
			resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "immutable" {
		t.Errorf("code = %v, want immutable", failure["code"])
	}
	fields, _ := failure["fields"].(map[string]any)
	if why, _ := fields["country"].(string); why == "" {
		t.Errorf("no reason given against country in %v; a refusal must say why", failure)
	}

	// And it did not take effect.
	resp = h.do(t, http.MethodGet, businessPath(f), f.token, nil)
	business := decodeJSON(t, resp)["business"].(map[string]any)
	if business["country"] == "bd" {
		t.Error("the country changed despite the refusal")
	}

	// The same for a field settled by work already done rather than by policy.
	resp = h.do(t, http.MethodPut, businessPath(f), f.token, map[string]any{
		"costing_method": "fifo",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("changing the costing method after stock moved: status %d, want 409",
			resp.StatusCode)
	}
}

// Validation speaks in the vocabulary of the form, per field.
func TestBusinessValidationNamesTheFieldAndSaysWhat(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a timezone that does not exist",
			map[string]any{"timezone": "Mars/Olympus"}, "timezone"},
		{"an empty legal name",
			map[string]any{"legal_name": "   "}, "legal_name"},
		{"a SARIE code in lower case",
			map[string]any{"wps_bank_sarie_id": "rjhi"}, "wps_bank_sarie_id"},
		{"a establishment id with letters in it",
			map[string]any{"mol_establishment_id": "AB12"}, "mol_establishment_id"},
		{"a tolerance over one hundred percent",
			map[string]any{"match_tolerance_pct": "150"}, "match_tolerance_pct"},
		{"a deadline that is not a date",
			map[string]any{"zatca_deadline": "next Tuesday"}, "zatca_deadline"},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do(t, http.MethodPut, businessPath(f), f.token, c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d — %s, want 400", resp.StatusCode, readBody(t, resp))
			}
			failure, _ := decodeJSON(t, resp)["error"].(map[string]any)
			fields, _ := failure["fields"].(map[string]any)
			if _, named := fields[c.field]; !named {
				t.Errorf("no message against %s; got %v", c.field, fields)
			}
		})
	}
}

// A shop opens a second branch, addresses it, and closes it.
func TestAShopOpensAddressesAndClosesABranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// A branch is recorded before its address is known. A shop opening next
	// week knows the name before the postal code, and refusing the branch until
	// the address is complete would leave them unable to start.
	resp := h.do(t, http.MethodPost, branchesPath(f), f.token, map[string]any{
		"code": "KHB", "name": "Khobar Branch",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open a branch: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	branch := decodeJSON(t, resp)["branch"].(map[string]any)
	branchID, _ := branch["id"].(string)
	if branchID == "" {
		t.Fatalf("no id on the new branch: %v", branch)
	}

	// It exists, and says honestly that it cannot invoice yet, naming what is
	// missing rather than leaving it to be discovered at the till.
	if branch["can_invoice"] != false {
		t.Errorf("a branch with no address reports can_invoice %v; "+
			"sales/document.go refuses to issue one", branch["can_invoice"])
	}
	missing, _ := branch["incomplete"].([]any)
	if len(missing) == 0 {
		t.Error("nothing reported incomplete on a branch with no street or postal code")
	}

	// The country is inherited from the company rather than demanded again.
	if branch["effective_country_code"] == "" {
		t.Error("no effective country; sales/document.go falls back to the company's")
	}

	// Address it, and it can trade.
	resp = h.do(t, http.MethodPut, branchesPath(f)+"/"+branchID, f.token, map[string]any{
		"street": "Prince Sultan Road", "district": "Al Ulaya", "city": "Khobar",
		"building_number": "4321", "postal_code": "34445",
		"name_ar": "فرع الخبر", "phone": "+966500000000",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("address the branch: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	addressed := decodeJSON(t, resp)["branch"].(map[string]any)
	if addressed["can_invoice"] != true {
		t.Errorf("still cannot invoice with a complete address: %v", addressed["incomplete"])
	}
	if addressed["name_ar"] != "فرع الخبر" {
		t.Errorf("name_ar = %v; CommitStores had no Arabic name at all", addressed["name_ar"])
	}

	// Closing is deactivation, never deletion: the branch's code is inside
	// every document number it ever issued.
	resp = h.do(t, http.MethodPut, branchesPath(f)+"/"+branchID, f.token, map[string]any{
		"is_active": false,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close the branch: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if decodeJSON(t, resp)["branch"].(map[string]any)["is_active"] != false {
		t.Error("the branch is still active after being closed")
	}

	// And it is still listed, below the open ones, so it can be reopened.
	resp = h.do(t, http.MethodGet, businessPath(f), f.token, nil)
	branches, _ := decodeJSON(t, resp)["branches"].([]any)
	var found bool
	for _, b := range branches {
		if b.(map[string]any)["id"] == branchID {
			found = true
		}
	}
	if !found {
		t.Error("a closed branch vanished from the list; it could never be reopened")
	}
}

// A branch code is unique within the company, and the refusal says so.
func TestABranchCodeCannotBeTakenTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, branchesPath(f), f.token, map[string]any{
		"code": "DUP", "name": "First",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first branch: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp = h.do(t, http.MethodPost, branchesPath(f), f.token, map[string]any{
		"code": "DUP", "name": "Second",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate code: status %d — %s, want 409",
			resp.StatusCode, readBody(t, resp))
	}
}

// Opening a branch is refused at the plan ceiling — and, unlike CommitStores,
// AMENDING one at the ceiling is not.
//
// CommitStores checks `existing + len(payload) > ceiling`. It upserts by code,
// so a shop at its ceiling editing the branches it already has is counted as
// creating them again and refused. This asserts the amendment path does not
// share that arithmetic.
func TestTheBranchCeilingCountsAdditionsNotAmendments(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// One spare place, so a single addition succeeds and the next is over the
	// line. Set rather than counted up to, so the test does not depend on what
	// the seeded plan happens to allow.
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE tenant_limit
			   SET max_stores = (SELECT count(*) + 1 FROM store WHERE company_id = $2)
			 WHERE tenant_id = $1`, f.tenantID, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("set the ceiling: %v", err)
	}

	resp := h.do(t, http.MethodPost, branchesPath(f), f.token, map[string]any{
		"code": "LAST", "name": "The Last Place",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the last allowed branch: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	lastID, _ := decodeJSON(t, resp)["branch"].(map[string]any)["id"].(string)

	// One more is refused, and says what the plan allows.
	resp = h.do(t, http.MethodPost, branchesPath(f), f.token, map[string]any{
		"code": "OVER", "name": "One Too Many",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("past the ceiling: status %d — %s, want a refusal",
			resp.StatusCode, readBody(t, resp))
	}
	failure, _ := decodeJSON(t, resp)["error"].(map[string]any)
	if failure["code"] != "plan_limit_reached" {
		t.Errorf("code = %v, want plan_limit_reached", failure["code"])
	}

	// But amending a branch at the ceiling still works. A shop that has used
	// its whole allowance can still correct the branches it has — which is the
	// arithmetic CommitStores gets wrong, counting an upsert of what already
	// exists as though it were an addition.
	resp = h.do(t, http.MethodPut, branchesPath(f)+"/"+lastID, f.token, map[string]any{
		"name": "Renamed At The Ceiling",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("amending at the ceiling: status %d — %s; "+
			"an amendment is not an addition", resp.StatusCode, readBody(t, resp))
	}
}

// A branch with no country of its own can still invoice, because the invoice
// takes the company's.
//
// This is a regression. The readiness check was first written to require
// store.country_code, and sales/document.go does not: it falls back to
// company.country when the branch has set none, upper-cased for BT-40. So every
// branch that had never stated a country — which is every branch the
// development seeder makes, and every branch created before the column was
// filled in — was reported as unable to trade, on a screen whose whole job is to
// tell a shop whether it can.
func TestABranchWithNoCountryOfItsOwnStillInvoices(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Complete the address except for the country, and clear the country
	// outright — the state a branch is in when nobody ever answered that
	// question.
	resp := h.do(t, http.MethodPut,
		branchesPath(f)+"/"+f.storeID.String(), f.token, map[string]any{
			"street": "King Fahd Road", "district": "Al Olaya", "city": "Riyadh",
			"building_number": "1234", "postal_code": "12211",
			"country_code": "",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("address the branch: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	branch := decodeJSON(t, resp)["branch"].(map[string]any)

	if branch["country_code"] != "" {
		t.Errorf("country_code = %v; the form shows the field as stored, and it is blank",
			branch["country_code"])
	}
	if branch["effective_country_code"] == "" {
		t.Error("no effective country; the invoice takes the company's when the branch has none")
	}
	if branch["can_invoice"] != true {
		t.Errorf("a branch with a complete address and no country of its own "+
			"reports can_invoice %v, incomplete %v — sales/document.go would issue this invoice",
			branch["can_invoice"], branch["incomplete"])
	}
}

// A branch of another company in the same tenant is not amendable through this
// company's path, even though the caller can see both.
func TestABranchIsConfinedToTheCompanyNamedInThePath(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	other := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut,
		branchesPath(f)+"/"+other.storeID.String(), f.token, map[string]any{
			"name": "Reached Across",
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("amending another company's branch: status %d — %s, want 404",
			resp.StatusCode, readBody(t, resp))
	}
}
