-- 0037 — Pairing a terminal, per blueprint H3.
--
-- The device table has existed since 0002 with a `pending` status that nothing
-- could ever move out of: there was no way for a terminal to prove it should be
-- trusted, so every POS route that resolves the till from `a.DeviceID` was
-- unreachable by a real terminal. This is that missing half.
--
-- # How a terminal is paired
--
-- The Owner creates the terminal in Device Management and the back office shows
-- a short, single-use, expiring enrolment code. Somebody types it into the new
-- till, which claims the pending device and receives its own long-lived secret.
--
-- Chosen over the alternatives because nothing long-lived is ever shared: the
-- code dies on first use and expires on its own, so a code read over the phone
-- or left on a screen stops being useful within minutes. It also mirrors the
-- shape of ZATCA's own CSID onboarding, which issues an OTP from the taxpayer
-- portal — one fewer unfamiliar idea for a shop to learn.
--
-- # What the terminal holds afterwards
--
-- An opaque secret, stored by Tauri in the OS keystore (Windows DPAPI) exactly
-- as 01-invoice-zatca-engine.md §7 requires of the CSID key: never in the
-- SQLite file, never in .env, never in a log. Only its HASH is stored here, for
-- the same reason a password is — a database copy must not yield working
-- terminal credentials.
--
-- The secret is not itself an access token. It is exchanged for a short-lived
-- device-bound one, and the exchange re-reads the device's status every time.
-- That is what makes revocation immediate: an Owner who revokes a stolen till
-- at 10:00 has it locked out at 10:00, not whenever a long-lived token would
-- have expired.
--
-- # What this migration does NOT do
--
-- No CSID, no key generation, no ZATCA onboarding call. E1.3 puts the signing
-- key on the terminal and the P1 verification gate is still open, so the
-- columns for it (csid_serial, csid_issued_at, csid_expires_at) stay exactly as
-- 0002 left them: present, and untouched. Pairing a terminal is not onboarding
-- it for e-invoicing, and the two must not be conflated.

-- ---------------------------------------------------------------------------
-- The terminal's own credential
-- ---------------------------------------------------------------------------

ALTER TABLE device
  -- Only the hash. See the header: a database copy must not yield a working
  -- terminal.
  ADD COLUMN secret_hash    text,
  ADD COLUMN enrolled_at    timestamptz,
  -- Who paired it and from where, because "when did this till appear and who
  -- authorised it" is the first question asked after a terminal is found doing
  -- something it should not.
  ADD COLUMN enrolled_by    uuid REFERENCES app_user(id),
  ADD COLUMN enrolled_ip    inet,
  ADD COLUMN revoked_at     timestamptz,
  ADD COLUMN revoked_by     uuid REFERENCES app_user(id),
  -- Free text, shown in Device Management. A revocation nobody can explain a
  -- month later is a revocation somebody will undo.
  ADD COLUMN revoked_reason text;

COMMENT ON COLUMN device.secret_hash IS
  'Argon2id hash of the terminal enrolment secret. The secret itself is shown '
  'once at enrolment and then lives only in the terminal OS keystore.';

-- A paired device must have a secret and an enrolment record; an unpaired one
-- must have neither. Representing the two states loosely is how a terminal ends
-- up active with nothing behind it.
ALTER TABLE device ADD CONSTRAINT device_enrolment_coherent CHECK (
  (secret_hash IS NULL     AND enrolled_at IS NULL) OR
  (secret_hash IS NOT NULL AND enrolled_at IS NOT NULL)
);

-- Revoked is terminal, and it must say when. Reassignment, renaming and
-- deactivation are all reversible; revocation is the one that is not, because
-- 01 §7 pairs it with destroying the CSID key on next start.
ALTER TABLE device ADD CONSTRAINT device_revocation_dated CHECK (
  (status <> 'revoked' AND revoked_at IS NULL) OR
  (status =  'revoked' AND revoked_at IS NOT NULL)
);

-- ---------------------------------------------------------------------------
-- The enrolment code
-- ---------------------------------------------------------------------------

-- One row per attempt to pair one device. Kept after use rather than deleted:
-- it is the audit record of who authorised a terminal, and H3 gives an Owner
-- the right to ask.
CREATE TABLE device_enrolment (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  device_id   uuid NOT NULL REFERENCES device(id) ON DELETE CASCADE,

  -- The hash of the code, never the code. It is shown once, in the back office,
  -- to the person who created it.
  code_hash   text NOT NULL,

  -- Short by design. A code that lives for a day is a code that gets written on
  -- a sticky note; fifteen minutes is long enough to walk to the till.
  expires_at  timestamptz NOT NULL,

  -- Single use. Set the moment it is redeemed, so a second attempt with the
  -- same code is refused even if it arrives a millisecond later.
  redeemed_at timestamptz,

  -- Wrong codes are counted so a till cannot be used to guess one. The code is
  -- short enough to be typed, which means it is short enough to be brute
  -- forced without this.
  attempts    integer NOT NULL DEFAULT 0,

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),

  CONSTRAINT device_enrolment_attempts_sane CHECK (attempts >= 0)
);

-- A device has at most ONE code outstanding. Issuing a new one supersedes the
-- old, which is enforced by the service deleting first — two live codes for one
-- terminal would mean revoking one achieved nothing.
CREATE UNIQUE INDEX device_enrolment_live
  ON device_enrolment (device_id) WHERE redeemed_at IS NULL;

CREATE INDEX device_enrolment_tenant_idx ON device_enrolment (tenant_id);
-- The lookup the redeem path makes, on a code that has not been used yet.
CREATE INDEX device_enrolment_open_idx
  ON device_enrolment (expires_at) WHERE redeemed_at IS NULL;

ALTER TABLE device_enrolment ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_enrolment FORCE  ROW LEVEL SECURITY;
CREATE POLICY device_enrolment_isolation ON device_enrolment
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- devices.view   — see the terminals in this business and their health
-- devices.manage — add, rename, reassign, deactivate and revoke terminals, and
--                  issue enrolment codes
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  -- H3 gives these operations to the Owner by name.
  ('owner', 'devices.view'),
  ('owner', 'devices.manage'),

  -- A store manager runs the shop floor, so they see the tills and can pair a
  -- replacement when one dies mid-trade. Revoking is deliberately theirs too:
  -- a till stolen from the counter must not wait for an owner to answer their
  -- phone.
  ('store_manager', 'devices.view'),
  ('store_manager', 'devices.manage'),

  -- An auditor reads, and reads everything.
  ('auditor', 'devices.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- Cloned into every tenant that already exists, exactly as 0032 does, so a
-- business created before this migration gets the new verbs on its existing
-- roles rather than only on ones created later.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN role template ON template.id = r.cloned_from
JOIN (VALUES
  ('owner', 'devices.view'),
  ('owner', 'devices.manage'),
  ('store_manager', 'devices.view'),
  ('store_manager', 'devices.manage'),
  ('auditor', 'devices.view')
) AS p(role_key, permission) ON template.key = p.role_key
WHERE r.tenant_id IS NOT NULL
ON CONFLICT DO NOTHING;
