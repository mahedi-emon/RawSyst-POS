-- 0098 — Multi-company groups and consolidated reporting (blueprint F4).
--
-- # What a group is, and what it is not
--
-- F4: "One Owner login can hold multiple companies under a group. Consolidated
-- reporting across companies (group P&L, group balance sheet), while each
-- company keeps its own separate books, VAT registration, and ZATCA sequence."
--
-- The second half is the constraint that shapes this migration. A group is a
-- REPORTING construct and nothing else. It owns no ledger, no accounts, no
-- invoice sequence and no VAT registration. Every posting still belongs to one
-- company, and consolidation is a query that adds companies up — never a second
-- set of books that could drift from the first.
--
-- That is not a simplification. Each company in a Saudi group is a separate
-- legal entity with its own commercial registration, its own VAT number and its
-- own e-invoicing sequence, and a design that let a journal entry belong to "the
-- group" would produce an entry no company could file.
--
-- # Membership carries the ownership percentage
--
-- Consolidation of a partly-owned subsidiary is not the same arithmetic as
-- consolidation of a wholly-owned one, and a group that holds 60% of a company
-- and reports 100% of its profit is publishing a false figure. The percentage is
-- recorded here; what the reporting code does with it is stated in the service,
-- and it deliberately does not invent a minority-interest calculation nobody
-- asked for — it reports the group's share and says what share it is.
--
-- # Inter-company transactions are marked, not guessed
--
-- F4 asks for them to be "tracked and eliminated in consolidation". Guessing
-- which entries are inter-company — matching amounts, matching dates, a customer
-- whose name resembles a sister company — is how a consolidation quietly removes
-- a real third-party sale. So an entry is inter-company only when somebody says
-- it is, by naming the counterparty company, and elimination removes exactly
-- those.

-- ---------------------------------------------------------------------------
-- The group
-- ---------------------------------------------------------------------------

CREATE TABLE company_group (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  name        text NOT NULL,
  name_ar     text,

  -- The currency the consolidated statements are presented in. Not derived
  -- from the parent: a group of Saudi and Bangladeshi entities reporting in
  -- riyals is a decision somebody makes, and the alternative is a statement
  -- whose currency changes when the parent does.
  presentation_currency char(3) NOT NULL,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT company_group_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT company_group_currency_upper
    CHECK (presentation_currency = upper(presentation_currency))
);

CREATE UNIQUE INDEX company_group_name_uq
  ON company_group (tenant_id, lower(name));
CREATE INDEX company_group_tenant_idx ON company_group (tenant_id);

CREATE TRIGGER company_group_touch BEFORE UPDATE ON company_group
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE company_group ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_group FORCE  ROW LEVEL SECURITY;
CREATE POLICY company_group_isolation ON company_group
  USING (tenant_id = current_tenant_id());

CREATE TABLE company_group_member (
  group_id    uuid NOT NULL REFERENCES company_group(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- The group's share. 100 for a wholly-owned subsidiary, which is the
  -- ordinary case and the default.
  ownership_pct numeric(7,4) NOT NULL DEFAULT 100,

  -- Exactly one company in a group is the parent. Not enforced as "must
  -- exist" — a group being assembled has no parent yet and refusing to save it
  -- would be refusing a half-finished form — but enforced as "at most one",
  -- because two parents is a structure nobody can consolidate.
  is_parent   boolean NOT NULL DEFAULT false,

  joined_on   date NOT NULL DEFAULT current_date,
  left_on     date,

  PRIMARY KEY (group_id, company_id),
  CONSTRAINT group_member_pct_sane
    CHECK (ownership_pct > 0 AND ownership_pct <= 100),
  CONSTRAINT group_member_dates_ordered
    CHECK (left_on IS NULL OR left_on >= joined_on)
);

CREATE UNIQUE INDEX company_group_one_parent
  ON company_group_member (group_id) WHERE is_parent;
-- A company belongs to at most one group. A company in two groups would be
-- consolidated twice, and there is no honest answer to which total is right.
CREATE UNIQUE INDEX company_group_member_company_uq
  ON company_group_member (company_id);
CREATE INDEX company_group_member_tenant_idx
  ON company_group_member (tenant_id);

ALTER TABLE company_group_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_group_member FORCE  ROW LEVEL SECURITY;
CREATE POLICY company_group_member_isolation ON company_group_member
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Inter-company marking
-- ---------------------------------------------------------------------------

-- Which journal entries are between two companies of the same group.
--
-- A side table rather than a column on `journal_entry`, because a posted entry
-- is immutable: the reject_always trigger on that table refuses an UPDATE, and
-- it refuses it for the right reason. Marking an entry as inter-company after
-- the fact is a REPORTING annotation, not a change to what was posted, and this
-- is where such an annotation belongs.
CREATE TABLE intercompany_entry (
  entry_id      uuid PRIMARY KEY REFERENCES journal_entry(id) ON DELETE CASCADE,
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id    uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- The other company. Constrained to be a different one: an entry
  -- inter-company with itself is a data-entry slip that would eliminate a real
  -- transaction.
  counterparty_id uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- What it was: a sale between group companies, a transfer of stock, a loan,
  -- a management charge, an expense recharge.
  kind          text NOT NULL DEFAULT 'sale',
  note          text,

  marked_at     timestamptz NOT NULL DEFAULT now(),
  marked_by     uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT intercompany_not_self CHECK (company_id <> counterparty_id),
  CONSTRAINT intercompany_kind_valid CHECK (kind IN (
    'sale', 'purchase', 'stock_transfer', 'loan', 'management_charge',
    'expense_recharge', 'dividend'))
);

CREATE INDEX intercompany_company_idx
  ON intercompany_entry (company_id, counterparty_id);
CREATE INDEX intercompany_tenant_idx ON intercompany_entry (tenant_id);

ALTER TABLE intercompany_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE intercompany_entry FORCE  ROW LEVEL SECURITY;
CREATE POLICY intercompany_entry_isolation ON intercompany_entry
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Reading a consolidated statement is reading every company's books at once,
-- so it is narrower than `report.view`: a store manager holding report.view for
-- their own shop must not get the group's profit through it.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',      'group.view'),   ('owner',      'group.manage'),
  ('accountant', 'group.view'),
  ('auditor',    'group.view')
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
      ('owner',      'group.view'),   ('owner',      'group.manage'),
      ('accountant', 'group.view'),
      ('auditor',    'group.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
