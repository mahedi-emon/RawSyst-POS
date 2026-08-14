-- 0012 — Record the verified ZATCA rules, and correct three design errors.
--
-- Source: `ZATCA E-Invoicing Phase 2.md`, a point-by-point verification against
-- ZATCA primary documents. Versions cited throughout:
--   * E-Invoicing Detailed Guideline           V2,   May 2023
--   * E-Invoicing Detailed Technical Guideline V2,   Nov 2022
--   * XML Implementation Standard              v1.2, 2023-05-19
--   * Security Features Implementation Standard v1.2, 2023-05-19
--
-- # Why payloads are edited in place
--
-- The registry is append-only for LEGAL CHANGES: a new rate opens a new
-- effective period so history stays honest. This migration is not a legal
-- change. Nothing in Saudi law changed on this date — we verified what the law
-- already said and replaced placeholders with the real values. Opening new
-- effective periods would assert in the data that something happened today,
-- and a report reconstructing "what did we believe the law was" would show a
-- phantom event. So the frozen-column trigger is lifted, exactly as in 0011.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

-- ---------------------------------------------------------------------------
-- Verified: the three release blockers that can now be closed
-- ---------------------------------------------------------------------------

-- Submission format. CORRECTION: the design said "UBL 2.1 XML or PDF/A-3 with
-- embedded XML". That is wrong for SUBMISSION. Detailed Guideline §4.1.2(b)
-- and §4.2.2(c) and Technical Guideline §4.3 all state the EGS must submit in
-- XML and NOT PDF/A-3. PDF/A-3 is permitted only for storage and for the
-- buyer's human-readable copy.
UPDATE regulatory_rule SET
  payload = '{"submission_format": "UBL_2.1_XML",
              "pdf_a3_permitted_for": ["storage", "buyer_copy"],
              "pdf_a3_permitted_for_submission": false,
              "schema_version": "1.2"}'::jsonb,
  source_document = 'XML Implementation Standard v1.2 (2023-05-19); Detailed Guideline V2 §4.1.2(b), §4.2.2(c); Technical Guideline V2 §4.3',
  source_url = 'https://zatca.gov.sa',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED. Submission to Fatoora is XML only — PDF/A-3 is NOT accepted for submission, only for storage and the buyer copy. Watch the Educational Library page for a new version of the XML Implementation Standard.'
WHERE rule_key = 'SA.ZATCA.XML_SCHEMA_VERSION';

UPDATE regulatory_rule SET
  payload = '{"algorithm": "SHA-256",
              "rounding": "half_up",
              "monetary_decimals": 2,
              "rounding_applied_on_decimal": 3,
              "unit_price_decimals": null}'::jsonb,
  source_document = 'XML Implementation Standard v1.2 (2023-05-19) §7.3',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED. Half-up rounding, applied on the third decimal to yield two. Monetary and VAT amounts are capped at 2 decimals; UNIT PRICE has no decimal restriction, which is a real distinction — a unit price of 3.333 is valid, the line total it produces is not.'
WHERE rule_key = 'SA.ZATCA.HASH_ALGORITHM';

-- CSID validity. The "1 year compliance / 3 year production" figure that
-- circulates in vendor blogs is contradicted by the primary source. The only
-- published number is the X.509 NotAfter ceiling.
UPDATE regulatory_rule SET
  payload = '{"max_months": 60,
              "authoritative_source": "per_certificate_not_after",
              "renewal_process": "revoke_existing_then_issue_new",
              "compliance_csid_validity": "not_published"}'::jsonb,
  source_document = 'Security Features Implementation Standard v1.2 (2023-05-19) §2.2.2; Technical Guideline V2 §3.2.2',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED with a caveat. ZATCA publishes no single fixed validity. The certificate profile caps NotAfter at "generation + up to 60 months (5 years)", so renewal reminders MUST be driven by each certificate''s embedded expiry rather than a hard-coded term. Renewal revokes the old CSID and issues a new one. Compliance CSID validity is unpublished; treat it as short-lived. Publication of ZATCA''s CA CP/CPS would fix this definitively.'
WHERE rule_key = 'SA.ZATCA.CSID_RENEWAL_DAYS';

