-- 0088 — Delivery, stock reservation, instalments and after-sales
--        (blueprint B13, B14, B15).
--
-- # Reservation is a movement, not a column
--
-- B13: stock reservation exists "so two channels can't both sell the last
-- unit". The obvious implementation — a `qty_reserved` column on the variant —
-- is wrong here for the reason design 03 gives for stock generally: quantity is
-- the SUM of movements, never a stored figure, because a stored figure and its
-- movements drift and only one of them can be right.
--
-- So a reservation is a row in its own ledger with a sign, and available-to-sell
-- is `on hand − reserved`. Releasing is another row, not a decrement. That also
-- makes "why is this unit unavailable" answerable: the order that holds it is
-- on the row.
--
-- Reservations deliberately do NOT touch stock_movement. Reserved goods are
-- still owned, still on the shelf, still in the inventory valuation and still
-- in the Inventory control account — nothing has left the business, so nothing
-- posts. A reservation that wrote a stock movement would take the goods out of
-- the valuation while they were still there, and the C13 tie-out would fail.
--
-- # An instalment plan is not revenue over time
--
-- B14's example: a 1,200 product, 300 down, 3 × 300. The SALE is 1,200 today —
-- the customer has the goods — and the 900 is a receivable, exactly like any
-- other credit sale. What the markup adds is finance income, which is earned
-- over the term rather than at the sale.
--
-- This module therefore does not post the sale (the POS already did) and does
-- not invent a second revenue line. It records the schedule against the
-- existing receivable, and each collection is an ordinary customer receipt.
-- Any markup is booked as deferred finance income and released as instalments
-- fall due, so a shop selling on credit does not report profit it has not
-- earned yet.
--
-- # A serial number is a unit of stock, not a note about one
--
-- B15 wants the lifecycle "Supplier → Purchase → Inventory → Sale → Customer →
-- Warranty → Return" for one physical unit. That needs the unit to have an
-- identity of its own: two phones of the same variant are the same SKU and
-- different things, and only one of them is the one with the cracked screen.

-- ---------------------------------------------------------------------------
-- Stock reservation (B13)
-- ---------------------------------------------------------------------------

CREATE TABLE stock_reservation (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- Signed, like a stock movement: positive holds stock, negative releases it.
  -- A balance is one SUM and cannot be got wrong by subtracting in the wrong
  -- order, and the history of a contested unit stays readable.
  qty          numeric(18,4) NOT NULL,

  -- held     — the order is open and the stock is spoken for
  -- released — the order was cancelled or expired
  -- consumed — the goods went out; a stock movement now exists
  reason       text NOT NULL,

  order_id     uuid REFERENCES sales_order(id) ON DELETE CASCADE,

  -- When an unpaid reservation stops holding stock. B13 reserves against
  -- UNPAID online orders, and an abandoned basket must not hold the last unit
  -- forever. Null means it is held until the order resolves.
  expires_at   timestamptz,

  note         text,
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT stock_reservation_qty_not_zero CHECK (qty <> 0),
  CONSTRAINT stock_reservation_reason_valid CHECK (reason IN (
    'held', 'released', 'consumed')),
  -- Holding is positive, letting go is negative. Encoded so a caller cannot
  -- write a "release" that in fact reserves more.
  CONSTRAINT stock_reservation_sign_matches_reason CHECK (
    (reason = 'held' AND qty > 0) OR (reason <> 'held' AND qty < 0))
);

CREATE INDEX stock_reservation_available_idx
  ON stock_reservation (company_id, variant_id, warehouse_id);
CREATE INDEX stock_reservation_order_idx ON stock_reservation (order_id)
  WHERE order_id IS NOT NULL;
CREATE INDEX stock_reservation_expiry_idx ON stock_reservation (expires_at)
  WHERE expires_at IS NOT NULL AND reason = 'held';
CREATE INDEX stock_reservation_tenant_idx ON stock_reservation (tenant_id);

