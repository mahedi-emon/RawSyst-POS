-- 0104 — How a counter proves which counter it is.
--
-- # The model
--
--   business (tenant) -> company -> store/shop -> COUNTER -> session -> sale
--
-- A counter is a `device` row and stays one. There is no second table and no
-- second POS: a till in the desktop app and a till in a browser tab are the
-- same object, sell through the same service, take the same shift, move the
-- same stock and land in the same audit trail. What differs is only how a
-- session on that counter is authorised, and that is what this column records.
--
-- | binding   | who may open a session on it | proves itself with |
-- |-----------|------------------------------|--------------------|
-- | `session` | any user the RBAC scope allows | their own signed-in session |
-- | `paired`  | the enrolled machine only      | the device secret in the OS keystore |
--
-- # Why the web needs its own answer
--
-- Until now every counter was `paired`: it enrolled with a single-use code and
-- received a secret which lives in the terminal's OS keystore, and the sale
-- routes read the device from `did` in the token. A browser has no keystore.
-- The two ways to give it one are both wrong — put the secret in browser
-- storage (which `credential.ts` refuses on purpose, in a comment naming it as
-- how a secret reaches production), or have the browser pretend to be a machine
-- it is not.
--
-- So a `session` counter does not try. It is not a machine that proves itself;
-- it is a named till that an authorised person opens, and the authority is the
-- person's own session and permissions. That is the ordinary web-POS posture
-- and it is honest about what it checks.
--
-- **This is weaker than `paired` and deliberately so.** A stolen user session
-- can open any counter that user's scope allows, where a stolen user session
-- could not previously ring up a sale at all. What it is not weaker at:
-- everything else still applies — tenant isolation, the permission on every
-- route, company and store scope, the open-shift requirement, and an audit
-- trail naming the user AND the counter.
--
-- # The upgrade path is the same API
--
-- A counter is not stuck with the answer it was created with. Enrolling one —
-- the existing flow, unchanged — moves it to `paired`, so a shop can run on
-- browsers today and put a locked-down desktop till on the busy counter later
-- without re-creating it, losing its history, or breaking its chain. That is
-- the whole reason this is a column on `device` and not a separate kind of
-- thing.
--
-- # Existing counters are untouched
--
-- Every device that exists today was registered under the paired model, so the
-- column is ADDED with `paired` as its default — which fills every existing row
-- without an UPDATE — and the default is only then changed to `session` for
-- what gets created from here.
--
-- Doing it in that order is not a trick for its own sake: `device` is ENABLE +
-- FORCE row-level security and the migration connection carries no tenant, so
-- an `UPDATE device SET ...` would match zero rows, silently, and leave the
-- backfill undone. A column default is applied by the table rewrite rather than
-- by a query, so it reaches every row regardless of what any policy would let
-- this connection see. (0103 met the same wall the other way and had to lift
-- FORCE for its transaction.)

ALTER TABLE device
  ADD COLUMN binding text NOT NULL DEFAULT 'paired';

ALTER TABLE device
  ALTER COLUMN binding SET DEFAULT 'session';

ALTER TABLE device
  ADD CONSTRAINT device_binding_known
    CHECK (binding IN ('session', 'paired'));

-- A session counter has no secret, and cannot be allowed to acquire one while
-- still calling itself session-bound: that would be a counter a browser may
-- claim AND a machine may authenticate as, which is the weaker of the two
-- authorities silently governing the stronger one.
--
-- `Enrol` therefore sets `binding = 'paired'` in the same statement that writes
-- the secret. This constraint is what makes forgetting that a failed write
-- rather than a quiet hole.
ALTER TABLE device
  ADD CONSTRAINT device_session_binding_holds_no_secret
    CHECK (binding <> 'session' OR secret_hash IS NULL);

COMMENT ON COLUMN device.binding IS
  'How a session on this counter is authorised: ''session'' — any user whose '
  'RBAC scope allows it, proved by their own sign-in; ''paired'' — only the '
  'enrolled machine, proved by the device secret in its OS keystore. '
  'Enrolling a counter moves it from session to paired.';
