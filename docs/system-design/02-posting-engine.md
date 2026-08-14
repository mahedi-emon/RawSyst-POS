# 02 — Double-Entry Posting Engine

> **PILLAR 2 of 3.** Without a real double-entry engine, this software is *"a sales register with reports, not an ERP"* (blueprint C9).

**Binding source:** Blueprint C1, C9–C14.
**Acceptance gate (QA M1):** Trial balance always balances · Balance Sheet balances · **sub-ledgers tie to control accounts** · **closed periods truly locked**.

---

## 1. Design stance

Three commitments drive everything below.

1. **Posting rules are data, not code.** Blueprint C9.2 lists rules 6–12 as each needing *"its own **defined, configurable** posting rule."* Hard-coding twelve posting functions means every new transaction type is a code release, and no tenant can ever vary a chart of accounts mapping. Rules live in a table and are evaluated by an engine.
2. **Journal entries are created synchronously, inside the originating transaction.** A sale and its journal entry commit together or not at all. An eventually-consistent posting pipeline would allow a sale to exist without its accounting entry — the trial balance would drift and QA gate M1 would fail intermittently, which is worse than failing consistently.
3. **Immutability is enforced by the database.** Application-level "don't edit this" is a convention. A trigger is a guarantee.

---

## 2. Core schema

### 2.1 Chart of accounts

```sql
CREATE TABLE account (
  id             UUID PRIMARY KEY,
  tenant_id      UUID NOT NULL,
  company_id     UUID NOT NULL,              -- books are per COMPANY, not per group
  code           TEXT NOT NULL,              -- '1100'
  name           TEXT NOT NULL,
  name_ar        TEXT,                       -- Law of Commercial Books: Arabic records
  type           account_type NOT NULL,      -- ASSET|LIABILITY|EQUITY|REVENUE|EXPENSE
  parent_id      UUID REFERENCES account(id),-- multi-level tree
  is_control     BOOLEAN NOT NULL DEFAULT false,
  control_of     TEXT,                       -- 'AR' | 'AP' | 'INVENTORY'
  currency       CHAR(3),                    -- NULL = base currency
  is_postable    BOOLEAN NOT NULL DEFAULT true,  -- parents are not postable
  is_system      BOOLEAN NOT NULL DEFAULT false, -- cannot be deleted
  UNIQUE (company_id, code)
);
```

Default tree seeded per blueprint C1: Assets (Cash, Bank, Inventory, AR, Fixed Assets) · Liabilities (AP, Loans, Employee Payables, Tax Payables) · Equity (Owner Capital, Investor Capital, Retained Earnings) · Revenue (Sales, Other Income) · Expenses (Operating, **COGS**, Salary, Rent, Utilities, Marketing).

### 2.2 Journal

```sql
CREATE TABLE journal_entry (
  id             UUID PRIMARY KEY,
  tenant_id      UUID NOT NULL,
  company_id     UUID NOT NULL,
  period_id      UUID NOT NULL REFERENCES fiscal_period(id),
  entry_no       BIGINT NOT NULL,
  entry_date     DATE   NOT NULL,            -- transaction date, drives registry lookup
  source_type    TEXT   NOT NULL,            -- 'SALE_INVOICE', 'PURCHASE_BILL', …
  source_id      UUID   NOT NULL,
  rule_key       TEXT   NOT NULL,            -- which posting rule produced this
  rule_version   INT    NOT NULL,            -- reproducibility
  description    TEXT,
  reverses_id    UUID REFERENCES journal_entry(id),
  posted_at      TIMESTAMPTZ NOT NULL,
  posted_by      UUID NOT NULL,
  UNIQUE (company_id, entry_no),
  UNIQUE (source_type, source_id, rule_key)  -- idempotency: post once per source
);

CREATE TABLE journal_line (
  id             UUID PRIMARY KEY,
  entry_id       UUID NOT NULL REFERENCES journal_entry(id),
  line_no        INT  NOT NULL,
  account_id     UUID NOT NULL REFERENCES account(id),
  debit          NUMERIC(18,4) NOT NULL DEFAULT 0,
  credit         NUMERIC(18,4) NOT NULL DEFAULT 0,
  currency       CHAR(3) NOT NULL,
  fx_rate        NUMERIC(18,8) NOT NULL DEFAULT 1,
  base_debit     NUMERIC(18,4) NOT NULL,     -- always in company base currency
  base_credit    NUMERIC(18,4) NOT NULL,
  store_id       UUID,                       -- dimension
  subledger_type TEXT,                       -- 'CUSTOMER' | 'SUPPLIER' | 'ITEM'
  subledger_id   UUID,
  CHECK (debit >= 0 AND credit >= 0),
  CHECK ((debit = 0) <> (credit = 0))        -- exactly one side per line
);
```

