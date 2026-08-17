-- Goods received not invoiced, and landed cost.
--
-- # The invariant this exists to restore
--
-- Design 02 §6.6, quoting C13: "Inventory valuation report must always tie
-- exactly to the Inventory account balance in the General Ledger — any
-- divergence is flagged as an exception."
--
-- B5 says only a goods receipt increases stock, and Rule 3 debits Inventory on
-- the BILL. Both are in the documents, and together they leave the valuation
-- ahead of the ledger for the whole window between a delivery arriving and the
-- supplier invoicing it — measured at the full value of the receipt. The
-- documents never described a receipt-time entry, so the accrual account and
-- its reversal timing were an accounting decision rather than an implementation
-- detail, and were taken deliberately:
--
--   ON RECEIPT   Dr Inventory      / Cr GRNI accrual
--   ON THE BILL  Dr GRNI accrual
--                Dr Input VAT      / Cr Accounts Payable
--
-- So the tie-out holds continuously, and GRNI carries a real reportable figure:
-- what the shop has on its shelves and has not yet been invoiced for. A
-- non-zero balance at period end is meaningful rather than a reconciliation
-- artefact, which is why the accrual clears against the bill rather than at
-- close.
--
-- # Landed cost
--
-- Defined already, in 10-catalog-and-inventory.md: allocation basis
-- configurable across value, quantity and weight, and — E2.5 — import VAT
-- EXCLUDED from the allocation and routed to Input VAT while duty goes into
-- inventory cost. Mixing them overstates stock and understates the reclaim.
--
-- The default basis is by value. Quantity is wrong the moment a carton of
-- scarves and a carton of gold share a container, and weight would need a
-- variant.weight column that does not exist.

-- ---------------------------------------------------------------------------
-- The accrual account
-- ---------------------------------------------------------------------------

-- Added to every company that already has a chart, so an existing tenant does
-- not have to be touched by hand before its next delivery. Code 2150 sits with
-- the other payables-adjacent liabilities.
INSERT INTO account (tenant_id, company_id, code, name, type)
SELECT c.tenant_id, c.id, '2150', 'Goods Received Not Invoiced', 'liability'
FROM company c
WHERE EXISTS (SELECT 1 FROM account_role_map m WHERE m.company_id = c.id)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, 'grni', a.id
FROM account a
WHERE a.code = '2150'
  AND EXISTS (SELECT 1 FROM account_role_map m WHERE m.company_id = a.company_id)
ON CONFLICT (company_id, role) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Landed cost on the receipt
-- ---------------------------------------------------------------------------

-- Captured on the GOODS RECEIPT rather than the order, because freight and duty
-- are known when the goods land and are frequently not known when the order is
-- placed. An order that guessed them would put a wrong cost into the layers.
ALTER TABLE goods_receipt
  -- Everything that belongs in the cost of the stock: freight, duty, handling,
  -- insurance. Allocated across the lines.
  ADD COLUMN landed_cost numeric(18,4) NOT NULL DEFAULT 0,
  -- Import VAT, which does NOT belong in the cost of the stock. E2.5 is
  -- explicit: duty is inventory cost, import VAT is recoverable. Kept as its
  -- own column so the two can never be added together by accident.
  ADD COLUMN import_vat  numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN landed_cost_basis text NOT NULL DEFAULT 'value';

ALTER TABLE goods_receipt
  ADD CONSTRAINT grn_landed_cost_sane CHECK (
    landed_cost >= 0 AND import_vat >= 0),
  ADD CONSTRAINT grn_landed_basis_valid CHECK (
    landed_cost_basis IN ('value', 'quantity'));

-- What each line absorbed, kept rather than recomputed.
--
-- A later amendment to the receipt's freight must not retrospectively change
-- what a cost layer was worth, and an auditor asking why a layer cost 103.33
-- rather than 100 needs the answer to be a stored number rather than a
-- reconstruction.
ALTER TABLE grn_line
  ADD COLUMN landed_cost_alloc numeric(18,4) NOT NULL DEFAULT 0;

ALTER TABLE grn_line
  ADD CONSTRAINT grn_line_landed_alloc_sane CHECK (landed_cost_alloc >= 0);

-- ---------------------------------------------------------------------------
-- The two posting rules
-- ---------------------------------------------------------------------------

-- Rules are data, per 0025. Two new keys rather than a new version of
-- purchase.credit, because a bill against a receipt and a bill with no receipt
-- behind it are genuinely different transactions — rent and a utility invoice
-- have no accrual to clear and must keep posting straight to their expense or
-- inventory account.
INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
  -- Goods arrive. Stock is real and its value is real; what is not yet real is
  -- the invoice, so the other side is an accrual rather than a payable.
  ('purchase.accrual', NULL, 1,
   '[{"role": "inventory", "side": "debit",  "amount": "net_amount"},
     {"role": "grni",      "side": "credit", "amount": "net_amount"}]'::jsonb,
   'Goods received: stock at landed cost, accrued against the supplier''s invoice.',
   '2020-01-01'),

  -- The invoice turns up. The accrual is discharged and the payable created.
  -- Input tax appears HERE and not on the receipt, because a supplier who has
  -- not invoiced has not charged tax and there is nothing to reclaim yet.
  ('purchase.clear_accrual', NULL, 1,
   '[{"role": "grni",             "side": "debit",  "amount": "accrued_amount"},
     {"role": "input_vat",        "side": "debit",  "amount": "tax_amount"},
     {"role": "inventory",        "side": "debit",  "amount": "unaccrued_amount"},
     {"role": "accounts_payable", "side": "credit", "amount": "total_inclusive"}]'::jsonb,
   'Supplier bill against a receipt: clears the accrual, raises the payable.',
   '2020-01-01')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- What is accrued and not yet billed
-- ---------------------------------------------------------------------------

-- Per receipt line: what went into stock, and how much of it a bill has since
-- claimed. The difference is what the accrual still holds.
--
-- Billed value is apportioned by QUANTITY rather than taken from the bill's own
-- amounts. A supplier billing a different price is a discrepancy the three-way
-- match reports; it must not silently change how much accrual is discharged, or
-- the accrual would never clear to zero on exactly the receipt it belongs to.
CREATE OR REPLACE FUNCTION grn_accrual_outstanding(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(
    (gl.qty_received - gl.qty_rejected) * gl.unit_cost
    - least(coalesce(b.billed_qty, 0), gl.qty_received - gl.qty_rejected) * gl.unit_cost
  ), 0)
  FROM grn_line gl
  JOIN goods_receipt g ON g.id = gl.grn_id
  LEFT JOIN (
    SELECT bl.po_line_id, sum(bl.qty_billed) AS billed_qty
    FROM bill_line bl
    JOIN purchase_bill pb ON pb.id = bl.bill_id
    WHERE bl.po_line_id IS NOT NULL AND pb.status <> 'cancelled'
    GROUP BY bl.po_line_id
  ) b ON b.po_line_id = gl.po_line_id
  WHERE g.company_id = p_company_id;
$$;
