-- 0013 — Two schema corrections forced by the ZATCA verification.
--
-- 1. The CSID belongs to an EGS Unit, not to a physical terminal.
-- 2. An extended outage has an official B2B path the design did not model.

-- ---------------------------------------------------------------------------
-- 1. B2B offline policy gains the officially documented option
-- ---------------------------------------------------------------------------
--
-- Migration 0002 modelled three choices — block, draft and hold, convert to
-- simplified — on the assumption that a standard invoice simply cannot be
-- issued while offline. Detailed Guideline §10 documents a fourth: during an
-- extended outage the seller MAY issue an uncleared invoice, which is not
-- fully compliant but counts as a VAT invoice until the cleared one is issued
-- on reconnection.
--
-- The enum becomes TEXT with a check constraint, for the same reason
-- regulatory_authority did in 0010: PostgreSQL will not let a newly added enum
-- value be used in the transaction that adds it, so every future option would
-- need two ordered deployments. A check constraint is edited in one.

ALTER TABLE company
  ALTER COLUMN b2b_offline_policy DROP DEFAULT;

ALTER TABLE company
  ALTER COLUMN b2b_offline_policy TYPE text
  USING b2b_offline_policy::text;

DROP TYPE b2b_offline_policy;

ALTER TABLE company
  ALTER COLUMN b2b_offline_policy SET DEFAULT 'block',
  ADD CONSTRAINT company_b2b_offline_policy_valid
  CHECK (b2b_offline_policy IN (
    'block',               -- refuse to finalize while offline. Safest, default.
    'draft_hold',          -- save as draft; release goods on a delivery note
    'convert_simplified',  -- offer one-tap conversion to a simplified invoice
    -- The ZATCA-documented extended-outage route. Three obligations travel
    -- with it and the application must honour all three: the invoice is marked
    -- NOT VAT-deductible for the buyer, clearance attempts are logged as
    -- evidence, and ZATCA's failure-notification form is filed. On
    -- reconnection the invoice is cleared and re-issued.
    'uncleared_invoice'
  ));

COMMENT ON COLUMN company.b2b_offline_policy IS
  'What the POS does when a B2B standard tax invoice is raised offline. '
  '''block'' is the default because it is safest. ''uncleared_invoice'' is the '
  'route ZATCA documents for an extended outage (Detailed Guideline V2 §10), '
  'bounded by the VAT Art. 53(1) window of 15 days from the end of the month '
  'of supply.';

-- ---------------------------------------------------------------------------
-- 2. EGS Unit becomes its own entity
-- ---------------------------------------------------------------------------
--
-- Technical Guideline §3.5 documents three architectures, and only one of them
-- puts a CSID on the till:
--
--   centralized server  — one CSID per taxpayer, plus one per document sequence
--   smart POS           — a CSID on each POS device
--   dumb terminals      — NO CSID on the devices; a branch or sending server
--                         holds it and stamps on their behalf
--
-- Holding the CSID on `device`, as migration 0002 did, can only express the
-- middle case. An EGS Unit is properly "the software unit that signs and
-- generates one unique invoice sequence", and one EGS Unit owns exactly one
-- ICV/PIH chain. Devices point at the unit that signs for them.

CREATE TABLE egs_unit (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- NULL for a centralized unit that serves the whole company rather than one
  -- branch.
  store_id    uuid REFERENCES store(id) ON DELETE RESTRICT,

  label       text NOT NULL,

  -- Which of ZATCA's three architectures this unit represents. It decides
  -- where signing happens and therefore where the private key must live.
  architecture text NOT NULL,

  -- The nine CSR fields (Technical Guideline §3.3.3). All are mandatory at
  -- onboarding and a wrong format causes CSR rejection, so they are captured
  -- as columns rather than buried in a JSON blob where a typo goes unnoticed.
  csr_common_name        text,
  csr_egs_serial_number  text,   -- 1-<Manufacturer>|2-<Model/Version>|3-<Serial>
  csr_organization_identifier text, -- VAT number: 15 digits, starts and ends with 3
  csr_organization_unit  text,   -- branch, or the member TIN for a VAT group
  csr_organization_name  text,
  csr_country            char(2),
  csr_invoice_type       text,   -- functionality map: 1000 | 0100 | 1100
  csr_location           text,
  csr_industry           text,

  -- Only the serial is held server-side. The private stamping key never leaves
  -- the unit that signs, and Detailed Guideline §6.5 forbids the solution from
  -- offering any way to export it.
  csid_serial     text,
  csid_issued_at  timestamptz,
  csid_expires_at timestamptz,   -- from the certificate's own NotAfter
  csid_status     text NOT NULL DEFAULT 'not_started',

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT egs_unit_architecture_valid CHECK (architecture IN (
    'centralized_server',              -- signs for many devices, company-wide
    'branch_server',                   -- signs for the devices in one branch
    'smart_pos'                        -- the terminal signs for itself
  )),
  CONSTRAINT egs_unit_csid_status_valid CHECK (csid_status IN (
    'not_started', 'compliance_csid', 'production_csid', 'live', 'revoked', 'expired'
  )),
  -- The functionality map is exactly four digits of 0/1, with the last two
  -- reserved and set to 0.
  CONSTRAINT egs_unit_invoice_type_format CHECK (
    csr_invoice_type IS NULL OR csr_invoice_type ~ '^[01][01]00$'),
  -- Saudi VAT numbers are 15 digits beginning and ending with 3.
  CONSTRAINT egs_unit_vat_format CHECK (
    csr_organization_identifier IS NULL
    OR csr_organization_identifier ~ '^3[0-9]{13}3$'),
  -- A branch server must name its branch; a centralized unit must not.
  CONSTRAINT egs_unit_store_matches_architecture CHECK (
    (architecture = 'centralized_server' AND store_id IS NULL)
    OR (architecture <> 'centralized_server' AND store_id IS NOT NULL)
  )
);

CREATE INDEX egs_unit_tenant_idx  ON egs_unit (tenant_id);
CREATE INDEX egs_unit_company_idx ON egs_unit (company_id);
CREATE UNIQUE INDEX egs_unit_label_uq ON egs_unit (company_id, label);

CREATE TRIGGER egs_unit_touch BEFORE UPDATE ON egs_unit
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- An EGS unit owns an invoice chain that must remain retrievable for the full
-- retention period — up to 15 years for immovable capital assets. It can be
-- revoked, never deleted.
CREATE TRIGGER egs_unit_no_delete BEFORE DELETE ON egs_unit
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE egs_unit ENABLE ROW LEVEL SECURITY;
ALTER TABLE egs_unit FORCE  ROW LEVEL SECURITY;
-- Super Admin sees onboarding and CSID status for the platform-wide compliance
-- watch (blueprint A4), which is the same reason `device` carries the predicate.
CREATE POLICY egs_unit_isolation ON egs_unit
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- A device now points at the unit that signs for it. A dumb terminal shares its
-- branch server's unit; a smart POS has its own.
ALTER TABLE device
  ADD COLUMN egs_unit_id uuid REFERENCES egs_unit(id) ON DELETE RESTRICT;

CREATE INDEX device_egs_unit_idx ON device (egs_unit_id);

-- The CSID columns on `device` are left in place but are no longer the source
-- of truth; they are dropped once the ZATCA engine ships and any early data is
-- migrated across. Dropping them now would be a silent data loss for anything
-- already written against them.
COMMENT ON COLUMN device.csid_serial IS
  'DEPRECATED — superseded by egs_unit.csid_serial. A CSID belongs to an EGS '
  'Unit, which is only the same thing as a device in the smart-POS '
  'architecture (Technical Guideline V2 §3.5).';