`UNIQUE (source_type, source_id, rule_key)` is the **idempotency guarantee**. A sync retry that replays the same invoice cannot double-post it — the insert fails and the replay is recognised as already-applied. This is what makes Pillar 3's at-least-once delivery safe for accounting.

### 2.3 Balance enforcement — at the database

```sql
CREATE CONSTRAINT TRIGGER journal_must_balance
  AFTER INSERT OR UPDATE ON journal_line
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_entry_balanced();
```

`assert_entry_balanced()` asserts `SUM(base_debit) = SUM(base_credit)` for the entry. **Deferred** so lines can be inserted one at a time within a transaction, checked at COMMIT. Blueprint C9.1: *"an unbalanced entry can never be saved."* This makes that literally true.

### 2.4 Immutability

```sql
CREATE TRIGGER journal_entry_immutable
  BEFORE UPDATE OR DELETE ON journal_entry
  FOR EACH ROW EXECUTE FUNCTION reject_always();

CREATE TRIGGER journal_line_immutable
  BEFORE UPDATE OR DELETE ON journal_line
  FOR EACH ROW EXECUTE FUNCTION reject_always();
```

Corrections happen **only** by posting a reversing entry with `reverses_id` set. There is no code path — and no database permission — that edits posted history. Owner and Super Admin included.

---

## 3. Posting rules as data

### 3.1 Rule schema

```sql
CREATE TABLE posting_rule (
  id             UUID PRIMARY KEY,
  tenant_id      UUID,                       -- NULL = platform default
  company_id     UUID,                       -- NULL = all companies
  rule_key       TEXT NOT NULL,              -- 'SALE_INVOICE_CASH'
  version        INT  NOT NULL,
  source_type    TEXT NOT NULL,
  condition      JSONB,                      -- optional guard expression
  lines          JSONB NOT NULL,             -- ordered line templates
  effective_from DATE NOT NULL,
  effective_to   DATE,
  UNIQUE (COALESCE(company_id, '…'), rule_key, version)
);
```

A line template:

```json
{
  "account": { "resolve": "MAPPING", "key": "CASH_ACCOUNT", "fallback": "1100" },
  "side": "DEBIT",
  "amount": { "expr": "total_inclusive" },
  "dimensions": { "store_id": "{{store_id}}" }
}
```

Account resolution goes through a **per-company mapping table**, so a tenant that renumbers its chart of accounts does not need a new rule — only a new mapping.

### 3.2 Evaluation

```
Post(ctx, ruleKey, event):
    period  := ResolvePeriod(event.company, event.date)
    ASSERT period.state == OPEN                    -- else reject
    rule    := ResolveRule(company, ruleKey, event.date)   -- effective-dated
    vars    := Flatten(event)                      -- totals, tax, cogs, tender split
    lines   := []
    FOR tmpl IN rule.lines:
        IF tmpl.condition AND NOT Eval(tmpl.condition, vars): CONTINUE
        acct   := ResolveAccount(company, tmpl.account)
        amount := Eval(tmpl.amount, vars)
        IF amount == 0 AND tmpl.skip_if_zero: CONTINUE
        lines.append(...)
    ASSERT sum(debits) == sum(credits)             -- fail fast, before DB
    INSERT journal_entry + lines                    -- ON CONFLICT DO NOTHING (idempotent)
```

Both the rule **and** the registry values it depends on are resolved **at `event.date`, never at "now."** A VAT report for March uses March's rate even if the rate changed in June (blueprint E8.1).

---

## 4. The twelve posting rules

Rules 1–5 are specified verbatim in C9.2. Rules 6–12 are named there as requiring their own configurable rule; their line structure is designed here.

