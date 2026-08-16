-- Purchasing: suppliers, orders, receipts, bills, the three-way match, and
-- payment.
--
-- Blueprint B5 gives the chain as
--   Requisition → Approval → RFQ → Quote → PO → GRN → Bill → 3-way match → Payment
-- and this migration builds the load-bearing half of it: supplier through
-- payment. Requisition, RFQ and quote comparison are an approval workflow that
-- FEEDS a purchase order and that nothing downstream depends on, so they are
-- deliberately absent rather than half-present.
--
-- # The rule that shapes everything here
--
-- B5: "Only GRN increases stock — a PO alone never inflates inventory." An
-- order is an intention. Stock and cost layers move when goods physically
-- arrive and not a moment sooner, which is why goods_receipt is the only table
-- in this file that the inventory engine ever hears about.
--
-- # Three documents, three quantities, deliberately separate
--
-- po_line.qty_ordered, grn_line.qty_received and bill_line.qty_billed are
-- stored independently and never derived from one another. That is the whole
-- point of a three-way match: a supplier who ships 90 and bills 100 is only
-- detectable if the two numbers were recorded separately. Collapsing them into
-- one "quantity" column would make the control impossible to implement, and
-- B5.2 calls that control "the single most effective control against supplier
-- overbilling and internal fraud".

-- ---------------------------------------------------------------------------
-- Suppliers
-- ---------------------------------------------------------------------------

CREATE TABLE supplier (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,
  legal_name  text NOT NULL,
  name_ar     text,
  contact_name  text,
  email       text,
  phone       text,

  -- A supplier's VAT number appears on every bill they send and is what makes
  -- input tax recoverable. Not validated against a registry here: E8 owns the
  -- format rules per country and this table must not encode a second opinion.
  vat_number  text,
  cr_number   text,
  country     text,

  -- Net days. Drives the due date on a bill and the ageing buckets, and lives
  -- on the supplier because it is negotiated with them rather than per order.
  payment_terms_days integer NOT NULL DEFAULT 0,

  -- A ceiling on what may be owed to this supplier at once. Nullable because
  -- most shops do not set one; where set, it is checked when a bill is raised.
  credit_limit numeric(18,4),

  notes       text,
  is_active   boolean NOT NULL DEFAULT true,

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT supplier_terms_sane CHECK (payment_terms_days BETWEEN 0 AND 365),
  CONSTRAINT supplier_credit_limit_positive CHECK (
    credit_limit IS NULL OR credit_limit >= 0)
);

CREATE UNIQUE INDEX supplier_code_uq ON supplier (company_id, lower(code));
CREATE INDEX supplier_tenant_idx ON supplier (tenant_id);
CREATE INDEX supplier_active_idx ON supplier (company_id) WHERE is_active;

ALTER TABLE supplier ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_isolation ON supplier
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Purchase orders
-- ---------------------------------------------------------------------------

CREATE TABLE purchase_order (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES supplier(id) ON DELETE RESTRICT,

  -- Where the goods are expected. Fixed at order time so a receipt cannot
  -- quietly land stock in a different branch's warehouse than was authorised.
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  po_number   text NOT NULL,

  -- draft     → editable, no commitment
  -- issued    → sent to the supplier; lines are frozen
  -- receiving → at least one GRN, more expected
  -- received  → every line fully received
  -- closed    → deliberately finished, short deliveries accepted
  -- cancelled → abandoned before any receipt
  status      text NOT NULL DEFAULT 'draft',

  ordered_on  date NOT NULL DEFAULT current_date,
  expected_on date,

  currency    text NOT NULL,
  -- The rate at order time, kept so a later bill in the same currency can be
  -- compared against what was authorised rather than against today's rate.
  fx_rate     numeric(18,8) NOT NULL DEFAULT 1,

  subtotal_net   numeric(18,4) NOT NULL DEFAULT 0,
  tax_total      numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive numeric(18,4) NOT NULL DEFAULT 0,

  notes       text,

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),
  issued_at   timestamptz,
  issued_by   uuid REFERENCES app_user(id),

  CONSTRAINT po_status_valid CHECK (status IN (
    'draft', 'issued', 'receiving', 'received', 'closed', 'cancelled')),
  CONSTRAINT po_fx_positive CHECK (fx_rate > 0),
  CONSTRAINT po_totals_sane CHECK (
    subtotal_net >= 0 AND tax_total >= 0 AND total_inclusive >= 0)
);

