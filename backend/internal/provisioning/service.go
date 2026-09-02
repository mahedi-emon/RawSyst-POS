// Package provisioning creates tenants and their first Owner account.
//
// Blueprint A5: Super Admin creates the tenant and generates the Owner's first
// login, which arrives as a temporary password that must be changed on first
// use. Everything after that happens inside the tenant, by the Owner.
//
// The boundary matters. Provisioning is the only moment the platform operator
// touches a tenant's records, and even here it creates the account rather than
// configuring the business — the seven-step wizard that follows runs entirely
// as the Owner.
package provisioning

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service provisions tenants.
type Service struct {
	pool *db.Pool

	// rules is the regulatory registry, consulted before a tenant is created in
	// a market. Optional; see WithRules.
	rules *registry.Service
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// WithRules gives provisioning the regulatory registry, so creating a tenant can
// refuse a market whose legal values are still placeholders.
//
// Optional rather than a constructor argument, in the shape identity.WithCipher
// already uses here: a caller that has not wired it keeps working, and the
// check below states plainly that it was not asked rather than silently
// passing. The API server wires it.
func (s *Service) WithRules(rules *registry.Service) *Service {
	s.rules = rules
	return s
}

// requireMarketIsUsable refuses to create a tenant in a market whose
// release-blocking legal values have never been verified.
//
// # Why provisioning and not only boot
//
// The boot gate asks "may this process start given the tenants it has". That
// answer changes the moment a tenant is created in a new market, and the
// process does not re-run it — so a Bangladesh-only deployment could be given a
// Saudi client at 10:00 and keep serving it on placeholder GOSI, EOSB and WPS
// values until somebody happened to restart. This closes that window at the
// only point where the market is chosen.
//
// # It refuses only where the deployment refuses unverified values anyway
//
// A development machine creates tenants in any market, because that is what
// development is for and the per-use gate is off there too. Where
// requireVerified is set, the refusal here is the same judgement the boot gate
// and gate() already make, applied earlier.
//
// # It does not mark anything verified
//
// The remedy is to verify the rule against its official source and record the
// evidence. There is deliberately no override flag: one would be used.
func (s *Service) requireMarketIsUsable(ctx context.Context, market string) error {
	if s.rules == nil || !s.rules.RequiresVerification() {
		return nil
	}

	blockers, err := s.rules.UnverifiedBlockersFor(ctx, market)
	if err != nil {
		return err
	}
	if len(blockers) == 0 {
		return nil
	}

	return errs.Newf(errs.CodeUnverifiedRule,
		"This deployment cannot take on a business in %s yet: %s "+
			"%s never been verified against their official source. "+
			"Verify them in Super Admin > Regulatory Registry first — a "+
			"business created now would compute legal figures from placeholders.",
		marketName(market), strings.Join(blockers, ", "),
		plural(len(blockers), "has", "have"))
}

// marketName is the reader's word for a market code, falling back to the code.
func marketName(market string) string {
	if name, ok := supportedCountries[strings.ToLower(strings.TrimSpace(market))]; ok {
		return name
	}
	return strings.ToUpper(market)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// NewTenant is a provisioning request.
type NewTenant struct {
	// Name is the trading name shown to the Owner. The legal entity is captured
	// during onboarding, because the Owner knows it and the platform operator
	// often does not.
	Name string

	// DataRegion pins where this tenant's data lives. Blueprint E4.2 requires
	// per-tenant residency because Saudi rules condition transfers outside the
	// Kingdom, and it cannot be changed later without moving the data — so it is
	// asked at creation rather than defaulted silently.
	DataRegion string

	PlanTier string

	// Market is the country this account is sold into, chosen here by the
	// platform operator rather than left to the Owner.
	//
	// It is NOT a second copy of `company.country`. That column decides tax
	// rules and stays authoritative for them. This one answers the operator's
	// question — which market is this client in — at the only moment the
	// operator is present to answer it. Without it a tenant has no market at
	// all between provisioning and the day the Owner happens to reach setup
	// step `business_info`, which is also the window in which the operator is
	// most likely to be asked about the account.
	//
	// Onboarding then holds the Owner to it, so the two cannot disagree.
	Market string

	OwnerEmail string
	OwnerName  string
}

// Provisioned is the result. The temporary password is returned once and never
// stored in readable form.
type Provisioned struct {
	TenantID          uuid.UUID `json:"tenant_id"`
	OwnerUserID       uuid.UUID `json:"owner_user_id"`
	OwnerEmail        string    `json:"owner_email"`
	TemporaryPassword string    `json:"temporary_password"`
}

// CreateTenant provisions a tenant, its limits, its Owner role and the Owner's
// account, in one transaction.
//
// All or nothing: a tenant with no Owner is unreachable and a tenant with no
// limits has no ceilings, and both would need manual repair from outside the
// product. There is no partial success worth keeping.
func (s *Service) CreateTenant(ctx context.Context, req NewTenant) (Provisioned, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return Provisioned{}, errs.New(errs.CodeForbidden,
			"Only a platform administrator can create a tenant.")
	}

	if err := req.validate(); err != nil {
		return Provisioned{}, err
	}

	// Before anything is written. A tenant half-created and then refused would
	// leave the operator with an account they cannot use and cannot see.
	if err := s.requireMarketIsUsable(ctx, req.Market); err != nil {
		return Provisioned{}, err
	}

	tempPassword, err := identity.GenerateTemporaryPassword()
	if err != nil {
		return Provisioned{}, err
	}
	hash, err := identity.HashPassword(tempPassword)
	if err != nil {
		return Provisioned{}, err
	}

	out := Provisioned{OwnerEmail: req.OwnerEmail, TemporaryPassword: tempPassword}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO tenant (name, data_region, plan_tier, market)
			VALUES ($1, $2::data_region, $3::plan_tier, $4)
			RETURNING id`,
			req.Name, req.DataRegion, req.PlanTier, req.Market).Scan(&out.TenantID); err != nil {
			return err
		}

		// Ceilings come from the tier defaults rather than being written here,
		// so raising a tier's limits is one central update instead of a
		// migration touching every tenant.
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_limit
			  (tenant_id, max_companies, max_stores, max_users, max_terminals,
			   max_skus, max_held_carts, max_custom_roles, max_storage_mb, sms_credits)
			SELECT $1, max_companies, max_stores, max_users, max_terminals,
			       max_skus, max_held_carts, max_custom_roles, max_storage_mb, sms_credits
			FROM plan_tier_default WHERE tier = $2::plan_tier`,
			out.TenantID, req.PlanTier); err != nil {
			return err
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash, status, must_change_password)
			VALUES ($1, $2, $3, $4, 'invited', true)
			RETURNING id`,
			out.TenantID, req.OwnerEmail, req.OwnerName, hash).Scan(&out.OwnerUserID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO onboarding_progress (tenant_id) VALUES ($1)`,
			out.TenantID); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return Provisioned{}, db.Translate(err,
			"That tenant could not be created.")
	}

	// The Owner's role is a tenant-owned record, so it is created in tenant
	// context. Migration 0006 deliberately keeps `role` off the platform plane:
	// a tenant's own role definitions are not the platform operator's business,
	// and provisioning is not an excuse to widen that.
	err = s.pool.TxAsTenant(ctx, out.TenantID, func(tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name, name_ar, description, is_system, cloned_from)
			SELECT $1, key, name, name_ar, description, true, id
			FROM role WHERE tenant_id IS NULL AND key = 'owner'
			RETURNING id`, out.TenantID).Scan(&roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT $1, rp.permission
			FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = 'owner'`, roleID); err != nil {
			return err
		}
		// Unscoped: no company, store, warehouse or amount limit. Blueprint
		// A6.1 gives the Owner "everything in their tenant, unrestricted".
		_, err := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id)
			VALUES ($1, $2, $3)`, out.TenantID, out.OwnerUserID, roleID)
		return err
	})
	if err != nil {
		return Provisioned{}, db.Translate(err,
			"The tenant was created but its Owner role could not be set up.")
	}

	// Audited on the platform plane, since this is a Super Admin action on a
	// tenant. Blueprint A4 requires every such action to be permanently logged.
	_ = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Through the shared writer, which fills in `actor_label` — the field
		// this INSERT used to omit, and the one that exists so the trail
		// survives the user being deleted.
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &out.TenantID, ActorID: &a.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, a.UserID),
			Action:     "tenant_provisioned",
			EntityType: "tenant", EntityID: &out.TenantID,
			After: map[string]any{
				"plan_tier": req.PlanTier, "data_region": req.DataRegion,
				"market": req.Market,
			},
		})
	})

	return out, nil
}

func (r *NewTenant) validate() error {
	v := errs.Validation("Some details are missing or not valid.")
	bad := false

	r.Name = strings.TrimSpace(r.Name)
	r.OwnerEmail = strings.ToLower(strings.TrimSpace(r.OwnerEmail))
	r.OwnerName = strings.TrimSpace(r.OwnerName)

	if r.Name == "" {
		v.WithField("name", "Enter the business name.")
		bad = true
	}
	if r.OwnerName == "" {
		v.WithField("owner_name", "Enter the owner's full name.")
		bad = true
	}
	if !strings.Contains(r.OwnerEmail, "@") || strings.HasPrefix(r.OwnerEmail, "@") {
		v.WithField("owner_email", "Enter a valid email address.")
		bad = true
	}

	switch r.DataRegion {
	case "sa", "eu", "asia", "other":
	case "":
		// Saudi is the launch market and the only region deployed in v1.
		r.DataRegion = "sa"
	default:
		v.WithField("data_region", "Choose sa, eu, asia or other.")
		bad = true
	}

	// Checked against the same map the Owner's country is checked against, so
	// the operator cannot sell an account into a market the product has no tax
	// rules for — which would produce a client who completes setup and then
	// cannot ring up a sale, discovered at the counter rather than here.
	r.Market = strings.ToLower(strings.TrimSpace(r.Market))
	if r.Market == "" {
		v.WithField("market",
			"Choose the market this business is being sold into.")
		bad = true
	} else if _, ok := supportedCountries[r.Market]; !ok {
		v.WithField("market", "RawSyst serves "+offered(supportedCountries)+
			" so far. Tax rules come from the regulatory register for the "+
			"market you choose, and there are none on file for that one.")
		bad = true
	}

	switch r.PlanTier {
	case "starter", "professional", "business", "enterprise":
	case "":
		r.PlanTier = "starter"
	default:
		v.WithField("plan_tier",
			"Choose starter, professional, business or enterprise.")
		bad = true
	}

	if bad {
		return v
	}
	return nil
}

// Limits are a tenant's ceilings.
type Limits struct {
	MaxCompanies   int `json:"max_companies"`
	MaxStores      int `json:"max_stores"`
	MaxUsers       int `json:"max_users"`
	MaxTerminals   int `json:"max_terminals"`
	MaxSKUs        int `json:"max_skus"`
	MaxHeldCarts   int `json:"max_held_carts"`
	MaxCustomRoles int `json:"max_custom_roles"`
	MaxStorageMB   int `json:"max_storage_mb"`
	SMSCredits     int `json:"sms_credits"`
}

// LimitsFor reads a tenant's ceilings.
func (s *Service) LimitsFor(ctx context.Context, tenantID uuid.UUID) (Limits, error) {
	var l Limits
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT max_companies, max_stores, max_users, max_terminals, max_skus,
			       max_held_carts, max_custom_roles, max_storage_mb, sms_credits
			FROM tenant_limit WHERE tenant_id = $1`, tenantID).
			Scan(&l.MaxCompanies, &l.MaxStores, &l.MaxUsers, &l.MaxTerminals,
				&l.MaxSKUs, &l.MaxHeldCarts, &l.MaxCustomRoles,
				&l.MaxStorageMB, &l.SMSCredits)
	})
	if err != nil {
		return Limits{}, db.Translate(err, "No limits are configured for this tenant.")
	}
	return l, nil
}

// CheckLimit reports whether adding one more of something stays within the
// tenant's ceiling.
//
// The message names the current plan and what to do, because "limit reached" on
// its own leaves an Owner stuck: they need to know whether to delete something
// or upgrade.
func (s *Service) CheckLimit(
	ctx context.Context, tenantID uuid.UUID, what string, current, ceiling int,
) error {
	if current < ceiling {
		return nil
	}
	return errs.Newf(errs.CodeLimitReached,
		"Your plan allows %d %s and you have %d. Remove one, or ask us to raise "+
			"your limit.", ceiling, what, current)
}
