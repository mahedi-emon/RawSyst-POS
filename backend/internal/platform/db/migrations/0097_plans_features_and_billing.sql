-- 0097 — Plan entitlements, per-tenant feature flags, and subscription billing
--        (blueprint H5).
--
-- # What already exists, and what was missing
--
-- 0001 gave the platform a `plan_tier` enum and 0002 gave every tenant a
-- `tenant_limit` row with concrete ceilings, and those ceilings are enforced —
-- provisioning refuses a fourth company to a tenant allowed three. So the
-- COUNTABLE half of H5 has worked from the beginning.
--
-- What did not exist is the half H5 says is the commercially important one:
--
--   "Feature Flags per tenant: Super Admin toggles individual modules on/off
--    per client independent of a fixed plan (e.g., a Starter client who wants
--    Warranty Management added individually) — this is what makes the platform
--    commercially flexible."
--
-- and the subscription itself: a cycle, a price, an invoice to the tenant, and
-- what happens when nobody pays.
--
-- # A flag is resolved, not stored
--
-- Two tables rather than one. `plan_feature` says what a TIER includes and is
-- platform-wide; `tenant_feature` is one tenant's deliberate exception, on or
-- off, with a reason and an optional expiry. What a tenant may use is the plan
-- with their exceptions applied — resolved at read time.
--
-- The alternative, copying the plan's features onto each tenant at signup, is
-- the design that cannot answer "we are adding Wholesale to Professional next
-- month": it would need a backfill across every tenant, and every tenant who
-- had ever been given a manual exception would be silently overwritten by it.
--
-- # Suspension is a tenant status, not a new mechanism
--
-- `tenant.status` already has 'suspended', and the sign-in path already refuses
-- a tenant that is not active. Dunning therefore has nothing to invent: it moves
-- a tenant to suspended after the configured grace period and back to active on
-- payment. A separate "billing_locked" flag would be a second way to be locked
-- out and a second thing to check on every request.
--
-- # The platform's own invoices are not the tenant's invoices
--
-- `subscription_invoice` is the platform billing a client for the software. It
-- is deliberately NOT `sales_invoice` and carries no ZATCA anything: the tenant
-- is the customer here, the platform is the seller, and mixing the two would put
-- the platform's revenue inside a tenant's books and inside their VAT return.

-- ---------------------------------------------------------------------------
-- What a plan includes
-- ---------------------------------------------------------------------------

-- The modules H5 names as gateable, plus the ones the product has grown since.
-- A CHECK rather than an enum: adding a module should be an INSERT, and the
-- constraint is here to catch a typo rather than to enumerate the roadmap.
CREATE TABLE plan_feature (
  tier         plan_tier NOT NULL,
  feature      text      NOT NULL,
  included     boolean   NOT NULL DEFAULT true,
  PRIMARY KEY (tier, feature),
  CONSTRAINT plan_feature_name_not_blank CHECK (btrim(feature) <> '')
);

-- Platform-wide reference data, the same for every tenant, and readable by all
-- of them: a client comparing plans is reading this.
ALTER TABLE plan_feature ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_feature FORCE  ROW LEVEL SECURITY;
CREATE POLICY plan_feature_readable ON plan_feature USING (true);

-- One tenant's deliberate exception to their plan.
CREATE TABLE tenant_feature (
  tenant_id  uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  feature    text NOT NULL,

  -- TRUE grants something the plan does not include; FALSE withdraws something
  -- it does. Both directions are needed: H5's worked example is the first, and
  -- the second is how a module is turned off for a client who abused it or
  -- asked for it to be hidden.
  enabled    boolean NOT NULL,

  -- NOT NULL. An exception without a reason is indistinguishable from a
  -- mistake six months later, and this is the table where a support
  -- conversation becomes a commercial fact.
  reason     text NOT NULL,

  -- A trial. NULL means permanent; a date means the exception lapses and the
  -- plan's own answer resumes without anybody having to remember.
  expires_on date,

  granted_at timestamptz NOT NULL DEFAULT now(),
  granted_by uuid REFERENCES app_user(id) ON DELETE SET NULL,

  PRIMARY KEY (tenant_id, feature),
  CONSTRAINT tenant_feature_reason_not_blank CHECK (btrim(reason) <> '')
);

ALTER TABLE tenant_feature ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_feature FORCE  ROW LEVEL SECURITY;
-- A tenant may see what they have been granted; the platform sees all of them,
-- because the Super Admin screen is the only place these are set.
CREATE POLICY tenant_feature_isolation ON tenant_feature
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- The subscription
-- ---------------------------------------------------------------------------

