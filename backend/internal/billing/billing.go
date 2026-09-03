// Package billing is the SaaS subscription, its invoices, and the per-tenant
// feature flags that decide what a client may reach (blueprint H5).
//
// # Entitlement is resolved, never copied
//
// What a tenant may use is their PLAN, with their own exceptions applied. Two
// tables, one answer, computed at read time.
//
// The alternative — copying a tier's features onto each tenant at signup — is
// the design that cannot answer "we are adding Wholesale to Professional next
// month". It would need a backfill over every tenant, and every tenant who had
// ever been given a hand-made exception would be silently overwritten by that
// backfill. H5's whole argument for flags is commercial flexibility, and a
// design that loses the exceptions has none.
//
// # A flag hides a module; it does not weaken a permission
//
// `Allows` is asked at the route, before the permission check, and answers a
// commercial question: has this client paid for this module. Permissions answer
// a different one: may this person, in this shop, do this thing. Both have to
// pass. Folding entitlement into the permission system would mean revoking a
// feature had to rewrite every affected role, and restoring it had to guess
// which roles used to hold what.
//
// # Suspension reuses the tenant status
//
// `tenant.status` already has 'suspended' and the sign-in path already refuses
// a tenant that is not active. Dunning therefore invents nothing: past the
// grace period it moves the tenant to suspended, and payment moves it back.
// A separate billing lock would be a second way to be locked out, and a second
// thing every request would have to check.
//
// # The platform's invoices are not the tenant's invoices
//
// `subscription_invoice` is the platform billing a client for the software. It
// carries no e-invoicing anything and never touches the tenant's ledger: the
// tenant is the customer here and the platform is the seller, and putting these
// in `sales_invoice` would put the platform's revenue inside a client's books
// and inside their VAT return.
package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service carries plans, entitlements and subscription invoices.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking.
type Scope struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// Entitlement is one module and whether this tenant may reach it.
type Entitlement struct {
	Feature string `json:"feature"`
	// Allowed is the answer after the tenant's exceptions are applied.
	Allowed bool `json:"allowed"`
	// InPlan is what the tier alone says, so a screen can show "not in your
	// plan, granted to you" rather than a bare tick.
	InPlan bool `json:"in_plan"`
	// Reason and ExpiresOn are present only where an exception is in force.
	Reason    string `json:"reason,omitempty"`
	ExpiresOn string `json:"expires_on,omitempty"`
}

// Subscription is the commercial relationship.
type Subscription struct {
	Tier   string `json:"tier"`
	Cycle  string `json:"cycle"`
	Price  string `json:"price"`
	Status string `json:"status"`

	Currency string `json:"currency"`

	StartedOn        string `json:"started_on"`
	TrialEndsOn      string `json:"trial_ends_on,omitempty"`
	CurrentPeriodEnd string `json:"current_period_end,omitempty"`
	CancelledOn      string `json:"cancelled_on,omitempty"`
	GraceDays        int    `json:"grace_days"`
	Note             string `json:"note,omitempty"`

	// Outstanding is what is billed and unpaid, and DaysOverdue counts from the
	// oldest unpaid invoice's due date. Both computed: an "overdue" column
	// would be a stored fact that can disagree with the calendar.
	Outstanding string `json:"outstanding"`
	DaysOverdue *int   `json:"days_overdue,omitempty"`

	Limits Limits `json:"limits"`
}

// Limits is the countable half of H5, which has been enforced since 0002.
//
// Reported beside the plan because a client asking "why can I not add a fourth
// till" is asking about their subscription, and the answer lives here.
type Limits struct {
	MaxCompanies   int `json:"max_companies"`
	MaxStores      int `json:"max_stores"`
	MaxUsers       int `json:"max_users"`
	MaxTerminals   int `json:"max_terminals"`
	MaxSKUs        int `json:"max_skus"`
	MaxCustomRoles int `json:"max_custom_roles"`
	MaxStorageMB   int `json:"max_storage_mb"`
	SMSCredits     int `json:"sms_credits"`

	// What is actually used, so the screen shows 3 of 5 rather than 5.
	Companies int `json:"companies"`
	Stores    int `json:"stores"`
	Users     int `json:"users"`
	Terminals int `json:"terminals"`
}