**Rule 1 — Retail sale, cash** (example: SAR 1,150 = 1,000 goods + 150 VAT)
```
Dr  Cash                          1,150
    Cr  Sales Revenue                     1,000
    Cr  Output VAT Payable                  150
```
Multi-tender splits the debit across Cash / Mada clearing / Card clearing / BNPL clearing / Store Credit / Loyalty liability / AR, one line per tender. **Mada is its own account, never merged into "card"** — blueprint E3.1 requires per-tender cost visibility.

**Rule 2 — COGS, posted simultaneously with every sale**
```
Dr  Cost of Goods Sold              600
    Cr  Inventory                           600
```
Not a month-end job. Gross profit is live.

**Rule 3 — Purchase on credit**
```
Dr  Inventory              (net of recoverable VAT, incl. landed cost)
Dr  Input VAT Receivable
    Cr  Accounts Payable
```

**Rule 4 — Customer return** — reverses Rules 1 and 2, plus loyalty and commission. See §7.

**Rule 5 — Expense paid in cash**
```
Dr  Expense Account
Dr  Input VAT Receivable    (only where recoverable — see below)
    Cr  Cash / Bank
```
Blueprint E2.3: entertainment, some vehicles, and fuel have **restricted input VAT recovery**. Each expense head carries `input_vat_recoverable BOOLEAN`; when false, the VAT is absorbed into the expense line rather than claimed, so the VAT return is not overstated.

**Rule 6 — Salary payment**
```
Dr  Salary Expense
    Cr  GOSI Payable · Employee Advances Recovered · Bank
```

**Rule 7 — Investment injection** — `Dr Cash/Bank / Cr Investor Capital`.
Blueprint C3.2: investment is **kept fully separate from revenue, never mixed with sales income**, so P&L stays clean.

**Rule 8 — Asset purchase** — `Dr Fixed Asset, Dr Input VAT / Cr Cash-Bank-AP`.

**Rule 9 — Depreciation** (monthly straight-line) — `Dr Depreciation Expense / Cr Accumulated Depreciation`.

**Rule 10 — Stock write-off** — `Dr Loss on Inventory Write-off / Cr Inventory`. Requires mandatory reason (blueprint B4).

**Rule 11 — Inter-account transfer** — `Dr Destination / Cr Source`. Covers all four directions in C2, including branch-to-branch cash.

**Rule 12 — Payment settlement** — see §8.

---

## 5. Fiscal periods

```sql
CREATE TABLE fiscal_period (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL,
  fiscal_year  INT  NOT NULL,
  period_no    INT  NOT NULL,                -- 1..12
  starts_on    DATE NOT NULL,
  ends_on      DATE NOT NULL,
  state        period_state NOT NULL,        -- OPEN | CLOSED | LOCKED
  closed_at    TIMESTAMPTZ,
  closed_by    UUID,
  reopen_reason TEXT,
  UNIQUE (company_id, fiscal_year, period_no)
);
```

Fiscal year is calendar **or custom**, for international clients (blueprint C10).

### Enforcement

Every posting resolves the period first and rejects unless `OPEN`. Belt and braces, this is *also* a trigger on `journal_entry`, because QA gate M1 tests period locking by direct manipulation, not only through the API.

`CLOSED` — no new postings; reopenable by Owner-level permission with a **mandatory reason**, permanently audit-logged.
`LOCKED` — year-end complete. Not reopenable through the application at all.

### Year-end routine (C10, six ordered steps)

1. Verify trial balance
2. Post adjusting entries
3. **Close revenue and expense accounts into Retained Earnings**
4. Roll balances forward
5. Generate the closing financial statement pack
6. **Lock the year**

Each step is a checkpoint; the routine cannot skip ahead. Step 3 generates a single closing entry per account group, so the audit trail shows exactly what was closed and when.

---

## 6. Costing and COGS

### 6.1 Methods

Per **C13**, which is authoritative over B4 and D1 (both of which list only FIFO and WAC):

| Method | Notes |
|---|---|
| **Weighted Average Cost** | Running average per item per warehouse |
| **FIFO** | Layer-tracked, consumed oldest-first |
| **Standard Cost** | Fixed cost + **variance tracking** to a variance account |
| **Landed Cost** | An **overlay on any of the above** — purchase + shipping + customs + fees allocated across the receipt by a configurable rule |