CREATE TABLE subscription (
  tenant_id    uuid PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,

  tier         plan_tier NOT NULL DEFAULT 'starter',
  -- monthly, yearly, lifetime. H5's three, named as it names them.
  cycle        text NOT NULL DEFAULT 'monthly',

  -- What the platform charges, in the platform's own currency. Not the
  -- tenant's base currency: a Bangladeshi shop billed in taka and a Saudi one
  -- billed in riyals are two prices, and the one recorded here is the one
  -- agreed.
  price        numeric(18,4) NOT NULL DEFAULT 0,
  currency     char(3) NOT NULL DEFAULT 'SAR',

  -- trialing, active, past_due, suspended, cancelled. Distinct from
  -- tenant.status, which is what the sign-in path reads: this is the
  -- COMMERCIAL state, and suspension is the consequence it has.
  status       text NOT NULL DEFAULT 'trialing',

  started_on   date NOT NULL DEFAULT current_date,
  trial_ends_on date,
  -- The end of the period currently paid for. A lifetime subscription has
  -- none, which is what makes it a lifetime subscription.
  current_period_end date,
  cancelled_on date,

  -- How long after a missed payment before the tenant is suspended.
  -- Configurable per H5, and per tenant rather than per tier because it is
  -- almost always a concession made to one client.
  grace_days   smallint NOT NULL DEFAULT 14,

  note         text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT subscription_cycle_valid CHECK (cycle IN (
    'monthly', 'yearly', 'lifetime')),
  CONSTRAINT subscription_status_valid CHECK (status IN (
    'trialing', 'active', 'past_due', 'suspended', 'cancelled')),
  CONSTRAINT subscription_price_sane CHECK (price >= 0),
  CONSTRAINT subscription_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT subscription_grace_sane CHECK (grace_days BETWEEN 0 AND 180),
  -- A lifetime subscription has no renewal date; anything else does once it is
  -- past its trial.
  CONSTRAINT subscription_lifetime_has_no_period_end
    CHECK (cycle <> 'lifetime' OR current_period_end IS NULL)
);

CREATE TRIGGER subscription_touch BEFORE UPDATE ON subscription
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE INDEX subscription_due_idx ON subscription (current_period_end)
  WHERE status IN ('active', 'past_due');

ALTER TABLE subscription ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription FORCE  ROW LEVEL SECURITY;
CREATE POLICY subscription_isolation ON subscription
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

-- What the platform billed a tenant, and whether it was paid.
CREATE TABLE subscription_invoice (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  invoice_no   text NOT NULL,
  period_start date NOT NULL,
  period_end   date NOT NULL,

  amount       numeric(18,4) NOT NULL,
  currency     char(3) NOT NULL,

  -- issued, paid, void. Deliberately not 'overdue': overdue is a fact about
  -- the date and the payment, computed rather than stored, so a row cannot
  -- drift into disagreeing with the calendar.
  status       text NOT NULL DEFAULT 'issued',

  issued_on    date NOT NULL DEFAULT current_date,
  due_on       date NOT NULL,
  paid_at      timestamptz,
  payment_ref  text,
  note         text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT subscription_invoice_status_valid CHECK (status IN (
    'issued', 'paid', 'void')),
  CONSTRAINT subscription_invoice_amount_sane CHECK (amount >= 0),
  CONSTRAINT subscription_invoice_period_ordered
    CHECK (period_end >= period_start),
  CONSTRAINT subscription_invoice_paid_is_stamped
    CHECK ((status = 'paid') = (paid_at IS NOT NULL)),
  CONSTRAINT subscription_invoice_currency_upper
    CHECK (currency = upper(currency))
);

CREATE UNIQUE INDEX subscription_invoice_no_uq
  ON subscription_invoice (invoice_no);
CREATE INDEX subscription_invoice_tenant_idx
  ON subscription_invoice (tenant_id, issued_on DESC);
CREATE INDEX subscription_invoice_unpaid_idx
  ON subscription_invoice (due_on) WHERE status = 'issued';

CREATE TRIGGER subscription_invoice_touch
  BEFORE UPDATE ON subscription_invoice
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE subscription_invoice ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscription_invoice FORCE  ROW LEVEL SECURITY;
-- A tenant can see what they were billed. They have to be able to: H5 puts
-- invoicing to tenants in the product, and a bill nobody can read is a bill
-- nobody pays.
CREATE POLICY subscription_invoice_isolation ON subscription_invoice
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

CREATE SEQUENCE IF NOT EXISTS subscription_invoice_seq START 1000;

-- ---------------------------------------------------------------------------
-- What each tier includes
-- ---------------------------------------------------------------------------

