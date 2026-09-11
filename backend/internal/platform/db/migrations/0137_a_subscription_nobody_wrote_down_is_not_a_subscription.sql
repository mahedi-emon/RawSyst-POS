-- Every business gets a real subscription row.
--
-- # The thing this fixes
--
-- `provisioning.CreateTenant` created a tenant, its limits, its Owner, its
-- Owner role and its onboarding record, and no subscription at all. The read
-- path in `billing.read` then LEFT JOINed the missing row and coalesced it:
--
--     coalesce(s.tier, t.plan_tier), coalesce(s.status, 'active'), ...
--
-- So the platform answered `GET /platform/tenants/{id}/subscription` with a
-- complete, plausible, ACTIVE subscription for a client who had none. Every
-- business ever created through the product was in that state.
--
-- Two consequences, and the second is the serious one.
--
-- The first is that the figures were fiction. An operator counting active
-- subscriptions was counting tenants.
--
-- The second is that `current_period_end` was NULL and coalesced to nothing,
-- so there was no date for anything to compare against. A subscription with no
-- end cannot expire. Whatever enforcement is built later -- and it is the next
-- phase's work, deliberately not this one's -- would have swept the table
-- looking for subscriptions past their period end and found none, for every
-- client, for ever. It would have passed its own tests and enforced nothing.
--
-- # Why a backfill and not a NOT NULL constraint on the join
--
-- Because the rows have to exist before anything can require them, and because
-- the alternative -- having `read` return "no subscription" -- would change
-- what every existing tenant reports, mid-flight, from an answer to an error.
--
-- # What the backfilled rows say
--
-- Exactly what the coalesce was already asserting, and not one thing more:
--
--   tier      the tenant's own plan_tier, which is where coalesce got it
--   status    'active', which is what coalesce claimed
--   cycle     'monthly', the column default and coalesce's fallback
--   price     0, and this one is deliberate: the platform does not know what
--             these clients pay, and inventing a number would put a figure in
--             front of an operator that nobody agreed. Zero reads as "not
--             recorded" and is correctable on the billing screen.
--   currency  'SAR', the column default and the launch market's currency
--
-- started_on is the only place this migration knows better than the default.
-- `current_date` would say every existing client signed up today, which is
-- false for all of them and would be baked in permanently. The tenant's own
-- created_at is the real date and is already on file.
--
-- current_period_end is left NULL, and that is the honest answer rather than a
-- convenient one. The platform does not know when these subscriptions run to;
-- it never recorded it. Writing "a month from now" for a client who has been
-- trading for a year would be an invented commercial fact, and the first thing
-- it would do is expire them. An operator sets the real date on the billing
-- screen, which can now accept one.

INSERT INTO subscription (tenant_id, tier, cycle, price, currency, status,
                          started_on, current_period_end)
SELECT t.id, t.plan_tier, 'monthly', 0, 'SAR', 'active',
       t.created_at::date, NULL
FROM tenant t
WHERE NOT EXISTS (
  SELECT 1 FROM subscription s WHERE s.tenant_id = t.id
);

-- A note on the rows this just wrote, so the next person to read the table
-- knows the price of zero is an absence rather than a free account.
COMMENT ON COLUMN subscription.price IS
  'What the platform charges, in the platform''s own currency. Zero on a row '
  'created by migration 0137 means the real figure was never recorded, not '
  'that the client pays nothing.';
