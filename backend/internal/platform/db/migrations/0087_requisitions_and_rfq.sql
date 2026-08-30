-- 0087 — Requisition, RFQ and supplier quote comparison (blueprint B5, B5.1).
--
-- # The half of the purchasing chain 0031 left out
--
-- 0031 built supplier → PO → GRN → Bill → match → payment and said plainly why
-- it stopped there: requisition and RFQ "are an approval workflow that FEEDS a
-- purchase order and that nothing downstream depends on, so they are
-- deliberately absent rather than half-present". This is that workflow, and the
-- seam is exactly where 0031 left it — everything here ENDS at a purchase
-- order, and the purchase order does not change shape to accommodate it.
--
-- # Why a quote is not a purchase order
--
-- They carry the same columns — supplier, lines, quantities, unit costs, tax —
-- and the temptation is to make a quote a purchase order in a 'quoted' status.
-- That is wrong for a reason that shows up at audit: a shop asks three
-- suppliers and buys from one. Two of those documents must never become orders,
-- never appear in committed-spend, and never age into a payable. A status on
-- purchase_order would put them one careless WHERE clause away from all three.
--
-- B5.1 also wants them kept: "historical quote archive per supplier — useful
-- for negotiating next time and for proving best-price sourcing during an
-- audit". So the losing quotes are not deleted; they are simply not orders.
--
-- # Why the requisition holds no prices
--
-- B5's requisition is "any authorized staff can request stock (need 100 pcs
-- Black T-Shirt)". The person who knows the shelf is empty is not the person
-- who negotiates cost, and asking them for a price either invents a number or
-- stops the request. Cost enters at the quote, from the supplier, which is the
-- only place it is a fact rather than a guess.
--
-- # Award is recorded, not inferred
--
-- B5.1: "Select winning supplier (with reason recorded)". Cheapest does not
-- always win — lead time, payment terms and quality carry weight — so the
-- winning quote is stamped with WHO chose it and WHY. Deriving the winner as
-- "the quote whose total is lowest" would be false the first time a shop paid
-- more for faster delivery, and it is precisely the decision an auditor asks
-- about.

-- ---------------------------------------------------------------------------
-- Requisition
-- ---------------------------------------------------------------------------

CREATE TABLE purchase_requisition (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  requisition_no text NOT NULL,

  -- Where the stock is wanted. A requisition raised at a branch that is
  -- fulfilled into the central warehouse is the normal case, so both are
  -- recorded and neither is derived from the other.
  store_id     uuid REFERENCES store(id)     ON DELETE RESTRICT,
  warehouse_id uuid REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- draft     — being written, visible only as a draft
  -- submitted — waiting for a manager
  -- approved  — may be turned into an RFQ or an order
  -- rejected  — declined, with a reason
  -- sourcing  — an RFQ has been raised from it
  -- ordered   — a purchase order exists
  -- cancelled — withdrawn by the requester
  status       text NOT NULL DEFAULT 'draft',

  needed_by    date,
  justification text,

  requested_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),

  decided_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  decided_at   timestamptz,
  decision_note text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT requisition_status_valid CHECK (status IN (
    'draft', 'submitted', 'approved', 'rejected',
    'sourcing', 'ordered', 'cancelled')),

  -- A rejection without a reason is a decision the requester cannot act on.
  CONSTRAINT requisition_rejection_says_why CHECK (
    status <> 'rejected' OR btrim(coalesce(decision_note, '')) <> ''),

  -- Anything past submission was decided by somebody, and that somebody is on
  -- the record. B5 wants "each step logged".
  CONSTRAINT requisition_decision_has_an_author CHECK (
    status NOT IN ('approved', 'rejected') OR decided_by IS NOT NULL)
);

CREATE UNIQUE INDEX requisition_no_uq
  ON purchase_requisition (company_id, requisition_no);
