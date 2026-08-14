# 04 — Identity, Tenancy, RBAC & Security

**Binding source:** Blueprint A3, A4, A4.1, A4.2, A5, A6, H1, H3.
**Acceptance gates:** M7 (every restricted action attempted **via direct API call** as a Cashier must be rejected server-side) · M8 (cross-tenant access via **manipulated API requests** must fail in every case).

Build order note: this is **step 2** of the Phase 1 backend, immediately after the repo skeleton. Nothing else is safe to build until tenant isolation and authorization exist.

---

## 1. The organisational model

```
Group                one Owner login may span several legal companies
 └── Company          legal entity — OWN books · OWN VAT registration · OWN ZATCA sequence
      └── Store       branch / showroom / warehouse location
           └── Terminal   POS device — OWN CSID · OWN ICV counter · OWN PIH chain
```

Two requirements pull against each other and this shape resolves both:

- **F4** wants one login across multiple companies, consolidated group P&L, and inter-company elimination.
- **E1/E2** require books, VAT registration, and the ZATCA sequence to belong to **one legal entity**, never a group.

So `Group` is an access-and-reporting construct only. **`Company` is the accounting and compliance boundary.** Nothing financial or legal ever aggregates at group level except explicitly-computed consolidation.

### Tables

```sql
CREATE TABLE tenant (                        -- == Group
  id             UUID PRIMARY KEY,
  name           TEXT NOT NULL,
  data_region    data_region NOT NULL DEFAULT 'sa',   -- sa|eu|asia|other
  plan_tier      plan_tier NOT NULL,
  status         tenant_status NOT NULL,    -- ACTIVE|SUSPENDED|DEACTIVATED
  created_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE company (
  id                    UUID PRIMARY KEY,
  tenant_id             UUID NOT NULL REFERENCES tenant(id),
  legal_name            TEXT NOT NULL,
  legal_name_ar         TEXT,
  country               CHAR(2) NOT NULL,
  base_currency         CHAR(3) NOT NULL,
  cr_number             TEXT,                -- Commercial Registration
  vat_registered        BOOLEAN NOT NULL DEFAULT false,
  vat_number            TEXT,                -- validated when vat_registered
  fiscal_year_start     SMALLINT NOT NULL DEFAULT 1,
  -- ZATCA obligation: captured from the taxpayer's own notification, never inferred
  zatca_wave            TEXT,
  zatca_deadline        DATE,
  zatca_status          zatca_onboarding_status NOT NULL DEFAULT 'NOT_STARTED',
  b2b_offline_policy    b2b_policy NOT NULL DEFAULT 'BLOCK',
  negative_stock_policy neg_stock_policy NOT NULL DEFAULT 'BLOCK',
  costing_method        costing_method NOT NULL DEFAULT 'WAC',
  CHECK (NOT vat_registered OR vat_number IS NOT NULL)
);
```

The `CHECK` constraint enforces blueprint E2.1's requirement that the VAT number is *"a validated, required field for any VAT-registered tenant."*

---

## 2. Tenant isolation — Row-Level Security

Blueprint A3 requires enforcement **"at the database query layer, not just the frontend."** Implemented literally.

### Mechanism

```sql
ALTER TABLE sales_invoice ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_invoice FORCE ROW LEVEL SECURITY;   -- applies to table owner too

CREATE POLICY tenant_isolation ON sales_invoice
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
```

Every request opens a transaction and sets the GUC from the **verified JWT claim** before any query runs:

```go
func (m *TenantMiddleware) Handle(next http.Handler) http.Handler {
    // claims already verified by auth middleware
    tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, claims.TenantID)
    // `true` = transaction-scoped; resets automatically on commit/rollback
}
```

`FORCE ROW LEVEL SECURITY` matters: without it, the table owner bypasses policies, and the application role is often the owner in small deployments.

### Why this over the alternatives

| Approach | Verdict |
|---|---|
| Database per tenant | **Rejected.** N migration targets, connection-pool exhaustion, N backup schedules — operational load a solo maintainer cannot carry |
| Schema per tenant | **Rejected.** Same migration problem, marginal isolation gain |
| Application-layer `WHERE tenant_id = ?` only | **Rejected.** One forgotten clause is a breach. M8 tests exactly this |
| **Shared schema + RLS** | **Chosen.** One migration path; isolation enforced by the engine; **M8 satisfied by construction** |

### The honest risk

A bug in the `app.tenant_id` plumbing is systemic rather than local. Mitigations:

1. That plumbing exists in **exactly one middleware**, with its own test suite.
2. A migration lint fails CI if any new table with a `tenant_id` column lacks an RLS policy.
3. An integration test iterates **every** endpoint as tenant A and asserts tenant B's rows are unreachable — this is M8, automated.
4. Connections never run as a superuser (superusers bypass RLS).

