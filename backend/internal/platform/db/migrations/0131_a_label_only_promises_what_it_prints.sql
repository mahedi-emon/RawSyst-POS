-- A line on a label that prints nothing.
--
-- The loyalty card seeded by 0095 — and by `provisioning.SeedLabelStudio` for
-- every company created since — carries four lines: a logo, the customer's
-- name, a `tier`, and the barcode. The studio's renderer has a case for the
-- first, the second and the fourth, and no case at all for `tier`. Nor could
-- it have one: `labels.Label` is built from a variant and carries no tier, so
-- there is nothing to put on that line even if something drew it.
--
-- So the template promised a loyalty tier and printed a blank. Nobody noticed,
-- because a missing line on a label is invisible until somebody counts the
-- lines — which is exactly the failure the label studio's own comment warns
-- about: the layout is right, a line is silently absent, and the discovery
-- happens on a rail of nine hundred printed cards.
--
-- `labels.PrintableFields` now refuses a field the printer cannot fill, and
-- refusing one that this product's own seed put there would leave a shop
-- unable to save its own loyalty card. So the seed is corrected here, in the
-- data, rather than the rule being relaxed to accommodate it.
--
-- Written as a jsonb filter rather than a literal replacement so a shop that
-- has since edited its card keeps every other change it made. A template with
-- no `tier` line is left exactly as it is.

UPDATE label_template
SET fields = (
      SELECT coalesce(jsonb_agg(line ORDER BY ord), '[]'::jsonb)
      FROM jsonb_array_elements(fields) WITH ORDINALITY AS t(line, ord)
      WHERE line->>'field' IS DISTINCT FROM 'tier'
    )
WHERE fields @> '[{"field":"tier"}]'::jsonb;

COMMENT ON COLUMN label_template.fields IS
  'Which lines are printed, in order. Every entry names one of the fields '
  'labels.PrintableFields lists; anything else is refused, because a label '
  'that asks for something the print run cannot fill prints a blank line and '
  'says nothing about it.';
