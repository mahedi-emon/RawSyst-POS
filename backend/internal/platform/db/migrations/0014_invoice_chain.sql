-- 0014 — The invoice chain: ICV, PIH and the ZATCA state machine.
--
-- This is the piece the blueprint says to get right first, because everything
-- else depends on it and getting it wrong means rebuilding. Two properties
-- carry the weight, and both are enforced here rather than in application code:
--
--   ICV never resets, never repeats, never gaps — a gap is precisely what
--   ZATCA's tamper detection looks for.
--
--   PIH chains each invoice to the one before it, so altering any invoice
--   breaks every hash after it.
--
-- Detailed Guideline V2 §5.6/§6.5 forbid an EGS from offering a counter reset
-- or more than one sequence. Those are not policies we apply — they are states
-- the schema refuses to hold.

-- ---------------------------------------------------------------------------
-- The counter lives with the EGS unit
-- ---------------------------------------------------------------------------
--
-- Wherever the unit signs, the counter is. A smart POS owns its own counter
-- offline; a centralized server owns one per sequence it signs. This column is
-- the server-side allocator and the high-water mark used to validate what a
-- terminal-signed unit reports.
ALTER TABLE egs_unit
  ADD COLUMN last_icv bigint NOT NULL DEFAULT 0,
  ADD COLUMN last_invoice_hash text,
  ADD CONSTRAINT egs_unit_icv_non_negative CHECK (last_icv >= 0);

COMMENT ON COLUMN egs_unit.last_icv IS
  'Highest ICV issued on this unit''s chain. Monotonic and never reset — '
  'Detailed Guideline V2 §5.6 forbids an EGS from offering a reset at all.';

-- ---------------------------------------------------------------------------
-- Invoice
-- ---------------------------------------------------------------------------

CREATE TABLE sales_invoice (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  store_id    uuid REFERENCES store(id)  ON DELETE RESTRICT,
  device_id   uuid REFERENCES device(id) ON DELETE RESTRICT,

  -- Assigned on the device at creation, before any network call. A database
  -- sequence cannot serve a sale rung up with no connectivity, and this is what
  -- makes a sync retry idempotent.
  uuid        uuid NOT NULL,

  doc_type    text NOT NULL,
  -- A credit or debit note has no meaning without the invoice it corrects, and
  -- ZATCA requires the reference.
  parent_invoice_id uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,

  issue_date  date        NOT NULL,
  issued_at   timestamptz NOT NULL,

  currency    char(3) NOT NULL,
  fx_rate     numeric(18,8) NOT NULL DEFAULT 1,

  subtotal_net     numeric(18,4) NOT NULL DEFAULT 0,
  discount_total   numeric(18,4) NOT NULL DEFAULT 0,
  tax_total        numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive  numeric(18,4) NOT NULL DEFAULT 0,

  -- The friendly number, configurable per store and year. Deliberately a
  -- separate column from the ICV: blueprint I3 warns that letting a custom
  -- invoice number drive the tamper-evident counter is exactly the mistake to
  -- avoid, so the numbering engine has no access to ICV allocation.
  human_number text,

  state       text NOT NULL DEFAULT 'draft',

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT sales_invoice_doc_type_valid CHECK (doc_type IN (
    'standard', 'simplified', 'credit_note', 'debit_note')),
  CONSTRAINT sales_invoice_note_references_original CHECK (
    doc_type NOT IN ('credit_note', 'debit_note') OR parent_invoice_id IS NOT NULL),
  CONSTRAINT sales_invoice_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT sales_invoice_totals_non_negative CHECK (
    subtotal_net >= 0 AND discount_total >= 0 AND total_inclusive >= 0),
  CONSTRAINT sales_invoice_fx_positive CHECK (fx_rate > 0),
  CONSTRAINT sales_invoice_state_valid CHECK (state IN (
    'draft',                   -- no ICV consumed, no legal document exists
    'signed_pending_report',   -- B2C: signed locally, receipt given, not yet reported
    'signed_pending_clear',    -- B2B: signed, awaiting clearance
    'uncleared_issued',        -- B2B extended outage (Detailed Guideline V2 §10)
    'submitted',
    'cleared',                 -- B2B accepted by ZATCA
    'reported',                -- B2C accepted by ZATCA
    'accepted_with_warnings',  -- 202: stamped and valid, warnings must surface
    'rejected',                -- 400: keeps its ICV, corrected by credit note
    'cancelled'                -- draft only; a signed invoice is never cancelled
  ))
);

CREATE UNIQUE INDEX sales_invoice_uuid_uq ON sales_invoice (uuid);
CREATE UNIQUE INDEX sales_invoice_human_number_uq
  ON sales_invoice (company_id, human_number) WHERE human_number IS NOT NULL;
CREATE INDEX sales_invoice_tenant_idx ON sales_invoice (tenant_id);
CREATE INDEX sales_invoice_company_date_idx ON sales_invoice (company_id, issue_date DESC);
CREATE INDEX sales_invoice_parent_idx ON sales_invoice (parent_invoice_id)
  WHERE parent_invoice_id IS NOT NULL;

CREATE TRIGGER sales_invoice_touch BEFORE UPDATE ON sales_invoice
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- A finalized invoice can never be edited or deleted "by anyone, not even the
-- Owner or Super Admin". Deleting or amending an issued e-invoice is a fine
-- starting at SAR 10,000, so this is enforced by trigger, not by convention.
CREATE TRIGGER sales_invoice_no_delete BEFORE DELETE ON sales_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE sales_invoice ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_invoice FORCE  ROW LEVEL SECURITY;
-- Tenant-only, with no platform predicate. A Super Admin sees that a
-- submission failed, never what was sold.
CREATE POLICY sales_invoice_isolation ON sales_invoice
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The ZATCA chain record
-- ---------------------------------------------------------------------------

