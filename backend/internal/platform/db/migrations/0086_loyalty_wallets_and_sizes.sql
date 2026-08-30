-- 0086 — Loyalty, store credit, gift cards and fitting history (blueprint B16).
--
-- # A till can already spend money nobody has
--
-- `store_credit` and `loyalty_points` have been accepted tenders since 0018,
-- and account 2300 and 2400 have existed since the first chart. What has never
-- existed is a BALANCE. A cashier could settle a 400 riyal sale with 400 riyals
-- of store credit against a customer who had none, the sale would post, the
-- liability would go negative, and nothing anywhere would say so. This
-- migration is where that money starts existing.
--
-- # One ledger, not two
--
-- A customer wallet and a gift card are the same thing: money the shop owes to
-- whoever holds it, sitting in account 2300. Splitting them into two tables
-- would give one control account two subsidiary ledgers, two balance
-- calculations to keep in agreement, and two paths through the till — and the
-- second one is the one nobody tests. `store_credit_entry` carries both, with
-- exactly one of `customer_id` and `gift_card_id` set.
--
-- # Balances are summed, never stored
--
-- No `balance` column on a customer, a gift card or a points account. A figure
-- computed twice is a figure that eventually disagrees with itself, and this
-- one has to tie to the general ledger: the sum of every store credit entry for
-- a company IS the balance of account 2300, and an UPDATE-able column would let
-- the two drift with nothing to notice.
--
-- # Expiry is an entry, not a flag
--
-- Gift cards and points both expire. Expiring them by setting a status would
-- mean the balance arithmetic had to know about dates as well as amounts.
-- Writing a negative entry keeps the balance one sum, keeps the expiry visible
-- in the customer's history where a person asking "where did my points go" can
-- be shown it, and posts the write-back to the P&L like any other movement.
--
-- # Points are whole numbers
--
-- B16's example is "100 SAR spent = 1 point". Fractional points are an argument
-- with a customer that nobody wins, so accrual rounds DOWN to a whole point and
-- the remainder is simply not earned — the shop never owes a third of a point
-- and the customer is never told they have one.

-- ---------------------------------------------------------------------------
-- The programme
-- ---------------------------------------------------------------------------
--
-- One per company, and it may not exist: a shop that does not run a loyalty
-- scheme has no row, which is different from a row with everything set to
-- zero. Points cannot be earned or spent without one.

CREATE TABLE loyalty_program (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  is_active    boolean NOT NULL DEFAULT true,

  -- How much has to be spent to earn one point. B16's example is 100.
  spend_per_point numeric(18,4) NOT NULL DEFAULT 100,
  -- What one point is worth when it is spent. Kept separate from the accrual
  -- rate on purpose: a shop that wants to make points more generous changes one
  -- number, and a shop that wants each point to buy more changes the other.
  -- Folding them into a single "rate" makes both changes the same change.
  point_value  numeric(18,4) NOT NULL DEFAULT 1,

  -- Points earned expire this many months after they were earned. Null means
  -- they do not. An expiry the customer was never told about is a complaint,
  -- so the number is the shop's to set and to print.
  expiry_months integer,

  -- The tiers, in ascending order of lifetime spend:
  --   [{"key":"bronze","name":"Bronze","name_ar":"...","min_spend":"0",
  --     "discount_percent":"0"}, ...]
  --
  -- jsonb rather than a table because a tier is read as a set — "which of these
  -- five does this customer fall into" — never joined to, and a shop changes
  -- all of them at once or none of them.
  tiers        jsonb NOT NULL DEFAULT '[]'::jsonb,

  updated_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT loyalty_program_spend_positive CHECK (spend_per_point > 0),
  CONSTRAINT loyalty_program_value_positive CHECK (point_value > 0),
  CONSTRAINT loyalty_program_expiry_sane CHECK (
    expiry_months IS NULL OR expiry_months > 0),
  CONSTRAINT loyalty_program_tiers_is_a_list CHECK (
    jsonb_typeof(tiers) = 'array')
);

CREATE UNIQUE INDEX loyalty_program_company_uq ON loyalty_program (company_id);
CREATE INDEX loyalty_program_tenant_idx ON loyalty_program (tenant_id);

CREATE TRIGGER loyalty_program_touch BEFORE UPDATE ON loyalty_program
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE loyalty_program ENABLE ROW LEVEL SECURITY;
ALTER TABLE loyalty_program FORCE  ROW LEVEL SECURITY;
CREATE POLICY loyalty_program_isolation ON loyalty_program
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The points ledger
-- ---------------------------------------------------------------------------

