-- 0064 — Where the credentials ZATCA issues are kept.
--
-- # What was missing
--
-- `egs_unit` carried csid_serial, csid_issued_at, csid_expires_at and
-- csid_status since 0013 — the metadata — but nowhere to put the things those
-- describe. Onboarding produces three artefacts that must survive a restart:
--
--   * the compliance certificate ZATCA returns for the CSR (binarySecurityToken)
--   * the CSID itself, which is the USERNAME for every subsequent API call
--   * the secret ZATCA returns with it, which is the PASSWORD
--
-- Without somewhere to keep them, onboarding could complete and the next
-- process could not report a single invoice.
--
-- # What is deliberately NOT here
--
-- The device STAMPING key. docs/system-design/01-invoice-zatca-engine.md §7
-- settles this and marks it locked: the key pair is generated on the terminal,
-- held in Windows DPAPI through Tauri's native layer, and never leaves the
-- device. The same table gives the cloud "onboarding credentials and the
-- compliance-CSID request flow only".
--
-- So there is no private_key column, and its absence is the design rather than
-- an omission to be filled in later. A future hardware-module architecture
-- would add a key REFERENCE, never key material.
--
-- # Why the secret is bytea and not text
--
-- Because it is ciphertext, not a string. internal/platform/secrets seals it
-- with AES-256-GCM and the sealed value is version || nonce || ciphertext —
-- arbitrary bytes that would be corrupted by any encoding conversion a text
-- column invites.
--
-- # Why a separate table rather than more columns on egs_unit
--
-- Two reasons. A unit holds a DIFFERENT credential per environment, and
-- sandbox onboarding must never be able to overwrite the production one — that
-- is a unique constraint here and would be an unenforceable convention there.
-- And the row can then be denied to every ordinary query by RLS and read only
-- by the paths that submit, instead of riding along in every SELECT * that
-- touches a till.

CREATE TABLE zatca_credential (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)   ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id)  ON DELETE CASCADE,
  egs_unit_id uuid NOT NULL REFERENCES egs_unit(id) ON DELETE RESTRICT,

  -- Which ZATCA stack issued this. Sandbox credentials are useless against
  -- production and vice versa, and mixing them produces authentication errors
  -- that read like a broken integration.
  environment text NOT NULL,

  -- Compliance comes first and can only check invoices; production is what
  -- reports and clears real ones. A unit holds both at different times, and
  -- the compliance one is kept after promotion so a renewal can be traced.
  kind text NOT NULL,

  -- The certificate as ZATCA returned it, DER. Not a secret -- it is public by
  -- construction and travels inside every signed invoice -- but stored here so
  -- the signer does not have to re-derive it.
  certificate bytea,

  -- The CSID: username for HTTP Basic against the reporting and clearance
  -- endpoints. Not secret on its own, so it is readable for display.
  csid text,

  -- The password. Sealed by internal/platform/secrets, never selected into a
  -- response, never logged.
  secret_sealed bytea,

  -- Which encryption key version sealed it, so an operator can see what is
  -- still to rotate without decrypting anything.
  secret_key_version smallint,

  -- The CSR that was sent, kept for support: when ZATCA rejects an onboarding
  -- the first question is always what was actually in the request. Public.
  csr text,

  requested_at timestamptz NOT NULL DEFAULT now(),
  issued_at    timestamptz,
  expires_at   timestamptz,

  -- Onboarding is several network calls and can fail between them. The state
  -- is recorded so a retry knows where it got to rather than starting again
  -- and stranding a CSID that was already issued.
  status text NOT NULL DEFAULT 'requested',

  -- What ZATCA said when it refused, verbatim. A paraphrased compliance error
  -- loses the detail that identifies which of the nine CSR fields was wrong.
  last_error text,
  last_attempt_at timestamptz,
  attempts int NOT NULL DEFAULT 0,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT zatca_credential_environment_valid
    CHECK (environment IN ('sandbox', 'simulation', 'production')),
  CONSTRAINT zatca_credential_kind_valid
    CHECK (kind IN ('compliance', 'production')),
  CONSTRAINT zatca_credential_status_valid
    CHECK (status IN ('requested', 'issued', 'failed', 'revoked', 'superseded')),

  -- A sealed secret must record which key sealed it, or it cannot be opened
  -- after a rotation. Enforced rather than trusted to the writing code.
  CONSTRAINT zatca_credential_sealed_secret_has_version CHECK (
    (secret_sealed IS NULL AND secret_key_version IS NULL)
    OR (secret_sealed IS NOT NULL AND secret_key_version IS NOT NULL)),

  -- An issued credential must actually carry something.
  CONSTRAINT zatca_credential_issued_is_complete CHECK (
    status <> 'issued'
    OR (csid IS NOT NULL AND secret_sealed IS NOT NULL AND certificate IS NOT NULL))
);

-- One LIVE credential of each kind per unit per environment. Partial, so the
-- superseded history stays for audit while the constraint still guarantees
-- there is never a question about which one to authenticate with.
CREATE UNIQUE INDEX zatca_credential_live_uq
  ON zatca_credential (egs_unit_id, environment, kind)
  WHERE status IN ('requested', 'issued');

CREATE INDEX zatca_credential_tenant_idx ON zatca_credential (tenant_id);
CREATE INDEX zatca_credential_unit_idx   ON zatca_credential (egs_unit_id);
-- Renewal sweeps ask "what expires soon", so the index is on the date.
CREATE INDEX zatca_credential_expiry_idx
  ON zatca_credential (expires_at) WHERE status = 'issued';

CREATE TRIGGER zatca_credential_touch BEFORE UPDATE ON zatca_credential
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- A credential is evidence of how an invoice came to be stamped, and the
-- invoices it authenticated outlive it. Superseded, never deleted -- the same
-- rule egs_unit follows and for the same retention reason.
CREATE TRIGGER zatca_credential_no_delete BEFORE DELETE ON zatca_credential
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE zatca_credential ENABLE ROW LEVEL SECURITY;
ALTER TABLE zatca_credential FORCE  ROW LEVEL SECURITY;

-- Note what is NOT in this predicate: is_platform_admin(). Every other table
-- lets a Super Admin read across tenants for the compliance watch, and that is
-- right for onboarding STATUS -- which lives on egs_unit and remains visible.
-- It is not right for the secret that authenticates as the tenant to their tax
-- authority. A platform operator has no business holding that, and the
-- narrower predicate is what stops them.
CREATE POLICY zatca_credential_isolation ON zatca_credential
  USING (tenant_id = current_tenant_id());

COMMENT ON TABLE zatca_credential IS
  'Credentials ZATCA issues at onboarding. Never the device stamping key: '
  'see docs/system-design/01-invoice-zatca-engine.md section 7, which locks '
  'that to the terminal.';
COMMENT ON COLUMN zatca_credential.secret_sealed IS
  'AES-256-GCM ciphertext from internal/platform/secrets. Never returned by an '
  'API and never logged.';
COMMENT ON COLUMN zatca_credential.csid IS
  'HTTP Basic username for the reporting and clearance endpoints.';