-- Every gateable module, per tier. Read as a matrix: Starter is a single shop
-- selling over a counter, Professional adds the things a growing shop needs,
-- Business adds the ones a chain needs, and Enterprise is everything.
--
-- The modules NOT listed here are deliberately ungateable: sales, stock,
-- purchasing, accounting, customers, VAT, the till itself. A plan that could
-- turn off double-entry accounting would be selling a broken product.
INSERT INTO plan_feature (tier, feature, included) VALUES
  -- Starter
  ('starter',      'promotions',    true),
  ('starter',      'loyalty',       false),
  ('starter',      'wholesale',     false),
  ('starter',      'online_orders', false),
  ('starter',      'installments',  false),
  ('starter',      'warranty',      false),
  ('starter',      'payroll',       false),
  ('starter',      'assets',        false),
  ('starter',      'analytics',     false),
  ('starter',      'approvals',     false),
  ('starter',      'api_access',    false),
  ('starter',      'webhooks',      false),
  ('starter',      'multi_company', false),
  ('starter',      'consolidation', false),
  ('starter',      'einvoicing',    true),
  ('starter',      'label_studio',  true),

  -- Professional
  ('professional', 'promotions',    true),
  ('professional', 'loyalty',       true),
  ('professional', 'wholesale',     false),
  ('professional', 'online_orders', true),
  ('professional', 'installments',  true),
  ('professional', 'warranty',      true),
  ('professional', 'payroll',       false),
  ('professional', 'assets',        true),
  ('professional', 'analytics',     true),
  ('professional', 'approvals',     true),
  ('professional', 'api_access',    false),
  ('professional', 'webhooks',      false),
  ('professional', 'multi_company', false),
  ('professional', 'consolidation', false),
  ('professional', 'einvoicing',    true),
  ('professional', 'label_studio',  true),

  -- Business
  ('business',     'promotions',    true),
  ('business',     'loyalty',       true),
  ('business',     'wholesale',     true),
  ('business',     'online_orders', true),
  ('business',     'installments',  true),
  ('business',     'warranty',      true),
  ('business',     'payroll',       true),
  ('business',     'assets',        true),
  ('business',     'analytics',     true),
  ('business',     'approvals',     true),
  ('business',     'api_access',    true),
  ('business',     'webhooks',      true),
  ('business',     'multi_company', true),
  ('business',     'consolidation', false),
  ('business',     'einvoicing',    true),
  ('business',     'label_studio',  true),

  -- Enterprise
  ('enterprise',   'promotions',    true),
  ('enterprise',   'loyalty',       true),
  ('enterprise',   'wholesale',     true),
  ('enterprise',   'online_orders', true),
  ('enterprise',   'installments',  true),
  ('enterprise',   'warranty',      true),
  ('enterprise',   'payroll',       true),
  ('enterprise',   'assets',        true),
  ('enterprise',   'analytics',     true),
  ('enterprise',   'approvals',     true),
  ('enterprise',   'api_access',    true),
  ('enterprise',   'webhooks',      true),
  ('enterprise',   'multi_company', true),
  ('enterprise',   'consolidation', true),
  ('enterprise',   'einvoicing',    true),
  ('enterprise',   'label_studio',  true)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Existing tenants
-- ---------------------------------------------------------------------------

-- Every tenant that already exists gets a subscription row matching the tier
-- they are already on, marked active with no price. A price of zero is honest:
-- this migration does not know what anybody agreed to pay, and inventing a
-- figure would put a number on a screen that somebody might invoice from.
--
-- Every existing tenant is also granted every feature their tier does NOT
-- include, as a permanent exception with the reason stated. That is the only
-- safe migration: a shop using Warranty Management today on a Starter plan must
-- not lose it because a flag table appeared, and the exception makes the grant
-- visible on the Super Admin screen where it can be reviewed and priced.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id, plan_tier FROM tenant LOOP
    INSERT INTO subscription (tenant_id, tier, status)
    VALUES (t.id, t.plan_tier, 'active')
    ON CONFLICT DO NOTHING;

    INSERT INTO tenant_feature (tenant_id, feature, enabled, reason)
    SELECT t.id, f.feature, true,
           'Granted on migration: this tenant was using the product before '
           'plan feature flags existed and must not lose a module because a '
           'table appeared.'
    FROM plan_feature f
    WHERE f.tier = t.plan_tier AND NOT f.included
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Only reading. Setting a plan, a price or a flag is the platform owner's, and
-- those routes carry AccessSuperAdmin rather than a permission a tenant could
-- ever be granted — a tenant who could edit their own entitlements would be a
-- tenant on the Enterprise plan.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',      'subscription.view'),
  ('accountant', 'subscription.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',      'subscription.view'),
      ('accountant', 'subscription.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