CREATE INDEX requisition_open_idx
  ON purchase_requisition (company_id, requested_at DESC)
  WHERE status IN ('submitted', 'approved', 'sourcing');
CREATE INDEX requisition_tenant_idx ON purchase_requisition (tenant_id);

CREATE TRIGGER purchase_requisition_touch BEFORE UPDATE ON purchase_requisition
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE purchase_requisition ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_requisition FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_requisition_isolation ON purchase_requisition
  USING (tenant_id = current_tenant_id());

CREATE TABLE requisition_line (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  requisition_id uuid NOT NULL REFERENCES purchase_requisition(id)
                   ON DELETE CASCADE,
  line_no        integer NOT NULL,

  -- The variant is the SKU (design 06). Nullable for the one case B5 implies
  -- and a shop actually needs: asking for something not in the catalogue yet,
  -- described in words, which the buyer turns into a product when it is
  -- sourced.
  variant_id     uuid REFERENCES variant(id) ON DELETE RESTRICT,
  description    text NOT NULL,

  qty_requested  numeric(18,4) NOT NULL,

  note           text,

  CONSTRAINT requisition_line_qty_positive CHECK (qty_requested > 0),
  CONSTRAINT requisition_line_says_what CHECK (
    variant_id IS NOT NULL OR btrim(description) <> '')
);

CREATE UNIQUE INDEX requisition_line_no_uq
  ON requisition_line (requisition_id, line_no);
CREATE INDEX requisition_line_tenant_idx ON requisition_line (tenant_id);
CREATE INDEX requisition_line_variant_idx ON requisition_line (variant_id)
  WHERE variant_id IS NOT NULL;

ALTER TABLE requisition_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE requisition_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY requisition_line_isolation ON requisition_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- RFQ
-- ---------------------------------------------------------------------------

CREATE TABLE rfq (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  rfq_number   text NOT NULL,

  -- What prompted it, when it was a requisition. Nullable because a buyer who
  -- already knows what they need does not have to raise a requisition to
  -- themselves first.
  requisition_id uuid REFERENCES purchase_requisition(id) ON DELETE SET NULL,

  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- draft     — being prepared
  -- issued    — suppliers have been asked
  -- comparing — at least one quote is in
  -- awarded   — a winner was chosen
  -- cancelled — abandoned, with a reason
  status       text NOT NULL DEFAULT 'draft',

  -- B5.1: "quote validity dates, automatic expiry". This is the date the shop
  -- asked for replies BY; a quote carries its own validity separately, because
  -- a supplier who answers late may still offer a long-lived price.
  closes_on    date,

  currency     text NOT NULL,
  notes        text,

  -- Set when a quote wins. The reason is required with it — see the check.
  awarded_quote_id uuid,
  award_reason text,
  awarded_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  awarded_at   timestamptz,

  cancel_reason text,

  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  issued_at    timestamptz,
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT rfq_status_valid CHECK (status IN (
    'draft', 'issued', 'comparing', 'awarded', 'cancelled')),
  CONSTRAINT rfq_currency_upper CHECK (currency = upper(currency)),

  -- B5.1 requires the reason to be recorded, so an award without one cannot be
  -- written. This is the audit answer to "why did we not take the cheapest".
  CONSTRAINT rfq_award_is_complete CHECK (
    status <> 'awarded' OR (
      awarded_quote_id IS NOT NULL
      AND btrim(coalesce(award_reason, '')) <> ''
      AND awarded_by IS NOT NULL)),

  CONSTRAINT rfq_cancel_says_why CHECK (
    status <> 'cancelled' OR btrim(coalesce(cancel_reason, '')) <> '')
);

CREATE UNIQUE INDEX rfq_number_uq ON rfq (company_id, rfq_number);
CREATE INDEX rfq_open_idx ON rfq (company_id, created_at DESC)
  WHERE status IN ('issued', 'comparing');
CREATE INDEX rfq_requisition_idx ON rfq (requisition_id)
  WHERE requisition_id IS NOT NULL;
