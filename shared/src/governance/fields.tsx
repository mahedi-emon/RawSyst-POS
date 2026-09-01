// Two compositions of the existing form primitives.
//
// `Field` carries the label and `TextInput` carries the control, and every form
// in this product pairs them by hand. The governance screens have a great many
// short forms — a consent, an incident, a retention policy, a disclosure — and
// spelling the pair out each time buries what each form is actually asking.
//
// No new markup and no new classes: these render exactly what the hand-written
// pair renders, which is what keeps them consistent with every other form
// rather than being a second style of field.

import {
  Field,
  SelectInput,
  TextInput,
} from '../ui/Form';

export function LabelledText({
  id,
  label,
  value,
  onChange,
  hint,
  placeholder,
  type,
  inputMode,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
  placeholder?: string;
  type?: string;
  inputMode?: 'text' | 'decimal' | 'numeric' | 'email' | 'tel';
}) {
  return (
    <Field label={label} hint={hint} htmlFor={id}>
      <TextInput
        id={id}
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        type={type}
        inputMode={inputMode}
      />
    </Field>
  );
}

export function LabelledSelect({
  id,
  label,
  value,
  onChange,
  options,
  hint,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: Array<{ value: string; label: string }>;
  hint?: string;
}) {
  return (
    <Field label={label} hint={hint} htmlFor={id}>
      <SelectInput
        id={id}
        value={value}
        onChange={onChange}
        options={options.map((o) => ({ id: o.value, label: o.label }))}
        label={(o) => o.label}
      />
    </Field>
  );
}
