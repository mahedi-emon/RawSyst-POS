# UI/UX Design System

**Binding source:** Blueprint A7 (one design system across Web/Desktop/Mobile), A8 (dashboard), B7 (POS speed), E1.5 (Arabic RTL), G3 (full RTL mirroring), J4 (performance).
**Acceptance gate M6:** receipt, invoice, and UI render correctly in Arabic RTL **including mixed Arabic/English product names and numerals**.

---

## 1. The design brief in one line

> A cashier who has never used this software should complete their first sale correctly, without training, on their first shift.

That is the bar. Everything below serves it.

### Five principles

1. **The cashier is not looking at the screen.** They are looking at the customer, the barcode, the cash. The POS must work by muscle memory and sound, not by reading. Big targets, unambiguous states, audible confirmation.
2. **Show the number that matters, once.** An ERP can display four hundred figures. On each screen, exactly one number is the answer to the question the user came with — that one is large, the rest are context.
3. **Never make someone guess whether it worked.** Every action confirms visibly within 100 ms, even when the real work is still happening.
4. **Offline is a normal state, not an error.** The POS is *designed* to work offline. The UI says "Offline — 23 sales queued" calmly, in neutral colour. Red is reserved for things that are actually wrong.
5. **Arabic is not a translation layer.** It is a full mirror of the interface. Designed alongside English, never retrofitted.

---

## 2. Colour

Semantic tokens, defined once, consumed everywhere. Light and dark are both first-class.

```css
:root {
  /* Neutrals — the interface is mostly this */
  --bg:              #FBFBFD;
  --surface:         #FFFFFF;
  --surface-sunken:  #F4F5F7;
  --border:          #E3E5E9;
  --border-strong:   #C9CDD4;
  --text:            #16181D;
  --text-muted:      #5C6270;
  --text-subtle:     #878D9A;

  /* Brand — used sparingly: primary actions, active nav, focus */
  --brand:           #1F6FEB;
  --brand-hover:     #1A5FCC;
  --brand-subtle:    #EAF1FE;
  --on-brand:        #FFFFFF;

  /* Semantic */
  --success:         #17803D;   --success-subtle: #E7F6EC;
  --warning:         #A65B00;   --warning-subtle: #FDF2E3;
  --danger:          #C02626;   --danger-subtle:  #FDECEC;
  --info:            #0F6C349;  --info-subtle:    #E8F4FB;

  /* Offline is neutral, NOT a warning */
  --offline:         #5C6270;   --offline-subtle: #F0F1F4;

  /* Financial semantics — never rely on colour alone */
  --credit:          #17803D;
  --debit:           #C02626;
}

:root[data-theme="dark"] {
  --bg:              #0E1014;
  --surface:         #171A20;
  --surface-sunken:  #101318;
  --border:          #262A33;
  --border-strong:   #3A404C;
  --text:            #F2F3F5;
  --text-muted:      #A2A9B8;
  --text-subtle:     #757D8C;
  --brand:           #5B9BFF;
  --brand-subtle:    #16233A;
  --success:         #4ADE80;   --success-subtle: #10261A;
  --warning:         #FBBF6B;   --warning-subtle: #2A1D0B;
  --danger:          #FF7B72;   --danger-subtle:  #2C1416;
}
```

**Contrast:** every text/background pair meets **WCAG AA (4.5:1)**; POS screens target **AAA (7:1)** because showroom lighting is unpredictable and cashiers work long shifts.

**Colour is never the only signal.** Debits and credits carry a sign and column position. Status carries an icon and a label. Roughly 1 in 12 men has a colour vision deficiency, and a POS is used by whoever is on shift.

---

## 3. Typography

```css
--font-latin:  'Inter', system-ui, sans-serif;
--font-arabic: 'IBM Plex Sans Arabic', 'Noto Sans Arabic', sans-serif;
--font-mono:   'JetBrains Mono', ui-monospace, monospace;   /* all numbers */
```

