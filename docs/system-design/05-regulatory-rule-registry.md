# 05 — Regulatory Rule Registry

**Binding source:** Blueprint E8 (lines 1030–1077) + Part N.

> **HARD RULE, verbatim from E8:** *"No legal figure, deadline, threshold, rate, or file format may be hard-coded anywhere in the codebase."*

Build order note: this is **step 3** of the Phase 1 backend, before any tax, invoicing, or payroll logic. Building tax code first guarantees hard-coding exactly what must be data.

---

## 1. Why this exists

Saudi regulatory parameters change on their own schedule — VAT rates, GOSI contribution rates, wage-file formats, filing thresholds, PDPL response deadlines. The blueprint's reasoning:

> If any of these are written into source code, **every change becomes an emergency release across every tenant**. If they are **versioned data with effective dates**, a change is a configuration update Super Admin applies centrally.

There is a second, subtler reason that matters more in an audit: **historical accuracy**.

> *"A VAT report for March must use March's rate, even if the rate changed in June. **NEVER apply the current value retroactively.**"*

A system that stores only the current rate cannot regenerate a defensible prior-period report. That is not a feature gap — it is an audit failure.

---

## 2. Schema

```sql
CREATE TABLE regulatory_rule (
  id               UUID PRIMARY KEY,
  rule_key         TEXT NOT NULL,            -- 'SA.VAT.STANDARD_RATE'
  country          CHAR(2) NOT NULL,
  payload          JSONB NOT NULL,           -- rate | threshold | format spec | SLA days
  effective_from   DATE NOT NULL,
  effective_to     DATE,                     -- NULL = currently in force
  source_authority TEXT NOT NULL,            -- ZATCA|SDAIA|GOSI|MHRSD|SAMA|MoC
  source_document  TEXT NOT NULL,            -- official doc name + version/date
  source_url       TEXT,
  verified_on      DATE,                     -- NULL = NEVER VERIFIED, placeholder
  verified_by      UUID,
  notes            TEXT,
  created_at       TIMESTAMPTZ NOT NULL,
  created_by       UUID NOT NULL,

  EXCLUDE USING gist (
    rule_key WITH =, country WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&
  )
);
```

The `EXCLUDE` constraint is the load-bearing part: **PostgreSQL refuses to store two rows for the same rule whose date ranges overlap.** Ambiguity about which rate applied on a given date becomes structurally impossible rather than a bug to be found later.

`payload` is JSONB because rules are heterogeneous — a VAT rate is a number, a Mudad wage-file spec is a structured document, a QR TLV field set is a list. A typed column per rule shape would mean a migration per regulation.

### Per-tenant override

```sql
CREATE TABLE regulatory_rule_override (
  id               UUID PRIMARY KEY,
  tenant_id        UUID NOT NULL,
  company_id       UUID,
  rule_key         TEXT NOT NULL,
  payload          JSONB NOT NULL,
  effective_from   DATE NOT NULL,
  effective_to     DATE,
  justification    TEXT NOT NULL,            -- MANDATORY, per E8.3
  approved_by      UUID NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL
);
```

E8.3 permits overrides but calls them rare — e.g. a tenant with a ZATCA-approved special arrangement. `justification` is `NOT NULL` because a written reason is required, and every override is audit-logged.

---

## 3. Resolution

### The only supported access pattern

```go
// Correct — resolves at the transaction's date
rate, err := registry.Decimal(ctx, "SA.VAT.STANDARD_RATE", RuleCtx{
    Country:   "SA",
    TenantID:  tenantID,
    CompanyID: companyID,
    AsOf:      invoice.IssueDate,        // ← NEVER time.Now()
})
```

Resolution order: **tenant override → company override → platform rule**, each filtered by `AsOf` falling inside `[effective_from, effective_to)`.

`AsOf` is a required parameter with no default. There is deliberately no `registry.Current()` convenience function — its existence would guarantee that someone eventually uses it inside a historical report.

### Caching

Rules change rarely and are read constantly. In-memory cache keyed by `(rule_key, country, as_of_month)`, invalidated on any registry write via Redis pub/sub across API instances. Cold-start reads warm the cache for the current period.

### Offline resolution

POS terminals cache the rules they need with their **full effective-date ranges**, not just current values. A terminal that last synced in January still applies March's rate correctly when selling in March, provided the March row was already published.

