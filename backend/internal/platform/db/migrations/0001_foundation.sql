-- 0001 — Foundation: extensions, enums and the guard functions that the rest
-- of the schema depends on.
--
-- Two ideas here carry most of the weight of the whole system:
--
--   1. reject_always() — the mechanism behind immutability. Blueprint A2 #7,
--      C9.1, C10 and D4 each independently demand that something can never be
--      edited or deleted "by anyone, including Owner and Super Admin". An
--      application-layer check is a convention; a trigger is a guarantee.
--
--   2. current_tenant_id() — the mechanism behind row-level security. Every
--      tenant-scoped policy calls it. It is STABLE rather than IMMUTABLE so
--      the planner may cache it per statement but never across statements.

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid, digest
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive email
CREATE EXTENSION IF NOT EXISTS btree_gist; -- exclusion constraints over ranges

-- ---------------------------------------------------------------------------
-- Tenant context
-- ---------------------------------------------------------------------------

-- Returns the tenant bound to the current transaction, or NULL when none is
-- set. NULL is the safe default: a policy comparing tenant_id = NULL matches
-- no rows, so an unscoped connection sees nothing rather than everything.
CREATE OR REPLACE FUNCTION current_tenant_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

-- ---------------------------------------------------------------------------
-- Immutability guards
-- ---------------------------------------------------------------------------

-- Blocks every UPDATE and DELETE on a table. Used for audit records and
-- posted journal lines, where history must be append-only.
CREATE OR REPLACE FUNCTION reject_always() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION
    'Records in % are immutable and cannot be modified or deleted.', TG_TABLE_NAME
    USING ERRCODE = 'P0001';
END;
$$;

-- Blocks DELETE only, permitting UPDATE. Used where a row must survive but may
-- legitimately change state — a ZATCA invoice moving from SUBMITTED to
-- REPORTED, for instance.
CREATE OR REPLACE FUNCTION reject_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION
    'Records in % cannot be deleted.', TG_TABLE_NAME
    USING ERRCODE = 'P0001';
END;
$$;

-- Freezes a named set of columns while allowing the rest to change. This is
-- how a ZATCA invoice keeps its financial content and chain fields fixed at
-- signing time while its submission status continues to advance.
--
-- Pass the frozen column names as trigger arguments.
CREATE OR REPLACE FUNCTION reject_column_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  col  text;
  before_val text;
  after_val  text;
BEGIN
  FOREACH col IN ARRAY TG_ARGV LOOP
    EXECUTE format('SELECT ($1).%I::text', col) INTO before_val USING OLD;
    EXECUTE format('SELECT ($1).%I::text', col) INTO after_val  USING NEW;
    IF before_val IS DISTINCT FROM after_val THEN
      RAISE EXCEPTION
        'Column %.% is immutable once written and cannot be modified.',
        TG_TABLE_NAME, col
        USING ERRCODE = 'P0001';
    END IF;
  END LOOP;
  RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- Housekeeping
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- Shared enums
-- ---------------------------------------------------------------------------

-- Per-tenant data region. Blueprint E4.2 requires a tenant's data to live in a
-- specific region rather than one global database, because Saudi rules place
-- conditions on transferring personal data outside the Kingdom. v1 deploys the
-- 'sa' region only; the column exists from day one so adding a region is a
-- deployment exercise rather than a schema change.
CREATE TYPE data_region AS ENUM ('sa', 'eu', 'asia', 'other');

CREATE TYPE plan_tier AS ENUM ('starter', 'professional', 'business', 'enterprise');

CREATE TYPE tenant_status AS ENUM ('active', 'suspended', 'deactivated');

CREATE TYPE user_status AS ENUM ('invited', 'active', 'suspended', 'disabled');

-- ZATCA onboarding progresses compliance CSID -> production CSID -> live.
-- Blueprint E1.0: obligation is per-taxpayer and notification-driven. The
-- software captures what ZATCA told the taxpayer; it never infers it.
CREATE TYPE zatca_onboarding_status AS ENUM (
  'not_started',      -- not obligated, or not yet begun
  'compliance_csid',  -- compliance CSID obtained, in ZATCA's test phase
  'production_csid',  -- production CSID issued
  'live'              -- submitting real invoices
);

-- What the POS does when a B2B standard tax invoice is raised offline.
-- A standard invoice has no legal standing for the buyer until ZATCA clears
-- it, so it can never simply print. Blueprint E1.3 RULE 2 fixes these three
-- options; 'block' is the documented default because it is the safest.
CREATE TYPE b2b_offline_policy AS ENUM (
  'block',               -- refuse to finalize while offline
  'draft_hold',          -- save as draft, release goods on a delivery note
  'convert_simplified'   -- offer one-tap conversion to a simplified invoice
);

CREATE TYPE negative_stock_policy AS ENUM ('block', 'allow_warn');

-- Blueprint C13. Standard Cost is in scope: C13 is the authoritative section
-- and supersedes the two-method lists in B4 and D1.
CREATE TYPE costing_method AS ENUM ('wac', 'fifo', 'standard');