CREATE TABLE loyalty_entry (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  customer_id uuid NOT NULL REFERENCES customer(id) ON DELETE CASCADE,

  -- Signed. Earning is positive, spending and expiry negative, and an
  -- adjustment is whichever a manager decided. One column rather than
  -- earned/spent pairs, so a balance is one SUM and cannot be got wrong by
  -- subtracting in the wrong order.
  points      integer NOT NULL,

  reason      text NOT NULL,

  invoice_id  uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  -- What the points were earned on, so a shop can answer "how much did this
  -- scheme cost" without joining back through the invoices.
  spend       numeric(18,4),

  -- When these points stop being spendable. Null for points that do not
  -- expire, and always null on a negative entry: money going out does not
  -- have a use-by date.
  expires_on  date,

  note        text,
  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT loyalty_entry_points_not_zero CHECK (points <> 0),
  CONSTRAINT loyalty_entry_reason_valid CHECK (reason IN (
    'earned', 'redeemed', 'expired', 'adjusted', 'reversed')),
  CONSTRAINT loyalty_entry_expiry_only_on_earning CHECK (
    expires_on IS NULL OR points > 0)
);

CREATE INDEX loyalty_entry_customer_idx
  ON loyalty_entry (customer_id, created_at DESC);
CREATE INDEX loyalty_entry_invoice_idx ON loyalty_entry (invoice_id)
  WHERE invoice_id IS NOT NULL;
-- The index the expiry sweep reads.
CREATE INDEX loyalty_entry_expiring_idx ON loyalty_entry (company_id, expires_on)
  WHERE expires_on IS NOT NULL;
CREATE INDEX loyalty_entry_tenant_idx ON loyalty_entry (tenant_id);

ALTER TABLE loyalty_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE loyalty_entry FORCE  ROW LEVEL SECURITY;
CREATE POLICY loyalty_entry_isolation ON loyalty_entry
  USING (tenant_id = current_tenant_id());

-- A ledger entry is a record of something that happened. Correcting one is a
-- new entry with the opposite sign, which is why `reversed` is a reason.
CREATE TRIGGER loyalty_entry_no_change
  BEFORE UPDATE OR DELETE ON loyalty_entry
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- Gift cards
-- ---------------------------------------------------------------------------
--
-- A gift card is a bearer instrument: whoever holds the card can spend it, so
-- the code is stored as it is printed rather than hashed. Hashing it would stop
-- a cashier looking up a card a customer has half-rubbed-off, which is the most
-- common thing that happens to them, and would protect nothing that holding the
-- card does not already give away.

CREATE TABLE gift_card (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,

  -- What it was sold for. The BALANCE is the sum of its entries; this is what
  -- it started at, which a shop needs to answer "what did we sell" separately
  -- from "what is left on it".
  face_value  numeric(18,4) NOT NULL,
  currency    text NOT NULL,

  expires_on  date,

  -- Who it was issued to, when a shop chose to record it. A gift card is
  -- usually anonymous, which is the point of one.
  customer_id uuid REFERENCES customer(id) ON DELETE SET NULL,

  -- Voided cards cannot be spent. A card is voided when it is reported lost,
  -- and its remaining balance is written back by an entry rather than by this
  -- column, so the liability moves for a reason somebody can read.
  is_void     boolean NOT NULL DEFAULT false,
  void_reason text,

  note        text,
  issued_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  issued_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT gift_card_face_positive CHECK (face_value > 0),
  CONSTRAINT gift_card_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT gift_card_void_says_why CHECK (
    NOT is_void OR void_reason IS NOT NULL)
);

CREATE UNIQUE INDEX gift_card_code_uq ON gift_card (company_id, upper(code));
CREATE INDEX gift_card_customer_idx ON gift_card (customer_id)
  WHERE customer_id IS NOT NULL;
CREATE INDEX gift_card_tenant_idx ON gift_card (tenant_id);

ALTER TABLE gift_card ENABLE ROW LEVEL SECURITY;
ALTER TABLE gift_card FORCE  ROW LEVEL SECURITY;
CREATE POLICY gift_card_isolation ON gift_card
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The store credit ledger
-- ---------------------------------------------------------------------------
--
-- The subsidiary ledger of account 2300. Every row here has a matching journal
-- line, and the sum of the two agrees by construction rather than by a nightly
-- job that hopes.

