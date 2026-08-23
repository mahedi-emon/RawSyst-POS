-- 0059 — What the Security Features Implementation Standard actually says.
--
-- On 2026-08-23 the primary source was obtained and read directly, rather than
-- summarised: ZATCA, "Security Features Implementation Standards to the
-- E-Invoicing resolution", Version 1.1, dated 2022-06-24, 27 pages, from
-- zatca.gov.sa. It was extracted from the PDF locally and quoted, not passed
-- through a model that paraphrases.
--
-- That document had not been read by the 2026-08-19 desk verification, which
-- worked from four other PDFs. It answers three things those four did not, and
-- one of them the registry had recorded as obtainable only from the Fatoora
-- SDK.
--
-- # What is now verified
--
-- SA.ZATCA.XML_CANONICALIZATION, in full. §2.3.3 prints the ds:Transforms
-- element: three XPath removals using
-- "http://www.w3.org/TR/1999/REC-xpath-19991116", then
-- "http://www.w3.org/2006/12/xml-c14n11" — Canonical XML 1.1. Not exclusive
-- c14n and not the 2001 version, both of which are the usual assumptions.
-- §3 then defines the previous-invoice hash as "applying the same transform as
-- is used for the cryptographic stamp and as specified in section 2.3.3 and
-- taking the sha256 algorithm".
--
-- 0044 opened this rule saying "two implementations that agree on the algorithm
-- and disagree on the canonical form produce different hashes, so every PIH in
-- the chain would be wrong together and the error would surface only at
-- submission". The canonical form is now on the record.
--
-- # What is now PARTLY evidenced, and stays unverified
--
-- SA.ZATCA.QR_TAG_VALUE_ENCODING. §4.1 gives the encoding per tag group:
--
--   [for tags 1 to 5]  Length: the length of the byte array resulted from the
--                      UTF8 encoding of the field value... Value: the byte
--                      array resulting from the UTF8 encoding of the field
--                      value.
--   [for tag 6]        Length: length of hash (SHA256 ) is 32 bytes
--                      Value: the byte array constituting the value of the field
--
-- So tag 6 is the RAW digest, which is what the Technical Guideline's worked
-- example seemed to contradict. Tags 7, 8 and 9 are covered by neither bracket:
-- §4.1 stops at tag 6. The rule stays unverified because a third of it is still
-- unstated, and it cannot be exercised anyway until there is a certificate to
-- sign with.
--
-- SA.ZATCA.CSR_SUBJECT_LAYOUT. Table 1, "CSR 'Subject' field content or
-- Relative Distinguished Names (RDNs)", maps all nine inputs:
--
--   Common Name             x509.subject.common_name
--   EGS Serial Number       x509.alternative_names (GUID), "1-...|2-...|3-..."
--   Organization Identifier organizationIdentifier (2.5.4.97), 15 digits,
--                           begins with 3 and ends with 3
--   Organization Unit Name  x509.subject.organizational_unit
--   Organization Name       x509.subject.organization
--   Country Name            x509.subject.country, ISO 3166 Alpha-2
--   Invoice Type            businessCategory (2.5.4.15), 4 digits over "TSCZ"
--   Location                x509.alternative_names / registeredAddress 2.5.4.26
--   Industry                x509.alternative_names / businessCategory 2.5.4.15
--
-- 0045 said this table "is not published in any of the four guideline PDFs" and
-- to "read it from the official Fatoora SDK". It is published — in a fifth
-- document. The SDK is still needed for something else (below), but not for
-- this.
--
-- It also settles the conflict 0045 recorded: Technical Guideline V2 p.28 maps
-- the functionality digits to "TSXY" and calls X and Y undefined; this document
-- maps them to "TSCZ" and says C and Z are "for future use". Same four
-- positions, and this is the later and more specific statement.
--
-- The rule STAYS UNVERIFIED because one stated unknown is untouched: the OID
-- under which certificateTemplateName is carried. That string does not appear
-- anywhere in this document. A CSR with a correct DN and no template extension
-- is still refused at onboarding.
--
-- # Nothing here changes release_blocker
--
-- Verifying a rule records that its content is known. Whether it blocks release
-- is a separate judgement and not one a migration should quietly make — the
-- same convention SA.ZATCA.HASH_ALGORITHM already follows, which is verified
-- and still flagged.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