Configured per tenant. Changing method mid-life requires a revaluation entry with reason and approval — it is not a silent setting flip.

### 6.2 FIFO layers

```sql
CREATE TABLE cost_layer (
  id            UUID PRIMARY KEY,
  tenant_id     UUID NOT NULL,
  item_id       UUID NOT NULL,               -- variant-level, not parent product
  warehouse_id  UUID NOT NULL,
  received_at   TIMESTAMPTZ NOT NULL,
  qty_received  NUMERIC(18,4) NOT NULL,
  qty_remaining NUMERIC(18,4) NOT NULL,
  unit_cost     NUMERIC(18,6) NOT NULL,      -- includes landed cost allocation
  source_id     UUID NOT NULL                -- GRN line
);
CREATE INDEX ON cost_layer (item_id, warehouse_id, received_at)
  WHERE qty_remaining > 0;
```

Consumption walks layers oldest-first, decrementing `qty_remaining`, emitting one COGS component per layer touched.

### 6.3 The offline costing problem

**The problem:** the terminal sells offline and must decrement stock locally, but the authoritative cost layers live in the cloud. The POS cannot compute exact FIFO cost while disconnected.

**Resolution:** the POS records the sale with a **provisional cost** from its cached snapshot. On sync, the server recomputes COGS against the real layers and posts the authoritative figure. Any difference posts to a **Cost Variance** account, and the divergence is visible in the exception report.

