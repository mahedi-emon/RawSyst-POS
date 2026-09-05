'use client';

// Writing an adjustment.
//
// # The difference, not the two totals
//
// The server refuses an unbalanced entry and says it as a difference:
// *"debits come to X and credits to Y, a difference of Z"* — because the number
// a person has to close is the difference. This screen shows the same figure
// while they type, so the refusal is prevented rather than explained. An entry
// that does not balance is the ordinary state of one half-written, and finding
// out on submit is finding out too late.
//
// # A line is a debit or a credit
//
// Two boxes and exactly one of them filled. The server refuses both — *"a
// negative debit is a credit"* — so the form says which line is wrong as soon
// as it is, rather than collecting four mistakes and reporting the first.
//
// # Headers are not offered
//
// A header account groups its children and holds nothing. Posting to one is how
// a chart of accounts silently stops adding up, so the picker carries only what
// can take an entry.

import { Plus, Trash2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import {
  balanceOf,
  blankLine,
  lineProblem,
  postableAccounts,
  postableLines,
  readiness,
  type Account,
  type DraftLine,
  type Journal,
} from '@/lib/accounting/journals';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

const LINE_PROBLEM: Record<string, Key> = {
  no_account: 'nx.jnl.lineNoAccount',
  no_amount: 'nx.jnl.lineNoAmount',
  both_sides: 'nx.jnl.lineBothSides',
  negative: 'nx.jnl.lineNegative',
};

const NOT_READY: Record<string, Key> = {
  no_reason: 'nx.jnl.needReason',
  too_few: 'nx.jnl.needTwo',
  line_problem: 'nx.jnl.needFix',
  unbalanced: 'nx.jnl.needBalance',
  nothing: 'nx.jnl.needAmounts',
};

function NewJournalScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [entryDate, setEntryDate] = useState(() =>
    new Date().toISOString().slice(0, 10),
  );
  const [reason, setReason] = useState('');
  const [memo, setMemo] = useState('');
  // Two, because that is the smallest entry there is.
  const [lines, setLines] = useState<DraftLine[]>(() => [blankLine(), blankLine()]);
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const chart = useApiList<Account>(
    scope ? '/accounting/chart' : null,
    scope ?? undefined,
  );
  const accounts = postableAccounts(chart.data?.data ?? []);
  const balance = balanceOf(lines);
  const state = readiness(lines, reason);
  const money = (v: string) => formatMoney(v, { currency, market });

  function edit(i: number, patch: Partial<DraftLine>) {
    setLines((current) => current.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  }

  async function post() {
    if (!scope || !state.ok) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<Journal>(
        `/accounting/journals?company_id=${scope.company_id}`,
        {
          // Minted here, so a retry after a lost response returns the original
          // entry rather than posting the adjustment twice.
          uuid: docUUID,
          entry_date: entryDate,
          reason: reason.trim(),
          memo: memo.trim(),
          lines: postableLines(lines),
        },
      );
      setDocUUID(crypto.randomUUID());
      router.push(`/money/journals/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.jnl.newTitle')}
        description={t('nx.jnl.newSubtitle')}
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem]">
        <div className="min-w-0">
          <Panel flush>
            <table className="w-full text-body">
              <caption className="sr-only">{t('nx.jnl.linesCaption')}</caption>
              <thead>
                <tr className="border-b border-line-strong bg-surface-sunken">
                  <th scope="col" className="px-3 py-2 text-start text-label text-muted">
                    {t('nx.jnl.colAccount')}
                  </th>
                  <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                    {t('nx.jnl.colDebit')}
                  </th>
                  <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                    {t('nx.jnl.colCredit')}
                  </th>
                  <th scope="col" className="w-12 px-3 py-2">
                    <span className="sr-only">{t('nx.jnl.removeLine')}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {lines.map((line, i) => {
                  const problem = lineProblem(line);
                  return (
                    <tr key={i} className="border-b border-line last:border-b-0">
                      <td className="px-3 py-2 align-top">
                        <select
                          value={line.accountID}
                          onChange={(e) => edit(i, { accountID: e.target.value })}
                          aria-label={`${t('nx.jnl.colAccount')} ${i + 1}`}
                          className={cn(
                            'select-chevron h-11 w-full appearance-none rounded-sm',
                            'border border-input bg-input-bg ps-3 pe-8 text-body',
                          )}
                        >
                          <option value="">{t('nx.jnl.chooseAccount')}</option>
                          {accounts.map((a) => (
                            <option key={a.id} value={a.id}>
                              {a.code} — {a.name}
                            </option>
                          ))}
                        </select>
                        <input
                          value={line.memo}
                          onChange={(e) => edit(i, { memo: e.target.value })}
                          placeholder={t('nx.jnl.colMemo')}
                          aria-label={`${t('nx.jnl.colMemo')} ${i + 1}`}
                          className={cn(
                            'mt-1.5 h-9 w-full rounded-sm border border-input',
                            'bg-input-bg px-3 text-caption',
                          )}
                        />
                        {problem !== 'none' ? (
                          <p role="alert" className="mt-1 text-caption text-critical-fg">
                            {t(LINE_PROBLEM[problem] as Key)}
                          </p>
                        ) : null}
                      </td>
                      <td className="px-3 py-2 align-top">
                        <input
                          value={line.debit}
                          onChange={(e) => edit(i, { debit: e.target.value })}
                          inputMode="decimal"
                          aria-label={`${t('nx.jnl.colDebit')} ${i + 1}`}
                          className={cn(
                            'num h-11 w-full rounded-sm border bg-input-bg px-3',
                            'text-end tabular-nums [direction:ltr]',
                            problem === 'both_sides' || problem === 'negative'
                              ? 'border-critical'
                              : 'border-input',
                          )}
                        />
                      </td>
                      <td className="px-3 py-2 align-top">
                        <input
                          value={line.credit}
                          onChange={(e) => edit(i, { credit: e.target.value })}
                          inputMode="decimal"
                          aria-label={`${t('nx.jnl.colCredit')} ${i + 1}`}
                          className={cn(
                            'num h-11 w-full rounded-sm border bg-input-bg px-3',
                            'text-end tabular-nums [direction:ltr]',
                            problem === 'both_sides' || problem === 'negative'
                              ? 'border-critical'
                              : 'border-input',
                          )}
                        />
                      </td>
                      <td className="px-3 py-2 align-top">
                        {lines.length > 2 ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={t('nx.jnl.removeLine')}
                            onClick={() =>
                              setLines((c) => c.filter((_, j) => j !== i))
                            }
                          >
                            <Trash2 aria-hidden="true" />
                          </Button>
                        ) : null}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
              <tfoot>
                <tr className="border-t-[3px] border-double border-line-strong">
                  <th scope="row" className="px-3 py-2.5 text-start font-medium">
                    {t('nx.jnl.debits')} / {t('nx.jnl.credits')}
                  </th>
                  <td className="num px-3 py-2.5 text-end font-semibold tabular-nums">
                    {money(balance.debits)}
                  </td>
                  <td className="num px-3 py-2.5 text-end font-semibold tabular-nums">
                    {money(balance.credits)}
                  </td>
                  <td />
                </tr>
              </tfoot>
            </table>

            <div className="border-t border-line px-3 py-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setLines((c) => [...c, blankLine()])}
              >
                <Plus aria-hidden="true" />
                {t('nx.jnl.addLine')}
              </Button>
            </div>
          </Panel>

          {/* The one figure that matters while typing. Announced, because
              somebody watching the totals is watching this. */}
          <p
            role="status"
            className={cn(
              'mt-4 flex items-baseline justify-between gap-4 rounded-sm border px-4 py-3',
              balance.balanced
                ? 'border-positive/25 bg-positive-subtle'
                : 'border-caution/25 bg-caution-subtle',
            )}
          >
            <span className="text-label font-medium">
              {balance.balanced ? t('nx.jnl.balanced') : t('nx.jnl.difference')}
            </span>
            {!balance.balanced ? (
              <span
                className={cn(
                  'num text-section font-semibold tabular-nums',
                  isZero(balance.difference) ? 'text-muted' : 'text-caution-fg',
                )}
              >
                {money(balance.difference)}
              </span>
            ) : null}
          </p>
        </div>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              <Field
                name="entry_date"
                label={t('nx.jnl.when')}
                hint={t('nx.jnl.whenHint')}
                error={fieldErrors.entry_date}
                required
              >
                <Input
                  type="date"
                  value={entryDate}
                  onChange={(e) => setEntryDate(e.target.value)}
                />
              </Field>

              <Field
                name="reason"
                label={t('nx.jnl.reason')}
                hint={t('nx.jnl.reasonHint')}
                error={fieldErrors.reason}
                required
              >
                <Textarea
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  rows={3}
                />
              </Field>

              <Field label={t('nx.jnl.memo')}>
                <Input value={memo} onChange={(e) => setMemo(e.target.value)} />
              </Field>
            </div>
          </Panel>

          <div>
            <Button
              variant="primary"
              busy={busy}
              busyLabel={t('nx.jnl.posting')}
              className="w-full"
              disabled={!state.ok}
              onClick={() => void post()}
            >
              {t('nx.jnl.post')}
            </Button>
            <p className="mt-2 text-caption text-muted">
              {state.ok ? t('nx.jnl.postHint') : t(NOT_READY[state.reason] as Key)}
            </p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewJournalPage() {
  return (
    <RequirePermission anyOf={['accounting.create']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewJournalScreen />
      </Suspense>
    </RequirePermission>
  );
}