// Invoice is one bill the platform sent a tenant.
type Invoice struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"invoice_no"`
	PeriodFrom string    `json:"period_start"`
	PeriodTo   string    `json:"period_end"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	IssuedOn   string    `json:"issued_on"`
	DueOn      string    `json:"due_on"`
	PaidAt     string    `json:"paid_at,omitempty"`
	PaymentRef string    `json:"payment_ref,omitempty"`
	Note       string    `json:"note,omitempty"`
	// Overdue is computed from the due date and the status, never stored.
	Overdue bool `json:"overdue"`
}

// ---------------------------------------------------------------------------
// Entitlement
// ---------------------------------------------------------------------------

// Allows answers whether a tenant may reach one module.
//
// Takes the caller's transaction, because it is asked on the request path in
// front of a handler and resolving it on a second connection while the handler
// holds the first is the pool deadlock this codebase has already met once.
func (s *Service) Allows(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, feature string,
) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `
		SELECT coalesce(
		         (SELECT tf.enabled FROM tenant_feature tf
		           WHERE tf.tenant_id = t.id AND tf.feature = $2
		             AND (tf.expires_on IS NULL
		                  OR tf.expires_on >= current_date)),
		         (SELECT pf.included FROM plan_feature pf
		           WHERE pf.tier = coalesce(s.tier, t.plan_tier)
		             AND pf.feature = $2),
		         false)
		FROM tenant t
		LEFT JOIN subscription s ON s.tenant_id = t.id
		WHERE t.id = $1`, tenantID, feature).Scan(&allowed)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return allowed, err
}

// Entitlements lists every gateable module and this tenant's answer for it.
func (s *Service) Entitlements(
	ctx context.Context, scope Scope,
) ([]Entitlement, error) {
	out := []Entitlement{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT pf.feature, pf.included,
			       tf.enabled, coalesce(tf.reason, ''), tf.expires_on
			FROM tenant t
			LEFT JOIN subscription s ON s.tenant_id = t.id
			JOIN plan_feature pf ON pf.tier = coalesce(s.tier, t.plan_tier)
			LEFT JOIN tenant_feature tf
			       ON tf.tenant_id = t.id AND tf.feature = pf.feature
			      AND (tf.expires_on IS NULL OR tf.expires_on >= current_date)
			WHERE t.id = $1
			ORDER BY pf.feature`, scope.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var ent Entitlement
			var override *bool
			var expires *time.Time
			if e := rows.Scan(&ent.Feature, &ent.InPlan, &override,
				&ent.Reason, &expires); e != nil {
				return e
			}
			ent.Allowed = ent.InPlan
			if override != nil {
				ent.Allowed = *override
				if expires != nil {
					ent.ExpiresOn = expires.Format("2006-01-02")
				}
			} else {
				// A reason belongs to an exception. Without one it would read
				// as an explanation of the plan, which it is not.
				ent.Reason = ""
			}
			out = append(out, ent)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SetFeature grants or withdraws a module for one tenant.
//
// Platform-only: a tenant who could edit their own entitlements would be a
// tenant on the Enterprise plan. The reason is required for the same purpose it
// is required on a legal hold — six months later, an exception without one is
// indistinguishable from a mistake.
func (s *Service) SetFeature(
	ctx context.Context, actorID, tenantID uuid.UUID,
	feature string, enabled bool, reason, expiresOn string,
) error {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return errs.New(errs.CodeInvalidInput, "Name the module.")
	}
	if strings.TrimSpace(reason) == "" {
		return errs.New(errs.CodeInvalidInput,
			"Say why this client is being given an exception to their plan.")
	}
	var expires *time.Time
	if expiresOn != "" {
		d, err := time.Parse("2006-01-02", expiresOn)
		if err != nil {
			return errs.New(errs.CodeInvalidInput,
				"That expiry date is not a date.")
		}
		expires = &d
	}

	return db.Translate(s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var known bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM plan_feature WHERE feature = $1)`,
			feature).Scan(&known); e != nil {
			return e
		}
		if !known {
			return errs.New(errs.CodeInvalidInput,
				"That module is not one the plans control.")
		}

		_, e := tx.Exec(ctx, `
			INSERT INTO tenant_feature (
			  tenant_id, feature, enabled, reason, expires_on, granted_by)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (tenant_id, feature) DO UPDATE SET
			  enabled = excluded.enabled,
			  reason = excluded.reason,
			  expires_on = excluded.expires_on,
			  granted_by = excluded.granted_by,
			  granted_at = now()`,
			tenantID, feature, enabled, strings.TrimSpace(reason), expires,
			actorID)
		if e != nil {
			return e
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "tenant_feature_set",
			EntityType: "tenant_feature", EntityID: &tenantID,
			After: map[string]any{
				"feature": feature, "enabled": enabled, "reason": reason,
			},
		})
	}), "")
}

