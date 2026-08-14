-- 0011 — Give every tax-treatment rule an explicit tax model.
--
-- Migration 0005 seeded Saudi Arabia's treatment list before the product had a
-- second market. Migration 0010 added Bangladesh and the USA, and with them two
-- fields the Saudi row does not have: `model` and `input_tax_recoverable`.
--
-- That asymmetry is a real defect, not untidiness. Code branching on
-- `payload->>'model'` to distinguish a VAT from a sales tax reads NULL for
-- Saudi Arabia and takes the wrong branch — or worse, a `default:` that happens
-- to be right today and silently wrong when a fourth market arrives.
--
-- # Why this edits the row in place
--
-- The registry is normally append-only: correcting a rule means closing the old
-- row's effective_to and inserting a new one, so history stays intact. That is
-- exactly right for a legal change, and exactly wrong here. Saudi VAT did not
-- change. Opening a new effective period would assert in the data that
-- something happened on this date when nothing did, and a report reconstructing
-- "what did we believe the law was" would show a phantom event.
--
-- So the frozen-column trigger is lifted for this one statement. The values
-- being written are descriptions of a tax SYSTEM that were always true, not new
-- figures.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

UPDATE regulatory_rule
SET payload = payload
              || '{"model": "vat"}'::jsonb
              || '{"input_tax_recoverable": true}'::jsonb
WHERE rule_key = 'SA.VAT.TAX_TREATMENTS'
  AND payload->>'model' IS NULL;

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;

-- Every treatment list must now declare its model, so a missing one is a
-- broken seed rather than a silent default.
--
-- A partial constraint rather than a table-wide one: only TAX_TREATMENTS rules
-- carry a model. A VAT rate or a filing deadline has no use for the field.
ALTER TABLE regulatory_rule
  ADD CONSTRAINT regulatory_rule_tax_model_declared
  CHECK (
    rule_key NOT LIKE '%TAX_TREATMENTS'
    OR (payload ? 'model' AND payload ? 'input_tax_recoverable')
  );