-- ---------------------------------------------------------------------------
-- CORRECTION: there IS an official offline path for B2B standard invoices
-- ---------------------------------------------------------------------------
--
-- The design assumed a standard invoice simply cannot be issued offline, and
-- offered only block / draft-and-hold / convert-to-simplified. Detailed
-- Guideline §10 documents a fourth, official route for an extended outage: the
-- seller may issue an UNCLEARED invoice, which "will not be considered fully
-- compliant but will be considered as a VAT invoice until fully compliant
-- invoice is issued immediately once the connection is restored."
--
-- Three conditions travel with it, and all three are product requirements:
--   * the uncleared invoice is NOT eligible for VAT deduction by the buyer
--   * the seller must retain evidence of attempting clearance (API logs)
--   * the seller must file ZATCA's failure-notification form
--
-- There is no fixed outage duration that makes an outage "extended". The
-- operative bound is VAT Art. 53(1): a standard invoice may be issued within
-- 15 days from the end of the month of supply.
UPDATE regulatory_rule SET
  payload = '{"uncleared_invoice_permitted": true,
              "issuance_deadline_rule": "15_days_from_end_of_month_of_supply",
              "vat_law_article": "53(1)",
              "uncleared_invoice_vat_deductible": false,
              "requires_clearance_attempt_evidence": true,
              "requires_failure_notification_to_zatca": true,
              "must_reissue_after_clearance": true,
              "short_outage_retry_minutes": 5,
              "short_outage_retry_interval_minutes": 15,
              "retry_timings_binding": false}'::jsonb,
  source_document = 'E-Invoicing Detailed Guideline V2 §10 (Failure Scenarios, pp. 47-52); VAT Implementing Regulations Art. 53(1)',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED — and it corrects the design. An extended outage does NOT force a block. ZATCA documents issuing an uncleared invoice, marked non-deductible, with API-log evidence and a filed failure notification, re-issued once cleared. The retry timings (~5 min, ~15 min) are marked "TBC" in the guideline and are NOT binding. Frequent or extended failure reports trigger individual ZATCA investigation.'
WHERE rule_key = 'SA.ZATCA.STANDARD_OFFLINE_TOLERANCE';

-- Reporting window, now with its source.
UPDATE regulatory_rule SET
  source_document = 'E-Invoicing Detailed Guideline V2 §4.2.2(c)',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED. Simplified (B2C) invoices are reported to Fatoora within 24 hours of issuance. Drives the staleness escalation on the unsubmitted queue.'
WHERE rule_key = 'SA.ZATCA.REPORTING_WINDOW_HOURS';

-- Retention gains its third tier. The design carried 6 years with an "extended
-- to ~11", missing immovable property entirely.
UPDATE regulatory_rule SET
  payload = '{"general_years": 6,
              "movable_capital_asset_years": 11,
              "immovable_capital_asset_years": 15,
              "derivation": "Art.66 base plus Art.50/52 adjustment period plus 5 years"}'::jsonb,
  source_document = 'VAT Implementing Regulations Art. 66, read with Art. 50/52',
  verified_on = DATE '2026-08-15',
  notes = 'VERIFIED. General records 6 years. Capital assets keep the adjustment period plus 5: movable 6+5=11, immovable/real estate 10+5=15. The 2025 amendments to the VAT Implementing Regulations left this basis intact. The archive and the PDPL legal-hold override must honour all three tiers, not just six years.'
WHERE rule_key = 'SA.VAT.RECORD_RETENTION';

-- Wave 24, confirmed against the primary announcement.
UPDATE regulatory_rule SET
  source_document = 'ZATCA news_1426 — Wave 24 criteria; Governor Decision 287-99-1447 (gazetted 26 Sep 2025)',
  verified_on = DATE '2026-08-15'
WHERE rule_key = 'SA.VAT.MANDATORY_REGISTRATION_THRESHOLD';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;

-- ---------------------------------------------------------------------------
-- New verified rules
-- ---------------------------------------------------------------------------

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority,
   source_document, source_url, verified_on, release_blocker, notes)
VALUES