// ClearFeature drops an exception, so the plan's own answer resumes.
func (s *Service) ClearFeature(
	ctx context.Context, actorID, tenantID uuid.UUID, feature string,
) error {
	return db.Translate(s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx,
			`DELETE FROM tenant_feature WHERE tenant_id = $1 AND feature = $2`,
			tenantID, feature)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That client has no exception for that module.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "tenant_feature_cleared",
			EntityType: "tenant_feature", EntityID: &tenantID,
			Before: map[string]any{"feature": feature},
		})
	}), "")
}

// Plans is the tier matrix, for a screen comparing what each includes.
func (s *Service) Plans(
	ctx context.Context, scope Scope,
) (map[string][]string, error) {
	out := map[string][]string{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT tier::text, feature FROM plan_feature
			 WHERE included ORDER BY tier, feature`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var tier, feature string
			if e := rows.Scan(&tier, &feature); e != nil {
				return e
			}
			out[tier] = append(out[tier], feature)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ---------------------------------------------------------------------------
// The subscription
// ---------------------------------------------------------------------------

// Subscription reads a tenant's plan, usage and outstanding balance.
func (s *Service) Subscription(
	ctx context.Context, scope Scope,
) (Subscription, error) {
	var out Subscription
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return s.read(ctx, tx, scope.TenantID, &out)
	})
	return out, db.Translate(err, "")
}

// SubscriptionOf is the platform's read of any tenant.
func (s *Service) SubscriptionOf(
	ctx context.Context, tenantID uuid.UUID,
) (Subscription, error) {
	var out Subscription
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return s.read(ctx, tx, tenantID, &out)
	})
	return out, db.Translate(err, "")
}

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, out *Subscription,
) error {
	var price decimal.Decimal
	var trial, periodEnd, cancelled *time.Time
	var started time.Time
	e := tx.QueryRow(ctx, `
		SELECT coalesce(s.tier, t.plan_tier)::text,
		       coalesce(s.cycle, 'monthly'),
		       coalesce(s.price, 0), coalesce(s.currency, 'SAR'),
		       coalesce(s.status, 'active'),
		       coalesce(s.started_on, current_date), s.trial_ends_on,
		       s.current_period_end, s.cancelled_on,
		       coalesce(s.grace_days, 14), coalesce(s.note, '')
		FROM tenant t
		LEFT JOIN subscription s ON s.tenant_id = t.id
		WHERE t.id = $1`, tenantID).Scan(
		&out.Tier, &out.Cycle, &price, &out.Currency, &out.Status,
		&started, &trial, &periodEnd, &cancelled, &out.GraceDays, &out.Note)
	if e == pgx.ErrNoRows {
		return errs.New(errs.CodeNotFound, "That client was not found.")
	}
	if e != nil {
		return e
	}
	out.Price = price.StringFixed(2)
	out.StartedOn = started.Format("2006-01-02")
	for _, p := range []struct {
		t   *time.Time
		dst *string
	}{{trial, &out.TrialEndsOn}, {periodEnd, &out.CurrentPeriodEnd},
		{cancelled, &out.CancelledOn}} {
		if p.t != nil {
			*p.dst = p.t.Format("2006-01-02")
		}
	}

	var outstanding decimal.Decimal
	var oldestDue *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT coalesce(sum(amount), 0), min(due_on)
		FROM subscription_invoice
		WHERE tenant_id = $1 AND status = 'issued'`, tenantID).Scan(
		&outstanding, &oldestDue); e != nil {
		return e
	}
	out.Outstanding = outstanding.StringFixed(2)
	if oldestDue != nil && oldestDue.Before(time.Now().UTC()) {
		days := int(time.Since(*oldestDue).Hours() / 24)
		out.DaysOverdue = &days
	}

	return s.limits(ctx, tx, tenantID, &out.Limits)
}

