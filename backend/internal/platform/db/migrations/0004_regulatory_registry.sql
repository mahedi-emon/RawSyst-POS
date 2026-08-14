-- 0004 — Regulatory Rule Registry.
--
-- Blueprint E8, verbatim: "No legal figure, deadline, threshold, rate, or file
-- format may be hard-coded anywhere in the codebase."
--
-- Two reasons, and the second matters more in an audit than the first:
--
--   1. A rate change becomes a configuration update rather than an emergency
--      release across every tenant.
--
--   2. Historical accuracy. "A VAT report for March must use March's rate,
--      even if the rate changed in June. NEVER apply the current value
--      retroactively." A system storing only the current value cannot produce
--      a defensible prior-period report — that is an audit failure, not a
--      missing feature.
--
-- This migration runs before any tax, invoicing or payroll table, because
-- building those first is how legal values end up hard-coded.

CREATE TYPE regulatory_authority AS ENUM (
  -- Tier 1 sources only. Blueprint Part N: "No compliance feature may be
  -- implemented from Tier 2 alone." Constraining the enum means a consulting
  -- firm's summary cannot be recorded as justification even by mistake.
  'zatca',   -- e-invoicing, VAT
  'sdaia',   -- PDPL, data transfer
  'gosi',    -- social insurance
  'mhrsd',   -- labour, WPS via Mudad
  'qiwa',    -- employment contracts, Nitaqat
  'sama',    -- licensed payment service providers
  'moc',     -- commerce, e-commerce law
  'socpa'    -- accounting standards
);

CREATE TABLE regulatory_rule (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  rule_key         text    NOT NULL,   -- 'SA.VAT.STANDARD_RATE'
  country          char(2) NOT NULL,

  -- JSONB because rules are heterogeneous: a VAT rate is a number, a GOSI
  -- schedule is a matrix, a Mudad wage-file spec is a structured document. A
  -- typed column per rule shape would mean a migration per regulation.
  payload          jsonb   NOT NULL,

  effective_from   date NOT NULL,
  effective_to     date,               -- NULL = currently in force

  source_authority regulatory_authority NOT NULL,
  source_document  text NOT NULL,      -- official document name + version/date
  source_url       text,

  -- NULL means NEVER VERIFIED against the Tier 1 source. Such a rule is usable
  -- in development but fails the release gate. Blueprint: "Do not let
  -- developers fill these in from assumption."
  verified_on      date,
  verified_by      uuid REFERENCES app_user(id),

  -- Marks a rule that blocks release until verified. Blueprint E8.4 names
  -- three: the Mudad wage-file format, the GOSI dated rate schedule, and the
  -- ZATCA XML/QR schema version.
  release_blocker  boolean NOT NULL DEFAULT false,

  notes            text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid REFERENCES app_user(id),

  CONSTRAINT regulatory_rule_key_format
    CHECK (rule_key ~ '^[A-Z]{2}\.[A-Z0-9_]+\.[A-Z0-9_]+$'),
  CONSTRAINT regulatory_rule_period_ordered
    CHECK (effective_to IS NULL OR effective_to > effective_from),
  CONSTRAINT regulatory_rule_country_lower
    CHECK (country = lower(country))
);

-- The load-bearing constraint: PostgreSQL refuses to store two rows for the
-- same rule whose date ranges overlap. Ambiguity about which rate applied on a
-- given date becomes unrepresentable rather than a bug to be found later.
ALTER TABLE regulatory_rule
  ADD CONSTRAINT regulatory_rule_no_overlap
  EXCLUDE USING gist (
    rule_key WITH =,
    country  WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&
  );

CREATE INDEX regulatory_rule_lookup_idx
  ON regulatory_rule (country, rule_key, effective_from DESC);
CREATE INDEX regulatory_rule_unverified_idx
  ON regulatory_rule (verified_on) WHERE verified_on IS NULL;

