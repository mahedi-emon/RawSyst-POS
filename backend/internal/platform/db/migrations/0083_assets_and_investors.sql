-- 0083 — Fixed assets (C7) and investors (C3.2).
--
-- Two modules in one migration because they are the same shape: a register of
-- things that are not stock and not expenses, each with its own posting rules,
-- and both of which the chart of accounts has been carrying accounts for since
-- provisioning was written.
--
-- # What makes an asset different from an expense
--
-- A shop that buys a delivery van has not spent the money in the sense a shop
-- that buys electricity has. It still has the van. So the cost sits on the
-- balance sheet and is charged to the P&L a little at a time over the years the
-- van is useful — which is depreciation, and it is the only reason this module
-- exists rather than "record it as an expense and move on".
--
-- Getting that wrong in either direction is a real error: capitalising a repair
-- flatters this year's profit, and expensing a van understates the assets a
-- bank is lending against.
--
-- # What makes an investor different from a customer
--
-- C3.2: investment activity is "kept fully separate from normal revenue in the
-- accounting model — never mixed with sales income, so P&L stays clean." Money
-- an owner puts in is not something the shop earned. It is equity, it never
-- touches the profit and loss, and a product that let it be recorded as income
-- would flatter every figure a business is judged by.

-- ---------------------------------------------------------------------------
-- The asset register
-- ---------------------------------------------------------------------------

CREATE TABLE fixed_asset (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  asset_no    text NOT NULL,
  name        text NOT NULL,
  name_ar     text,
  category    text NOT NULL,

  -- Where it is and who is answerable for it. C7 asks for both by name, and
  -- they are the two facts that make a register useful for anything other than
  -- accounting: an asset nobody is responsible for is an asset nobody notices
  -- has gone.
  store_id    uuid REFERENCES store(id)    ON DELETE SET NULL,
  custodian_id uuid REFERENCES app_user(id) ON DELETE SET NULL,
  serial_number text,
  warranty_until date,

  acquired_on date NOT NULL,
  cost        numeric(18,4) NOT NULL,

  -- What it is expected to be worth when the shop is finished with it. Not
  -- depreciated: straight-line spreads cost LESS residual over the life, and a
  -- van written down to nothing that then sells for eight thousand produces a
  -- gain that was really bad estimation.
  residual_value numeric(18,4) NOT NULL DEFAULT 0,

  -- In months. C7 asks for straight-line only, so one number is the whole
  -- schedule and there is no depreciation-method column to get wrong.
  useful_life_months integer NOT NULL,

  -- The month depreciation was last charged for, as its first day. NULL until
  -- the first charge. The register is driven from this rather than from a count
  -- of postings, because a company that starts using the product mid-life needs
  -- to be able to say "begin from here".
  depreciated_to date,

  status      text NOT NULL DEFAULT 'in_use',
  disposed_on date,
  disposal_proceeds numeric(18,4),
  disposal_note text,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT fixed_asset_cost_positive CHECK (cost > 0),
  CONSTRAINT fixed_asset_residual_sane CHECK (
    residual_value >= 0 AND residual_value < cost),
  CONSTRAINT fixed_asset_life_positive CHECK (useful_life_months > 0),
  CONSTRAINT fixed_asset_status_valid CHECK (status IN ('in_use', 'disposed')),
  CONSTRAINT fixed_asset_disposal_is_complete CHECK (
    status <> 'disposed'
    OR (disposed_on IS NOT NULL AND disposal_proceeds IS NOT NULL)),
  CONSTRAINT fixed_asset_disposal_proceeds_sane CHECK (
    disposal_proceeds IS NULL OR disposal_proceeds >= 0)
);

CREATE UNIQUE INDEX fixed_asset_no_uq ON fixed_asset (company_id, asset_no);
CREATE INDEX fixed_asset_tenant_idx ON fixed_asset (tenant_id);
-- The index the monthly depreciation run reads: everything still in use whose
-- charge has not reached the month being run.
CREATE INDEX fixed_asset_due_idx ON fixed_asset (company_id, depreciated_to)
  WHERE status = 'in_use';

CREATE TRIGGER fixed_asset_touch BEFORE UPDATE ON fixed_asset
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE fixed_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE fixed_asset FORCE  ROW LEVEL SECURITY;
CREATE POLICY fixed_asset_isolation ON fixed_asset
  USING (tenant_id = current_tenant_id());

