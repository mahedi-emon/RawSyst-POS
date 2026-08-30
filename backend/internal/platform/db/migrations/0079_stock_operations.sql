-- 0079 — Counting stock, correcting it, writing it off, and moving it.
--
-- Blueprint B4 asks for four things this schema carries: wastage logging with a
-- mandatory reason and "automatic loss write-off posted to accounting", stock
-- adjustment, a physical count that "auto-generates a signed Adjustment Voucher
-- (with reason, user, approval, timestamp) for any variance", and an
-- inter-branch transfer running Request -> Approval -> Dispatch (in-transit
-- lock) -> Receiving branch confirms and reconciles.
--
-- Every piece underneath already existed. `stock_movement` has carried
-- 'adjustment', 'wastage', 'transfer_in', 'transfer_out', 'count' and
-- 'internal_use' in its reason vocabulary since 0020, and refuses an adjustment
-- or a wastage with no explanation. Posting rule 10, `inventory.writeoff`, has
-- been seeded and callable since 0025. The permissions `inventory.adjust_stock`
-- and `inventory.transfer_stock` have been granted to three roles since 0005.
--
-- None of it was reachable. The route audit lists both verbs as "awaited", the
-- write-off rule has never posted a single entry, and four of the six movement
-- reasons have never been written. This migration is the documents that use
-- them.

-- ---------------------------------------------------------------------------
-- Where stock goes while it is nowhere
-- ---------------------------------------------------------------------------
--
-- A transfer takes days. Stock leaves Riyadh on Monday and arrives in Jeddah on
-- Wednesday, and on Tuesday it is real, owned, and in neither branch.
--
-- C13's hard invariant is that the inventory valuation ties EXACTLY to the
-- Inventory control account. If Monday's dispatch removed the stock from
-- Riyadh and nothing received it, the valuation would fall by the value of a
-- lorry-load while the ledger — correctly — did not move at all, and the
-- invariant would break for two days on every transfer. Posting the dispatch to
-- an expense would be worse: the company has not lost anything.
--
-- So in-transit stock lives in a real location that the valuation counts. One
-- per company, system-owned, invisible to every screen that offers a choice of
-- location, and never a place a person can sell from, count, or adjust.
--
-- The alternative — leaving the stock in the source branch until receipt — was
-- rejected because it lies in the other direction: Riyadh's shop floor would
-- report stock that had physically left the building, and a count on Tuesday
-- would raise a variance against goods that are on a lorry.

INSERT INTO warehouse (tenant_id, company_id, store_id, code, name, kind)
SELECT c.tenant_id, c.id, NULL, 'TRANSIT', 'In transit', 'transit'
FROM company c
ON CONFLICT (company_id, code) DO NOTHING;

CREATE UNIQUE INDEX warehouse_one_transit_per_company
  ON warehouse (company_id) WHERE kind = 'transit';

-- And every company created after this migration ran?
--
-- Not here. A trigger on `company` was written, and the test fixtures refused
-- it within the minute: the row-level policy on `warehouse` is
-- `tenant_id = current_tenant_id()` with FORCE, and a company can be created on
-- the PLATFORM plane, where there is no current tenant. The trigger's INSERT is
-- refused, and the whole company creation fails with it.
--
-- Widening the policy so a trigger could write through it would trade a real
-- isolation boundary for the convenience of one row. So the fallback lives in
-- `stockops.transitLocation`, which always runs as the tenant and creates the
-- location the first time a transfer needs it.

-- ---------------------------------------------------------------------------
-- Document numbering
-- ---------------------------------------------------------------------------
--
-- Counters on the company row, the way every other document in this product is
-- numbered: `max()+1` collides under load and a sequence is not transactional,
-- so a rolled-back voucher would leave a permanent hole in a numbered record an
-- auditor reads as a deletion.

