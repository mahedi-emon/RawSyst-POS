-- 0038 — A session remembers which terminal it was opened on.
--
-- Sign-in on a paired till issues a device-bound token, and a refresh fifteen
-- minutes later has to issue another one. Without this column the refresh has
-- nothing to bind to and the till silently loses its terminal mid-shift — the
-- next sale would be refused for having no registered terminal, which is the
-- worst possible moment to find out.
--
-- It also gives H3's "assigned cashier" a factual answer: who is signed in on
-- which till, right now, rather than a field somebody has to maintain by hand.
ALTER TABLE user_session
  ADD COLUMN device_id uuid REFERENCES device(id) ON DELETE SET NULL;

CREATE INDEX user_session_device_idx
  ON user_session (device_id) WHERE device_id IS NOT NULL;

COMMENT ON COLUMN user_session.device_id IS
  'The terminal this session was opened on, when it was opened on one. Null '
  'for a browser session in the back office.';
