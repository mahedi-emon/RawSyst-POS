-- 0109 — Which tax jurisdiction a shop sells in.
--
-- 0106 built the jurisdiction hierarchy and the rate table and nothing read
-- them, because a sale had no way to say where it happened. This is that link.
--
-- # On the store, not the company
--
-- A company with branches in two cities is taxed differently in each, which is
-- the whole reason jurisdictions are hierarchical. Putting the link on the
-- company would make every branch of a chain share one rate — wrong in exactly
-- the case the model exists for.
--
-- # Nullable, and only consulted where tax is not national
--
-- Saudi Arabia and Bangladesh set VAT nationally: their rate comes from the
-- regulatory registry by country and date, and their shops leave this null for
-- ever. A market whose tax is levied by state, county and city resolves through
-- here instead. See registry.rateKeyFor.
--
-- # Origin versus destination is NOT decided here
--
-- Some US jurisdictions tax where the sale originates and some where it is
-- delivered, and `tax_jurisdiction.is_origin_based` records that fact per
-- authority without anything yet reading it. This column is the ORIGIN: the
-- shop's own jurisdiction, which is the right answer for a customer standing at
-- the counter and the starting point for the destination case. Wiring delivery
-- addresses into rate selection needs the sourcing rules per state, and
-- choosing one silently would be inventing a rule.

ALTER TABLE store
  ADD COLUMN tax_jurisdiction_id uuid
    REFERENCES tax_jurisdiction(id) ON DELETE RESTRICT;

CREATE INDEX store_tax_jurisdiction_idx ON store (tax_jurisdiction_id)
  WHERE tax_jurisdiction_id IS NOT NULL;

COMMENT ON COLUMN store.tax_jurisdiction_id IS
  'Where this shop sells, for markets whose tax is set below national level. '
  'Null where the market sets tax nationally (SA, BD) — those resolve from the '
  'regulatory registry by country and date.';

-- ---------------------------------------------------------------------------
-- One worked example, from the binding authority
-- ---------------------------------------------------------------------------
--
-- The United States and California, with California's statewide base rate.
--
-- Source: California Department of Tax and Fee Administration, "Sales & Use Tax
-- Rates" — https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm —
-- read 2026-09-03: "The statewide tax rate is 7.25%."
--
-- # This is NOT enough to sell in California, and is not pretended to be
--
-- The same page says: "In most areas of California, local jurisdictions have
-- added district taxes that increase the tax owed by a seller. Those district
-- tax rates range from 0.10% to 2.00%", and directs sellers to look the
-- combined rate up BY ADDRESS.
--
-- So this seeds the state's share and no district. A shop in California will
-- resolve 7.25% and be undercharging wherever a district applies, which is
-- most of the state — and `verified_on` is deliberately left NULL so the
-- production gate refuses it until somebody records the districts that apply to
-- their address and confirms them. An unverified rate that refuses is safer
-- than a plausible one that quietly undercharges.
--
-- Seeded at all because a hierarchy with no rows in it cannot be shown to work,
-- and because the state share is a real figure from the real authority. The
-- district rows are a data-loading task per state, not a code gap.

INSERT INTO tax_jurisdiction (country, level, code, name, is_origin_based)
VALUES ('us', 'country', 'US', 'United States', NULL)
ON CONFLICT (country, level, code) DO NOTHING;

INSERT INTO tax_jurisdiction (parent_id, country, level, code, name, is_origin_based)
SELECT j.id, 'us', 'state', 'CA', 'California', NULL
FROM tax_jurisdiction j
WHERE j.country = 'us' AND j.level = 'country' AND j.code = 'US'
ON CONFLICT (country, level, code) DO NOTHING;

INSERT INTO tax_jurisdiction_rate
  (jurisdiction_id, treatment, rate, effective_from,
   source_authority, source_document, source_url, notes)
SELECT j.id, 'taxable', 0.0725, '2017-01-01',
       'cdtfa',
       'California Department of Tax and Fee Administration — Sales & Use Tax Rates',
       'https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm',
       'Statewide base rate only, read from CDTFA on 2026-09-03: "The '
       'statewide tax rate is 7.25%." District taxes of 0.10% to 2.00% apply '
       'in most areas and are NOT included here — the same page directs '
       'sellers to look the combined rate up by address. Left unverified on '
       'purpose: a sale resolving only this rate would undercharge wherever a '
       'district applies. Record the applicable district jurisdictions and '
       'rates, then verify.'
FROM tax_jurisdiction j
WHERE j.country = 'us' AND j.level = 'state' AND j.code = 'CA'
ON CONFLICT DO NOTHING;
