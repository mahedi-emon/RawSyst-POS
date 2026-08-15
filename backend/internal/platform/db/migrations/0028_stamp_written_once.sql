-- 0028 — A terminal's stamp may be written once.
--
-- zatca_invoice.stamp and qr_tlv were frozen from the moment the row was
-- created, alongside the ICV and the hash chain. That was right for the chain
-- and wrong for these two, and the difference only became visible once
-- submission was built.
--
-- # The sequence the freeze made impossible
--
-- The server allocates the ICV and the PIH — it has to, because they are a
-- per-terminal sequence the device cannot arbitrate alone. The TERMINAL then
-- signs the document with a key the server has never held and never will
-- (E1.3, H1), and sends the stamp back. So the stamp necessarily arrives after
-- the row exists.
--
-- With the column frozen at insert, it could never arrive at all: every invoice
-- created through the online sale path had a NULL stamp forever, and a NULL
-- stamp cannot be submitted. Nothing would ever have reached ZATCA, and the
-- backlog would have grown with no cause anyone could see in the code.
--
-- # Write once, never rewrite
--
-- The freeze still holds in the direction that matters. A stamp may go from
-- absent to present exactly once; it can never be changed afterwards, and it
-- can never be erased. Rewriting a stamp would let a document be re-signed
-- after the fact, which is precisely what a tamper-evident chain exists to
-- prevent.

DROP TRIGGER IF EXISTS zatca_invoice_frozen_chain ON zatca_invoice;

CREATE OR REPLACE FUNCTION reject_zatca_chain_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- The chain itself is immutable, exactly as before.
  IF NEW.egs_unit_id  IS DISTINCT FROM OLD.egs_unit_id
     OR NEW.icv          IS DISTINCT FROM OLD.icv
     OR NEW.pih          IS DISTINCT FROM OLD.pih
     OR NEW.invoice_hash IS DISTINCT FROM OLD.invoice_hash
     OR NEW.schema_version IS DISTINCT FROM OLD.schema_version THEN
    RAISE EXCEPTION
      'An invoice''s position on the e-invoicing chain cannot be changed once '
      'recorded. Correct it with a credit note instead.'
      USING ERRCODE = 'raise_exception';
  END IF;

  -- The stamp and the QR payload may be filled in ONCE, by the terminal that
  -- signed the document. Never changed, never cleared.
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

CREATE TRIGGER zatca_invoice_frozen_chain
  BEFORE UPDATE ON zatca_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_zatca_chain_change();

COMMENT ON COLUMN zatca_invoice.stamp IS
  'The terminal''s cryptographic stamp, written once after the device signs. '
  'The signing key never reaches this server (E1.3, H1).';
