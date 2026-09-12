# Tables

Built for real business data: many rows, read in columns, scanned for outliers.
**Zebra striping is deliberately absent** — a hairline rule per row is quieter
and does not fight the status colours that carry meaning.

## The canonical markup

```tsx
<section className="ds-panel" aria-label={t('acct.trail')}>
  <div className="ds-panel__head">
    <h2 className="ds-h3">{t('acct.trail')}</h2>
    <p className="ds-caption">{t('audit.appendOnly')}</p>
  </div>

  <RemoteBody remote={remote} onRetry={reload}>
    {(data) => data.rows.length === 0 ? (
      <div className="ds-panel__body">
        <EmptyState title={t('…noneTitle')} body={t('…noneBody')} />
      </div>
    ) : (
      <div className="ds-panel__body ds-scroll-x">
        <table className="ds-table">
          <caption>{t('common.amountsIn', { currency: data.base_currency })}</caption>
          <thead>
            <tr>
              <th scope="col">{t('col.date')}</th>
              <th scope="col" className="num">{t('col.amount')}</th>
            </tr>
          </thead>
          <tbody>
            {data.rows.map((r) => (
              <tr key={r.id}>
                <td className="ds-date">{longDate(r.date, locale)}</td>
                <td className="num">{money(r.amount)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )}
  </RemoteBody>
</section>
```

Points that are not optional:

- `.ds-scroll-x` **on the panel body**, always. A wide table scrolls inside its
  panel; the page never scrolls sideways.
- `scope="col"` on every header cell.
- `.num` on the `<th>` as well as the `<td>` — the header must sit over its
  column. The header keeps the interface face (`.ds-table th.num` re-states
  `font-family: inherit`); only the cells are forced LTR.
- `.ds-date` on date cells, never `.num`.
- A `<caption>` is where a table says what currency it is denominated in. UI
  spec §8 requires an amount to carry its currency; a table six money-columns
  wide says it **once**, before the figures, where a reader and a screen reader
  both meet it first. `caption-side: top`. The real key is
  `t('common.amountsIn', { currency })` — `t` takes an optional
  `Record<string, string | number>` of interpolation params. Where the caption
  would be visual noise but is still owed to a screen reader, use
  `<caption className="ds-visually-hidden">`.

## What the styles already do for you

| Behaviour | Detail |
|---|---|
| Sticky header | `.ds-table th` is `position: sticky; inset-block-start: 0` on `--surface`, z-index 1 |
| Row hover | `--surface-sunken`, and `cursor: pointer` **only** where the row contains `.detail__rowbtn` — a static list does not pretend to be clickable |
| Last row | No bottom border |
| Totals | `tfoot td` gets weight 600 and a 2px `--border-strong` rule above. Put the total in `tfoot`, not in a paragraph under the table |
| Flush edges | Inside `.ds-panel__body--flush`, first/last cells get `--space-4` inline padding so the table reads as part of the panel rather than a box within a box |
| Captions in cells | `.ds-caption` inside a `td` or a `tbody th` becomes a **block** with `max-inline-size: 46ch`. It is a second line, not a suffix — inline, "Main Branch" + "till-1" ran together as "Main Branchtill-1" |

## The scroll shadow

`.ds-scroll-x` paints four background layers: two `local` covers in `--surface`
that scroll with the content, and two `scroll` shadows fixed to the container.
At the start of the scroll the cover hides the shadow; scroll and the cover
moves away, revealing "there is more back there". No JavaScript, no resize
listener, no scroll handler, and it mirrors for free.

It also carries `min-inline-size: 0` — without that, a scroll container inside a
flex or grid parent is sized by its contents and never scrolls; it just makes
its parent wider.

## Checkboxes in cells

Wrap the native input in `.ds-check`:

```tsx
<td><label className="ds-check"><input type="checkbox" … /></label></td>
```

The **label is the target** and is grown to 24px (44px under `pointer: coarse`).
The control stays native — a div with an icon loses the platform focus ring, the
keyboard behaviour and high-contrast rendering, and gains nothing a shop can
see. Above 640px a tick-only column is squeezed to `inline-size: 1%`; below it
that rule is off, because every cell is a block and "as narrow as possible"
would be three pixels.

## Row actions

```tsx
<td>
  <div className="ds-table__actions">
    <button className="ds-btn ds-btn--quiet">{t('action.edit')}</button>
    <button className="ds-btn ds-btn--warn">{t('action.void')}</button>
  </div>
</td>
```

`.ds-table__actions` is `flex-wrap: wrap` and end-justified, so the eye finds
the verbs in the same place on every row. It wraps rather than `nowrap` — a
horizontal scrollbar to reach a Remove button is worse than a second line.
(`.supplier__actions` and `.detail__actions` inside a cell are pinned to
`nowrap` because they are two or three short verbs.)

## The three responsive behaviours

### ≥ 1100px — an ordinary table.

### 640–1099px (tablet) — no blanket minimum width.

A first attempt set `min-inline-size: 46rem` on every table and made the tables
that *did* fit scroll for no reason — the buying list sat comfortably in 652px
until it was told to be 736px, at which point the value column went off the
right edge. Instead, two rules say what specific columns need:

- `td:has(.detail__strong)` → `min-inline-size: 11rem` (the identity cell)
- `td:has(> .ds-caption)` → `min-inline-size: 9rem` (a cell holding a sentence)

Everything else is left to wrap. A short value on two lines is untidy; a figure
disappearing off the side of the screen is a defect. **Do not add
`white-space: nowrap` to other columns** — that was tried and it pushed a
650px table out to 867px.

### < 640px — cards, not a narrower table.

Every `tr` becomes a bordered card; every `td` becomes a labelled row with the
column name from `::before { content: attr(data-label) }`. `thead` is moved
off-screen with a clip, **not** `display: none` — the header is how a screen
reader associates each cell with its column.

The `data-label` attributes are **stamped at runtime** by
`shared/src/ui/CardTableLabels.tsx` from the table's own `<th>` text. Mount that
component once per application, next to the locale provider. Do not hand-write
`data-label` — 174 attributes across 16 files is 174 chances to label a figure
with the wrong heading, which on a money table is worse than not transforming.
It reads the **last** header row (a grouped header puts the real names nearest
the body), it is idempotent, and it batches into an animation frame.

Details the card rules handle:
- The label is forced to `--font-latin` (or `--font-arabic` under `:lang(ar)`)
  and `direction: inherit`, because inheriting a numeric cell's font put "Owed"
  and "Limit" in a code face beside "Customer" in the interface face.
- A cell with more than one child wraps: the value stays on the label's line,
  everything after it takes a full line. Without this, a status badge and its
  ten-item explanation sat side by side in two narrow columns, forty lines tall.
- `tfoot` loses its box and gets a rule above — a total is a total, not another
  card.
- `.ds-scroll-x` is released to `overflow-x: visible`.

### `.ds-table--pairs` — a two-column table stays a pair on a phone

For label-and-amount tables: "where the money is", the stock summary, the
expense breakdown, the shift takings. Turned into cards they became the worst of
both — every row a bordered box with the label on one line and the figure on the
next, so six rows filled a screen and the amounts no longer lined up with each
other, which is the entire reason they were a table.

With `.ds-table--pairs` the row stays one line: label at the start, figure at
the end, no box. It reads like a receipt, which is what it is. **Use it whenever
a table is exactly two columns and the second is a figure.** It also opts out of
the tablet minimum-width rules.
