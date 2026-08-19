-- Customers, and what they owe.
--
-- The mirror of purchasing, and deliberately shaped the same way: a party
-- record, a ledger of what moves, receipts that settle documents, ageing from
-- the DUE date, and a control-account tie-out. A buyer and a seller reading the
-- two modules should recognise the second from the first.
--
-- # What the documents already decided
--
-- 11-pos-and-sales.md §5: "customer_due posts to AR and is refused when it
-- would breach the customer's credit limit (B16)." Refused, not warned — so the
-- till blocks, and this migration gives the check something to read.
--
-- 02-posting-engine.md §6.6, quoting C9.3, names the invariant this module has
-- to hold up:
--
--     SUM(customer open balances) == Accounts Receivable control balance
--
-- That is why the receivable is derived from invoices and receipts rather than
-- stored as a running total on the customer. A cached balance is a second
-- source of truth, and the first thing it does is disagree.
--
-- Rule 8 (`payment.customer`) has been seeded since 0025 and had no caller.
-- This is its caller.
--
-- # Retail and wholesale are the same table
--
-- B12 keeps wholesale reporting separate from retail so retail figures are not
-- distorted, which is a reporting concern rather than a storage one. One table
-- with a type column, and every report that needs the split filters on it —
-- two tables would mean two of every query and a customer who graduated from
-- one to the other would lose their history.

CREATE TABLE customer (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,
  name        text NOT NULL,
  name_ar     text,

  -- retail | wholesale | vip, per B16. Drives which price list applies later
  -- and keeps wholesale out of retail reporting (B12).
  customer_type text NOT NULL DEFAULT 'retail',

  phone       text,
  email       text,
  -- A tax number, for a customer who needs a full tax invoice rather than a
  -- simplified one. Not validated here: the regulatory registry owns format
  -- rules per country and this table must not hold a second opinion.
  vat_number  text,
  address     text,

  -- Net days. Zero means cash only — a walk-in has no terms, and defaulting to
  -- anything else would let a till put a stranger on account.
  payment_terms_days integer NOT NULL DEFAULT 0,

  -- The ceiling B16 requires enforced. NULL means no account: a customer with
  -- no limit cannot buy on credit at all, which is the safe default for a
  -- record somebody created in a hurry at the counter.
  credit_limit numeric(18,4),

  notes       text,
  is_active   boolean NOT NULL DEFAULT true,

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT customer_type_valid CHECK (
    customer_type IN ('retail', 'wholesale', 'vip')),
  CONSTRAINT customer_terms_sane CHECK (payment_terms_days BETWEEN 0 AND 365),
  CONSTRAINT customer_credit_limit_positive CHECK (
    credit_limit IS NULL OR credit_limit >= 0)
);

CREATE UNIQUE INDEX customer_code_uq ON customer (company_id, lower(code));
CREATE INDEX customer_tenant_idx ON customer (tenant_id);
CREATE INDEX customer_active_idx ON customer (company_id) WHERE is_active;
-- A till looks a customer up by phone more than by anything else.
CREATE INDEX customer_phone_idx ON customer (company_id, phone)
  WHERE phone IS NOT NULL;

ALTER TABLE customer ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_isolation ON customer
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Attaching a sale to a customer
-- ---------------------------------------------------------------------------

-- Nullable, and that is the common case. A shop sells to whoever walks in, and
-- demanding a customer record for a cash sale would slow the counter down for
-- no benefit. It becomes REQUIRED only for a sale on account, which the sale
-- service enforces: money owed with nobody to owe it is a receivable that can
-- never be collected.
ALTER TABLE sales_invoice
  ADD COLUMN customer_id uuid REFERENCES customer(id) ON DELETE RESTRICT;

CREATE INDEX sales_invoice_customer_idx
  ON sales_invoice (customer_id, issue_date DESC)
  WHERE customer_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Receipts
-- ---------------------------------------------------------------------------

CREATE TABLE customer_receipt (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  customer_id uuid NOT NULL REFERENCES customer(id) ON DELETE RESTRICT,

  receipt_number text NOT NULL,

  -- Assigned by the client BEFORE the call, like every other money-moving
  -- document here. A network failure after the server committed would otherwise
  -- have a cashier take the same payment twice.
  uuid        uuid NOT NULL,

  received_on date NOT NULL DEFAULT current_date,
  method      text NOT NULL,
  reference   text,

  amount      numeric(18,4) NOT NULL,
  currency    text NOT NULL,

  journal_entry_id uuid REFERENCES journal_entry(id),

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),

  CONSTRAINT customer_receipt_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX customer_receipt_number_uq
  ON customer_receipt (company_id, receipt_number);
CREATE UNIQUE INDEX customer_receipt_uuid_uq
  ON customer_receipt (tenant_id, uuid);
CREATE INDEX customer_receipt_customer_idx
  ON customer_receipt (customer_id, received_on DESC);
CREATE INDEX customer_receipt_tenant_idx ON customer_receipt (tenant_id);

ALTER TABLE customer_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_receipt FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_receipt_isolation ON customer_receipt
  USING (tenant_id = current_tenant_id());