CREATE UNIQUE INDEX po_number_uq ON purchase_order (company_id, po_number);
CREATE INDEX po_tenant_idx   ON purchase_order (tenant_id);
CREATE INDEX po_supplier_idx ON purchase_order (supplier_id, ordered_on DESC);
-- The queue a buyer works from: what is outstanding, oldest first.
CREATE INDEX po_open_idx ON purchase_order (company_id, ordered_on DESC)
  WHERE status IN ('issued', 'receiving');

ALTER TABLE purchase_order ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_order FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_order_isolation ON purchase_order
  USING (tenant_id = current_tenant_id());

CREATE TABLE po_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  po_id       uuid NOT NULL REFERENCES purchase_order(id) ON DELETE CASCADE,
  line_no     integer NOT NULL,

  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  description text NOT NULL,

  qty_ordered numeric(18,4) NOT NULL,
  unit_cost   numeric(18,4) NOT NULL,

  -- The tax treatment AGREED with the supplier, which is not necessarily how
  -- the item is sold. Imported goods are frequently zero-rated inbound and
  -- standard-rated on sale.
  tax_treatment text NOT NULL DEFAULT 'standard',
  tax_rate    numeric(9,6) NOT NULL DEFAULT 0,

  net_amount  numeric(18,4) NOT NULL,
  tax_amount  numeric(18,4) NOT NULL,
  gross_amount numeric(18,4) NOT NULL,

  CONSTRAINT po_line_qty_positive  CHECK (qty_ordered > 0),
  CONSTRAINT po_line_cost_positive CHECK (unit_cost >= 0),
  CONSTRAINT po_line_amounts_sane  CHECK (
    net_amount >= 0 AND tax_amount >= 0 AND gross_amount >= 0)
);

CREATE UNIQUE INDEX po_line_no_uq ON po_line (po_id, line_no);
CREATE INDEX po_line_tenant_idx  ON po_line (tenant_id);
CREATE INDEX po_line_variant_idx ON po_line (variant_id);

ALTER TABLE po_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE po_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY po_line_isolation ON po_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Goods receipts — the ONLY thing that increases stock
-- ---------------------------------------------------------------------------

CREATE TABLE goods_receipt (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  po_id       uuid NOT NULL REFERENCES purchase_order(id) ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  grn_number  text NOT NULL,

  -- Assigned by the receiving device or client BEFORE the request, so a retry
  -- after a network failure is recognised rather than receiving the same
  -- delivery twice. Same mechanism as an invoice UUID.
  uuid        uuid NOT NULL,

  received_on date NOT NULL DEFAULT current_date,
  -- The supplier's own delivery note, so a physical document on the loading
  -- bay can be matched to this record during a dispute.
  delivery_note_ref text,

  notes       text,

  created_at  timestamptz NOT NULL DEFAULT now(),
  received_by uuid REFERENCES app_user(id)
);

CREATE UNIQUE INDEX grn_number_uq ON goods_receipt (company_id, grn_number);
CREATE UNIQUE INDEX grn_uuid_uq   ON goods_receipt (tenant_id, uuid);
CREATE INDEX grn_po_idx     ON goods_receipt (po_id);
CREATE INDEX grn_tenant_idx ON goods_receipt (tenant_id);

ALTER TABLE goods_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE goods_receipt FORCE  ROW LEVEL SECURITY;
CREATE POLICY goods_receipt_isolation ON goods_receipt
  USING (tenant_id = current_tenant_id());