-- One row per asset per month it was charged for.
--
-- A ledger rather than a running total on the asset, for the reason the stock
-- module keeps movements rather than levels: a total loses the information
-- about WHEN, and "why is this van worth that" is a question about a schedule.
CREATE TABLE asset_depreciation (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  asset_id    uuid NOT NULL REFERENCES fixed_asset(id) ON DELETE RESTRICT,

  -- The first day of the month being charged for.
  charged_for date NOT NULL,
  amount      numeric(18,4) NOT NULL,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT asset_depreciation_positive CHECK (amount > 0)
);

-- One charge per asset per month, enforced rather than checked. A depreciation
-- run that ran twice would halve the asset's remaining life without anything
-- looking wrong, and it is the kind of error found when the asset reaches zero
-- a year early.
CREATE UNIQUE INDEX asset_depreciation_once_per_month
  ON asset_depreciation (asset_id, charged_for);
CREATE INDEX asset_depreciation_tenant_idx ON asset_depreciation (tenant_id);

ALTER TABLE asset_depreciation ENABLE ROW LEVEL SECURITY;
ALTER TABLE asset_depreciation FORCE  ROW LEVEL SECURITY;
CREATE POLICY asset_depreciation_isolation ON asset_depreciation
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER asset_depreciation_no_change
  BEFORE UPDATE OR DELETE ON asset_depreciation
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE company ADD COLUMN next_asset_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_asset_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company SET next_asset_no = next_asset_no + 1
  WHERE id = p_company_id
  RETURNING next_asset_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'FA-' || to_char(claimed, 'FM000000');
END;
$$;

-- ---------------------------------------------------------------------------
-- Investors
-- ---------------------------------------------------------------------------

CREATE TABLE investor (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,
  name_ar     text,
  -- `owner` for the proprietor, `investor` for anybody else. The distinction
  -- is not decoration: C3.2 asks for each investor's proportional share, and an
  -- owner's stake and an outside investor's are read differently by anybody
  -- looking at the business.
  kind        text NOT NULL DEFAULT 'investor',

  -- Where their money reaches them, and how to reach them. Optional, because a
  -- sole trader investing their own savings is one of these rows and has no
  -- paperwork to record.
  email       citext,
  phone       text,
  note        text,

  -- The person's own login, where they have one. C3.2: "each investor can (if
  -- given access) see only their own contribution/return history."
  user_id     uuid REFERENCES app_user(id) ON DELETE SET NULL,

  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT investor_kind_valid CHECK (kind IN ('owner', 'investor'))
);

CREATE INDEX investor_company_idx ON investor (company_id) WHERE is_active;
CREATE INDEX investor_tenant_idx ON investor (tenant_id);
CREATE INDEX investor_user_idx ON investor (user_id) WHERE user_id IS NOT NULL;

CREATE TRIGGER investor_touch BEFORE UPDATE ON investor
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE investor ENABLE ROW LEVEL SECURITY;
ALTER TABLE investor FORCE  ROW LEVEL SECURITY;
CREATE POLICY investor_isolation ON investor
  USING (tenant_id = current_tenant_id());

CREATE TABLE investment (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  investor_id uuid NOT NULL REFERENCES investor(id) ON DELETE RESTRICT,

  -- `contribution` is money in, `withdrawal` is money out. Two directions
  -- rather than a signed amount, for the reason the posting rules give: a
  -- negative debit where a credit belongs is unreadable in a ledger.
  direction   text NOT NULL,
  amount      numeric(18,4) NOT NULL,
  moved_on    date NOT NULL,

  -- Which of the company's money accounts it arrived in or left from.
  money_account_id uuid REFERENCES money_account(id) ON DELETE RESTRICT,

  reference   text,
  note        text,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT investment_positive CHECK (amount > 0),
  CONSTRAINT investment_direction_valid
    CHECK (direction IN ('contribution', 'withdrawal'))
);

CREATE INDEX investment_investor_idx ON investment (investor_id, moved_on DESC);
CREATE INDEX investment_company_idx ON investment (company_id, moved_on DESC);
CREATE INDEX investment_tenant_idx ON investment (tenant_id);

ALTER TABLE investment ENABLE ROW LEVEL SECURITY;
ALTER TABLE investment FORCE  ROW LEVEL SECURITY;
CREATE POLICY investment_isolation ON investment
  USING (tenant_id = current_tenant_id());

-- Money that changed hands does not change. Correcting one means the opposite
-- movement, which leaves both facts on the investor's statement.
CREATE TRIGGER investment_no_change
  BEFORE UPDATE OR DELETE ON investment
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- The accounts these post to
-- ---------------------------------------------------------------------------
--
-- The chart already has 3100 Owner Capital, which rule 12 uses. These are the
-- three it does not have, added to the seeded chart AND mapped for every
-- company that already exists — the 0048 lesson, which this migration would
-- otherwise repeat for the fourth time.

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, v.code, v.name,
       jsonb_build_object('ar', v.name_ar), v.kind
