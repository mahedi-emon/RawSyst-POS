-- 0010 — Catalog: categories, brands, units, products and variants.
--
-- Two decisions here are shaped by the product selling into Saudi Arabia,
-- Bangladesh and the USA rather than Saudi alone.
--
-- 1. Tax treatment is TEXT validated against a country-scoped registry list,
--    not a PostgreSQL enum. Saudi has seven VAT treatments; Bangladesh has its
--    own; the USA has sales tax, which is not a VAT at all — it is charged only
--    at the final retail sale and has no input tax to reclaim. An enum would
--    need a migration per country, and a Saudi-shaped enum simply cannot
--    express the US case.
--
-- 2. Product names carry a translations map instead of a fixed name_ar column.
--    Arabic, English and Bengali ship at launch and blueprint G3 requires new
--    languages to arrive without a code release, which a fixed column pair
--    cannot do.

-- Trigram search, for finding a product by a fragment of its name in any
-- script. Needed before the index below.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- Regulatory authorities become data, for the same reason tax treatments did
-- ---------------------------------------------------------------------------
--
-- Migration 0004 made regulatory_authority a PostgreSQL enum listing the eight
-- Saudi bodies. Adding Bangladesh's NBR or a US state revenue department would
-- then need a migration — and worse, PostgreSQL will not let a newly added
-- enum value be used in the same transaction that adds it, so each new country
-- would need two deployments in order.
--
-- A lookup table has neither problem. Registering a country becomes an insert.

-- Order matters: the enum and the new table share a name, so the column must
-- stop referring to the type before the type can be dropped.
ALTER TABLE regulatory_rule
  ALTER COLUMN source_authority TYPE text USING source_authority::text;

DROP TYPE regulatory_authority;

CREATE TABLE regulatory_authority (
  code    text PRIMARY KEY,
  country char(2),          -- NULL for a body with international scope
  name    text NOT NULL,
  url     text,
  CONSTRAINT regulatory_authority_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$')
);

INSERT INTO regulatory_authority (code, country, name, url) VALUES
  -- Saudi Arabia
  ('zatca',    'sa', 'Zakat, Tax and Customs Authority',        'https://zatca.gov.sa'),
  ('sdaia',    'sa', 'Saudi Data and AI Authority',             'https://sdaia.gov.sa'),
  ('gosi',     'sa', 'General Organization for Social Insurance','https://gosi.gov.sa'),
  ('mhrsd',    'sa', 'Ministry of Human Resources and Social Development', 'https://hrsd.gov.sa'),
  ('qiwa',     'sa', 'Qiwa',                                    'https://qiwa.sa'),
  ('sama',     'sa', 'Saudi Central Bank',                      'https://sama.gov.sa'),
  ('moc',      'sa', 'Ministry of Commerce',                    'https://mc.gov.sa'),
  ('socpa',    'sa', 'Saudi Organization for Chartered and Professional Accountants', NULL),
  -- Bangladesh
  ('nbr',      'bd', 'National Board of Revenue',               'https://nbr.gov.bd'),
  ('bb',       'bd', 'Bangladesh Bank',                         'https://bb.org.bd'),
  ('rjsc',     'bd', 'Registrar of Joint Stock Companies and Firms', 'https://roc.gov.bd'),
  -- United States. Sales tax is administered per state, so there is no single
  -- national authority to name — the jurisdiction is part of the rule key.
  ('us_state', 'us', 'State revenue department (per jurisdiction)', NULL),
  ('irs',      'us', 'Internal Revenue Service',                'https://irs.gov');

ALTER TABLE regulatory_rule
  ADD CONSTRAINT regulatory_rule_authority_fk
  FOREIGN KEY (source_authority) REFERENCES regulatory_authority(code);

-- ---------------------------------------------------------------------------
-- Category (tree, unlimited depth — blueprint B1)
-- ---------------------------------------------------------------------------

