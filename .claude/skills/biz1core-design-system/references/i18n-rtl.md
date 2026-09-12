# Arabic, Bangla, RTL and formatting

Blueprint G3 asks for **full RTL layout mirroring, not translated text sitting
in an LTR frame.** The layout half is CSS — every rule in the system uses
logical properties, so mirroring is a document direction change rather than a
second stylesheet. This file is the other half, plus the exceptions that must
*not* mirror.

## Locales

`shared/src/i18n/strings.ts` — `type Locale = 'en' | 'ar' | 'bn'`, and
`directionOf(locale)` returns `'rtl'` for `ar` and `'ltr'` otherwise. Direction
is derived, never stored: there is one right answer.

`LocaleProvider` sets `root.dir` and the language on the document.
`useLocale()` gives `{ locale, direction, t, … }`; `useT()` gives just `t`,
which is what most components want.

## Strings

```tsx
const t = useT();
…
<h2 className="ds-h3">{t('acct.trail')}</h2>
<caption>{t('common.amountsIn', { currency })}</caption>
```

`Translate` is `(key: Key, params?: Record<string, string | number>) => string`.

The catalogue is a **flat typed record**, not a library: `Record<Key, string>`
for each locale, so a key present in `en` and missing from `ar` is a compile
error, not a Saudi shop's till silently falling back to English. Two-and-a-half
locales, known at build time, no runtime negotiation — an i18n framework would
add a dependency, a loader and an async boundary for problems this product does
not have.

**Never hard-code a user-facing English string in `shared/`.**

### What is deliberately not in the catalogue

- **Tenant data.** A product name, a supplier's legal name, a customer's
  address. Those live in `*_ar` columns and render as written — translating them
  would put a machine translation of a company's own name on its tax invoice.
  Use `localName(locale, name, name_ar)`, which falls back to `name` in both
  directions, because a name is better than a blank cell.
- **Money and dates.** They do not branch on locale at all — see below.

## Numbers

Western digits in every locale. Arabic-Indic numerals are a per-tenant
preference that is off by default, so no formatting branches on locale.

`.num` / `.numeric` carry `unicode-bidi: isolate` **in both directions**, and
under `[dir='rtl']` also `direction: ltr; text-align: start`.

Both properties are needed and they do different jobs. `direction` sets the base
direction *inside* the box; `isolate` takes the box out of the surrounding bidi
paragraph so it can neither be reordered by the Arabic around it nor reorder
that Arabic itself. With only `direction` set, an amount sitting inline in a
sentence — "بمقدار SAR 1,250.00" — was still a participant in a paragraph whose
direction disagreed with it.

`isolate` applies in **both** directions because a shop's names and references
are mixed-script in every language: an Arabic product name in an English
sentence has the same problem in mirror image.

### The header of a numeric column

A column heading is a **word**, not a number. `[dir='rtl'] .ds-table th.num` is
given `direction: rtl` (so the Arabic word is not reversed) but
`text-align: left` — because the cells below it are LTR boxes whose `start` is
the left edge, so a column of money hugs the left of its cell in an Arabic
table, and the heading must follow the column rather than the page.

## Dates

`.ds-date`, never `.num`. It carries `direction: ltr; unicode-bidi: isolate`
because "28 Aug 2026" is three runs the bidi algorithm classes differently — two
weak (the numbers) and one strong LTR (the month). Dropped into an Arabic
paragraph they get reordered into "Aug 2026 28": not merely mirrored, but
*wrong*, and wrong in a way that still looks like a date. That was on every
purchase order, bill and invoice row in the Arabic interface.

`[dir='rtl'] .ds-date` is then explicitly `text-align: right`, so the box sits
where the column wants it while the characters keep their own order — which is
how a date is written in Arabic anyway.

Format dates with `shortDate(iso, locale)` (chart axes and rows, no year) or
`longDate(iso, locale)` (documents, with the year). Both handle a full RFC3339
timestamp, not just `YYYY-MM-DD` — splitting naively on the hyphen produced
"NaN Aug" on eleven card settlements and the whole Terminals list.

Month names come from the module's own table in all three languages, **not**
`toLocaleDateString`, which reads the *browser's* locale — a shop that chose
Arabic in this product would still get English months on a machine set to
English. Numeric months are never used: "08/09" is September in one market and
August in another, and this is sold into both.

## Money

Always `money()` from `shared/src/ui/format.ts`. Strings in, strings out —
amounts arrive from the server as decimal strings and are formatted **without
ever becoming a float**, and grouping is applied to the integer part by string
manipulation for the same reason. `Intl.NumberFormat` would do it beautifully
and would require a float to do it to.

- Always two decimal places. A column where some cells show one and others two
  cannot be scanned.
- Negatives are **parentheses**, not a hyphen. A minus sign is easy to miss at
  the start of a right-aligned column; accounting has used brackets for a
  century for that reason.
- Currency is an **ISO code, never a symbol.** ﷼, ৳ and $ are ambiguous across
  the markets this product serves, and $ in particular is claimed by a dozen
  currencies.
- `null`/empty renders as an em dash, not `0.00`.

Companions: `percent()` (null → em dash, because "not computed" and "no change"
are different facts and only one is reassuring), `direction()` (up/down/flat,
used for an arrow *and* a colour together, never colour alone), `isZero()`
(handles `0`, `0.00`, `0.0000` — Postgres sends all three), and `tenderName()`.

## Fonts and sizing

See `layout-and-type.md`. The short version: family switches on `:lang()` at any
depth; the size multiplier is set **once on `html`** and must never be written
on an inherited selector, because `em` on `:lang()` compounds at every nesting
level and inverted the type scale.

## What must not mirror

| Thing | Why |
|---|---|
| Amounts (`.num`) | Read most-significant-digit first in both scripts |
| Dates (`.ds-date`) | Bidi reordering produces a wrong-but-plausible date |
| Numeric column headers | Follow their column, not the page |
| `.ds-sparkline` | `transform: scaleX(-1)` under RTL — time reads left-to-right in both scripts, so it is flipped back after the surrounding layout mirrors |
| The select chevron | Explicitly repositioned to the left edge under `[dir='rtl']` |

## Checklist for any UI change

- Logical properties only. No `left`, `right`, `margin-left`, `padding-right`.
- Every visible string through `t()`.
- Amounts through `money()`, marked `.num` on both `td` and `th`.
- Dates through `shortDate`/`longDate`, marked `.ds-date`.
- Tenant-authored names through `localName()`.
- If you add a key to `en`, add it to `ar` and `bn` — the compiler will insist,
  and `shared/src/i18n/coverage.test.ts` checks the catalogue.
