-- 0041 — Find an enrolment code without hashing every outstanding one.
--
-- The same flaw 0040 fixed for the device secret, in the place it matters more:
-- redeeming a code is a PUBLIC, unauthenticated endpoint, and it was comparing
-- the submitted code against every open enrolment with argon2 — 64 MiB and
-- three passes each. A handful of concurrent pairings made it slow; a few
-- hundred would make the endpoint a denial-of-service amplifier that anybody on
-- the internet could pull.
--
-- The code is displayed as two groups, "K7QP-4M2X", because that is how a person
-- reads one out. The FIRST group is now the selector — public, indexed, and
-- useless on its own — and only the second is hashed. One indexed lookup, one
-- hash comparison, and the code a cashier types is exactly as long as before.
--
-- Guessing the verifier still means guessing four characters of a 30-symbol
-- alphabet, inside fifteen minutes, against a per-caller attempt limit — and
-- only after guessing the selector that finds the row at all.
ALTER TABLE device_enrolment ADD COLUMN code_selector text;

CREATE INDEX device_enrolment_selector_idx
  ON device_enrolment (code_selector) WHERE redeemed_at IS NULL;

COMMENT ON COLUMN device_enrolment.code_selector IS
  'Public first group of the enrolment code, used only to find the row. The '
  'second group is hashed into code_hash and never stored in the clear.';

-- Codes issued before this cannot be found by selector. They expire within
-- fifteen minutes anyway, and an unredeemable code that still looks live is
-- worse than one that is plainly gone.
DELETE FROM device_enrolment WHERE redeemed_at IS NULL AND code_selector IS NULL;
