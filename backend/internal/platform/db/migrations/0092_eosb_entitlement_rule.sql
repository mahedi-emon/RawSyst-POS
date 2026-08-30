-- 0092 — The end-of-service entitlement, as a registry rule.
--
-- E6 puts end-of-service benefit under Saudi labour law, and design 20 makes
-- the accrual monthly. What neither settles — because it is not ours to settle
-- — is HOW MUCH accrues: the Saudi Labour Law sets the entitlement in days of
-- wage per year of service, with a different figure for the first five years
-- and for service beyond them, and further variation between resignation and
-- termination.
--
-- Those numbers are exactly what E8 forbids putting in code: "every legal rate,
-- threshold, deadline and file format is versioned data with effective dates
-- instead of hard-coded logic". So the rule exists here with its shape and its
-- source, carrying `__VERIFY__`, and the accrual engine refuses until somebody
-- checks it against the Labour Law and stamps `verified_on`.
--
-- It is a release blocker for the same reason the GOSI schedule is. An accrual
-- at a guessed rate understates a real liability every month for years, and the
-- error is only discovered when somebody leaves and the provision turns out not
-- to cover what they are owed — by which time the misstatement is in several
-- years of signed accounts.
--
-- The payload is a SHAPE, not a guess. The field names say what a verifier has
-- to find; the values say that nobody has found them yet.

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority,
   source_document, source_url, release_blocker, notes)
VALUES
('SA.EOSB.ENTITLEMENT', 'sa',
 '{"days_per_year_first_five": "__VERIFY__",
   "days_per_year_after_five": "__VERIFY__",
   "resignation_fraction_under_two_years": "__VERIFY__",
   "resignation_fraction_two_to_five_years": "__VERIFY__",
   "resignation_fraction_five_to_ten_years": "__VERIFY__",
   "wage_basis": "__VERIFY__"}'::jsonb,
 '2026-01-01', 'mhrsd', 'Saudi Labour Law — end of service award',
 'https://www.hrsd.gov.sa', true,
 'RELEASE BLOCKER. The entitlement is days of wage per year of service, and it '
 'differs for the first five years and beyond, and again between resignation '
 'and termination. `wage_basis` must record WHICH wage the award is computed '
 'on — basic only, or basic plus which allowances — because the two give '
 'materially different answers for the same employee. Accrual resolves at the '
 'period being charged, so correcting the rule later does not rewrite months '
 'already posted.')
ON CONFLICT DO NOTHING;
