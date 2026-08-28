//go:build integration

// Company confinement, walked over the whole route table.
//
// Two companies in one tenant keep separate books, separate VAT registrations
// and separate legal identities, and `user_role_assignment.company_id` is how
// an Owner confines a bookkeeper to one of them. P65 found that the dimension
// was declared, stored, claimed, parsed and checked — and never fed, so every
// check passed for everybody. That was fixed, and the test that proved it named
// TWO routes.
//
// Row-level security does not help here. Its predicate is the TENANT, and both
// companies are inside it, so a confined user reaching a sister company is not
// something the database can see as wrong. Confinement is enforced entirely in
// the handlers, which means it holds exactly where somebody remembered to put
// `CanAccessCompany` — and nowhere else.
//
// So this walks the table. Every route that names a company, and every route
// that names a RECORD belonging to one, is called by a user confined to Alpha
// and pointed at Beta.
//
// # The record-naming routes are the harder half
//
// A route taking `?company_id=` is at least obviously about a company. A route
// taking `{invoiceID}` or `{poID}` looks like it is about a document, and the
// document silently carries a company with it. Those are the ones that get
// written without the check, because the check is not visibly missing.
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// tradedCompany is a company inside an existing tenant that has done enough
// business to have a record of each kind.
type tradedCompany struct {
	companyID uuid.UUID
	ids       map[string]string
}

// A confined user, a company they may not reach, and records inside it.
//
// Built on seedTwoCompanyShop, which is the fixture the confinement rules were
// written against; this adds the trading, because a route pointed at a company
// with nothing in it cannot tell a refusal from an empty answer.
func seedConfinedGroup(t *testing.T, h *harness) (twoCompanyShop, tradedCompany) {
	t.Helper()
	ctx := context.Background()
	s := seedTwoCompanyShop(t, h)

	beta := tradedCompany{companyID: s.companyB, ids: map[string]string{
		"companyID": s.companyB.String(),
	}}

	// Beta needs the furniture a company needs before it can trade at all.
	var storeID, warehouseID, supplierID, customerID, productID, variantID uuid.UUID
	err := h.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := provisioning.SeedChartOfAccounts(ctx, tx, s.tenantID, s.companyB); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name, street,
			                   building_number, district, city, postal_code, country_code)
			VALUES ($1,$2,'BETA','Beta Main','King Fahd Road','1188','Olaya',
			        'Riyadh','12333','SA') RETURNING id`,
			s.tenantID, s.companyB).Scan(&storeID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,'BWH','Beta Stock') RETURNING id`,
			s.tenantID, s.companyB, storeID).Scan(&warehouseID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO supplier (tenant_id, company_id, code, legal_name,
			                      payment_terms_days)
			VALUES ($1,$2,'BSUP','Beta Supplier',30) RETURNING id`,
			s.tenantID, s.companyB).Scan(&supplierID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO customer (tenant_id, company_id, code, name, credit_limit)
			VALUES ($1,$2,'BCUST','Beta Customer',5000) RETURNING id`,
			s.tenantID, s.companyB).Scan(&customerID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'BETA-SKU','Beta Abaya','standard') RETURNING id`,
			s.tenantID, s.companyB).Scan(&productID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO variant (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,'BETA-SKU-1',100) RETURNING id`,
			s.tenantID, s.companyB, productID).Scan(&variantID)
	})
	if err != nil {
		t.Fatalf("give Beta something to trade with: %v", err)
	}

	beta.ids["storeID"] = storeID.String()
	beta.ids["supplierID"] = supplierID.String()
	beta.ids["customerID"] = customerID.String()
	beta.ids["productID"] = productID.String()
	beta.ids["variantID"] = variantID.String()

	// A purchase order, through the real routes, as the UNCONFINED user — who
	// is entitled to act for Beta. Doing it by INSERT would leave records no
	// handler would have produced.
	order := h.do(t, http.MethodPost,
		"/api/v1/purchasing/orders?company_id="+s.companyB.String(), s.unscoped,
		map[string]any{
			"supplier_id": supplierID.String(), "warehouse_id": warehouseID.String(),
			"lines": []map[string]any{{
				"variant_id": variantID.String(), "description": "Beta Abaya",
				"qty": "10", "unit_cost": "50.00", "tax_rate": "0.15",
			}},
		})
	if order.StatusCode != http.StatusCreated {
		t.Fatalf("Beta could not raise an order: %s", readBody(t, order))
	}
	body := decodeJSON(t, order)
	beta.ids["poID"], _ = body["id"].(string)

	return s, beta
}

