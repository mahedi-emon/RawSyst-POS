# Panels and dialogs

## Panel

**A panel is a border and a background — no shadow at rest.** Elevation is for
things that float above the page, not for everything.

```tsx
<section className="ds-panel" aria-label={t('section.name')}>
  <div className="ds-panel__head">
    <h2 className="ds-h3">{t('section.name')}</h2>
    <div className="ds-panel__actions">
      <button className="ds-btn ds-btn--secondary">{t('action.export')}</button>
    </div>
  </div>

  <div className="ds-panel__body">…</div>

  <div className="ds-panel__foot">
    <button className="ds-btn ds-btn--primary">{t('action.save')}</button>
  </div>
</section>
```

| Part | Behaviour |
|---|---|
| `.ds-panel` | `--surface`, 1px `--border`, `--radius-md`, `--shadow-1` |
| `.ds-panel__head` | Flex, space-between, **`flex-wrap: wrap`**, `min-block-size: 48px`, separated from the body by a rule |
| `.ds-panel__actions` | The panel's own controls, grouped at the far edge |
| `.ds-panel__body` | `--space-4` padding |
| `.ds-panel__body--flush` | No padding — for a body that holds only a table. The table's cell padding is the margin, and doubling it wastes a column's width |
| `.ds-panel__foot` | End-justified, `--surface-sunken`, rule above, bottom corners rounded |

The head is separated by a **rule**, not by whitespace alone. A panel whose
title floats above its contents with nothing between them reads as two unrelated
things; the rule is what makes it one object with a name.

**On a phone the head stacks** (`flex-direction: column; align-items: stretch`).
Centred against a tall control block the title ended up in the *middle* of it —
the customers screen read "[search field] / Customers / [include retired]", with
the panel's own name between two of its own controls, so the reader met the
controls before being told what they were for.

Most panel titles are `.ds-h3`, not `.ds-h2` — reserve `.ds-h2` for a section
above several panels.

## Dialog

A dialog is the **one thing in this system that gets `--shadow-3`.** Everything
else about it is the same panel the rest of the product is made of.

```tsx
<div className="dialog__backdrop">
  <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="d-title">
    <h2 className="ds-h2" id="d-title">{t('revoke.title')}</h2>
    <p className="dialog__body">{t('revoke.explain')}</p>
    {/* fields, if it asks for something */}
    <div className="form__actions">
      <button className="ds-btn ds-btn--danger">{t('action.revoke')}</button>
      <button className="ds-btn ds-btn--quiet" onClick={close}>{t('action.cancel')}</button>
    </div>
  </div>
</div>
```

| Part | Behaviour |
|---|---|
| `.dialog__backdrop` | `position: fixed; inset: 0`, z-index 100, `rgb(8 11 18 / 45%)`, grid place-items centre |
| `.dialog` | `min(480px, 100%)`, `max-block-size: min(86dvh, 720px)`, scrolls, `--surface-raised`, `--radius-lg`, `--shadow-3`, `--space-5` padding, `display: grid; gap: --space-3` |
| `.dialog__body` | The explanatory paragraph, `--text-muted` |
| `.dialog--danger` | `--danger-border` all round plus a 3px `--danger` top edge |

**On a phone a dialog is a bottom sheet.** The backdrop is `align-items: end` by
default and only becomes `center` at `min-width: 640px` — one rule covering both,
and the phone case is the default because that is where a thumb is.

Use `.dialog--danger` for anything irreversible, and make the confirm
`ds-btn--danger`. The control that *opened* it should have been `ds-btn--warn`.

`.dialog`, `.dialog__backdrop` and `.dialog__body` are defined in both
`design-system.css` and `dashboard.css`; the design-system definitions are the
current ones.

## Empty and state panels

A state that fills a panel goes inside it, not instead of it — see
`patterns/states.md`.

## Elevation summary

| Level | Used by |
|---|---|
| `--shadow-1` | `.ds-panel`, filled buttons, `.zatca__env--on` |
| `--shadow-2` | Defined, effectively unused |
| `--shadow-3` | `.dialog`, `.lang__menu` |

If you are reaching for a shadow on something that does not float above the
page, use a border instead.
