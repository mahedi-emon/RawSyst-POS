// Form primitives, for the whole product.
//
// Written once because forms are where inconsistency shows most: a label above
// one field and beside the next, an error in red text here and a banner there,
// makes an application feel assembled rather than designed. These are the
// pieces every write screen uses.
//
// # Errors sit beside the field they belong to
//
// A banner saying "some details are missing" makes the reader hunt. The server
// returns field-level messages precisely so the form can put each one under its
// own input, and this is what consumes them.
//
// # The server is the authority on validity
//
// Client-side checks here exist to save a round trip and to stop somebody
// submitting an obviously empty form. They are never the only check — the same
// validation runs in Go, a test asserts it names every missing field, and a
// form that disagreed with the server would simply be wrong.

import type { ReactNode } from 'react';
import { useT } from '../i18n/locale';

/** Field-level messages, keyed as the server keys them. */
export type FieldErrors = Record<string, string>;

export function Field({
  label,
  hint,
  error,
  htmlFor,
  required,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  htmlFor: string;
  required?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <div className={`field${error ? ' field--bad' : ''}`}>
      <label className="field__label" htmlFor={htmlFor}>
        {label}
        {/* A word, not an asterisk. An asterisk needs a legend somewhere else
            on the page, which is one more thing to read and to translate. */}
        {!required && <span className="field__optional"> {t('field.optional')}</span>}
      </label>

      {hint && <span className="field__hint">{hint}</span>}
      {children}

      {/* aria-live so a screen reader announces the message when it appears
          rather than only when the field is next focused. */}
      {error && (
        <span className="field__error" id={`${htmlFor}-error`} role="alert">
          {error}
        </span>
      )}
    </div>
  );
}

export function TextInput({
  id,
  value,
  onChange,
  placeholder,
  error,
  inputMode,
  type = 'text',
  autoFocus,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  error?: string;
  inputMode?: 'text' | 'decimal' | 'numeric' | 'email' | 'tel';
  type?: string;
  autoFocus?: boolean;
}) {
  return (
    <input
      id={id}
      className="input"
      type={type}
      inputMode={inputMode}
      value={value}
      placeholder={placeholder}
      autoFocus={autoFocus}
      aria-invalid={error ? true : undefined}
      aria-describedby={error ? `${id}-error` : undefined}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

export function SelectInput<T extends { id: string }>({
  id,
  value,
  onChange,
  options,
  label,
  placeholder,
  error,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  options: T[];
  label: (option: T) => string;
  placeholder?: string;
  error?: string;
}) {
  return (
    <select
      id={id}
      className="input"
      value={value}
      aria-invalid={error ? true : undefined}
      aria-describedby={error ? `${id}-error` : undefined}
      onChange={(e) => onChange(e.target.value)}
    >
      {/* An explicit empty option rather than defaulting to the first. A form
          that pre-selects a supplier invites somebody to raise an order against
          whoever happened to sort first. */}
      {placeholder && <option value="">{placeholder}</option>}
      {options.map((o) => (
        <option key={o.id} value={o.id}>
          {label(o)}
        </option>
      ))}
    </select>
  );
}

/** What went wrong that was not about one field.
 *
 * A duplicate supplier code, a refused permission, a server that was not
 * there. Shown once at the top of the form, where somebody who has just
 * pressed Save is already looking. */
export function FormError({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <p className="form__error" role="alert">
      {message}
    </p>
  );
}

export function FormActions({
  submitLabel,
  busy,
  disabled,
  onCancel,
  children,
}: {
  submitLabel: string;
  busy?: boolean;
  disabled?: boolean;
  onCancel: () => void;
  children?: ReactNode;
}) {
  const t = useT();
  return (
    <div className="form__actions">
      <button
        className="ds-btn ds-btn--primary"
        type="submit"
        disabled={busy || disabled}
      >
        {busy ? 'Saving…' : submitLabel}
      </button>
      <button className="ds-btn ds-btn--quiet" type="button" onClick={onCancel}>
        {t('action.cancel')}
      </button>
      {children}
    </div>
  );
}
