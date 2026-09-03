-- 0115 — The establishment details a WPS wage file has to carry.
--
-- The official specification is HRSD's "WPS Wages File Specification", pulled
-- from https://www.hrsd.gov.sa/sites/default/files/2017-06/WPS%20Wages%20File%20Technical%20Specification.pdf
-- on 2026-09-03. Its Header Group is ten fields describing the ESTABLISHMENT
-- and its bank, and this product held none of them: `employee.iban` was the
-- only banking detail anywhere, which is the beneficiary side of the file.
--
-- Without these a wage file cannot be written at all, whatever the format, so
-- the columns come before the generator does.
--
-- # What each one is, in the specification's own terms
--
--   [DEST-ID]     the SARIE ID of the bank the file is sent to — always the
--                 bank holding the account the wages are paid from. 4!A.
--   [ESTB-ID]     the establishment's ID with that bank. 10d. The document
--                 says banks "can continue using their current standards",
--                 so this is the bank's reference for the employer, not a
--                 government number.
--   [BANK-ACC]    the account the total is debited from. 24!X.
--   [MOL-ESTBID]  the Ministry of Labour establishment ID. 2d-15d.
--
-- All nullable: a shop in Bangladesh has no SARIE bank and never will, and a
-- Saudi shop that has not yet joined the Wage Protection System has not been
-- given these numbers. The wage file refuses when they are missing rather than
-- inventing them, which is the same judgement the registry makes about a rate.

ALTER TABLE company
  ADD COLUMN wps_bank_sarie_id      text,
  ADD COLUMN wps_establishment_id   text,
  ADD COLUMN wps_bank_account       text,
  ADD COLUMN mol_establishment_id   text;

-- Formats as the specification writes them. Checked rather than trusted,
-- because a file rejected by the bank costs a payroll cycle and the error
-- comes back days later as a rejection code.
ALTER TABLE company
  ADD CONSTRAINT company_wps_sarie_format CHECK (
    wps_bank_sarie_id IS NULL OR wps_bank_sarie_id ~ '^[A-Z]{4}$'),
  ADD CONSTRAINT company_wps_estbid_format CHECK (
    wps_establishment_id IS NULL OR wps_establishment_id ~ '^[0-9]{1,10}$'),
  ADD CONSTRAINT company_wps_account_format CHECK (
    wps_bank_account IS NULL OR
    (length(wps_bank_account) BETWEEN 1 AND 24
     AND wps_bank_account ~ '^[A-Za-z0-9]+$')),
  ADD CONSTRAINT company_mol_estbid_format CHECK (
    mol_establishment_id IS NULL OR mol_establishment_id ~ '^[0-9]{2,15}$');

COMMENT ON COLUMN company.wps_bank_sarie_id IS
  'WPS [DEST-ID]: the SARIE code of the bank the wage file is sent to. Null '
  'outside Saudi Arabia and until the establishment joins WPS.';
COMMENT ON COLUMN company.mol_establishment_id IS
  'WPS [MOL-ESTBID]: the Ministry of Labour establishment number, which is '
  'what ties the file to the employer in the Wage Protection System.';
