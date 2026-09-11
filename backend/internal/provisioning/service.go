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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/billing"
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

	// mail queues the new owner's welcome message. Optional, on the same terms
	// as rules: a caller that has not wired it creates the business anyway and
	// says so in the result, rather than failing a provisioning run over a
	// message.
	mail identity.Enqueuer

	// appURL is where a business signs in, for the welcome message to name.
	// Empty leaves the address out rather than guessing at one.
	appURL string

	// mailState is what this deployment will actually DO with a queued
	// message, which is not the same as whether it was queued. See MailStatus.
	mailState string
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// WithMail wires the welcome message and says what will become of it.
//
// `state` is one of the MailStatus constants and describes the WORKER's
// configuration, not this process's: a message queued into a deployment whose
// mailer only logs has not been sent to anybody, and the operator handing over
// the account is the one who needs to know that. The alternative -- reporting
// "queued" and leaving it there -- is how an owner is left waiting for a mail
// that was never going to arrive.
func (s *Service) WithMail(mail identity.Enqueuer, state string) *Service {
	s.mail = mail
	s.mailState = state
	return s
}

// WithAppURL tells provisioning where the business application answers, so the
// welcome message can name it. Empty leaves it unmentioned.
func (s *Service) WithAppURL(url string) *Service {
	s.appURL = strings.TrimRight(strings.TrimSpace(url), "/")
	return s
}

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
// Saudi client at 10:00 and keep selling it a till that cannot issue an invoice
// until somebody happened to restart. This closes that window at the only point
// where the market is chosen.
//
// # It asks only about rules that stop a market trading
//
// 0124 split a release blocker into what it prevents, and this reads
// `blocks = 'onboarding'` only. The ZATCA invoice formats qualify: without them
// nothing in Saudi Arabia can be rung up, so onboarding a business would be
// selling them something broken. End of service and the wage-file layout do
// not: a shop can trade for a year without processing a leaver or filing a wage
// run, and `gate()` refuses those calculations by name where they are made. It
// used to refuse on both, which turned a caution into a wall across a market.
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

// NotifyKindOwnerInvitation is the `notify.send` payload kind for the message
// a new business owner gets.
const NotifyKindOwnerInvitation = "owner_invitation"

// mailStatus is what this deployment will do with the message it just queued.
func (s *Service) mailStatus() string {
	if s.mail == nil {
		return MailNotConfigured
	}
	if s.mailState == "" {
		// Wired without saying what happens to it. Reported as the weakest
		// claim available rather than the most flattering one.
		return MailQueuedNoProvider
	}
	return s.mailState
}

// What a deployment does with a queued message. Reported to the operator with
// the new account, because "we queued it" is not an answer to "did they get
// it".
//
// These are deliberately four different words for four different situations,
// and none of them is "sent". Nothing in this product can honestly say sent
// until a provider is wired and has acknowledged the message; see the file
// comment on jobs/notify.go for why that decision has not been made yet.
const (
	// MailQueuedForLogging: the worker will write the message to its log and
	// mark the job done. Development. Nobody receives anything.
	MailQueuedForLogging = "queued_for_logging"

	// MailQueuedNoProvider: the worker will REFUSE the job, which retries,
	// escalates, and appears in the failed-jobs view. That is the honest
	// outcome for a deployment with no mail provider, and it is visible rather
	// than silent.
	MailQueuedNoProvider = "queued_no_provider"

	// MailQueued: queued into a deployment that has a provider wired. No such
	// deployment exists yet; the constant exists so that wiring one is a
	// one-line change here rather than a rewrite of this vocabulary.
	MailQueued = "queued"

	// MailNotConfigured: nothing was queued, because this process has no queue
	// wired at all. The business was still created.
	MailNotConfigured = "not_configured"
)

// expiryLabel names the absence of an expiry, so a lifetime subscription and
// an unrecorded one do not read identically in the audit trail.
func expiryLabel(expires string) string {
	if expires == "" {
		return "never"
	}
	return expires
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

	// The commercial terms, all optional and all with a defensible default.
	//
	// A tenant used to be created with no `subscription` row at all, and the
	// read path coalesced that absence into a report of an ACTIVE subscription
	// on the tenant's tier -- so the platform stated a commercial relationship
	// that existed nowhere, and nothing could ever find it expired because it
	// had no end date to compare against. The row is written here now, in the
	// same transaction as the tenant, so there is no window in which a business
	// exists without commercial terms.
	Cycle     string
	Price     string
	Currency  string
	StartedOn string
	ExpiresOn string
}