CREATE TABLE category (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  parent_id  uuid REFERENCES category(id) ON DELETE RESTRICT,
  name       text NOT NULL,
  translations jsonb NOT NULL DEFAULT '{}'::jsonb,  -- {"ar": "...", "bn": "..."}

  -- Materialised ancestry, maintained by trigger. Breadcrumbs and
  -- "everything under Men's" are both common; walking parent_id per row for a
  -- category filter on a product list is not acceptable at POS speed.
  path       uuid[] NOT NULL DEFAULT '{}',
  depth      integer NOT NULL DEFAULT 0,

  sort_order integer NOT NULL DEFAULT 0,
  is_active  boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT category_name_not_blank CHECK (length(btrim(name)) > 0),
  CONSTRAINT category_not_own_parent CHECK (parent_id IS DISTINCT FROM id),
  -- A tree deep enough to hit this is a data-entry mistake, and an unbounded
  -- depth makes the recursive maintenance below unbounded too.
  CONSTRAINT category_depth_sane CHECK (depth BETWEEN 0 AND 10)
);

CREATE INDEX category_tenant_idx  ON category (tenant_id);
CREATE INDEX category_company_idx ON category (company_id, parent_id);
CREATE INDEX category_path_idx    ON category USING gin (path);

CREATE OR REPLACE FUNCTION category_maintain_path() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  parent_path  uuid[];
  parent_depth integer;
BEGIN
  IF NEW.parent_id IS NULL THEN
    NEW.path  := ARRAY[]::uuid[];
    NEW.depth := 0;
  ELSE
    SELECT path, depth INTO parent_path, parent_depth
    FROM category WHERE id = NEW.parent_id;

    IF parent_path IS NULL THEN
      RAISE EXCEPTION 'Parent category does not exist.' USING ERRCODE = 'P0001';
    END IF;
    -- A cycle would make the tree infinite and every descendant query hang.
    IF NEW.id = ANY(parent_path) THEN
      RAISE EXCEPTION 'A category cannot be placed inside one of its own subcategories.'
        USING ERRCODE = 'P0001';
    END IF;

    NEW.path  := parent_path || NEW.parent_id;
    NEW.depth := parent_depth + 1;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER category_path BEFORE INSERT OR UPDATE OF parent_id ON category
  FOR EACH ROW EXECUTE FUNCTION category_maintain_path();

CREATE TRIGGER category_touch BEFORE UPDATE ON category
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE category ENABLE ROW LEVEL SECURITY;
ALTER TABLE category FORCE  ROW LEVEL SECURITY;
CREATE POLICY category_isolation ON category
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Brand and unit of measure
-- ---------------------------------------------------------------------------

CREATE TABLE brand (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  name         text NOT NULL,
  translations jsonb NOT NULL DEFAULT '{}'::jsonb,
  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT brand_name_not_blank CHECK (length(btrim(name)) > 0)
);

CREATE UNIQUE INDEX brand_name_uq ON brand (company_id, lower(name));
CREATE INDEX brand_tenant_idx ON brand (tenant_id);

CREATE TRIGGER brand_touch BEFORE UPDATE ON brand
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE brand ENABLE ROW LEVEL SECURITY;
ALTER TABLE brand FORCE  ROW LEVEL SECURITY;
CREATE POLICY brand_isolation ON brand USING (tenant_id = current_tenant_id());

-- Units are tenant data rather than a fixed list, because the markets differ:
-- metric across Saudi Arabia and Bangladesh, imperial common in the USA, and
-- fabric sold by the metre or the yard depending on where the shop is.
CREATE TABLE unit_of_measure (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  code         text NOT NULL,            -- PCS, KG, M, YD, LB
  name         text NOT NULL,
  translations jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- Whether a quantity may be fractional. Selling 2.5 metres of fabric is
  -- normal; selling 2.5 shirts is a bug the POS should refuse.
  allows_fraction boolean NOT NULL DEFAULT false,

  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT unit_code_format CHECK (code ~ '^[A-Z0-9]{1,8}$')
);

CREATE UNIQUE INDEX unit_code_uq ON unit_of_measure (company_id, code);
CREATE INDEX unit_tenant_idx ON unit_of_measure (tenant_id);