**Numbers are always monospaced with tabular figures.** In a column of currency, proportional digits make totals visually jagged and genuinely harder to scan.

```css
.numeric { font-variant-numeric: tabular-nums; font-family: var(--font-mono); }
```

| Token | Size / line-height | Use |
|---|---|---|
| `display` | 40 / 44 | POS running total only |
| `h1` | 28 / 34 | Page title |
| `h2` | 22 / 28 | Section |
| `h3` | 17 / 24 | Card title |
| `body` | 15 / 22 | Default |
| `body-sm` | 13 / 18 | Secondary |
| `caption` | 12 / 16 | Labels, metadata |
| `pos-line` | **18 / 26** | **POS cart lines — deliberately larger than back-office body** |

**Arabic runs ~15% larger** at the same nominal size to match Latin x-height perception. Applied automatically via `:lang(ar)`.

---

## 4. Spacing, radius, elevation

4px base scale: `4 · 8 · 12 · 16 · 24 · 32 · 48 · 64`.

Radius: `sm 6px` (inputs, chips) · `md 10px` (cards, buttons) · `lg 16px` (modals) · `full` (pills, avatars).

Elevation is used sparingly — three levels only. Flat surfaces with clear borders read better under fluorescent showroom lighting than soft shadows.

---

## 5. Touch targets

| Context | Minimum | Rationale |
|---|---|---|
| Back-office (mouse) | 32 × 32 px | Standard |
| **POS (touch)** | **56 × 56 px** | Fast, repeated, sometimes with gloves |
| **POS primary actions** (Pay, Complete) | **72 px tall, full width of its column** | Cannot be mis-tapped |
| Mobile | 44 × 44 px | Apple/Material baseline |

Spacing between adjacent touch targets is never below 8 px.

---

## 6. RTL — full mirroring

Blueprint G3: *"Full RTL layout mirroring for Arabic (menus, receipts, invoices, dashboards) — not just translated text sitting in an LTR layout."*

### Rules

1. **Use logical properties everywhere.** `margin-inline-start`, not `margin-left`. `padding-inline-end`, not `padding-right`. `inset-inline-start`, not `left`. The layout then mirrors with `dir="rtl"` and no separate stylesheet.
2. **Mirror:** navigation side, table column order, progress direction, back/forward chevrons, drawer origin.
3. **Do NOT mirror:** clock faces, media playback controls, checkmarks, the barcode graphic itself, logos.
4. **Numbers stay LTR** even inside Arabic text. `SAR 1,150.00` reads left-to-right in both languages. Arabic-Indic numerals are a **per-tenant preference**, off by default — Saudi retail commonly uses Western digits on receipts.
5. **Mixed content is the norm, not the edge case.** A product named `قميص رجالي Slim Fit — L` must render correctly. This is explicitly QA gate M6. Every component is tested with a mixed-script fixture string.

```css
.cart-row { padding-inline: var(--space-4); border-inline-start: 3px solid transparent; }
[dir="rtl"] .chevron-forward { transform: scaleX(-1); }
.amount { direction: ltr; unicode-bidi: isolate; }   /* numbers never reorder */
```

`unicode-bidi: isolate` on every currency and number field is not optional — without it, a total placed next to Arabic text can visually reorder its digits.

---

## 7. Responsive

Blueprint A8 requires the Owner dashboard to be *"accessible identically from Phone, Laptop, or iPad with one-click drill-through on every widget."*

| Breakpoint | Width | Primary surface |
|---|---|---|
| `xs` | < 640 | Phone — owner monitoring, approvals |
| `sm` | 640–1023 | Large phone / small tablet |
| `md` | 1024–1279 | iPad — full back-office, portrait POS |
| `lg` | 1280–1679 | Laptop |
| `xl` | ≥ 1680 | **POS terminal, desktop back-office** |

**Layout transformations, not hidden content.** A table becomes a card list on phone; it does not lose columns. If a figure matters at 1680 px it matters at 375 px — the Owner checking margin on their phone at the airport is a real, stated use case.

