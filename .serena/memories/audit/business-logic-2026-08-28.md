# Business-logic audit, 2026-08-28

Re-derived from the Blueprint and the design docs rather than from the existing
tests. Four defects, all fixed at the root, all with regression tests that were
first shown to FAIL against the old code.

## The method that found all four

Assert against the **definition**, not against a field list or against the code
agreeing with itself.

- The blind-close test reads `cash_session_expected` out of the database and
  walks every add/subtract combination of what the cashier's response returned.
  It found a second route to the figure that reasoning alone had missed.
- The fee-allocation test sweeps 4,000 batches at four decimal places rather
  than naming a worked example. Every hand-written case sat on the hallala grid,
  where the defect provably cannot occur.
- The race tests wait on `pg_stat_activity` until the other connection is really
  blocked. A goroutine started and immediately released races against its own
  subject and passes whether or not the defect is present — both race tests did
  exactly that on the first attempt and had to be rewritten.

## P75 — the blind close published its own addends

`expected = opening_float + cash_takings + cash_movements`, and the cashier's
peek (`GET /api/v1/shifts/{id}`, on `sales.receive_payment`) returned all three;
the POS shift panel listed them under a comment saying the expected drawer was
deliberately withheld. `non_cash_takings` was a second route:
`gross − refunds − non_cash = cash takings` for a cash-and-card shop.

All four withheld while a blind session is open, in `shift.withholdTheDrawer`.
The three POS/back-office types became optional; a screen defaulting them to
'0' would undo the control.

## P76 — writes could land in a session that had just been closed (0068)

`Close` took `FOR UPDATE`; `resolveTerminal` and `RecordMovement` read `state`
with no lock. The FK's own `FOR KEY SHARE` does not conflict with an UPDATE of a
non-key column, so in-flight work committed into a reconciled session.
`cash_session_report` freezes `expected_cash` and recomputes takings live, so
the Z report then cannot reconcile.

Both paths take `FOR SHARE`; `reject_write_to_closed_session` refuses it at the
table for everything else.

## P77 — the three-way match ignored what earlier bills had claimed (0067)

The quantity dimension compared against `po_outstanding.qty_received`. The same
function has always returned `qty_billed` and the match never read it, so a
second invoice under a different supplier_ref — which the unique key on
`(supplier_id, supplier_ref)` does not catch — passed every dimension.
`accruedFor` had the identical defect: GRNI discharged twice against an accrual
raised once, taking a liability through zero into a debit.

Now compared against `received − billed on earlier non-cancelled bills`, tracked
across the lines of one bill too.

## P78 — settlement fee shares need not sum to the fee (0069)

The per-tender cap passes its withheld excess to the NEXT tender; the last one
has none. Reachable only below the hallala grid, which `numeric(18,4)` allows
and any converted currency produces. Remainder now redistributed;
`assert_settlement_batch_adds_up` holds gross-equals-lines and
fee-equals-shares at the table.

## Checked and found correct (do not re-litigate)

- **Receipt allocation and credit limits under concurrency.** `FOR UPDATE` on
  the invoice and on the customer row both hold; PostgreSQL re-evaluates the
  qualifier against the committed row under EvalPlanQual, including the
  `customer_open_invoices` function scan. Standing tests added anyway.
- **Dashboard day windows are NOT timezone-dependent.** pgx sends the parameter
  with an unspecified OID, so PostgreSQL infers `date` from the comparison
  against a `date` column and the session TimeZone never enters it. Verified
  under UTC, Asia/Riyadh and America/New_York. The dev server's TimeZone is
  `Asia/Dhaka`, which is what made this worth checking.
- **Tenders cannot carry change given.** `sales_tender_invoice_balanced` makes
  them sum to `total_inclusive` exactly, so the drawer is never overstated.

## Environment note, not a code defect

`rawsyst_dev` carries a leaked `sale.revenue` version 2 ("Later shape, for
testing effective dating", `effective_from` 2026-09-01) naming an unmapped
`deferred_revenue` role. It is in no migration; a rewritten `rules_test` now
uses throwaway `test.*` keys and says why. `posting_rule` has `reject_delete()`,
so removing it needs a superuser with `session_replication_role = replica`.
**Until it is removed, from 2026-09-01 every sale posting in that database
resolves to a rule that cannot post.** `rawsyst_test` is clean; run the
integration suite against it.

## Related
[[code/module-status]] · [[code/backend-state]] · [[design/index]]