// companyConfinementRoutes is every route this walk knows how to aim at Beta.
//
// Written out rather than derived, and each entry says which of Beta's records
// it names, because the point is to be able to see at a glance what is covered
// and what is not. A route missing from this list is a route nobody has checked.
func companyConfinementRoutes(beta tradedCompany) []struct {
	method, path, names string
} {
	c := beta.companyID.String()
	out := []struct{ method, path, names string }{
		// Named-company routes: the obvious half.
		{http.MethodGet, "/api/v1/catalog/products?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/purchasing/suppliers?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/purchasing/orders?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/purchasing/bills?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/purchasing/ageing?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/customers?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/receivables/ageing?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/settlement/pending?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/devices?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/einvoicing/units?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/dashboard/overview?company_id=" + c, "companyID"},
		{http.MethodGet, "/api/v1/reports/trial-balance?company_id=" + c +
			"&as_of=2026-08-31", "companyID"},
		{http.MethodGet, "/api/v1/reports/vat-return?company_id=" + c +
			"&from=2026-08-01&to=2026-08-31", "companyID"},

		// Branding and templates take the company in the PATH. These are the
		// ones the two-route test never reached, and two of them are writes:
		// a confined user replacing another company's logo, or rewriting the
		// template that prints on its legal invoices.
		{http.MethodGet, "/api/v1/companies/" + c + "/logo", "companyID"},
		{http.MethodGet, "/api/v1/companies/" + c + "/templates", "companyID"},

		// Record-naming routes: the half that does not look like it is about a
		// company at all.
		{http.MethodGet, "/api/v1/catalog/products/" + beta.ids["productID"] +
			"/matrix", "productID"},
		{http.MethodGet, "/api/v1/purchasing/orders/" + beta.ids["poID"], "poID"},
		{http.MethodGet, "/api/v1/purchasing/orders/" + beta.ids["poID"] +
			"/receipts", "poID"},
		{http.MethodGet, "/api/v1/customers/" + beta.ids["customerID"], "customerID"},
		{http.MethodGet, "/api/v1/customers/" + beta.ids["customerID"] +
			"/ledger", "customerID"},
		{http.MethodGet, "/api/v1/customers/" + beta.ids["customerID"] +
			"/open-invoices", "customerID"},
	}
	return out
}

// THE WALK, refusing side.
func TestAConfinedUserIsRefusedEveryRouteIntoAnotherCompany(t *testing.T) {
	h := newHarness(t)
	s, beta := seedConfinedGroup(t, h)

	for _, rt := range companyConfinementRoutes(beta) {
		t.Run(rt.method+" "+strings.SplitN(rt.path, "?", 2)[0], func(t *testing.T) {
			resp := h.do(t, rt.method, rt.path, s.token, nil)
			defer resp.Body.Close()

			if resp.StatusCode < 300 {
				t.Errorf("a user confined to Alpha reached Beta through %s and "+
					"got %d. Two companies in one tenant keep separate books and "+
					"separate VAT registrations, and row-level security cannot "+
					"tell them apart — its predicate is the tenant. Body: %s",
					rt.path, resp.StatusCode, truncate(readBody(t, resp), 240))
			}
		})
	}
}

// Writing into another company is worse than reading it, and gets its own test
// so the failure names the act rather than a status code.
func TestAConfinedUserCannotRewriteAnotherCompanysInvoiceTemplate(t *testing.T) {
	h := newHarness(t)
	s, beta := seedConfinedGroup(t, h)
	c := beta.companyID.String()

	logo := h.do(t, http.MethodPut, "/api/v1/companies/"+c+"/logo", s.token,
		map[string]any{
			// A one-pixel PNG. Small enough to be obviously a test, real enough
			// to pass the content sniff on the way in.
			"data": onePixelPNG,
		})
	defer logo.Body.Close()
	if logo.StatusCode < 300 {
		t.Errorf("a user confined to Alpha replaced BETA's logo (status %d). It "+
			"prints on every invoice and receipt Beta issues.", logo.StatusCode)
	}

	tmpl := h.do(t, http.MethodPut, "/api/v1/companies/"+c+"/templates/standard",
		s.token, map[string]any{
			"footer_text":   "Pay to a different bank account.",
			"return_policy": "",
		})
	defer tmpl.Body.Close()
	if tmpl.StatusCode < 300 {
		t.Errorf("a user confined to Alpha rewrote the footer of BETA's standard "+
			"tax invoice (status %d). It is a legal document and the text is on "+
			"the face of it.", tmpl.StatusCode)
	}
}

// The confinement must not become a wall around the common case.
//
// Provisioning writes every assignment unscoped, so almost every real user has
// no confinement at all. If tightening these routes refused THEM, the product
// would be broken for everybody to protect a case that is rare.
func TestAnUnconfinedUserStillReachesEveryRouteIntoEitherCompany(t *testing.T) {
	h := newHarness(t)
	s, beta := seedConfinedGroup(t, h)

	for _, rt := range companyConfinementRoutes(beta) {
		t.Run(rt.method+" "+strings.SplitN(rt.path, "?", 2)[0], func(t *testing.T) {
			resp := h.do(t, rt.method, rt.path, s.unscoped, nil)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound ||
				resp.StatusCode == http.StatusForbidden {
				t.Errorf("an UNCONFINED user was refused %s with %d; the "+
					"confinement was tightened into the common case. Body: %s",
					rt.path, resp.StatusCode, truncate(readBody(t, resp), 240))
			}
		})
	}

	// And the confined user still reaches their OWN company, or the fix is a
	// wall rather than a boundary.
	own := fmt.Sprintf("/api/v1/catalog/products?company_id=%s", s.companyA)
	resp := h.do(t, http.MethodGet, own, s.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the confined user was refused their OWN company: %d", resp.StatusCode)
	}
}

// A 1x1 transparent PNG, base64. Written out rather than generated so the test
// does not depend on an image encoder agreeing with the sniffer.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk" +
	"YPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
