-- 0025 — Posting rules become data, and entries record which version made them.
--
-- The posting_rule table has existed since 0015, versioned and immutable, and
-- nothing has ever read it. Meanwhile sale.revenue, sale.cogs, return.reversal
-- and return.cogs were written as Go literals. That is precisely what C9.2
-- rules out: "each needs its own defined, CONFIGURABLE posting rule." Every new
-- transaction type was a code release, and no tenant could vary its mapping.
--
-- # Reproducibility needs the version
--
-- journal_entry recorded rule_key but not which VERSION of that rule produced
-- it. Rules are versioned and never edited, so an entry posted last March must
-- remain explainable by the rule that made it — and without the version, an
-- auditor asking "why these accounts" can only be shown today's rule, which may
-- not be the one that ran.
--
-- # The line shape
--
-- As documented on posting_rule.lines in 0015:
--
--   {"role": "cash", "side": "debit", "amount": "total_inclusive"}
--
-- `role` is resolved per company through account_role_map, and `amount` NAMES a
-- field the transaction supplies. The rule describes the SHAPE of the entry;
-- the transaction supplies the numbers. Neither knows the other's business.
--
-- One extension was unavoidable. Rule 1 splits the debit across however many
-- tenders the customer used — cash and Mada and a gift card on one sale — and a
-- fixed list of lines cannot express a variable count. So a line may instead
-- say:
--
--   {"for_each": "tenders", "side": "debit"}
--
-- which expands to one line per member of a named group, each member carrying
-- its own role and amount. Blueprint E3.1 requires per-tender cost visibility
-- and specifically that Mada is never merged into "card", so this had to stay
-- expressive rather than collapse to a single "payments" line.

ALTER TABLE journal_entry
  ADD COLUMN rule_version integer;

COMMENT ON COLUMN journal_entry.rule_version IS
  'Which version of rule_key produced this entry. Rules are versioned and never '
  'edited, so an entry stays explainable by the rule that actually made it.';

-- ---------------------------------------------------------------------------
-- The rules
-- ---------------------------------------------------------------------------
--
-- Country NULL: these shapes hold anywhere. What differs between markets is
-- which ACCOUNTS the roles map to and whether input tax is recoverable, and
-- both of those already live elsewhere — the role map and the regulatory
-- registry. Duplicating a rule per country would mean three copies drifting
-- apart for no gain.

INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES

-- Rule 1 — Retail sale. The debit splits across however many tenders were
-- used; Mada, card, BNPL and store credit each land in their own account
-- because E3.1 requires per-tender cost visibility.
('sale.revenue', NULL, 1,
 '[{"for_each": "tenders", "side": "debit"},
   {"role": "sales_revenue", "side": "credit", "amount": "subtotal_net"},
   {"role": "output_vat",    "side": "credit", "amount": "tax_total"}]'::jsonb,
 'Retail sale: what the customer paid, against revenue and the tax owed on it.',
 '2020-01-01'),

-- Rule 2 — Cost of sale, posted WITH the sale so gross profit is live rather
-- than reconstructed at month end (C13).
('sale.cogs', NULL, 1,
 '[{"role": "cogs",      "side": "debit",  "amount": "cogs_total"},
   {"role": "inventory", "side": "credit", "amount": "cogs_total"}]'::jsonb,
 'Cost of goods sold, posted simultaneously with the sale.',
 '2020-01-01'),

-- Rule 4 — Customer return. Reverses rules 1 and 2; loyalty and commission are
-- Phase 2 and are reported as outstanding rather than silently skipped.
('return.reversal', NULL, 1,
 '[{"role": "sales_revenue", "side": "debit",  "amount": "subtotal_net"},
   {"role": "output_vat",    "side": "debit",  "amount": "tax_total"},
   {"for_each": "refunds",   "side": "credit"}]'::jsonb,
 'Customer return: revenue and output tax reversed, refund paid out.',
 '2020-01-01'),

('return.cogs', NULL, 1,
 '[{"role": "inventory", "side": "debit",  "amount": "cogs_total"},
   {"role": "cogs",      "side": "credit", "amount": "cogs_total"}]'::jsonb,
 'Cost of goods returned to stock.',
 '2020-01-01'),

-- Rule 3 — Purchase on credit. Input tax is separated from the inventory value
-- because E2.5 puts duty in inventory cost and recoverable tax in a receivable;
-- merging them overstates stock while understating the reclaim.
('purchase.credit', NULL, 1,
 '[{"role": "inventory",     "side": "debit",  "amount": "net_amount"},
   {"role": "input_vat",     "side": "debit",  "amount": "tax_amount"},
   {"role": "accounts_payable", "side": "credit", "amount": "total_inclusive"}]'::jsonb,
 'Purchase on credit: stock at landed cost, recoverable tax separated.',
 '2020-01-01'),

-- Rule 5 — Expense paid immediately. Where input tax is NOT recoverable it is
-- absorbed into the expense rather than claimed (E2.3 restricts recovery on
-- entertainment, some vehicles and fuel), so the caller supplies a tax figure
-- of zero and the line drops out.
('expense.cash', NULL, 1,
 '[{"role": "expense",   "side": "debit",  "amount": "net_amount"},
   {"role": "input_vat", "side": "debit",  "amount": "tax_amount"},
   {"for_each": "payments", "side": "credit"}]'::jsonb,
 'Expense paid from cash or bank, with input tax claimed only where recoverable.',
 '2020-01-01'),

-- Rule 7 — Supplier paid.
('payment.supplier', NULL, 1,
 '[{"role": "accounts_payable", "side": "debit", "amount": "amount"},
   {"for_each": "payments",     "side": "credit"}]'::jsonb,
 'Payment to a supplier, settling accounts payable.',
 '2020-01-01'),

-- Rule 8 — Customer paid on account.
('payment.customer', NULL, 1,
 '[{"for_each": "payments", "side": "debit"},
   {"role": "accounts_receivable", "side": "credit", "amount": "amount"}]'::jsonb,
 'Payment from a customer against an outstanding invoice.',
 '2020-01-01'),

-- Rule 9 — Moving money between the shop's own accounts. Both sides are named
-- by the transaction, because "which bank to which till" is not a property of
-- the rule.
('transfer.internal', NULL, 1,
 '[{"for_each": "destination", "side": "debit"},
   {"for_each": "source",      "side": "credit"}]'::jsonb,
 'Transfer between the company own cash and bank accounts.',
 '2020-01-01'),

-- Rule 10 — Stock written off. B4 requires a reason on every wastage, which the
-- movement carries; this is only its accounting effect.
('inventory.writeoff', NULL, 1,
 '[{"role": "stock_writeoff", "side": "debit",  "amount": "value"},
   {"role": "inventory",      "side": "credit", "amount": "value"}]'::jsonb,
 'Stock written off through wastage, damage or a count adjustment.',
 '2020-01-01'),

-- Rule 11 — Standard-costing variance, so an unexpected purchase price is
-- visible rather than absorbed into margin.
('inventory.variance', NULL, 1,
 '[{"role": "cost_variance", "side": "debit",  "amount": "variance"},
   {"role": "inventory",     "side": "credit", "amount": "variance"}]'::jsonb,
 'Difference between standard cost and what stock actually cost.',
 '2020-01-01'),

-- Rule 12 — Owner or investor capital in.
('equity.contribution', NULL, 1,
 '[{"for_each": "destination", "side": "debit"},
   {"role": "owner_capital",   "side": "credit", "amount": "amount"}]'::jsonb,
 'Capital introduced by an owner or investor.',
 '2020-01-01');