-- Which invoices a receipt settled. Its own table for the same reason the
-- supplier side has one: netting it into either end loses which invoice a part
-- payment was against, and that is the first thing a customer disputes.
CREATE TABLE customer_receipt_allocation (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  receipt_id  uuid NOT NULL REFERENCES customer_receipt(id) ON DELETE CASCADE,
  invoice_id  uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  amount      numeric(18,4) NOT NULL,

  CONSTRAINT customer_allocation_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX customer_allocation_uq
  ON customer_receipt_allocation (receipt_id, invoice_id);
CREATE INDEX customer_allocation_invoice_idx
  ON customer_receipt_allocation (invoice_id);
CREATE INDEX customer_allocation_tenant_idx
  ON customer_receipt_allocation (tenant_id);

ALTER TABLE customer_receipt_allocation ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_receipt_allocation FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_receipt_allocation_isolation
  ON customer_receipt_allocation USING (tenant_id = current_tenant_id());

ALTER TABLE company ADD COLUMN next_receipt_no bigint NOT NULL DEFAULT 1;

-- ---------------------------------------------------------------------------
-- What a customer owes
-- ---------------------------------------------------------------------------

-- Per invoice: what was sold on account, what has been credited back, and what
-- has been received against it.
--
-- Only the ON-ACCOUNT part of an invoice counts. A sale settled half in cash
-- and half on account owes only the second half, so the receivable is the sum
-- of the customer_due TENDERS rather than the invoice total — otherwise every
-- split-payment sale would show the whole amount as outstanding and the tie-out
-- would fail on the first one.
CREATE OR REPLACE FUNCTION customer_open_invoices(p_company_id uuid)
RETURNS TABLE (
  invoice_id   uuid,
  customer_id  uuid,
  human_number text,
  issue_date   date,
  due_date     date,
  on_account   numeric,
  received     numeric,
  outstanding  numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    i.id, i.customer_id, coalesce(i.human_number, ''),
    i.issue_date::date,
    (i.issue_date + make_interval(days => coalesce(c.payment_terms_days, 0)))::date,
    d.on_account,
    coalesce(r.received, 0),
    d.on_account - coalesce(r.received, 0)
  FROM sales_invoice i
  JOIN customer c ON c.id = i.customer_id
  CROSS JOIN LATERAL (
    SELECT coalesce(sum(t.amount), 0) AS on_account
    FROM sales_tender t
    WHERE t.invoice_id = i.id AND t.method = 'customer_due'
  ) d
  LEFT JOIN (
    SELECT a.invoice_id, sum(a.amount) AS received
    FROM customer_receipt_allocation a
    GROUP BY a.invoice_id
  ) r ON r.invoice_id = i.id
  WHERE i.company_id = p_company_id
    AND i.customer_id IS NOT NULL
    AND i.doc_type <> 'credit_note'
    AND d.on_account > 0
$$;

-- The invariant of C9.3, as a number. Zero, or the exception to raise.
--
-- Same shape as inventory_gl_difference so the nightly job, the acceptance test
-- and a support engineer all ask one question and get one answer.
CREATE OR REPLACE FUNCTION receivable_gl_difference(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce((
    SELECT sum(o.outstanding) FROM customer_open_invoices(p_company_id) o
  ), 0) - coalesce((
    SELECT sum(l.base_debit - l.base_credit)
    FROM journal_line l
    JOIN account a ON a.id = l.account_id
    WHERE a.company_id = p_company_id
      AND a.is_control AND a.control_of = 'receivable'
  ), 0)
$$;

-- What one customer owes right now, for the credit-limit check.
CREATE OR REPLACE FUNCTION customer_balance(p_customer_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(o.outstanding), 0)
  FROM customer c
  CROSS JOIN LATERAL customer_open_invoices(c.company_id) o
  WHERE c.id = p_customer_id AND o.customer_id = p_customer_id
$$;

-- Ageing, measured from the DUE date exactly as the supplier side is.
--
-- A 30-day-terms invoice raised 20 days ago is not overdue, and ageing it from
-- issue would put it in a chasing queue it does not belong in.
CREATE OR REPLACE FUNCTION customer_ageing(p_company_id uuid, p_as_of date)
RETURNS TABLE (
  customer_id   uuid,
  customer_name text,
  not_due       numeric,
  days_0_30     numeric,
  days_31_60    numeric,
  days_61_90    numeric,
  days_90_plus  numeric,
  total         numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    c.id, c.name,
    coalesce(sum(o.outstanding) FILTER (WHERE o.due_date >= p_as_of), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE o.due_date < p_as_of AND p_as_of - o.due_date <= 30), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - o.due_date BETWEEN 31 AND 60), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - o.due_date BETWEEN 61 AND 90), 0),
    coalesce(sum(o.outstanding) FILTER (WHERE p_as_of - o.due_date > 90), 0),
    coalesce(sum(o.outstanding), 0)
  FROM customer c
  JOIN customer_open_invoices(p_company_id) o ON o.customer_id = c.id
  WHERE c.company_id = p_company_id AND o.outstanding > 0
  GROUP BY c.id, c.name
  HAVING sum(o.outstanding) <> 0
  ORDER BY sum(o.outstanding) DESC
$$;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Seeded as the module ships, per 0005. sales.receive_payment already existed
-- and had no caller; this is it. Managing a customer record — and therefore
-- their credit limit — is a separate grant, because setting the ceiling on what
-- somebody may owe is a different act from taking their money.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner', 'customers.view'), ('owner', 'customers.manage'),
  ('owner', 'customers.set_credit_limit'),

  -- A store manager runs the shop floor: they see who owes what and take
  -- payment, and they may correct a customer's details. Setting the credit
  -- ceiling stays with the owner and the accountant.
  ('store_manager', 'customers.view'), ('store_manager', 'customers.manage'),

  -- A cashier needs to find a customer to put a sale on account and to take a
  -- payment against one. They cannot change the record.
  ('cashier', 'customers.view'),

  -- The accountant collects. They see the ledger, take receipts, and own the
  -- credit limit — which is a credit decision, not a counter decision.
  ('accountant', 'customers.view'), ('accountant', 'customers.manage'),
  ('accountant', 'customers.set_credit_limit'),

  ('auditor', 'customers.view'),
  ('customer_service', 'customers.view'), ('customer_service', 'customers.manage')
) AS p(role_key, permission) ON p.role_key = r.key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;
