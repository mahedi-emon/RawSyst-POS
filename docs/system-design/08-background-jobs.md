# 08 — Background Jobs

Blueprint H9 states the purpose plainly: background workers exist "so none of
these ever block a cashier's checkout screen." Blueprint A2 #8 makes it a
principle — heavy reports run asynchronously and the POS never freezes.

---

## 1. What belongs here, and what does not

The distinction matters more than it looks, because putting the wrong thing in a
queue breaks the trial balance.

| | Synchronous (event bus, same transaction) | Asynchronous (job queue) |
|---|---|---|
| Examples | Journal postings · stock movements · audit entries | ZATCA submission · reports · email/SMS · backups · settlement import |
| Failure | Rolls back the originating write | Retries independently, alerts on exhaustion |
| Guarantee | Exactly once, atomically | At least once |

**A sale and its journal entry must commit together or not at all.** If posting
were asynchronous, a sale could exist without its accounting entry and the trial
balance would drift — failing QA gate M1 intermittently, which is worse than
failing consistently because it looks like a rounding quirk.

ZATCA submission is the opposite case. It **must** be asynchronous: the invoice
is already signed and legally delivered to the customer (blueprint E1.3 RULE 1),
and the network may be down for days. Blocking the sale on it would violate the
hard offline-first requirement.

---

## 2. Queue design

PostgreSQL-backed, using `SELECT … FOR UPDATE SKIP LOCKED`. Redis is available
but the job table is deliberately in the same database as the business data:

- A job enqueued in the same transaction as its trigger cannot be orphaned by a
  crash between the two writes.
- Job state is queryable with SQL, which matters when the Owner asks why an
  invoice has not reached ZATCA.
- One fewer moving part for a solo operator. Redis stays for sessions, rate
  limiting and caching, where losing state is survivable.

```sql
CREATE TABLE job (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid,                       -- NULL for platform jobs
  kind          text NOT NULL,
  payload       jsonb NOT NULL,

  -- Ordering key. Jobs sharing a queue_key run strictly in sequence, never in
  -- parallel. ZATCA submission uses the device id, because the hash chain must
  -- be submitted in ICV order (E1.3 RULE 4).
  queue_key     text,

  state         job_state NOT NULL DEFAULT 'pending',
  priority      smallint NOT NULL DEFAULT 100,   -- lower runs first
  attempts      integer  NOT NULL DEFAULT 0,
  max_attempts  integer  NOT NULL DEFAULT 25,
  run_after     timestamptz NOT NULL DEFAULT now(),
  locked_at     timestamptz,
  locked_by     text,
  last_error    text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,

  -- Idempotency: the same logical job cannot be enqueued twice.
  dedupe_key    text
);

CREATE UNIQUE INDEX job_dedupe_uq ON job (dedupe_key)
  WHERE dedupe_key IS NOT NULL AND state IN ('pending','running');

CREATE INDEX job_claim_idx ON job (state, run_after, priority)
  WHERE state = 'pending';
```

### Claiming

```sql
UPDATE job SET state = 'running', locked_at = now(), locked_by = $1,
               attempts = attempts + 1
WHERE id = (
  SELECT j.id FROM job j
  WHERE j.state = 'pending' AND j.run_after <= now()
    -- Serialise within a queue_key: skip if an earlier sibling is still running
    AND NOT EXISTS (
      SELECT 1 FROM job o
      WHERE o.queue_key = j.queue_key AND o.queue_key IS NOT NULL
        AND o.state = 'running'
    )
  ORDER BY j.priority, j.run_after
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING *;
```

`SKIP LOCKED` lets several workers claim different jobs concurrently without
blocking each other. The `NOT EXISTS` clause is what keeps ZATCA submissions
strictly ordered per device while remaining parallel *across* devices.

---

## 3. Job types

| Kind | Queue key | Priority | Max attempts | Notes |
|---|---|---|---|---|
| `zatca.submit` | `device:{id}` | 10 | **unlimited** | Never gives up. See §4. |
| `zatca.verify_chain` | `device:{id}` | 80 | 5 | Nightly chain integrity walk |
| `sync.process_batch` | `device:{id}` | 20 | 10 | Ordered by `seq` |
| `accounting.tie_out` | `company:{id}` | 70 | 3 | Nightly sub-ledger vs control account |
| `report.generate` | — | 50 | 3 | Result to object storage, notify on done |
| `report.scheduled` | — | 60 | 3 | Daily sales, VAT, stock, payables |
| `notify.send` | — | 40 | 8 | In-app, email, SMS, push |
| `backup.run` | `tenant:{id}` | 90 | 3 | Encrypted, then **verified** |
| `settlement.import` | `company:{id}` | 60 | 5 | Gateway batch reconciliation |
| `registry.staleness_check` | — | 95 | 3 | Weekly `verified_on` audit |
| `pdpl.retention` | `tenant:{id}` | 85 | 3 | Respects legal hold |
| `pdpl.dsr_deadline` | — | 30 | 5 | Warns before the 30-day SLA lapses |

---

## 4. ZATCA submission — the job that must never give up

Blueprint E1.2: "a failed submission must **never** be silently dropped; it
retries automatically and raises a **critical alert**."

```
attempt 1  immediate
attempt 2  30s
attempt 3  2m
attempt 4  10m
attempt 5  1h
attempt 6+ 6h, indefinitely
```

`max_attempts` is effectively unlimited for this kind. There is no dead-letter
path that discards the job, because an unreported invoice is a legal exposure
that does not stop being one after twenty-five tries.

