-- 0110 — The United States levies no federal sales tax, recorded as a fact.
--
-- 0106 made a rate resolve by walking a shop's jurisdiction to its country root
-- and summing every authority's share. Until now an authority with no rate row
-- was skipped silently, which meant a chain with a city rate loaded and a state
-- rate not yet loaded produced a total that looked fine and was short by the
-- state's share. registry.JurisdictionRate now refuses that chain and names the
-- authority that has not answered.
--
-- That correctness fix has a consequence this migration settles: the country
-- root is in the chain like any other authority, so 'US' having no row would
-- refuse every American sale for ever.
--
-- # Zero is a statement, absence is not
--
-- The whole point of the change above is that "nobody recorded a rate" and
-- "this authority charges nothing" are different facts, and only the second one
-- is something a person asserts. The United States has no federal sales or use
-- tax — sales tax in the US is levied by states and their subdivisions, which is
-- the entire reason this hierarchy exists — so the assertion is made here,
-- explicitly, as 0.
--
-- # Still unverified, on purpose
--
-- `verified_on` is left NULL like every other rate this repository ships. Not
-- because the fact is in doubt, but because the verification column means "an
-- operator of this deployment checked this against the authority and put their
-- name to it", and nobody has. The Platform Owner verifies it in the same pass
-- as the state and district rates for whichever states they actually sell in.

INSERT INTO tax_jurisdiction_rate
  (jurisdiction_id, treatment, rate, effective_from,
   source_authority, source_document, notes)
SELECT j.id, 'taxable', 0, '1900-01-01',
       'none',
       'No federal sales or use tax exists in the United States; sales tax is '
       'levied by the states and their subdivisions.',
       'Recorded as an explicit zero rather than left absent, because '
       'JurisdictionRate refuses a chain in which any authority has no rate on '
       'file. Absence means "not yet loaded" and refuses the sale; zero means '
       '"this authority levies nothing" and is a statement. Left unverified '
       'like every rate shipped here: verification records that an operator '
       'checked it, and that has not happened yet.'
FROM tax_jurisdiction j
WHERE j.country = 'us' AND j.level = 'country' AND j.code = 'US'
ON CONFLICT DO NOTHING;