ALTER TABLE stock_reservation ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_reservation FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_reservation_isolation ON stock_reservation
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER stock_reservation_no_change
  BEFORE UPDATE OR DELETE ON stock_reservation
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- How much of a variant is spoken for but not yet gone.
CREATE OR REPLACE FUNCTION stock_reserved(
  p_variant_id uuid, p_warehouse_id uuid
) RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(qty), 0)
  FROM stock_reservation
  WHERE variant_id = p_variant_id AND warehouse_id = p_warehouse_id;
$$;

-- What a till or a web shop may actually sell. B4 calls this
-- "Available-to-Sell" and lists it beside Total and Reserved.
CREATE OR REPLACE FUNCTION stock_available_to_sell(
  p_variant_id uuid, p_warehouse_id uuid
) RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT stock_on_hand(p_variant_id, p_warehouse_id)
       - stock_reserved(p_variant_id, p_warehouse_id);
$$;

-- ---------------------------------------------------------------------------
-- Delivery (B13)
-- ---------------------------------------------------------------------------
--
-- Separate from sales_order because the pipelines are genuinely different
-- lengths: an order is placed, packed and completed, while a delivery is
-- assigned to a driver, picked up, attempted, and sometimes fails and comes
-- back. Folding seven delivery states into the order's state column would make
-- "delivered" mean two things and lose the attempt that failed.

CREATE TABLE delivery (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  delivery_no  text NOT NULL,
  order_id     uuid NOT NULL REFERENCES sales_order(id) ON DELETE RESTRICT,

  -- B13's pipeline, exactly: Pending → Assigned → Picked Up → Out for Delivery
  -- → Delivered → Failed → Returned.
  status       text NOT NULL DEFAULT 'pending',

  -- The driver is a member of staff (the Delivery Staff role in A6.1), so a
  -- driver signing in sees their own assignments and nothing else.
  driver_id    uuid REFERENCES app_user(id) ON DELETE SET NULL,

  address      text NOT NULL,
  phone        text,
  -- What the customer pays to have it brought. Revenue when the delivery
  -- completes, and part of the invoice rather than of this record — carried
  -- here so a driver can be told what to collect.
  fee          numeric(18,4) NOT NULL DEFAULT 0,

  -- Cash on delivery: the driver collects, so the money is not the shop's
  -- until they hand it in. Tracked so an owner can see what is out with
  -- drivers, which is a real exposure in a COD market.
  is_cod       boolean NOT NULL DEFAULT false,
  cod_amount   numeric(18,4) NOT NULL DEFAULT 0,
  cod_collected_at timestamptz,

  assigned_at  timestamptz,
  picked_up_at timestamptz,
  delivered_at timestamptz,
  -- Why it did not arrive. Required on a failure: "failed" alone tells the
  -- shop nothing about whether to try again tomorrow or ring the customer.
  failure_reason text,

  attempt_count integer NOT NULL DEFAULT 0,
  note         text,
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT delivery_status_valid CHECK (status IN (
    'pending', 'assigned', 'picked_up', 'out_for_delivery',
    'delivered', 'failed', 'returned')),
  CONSTRAINT delivery_fee_sane CHECK (fee >= 0),
  CONSTRAINT delivery_cod_sane CHECK (
    cod_amount >= 0 AND (is_cod OR cod_amount = 0)),
  CONSTRAINT delivery_failure_says_why CHECK (
    status <> 'failed' OR btrim(coalesce(failure_reason, '')) <> ''),
  -- Anything past 'assigned' has somebody carrying it.
  CONSTRAINT delivery_assigned_has_a_driver CHECK (
    status IN ('pending', 'returned') OR driver_id IS NOT NULL)
);

CREATE UNIQUE INDEX delivery_no_uq ON delivery (company_id, delivery_no);
CREATE INDEX delivery_order_idx ON delivery (order_id);
CREATE INDEX delivery_driver_idx ON delivery (driver_id, status)
  WHERE driver_id IS NOT NULL;
