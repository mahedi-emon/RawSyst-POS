-- 0108 — Product bundles / combo packages (blueprint B1).
--
-- B1: "Product Bundles / Combo Packages: sell multiple SKUs as one package
-- (e.g., 'Suit + Shirt + Tie Combo') with automatic proportional stock
-- deduction of each component on sale."
--
-- A `bundle_price` PROMOTION has existed since 0084 and is a different thing:
-- it prices several items together at the till while each line remains its own
-- product. A bundle is one SELLABLE SKU — it scans as one barcode, prints as
-- one line on the receipt, and takes its stock from the things inside it.
--
-- # A bundle holds no stock of its own
--
-- The whole point is that it does not. Selling "Suit + Shirt + Tie" must leave
-- one fewer suit, one fewer shirt and one fewer tie; a bundle with its own
-- stock level would be a fourth number nobody maintains, and it would go wrong
-- the first time somebody sold a shirt on its own.
--
-- So a bundle variant is never received, never counted and never valued. Its
-- cost of sale is the sum of what its components cost when they left, which is
-- the only figure that keeps gross profit true and keeps C13's tie-out holding:
-- the components' own consumption is what moves the Inventory account, and the
-- bundle adds nothing of its own to reconcile.
--
-- # Components are quantities, not a list
--
-- A "6-pack" is one component with a quantity of six, not six rows. Selling two
-- of them takes twelve, which is what "proportional" means in B1's sentence.
--
-- # No bundles inside bundles
--
-- A component that is itself a bundle would need recursive expansion, recursive
-- costing and a cycle check, and B1's example is one level deep. The CHECK
-- below makes the nested case unrepresentable rather than half-supported: a
-- shop that needs it will say so, and a wrong answer computed confidently is
-- worse than a refusal.

ALTER TABLE variant
  ADD COLUMN is_bundle boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN variant.is_bundle IS
  'B1 combo package. A bundle is sellable and holds no stock of its own: its '
  'components are deducted on sale and its cost is theirs.';

CREATE TABLE bundle_component (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  bundle_variant_id    uuid NOT NULL REFERENCES variant(id) ON DELETE CASCADE,
  component_variant_id uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,

  -- How many of the component one bundle contains. A 6-pack is qty 6.
  qty numeric(18,4) NOT NULL,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT bundle_component_qty_positive CHECK (qty > 0),
  -- A bundle containing itself would expand for ever.
  CONSTRAINT bundle_component_not_itself
    CHECK (bundle_variant_id <> component_variant_id)
);

-- One row per component per bundle. A second helping of the same item raises
-- the quantity rather than adding a rival row, so "how many shirts are in this
-- combo" has one answer.
CREATE UNIQUE INDEX bundle_component_identity_uq
  ON bundle_component (bundle_variant_id, component_variant_id);

CREATE INDEX bundle_component_bundle_idx ON bundle_component (bundle_variant_id);
CREATE INDEX bundle_component_component_idx
  ON bundle_component (component_variant_id);

CREATE TRIGGER bundle_component_touch BEFORE UPDATE ON bundle_component
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE bundle_component ENABLE ROW LEVEL SECURITY;
ALTER TABLE bundle_component FORCE  ROW LEVEL SECURITY;
CREATE POLICY bundle_component_isolation ON bundle_component
  USING (tenant_id = current_tenant_id());

-- The two rules that keep expansion one level deep and honest, enforced where
-- they cannot be forgotten.
--
-- A trigger rather than a CHECK because both questions are about OTHER rows:
-- whether the parent is flagged as a bundle, and whether the component is one.
CREATE OR REPLACE FUNCTION assert_bundle_shape() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  parent_is_bundle    boolean;
  component_is_bundle boolean;
BEGIN
  SELECT is_bundle INTO parent_is_bundle
  FROM variant WHERE id = NEW.bundle_variant_id;

  IF NOT coalesce(parent_is_bundle, false) THEN
    RAISE EXCEPTION 'that product is not a bundle, so it cannot hold components'
      USING ERRCODE = 'check_violation';
  END IF;

  SELECT is_bundle INTO component_is_bundle
  FROM variant WHERE id = NEW.component_variant_id;

  IF coalesce(component_is_bundle, false) THEN
    RAISE EXCEPTION 'a bundle cannot contain another bundle'
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER bundle_component_shape
  BEFORE INSERT OR UPDATE ON bundle_component
  FOR EACH ROW EXECUTE FUNCTION assert_bundle_shape();