func (s *Service) limits(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, out *Limits,
) error {
	if e := tx.QueryRow(ctx, `
		SELECT max_companies, max_stores, max_users, max_terminals, max_skus,
		       max_custom_roles, max_storage_mb, sms_credits
		FROM tenant_limit WHERE tenant_id = $1`, tenantID).Scan(
		&out.MaxCompanies, &out.MaxStores, &out.MaxUsers, &out.MaxTerminals,
		&out.MaxSKUs, &out.MaxCustomRoles, &out.MaxStorageMB,
		&out.SMSCredits); e != nil && e != pgx.ErrNoRows {
		return e
	}

	return tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM company WHERE tenant_id = $1),
		       (SELECT count(*) FROM store   WHERE tenant_id = $1),
		       (SELECT count(*) FROM app_user
		         WHERE tenant_id = $1 AND status <> 'disabled'),
		       (SELECT count(*) FROM device
		         WHERE tenant_id = $1 AND status <> 'revoked')`,
		tenantID).Scan(&out.Companies, &out.Stores, &out.Users,
		&out.Terminals)
}

// NewPlan is the platform setting a client's commercial terms.
type NewPlan struct {
	Tier        string
	Cycle       string
	Price       string
	Currency    string
	Status      string
	TrialEndsOn string
	GraceDays   int
	Note        string
}

// SetPlan writes the subscription and moves the tenant's tier with it.
//
// The tier is written in two places on purpose: `tenant.plan_tier` is what the
// rest of the product already reads and what `tenant_limit` was provisioned
// from, and the subscription is where the commercial detail lives. Leaving them
// able to disagree would mean an upgrade that changed the price and not the
// ceilings.
func (s *Service) SetPlan(
	ctx context.Context, actorID, tenantID uuid.UUID, in NewPlan,
) (Subscription, error) {
	switch in.Tier {
	case "starter", "professional", "business", "enterprise":
	default:
		return Subscription{}, errs.New(errs.CodeInvalidInput,
			"That is not one of the four plans.")
	}
	switch in.Cycle {
	case "monthly", "yearly", "lifetime":
	default:
		return Subscription{}, errs.New(errs.CodeInvalidInput,
			"A subscription is billed monthly, yearly, or once.")
	}
	price, err := decimal.NewFromString(strings.TrimSpace(in.Price))
	if err != nil || price.IsNegative() {
		return Subscription{}, errs.New(errs.CodeInvalidInput,
			"That price is not an amount.")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if len(currency) != 3 {
		return Subscription{}, errs.New(errs.CodeInvalidInput,
			"Name the currency the client is billed in.")
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	grace := in.GraceDays
	if grace <= 0 {
		grace = 14
	}

	var trial *time.Time
	if in.TrialEndsOn != "" {
		d, perr := time.Parse("2006-01-02", in.TrialEndsOn)
		if perr != nil {
			return Subscription{}, errs.New(errs.CodeInvalidInput,
				"That trial end date is not a date.")
		}
		trial = &d
	}

	// A lifetime subscription has no renewal date; anything else renews at the
	// end of the cycle it is on.
	periodEnd := ""
	switch in.Cycle {
	case "monthly":
		periodEnd = time.Now().UTC().AddDate(0, 1, 0).Format("2006-01-02")
	case "yearly":
		periodEnd = time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			INSERT INTO subscription (
			  tenant_id, tier, cycle, price, currency, status, trial_ends_on,
			  current_period_end, grace_days, note)
			VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,'')::date,$9,nullif($10,''))
			ON CONFLICT (tenant_id) DO UPDATE SET
			  tier = excluded.tier,
			  cycle = excluded.cycle,
			  price = excluded.price,
			  currency = excluded.currency,
			  status = excluded.status,
			  trial_ends_on = excluded.trial_ends_on,
			  current_period_end = excluded.current_period_end,
			  grace_days = excluded.grace_days,
			  note = excluded.note`,
			tenantID, in.Tier, in.Cycle, price, currency, status, trial,
			periodEnd, grace, strings.TrimSpace(in.Note)); e != nil {
			return db.Translate(e, "That plan could not be saved.")
		}

		if _, e := tx.Exec(ctx,
			`UPDATE tenant SET plan_tier = $2 WHERE id = $1`,
			tenantID, in.Tier); e != nil {
			return e
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "subscription_set",
			EntityType: "subscription", EntityID: &tenantID,
			After: map[string]any{
				"tier": in.Tier, "cycle": in.Cycle,
				"price": price.StringFixed(2), "currency": currency,
			},
		})
	})
	if err != nil {
		return Subscription{}, db.Translate(err, "")
	}
	return s.SubscriptionOf(ctx, tenantID)
}