CREATE TABLE grn_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  grn_id      uuid NOT NULL REFERENCES goods_receipt(id) ON DELETE CASCADE,
  po_line_id  uuid NOT NULL REFERENCES po_line(id) ON DELETE RESTRICT,
  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,

  qty_received numeric(18,4) NOT NULL,
  -- Damaged or wrong goods sent straight back. Recorded rather than netted off
  -- qty_received: what arrived and what was kept are different facts, and a
  -- supplier arguing about a short payment will ask about both.
  qty_rejected numeric(18,4) NOT NULL DEFAULT 0,
  reject_reason text,

  -- What the stock was actually valued at, INCLUDING any landed cost allocated
  -- to it. Copied from the PO line at receipt time rather than read through a
  -- join, because a PO that is later amended must not retrospectively change
  -- what a cost layer was worth.
  unit_cost   numeric(18,4) NOT NULL,

  CONSTRAINT grn_line_qty_positive CHECK (qty_received > 0),
  CONSTRAINT grn_line_rejected_sane CHECK (
    qty_rejected >= 0 AND qty_rejected <= qty_received),
  CONSTRAINT grn_line_cost_positive CHECK (unit_cost >= 0)
);

CREATE INDEX grn_line_grn_idx     ON grn_line (grn_id);
CREATE INDEX grn_line_po_line_idx ON grn_line (po_line_id);
CREATE INDEX grn_line_tenant_idx  ON grn_line (tenant_id);

ALTER TABLE grn_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE grn_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY grn_line_isolation ON grn_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Bills
-- ---------------------------------------------------------------------------

CREATE TABLE purchase_bill (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES supplier(id) ON DELETE RESTRICT,
  po_id       uuid REFERENCES purchase_order(id) ON DELETE RESTRICT,

  -- The supplier's own invoice number. Unique per supplier so the same
  -- document cannot be entered twice, which is the commonest way a shop pays
  -- one invoice two times.
  supplier_ref text NOT NULL,

  uuid        uuid NOT NULL,

  bill_date   date NOT NULL,
  due_date    date NOT NULL,

  currency    text NOT NULL,
  fx_rate     numeric(18,8) NOT NULL DEFAULT 1,

  subtotal_net    numeric(18,4) NOT NULL DEFAULT 0,
  tax_total       numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive numeric(18,4) NOT NULL DEFAULT 0,
  amount_paid     numeric(18,4) NOT NULL DEFAULT 0,

  -- draft    → entered, not yet posted
  -- matched  → three-way match passed, posted to the ledger
  -- blocked  → match failed beyond tolerance, payment refused
  -- approved → match failed but an approver accepted it, posted
  -- paid     → settled in full
  -- cancelled
  status      text NOT NULL DEFAULT 'draft',

  -- Set when a blocked bill is let through. Who allowed it is not optional:
  -- B5.2's control is worthless if an override leaves no name behind.
  approved_by uuid REFERENCES app_user(id),
  approved_at timestamptz,
  approval_reason text,

  journal_entry_id uuid REFERENCES journal_entry(id),

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),

  CONSTRAINT bill_status_valid CHECK (status IN (
    'draft', 'matched', 'blocked', 'approved', 'paid', 'cancelled')),
  CONSTRAINT bill_fx_positive CHECK (fx_rate > 0),
  CONSTRAINT bill_due_after_date CHECK (due_date >= bill_date),
  CONSTRAINT bill_totals_sane CHECK (
    subtotal_net >= 0 AND tax_total >= 0 AND total_inclusive >= 0
    AND amount_paid >= 0),
  -- An override must carry a reason and a name together, or neither.
  CONSTRAINT bill_approval_complete CHECK (
    (approved_by IS NULL AND approved_at IS NULL AND approval_reason IS NULL)
    OR (approved_by IS NOT NULL AND approved_at IS NOT NULL
        AND approval_reason IS NOT NULL))
);

