-- 0116 — The WPS wage-file format, verified against the Ministry's own document.
--
-- `SA.WPS.WAGE_FILE_FORMAT` shipped as {"format": "__VERIFY__"} and was named a
-- release blocker, with a note saying to "pull from the live Mudad
-- specification". It sat unverified because nobody had, and the two formats the
-- generator could write — `mudad_xml` and `sif` — were invented. Neither
-- appears in any Ministry document.
--
-- # The document
--
-- Ministry of Human Resources and Social Development, "WPS Wages File
-- Specification", 21 pages, published under /sites/default/files/2017-06/ and
-- retrieved 2026-09-03 from:
--
--   https://www.hrsd.gov.sa/sites/default/files/2017-06/WPS%20Wages%20File%20Technical%20Specification.pdf
--
-- It is the Ministry's own technical specification, not a summary of one. §1.7
-- gives the file shape; §1.8.2 table 2 gives the ten Header Group fields with
-- their formats and whether each is mandatory; §1.8.4 table 4 gives the
-- fourteen Content Group fields on the same terms. `internal/people/wpsfile.go`
-- implements exactly those and cites the section for each rule.
--
-- # Why the format is called wps_tab and not mudad
--
-- Mudad is the portal a file is uploaded to. The FORMAT is the Ministry's
-- tab-delimited wages file, and conflating the two is what produced an invented
-- "mudad_xml" in the first place. If Mudad later publishes a different layout
-- it becomes another version of this rule with its own effective date, which is
-- what the registry's dating is for — the generator dispatches on the value
-- recorded here, so a change is a registry entry and a writer, not a rewrite.
--
-- # Verified, and what that claims
--
-- `verified_on` is set because the figures come from the Ministry's document
-- rather than from a summary of it, and `notes` records exactly which sections.
-- What it does NOT claim is that the document is the newest one: §1.7 dates the
-- format to a banks' workshop in May 2012, and an operator with Mudad portal
-- access should confirm no later revision applies before the first live run.
-- That is a re-verification of a known value, not the absence of one.
--
-- `release_blocker` stays true. The value is now present, so the blocker no
-- longer fires; leaving the flag on keeps the rule in the release checklist,
-- which is where a format that "changes without publicity" belongs.

-- # A new version, not an edit
--
-- `regulatory_rule` freezes rule_key, country, payload, effective_from and
-- source_authority once written, and refuses DELETE outright. That is the same
-- discipline posting rules keep, and for the same reason: a payroll run resolves
-- the rule in force on the period being processed, so rewriting a value in place
-- would silently restate months that were computed under the old one.
--
-- So the placeholder row is closed off and the verified value is inserted as the
-- version that follows it.

UPDATE regulatory_rule
SET effective_to = DATE '2026-02-01'
WHERE rule_key = 'SA.WPS.WAGE_FILE_FORMAT' AND country = 'sa'
  AND effective_to IS NULL;

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority,
   source_document, source_url, release_blocker, notes, verified_on)
VALUES
('SA.WPS.WAGE_FILE_FORMAT', 'sa',
 jsonb_build_object(
   'format', 'wps_tab', 'version', '2017-06', 'delimiter', 'tab',
   'currency', 'SAR', 'header_fields', 10, 'content_fields', 14),
 DATE '2026-02-01', 'mhrsd',
 'WPS Wages File Specification (MHRSD), sections 1.7, 1.8.2 table 2 and '
 '1.8.4 table 4',
 'https://www.hrsd.gov.sa/sites/default/files/2017-06/'
 'WPS%20Wages%20File%20Technical%20Specification.pdf',
 true,
 'Implemented in internal/people/wpsfile.go against the Ministry document '
 'itself. Header Group and Content Group field order, formats and mandatory '
 'flags are taken from tables 2 and 4; the bank-only fields ([FILE-REJCDE], '
 '[RET-CODE], [TRN-REF], [TRN-STATUS], [TRN-DATE]) are written empty because '
 'an establishment filling them would be claiming a payment had been executed. '
 'Section 1.7 dates the layout to a banks workshop in May 2012 - confirm '
 'against the Mudad portal that no later revision applies before the first '
 'live submission, and record any newer layout as a further version of this '
 'rule rather than editing this one.',
 DATE '2026-09-03');
