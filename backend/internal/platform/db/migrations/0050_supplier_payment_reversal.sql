-- 0050 — Putting a supplier payment right without editing it.
--
-- The mirror of 0049, and deliberately the same shape: the original payment
-- stays exactly as it was and a NEW payment reverses it, posted through the same
-- rule (`payment.supplier`) with the sides flipped. Design 02 §111 allows no
-- other correction — "Corrections happen only by posting a reversing entry with
-- reverses_id set. There is no code path — and no database permission — that
-- edits posted history."
--
-- # One thing here is NOT a mirror, and it is the interesting part
--
-- Receivables derives everything: `customer_open_invoices` recomputes what is
-- owed from the tenders and the allocations, so a reversing allocation makes an
-- invoice open again with no stored state to unwind.
--
-- Payables does not. `purchase_bill` STORES `amount_paid`, and paying a bill in
-- full flips its status to 'paid'. So a reversal has to unwind two stored facts,
-- and the second one asks a question receivables never had to answer: rolled
-- back to WHAT status?
--
-- A bill reaches payment as either 'matched' (the three-way match agreed) or
-- 'approved' (it did not, and somebody accepted the discrepancy by name). Those
-- are different facts about how the bill got there, and B5.2's control is
-- worthless if reversing a payment quietly promotes a bill that was only ever
-- approved-under-override into one that matched cleanly — or demotes one that
-- matched into one that looks overridden.
--
-- Guessing would be inventing a business rule. So the allocation RECORDS the
-- status it found, and the reversal puts back exactly that. Recording a fact,
-- not deciding a policy.

ALTER TABLE supplier_payment
  ADD COLUMN reverses_id uuid REFERENCES supplier_payment(id) ON DELETE RESTRICT;

-- One reversal per payment. Partial reversal is not a thing 0049 described and
-- not a thing this describes: a clerk who allocated a payment wrongly reverses
-- the whole document and pays again.
CREATE UNIQUE INDEX supplier_payment_reverses_uq
  ON supplier_payment (reverses_id) WHERE reverses_id IS NOT NULL;

ALTER TABLE supplier_payment
  ADD CONSTRAINT supplier_payment_not_self_reversal
  CHECK (reverses_id IS DISTINCT FROM id);

-- What the bill was before this allocation touched it.
--
-- Nullable because payments made before this migration cannot have it — the
-- fact was never captured and cannot be recovered. The service falls back for
-- those and says so; every payment taken from here on records it.
ALTER TABLE supplier_payment_allocation
  ADD COLUMN bill_status_before text;

COMMENT ON COLUMN supplier_payment_allocation.bill_status_before IS
  'The bill status this allocation found, so a reversal restores it exactly '
  'rather than guessing between matched and approved.';

-- ---------------------------------------------------------------------------
-- The original is frozen
-- ---------------------------------------------------------------------------

-- The same guarantee 0049 gives a receipt. journal_entry_id is written once, on
-- the insert that posts it; everything describing the money that left is frozen.
CREATE TRIGGER supplier_payment_frozen_facts
  BEFORE UPDATE ON supplier_payment
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'tenant_id', 'company_id', 'supplier_id', 'payment_number', 'uuid',
    'paid_on', 'method', 'reference', 'amount', 'currency', 'reverses_id',
    'created_by');

CREATE TRIGGER supplier_payment_no_delete BEFORE DELETE ON supplier_payment
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER supplier_payment_allocation_immutable
  BEFORE UPDATE OR DELETE ON supplier_payment_allocation
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- What a supplier is owed, with reversals netted out
-- ---------------------------------------------------------------------------

-- `supplier_ageing` and `po_outstanding` read `purchase_bill.amount_paid`, which
-- the service unwinds directly, so neither function changes. What DOES change is
-- the payment history a screen shows: a reversal is a payment row like any
-- other, and a list that showed both without saying which was which would read
-- as the supplier having been paid twice.
--
-- Exposed as a view rather than folded into every query, so the one definition
-- of "what did we actually pay this supplier" cannot drift.
CREATE OR REPLACE VIEW supplier_payment_effect AS
  SELECT
    p.id,
    p.tenant_id,
    p.company_id,
    p.supplier_id,
    p.payment_number,
    p.paid_on,
    p.method,
    p.amount,
    p.currency,
    p.reverses_id,
    -- Negative for a reversal: money coming back. Summing this column over a
    -- supplier gives what they were net paid, which is the figure a statement
    -- has to show.
    CASE WHEN p.reverses_id IS NULL THEN p.amount ELSE -p.amount END AS net_amount,
    (p.reverses_id IS NOT NULL) AS is_reversal,
    -- True once something else reverses THIS payment, so a screen can strike it
    -- through rather than offering to reverse it again.
    EXISTS (
      SELECT 1 FROM supplier_payment r WHERE r.reverses_id = p.id
    ) AS is_reversed
  FROM supplier_payment p;

COMMENT ON VIEW supplier_payment_effect IS
  'Supplier payments with reversals signed, so net_amount sums to what was '
  'actually paid. is_reversed says a payment has already been undone.';
