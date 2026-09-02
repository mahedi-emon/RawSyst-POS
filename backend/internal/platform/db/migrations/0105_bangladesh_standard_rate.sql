-- 0105 — Bangladesh's standard VAT rate, so a Bangladeshi till can ring up a
-- sale at all.
--
-- # What was actually broken
--
-- `registry.VATRate` asked for the rule key `SA.VAT.STANDARD_RATE` and filtered
-- it by the caller's country. The country was plumbed correctly the whole way
-- from `company.country` through `applyTaxProfile`; the KEY was not. So a
-- Bangladeshi sale asked for Saudi Arabia's rate rule restricted to Bangladesh,
-- matched no row, and the till refused the sale.
--
-- The key is now derived from the country, which makes this row the one that
-- has to exist. `BD.VAT.TAX_TREATMENTS` was seeded in 0010 and has been sitting
-- next to a rate that was never written.
--
-- # This value is UNVERIFIED, and that is recorded rather than glossed
--
-- Part N is explicit that developers must not fill legal values in from
-- assumption, and this one is seeded exactly as 0010 seeded the Bangladeshi
-- treatment list: with the shape right, the number stated as a placeholder, and
-- a note naming who has to confirm it.
--
-- Bangladesh is genuinely more complicated than one number. The VAT and
-- Supplementary Duty Act 2012 sets a standard rate alongside several reduced
-- and sector-specific rates, supplementary duty applies to particular goods,
-- and invoicing runs on the Mushak forms. A single national standard rate is
-- the right SHAPE for the rate a POS line uses and is not the whole regime.
--
-- `release_blocker` is false, matching `BD.VAT.TAX_TREATMENTS`: a true release
-- blocker refuses to let the API start, and blocking every deployment on a
-- value Saudi tenants do not use would be the wrong trade. `verified_on` stays
-- null, so this appears in the registry health report as never verified and in
-- E8.3's staleness alerting until somebody checks it against the NBR.

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority, source_document,
   source_url, release_blocker, notes)
VALUES
('BD.VAT.STANDARD_RATE', 'bd', '{"rate": "0.15"}'::jsonb, '2020-01-01',
 'nbr', 'VAT and Supplementary Duty Act 2012 — VERIFY AGAINST NBR',
 'https://nbr.gov.bd', false,
 'PLACEHOLDER. 15% is the standard rate commonly stated for Bangladesh, and it '
 'has NOT been confirmed against the National Board of Revenue by anyone on '
 'this project. Bangladesh also applies reduced and sector-specific rates and '
 'supplementary duty, none of which this single value expresses. Confirm the '
 'standard rate, its effective date and whether a POS line needs to select '
 'among several rates before any Bangladeshi shop trades on this.')
ON CONFLICT DO NOTHING;
