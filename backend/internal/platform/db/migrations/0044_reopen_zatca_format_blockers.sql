-- 0044 — Put back the two ZATCA questions 0012 closed by accident.
--
-- 0012 verified real things and recorded them correctly. What it did not
-- intend, and did, was silence two blockers.
--
-- `registry.Health` reports a rule as blocking release when
-- `release_blocker AND verified_on IS NULL`. Both rules below kept
-- release_blocker = true, so the flag survived — but 0012 set verified_on on
-- both, and the gate stopped naming them.
--
--   SA.ZATCA.HASH_ALGORITHM was seeded as
--     {"algorithm": "SHA-256", "canonicalization": "__VERIFY__"}
--   and 0012 replaced the whole payload with the algorithm and the rounding
--   rules from XML Implementation Standard v1.2 §7.3. Those are genuinely
--   verified. The `canonicalization` key was not answered; it was deleted.
--
--   SA.ZATCA.XML_SCHEMA_VERSION was seeded as {"version": "__VERIFY__"} and
--   0012 established, with citations, that submission is XML only and the
--   standard in force is v1.2. Also genuinely verified. What it did not
--   establish is the mandatory UBL field set — which is the thing E8.4 #3 was
--   actually about, and the thing an invoice builder needs.
--
-- The honest repair is not to un-verify two findings that hold. It is to give
-- each open question its own rule, so the verified half stays verified and the
-- gate names the half that is still missing. `zatca.DocumentHasher` and
-- `zatca.UnverifiedSubmitter` already refuse to act on either; this makes the
-- registry agree with the code.
--
-- Nothing here asserts a ZATCA value. Both payloads are __VERIFY__ placeholders
-- and both rules are unverified by construction.

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from,
   source_authority, source_document, source_url, release_blocker, notes)
VALUES
('SA.ZATCA.XML_CANONICALIZATION', 'sa',
 '{"method": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing Security Features Implementation Standard', 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER. Split out of SA.ZATCA.HASH_ALGORITHM, whose seeded '
 '"canonicalization": "__VERIFY__" was dropped by 0012 rather than answered. '
 'SHA-256 and the rounding rules are verified; the canonicalisation applied '
 'before hashing is not. Two implementations that agree on the algorithm and '
 'disagree on the canonical form produce different hashes, so every PIH in the '
 'chain would be wrong together and the error would surface only at '
 'submission. zatca.DocumentHasher exists as a seam for exactly this.'),

('SA.ZATCA.UBL_FIELD_SET', 'sa',
 '{"mandatory_fields": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing XML Implementation Standard', 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER, and the substance of E8.4 #3. SA.ZATCA.XML_SCHEMA_VERSION '
 'verified that submission is XML only and that the standard in force is v1.2. '
 'It did not enumerate the mandatory UBL 2.1 elements, their cardinality or '
 'the business rules attached to them, which is what building an invoice '
 'requires. Unverified until read from the standard and its rule set.');

-- The notes on the two verified rules now say what was verified and where the
-- rest went, so a reader who finds "VERIFIED" does not conclude more than 0012
-- established.
UPDATE regulatory_rule SET
  notes = notes || ' SCOPE: this rule covers the submission format and the '
    'standard version only. The mandatory UBL field set is a separate, '
    'unverified rule — SA.ZATCA.UBL_FIELD_SET.'
WHERE rule_key = 'SA.ZATCA.XML_SCHEMA_VERSION'
  AND notes NOT LIKE '%SA.ZATCA.UBL_FIELD_SET%';

UPDATE regulatory_rule SET
  notes = notes || ' SCOPE: algorithm and rounding only. The canonicalisation '
    'applied before hashing was seeded as __VERIFY__ here and is now carried '
    'by SA.ZATCA.XML_CANONICALIZATION, which remains unverified.'
WHERE rule_key = 'SA.ZATCA.HASH_ALGORITHM'
  AND notes NOT LIKE '%SA.ZATCA.XML_CANONICALIZATION%';