A rule published *after* the terminal's last sync and effective *before* the current sale is the one genuine failure mode. Mitigation: the pull channel prioritises registry rows, and a terminal whose registry cache is older than 7 days shows a warning to the Owner.

---

## 4. What must live here (E8.2)

| Domain | Rule keys |
|---|---|
| **VAT** | `SA.VAT.STANDARD_RATE` (currently 15%) · `MANDATORY_REGISTRATION_THRESHOLD` (375,000) · `VOLUNTARY_REGISTRATION_THRESHOLD` (187,500) · `MONTHLY_FILING_THRESHOLD` (40M) · `FILING_DUE_DATE_RULE` · `RECORD_RETENTION_YEARS` (6, extended for defined assets) |
| **ZATCA** | `REPORTING_WINDOW_HOURS` (24) · `STANDARD_CLEARANCE_RULE` · **`XML_SCHEMA_VERSION`** · **`QR_TLV_FIELDS`** · **`HASH_ALGORITHM`** · `CSID_RENEWAL_DAYS` · `WAVE_DEFINITIONS` |
| **GOSI** | `EMPLOYER_RATE` · `EMPLOYEE_RATE` · split by **Saudi vs expatriate** · split by **pre/post-July-2024 hire** · `CONTRIBUTION_WAGE_CAP` · **step-increases through 2028, each as its own dated row** |
| **WPS / Mudad** | **`WAGE_FILE_FORMAT_SPEC`** + version · `SUBMISSION_LEAD_TIME` · `SALARY_PAYMENT_WINDOW` · `REQUIRED_IDENTIFIER_FIELDS` |
| **PDPL** | `DSR_RESPONSE_DAYS` (30, extendable 30) · `BREACH_NOTIFICATION_HOURS` (72) · retention defaults per data category · cross-border transfer conditions |
| **Labour** | `EOSB_ACCRUAL_FORMULA` · `LEAVE_ENTITLEMENTS` · `OVERTIME_MULTIPLIERS` |
| **E-Commerce** | `COOLING_OFF_DAYS` (14) · `CATEGORY_EXEMPTIONS` · `REQUIRED_DISCLOSURE_FIELDS` |
| **Withholding Tax** | Rates by payment type and recipient status |

Values shown are the blueprint's **unverified starting figures**. Every one ships with `verified_on = NULL` until a human checks it against the Tier 1 source.

### The GOSI shape, as an illustration

GOSI is the clearest case for why dated rows beat a settings screen. Rates differ by nationality, differ by hire date relative to July 2024, and step upward on a legislated schedule through 2028. That is a two-dimensional matrix that changes over time:

```json
{
  "rule_key": "SA.GOSI.RATES",
  "effective_from": "2026-01-01",
  "effective_to": "2027-01-01",
  "payload": {
    "saudi_post_jul2024":  { "employer": "__VERIFY__", "employee": "__VERIFY__" },
    "saudi_pre_jul2024":   { "employer": "__VERIFY__", "employee": "__VERIFY__" },
    "expatriate":          { "employer": "__VERIFY__", "employee": 0 },
    "wage_cap":            "__VERIFY__"
  },
  "verified_on": null
}
```

Payroll resolves at the **pay period being processed**, so re-running an old month produces the historically correct figure — blueprint E6's explicit requirement.

---

## 5. Governance (E8.3)

| Control | Rule |
|---|---|
| **Who may edit** | **Super Admin only.** No tenant-level user, no Owner |
| **Audit** | Every change permanently logged with before/after values **and the source document cited** |
| **Effective-dated rollout** | A change entered today with next-month effective date **applies automatically on that date across all tenants — no deployment, no per-tenant work** |
| **Staleness alerting** | Suggested re-verification every **6 months for tax and payroll**, **12 months for others** |
| **Overrides** | Rare, always with written justification and audit entry |

### Staleness — the mechanism that prevents quiet drift

A nightly job flags every rule whose `verified_on` is older than its domain's window, or is `NULL`:

```
Registry Health
  🔴  3 rules NEVER verified          ← blocks first release
  🟠  2 rules stale > 6 months (tax/payroll)
  🟡  5 rules stale > 12 months
  ✅ 41 rules verified within window
```

The blueprint calls this *"the operational mechanism that keeps the platform legally current instead of quietly drifting."* It is surfaced on the Super Admin dashboard and emailed weekly, because a passive flag nobody looks at achieves nothing.

---

## 6. Pre-release blockers (E8.4)

