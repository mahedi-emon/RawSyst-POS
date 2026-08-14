-- 0002 — Tenancy: group -> company -> store -> terminal, with row-level
-- security enabled from the first table.
--
-- The hierarchy resolves two requirements that pull against each other:
--
--   * Blueprint F4 wants one Owner login spanning several legal companies,
--     with consolidated group reporting and inter-company elimination.
--   * Blueprint E1/E2 require books, VAT registration and the ZATCA invoice
--     sequence to belong to a SINGLE legal entity, never to a group.
--
-- So `tenant` is an access and reporting construct, `company` is the
-- accounting and compliance boundary, and `device` owns the ZATCA chain.

-- ---------------------------------------------------------------------------
-- Tenant (the group)
-- ---------------------------------------------------------------------------

CREATE TABLE tenant (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name         text NOT NULL,
  data_region  data_region   NOT NULL DEFAULT 'sa',
  plan_tier    plan_tier     NOT NULL DEFAULT 'starter',
  status       tenant_status NOT NULL DEFAULT 'active',
  created_at   timestamptz   NOT NULL DEFAULT now(),
  updated_at   timestamptz   NOT NULL DEFAULT now(),
  CONSTRAINT tenant_name_not_blank CHECK (length(btrim(name)) > 0)
);

CREATE TRIGGER tenant_touch BEFORE UPDATE ON tenant
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- The tenant table is the one place tenant_id is the primary key rather than a
-- foreign key, so its policy compares id instead.
ALTER TABLE tenant ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant
  USING (id = current_tenant_id());

-- Plan ceilings. Blueprint A3 is explicit that "unlimited" is a marketing
-- word, not a specification: every limit needs a concrete default and maximum
-- because undefined limits create untestable performance expectations and
-- unbounded storage cost. Defaults live here per tier and may be raised for an
-- individual tenant by Super Admin.
CREATE TABLE tenant_limit (
  tenant_id       uuid PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
  max_companies   integer NOT NULL DEFAULT 1,
  max_stores      integer NOT NULL DEFAULT 1,
  max_users       integer NOT NULL DEFAULT 5,
  max_terminals   integer NOT NULL DEFAULT 2,
  max_skus        integer NOT NULL DEFAULT 5000,
  max_held_carts  integer NOT NULL DEFAULT 10,
  max_custom_roles integer NOT NULL DEFAULT 2,
  max_storage_mb  integer NOT NULL DEFAULT 2048,
  sms_credits     integer NOT NULL DEFAULT 100,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT tenant_limit_positive CHECK (
    max_companies > 0 AND max_stores > 0 AND max_users > 0 AND
    max_terminals > 0 AND max_skus > 0 AND max_held_carts > 0 AND
    max_custom_roles >= 0 AND max_storage_mb > 0 AND sms_credits >= 0
  )
);

ALTER TABLE tenant_limit ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_limit FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_limit_isolation ON tenant_limit
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Company (the legal entity — books, VAT registration, ZATCA sequence)
-- ---------------------------------------------------------------------------