---

## 3. Authentication

### Users and sessions

```sql
CREATE TABLE app_user (
  id                UUID PRIMARY KEY,
  tenant_id         UUID REFERENCES tenant(id),   -- NULL = Super Admin
  email             CITEXT NOT NULL,
  phone             TEXT,
  password_hash     TEXT NOT NULL,                -- argon2id
  must_change_pw    BOOLEAN NOT NULL DEFAULT true,
  mfa_enabled       BOOLEAN NOT NULL DEFAULT false,
  mfa_secret_enc    BYTEA,                        -- KMS-encrypted
  status            user_status NOT NULL,
  failed_attempts   INT NOT NULL DEFAULT 0,
  locked_until      TIMESTAMPTZ,
  UNIQUE (tenant_id, email)
);

CREATE TABLE user_session (
  id             UUID PRIMARY KEY,
  user_id        UUID NOT NULL,
  device_label   TEXT,
  ip             INET,
  user_agent     TEXT,
  created_at     TIMESTAMPTZ NOT NULL,
  last_seen_at   TIMESTAMPTZ NOT NULL,
  revoked_at     TIMESTAMPTZ                      -- remote revoke (H1)
);
```

**Password hashing is argon2id.** Blueprint A4.2 is explicit that a password is *"stored as an irreversible hash, not plain text — this is a security requirement, not just a policy choice."*

### Tokens

| Token | Lifetime | Contents |
|---|---|---|
| Access (JWT) | 15 min | `sub`, `tenant_id`, `company_ids[]`, `role_ids[]`, `session_id` |
| Refresh (opaque) | 30 days, rotating | Stored server-side, revocable |
| POS device token | Long-lived, device-bound | `device_id`, `terminal_id`, `company_id` — scoped to sync endpoints only |

**Permissions are NOT embedded in the JWT.** They are resolved server-side per request from the role assignment. A permission revoked at 10:00 must not remain effective until a 15-minute token expires — for an ERP handling money, that window is unacceptable. Resolution is cached in Redis with explicit invalidation on role change.

### MFA

**Mandatory for Super Admin** (A4.1). Available and encouraged for Owner and Accountant roles. TOTP authenticator app or SMS OTP.

### Super Admin recovery (A4.1)

Recovery email + MFA-based recovery + **one-time backup codes generated at setup, printable/downloadable once** + a documented emergency procedure that *"cannot be triggered by anyone but the verified platform owner."* The Super Admin's own password is **never stored or displayed in plain text anywhere, including to Super Admin.**

### Owner recovery (A4.2) — exact workflow

```
1. Owner attempts self-service recovery (recovery email / OTP to registered phone)
2. If that fails → Owner contacts Super Admin
3. Super Admin verifies identity (registered phone / email / company details)
4. Super Admin triggers a secure reset → issues a ONE-TIME password
5. Super Admin does NOT view or restore the existing password — it is an
   irreversible hash. No code path exists to reveal it.
6. Every assisted recovery is written to the permanent audit log:
   who requested, who approved, timestamp, IP
```

Step 5 is a structural guarantee, not a policy: there is no decryption function to call.

---

## 4. Authorization model

### 4.1 The permission atom

```
permission := <module>.<verb>
```

Blueprint A6.2 lists 14 verbs, but its own worked example uses `Hold`, `Exchange`, and `Receive Payment`, which are not in the list. **The verb set is therefore per-module and extensible, not a fixed global enum** — a registry populated at startup from each module's declaration.

Base verbs: `view · create · edit · delete · approve · export · print · refund · discount · void · adjust_stock · transfer_stock · view_cost_price · view_profit_margin`
Module-declared examples: `sales.hold` · `sales.exchange` · `sales.receive_payment` · `zatca.retry_submission`

### 4.2 Roles

```sql
CREATE TABLE role (
  id            UUID PRIMARY KEY,
  tenant_id     UUID,                        -- NULL = platform predefined template
  name          TEXT NOT NULL,
  is_system     BOOLEAN NOT NULL DEFAULT false,
  cloned_from   UUID REFERENCES role(id)
);

CREATE TABLE role_permission (
  role_id       UUID NOT NULL REFERENCES role(id),
  permission    TEXT NOT NULL,
  PRIMARY KEY (role_id, permission)
);
```

**The 12 predefined roles** (A6.1), seeded per tenant and clonable:

