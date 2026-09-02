# Rules, guardrails and the mistakes this system has already made

## The mechanical guardrail

`shared/src/ui/stylesheetCoverage.test.ts` fails the build when a component in
`shared/` names a class that no stylesheet the **browser app** imports defines.

This bug has shipped three times, always the same way: a component in `shared/`
is rendered by both apps, but only the POS imports `pos/src/styles.css`, so
anything defined there alone renders bare in the back office. It survived review
each time because *the POS looked perfect*, and because a page with no CSS still
has no horizontal overflow — which is what the responsive checks measure.
Nothing failed; it just looked wrong, on the screens a reviewer is least likely
to open.

Casualties: `box-sizing` (P51), then `.field` / `.field__input` (P52), then
`.button` and the entire sign-in screen.

**The rule:** a component in `shared/` may only name classes defined in
`design-system.css` or `dashboard.css`. If a rule is till-specific, put the
shared shape in the system and keep only the override in `pos/src/styles.css`.

A class that genuinely carries no rules — a naming hook beside a `ds-` primitive,
like `<section className="ds-panel attention">` — goes in the test's
`STYLING_HOOKS` set. Anything added there must be a naming hook and nothing else.

## Anti-patterns, with the damage each one did

| Do not | What happened |
|---|---|
| Write a script size boost on an inherited selector | `:lang(ar) { font-size: 1.15em }` compounded at every nesting level: 1.15⁴ ≈ 1.75× four levels down, and deeper elements were *larger* than shallower ones. The scale inverted — navigation was set larger than the page heading |
| Mark a date `.num` | It took the mono face and pushed to the column's end, so the header sat at the start and the values at the end. Every document list had one column that looked misaligned |
| Force a numeric column header to LTR | "القيمة" came out with its leading character stranded at the wrong end |
| Set a blanket `min-inline-size` on tablet tables | 46rem on every table made a 652px table 736px wide and pushed the value column off the edge |
| Add `white-space: nowrap` to free-text columns | "Jeddah Fabric Mills Company Limited" held to one line pushed a 650px table out to 867px |
| Use flex for the phone bottom bar | Nine sections at 390px overflowed, because a flex item will not shrink below its own text. It is a grid with `auto-fit` |
| Hide `thead` with `display: none` in card mode | Removes the association a screen reader needs. Clip it off-screen instead |
| Use `opacity: 0` to hide the collapsed rail's captions | Opacity is not hiding; it is invisible ink, still in the accessibility tree |
| Colour an offline state amber | Teaches cashiers to ignore amber, which is where the warnings that matter live |
| Give a table row `cursor: pointer` unconditionally | A static list should not pretend to be clickable. Scoped to rows containing `.detail__rowbtn` |
| Leave a native `<input type="number">` with spinners on a money field | A stray scroll over a focused input silently changes an amount |
| Put a bare checkbox in a `<td>` | 24 targets measured 13px on a tablet, on the screen where a shop ticks which card payments went into a deposit — the whole interaction. Wrap it in `.ds-check` |
| Scope tap-target growth to `max-width: 639px` alone | A tablet is a finger at 768px. A browser walk found 69 controls under 44px there. Use `pointer: coarse` as well |
| Set a panel head's alignment to centre on a phone | The title ended up between two of its own controls: "[search] / Customers / [include retired]" |
| Let `.ds-caption` run inline inside a table cell | "Main Branch" + "till-1" read as "Main Branchtill-1" — one mangled name instead of two facts |
| Give a cell caption no measure | A nine-field sentence claimed most of the row and squeezed a two-word branch name onto two lines. `max-inline-size: 46ch` |
| Turn a two-column table into cards | Six rows filled a phone screen and the amounts stopped lining up — the entire reason they were a table. Use `.ds-table--pairs` |
| Write an override in an earlier stylesheet | `dashboard.css` is imported after `design-system.css`, so a rule there wins however the earlier one is written. Put the override next to what it overrides |

## Things that are settled and should not be re-litigated

- **No Tailwind, no `shadcn init`, no runtime shadcn components.** Reading
  external registries for reference is encouraged; importing their idiom is not.
  See `FRONTEND_TOOLBOX.md` §4 for the full reasoning and the exact commands
  that are safe.
- **No icon library.** ~30 hand-drawn paths on one grid.
- **No i18n framework.** A typed flat record.
- **No animation library.** A skeleton shimmer and 120ms colour transitions.
- **No CSS-in-JS, no CSS modules, no utility classes.** Plain stylesheets with
  BEM-ish names and custom properties.
- **Light theme is the default**, not `prefers-color-scheme`. Dark is opt-in via
  `data-theme="dark"` and is complete.
- **Currency as an ISO code, never a symbol.**
- **Negatives in parentheses, never a hyphen.**
- **The mono face is for machine output, never for money.**

## Before you finish a UI change

1. Did you use a token for every colour, space, radius and shadow?
2. Logical properties throughout — nothing that breaks the Arabic mirror?
3. Every string through `t()`, every amount through `money()`, every date
   through `shortDate`/`longDate`?
4. Does it work at 375px, 768px and 1440px, and under `pointer: coarse`?
5. Loading, empty, error, offline and denied — all five, via `RemoteBody`?
6. Is the focus ring intact and is every interactive thing reachable by keyboard?
   These apps are driven by keyboard and scanner far more than by mouse.
7. Does colour carry any meaning on its own? It must not.
8. Run the checks below.

## The checks

```bash
cd shared && npx vitest run          # includes stylesheetCoverage + i18n coverage
cd pos    && npx vitest run
cd shared && npx tsc --noEmit
cd pos    && npx tsc --noEmit
cd web    && npx tsc --noEmit
```

And this skill's own drift check:

```bash
node .claude/skills/rawsyst-design-system/verify.mjs
```

### The design-system lints in `e2e/`

Four of these are about this system specifically and need no browser. They
**report rather than fail** — a one-off value is sometimes right, and the
comment beside it is where that gets argued — so read the output, do not just
check the exit code.

| Script | Finds |
|---|---|
| `node e2e/tokens.mjs` | Literal colours, 4px-scale pixel spacing and radii written where a token exists. The moment one sheet writes `#1f6feb` and another `var(--brand)`, they drift on the next change |
| `node e2e/classes.mjs` | Class names the components use and no stylesheet defines — the `.dialog` failure that left the terminal-revoke confirmation unstyled in the corner of the page |
| `node e2e/strings.mjs` | User-facing English that never reaches the catalogue |
| `node e2e/currency.mjs` | A screen that shows money and never says what currency it is in. The rule is **per screen, not per figure** — a bare `money(x)` is fine; a file where *every* call is bare is not |

The browser-driven ones are `audit.mjs` (`npm run e2e`), `layout-probe.mjs`
(measures overflow and header/cell alignment) and `workflows.mjs` (presses real
buttons); `shots.mjs`, `reachable.mjs`, `pos-spec.mjs`, `tauri.mjs` and
`seed-trade.mjs` cover other ground. Several rules above were found by the
layout probe rather than by reading the stylesheet — when a responsive change
looks right and you are not sure, measure it.
