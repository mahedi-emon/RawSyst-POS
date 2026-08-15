# 01 — Invoice State Machine & ZATCA Engine

> **PILLAR 1 of 3.** Blueprint line 1658: *"Get those three right and the remaining modules are conventional work. Get them wrong and the entire system has to be rebuilt."*

**Binding source:** Blueprint E1 (lines 742–863), especially **E1.3 which is marked "LOCKED — DECIDED"**.
**Legal caveat:** This document designs the *machinery*. The byte-level XML, TLV QR layout, and hash construction must be taken from ZATCA's official **XML Implementation Standard** and **Security Features Implementation Standard** — not from this document and not from the blueprint. See `90-regulatory-verification-checklist.md`.

---

## 0. The terminal hands its document back

> **Implemented 2026-08-16.** `PUT /api/v1/pos/sales/{invoiceID}/signed-document`

Signing is local, but the server allocates the ICV and the PIH — those are a
per-terminal sequence only one authority can arbitrate. So the order is
necessarily:

```
server    allocates ICV + PIH             POST /api/v1/pos/sales
terminal  builds UBL, signs, derives QR   LOCAL; key never leaves the device
terminal  uploads document + stamp + QR   PUT  …/signed-document
worker    submits to ZATCA                gated until the format is verified
```

Three artefacts, three columns, kept distinct because they are three different
things: `xml` is the canonical signed document ZATCA receives, `stamp` is the
ECDSA signature over it, and `qr_tlv` is the payload derived from that
signature for the receipt. Sending a signature where a document belongs posts a
stamp attached to nothing.

All three are **write-once** (migration `0029`). A document that could be
replaced after signing is exactly the substitution a tamper-evident chain
exists to catch — the stamp would still verify against a document nobody kept.

The upload validates nothing against ZATCA's standard, because the standard is
not yet verified. It stores what the terminal produced and reports plainly
whether submission is available.

**Still open (P1).** The chain hash. §3 defines it as SHA-256 over the
canonical signed XML, and the canonicalisation is one of the values still
marked `__VERIFY__` in the registry. The server allocates a hash through the
stubbed `DocumentHasher`; reconciling that with the terminal's belongs to the
verification pass and must not be improvised.

---

## 1. The decision that shapes everything: signing is local

The blueprint contradicts itself. Resolving it wrong means rebuilding the POS.

| Source | Says |
|---|---|
| J3 diagram (line 1352) | Invoice is signed **in the cloud, after sync** |
| **E1.3 RULE 1 (line 813), marked LOCKED** | *"the device's CSID private key and the local ICV/PIH chain must exist on the terminal itself, not only in the cloud. **Signing is a LOCAL operation.**"* — explicitly called a hard architectural requirement for the Tauri desktop POS |
| Part L (line 1483) | *"Offline signing architecture (sign locally, transmit later)"* |
| Part M item 3 | Sell **500 invoices fully offline** → reconnect → **correct hash chain order** |

**Ruling: signing is local.** E1.3 is explicitly locked and far more specific; Part L corroborates it; and Part M is only satisfiable with local signing — a cloud-signed chain would not exist at all until reconnection, so 500 offline receipts could not carry valid QR codes.

**J3 is a simplified diagram and should be amended in the blueprint.**

### What follows from that ruling

1. Each terminal holds its **own ECDSA private key** (device CSID).
2. Each terminal owns its **own ICV counter** and **own PIH chain**. Chains are never merged and never re-sequenced by the cloud (E1.3 RULE 5).
3. The cloud is a **replica and submitter**, not the origin, of the chain.
4. Key custody on a Windows desktop becomes a first-class security problem — solved in §7.

---

## 2. Document types — five separate models, not one "invoice"

ZATCA treats these as distinct document types with different rules. Modelling them as variations of one entity is the classic mistake.

| Type | Model | Offline-capable | Requires |
|---|---|---|---|
| **Standard Tax Invoice** (B2B) | **Clearance** — cleared by ZATCA *before* handing to buyer | ❌ No | Full buyer details incl. **buyer VAT number** |
| **Simplified Tax Invoice** (B2C) | **Reporting** — issued immediately, reported within 24 h | ✅ Yes | Nothing beyond seller data |
| **Credit Note** | Follows the type it corrects | Follows parent | **Must reference original invoice** |
| **Debit Note** | Follows the type it corrects | Follows parent | **Must reference original invoice** |
| **Self-billing / third-party** | Special handling | Per tenant | Configurable per tenant |

