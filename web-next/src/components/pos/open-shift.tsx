'use client';

// Counting the drawer.
//
// The first thing that happens at a counter, and the reason the till refuses to
// sell before it: a session that starts from an assumed float has no baseline,
// so the variance at close means nothing. The server enforces this — a sale
// before it answers 409 — and this screen is what makes that a step rather than
// a refusal a cashier cannot act on.

import { useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { ErrorState } from '@/components/ui/states';

export function OpenShift({
  currency,
  busy,
  error,
  onOpen,
}: {
  currency: string;
  busy: boolean;
  error: unknown;
  onOpen: (openingFloat: string, blindClose: boolean) => void;
}) {
  const [float, setFloat] = useState('');
  const [blind, setBlind] = useState(false);

  function submit(e: FormEvent) {
    e.preventDefault();
    onOpen(float.trim() === '' ? '0' : float.trim(), blind);
  }

  return (
    <div className="grid flex-1 place-items-center p-4">
      <form onSubmit={submit} className="w-full max-w-sm">
        <h1 className="text-page font-semibold text-fg">Open the counter</h1>
        <p className="mt-1 mb-5 text-body text-muted">
          Count what is in the drawer and put the figure in. Everything you take
          today is measured against it.
        </p>

        {error != null && (
          <div className="mb-4">
            <ErrorState error={error} />
          </div>
        )}

        <Field
          label={`Opening float (${currency || 'cash in the drawer'})`}
          hint="Leave it at zero if the drawer starts empty."
          required
        >
          <Input
            value={float}
            onChange={(e) => setFloat(e.target.value)}
            inputMode="decimal"
            placeholder="0.00"
            autoFocus
            numeric
          />
        </Field>

        <div className="mt-4">
          <Checkbox
            checked={blind}
            onChange={(e) => setBlind(e.target.checked)}
            label="Blind close"
            // Per session, not per till: a trainee and a supervisor may run the
            // same counter on the same day and the shop may want it only for
            // one of them.
            hint="Hide the expected total at close, so the count is a real count."
          />
        </div>

        <Button
          type="submit"
          variant="primary"
          size="lg"
          block
          className="mt-6 h-12"
          busy={busy}
          busyLabel="Opening the counter"
        >
          Start selling
        </Button>
      </form>
    </div>
  );
}
