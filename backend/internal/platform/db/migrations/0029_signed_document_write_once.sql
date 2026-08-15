-- 0029 — The signed document joins the write-once set.
--
-- Migration 0028 let the terminal fill in `stamp` and `qr_tlv` once. It missed
-- `xml`, which is the document those two belong to and the more important of
-- the three: design 01 §3 has the terminal build the UBL, sign it, derive the
-- QR from the signature, and take the chain hash over the canonical SIGNED
-- XML. The stamp without the document it signs proves nothing.
--
-- Left mutable, `xml` could be replaced after the fact while the stamp stayed
-- fixed — a document swapped underneath a signature that still looks valid.
-- That is precisely the substitution a tamper-evident chain exists to catch.
--
-- All three are now the same rule: absent until the terminal sends them,
-- written exactly once, never rewritten and never cleared.

CREATE OR REPLACE FUNCTION reject_zatca_chain_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- The chain itself stays immutable.
  IF NEW.egs_unit_id    IS DISTINCT FROM OLD.egs_unit_id
     OR NEW.icv            IS DISTINCT FROM OLD.icv
     OR NEW.pih            IS DISTINCT FROM OLD.pih
     OR NEW.invoice_hash   IS DISTINCT FROM OLD.invoice_hash
     OR NEW.schema_version IS DISTINCT FROM OLD.schema_version THEN
    RAISE EXCEPTION
      'An invoice''s position on the e-invoicing chain cannot be changed once '
      'recorded. Correct it with a credit note instead.'
      USING ERRCODE = 'raise_exception';
  END IF;

  -- The signed document, its stamp and its QR payload may each be filled in
  -- ONCE by the terminal that produced them. Never changed, never cleared.
  IF OLD.xml IS NOT NULL AND NEW.xml IS DISTINCT FROM OLD.xml THEN
    RAISE EXCEPTION
      'This invoice already has a signed document and it cannot be replaced. '
      'Replacing it would leave the stamp attached to a different document.'
      USING ERRCODE = 'raise_exception';
  END IF;
  IF OLD.stamp IS NOT NULL AND NEW.stamp IS DISTINCT FROM OLD.stamp THEN
    RAISE EXCEPTION
      'This invoice has already been stamped. A document cannot be re-signed '
      'after the fact.'
      USING ERRCODE = 'raise_exception';
  END IF;
  IF OLD.qr_tlv IS NOT NULL AND NEW.qr_tlv IS DISTINCT FROM OLD.qr_tlv THEN
    RAISE EXCEPTION
      'This invoice already carries a QR payload and it cannot be replaced.'
      USING ERRCODE = 'raise_exception';
  END IF;

  RETURN NEW;
END;
$$;

COMMENT ON COLUMN zatca_invoice.xml IS
  'The canonical signed UBL 2.1 document, as produced ON THE TERMINAL. '
  'Written once when the device uploads it; never rewritten.';
COMMENT ON COLUMN zatca_invoice.qr_tlv IS
  'The TLV QR payload derived from the signature, produced on the terminal. '
  'Written once.';