They share one table with a discriminator, because they share the ICV chain and must be sequenced together — but the **rules engine treats them separately**.

---

## 3. State machine

### 3.1 States

```
                          ┌──────────┐
                          │  DRAFT   │  no ICV consumed, no stamp, not a legal document
                          └────┬─────┘
                               │ finalize()
                               ▼
                       ┌───────────────┐
                       │   GENERATED   │  ICV allocated, UBL built, not yet signed
                       └───────┬───────┘
                               │ sign()  ← LOCAL, on terminal
                               ▼
              ┌────────────────────────────────┐
              │  SIGNED_PENDING_SUBMIT         │  legally deliverable if Simplified
              │  (chain member, QR printed)    │  receipt printed here
              └────────────┬───────────────────┘
                           │ submit()
                           ▼
                   ┌───────────────┐
                   │   SUBMITTED   │  awaiting Fatoora response
                   └───┬───────┬───┘
             accepted  │       │  transport / 5xx / timeout
                       ▼       ▼
        ┌──────────────────┐  ┌──────────────┐
        │ CLEARED (B2B)    │  │    FAILED    │──► RETRY_QUEUE ──┐
        │ REPORTED (B2C)   │  └──────────────┘                  │
        └────────┬─────────┘         ▲                          │
                 │                   └──────────────────────────┘
                 ▼                        (exponential backoff)
          ┌──────────────┐
          │   ACCEPTED   │  terminal state — archived immutably
          └──────────────┘

        ┌──────────────┐
        │   REJECTED   │  ZATCA rejected on business grounds.
        └──────────────┘  STAYS IN CHAIN. ICV never reused. Critical alert.
                          Corrected only by Credit Note.
```

### 3.2 Transition table

| From | Event | To | Guard | Side effects |
|---|---|---|---|---|
| — | `create` | `DRAFT` | Cart valid | No ICV |
| `DRAFT` | `finalize` | `GENERATED` | Period open · stock available or negative-stock policy allows · **B2B: online, unless Option B** | **ICV allocated** · UBL 2.1 XML built · PIH read from previous chain member |
| `GENERATED` | `sign` | `SIGNED_PENDING_SUBMIT` | Device CSID present and valid | ECDSA stamp · TLV QR generated · **hash written as next PIH** · **receipt printable** |
| `SIGNED_PENDING_SUBMIT` | `submit` | `SUBMITTED` | Online · **all lower-ICV invoices on this device already submitted** | Fatoora call (clearance or reporting) |
| `SUBMITTED` | `zatca_accept` | `CLEARED` / `REPORTED` | — | Store ZATCA UUID + clearance/reporting timestamp |
| `SUBMITTED` | `zatca_reject` | `REJECTED` | — | **Critical alert** · ICV retained · Credit Note required |
| `SUBMITTED` | `transport_error` | `FAILED` | — | Enqueue retry with backoff |
| `FAILED` | `retry` | `SUBMITTED` | Backoff elapsed · ordering still satisfied | — |
| `CLEARED`/`REPORTED` | `archive` | `ACCEPTED` | Retention record written | Immutable archive (XML + PDF + chain proof) |
| `DRAFT` | `discard` | *(deleted)* | Still DRAFT | **Allowed — drafts consume no ICV** |
| Any post-`GENERATED` | `edit`/`delete` | ❌ **BLOCKED** | — | Never permitted, by anyone including Super Admin |

### 3.3 The critical guard: submission ordering

`SIGNED_PENDING_SUBMIT → SUBMITTED` carries the guard **"all lower-ICV invoices on this device already submitted."** This implements E1.3 RULE 4: *"The hash chain must be submitted in ICV order. The sync engine must preserve ordering per device, not submit in arrival order."*

The submitter is therefore **strictly serial per device**, never a parallel worker pool over a shared queue. Parallelism happens *across* devices, never *within* one.

---