// Provisioned is the result. The temporary password is returned once and never
// stored in readable form.
type Provisioned struct {
	TenantID          uuid.UUID `json:"tenant_id"`
	OwnerUserID       uuid.UUID `json:"owner_user_id"`
	OwnerEmail        string    `json:"owner_email"`
	TemporaryPassword string    `json:"temporary_password"`

	// What the operator has just committed the client to, echoed back so the
	// handover screen can state it rather than the operator having to open the
	// billing screen to find out what they just sold.
	PlanTier  string `json:"plan_tier"`
	Cycle     string `json:"cycle"`
	Price     string `json:"price"`
	Currency  string `json:"currency"`
	StartedOn string `json:"started_on"`
	// ExpiresOn is absent for a lifetime subscription, which is the only kind
	// that genuinely never ends.
	ExpiresOn string `json:"expires_on,omitempty"`

	// LoginURL is where to send the owner. Empty when the deployment has not
	// been told its own address, in which case the screen says so rather than
	// showing a link that goes nowhere.
	LoginURL string `json:"login_url,omitempty"`

	// MailStatus is one of the constants above: what will actually become of
	// the welcome message, never a claim that it was delivered.
	MailStatus string `json:"mail_status"`
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

	// The same rules the platform billing screen goes through, so a client
	// signed up here and a client whose plan is edited later cannot end up
	// with differently-shaped commercial terms.
	dates, err := billing.ResolvePlanDates(billing.NewPlan{
		Cycle:     req.Cycle,
		StartedOn: req.StartedOn,
		ExpiresOn: req.ExpiresOn,
	}, nil, time.Now().UTC())
	if err != nil {
		return Provisioned{}, err
	}

	out := Provisioned{
		OwnerEmail: req.OwnerEmail, TemporaryPassword: tempPassword,
		PlanTier: req.PlanTier, Cycle: req.Cycle,
		Price: req.Price, Currency: req.Currency,
		StartedOn: dates.StartedOn, ExpiresOn: dates.ExpiresOn,
		MailStatus: s.mailStatus(),
	}
	if s.appURL != "" {
		out.LoginURL = s.appURL + "/login"
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// The double-submit guard, and it has to be here rather than in the
		// browser.
		//
		// A disabled button stops the second press; it does not stop a
		// reloaded form, a retried request, a flaky connection, or anybody
		// with curl. Two identical businesses is the expensive mistake to
		// make, because unpicking one means deleting a tenant that a real
		// person may already have signed into.
		//
		// The pair, not the name alone. Two unrelated shops called "Al Noor
		// Bakery" is ordinary and must stay possible. The SAME name owned by
		// the SAME address is a repeat of one request.
		//
		// And note what this is not: a rule that one person owns one business.
		// The same owner across differently-named businesses is supported all
		// the way through -- sign-in asks which one -- and nothing here
		// narrows that.
		var clash bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM tenant t
			  JOIN app_user u ON u.tenant_id = t.id
			  WHERE lower(btrim(t.name)) = lower(btrim($1))
			    AND u.email = $2)`,
			req.Name, req.OwnerEmail).Scan(&clash); err != nil {
			return err
		}
		if clash {
			return errs.New(errs.CodeConflict,
				"A business with that name already exists for that owner. If "+
					"this is a second business for the same person, give it a "+
					"name that tells them apart.")
		}

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

		// The commercial terms, in the same transaction as the business they
		// describe. See the comment on NewTenant's plan fields for what used
		// to happen instead.
		if _, err := tx.Exec(ctx, `
			INSERT INTO subscription
			  (tenant_id, tier, cycle, price, currency, status, started_on,
			   current_period_end)
			VALUES ($1, $2::plan_tier, $3, $4::numeric, $5, 'active',
			        $6::date, nullif($7,'')::date)`,
			out.TenantID, req.PlanTier, out.Cycle, out.Price, out.Currency,
			dates.StartedOn, dates.ExpiresOn); err != nil {
			return err
		}

		// Inside the transaction, so a business that exists and a message that
		// will be sent commit together. A message queued for a tenant whose
		// creation then rolled back would tell somebody they have an account
		// they do not have.
		if s.mail != nil {
			if err := s.mail.QueueNotification(ctx, tx, identity.NotifyPayload{
				Kind:         NotifyKindOwnerInvitation,
				Email:        req.OwnerEmail,
				FullName:     req.OwnerName,
				BusinessName: req.Name,
				LoginURL:     out.LoginURL,
				PlanTier:     req.PlanTier,
				PlanUntil:    expiryLabel(dates.ExpiresOn),
			}); err != nil {
				return err
			}
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
				// The commercial terms belong in the same entry as the
				// business: "who took this client on, on what plan, until
				// when" is one question and reading it from two rows that can
				// be minutes apart is how it gets answered wrongly.
				"cycle": out.Cycle, "price": out.Price,
				"currency": out.Currency, "started_on": out.StartedOn,
				"expires_on": expiryLabel(out.ExpiresOn),
				// Never the password, the hash, or anything derived from
				// either. The owner's address is enough to say who was given
				// an account.
				"owner_email": req.OwnerEmail,
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

	switch r.Cycle {
	case "monthly", "yearly", "lifetime":
	case "":
		r.Cycle = "monthly"
	default:
		v.WithField("cycle",
			"A subscription is billed monthly, yearly, or once.")
		bad = true
	}

	// Zero rather than refused. A client is very often taken on before the
	// price is agreed -- a pilot, a migration, a reseller's account -- and a
	// form that insisted on a number would be answered with a made-up one.
	r.Price = strings.TrimSpace(r.Price)
	if r.Price == "" {
		r.Price = "0"
	} else if p, err := decimal.NewFromString(r.Price); err != nil || p.IsNegative() {
		v.WithField("price", "That price is not an amount.")
		bad = true
	}

	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	switch {
	case r.Currency == "":
		r.Currency = "SAR"
	case len(r.Currency) != 3:
		v.WithField("currency", "Name the currency the client is billed in.")
		bad = true
	}

	// The dates themselves are checked by `billing.ResolvePlanDates`, which is
	// the one place those rules live. Doing it here as well would be two
	// answers to the same question, free to drift apart.

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