FROM company c
CROSS JOIN (VALUES
  ('1500', 'Fixed Assets', 'الأصول الثابتة', 'asset'),
  ('1590', 'Accumulated Depreciation', 'مجمع الإهلاك', 'asset'),
  ('5600', 'Depreciation', 'الإهلاك', 'expense'),
  ('5700', 'Loss on Disposal', 'خسارة استبعاد أصل', 'expense'),
  ('4900', 'Gain on Disposal', 'ربح استبعاد أصل', 'revenue')
) AS v(code, name, name_ar, kind)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, v.role, a.id
FROM account a
JOIN (VALUES
  ('1500', 'fixed_assets'),
  ('1590', 'accumulated_depreciation'),
  ('5600', 'depreciation'),
  ('5700', 'disposal_loss'),
  ('4900', 'disposal_gain')
) AS v(code, role) ON v.code = a.code
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The posting rules
-- ---------------------------------------------------------------------------
--
-- C7 asks for "automated monthly straight-line depreciation with journal
-- postings" and disposal "with automatic gain/loss-on-disposal calculation".
-- C3.2 asks for investment activity that never touches the P&L.
--
-- Rule 12, `equity.contribution`, has been seeded since 0025 and posts a
-- contribution already. A withdrawal is its mirror and did not exist.
--
-- Accumulated depreciation is credited rather than the asset being written
-- down. That is not a preference: a balance sheet that shows cost and
-- accumulated depreciation separately says what the shop paid AND how much life
-- is left in it, and netting them into one figure throws the first away.

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES

('asset.depreciation', NULL, 1,
 '[{"role": "depreciation",             "side": "debit",  "amount": "amount"},
   {"role": "accumulated_depreciation", "side": "credit", "amount": "amount"}]'::jsonb,
 'One month of straight-line depreciation on a fixed asset.',
 '2020-01-01'),

-- Disposal, in two directions. Both clear the asset's cost out of Fixed Assets
-- and its accumulated depreciation out of the contra account, take the proceeds
-- into a money account, and put the difference where it belongs.
('asset.disposal_loss', NULL, 1,
 '[{"for_each": "proceeds",             "side": "debit"},
   {"role": "accumulated_depreciation", "side": "debit",  "amount": "depreciated"},
   {"role": "disposal_loss",            "side": "debit",  "amount": "difference"},
   {"role": "fixed_assets",             "side": "credit", "amount": "cost"}]'::jsonb,
 'An asset sold or scrapped for less than it was worth in the books.',
 '2020-01-01'),

('asset.disposal_gain', NULL, 1,
 '[{"for_each": "proceeds",             "side": "debit"},
   {"role": "accumulated_depreciation", "side": "debit",  "amount": "depreciated"},
   {"role": "fixed_assets",             "side": "credit", "amount": "cost"},
   {"role": "disposal_gain",            "side": "credit", "amount": "difference"}]'::jsonb,
 'An asset sold for more than it was worth in the books.',
 '2020-01-01'),

-- Rule 12's mirror. Money an owner or investor takes back out.
('equity.withdrawal', NULL, 1,
 '[{"role": "owner_capital", "side": "debit",  "amount": "amount"},
   {"for_each": "source",    "side": "credit"}]'::jsonb,
 'Capital taken back out by an owner or investor.',
 '2020-01-01');

-- ---------------------------------------------------------------------------
-- The permissions
-- ---------------------------------------------------------------------------
--
-- Assets and investors are both accounting, and both are Owner-and-Accountant
-- work. Deliberately not the Store Manager: 0005 describes that role as unable
-- to see "bank ledgers or true net profit", and an investor register is a
-- statement of who owns the business.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',      'asset.view'),      ('owner',      'asset.manage'),
  ('accountant', 'asset.view'),      ('accountant', 'asset.manage'),
  ('auditor',    'asset.view'),
  ('owner',      'investor.view'),   ('owner',      'investor.manage'),
  ('accountant', 'investor.view'),   ('accountant', 'investor.manage'),
  ('auditor',    'investor.view')
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
      ('owner',      'asset.view'),      ('owner',      'asset.manage'),
      ('accountant', 'asset.view'),      ('accountant', 'asset.manage'),
      ('auditor',    'asset.view'),
      ('owner',      'investor.view'),   ('owner',      'investor.manage'),
      ('accountant', 'investor.view'),   ('accountant', 'investor.manage'),
      ('auditor',    'investor.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
