# 03 — Sync & Idempotency Model

> **PILLAR 3 of 3.** Blueprint H2: *"The single most important POS engineering rule: selling must never stop just because the internet stopped."*

**Binding source:** Blueprint H2, J3, J4, and QA gates M2/M3.
**Acceptance gate (QA M3):** Sell **500 invoices with internet fully disconnected** → reconnect → verify **zero duplicates, zero lost invoices, correct hash chain order, correct ZATCA submission**.

---

## 1. The core insight

Most POS "offline modes" treat the cloud as authoritative and the terminal as a cache that queues writes. That model cannot satisfy this system's requirements, because ZATCA's ICV/PIH chain is **generated and signed on the device** (see `01-invoice-zatca-engine.md` §1). The chain *is* the legal record, and it exists on the terminal first.

So the model here is inverted:

> **For sales, the terminal is the system of record. The cloud is a replica and a submitter.**
> **For master data, the cloud is the system of record. The terminal holds a cached snapshot.**

This split determines every conflict rule below. Sales flow **up** and never conflict. Master data flows **down** and never conflicts. The only genuinely contended resource is **stock quantity**, and §6 deals with it explicitly.

---

## 2. What lives on the terminal

Local SQLite (blueprint H2), all usable with **zero internet**:

| Data | Direction | Authority |
|---|---|---|
| Products, variants, prices, barcodes, tax categories | ↓ pulled | Cloud |
| Stock snapshot | ↓ pulled, ↑ adjusted locally | Cloud (reconciled) |
| Customers | ↓ pulled, ↑ new ones pushed | Cloud |
| POS configuration, terminal settings, receipt template | ↓ pulled | Cloud |
| **Completed sales, payments, ZATCA chain** | ↑ pushed | **Terminal** |
| Held carts | local only | Terminal (never synced) |
| Sync queue | local only | Terminal |
| **Device CSID private key** | never leaves | Terminal (OS keystore, not SQLite) |
| Registry values needed offline (VAT rate, reporting window) | ↓ pulled, effective-dated | Cloud |

Registry values are cached **with their effective dates**, so an offline terminal selling on 3 March uses March's VAT rate even if the cache was populated in January.

---

## 3. Idempotency

### 3.1 The UUID contract

Blueprint H2: *"every transaction carries a UUID generated at creation time; if that UUID already exists in the cloud, the sync engine must not create a duplicate — this is what makes 'sell offline all day, sync at night' safe."*

Every syncable record gets a **v4 UUID at the moment of creation on the device**, before any network involvement. The UUID is the primary key in both SQLite and PostgreSQL. It is never regenerated, never reassigned on retry.

### 3.2 Three layers of protection

Defence in depth, because at-least-once delivery means duplicates *will* be attempted:

| Layer | Mechanism | Catches |
|---|---|---|
| 1 — Transport | `Idempotency-Key` header per batch | Whole-batch replay after an ambiguous timeout |
| 2 — Database | `INSERT … ON CONFLICT (uuid) DO NOTHING` | Individual record replay |
| 3 — Accounting | `UNIQUE (source_type, source_id, rule_key)` on `journal_entry` | Double-posting even if a record somehow re-inserts |

Layer 3 is the important one. It means that even a bug in layers 1 and 2 cannot corrupt the general ledger — the worst case is a rejected insert, not a doubled trial balance.

### 3.3 Response semantics

The sync endpoint returns per-record status, so the client can distinguish outcomes:

```json
{ "results": [
  { "uuid": "…", "status": "accepted" },
  { "uuid": "…", "status": "duplicate_ignored" },
  { "uuid": "…", "status": "rejected", "code": "PERIOD_CLOSED", "retryable": false }
]}
```

`duplicate_ignored` is a **success** from the client's perspective — it clears the queue entry. Treating it as a failure is how retry storms start.

---

## 4. Sync protocol

### 4.1 Shape

Two independent channels, deliberately not coupled:

```
PUSH  terminal → cloud    sales, payments, ZATCA chain, new customers, stock adjustments
PULL  cloud → terminal    products, prices, stock corrections, customers, config, registry
```

Push runs first on reconnect. Pull is safe to run at any time.

### 4.2 Push

```
POST /api/v1/sync/push
Idempotency-Key: <batch-uuid>
{
  "device_id": "…",
  "cursor": 41822,                       // client's last acknowledged seq
  "records": [ { "uuid": …, "seq": 41823, "type": "SALE", "payload": {…} }, … ]
}
```

**Ordering guarantee:** records carry a monotonic per-device `seq`. The server processes them **strictly in `seq` order** and stops at the first non-retryable failure, returning the last successfully applied `seq`. The client resumes from there.

This matters because of ZATCA. Blueprint E1.3 RULE 4: *"The hash chain must be submitted in ICV order. The sync engine must preserve ordering per device, not submit in arrival order."* Processing out of order would submit ICV 4,183 before 4,182 and break the chain.

**Batch size: 100 records.** Large enough to drain 500 offline invoices in 5 round-trips; small enough that a failure retries cheaply.

### 4.3 Pull

