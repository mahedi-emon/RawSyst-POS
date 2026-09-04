'use client';

// Closing the counter.
//
// # The count is the point
//
// A shift close is not a form; it is a reconciliation. The screen exists to get
// an honest figure for what is in the drawer, and the variance that falls out
// of it is the thing an owner reads. So the counting grid is the body of the
// page and everything else is around it.
//
// # A blind close hides the expected figure, and that is not a UI preference
//
// When the session was opened blind, the server withholds the expected cash --
// and, deliberately, the three takings figures it could be derived from -- until
// the count is committed. A cashier who can see what the drawer should hold can
// make it agree, and the variance reads zero on every shift. The screen must not
// work around that, so where the figure is absent it says why rather than
// showing a blank.
//
// # Counting note by note is offered, not imposed
//
// Some shops count denominations, some sight-count a total. Both are supported;
// the grid is the default because it is the one that removes arithmetic from
// the end of a shift.

import { Coins, Keyboard } from 'lucide-react';
import { useMemo, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useCompany } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { denominationsFor, totalOf, varianceOf } from '@/lib/pos/denominations';
import type { Shift, ShiftReport } from '@/lib/pos/shift';
import { cn } from '@/lib/utils';

export function CloseShift({
  shift,
  report,
  onClosed,
  onCancel,
}: {
  shift: Shift;
  report: ShiftReport | null;
  onClosed: (final: ShiftReport) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { currency, market } = useCompany();

  const denominations = denominationsFor(currency);
  const [byDenomination, setByDenomination] = useState(denominations.length > 0);
  const [counts, setCounts] = useState<Record<string, string>>({});
  const [typedTotal, setTypedTotal] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const counted = useMemo(
    () => (byDenomination ? totalOf(counts, currency) : typedTotal.trim() || '0'),
    [byDenomination, counts, currency, typedTotal],
  );

  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, { currency, market, bare: true });

  // Absent means withheld, not zero -- a blind close, before the count is in.
  const expected = report?.expected_cash;
  const variance = expected ? varianceOf(counted, expected) : null;

  async function close() {
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      const final = await api.post<ShiftReport>(`/shifts/${shift.id}/close`, {
        counted_cash: counted,
        note: note.trim(),
      });
      onClosed(final);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-5 p-4">
      <div>
        <h1 className="text-page font-semibold text-fg">{t('nx.shift.closeTitle')}</h1>
        <p className="mt-1 text-body text-muted">{t('nx.shift.closeDesc')}</p>
      </div>

      <FormError message={error} fields={fieldErrors} />

      {/* What the till believes happened, before anybody counts anything. */}
      {report && (
        <Panel title={t('nx.shift.reading')} flush>
          <dl className="grid grid-cols-2 gap-px bg-line sm:grid-cols-3">
            <Reading label={t('nx.shift.openingFloat')} value={money(report.opening_float)} />
            <Reading label={t('nx.shift.invoices')} value={String(report.invoice_count)} />
            <Reading label={t('nx.shift.grossSales')} value={money(report.gross_sales)} />
            <Reading label={t('nx.shift.refunds')} value={money(report.refund_total)} />
            {report.cash_takings ? (
              <Reading label={t('nx.shift.cashTakings')} value={money(report.cash_takings)} />
            ) : null}
            {report.non_cash_takings ? (
              <Reading
                label={t('nx.shift.nonCashTakings')}
                value={money(report.non_cash_takings)}
              />
            ) : null}
            {report.cash_movements ? (
              <Reading
                label={t('nx.shift.cashMovements')}
                value={money(report.cash_movements)}
              />
            ) : null}
          </dl>
        </Panel>
      )}

      <Panel
        title={t('nx.shift.countedTotal')}
        actions={
          denominations.length > 0 ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setByDenomination((v) => !v)}
            >
              {byDenomination ? (
                <>
                  <Keyboard aria-hidden="true" />
                  {t('nx.shift.enterTotal')}
                </>
              ) : (
                <>
                  <Coins aria-hidden="true" />
                  {t('nx.shift.countByDenomination')}
                </>
              )}
            </Button>
          ) : null
        }
      >
        {byDenomination && denominations.length > 0 ? (
          <table className="w-full">
            <caption className="sr-only">{t('nx.shift.countByDenomination')}</caption>
            <thead>
              <tr>
                <th scope="col" className="pb-2 text-start text-label text-muted">
                  {t('nx.shift.denomination')}
                </th>
                <th scope="col" className="pb-2 text-end text-label text-muted">
                  {t('nx.shift.howMany')}
                </th>
                <th scope="col" className="pb-2 text-end text-label text-muted">
                  {t('nx.pos.total')}
                </th>
              </tr>
            </thead>
            <tbody>
              {denominations.map((d) => {
                const n = counts[d.value] ?? '';
                const line = totalOf({ [d.value]: n }, currency);
                return (
                  <tr key={d.value} className="border-t border-line">
                    <td className="py-1.5">
                      <span className="num font-medium">{d.label}</span>
                      <span className="ms-2 text-caption text-subtle">
                        {d.kind === 'note' ? '' : '·'}
                      </span>
                    </td>
                    <td className="py-1.5 text-end">
                      <input
                        value={n}
                        onChange={(e) =>
                          setCounts((c) => ({ ...c, [d.value]: e.target.value }))
                        }
                        inputMode="numeric"
                        // A count is a whole number of notes. `type=number`
                        // would let somebody enter 2.5 notes.
                        pattern="[0-9]*"
                        autoComplete="off"
                        aria-label={`${d.label} — ${t('nx.shift.howMany')}`}
                        className={cn(
                          'num h-10 w-20 rounded-sm border border-input bg-input-bg',
                          'text-center text-body tabular-nums [direction:ltr]',
                        )}
                      />
                    </td>
                    <td className="num py-1.5 text-end tabular-nums text-muted">
                      {isZero(line) ? '—' : money(line)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
            <tfoot>
              <tr className="rule-total">
                <td className="pt-2 text-body font-semibold" colSpan={2}>
                  {t('nx.shift.countedTotal')}
                </td>
                <td className="num pt-2 text-end text-body font-semibold tabular-nums">
                  {money(counted)}
                </td>
              </tr>
            </tfoot>
          </table>
        ) : (
          <Field label={`${t('nx.shift.countedTotal')} (${currency})`} required>
            <Input
              value={typedTotal}
              onChange={(e) => setTypedTotal(e.target.value)}
              inputMode="decimal"
              placeholder="0.00"
              autoFocus
              numeric
            />
          </Field>
        )}
      </Panel>

      {/* The figure the whole exercise exists to produce. */}
      <section className="grid gap-px overflow-hidden rounded-md border border-line bg-line sm:grid-cols-3">
        <Reading label={t('nx.shift.countedTotal')} value={money(counted)} strong />
        {expected ? (
          <Reading label={t('nx.shift.expectedInDrawer')} value={money(expected)} />
        ) : (
          <div className="bg-surface p-4">
            <p className="text-label text-muted">{t('nx.shift.expected')}</p>
            <p className="mt-1 text-caption text-subtle">
              {t('nx.shift.blindWithheld')}
            </p>
          </div>
        )}
        <div className="bg-surface p-4">
          <p className="text-label text-muted">{t('nx.shift.variance')}</p>
          {variance === null ? (
            <p className="num mt-1 text-display font-semibold text-disabled">—</p>
          ) : (
            <p
              className={cn(
                'num mt-1 text-display font-semibold tabular-nums',
                isZero(variance)
                  ? 'text-positive-fg'
                  : variance.startsWith('-')
                    ? 'text-critical-fg'
                    : 'text-caution-fg',
              )}
            >
              {money(variance.replace('-', ''))}
              <Badge
                tone={
                  isZero(variance)
                    ? 'positive'
                    : variance.startsWith('-')
                      ? 'critical'
                      : 'caution'
                }
                className="ms-2 align-middle"
              >
                {isZero(variance)
                  ? t('nx.shift.exact')
                  : variance.startsWith('-')
                    ? t('nx.shift.short')
                    : t('nx.shift.over')}
              </Badge>
            </p>
          )}
        </div>
      </section>

      <Field label={t('nx.shift.closeNote')} hint={t('nx.shift.closeNoteHint')}>
        <Textarea value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          size="lg"
          busy={busy}
          busyLabel={t('nx.shift.closing')}
          onClick={() => void close()}
        >
          {t('nx.shift.closeIt')}
        </Button>
        <Button variant="secondary" size="lg" onClick={onCancel} disabled={busy}>
          {t('nx.shift.backToTill')}
        </Button>
      </div>
    </div>
  );
}

function Reading({
  label,
  value,
  strong,
}: {
  label: string;
  value: string;
  strong?: boolean;
}) {
  return (
    <div className="bg-surface p-4">
      <dt className="text-label text-muted">{label}</dt>
      <dd
        className={cn(
          'num mt-1 tabular-nums',
          strong ? 'text-display font-semibold' : 'text-section font-medium',
        )}
      >
        {value}
      </dd>
    </div>
  );
}
