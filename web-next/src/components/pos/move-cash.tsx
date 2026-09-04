'use client';

// Taking money out of the drawer, or putting a float back in.
//
// # One amount, two directions
//
// The API takes a SIGNED amount: negative moves cash out to the safe, positive
// puts it back, and zero is refused. Asking a cashier to type a minus sign at a
// counter is asking for the wrong sign at the worst moment, so the direction is
// two buttons and the sign is applied here.
//
// # The reason is not optional in practice
//
// A drawer that empties without a reason is the thing an audit stops at. The
// server accepts a blank one; this screen still asks, because the person who
// can answer is standing at the till and the person who will ask is not.

import { ArrowDownToLine, ArrowUpFromLine, X } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { messageFor } from '@/lib/api/errors';
import { useCompany } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

export function MoveCash({
  onMove,
  onClose,
}: {
  onMove: (amount: string, reason: string, note: string) => Promise<void>;
  onClose: () => void;
}) {
  const t = useT();
  const { currency } = useCompany();
  const [outward, setOutward] = useState(true);
  const [amount, setAmount] = useState('');
  const [reason, setReason] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const magnitude = amount.trim();
    if (!magnitude) return;
    setBusy(true);
    setError(null);
    try {
      // The sign is applied here, from the direction the person chose. A minus
      // typed at a counter is a minus typed wrong.
      await onMove(outward ? `-${magnitude}` : magnitude, reason.trim(), note.trim());
      onClose();
    } catch (err) {
      setError(messageFor(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-[rgb(15_27_24/0.5)] p-4"
      onClick={onClose}
    >
      <form
        role="dialog"
        aria-modal="true"
        aria-label={t('nx.shift.cashDrop')}
        onClick={(e) => e.stopPropagation()}
        onSubmit={submit}
        className="scroll-trap w-full max-w-md overflow-y-auto rounded-lg bg-surface p-5 shadow-overlay"
      >
        <div className="mb-4 flex items-start justify-between gap-3">
          <div>
            <h2 className="text-card-title font-semibold text-fg">
              {t('nx.shift.cashDrop')}
            </h2>
            <p className="mt-0.5 text-label text-muted">
              {t('nx.shift.cashDropDesc')}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('nx.common.close')}
            className="grid size-9 shrink-0 place-items-center rounded-sm text-muted hover:bg-surface-hover"
          >
            <X className="size-4" aria-hidden="true" />
          </button>
        </div>

        <FormError message={error} className="mb-4" />

        {/* Direction, as two targets rather than a sign to type. */}
        <div
          role="radiogroup"
          aria-label={t('nx.shift.cashDrop')}
          className="mb-4 grid grid-cols-2 gap-2"
        >
          {[
            { out: true, label: t('nx.shift.dropOut'), Icon: ArrowUpFromLine },
            { out: false, label: t('nx.shift.dropIn'), Icon: ArrowDownToLine },
          ].map(({ out, label, Icon }) => (
            <button
              key={String(out)}
              type="button"
              role="radio"
              aria-checked={outward === out}
              onClick={() => setOutward(out)}
              className={cn(
                'flex min-h-11 items-center justify-center gap-2 rounded-sm border text-body',
                outward === out
                  ? 'border-primary bg-primary-subtle font-medium text-primary-subtle-fg'
                  : 'border-line-strong hover:bg-surface-hover',
              )}
            >
              <Icon className="size-4" aria-hidden="true" />
              {label}
            </button>
          ))}
        </div>

        <Field label={`${t('nx.shift.dropAmount')} (${currency})`} required>
          <Input
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="decimal"
            placeholder="0.00"
            autoFocus
            required
            numeric
          />
        </Field>

        <div className="mt-4">
          <Field label={t('nx.shift.dropReason')} hint={t('nx.shift.dropReasonHint')}>
            <Input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              autoComplete="off"
            />
          </Field>
        </div>

        <div className="mt-4">
          <Field label={t('nx.shift.closeNote')}>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
        </div>

        <Button
          type="submit"
          variant="primary"
          size="lg"
          block
          className="mt-5"
          busy={busy}
          disabled={amount.trim() === ''}
        >
          {t('nx.shift.recordDrop')}
        </Button>
      </form>
    </div>
  );
}
