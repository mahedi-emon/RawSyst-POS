'use client';

// Form fields.
//
// # The label is wired, always
//
// Every control here is built around a generated id so the label points at it.
// That is not a nicety: a label that is only visually adjacent cannot be read
// by a screen reader, and cannot be tapped to focus the input, which is how
// most people use a form on a phone.
//
// # Errors come from the server's own words
//
// The API returns `fields: { discount: "Exceeds your approval limit." }`, and
// that sentence is written for the person reading it. `Field` takes it as-is.
// There is no client-side rewording layer, because the server is the only place
// that knows why a value was refused.

import { AlertCircle } from 'lucide-react';
import {
  createContext,
  useContext,
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react';

import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

interface FieldContextValue {
  id: string;
  describedBy: string | undefined;
  invalid: boolean;
}

const FieldContext = createContext<FieldContextValue | null>(null);

function useField(): FieldContextValue {
  const ctx = useContext(FieldContext);
  if (!ctx) throw new Error('Form controls must be used inside <Field>.');
  return ctx;
}

export interface FieldProps {
  label: ReactNode;
  /** Guidance shown before anything goes wrong. */
  hint?: ReactNode;
  /** The refusal. Shown instead of the hint, because two messages compete. */
  error?: string | null;
  required?: boolean;
  className?: string;
  children: ReactNode;
}

export function Field({
  label,
  hint,
  error,
  required,
  className,
  children,
}: FieldProps) {
  const t = useT();
  const base = useId();
  const id = `${base}-control`;
  const hintId = `${base}-hint`;
  const errorId = `${base}-error`;

  // Only one of the two is ever present, so the control is never pointed at a
  // node that is not rendered -- which reads as an empty description.
  const describedBy = error ? errorId : hint ? hintId : undefined;

  return (
    <FieldContext value={{ id, describedBy, invalid: Boolean(error) }}>
      <div className={cn('flex flex-col gap-1.5', className)}>
        <label
          htmlFor={id}
          className="text-label font-medium text-fg flex items-center gap-1"
        >
          {label}
          {required && (
            <>
              <span aria-hidden="true" className="text-critical">
                *
              </span>
              <span className="sr-only">{t('nx.field.required')}</span>
            </>
          )}
        </label>

        {children}

        {error ? (
          <p
            id={errorId}
            // Announced when it appears, so somebody who has just pressed Save
            // and is not looking at this field still hears why it did not save.
            role="alert"
            className="text-caption text-critical-fg flex items-start gap-1.5"
          >
            <AlertCircle className="size-3.5 shrink-0 mt-px" aria-hidden="true" />
            {error}
          </p>
        ) : hint ? (
          <p id={hintId} className="text-caption text-muted">
            {hint}
          </p>
        ) : null}
      </div>
    </FieldContext>
  );
}

/** Shared shell for every control, so they line up and focus identically. */
const control = [
  'w-full rounded-sm border bg-input-bg text-body text-fg',
  'placeholder:text-disabled',
  'transition-[border-color,box-shadow] duration-[120ms]',
  'disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-disabled',
  'aria-[invalid=true]:border-critical',
].join(' ');

export function Input({
  className,
  numeric,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & {
  /** Money or a quantity. Aligns to the end and uses tabular figures. */
  numeric?: boolean;
}) {
  const { id, describedBy, invalid } = useField();
  return (
    <input
      id={id}
      aria-describedby={describedBy}
      aria-invalid={invalid || undefined}
      // 40px: comfortably above the 44px thumb target once the label above it
      // is counted, and tall enough that a value is not cramped against the box.
      className={cn(
        control,
        'h-10 px-3',
        // A numeric field is read left to right even in Arabic, for the same
        // reason a total is: reordering the digits changes the figure. With
        // `direction: ltr` forced on the element, `text-right` IS the end of
        // the field in both scripts -- an `rtl:text-left` here was wrong, and
        // pushed the digits to the wrong side of an Arabic form.
        numeric && 'num [direction:ltr] text-right',
        className,
      )}
      {...props}
    />
  );
}

export function Textarea({
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const { id, describedBy, invalid } = useField();
  return (
    <textarea
      id={id}
      aria-describedby={describedBy}
      aria-invalid={invalid || undefined}
      className={cn(control, 'min-h-20 px-3 py-2 resize-y', className)}
      {...props}
    />
  );
}

export function Select({
  className,
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement>) {
  const { id, describedBy, invalid } = useField();
  return (
    <select
      id={id}
      aria-describedby={describedBy}
      aria-invalid={invalid || undefined}
      // The native select, deliberately. It is the control every mobile
      // operating system already knows how to present well, it is keyboard
      // accessible without a line of code, and it does not need a portal. A
      // custom listbox is warranted where options need searching or grouping
      // by something the platform cannot express -- not for a list of five
      // branches.
      //
      // `select-chevron` carries the arrow. It lives in globals.css because
      // `background-position` has no logical form: it takes `left` or `right`
      // and nothing else, so mirroring it needs a `[dir='rtl']` rule rather
      // than a token. Written inline it put the arrow on top of the text in
      // every Arabic select in the product.
      className={cn(
        control,
        'select-chevron h-10 ps-3 pe-8 appearance-none',
        className,
      )}
      {...props}
    >
      {children}
    </select>
  );
}

/** A checkbox with its label, for a permission list or a settings row. */
export function Checkbox({
  label,
  hint,
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & {
  label: ReactNode;
  hint?: ReactNode;
}) {
  const id = useId();
  return (
    <div className={cn('flex items-start gap-2.5', className)}>
      <input
        id={id}
        type="checkbox"
        className={cn(
          'mt-0.5 size-4 shrink-0 rounded-xs border border-input',
          'accent-[var(--ry-primary)]',
          'disabled:cursor-not-allowed disabled:opacity-55',
        )}
        {...props}
      />
      <div className="flex flex-col gap-0.5">
        <label htmlFor={id} className="text-body text-fg leading-snug">
          {label}
        </label>
        {hint && <p className="text-caption text-muted">{hint}</p>}
      </div>
    </div>
  );
}
