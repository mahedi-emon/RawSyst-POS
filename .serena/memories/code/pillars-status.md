# The three pillars — status

Blueprint line 1658: "Design these three first, because every other module depends on them. Get those three right and the remaining modules are conventional work. Get them wrong and the entire system has to be rebuilt."

| Pillar | State | Gate |
|---|---|---|
| 1. Invoice chain (ZATCA) | ✅ built, `internal/zatca` | **M2 passed** — 10,000 invoices, no reset, no gap |
| 2. Double-entry posting | ✅ built, migration 0015 | **M1 passed** — balance, immutability, period lock |
| 3. Sync & idempotency | ⬜ next | M3 — 500 offline invoices, zero duplicates |

## Pillar 1 — what is done and what is deliberately not
Chain STRUCTURE is complete and verified: SHA-256, per-EGS-unit sequential non-resetting counter, PIH linkage, rejected invoices keeping their position.

The BYTES are not. `SA.ZATCA.QR_TLV_FIELDS` is still an unverified release blocker, so `Chain` takes a `DocumentHasher` interface instead of building XML. **Do not implement that hasher from a blog post or an example** — it needs the XML Implementation Standard and the Security Features Standard. Wrong bytes give a chain that is internally consistent and rejected at scale, which looks correct until thousands of invoices fail.

Server-side allocation (`Allocate`) is for centralized/branch units. A smart POS owns its counter offline and reports it via `RecordTerminalSigned`, which refuses replays, gaps and broken linkage with distinct messages. The high-water mark only moves forward — a device restored from backup must not drag it back.

## Pillar 2 — where the guarantees actually live
Both in the database, not in Go:
- `journal_line_balanced` — CONSTRAINT TRIGGER, **DEFERRABLE INITIALLY DEFERRED**. Must be deferred: lines insert one at a time and the entry is unbalanced in between.
- `journal_entry_immutable` / `journal_line_immutable` — reject_always
- `journal_entry_period_open` — closed period refuses, and entry_date must fall inside its period
- `journal_entry_source_uq` — idempotency on (source_type, source_id, rule_key)

Posting rules name **account roles**, mapped per company via `account_role_map`. That is what lets one rule row serve every company and every country: a Saudi purchase debits Input VAT, a US purchase has none.

Base currency is what balances. A separate constraint stops a line debiting in transaction currency while crediting in base — that would balance arithmetically while recording the opposite.

## Bugs these tests caught (worth remembering the pattern)
- `Chain.Verify` used `pool.Raw()`, so RLS hid every row and it reported **every chain intact**. A verifier that sees nothing while reassuring you is worse than none.
- Tests attempting mutations via `TxAsPlatform` on tenant-scoped tables matched zero rows and "succeeded", making immutability look broken when it was the test at fault.

**Pattern:** on a tenant-scoped table, an unscoped connection silently sees nothing. Always ask whether a query returned nothing because there is nothing, or because it could not look.

## Related
[[code/backend-state]] · [[design/index]] · [[architecture/target-markets]]