## 4. The ICV / PIH chain

### 4.1 Rules (E1.3 RULES 4 and 5)

1. ICV is **strictly sequential and non-resetting, per device**. A new terminal starts at **ICV 1**.
2. **Drafts never consume an ICV.** A gap is precisely what ZATCA's tamper detection looks for.
3. A **rejected** invoice keeps its ICV and stays in the chain. The ICV is never reused.
4. Chains are **never merged across devices** and **never re-sequenced by the cloud**.
5. PIH of invoice *n* = **SHA-256 of the canonical signed XML of invoice n−1** on the same device. The first invoice on a device uses ZATCA's defined genesis value.

### 4.2 Allocation algorithm — local, transactional

```
BEGIN IMMEDIATE TRANSACTION            -- SQLite, blocks concurrent writers

  chain := SELECT * FROM zatca_chain WHERE terminal_id = ? FOR UPDATE
  icv   := chain.last_icv + 1
  pih   := chain.last_hash            -- genesis value if first

  xml   := build_ubl(invoice, icv, pih)
  stamp := ecdsa_sign(canonicalize(xml), device_private_key)
  hash  := sha256(canonical_signed_xml)
  qr    := tlv_base64(seller, vat_no, ts, total, vat, hash, stamp, pubkey)

  INSERT INTO local_invoice (…, icv, pih, hash, stamp, qr, state='SIGNED_PENDING_SUBMIT')
  UPDATE zatca_chain SET last_icv = icv, last_hash = hash WHERE terminal_id = ?

COMMIT                                 -- receipt prints only after commit
```

**Why `BEGIN IMMEDIATE` and one transaction:** the counter increment, the signature, and the chain-head update must be atomic. If the machine loses power between allocating ICV 41 and writing the invoice, ICV 41 must either exist fully or not at all — a half-allocated counter is an unrecoverable chain gap.

**Why the receipt prints only after commit:** a printed receipt is a legally delivered document. Printing before the durable write risks a customer holding an invoice the system has no record of.

### 4.3 Tables

**Local (SQLite, on the terminal — authoritative for the chain):**

```sql
CREATE TABLE zatca_chain (
  terminal_id     TEXT PRIMARY KEY,
  company_id      TEXT NOT NULL,
  csid_serial     TEXT NOT NULL,
  last_icv        INTEGER NOT NULL DEFAULT 0,
  last_hash       TEXT    NOT NULL,          -- genesis value initially
  schema_version  TEXT    NOT NULL,          -- which ZATCA schema signed these
  updated_at      TEXT    NOT NULL
);

CREATE TABLE local_invoice (
  uuid            TEXT PRIMARY KEY,          -- 128-bit, generated at creation
  terminal_id     TEXT NOT NULL,
  doc_type        TEXT NOT NULL,             -- STANDARD|SIMPLIFIED|CREDIT|DEBIT
  parent_uuid     TEXT,                      -- required for CREDIT/DEBIT
  icv             INTEGER,                   -- NULL while DRAFT
  pih             TEXT,
  hash            TEXT,
  stamp           TEXT,
  qr_tlv          TEXT,
  xml             BLOB,
  state           TEXT NOT NULL,
  signed_at       TEXT,
  submitted_at    TEXT,
  zatca_uuid      TEXT,
  reject_reason   TEXT,
  payload         BLOB NOT NULL,             -- full sale for cloud replay
  UNIQUE (terminal_id, icv)                  -- gap/duplicate detection
);
```

**Cloud (PostgreSQL — replica + submission tracker):**

