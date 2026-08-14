-- 0017 — Keep sync payloads out of the platform's reach.
--
-- Migration 0016 gave sync_batch, sync_item and device_sync_cursor the platform
-- predicate, reasoning that blueprint A4 puts "per-tenant offline-sync-queue
-- depth" on the Super Admin health dashboard. That is true, but it was applied
-- too broadly and the boundary test caught it.
--
-- sync_item carries a `payload` column holding the actual business record — the
-- invoice, its total, its customer. Granting the platform read access there
-- means the platform operator can read a tenant's sales, which is precisely
-- what A4 forbids: Super Admin "does not interfere in the Owner's day-to-day
-- business data."
--
-- What the dashboard actually needs is a DEPTH, not the rows. So the counts
-- move onto the cursor, which holds no business content, and sync_item becomes
-- tenant-only.

-- ---------------------------------------------------------------------------
-- Counters on the cursor, which carries no payload
-- ---------------------------------------------------------------------------

ALTER TABLE device_sync_cursor
  ADD COLUMN pending_count integer NOT NULL DEFAULT 0,
  ADD COLUMN blocked_count integer NOT NULL DEFAULT 0,
  ADD COLUMN failed_count  integer NOT NULL DEFAULT 0,
  ADD COLUMN oldest_unsettled_at timestamptz,
  ADD CONSTRAINT device_sync_cursor_counts_non_negative CHECK (
    pending_count >= 0 AND blocked_count >= 0 AND failed_count >= 0);

COMMENT ON TABLE device_sync_cursor IS
  'Per-device sync position and queue depth. Deliberately carries no business '
  'content, so the platform health dashboard can read it without the platform '
  'operator gaining sight of what a tenant sold.';

-- Maintained by trigger rather than by the application, so a count cannot drift
-- from reality when work is applied by a path that forgets to update it.
CREATE OR REPLACE FUNCTION refresh_device_sync_counts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  d uuid;
BEGIN
  d := coalesce(NEW.device_id, OLD.device_id);

  UPDATE device_sync_cursor c
  SET pending_count = s.pending,
      blocked_count = s.blocked,
      failed_count  = s.failed,
      oldest_unsettled_at = s.oldest
  FROM (
    SELECT
      count(*) FILTER (WHERE state = 'pending')::integer AS pending,
      count(*) FILTER (WHERE state = 'blocked')::integer AS blocked,
      count(*) FILTER (WHERE state = 'failed')::integer  AS failed,
      min(created_at) FILTER (WHERE state IN ('pending','blocked','failed')) AS oldest
    FROM sync_item WHERE device_id = d
  ) s
  WHERE c.device_id = d;

  RETURN NULL;
END;
$$;

CREATE TRIGGER sync_item_refresh_counts
  AFTER INSERT OR UPDATE OF state ON sync_item
  FOR EACH ROW EXECUTE FUNCTION refresh_device_sync_counts();

-- ---------------------------------------------------------------------------
-- Narrow the policies
-- ---------------------------------------------------------------------------

-- sync_item holds business content. Tenant only.
DROP POLICY sync_item_isolation ON sync_item;
CREATE POLICY sync_item_isolation ON sync_item
  USING (tenant_id = current_tenant_id());

-- sync_batch holds counts and timings, no payload. The platform may see that a
-- device pushed 500 items and 3 failed; it cannot see what they were.
-- Left as-is deliberately.

-- ---------------------------------------------------------------------------
-- Backfill
-- ---------------------------------------------------------------------------

UPDATE device_sync_cursor c
SET pending_count = s.pending,
    blocked_count = s.blocked,
    failed_count  = s.failed,
    oldest_unsettled_at = s.oldest
FROM (
  SELECT device_id,
    count(*) FILTER (WHERE state = 'pending')::integer AS pending,
    count(*) FILTER (WHERE state = 'blocked')::integer AS blocked,
    count(*) FILTER (WHERE state = 'failed')::integer  AS failed,
    min(created_at) FILTER (WHERE state IN ('pending','blocked','failed')) AS oldest
  FROM sync_item GROUP BY device_id
) s
WHERE c.device_id = s.device_id;

-- ---------------------------------------------------------------------------
-- Platform-safe health view
-- ---------------------------------------------------------------------------

-- What the Super Admin compliance watch reads: depth and age per device, with
-- no route to the content. A tenant reading its own devices sees the same
-- shape, so there is one function rather than two that could drift apart.
CREATE OR REPLACE FUNCTION device_sync_depth(p_device_id uuid)
RETURNS TABLE (
  pending bigint, blocked bigint, failed bigint,
  oldest_unsettled_at timestamptz, gap_size bigint
)
LANGUAGE sql STABLE AS $$
  SELECT pending_count::bigint, blocked_count::bigint, failed_count::bigint,
         oldest_unsettled_at,
         (highest_seen_seq - last_applied_seq)::bigint
  FROM device_sync_cursor
  WHERE device_id = p_device_id
$$;
