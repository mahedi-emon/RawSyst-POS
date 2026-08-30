-- 0095 — The label studio (blueprint B3) and what global search reads (D7).
--
-- # Why a label is a template, and a barcode is not
--
-- B3 asks for two different things and they need different homes.
--
-- The BARCODE is a value on the variant, and it is already there: `barcode`
-- has existed since 0010. What has never existed is the RULE that builds a
-- meaningful one — B3's "M-WIN-BLK-XL = Men / Winter / Black / XL" — because
-- that rule is a property of the shop, not of any product. So a company gets
-- one barcode scheme, and every variant it generates a code for follows it.
--
-- The LABEL is a layout: what appears on the piece of paper, at what size, for
-- which printer. B3 lists a hang tag, a thermal sticker, an A4 sheet and a
-- loyalty card, and they differ in size and in what they show rather than in
-- how they are produced. One table with a kind, a size and a field list serves
-- all four; four tables would be four render paths and the fourth would be the
-- one nobody tested.
--
-- # No table for a print run
--
-- B3's bulk generator produces a thousand labels from a thousand variants. That
-- is a READ — a render of rows that already exist — and recording it would add
-- a table that grows without bound and answers no question anybody asks. What
-- IS worth recording is that a barcode was assigned, and that lands on the
-- variant where it belongs.
--
-- # The search indexes
--
-- D7 wants one box that finds a product, a customer, a supplier, an invoice, an
-- order, an employee or a serial number. Without an index, every one of those
-- is a sequential scan behind a `%term%`, and the box that is supposed to feel
-- instant takes two seconds on a real shop's data. pg_trgm makes a leading
-- wildcard indexable, which is what a search box needs: somebody typing "haddad"
-- expects to find "Layla Haddad".

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- How this shop builds a meaningful barcode
-- ---------------------------------------------------------------------------

CREATE TABLE barcode_scheme (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- The parts, in order, that make up a generated code:
  --   ["category", "season", "colour", "size"]
  --
  -- A list rather than four columns, because a shop that sells shoes wants
  -- width where a shop that sells abayas wants length, and neither should need
  -- a migration to say so.
  parts       jsonb NOT NULL DEFAULT '["category","colour","size"]'::jsonb,
  separator   text NOT NULL DEFAULT '-',

  -- Which symbology the printed code carries. The human-readable string above
  -- is what a person reads; this is what the scanner reads, and they are not
  -- the same thing: EAN-13 is thirteen digits and cannot hold "M-WIN-BLK-XL".
  symbology   text NOT NULL DEFAULT 'code128',

  -- Upper-cased and truncated so the parts line up in a column on a shelf
  -- edge. Three characters is enough to tell BLK from BLU and short enough
  -- that four parts fit on a 25mm label.
  part_length integer NOT NULL DEFAULT 3,

  -- A prefix every code carries, for a shop whose codes have to be
  -- distinguishable from a supplier's.
  prefix      text,

  updated_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT barcode_scheme_parts_is_a_list CHECK (
    jsonb_typeof(parts) = 'array' AND jsonb_array_length(parts) > 0),
  CONSTRAINT barcode_scheme_symbology_valid CHECK (symbology IN (
    'code128', 'ean13', 'ean8', 'upca', 'qr', 'datamatrix')),
  CONSTRAINT barcode_scheme_part_length_sane CHECK (
    part_length BETWEEN 1 AND 8),
  CONSTRAINT barcode_scheme_separator_short CHECK (length(separator) <= 2)
);

CREATE UNIQUE INDEX barcode_scheme_company_uq ON barcode_scheme (company_id);
CREATE INDEX barcode_scheme_tenant_idx ON barcode_scheme (tenant_id);

CREATE TRIGGER barcode_scheme_touch BEFORE UPDATE ON barcode_scheme
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE barcode_scheme ENABLE ROW LEVEL SECURITY;
ALTER TABLE barcode_scheme FORCE  ROW LEVEL SECURITY;
CREATE POLICY barcode_scheme_isolation ON barcode_scheme
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- What goes on the label
-- ---------------------------------------------------------------------------

