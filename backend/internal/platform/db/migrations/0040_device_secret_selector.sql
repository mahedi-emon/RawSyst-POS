-- 0040 — Find a terminal by its credential without hashing every one.
--
-- 0037 stored only a hash of the device secret, which is right, and made the
-- lookup scan every paired terminal and run argon2 against each. Argon2 is
-- deliberately expensive — 64 MiB and three passes — so that is 64 MiB of work
-- per terminal per request. On a platform with a thousand tills it is a
-- self-inflicted denial of service, and it showed up as a five-fold slowdown in
-- the test suite before it could show up in a shop.
--
-- The fix is the standard one: split the credential into a public SELECTOR that
-- identifies which row to look at, and a secret VERIFIER that is hashed. The
-- selector is indexed and carries no secret — knowing it tells an attacker
-- which terminal a credential belongs to and gives them no way to act as it.
-- One indexed lookup, one hash comparison.
ALTER TABLE device ADD COLUMN secret_selector text;

CREATE UNIQUE INDEX device_secret_selector_uq
  ON device (secret_selector) WHERE secret_selector IS NOT NULL;

COMMENT ON COLUMN device.secret_selector IS
  'Public half of the terminal credential, used only to find the row. The '
  'secret half is hashed into secret_hash and never stored in the clear.';

-- Any terminal paired under 0037 has a hash but no selector, so it can no
-- longer be found. There is exactly one safe answer: it must be paired again.
-- Silently leaving it "active" with an unusable credential would be a till that
-- looks fine and fails at the counter.
UPDATE device
SET status = 'pending', secret_hash = NULL, enrolled_at = NULL
WHERE secret_hash IS NOT NULL AND secret_selector IS NULL;
