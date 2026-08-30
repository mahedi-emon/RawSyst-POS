-- Self-service password recovery, which A4.2 names and nothing implemented.
--
-- Blueprint A4.2 describes two routes back into a locked-out account, in order:
-- the Owner "first attempts normal self-service recovery (recovery email / OTP
-- to registered phone)", and only "if self-service recovery fails or is
-- unavailable" do they contact Super Admin. The second half was built. The
-- first was not, which made a forgotten password a phone call to the vendor —
-- for every shop, at every hour, forever.
--
-- # Why the code is hashed
--
-- It is a credential. For the ten minutes it is alive it is exactly as good as
-- the password it replaces, and a leaked database backup that yields live reset
-- codes is a leaked database backup that yields accounts. The same argument A4.2
-- makes about passwords — "an irreversible hash, a security requirement, not
-- just a policy choice" — applies to anything that can be exchanged for one.
--
-- # Why the row is kept after use
--
-- `used_at` rather than DELETE. A deleted row cannot distinguish "this code
-- never existed" from "this code was already spent", and the second is the
-- shape of an attack: somebody replaying a code they intercepted. Keeping it
-- lets the exchange refuse a replay differently from a guess, and lets the
-- pruner in 0075 clear it later on the ordinary schedule.

CREATE TABLE password_reset_request (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Tenant-scoped like every other row about a person. A Super Admin
  -- (tenant_id IS NULL) recovers through the documented emergency procedure in
  -- A4.1, not through this.
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,

  -- argon2id, like a password. Never the code itself.
  code_hash   text NOT NULL,

  requested_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,

  -- Non-NULL once exchanged. A second exchange of the same code is a replay,
  -- and is refused differently from a wrong code.
  used_at      timestamptz,

  -- Wrong guesses against THIS code. Five and it is dead — a six-digit code
  -- has a million values, and unlimited guessing turns ten minutes into enough
  -- time to walk a meaningful fraction of them.
  attempts     integer NOT NULL DEFAULT 0,

  -- Where the request came from, for the audit trail. A4.2 requires assisted
  -- recovery to record "who requested it, who approved it, timestamp, IP"; a
  -- self-service reset is the same event without the approver.
  requested_ip inet,

  CONSTRAINT prr_attempts_non_negative CHECK (attempts >= 0),
  CONSTRAINT prr_window_ordered CHECK (expires_at > requested_at)
);

-- The lookup on exchange: the newest live request for a user.
CREATE INDEX password_reset_request_live_idx
  ON password_reset_request (user_id, requested_at DESC)
  WHERE used_at IS NULL;

-- The rate-limit count, which asks "how many in the last hour".
CREATE INDEX password_reset_request_recent_idx
  ON password_reset_request (user_id, requested_at DESC);

ALTER TABLE password_reset_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE password_reset_request FORCE  ROW LEVEL SECURITY;

-- Deliberately NO tenant policy, and therefore no tenant access.
--
-- Every other table here is readable inside its tenant. This one must not be:
-- a person with a valid session in the tenant could otherwise read the hash of
-- a reset code issued to somebody else, and while a hash is not the code, it is
-- an offline guessing target against a six-digit space. The recovery flow runs
-- unauthenticated by definition, so it goes through the platform plane, and
-- FORCE with no permissive policy means nothing else reaches it at all.

-- Widen the pruner from 0075 to cover this table too.
--
-- Spent and expired reset requests are exactly the churn that migration is
-- about: read by nobody, kept only long enough to tell a replay from a guess.
CREATE OR REPLACE FUNCTION prune_expired_credentials(p_grace interval)
RETURNS integer
LANGUAGE sql VOLATILE AS $$
  WITH tokens AS (
    DELETE FROM session_refresh_token
    WHERE expires_at < now() - p_grace
    RETURNING 1
  ), sessions AS (
    DELETE FROM user_session s
    WHERE s.expires_at < now() - p_grace
      AND NOT EXISTS (
        SELECT 1 FROM session_refresh_token t WHERE t.session_id = s.id)
    RETURNING 1
  ), resets AS (
    DELETE FROM password_reset_request
    WHERE expires_at < now() - p_grace
    RETURNING 1
  )
  SELECT (SELECT count(*) FROM tokens)
       + (SELECT count(*) FROM sessions)
       + (SELECT count(*) FROM resets)
$$;

COMMENT ON TABLE password_reset_request IS
  'Self-service password recovery (blueprint A4.2). Codes are hashed; rows are '
  'kept after use so a replay is distinguishable from a guess. No RLS policy '
  'by design: nothing inside a tenant may read these.';
