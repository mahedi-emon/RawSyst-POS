---
name: rawsyst-design-system
description: >
  RawSyst's own design system — the CSS custom properties, class primitives,
  React helpers and layout rules that shared/, web/ and pos/ are actually built
  from. Use whenever writing or changing UI in this repository: picking a token,
  building a panel, table, form, dialog or navigation, choosing a colour or a
  spacing step, handling loading/empty/error states, or working on Arabic RTL
  and Bangla. Every fact here was read out of the source, not assumed.
---

# RawSyst design system

RawSyst is an ERP and POS. Its screens are read in columns of currency, under
fluorescent light, by someone with a queue behind them. The design system is
plain CSS custom properties and class primitives — **no Tailwind, no CSS-in-JS,
no component library.** Do not add one.

## Pre-flight — read before writing any UI

1. **`shared/src/design-system.css` is the system.** 2,290 lines, and roughly
   half of it is comments explaining *why* a rule is the way it is. When a rule
   looks arbitrary, the reason is written above it. Read that reason before
   changing it — most of these rules exist because a specific screen broke.
2. **Never invent a value.** Every colour, space, radius, shadow, font and tap
   target is already a token. A literal hex or px in a component is a defect.
3. **Extend, do not add a second vocabulary.** If a screen needs something the
   system does not have, the fix is a new primitive in the system, not a local
   style with a new look.
4. **Logical properties only.** `inline-size`, `padding-inline`,
   `border-inline-start`, `inset-block-end`. Never `width`, `padding-left`,
   `margin-right`. This is what makes Arabic mirror for free.

## The hard rules

These are not preferences. Breaking one is a bug.

| Rule | Why |
|---|---|
| A component in `shared/` may only name classes defined in `design-system.css` or `dashboard.css` | Both apps load those two. A class defined only in `pos/src/styles.css` renders bare in the back office. This bug has shipped three times; `shared/src/ui/stylesheetCoverage.test.ts` now fails on it |
| Colour is never the only signal | ~1 in 12 men has a colour vision deficiency and a till is used by whoever is on shift. Status carries an icon or a word; financial direction carries a sign |
| Numbers are `tabular-nums` and stay LTR in every locale | A column of currency cannot be scanned with proportional digits, and a mirrored amount is simply wrong |
| Money is formatted by `shared/src/ui/format.ts`, never by hand and never through a float | `Number('0.1') + Number('0.2')` is `0.30000000000000004`, and a dashboard is where that surfaces |
| The page never scrolls sideways | A wide table scrolls inside `.ds-scroll-x`. A sideways-scrolling page is a defect; a sideways-scrolling table is a table |
| Every user-facing string comes from the catalogue via `useT()` | `shared/src/i18n/strings.ts` is typed — a key missing from a locale fails to compile |
| Dark theme is opt-in via `data-theme="dark"`, never `prefers-color-scheme` | A till is a shared machine; its appearance is the shop's decision, not the last person to touch Windows display settings |

## Where to look

| You are doing this | Read this |
|---|---|
| Picking a colour, space, radius, shadow, font, tap target | `references/tokens.md` |
| Type scale, breakpoints, the shell, responsive behaviour | `references/layout-and-type.md` |
| "Is there already a class for this?" | `references/components.md` |
| A data table, a card list on a phone, a totals row | `references/patterns/tables.md` |
| A form, a field, validation, errors | `references/patterns/forms.md` |
| A button, a row action, a destructive action | `references/patterns/buttons-and-actions.md` |
| A panel, a dialog, a confirmation | `references/patterns/panels-and-dialogs.md` |
| Loading, empty, error, offline, permission-denied | `references/patterns/states.md` |
| Arabic, RTL, Bangla, dates, currency, mixed script | `references/i18n-rtl.md` |
| About to change the system itself, or unsure if something is allowed | `references/rules.md` |

## The stylesheet graph

Four files, and which app loads which decides where a rule may live.

```
shared/src/design-system.css     ← both apps. The system.
shared/src/dashboard/dashboard.css ← both apps. Screen furniture + the form primitives.
web/app/back-office.css          ← back office only. Rail, wizard, invoice, matrix.
pos/src/styles.css               ← till only. Till-specific sizing overrides.
```

- `web/app/layout.tsx` imports design-system, dashboard, back-office — in that order.
- `pos/src/main.tsx` imports design-system, styles, dashboard.

A surprise worth knowing: the **form primitives** (`.field__label`,
`.field__hint`, `.field__error`, `.field__optional`, `.form__error`,
`.form__actions`) live in **`dashboard.css`**, not `design-system.css`. The
field *box* (`.field__input` / `.input`) is in `design-system.css`. Both are
loaded everywhere, so both are safe from `shared/`.

## Keeping this skill true

A design-system skill is only useful while it is true. Every class name, custom
property and repository path stated in backticks anywhere in this skill is
checked against the actual stylesheets and the actual tree by:

```bash
node .claude/skills/rawsyst-design-system/verify.mjs
```

Run it after changing the design system, and after changing this skill. It reads
the legitimate no-rule naming hooks out of `stylesheetCoverage.test.ts` rather
than keeping its own list, so the two cannot disagree.

## What not to do

- Do not initialize shadcn/ui or add Tailwind. Reading external registries for
  reference is fine and encouraged; importing their idiom is not. See
  `FRONTEND_TOOLBOX.md` §4.
- Do not add an icon library. `shared/src/ui/Icon.tsx` is ~30 hand-drawn paths
  on one 24-unit grid at 1.7 stroke. Add a path, do not add a dependency.
- Do not add an animation library to satisfy a skill. The only motion in the
  system is a skeleton shimmer and 120ms colour transitions, both disabled
  under `prefers-reduced-motion`.
- Do not add an i18n framework. Two-and-a-half locales, known at build time, a
  typed record.
- Do not reach for `!important`, a z-index above the ones already in use
  (`.ds-table th` 1, `.lang__menu` 60, `.dialog__backdrop` 100), or a new
  breakpoint.
