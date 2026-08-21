-- 0056 — P35: the template a client writes their own documents with, blueprint I2.
--
-- I2 makes the logo, header, footer, return-policy text, tax numbers and
-- Arabic/English content blocks customizable per template type. 0054 built the
-- logo half and could go no further: the only document surface in the product
-- was a 42-column plain-text thermal receipt that cannot carry an image. UI
-- spec §5 has since built one that can, so the rest of I2 has somewhere to land.
--
-- # One row per company per document type
--
-- I2 is explicit that customization is PER TEMPLATE TYPE — a credit note may
-- carry a different footer from an invoice, and usually should, because one is
-- an apology and the other is a demand. The primary key is the pair.
--
-- The types are the four `sales_invoice.doc_type` values and no more. I2 lists
-- nine, and the other five — quotation, purchase order, delivery challan,
-- payment receipt, customer statement — are documents this product does not
-- issue yet. A template for a document that cannot be printed is configuration
-- for nothing, which is the mistake 0054 was careful to admit to rather than
-- repeat. They arrive with the documents.
--
-- # What a template may and may not change
--
-- It carries PRESENTATION only: the blocks a shop writes for itself. It cannot
-- reach the figures, the parties, the tax numbers or the dates — those come off
-- the invoice row, which is immutable posted history. That line is what keeps
-- this safe: no setting here, changed at any time, can alter what a document
-- says about the transaction it recorded. A footer changed today appears on a
-- reprint of last year's invoice, and that is correct — a reprint is a copy of
-- a record on today's stationery, not a reissue of the record.
--
-- # QR placement is deliberately absent
--
-- I2 lists it. The QR is produced by the terminal when it signs, and signing is
-- an open release blocker, so a placement setting would be a control over
-- something that cannot yet exist. It arrives with the QR.

CREATE TABLE document_template (
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Mirrors sales_invoice.doc_type. Restated rather than referenced because a
  -- template may exist for a type before any document of it has been issued.
  doc_type    text NOT NULL,

  -- The blocks I2 names. Each in both languages, because I2 asks for
  -- "Arabic/English content blocks" and a Saudi tax invoice carries both — and
  -- because a shop that writes only one should have the other stay empty
  -- rather than be filled with a machine translation nobody approved.
  header_text       text NOT NULL DEFAULT '',
  header_text_ar    text NOT NULL DEFAULT '',
  footer_text       text NOT NULL DEFAULT '',
  footer_text_ar    text NOT NULL DEFAULT '',
  return_policy     text NOT NULL DEFAULT '',
  return_policy_ar  text NOT NULL DEFAULT '',
  payment_terms     text NOT NULL DEFAULT '',
  payment_terms_ar  text NOT NULL DEFAULT '',

  -- Whether the company's own mark appears, or the document falls back to the
  -- RawSyst one. A client who has uploaded a logo and wants a plain document
  -- for a particular type should not have to delete the logo to get one.
  show_logo        boolean NOT NULL DEFAULT true,

  -- Tax numbers are a legal requirement on a Saudi standard invoice and noise
  -- on a receipt for a bottle of water, which is why I2 makes it per type.
  show_tax_number  boolean NOT NULL DEFAULT true,

  updated_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (company_id, doc_type),

  CONSTRAINT document_template_type_valid CHECK (doc_type IN (
    'standard', 'simplified', 'credit_note', 'debit_note')),

  -- A block long enough to be a document in its own right is a mistake rather
  -- than a return policy, and it would be reprinted on every copy.
  CONSTRAINT document_template_blocks_sane CHECK (
    length(header_text)      <= 500 AND length(header_text_ar)     <= 500 AND
    length(footer_text)      <= 500 AND length(footer_text_ar)     <= 500 AND
    length(return_policy)    <= 2000 AND length(return_policy_ar)  <= 2000 AND
    length(payment_terms)    <= 500 AND length(payment_terms_ar)   <= 500)
);

ALTER TABLE document_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE document_template FORCE  ROW LEVEL SECURITY;

CREATE POLICY document_template_isolation ON document_template
  USING (tenant_id = current_tenant_id());

-- Deliberately NOT on the platform plane, like company_logo: what a client
-- writes on their own invoices is not the platform operator's business, and
-- 0006's rule is that business tables carry no platform predicate.

COMMENT ON TABLE document_template IS
  'Blueprint I2 / P35: the presentation blocks a client writes for each '
  'document type. Carries no figure, party or date — those are the '
  'document''s own, and immutable.';