```sql
CREATE TABLE zatca_invoice (
  uuid            UUID PRIMARY KEY,
  tenant_id       UUID NOT NULL,
  company_id      UUID NOT NULL,
  terminal_id     UUID NOT NULL,
  doc_type        zatca_doc_type NOT NULL,
  parent_uuid     UUID REFERENCES zatca_invoice(uuid),
  icv             BIGINT NOT NULL,
  pih             TEXT   NOT NULL,
  hash            TEXT   NOT NULL,
  stamp           TEXT   NOT NULL,
  qr_tlv          TEXT   NOT NULL,
  xml             BYTEA  NOT NULL,
  schema_version  TEXT   NOT NULL,           -- E8.4 blocker #3
  state           zatca_state NOT NULL,
  signed_at       TIMESTAMPTZ NOT NULL,
  submitted_at    TIMESTAMPTZ,
  responded_at    TIMESTAMPTZ,
  zatca_uuid      TEXT,
  reject_reason   TEXT,
  retry_count     INT NOT NULL DEFAULT 0,
  next_retry_at   TIMESTAMPTZ,
  UNIQUE (terminal_id, icv)
);

-- Immutability enforced by the database, not by application discipline
CREATE TRIGGER zatca_invoice_immutable
  BEFORE UPDATE OR DELETE ON zatca_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_unless_state_transition();
```

`reject_unless_state_transition()` permits only the state/timestamp/response columns to change, and only along legal transitions. Financial content and chain fields are frozen at signing.

### 4.4 Chain integrity verification

A scheduled job re-walks each device chain and asserts:

- ICVs form an unbroken sequence `1..n` with no gaps and no duplicates
- `invoice[n].pih == invoice[n-1].hash` for every *n*
- every `hash` recomputes from the stored XML
- every stamp verifies against the device public key

Any failure raises a **critical alert** to Owner and Super Admin. This is the mechanism that makes QA gate M2 ("hash chain unbroken across 10,000+ sequential invoices, ICV never resets or gaps") a continuously-monitored property rather than a one-off test.

---

## 5. B2C offline flow (RULE 1) — the fast path

This is the normal showroom flow and **must be the fastest path in the entire application**.

```
Cashier scans items (local SQLite lookup, no network)
  → Cart totals computed locally, VAT per line tax category
  → Tender(s) taken — split across any combination
  → finalize()
      ICV allocated locally · PIH chained · ECDSA stamp applied locally · QR generated
  → COMMIT to local SQLite
  → Receipt prints (ESC/POS, no browser dialog) ✔ legally deliverable immediately
  → state = SIGNED_PENDING_SUBMIT
  → [later, on reconnect] submitted to Fatoora in ICV order → REPORTED
```

**No network call occurs anywhere in the customer-facing path.** The customer never waits. Blueprint J4 budget: cart update under 100 ms, payment completion immediate.

---

## 6. B2B offline flow (RULE 2) — three tenant-selectable behaviours

A Standard Tax Invoice has **no legal standing for the buyer until ZATCA clears it**. The system must never print or email a "final" B2B tax invoice while offline.

`Company.b2b_offline_policy ∈ {BLOCK, DRAFT_HOLD, CONVERT_SIMPLIFIED}`

### Option A — BLOCK (default, safest)

POS refuses to finalize. Cashier sees the exact message specified in the blueprint:

> *"Standard tax invoice requires internet connection. Save as Draft, or issue as Simplified Invoice if the buyer does not require a VAT invoice."*

### Option B — DRAFT_HOLD (recommended for most retail clients)

```
B2B sale created offline
  → saved as DRAFT   (no ICV consumed, no stamp, no legal document produced)
  → goods released only if tenant policy allows, against a document
    clearly labelled "Delivery Note — NOT A TAX INVOICE"
  → on reconnect: invoice generated → cleared by ZATCA → THEN issued to buyer
```

The Delivery Note is a distinct template (blueprint I2 lists it among the nine template types) and must be visually impossible to mistake for a tax invoice.

### Option C — CONVERT_SIMPLIFIED

Many walk-in purchases by small businesses don't actually need a Standard invoice. The POS offers this as a **one-tap option** rather than leaving the cashier stuck. Once converted, RULE 1 applies and the sale completes offline normally.

> ⚠️ Whether ZATCA permits any offline tolerance for Standard invoices is **flagged unverified** in the blueprint itself (line 855). If Tier 1 verification finds a tolerance, this section is amended. Until then the design implements one explicitly decided rule per scenario — never undefined behaviour.

---

## 7. Key custody — resolving the E1.3 / H1 conflict

**The conflict:** E1.3 requires the CSID private key on the terminal. H1 forbids keys in source code or unencrypted `.env` files, and requires a Secret Manager/Vault/KMS.

Both are satisfiable. "On the device" does not have to mean "in a file next to the app."