CREATE INDEX delivery_open_idx ON delivery (company_id, created_at DESC)
  WHERE status NOT IN ('delivered', 'returned');
CREATE INDEX delivery_tenant_idx ON delivery (tenant_id);

CREATE TRIGGER delivery_touch BEFORE UPDATE ON delivery
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE delivery FORCE  ROW LEVEL SECURITY;
CREATE POLICY delivery_isolation ON delivery
  USING (tenant_id = current_tenant_id());

-- Every state change, kept. A delivery that failed twice and then arrived is a
-- different story from one that arrived first time, and the current status
-- alone cannot tell them apart.
CREATE TABLE delivery_event (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  delivery_id uuid NOT NULL REFERENCES delivery(id) ON DELETE CASCADE,

  status      text NOT NULL,
  note        text,
  -- Where the driver said they were. Optional, and deliberately not required:
  -- a phone with no signal in a basement still has to be able to mark a
  -- delivery done.
  latitude    numeric(9,6),
  longitude   numeric(9,6),

  recorded_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  recorded_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX delivery_event_idx ON delivery_event (delivery_id, recorded_at);
CREATE INDEX delivery_event_tenant_idx ON delivery_event (tenant_id);

ALTER TABLE delivery_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE delivery_event FORCE  ROW LEVEL SECURITY;
CREATE POLICY delivery_event_isolation ON delivery_event
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER delivery_event_no_change
  BEFORE UPDATE OR DELETE ON delivery_event
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- Serial / IMEI tracking (B15, B4)
-- ---------------------------------------------------------------------------

CREATE TABLE stock_serial (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  variant_id   uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  serial_no    text NOT NULL,

  -- in_stock  — received, sellable
  -- reserved  — allocated to an order
  -- sold      — with a customer
  -- returned  — came back, not yet resolved
  -- in_repair — with the service desk
  -- scrapped  — written off
  status       text NOT NULL DEFAULT 'in_stock',

  warehouse_id uuid REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- Where it came from and where it went. B15's lifecycle in four columns.
  grn_id       uuid REFERENCES goods_receipt(id) ON DELETE SET NULL,
  supplier_id  uuid REFERENCES supplier(id)      ON DELETE SET NULL,
  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  customer_id  uuid REFERENCES customer(id)      ON DELETE SET NULL,

  sold_at      timestamptz,
  -- Warranty runs from the SALE, not from the receipt: the customer's cover
  -- starts when they take it home. Computed when it is sold, from the
  -- product's warranty_months, and stored because a product whose warranty
  -- terms change later must not retroactively alter a sold unit's cover.
  warranty_until date,

  note         text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT stock_serial_status_valid CHECK (status IN (
    'in_stock', 'reserved', 'sold', 'returned', 'in_repair', 'scrapped')),
  CONSTRAINT stock_serial_no_not_blank CHECK (btrim(serial_no) <> ''),
  -- A sold unit knows when and to whom. Without this a serial could be marked
  -- sold with no invoice, and the warranty desk would have nothing to verify.
  CONSTRAINT stock_serial_sold_has_an_invoice CHECK (
    status <> 'sold' OR (invoice_id IS NOT NULL AND sold_at IS NOT NULL))
);

-- A serial number is unique within a company, not globally: two tenants may
-- legitimately hold the same manufacturer's serial, and an IMEI is unique in
-- the world but a hand-typed asset tag is not.
CREATE UNIQUE INDEX stock_serial_uq
  ON stock_serial (company_id, upper(serial_no));
CREATE INDEX stock_serial_variant_idx ON stock_serial (variant_id, status);
CREATE INDEX stock_serial_customer_idx ON stock_serial (customer_id)
  WHERE customer_id IS NOT NULL;
CREATE INDEX stock_serial_invoice_idx ON stock_serial (invoice_id)
  WHERE invoice_id IS NOT NULL;
CREATE INDEX stock_serial_warranty_idx ON stock_serial (company_id, warranty_until)
  WHERE warranty_until IS NOT NULL;
CREATE INDEX stock_serial_tenant_idx ON stock_serial (tenant_id);

CREATE TRIGGER stock_serial_touch BEFORE UPDATE ON stock_serial
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE stock_serial ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_serial FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_serial_isolation ON stock_serial
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Warranty claims and service work orders (B15)
-- ---------------------------------------------------------------------------

CREATE TABLE service_order (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  job_no       text NOT NULL,
  store_id     uuid REFERENCES store(id) ON DELETE SET NULL,

  customer_id  uuid REFERENCES customer(id) ON DELETE SET NULL,
  serial_id    uuid REFERENCES stock_serial(id) ON DELETE SET NULL,
  variant_id   uuid REFERENCES variant(id) ON DELETE SET NULL,
  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE SET NULL,

  -- warranty — the shop is covering it
  -- paid     — the customer is paying
  -- goodwill — out of warranty, but the shop chose to absorb it
  --
  -- Recorded rather than derived from the warranty date, because a manager can
  -- decide to honour a claim that expired last week and the books must say
  -- that is what happened.
  kind         text NOT NULL DEFAULT 'paid',

  -- B15: Received → Under Inspection → Repaired → Delivered. Plus the two
  -- endings that are not "repaired": irreparable, and replaced outright.
  status       text NOT NULL DEFAULT 'received',

  fault_reported text NOT NULL,
  diagnosis    text,
  work_done    text,

  -- What the job cost the shop, split as B15 asks. Parts come out of stock and
  -- post; labour is recorded for margin and does not post, because the
  -- technician's wage is already an expense through payroll and booking it
  -- again here would count it twice.
  parts_cost   numeric(18,4) NOT NULL DEFAULT 0,
  labour_cost  numeric(18,4) NOT NULL DEFAULT 0,
  -- What the customer is charged. Zero on a warranty job by definition.
  charged      numeric(18,4) NOT NULL DEFAULT 0,

  -- Set when a replacement was given instead of a repair, so B15's
  -- "Product Replacement Register" is a query rather than a second table.
  replacement_serial_id uuid REFERENCES stock_serial(id) ON DELETE SET NULL,

  promised_on  date,
  received_at  timestamptz NOT NULL DEFAULT now(),
  closed_at    timestamptz,

  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT service_order_kind_valid CHECK (kind IN (
    'warranty', 'paid', 'goodwill')),
  CONSTRAINT service_order_status_valid CHECK (status IN (
    'received', 'inspecting', 'awaiting_parts', 'repaired', 'irreparable',
    'replaced', 'delivered', 'cancelled')),
  CONSTRAINT service_order_costs_sane CHECK (
    parts_cost >= 0 AND labour_cost >= 0 AND charged >= 0),
  -- A warranty job is free to the customer. If a shop wants to charge, the job
  -- is not a warranty job — it is paid or goodwill, and calling it what it is
  -- keeps the warranty cost figure honest.
  CONSTRAINT service_order_warranty_is_free CHECK (
    kind <> 'warranty' OR charged = 0),
  CONSTRAINT service_order_fault_not_blank CHECK (btrim(fault_reported) <> ''),
  CONSTRAINT service_order_replacement_is_marked CHECK (
    replacement_serial_id IS NULL OR status IN ('replaced', 'delivered'))
);

CREATE UNIQUE INDEX service_order_no_uq ON service_order (company_id, job_no);
CREATE INDEX service_order_open_idx ON service_order (company_id, received_at DESC)
  WHERE status NOT IN ('delivered', 'cancelled');
CREATE INDEX service_order_customer_idx ON service_order (customer_id)
  WHERE customer_id IS NOT NULL;
CREATE INDEX service_order_serial_idx ON service_order (serial_id)
  WHERE serial_id IS NOT NULL;
CREATE INDEX service_order_tenant_idx ON service_order (tenant_id);

CREATE TRIGGER service_order_touch BEFORE UPDATE ON service_order
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE service_order ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_order FORCE  ROW LEVEL SECURITY;
CREATE POLICY service_order_isolation ON service_order
  USING (tenant_id = current_tenant_id());

-- Parts consumed on a job. Their own table because a job can take three parts
-- and each leaves stock separately.
CREATE TABLE service_part (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  service_id   uuid NOT NULL REFERENCES service_order(id) ON DELETE CASCADE,

  variant_id   uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  qty          numeric(18,4) NOT NULL,
  -- What it cost the shop, taken from the costing engine at the moment it was
  -- issued. Stored because the cost of that part changes with the next
  -- delivery and this job's margin must not move with it.
  unit_cost    numeric(18,4) NOT NULL DEFAULT 0,

  issued_at    timestamptz NOT NULL DEFAULT now(),
  issued_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT service_part_qty_positive CHECK (qty > 0),
  CONSTRAINT service_part_cost_sane CHECK (unit_cost >= 0)
);

CREATE INDEX service_part_job_idx ON service_part (service_id);
CREATE INDEX service_part_tenant_idx ON service_part (tenant_id);

ALTER TABLE service_part ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_part FORCE  ROW LEVEL SECURITY;
CREATE POLICY service_part_isolation ON service_part
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Instalments / EMI (B14)
-- ---------------------------------------------------------------------------

CREATE TABLE installment_plan (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  plan_no      text NOT NULL,
  customer_id  uuid NOT NULL REFERENCES customer(id) ON DELETE RESTRICT,
  invoice_id   uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,

  -- active    — instalments outstanding
  -- settled   — everything paid
  -- defaulted — written off or handed to collection
  -- cancelled — unwound, e.g. the goods came back
  status       text NOT NULL DEFAULT 'active',

  -- B14's arithmetic, all recorded rather than recomputed: a plan agreed at
  -- last year's rate must still read the way it was signed.
  principal    numeric(18,4) NOT NULL,   -- the invoice total
  down_payment numeric(18,4) NOT NULL DEFAULT 0,
  financed     numeric(18,4) NOT NULL,   -- principal − down payment
  markup_rate  numeric(9,6)  NOT NULL DEFAULT 0,
  markup_amount numeric(18,4) NOT NULL DEFAULT 0,
  tenure_months integer NOT NULL,
  installment_amount numeric(18,4) NOT NULL,

  -- Late fee per B14's "configurable late-payment penalty". A flat amount and
  -- a rate, either or both, because shops charge both ways and a Saudi shop
  -- may charge neither.
  late_fee_flat numeric(18,4) NOT NULL DEFAULT 0,
  late_fee_rate numeric(9,6)  NOT NULL DEFAULT 0,
  grace_days   integer NOT NULL DEFAULT 0,

  currency     text NOT NULL,
  starts_on    date NOT NULL,

  -- B14's verification records. PDPL-controlled (E4): these are copies of a
  -- national ID, so they are stored as document references rather than as
  -- files here, and reading them is a separate permission.
  guarantor_name  text,
  guarantor_phone text,
  guarantor_id_no text,
  verification_note text,

  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT installment_plan_status_valid CHECK (status IN (
    'active', 'settled', 'defaulted', 'cancelled')),
  CONSTRAINT installment_plan_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT installment_plan_amounts_sane CHECK (
    principal > 0 AND down_payment >= 0 AND down_payment <= principal
    AND financed >= 0 AND markup_amount >= 0 AND installment_amount > 0),
  -- The figure that must hold: what was financed is what was not paid up front.
  CONSTRAINT installment_plan_financed_adds_up CHECK (
    financed = principal - down_payment),
  CONSTRAINT installment_plan_tenure_positive CHECK (tenure_months > 0),
  CONSTRAINT installment_plan_fees_sane CHECK (
    late_fee_flat >= 0 AND late_fee_rate >= 0 AND grace_days >= 0)
);

CREATE UNIQUE INDEX installment_plan_no_uq
  ON installment_plan (company_id, plan_no);
-- One plan per invoice. Two would each claim the same receivable.
CREATE UNIQUE INDEX installment_plan_invoice_uq
  ON installment_plan (invoice_id);
CREATE INDEX installment_plan_customer_idx
  ON installment_plan (customer_id, status);
CREATE INDEX installment_plan_tenant_idx ON installment_plan (tenant_id);

CREATE TRIGGER installment_plan_touch BEFORE UPDATE ON installment_plan
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE installment_plan ENABLE ROW LEVEL SECURITY;
ALTER TABLE installment_plan FORCE  ROW LEVEL SECURITY;
CREATE POLICY installment_plan_isolation ON installment_plan
  USING (tenant_id = current_tenant_id());

-- The schedule. One row per instalment, written when the plan is created, so a
-- customer can be shown the whole thing on day one.
CREATE TABLE installment_due (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  plan_id      uuid NOT NULL REFERENCES installment_plan(id) ON DELETE CASCADE,

  seq          integer NOT NULL,
  due_on       date NOT NULL,
  amount       numeric(18,4) NOT NULL,

  -- Paid is a SUM of the receipts allocated to this instalment, not a stored
  -- figure — the same rule the rest of the ledger follows. Status is derived
  -- from it and the date by installment_state().
  waived       numeric(18,4) NOT NULL DEFAULT 0,
  late_fee     numeric(18,4) NOT NULL DEFAULT 0,

  CONSTRAINT installment_due_amount_positive CHECK (amount > 0),
  CONSTRAINT installment_due_waived_sane CHECK (waived >= 0 AND waived <= amount),
  CONSTRAINT installment_due_fee_sane CHECK (late_fee >= 0),
  CONSTRAINT installment_due_seq_positive CHECK (seq > 0)
);

CREATE UNIQUE INDEX installment_due_seq_uq ON installment_due (plan_id, seq);
CREATE INDEX installment_due_date_idx ON installment_due (due_on);
CREATE INDEX installment_due_tenant_idx ON installment_due (tenant_id);

ALTER TABLE installment_due ENABLE ROW LEVEL SECURITY;
ALTER TABLE installment_due FORCE  ROW LEVEL SECURITY;
CREATE POLICY installment_due_isolation ON installment_due
  USING (tenant_id = current_tenant_id());

-- Which receipt paid which instalment. A customer receipt already exists and
-- already posts; this says what it was FOR, so the schedule can be marked off
-- without inventing a second payment path that would double-count the money.
CREATE TABLE installment_payment (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  due_id       uuid NOT NULL REFERENCES installment_due(id) ON DELETE CASCADE,
  receipt_id   uuid NOT NULL REFERENCES customer_receipt(id) ON DELETE RESTRICT,

  amount       numeric(18,4) NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT installment_payment_amount_positive CHECK (amount > 0)
);

CREATE INDEX installment_payment_due_idx ON installment_payment (due_id);
CREATE INDEX installment_payment_receipt_idx ON installment_payment (receipt_id);
CREATE INDEX installment_payment_tenant_idx ON installment_payment (tenant_id);

ALTER TABLE installment_payment ENABLE ROW LEVEL SECURITY;
ALTER TABLE installment_payment FORCE  ROW LEVEL SECURITY;
CREATE POLICY installment_payment_isolation ON installment_payment
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER installment_payment_no_change
  BEFORE UPDATE OR DELETE ON installment_payment
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- What an instalment is, right now. B14 wants Paid / Unpaid / Overdue /
-- Partial, and all four fall out of the money and the date — so this is a
-- function rather than a column that a missed job would leave stale.
CREATE OR REPLACE FUNCTION installment_state(p_due_id uuid)
RETURNS text
LANGUAGE sql STABLE AS $$
  SELECT CASE
    WHEN d.waived >= d.amount THEN 'waived'
    WHEN coalesce(p.paid, 0) + d.waived >= d.amount THEN 'paid'
    WHEN coalesce(p.paid, 0) > 0 AND d.due_on < current_date THEN 'overdue'
    WHEN coalesce(p.paid, 0) > 0 THEN 'partial'
    WHEN d.due_on < current_date THEN 'overdue'
    ELSE 'unpaid'
  END
  FROM installment_due d
  LEFT JOIN (
    SELECT due_id, sum(amount) AS paid
    FROM installment_payment GROUP BY due_id
  ) p ON p.due_id = d.id
  WHERE d.id = p_due_id;
$$;

-- ---------------------------------------------------------------------------
-- Where instalment finance income lands
-- ---------------------------------------------------------------------------
--
-- The markup on a plan is not earned when the goods go out; it is earned over
-- the term the shop waits for its money. Booking it all at the sale would
-- report profit the business has not made yet, which is exactly the error a
-- shop selling heavily on instalments would compound month after month.
--
-- So the markup sits in a liability (income received in advance) and is
-- released to revenue as each instalment falls due.

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, v.code, v.name,
       jsonb_build_object('ar', v.name_ar), v.kind
FROM company c
CROSS JOIN (VALUES
  ('2500', 'Deferred Finance Income', 'إيرادات تمويل مؤجلة', 'liability'),
  ('4300', 'Finance Income',          'إيرادات التمويل',     'revenue'),
  ('4400', 'Delivery Income',         'إيرادات التوصيل',     'revenue'),
  ('5450', 'Warranty & Service Cost', 'تكلفة الضمان والصيانة', 'expense')
) AS v(code, name, name_ar, kind)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, v.role, a.id
FROM account a
JOIN (VALUES
  ('2500', 'deferred_finance_income'),
  ('4300', 'finance_income'),
  ('4400', 'delivery_income'),
  ('5450', 'warranty_cost')
) AS v(code, role) ON v.code = a.code
ON CONFLICT DO NOTHING;

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES

-- Setting up a plan moves the markup out of the receivable's future and into
-- a liability. The SALE has already posted revenue, VAT and COGS; this is only
-- about the finance charge on top.
('installment.open', NULL, 1,
 '[{"role": "accounts_receivable",      "side": "debit",  "amount": "markup"},
   {"role": "deferred_finance_income",  "side": "credit", "amount": "markup"}]'::jsonb,
 'The finance charge on an instalment plan, owed by the customer and not yet earned.',
 '2020-01-01'),