QA gate M2 requires recovery after a **simulated 24-hour outage**. With this
schedule a 24-hour outage produces roughly eight attempts and then succeeds on
reconnection, with no human intervention.

**Transport failures retry. Business rejections do not** — a rejected invoice
moves to `REJECTED`, keeps its ICV, raises a critical alert, and is corrected by
credit note. Retrying a rejection would never succeed and would mask the alert.

### Ordering

`queue_key = device:{id}` with the serialisation clause above guarantees ICV
order. Blueprint E1.3 RULE 4: "The hash chain must be submitted in ICV order.
The sync engine must preserve ordering per device, not submit in arrival order."

Submitting ICV 4,183 before 4,182 breaks the chain — the exact tamper signal
ZATCA looks for.

---

## 5. Staleness alerting

Two independent clocks, both required by the blueprint.

### Unsubmitted invoices (E1.3 RULE 6)

| Age of oldest unsubmitted | Level | Goes to |
|---|---|---|
| > 12 h | Notice | Owner dashboard |
| > 24 h | Warning | Owner dashboard + email/push |
| > 72 h | **Critical** | Owner **and** Super Admin compliance watch |

The blueprint's reasoning is worth keeping in view: "a client sitting on
thousands of unreported invoices is a legal problem you want to catch before
they do." Thresholds are conservative because the simplified-invoice reporting
window is tight — and that window is a **registry value**, not a constant, so
the thresholds derive from it rather than hard-coding 24.

### Registry verification (E8.3)

Weekly. Flags any rule whose `verified_on` is older than 6 months (tax and
payroll) or 12 months (others), or is `NULL`. Emailed as well as displayed,
because a passive flag nobody looks at achieves nothing.

---

## 6. Nightly integrity jobs

These are how the QA gates become continuously-monitored properties rather than
one-off launch tests.

| Job | Asserts | Gate |
|---|---|---|
| `accounting.tie_out` | AR sub-ledger = AR control · AP = AP control · **inventory valuation = Inventory GL balance** | M1 |
| `zatca.verify_chain` | ICV sequence unbroken · `pih[n] == hash[n-1]` · every hash recomputes · every stamp verifies | M2 |
| `archive.verify` | A sampled six-year-old record is retrievable **and its hash still validates** | M9 |

Any failure raises a critical alert with enough drill-down to locate it.
Blueprint C13 requires exactly this for inventory: "any divergence is flagged as
an exception."

---

## 7. Scheduling

A single leader worker owns cron-style scheduling, elected by PostgreSQL
advisory lock. Simpler than a distributed scheduler and sufficient at this
scale; if the leader dies another acquires the lock within its lease.

| Schedule | Job |
|---|---|
| Every minute | ZATCA retry sweep, staleness evaluation |
| Every 15 min | Settlement status polling |
| Hourly | Sync health check |
| Daily 02:00 tenant-local | Backup, then verify |
| Daily 03:00 | PDPL retention (respecting legal hold) |
| Daily 04:00 | Accounting tie-out, chain verification |
| Weekly Sunday | Registry staleness, archive spot-check |
| Monthly | Fiscal period close reminder, VAT deadline countdown |

Tenant-local scheduling matters: a Riyadh backup at 02:00 UTC would run at
05:00 local, during setup for the trading day.

---

## 8. Failure handling and visibility

Blueprint H2: failed work is "visible to Super Admin/Owner, never silently
lost."

| Surface | Shows |
|---|---|
| Owner dashboard | Failed jobs affecting them, unsubmitted invoice age |
| Compliance screen | ZATCA queue depth with the 12/24/72 h escalation |
| Super Admin health | Failed jobs platform-wide, **failed ZATCA across all tenants**, per-tenant sync queue depth |
| Exception queue | Items needing a human decision — oversells, rejections, closed-period conflicts |

A job that exhausts `max_attempts` moves to `failed` and stays in the table. It
is never deleted: the record that something did not happen is itself
operationally important.

---

## 9. Worker operation

Workers are the same binary as the API (`cmd/worker`), so there is one build,
one config path, one deployment story.

- **Graceful shutdown**: on SIGTERM, stop claiming, finish in-flight work, then
  exit. A job killed mid-flight is retried, but finishing cleanly avoids
  duplicate side effects such as a second SMS.
- **Poison-pill protection**: a job that panics is caught, recorded with its
  stack, and retried with backoff. One bad payload must not take down the pool.
- **Long jobs heartbeat** `locked_at`; a lock older than the lease is reclaimed,
  so a crashed worker's jobs do not stall forever.
- **Concurrency** defaults to 4 per worker, tunable. ZATCA submission is
  effectively serial per device regardless, by design.

---

## 10. Judgment calls

| Call | Alternative rejected | Why |
|---|---|---|
| Postgres queue, not Redis/RabbitMQ | Dedicated broker | Enqueue in the same transaction as the trigger; SQL-queryable state; one fewer thing to operate alone |
| Journal posting is synchronous | Post via the queue | Async posting permits a sale with no journal entry, breaking M1 intermittently |
| ZATCA retries forever | Dead-letter after N | An unreported invoice stays a legal exposure after twenty-five tries |
| Ordering via `queue_key` | Global ordering, or none | Per-device order is a legal requirement; global order would serialise the whole platform |
| Leader election by advisory lock | Distributed scheduler | Adequate at this scale, and far less to debug |
| Failed jobs retained | Purge after N days | The absence of a job is not evidence; a failed row is |