// ---------------------------------------------------------------------------
// Invoices and dunning
// ---------------------------------------------------------------------------

// Invoices lists what a tenant was billed.
func (s *Service) Invoices(
	ctx context.Context, scope Scope,
) ([]Invoice, error) {
	var out []Invoice
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		list, e := invoicesOf(ctx, tx, scope.TenantID)
		out = list
		return e
	})
	return out, db.Translate(err, "")
}

// InvoicesOf is the platform's read of any tenant's bills.
func (s *Service) InvoicesOf(
	ctx context.Context, tenantID uuid.UUID,
) ([]Invoice, error) {
	var out []Invoice
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		list, e := invoicesOf(ctx, tx, tenantID)
		out = list
		return e
	})
	return out, db.Translate(err, "")
}

func invoicesOf(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID,
) ([]Invoice, error) {
	out := []Invoice{}
	rows, err := tx.Query(ctx, `
		SELECT id, invoice_no, period_start, period_end, amount, currency,
		       status, issued_on, due_on, paid_at, coalesce(payment_ref, ''),
		       coalesce(note, '')
		FROM subscription_invoice
		WHERE tenant_id = $1
		ORDER BY issued_on DESC, invoice_no DESC
		LIMIT 200`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var inv Invoice
		var amount decimal.Decimal
		var from, to, issued, due time.Time
		var paid *time.Time
		if e := rows.Scan(&inv.ID, &inv.Number, &from, &to, &amount,
			&inv.Currency, &inv.Status, &issued, &due, &paid,
			&inv.PaymentRef, &inv.Note); e != nil {
			return nil, e
		}
		inv.Amount = amount.StringFixed(2)
		inv.PeriodFrom = from.Format("2006-01-02")
		inv.PeriodTo = to.Format("2006-01-02")
		inv.IssuedOn = issued.Format("2006-01-02")
		inv.DueOn = due.Format("2006-01-02")
		if paid != nil {
			inv.PaidAt = paid.UTC().Format(time.RFC3339)
		}
		inv.Overdue = inv.Status == "issued" && due.Before(time.Now().UTC())
		out = append(out, inv)
	}
	return out, rows.Err()
}

