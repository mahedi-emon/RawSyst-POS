# The nested-transaction pool deadlock (found and fixed 2026-08-31)

## What it was

A request that held a transaction asked the pool for a SECOND connection while
still holding the first. With `MaxConns` sales in flight, every connection was
held by a sale and every sale waited for one that only another could release.

Acquiring from a pgx pool has **no deadline**, so there was no error, no timeout
and no log line. Every till in the shop stopped mid-sale and stayed stopped.

## Where

`registry.Resolve` opened its own `TxAsTenant` / `pool.Raw().Begin`. Four call
paths reached it from inside a transaction:

- `sales.applyTaxProfile` ← `FinalizeInTx` ← `RingUp` (the POS checkout)
- `sales.applyTaxProfile` ← the exchange path (`exchange.go`)
- `expenses.price` — already had a `tx` in hand and ignored it
- `catalog.checkTreatment` ← `CreateProduct`

## The fix

`registry.Query` gained a `Tx pgx.Tx` field. When set, the lookup runs on the
caller's connection instead of asking the pool. `TaxRulesFor` and `VATRate` take
a `tx` and forward it. Registry rows are read-only reference data, so joining
the caller's transaction takes no lock and cannot deadlock in the database.

## Why it hid for so long

The registry **cache**. A warm cache answers without touching the database, so
the second connection is never requested. The miss window is exactly when load
is highest and identical across tills: a fresh deployment, a registry write that
invalidates, or the first sale of a new month.

`TestConcurrentSalesTakeDistinctChainPositions` (8 tills, pool of 8) hit it
every run — it hung 30 minutes and blew the suite timeout. It read as a slow
test rather than a deadlock, because the panic names the WaitGroup, not the pool.

## The guard

`TestASaleDoesNotHoldTwoConnections` — fills the pool exactly, cold cache,
asserts only that requests return. Verified to genuinely catch it: disabling the
fix makes it hang; restoring it passes in 0.54s.

**Rule for every new module:** a service method that already receives a `tx`
must never call something that opens its own. When a helper needs the database
and the caller is in a transaction, pass the `tx` down.

## Related
[[code/module-status]] · [[code/pillars-status]]