CREATE UNIQUE INDEX bill_supplier_ref_uq
  ON purchase_bill (supplier_id, lower(supplier_ref));
CREATE UNIQUE INDEX bill_uuid_uq ON purchase_bill (tenant_id, uuid);
CREATE INDEX bill_tenant_idx ON purchase_bill (tenant_id);
CREATE INDEX bill_po_idx     ON purchase_bill (po_id);
-- The ageing report reads this: what is owed, by when.
CREATE INDEX bill_outstanding_idx
  ON purchase_bill (company_id, due_date)
  WHERE status IN ('matched', 'blocked', 'approved');

ALTER TABLE purchase_bill ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_bill FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_bill_isolation ON purchase_bill
  USING (tenant_id = current_tenant_id());

CREATE TABLE bill_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  bill_id     uuid NOT NULL REFERENCES purchase_bill(id) ON DELETE CASCADE,
  po_line_id  uuid REFERENCES po_line(id) ON DELETE RESTRICT,
  variant_id  uuid REFERENCES variant(id) ON DELETE RESTRICT,

  line_no     integer NOT NULL,
  description text NOT NULL,

  qty_billed  numeric(18,4) NOT NULL,
  unit_cost   numeric(18,4) NOT NULL,

  tax_treatment text NOT NULL DEFAULT 'standard',
  tax_rate    numeric(9,6) NOT NULL DEFAULT 0,

  net_amount  numeric(18,4) NOT NULL,
  tax_amount  numeric(18,4) NOT NULL,
  gross_amount numeric(18,4) NOT NULL,

  CONSTRAINT bill_line_qty_positive CHECK (qty_billed > 0),
  CONSTRAINT bill_line_cost_positive CHECK (unit_cost >= 0)
);

CREATE UNIQUE INDEX bill_line_no_uq ON bill_line (bill_id, line_no);
CREATE INDEX bill_line_tenant_idx  ON bill_line (tenant_id);
CREATE INDEX bill_line_po_line_idx ON bill_line (po_line_id);

ALTER TABLE bill_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE bill_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY bill_line_isolation ON bill_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The three-way match
-- ---------------------------------------------------------------------------

-- One row per bill line examined, kept as evidence rather than recomputed.
--
-- Stored because the match is a CONTROL, and a control that leaves no record
-- cannot be audited. Recomputing it later would also give a different answer
-- once someone amends a PO, which is exactly the circumstance in which
-- somebody would want to check what the match originally said.
CREATE TABLE three_way_match (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  bill_id     uuid NOT NULL REFERENCES purchase_bill(id) ON DELETE CASCADE,
  bill_line_id uuid REFERENCES bill_line(id) ON DELETE CASCADE,

  -- qty | price | tax | total — which of the four comparisons this row is.
  dimension   text NOT NULL,

  ordered     numeric(18,4),
  received    numeric(18,4),
  billed      numeric(18,4),
  variance    numeric(18,4) NOT NULL,
  variance_pct numeric(9,4),

  -- pass | within_tolerance | breach
  outcome     text NOT NULL,
  detail      text,

  checked_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT twm_dimension_valid CHECK (
    dimension IN ('qty', 'price', 'tax', 'total')),
  CONSTRAINT twm_outcome_valid CHECK (
    outcome IN ('pass', 'within_tolerance', 'breach'))
);

CREATE INDEX twm_bill_idx   ON three_way_match (bill_id);
CREATE INDEX twm_tenant_idx ON three_way_match (tenant_id);

ALTER TABLE three_way_match ENABLE ROW LEVEL SECURITY;
ALTER TABLE three_way_match FORCE  ROW LEVEL SECURITY;
CREATE POLICY three_way_match_isolation ON three_way_match
  USING (tenant_id = current_tenant_id());

