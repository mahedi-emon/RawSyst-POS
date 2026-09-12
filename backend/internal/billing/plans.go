// The price list: what each tier sells, and what it allows.
//
// # Why this exists
//
// `tenant_feature` grants a module to ONE client, and `tenant_limit` raises ONE
// client's ceilings. Both existed and both are the exception. The rule — what a
// tier includes for everybody on it — was written by migration 0097 and by
// nothing else, so "put analytics in Professional" or "raise Starter to ten
// users" meant a code change, a review, a build and a deploy, for a decision
// that is a sentence.
//
// Migration 0139 made the two tables writable by the platform. This is the
// service that writes them.
//
// # Why a tier cannot be created or removed here
//
// The four tiers are the `plan_tier` enum. A fifth needs seeded features, a
// place in the pricing and a decision about what it contains, none of which a
// form supplies; removing one would orphan every tenant on it. So the rows are
// fixed and their values are editable, which is the shape the product needs.

package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/audit"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// PlanDefinition is one tier, as the software owner sells it.
type PlanDefinition struct {
	Tier string `json:"tier"`
	// Features are the module keys this tier includes. Sorted, because a list
	// of forty that reshuffles on every request is one nobody can read.
	Features []string `json:"features"`
	Limits   Limits   `json:"limits"`
}

// tiers are the four the enum allows, in the order they are sold.
var tiers = []string{"starter", "professional", "business", "enterprise"}

func validTier(t string) bool {
	for _, v := range tiers {
		if v == t {
			return true
		}
	}
	return false
}

// PlanDefinitions is the whole price list: every tier, what it includes, and
// the ceilings it carries.
func (s *Service) PlanDefinitions(ctx context.Context) ([]PlanDefinition, error) {
	out := []PlanDefinition{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		byTier := map[string]*PlanDefinition{}
		for _, t := range tiers {
			byTier[t] = &PlanDefinition{Tier: t, Features: []string{}}
		}

		rows, e := tx.Query(ctx, `
			SELECT tier::text, feature FROM plan_feature
			WHERE included ORDER BY tier::text, feature`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var tier, feature string
			if e := rows.Scan(&tier, &feature); e != nil {
				rows.Close()
				return e
			}
			if d, ok := byTier[tier]; ok {
				d.Features = append(d.Features, feature)
			}
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		limitRows, e := tx.Query(ctx, `
			SELECT tier::text, max_companies, max_stores, max_users,
			       max_terminals, max_skus, max_custom_roles, max_storage_mb,
			       sms_credits
			FROM plan_tier_default`)
		if e != nil {
			return e
		}
		defer limitRows.Close()
		for limitRows.Next() {
			var tier string
			var l Limits
			if e := limitRows.Scan(&tier, &l.MaxCompanies, &l.MaxStores,
				&l.MaxUsers, &l.MaxTerminals, &l.MaxSKUs, &l.MaxCustomRoles,
				&l.MaxStorageMB, &l.SMSCredits); e != nil {
				return e
			}
			if d, ok := byTier[tier]; ok {
				d.Limits = l
			}
		}
		if e := limitRows.Err(); e != nil {
			return e
		}

		for _, t := range tiers {
			out = append(out, *byTier[t])
		}
		return nil
	})
	return out, db.Translate(err, "")
}

