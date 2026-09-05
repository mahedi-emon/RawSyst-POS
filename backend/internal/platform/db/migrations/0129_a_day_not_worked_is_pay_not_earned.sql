-- A payroll run with an absence on it could not be approved.
--
-- `payroll.accrue` version 1 debits the gross and credits social insurance,
-- advance recovery and the net. That set balances only while the payslip has
-- no OTHER deduction on it — and it has two. `absence_deduction` and
-- `other_deduction` are both part of `deduction_total`, and both are subtracted
-- to reach the net the rule credits, so the entry is short by exactly their sum:
--
--     gross  =  net + gosi_employee + advance_recovery
--                   + absence_deduction + other_deduction
--
-- Mark one person absent for one day and approving the month answers 500 —
-- "This entry does not balance: debits total 16532.26 against credits of
-- 16370.97, a difference of 161.29" — where 161.29 is that person's absence.
-- The wage cannot be booked, the run cannot be paid, and the month is stuck
-- with no way forward but deleting the attendance that was correctly recorded.
--
-- Found by running an actual month against the server. Nothing caught it
-- because every existing test pays people who were never away.
--
-- # An absence is a credit to the wage expense, not a liability
--
-- The two missing figures are not the same kind of thing and must not post to
-- the same place.
--
-- A day not worked is pay that was never EARNED. Nobody is owed it and nobody
-- is holding it, so it belongs against the expense: the business's wage cost
-- for the month is what the staff earned, not what they would have earned had
-- everybody come in. It posts as a credit to Salaries — the same account the
-- gross debits — which leaves the net cost right AND leaves both figures
-- visible, so the payroll register still ties to the ledger line for line.
-- Debiting a smaller number instead would balance just as well and lose the
-- absence entirely.
--
-- `other_deduction` is the opposite: money the employee EARNED that the
-- business is keeping back — a staff purchase, a fine, a loan that is not an
-- advance. The business holds it and owes it onward, so it is a liability, and
-- 2630 is created for it. Nothing populates that field yet. It gets a rule line
-- anyway, because the day something does, the failure would be this same
-- unbalanced entry all over again.
--
-- # Superseded, not edited
--
-- 0015 forbids editing a posting rule: an entry posted last March must stay
-- explainable by the rule that produced it. Version 2 wins from today by
-- `ORDER BY version DESC`; version 1 stays on record for the runs it posted.

-- ---------------------------------------------------------------------------
-- The account that holds what is withheld
-- ---------------------------------------------------------------------------

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, '2630', 'Staff Deductions Payable',
       jsonb_build_object('ar', 'استقطاعات الموظفين'), 'liability'
FROM company c
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, 'staff_deductions', a.id
FROM account a
WHERE a.code = '2630'
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The rule that balances
-- ---------------------------------------------------------------------------

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES
('payroll.accrue', NULL, 2,
 '[{"role": "expense_salaries",  "side": "debit",  "amount": "gross"},
   {"role": "expense_salaries",  "side": "credit", "amount": "absence"},
   {"role": "gosi_payable",      "side": "credit", "amount": "gosi_employee"},
   {"role": "employee_advances", "side": "credit", "amount": "advance_recovery"},
   {"role": "staff_deductions",  "side": "credit", "amount": "other_deduction"},
   {"role": "wages_payable",     "side": "credit", "amount": "net"}]'::jsonb,
 'Wages earned for the period: the whole cost to the business, less the days nobody worked, and what is withheld from the rest.',
 '2020-01-01');
