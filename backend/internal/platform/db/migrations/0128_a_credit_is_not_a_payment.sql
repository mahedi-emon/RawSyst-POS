-- What a supplier owes back is not money the shop paid.
--
-- 0127 reduced a bill's balance by writing the claim into `amount_paid`,
-- because that is the column payables subtracts from. It works arithmetically
-- and it is wrong twice.
--
-- It LIES. `amount_paid` is read by the supplier portal, by the ageing report
-- and by every screen that shows what has been settled, and all of them would
-- have told a supplier they had been paid for goods they had taken back.
--
-- And it BREAKS. A bill that was paid and then partly returned ends with
-- `amount_paid` above `total_inclusive`, so `total_inclusive - amount_paid`
-- goes negative and the payment screen offers a bill with a negative balance.
-- Found by running the verification suite after 0127: "A payment of nothing is
-- not a payment", raised on a bill whose outstanding had gone below zero.
--
-- So a credit gets a column of its own, and what is owed is what was billed
-- less what was paid less what was credited — floored at zero, because a
-- supplier who has been paid and then handed goods back owes the shop money,
-- and that is a debit balance on the supplier rather than a negative payable
-- on one invoice. The journal already carries it; this column only says what
-- the bill itself still holds.

ALTER TABLE purchase_bill
  ADD COLUMN IF NOT EXISTS amount_credited numeric(18,4) NOT NULL DEFAULT 0;

ALTER TABLE purchase_bill
  ADD CONSTRAINT bill_credited_positive CHECK (amount_credited >= 0);

-- Move whatever 0127's service put in the wrong column. Computed from the
-- returns themselves rather than assumed, so a bill that was genuinely paid
-- keeps its payment and only the credited part moves.
UPDATE purchase_bill b
SET amount_paid     = greatest(b.amount_paid - r.claimed, 0),
    amount_credited = r.claimed
FROM (
  SELECT bill_id, sum(total_inclusive) AS claimed
  FROM purchase_return
  GROUP BY bill_id
) AS r
WHERE r.bill_id = b.id;

-- The status follows the same arithmetic. A bill settled only because it was
-- credited is still settled; one where the credit was undone by a later
-- payment reversal is owed again.
UPDATE purchase_bill
SET status = 'approved'
WHERE status = 'paid'
  AND amount_paid + amount_credited < total_inclusive;
