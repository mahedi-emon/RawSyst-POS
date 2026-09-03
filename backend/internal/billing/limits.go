// Changing what a tenant is allowed (blueprint A4).
//
// `tenant_limit` decides how many companies, shops, users, terminals, SKUs,
// custom roles, megabytes and SMS credits a business may have. It is enforced
// throughout — provisioning refuses a second company on a one-company plan,
// identity refuses the sixth user on a plan that sells five — and it was
// provisioned once at signup and then unreachable.
//
// So a tenant that upgraded could not actually be given the headroom they had
// paid for without a SQL client against production. A4 puts tenant limits in
// the Platform Owner's hands; this is that.
package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// LimitChange is what a Platform Owner is raising or lowering.
//
// Every field is optional: a plan upgrade usually moves one or two numbers, and
// requiring the whole set would mean re-sending values nobody meant to touch —
// which is how a storage limit gets reset because somebody was adding a till.
type LimitChange struct {
	MaxCompanies   *int
	MaxStores      *int
	MaxUsers       *int
	MaxTerminals   *int
	MaxSKUs        *int
	MaxCustomRoles *int
	MaxStorageMB   *int
	SMSCredits     *int
}

// SetLimits changes a tenant's allowances.
//
// # Lowering below what is already in use is refused
//
// A limit is a gate on creating the next one, not a command to delete what
// exists. Setting max_stores to two for a business running five would leave it
// in a state the product cannot express: nothing would close the three, and
// every subsequent check would fail against a number that was never true. The
// refusal names what is in use so the Platform Owner can see why.
func (s *Service) SetLimits(
	ctx context.Context, tenantID uuid.UUID, by uuid.UUID, in LimitChange,
) (Limits, error) {
	var out Limits
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var current Limits
		if e := s.limits(ctx, tx, tenantID, &current); e != nil {
			return e
		}

		// In use now, so a lower limit cannot be set behind a business's back.
		for _, c := range []struct {
			name    string
			want    *int
			inUse   int
			present bool
		}{
			{"companies", in.MaxCompanies, current.Companies, true},
			{"shops", in.MaxStores, current.Stores, true},
			{"users", in.MaxUsers, current.Users, true},
			{"terminals", in.MaxTerminals, current.Terminals, true},
		} {
			if c.want != nil && c.present && *c.want < c.inUse {
				return errs.Newf(errs.CodeConflict,
					"This business already has %d %s, so the limit cannot be "+
						"set to %d. A limit governs the next one created; it "+
						"does not remove what exists.",
					c.inUse, c.name, *c.want)
			}
		}

		if _, e := tx.Exec(ctx, `
			UPDATE tenant_limit SET
			  max_companies    = coalesce($2, max_companies),
			  max_stores       = coalesce($3, max_stores),
			  max_users        = coalesce($4, max_users),
			  max_terminals    = coalesce($5, max_terminals),
			  max_skus         = coalesce($6, max_skus),
			  max_custom_roles = coalesce($7, max_custom_roles),
			  max_storage_mb   = coalesce($8, max_storage_mb),
			  sms_credits      = coalesce($9, sms_credits),
			  updated_at       = now()
			WHERE tenant_id = $1`,
			tenantID, in.MaxCompanies, in.MaxStores, in.MaxUsers,
			in.MaxTerminals, in.MaxSKUs, in.MaxCustomRoles, in.MaxStorageMB,
			in.SMSCredits); e != nil {
			return db.Translate(e, "Those limits could not be saved.")
		}

		if e := s.limits(ctx, tx, tenantID, &out); e != nil {
			return e
		}

		// Written against the TENANT whose allowance changed, so the record
		// sits where somebody investigating that business will look, rather
		// than only in the platform's own log.
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &by,
			Action:     "tenant_limits_changed",
			EntityType: "tenant", EntityID: &tenantID,
			Before: map[string]any{
				"max_companies": current.MaxCompanies,
				"max_stores":    current.MaxStores,
				"max_users":     current.MaxUsers,
				"max_terminals": current.MaxTerminals,
			},
			After: map[string]any{
				"max_companies": out.MaxCompanies,
				"max_stores":    out.MaxStores,
				"max_users":     out.MaxUsers,
				"max_terminals": out.MaxTerminals,
			},
		})
	})
	return out, db.Translate(err, "")
}

// TenantLimits reads a tenant's allowances and what is used against them.
func (s *Service) TenantLimits(
	ctx context.Context, tenantID uuid.UUID,
) (Limits, error) {
	var out Limits
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return s.limits(ctx, tx, tenantID, &out)
	})
	return out, db.Translate(err, "")
}