### Design

| Concern | Mechanism |
|---|---|
| **Key generation** | Key pair generated **on the terminal** during CSID onboarding. The private key never leaves the device — not to the cloud, not to a backup, not to a log |
| **Key storage** | **Windows DPAPI / Credential Manager**, accessed through Tauri's native layer. Encrypted at rest by the OS, scoped to the machine and the app. **Never in the SQLite file**, never in `.env`, never in application code |
| **Key access** | Only the Tauri Rust layer can read it. The React front end can request a *signature*; it can never read the *key* |
| **Cloud KMS role** | Holds **onboarding credentials and the compliance-CSID request flow only** — never the device signing key |
| **Device revocation** | Owner revokes a terminal from Device Management → CSID revoked with ZATCA → local key destroyed on next app start → **the chain remains intact and archived** |
| **Loss of a device** | The chain up to the last synced invoice is preserved in the cloud. Unsynced invoices on a destroyed device are unrecoverable — mitigated by aggressive sync-on-reconnect and the escalating staleness alerts in §9 |

### Honest risk statement

A determined attacker with local administrator rights on a POS terminal can extract a DPAPI-protected key. This is inherent to ZATCA's device-signing model, not a flaw in this design — the same exposure exists for every compliant Saudi POS. It is mitigated by machine-scoped DPAPI, device revocation, chain verification that detects forged entries, and audit alerting. **A hardware security module or TPM-backed key is the correct upgrade path** if a client's threat model demands it, and the key-custody interface is designed so that swap requires no change above the Rust layer.

---

## 8. Fatoora submission

### 8.1 Submitter design

- **Strictly serial per device**, honouring ICV order (§3.3). Parallel across devices.
- Clearance endpoint for Standard; reporting endpoint for Simplified.
- Every request and response persisted for audit — a compliance record, not a debug log.
- Idempotent: re-submitting the same UUID after an ambiguous timeout must not create a duplicate. The submitter first queries ZATCA for the UUID's status when a prior attempt timed out.

### 8.2 Retry policy

| Attempt | Delay |
|---|---|
| 1 | immediate |
| 2 | 30 s |
| 3 | 2 min |
| 4 | 10 min |
| 5 | 1 h |
| 6+ | 6 h, indefinitely |

**Never give up, never silently drop.** Blueprint E1.2 item 8 is explicit. QA gate M2 requires the retry queue to recover correctly after a **simulated 24-hour outage** — with this schedule, a 24 h outage results in ~8 attempts and full recovery on reconnection, with no manual intervention.

Transport failures retry. **Business rejections do not** — they go to `REJECTED` and require a Credit Note.

### 8.3 Rejection handling

```
ZATCA rejects invoice ICV 4,182
  → state = REJECTED, reason stored verbatim
  → ICV 4,182 stays in the chain, is NEVER reused
  → CRITICAL alert to Owner + Super Admin compliance watch
  → Owner corrects by issuing a Credit Note referencing the original
  → chain continues at ICV 4,183 undisturbed
```

Deleting a rejected invoice would create exactly the gap ZATCA's tamper detection looks for. The design makes deletion impossible at the database level.

---

## 9. Staleness alerting (RULE 6)

No cap on offline B2C invoice count — the POS must survive a multi-day outage. But an unreported backlog is a legal exposure, so it escalates:

| Age of oldest unsubmitted invoice | Level | Surfaced to |
|---|---|---|
| > 12 hours | **Notice** | Owner dashboard |
| > 24 hours | **Warning** | Owner dashboard + email/push |
| > 72 hours | **Critical** | Owner + **Super Admin compliance watch** |

The blueprint's rationale is worth keeping in view: *"a client sitting on thousands of unreported invoices is a legal problem you want to catch before they do."* Thresholds are deliberately conservative because the reporting deadline for simplified invoices is tight (currently 24 hours) — and that window is a **registry value**, not a constant.

---

## 10. Credit and Debit Notes

- Always reference the original invoice (`parent_uuid`, NOT NULL for these types).
- Follow the **parent's** model: a note against a Standard invoice needs clearance; against a Simplified invoice, reporting.
- **Consume their own ICV** on the issuing device — they are legally issued documents.
- Are the **only** mechanism for correcting a finalized invoice. There is no edit, no void, no delete.
- Trigger the full 9-effect accounting reversal in `02-posting-engine.md` §Returns.