| Role | Notable restriction |
|---|---|
| Owner | Everything within their tenant |
| Branch/Store Manager | **No bank ledgers, no true net profit** |
| Cashier / POS Operator | **Cost price and margins always hidden** |
| Accountant | **Cannot edit inventory or products** |
| Inventory / Warehouse Keeper | **No pricing or sales access** |
| Purchase Manager | Requisitions, POs, supplier communication |
| HR Manager | **No sales or inventory access** |
| Sales Executive | **Own** customer list only |
| Delivery Staff | **Assigned** delivery orders only |
| Online Order Manager | Web/app order queue, packing, dispatch |
| Auditor | **Read-only everything, edit nothing** |
| Customer Service | **No financial data** |

### 4.3 Scoping — four dimensions on top of verbs

A verb grant is necessary but not sufficient. Every assignment carries scope:

```sql
CREATE TABLE user_role_assignment (
  id             UUID PRIMARY KEY,
  user_id        UUID NOT NULL,
  role_id        UUID NOT NULL,
  company_id     UUID,                       -- NULL = all companies in group
  store_ids      UUID[],                     -- NULL = all stores
  warehouse_ids  UUID[],                     -- NULL = all warehouses
  amount_limit   NUMERIC(18,4),              -- NULL = unlimited
  valid_from     TIMESTAMPTZ,
  valid_until    TIMESTAMPTZ                 -- temporary/seasonal staff
);
```

| Dimension | Example from blueprint |
|---|---|
| **Store/branch** | A manager sees only their own branch |
| **Warehouse** | Inventory staff scoped to one warehouse |
| **Amount limit** | Cashier discount up to SAR 50 · Manager up to SAR 500 · Owner unlimited |
| **Time window** | A temporary staff role that expires automatically |

### 4.4 Field-level data masking

Distinct from row and verb permissions. A6.2: cashiers can be blocked from ever seeing **supplier cost prices, profit margins, or other employees' salaries** — *"even if they technically have 'view stock' access."*

Masked fields are declared per module and stripped in the serialisation layer, **not** filtered in the front end:

```go
type ProductDTO struct {
    Name      string  `json:"name"`
    Price     Money   `json:"price"`
    CostPrice *Money  `json:"cost_price,omitempty" mask:"catalog.view_cost_price"`
    Margin    *float64 `json:"margin,omitempty"    mask:"catalog.view_profit_margin"`
}
```

If the caller lacks the permission the field is **absent from the payload**, not null and not zero — so it cannot be recovered from the wire, a cached response, or a browser devtools panel.

### 4.5 Enforcement

Every handler declares its requirement; a middleware enforces it before the handler body runs. There is no "internal" endpoint exemption.

```go
r.With(auth.Require("sales.refund"), auth.AmountLimit()).
  Post("/api/v1/sales/{id}/refund", h.Refund)
```

**M7 is automated:** a test enumerates the route table, calls every route as a Cashier, and asserts `403` for everything outside the Cashier role. Adding a route without a permission declaration fails CI.

---

## 5. Compliance capability is derived, never granted

```
company.country == 'SA'
  AND company.vat_registered
  AND company.zatca_status IN ('WAVE_NOTIFIED','LIVE')
      ⇒ ZATCA engine ON — no toggle exists
```

Per A4's HARD RULE and v2.2 correction #1, and **overriding H5's listing of "ZATCA module" as a plan entitlement**: ZATCA e-invoicing, VAT calculation, invoice immutability, audit logging, and PDPL handling are **core platform capabilities, not sellable modules**.

`feature_flag` rows are validated against a deny-list at write time — attempting to create a flag named for a compliance capability is rejected by the API. The capability is not merely "on by default"; **there is no representation of it being off**.

Only compliance **monitoring** is premium: multi-branch compliance analytics, extended audit-export tooling, proactive deadline dashboards, priority ZATCA failure alerting.

---

## 6. Audit trail

Blueprint D4 specifies **exactly six fields**:

```sql
CREATE TABLE audit_log (
  id            BIGSERIAL PRIMARY KEY,
  tenant_id     UUID,
  actor_id      UUID,                        -- WHO
  action        TEXT NOT NULL,               -- WHAT
  occurred_at   TIMESTAMPTZ NOT NULL,        -- WHEN
  ip            INET,                        -- WHERE
  device_label  TEXT,                        --   "
  entity_type   TEXT NOT NULL,
  entity_id     UUID,
  before_value  JSONB,                       -- BEFORE
  after_value   JSONB                        -- AFTER
);

CREATE TRIGGER audit_log_append_only
  BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION reject_always();
```

**Append-only, enforced by trigger.** D4: *"cannot be edited or deleted by any user, including Owner, to preserve evidentiary integrity."*

The twelve sensitive actions logged: price changed · product deleted/deactivated · invoice created · refund issued · discount applied · stock adjusted · **user permission changed** · bank transfer · expense created · **employee salary changed** · login/logout · **Owner-recovery events**.

