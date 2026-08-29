# UI/UX — Phase 1 Screen Specifications

**Depends on:** `00-design-system.md`. Live prototype: `prototype.html`.

Screens are ordered by design attention earned, which is not build order. The
POS is used thousands of times a day by the least-trained user; the audit log is
opened twice a year.

---

## 1. POS Billing Counter

The one screen with a hard latency budget and the one where a mis-tap costs
money.

### Layout

```
┌─ status bar ────────────────────────────────────────────────────┐
│ ● Online · 0 queued   Olaya · Terminal 02   Shift 09:14   Fatima │
├──────────────────────────────────┬──────────────────────────────┤
│  ⌕ Scan barcode or search…       │  Cart              4 items   │
│  [Favorites][Recent][Thobes][…]  ├──────────────────────────────┤
│  ┌──────┐┌──────┐┌──────┐        │  Executive Abaya     449.00  │
│  │      ││      ││      │        │  Black · L                   │
│  └──────┘└──────┘└──────┘        │  قميص Slim Fit       318.00  │
│  ┌──────┐┌──────┐┌──────┐        │  Navy · M · 2 × 159.00       │
│  └──────┘└──────┘└──────┘        ├──────────────────────────────┤
│                                  │  Subtotal          1,012.17  │
│                                  │  VAT 15%             151.83  │
│                                  │  Total          ┌─────────┐  │
│                                  │                 │1,164.00 │  │
│                                  ├─────────────────┴─────────┤  │
│                                  │  Cash 200 · Mada 464 ·    │  │
│                                  │  Tabby 500                │  │
│                                  ├───────────────────────────┤  │
│                                  │   Complete sale      F12  │  │
└──────────────────────────────────┴──────────────────────────────┘
```

### Non-negotiables

| Requirement | Reason |
|---|---|
| **No field focus needed to scan** | Scanner input captured globally (B7). Requiring a click loses sales at a busy counter |
| Total in `display` size (40px), tabular figures | The one number the customer will ask about |
| Cart lines at **18px**, larger than back-office body | Read at arm's length, at speed |
| Pay button **72px tall**, full column width | Cannot be mis-tapped |
| Touch targets **≥ 56px** | Fast, repeated, sometimes with gloves |
| **AAA contrast** | Showroom lighting is unpredictable; shifts are long |
| No spinner in the checkout path | Under 100ms means a spinner would itself blow the budget |

### States

| State | Treatment |
|---|---|
| Online | Neutral dot, "Online" |
| **Offline** | **Neutral grey**, "Offline — 23 queued". Never red. Offline is a designed operating mode; styling it as an error teaches cashiers to ignore warnings |
| Over-limit discount | Inline manager-PIN prompt, not a dead end |
| Below price floor | Blocked with the floor shown: "Minimum price is SAR 380.00" |
| B2B offline | The blueprint's exact message, plus one-tap "Issue as Simplified" |
| Held carts exist | Badge on Resume (F9) |

### Keyboard

`F2` search · `F4` customer · `F8` hold · `F9` resume · `F12` pay · `Esc` cancel ·
`Ctrl+K` command palette. Fully operable with no mouse.

**Built**, in `pos/src/pos/keys.ts`, except `Ctrl+K`: there is no command
palette yet, and a shortcut that opens nothing is worse than one that is not
there. `Esc` backs out of what is open before it clears anything — a dialog,
then a notice, then the cart — because clearing is destructive and must not
also be how a dialog is dismissed.

The same file implements the "no field focus needed to scan" non-negotiable.
`autoFocus` on the scan box was not enough: it focuses once, and the first
tender button a cashier presses takes focus away for good, after which a
barcode lands in whatever has it. Keystrokes are captured at the document and
routed to the scan box whenever focus is somewhere that does not take typed
characters — a button, a link, the body, a checkbox — and left entirely alone
when somebody is typing. It does not try to tell a scanner from a person by
timing: a scanner interleaved with a fast typist produces the same intervals,
and a wedge that guesses wrong drops a barcode on the day the shop is busiest.

### Arabic

Full mirror: catalogue moves right, cart left. Product names render mixed
(`قميص رجالي Slim Fit`). Amounts stay LTR with `unicode-bidi: isolate` so digits
never reorder next to Arabic text.

---

## 2. Owner Dashboard

**Implemented.** `GET /api/v1/dashboard/overview` (one call, `accounting.view`)
and `pos/src/dashboard/`. Every figure is computed by the posting engine — the
same journal the trial balance reads — so a number an owner disputes traces to
entries rather than to browser code, and a backend test asserts the dashboard's
revenue equals the P&L's.

Departures from the sketch below, each deliberate:

- **Expenses are listed by account, not by category.** Categories are a Phase 2
  concept; the accounts exist now and are what the books actually hold.