-- Registry rows are platform-wide, not tenant-scoped: the VAT rate in Saudi
-- Arabia is the same fact for every tenant. Write access is restricted to
-- Super Admin in the authorization layer (blueprint E8.3).
--
-- History is append-only. Correcting a rule means closing the old row's
-- effective_to and inserting a new one, so the registry can always answer
-- "what did we believe the law was, on this date, when we computed that
-- report?" — which is the question an auditor actually asks.
CREATE TRIGGER regulatory_rule_no_delete
  BEFORE DELETE ON regulatory_rule
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER regulatory_rule_frozen_fields
  BEFORE UPDATE ON regulatory_rule
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'rule_key', 'country', 'payload', 'effective_from', 'source_authority');

-- ---------------------------------------------------------------------------
-- Per-tenant override (blueprint E8.3 — rare, always justified)
-- ---------------------------------------------------------------------------

CREATE TABLE regulatory_rule_override (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id     uuid REFERENCES company(id) ON DELETE CASCADE,

  rule_key       text    NOT NULL,
  country        char(2) NOT NULL,
  payload        jsonb   NOT NULL,
  effective_from date    NOT NULL,
  effective_to   date,

  -- NOT NULL by design: an override without a written reason is exactly what
  -- E8.3 forbids. Example legitimate case: a tenant with a ZATCA-approved
  -- special arrangement.
  justification  text NOT NULL,
  approved_by    uuid NOT NULL REFERENCES app_user(id),

  created_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT rro_justification_substantive CHECK (length(btrim(justification)) >= 20),
  CONSTRAINT rro_period_ordered CHECK (effective_to IS NULL OR effective_to > effective_from)
);

ALTER TABLE regulatory_rule_override
  ADD CONSTRAINT regulatory_rule_override_no_overlap
  EXCLUDE USING gist (
    tenant_id WITH =,
    rule_key  WITH =,
    country   WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&
  );

CREATE INDEX rro_lookup_idx ON regulatory_rule_override (tenant_id, rule_key, country);

CREATE TRIGGER rro_no_delete BEFORE DELETE ON regulatory_rule_override
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE regulatory_rule_override ENABLE ROW LEVEL SECURITY;
ALTER TABLE regulatory_rule_override FORCE  ROW LEVEL SECURITY;
CREATE POLICY rro_isolation ON regulatory_rule_override
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Resolution
-- ---------------------------------------------------------------------------

-- Resolves a rule as it stood on a given date, preferring a tenant override.
--
-- as_of is a required argument with no default. There is deliberately no
-- "current value" convenience function: its existence would guarantee that
-- someone eventually calls it from inside a historical report, which is the
-- precise failure E8.1 warns about.
CREATE OR REPLACE FUNCTION resolve_regulatory_rule(
  p_rule_key  text,
  p_country   char(2),
  p_as_of     date,
  p_tenant_id uuid DEFAULT NULL
) RETURNS TABLE (
  payload      jsonb,
  verified_on  date,
  source_doc   text,
  is_override  boolean
)
LANGUAGE sql STABLE AS $$
  SELECT o.payload, NULL::date, 'tenant override'::text, true
  FROM regulatory_rule_override o
  WHERE p_tenant_id IS NOT NULL
    AND o.tenant_id = p_tenant_id
    AND o.rule_key  = p_rule_key
    AND o.country   = p_country
    AND daterange(o.effective_from, o.effective_to, '[)') @> p_as_of
  UNION ALL
  SELECT r.payload, r.verified_on, r.source_document, false
  FROM regulatory_rule r
  WHERE r.rule_key = p_rule_key
    AND r.country  = p_country
    AND daterange(r.effective_from, r.effective_to, '[)') @> p_as_of
    AND NOT EXISTS (
      SELECT 1 FROM regulatory_rule_override o2
      WHERE p_tenant_id IS NOT NULL
        AND o2.tenant_id = p_tenant_id
        AND o2.rule_key  = p_rule_key
        AND o2.country   = p_country
        AND daterange(o2.effective_from, o2.effective_to, '[)') @> p_as_of
    )
  LIMIT 1
$$;