```
GET /api/v1/sync/pull?device_id=…&since=<watermark>
```

Watermark-based, using a server-side monotonic `updated_seq` per tenant — **not wall-clock timestamps**, which are unreliable across machines with drifting clocks. Response is paginated and includes tombstones for deletions.

### 4.4 Queue schema

```sql
CREATE TABLE sync_queue (
  seq           INTEGER PRIMARY KEY AUTOINCREMENT,   -- per-device ordering
  uuid          TEXT NOT NULL UNIQUE,
  entity_type   TEXT NOT NULL,
  payload       BLOB NOT NULL,
  created_at    TEXT NOT NULL,
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT,
  state         TEXT NOT NULL DEFAULT 'PENDING'      -- PENDING|SENT|ACKED|FAILED
);
```

Entries move to `ACKED` only on explicit server acknowledgement, then are pruned after a retention window (kept briefly for support diagnostics).

---

## 5. Conflict policy — per entity, decided in advance

Blueprint H2 names five detection mechanisms (ordered events, offline timestamps, device identity, per-device sync cursor, and a defined resolution rule) but leaves the per-entity policy open. Deciding it per entity, in advance, is how "a clearly defined resolution rule" becomes real.

| Entity | Policy | Rationale |
|---|---|---|
| **Sale / Invoice** | **Append-only — cannot conflict** | Terminal is authoritative. Cloud never modifies a synced sale |
| **ZATCA chain entry** | **Append-only, per-device — cannot conflict** | Chains are never merged or re-sequenced (E1.3 RULE 5) |
| **Payment** | **Append-only** | Child of a sale |
| **Stock quantity** | **Movement-based, never absolute** | See §6 — this is the only genuinely contended resource |
| **Product / price / tax category** | **Cloud wins** | Master data. A terminal never edits a product |
| **Customer — new, created at POS** | **Append-only**, deduplicated by phone number | Two terminals may create the same walk-in customer |
| **Customer — edits** | **Last-write-wins**, with the loser retained in audit | Low-stakes; blueprint permits LWW with flagged review |
| **Customer credit balance** | **Movement-based**, server-authoritative | Financial — must not be LWW |
| **Terminal settings** | **Cloud wins** | Administered centrally |
| **Held cart** | **Never synced** | Local ephemeral state |
| **Fiscal-period-closed record** | **Server rejects, non-retryable** | Requires human decision — surfaces in the exception queue |

**The principle:** no financial quantity is ever resolved by last-write-wins. Blueprint H2 permits *"manual reconciliation for financial records"*, and that is what the exception queue provides.

---

## 6. Stock reconciliation across offline terminals

**The problem.** Three terminals are offline. Each holds a cached snapshot showing 10 units of a shirt. Each sells 4. Total sold: 12. Actual stock: 10. No terminal did anything wrong.

**Why absolute quantities cannot work.** If each terminal syncs "stock is now 6", the last writer wins and the cloud shows 6 when it should show −2. The information about *what happened* is lost.

**The design: movements, not levels.**

```sql
CREATE TABLE stock_movement (
  uuid          UUID PRIMARY KEY,           -- device-generated, idempotent
  tenant_id     UUID NOT NULL,
  item_id       UUID NOT NULL,              -- variant-level
  warehouse_id  UUID NOT NULL,
  delta         NUMERIC(18,4) NOT NULL,     -- −4, not "6"
  reason        movement_reason NOT NULL,   -- SALE|RETURN|GRN|ADJUST|TRANSFER|WASTAGE
  source_id     UUID NOT NULL,
  device_id     UUID,
  occurred_at   TIMESTAMPTZ NOT NULL,       -- device clock, for ordering only
  recorded_at   TIMESTAMPTZ NOT NULL        -- server clock, authoritative
);
```

Stock level is **always** `SUM(delta)`, never a stored mutable number. Deltas commute — they can arrive in any order from any device and the total is identical. Three `−4` movements sum to `−12` regardless of arrival sequence.

**Overselling is detected, not prevented.** When the sum goes negative, the server:

1. Applies the movements anyway (the goods physically left the shop — refusing to record that would make the books lie)
2. Raises an **oversell exception** naming the item, the terminals, and the quantity
3. Applies the tenant's `negative_stock_policy` — `BLOCK` tenants get a critical alert; `ALLOW_WARN` tenants get the warning and the cost auto-corrects on next receipt

**Prevention** is a separate, best-effort mechanism: while online, terminals receive stock deltas via WebSocket in near-real-time, so the window for concurrent oversell is seconds rather than hours. Offline, prevention is impossible by definition — the design chooses accurate detection over false confidence.

### Snapshot rebase

On pull, the terminal receives an authoritative level plus a server watermark, and rebases:

```
local_level = server_level + SUM(local deltas not yet acknowledged)
```

This converges correctly even if a push succeeded but its acknowledgement was lost.

---

## 7. Clock skew

Device clocks drift and can be changed by users. The design never trusts them for anything consequential.

