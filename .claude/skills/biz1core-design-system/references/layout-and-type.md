# Type scale, breakpoints, shell

## The type scale

Root is **15px / 1.47**. Everything below is `rem`, so the whole scale moves
when the root does — which is how Arabic and Bangla get their size boost.

| Class | Size | Weight | Use |
|---|---|---|---|
| `.ds-display` | 2.6rem / 1.08 | 700 | **The POS running total, and nothing else** |
| `.ds-h1` | 1.46rem / 1.25 | 650 | Page title |
| `.ds-h2` | 1.06rem / 1.35 | 600 | Section |
| `.ds-h3` | 0.933rem / 1.4 | 600 | Panel title — this is what most `ds-panel__head` use |
| *(body)* | 1rem / 1.47 | 400 | Default |
| `.ds-body-sm` | 0.866rem / 1.5 | 400 | Dense secondary text |
| `.ds-caption` | 0.75rem / 1.35 | 500 | Labels and glosses, in `--text-muted` |

Colour helpers: `.ds-muted` (`--text-muted`), `.ds-subtle` (`--text-subtle`).

The gaps between h1, h2 and h3 are deliberately wide and the weights differ. An
earlier scale had h1 at 1.55 and h2 at 1.2, which left almost nothing between a
page title, a section and a card title — every screen read as one flat wall of
semibold text, which is most of what makes a dashboard look generated.

### Numbers

`.num` / `.numeric` — tabular figures, `'tnum' 1, 'cv05' 1`, `text-align: end`,
`nowrap`, `unicode-bidi: isolate`. Put it on the `<td>` **and** the `<th>` so
the header sits over its column; the header keeps the interface face and only
the cells are forced LTR.

`.ds-date` — **a date is not a figure.** Tabular figures for column alignment,
but `text-align: start`, the interface face, and `direction: ltr` +
`unicode-bidi: isolate` so "28 Aug 2026" is not reordered to "Aug 2026 28" by
the bidi algorithm inside an Arabic paragraph. Do not mark dates `.num`.

### Script sizing — applied once, at the root

```css
:lang(ar) { font-family: var(--font-arabic); }   /* any depth */
:lang(bn) { font-family: var(--font-bengali); }
html:lang(ar) { font-size: 17.25px; }            /* 15 × 1.15, root only */
html:lang(bn) { font-size: 16.5px; line-height: 1.62; }
```

The size multiplier is on `html` alone and stated rather than computed. It was
once `:lang(ar) { font-size: 1.15em }`, and because `:lang()` is inherited and
`em` is relative to the parent, the boost compounded at every nesting level —
four levels deep (an ordinary table cell) Arabic came out at 1.15⁴ ≈ 1.75×, and
deeper elements were *larger* than shallower ones. The scale did not merely
grow, it inverted. **Never write a script size boost on a non-root selector.**

Bangla gets more size and noticeably more leading because matras hang above
every word and several conjuncts descend.

## Breakpoints

Declared in `design-system.css` as documentation (custom properties cannot be
used in media queries). Hold any new query to this table.

| Name | Range | Device |
|---|---|---|
| xs | < 640 | Phone — owner monitoring, approvals |
| sm | 640–1023 | Large phone / small tablet |
| md | 1024–1279 | iPad — full back office, portrait POS |
| lg | 1280–1679 | Laptop |
| xl | ≥ 1680 | POS terminal, desktop back office |

The queries actually in the stylesheets are `max-width: 639px`,
`min-width: 640px`, `min-width: 640px and max-width: 1099px`, `max-width: 640px`
(fields), `max-width: 30rem` (ZATCA step rail), and `pointer: coarse`.

**The principle:** layout transformations, not hidden content. A table becomes a
card list on a phone; it does not lose columns. If a figure matters at 1680px it
matters at 375px — the owner checking margin on their phone at the airport is a
real, stated use case.

## Focus

One ring, product-wide, drawn *outside* the border so it is visible on a filled
button and a white field alike:

```css
outline: 2px solid var(--brand);
outline-offset: 2px;
```

Applied by `:focus-visible` globally and re-stated for `.ds-btn`,
`.field__input`, `.ds-check:focus-within`, `.app__navlink`, `.lang__opt`. These
apps are driven by keyboard and scanner far more than by mouse; an invisible
focus ring makes them unusable. Never remove it.

## The shell

```
.app / .bo          the application root
.app__nav           the section list
.app__navlink       one section
.app__navlink--on   the current one
.app__spacer        flex: 1 1 auto, pushes what follows to the far edge
.app__company       the company name block in the top bar
.bo__iconbtn        a square icon button in the top bar
```

- **Desktop/tablet:** a wrapping flex row. `flex-wrap` is load-bearing — nine
  sections do not fit across a 768px tablet, and without it the row extended
  past the viewport and took the whole *page* into horizontal overflow.
- **The current section is marked by weight and a 2px inset underline, not a
  filled pill.** A filled nav item competes with the primary action on the page.
- **Phone (< 640px):** a fixed bottom bar, because that is where a thumb is. It
  is a `grid` with `repeat(auto-fit, minmax(68px, 1fr))`, not a flex row — nine
  sections on one flex row at 390px pushed the last three off the screen,
  because a flex item will not shrink below its own text. The grid wraps to a
  second row instead, costing 44px of height and truncating no label. The marker
  moves to `border-block-start` so it reads against the bar's own edge.
  `.bo` / `.app` get `padding-block-end: calc(var(--tap-mobile) * 2 + var(--space-4))`
  so the last table row is never under the bar.
- The rail proper (dark, 244px, collapsible, pinned) lives in
  `web/app/back-office.css` — back office only. It deliberately does **not**
  expand on hover.

## The language menu

`.lang` › `.lang__trigger` (globe + current) › `.lang__menu` (absolute,
`--shadow-3`, z-index 60) › `.lang__opt` / `.lang__opt--on` with
`.lang__native`, `.lang__region`, `.lang__tick`.

One control whatever the list holds — two pills were right for two languages, a
third makes the top bar a row of tabs competing with the navigation. Each option
is written **in its own script** (العربية, বাংলা), because somebody looking for
Arabic looks for العربية, not for the word "Arabic". On a phone the label is
hidden and the globe is the affordance — except on the sign-in page
(`.login__lang .lang__current`), the one screen where the person most likely to
need the control is least able to read the page around it.