// SetPlanFeature puts a module in a tier, or takes it out.
//
// Audited with no tenant, because it belongs to no tenant: this is the platform
// changing its own product, and it changes what every client on that tier may
// reach. That is the entry somebody will want when a shop asks why a screen
// appeared or vanished without them doing anything.
func (s *Service) SetPlanFeature(
	ctx context.Context, actorID uuid.UUID, tier, feature string, included bool,
) error {
	if !validTier(tier) {
		return errs.New(errs.CodeInvalidInput, "That is not one of the four plans.")
	}
	if feature == "" {
		return errs.New(errs.CodeInvalidInput, "Name the module.")
	}

	// A feature nothing sells is a typo that silently grants nothing. The
	// catalogue of real keys is `plan_feature` itself, which every tier was
	// seeded from — so a key that appears against no tier has never existed.
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var known bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM plan_feature WHERE feature = $1)`,
			feature).Scan(&known); e != nil {
			return e
		}
		if !known {
			return errs.Newf(errs.CodeInvalidInput,
				"There is no module called %q. Adding a new one is a change to "+
					"the product, not to a plan.", feature)
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO plan_feature (tier, feature, included)
			VALUES ($1::plan_tier, $2, $3)
			ON CONFLICT (tier, feature) DO UPDATE SET included = excluded.included`,
			tier, feature, included); e != nil {
			return db.Translate(e, "That plan could not be changed.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "plan_feature_set",
			EntityType: "plan",
			After: map[string]any{
				"tier": tier, "feature": feature, "included": included,
			},
		})
	})
	return db.Translate(err, "")
}

// SetPlanLimits changes the ceilings a tier carries.
//
// # What it does NOT do
//
// Touch any existing client. `tenant_limit` is written once at provisioning
// from these defaults and is a tenant's own record afterwards, so raising a
// tier here changes what NEW clients get and leaves every current one where
// they are.
//
// That is deliberate and it is the safe direction. Rewriting every tenant's
// ceilings from a plan edit would silently LOWER somebody who had been granted
// an exception — the commonest reason `tenant_limit` differs from its tier is
// that an operator raised it for a client on purpose. Applying a new ceiling to
// existing clients is a separate, per-client act, and the limits screen is
// where it already lives.
func (s *Service) SetPlanLimits(
	ctx context.Context, actorID uuid.UUID, tier string, in Limits,
) error {
	if !validTier(tier) {
		return errs.New(errs.CodeInvalidInput, "That is not one of the four plans.")
	}

	// The table's own CHECK constraint enforces these too. Checking here as
	// well turns a database error into a sentence naming the field.
	v := errs.Validation("Those allowances are not usable.")
	bad := false
	for _, c := range []struct {
		field string
		value int
		min   int
	}{
		{"max_companies", in.MaxCompanies, 1},
		{"max_stores", in.MaxStores, 1},
		{"max_users", in.MaxUsers, 1},
		{"max_terminals", in.MaxTerminals, 1},
		{"max_skus", in.MaxSKUs, 1},
		{"max_custom_roles", in.MaxCustomRoles, 0},
		{"max_storage_mb", in.MaxStorageMB, 1},
		{"sms_credits", in.SMSCredits, 0},
	} {
		if c.value < c.min {
			v.WithField(c.field, "At least "+itoa(c.min)+".")
			bad = true
		}
	}
	if bad {
		return v
	}

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE plan_tier_default
			   SET max_companies = $2, max_stores = $3, max_users = $4,
			       max_terminals = $5, max_skus = $6, max_custom_roles = $7,
			       max_storage_mb = $8, sms_credits = $9, updated_at = now()
			 WHERE tier = $1::plan_tier`,
			tier, in.MaxCompanies, in.MaxStores, in.MaxUsers, in.MaxTerminals,
			in.MaxSKUs, in.MaxCustomRoles, in.MaxStorageMB, in.SMSCredits)
		if e != nil {
			return db.Translate(e, "Those allowances could not be saved.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That plan was not found.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "plan_limits_set",
			EntityType: "plan",
			After: map[string]any{
				"tier": tier, "max_users": in.MaxUsers,
				"max_stores": in.MaxStores, "max_terminals": in.MaxTerminals,
				"max_skus": in.MaxSKUs, "max_storage_mb": in.MaxStorageMB,
				// Said in the trail, because it is the question somebody will
				// ask when a client's ceiling did not move.
				"applies_to": "new clients only; existing tenant_limit rows are unchanged",
			},
		})
	})
	return db.Translate(err, "")
}

// itoa avoids pulling strconv in for one message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
