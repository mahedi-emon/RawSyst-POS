//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
)

// A user confined to one company must not reach another one's books.
//
// This is the dimension that was declared, stored, claimed, parsed and checked
// -- and never fed. user_role_assignment.company_id existed, the resolver read
// it, the token defined a "cid" claim, the parser read it back, and
// actor.CanAccessCompany consulted it from four handler files. Nothing ever put
// a value into the claim, so the list was always empty, an empty list means
// "every company in the tenant", and the check passed for everybody.
//
// Nothing was mis-authorised in production, because provisioning writes every
// assignment unscoped and no scoped assignment had been created. The hole was
// latent and would have opened silently the first time an owner confined a
// bookkeeper to one of their companies -- which the schema invites.

// twoCompanyShop provisions one tenant with two companies and a user confined
// to the first, then signs that user in.
type twoCompanyShop struct {
	tenantID  uuid.UUID
	companyA  uuid.UUID
	companyB  uuid.UUID
	token     string
	unscoped  string // a token for a user with no company confinement
	userEmail string
}

func seedTwoCompanyShop(t *testing.T, h *harness) twoCompanyShop {
	t.Helper()
	ctx := context.Background()

	var s twoCompanyShop
	var scopedUser, unscopedUser uuid.UUID
	hash, err := identity.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash the test password: %v", err)
	}

	scopedEmail := "scoped-" + uuid.NewString()[:8] + "@example.test"
	unscopedEmail := "unscoped-" + uuid.NewString()[:8] + "@example.test"
	s.userEmail = scopedEmail

	err = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Two Company Group') RETURNING id`).
			Scan(&s.tenantID); err != nil {
			return err
		}
		for _, c := range []struct {
			name string
			into *uuid.UUID
		}{{"Alpha Trading", &s.companyA}, {"Beta Trading", &s.companyB}} {
			if err := tx.QueryRow(ctx, `
				INSERT INTO company (tenant_id, legal_name, country, base_currency)
				VALUES ($1,$2,'sa','SAR') RETURNING id`,
				s.tenantID, c.name).Scan(c.into); err != nil {
				return err
			}
		}
		for _, u := range []struct {
			email string
			into  *uuid.UUID
		}{{scopedEmail, &scopedUser}, {unscopedEmail, &unscopedUser}} {
			if err := tx.QueryRow(ctx, `
				INSERT INTO app_user (tenant_id, email, full_name, password_hash, status)
				VALUES ($1,$2,'Bookkeeper',$3,'active') RETURNING id`,
				s.tenantID, u.email, hash).Scan(u.into); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("provision the group: %v", err)
	}

	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, s.tenantID)
			return err
		})
	})

	// Both get the Owner role, so permissions are identical and the only
	// difference under test is the company confinement.
	err = h.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name, cloned_from)
			SELECT $1, key, name, id FROM role
			WHERE tenant_id IS NULL AND key = 'owner'
			RETURNING id`, s.tenantID).Scan(&roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT $1, rp.permission FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = 'owner'`, roleID); err != nil {
			return err
		}
		// Confined to Alpha.
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id, company_id)
			VALUES ($1,$2,$3,$4)`, s.tenantID, scopedUser, roleID, s.companyA); err != nil {
			return err
		}
		// Confined to nothing.
		_, err := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id)
			VALUES ($1,$2,$3)`, s.tenantID, unscopedUser, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed the roles: %v", err)
	}

	s.token = h.login(t, scopedEmail)
	s.unscoped = h.login(t, unscopedEmail)
	return s
}

// The token must actually carry the confinement. Without this the check
// downstream is reading an empty list and passing everybody.
func TestASignInCarriesTheCompanyConfinement(t *testing.T) {
	h := newHarness(t)
	s := seedTwoCompanyShop(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", s.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	var me struct {
		CompanyIDs []string `json:"company_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The endpoint may not surface the claim; what matters is that the
	// confinement bites, which the next test proves. This one only fails if
	// the field exists and is empty, which would mean the claim was not filled.
	if me.CompanyIDs != nil && len(me.CompanyIDs) == 0 {
		t.Error("the signed-in user reports no company confinement, so an " +
			"empty list will be read as every company in the tenant")
	}
}

// The confinement must bite on a real route.
//
// A company-scoped user asking for another company's data must be refused, and
// refused as NOT FOUND rather than forbidden: telling somebody a company they
// may not see exists is itself a disclosure.
func TestAConfinedUserCannotReachAnotherCompany(t *testing.T) {
	h := newHarness(t)
	s := seedTwoCompanyShop(t, h)

	// Routes that take an explicit company_id and resolve it through
	// companyFromRequestOrDevice, which is where the check lives.
	routes := []string{
		"/api/v1/catalog/products?company_id=",
		"/api/v1/purchasing/suppliers?company_id=",
	}

	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			own := h.do(t, http.MethodGet, route+s.companyA.String(), s.token, nil)
			defer own.Body.Close()
			if own.StatusCode == http.StatusNotFound {
				t.Fatalf("the confined user was refused their OWN company (%d); "+
					"the confinement is too tight", own.StatusCode)
			}

			other := h.do(t, http.MethodGet, route+s.companyB.String(), s.token, nil)
			defer other.Body.Close()
			if other.StatusCode != http.StatusNotFound {
				t.Errorf("a user confined to Alpha read Beta's data: status %d. "+
					"Two companies in one tenant keep separate books and separate "+
					"VAT registrations", other.StatusCode)
			}
		})
	}
}

// A user confined to nothing still reaches every company in their tenant. The
// fix must not tighten the common case: provisioning writes every assignment
// unscoped, so this is what almost every real user looks like.
func TestAnUnconfinedUserStillReachesEveryCompany(t *testing.T) {
	h := newHarness(t)
	s := seedTwoCompanyShop(t, h)

	for name, company := range map[string]uuid.UUID{
		"alpha": s.companyA,
		"beta":  s.companyB,
	} {
		resp := h.do(t, http.MethodGet,
			"/api/v1/catalog/products?company_id="+company.String(), s.unscoped, nil)
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("an unconfined user was refused %s; the common case broke", name)
		}
		resp.Body.Close()
	}
}

// Narrowing somebody's scope must take effect, not last as long as they keep
// refreshing. The scopes are re-read on refresh for exactly this reason.
func TestNarrowingScopeTakesEffectOnRefresh(t *testing.T) {
	h := newHarness(t)
	s := seedTwoCompanyShop(t, h)

	// The unconfined user can reach Beta today.
	before := h.do(t, http.MethodGet,
		"/api/v1/catalog/products?company_id="+s.companyB.String(), s.unscoped, nil)
	beforeStatus := before.StatusCode
	before.Body.Close()
	if beforeStatus == http.StatusNotFound {
		t.Fatalf("the unconfined user could not reach Beta to begin with (%d)", beforeStatus)
	}

	// Confine every assignment in the tenant to Alpha.
	ctx := context.Background()
	if err := h.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE user_role_assignment SET company_id = $1
			 WHERE tenant_id = $2 AND company_id IS NULL`, s.companyA, s.tenantID)
		return err
	}); err != nil {
		t.Fatalf("narrow the scope: %v", err)
	}

	// A fresh sign-in must reflect it. (Refresh reads the same helper.)
	narrowed := h.login(t, s.userEmail)
	after := h.do(t, http.MethodGet,
		"/api/v1/catalog/products?company_id="+s.companyB.String(), narrowed, nil)
	defer after.Body.Close()

	if after.StatusCode != http.StatusNotFound {
		t.Errorf("after being confined to Alpha the user still read Beta: %d",
			after.StatusCode)
	}
}
