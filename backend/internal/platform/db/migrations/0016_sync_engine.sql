-- 0016 — The sync engine.
--
-- Pillar 3. Blueprint H2: "selling must never stop just because the internet
-- stopped", and every transaction carries a UUID generated at creation so that
-- "if that UUID already exists in the cloud, the sync engine must not create a
-- duplicate — this is what makes 'sell offline all day, sync at night' safe."
--
-- QA gate M3 is the acceptance test: 500 invoices sold with the internet fully
-- disconnected, then reconnect, then verify zero duplicates, zero lost
-- invoices, correct hash chain order and correct submission.
--
-- # The terminal is authoritative
--
-- A sale rung up offline is already final: the receipt is printed, the customer
-- has gone, and for a B2C simplified invoice the document is legally
-- deliverable the moment it is signed. The server therefore cannot renumber,
-- reorder or reject a completed sale. It can only refuse to record something
-- that would corrupt the chain, and say precisely why.

-- ---------------------------------------------------------------------------
-- Batch
-- ---------------------------------------------------------------------------

CREATE TABLE sync_batch (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  device_id   uuid NOT NULL REFERENCES device(id) ON DELETE RESTRICT,

  -- The device's own idempotency key for this batch. A retry after a timeout
  -- carries the same key, so the server can return the original outcome rather
  -- than reprocessing — the client never learns whether its first attempt
  -- reached us, and it should not have to.
  idempotency_key text NOT NULL,

  item_count  integer NOT NULL DEFAULT 0,
  applied     integer NOT NULL DEFAULT 0,
  duplicates  integer NOT NULL DEFAULT 0,
  failed      integer NOT NULL DEFAULT 0,

  received_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,

  CONSTRAINT sync_batch_counts_non_negative CHECK (
    item_count >= 0 AND applied >= 0 AND duplicates >= 0 AND failed >= 0)
);

CREATE UNIQUE INDEX sync_batch_idempotency_uq
  ON sync_batch (device_id, idempotency_key);
CREATE INDEX sync_batch_tenant_idx ON sync_batch (tenant_id);

ALTER TABLE sync_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_batch FORCE  ROW LEVEL SECURITY;
CREATE POLICY sync_batch_isolation ON sync_batch
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- ---------------------------------------------------------------------------
-- Item
-- ---------------------------------------------------------------------------

CREATE TABLE sync_item (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  batch_id    uuid NOT NULL REFERENCES sync_batch(id) ON DELETE RESTRICT,
  device_id   uuid NOT NULL REFERENCES device(id)    ON DELETE RESTRICT,

  -- The device's monotonic sequence number. Order within a device matters:
  -- invoices must reach ZATCA in ICV order, and a stock movement recorded
  -- before its sale would briefly show impossible stock.
  seq         bigint NOT NULL,

  -- Assigned on the device at creation, before any network call. This is the
  -- idempotency anchor: the same sale arriving twice carries the same UUID.
  entity_uuid uuid NOT NULL,
  entity_type text NOT NULL,

  payload     jsonb NOT NULL,

  state       text NOT NULL DEFAULT 'pending',
  error       text,
  applied_at  timestamptz,

  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT sync_item_state_valid CHECK (state IN (
    'pending',
    'applied',
    -- Already present. Not an error: a retried batch SHOULD produce these, and
    -- reporting them as failures would send a device into a retry loop over
    -- work that is already done.
    'duplicate',
    -- Refused because applying it would corrupt something — a chain gap, a
    -- closed period. Needs a human.
    'failed',
    -- Held because an earlier item on the same chain has not been applied.
    -- Applying this one would leave a gap, which is exactly the signal ZATCA's
    -- tamper detection looks for.
    'blocked'
  )),
  CONSTRAINT sync_item_seq_positive CHECK (seq > 0),
  CONSTRAINT sync_item_failed_has_reason CHECK (
    state <> 'failed' OR error IS NOT NULL)
);

-- The device's sequence is unique per device. A replayed batch collides here
-- before it can do anything, which is the cheapest of the three idempotency
-- layers.
CREATE UNIQUE INDEX sync_item_device_seq_uq ON sync_item (device_id, seq);

-- The business entity is unique platform-wide. Two devices cannot both claim
-- to have created the same sale.
CREATE UNIQUE INDEX sync_item_entity_uq ON sync_item (entity_uuid);

CREATE INDEX sync_item_batch_idx  ON sync_item (batch_id);
CREATE INDEX sync_item_tenant_idx ON sync_item (tenant_id);
CREATE INDEX sync_item_pending_idx
  ON sync_item (device_id, seq) WHERE state IN ('pending', 'blocked');

-- A sync item is evidence of what a device sent and when. Blueprint H2:
-- anything that fails to sync is "visible to Super Admin/Owner, never silently
-- lost", which a deleted row cannot be.
CREATE TRIGGER sync_item_no_delete BEFORE DELETE ON sync_item
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE sync_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_item FORCE  ROW LEVEL SECURITY;
CREATE POLICY sync_item_isolation ON sync_item
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- ---------------------------------------------------------------------------
-- Per-device cursor
-- ---------------------------------------------------------------------------

CREATE TABLE device_sync_cursor (
  device_id        uuid PRIMARY KEY REFERENCES device(id) ON DELETE CASCADE,
  tenant_id        uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- Highest contiguous sequence applied. Contiguous, not highest: if 1-3 and 5
  -- are applied, this is 3, because 4 is still outstanding and the device needs
  -- to know that.
  last_applied_seq bigint NOT NULL DEFAULT 0,

  -- Highest seq seen at all, contiguous or not. The difference between the two
  -- is the size of the hole.
  highest_seen_seq bigint NOT NULL DEFAULT 0,

  last_sync_at     timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT device_sync_cursor_seqs_ordered CHECK (highest_seen_seq >= last_applied_seq)
);

CREATE TRIGGER device_sync_cursor_touch BEFORE UPDATE ON device_sync_cursor
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE device_sync_cursor ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_sync_cursor FORCE  ROW LEVEL SECURITY;
CREATE POLICY device_sync_cursor_isolation ON device_sync_cursor
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- ---------------------------------------------------------------------------
-- Health
-- ---------------------------------------------------------------------------

-- What is outstanding for a device: the gap between what it sent and what
-- landed. Feeds the Owner dashboard and the Super Admin compliance watch, which
-- blueprint A4 requires to show per-tenant sync-queue depth.
CREATE OR REPLACE FUNCTION device_sync_health(p_device_id uuid)
RETURNS TABLE (
  pending      bigint,
  blocked      bigint,
  failed       bigint,
  oldest_pending_at timestamptz,
  gap_size     bigint
)
LANGUAGE sql STABLE AS $$
  SELECT
    count(*) FILTER (WHERE state = 'pending'),
    count(*) FILTER (WHERE state = 'blocked'),
    count(*) FILTER (WHERE state = 'failed'),
    min(created_at) FILTER (WHERE state IN ('pending', 'blocked')),
    coalesce((SELECT highest_seen_seq - last_applied_seq
              FROM device_sync_cursor WHERE device_id = p_device_id), 0)
  FROM sync_item
  WHERE device_id = p_device_id
$$;