---

## 11. Numbering vs ICV — keep them separate

Blueprint I3 flags this precisely: *"the numbering engine and the ZATCA ICV are related but not the same thing, and the design must not let a 'friendly' custom invoice number break the mandatory tamper-evident counter underneath it."*

| | Human invoice number | ICV |
|---|---|---|
| Example | `INV-RYD-000123` | `4182` |
| Scope | Configurable per store / type / fiscal year | **Per terminal, always** |
| Resets | May reset each fiscal year | **NEVER resets** |
| Purpose | Human readability, tenant convention | ZATCA tamper detection |
| Owner | Numbering engine | ZATCA engine |

They are **separate columns**, generated by separate components. The numbering engine can never influence ICV allocation.

---

## 12. Compliance is derived, not toggled

```
Company.country == 'SA'
  AND Company.vat_registered == true
  AND Company.zatca_obligation_status IN ('WAVE_NOTIFIED', 'LIVE')
      ⇒ ZATCA engine ENABLED — no toggle exists to turn it off
```

Per blueprint A4's HARD RULE and v2.2 correction #1, this **overrides** H5's listing of "ZATCA module" as a plan entitlement. There is no Super Admin switch, no feature flag, no plan tier that disables e-invoicing for an obligated tenant. Only compliance **monitoring** (multi-branch analytics, extended audit export, deadline dashboards, priority alerting) is premium.

Obligation status is **captured as tenant configuration from the taxpayer's own ZATCA notification** — never inferred by the software, never asserted by sales.

---

## 13. Acceptance criteria (QA gate M2 + M3)

| # | Criterion | How the design satisfies it |
|---|---|---|
| 1 | SDK validation passes for Standard, Simplified, Credit, Debit | Separate builders per doc type; SDK validation wired into CI |
| 2 | **Hash chain unbroken across 10,000+ sequential invoices** | Atomic ICV+PIH transaction; `UNIQUE(terminal_id, icv)`; continuous verification job |
| 3 | **ICV never resets or gaps** | Non-resetting counter per terminal; drafts consume none; rejects retain theirs; DB uniqueness constraint |
| 4 | QR scans correctly in ZATCA's verification app | TLV built from registry-defined field set; validated in CI against SDK |
| 5 | Sandbox clearance **and** reporting both succeed | Separate code paths per model, both exercised in integration tests |
| 6 | **Retry queue recovers after simulated 24-hour outage** | Indefinite backoff; ordering guard; no drop path exists |
| 7 | **500 invoices sold fully offline → correct chain order** | Chain is authoritative on the device; submitter replays strictly in ICV order |
| 8 | 6-year archive record retrievable, hash still validates | Immutable archive stores XML + hash + schema version; verification job re-validates |

---

## 14. Open items — blocked on Tier 1 verification

These are **data**, not code. The machinery above accepts them as registry values.

| # | Item | Registry key | Source |
|---|---|---|---|
| 1 | Exact UBL 2.1 field set | `SA.ZATCA.XML_SCHEMA_VERSION` | ZATCA XML Implementation Standard |
| 2 | TLV QR field set and byte layout | `SA.ZATCA.QR_TLV_FIELDS` | ZATCA Security Features Standard |
| 3 | Hash algorithm and canonicalization | `SA.ZATCA.HASH_ALGORITHM` | ZATCA Security Features Standard |
| 4 | Simplified reporting window (assumed 24 h) | `SA.ZATCA.REPORTING_WINDOW_HOURS` | ZATCA Detailed Guideline |
| 5 | Whether any offline tolerance exists for Standard | `SA.ZATCA.STANDARD_OFFLINE_TOLERANCE` | ZATCA Detailed Guideline |
| 6 | CSID renewal interval | `SA.ZATCA.CSID_RENEWAL_DAYS` | ZATCA onboarding docs |

> Blueprint's own instruction: *"Do not let developers fill these in from assumption."* Every one of these is a placeholder in the registry with `verified_on = NULL` until a human checks it against the official source.