ALTER TABLE unit_of_measure ENABLE ROW LEVEL SECURITY;
ALTER TABLE unit_of_measure FORCE  ROW LEVEL SECURITY;
CREATE POLICY unit_isolation ON unit_of_measure USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Product (the parent concept)
-- ---------------------------------------------------------------------------

CREATE TYPE product_lifecycle AS ENUM ('active', 'inactive', 'discontinued');

CREATE TABLE product (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  sku          text NOT NULL,
  name         text NOT NULL,
  translations jsonb NOT NULL DEFAULT '{}'::jsonb,
  description  text,

  category_id  uuid REFERENCES category(id)        ON DELETE RESTRICT,
  brand_id     uuid REFERENCES brand(id)           ON DELETE RESTRICT,
  unit_id      uuid REFERENCES unit_of_measure(id) ON DELETE RESTRICT,

  -- Country-scoped: 'standard' in Saudi, 'taxable' in the USA, and so on. The
  -- valid set for a company's country comes from the regulatory registry and
  -- is checked in the application, where the country and date are known.
  tax_treatment text NOT NULL DEFAULT 'standard',

  -- ZATCA requires the reason for any non-standard treatment to appear on the
  -- invoice. Other jurisdictions want it for audit even where not mandated.
  tax_exemption_reason_code text,

  track_serial    boolean NOT NULL DEFAULT false,
  track_batch     boolean NOT NULL DEFAULT false,
  warranty_months integer,

  lifecycle    product_lifecycle NOT NULL DEFAULT 'active',

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid REFERENCES app_user(id),

  CONSTRAINT product_name_not_blank CHECK (length(btrim(name)) > 0),
  CONSTRAINT product_sku_format CHECK (sku ~ '^[A-Za-z0-9._/-]{1,64}$'),
  CONSTRAINT product_warranty_sane CHECK (warranty_months IS NULL OR warranty_months BETWEEN 0 AND 600),
  CONSTRAINT product_tax_treatment_format CHECK (tax_treatment ~ '^[a-z][a-z0-9_]*$')
);

CREATE UNIQUE INDEX product_sku_uq ON product (company_id, lower(sku));
CREATE INDEX product_tenant_idx   ON product (tenant_id);
CREATE INDEX product_category_idx ON product (company_id, category_id) WHERE lifecycle = 'active';
CREATE INDEX product_brand_idx    ON product (company_id, brand_id)    WHERE lifecycle = 'active';
CREATE INDEX product_name_trgm    ON product USING gin (name gin_trgm_ops);
CREATE INDEX product_translations_idx ON product USING gin (translations);

CREATE TRIGGER product_touch BEFORE UPDATE ON product
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- Blueprint B1: a discontinued product must still display correctly on old
-- invoices and reports, so there is no delete path. Lifecycle carries the
-- state instead.
CREATE TRIGGER product_no_delete BEFORE DELETE ON product
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE product ENABLE ROW LEVEL SECURITY;
ALTER TABLE product FORCE  ROW LEVEL SECURITY;
CREATE POLICY product_isolation ON product USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Variant — the actual stockkeeping unit
-- ---------------------------------------------------------------------------

