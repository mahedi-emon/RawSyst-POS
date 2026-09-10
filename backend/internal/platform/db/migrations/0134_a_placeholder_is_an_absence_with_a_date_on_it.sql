-- 0134 — a placeholder is an absence with a date on it.
--
-- ---------------------------------------------------------------------------
-- The thing that could not be done
-- ---------------------------------------------------------------------------
--
-- `SA.EOSB.ENTITLEMENT` was seeded by 0092 with every field `__VERIFY__` and an
-- effective date of 2026-01-01. Articles 84 and 85 of the Labour Law have been
-- in force since 27 September 2005.
--
-- So when the Ministry's own publication is retrieved and read, the figures it
-- states want to be recorded from 2005-09-27 — and cannot be. `regulatory_rule`
-- refuses two rows for the same rule whose date ranges overlap, which is the
-- constraint that makes "what did we believe the law was in March" answerable
-- at all, and the placeholder occupies 2026-01-01 onwards. Recording the real
-- figures from any earlier date collides with it. Recording them from the same
-- date collides too: `RecordRule` closes off a row that starts BEFORE the new
-- one, and this one starts on the same day.
--
-- The correction workflow could not correct the one kind of row it exists for.
--
-- ---------------------------------------------------------------------------
-- Why the answer is a delete, and why that is not a hole in the trail
-- ---------------------------------------------------------------------------
--
-- History here is append-only for a reason worth restating: a report re-run for
-- last March must give March's answer, so a figure that governed a period is
-- kept for ever and a correction supersedes it rather than overwriting.
--
-- A placeholder governed nothing. `__VERIFY__` does not compute; every use of it
-- is refused by name at the point of use. There is no report it produced, no
-- payslip it priced and no return it filed. It is not a figure this product
-- believed — it is the record that this product had been told nothing, wearing
-- a date because the table requires one.
--
-- Superseding it would therefore write a falsehood into the trail: "we believed
-- one thing until this date and another after it", when what was believed
-- throughout was nothing at all. 0132 made exactly this argument in SQL when it
-- corrected the shape of the same rule in place.
--
-- So a row may be deleted if, and only if, every one of these holds:
--
--   * its payload still contains the placeholder marker
--   * nobody has ever verified it
--   * no source document was ever applied to produce it
--
-- A figure remains undeletable, for ever, exactly as before. The narrowness is
-- the point: this is not a delete capability, it is the recognition that an
-- absence was never a record.

CREATE OR REPLACE FUNCTION regulatory_rule_reject_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.payload::text LIKE '%__VERIFY__%'
     AND OLD.verified_on IS NULL
     AND OLD.source_document_id IS NULL
  THEN
    -- An unfilled placeholder that nothing was ever computed from. Retiring it
    -- is what recording the real figure means.
    RETURN OLD;
  END IF;

  RAISE EXCEPTION
    'A regulatory rule that has ever held a figure cannot be deleted. A '
    'report re-run for an earlier period must still give that period''s '
    'answer, so a correction is recorded as a new rule from a new date and '
    'the old one is closed, never removed.';
END $$;

DROP TRIGGER regulatory_rule_no_delete ON regulatory_rule;

CREATE TRIGGER regulatory_rule_no_delete
  BEFORE DELETE ON regulatory_rule
  FOR EACH ROW EXECUTE FUNCTION regulatory_rule_reject_delete();

COMMENT ON FUNCTION regulatory_rule_reject_delete() IS
  'Refuses to delete any rule that has ever held a figure. Permits deleting an '
  'unverified placeholder that nothing was ever computed from, because such a '
  'row is the absence of a rule rather than a rule.';
