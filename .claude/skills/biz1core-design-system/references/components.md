# The class catalogue

Before writing a new class, check here. `D` = defined in
`shared/src/design-system.css`, `Db` = `shared/src/dashboard/dashboard.css`.
Both are loaded by both apps, so both are safe to name from `shared/`.

## Type and figures — D

`.ds-display` `.ds-h1` `.ds-h2` `.ds-h3` `.ds-body-sm` `.ds-caption`
`.ds-muted` `.ds-subtle` `.num` `.numeric` `.ds-date` `.ds-up` `.ds-down`
`.ds-link` `.ds-visually-hidden`

## Buttons — D

Two families exist and both are real. **`.ds-btn` is the current one** — use it
for anything new.

| Class | What |
|---|---|
| `.ds-btn` | Base: inline-flex, `--tap-desk`, `--radius-sm`, 0.933rem/500 |
| `.ds-btn--primary` | Brand fill. **One per screen** |
| `.ds-btn--secondary` | Surface + `--border-strong`. The default non-primary |
| `.ds-btn--quiet` | Brand text, no border at rest. What a table row's actions are made of |
| `.ds-btn--danger` | Filled red. The button a confirmation dialog *ends* with |
| `.ds-btn--danger-quiet` | Red text. A destructive action that is not the primary one |
| `.ds-btn--warn` | Red text for a destructive action *inside a row* — the control that **opens** the dialog |
| `.ds-btn--sm` | 28px, 0.866rem |
| `.ds-btn--block` | Full width |

`.button` / `.button--primary` / `.button--quiet` / `.button--large` is the
older family, still used by the till (`.button--large` is the 56px tender
button, and `.tenders .button` gets a 1.06rem face). Do not remove it; do not
extend it.

## Surfaces — D

`.ds-panel` `.ds-panel__head` `.ds-panel__actions` `.ds-panel__body`
`.ds-panel__body--flush` `.ds-panel__foot`

`.dialog` `.dialog__backdrop` `.dialog__body` `.dialog--danger`

## Tables — D

`.ds-table` `.ds-table--pairs` `.ds-table__actions` `.ds-scroll-x` `.ds-check`

## Status — D

`.ds-badge` plus one of `.ds-badge--success` `.ds-badge--warning`
`.ds-badge--danger` `.ds-badge--info` `.ds-badge--neutral`

## States — D

`.ds-skeleton` `.ds-state` `.ds-state__title` `.ds-state__body`
`.ds-state__actions`

## Fields — the box is D, the furniture is Db

| Class | Where | What |
|---|---|---|
| `.field` | D | The grid wrapper, `gap: --space-1` |
| `.field__input` / `.input` | D | **Two names, one set of declarations.** Both defined together on purpose so a change cannot reach half the product |
| `.field--bad` / `.input--bad` | D | Invalid |
| `.cart__qty` | D | A quantity cell — shared by the till counter and the purchase-order screen |
| `.field__label` | **Db** | |
| `.field__hint` | **Db** | |
| `.field__error` | **Db** | |
| `.field__optional` | **Db** | |
| `.form__error` | **Db** | Form-level, not field-level |
| `.form__actions` | **Db** | |
| `.field__fixed` `.input--narrow` | Db | |

## Shell and navigation — D

`.app` `.bo` `.app__nav` `.app__navlink` `.app__navlink--on` `.app__spacer`
`.app__company` `.bo__iconbtn`

`.lang` `.lang__trigger` `.lang__current` `.lang__menu` `.lang__opt`
`.lang__opt--on` `.lang__native` `.lang__region` `.lang__tick`

## Sign-in — D

`.login` `.login__lang` `.login__card` `.login__title` `.login__subtitle`
`.login__problem` / `.login__error` (both resolve, so a rename cannot silently
unstyle an error) `.login__forgot`. `.login__hint` `.login__tenant`
`.login__tenants` are Db.

## ZATCA onboarding — D

`.zatca` and its `__head __envs __env __env--on __status __statusHead __next
__steps __step __step--done __stepMark __stepBody __stepTitle __warn __form
__csr __detail __detailSummary __csid __error __readonly __renew`.

Do not touch these without a specific instruction — they are the compliance
onboarding flow.

