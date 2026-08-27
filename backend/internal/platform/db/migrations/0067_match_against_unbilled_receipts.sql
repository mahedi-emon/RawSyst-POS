-- 0067 — The three-way match compared a bill against everything that arrived,
-- including what an earlier bill had already been paid for.
--
-- # What was wrong
--
-- B5.2's quantity dimension compared the billed quantity against
-- po_outstanding.qty_received: the TOTAL that ever arrived on the order line.
-- The same function has always reported qty_billed beside it, and the match
-- never read it.
--
-- So the control had a hole shaped exactly like the fraud it exists to catch.
-- A hundred cartons arrive and are invoiced. The supplier sends a second
-- invoice for the same hundred under a different number, which the unique key
-- on (supplier_id, supplier_ref) does not fire on because the number is
-- genuinely different. The match reads "billed 100 against 100 received" — the
-- same comparison it made for the first invoice, because nothing told it the
-- first invoice existed — records a pass on every dimension, posts the bill,
-- and the shop pays twice for one delivery.
--
-- The accrual went with it. postBill discharges Goods Received Not Invoiced by
-- the quantity billed capped at the quantity received, so the second invoice
-- discharged an accrual that had only ever been raised once. GRNI is a
-- liability; taking it through zero leaves the balance sheet asserting that the
-- shop's own stockroom owes it money.
--
-- # The comparison now
--
--   outstanding = received − billed on EARLIER, non-cancelled bills
--   variance    = billed on this bill − outstanding
--
-- which is the comparison that was there before whenever nothing has been
-- billed yet, so a supplier shipping 90 of 100 and billing for 100 is caught
-- exactly as it was.
--
-- # What this migration is for
--
-- The Go change is the fix. This column is the evidence. three_way_match is
-- what an auditor reads to see what the control checked and what it saw, and a
-- row that records "received 100, billed 100, outcome breach" without the
-- figure that made it a breach reads as a malfunction rather than as a finding.

ALTER TABLE three_way_match
  ADD COLUMN previously_billed numeric(18,4);

COMMENT ON COLUMN three_way_match.previously_billed IS
  'Quantity of this order line already claimed by earlier, non-cancelled '
  'bills. NULL when there were none. The quantity dimension compares the '
  'billed quantity against received minus this, not against received.';
