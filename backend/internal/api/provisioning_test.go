//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
)

// uniqueVATNumber returns a 15-digit value in the Saudi shape (3…3), distinct
// per call so repeated runs do not collide on the global uniqueness constraint.
func uniqueVATNumber() string {
	digits := make([]byte, 0, 15)
	digits = append(digits, '3')
	for _, c := range strings.ReplaceAll(uuid.NewString(), "-", "") {
		if c >= '0' && c <= '9' {
			digits = append(digits, byte(c))
		}
		if len(digits) == 14 {
			break
		}
	}
	for len(digits) < 14 {
		digits = append(digits, '0')
	}
	return string(append(digits, '3'))
}

// seedSuperAdmin creates a platform administrator and returns their email.
func (h *harness) seedSuperAdmin(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	email := "admin" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"

	hash, err := identity.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var id uuid.UUID
	err = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO app_user (tenant_id, email, full_name, password_hash, status)
			VALUES (NULL,$1,'Platform Admin',$2,'active') RETURNING id`,
			email, hash).Scan(&id)
	})
	if err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM app_user WHERE id = $1`, id)
			return err
		})
	})
	return email
}

// The whole provisioning story, end to end and over real HTTP: the platform
// operator creates a tenant with its Owner, the Owner signs in with the
// temporary password, is forced to change it, and completes setup.
//
// This is the flow the product is sold on, so it is tested as one narrative
// rather than as isolated units that each pass while the join between them
// fails.
func TestProvisionTenantThenOwnerCompletesOnboarding(t *testing.T) {
	h := newHarness(t)

	adminEmail := h.seedSuperAdmin(t)
	adminToken := h.login(t, adminEmail)

	ownerEmail := "owner" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"

	// --- 1. the platform operator creates the tenant ---
	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", adminToken,
		map[string]string{
			"name":        "Al Nakheel Fashion",
			"data_region": "sa",
			"plan_tier":   "professional",
			"owner_email": ownerEmail,
			"owner_name":  "Mahedi Hasan",
		})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := json.Marshal(readBody(t, resp))
		t.Fatalf("create tenant status = %d, want 201: %s", resp.StatusCode, body)
	}

	var created struct {
		TenantID          string `json:"tenant_id"`
		OwnerUserID       string `json:"owner_user_id"`
		TemporaryPassword string `json:"temporary_password"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.TemporaryPassword == "" {
		t.Fatal("no temporary password was issued for the new Owner")
	}

	tenantID, err := uuid.Parse(created.TenantID)
	if err != nil {
		t.Fatalf("bad tenant id: %v", err)
	}
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})

	// --- 2. the Owner signs in and is told to change the password ---
	loginResp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": ownerEmail, "password": created.TemporaryPassword})
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("owner login status = %d, want 200", loginResp.StatusCode)
	}
	var session struct {
		AccessToken        string `json:"access_token"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&session); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if !session.MustChangePassword {
		t.Fatal("the Owner was not required to change their temporary password")
	}

	// --- 3. the Owner holds the full Owner permission set ---
	meResp := h.do(t, http.MethodGet, "/api/v1/auth/me", session.AccessToken, nil)
	defer meResp.Body.Close()

	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	held := make(map[string]bool, len(me.Permissions))
	for _, p := range me.Permissions {
		held[p] = true
	}
	for _, required := range []string{
		"identity.manage_roles", "accounting.close_period",
		"catalog.view_cost_price", "compliance.view",
	} {
		if !held[required] {
			t.Fatalf("the provisioned Owner is missing %q; provisioning did not "+
				"attach the Owner role correctly", required)
		}
	}

	// --- 4. setup starts at the first step ---
	progResp := h.do(t, http.MethodGet, "/api/v1/onboarding", session.AccessToken, nil)
	defer progResp.Body.Close()

	var prog struct {
		CurrentStep string `json:"current_step"`
		NextStep    string `json:"next_step"`
		Finished    bool   `json:"finished"`
	}
	if err := json.NewDecoder(progResp.Body).Decode(&prog); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if prog.CurrentStep != "business_info" {
		t.Fatalf("setup starts at %q, want business_info", prog.CurrentStep)
	}
	if prog.Finished {
		t.Fatal("a brand new tenant is already marked as finished setup")
	}

	// --- 5. a step cannot be completed before it is filled in ---
	badComplete := h.do(t, http.MethodPost,
		"/api/v1/onboarding/steps/business_info/complete", session.AccessToken, nil)
	badComplete.Body.Close()
	if badComplete.StatusCode == http.StatusOK {
		t.Fatal("an empty business-information step was accepted")
	}

	// --- 6. VAT registered without a VAT number is refused, with a reason ---
	saveBad := h.doRaw(t, http.MethodPut,
		"/api/v1/onboarding/steps/business_info", session.AccessToken,
		`{"legal_name":"Al Nakheel Fashion LLC","country":"sa","base_currency":"SAR",
		  "vat_registered":true}`)
	saveBad.Body.Close()

	completeBad := h.do(t, http.MethodPost,
		"/api/v1/onboarding/steps/business_info/complete", session.AccessToken, nil)
	defer completeBad.Body.Close()
	if completeBad.StatusCode == http.StatusOK {
		t.Fatal("a VAT-registered business with no VAT number was accepted")
	}
	envelope := readBody(t, completeBad)
	if !strings.Contains(strings.ToLower(envelope), "vat") {
		t.Fatalf("the refusal does not mention the VAT number: %s", envelope)
	}

	// --- 7. valid answers, then complete ---
	//
	// The VAT number is unique per run. A VAT registration identifies one
	// business within a country, so the schema enforces global uniqueness on
	// (country, vat_number) — a fixed value here would collide with the
	// previous run rather than testing anything.
	vatNo := uniqueVATNumber()
	saveOK := h.doRaw(t, http.MethodPut,
		"/api/v1/onboarding/steps/business_info", session.AccessToken,
		`{"legal_name":"Al Nakheel Fashion LLC","legal_name_ar":"شركة النخيل للأزياء",
		  "country":"sa","base_currency":"SAR","cr_number":"1010101010",
		  "vat_registered":true,"vat_number":"`+vatNo+`"}`)
	saveOK.Body.Close()
	if saveOK.StatusCode != http.StatusNoContent {
		t.Fatalf("saving the step returned %d, want 204", saveOK.StatusCode)
	}

	completeOK := h.do(t, http.MethodPost,
		"/api/v1/onboarding/steps/business_info/complete", session.AccessToken, nil)
	defer completeOK.Body.Close()
	if completeOK.StatusCode != http.StatusOK {
		t.Fatalf("completing a valid step returned %d: %s",
			completeOK.StatusCode, readBody(t, completeOK))
	}

	var afterStep struct {
		CurrentStep    string   `json:"current_step"`
		CompletedSteps []string `json:"completed_steps"`
	}
	if err := json.NewDecoder(completeOK.Body).Decode(&afterStep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if afterStep.CurrentStep != "stores" {
		t.Fatalf("after business info the wizard is at %q, want stores", afterStep.CurrentStep)
	}
	if len(afterStep.CompletedSteps) != 1 || afterStep.CompletedSteps[0] != "business_info" {
		t.Fatalf("completed steps = %v, want [business_info]", afterStep.CompletedSteps)
	}

	// --- 8. steps cannot be skipped ---
	skip := h.do(t, http.MethodPost,
		"/api/v1/onboarding/steps/opening_balances/complete", session.AccessToken, nil)
	skip.Body.Close()
	if skip.StatusCode == http.StatusOK {
		t.Fatal("the wizard allowed a step to be skipped; opening balances would " +
			"be entered before the chart of accounts exists")
	}

	// --- 9. the company record is created from the wizard's answers ---
	commit := h.do(t, http.MethodPost, "/api/v1/onboarding/company",
		session.AccessToken, nil)
	defer commit.Body.Close()
	if commit.StatusCode != http.StatusCreated {
		t.Fatalf("committing the company returned %d: %s",
			commit.StatusCode, readBody(t, commit))
	}

	var company struct {
		CompanyID string `json:"company_id"`
	}
	if err := json.NewDecoder(commit.Body).Decode(&company); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var legalName, vatNumber string
	var vatRegistered bool
	err = h.pool.TxAsTenant(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT legal_name, vat_number, vat_registered FROM company WHERE id = $1`,
			company.CompanyID).Scan(&legalName, &vatNumber, &vatRegistered)
	})
	if err != nil {
		t.Fatalf("read company: %v", err)
	}
	if legalName != "Al Nakheel Fashion LLC" || !vatRegistered || vatNumber == "" {
		t.Fatalf("company did not persist the wizard's answers: %q / %v / %q",
			legalName, vatRegistered, vatNumber)
	}
}

// Plan ceilings must come from the tier defaults, so raising a tier is one
// central change rather than a migration touching every tenant.
func TestProvisioningAppliesPlanTierLimits(t *testing.T) {
	h := newHarness(t)
	adminToken := h.login(t, h.seedSuperAdmin(t))

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", adminToken,
		map[string]string{
			"name":        "Starter Shop",
			"plan_tier":   "starter",
			"owner_email": "s" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test",
			"owner_name":  "Starter Owner",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, readBody(t, resp))
	}

	var created struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tenantID := uuid.MustParse(created.TenantID)
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})

	var stores, terminals, defStores, defTerminals int
	err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(),
			`SELECT max_stores, max_terminals FROM tenant_limit WHERE tenant_id = $1`,
			tenantID).Scan(&stores, &terminals); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(),
			`SELECT max_stores, max_terminals FROM plan_tier_default WHERE tier = 'starter'`).
			Scan(&defStores, &defTerminals)
	})
	if err != nil {
		t.Fatalf("read limits: %v", err)
	}
	if stores != defStores || terminals != defTerminals {
		t.Fatalf("tenant limits (%d stores, %d terminals) do not match the starter "+
			"defaults (%d, %d)", stores, terminals, defStores, defTerminals)
	}
	// Blueprint A3: no ceiling may be undefined.
	if stores <= 0 || terminals <= 0 {
		t.Fatal("a plan ceiling is zero or negative; every limit needs a concrete number")
	}
}

// Only the platform operator may create a tenant. An Owner attempting it must
// be refused without learning that the endpoint exists.
func TestOwnerCannotCreateATenant(t *testing.T) {
	h := newHarness(t)
	ownerEmail := h.seedUserWithRole(t, "owner")
	token := h.login(t, ownerEmail)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", token,
		map[string]string{
			"name": "Rogue Tenant", "owner_email": "x@example.test", "owner_name": "X",
		})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — an Owner must not be able to provision "+
			"tenants, nor learn that the endpoint exists", resp.StatusCode)
	}
}

// A provisioning request with bad details must name each field, so the operator
// can fix them in one pass rather than one at a time.
func TestProvisioningReportsEveryInvalidField(t *testing.T) {
	h := newHarness(t)
	adminToken := h.login(t, h.seedSuperAdmin(t))

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", adminToken,
		map[string]string{
			"name": "", "owner_email": "not-an-email", "owner_name": "",
		})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var env struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"name", "owner_email", "owner_name"} {
		if env.Error.Fields[field] == "" {
			t.Errorf("field %q was invalid but no message was returned for it", field)
		}
	}
}
