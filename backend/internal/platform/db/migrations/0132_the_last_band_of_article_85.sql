-- 0132 — the resignation band nobody could record, and the settlement nobody
-- could ask for.
--
-- ---------------------------------------------------------------------------
-- What was wrong
-- ---------------------------------------------------------------------------
--
-- `SA.EOSB.ENTITLEMENT` has carried three resignation fractions since 0092:
-- under two years, two to five, five to ten. Article 85 bands the fraction by
-- length of service, and a person who resigns after eleven years is in a band
-- the rule cannot express. There was no fourth field, so there was no figure to
-- apply and the software would have had to assume one.
--
-- Assuming it is exactly what this registry exists to prevent. The fourth band
-- is added here as a placeholder like the other five: the shape says what a
-- verifier has to find, and the values still say that nobody has found them.
--
-- Worth being plain about the direction of this change. It does not fill
-- anything in and it does not bring the rule closer to being verified — it adds
-- one more `__VERIFY__`, so the count of unfilled fields goes UP. That is the
-- correct direction: the rule was previously incomplete in a way that would
-- have shown up as a wrong number rather than as a refusal.
--
-- ---------------------------------------------------------------------------
-- Why the payload is edited rather than superseded
-- ---------------------------------------------------------------------------
--
-- Superseding closes a row and opens a new one, and the closed row keeps
-- answering for dates before the change. That is right when a FIGURE changes,
-- because a report re-run for last March must still give March's answer.
--
-- No figure changes here. Every value in this rule is a placeholder, on this
-- date and on every date since 0092, and a placeholder refuses. Superseding
-- would record that the product believed one thing until today and another
-- thereafter, when what it believed throughout is "nobody has read the
-- article". So the SHAPE is corrected in place, the way 0044, 0046, 0059, 0060,
-- 0061 and 0062 correct a shape, and the effective date does not move.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

UPDATE regulatory_rule
SET payload = payload || jsonb_build_object(
      'resignation_fraction_over_ten_years', '__VERIFY__'),
    notes = notes ||
      ' Article 85 bands the resignation fraction by length of service and '
      '0092 recorded three of those bands, so service beyond ten years had no '
      'fraction to apply. 0132 adds the fourth. Nothing is filled in.'
WHERE rule_key = 'SA.EOSB.ENTITLEMENT'
  AND effective_to IS NULL
  AND NOT payload ? 'resignation_fraction_over_ten_years';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;