- **Purchases, suppliers, payables, customers and employees are named as
  unbuilt** rather than shown as zero. An owner reading "Payables: 0.00" would
  reasonably conclude they owe nobody, which is a different and much worse
  statement than "not yet built". The API returns an `unbuilt` list for exactly
  this.
- **No activity feed.** It answered no question an owner arrives with, and a
  feed of every sale on a dashboard is the thing people scroll past.
- **Drill-through is wired.** Sales, Gross profit and Expenses open their detail;
  the stock badges and every attention row with a known target open theirs. A KPI
  you cannot open is trivia, so the two tiles with nowhere useful to go — Cash and
  bank — stay visibly inert rather than pretending to be pressable.
- **The tiles are not cards.** Four bordered boxes in a row is the most
  recognisable generic-dashboard signature and adds nothing; position already
  groups them. On desktop they are separated by hairlines.

Answers "where is my money going" in one click (A2 #10).

### Layout

Four KPI tiles → payment-method breakdown + activity feed → attention list.

| Tile | Value | Sub |
|---|---|---|
| Today's sales | 48,290 | ▲ 12.4% vs yesterday, sparkline |
| Gross profit | 17,384 | ▲ 36.0% margin |
| Expenses today | 3,120 | **one-click drill-down by category** |
| Cash + bank | 312,940 | 18,400 not yet settled |

**Every widget drills through.** A8 requires one-click drill-through on all of
them; a KPI you cannot open is trivia.

**"Collected but not yet settled" is its own figure** (C12). The Owner asking
"where is my money" needs to know SAR 18,400 exists but is with the processor.

### Payment-method bar chart

Mada first and largest — it is the majority of Saudi retail. Each tender has its
own colour and its own row. Copy under the chart explains why they are not
merged: Mada's fee is materially lower, so folding it into "card" would misstate
margin.

### Needs attention

Severity-striped rows, most severe first: rejected ZATCA submission, invoices
pending report, below reorder level, expenses awaiting approval, customers over
credit limit. Each links to its queue.

### Responsive

| Width | Layout |
|---|---|
| Phone | 1 column, tiles stacked, chart scrolls horizontally |
| iPad | 2 columns |
| Desktop | 4 tiles across, chart and feed side by side |

Content is never dropped — only rearranged. The Owner checking margin on a phone
at the airport is a stated use case.

---

## 2a. Drill-through screens

**Implemented.** `pos/src/dashboard/` — `SalesDetailScreen`, `ExpensesDetailScreen`,
`ComplianceScreen`, `StockScreen`, all on one `DetailScreen` frame and one
`useRemote` hook so the five states (loading, ready, denied, offline, error) are
written once rather than five times. The states that drift first are the ones
nobody demos, which are exactly the ones a real deployment spends its first week in.

| Screen | Route | Permission |
|---|---|---|
| Sales | `GET /api/v1/dashboard/sales` | `sales.view` |
| Expenses | `GET /api/v1/dashboard/expenses` | `accounting.view` |
| Reporting to ZATCA | `GET /api/v1/dashboard/compliance` | `accounting.view` |
| Stock to reorder | `GET /api/v1/dashboard/stock` | `inventory.view` |

Each is gated on the permission covering the **records it shows** rather than on
the dashboard's own `accounting.view` — a role holding one and not the other is an
ordinary arrangement, and a backend test asserts each pairing.

Every list sums to the tile it sits behind, asserted by test. A detail screen
filtered slightly differently from its summary is worse than no detail screen,
because it makes an owner believe the summary is wrong.

**The compliance screen carries no Retry button.** There is nothing to retry: the
terminal refuses to sign because the P1 verification gate is open, and a button
implying otherwise would have an owner clicking it for weeks. It states what is
outstanding (the submission) and what is not (the sale, the receipt, the books,
the chain), because precision there reassures more than vagueness does.

## 3. Compliance (ZATCA / VAT / PDPL)

Answers "am I legally exposed right now?" (E7).

### Verdict banner

The whole screen resolves to one sentence at the top:

```
◆  No immediate legal exposure                          98.4%
   1 rejected invoice needs a credit note.        reported on time, 30 days
   Everything else is within its deadline.
```

Green when clear, amber when a deadline approaches, red when something is
overdue or rejected. An Owner who reads only this line should be correctly
informed.

### Panels

| Panel | Shows |
|---|---|
| **ZATCA** | Reported today · signed pending · **rejected (action needed)** · oldest unsubmitted age · per-terminal chain status with current ICV |
| **VAT return** | Countdown to deadline · **four-way reconciliation** with each source's figure |
| **PDPL** | Consent coverage · open DSRs · nearest deadline · open incidents · retention job · records under legal hold |
| **Archive & registry** | Invoices archived · hash verification · oldest retrievable · registry verified/stale/never-verified counts |

### Explaining, not just reporting

Each panel carries one sentence of plain-language context. The four-way
reconciliation does not show an unexplained SAR 2,420 gap — it says the gap
traces to 23 invoices queued on Terminal 02, and links there.

`SIGNED_PENDING_SUBMIT` is styled **informational, not warning**. Offline-signed
invoices are legally valid and normal during an outage.

---

## 4. Inventory & Variant Matrix

The fashion/RMG differentiator.

```
Colour \ Size      S      M      L      XL      XXL
Black · أسود      14     22     19     0       3
                                       Out     Low
Navy · كحلي        9     16     4      11      7
                              Low
Maroon · عنابي     6      8     5      0       2
                  Dead                 Out     Dead
```

| Cell state | Treatment |
|---|---|
| In stock | Plain |
| At/below reorder | Amber background + "Low" label |
| Out | Red background + "Out" label |
| No sale in 90 days | Grey + "Dead" label |

**Colour is never the only signal** — every non-normal cell carries a text
label, because roughly 1 in 12 men has a colour vision deficiency and a POS is
used by whoever is on shift.

Copy under the grid states the point plainly: a standard POS shows "Executive
Abaya: 126 in stock" and hides that Black XL is out while Maroon XXL has not
moved in three months.

Wide tables scroll inside their own container; the page never scrolls sideways.

---

## 5. Invoice detail

Most-viewed record. Header, lines, tenders, ZATCA panel (state, ICV, QR, chain
position), audit trail.

**No edit button, no delete button, no void.** Where a user expects one:

> **This invoice cannot be edited or deleted.**
> Finalized tax invoices are immutable under ZATCA rules. To correct it, issue a
> **Credit Note**.  `[ Issue Credit Note ]`

That is a teaching moment rather than a dead end, and it is the difference
between a user trusting the system and a user trying to work around it.

Reprint is available and logged — reprinting is not reissuing.

---

## 6. Onboarding wizard (A5)

Seven steps, designed so a non-technical shop owner completes setup alone.

```
1 Business  →  2 Stores  →  3 Tax  →  4 Employees
            →  5 Hardware  →  6 Opening balances  →  7 Finish
```

| Step | Notes |
|---|---|
| 3 Tax | **Saudi auto-loads VAT 15%, ZATCA settings, Arabic RTL** — all from the registry. Also captures the ZATCA wave and deadline, with copy making clear these come **from the taxpayer's own ZATCA notification**, never from the software |
| 5 Hardware | Detect scanner → detect printer → test print → test drawer → receipt layout |
| 6 Opening balances | **Validates that the opening trial balance balances** before committing. This is the step that most often goes wrong in practice |

Progress is saved per step; the wizard is resumable.

---

## 7. Shift close (Z-report)

Cashier counts physical cash and enters denominations. System shows expected vs
actual and **Short / Over / Exact** in large type.

A variance is not hidden or auto-absorbed — it posts to Cash Over/Short and
appears on the signed closing report. An unexplained drawer difference is
information.

---

## 8. Patterns across every screen

| Pattern | Rule |
|---|---|
| Empty state | Offers the next action: "No products yet — Import from Excel / Add Product" |
| Loading | < 1s inline spinner · 1–5s skeleton · > 5s background job with notification |
| Error | What happened → why → what to do. Never a bare code |
| Destructive | Type the entity name. Reserved for genuinely reversible-but-serious actions, since financial records are never deleted |
| Amounts | Tabular figures, currency code always shown, LTR-isolated |
| Dates | `15 Aug 2026` — never `08/15/26`, which is ambiguous internationally |
| Tables | Scroll inside their own container |
| Focus | Visible ring, 2px, brand colour, 2px offset |

---

## 9. Accessibility

WCAG 2.2 AA product-wide; **AAA contrast on POS**. Keyboard reachable in logical
order. Semantic HTML first, ARIA only where semantics are genuinely absent. Live
regions announce cart changes and sync-state changes.
`prefers-reduced-motion` drops all transitions to zero.

Form errors are announced, tied to their input, and written plainly:
*"Discount exceeds your limit of SAR 50. Ask a manager to approve."* — not
*"Validation error 4021"*.

---

## 10. Arabic and RTL

| Rule | |
|---|---|
| Logical properties everywhere | `margin-inline-start`, never `margin-left` — the layout then mirrors with `dir="rtl"` and no second stylesheet |
| Mirror | Navigation side, table column order, progress direction, chevrons, drawers |
| Do **not** mirror | Clock faces, media controls, checkmarks, barcodes, logos |
| Numbers | Always LTR, `unicode-bidi: isolate`. Arabic-Indic digits are a per-tenant preference, off by default |
| Mixed content | The norm, not the edge case. Every component ships with a mixed Arabic/English fixture in its tests, so QA gate M6 is verified continuously |

Arabic copy is written by a native speaker, never machine-translated. Saudi
financial and legal terminology has established conventions, and subtly wrong on
a tax invoice is a compliance problem.