Three rules are singled out as **the most volatile and the most damaging if wrong**. Release gates, not warnings:

| # | Rule | Why | Gate |
|---|---|---|---|
| 1 | **Mudad wage-file format** | *"File layouts change without publicity."* Must be pulled from the live Mudad specification immediately before build, and **re-checked before every payroll-module release** | Blocks Phase 3 |
| 2 | **GOSI rate schedule** | On a legislated upward path — must be entered as a **complete dated schedule, not a single current figure** | Blocks Phase 3 |
| 3 | **ZATCA XML/QR schema version** | Must match the schema version ZATCA currently accepts. **The system must record which schema version each archived invoice was generated under** | Blocks Phase 1 |

Blocker 3 is why `zatca_invoice.schema_version` exists in `01-invoice-zatca-engine.md` §4.3: when ZATCA moves to a new schema, six-year-old archived invoices must still be verifiable against the schema *they were signed under*.

CI fails the release build if any rule tagged `release_blocker` still has `verified_on IS NULL`.

---

## 7. Source hierarchy (Part N)

> **Gate rule:** *"No compliance feature may be implemented from Tier 2 alone."*

**Tier 1 — binding.** Only these may justify a technical decision:

| Domain | Authority | Documents |
|---|---|---|
| E-Invoicing | **ZATCA** `zatca.gov.sa` | E-Invoicing Detailed Guideline · **XML Implementation Standard** · **Security Features Implementation Standard** · Data Dictionary · Fatoora SDK · CSID onboarding docs |
| VAT | **ZATCA** | VAT Implementing Regulations · VAT guidelines · taxpayer portal e-services |
| Data Protection | **SDAIA** `sdaia.gov.sa` | PDPL · Implementing Regulation · **Transfer Outside the Kingdom Regulation** · destruction & anonymization guidance |
| Payroll / WPS | **MHRSD + Mudad** | Current wage-file specification and format · submission timing |
| Social Insurance | **GOSI** `gosi.gov.sa` | Current contribution rate schedule (**a dated schedule, not a number**) |
| Employment | **Qiwa** `qiwa.sa` | Contract registration · Nitaqat |
| Payments | **SAMA** `sama.gov.sa` | **Current licensed PSP list** · Payment Services Provider Regulations |
| E-Commerce | **Ministry of Commerce** `mc.gov.sa` / `business.sa` | E-Commerce Law & Implementing Regulations · current registration channel |
| Accounting Standards | **SOCPA** | IFRS as adopted in Saudi Arabia |

**Tier 2 — orientation only.** Consulting firms and advisory blogs informed *scoping*. They are **never** a basis for a technical decision and must not be cited in `source_document`.

**Process rule, verbatim:** *"When a Tier 1 source contradicts this document, the Tier 1 source wins and this document is amended — with the change dated and noted, so the blueprint never silently drifts out of alignment with the law."*

`source_authority` is a constrained enum limited to Tier 1 bodies, so a Tier 2 source cannot be recorded as justification even by mistake.

---

## 8. Seeding and the placeholder discipline

The `sa-baseline` seed creates every rule listed in §4 with:

- the blueprint's stated value as a **starting point**
- `verified_on = NULL`
- `source_document` naming the document that **must** be consulted
- `notes` quoting the blueprint's caveat where one exists

**A `verified_on = NULL` rule is usable in development but fails the release gate.** This is the mechanism that enforces the blueprint's instruction:

> *"Do not let developers fill these in from assumption."*

The system runs from day one; it cannot **ship** on assumptions.

---

## 9. Judgment calls made here

| Call | Alternative rejected | Why |
|---|---|---|
| `EXCLUDE` constraint on overlapping date ranges | Application-level validation | Overlapping rules mean ambiguous law; the DB should refuse to represent it |
| JSONB payload | Typed column per rule shape | Rules are heterogeneous; typed columns mean a migration per regulation |
| `AsOf` is required, no `Current()` helper | Convenience accessor with a default | A default of "now" silently breaks historical reports — the exact failure E8.1 warns about |
| `verified_on = NULL` blocks release, not development | Block all use until verified | Development would be impossible; the gate belongs at release |
| Terminals cache full date ranges | Cache current values only | An offline terminal must apply the rate correct for the sale date, not for its last sync |
| `source_authority` constrained to Tier 1 | Free-text field | Part N's gate rule is only real if the schema enforces it |
