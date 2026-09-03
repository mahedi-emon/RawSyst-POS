//go:build integration

// E7's compliance dashboard.
//
// One route over eight read-only aggregations — e-invoicing, VAT, privacy,
// storefront, payroll, people, records and the regulatory registry's own
// health. It had no tests at all, which for a read-only reporting surface means
// nobody had established that its SQL runs: a query naming a column that does
// not exist fails identically to one that does, right up until somebody opens
// the screen. That is exactly how the batches route shipped 500ing on
// `supplier.name`.
//
// These tests therefore start by simply reaching the report, for a Saudi shop
// and for one in a market whose obligations this product does not carry.
package api

import (
	"net/http"
	"testing"
)

// A Saudi shop can read its compliance report.
//
// The smoke test the module never had. Every part has to run: `Read` walks all
// eight in one transaction and returns the first error, so a single broken
// query takes the whole dashboard down.
func TestTheComplianceReportAssemblesForASaudiShop(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compliance report: %d %s", resp.StatusCode, readBody(t, resp))
	}

	body, _ := decodeJSON(t, resp)["report"].(map[string]any)
	if body == nil {
		t.Fatal("the response carries no report")
	}
	for _, part := range []string{
		"invoicing", "vat", "privacy", "storefront", "payroll", "people",
		"records",
	} {
		if _, ok := body[part]; !ok {
			t.Errorf("the report has no %q section", part)
		}
	}
	// E8.4: an unverified rule the product depends on is itself an exposure,
	// and the dashboard is where it surfaces.
	if _, ok := body["unverified_rules"]; !ok {
		t.Error("the report does not carry the registry's own health")
	}
}

// A shop that has not started e-invoicing is reported as not started.
//
// Distinguished on purpose from "onboarded with nothing pending": both show
// zero pending submissions, and only one of them is fine.
func TestAShopThatHasNotOnboardedIsReportedAsNotStarted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compliance report: %d %s", resp.StatusCode, readBody(t, resp))
	}

	report, _ := decodeJSON(t, resp)["report"].(map[string]any)
	invoicing, _ := report["invoicing"].(map[string]any)
	if invoicing == nil {
		t.Fatal("no invoicing section")
	}
	if started, _ := invoicing["started"].(bool); started {
		t.Error("a shop with no onboarding is reported as started")
	}
	if pending, _ := invoicing["pending"].(float64); pending != 0 {
		t.Errorf("pending = %v, want 0", pending)
	}
}

// A shop in a market this product carries no obligations for still gets a
// report.
//
// The market gate that broke payroll for Bangladesh is the same shape of bug
// waiting here: a dashboard that asked Saudi questions of a Bangladeshi company
// and failed would leave that tenant with no compliance view at all.
func TestTheComplianceReportAssemblesOutsideSaudiArabia(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")

	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a Bangladeshi shop could not read its compliance report: "+
			"%d %s", resp.StatusCode, readBody(t, resp))
	}
}

// One company's compliance posture is not another's.
func TestTheComplianceReportIsScopedToItsCompany(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+theirs.companyID.String(),
		mine.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a shop read another tenant's compliance report")
	}
}

// Reading the compliance posture takes a permission.
func TestTheComplianceReportNeedsItsPermission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a cashier read the compliance dashboard")
	}
}