// IssueInvoice bills a tenant for one period.
func (s *Service) IssueInvoice(
	ctx context.Context, actorID, tenantID uuid.UUID,
	periodStart, periodEnd, amount, note string,
) (Invoice, error) {
	from, err := time.Parse("2006-01-02", periodStart)
	if err != nil {
		return Invoice{}, errs.New(errs.CodeInvalidInput,
			"That period start is not a date.")
	}
	to, err := time.Parse("2006-01-02", periodEnd)
	if err != nil || to.Before(from) {
		return Invoice{}, errs.New(errs.CodeInvalidInput,
			"The period has to end on or after it starts.")
	}
	value, err := decimal.NewFromString(strings.TrimSpace(amount))
	if err != nil || value.IsNegative() {
		return Invoice{}, errs.New(errs.CodeInvalidInput,
			"That amount is not an amount.")
	}

	var id uuid.UUID
	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var currency string
		var grace int
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(s.currency, 'SAR'), coalesce(s.grace_days, 14)
			FROM tenant t
			LEFT JOIN subscription s ON s.tenant_id = t.id
			WHERE t.id = $1`, tenantID).Scan(&currency, &grace); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That client was not found.")
			}
			return e
		}

		var n int64
		if e := tx.QueryRow(ctx,
			`SELECT nextval('subscription_invoice_seq')`).Scan(&n); e != nil {
			return e
		}

		// The due date is the grace period after issue, so the same number
		// governs both when the bill is due and when suspension follows.
		if e := tx.QueryRow(ctx, `
			INSERT INTO subscription_invoice (
			  tenant_id, invoice_no, period_start, period_end, amount,
			  currency, due_on, note)
			VALUES ($1,$2,$3,$4,$5,$6,
			        current_date + make_interval(days => $7), nullif($8,''))
			RETURNING id`,
			tenantID, fmt.Sprintf("SUB-%06d", n), from, to, value, currency,
			grace, strings.TrimSpace(note)).Scan(&id); e != nil {
			return db.Translate(e, "That invoice could not be raised.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "subscription_invoice_issued",
			EntityType: "subscription_invoice", EntityID: &id,
			After: map[string]any{
				"amount": value.StringFixed(2), "currency": currency,
			},
		})
	})
	if err != nil {
		return Invoice{}, db.Translate(err, "")
	}
	return s.invoice(ctx, id)
}

// MarkPaid settles an invoice and lifts a suspension it caused.
//
// The lift is unconditional on this tenant having nothing else outstanding:
// paying the oldest of three overdue bills should not restore service, and
// checking is one query rather than a support call.
func (s *Service) MarkPaid(
	ctx context.Context, actorID, invoiceID uuid.UUID, ref string,
) (Invoice, error) {
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var tenantID uuid.UUID
		e := tx.QueryRow(ctx, `
			UPDATE subscription_invoice
			   SET status = 'paid', paid_at = now(),
			       payment_ref = nullif($2,'')
			 WHERE id = $1 AND status = 'issued'
			RETURNING tenant_id`,
			invoiceID, strings.TrimSpace(ref)).Scan(&tenantID)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeConflict,
				"That invoice was not found, or it is already settled.")
		}
		if e != nil {
			return e
		}

		var stillOwing bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM subscription_invoice
			                WHERE tenant_id = $1 AND status = 'issued')`,
			tenantID).Scan(&stillOwing); e != nil {
			return e
		}

		if !stillOwing {
			if _, e := tx.Exec(ctx, `
				UPDATE subscription SET status = 'active'
				 WHERE tenant_id = $1 AND status IN ('past_due', 'suspended')`,
				tenantID); e != nil {
				return e
			}
			// The tenant is only un-suspended if billing is what suspended
			// them. A tenant suspended for any other reason stays suspended,
			// and this has no way to know which — so it only lifts a
			// suspension whose subscription said it was the cause.
			if _, e := tx.Exec(ctx, `
				UPDATE tenant SET status = 'active'
				 WHERE id = $1 AND status = 'suspended'
				   AND EXISTS (SELECT 1 FROM subscription
				                WHERE tenant_id = $1 AND status = 'active')`,
				tenantID); e != nil {
				return e
			}
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "subscription_invoice_paid",
			EntityType: "subscription_invoice", EntityID: &invoiceID,
			After: map[string]any{"payment_ref": ref},
		})
	})
	if err != nil {
		return Invoice{}, db.Translate(err, "")
	}
	return s.invoice(ctx, invoiceID)
}