-- The CSID is issued per EGS UNIT, not per physical terminal. This is the
-- second design correction: the schema models a CSID on `device`, which is only
-- right for one of ZATCA's three documented architectures.
('SA.ZATCA.EGS_UNIT_MODEL', 'sa',
 '{"csid_scope": "egs_unit",
   "egs_unit_definition": "the software unit that signs and generates one unique invoice sequence",
   "architectures": {
     "centralized_server": "one CSID per taxpayer and one per unique document sequence",
     "smart_pos": "a CSID is required on each POS device",
     "dumb_terminal_with_signing_server": "no CSID on the POS devices; the branch or sending server holds it"
   },
   "one_egs_unit_equals_one_sequence": true}'::jsonb,
 '2022-11-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 §3.5; Detailed Guideline V2 §2.7, §2.10',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED — and it corrects the design. An EGS Unit is NOT necessarily a physical terminal. Where dumb terminals feed a central signing server, the POS devices hold no CSID at all. The schema must therefore model an EGS unit as its own entity that a device points at, rather than putting the CSID on the device.'),

-- Prohibited functions, confirmed verbatim. Several of these are already
-- enforced by database triggers; recording them here keeps the reason traceable.
('SA.ZATCA.PROHIBITED_FUNCTIONS', 'sa',
 '{"invoice_counter_reset": "prohibited",
   "multiple_parallel_sequences": "prohibited",
   "export_of_stamping_private_key": "prohibited",
   "key_storage_requirement": "software or hardware key vault",
   "alteration_or_deletion_of_issued_invoice": "prohibited; cancellation only via credit note",
   "log_modification_or_deletion": "prohibited",
   "non_sequential_log_generation": "prohibited",
   "anonymous_access": "prohibited",
   "default_or_factory_passwords": "prohibited",
   "inaccurate_timestamps_or_time_change": "prohibited",
   "training_mode_named_by_zatca": false}'::jsonb,
 '2022-11-01', 'zatca',
 'E-Invoicing Detailed Guideline V2 §5.4, §5.6, §6.5; Security Features Implementation Standard v1.2',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED. ZATCA does not use the phrase "training mode", but a hidden sales mode is prohibited in effect: it would break the single-chain requirement, the sequential logging requirement and the ban on anonymous access. Note the key-export ban is explicit and names a software or hardware key vault — the terminal keystore design satisfies this, and any "export key" affordance would violate it outright.'),

-- API response handling. The 303 case is a state transition the design missed.
('SA.ZATCA.API_RESPONSES', 'sa',
 '{"outcomes": ["accepted", "accepted_with_warnings", "rejected"],
   "codes": {"200": "accepted", "202": "accepted_with_warnings",
             "303": "clearance_switched_off_submit_via_reporting",
             "400": "rejected", "401": "unauthorized", "413": "payload_too_large",
             "429": "too_many_requests", "500": "server_error",
             "503": "service_unavailable", "504": "gateway_timeout"},
   "clearance_status_labels": ["CLEARED"],
   "reporting_status_labels": ["REPORTED", "NOT_REPORTED"],
   "warnings_still_return_stamped_document": true,
   "rejected_requires_new_document": true}'::jsonb,
 '2023-05-01', 'zatca',
 'E-Invoicing Detailed Guideline V2 §10; Technical Guideline V2 §4.3',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED. Three outcomes, not two: "accepted with warnings" (202) still returns a stamped document and must NOT be treated as a failure, though ZATCA warns such warnings "might become rejections in the future" — so they must be surfaced, not swallowed. 303 means clearance is switched off and the document must be resubmitted through the reporting path, which is a state transition rather than an error. A rejected document is never stamped and must be corrected and resubmitted as a NEW document with a new ICV and hash — confirming that a rejection keeps its place in the chain.'),

-- The nine CSR fields, with their exact formats.
('SA.ZATCA.CSR_FIELDS', 'sa',
 '{"all_mandatory": true,
   "fields": {
     "common_name": "name or asset tracking number of the solution unit",
     "egs_serial_number": "1-<Manufacturer>|2-<Model/Version>|3-<Serial>",
     "organization_identifier": "VAT or Group VAT number: 15 digits, starting and ending with 3",
     "organization_unit_name": "branch name; for VAT groups the 10-digit TIN of the member being onboarded, required when the 11th digit of the organization identifier is 1",
     "organization_name": "taxpayer or organization name",
     "country_name": "ISO 3166 alpha-2",
     "invoice_type": "functionality map TSXY: 1000 standard only, 0100 simplified only, 1100 both; X and Y reserved, set to 0",
     "location": "branch or EGS address, preferably Saudi National Address short format",
     "industry": "industry or sector"
   }}'::jsonb,
 '2022-11-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 §3.3.3, §3.3.6',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED with exact formats. Every field is mandatory and a wrong format causes CSR rejection at onboarding, which is a support call rather than a silent failure. The VAT-group rule is easy to miss: when the 11th digit of the organization identifier is 1, the organization unit name must carry the member''s 10-digit TIN.'),

