-- 0084 — Promotions, discounts and the pricing engine (blueprint B9).
--
-- B9 lists eleven kinds of promotion. They are not eleven mechanisms: they are
-- three, crossed with what they apply to.
--
--   WHAT IT DOES        percentage off, a fixed amount off,
--                       buy X get Y, a flat price for a quantity
--   WHAT IT APPLIES TO  everything, a category, a brand, a product,
--                       a customer type
--   WHEN IT APPLIES     dates, stores, a minimum purchase, a coupon code
--
-- Modelling those as eleven tables would produce eleven code paths through the
-- till, and the eleventh would be the one nobody tested. One promotion with a
-- kind, a scope and a set of conditions is the same feature with one path
-- through it.
--
-- # Why a promotion is not a price
--
-- The catalogue already carries four price tiers and a floor. A promotion is
-- not a fifth price: it is a REASON a line came out cheaper, and the difference
-- matters at three separate moments.
--
--   * The receipt has to say why, or a customer cannot check it.
--   * The floor price still applies. B1 calls it the price "even after
--     discount — enforced by the system, not just policy", and a promotion that
--     bypassed it would be a promotion that sells at a loss silently.
--   * D2's campaign analytics asks what each campaign cost and what it
--     brought in, which is answerable only if the discount stayed attached to
--     the campaign that caused it.
--
-- So a promotion produces a discount ON a line, recorded against the promotion,
-- and the price it started from is still the price.

CREATE TABLE promotion (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,
  name        text NOT NULL,
  name_ar     text,

  -- What it does.
  kind        text NOT NULL,

  -- The figure the kind reads. Its meaning depends on the kind, which is the
  -- one place this table is not self-describing and the reason each kind names
  -- what it expects in a CHECK below.
  --
  --   percentage    5 means five per cent off
  --   amount        5 means five off the line
  --   buy_x_get_y   unused; buy_qty and get_qty carry it
  --   bundle_price  the flat price for `buy_qty` of them
  value       numeric(18,4),

  buy_qty     numeric(18,4),
  get_qty     numeric(18,4),

  -- What it applies to. All four null means everything in the shop.
  category_id uuid REFERENCES category(id) ON DELETE CASCADE,
  brand_id    uuid REFERENCES brand(id)    ON DELETE CASCADE,
  variant_id  uuid REFERENCES variant(id)  ON DELETE CASCADE,
  customer_type text,

  -- When it applies.
  starts_on   date,
  ends_on     date,
  store_id    uuid REFERENCES store(id) ON DELETE CASCADE,
  min_purchase numeric(18,4),

  -- A coupon code the customer types, or null for a promotion that applies by
  -- itself. B9 lists "Coupon/Promo codes" as its own kind of promotion; it is
  -- really a CONDITION on any of them, which is why it is a column here rather
  -- than a twelfth kind.
  coupon_code text,

  -- How many times a coupon may be used in total, and by one customer. Null
  -- for no limit. A coupon with no cap is how a discount code posted publicly
  -- takes a shop's margin for a week.
  max_uses    integer,
  max_uses_per_customer integer,

  -- Where a promotion's cost lands. B9 asks for the total discount cost per
  -- campaign, and 4200 Sales Discounts is where every discount already goes;
  -- keeping the promotion id on the line is what makes the campaign figure
  -- answerable.
  is_active   boolean NOT NULL DEFAULT true,
  priority    integer NOT NULL DEFAULT 0,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT promotion_kind_valid CHECK (kind IN (
    'percentage', 'amount', 'buy_x_get_y', 'bundle_price')),

  -- Each kind says what it needs, so a promotion that cannot be applied cannot
  -- be saved. A percentage promotion with no percentage on it is the kind of
  -- row that sits harmlessly in a table until the first customer tries to use
  -- it at a till in front of a queue.
  CONSTRAINT promotion_has_what_it_needs CHECK (
    CASE kind
      WHEN 'percentage'   THEN value IS NOT NULL AND value > 0 AND value <= 100
      WHEN 'amount'       THEN value IS NOT NULL AND value > 0
      WHEN 'buy_x_get_y'  THEN buy_qty IS NOT NULL AND buy_qty > 0
                           AND get_qty IS NOT NULL AND get_qty > 0
      WHEN 'bundle_price' THEN value IS NOT NULL AND value >= 0
                           AND buy_qty IS NOT NULL AND buy_qty > 0
    END),

  CONSTRAINT promotion_dates_ordered CHECK (
    starts_on IS NULL OR ends_on IS NULL OR ends_on >= starts_on),
  CONSTRAINT promotion_min_purchase_sane CHECK (
    min_purchase IS NULL OR min_purchase > 0),
  CONSTRAINT promotion_uses_sane CHECK (
    (max_uses IS NULL OR max_uses > 0)
    AND (max_uses_per_customer IS NULL OR max_uses_per_customer > 0)),

  -- A coupon-less promotion cannot have a use cap: the caps count coupon
  -- redemptions, and an automatic promotion is not redeemed, it just applies.
  CONSTRAINT promotion_caps_need_a_coupon CHECK (
    coupon_code IS NOT NULL
    OR (max_uses IS NULL AND max_uses_per_customer IS NULL))
);