Personal data inside `before_value`/`after_value` is subject to PDPL. Anonymisation replaces the values while **retaining the audit row** — the evidence that a change occurred survives even when the personal content is erased.

---

## 7. Application security (H1)

| Control | Implementation |
|---|---|
| Transport | HTTPS only, HSTS |
| CSRF | SameSite cookies + token for cookie-auth flows |
| Rate limiting | Redis token bucket, per IP and per user; stricter on auth endpoints |
| Input validation | Zod on the client, struct validation on the server — server is authoritative |
| SQL injection | Parameterised queries only; string-built SQL fails CI lint |
| XSS | React escaping by default; `dangerouslySetInnerHTML` is lint-banned |
| Headers | CSP, X-Content-Type-Options, X-Frame-Options, Referrer-Policy |
| API tokens | Signed, expiring; device tokens bound to `device_id` |
| Secrets | KMS/Vault. **Never** in source or unencrypted `.env`. Rotation documented |
| **ZATCA CSID private keys** | **On the terminal in OS keystore** — see `01-invoice-zatca-engine.md` §7 |

Password policy is a **global setting** administered by Super Admin (A4 lists "global security policy (password rules, session timeout, lockout thresholds)"), not a hard-coded constant — the blueprint states no specific policy, so it is configuration with sensible defaults: minimum 12 characters, breach-list check, no forced rotation (current NIST guidance), lockout after 8 failed attempts.

---

## 8. Onboarding (A5) — 7 steps

Designed so a non-technical shop owner can complete setup alone.

| # | Step | Notes |
|---|---|---|
| 1 | Business Information | Company/legal name, type, **country**, address, phone, email, website, **VAT number**, **CR number**, base currency, timezone |
| 2 | Store Setup | Name, code, address, phone, opening hours — repeatable for multi-branch |
| 3 | **Tax Configuration** | Country-driven. **Saudi ⇒ auto pre-load VAT 15%, ZATCA settings, Arabic RTL defaults** — all read from the Regulatory Rule Registry, never hard-coded |
| 4 | Employees | Add Owner, Manager, Cashier, Accountant, Inventory staff with roles |
| 5 | Hardware Setup | Detect/pair barcode scanner, thermal printer, cash drawer, label printer |
| 6 | Opening Balances | Cash, bank, inventory, investment, payables, receivables — posts an opening journal entry |
| 7 | Finish | Full environment provisioned automatically; Owner lands on Dashboard |

Step 3 also captures the **ZATCA obligation profile** — wave and deadline as notified to the taxpayer by ZATCA. The wizard makes clear this comes from **their own ZATCA notification**, never from the software.

Step 6 is the one that most often goes wrong in practice, so it validates that the opening trial balance balances before committing, and refuses to proceed otherwise.

---

## 9. Device management (H3)

```sql
CREATE TABLE device (
  id                UUID PRIMARY KEY,
  tenant_id         UUID NOT NULL,
  company_id        UUID NOT NULL,
  store_id          UUID NOT NULL,
  terminal_label    TEXT NOT NULL,
  os                TEXT,
  app_version       TEXT,
  last_sync_at      TIMESTAMPTZ,
  last_active_at    TIMESTAMPTZ,
  assigned_user_id  UUID,
  printer_config    JSONB,
  status            device_status NOT NULL,  -- ACTIVE|INACTIVE|REVOKED
  csid_serial       TEXT,
  csid_expires_at   TIMESTAMPTZ
);
```

Owner can activate · deactivate · revoke · rename · reassign to another store · force an app update · view health and sync status remotely.

**Reassignment does not start a new ZATCA chain** — the chain belongs to the device under its company's VAT registration, and ICV continues unbroken. Only a genuinely new terminal starts at ICV 1 (E1.3 RULE 5).

---

## 10. Judgment calls made here

| Call | Alternative rejected | Why |
|---|---|---|
| Permissions resolved per request, not in the JWT | Embed permissions in the token | A revoked permission must take effect immediately, not after token expiry |
| Shared schema + RLS | DB-per-tenant | Operational load; RLS satisfies M8 structurally |
| Masked fields **omitted** from payload | Nulled or zeroed | Null still reveals the field exists; omission reveals nothing |
| Verb set per module, extensible | Fixed global enum of 14 | A6.2's own example uses verbs outside its list |
| Compliance capability has no "off" representation | Default-on boolean flag | A default can be flipped; a nonexistent state cannot |
| `FORCE ROW LEVEL SECURITY` | Plain RLS | Table owners bypass plain RLS, and the app role often is the owner |
| Password policy as Super Admin configuration | Hard-coded rules | A4 explicitly lists global security policy as Super Admin-managed |
