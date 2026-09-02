# Buttons and actions

Three weights, and **the hierarchy is carried by fill rather than by size.** A
screen with two competing filled buttons has no primary action.

## Choosing

| Situation | Class |
|---|---|
| The one thing this screen is for | `ds-btn ds-btn--primary` |
| Anything else with a border | `ds-btn ds-btn--secondary` |
| A verb repeated on every table row | `ds-btn ds-btn--quiet` |
| The destructive button a confirmation dialog **ends** with | `ds-btn ds-btn--danger` |
| The control that **opens** that dialog, inside a row | `ds-btn ds-btn--warn` |
| A destructive action that is not the primary one | `ds-btn ds-btn--danger-quiet` |
| Dense context | add `ds-btn--sm` (28px) |
| One button on its own row | add `ds-btn--block` |

The red distinction matters and is easy to get wrong. `--danger` is a filled red
button; a *row* of them makes a list look like a list of problems, and a
destructive action that looks identical to Edit gets pressed by accident. So the
row gets `--warn` (red text, red-subtle hover) and only the dialog's final
confirm gets the fill.

## The quiet button

`.ds-btn--quiet` is brand-coloured text with no border at rest, gaining a
`--brand-subtle` fill and a `--brand-border` hairline on hover. It was grey once
and that was wrong: grey text with no border and no underline is
indistinguishable from the data in the cell beside it, so it read as a label
rather than as something to press. It keeps the low weight, which is right for
an action repeated on every row, and takes the brand colour so it is legible
*as* an action.

Borders around every control produce the boxed-in look the brief rules out.

## Base behaviour

`.ds-btn` is `inline-flex`, centred, `gap: --space-2` (so an `Icon` sits beside
a label without extra markup), `min-block-size: --tap-desk`,
`padding-inline: --space-4`, `--radius-sm`, 0.933rem/500, `white-space: nowrap`,
and a 120ms transition on background, border and shadow. Disabled is
`opacity: 0.45; cursor: not-allowed`.

Under `pointer: coarse` — a tablet, not only a phone — it grows to
`--tap-mobile`.

## Links versus buttons

The product has almost no hypertext. It is an application, and **its verbs are
buttons**. `.ds-link` exists for the one genuine case: a stored document the
browser follows and downloads. Dressing that as a button would promise something
happens on this page.

Conversely `.login__forgot` is deliberately *not* a link — it does something
rather than going somewhere.

## The older `.button` family

`.button` / `.button--primary` / `.button--quiet` / `.button--large` predates
`.ds-btn` and is still what the till uses. Keep it working; do not extend it and
do not use it for new back-office work.

- `.button--large` is `--tap-pos` (56px) and full width — a primary action that
  must not be mis-tapped.
- `.tenders .button` gets a 1.06rem face: a tender button is pressed by thumb,
  hundreds of times a shift, while the cashier is looking at the customer.

## Badges are not buttons — until they are

`.ds-badge` is inline status text with a tint **and a hairline** (a pale fill
alone disappears on a white row, and twice over on a dark theme). Variants:
`.ds-badge--success` `.ds-badge--warning` `.ds-badge--danger` `.ds-badge--info`
`.ds-badge--neutral`.

Where a badge doubles as a drill-down control, `button.ds-badge` is grown to
`--tap-mobile` with `--space-3` inline padding on a phone and under
`pointer: coarse`. A badge measured 20px before that rule existed.

## Segmented controls

`.segmented` / `.segmented__btn` / `.segmented__btn--on` (in `dashboard.css`),
and the ZATCA environment picker `.zatca__envs` / `.zatca__env` /
`.zatca__env--on`. Use one when every option must stay visible because choosing
wrong is expensive — a dropdown would hide two of three behind a tap. It
scrolls rather than wraps on a narrow phone: a wrapped segmented control stops
reading as one control.

Both `.segmented__btn` and `.attention__open` set their tap-target override in
`dashboard.css`, not `design-system.css`, because `dashboard.css` is imported
after it and a rule in the earlier file would lose the cascade however it were
written. Put an override next to what it overrides.

## Colour is never the only signal

A status carries an icon or a word as well as a tint. A financial figure carries
a sign as well as `.ds-up` / `.ds-down`. Roughly 1 in 12 men has a colour vision
deficiency and a POS is used by whoever is on shift.