ALTER TABLE company
  ADD COLUMN next_stock_adjustment_no bigint NOT NULL DEFAULT 1,
  ADD COLUMN next_stock_transfer_no   bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_stock_adjustment_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_stock_adjustment_no = next_stock_adjustment_no + 1
  WHERE id = p_company_id
  RETURNING next_stock_adjustment_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'ADJ-' || to_char(claimed, 'FM000000');
END;
$$;

CREATE OR REPLACE FUNCTION claim_stock_transfer_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_stock_transfer_no = next_stock_transfer_no + 1
  WHERE id = p_company_id
  RETURNING next_stock_transfer_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'TRF-' || to_char(claimed, 'FM000000');
END;
$$;

-- ---------------------------------------------------------------------------
-- The adjustment voucher
-- ---------------------------------------------------------------------------
--
-- One table for three documents, because they are three shapes of the same
-- event: stock on hand is not what the system says, and the difference is being
-- recorded with a reason and a name against it.
--
--   adjustment — a known correction. "Six went out on a delivery note nobody
--                keyed." The person states the difference.
--   wastage    — value destroyed. Damaged, expired, stolen. Always negative,
--                and B4 requires the reason.
--   count      — a physical count. The person states what they COUNTED and the
--                difference is worked out, which is the whole point: a counter
--                who is told the expected figure counts to it.
--
-- Splitting them into three tables would triple the posting path for one
-- journal entry and one movement per line.

CREATE TABLE stock_adjustment (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id    uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  warehouse_id  uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  adjustment_no text NOT NULL,
  kind          text NOT NULL,

  -- B4: "mandatory reason + category". The category is this column; the
  -- sentence a person types is `note`.
  reason        text NOT NULL,
  note          text,

  status        text NOT NULL DEFAULT 'draft',

  -- Set when it posts. A voucher that moved no stock at all — a count that
  -- found everything exactly right, which is the outcome to hope for — posts
  -- nothing and leaves this NULL. That is a success, not an incomplete record.
  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  created_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  posted_by     uuid REFERENCES app_user(id) ON DELETE SET NULL,
  posted_at     timestamptz,

  CONSTRAINT stock_adjustment_kind_valid
    CHECK (kind IN ('adjustment', 'wastage', 'count')),
  CONSTRAINT stock_adjustment_status_valid
    CHECK (status IN ('draft', 'posted', 'cancelled')),

  -- 0020 already refuses a movement with reason 'adjustment' or 'wastage' and
  -- no note. Saying it again at the document level is not duplication: the
  -- movement's constraint fires deep inside a posting run, and a person filling
  -- in a form should be told before any of that starts.
  CONSTRAINT stock_adjustment_explained
    CHECK (kind = 'count' OR (note IS NOT NULL AND length(btrim(note)) >= 3)),

  -- A posted voucher knows who posted it and when. B4 calls the count's output
  -- a SIGNED voucher, and a signature with no name on it is a blank.
  CONSTRAINT stock_adjustment_posted_is_signed
    CHECK (status <> 'posted' OR (posted_by IS NOT NULL AND posted_at IS NOT NULL))
);

CREATE UNIQUE INDEX stock_adjustment_no_uq
  ON stock_adjustment (company_id, adjustment_no);
CREATE INDEX stock_adjustment_tenant_idx ON stock_adjustment (tenant_id);
CREATE INDEX stock_adjustment_open_idx
  ON stock_adjustment (company_id, warehouse_id) WHERE status = 'draft';

ALTER TABLE stock_adjustment ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_adjustment FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_adjustment_isolation ON stock_adjustment
  USING (tenant_id = current_tenant_id());