-- The tolerance, per company.
--
-- B5.2 requires it configurable and gives "2% or SAR 50" as the example. BOTH
-- apply, whichever is larger: a percentage alone lets a large order absorb a
-- big absolute overcharge, and an absolute figure alone blocks every rounding
-- difference on a small one.
ALTER TABLE company
  ADD COLUMN match_tolerance_pct    numeric(9,4)  NOT NULL DEFAULT 2,
  ADD COLUMN match_tolerance_amount numeric(18,4) NOT NULL DEFAULT 50;

ALTER TABLE company
  ADD CONSTRAINT company_tolerance_sane CHECK (
    match_tolerance_pct >= 0 AND match_tolerance_pct <= 100
    AND match_tolerance_amount >= 0);

-- ---------------------------------------------------------------------------
-- Paying suppliers
-- ---------------------------------------------------------------------------

CREATE TABLE supplier_payment (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES supplier(id) ON DELETE RESTRICT,

  payment_number text NOT NULL,
  uuid        uuid NOT NULL,

  paid_on     date NOT NULL DEFAULT current_date,
  method      text NOT NULL,
  reference   text,

  amount      numeric(18,4) NOT NULL,
  currency    text NOT NULL,

  journal_entry_id uuid REFERENCES journal_entry(id),

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id),

  CONSTRAINT supplier_payment_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX supplier_payment_number_uq
  ON supplier_payment (company_id, payment_number);
CREATE UNIQUE INDEX supplier_payment_uuid_uq
  ON supplier_payment (tenant_id, uuid);
CREATE INDEX supplier_payment_supplier_idx
  ON supplier_payment (supplier_id, paid_on DESC);
CREATE INDEX supplier_payment_tenant_idx ON supplier_payment (tenant_id);

ALTER TABLE supplier_payment ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_payment FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_payment_isolation ON supplier_payment
  USING (tenant_id = current_tenant_id());

