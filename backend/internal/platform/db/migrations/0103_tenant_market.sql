-- 0103 — The market a tenant was sold into, chosen by the platform operator.
--
-- # Why this is not just `company.country`
--
-- `company.country` already decides tax rules: the sale service resolves a VAT
-- rate from it at the date of every sale. That column stays exactly as it is
-- and stays authoritative for tax.
--
-- What it cannot do is answer the question the platform operator asks at the
-- moment of provisioning: *which market am I selling this account into?* Today
-- that answer only appears later, when the Business Owner reaches onboarding
-- step `business_info` and picks a country for themselves. Between provisioning
-- and that step the account has no market at all, so the operator cannot filter
-- the tenant list by market, cannot price by it, and cannot tell a client who
-- rings up "we bought the Bangladesh product" from one who did not.
--
-- So the market is recorded where the decision is made — on the tenant, by the
-- Super Admin — and the company's country is constrained to it during
-- onboarding. Two columns because they answer two questions: the operator's
-- commercial one, and the tax engine's legal one.
--
-- # The value set is the one the product actually serves
--
-- `sa`, `bd`, `us` — the same three as `supportedCountries` in
-- `provisioning/onboarding.go`, and for the same reason stated there: a country
-- with no rules in the regulatory register produces an owner who finishes setup
-- and then cannot ring up a sale. A CHECK rather than an enum, because adding a
-- fourth market should be one ALTER and not a type rewrite that every dependent
-- column has to be talked through.
--
-- # Existing tenants are backfilled from what they already told us
--
-- 0032 and 0033 added columns and left existing tenants behind, and that had to
-- be repaired afterwards. Not repeating it: a tenant that already has a company
-- has already stated its country, so that answer is carried up rather than
-- overwritten with a default that would be wrong for every Bangladeshi client
-- already on the system.

ALTER TABLE tenant ADD COLUMN market text;

-- # The backfill has to be able to SEE the rows it is backfilling
--
-- `tenant` and `company` are both ENABLE + FORCE row-level security. The
-- migration connection is the app role with no tenant context and no
-- `app.platform_admin`, so every row of both tables is invisible to it: the
-- UPDATE below matches nothing, silently, and the `SET NOT NULL` that follows
-- is what finally reports it — as a null-values error that says nothing about
-- RLS. (It did exactly that on the first run of this migration.)
--
-- Setting `app.platform_admin` would not fix it either. 0006 grants the
-- platform plane a predicate on `tenant` and deliberately withholds one from
-- `company` and every other business table, which is the boundary
-- `TestPlatformAdminHasNoBusinessDataAccess` exists to hold. Widening it for a
-- backfill would be widening it permanently.
--
-- So FORCE is lifted for the length of this transaction and put back. Not
-- DISABLE: the policies stay enabled the entire time and keep applying to every
-- other role. FORCE is the flag that makes them apply to the table's OWNER as
-- well, and the owner is who is running this. `ALTER TABLE` takes an ACCESS
-- EXCLUSIVE lock, so no other session can read either table while the flag is
-- down, and the whole migration is one transaction — a failure rolls the flag
-- back with everything else.
ALTER TABLE tenant  NO FORCE ROW LEVEL SECURITY;
ALTER TABLE company NO FORCE ROW LEVEL SECURITY;

-- Carry up what the tenant already said, where it said anything.
--
-- `min(country)` rather than "the first company created": it needs no ordering
-- column, and in v1 a starter tenant is capped at one company anyway, so the
-- aggregate is over a single row in every real case. A tenant that somehow
-- holds companies in two markets keeps the lower code and the operator can
-- correct it — better than the migration guessing loudly.
UPDATE tenant t
SET    market = sub.country
FROM   (SELECT tenant_id, min(country) AS country
        FROM   company
        GROUP  BY tenant_id) sub
WHERE  sub.tenant_id = t.id
  AND  sub.country IN ('sa', 'bd', 'us');

-- Anything left has not reached onboarding yet. Saudi is the launch market and
-- the existing default for `data_region`, so it is the consistent answer here.
UPDATE tenant SET market = 'sa' WHERE market IS NULL;

ALTER TABLE tenant
  ALTER COLUMN market SET NOT NULL,
  ALTER COLUMN market SET DEFAULT 'sa',
  ADD CONSTRAINT tenant_market_supported
    CHECK (market IN ('sa', 'bd', 'us'));

-- Put the owner back under its own policies. If either of these is ever lost,
-- the table's owner stops being subject to row-level security and tenant
-- isolation quietly weakens for exactly the role the application connects as.
ALTER TABLE tenant  FORCE ROW LEVEL SECURITY;
ALTER TABLE company FORCE ROW LEVEL SECURITY;

COMMENT ON COLUMN tenant.market IS
  'The market this account was sold into, chosen by the platform operator at '
  'provisioning. Constrains company.country during onboarding; company.country '
  'remains what the tax engine reads.';
