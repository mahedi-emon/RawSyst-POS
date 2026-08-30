-- 0076 locked `password_reset_request` so tightly that nothing could write to
-- it, including the code that needs to.
--
-- The intent was right and is worth restating: no tenant session may read these
-- rows. A person with a valid login could otherwise read the hash of a code
-- issued to a colleague, and while a hash is not a code, it is an offline
-- guessing target against a six-digit space — a million candidates, which is
-- minutes of work.
--
-- The expression was wrong. `FORCE ROW LEVEL SECURITY` with NO policy denies
-- everybody, and "everybody" includes the platform connection the recovery flow
-- runs on. The first request for a reset code failed with 42501.
--
-- # What the policy says
--
-- `current_tenant_id() IS NULL` is true in exactly one situation: a connection
-- that has not set `app.tenant_id`, which is what `TxAsPlatform` opens and what
-- nothing else in this product uses. Every tenant session sets it, so every
-- tenant session sees no rows and can write none.
--
-- That is the same boundary 0076 described, said in a way the database can act
-- on rather than one that denies the application too.

CREATE POLICY password_reset_request_platform_only
  ON password_reset_request
  USING (current_tenant_id() IS NULL)
  WITH CHECK (current_tenant_id() IS NULL);

COMMENT ON POLICY password_reset_request_platform_only ON password_reset_request IS
  'Reachable only from a connection with no app.tenant_id set — the platform '
  'plane. A tenant session must not be able to read a colleague''s code hash.';