-- Compliance testing is driven by the declared functionality map.
('SA.ZATCA.COMPLIANCE_TEST_MATRIX', 'sa',
 '{"driven_by": "functionality_map_declared_in_csr",
   "documents_per_family": 3,
   "families": {"1000": ["standard_invoice", "standard_debit_note", "standard_credit_note"],
                "0100": ["simplified_invoice", "simplified_debit_note", "simplified_credit_note"],
                "1100": "both families, six documents"},
   "fixed_count_myth": 12}'::jsonb,
 '2022-11-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 §3.3.4.2',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED. Three documents per invoice-type family, so 3 or 6 depending on the functionality map — not the fixed "12" that circulates in vendor material.'),

-- Arabic is mandatory on the human-readable invoice. Recorded because the
-- product interface language and the invoice language are separate decisions.
('SA.ZATCA.INVOICE_LANGUAGE', 'sa',
 '{"human_readable_must_include_arabic": true,
   "other_languages_permitted_in_addition": true,
   "xml_tag_names": "english",
   "xml_data_content_must_be_arabic": true,
   "arabic_version_of_zatca_documents_prevails": true}'::jsonb,
 '2023-05-01', 'zatca',
 'E-Invoicing Detailed Guideline V2 §5.7, §6.6; FAQ; VAT Implementing Regulations Art. 53',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED. Arabic is MANDATORY on the human-readable invoice, whatever language the user interface is set to. Other languages may appear in addition. XML tag names are English but the invoice DATA rendered from the XML must be Arabic. This is independent of the product UI language: a Saudi tenant running an English interface still issues Arabic invoices.'),

-- Fines, worded to match the primary announcement rather than the ladder that
-- circulates in vendor material.
('SA.ZATCA.FINES', 'sa',
 '{"not_issuing_einvoice_from_sar": 5000,
   "deleting_or_amending_after_issuance_from_sar": 10000,
   "missing_qr_or_buyer_vat_or_unreported_malfunction": "warning_first",
   "statutory_ceiling_sar": 50000,
   "ceiling_basis": "VAT Law Article 45",
   "applied_by": "violation type and number of repetitions",
   "escalating_ladder_applies_to_einvoicing": false}'::jsonb,
 '2021-12-04', 'zatca',
 'ZATCA news "Violations and Fines related to E-invoicing" (News_465)',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED for the three stated categories. IMPORTANT: the "warning → 1,000 → 5,000 → 10,000 → 40,000" ladder widely quoted online is NOT the e-invoicing schedule — it comes from the separate 30 Jan 2022 VAT field-control reclassification decision. Do not present it as the e-invoicing fine schedule. No specific non-integration fine is stated in any primary e-invoicing source. The News_465 URL 404s on direct fetch (ZATCA reorganises news URLs); verify the live URL before publishing anything citing it.'),

-- Neither listing nor SDK validation is certification. Backs the wording lint.
('SA.ZATCA.CERTIFICATION_STATUS', 'sa',
 '{"solution_provider_listing_is_certification": false,
   "sdk_validation_is_certification": false,
   "sdk_is_optional_self_check": true,
   "compliance_responsibility": "taxpayer",
   "permitted_phrasing": ["supports ZATCA requirements", "built to support ZATCA and PDPL requirements"],
   "guidelines_are_binding": false,
   "binding_instruments": ["E-Invoicing Regulation", "Implementation Resolution and Annexes 1 and 2", "VAT Law", "VAT Implementing Regulations"]}'::jsonb,
 '2021-12-04', 'zatca',
 'ZATCA FAQ_037; Detailed Guideline V2 §2.18; Technical Guideline V2 §2.1.4',
 'https://zatca.gov.sa', DATE '2026-08-15', false,
 'VERIFIED, and it is the source of the wording lint. ZATCA states plainly that the provider list is "not considered as an approval by ZATCA of the solutions nor certification", and the SDK is an optional self-check. Compliance responsibility stays with the taxpayer. Note also that ZATCA''s guidelines carry a disclaimer that they are educational and not mandatory — the binding instruments are the Regulation, the Implementation Resolution and the VAT Law and its Regulations.');