-- ---------------------------------------------------------------------------
-- Verified: the canonical form
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET payload = jsonb_build_object(
      'method', 'http://www.w3.org/2006/12/xml-c14n11',
      'transforms', jsonb_build_array(
        'not(//ancestor-or-self::ext:UBLExtensions)',
        'not(//ancestor-or-self::cac:Signature)',
        'not(//ancestor-or-self::cac:AdditionalDocumentReference[cbc:ID=''QR''])'
      ),
      'transform_algorithm', 'http://www.w3.org/TR/1999/REC-xpath-19991116',
      'digest', 'sha256'
    ),
    source_document = 'Security Features Implementation Standards v1.1 (2022-06-24) §2.3.3, §3',
    source_url = 'https://zatca.gov.sa/ar/E-Invoicing/SystemsDevelopers/Documents/20220624_ZATCA_Electronic_Invoice_Security_Features_Implementation_Standards.pdf',
    verified_on = DATE '2026-08-23',
    notes = notes ||
      ' VERIFIED 2026-08-23 from the Security Features Implementation Standards'
      ' v1.1 §2.3.3, read directly from the PDF. Three XPath removals using'
      ' http://www.w3.org/TR/1999/REC-xpath-19991116 (UBLExtensions,'
      ' cac:Signature, and the QR AdditionalDocumentReference), then'
      ' http://www.w3.org/2006/12/xml-c14n11 — Canonical XML 1.1, NOT exclusive'
      ' c14n. §3: the previous-invoice hash applies the same transform and'
      ' sha256. Constants recorded in zatca/canonical.go. Applying them still'
      ' needs SA.ZATCA.UBL_FIELD_SET and a validator, which ships in the'
      ' Fatoora SDK.'
WHERE rule_key = 'SA.ZATCA.XML_CANONICALIZATION'
  AND verified_on IS NULL;

-- ---------------------------------------------------------------------------
-- Partly evidenced, still unverified: the QR tag values
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET notes = notes ||
      ' PARTIAL 2026-08-23, Security Features Implementation Standards v1.1'
      ' §4.1. Tags 1-5: length and value are the UTF-8 byte array. Tag 6:'
      ' "Length: length of hash (SHA256 ) is 32 bytes / Value: the byte array'
      ' constituting the value of the field" — the RAW digest, which is what the'
      ' Technical Guideline example appeared to contradict. zatca.QRHash now'
      ' enforces exactly 32 bytes and refuses the base64 text of a digest, which'
      ' is 44 bytes and would otherwise encode into a well-formed, wrong QR.'
      ' STILL OPEN: §4.1 brackets its rules as "[for tags 1 to 5]" and "[for tag'
      ' 6]" and says nothing about tags 7, 8 and 9, so their encoding is'
      ' unresolved. They cannot be produced regardless until a CSID exists.'
WHERE rule_key = 'SA.ZATCA.QR_TAG_VALUE_ENCODING'
  AND notes NOT LIKE '%PARTIAL 2026-08-23%';

-- ---------------------------------------------------------------------------
-- Partly evidenced, still unverified: the CSR subject
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET source_document = 'Security Features Implementation Standards v1.1 (2022-06-24) Table 1',
    source_url = 'https://zatca.gov.sa/ar/E-Invoicing/SystemsDevelopers/Documents/20220624_ZATCA_Electronic_Invoice_Security_Features_Implementation_Standards.pdf',
    notes = notes ||
      ' PARTIAL 2026-08-23. The RDN table IS published, in a fifth document this'
      ' registry had not read: Security Features Implementation Standards v1.1,'
      ' Table 1. All nine inputs are mapped — common_name; alternative_names'
      ' (GUID) for the EGS serial in "1-...|2-...|3-..." form;'
      ' organizationIdentifier 2.5.4.97 (15 digits, begins and ends with 3);'
      ' organizational_unit; organization; country (ISO 3166 Alpha-2);'
      ' businessCategory 2.5.4.15 for the invoice-type map; registeredAddress'
      ' 2.5.4.26 and businessCategory 2.5.4.15 under alternative_names for'
      ' location and industry. It also settles the TSXY/TSCZ conflict this'
      ' registry recorded on 2026-08-19: the functionality map is "TSCZ", with C'
      ' and Z "for future use". STILL UNVERIFIED because the OID carrying'
      ' certificateTemplateName appears nowhere in the document, and a CSR with'
      ' a correct DN and no template extension is refused at onboarding.'
WHERE rule_key = 'SA.ZATCA.CSR_SUBJECT_LAYOUT'
  AND notes NOT LIKE '%PARTIAL 2026-08-23%';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;