CREATE TABLE stock_adjustment_line (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  adjustment_id uuid NOT NULL REFERENCES stock_adjustment(id) ON DELETE CASCADE,
  variant_id    uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,

  -- What the system believed when the sheet was opened. Recorded so a count
  -- can report that stock moved WHILE it was being counted, which is the
  -- commonest reason a variance is not a real variance.
  system_qty_open numeric(18,4) NOT NULL,

  -- What the system believed at the moment of posting, and what the difference
  -- is actually measured against. Using the opening figure instead would
  -- silently absorb every sale made during the count into the variance.
  system_qty_posted numeric(18,4),

  -- What was physically counted. NULL for an adjustment or a wastage, where the
  -- person states the difference rather than the total.
  counted_qty   numeric(18,4),

  -- Signed. Negative takes stock out.
  delta         numeric(18,4),

  -- What the movement was worth, filled in at posting from the costing engine.
  -- Never from the caller: a person recording damage does not get to say what
  -- the damage cost, for the same reason a till does not get to say what a sale
  -- cost.
  value         numeric(18,2),

  CONSTRAINT stock_adjustment_line_counted_non_negative
    CHECK (counted_qty IS NULL OR counted_qty >= 0)
);

CREATE UNIQUE INDEX stock_adjustment_line_uq
  ON stock_adjustment_line (adjustment_id, variant_id);
CREATE INDEX stock_adjustment_line_tenant_idx ON stock_adjustment_line (tenant_id);

ALTER TABLE stock_adjustment_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_adjustment_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_adjustment_line_isolation ON stock_adjustment_line
  USING (tenant_id = current_tenant_id());

-- A posted voucher is a record of what happened, and what happened does not
-- change. Same reasoning as the invoice, the journal entry and the Z-report:
-- correcting a mistake means another voucher the other way, which leaves both
-- facts visible.
CREATE OR REPLACE FUNCTION reject_posted_adjustment_change()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'posted' THEN
    RAISE EXCEPTION
      'A posted stock voucher cannot be changed. Raise another one the other way.'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER stock_adjustment_frozen_once_posted
  BEFORE UPDATE OR DELETE ON stock_adjustment
  FOR EACH ROW EXECUTE FUNCTION reject_posted_adjustment_change();

-- ---------------------------------------------------------------------------
-- The transfer
-- ---------------------------------------------------------------------------
--
-- B4's four steps, as four states. They are states rather than a boolean pair
-- because the interesting question a transfer answers is "where is it now", and
-- a document that only knew sent/not-sent could not tell a lorry from a request
-- nobody has approved.

CREATE TABLE stock_transfer (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  transfer_no  text NOT NULL,

  from_warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,
  to_warehouse_id   uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  status       text NOT NULL DEFAULT 'requested',
  note         text,

  requested_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  approved_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  approved_at  timestamptz,
  dispatched_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  dispatched_at timestamptz,
  received_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  received_at  timestamptz,
  cancelled_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  cancelled_at timestamptz,

  CONSTRAINT stock_transfer_status_valid
    CHECK (status IN ('requested', 'approved', 'dispatched', 'received', 'cancelled')),

  -- Moving stock to where it already is is not a transfer, and the pair of
  -- movements it would write are a pointless round trip through the costing
  -- engine that can only lose a hallala to rounding.
  CONSTRAINT stock_transfer_goes_somewhere
    CHECK (from_warehouse_id <> to_warehouse_id),

  -- Each stamp implies the ones before it. Without this a transfer could be
  -- received having never been dispatched, and the receiving branch would
  -- create stock out of nothing.
  CONSTRAINT stock_transfer_timeline
    CHECK (
      (approved_at   IS NOT NULL OR status IN ('requested', 'cancelled')) AND
      (dispatched_at IS NOT NULL OR status IN ('requested', 'approved', 'cancelled')) AND
      (received_at   IS NOT NULL OR status <> 'received')
    )
);

CREATE UNIQUE INDEX stock_transfer_no_uq ON stock_transfer (company_id, transfer_no);
CREATE INDEX stock_transfer_tenant_idx ON stock_transfer (tenant_id);
CREATE INDEX stock_transfer_open_idx ON stock_transfer (company_id, status)
  WHERE status IN ('requested', 'approved', 'dispatched');

ALTER TABLE stock_transfer ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_transfer FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_transfer_isolation ON stock_transfer
  USING (tenant_id = current_tenant_id());