| Component | xs | md | xl |
|---|---|---|---|
| Navigation | Bottom bar, 5 items | Collapsible rail | Persistent sidebar |
| Data table | Card list | Table, priority columns | Full table |
| POS | *not supported* | Portrait: cart over keypad | Split: catalogue \| cart |
| Dashboard | 1 column | 2 columns | 4 columns |
| Filters | Bottom sheet | Inline row | Inline + saved views |

The POS is deliberately **not** phone-supported. A 5-inch checkout screen invites tap errors on financial transactions. SoftPOS on mobile is a separate, purpose-built flow.

---

## 8. Component inventory

**Primitives** — Button (primary/secondary/ghost/danger, 3 sizes) · IconButton · Input · NumberInput (POS variant with big keypad) · Select · Combobox (async, keyboard-first) · Checkbox · Radio · Switch · Textarea · DatePicker · DateRangePicker (with the six presets from A8: Today, Yesterday, This Week, This Month, This Year, Custom).

**Data** — DataTable (sortable, sticky header, virtualised, column visibility, saved views) · StatTile · Sparkline · Chart wrappers · EmptyState · Skeleton · Pagination.

**Feedback** — Toast · InlineAlert · ConfirmDialog · **DestructiveConfirm** (requires typing the entity name — used for anything irreversible) · ProgressBar · **OfflineBanner** · **SyncStatusChip**.

**Domain** — **CartLine** · **TenderSplitPanel** · **VariantGrid** (the size × colour matrix from B2) · **AmountDisplay** (tabular, RTL-safe, sign-aware) · **ComplianceStatusCard** · **ZATCAStateBadge** · **StockLevelIndicator** · **ApprovalCard** · **AuditEntry** · **PermissionMatrix**.

### ZATCA state badge — visual language

The invoice states from `01-invoice-zatca-engine.md` need to be legible at a glance to someone who is not an accountant:

| State | Label (EN / AR) | Colour | Icon |
|---|---|---|---|
| `DRAFT` | Draft / مسودة | neutral | pencil |
| `SIGNED_PENDING_SUBMIT` | Signed · awaiting report / موقعة | info | shield-check |
| `SUBMITTED` | Sending / جارٍ الإرسال | info | arrow-up-circle |
| `REPORTED` / `CLEARED` | Reported / Cleared | success | check-circle |
| `FAILED` | Retrying / إعادة المحاولة | warning | refresh |
| `REJECTED` | **Rejected — action needed** | danger | alert-triangle |

`SIGNED_PENDING_SUBMIT` deliberately reads as **informational, not a problem**. Offline-signed invoices are legally valid and the normal state during an outage; showing them as errors would train cashiers to ignore real errors.

---

## 9. Interaction

### Feedback timing