CREATE TABLE store_credit_entry (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Exactly one of these. A wallet belongs to a customer; a gift card belongs
  -- to whoever is holding it.
  customer_id  uuid REFERENCES customer(id)  ON DELETE CASCADE,
  gift_card_id uuid REFERENCES gift_card(id) ON DELETE CASCADE,

  -- Signed, in the company's base currency. Positive is credit given to the
  -- customer, negative is credit spent or written back.
  amount       numeric(18,4) NOT NULL,
  currency     text NOT NULL,

  reason       text NOT NULL,

  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  expires_on   date,
  note         text,
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT store_credit_entry_amount_not_zero CHECK (amount <> 0),
  CONSTRAINT store_credit_entry_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT store_credit_entry_reason_valid CHECK (reason IN (
    'issued', 'refunded', 'redeemed', 'expired', 'voided', 'adjusted')),
  CONSTRAINT store_credit_entry_belongs_to_exactly_one CHECK (
    (customer_id IS NOT NULL) <> (gift_card_id IS NOT NULL))
);

CREATE INDEX store_credit_entry_customer_idx
  ON store_credit_entry (customer_id, created_at DESC)
  WHERE customer_id IS NOT NULL;
CREATE INDEX store_credit_entry_card_idx
  ON store_credit_entry (gift_card_id, created_at DESC)
  WHERE gift_card_id IS NOT NULL;
CREATE INDEX store_credit_entry_invoice_idx ON store_credit_entry (invoice_id)
  WHERE invoice_id IS NOT NULL;
CREATE INDEX store_credit_entry_tenant_idx ON store_credit_entry (tenant_id);

ALTER TABLE store_credit_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_credit_entry FORCE  ROW LEVEL SECURITY;
CREATE POLICY store_credit_entry_isolation ON store_credit_entry
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER store_credit_entry_no_change
  BEFORE UPDATE OR DELETE ON store_credit_entry
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- Fitting history
-- ---------------------------------------------------------------------------
--
-- B16 calls this "fashion-specific, high-value": a customer walks in, and the
-- staff know their collar size without asking. It is deliberately not a column
-- on the customer — a person has a shirt size AND a trouser size AND a shoe
-- size, each confirmed on a different day, and each of those is a row.

CREATE TABLE customer_size (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)   ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)  ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customer(id) ON DELETE CASCADE,

  -- What it is a size for: shirt, trousers, shoes, jacket. Free text rather
  -- than an enumeration, because a shop that sells thobes and abayas has
  -- garments no list written in advance would contain.
  garment      text NOT NULL,
  size         text NOT NULL,

  -- The measurements behind the size, when somebody took them:
  --   {"collar": "16", "sleeve": "34", "waist": "34", "unit": "in"}
  -- Kept beside the size rather than instead of it: a customer knows they are
  -- a large, and a tailor knows what large means on them.
  measurements jsonb NOT NULL DEFAULT '{}'::jsonb,

  note         text,
  confirmed_on date NOT NULL DEFAULT current_date,
  recorded_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT customer_size_garment_not_blank CHECK (btrim(garment) <> ''),
  CONSTRAINT customer_size_size_not_blank CHECK (btrim(size) <> ''),
  CONSTRAINT customer_size_measurements_is_an_object CHECK (
    jsonb_typeof(measurements) = 'object')
);

-- One size per garment per customer. Somebody who has gone up a size has a
-- corrected row, not two rows leaving staff to guess which is current.
CREATE UNIQUE INDEX customer_size_uq
  ON customer_size (customer_id, lower(garment));
CREATE INDEX customer_size_tenant_idx ON customer_size (tenant_id);

CREATE TRIGGER customer_size_touch BEFORE UPDATE ON customer_size
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE customer_size ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_size FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_size_isolation ON customer_size
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Where the cost of the scheme lands
-- ---------------------------------------------------------------------------
--
-- Points a customer has earned are money the shop will hand over later, so they
-- are a liability the moment they are earned and an expense of the sale that
-- earned them. 2400 has existed since the first chart and had nothing to post
-- against it; this is the other half.

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, v.code, v.name,
       jsonb_build_object('ar', v.name_ar), v.kind
FROM company c
CROSS JOIN (VALUES
  ('5800', 'Loyalty Points Cost', 'تكلفة نقاط الولاء', 'expense')
) AS v(code, name, name_ar, kind)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, v.role, a.id
FROM account a
JOIN (VALUES
  ('5800', 'loyalty_expense')
) AS v(code, role) ON v.code = a.code
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The posting rules
-- ---------------------------------------------------------------------------
--
-- Selling a gift card is not a sale. No revenue, no VAT: the shop has taken
-- money and owes goods, and both happen when the card is spent. Booking it as
-- revenue on issue would overstate the month's takings, charge VAT twice, and
-- leave the redemption with nothing to settle against.

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES

