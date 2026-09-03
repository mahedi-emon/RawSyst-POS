-- 0117 — GOSI contribution rates, from GOSI's own employer guidance.
--
-- `SA.GOSI.RATES` shipped as __VERIFY__ in every field and was named a release
-- blocker. A Saudi shop could run payroll and got no social-insurance line,
-- because the registry correctly refused to guess.
--
-- # Sources, and which one controls
--
-- Two GOSI pages state figures, and on two points they appear to disagree. The
-- disagreements resolve rather than needing a coin toss, and both are recorded
-- here so nobody has to re-derive the reasoning:
--
--  * Occupational Hazards. GOSI's "Coverages" page is summarised in search as
--    1.5%; GOSI's EMPLOYER FAQ says, of the employer's share: "9% for Annuities
--    Branch and 2% for Occupational Hazard Branch". The employer FAQ is GOSI
--    telling employers what they pay, which is the question a payroll engine
--    asks, and a second GOSI page agrees with it. 2% is taken, and the
--    discrepancy is noted below so it is re-checked rather than forgotten.
--
--  * Minimum contributory wage. One page says SR 1,500, another SR 400. They
--    are not in conflict: the FAQ says "The minimum wage under the Annuities
--    Branch is SR [1,500] ... 400 for Occupational Hazard Branch". The minimum
--    differs BY BRANCH. Only the maximum, SR 45,000, is common, and that is
--    what `wage_cap` means here.
--
-- Retrieved 2026-09-03 from:
--   https://www.gosi.gov.sa/GOSIOnline/FAQ_Employer?locale=en_US
--   https://www.gosi.gov.sa/GOSIOnline/Saudi_Workers&locale=en_US
--
-- # What the figures are
--
-- Saudi national: Annuities 9% employer + 9% contributor, plus SANED
-- unemployment at 1.5% "borne equally by both the beneficiary and the employer
-- as of 01/01/2022" — 0.75% each — plus Occupational Hazards at 2% employer.
-- So 11.75% employer and 9.75% employee.
--
-- Non-Saudi: Occupational Hazards only, "applied on a mandatory basis to all
-- workers without distinction of sex, nationality or age", at 2% employer. The
-- Annuities Branch and SANED are Saudi-only, so the employee pays nothing.
--
-- # The July 2024 law, and what is deliberately NOT claimed here
--
-- The Council of Ministers approved a new Social Insurance Law in July 2024
-- applying to new employees with no prior contribution periods. GOSI's public
-- rate pages state the Annuities Branch flatly at 18% (9 + 9) with no
-- hire-date distinction and publish NO year-by-year escalation schedule.
--
-- So both Saudi bands are recorded at the published rate, which is what GOSI
-- says an employer pays today for either. This is NOT a claim that no
-- escalation exists — widely-reported commentary says one does, and Blueprint
-- Part N forbids implementing a compliance figure from Tier 2 sources. If GOSI
-- publishes a dated schedule, it becomes a further version of this rule with
-- its own effective_from, which is exactly what the registry's dating is for.
--
-- `release_blocker` stays true: these are figures to re-confirm each year, and
-- the flag is what keeps them on the release checklist.

-- # A new version, not an edit
--
-- The payload column is frozen once written and DELETE is refused, because a
-- payroll run resolves the rule in force on the month being processed. Editing
-- in place would restate every month already computed. So the placeholder row
-- is closed and this is the version that follows it.

UPDATE regulatory_rule
SET effective_to = DATE '2026-02-01'
WHERE rule_key = 'SA.GOSI.RATES' AND country = 'sa'
  AND effective_to IS NULL;

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority,
   source_document, source_url, release_blocker, notes, verified_on)
VALUES
('SA.GOSI.RATES', 'sa',
 jsonb_build_object(
   'saudi_post_jul2024', jsonb_build_object(
     'employer', '0.1175', 'employee', '0.0975'),
   'saudi_pre_jul2024', jsonb_build_object(
     'employer', '0.1175', 'employee', '0.0975'),
   'expatriate', jsonb_build_object('employer', '0.02', 'employee', '0'),
   'wage_cap', '45000'),
 DATE '2026-02-01', 'gosi',
 'GOSI Employer FAQ (employer share: 9% Annuities + 2% Occupational Hazards) '
 'and Saudi Workers page (Annuities 18% = 9% + 9%; contributory wage maximum '
 'SR 45,000); SANED 1.5% shared equally from 01/01/2022',
 'https://www.gosi.gov.sa/GOSIOnline/FAQ_Employer?locale=en_US',
 true,
 'Employer 11.75% = 9% Annuities + 0.75% SANED + 2% Occupational Hazards. '
 'Employee 9.75% = 9% Annuities + 0.75% SANED. Non-Saudis are in the '
 'Occupational Hazards Branch only, so 2% employer and nothing from the '
 'employee. Both Saudi bands carry the same rate because GOSI publishes one '
 'Annuities rate and no escalation schedule; the July 2024 law changed who it '
 'applies to, retirement age and eligibility. RE-CHECK ANNUALLY: (a) whether '
 'GOSI has published a dated escalation for post-July-2024 entrants, and (b) '
 'the Occupational Hazards rate, where the Coverages page reads 1.5% while the '
 'Employer FAQ says 2% - the Employer FAQ is taken as controlling because it '
 'states what an employer pays. Record any change as a NEW version of this '
 'rule rather than editing this one.',
 DATE '2026-09-03');
