//go:build integration

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Provisioning an EGS unit, and binding a terminal to one.
//
// The property most of these protect is the one Z1 exists for: a terminal
// registered through the product must be able to sell. Between 0013 and 0043
// it could not — `egs_unit_id` was nullable, nothing in the product set it, and
// the till discovered the problem on its first sale.

// --- helpers ---------------------------------------------------------------

func createUnit(
	t *testing.T, h *harness, f *shopFixture, body map[string]any,
) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/einvoicing/units?company_id="+f.companyID.String(), f.token, body)
}

// smartPOSUnit creates a unit in the fixture's branch and returns its id.
func smartPOSUnit(t *testing.T, h *harness, f *shopFixture, label string) string {
	t.Helper()
	resp := createUnit(t, h, f, map[string]any{
		"label":        label,
		"architecture": "smart_pos",
		"store_id":     f.storeID.String(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create unit %s: %d %s", label, resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

// --- Provisioning ----------------------------------------------------------

// The nine CSR fields survive the round trip exactly as they were typed. Each
// one is only entered once and a silently dropped field is discovered at
// onboarding, months later.
func TestAnEGSUnitCapturesAllNineCSRFields(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	csr := map[string]any{
		"common_name":             "Till 2",
		"egs_serial_number":       "1-RawSyst|2-POS|3-000001",
		"organization_identifier": "300000000000003",
		"organization_unit":       "Main Branch",
		"organization_name":       "Test Trading Co",
		"country":                 "sa",
		"invoice_type":            "1100",
		"location":                "Riyadh",
		"industry":                "Retail",
	}
	resp := createUnit(t, h, f, map[string]any{
		"label": "Counter 1", "architecture": "smart_pos",
		"store_id": f.storeID.String(), "csr": csr,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, readBody(t, resp))
	}

	body := decodeJSON(t, resp)
	got, _ := body["csr"].(map[string]any)
	for k, want := range csr {
		if got[k] != want {
			t.Errorf("csr.%s came back as %v, want %v", k, got[k], want)
		}
	}
	if complete, _ := body["csr_complete"].(bool); !complete {
		t.Error("a unit with all nine fields does not report itself ready")
	}
}

// A unit saves with the CSR blank. All nine are mandatory at ONBOARDING, which
// is a later act; refusing to save until a shop has tracked down its industry
// classification would stop them setting up the till they need today.
func TestAnEGSUnitSavesBeforeItsCSRIsComplete(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := createUnit(t, h, f, map[string]any{
		"label": "Counter 2", "architecture": "smart_pos",
		"store_id": f.storeID.String(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if complete, _ := decodeJSON(t, resp)["csr_complete"].(bool); complete {
		t.Error("an empty CSR reports itself ready for onboarding")
	}
}

// The two formats the database also enforces are refused at the boundary, with
// a message naming the field. A malformed VAT number is rejected by ZATCA at
// onboarding, which is a support call rather than a visible failure.
func TestMalformedCSRValuesAreRefusedWithTheFieldNamed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, tc := range []struct {
		name, field string
		csr         map[string]any
	}{
		{"a VAT number that is not 15 digits from 3 to 3",
			"csr.organization_identifier",
			map[string]any{"organization_identifier": "100000000000001"}},
		{"a functionality map that is not four 0/1 digits",
			"csr.invoice_type",
			map[string]any{"invoice_type": "1234"}},
		{"a branch name where a VAT group needs a member tax number",
			"csr.organization_unit",
			map[string]any{
				"organization_identifier": "300000000010003",
				"organization_unit":       "Main Branch",
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := createUnit(t, h, f, map[string]any{
				"label": "Unit " + tc.field, "architecture": "smart_pos",
				"store_id": f.storeID.String(), "csr": tc.csr,
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
			}
			if msg := readBody(t, resp); !strings.Contains(msg, tc.field) {
				t.Errorf("the refusal does not name %s: %s", tc.field, msg)
			}
		})
	}
}

// The architecture decides whether a branch is required, and 0013 enforces it
// as a check constraint. Both halves are refused before Postgres sees them, so
// the caller gets a sentence rather than a constraint name.
func TestTheArchitectureDecidesWhetherABranchIsRequired(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	noBranch := createUnit(t, h, f, map[string]any{
		"label": "Branchless", "architecture": "branch_server",
	})
	if noBranch.StatusCode != http.StatusBadRequest {
		t.Errorf("a branch server with no branch was accepted (%d)", noBranch.StatusCode)
	}

	central := createUnit(t, h, f, map[string]any{
		"label": "Central with a branch", "architecture": "centralized_server",
		"store_id": f.storeID.String(),
	})
	if central.StatusCode != http.StatusBadRequest {
		t.Errorf("a central unit tied to one branch was accepted (%d)", central.StatusCode)
	}

	ok := createUnit(t, h, f, map[string]any{
		"label": "Head office", "architecture": "centralized_server",
	})
	if ok.StatusCode != http.StatusCreated {
		t.Fatalf("a central unit with no branch was refused: %s", readBody(t, ok))
	}
	if store, present := decodeJSON(t, ok)["store_id"]; present && store != "" {
		t.Errorf("a central unit reports a branch: %v", store)
	}
}

// The CSID is read-only. There is no route that sets one, and a body that tries
// must not become one by accident: asserting a certificate the unit does not
// hold is the exact class of unverifiable claim the design refuses to make.
//
// The request is refused outright rather than quietly stripped, because the
// platform decoder rejects fields it does not know. A caller that believes it
// onboarded a unit is worse off than one told it cannot.
func TestCreatingAUnitCannotAssertACSID(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Asserted here so that adding a writable CSID field later fails this test
	// rather than shipping quietly.
	refused := createUnit(t, h, f, map[string]any{
		"label": "Optimistic", "architecture": "smart_pos",
		"store_id":    f.storeID.String(),
		"csid_status": "live", "csid_serial": "PRETEND",
	})
	if refused.StatusCode != http.StatusBadRequest {
		t.Fatalf("a body asserting a CSID returned %d, want 400: %s",
			refused.StatusCode, readBody(t, refused))
	}

	// And a unit created the only way there is starts with no certificate.
	resp := createUnit(t, h, f, map[string]any{
		"label": "Optimistic", "architecture": "smart_pos",
		"store_id": f.storeID.String(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got, _ := body["csid_status"].(string); got != "not_started" {
		t.Errorf("csid_status = %q, want not_started", got)
	}
	if got, _ := body["csid_serial"].(string); got != "" {
		t.Errorf("a CSID serial was accepted from the request: %q", got)
	}
}

// The architecture is chosen once, because it decides where the private signing
// key lives. Amending must not move it.
func TestTheArchitectureCannotBeChangedAfterCreation(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	unitID := smartPOSUnit(t, h, f, "Counter 3")

	resp := h.do(t, http.MethodPut,
		"/api/v1/einvoicing/units/"+unitID+"?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"label": "Counter 3", "architecture": "centralized_server",
			"store_id": f.storeID.String(),
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("amend: %s", readBody(t, resp))
	}
	if got, _ := decodeJSON(t, resp)["architecture"].(string); got != "smart_pos" {
		t.Errorf("architecture = %q; it was changed by an amend", got)
	}
}

// --- Isolation --------------------------------------------------------------

// M8: another tenant's unit reads as absent, not as forbidden.
func TestAnotherTenantsEGSUnitIsNotFound(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	theirUnit := smartPOSUnit(t, h, theirs, "Their Counter")

	read := h.do(t, http.MethodGet,
		"/api/v1/einvoicing/units/"+theirUnit+"?company_id="+mine.companyID.String(),
		mine.token, nil)
	if read.StatusCode != http.StatusNotFound {
		t.Errorf("reading another tenant's unit returned %d, want 404", read.StatusCode)
	}

	amend := h.do(t, http.MethodPut,
		"/api/v1/einvoicing/units/"+theirUnit+"?company_id="+mine.companyID.String(),
		mine.token, map[string]any{"label": "Hijacked"})
	if amend.StatusCode != http.StatusNotFound {
		t.Errorf("amending another tenant's unit returned %d, want 404", amend.StatusCode)
	}
}

// A unit cannot be attached to a branch of a company the caller is not in. The
// unit carries the VAT registration the invoice chain hangs from, so this is
// the same boundary the terminal routes defend.
func TestAnEGSUnitCannotBeCreatedInAnotherCompanysBranch(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := createUnit(t, h, mine, map[string]any{
		"label": "Smuggled", "architecture": "branch_server",
		"store_id": theirs.storeID.String(),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a unit was created in another company's branch (%d): %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Seeing the units is not the same permission as creating one: a unit decides
// how many invoice chains a business has and which registration they hang from.
func TestAStoreManagerSeesUnitsAndCannotCreateOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	manager := h.seedUserIn(t, f, "store_manager")

	list := h.do(t, http.MethodGet,
		"/api/v1/einvoicing/units?company_id="+f.companyID.String(), manager, nil)
	if list.StatusCode != http.StatusOK {
		t.Errorf("a store manager cannot see the units: %d %s",
			list.StatusCode, readBody(t, list))
	}

	create := h.do(t, http.MethodPost,
		"/api/v1/einvoicing/units?company_id="+f.companyID.String(), manager,
		map[string]any{
			"label": "Manager's unit", "architecture": "smart_pos",
			"store_id": f.storeID.String(),
		})
	if create.StatusCode != http.StatusForbidden {
		t.Errorf("a store manager created an EGS unit (%d)", create.StatusCode)
	}
}

// --- Binding a terminal ------------------------------------------------------

// The whole point of Z1: a terminal registered through the product ends up with
// a unit, and therefore with the thing the till checks before it will sell.
func TestARegisteredTerminalIsBoundToItsEGSUnit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	unitID := smartPOSUnit(t, h, f, "Counter 4")

	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"store_id": f.storeID.String(), "terminal_label": "Till 9",
			"egs_unit_id": unitID,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got, _ := body["egs_unit_id"].(string); got != unitID {
		t.Errorf("egs_unit_id = %q, want %q", got, unitID)
	}
	if got, _ := body["egs_unit"].(string); got != "Counter 4" {
		t.Errorf("the terminal does not name its unit: %q", got)
	}

	// And the column is genuinely set, not only reported.
	var bound string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT egs_unit_id::text FROM device WHERE id = $1::uuid`,
			body["id"]).Scan(&bound)
	}); err != nil {
		t.Fatalf("read the device back: %v", err)
	}
	if bound != unitID {
		t.Errorf("the device row points at %q, want %q", bound, unitID)
	}
}

// Registering without a unit is refused on the way in, naming the field. The
// alternative — the behaviour before Z1 — was a terminal that paired, reported
// itself healthy and refused the first sale a cashier attempted.
func TestATerminalCannotBeRegisteredWithoutAnEGSUnit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{"store_id": f.storeID.String(), "terminal_label": "Orphan"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
	}
	if msg := readBody(t, resp); !strings.Contains(msg, "egs_unit_id") {
		t.Errorf("the refusal does not name the field: %s", msg)
	}
}

// A unit from another business must never be bindable. The two terminals share
// a tenant in this test, so row-level security would not catch the substitution
// — the company check in the query is what does.
func TestATerminalCannotBeBoundToAnotherCompanysUnit(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	theirUnit := smartPOSUnit(t, h, theirs, "Their Counter")

	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+mine.companyID.String(), mine.token,
		map[string]any{
			"store_id": mine.storeID.String(), "terminal_label": "Borrowed",
			"egs_unit_id": theirUnit,
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a terminal was bound to another company's unit (%d): %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Picking a unit that signs for a different branch is a mistake, and refused
// with the field named rather than silently accepted.
func TestATerminalCannotPickAUnitFromAnotherBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	var second string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO store (tenant_id, company_id, code, name, street, building_number, district, city, postal_code, country_code)
			VALUES ($1,$2,'BR9','Ninth Branch','Prince Sultan Road','2322','Al-Murabba','Riyadh','23333','SA') RETURNING id::text`,
			f.tenantID, f.companyID).Scan(&second)
	}); err != nil {
		t.Fatalf("seed a second store: %v", err)
	}

	unitID := smartPOSUnit(t, h, f, "Counter 5") // in the fixture's branch

	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"store_id": second, "terminal_label": "Elsewhere",
			"egs_unit_id": unitID,
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
	}
}

// A central unit covers every branch, so any terminal may sign under it.
func TestACentralUnitSignsForEveryBranch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	created := createUnit(t, h, f, map[string]any{
		"label": "Group head office", "architecture": "centralized_server",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create a central unit: %s", readBody(t, created))
	}
	unitID, _ := decodeJSON(t, created)["id"].(string)

	var second string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO store (tenant_id, company_id, code, name, street, building_number, district, city, postal_code, country_code)
			VALUES ($1,$2,'BR8','Eighth Branch','Prince Sultan Road','2322','Al-Murabba','Riyadh','23333','SA') RETURNING id::text`,
			f.tenantID, f.companyID).Scan(&second)
	}); err != nil {
		t.Fatalf("seed a second store: %v", err)
	}

	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"store_id": second, "terminal_label": "Far Till",
			"egs_unit_id": unitID,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a central unit was refused for another branch: %s", readBody(t, resp))
	}
}

// The repair path for every terminal registered before Z1: it has no unit, and
// amending is how it gets one.
func TestATerminalWithNoUnitCanBeGivenOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// The state 0013 left behind, written directly because the product can no
	// longer produce it.
	var orphan string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO device (tenant_id, company_id, store_id, terminal_label, status)
			VALUES ($1,$2,$3,'Legacy Till','pending') RETURNING id::text`,
			f.tenantID, f.companyID, f.storeID).Scan(&orphan)
	}); err != nil {
		t.Fatalf("seed an unbound terminal: %v", err)
	}

	unitID := smartPOSUnit(t, h, f, "Counter 6")

	resp := h.do(t, http.MethodPut,
		"/api/v1/devices/"+orphan+"?company_id="+f.companyID.String(), f.token,
		map[string]any{"terminal_label": "Legacy Till", "egs_unit_id": unitID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("amend: %s", readBody(t, resp))
	}
	if got, _ := decodeJSON(t, resp)["egs_unit_id"].(string); got != unitID {
		t.Errorf("the terminal was not bound: %q", got)
	}
}

// A till that has traded cannot be repointed. Its old invoices stay valid on
// the old unit; what must not happen is a chain whose readers cannot tell which
// terminal wrote which part of it.
func TestATerminalThatHasSoldCannotBeRepointed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	body := oneItemSale(f, uuid.New(), "1", "115.00", "115.00")
	sale := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, body)
	if sale.StatusCode != http.StatusCreated {
		t.Fatalf("sell something first: %s", readBody(t, sale))
	}

	owner := h.seedUserIn(t, f, "owner")
	other := h.do(t, http.MethodPost,
		"/api/v1/einvoicing/units?company_id="+f.companyID.String(), owner,
		map[string]any{
			"label": "Counter 7", "architecture": "smart_pos",
			"store_id": f.storeID.String(),
		})
	if other.StatusCode != http.StatusCreated {
		t.Fatalf("create a second unit: %s", readBody(t, other))
	}
	otherID, _ := decodeJSON(t, other)["id"].(string)

	resp := h.do(t, http.MethodPut,
		"/api/v1/devices/"+f.deviceID.String()+"?company_id="+f.companyID.String(),
		owner, map[string]any{"terminal_label": "Till 1", "egs_unit_id": otherID})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a terminal that has already issued invoices was repointed")
	}
}

// --- The CSID comes from the unit -------------------------------------------

// 0013 moved the CSID to the EGS unit and left the columns on `device` behind
// as deprecated. The terminal list read the deprecated ones, so it reported an
// empty CSID for every properly onboarded unit that was not a smart POS.
func TestTheTerminalListReadsTheCSIDFromItsEGSUnit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	unitID := smartPOSUnit(t, h, f, "Counter 8")
	deviceID := registerTillOn(t, h, f, "Till 8", unitID)

	// Written directly: no route sets a CSID, and none should.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE egs_unit
			SET csid_serial = 'UNIT-SERIAL-1', csid_status = 'live'
			WHERE id = $1::uuid`, unitID)
		return e
	}); err != nil {
		t.Fatalf("set a CSID on the unit: %v", err)
	}

	row := findTerminal(t, decodeJSON(t, h.do(t, http.MethodGet,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token, nil)), deviceID)

	if got, _ := row["csid_serial"].(string); got != "UNIT-SERIAL-1" {
		t.Errorf("csid_serial = %q; the list is still reading device.csid_serial", got)
	}
	if got, _ := row["csid_status"].(string); got != "live" {
		t.Errorf("csid_status = %q, want live", got)
	}
}

// registerTillOn registers a terminal against a named unit rather than the
// fixture's, and returns its id.
func registerTillOn(
	t *testing.T, h *harness, f *shopFixture, label, unitID string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"store_id": f.storeID.String(), "terminal_label": label,
			"egs_unit_id": unitID,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: %d %s", label, resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}
