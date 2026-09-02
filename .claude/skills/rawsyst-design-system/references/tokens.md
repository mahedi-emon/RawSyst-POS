# Tokens

Every value in `shared/src/design-system.css` `:root`. A literal that is not one
of these is a defect. Dark values are the `:root[data-theme='dark']` overrides —
**every token below that has a dark value is redefined there, so never hard-code
a light value and never write a dark-mode branch in a component.**

## Neutrals — nine tenths of the interface

A cool grey ramp with a faint blue cast, not pure neutral: beside the white
surfaces this product is mostly made of, a dead-neutral grey reads yellow. The
canvas is close to white on purpose — an obviously grey page looks like a
template with cards dropped on it.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg` | `#f6f7f9` | `#11151d` | The page |
| `--surface` | `#ffffff` | `#1a1f2a` | Panels, table headers, fields |
| `--surface-sunken` | `#f1f3f6` | `#141821` | Panel feet, hover rows, disabled fields |
| `--surface-raised` | `#ffffff` | `#202634` | Dialogs and menus — things that float |
| `--border` | `#e4e7ec` | `#242b39` | Hairlines. The default separator |
| `--border-strong` | `#cdd2dc` | `#38414f` | Field and secondary-button edges, totals rule |
| `--text` | `#101423` | `#eef1f6` | Body |
| `--text-muted` | `#545c6f` | `#9fa8bb` | Secondary text, captions |
| `--text-subtle` | `#858da0` | `#737c90` | Table headers, placeholders |

In dark the four surface steps form a deliberate ladder — rail darkest, then
page, then panel, then raised — wide enough apart to be seen on a cheap monitor
in a lit shop.

## Brand — primary actions, active navigation, focus. Nowhere else.

A deep royal blue, not the bright default every framework ships: it has to sit
under a total in SAR, BDT and USD without shouting, and look deliberate beside a
red refund and a green settlement.

| Token | Light | Dark |
|---|---|---|
| `--brand` | `#2b57d4` | `#6c92f5` |
| `--brand-hover` | `#2247b4` | `#86a6f8` |
| `--brand-active` | `#1c3c99` | `#a3bcfb` |
| `--brand-subtle` | `#eef2fd` | `#182444` |
| `--brand-border` | `#c9d6f8` | `#2c3d6e` |
| `--on-brand` | `#ffffff` | `#08101f` |

**If you are reaching for brand colour on something that is not a primary
action, active navigation or a focus ring, you are reaching for the wrong
token.** A screen where everything is emphasised has nothing emphasised.

## The navigation rail

The one large dark surface in the product, in both themes.

`--rail` · `--rail-hover` · `--rail-active` · `--rail-text` ·
`--rail-text-strong` · `--rail-border`

Light: `#131a2b` / `#1d2740` / `#24304e` / `#c3cadb` / `#ffffff` / `#232c44`.
Dark: `#080b11` / `#161c28` / `#202836` / `#9fa8bb` / `#ffffff` / `#1c2331`.

## Semantic

Each is a triple: the ink, a `-subtle` fill, a `-border` hairline. A badge uses
all three; text uses the ink alone.

| Family | Light ink | Dark ink |
|---|---|---|
| `--success` | `#0f7a3d` | `#4ade80` |
| `--warning` | `#96540a` | `#f5b453` |
| `--danger` | `#bb2525` | `#ff8078` |
| `--info` | `#0d6b93` | `#74c4e8` |

**`--offline` (`#545c6f` / `#9fa8bb`) with `--offline-subtle` is neutral, not a
warning.** The POS is designed to work offline. Colouring an offline state amber
teaches cashiers to ignore amber.

**Financial direction:** `--credit` (green) and `--debit` (red), and they are
paired with a sign, never used as colour alone. `.ds-up` / `.ds-down` apply them.

## Type

| Token | Value |
|---|---|
| `--font-latin` | `'Inter Variable', 'Inter', system-ui, -apple-system, 'Segoe UI', sans-serif` |
| `--font-arabic` | `'IBM Plex Sans Arabic', 'Noto Sans Arabic', system-ui, sans-serif` |
| `--font-bengali` | `'Noto Sans Bengali', 'Hind Siliguri', system-ui, sans-serif` |
| `--font-mono` | `'JetBrains Mono', ui-monospace, 'Cascadia Mono', Consolas, monospace` |

All four are **self-hosted through `@fontsource`**, imported at the top of
`design-system.css`. Not a CDN: the POS is offline-first, and a font request
that hangs shows a cashier nothing until it times out. Saudi PDPL work is also
easier without every till announcing itself to a third-party font host on boot.

`--font-mono` is for things that *are* machine output and are read for their
shape — a base64 CSR, a CSID, a hash. **Not for money.** Money uses the
interface face with `tabular-nums`; setting a total in a code face makes a
ledger look like program output.

## Space — 4px base

`--space-1` 4 · `--space-2` 8 · `--space-3` 12 · `--space-4` 16 ·
`--space-5` 24 · `--space-6` 32 · `--space-7` 48 · `--space-8` 64

Common usage: `--space-3`/`--space-4` for panel head and cell padding,
`--space-4` for panel body, `--space-5` for a dialog, `--space-2` for the gap
between buttons.

## Radius

`--radius-sm` 6px · `--radius-md` 8px · `--radius-lg` 12px · `--radius-full` 999px

Things you press get 6px. Things that carry figures get 8px. A 16px radius on a
data table reads as a toy — that is why there is no 16.

## Shadow — three levels, and level 3 is for dialogs alone

| Token | Use |
|---|---|
| `--shadow-1` | Panels and filled buttons at rest |
| `--shadow-2` | Defined, rarely used |
| `--shadow-3` | Dialogs and the language menu. Nothing else |

Borders do the separating; shadows only lift what genuinely floats.

## Tap targets

| Token | Value | Where |
|---|---|---|
| `--tap-desk` | 34px | Mouse-driven controls |
| `--tap-mobile` | 44px | Phone, and **any coarse pointer** |
| `--tap-pos` | 56px | Till buttons — `.button--large` |

Two mechanisms raise a control to 44px, and both are needed:

- `@media (max-width: 639px)` — a phone.
- `@media (pointer: coarse)` — a **tablet**, which is a finger at 768px and was
  the gap the width rules missed. A browser walk at 768px found 69 controls
  under 44px that were fine at 390px and fine at 1440px.

## The shell

`--rail-width` 244px · `--rail-width-collapsed` 68px · `--topbar-height` 56px

## Not tokens, but fixed

- `--scroll-shadow`: `rgb(16 20 35 / 20%)` light, `rgb(0 0 0 / 45%)` dark. Used
  only by `.ds-scroll-x`.
- Dialog backdrop: `rgb(8 11 18 / 45%)`, stated inline in `.dialog__backdrop`.
- Transition duration: `120ms ease` on colour/border/shadow. Nothing longer.
