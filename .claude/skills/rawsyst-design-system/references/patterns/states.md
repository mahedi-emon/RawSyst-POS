# Loading, empty, error, offline, denied

**First-class, because the first run of any real deployment is entirely made of
these.** A screen that only looks right with data is a screen that looks broken
on day one.

## Do not hand-roll the switch

`RemoteBody` in `shared/src/dashboard/DetailScreen.tsx` renders the four states
that are not "ready" and hands `ready` back as a render prop, so each screen
contains only its own layout — the part that genuinely differs.

```tsx
const load = useCallback(() => auditTrail(companyId), [companyId]);
const { remote, reload } = useRemote(load);

<RemoteBody remote={remote} onRetry={reload}>
  {(data) => data.rows.length === 0
    ? <div className="ds-panel__body"><EmptyState title={…} body={…} /></div>
    : <div className="ds-panel__body ds-scroll-x">{/* the table */}</div>}
</RemoteBody>
```

`useRemote<T>(load: () => Promise<T>)` returns a `RemoteHandle<T>` —
`{ remote, reload, refreshing }`. Memoise `load` with `useCallback`; there is no
dependency-array argument. `Remote<T>` is a discriminated union over
`loading | denied | offline | error | ready`, so `remote.data` is only reachable
after narrowing on `remote.state === 'ready'`. Both live in
`shared/src/dashboard/useRemote.ts`.

`refreshing` is true while a reload runs over data already on screen. Keep
showing what you have rather than collapsing back to a skeleton — a table that
blanks on every refresh loses the reader's place.

## The five states

| State | What is rendered |
|---|---|
| `loading` | A **table-shaped** skeleton — six `.ds-skeleton` bars in a `.ds-panel`, with `aria-busy="true"` and an `aria-label`. Shaped like the real layout so the page does not jump when data lands |
| `denied` | A `.ds-state` panel: "no access to this" + why. Never an error |
| `offline` | A `.ds-state` panel with a retry. **Neutral, not red** — the system is working; the network is not |
| `error` | A `.ds-state` panel carrying the server's own message, plus retry |
| `ready` + no rows | `EmptyState` inside a `.ds-panel__body` |

## Empty is not a shrug

```tsx
<EmptyState title={t('audit.noneTitle')} body={t('audit.noneBody')} />
```

An empty list is a **fact about the business** — a quiet day, a healthy stock
room — and saying which one reassures the reader that the screen is working.
Write it in the screen's own words. Never "No data".

## The markup

```
.ds-state              grid, centred, --space-6/--space-4 padding, --text-muted
.ds-state__title       1rem/600 in --text
.ds-state__body        max-inline-size: 46ch, 0.9rem/1.55
.ds-state__actions     flex, wrap, centred — for a call to action under the words
```

`.ds-state > * { margin: 0 }` is load-bearing: both lines arrive as `<p>` with
the browser's block margin, which opened a gap between them wider than the gap
around the whole block and read as two unrelated sentences.

## Skeletons

`.ds-skeleton` is a 400%-wide three-stop gradient animated over 1.4s. Size it
inline (`style={{ blockSize: 20 }}`) — it has no size of its own.

Under `@media (prefers-reduced-motion: reduce)` the shimmer stops **and every
transition in the product drops to 1ms.** Respected, not optional: vestibular
disorders are common and a shimmering dashboard is genuinely unpleasant for
people who have one.

## Offline is a normal operating state

The POS is designed to work offline. `--offline` and `--offline-subtle` are
neutral greys, and `.ds-badge--neutral` uses them. **Do not colour an offline
state amber** — that teaches cashiers to ignore amber, which is where the
warnings that do matter live.

## Attention rows

The dashboard's list of things needing action uses `.attention__row` with one of
`.attention__row--critical`, `.attention__row--warning` or
`.attention__row--notice`, plus `.attention__title`, `.attention__count`,
`.attention__detail` and `.attention__open`. Severity is carried by the
modifier, and the row still says in words what it is.
