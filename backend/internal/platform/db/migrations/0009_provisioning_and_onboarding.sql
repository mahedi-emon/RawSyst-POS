-- 0009 — Tenant provisioning and the onboarding wizard.
--
-- Blueprint A5: Super Admin creates the tenant and its first Owner login, then
-- a seven-step wizard provisions the working environment. The wizard is
-- designed so a non-technical shop owner completes setup alone, which means it
-- must survive being abandoned halfway and resumed on a different device.

-- ---------------------------------------------------------------------------
-- Plan tier defaults
-- ---------------------------------------------------------------------------

-- Blueprint A3 is blunt about this: the vision is unlimited, the specification
-- is not. "Undefined limits create untestable performance expectations and
-- unbounded storage cost." So every ceiling has a concrete number per tier,
-- centrally adjustable by Super Admin rather than compiled in.
--
-- A tenant's own tenant_limit row is seeded from here at provisioning time and
-- can then be raised individually — a Starter client who needs one more
-- terminal should not require a plan change.
CREATE TABLE plan_tier_default (
  tier             plan_tier PRIMARY KEY,
  max_companies    integer NOT NULL,
  max_stores       integer NOT NULL,
  max_users        integer NOT NULL,
  max_terminals    integer NOT NULL,
  max_skus         integer NOT NULL,
  max_held_carts   integer NOT NULL,
  max_custom_roles integer NOT NULL,
  max_storage_mb   integer NOT NULL,
  sms_credits      integer NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT plan_tier_default_positive CHECK (
    max_companies > 0 AND max_stores > 0 AND max_users > 0 AND
    max_terminals > 0 AND max_skus > 0 AND max_held_carts > 0 AND
    max_custom_roles >= 0 AND max_storage_mb > 0 AND sms_credits >= 0
  )
);

INSERT INTO plan_tier_default
  (tier, max_companies, max_stores, max_users, max_terminals, max_skus,
   max_held_carts, max_custom_roles, max_storage_mb, sms_credits) VALUES
  ('starter',      1,  1,   5,   2,   5000,  10,  2,   2048,   100),
  ('professional', 1,  3,  20,   6,  25000,  20,  8,  10240,  1000),
  ('business',     3, 10,  75,  25, 100000,  40, 25,  51200,  5000),
  -- Enterprise is large but still finite, so capacity planning has a number to
  -- work from and load tests have a target.
  ('enterprise',  25, 60, 500, 200, 500000, 100, 100, 512000, 25000);

-- ---------------------------------------------------------------------------
-- Onboarding progress
-- ---------------------------------------------------------------------------

CREATE TYPE onboarding_step AS ENUM (
  'business_info',     -- 1. company, country, currency, VAT/CR numbers
  'stores',            -- 2. branches
  'tax',               -- 3. Saudi auto-loads VAT and ZATCA from the registry
  'employees',         -- 4. staff and their roles
  'hardware',          -- 5. scanner, printer, drawer
  'opening_balances',  -- 6. opening cash, stock, payables, receivables
  'finished'           -- 7. environment provisioned
);

CREATE TABLE onboarding_progress (
  tenant_id     uuid PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
  current_step  onboarding_step NOT NULL DEFAULT 'business_info',

  -- Per-step payloads, keyed by step name. Held as JSONB rather than columns
  -- because a half-finished step is not yet valid against the real schema —
  -- a store with no name should be resumable, not rejected at save time. Each
  -- step is validated properly when it is committed to its real tables.
  step_data     jsonb NOT NULL DEFAULT '{}'::jsonb,

  completed_steps onboarding_step[] NOT NULL DEFAULT '{}',
  started_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER onboarding_progress_touch BEFORE UPDATE ON onboarding_progress
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE onboarding_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_progress FORCE  ROW LEVEL SECURITY;
-- Super Admin can see onboarding state: a client stuck at step 3 is a support
-- call waiting to happen, and blueprint A4 puts provisioning on the platform
-- plane. It carries no business data — only setup answers.
CREATE POLICY onboarding_progress_isolation ON onboarding_progress
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- ---------------------------------------------------------------------------
-- Terminal / station settings (blueprint I5)
-- ---------------------------------------------------------------------------

CREATE TABLE terminal_setting (
  device_id            uuid PRIMARY KEY REFERENCES device(id) ON DELETE CASCADE,
  tenant_id            uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  default_warehouse_id uuid,
  receipt_template     text,
  printer_name         text,
  drawer_enabled       boolean NOT NULL DEFAULT true,
  scanner_prefix       text,

  -- Blueprint I5 and B7: a jeweller wants the customer recorded on every sale;
  -- a grocery does not. Forcing either is wrong.
  require_customer     boolean NOT NULL DEFAULT false,

  -- Bounded by the tenant's plan ceiling, checked in the application where the
  -- plan is known. A held cart is memory on the terminal, so "unlimited" here
  -- is a real resource question rather than a philosophical one.
  max_held_carts       integer NOT NULL DEFAULT 10,

  default_discount_rule text,
  updated_at           timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT terminal_setting_held_carts_positive CHECK (max_held_carts > 0)
);

CREATE TRIGGER terminal_setting_touch BEFORE UPDATE ON terminal_setting
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE terminal_setting ENABLE ROW LEVEL SECURITY;
ALTER TABLE terminal_setting FORCE  ROW LEVEL SECURITY;
CREATE POLICY terminal_setting_isolation ON terminal_setting
  USING (tenant_id = current_tenant_id());