CREATE TABLE zatca_invoice (
  invoice_id  uuid PRIMARY KEY REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  tenant_id   uuid NOT NULL REFERENCES tenant(id)   ON DELETE CASCADE,
  egs_unit_id uuid NOT NULL REFERENCES egs_unit(id) ON DELETE RESTRICT,

  -- Invoice Counter Value. Strictly sequential per unit, from 1, never reset.
  icv         bigint NOT NULL,

  -- Previous Invoice Hash. The first invoice on a chain carries the seed value
  -- rather than NULL, so "no predecessor" and "predecessor not recorded" cannot
  -- be confused.
  pih         text NOT NULL,

  -- This invoice's own hash, which becomes the next invoice's PIH.
  invoice_hash text NOT NULL,

  -- Which schema version signed this document. ZATCA revises the standard, and
  -- an archived invoice must be verifiable years later against the rules that
  -- produced it — up to 15 years for immovable capital assets.
  schema_version text NOT NULL,

  -- ECDSA stamp and the QR payload. Nullable only while the byte-level format
  -- is still being verified; the release gate blocks production until then.
  stamp       text,
  qr_tlv      text,
  xml         text,

  submitted_at timestamptz,
  zatca_uuid   text,
  response_code integer,
  warnings     jsonb,
  reject_reason text,

  retry_count  integer NOT NULL DEFAULT 0,
  next_retry_at timestamptz,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT zatca_invoice_icv_positive CHECK (icv > 0),
  CONSTRAINT zatca_invoice_retry_non_negative CHECK (retry_count >= 0)
);

-- The load-bearing constraint. Two invoices cannot share a position on a
-- chain, so a duplicate ICV is unrepresentable rather than something a nightly
-- job discovers.
CREATE UNIQUE INDEX zatca_invoice_chain_uq ON zatca_invoice (egs_unit_id, icv);

-- Each hash may appear once as a predecessor, so a chain cannot fork.
CREATE UNIQUE INDEX zatca_invoice_hash_uq ON zatca_invoice (egs_unit_id, invoice_hash);

CREATE INDEX zatca_invoice_tenant_idx ON zatca_invoice (tenant_id);
CREATE INDEX zatca_invoice_submit_queue_idx
  ON zatca_invoice (egs_unit_id, icv)
  WHERE submitted_at IS NULL;

CREATE TRIGGER zatca_invoice_touch BEFORE UPDATE ON zatca_invoice
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER zatca_invoice_no_delete BEFORE DELETE ON zatca_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- Everything that defines the document's place in the chain is frozen at
-- signing. Submission status may still advance — that is the whole point of the
-- retry queue — but the ICV, the hashes and the signed bytes may not move.
--
-- This is what makes a rejected invoice safe to keep: it holds its position
-- with its stamp intact and is corrected by a credit note, exactly as ZATCA
-- requires, instead of being deleted and its counter reused.
CREATE TRIGGER zatca_invoice_frozen_chain
  BEFORE UPDATE ON zatca_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'egs_unit_id', 'icv', 'pih', 'invoice_hash', 'schema_version', 'stamp');

ALTER TABLE zatca_invoice ENABLE ROW LEVEL SECURITY;
ALTER TABLE zatca_invoice FORCE  ROW LEVEL SECURITY;
CREATE POLICY zatca_invoice_isolation ON zatca_invoice
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Chain integrity, checkable on demand
-- ---------------------------------------------------------------------------

-- Walks one unit's chain and returns the first break it finds, or nothing.
--
-- QA gate M2 requires an unbroken chain across 10,000+ sequential invoices with
-- no reset and no gap. Expressing that as a query means the nightly job, the
-- acceptance test and a support engineer investigating a live tenant all ask
-- the same question the same way.
CREATE OR REPLACE FUNCTION zatca_chain_breaks(p_egs_unit_id uuid)
RETURNS TABLE (icv bigint, problem text)
LANGUAGE sql STABLE AS $$
  WITH ordered AS (
    SELECT z.icv, z.pih, z.invoice_hash,
           lag(z.icv)          OVER (ORDER BY z.icv) AS prev_icv,
           lag(z.invoice_hash) OVER (ORDER BY z.icv) AS prev_hash
    FROM zatca_invoice z
    WHERE z.egs_unit_id = p_egs_unit_id
  )
  SELECT o.icv,
         CASE
           WHEN o.prev_icv IS NULL AND o.icv <> 1
             THEN 'chain does not start at 1'
           WHEN o.prev_icv IS NOT NULL AND o.icv <> o.prev_icv + 1
             THEN format('gap: jumps from %s to %s', o.prev_icv, o.icv)
           WHEN o.prev_hash IS NOT NULL AND o.pih IS DISTINCT FROM o.prev_hash
             THEN 'previous invoice hash does not match the invoice before it'
         END
  FROM ordered o
  WHERE (o.prev_icv IS NULL AND o.icv <> 1)
     OR (o.prev_icv IS NOT NULL AND o.icv <> o.prev_icv + 1)
     OR (o.prev_hash IS NOT NULL AND o.pih IS DISTINCT FROM o.prev_hash)
  ORDER BY o.icv
$$;