CREATE TABLE company (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  legal_name    text NOT NULL,
  legal_name_ar text,                       -- Law of Commercial Books expects
                                            -- records to be available in Arabic
  trade_name    text,
  country       char(2) NOT NULL,
  base_currency char(3) NOT NULL,
  timezone      text NOT NULL DEFAULT 'Asia/Riyadh',

  cr_number     text,                       -- Commercial Registration

  -- Blueprint E2.1: the VAT number is a validated, required field for any
  -- VAT-registered tenant. The CHECK makes "registered without a number"
  -- unrepresentable rather than merely discouraged.
  vat_registered boolean NOT NULL DEFAULT false,
  vat_number     text,

  -- ZATCA obligation, captured from the taxpayer's OWN notification.
  -- Blueprint E1.0 is emphatic that the software must never assume or assert
  -- a tenant's wave: it comes from ZATCA to the taxpayer directly.
  zatca_wave     text,
  zatca_deadline date,
  zatca_status   zatca_onboarding_status NOT NULL DEFAULT 'not_started',

  -- Operating policies (blueprint E1.3 RULE 2, C13)
  b2b_offline_policy    b2b_offline_policy    NOT NULL DEFAULT 'block',
  negative_stock_policy negative_stock_policy NOT NULL DEFAULT 'block',
  costing_method        costing_method        NOT NULL DEFAULT 'wac',

  fiscal_year_start_month smallint NOT NULL DEFAULT 1,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT company_vat_number_required_when_registered
    CHECK (NOT vat_registered OR (vat_number IS NOT NULL AND length(btrim(vat_number)) > 0)),
  CONSTRAINT company_fiscal_month_valid
    CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
  CONSTRAINT company_country_lower CHECK (country = lower(country)),
  CONSTRAINT company_currency_upper CHECK (base_currency = upper(base_currency))
);

CREATE INDEX company_tenant_idx ON company (tenant_id);
CREATE UNIQUE INDEX company_vat_number_uq
  ON company (country, vat_number) WHERE vat_number IS NOT NULL;

CREATE TRIGGER company_touch BEFORE UPDATE ON company
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE company ENABLE ROW LEVEL SECURITY;
ALTER TABLE company FORCE  ROW LEVEL SECURITY;
CREATE POLICY company_isolation ON company
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Store (branch / showroom)
-- ---------------------------------------------------------------------------

CREATE TABLE store (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,                -- used in document numbering: INV-RYD-000001
  name        text NOT NULL,
  name_ar     text,
  address     text,
  phone       text,
  is_active   boolean NOT NULL DEFAULT true,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT store_code_format CHECK (code ~ '^[A-Z0-9-]{1,12}$')
);

CREATE UNIQUE INDEX store_code_uq ON store (company_id, code);
CREATE INDEX store_tenant_idx ON store (tenant_id);

CREATE TRIGGER store_touch BEFORE UPDATE ON store
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE store ENABLE ROW LEVEL SECURITY;
ALTER TABLE store FORCE  ROW LEVEL SECURITY;
CREATE POLICY store_isolation ON store
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Device (POS terminal — owns a ZATCA chain)
-- ---------------------------------------------------------------------------

CREATE TYPE device_status AS ENUM ('pending', 'active', 'inactive', 'revoked');

CREATE TABLE device (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id     uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  store_id       uuid NOT NULL REFERENCES store(id)   ON DELETE RESTRICT,

  terminal_label text NOT NULL,
  os             text,
  app_version    text,

  status         device_status NOT NULL DEFAULT 'pending',

  -- ZATCA cryptographic identity. Only the SERIAL is stored here; the private
  -- key never reaches the server. Blueprint E1.3 RULE 1 requires signing to
  -- happen on the terminal, and H1 forbids plain key storage, so the key lives
  -- in the terminal's OS keystore (Windows DPAPI) and is never transmitted.
  csid_serial     text,
  csid_issued_at  timestamptz,
  csid_expires_at timestamptz,

  last_sync_at   timestamptz,
  last_active_at timestamptz,
  printer_config jsonb NOT NULL DEFAULT '{}'::jsonb,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX device_label_uq ON device (store_id, terminal_label);
CREATE INDEX device_tenant_idx  ON device (tenant_id);
CREATE INDEX device_company_idx ON device (company_id);

CREATE TRIGGER device_touch BEFORE UPDATE ON device
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- A device may be deactivated or revoked, never deleted: its ZATCA chain is a
-- legal record that must remain retrievable for the full retention period
-- (blueprint E2.4, at least six years).
CREATE TRIGGER device_no_delete BEFORE DELETE ON device
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE device ENABLE ROW LEVEL SECURITY;
ALTER TABLE device FORCE  ROW LEVEL SECURITY;
CREATE POLICY device_isolation ON device
  USING (tenant_id = current_tenant_id());