| Delay | Treatment |
|---|---|
| < 100 ms | No indicator — it feels instant |
| 100 ms – 1 s | Inline spinner on the triggering control |
| 1 – 5 s | Skeleton or progress; the UI stays interactive |
| > 5 s | **Move to a background job**, notify on completion (A2 #8) |

### Optimistic UI at the POS

Scanning a barcode adds the line **immediately** from local SQLite. There is no loading state in the checkout path — J4 budgets cart update at **under 100 ms**, and a spinner would itself blow the budget.

### Keyboard-first (D7)

The POS is operable with **zero mouse use**:

| Key | Action |
|---|---|
| *(scanner input)* | Global keystroke capture — **no field focus required** (B7) |
| `F2` | Product search |
| `F4` | Customer attach |
| `F8` | Hold cart |
| `F9` | Resume held cart |
| `F12` | Payment |
| `Ctrl+K` | Global command palette |
| `Esc` | Cancel / close |

`Ctrl+K` opens the command menu from **anywhere** in the product — "Create Sale", "Create Expense", "Open Reports" — matching D7's requirement for power users to navigate *"without touching the mouse."*

### Destructive actions

Nothing financial is deleted, ever — that is the immutability primitive. So the destructive pattern is reserved for genuinely reversible-but-serious operations (voiding a draft, revoking a device, deleting a user). Those require typing the entity name, never a bare "Are you sure?".

Where a user *expects* delete but the system forbids it — a finalized invoice, a posted journal entry — the UI explains **why** and offers the legitimate path:

> **This invoice cannot be edited or deleted.**
> Finalized tax invoices are immutable under ZATCA rules. To correct it, issue a **Credit Note**.
> `[ Issue Credit Note ]`

That is a teaching moment, not a dead end. It is also the difference between a user trusting the system and a user trying to work around it.

---

## 10. Accessibility

- **WCAG 2.2 AA** across the product; **AAA contrast** on POS.
- Every interactive element reachable by keyboard, in logical order, with a visible focus ring (`2px solid var(--brand)`, `2px` offset).
- Semantic HTML first. ARIA only where semantics are genuinely absent.
- Live regions announce cart changes, sync state changes, and toasts.
- `prefers-reduced-motion` honoured — all transitions drop to 0 ms.
- Minimum body text 15 px; user zoom never blocked.
- Form errors announced, tied to their input with `aria-describedby`, and stated in plain language: "Discount exceeds your limit of SAR 50. Ask a manager to approve." — not "Validation error 4021".

---

## 11. Empty, loading, error states

Every list has all three designed. An empty state is an onboarding opportunity, not a blank panel:

> **No products yet**
> Import your existing catalogue from Excel, or add your first product manually.
> `[ Import from Excel ]` `[ Add Product ]`

Error states say what happened, whether it was the user's doing, and what to do next. Never a raw error code alone.

---

## 12. Voice and copy

| | |
|---|---|
| **Tone** | Direct, plain, calm. Never jokey — this handles people's money |
| **Person** | Second person: "Your VAT return is due in 6 days" |
| **Numbers** | Always with currency code: `SAR 1,150.00` |
| **Dates** | `14 Aug 2026`, never `08/14/26` — ambiguous internationally |
| **Errors** | What happened → why → what to do |
| **Banned** | "ZATCA-certified", "certified compliant", "guaranteed compliant", "never at legal risk" |

The banned list is **linted in CI** across locale files, templates, and docs. Blueprint A1 makes this a legal-exposure issue, not a style preference.

Arabic copy is written by a native speaker, not machine-translated. Financial and legal terminology in Saudi Arabic has established conventions that a translation engine will get subtly wrong — and subtly wrong on a tax invoice is a compliance problem.

---

## 13. Screen priorities

Ordered by how much design attention each earns, which is not the same as build order.

| Priority | Screen | Why |
|---|---|---|
| **1** | **POS Billing Counter** | Used thousands of times a day by the least-trained user. Every millisecond and every millimetre matters |
| **2** | **Owner Dashboard** | The reason the Owner bought the product — "where is my money going", answered in one click |
| **3** | **Compliance (ZATCA/VAT)** | Answers "am I legally exposed right now?" — the screen that prevents fines |
| **4** | **Inventory + Variant Matrix** | The fashion/RMG core need; the size × colour grid is the differentiator |
| 5 | Invoice detail | Most-viewed record |
| 6 | Product edit | Most-edited record |
| 7 | Reports | High value, lower frequency |

The first four are built as an interactive prototype before implementation begins.

---

## 14. Implementation notes

- **Tailwind + shadcn/ui**, per the frozen stack (J1). Design tokens map to Tailwind theme extension, so a token change propagates everywhere.
- Components live in a shared package consumed by **both** Next.js and the Tauri POS — A7 requires *"one design system across Web/Desktop/Mobile"* so the product feels identical everywhere.
- **The POS bundles its own fonts and assets.** No CDN, no network dependency — it must render identically with the internet unplugged.
- Every component ships with a mixed Arabic/English fixture in its test suite. M6 is verified continuously, not at the end.