CREATE TABLE label_template (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,

  -- hang_tag     — the tag that hangs on the garment (B3)
  -- thermal      — a sticker off a Xprinter, Zebra or TSC
  -- a4_sheet     — a laser sheet of 24, 30 or 48
  -- loyalty_card — B16's customer card, which is a label with a different
  --                barcode on it
  kind        text NOT NULL,

  -- Millimetres, because that is what every label roll and every printer
  -- driver is specified in, and because 50x25 is a number a shopkeeper reads
  -- off the box the labels came in.
  width_mm    numeric(8,2) NOT NULL,
  height_mm   numeric(8,2) NOT NULL,

  -- For an A4 sheet: how the labels are laid out on it. Null for the other
  -- kinds, where the sheet is the label.
  columns     integer,
  rows        integer,
  margin_mm   numeric(8,2) NOT NULL DEFAULT 0,
  gap_mm      numeric(8,2) NOT NULL DEFAULT 0,

  -- Which fields are printed, in order:
  --   [{"field":"name","size":9,"bold":true},
  --    {"field":"name_ar","size":9,"rtl":true},
  --    {"field":"price","size":11},
  --    {"field":"barcode","height":12}]
  --
  -- B3 names logo, English and Arabic product name, size, colour, VAT-inclusive
  -- price and the barcode. A list, because a shop that does not sell to Arabic
  -- speakers should not print an empty line, and one that sells only by weight
  -- has no size to print.
  fields      jsonb NOT NULL DEFAULT '[]'::jsonb,

  -- The default for its kind. A cashier printing a tag should not have to
  -- choose between four templates every time.
  is_default  boolean NOT NULL DEFAULT false,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT label_template_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT label_template_kind_valid CHECK (kind IN (
    'hang_tag', 'thermal', 'a4_sheet', 'loyalty_card')),
  CONSTRAINT label_template_size_positive CHECK (
    width_mm > 0 AND height_mm > 0),
  CONSTRAINT label_template_fields_is_a_list CHECK (
    jsonb_typeof(fields) = 'array'),
  -- A sheet needs a grid; a single label must not carry one, or a render has
  -- to decide which of two contradictory layouts to believe.
  CONSTRAINT label_template_sheet_has_a_grid CHECK (
    CASE WHEN kind = 'a4_sheet'
         THEN columns IS NOT NULL AND columns > 0
              AND rows IS NOT NULL AND rows > 0
         ELSE columns IS NULL AND rows IS NULL END),
  CONSTRAINT label_template_spacing_sane CHECK (
    margin_mm >= 0 AND gap_mm >= 0)
);

CREATE UNIQUE INDEX label_template_name_uq
  ON label_template (company_id, lower(name));
-- One default per kind. Two would leave the print button choosing arbitrarily.
CREATE UNIQUE INDEX label_template_default_uq
  ON label_template (company_id, kind) WHERE is_default;
CREATE INDEX label_template_tenant_idx ON label_template (tenant_id);

CREATE TRIGGER label_template_touch BEFORE UPDATE ON label_template
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE label_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE label_template FORCE  ROW LEVEL SECURITY;
CREATE POLICY label_template_isolation ON label_template
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The variant attributes a smart barcode is built from
-- ---------------------------------------------------------------------------
--
-- 0010's variant carries `attributes` as jsonb — colour, size, and whatever
-- else a shop uses. A generated code reads from there, and from the product's
-- category and brand. Season is the one part B3 names that nothing holds, so
-- it goes on the product: a shop's winter range is a property of the garment,
-- not of one size of it.

ALTER TABLE product
  ADD COLUMN IF NOT EXISTS season text;

-- ---------------------------------------------------------------------------
-- What global search reads (D7)
-- ---------------------------------------------------------------------------
--
-- Trigram indexes, so a leading wildcard is indexable. Somebody typing
-- "haddad" expects to find "Layla Haddad", and `name LIKE '%haddad%'` without
-- one of these is a sequential scan of every customer the shop has.

CREATE INDEX IF NOT EXISTS product_name_trgm_idx
  ON product USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS variant_sku_trgm_idx
  ON variant USING gin (sku gin_trgm_ops);
CREATE INDEX IF NOT EXISTS variant_barcode_trgm_idx
  ON variant USING gin (barcode gin_trgm_ops) WHERE barcode IS NOT NULL;
CREATE INDEX IF NOT EXISTS customer_name_trgm_idx
  ON customer USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS customer_phone_trgm_idx
  ON customer USING gin (phone gin_trgm_ops) WHERE phone IS NOT NULL;
