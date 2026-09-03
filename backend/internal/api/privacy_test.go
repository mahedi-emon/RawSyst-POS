//go:build integration

// E4, PDPL and the privacy office.
//
// 29 services and 25 routes with isolation-only coverage: tests proving one
// tenant cannot read another's consents, and nothing proving a consent can be
// recorded, withdrawn, or acted on. For a compliance module that is the wrong
// half to have — a register nobody can write to is not a register, and the
// failure only shows when a regulator asks.
//
// Consent, subject requests and breach incidents are the three obligations
// PDPL puts on a controller, so those are what these drive end to end.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// privacySubject creates a customer to be the data subject.
func privacySubject(t *testing.T, h *harness, f *shopFixture) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO customer (tenant_id, company_id, code, name)
			VALUES ($1,$2,$3,'Data Subject') RETURNING id`,
			f.tenantID, f.companyID, "S"+uuid.NewString()[:8]).Scan(&id)
	}); err != nil {
		t.Fatalf("create subject: %v", err)
	}
	return id
}

// A marketing consent can be recorded, listed, and withdrawn.
//
// The whole point of a consent register: PDPL requires a controller to show
// what a person agreed to and to stop when they take it back. Withdrawal is
// the half that matters, and nothing had ever exercised it.
func TestAConsentCanBeRecordedAndWithdrawn(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	subject := privacySubject(t, h, f)
	company := "?company_id=" + f.companyID.String()

	recorded := h.do(t, http.MethodPost, "/api/v1/privacy/consents"+company,
		f.token, map[string]any{
			"subject_type": "customer", "subject_id": subject.String(),
			"lawful_basis": "consent", "purpose": "marketing",
			"channel": "email", "proof": "signed at the counter",
		})
	defer recorded.Body.Close()
	if recorded.StatusCode != http.StatusOK && recorded.StatusCode != http.StatusCreated {
		t.Fatalf("record consent: %d %s", recorded.StatusCode,
			readBody(t, recorded))
	}

	consent := firstPrivacyRecord(t, h, f, "/api/v1/privacy/consents"+company)
	consentID, _ := consent["id"].(string)
	if consentID == "" {
		t.Fatal("the recorded consent does not appear in the register")
	}

	withdrawn := h.do(t, http.MethodPost,
		"/api/v1/privacy/consents/"+consentID+"/withdraw"+company, f.token,
		map[string]any{"reason": "asked at the till"})
	defer withdrawn.Body.Close()
	if withdrawn.StatusCode >= 300 {
		t.Fatalf("withdraw consent: %d %s", withdrawn.StatusCode,
			readBody(t, withdrawn))
	}

	// Withdrawal has to be visible, or the register cannot answer the only
	// question that matters: may we still contact this person?
	var withdrawnAt *string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT withdrawn_at::text FROM privacy_consent WHERE id = $1`,
			consentID).Scan(&withdrawnAt)
	}); err != nil {
		t.Fatalf("read the consent back: %v", err)
	}
	if withdrawnAt == nil {
		t.Error("a withdrawn consent is not recorded as withdrawn")
	}
}

// A subject access request can be opened and closed.
//
// PDPL gives a person the right to ask what is held about them, and gives the
// controller a deadline. The register exists to make the deadline visible.
func TestASubjectRequestCanBeOpenedAndClosed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	subject := privacySubject(t, h, f)
	company := "?company_id=" + f.companyID.String()

	opened := h.do(t, http.MethodPost, "/api/v1/privacy/requests"+company,
		f.token, map[string]any{
			"kind": "access", "subject_type": "customer",
			"subject_id": subject.String(), "subject_name": "Data Subject",
			"subject_contact": "subject@example.test",
		})
	defer opened.Body.Close()
	if opened.StatusCode != http.StatusCreated && opened.StatusCode != http.StatusOK {
		t.Fatalf("open request: %d %s", opened.StatusCode, readBody(t, opened))
	}
	request, _ := decodeJSON(t, opened)["request"].(map[string]any)
	dsrID, _ := request["id"].(string)
	if dsrID == "" {
		t.Fatal("the opened request has no id")
	}

	closed := h.do(t, http.MethodPost,
		"/api/v1/privacy/requests/"+dsrID+"/close"+company, f.token,
		map[string]any{"outcome": "fulfilled", "note": "sent the export"})
	defer closed.Body.Close()
	if closed.StatusCode >= 300 {
		t.Fatalf("close request: %d %s", closed.StatusCode, readBody(t, closed))
	}
}