CREATE TABLE stock_transfer_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  transfer_id uuid NOT NULL REFERENCES stock_transfer(id) ON DELETE CASCADE,
  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,

  qty_requested  numeric(18,4) NOT NULL,
  qty_dispatched numeric(18,4),
  qty_received   numeric(18,4),

  -- The exact value the stock left at, so the receiving end can put it back at
  -- the same figure rather than recomputing one. `inventory.Restore` takes a
  -- value for precisely this, and taking a unit cost instead would multiply and
  -- round a second time — which is how a transfer would quietly create or
  -- destroy a hallala of inventory value on every leg.
  value_dispatched numeric(18,2),

  CONSTRAINT stock_transfer_line_qty_positive CHECK (qty_requested > 0),
  CONSTRAINT stock_transfer_line_dispatch_sane
    CHECK (qty_dispatched IS NULL OR (qty_dispatched > 0 AND qty_dispatched <= qty_requested)),

  -- A branch may receive less than was sent — that is the discrepancy B4 wants
  -- flagged — but never more. More would mean the lorry gained stock.
  CONSTRAINT stock_transfer_line_receipt_sane
    CHECK (qty_received IS NULL OR
           (qty_received >= 0 AND qty_dispatched IS NOT NULL AND qty_received <= qty_dispatched))
);

CREATE UNIQUE INDEX stock_transfer_line_uq
  ON stock_transfer_line (transfer_id, variant_id);
CREATE INDEX stock_transfer_line_tenant_idx ON stock_transfer_line (tenant_id);

ALTER TABLE stock_transfer_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_transfer_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_transfer_line_isolation ON stock_transfer_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Rule 10a — stock found
-- ---------------------------------------------------------------------------
--
-- Rule 10 has posted a write-off since 0025: value destroyed, Dr Stock
-- Write-off / Cr Inventory. A physical count can also find MORE than the system
-- expected, and B4 asks for a voucher "for any variance" without saying which
-- way it points.
--
-- This is the mirror, against the same account, exactly as 0052 arranged
-- `cash.shortage` and `cash.overage`. The reasoning there applies here word for
-- word and is worth repeating rather than cross-referencing: an unexplained
-- surplus is as much a control failure as a shortfall. Stock that appears is
-- stock that was mis-recorded when it arrived, or mis-recorded when it left,
-- and booking it to Other Income would turn a symptom of a broken process into
-- a line of profit.
--
-- One signed rule with a negative amount was the other option and is the same
-- mistake 0052 declined: a negative debit where a credit belongs reads as a
-- typo to every accountant who ever opens the ledger.

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES
('inventory.writeon', NULL, 1,
 '[{"role": "inventory",      "side": "debit",  "amount": "value"},
   {"role": "stock_writeoff", "side": "credit", "amount": "value"}]'::jsonb,
 'Stock a physical count found that the records did not have.',
 '2020-01-01');

-- ---------------------------------------------------------------------------
-- Approving a transfer is not the same act as asking for one
-- ---------------------------------------------------------------------------
--
-- B4 puts a manager between the request and the lorry. With only
-- `inventory.transfer_stock` to check, whoever raises a transfer could approve
-- it, and the step would be theatre.
--
-- Granted to the Owner and the Store Manager. Deliberately NOT to the Inventory
-- Keeper: 0005 describes that role as "goods receipt, transfers, wastage
-- logging" — the person who does the work — and a control that the doer can
-- sign off is not a control.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'inventory.approve_transfer'),
  ('store_manager', 'inventory.approve_transfer')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- And the clones every existing tenant is already holding. This is the whole
-- lesson of 0042 and 0051: a grant written only to the platform TEMPLATES
-- reaches nobody who is already a customer, and the failure is invisible until
-- a shop that has been trading for a year finds a screen refusing its Owner.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',         'inventory.approve_transfer'),
      ('store_manager', 'inventory.approve_transfer')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
