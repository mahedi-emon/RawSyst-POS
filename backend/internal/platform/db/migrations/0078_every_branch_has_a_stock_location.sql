-- 0078 — A branch that cannot sell.
--
-- `sales.resolveWarehouse` refuses a sale when the branch has no stock
-- location, with this message:
--
--   "This branch has no stock location set up, so there is nothing to sell
--    from. An owner can add one under Settings > Stock locations."
--
-- Three things are wrong with that sentence today. There is no Settings > Stock
-- locations screen. There is no route behind one. And nothing in the product
-- has ever inserted a `warehouse` row at all — the only INSERTs against that
-- table in the whole repository are in test fixtures.
--
-- So a tenant provisioned the real way, through the A5 wizard, finishes setup
-- with stores, staff, a chart of accounts, a paired till and a tax profile, and
-- **cannot ring up a single sale**. Every guarantee underneath it works. The
-- shop just has nowhere to sell from.
--
-- This is the same shape as the shift service that was mounted nowhere and the
-- terminal that could never sell: each layer correct, and no path through them.
--
-- # Why a store gets one automatically rather than being asked
--
-- Blueprint B4 wants multi-warehouse — a central warehouse serving branches,
-- in-transit stock, per-branch views. That is a real capability and it is not
-- what a shop needs on its first morning. A shop needs the room it is standing
-- in to be somewhere stock can live.
--
-- So every store gets one location, named after itself, at the moment it is
-- created. A business that wants a central warehouse or a second store room
-- adds one; a business that never thinks about warehouses never has to.
--
-- The alternative — an eighth onboarding step asking a shopkeeper to define a
-- warehouse before they understand why — is how setup wizards get abandoned.

-- ---------------------------------------------------------------------------
-- What the location is for
-- ---------------------------------------------------------------------------
--
-- `kind` separates the room the customers walk into from the ones they do not.
-- It is not decoration: a physical count of the shop floor is a different piece
-- of work from a count of the back store room, and a transfer between the two
-- is the commonest transfer a single-branch shop makes.

ALTER TABLE warehouse
  ADD COLUMN kind text NOT NULL DEFAULT 'shop_floor';

ALTER TABLE warehouse
  ADD CONSTRAINT warehouse_kind_valid
  CHECK (kind IN ('shop_floor', 'store_room', 'central', 'transit'));

COMMENT ON COLUMN warehouse.kind IS
  'shop_floor: the room customers are in. store_room: back-of-house at one '
  'branch. central: serves several branches, store_id IS NULL. transit: '
  'system-owned, holds stock between dispatch and receipt.';

-- A central warehouse belongs to no single branch, and a shop floor cannot
-- belong to none — it IS a branch. Stating it as a constraint rather than a
-- convention because the difference decides which locations a till may sell
-- from.
ALTER TABLE warehouse
  ADD CONSTRAINT warehouse_kind_agrees_with_scope
  CHECK (
    (kind = 'central' AND store_id IS NULL) OR
    (kind = 'shop_floor' AND store_id IS NOT NULL) OR
    (kind IN ('store_room', 'transit'))
  );

-- ---------------------------------------------------------------------------
-- Backfill: every store that has nowhere to sell from
-- ---------------------------------------------------------------------------
--
-- The 0048 lesson, applied without being asked twice: a migration that adds a
-- capability for new records and leaves existing ones behind has fixed nothing
-- for anybody who is already a customer. Every store alive today gets its
-- location here, in the same migration that makes new ones get theirs.
--
-- `store_id IS NULL OR store_id = s.id` in the NOT EXISTS is deliberate. A
-- company that already has a central warehouse can sell from it — that branch
-- is not stuck — so it does not need a second location invented for it.
INSERT INTO warehouse (tenant_id, company_id, store_id, code, name, kind)
SELECT s.tenant_id, s.company_id, s.id, s.code, s.name, 'shop_floor'
FROM store s
WHERE NOT EXISTS (
  SELECT 1 FROM warehouse w
  WHERE w.company_id = s.company_id
    AND w.is_active
    AND (w.store_id = s.id OR w.store_id IS NULL)
);

-- ---------------------------------------------------------------------------
-- Naming a location that is not a branch
-- ---------------------------------------------------------------------------
--
-- Codes are claimed per company, and a store room added to a branch whose code
-- is already taken by its shop floor needs one of its own. The application
-- proposes `<STORE>-2`, `<STORE>-3` and so on; this is only the ceiling that
-- stops a proposal running past what the column accepts.
--
-- Sixteen characters, from 0020. A twelve-character store code plus `-99` fits
-- with one to spare, and a business with a hundred store rooms at one branch
-- has a naming problem this migration is not going to solve.

CREATE INDEX warehouse_company_kind_idx ON warehouse (company_id, kind)
  WHERE is_active;

COMMENT ON TABLE warehouse IS
  'A place stock can be. Every store has at least one from 0078; before that a '
  'branch could be fully set up and still refuse every sale, because nothing '
  'in the product created one.';