-- Each instalment falling due earns its share of that charge.
('installment.accrue', NULL, 1,
 '[{"role": "deferred_finance_income", "side": "debit",  "amount": "amount"},
   {"role": "finance_income",          "side": "credit", "amount": "amount"}]'::jsonb,
 'Finance income earned as an instalment falls due.',
 '2020-01-01'),

-- A part issued on a warranty job leaves stock at cost. The shop absorbs it:
-- no revenue, because the customer is paying nothing.
('service.warranty_part', NULL, 1,
 '[{"role": "warranty_cost", "side": "debit",  "amount": "amount"},
   {"role": "inventory",     "side": "credit", "amount": "amount"}]'::jsonb,
 'A part fitted under warranty: stock out, and the cost absorbed by the shop.',
 '2020-01-01')

ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'delivery.view'),   ('owner',            'delivery.manage'),
  ('owner',            'service.view'),    ('owner',            'service.manage'),
  ('owner',            'installment.view'),('owner',            'installment.manage'),
  ('owner',            'serial.view'),     ('owner',            'serial.manage'),

  ('store_manager',    'delivery.view'),   ('store_manager',    'delivery.manage'),
  ('store_manager',    'service.view'),    ('store_manager',    'service.manage'),
  ('store_manager',    'installment.view'),('store_manager',    'installment.manage'),
  ('store_manager',    'serial.view'),     ('store_manager',    'serial.manage'),

  -- A driver sees their own runs and marks them delivered. Nothing else.
  ('delivery_staff',   'delivery.view'),   ('delivery_staff',   'delivery.deliver'),

  ('online_manager',   'delivery.view'),   ('online_manager',   'delivery.manage'),
  ('online_manager',   'serial.view'),

  -- The counter sells serialised goods and takes repairs in.
  ('cashier',          'serial.view'),
  ('cashier',          'service.view'),
  ('cashier',          'installment.view'),

  ('customer_service', 'service.view'),    ('customer_service', 'service.manage'),
  ('customer_service', 'serial.view'),
  ('customer_service', 'delivery.view'),
  ('customer_service', 'installment.view'),

  ('inventory_keeper', 'serial.view'),     ('inventory_keeper', 'serial.manage'),

  ('accountant',       'installment.view'),('accountant',       'installment.manage'),
  ('accountant',       'delivery.view'),   ('accountant',       'service.view'),

  ('auditor',          'delivery.view'),   ('auditor',          'service.view'),
  ('auditor',          'installment.view'),('auditor',          'serial.view')
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
      ('owner',            'delivery.view'),   ('owner',            'delivery.manage'),
      ('owner',            'service.view'),    ('owner',            'service.manage'),
      ('owner',            'installment.view'),('owner',            'installment.manage'),
      ('owner',            'serial.view'),     ('owner',            'serial.manage'),
      ('store_manager',    'delivery.view'),   ('store_manager',    'delivery.manage'),
      ('store_manager',    'service.view'),    ('store_manager',    'service.manage'),
      ('store_manager',    'installment.view'),('store_manager',    'installment.manage'),
      ('store_manager',    'serial.view'),     ('store_manager',    'serial.manage'),
      ('delivery_staff',   'delivery.view'),   ('delivery_staff',   'delivery.deliver'),
      ('online_manager',   'delivery.view'),   ('online_manager',   'delivery.manage'),
      ('online_manager',   'serial.view'),
      ('cashier',          'serial.view'),
      ('cashier',          'service.view'),
      ('cashier',          'installment.view'),
      ('customer_service', 'service.view'),    ('customer_service', 'service.manage'),
      ('customer_service', 'serial.view'),
      ('customer_service', 'delivery.view'),
      ('customer_service', 'installment.view'),
      ('inventory_keeper', 'serial.view'),     ('inventory_keeper', 'serial.manage'),
      ('accountant',       'installment.view'),('accountant',       'installment.manage'),
      ('accountant',       'delivery.view'),   ('accountant',       'service.view'),
      ('auditor',          'delivery.view'),   ('auditor',          'service.view'),
      ('auditor',          'installment.view'),('auditor',          'serial.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- Document numbering
-- ---------------------------------------------------------------------------

ALTER TABLE company
  ADD COLUMN IF NOT EXISTS next_delivery_no    bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS next_service_no     bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS next_installment_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_document_no(
  p_company_id uuid, p_kind text
) RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_next bigint;
BEGIN
  IF p_kind = 'delivery' THEN
    UPDATE company SET next_delivery_no = next_delivery_no + 1
    WHERE id = p_company_id RETURNING next_delivery_no - 1 INTO v_next;
  ELSIF p_kind = 'service' THEN
    UPDATE company SET next_service_no = next_service_no + 1
    WHERE id = p_company_id RETURNING next_service_no - 1 INTO v_next;
  ELSIF p_kind = 'installment' THEN
    UPDATE company SET next_installment_no = next_installment_no + 1
    WHERE id = p_company_id RETURNING next_installment_no - 1 INTO v_next;
  ELSE
    RAISE EXCEPTION 'unknown document kind: %', p_kind;
  END IF;

  IF v_next IS NULL THEN
    RAISE EXCEPTION 'company % not found', p_company_id;
  END IF;
  RETURN v_next;
END;
$$;