('giftcard.issue', NULL, 1,
 '[{"for_each": "proceeds",            "side": "debit"},
   {"role": "store_credit_liability",  "side": "credit", "amount": "amount"}]'::jsonb,
 'A gift card sold. Money in, and goods owed — no revenue until it is spent.',
 '2020-01-01'),

-- Store credit given without money changing hands: a goodwill credit, or a
-- refund the customer took as credit rather than cash. The refund path posts
-- its own return; this rule is the credit itself.
('storecredit.issue', NULL, 1,
 '[{"role": "sales_discounts",        "side": "debit",  "amount": "amount"},
   {"role": "store_credit_liability", "side": "credit", "amount": "amount"}]'::jsonb,
 'Store credit given to a customer at the shop''s expense.',
 '2020-01-01'),

-- Credit that expired or was voided. The shop no longer owes it, so the
-- liability comes off and the write-back lands in Sales Discounts — the same
-- account the credit was given out of, so the two net to nothing over the life
-- of a credit nobody spent.
('storecredit.writeback', NULL, 1,
 '[{"role": "store_credit_liability", "side": "debit",  "amount": "amount"},
   {"role": "sales_discounts",        "side": "credit", "amount": "amount"}]'::jsonb,
 'Store credit that expired or was voided, written back.',
 '2020-01-01'),

('loyalty.accrue', NULL, 1,
 '[{"role": "loyalty_expense",   "side": "debit",  "amount": "amount"},
   {"role": "loyalty_liability", "side": "credit", "amount": "amount"}]'::jsonb,
 'Points earned on a sale: a cost of that sale, and money owed later.',
 '2020-01-01'),

('loyalty.expire', NULL, 1,
 '[{"role": "loyalty_liability", "side": "debit",  "amount": "amount"},
   {"role": "loyalty_expense",   "side": "credit", "amount": "amount"}]'::jsonb,
 'Points that expired unspent, written back.',
 '2020-01-01')

ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The permissions
-- ---------------------------------------------------------------------------
--
-- A cashier can look up a balance and take a card in payment; they cannot issue
-- one. Issuing store credit creates money out of nothing from the shop's point
-- of view, which is why it sits with the same people who can set a credit limit.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'loyalty.view'), ('owner',            'loyalty.manage'),
  ('owner',            'wallet.view'),  ('owner',            'wallet.manage'),
  ('store_manager',    'loyalty.view'), ('store_manager',    'loyalty.manage'),
  ('store_manager',    'wallet.view'),  ('store_manager',    'wallet.manage'),
  ('cashier',          'loyalty.view'),
  ('cashier',          'wallet.view'),
  ('sales_executive',  'loyalty.view'),
  ('sales_executive',  'wallet.view'),
  ('customer_service', 'loyalty.view'), ('customer_service', 'loyalty.manage'),
  ('customer_service', 'wallet.view'),  ('customer_service', 'wallet.manage'),
  ('online_manager',   'loyalty.view'),
  ('online_manager',   'wallet.view'),
  ('accountant',       'loyalty.view'), ('accountant',       'wallet.view'),
  ('auditor',          'loyalty.view'), ('auditor',          'wallet.view')
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
      ('owner',            'loyalty.view'), ('owner',            'loyalty.manage'),
      ('owner',            'wallet.view'),  ('owner',            'wallet.manage'),
      ('store_manager',    'loyalty.view'), ('store_manager',    'loyalty.manage'),
      ('store_manager',    'wallet.view'),  ('store_manager',    'wallet.manage'),
      ('cashier',          'loyalty.view'),
      ('cashier',          'wallet.view'),
      ('sales_executive',  'loyalty.view'),
      ('sales_executive',  'wallet.view'),
      ('customer_service', 'loyalty.view'), ('customer_service', 'loyalty.manage'),
      ('customer_service', 'wallet.view'),  ('customer_service', 'wallet.manage'),
      ('online_manager',   'loyalty.view'),
      ('online_manager',   'wallet.view'),
      ('accountant',       'loyalty.view'), ('accountant',       'wallet.view'),
      ('auditor',          'loyalty.view'), ('auditor',          'wallet.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