CREATE INDEX rfq_tenant_idx ON rfq (tenant_id);

CREATE TRIGGER rfq_touch BEFORE UPDATE ON rfq
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE rfq ENABLE ROW LEVEL SECURITY;
ALTER TABLE rfq FORCE  ROW LEVEL SECURITY;
CREATE POLICY rfq_isolation ON rfq USING (tenant_id = current_tenant_id());

-- What was asked for. Every supplier quotes against these same lines, which is
-- what makes the comparison in B5.1 a comparison rather than a pile of PDFs.
CREATE TABLE rfq_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  rfq_id      uuid NOT NULL REFERENCES rfq(id) ON DELETE CASCADE,
  line_no     integer NOT NULL,

  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  description text NOT NULL,
  qty         numeric(18,4) NOT NULL,

  CONSTRAINT rfq_line_qty_positive CHECK (qty > 0)
);

CREATE UNIQUE INDEX rfq_line_no_uq ON rfq_line (rfq_id, line_no);
CREATE INDEX rfq_line_tenant_idx ON rfq_line (tenant_id);
CREATE INDEX rfq_line_variant_idx ON rfq_line (variant_id);

ALTER TABLE rfq_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE rfq_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY rfq_line_isolation ON rfq_line
  USING (tenant_id = current_tenant_id());

