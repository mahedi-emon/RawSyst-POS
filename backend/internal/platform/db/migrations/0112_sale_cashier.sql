-- 0112 — Who made the sale.
--
-- A sale has never recorded the person who rang it up. `sales_invoice` names
-- the tenant, the company, the store, the device, the cash session and the
-- customer, and not one column says which human was standing at the till.
--
-- # What this broke
--
-- `people.commissionFor` (C6) has always tried to attribute a month's takings
-- to an employee with:
--
--     JOIN employee e ON e.user_id = i.created_by
--
-- and `sales_invoice.created_by` does not exist. The query therefore errors,
-- and payroll returns a 500 for any employee marked `commission_eligible`.
-- Commission has never worked -- not "computed the wrong figure", but failed
-- outright. It went unnoticed because every payroll test hired staff who were
-- not commission-eligible, so the function was never reached.
--
-- E-reporting's "Employee-wise" sales report and the cashier dashboard's
-- "today's own sales" need exactly the same column and had nothing to read
-- either.
--
-- # Why cashier_id, and why not a separate salesperson
--
-- The Blueprint has no salesperson distinct from the operator: A6 names the
-- role "Cashier / POS Operator", the cashier's dashboard shows "today's own
-- sales", and C6 measures commission per employee. The person who rang the sale
-- up is the person it is attributed to. Inventing a second "salesperson"
-- concept would be inventing a business rule.
--
-- `sales.Sale.CashierID` is already populated from the authenticated user on
-- every POS path and already used for the journal's `posted_by`; it simply had
-- nowhere to land on the invoice itself.
--
-- # Nullable, and not backfilled
--
-- Invoices written before this column existed have no attribution and cannot
-- honestly be given one -- the information was never captured. They stay null,
-- and commission for a period containing them is short rather than invented.
-- ON DELETE SET NULL for the same reason a journal keeps its entry when an
-- account's owner leaves: the sale is history, the person is a reference.

ALTER TABLE sales_invoice
  ADD COLUMN cashier_id uuid REFERENCES app_user(id) ON DELETE SET NULL;

COMMENT ON COLUMN sales_invoice.cashier_id IS
  'The signed-in user who rang this sale up, for commission (C6), '
  'employee-wise sales reporting and the cashier''s own-sales view. Null on '
  'invoices written before 0112, which were never attributed.';

-- The commission query's shape: one employee's documents in one company over
-- one month. Partial because historical rows are null and never selected.
CREATE INDEX sales_invoice_cashier_idx
  ON sales_invoice (company_id, cashier_id, issued_at)
  WHERE cashier_id IS NOT NULL;