-- Blueprint B2: standard POS logic (one product = one price = one stock count)
-- does not work for clothing. The variant carries its own SKU, barcode, price,
-- cost and stock, and every sale line, stock movement and cost layer points
-- here rather than at the product.
--
-- A product with no variation still gets exactly one variant row. Treating
-- "simple products" as a separate case would double every path in inventory,
-- costing and sales, and the two would drift.
CREATE TABLE variant (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  product_id  uuid NOT NULL REFERENCES product(id) ON DELETE CASCADE,

  sku         text NOT NULL,
  barcode     text,

  -- {"size":"L","color":"Black","season":"Winter"} — unlimited custom
  -- attributes per blueprint B2, so a fixed size/colour pair would not do.
  attributes  jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- Four price tiers (B1). numeric(18,4), never float: 0.15 has no exact
  -- binary representation and a price wrong in the last minor unit is wrong on
  -- a tax invoice.
  price_retail    numeric(18,4) NOT NULL DEFAULT 0,
  price_wholesale numeric(18,4),
  price_dealer    numeric(18,4),

  -- The lowest price a cashier may ever sell at, "even after discount —
  -- enforced by the system, not just policy" (B1).
  price_floor     numeric(18,4),

  cost_standard   numeric(18,4),
  weight          numeric(18,4),
  image_url       text,

  reorder_level   numeric(18,4),
  max_level       numeric(18,4),

  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT variant_sku_format CHECK (sku ~ '^[A-Za-z0-9._/-]{1,64}$'),
  CONSTRAINT variant_prices_non_negative CHECK (
    price_retail >= 0
    AND (price_wholesale IS NULL OR price_wholesale >= 0)
    AND (price_dealer    IS NULL OR price_dealer    >= 0)
    AND (price_floor     IS NULL OR price_floor     >= 0)
    AND (cost_standard   IS NULL OR cost_standard   >= 0)
  ),
  -- A floor above the retail price would make every sale illegal, which is a
  -- data-entry error rather than a policy.
  CONSTRAINT variant_floor_below_retail CHECK (
    price_floor IS NULL OR price_retail = 0 OR price_floor <= price_retail
  ),
  CONSTRAINT variant_levels_sane CHECK (
    reorder_level IS NULL OR max_level IS NULL OR max_level >= reorder_level
  )
);

CREATE UNIQUE INDEX variant_sku_uq ON variant (company_id, lower(sku));

-- A barcode identifies one variant within a company. Scanning must be
-- unambiguous or the POS picks the wrong item at speed.
CREATE UNIQUE INDEX variant_barcode_uq
  ON variant (company_id, barcode) WHERE barcode IS NOT NULL;

CREATE INDEX variant_tenant_idx     ON variant (tenant_id);
CREATE INDEX variant_product_idx    ON variant (product_id);
CREATE INDEX variant_attributes_idx ON variant USING gin (attributes);

CREATE TRIGGER variant_touch BEFORE UPDATE ON variant
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER variant_no_delete BEFORE DELETE ON variant
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE variant ENABLE ROW LEVEL SECURITY;
ALTER TABLE variant FORCE  ROW LEVEL SECURITY;
CREATE POLICY variant_isolation ON variant USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Country tax treatments in the registry
-- ---------------------------------------------------------------------------

-- Saudi's list already exists (SA.VAT.TAX_TREATMENTS, migration 0005). These
-- add the other two launch markets so the application can validate a product's
-- treatment against its company's country from day one.
--
-- Both are unverified, like every seeded legal value.
INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority, source_document, notes)
VALUES
('BD.VAT.TAX_TREATMENTS', 'bd',
 '{"treatments": ["standard", "reduced", "zero_rated", "exempt", "out_of_scope", "export"],
   "model": "vat", "input_tax_recoverable": true}'::jsonb,
 '2020-01-01', 'nbr', 'VAT and Supplementary Duty Act 2012 — VERIFY AGAINST NBR',
 'Placeholder shape only. Bangladesh applies several VAT rates rather than one national rate, and invoicing follows the Mushak forms. Confirm the treatment list, the rates and the Mushak requirements against the NBR before Bangladesh ships.'),

('US.SALESTAX.TAX_TREATMENTS', 'us',
 '{"treatments": ["taxable", "non_taxable", "exempt"],
   "model": "sales_tax", "input_tax_recoverable": false}'::jsonb,
 '2020-01-01', 'us_state', 'State revenue departments — VERIFY PER JURISDICTION',
 'US sales tax is NOT a VAT. It is charged only at the final retail sale, so there is no input tax to reclaim, and any report built on "output minus input" is meaningless here. Rates are set by state, county and city; sourcing may be origin- or destination-based; and exempt sales require a certificate held per customer. Rate resolution therefore needs a jurisdiction, not just a country and a date. Economic nexus rules decide whether a seller must collect at all.');