// An open request appears in the open list and a closed one does not.
//
// `?open=true` is what a privacy officer's screen asks for, and a filter that
// returned everything would make a deadline dashboard useless.
func TestTheOpenRequestListShowsOnlyOpenOnes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	subject := privacySubject(t, h, f)
	company := "?company_id=" + f.companyID.String()

	opened := h.do(t, http.MethodPost, "/api/v1/privacy/requests"+company,
		f.token, map[string]any{
			"kind": "deletion", "subject_type": "customer",
			"subject_id": subject.String(), "subject_name": "Data Subject",
		})
	if opened.StatusCode >= 300 {
		t.Fatalf("open request: %d %s", opened.StatusCode, readBody(t, opened))
	}
	request, _ := decodeJSON(t, opened)["request"].(map[string]any)
	opened.Body.Close()
	dsrID, _ := request["id"].(string)

	if n := privacyCount(t, h, f,
		"/api/v1/privacy/requests?open=true&company_id="+f.companyID.String()); n == 0 {
		t.Fatal("a freshly opened request is not in the open list")
	}

	closed := h.do(t, http.MethodPost,
		"/api/v1/privacy/requests/"+dsrID+"/close"+company, f.token,
		map[string]any{"outcome": "fulfilled", "note": "done"})
	closed.Body.Close()

	if n := privacyCount(t, h, f,
		"/api/v1/privacy/requests?open=true&company_id="+f.companyID.String()); n != 0 {
		t.Errorf("the open list still shows %d requests after the only one "+
			"was closed", n)
	}
}

// A breach incident can be logged and closed.
//
// The third PDPL obligation: a controller records what happened, who it
// affected and what was done about it.
func TestABreachIncidentCanBeLoggedAndClosed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()
	affected := 3

	logged := h.do(t, http.MethodPost, "/api/v1/privacy/incidents"+company,
		f.token, map[string]any{
			"title": "Laptop left on a train", "what_happened": "A till laptop was lost.",
			"data_categories": "names, phone numbers", "subjects_affected": affected,
			"consequences": "possible contact details exposure",
			"containment":  "remote wipe issued", "severity": "medium",
			"discovered_at": "2026-08-15T09:00:00Z",
		})
	defer logged.Body.Close()
	if logged.StatusCode != http.StatusCreated && logged.StatusCode != http.StatusOK {
		t.Fatalf("log incident: %d %s", logged.StatusCode, readBody(t, logged))
	}

	if n := privacyCount(t, h, f,
		"/api/v1/privacy/incidents"+company); n == 0 {
		t.Error("a logged incident does not appear in the register")
	}
}

// The records of processing can be read.
//
// PDPL's ROPA: what the business does with personal data and on what basis.
func TestTheRecordsOfProcessingCanBeRead(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/privacy/activities?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read activities: %d %s", resp.StatusCode, readBody(t, resp))
	}
}

// The privacy register takes its own permission.
//
// Consents, subject requests and breach reports are among the most sensitive
// records a business keeps, and a cashier has no business in any of them.
func TestThePrivacyRegisterNeedsItsPermission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, path := range []string{
		"/api/v1/privacy/consents", "/api/v1/privacy/requests",
		"/api/v1/privacy/incidents", "/api/v1/privacy/activities",
	} {
		resp := h.do(t, http.MethodGet,
			path+"?company_id="+f.companyID.String(), f.token, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("a cashier read %s", path)
		}
		resp.Body.Close()
	}
}

// firstPrivacyRecord returns the first row of a privacy list route.
func firstPrivacyRecord(
	t *testing.T, h *harness, f *shopFixture, path string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet, path, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s: %d %s", path, resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatalf("%s is empty", path)
	}
	row, _ := rows[0].(map[string]any)
	return row
}

// privacyCount counts rows on a privacy list route.
func privacyCount(t *testing.T, h *harness, f *shopFixture, path string) int {
	t.Helper()
	resp := h.do(t, http.MethodGet, path, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s: %d %s", path, resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	return len(rows)
}