## Screen furniture — Db

Detail screens: `.detail` `.detail__head` `.detail__head--flat`
`.detail__titles` `.detail__actions` `.detail__back` `.detail__backarrow`
`.detail__summary` `.detail__split` `.detail__more` `.detail__open`
`.detail__rowbtn` `.detail__row--on` `.detail__row--credit` `.detail__strong`

`.detail__strong` is how this codebase marks **the cell carrying a row's
identity** — and it is not always the first column. The tablet table rules key
off it rather than off position.

Dashboard: `.dash` `.dash__grid` `.dash__head` `.dash__kpis` `.dash__figure`
`.dash__note` `.dash__date` `.dash__soon` `.dash__stockline` `.dash__row--aside`
`.kpi` and its `__label __value __chart __chev __foot` and `.kpi--opens`
`.figure` `.figure__value` `.figure--strong` `.ds-sparkline`

Attention list: `.attention` (a naming hook with no rules of its own — it sits
beside `.ds-panel` and is listed in the coverage test's `STYLING_HOOKS`)
`.attention__main` `.attention__side` `.attention__list` `.attention__row` plus
`.attention__row--critical` `.attention__row--warning` `.attention__row--notice`
`.attention__title` `.attention__count` `.attention__detail` `.attention__open`

Forms and pickers: `.form` `.form__body` `.form__grid` `.form__aside`
`.form__lines` `.form__lineshead` `.form__empty` `.picker` `.picker__list`
`.picker__option` `.picker__empty` `.picker--chosen`
`.segmented` `.segmented__btn` `.segmented__btn--on`

Money mix: `.mix` `.mix__row` `.mix__name` `.mix__amount` `.mix__count`
`.mix__bar` `.mix__track` `.mix__why`

Domain: `.customer__*` `.purchase__*` `.supplier__*` `.pairing__*`
`.receipt__*` `.settle__outstanding` `.gate` `.gate__body` `.badge--opens`

## React helpers

| Export | File | What |
|---|---|---|
| `Field` | `shared/src/ui/Form.tsx` | Label + hint + control + `role="alert"` error, wired by `htmlFor` |
| `TextInput` `SelectInput` | same | `className="input"`, `aria-invalid`, `aria-describedby` |
| `FormError` | same | Form-level message, `role="alert"` |
| `FormActions` | same | Primary submit + quiet cancel |
| `RemoteBody` | `shared/src/dashboard/DetailScreen.tsx` | Renders loading / denied / offline / error and hands `ready` back as a render prop |
| `EmptyState` | same | Title + body |
| `Icon` | `shared/src/ui/Icon.tsx` | ~30 hand-drawn paths, 24-unit grid, 1.7 stroke, always `aria-hidden` |
| `ThemeSwitch` | `shared/src/ui/ThemeSwitch.tsx` | Writes `data-theme` on the root |
| `CardTableLabels` | `shared/src/ui/CardTableLabels.tsx` | Mount once per app. Stamps `data-label` on table cells |
| `money` `percent` `direction` `isZero` `shortDate` `longDate` `monthName` `localName` `tenderName` | `shared/src/ui/format.ts` | The only sanctioned formatters |
| `useT` `useLocale` `LocaleProvider` `directionOf` | `shared/src/i18n/` | Strings and direction |

### Icons

`Icon` takes `name`, `size` (default 18) and `className`. Every glyph is
`aria-hidden` and `focusable="false"` — **an icon never carries meaning on its
own** in this product. The rail sets labels beside them, a button says what it
does in words, a status carries a word as well as a tint.

Names: `dashboard buying inventory stock accounting assets customers expenses
settlement devices people einvoicing setup branding salesorders counter returns
shift menu close chevron search globe card signout sun moon plus check alert`.

To add one: add a path to `PATHS` and the name to `IconName`. Keep the 24-unit
grid, 1.7 stroke, round caps and joins, no fills. A filled glyph among stroked
ones is the fastest way to make a rail look assembled from clip art.

Two pairs are deliberately distinct and must stay so: `customers` vs `people`
(who the shop sells to vs the shop's own staff), and `settlement` vs `card`
(what a card provider can take vs what the bank actually deposited).