-- Who was asked. Separate from the quote, because "asked and did not reply" is
-- information a buyer needs and a missing quote row cannot carry.
CREATE TABLE rfq_invitation (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  rfq_id      uuid NOT NULL REFERENCES rfq(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES supplier(id) ON DELETE RESTRICT,

  invited_at  timestamptz NOT NULL DEFAULT now(),
  declined_at timestamptz,
  decline_reason text
);

CREATE UNIQUE INDEX rfq_invitation_uq ON rfq_invitation (rfq_id, supplier_id);
CREATE INDEX rfq_invitation_supplier_idx ON rfq_invitation (supplier_id);
CREATE INDEX rfq_invitation_tenant_idx ON rfq_invitation (tenant_id);

ALTER TABLE rfq_invitation ENABLE ROW LEVEL SECURITY;
ALTER TABLE rfq_invitation FORCE  ROW LEVEL SECURITY;
CREATE POLICY rfq_invitation_isolation ON rfq_invitation
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Supplier quotes
-- ---------------------------------------------------------------------------
--
-- B5.1: "quote versioning if a supplier revises". A revision is a NEW row with
-- revision + 1 pointing at the one it supersedes, never an edit — a supplier
-- who drops their price after seeing a competitor's is exactly the history an
-- audit wants to see, and an UPDATE would erase it.

CREATE TABLE supplier_quote (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  rfq_id      uuid NOT NULL REFERENCES rfq(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES supplier(id) ON DELETE RESTRICT,

  quote_number text,
  revision    integer NOT NULL DEFAULT 1,
  supersedes_id uuid REFERENCES supplier_quote(id) ON DELETE SET NULL,

  -- received  — in, and comparable
  -- superseded — a later revision replaced it
  -- awarded   — this one won
  -- rejected  — this one lost
  status      text NOT NULL DEFAULT 'received',

  received_on date NOT NULL DEFAULT current_date,
  -- B5.1: "quote validity dates, automatic expiry". Expiry is derived by
  -- comparing this to the date, not by a job flipping a status: a quote does
  -- not become a different document at midnight, it just stops being usable.
  valid_until date,

  currency    text NOT NULL,
  fx_rate     numeric(18,8) NOT NULL DEFAULT 1,

  -- The comparison axes B5.1 names beyond price.
  lead_time_days integer,
  payment_terms_days integer,
  quality_note text,

  subtotal_net    numeric(18,4) NOT NULL DEFAULT 0,
  tax_total       numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive numeric(18,4) NOT NULL DEFAULT 0,

  notes       text,
  recorded_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT supplier_quote_status_valid CHECK (status IN (
    'received', 'superseded', 'awarded', 'rejected')),
  CONSTRAINT supplier_quote_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT supplier_quote_fx_positive CHECK (fx_rate > 0),
  CONSTRAINT supplier_quote_revision_positive CHECK (revision > 0),
  CONSTRAINT supplier_quote_totals_sane CHECK (
    subtotal_net >= 0 AND tax_total >= 0 AND total_inclusive >= 0),
  CONSTRAINT supplier_quote_lead_time_sane CHECK (
    lead_time_days IS NULL OR lead_time_days >= 0),
  CONSTRAINT supplier_quote_terms_sane CHECK (
    payment_terms_days IS NULL OR payment_terms_days >= 0)
);

-- One live quote per supplier per RFQ. A revision supersedes its predecessor,
-- so the predecessor leaves 'received' as the replacement arrives and the
-- comparison screen never shows one supplier twice.
CREATE UNIQUE INDEX supplier_quote_live_uq
  ON supplier_quote (rfq_id, supplier_id)
  WHERE status IN ('received', 'awarded');
CREATE INDEX supplier_quote_rfq_idx ON supplier_quote (rfq_id, total_inclusive);
-- B5.1's "historical quote archive per supplier".
CREATE INDEX supplier_quote_supplier_idx
  ON supplier_quote (supplier_id, received_on DESC);
CREATE INDEX supplier_quote_tenant_idx ON supplier_quote (tenant_id);

CREATE TRIGGER supplier_quote_touch BEFORE UPDATE ON supplier_quote
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE supplier_quote ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_quote FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_quote_isolation ON supplier_quote
  USING (tenant_id = current_tenant_id());

CREATE TABLE supplier_quote_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  quote_id    uuid NOT NULL REFERENCES supplier_quote(id) ON DELETE CASCADE,

  -- Which RFQ line this answers. Required: a quote line that answers nothing
  -- cannot be compared, which is the only reason this table exists.
  rfq_line_id uuid NOT NULL REFERENCES rfq_line(id) ON DELETE CASCADE,
  line_no     integer NOT NULL,

  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  description text NOT NULL,

  -- A supplier may offer a different quantity than was asked — a case size, a
  -- minimum order — so this is recorded rather than copied from the RFQ line.
  qty         numeric(18,4) NOT NULL,
  unit_cost   numeric(18,4) NOT NULL,

  tax_treatment text NOT NULL DEFAULT 'standard',
  tax_rate      numeric(9,6) NOT NULL DEFAULT 0,

  net_amount    numeric(18,4) NOT NULL,
  tax_amount    numeric(18,4) NOT NULL,
  gross_amount  numeric(18,4) NOT NULL,

  note        text,

  CONSTRAINT supplier_quote_line_qty_positive CHECK (qty > 0),
  CONSTRAINT supplier_quote_line_cost_positive CHECK (unit_cost >= 0),
  CONSTRAINT supplier_quote_line_amounts_sane CHECK (
    net_amount >= 0 AND tax_amount >= 0 AND gross_amount >= 0)
);

CREATE UNIQUE INDEX supplier_quote_line_no_uq
  ON supplier_quote_line (quote_id, line_no);
CREATE UNIQUE INDEX supplier_quote_line_answers_once
  ON supplier_quote_line (quote_id, rfq_line_id);
CREATE INDEX supplier_quote_line_tenant_idx ON supplier_quote_line (tenant_id);
CREATE INDEX supplier_quote_line_variant_idx
  ON supplier_quote_line (variant_id);

ALTER TABLE supplier_quote_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_quote_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_quote_line_isolation ON supplier_quote_line
  USING (tenant_id = current_tenant_id());

-- The award points at a quote, and the quote points at the RFQ. Added after
-- supplier_quote exists because the two reference each other.
ALTER TABLE rfq ADD CONSTRAINT rfq_awarded_quote_fk
  FOREIGN KEY (awarded_quote_id) REFERENCES supplier_quote(id)
  ON DELETE RESTRICT;

-- Which order came out of which quote. B5.1: "Purchase Order generated
-- automatically from the winning quote" — and afterwards a buyer looking at an
-- order needs to get back to the quotes it beat.
ALTER TABLE purchase_order
  ADD COLUMN rfq_id uuid REFERENCES rfq(id) ON DELETE SET NULL,
  ADD COLUMN quote_id uuid REFERENCES supplier_quote(id) ON DELETE SET NULL,
  ADD COLUMN requisition_id uuid REFERENCES purchase_requisition(id)
    ON DELETE SET NULL;

CREATE INDEX po_rfq_idx ON purchase_order (rfq_id) WHERE rfq_id IS NOT NULL;
CREATE INDEX po_requisition_idx ON purchase_order (requisition_id)
  WHERE requisition_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Document numbering
-- ---------------------------------------------------------------------------
--
-- Two more counters on the company, and two more branches in the function that
-- claims from them. Extending the existing function rather than writing a
-- second one keeps a single definition of what "claim the next number" means:
-- the UPDATE ... RETURNING is atomic, so two buyers raising a requisition at
-- the same instant get different numbers without either of them waiting on a
-- lock the other holds.

ALTER TABLE company
  ADD COLUMN IF NOT EXISTS next_requisition_no bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS next_rfq_no         bigint NOT NULL DEFAULT 1;

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
  ELSIF p_kind = 'requisition' THEN
    UPDATE company SET next_requisition_no = next_requisition_no + 1
    WHERE id = p_company_id RETURNING next_requisition_no - 1 INTO v_next;
  ELSIF p_kind = 'rfq' THEN
    UPDATE company SET next_rfq_no = next_rfq_no + 1
    WHERE id = p_company_id RETURNING next_rfq_no - 1 INTO v_next;
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
-- Permissions
-- ---------------------------------------------------------------------------
--
-- Four, not one, and the split is the control B5 asks for. Anybody trusted with
-- stock may ASK for it (`purchasing.request`) — B5 says "any authorized staff".
-- Approving somebody else's request is a manager's act. Running an RFQ is the
-- buyer's job. Awarding it commits the business to a supplier and is separated
-- again, because "who chose this supplier, and why" is the question B5.1 exists
-- to answer and the answer is worth less if the buyer marks their own homework.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'purchasing.request'),
  ('owner',            'purchasing.approve_request'),
  ('owner',            'purchasing.manage_rfq'),
  ('owner',            'purchasing.award_rfq'),
  ('store_manager',    'purchasing.request'),
  ('store_manager',    'purchasing.approve_request'),
  ('purchase_manager', 'purchasing.request'),
  ('purchase_manager', 'purchasing.manage_rfq'),
  ('inventory_keeper', 'purchasing.request'),
  ('accountant',       'purchasing.approve_request'),
  ('auditor',          'purchasing.request')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- The auditor is read-only everywhere else, so the row above would hand them a
-- write. Remove it: an auditor may VIEW the requisitions through
-- purchasing.view, which they already hold.
DELETE FROM role_permission rp
USING role r
WHERE rp.role_id = r.id AND r.tenant_id IS NULL AND r.key = 'auditor'
  AND rp.permission = 'purchasing.request';

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
      ('owner',            'purchasing.request'),
      ('owner',            'purchasing.approve_request'),
      ('owner',            'purchasing.manage_rfq'),
      ('owner',            'purchasing.award_rfq'),
      ('store_manager',    'purchasing.request'),
      ('store_manager',    'purchasing.approve_request'),
      ('purchase_manager', 'purchasing.request'),
      ('purchase_manager', 'purchasing.manage_rfq'),
      ('inventory_keeper', 'purchasing.request'),
      ('accountant',       'purchasing.approve_request')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