-- One payment can settle several bills, and one bill can take several
-- payments. The allocation is its own table because netting it into either
-- side loses which invoice a part-payment was against — which is the first
-- thing a supplier asks during a reconciliation.
CREATE TABLE supplier_payment_allocation (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  payment_id  uuid NOT NULL REFERENCES supplier_payment(id) ON DELETE CASCADE,
  bill_id     uuid NOT NULL REFERENCES purchase_bill(id) ON DELETE RESTRICT,
  amount      numeric(18,4) NOT NULL,

  CONSTRAINT allocation_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX allocation_uq ON supplier_payment_allocation (payment_id, bill_id);
CREATE INDEX allocation_bill_idx   ON supplier_payment_allocation (bill_id);
CREATE INDEX allocation_tenant_idx ON supplier_payment_allocation (tenant_id);

ALTER TABLE supplier_payment_allocation ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_payment_allocation FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_payment_allocation_isolation
  ON supplier_payment_allocation USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Numbering
-- ---------------------------------------------------------------------------

-- The same row-lock counter pattern the invoice series uses. Never max()+1,
-- which collides under concurrency, and never a sequence, which is not
-- transactional and so leaves permanent gaps when a transaction rolls back —
-- and a gap in a purchase order series is what an auditor asks about.
ALTER TABLE company
  ADD COLUMN next_po_no      bigint NOT NULL DEFAULT 1,
  ADD COLUMN next_grn_no     bigint NOT NULL DEFAULT 1,
  ADD COLUMN next_payment_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_purchase_no(
  p_company_id uuid, p_kind text
) RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_next bigint;
BEGIN
  IF p_kind = 'po' THEN
    UPDATE company SET next_po_no = next_po_no + 1
    WHERE id = p_company_id RETURNING next_po_no - 1 INTO v_next;
  ELSIF p_kind = 'grn' THEN
    UPDATE company SET next_grn_no = next_grn_no + 1
    WHERE id = p_company_id RETURNING next_grn_no - 1 INTO v_next;
  ELSIF p_kind = 'payment' THEN
    UPDATE company SET next_payment_no = next_payment_no + 1
    WHERE id = p_company_id RETURNING next_payment_no - 1 INTO v_next;
  ELSE
    RAISE EXCEPTION 'unknown purchase document kind: %', p_kind;
  END IF;

  IF v_next IS NULL THEN
    RAISE EXCEPTION 'company % not found', p_company_id;
  END IF;
  RETURN v_next;
END;
$$;

-- ---------------------------------------------------------------------------
-- What is still owed on a purchase order
-- ---------------------------------------------------------------------------

-- Quantity AND money, for the same reason returnable_lines reports both: a
-- receiving screen needs to know how many are outstanding, and a match needs
-- to know what they were worth.
CREATE OR REPLACE FUNCTION po_outstanding(p_po_id uuid)
RETURNS TABLE (
  po_line_id     uuid,
  line_no        integer,
  variant_id     uuid,
  description    text,
  qty_ordered    numeric,
  qty_received   numeric,
  qty_outstanding numeric,
  qty_billed     numeric,
  unit_cost      numeric,
  net_amount     numeric,
  tax_amount     numeric,
  gross_amount   numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    l.id, l.line_no, l.variant_id, l.description,
    l.qty_ordered,
    coalesce(r.received, 0),
    -- Never negative. Over-receiving is a real event — a supplier sends a
    -- bonus case — and reporting "-2 outstanding" would be arithmetic rather
    -- than an answer to the question the receiving screen is asking.
    greatest(l.qty_ordered - coalesce(r.received, 0), 0),
    coalesce(b.billed, 0),
    l.unit_cost, l.net_amount, l.tax_amount, l.gross_amount
  FROM po_line l
  LEFT JOIN (
    SELECT gl.po_line_id, sum(gl.qty_received - gl.qty_rejected) AS received
    FROM grn_line gl GROUP BY gl.po_line_id
  ) r ON r.po_line_id = l.id
  LEFT JOIN (
    SELECT bl.po_line_id, sum(bl.qty_billed) AS billed
    FROM bill_line bl
    JOIN purchase_bill pb ON pb.id = bl.bill_id AND pb.status <> 'cancelled'
    WHERE bl.po_line_id IS NOT NULL
    GROUP BY bl.po_line_id
  ) b ON b.po_line_id = l.id
  WHERE l.po_id = p_po_id
  ORDER BY l.line_no;
$$;

-- What is owed to whom, and for how long.
--
-- Buckets are 0–30 / 31–60 / 61–90 / 90+ per B6, measured from the DUE date
-- rather than the bill date: a 60-day-terms invoice raised 45 days ago is not
-- overdue, and ageing it from issue would say it was.
CREATE OR REPLACE FUNCTION supplier_ageing(p_company_id uuid, p_as_of date)
RETURNS TABLE (
  supplier_id   uuid,
  supplier_name text,
  not_due       numeric,
  days_0_30     numeric,
  days_31_60    numeric,
  days_61_90    numeric,
  days_90_plus  numeric,
  total         numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    s.id, s.legal_name,
    coalesce(sum(o.outstanding) FILTER (WHERE b.due_date >= p_as_of), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE b.due_date < p_as_of AND p_as_of - b.due_date <= 30), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - b.due_date BETWEEN 31 AND 60), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - b.due_date BETWEEN 61 AND 90), 0),
    coalesce(sum(o.outstanding) FILTER (WHERE p_as_of - b.due_date > 90), 0),
    coalesce(sum(o.outstanding), 0)
  FROM supplier s
  JOIN purchase_bill b ON b.supplier_id = s.id
  CROSS JOIN LATERAL (
    SELECT b.total_inclusive - b.amount_paid AS outstanding
  ) o
  WHERE s.company_id = p_company_id
    AND b.status IN ('matched', 'blocked', 'approved')
    AND b.total_inclusive > b.amount_paid
  GROUP BY s.id, s.legal_name
  HAVING sum(o.outstanding) <> 0
  ORDER BY sum(o.outstanding) DESC;
$$;
