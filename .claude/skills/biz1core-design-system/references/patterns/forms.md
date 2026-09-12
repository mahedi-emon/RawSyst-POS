# Forms

Forms are where inconsistency shows most: a label above one field and beside the
next, an error in red text here and a banner there, makes an application feel
assembled rather than designed. Use the primitives in `shared/src/ui/Form.tsx`
rather than assembling a field by hand.

## The canonical markup

```tsx
<form className="form" onSubmit={onSubmit}>
  <FormError message={formError} />

  <div className="form__grid">
    <Field label={t('supplier.name')} htmlFor="name" required error={errors.name}>
      <TextInput id="name" value={name} onChange={setName} error={errors.name} />
    </Field>

    <Field label={t('supplier.terms')} htmlFor="terms" hint={t('supplier.termsHint')}>
      <SelectInput
        id="terms" value={terms} onChange={setTerms}
        options={termOptions} label={(o) => o.name}
        placeholder={t('common.choose')}
      />
    </Field>
  </div>

  <FormActions submitLabel={t('action.save')} busy={saving} onCancel={close} />
</form>
```

`Field` renders `.field` (plus `.field--bad` when `error` is set), a
`.field__label` bound by `htmlFor`, an optional `.field__hint`, the control, and
a `.field__error` with `role="alert"` and `id="{htmlFor}-error"`.

## Rules

**Errors sit beside the field they belong to.** A banner saying "some details
are missing" makes the reader hunt. The server returns field-level messages
precisely so the form can put each one under its own input; `FieldErrors` is
`Record<string, string>` keyed exactly as the server keys it. `FormError` is for
what was *not* about one field — a duplicate code, a refused permission, a
server that was not there.

**The server is the authority on validity.** Client checks exist to save a round
trip and stop an obviously empty submit. The same validation runs in Go and a
test asserts it names every missing field. A form that disagreed with the server
would simply be wrong.

**Optional is marked, required is not.** `Field` writes the word
`t('field.optional')` after the label when `required` is false. A word, not an
asterisk — an asterisk needs a legend somewhere else on the page, which is one
more thing to read and to translate.

**A select never pre-selects.** `SelectInput` renders an explicit empty
`<option>` when given a `placeholder`. A form that pre-selects a supplier
invites somebody to raise an order against whoever happened to sort first.

**Errors are announced.** `role="alert"` on the message and `aria-invalid` +
`aria-describedby` on the control, so a screen reader hears it when it appears
rather than when the field is next focused.

## The field box

`.field__input` and `.input` are **two names for one set of declarations**,
defined together in `design-system.css` on purpose. Two vocabularies is how the
original gap opened — `.field__input` was used by twenty-one controls across ten
files and defined nowhere, so each of them rendered as a bare operating-system
widget. The duplication is the point: a change to how a field looks cannot now
reach one half of the product and not the other. `TextInput` and `SelectInput`
both emit `className="input"`.

What the box already handles:

| | |
|---|---|
| Hover | `border-color: var(--text-subtle)` — how a mouse learns the whole box is the target, not just the caret |
| Focus | `--brand` border + `0 0 0 3px var(--brand-subtle)` ring |
| Invalid | `[aria-invalid='true']`, `.field--bad`, `.input--bad` → danger border, danger-subtle focus ring |
| Disabled | `--surface-sunken`, `--text-muted`, `not-allowed` |
| Select | Native appearance stripped and the chevron redrawn as an inline SVG data URI — no network, no icon font. It flips to the other edge under `[dir='rtl']` and has a separate dark-theme stroke colour |
| Date / time | Reduced inline-end padding, because the native picker button sits badly on a compact box |
| Number | `appearance: textfield` and both webkit spin buttons removed. **A stray scroll over a focused number input silently changes an amount** — every money field in this product has a keypad or a typed value instead |
| Textarea | `min-block-size: 5.5rem`, `resize: vertical` |
| Sizing | `--tap-desk` normally; `--tap-mobile` under 640px **and** under `pointer: coarse` |

## Actions

`FormActions` renders `.form__actions` with a `ds-btn ds-btn--primary` submit
and a `ds-btn ds-btn--quiet` cancel, and takes `busy` / `disabled` / extra
`children`. One primary per form.

## Layout

`.form` `.form__body` `.form__grid` `.form__aside` `.form__lines`
`.form__lineshead` `.form__empty` are in `dashboard.css`. Spacing comes from a
`gap` on the container, not a margin on the field — a margin was measured at 0px
between rows before the gap was introduced.

## Quantities

`.cart__qty` is the shared quantity cell — 72px, end-aligned, `--border-strong`,
`--tap-desk` (44px under 640px). It is used by both the till counter and the
purchase-order screen, which is why it lives in `design-system.css` rather than
in either app's sheet.

## Never

- A native unstyled `<input>`, `<select>` or `<textarea>`. Give it `.input`.
- A validation message anywhere but under its own field, unless it genuinely is
  not about one field.
- `type="number"` on a money field without understanding the scroll hazard above.
- Client-only validation treated as sufficient.