// VoidInvoice cancels a bill that should not have been raised.
func (s *Service) VoidInvoice(
	ctx context.Context, actorID, invoiceID uuid.UUID, reason string,
) (Invoice, error) {
	if strings.TrimSpace(reason) == "" {
		return Invoice{}, errs.New(errs.CodeInvalidInput,
			"Say why the invoice is being cancelled.")
	}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var tenantID uuid.UUID
		e := tx.QueryRow(ctx, `
			UPDATE subscription_invoice
			   SET status = 'void', note = $2
			 WHERE id = $1 AND status = 'issued'
			RETURNING tenant_id`, invoiceID, strings.TrimSpace(reason)).
			Scan(&tenantID)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeConflict,
				"That invoice was not found, or it is already settled.")
		}
		if e != nil {
			return e
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "subscription_invoice_voided",
			EntityType: "subscription_invoice", EntityID: &invoiceID,
			After: map[string]any{"reason": reason},
		})
	})
	if err != nil {
		return Invoice{}, db.Translate(err, "")
	}
	return s.invoice(ctx, invoiceID)
}

func (s *Service) invoice(
	ctx context.Context, id uuid.UUID,
) (Invoice, error) {
	var out Invoice
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var tenantID uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT tenant_id FROM subscription_invoice WHERE id = $1`,
			id).Scan(&tenantID); e != nil {
			return e
		}
		list, e := invoicesOf(ctx, tx, tenantID)
		if e != nil {
			return e
		}
		for _, inv := range list {
			if inv.ID == id {
				out = inv
				return nil
			}
		}
		return errs.New(errs.CodeNotFound, "That invoice was not found.")
	})
	return out, db.Translate(err, "")
}

// Dun suspends clients whose bills are past the grace period, and returns how
// many it moved.
//
// H5's "dunning/suspension on non-payment (configurable grace period)". Run
// from the scheduled job rather than on a request, so the moment a shop stops
// working is a batch somebody can see rather than an accident of who happened
// to sign in.
//
// Two steps rather than one, because past_due and suspended are different
// things to be: past_due means a bill is late and the shop still works, and
// suspended means it does not.
func (s *Service) Dun(ctx context.Context) (int, error) {
	moved := 0
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			UPDATE subscription s SET status = 'past_due'
			 WHERE s.status = 'active'
			   AND EXISTS (
			     SELECT 1 FROM subscription_invoice i
			      WHERE i.tenant_id = s.tenant_id AND i.status = 'issued'
			        AND i.due_on < current_date)`); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			UPDATE subscription s SET status = 'suspended'
			 WHERE s.status = 'past_due'
			   AND EXISTS (
			     SELECT 1 FROM subscription_invoice i
			      WHERE i.tenant_id = s.tenant_id AND i.status = 'issued'
			        AND i.due_on + make_interval(days => s.grace_days)
			            < current_date)
			RETURNING s.tenant_id`)
		if e != nil {
			return e
		}
		suspended := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			suspended = append(suspended, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, id := range suspended {
			if _, e := tx.Exec(ctx,
				`UPDATE tenant SET status = 'suspended'
				  WHERE id = $1 AND status = 'active'`, id); e != nil {
				return e
			}
			tenantID := id
			if e := audit.Write(ctx, tx, audit.Entry{
				TenantID: &tenantID,
				Action:   "tenant_suspended_for_non_payment",
				// No actor: nobody pressed anything. The job did it, and
				// recording a person would name somebody who was asleep.
				ActorLabel: "Billing",
				EntityType: "subscription", EntityID: &tenantID,
			}); e != nil {
				return e
			}
			moved++
		}
		return nil
	})
	return moved, db.Translate(err, "")
}

// Permits is Allows with its own transaction, for the request path.
//
// `Allows` takes the caller's transaction because it was written to be asked in
// front of a handler that already holds one. The entitlement middleware runs
// BEFORE any handler, so it holds nothing and opening a connection here is
// safe — and is what lets the gate be a middleware rather than a line repeated
// in every handler that could forget it.
func (s *Service) Permits(
	ctx context.Context, tenantID uuid.UUID, feature string,
) (bool, error) {
	var allowed bool
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		allowed, e = s.Allows(ctx, tx, tenantID, feature)
		return e
	})
	return allowed, err
}