CREATE INDEX IF NOT EXISTS supplier_name_trgm_idx
  ON supplier USING gin (legal_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS employee_name_trgm_idx
  ON employee USING gin (full_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS sales_invoice_number_trgm_idx
  ON sales_invoice USING gin (human_number gin_trgm_ops)
  WHERE human_number IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------
--
-- `label.print` is separate from `label.manage` for the same reason `sales.
-- refund` is separate from `sales.create`: printing a shelf ticket is a daily
-- act by whoever is putting stock out, and redesigning the tag that carries the
-- shop's price and logo is not.
--
-- Analytics reads through `report.view`, which already exists and which the
-- dashboard uses. A second verb for the same figures grouped differently would
-- be a permission an administrator has to discover before the screen works.
--
-- Global search carries no permission at all. It is a lens over what the caller
-- can already reach: every branch of the query is filtered by the permissions
-- that guard the thing it finds, so a cashier searching "Haddad" gets the
-- customer only if they could have opened the customer screen.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'label.print'), ('owner',            'label.manage'),
  ('store_manager',    'label.print'), ('store_manager',    'label.manage'),
  ('inventory_keeper', 'label.print'),
  ('cashier',          'label.print'),
  ('purchase_manager', 'label.print')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

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
      ('owner',            'label.print'), ('owner',            'label.manage'),
      ('store_manager',    'label.print'), ('store_manager',    'label.manage'),
      ('inventory_keeper', 'label.print'),
      ('cashier',          'label.print'),
      ('purchase_manager', 'label.print')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- A starting set of templates for every company
-- ---------------------------------------------------------------------------
--
-- Seeded rather than left empty, because B3's sizes are not preferences: 50x25
-- and 38x25 are what the label rolls come in, and a shop opening the studio to
-- an empty list has to measure a sticker before they can print one. A shop that
-- wants something else edits these.

INSERT INTO label_template
  (tenant_id, company_id, name, kind, width_mm, height_mm, columns, rows,
   margin_mm, gap_mm, fields, is_default)
SELECT c.tenant_id, c.id, v.name, v.kind, v.width, v.height,
       v.columns, v.rows, v.margin, v.gap, v.fields::jsonb, v.is_default
FROM company c
CROSS JOIN (VALUES
  ('Hang tag', 'hang_tag', 50.0, 80.0, NULL::integer, NULL::integer, 3.0, 0.0,
   '[{"field":"logo"},{"field":"name","size":10,"bold":true},
     {"field":"name_ar","size":10,"rtl":true},
     {"field":"attributes","size":8},
     {"field":"price","size":13,"bold":true},
     {"field":"barcode","height":14}]', true),
  ('Thermal 50x25', 'thermal', 50.0, 25.0, NULL, NULL, 1.0, 0.0,
   '[{"field":"name","size":7},{"field":"price","size":9,"bold":true},
     {"field":"barcode","height":10}]', true),
  ('Thermal 38x25', 'thermal', 38.0, 25.0, NULL, NULL, 1.0, 0.0,
   '[{"field":"name","size":6},{"field":"price","size":8,"bold":true},
     {"field":"barcode","height":9}]', false),
  ('A4 sheet of 24', 'a4_sheet', 63.5, 33.9, 3, 8, 8.0, 2.5,
   '[{"field":"name","size":7},{"field":"price","size":9,"bold":true},
     {"field":"barcode","height":10}]', true),
  ('A4 sheet of 30', 'a4_sheet', 63.5, 25.4, 3, 10, 8.0, 0.0,
   '[{"field":"name","size":6},{"field":"price","size":8,"bold":true},
     {"field":"barcode","height":9}]', false),
  ('Loyalty card', 'loyalty_card', 85.6, 53.98, NULL, NULL, 4.0, 0.0,
   '[{"field":"logo"},{"field":"customer_name","size":11,"bold":true},
     {"field":"tier","size":9},{"field":"barcode","height":12}]', true)
) AS v(name, kind, width, height, columns, rows, margin, gap, fields, is_default)
ON CONFLICT DO NOTHING;

INSERT INTO barcode_scheme (tenant_id, company_id)
SELECT c.tenant_id, c.id FROM company c
ON CONFLICT DO NOTHING;
