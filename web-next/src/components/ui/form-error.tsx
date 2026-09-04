'use client';

// The refusal at the top of a form.
//
// # Why focus moves here
//
// A person using a keyboard or a screen reader presses Save, the form is
// refused, and — with an inline error beside each field and nothing else —
// nothing tells them. Focus is still on the button they pressed, the errors are
// somewhere above, and the only way to find out is to walk the form again.
//
// So this takes focus when it appears. `tabIndex={-1}` makes it focusable
// without putting it in the tab order afterwards, and `role="alert"` means a
// screen reader announces it whether or not focus lands first.
//
// # It does not replace the inline errors
//
// Both are needed and they do different jobs: the summary says "this did not
// save, and here is why", the inline error says "this field, this problem".
// A summary alone leaves somebody hunting; inline alone leaves them unaware.

import { useEffect, useRef } from 'react';

import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

export function FormError({
  /** The server's own sentence. Shown as written. */
  message,
  /** Field-level messages, keyed by field name, as the API returns them. */
  fields,
  /** Where the remedy is, when it is not on this form. */
  action,
  className,
}: {
  message: string | null;
  fields?: Record<string, string> | null;
  action?: React.ReactNode;
  className?: string;
}) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);

  // Only on the transition into an error, not on every render while one is
  // shown -- otherwise typing a correction pulls focus back out of the field.
  const had = useRef(false);
  useEffect(() => {
    if (message && !had.current) ref.current?.focus();
    had.current = Boolean(message);
  }, [message]);

  if (!message) return null;

  const entries = Object.entries(fields ?? {});

  return (
    <div
      ref={ref}
      role="alert"
      tabIndex={-1}
      className={cn(
        'rounded-sm border border-critical/25 bg-critical-subtle p-3',
        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--ry-focus)]',
        className,
      )}
    >
      <p className="text-body font-medium text-critical-fg">{message}</p>

      {entries.length > 0 && (
        <ul className="mt-1.5 list-disc ps-4 text-caption text-critical-fg">
          {entries.map(([field, detail]) => (
            <li key={field}>{detail}</li>
          ))}
        </ul>
      )}

      {action && <div className="mt-2">{action}</div>}

      <p className="sr-only">{t('nx.err.notSaved')}</p>
    </div>
  );
}