CREATE UNIQUE INDEX promotion_code_uq ON promotion (company_id, code);
CREATE UNIQUE INDEX promotion_coupon_uq
  ON promotion (company_id, upper(coupon_code))
  WHERE coupon_code IS NOT NULL;
CREATE INDEX promotion_tenant_idx ON promotion (tenant_id);
-- The index the till reads: everything live, in the order it should be tried.
CREATE INDEX promotion_live_idx
  ON promotion (company_id, priority DESC, id)
  WHERE is_active;

CREATE TRIGGER promotion_touch BEFORE UPDATE ON promotion
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE promotion ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion FORCE  ROW LEVEL SECURITY;
CREATE POLICY promotion_isolation ON promotion
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- What a promotion actually did
-- ---------------------------------------------------------------------------
--
-- One row per promotion per invoice line it discounted.
--
-- D2 asks for "sales generated per campaign, total discount cost, new customers
-- acquired, profit impact/ROI". None of that is answerable from a discount
-- column on a line: it needs the discount attributed to the campaign that
-- caused it, on the invoice it appeared on, for the customer who got it.

CREATE TABLE promotion_redemption (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  promotion_id uuid NOT NULL REFERENCES promotion(id) ON DELETE RESTRICT,

  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  customer_id  uuid REFERENCES customer(id) ON DELETE SET NULL,

  -- What the customer saved, and what the shop sold. Both, because the ratio
  -- between them is the campaign's cost and neither alone gives it.
  discount     numeric(18,4) NOT NULL,
  line_total   numeric(18,4) NOT NULL,

  redeemed_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT promotion_redemption_discount_positive CHECK (discount > 0)
);

CREATE INDEX promotion_redemption_promo_idx
  ON promotion_redemption (promotion_id, redeemed_at DESC);
CREATE INDEX promotion_redemption_invoice_idx
  ON promotion_redemption (invoice_id);
CREATE INDEX promotion_redemption_customer_idx
  ON promotion_redemption (customer_id, promotion_id)
  WHERE customer_id IS NOT NULL;
CREATE INDEX promotion_redemption_tenant_idx ON promotion_redemption (tenant_id);

ALTER TABLE promotion_redemption ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_redemption FORCE  ROW LEVEL SECURITY;
CREATE POLICY promotion_redemption_isolation ON promotion_redemption
  USING (tenant_id = current_tenant_id());

-- A redemption is a record of a discount that was given. It does not change.
CREATE TRIGGER promotion_redemption_no_change
  BEFORE UPDATE OR DELETE ON promotion_redemption
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------
--
-- `sales.discount` already exists and is what a cashier holds to discount a
-- line at the till. Setting up a CAMPAIGN is a different act: it decides what
-- every till in every branch will charge, and B9 puts manager authorisation
-- around a discount far smaller than that.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'promotion.view'), ('owner',         'promotion.manage'),
  ('store_manager', 'promotion.view'),
  ('cashier',       'promotion.view'),
  ('accountant',    'promotion.view'),
  ('auditor',       'promotion.view')
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
      ('owner',         'promotion.view'), ('owner',         'promotion.manage'),
      ('store_manager', 'promotion.view'),
      ('cashier',       'promotion.view'),
      ('accountant',    'promotion.view'),
      ('auditor',       'promotion.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