This is a deliberate trade-off. The alternative — blocking offline sales until cost is known — violates the hard offline-first requirement (A2 #3). Provisional-then-reconciled keeps the till running and the books exact, at the cost of a variance line that is itself auditable.

### 6.4 Negative stock

`Company.negative_stock_policy ∈ {BLOCK, ALLOW_WARN}` (C13):

- **BLOCK** — the sale is refused when stock is insufficient.
- **ALLOW_WARN** — the sale proceeds with a warning; cost is provisional and **auto-corrected on the next receipt** of that item.

### 6.5 The tie-out invariant

**C13:** *"Inventory valuation report must always tie exactly to the Inventory account balance in the General Ledger — any divergence is flagged as an exception."*

A nightly job asserts, per company per warehouse:

```
SUM(cost_layer.qty_remaining × unit_cost)  ==  Inventory control account balance
```

Same job asserts the other two control-account ties required by C9.3:

```
SUM(customer open balances)  == Accounts Receivable control balance
SUM(supplier open balances)  == Accounts Payable control balance
```

Any mismatch raises an exception with the drill-down needed to find it. These three assertions **are** QA gate M1's "sub-ledgers tie to control accounts", running continuously rather than once at launch.

---

## 7. Returns — nine effects, one atomic transaction

Blueprint C14: *"A return is never just 'put the item back on the shelf.'"* All nine must happen together:

| # | Effect | Mechanism |
|---|---|---|
| 1 | Reverse inventory — **quantity and value** | Restore cost layer (FIFO returns to its original layer, preserving cost basis) |
| 2 | Reverse revenue | Reversing journal line |
| 3 | Reverse **Output VAT** | At the **original invoice's** VAT rate, from the registry at that date |
| 4 | Reverse **COGS** | At the **original** cost, not today's |
| 5 | Settle refund | Cash / card reversal / store credit / reduce outstanding due |
| 6 | **Reverse or adjust loyalty points** earned on the original sale | Loyalty liability adjusted |
| 7 | **Reverse sales commission** attributed to the original sale | Commission accrual adjusted |
| 8 | Generate linked **Credit Note** referencing the original | ZATCA engine, own ICV (see `01-…` §10) |
| 9 | Write journal entry + audit record | Standard posting path |

All nine inside **one database transaction**. A partial return that reverses stock but not commission is a silent data-integrity bug that surfaces months later in payroll.

### Partial returns — proportional allocation

C14 calls this out as *"a common place where cheaper POS software silently produces wrong numbers."*

Returning 2 of 5 units:

```
proportion       = 2 / 5 = 0.40
revenue_reversed = original_line_net       × 0.40
vat_reversed     = original_line_vat       × 0.40
discount_reversed= original_discount_alloc × 0.40    ← invoice-level discount
cogs_reversed    = original_line_cogs      × 0.40
```

**Invoice-level discounts must be allocated to lines at the time of sale** and stored per line. Reconstructing the allocation at return time invites rounding drift. Rounding uses banker's rounding to 2 decimals with the residual assigned to the largest line, so the sum of parts always equals the whole.

---

## 8. Payment settlement (C12)

The problem the blueprint states: *"A customer pays SAR 1,000 by card, but the bank deposits only SAR 985 two days later. Without this module, the books never balance and the Owner never knows their real card cost."*

```
Sale, card tender:
  Dr  Card Clearing (Mada)      1,000
      Cr  Sales Revenue                 869.57
      Cr  Output VAT Payable            130.43

Settlement received (T+2):
  Dr  Bank                        985
  Dr  Bank/Card Charges            15
      Cr  Card Clearing Account        1,000
```

`settlement_status ∈ {PENDING, SETTLED, RECONCILED}` (C12's three-state machine).

**Batch reconciliation** — one bank deposit covers many sales, so a many-to-one link table maps `settlement_batch → payment[]`. QA gate M4 requires the batch to reconcile back to individual sales; the link table is what makes that a query rather than a forensic exercise.

Per-tender fee configuration gives the Owner true net margin per payment method — Mada materially cheaper than international credit, BNPL materially more expensive. Chargebacks and refund reversals post as their **own accounting events**, never as edits.

---

## 9. Multi-currency

Books in company base currency. Foreign-currency transactions store both the transaction amount and the base amount at the rate on the transaction date. On settlement, the difference posts to **FX Gain/Loss** (C9.4).

Exchange rates are effective-dated and never retroactively applied — the same discipline as registry values.

---

## 10. Financial statements

Produced from the journal, never from a parallel aggregate table:

| Statement | Invariant |
|---|---|
| General Ledger | Per account, with running balance |
| **Trial Balance** | **Total debits = total credits, always** |
| Income Statement (P&L) | Revenue − Expenses, gross and net |
| **Balance Sheet** | **Assets = Liabilities + Equity, always** |
| Cash Flow Statement | Derived from cash/bank account movements |
| Retained Earnings | Roll-forward across years |

Materialised views refresh incrementally for dashboard speed, but the journal remains the single source of truth and any statement can be recomputed from it. Heavy reports run async (A2 #8).

---

## 11. Acceptance criteria (QA gate M1)

| # | Criterion | Mechanism |
|---|---|---|
| 1 | **Trial balance always balances** | Deferred DB constraint; unbalanced entry cannot commit |
| 2 | **Balance Sheet balances** | Follows from 1, plus closing entries into Retained Earnings |
| 3 | **Sub-ledgers tie to control accounts** | Nightly assertion job on AR, AP, Inventory |
| 4 | **Closed periods truly locked** | Period state checked in engine **and** enforced by trigger |
| 5 | Returns reverse everything correctly | Nine effects in one transaction, proportional allocation for partials |
| 6 | Settlement batch reconciles to individual sales | Many-to-one link table |
| 7 | Historical reports reproduce historical figures | Rules and registry values resolved at transaction date |

---

## 12. Judgment calls made here

| Call | Alternative rejected | Why |
|---|---|---|
| Posting rules as data | Hard-coded posting functions | C9.2 says "defined, configurable"; hard-coding blocks per-tenant COA variation and makes each new transaction type a release |
| Synchronous posting in the originating transaction | Async posting pipeline | Async permits a sale with no journal entry, breaking M1 intermittently |
| Provisional cost offline, reconciled on sync | Block offline sales until cost known | Blocking violates the hard offline-first requirement (A2 #3) |
| Invoice-level discount allocated to lines at sale time | Reconstruct allocation at return time | Reconstruction drifts on rounding; C14 flags this exact failure mode |
| DB triggers for immutability and balance | Application-layer checks only | M1 and M7 both test by direct manipulation, bypassing the application |
| Standard Cost is in scope | Follow B4/D1's two-method list | C13 is the newer, explicitly "NEW" section and lists three methods |
