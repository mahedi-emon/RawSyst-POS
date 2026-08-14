-- 0007 — Refresh token chain, for rotation with reuse detection.
--
-- Migration 0003 stored one refresh_token_hash directly on user_session and
-- overwrote it on every rotation. That silently defeated reuse detection: once
-- rotated, the old hash no longer existed anywhere, so replaying a stolen token
-- looked identical to presenting a garbage one. The integration test caught it.
--
-- The distinction matters. A token that simply does not exist is noise — a
-- typo, an old bookmark. A token that DID exist and has already been used is
-- evidence that a copy was captured, because the legitimate client always
-- discards its token the moment it exchanges it. Only the thief presents a
-- spent token.
--
-- So each issued token gets a row. Using one marks it spent and issues the
-- next. Presenting a spent token revokes the entire session family, which
-- matters because refusing only the replay would leave the thief's newer token
-- working.

CREATE TABLE session_refresh_token (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  uuid NOT NULL REFERENCES user_session(id) ON DELETE CASCADE,
  tenant_id   uuid REFERENCES tenant(id) ON DELETE CASCADE,

  token_hash  text        NOT NULL,
  issued_at   timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,

  -- NULL until exchanged. Non-NULL on a presented token means reuse.
  used_at     timestamptz,

  -- Ordinal within the chain, for forensics: how many rotations happened
  -- before the reuse was detected.
  generation  integer NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX session_refresh_token_hash_uq ON session_refresh_token (token_hash);
CREATE INDEX session_refresh_token_session_idx    ON session_refresh_token (session_id);

-- Tokens are retained after use rather than deleted. A deleted row cannot
-- distinguish "never existed" from "already spent", which is the whole point
-- of the table. A retention job prunes them well after the session expires.
CREATE TRIGGER session_refresh_token_no_delete
  BEFORE DELETE ON session_refresh_token
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE session_refresh_token ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_refresh_token FORCE  ROW LEVEL SECURITY;
CREATE POLICY session_refresh_token_isolation ON session_refresh_token
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- The column on user_session is now redundant. Dropping it removes the chance
-- of two sources of truth disagreeing about which token is current.
ALTER TABLE user_session DROP COLUMN refresh_token_hash;
