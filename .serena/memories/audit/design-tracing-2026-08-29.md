# Design-to-code tracing, 2026-08-29

Follows [[audit/business-logic-2026-08-28]] and
[[audit/security-and-frontend-2026-08-28]]. The method here was different and
worth reusing.

## The method: make the document the assertion

Every finding came from taking a sentence somebody had already written down and
asking whether the code agreed with it. Not from reading code looking for
smells.

- **`router.go`'s own header** said the route table exists so "a typo like
  `sales.refnud` is caught by a test rather than by a cashier who cannot issue a
  refund." **That test did not exist.** Written now
  (`TestEveryRoutePermissionIsOneSomeRoleHolds`); it passes, so nothing was
  broken — but `purchasing.viewe` would have 403'd every user forever.
- **UI spec §1 "Non-negotiables"** is a table of numbers with reasons. Three
  were wrong: touch targets 52 against 56, running total 38 against 40, cart
  lines inheriting 15 against 18. All under 5px, none findable by eye.
  `e2e/pos-spec.mjs` reads them out of the stylesheet now.
- **UI spec §1 keyboard** named seven shortcuts and "fully operable with no
  mouse". **None existed.** Also "no field focus needed to scan", which
  `autoFocus` does not deliver — it focuses once and the first tender button
  takes focus away. Built in `pos/src/pos/keys.ts`.
- **Design system §6 rule 4** says `unicode-bidi: isolate` on a currency "is not
  optional". `.num` had `direction: ltr` only. And an amount interpolated into a
  sentence has no element at all — `interpolate()` now fences every substituted
  value with U+2068/U+2069.

## Permission surface: four kinds of route-less verb

`TestSeededPermissionsWithNoRoute` is a ledger, not a pass/fail, because "no
route requires this" means four things:

- **structural** — `catalog.view_cost_price` names a decision enforced by the
  TYPE: `SellableVariant` has no cost field, so cost cannot reach a till
  whatever a role says. A check would be weaker.
- **local** — `sales.hold`, `sales.void_draft`: happens in the till's SQLite,
  no request, so no route to guard.
- **awaited** — seeded ahead of the module, like the chart of accounts carrying
  accounts before anything posts to them. Must name the phase.
- **widow** — `device.view`/`device.manage` → `devices.*` (0037),
  `compliance.view` → `einvoicing.view` (0043). Both renames were COMPLETE, so
  nobody lost access; the old grants were dead rows. 0074 removes them.

**Why removing dead grants matters:** `role_permission` is free text by design
(A6.2 lists fourteen verbs then uses three not in the list), so the seeded
grants are the only statement of which verbs exist. The Phase 2 role editor will
build its toggles from this table and would offer an owner a switch the product
does not read.

Removing them broke two tests, and both were wrong beforehand: one named four
verbs as a "sample" while claiming to prove provisioning attached the whole
role. It now compares against the seeded role and cannot go stale.

## Currency: the spec contradicted itself and the code diverged quietly

UI spec §8 said "currency code always shown". The receivables ageing table is
six money columns wide; a row of `SAR 0.00` six times is less informative, not
more. The rule's intent is in the row beneath it, about dates: **ambiguity for
an international product**. Amended to "named — on the figure, or once for a
table in its `<caption>`", with the reasoning recorded in the spec.

`settlement` and the customer list carried **no currency through the API at
all**. Fixed at the root. Note the asymmetry, which is deliberate: a pending
tender reports the INVOICE's currency (the document records what it was issued
in); a batch reports the COMPANY's (a bank deposit is a figure in the books).

## Three shapes of untranslated string a scan can miss

`e2e/strings.mjs` looked for prose in a quoted attribute and JSX text of 2+
words. It could not see:

1. a single word beside an expression — `{money(x)} returned`
2. English in a template literal — `` `${days} days` ``
3. anything in a `.ts` file (it only walked `.tsx`)

Twenty more strings, three at the counter. All three shapes are checked now.

## Blocked on the environment, not the code

`go test -race` needs cgo; the local gcc is 32-bit (`cc1.exe: sorry,
unimplemented: 64-bit mode not compiled in`). The race tests from the August 28
audit still exist and still matter — they need a machine with a 64-bit gcc.

## Related
[[audit/business-logic-2026-08-28]] · [[audit/security-and-frontend-2026-08-28]]
· [[code/module-status]] · [[design/index]]