| Purpose | Clock used |
|---|---|
| Ordering within one device | **`seq` counter** — monotonic, not a clock |
| Ordering across devices | **Server `recorded_at`** |
| Invoice timestamp on the ZATCA QR | Device clock — *unavoidable*, it is signed locally |
| Fiscal period assignment | **Server-side**, from the invoice date, validated on sync |
| Registry rule resolution | Transaction date, validated against a plausible range |

The invoice timestamp is the one genuine exposure: it is inside the signed payload, so it must come from the device. Mitigation — the terminal syncs time via NTP whenever online, records skew at each sync, and **raises an alert when skew exceeds 5 minutes**. A sale whose timestamp is implausible (future-dated, or before the previous invoice on the same chain) is flagged for review on sync rather than silently accepted.

---

## 8. Satisfying the 500-invoice test by construction

QA gate M3 walked through against this design:

| Requirement | How it holds |
|---|---|
| **Sell 500 invoices fully disconnected** | Nothing in the checkout path touches the network. Products, prices, tax rates, and the CSID key are all local. Chain allocation is a local SQLite transaction |
| **Zero duplicates** | UUID at creation + `ON CONFLICT DO NOTHING` + `UNIQUE(source_type, source_id, rule_key)` on the journal. Three independent layers |
| **Zero lost invoices** | The sale is durable in SQLite **before the receipt prints**. Queue entries clear only on explicit ACK. Failed entries retry indefinitely and are visible to Owner and Super Admin — never silently dropped |
| **Correct hash chain order** | `seq` is allocated in the same transaction as the ICV, so queue order *is* ICV order. The server processes strictly in `seq` order and stops on first failure |
| **Correct ZATCA submission** | Submitter is strictly serial per device with the guard "all lower ICVs already submitted" (`01-…` §3.3) |

**Throughput check:** 500 records ÷ 100 per batch = 5 round-trips. At a pessimistic 2 s per batch, full drain is ~10 seconds. The 24-hour-outage recovery test (M2) at a busy store — say 2,000 invoices — drains in well under a minute.

---

## 9. Failure visibility

Blueprint H2: failed syncs *"are visible to Super Admin/Owner, never silently lost."*

| Surface | Shows |
|---|---|
| POS status bar | Online/offline, queue depth, last successful sync |
| Owner dashboard | Per-terminal sync health, oldest unsynced invoice age |
| Compliance screen | Unsubmitted ZATCA count with the 12h/24h/72h escalation |
| Super Admin | **Per-tenant sync queue depth** and failed-submission counts platform-wide (blueprint A4) |
| Exception queue | Oversells, rejected records, closed-period conflicts — each needing a human decision |

The exception queue is the honest admission in this design: distributed systems produce situations software cannot resolve alone. Rather than guessing, those cases are surfaced with enough context for a person to decide.

---

## 10. Device lifecycle

Each terminal registers as a Device (blueprint H3) with `Device ID · Store ID · Terminal ID · OS · app version · last sync time · last active time · assigned cashier · linked printer config`.

| Event | Behaviour |
|---|---|
| **Registration** | Device row created → CSID onboarding → chain initialised at ICV 0 → initial full pull |
| **Revocation** | Push accepted one final time to drain the queue, then blocked. CSID revoked. Local key destroyed. **Chain preserved and archived** |
| **Reassignment to another store** | Requires an empty queue. **A new chain is NOT started** — ICV continues, because the chain belongs to the device under its company's VAT registration |
| **App update** | Forced from Device Management; schema migrations run against local SQLite on next start |
| **Loss / destruction** | Synced invoices are safe in the cloud. Unsynced ones are unrecoverable — the reason staleness alerting is aggressive |

---

## 11. Judgment calls made here

| Call | Alternative rejected | Why |
|---|---|---|
| Terminal is authoritative for sales | Cloud authoritative, terminal queues writes | ZATCA chain is generated and signed on the device; the cloud cannot be its origin |
| Stock as movement deltas | Absolute quantity sync | Absolute levels lose information and let last-write-wins corrupt inventory |
| Oversells detected, not prevented | Reserve stock centrally before sale | Central reservation requires a network call in the checkout path — violates A2 #3 and the sub-100 ms budget |
| Strict `seq` ordering, stop on first failure | Parallel batch processing | ICV order is a legal requirement, not a preference |
| No financial value uses last-write-wins | LWW everywhere for simplicity | H2 explicitly permits "manual reconciliation for financial records"; silent LWW on money is indefensible |
| Server-side monotonic watermark for pull | Timestamp-based pull | Clock drift across machines makes timestamp watermarks lose records |
| Held carts never sync | Sync them for cross-terminal resume | Adds conflict surface for an ephemeral convenience; blueprint scopes hold/resume to the terminal |

---

## 12. Open items

| # | Item | Depends on |
|---|---|---|
| 1 | WebSocket stock-delta broadcast — nice-to-have prevention layer, not required for correctness | Phase 1 completion |
| 2 | Compression for large initial pulls (500k SKU tenants) | Enterprise tier |
| 3 | Whether the terminal should pull a *partial* catalogue (assigned warehouse only) at high SKU counts | Measured performance at scale |
