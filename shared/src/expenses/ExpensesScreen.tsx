// Where the money is going (blueprint C3).
//
// C3 opens with the sentence this screen exists for: "the Owner must be able to
// see, in one click, exactly where every riyal is going." So the screen leads
// with the breakdown by category, not with a list of receipts. A list answers
// "what did we pay for" one row at a time; the breakdown answers the question
// that was actually asked.
//
// # Two figures that look alike and are not
//
// VAT the shop will reclaim is not a cost. VAT on a category E2.3 restricts —
// entertainment, some vehicles, fuel — is, because the shop pays it and never
// gets it back. The second is shown on its own line whenever there is any,
// because an owner comparing this screen to their invoices otherwise finds a
// gap they cannot account for.
//
// # Recording is a panel, not a page
//
// An expense is four fields and takes ten seconds. Sending somebody to another
// screen and back for that is the friction that makes a shop stop recording the
// small ones, which is exactly the money C3 is about.

import { useCallback, useMemo, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import type { FieldErrors } from '../ui/Form';
import { money, longDate } from '../ui/format';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  listExpenses,
  listExpenseHeads,
  recordExpense,
  type Expense,
  type ExpenseHead,
  type ExpenseSummary,
} from '../api/expenses';
import { ExpenseHeadsPanel } from './ExpenseHeadsPanel';
import { borneBy, monthToDate, splitOf } from './expenses';

type Tab = 'spending' | 'heads';

export function ExpensesScreen({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayRecord = can('expense.record');
  const mayManageHeads = can('expense.manage_heads');

  const [tab, setTab] = useState<Tab>('spending');
  const [period, setPeriod] = useState(monthToDate());
  const [recording, setRecording] = useState(false);

  const load = useCallback(
    () => listExpenses(client, companyId, period),
    [client, companyId, period],
  );
  const { remote, reload } = useRemote(load);

  const loadHeads = useCallback(
    () => listExpenseHeads(client, companyId),
    [client, companyId],
  );
  const heads = useRemote(loadHeads);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('exp.title')}</h1>
          <p className="ds-caption">{t('exp.intro')}</p>
        </div>

        <div className="detail__actions">
          {mayRecord && tab === 'spending' && !recording && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setRecording(true)}
            >
              {t('exp.record')}
            </button>
          )}

          {mayManageHeads && (
            <div className="segmented" role="group" aria-label={t('common.whatToShow')}>
              {(
                [
                  ['spending', 'exp.spending'],
                  ['heads', 'exp.categories'],
                ] as const
              ).map(([key, label]) => (
                <button
                  key={key}
                  className={`segmented__btn${tab === key ? ' segmented__btn--on' : ''}`}
                  aria-pressed={tab === key}
                  onClick={() => setTab(key)}
                >
                  {t(label as Key)}
                </button>
              ))}
            </div>
          )}
        </div>
      </header>

      {tab === 'heads' ? (
        <ExpenseHeadsPanel companyId={companyId} onChanged={reload} />
      ) : (
        <>
          {recording && (
            <RecordPanel
              companyId={companyId}
              heads={heads.remote.state === 'ready' ? heads.remote.data : []}
              onCancel={() => setRecording(false)}
              onRecorded={() => {
                setRecording(false);
                reload();
                heads.reload();
              }}
            />
          )}

          <PeriodPicker period={period} onChange={setPeriod} />

          <RemoteBody remote={remote} onRetry={reload}>
            {(summary: ExpenseSummary) =>
              summary.count === 0 ? (
                <div className="ds-panel">
                  <div className="ds-panel__body">
                    <EmptyState
                      title={t('exp.noneTitle')}
                      body={t('exp.noneBody')}
                    />
                  </div>
                </div>
              ) : (
                <Spending summary={summary} />
              )
            }
          </RemoteBody>
        </>
      )}
    </main>
  );
}

function PeriodPicker({
  period,
  onChange,
}: {
  period: { from: string; to: string };
  onChange: (p: { from: string; to: string }) => void;
}) {
  const t = useT();
  return (
    <div className="ds-panel exp__period">
      <div className="ds-panel__body exp__periodrow">
        <Field label={t('common.from')} htmlFor="exp-from" required>
          <input
            id="exp-from"
            type="date"
            className="field__input"
            value={period.from}
            max={period.to}
            onChange={(e) => onChange({ ...period, from: e.target.value })}
          />
        </Field>
        <Field label={t('common.to')} htmlFor="exp-to" required>
          <input
            id="exp-to"
            type="date"
            className="field__input"
            value={period.to}
            min={period.from}
            onChange={(e) => onChange({ ...period, to: e.target.value })}
          />
        </Field>
      </div>
    </div>
  );
}

/** The answer to the question C3 asks, above the receipts that make it up. */
function Spending({ summary }: { summary: ExpenseSummary }) {
  const t = useT();
  const { locale } = useLocale();
  const split = splitOf(summary);
  const borne = borneBy(summary);
  // Every expense in a period is in the company's own currency, so the first
  // one names it for the panel. Absent only when the period is empty, and this
  // panel is not rendered then.
  const currency = summary.expenses[0]?.currency;

  return (
    <>
      <section className="ds-panel" aria-label={t('exp.byCategory')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('exp.byCategory')}</h2>
          {/* What the categories came to, which is what the shop BORE — not
              the gross that left the bank.
              
              They differ by the VAT that will be reclaimed, and that is not a
              cost: it belongs to no category and comes back. A heading showing
              the gross over a list of categories that sums to something else
              is a panel that does not add up, and an owner checking it by hand
              is the person most likely to notice. */}
          <span className="num exp__total">
            {money(borne, { currency })}
          </span>
        </div>

        <div className="ds-panel__body">
          <ol className="exp__bars">
            {summary.by_head.map((h) => (
              <li key={h.head_id} className="exp__bar">
                <span className="exp__barname">{h.head}</span>
                {/* The bar is decoration over a figure that is already there,
                    not the only way to read the number: aria-hidden, and the
                    amount and share are both text beside it. */}
                <span className="exp__bartrack" aria-hidden="true">
                  <span className="exp__barfill" style={{ inlineSize: `${h.share}%` }} />
                </span>
                <span className="num exp__baramount">{money(h.amount)}</span>
                <span className="ds-caption exp__barshare">{h.share}%</span>
              </li>
            ))}
          </ol>

          {/* The two notes together are what makes this panel and the receipts
              below it reconcile: categories + reclaimable VAT = what left the
              bank. Without them the two totals differ and nothing on the screen
              says why. */}
          {split.recoverable && (
            <p className="exp__reclaim">
              {t('exp.reclaimNote')
                .replace('{amount}', money(summary.tax_recoverable, { currency }))
                .replace('{total}', money(summary.total, { currency }))}
            </p>
          )}
          {split.absorbed && (
            <p className="exp__absorbed">
              {t('exp.absorbedNote').replace(
                '{amount}',
                money(summary.tax_absorbed, { currency }),
              )}
            </p>
          )}
        </div>
      </section>

      <section className="ds-panel" aria-label={t('exp.receipts')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('exp.receipts')}</h2>
          <span className="ds-caption">
            {t('exp.count').replace('{n}', String(summary.count))}
          </span>
        </div>
        <div className="ds-panel__body ds-scroll-x">
          <table className="ds-table">
            <thead>
              <tr>
                <th scope="col">{t('common.date')}</th>
                <th scope="col">{t('exp.number')}</th>
                <th scope="col">{t('common.description')}</th>
                <th scope="col">{t('exp.paidFrom')}</th>
                <th scope="col" className="num">{t('common.total')}</th>
              </tr>
            </thead>
            <tbody>
              {summary.expenses.map((x: Expense) => (
                <tr key={x.id}>
                  <td className="ds-date">{longDate(x.expense_date, locale)}</td>
                  <td>
                    <span className="detail__strong">{x.expense_no}</span>
                    {x.reference && <span className="ds-caption">{x.reference}</span>}
                  </td>
                  <td>
                    {x.description || <span className="ds-subtle">—</span>}
                    {x.supplier && <span className="ds-caption">{x.supplier}</span>}
                  </td>
                  <td>{t(x.paid_from === 'cash' ? 'exp.cash' : 'exp.bank')}</td>
                  <td className="num">{money(x.total_inclusive, { currency: x.currency })}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

/** Recording one. Four fields, in the order somebody reads a receipt. */
function RecordPanel({
  companyId,
  heads,
  onCancel,
  onRecorded,
}: {
  companyId: string;
  heads: ExpenseHead[];
  onCancel: () => void;
  onRecorded: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [headId, setHeadId] = useState(heads[0]?.id ?? '');
  const [net, setNet] = useState('');
  const [when, setWhen] = useState(() => new Date().toISOString().slice(0, 10));
  const [paidFrom, setPaidFrom] = useState<'cash' | 'bank'>('cash');
  const [description, setDescription] = useState('');
  const [reference, setReference] = useState('');
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const [fields, setFields] = useState<FieldErrors>({});

  const head = useMemo(() => heads.find((h) => h.id === headId), [heads, headId]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setFailed(null);
    setFields({});
    try {
      await recordExpense(client, companyId, {
        // Assigned before the request, so a retry after a lost response gives
        // back the expense already recorded rather than paying the electricity
        // bill into the books twice.
        uuid: crypto.randomUUID(),
        expense_date: when,
        paid_from: paidFrom,
        reference: reference.trim() || undefined,
        description: description.trim() || undefined,
        lines: [{ head_id: headId, net_amount: net.trim() }],
      });
      onRecorded();
    } catch (err) {
      const e = err as { message?: string; fields?: FieldErrors };
      setFailed(e.message ?? t('exp.recordFailed'));
      if (e.fields) setFields(e.fields);
    } finally {
      setBusy(false);
    }
  }

  if (heads.length === 0) {
    return (
      <div className="ds-panel">
        <div className="ds-panel__body">
          <EmptyState title={t('exp.noHeadsTitle')} body={t('exp.noHeadsBody')} />
        </div>
      </div>
    );
  }

  return (
    <section className="ds-panel" aria-label={t('exp.record')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('exp.record')}</h2>
      </div>
      <form className="ds-panel__body exp__form" onSubmit={submit}>
        <FormError message={failed} />

        <div className="exp__formrow">
          <Field label={t('exp.category')} htmlFor="exp-head" required
            error={fields.head_id}>
            <select
              id="exp-head"
              className="field__input"
              value={headId}
              onChange={(e) => setHeadId(e.target.value)}
            >
              {heads.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.name}
                </option>
              ))}
            </select>
          </Field>

          <Field
            label={t('exp.netAmount')}
            htmlFor="exp-net"
            required
            error={fields.net_amount}
            /* Said before the number is typed rather than after it is wrong:
               the tax is added by the server, so a shopkeeper reading a
               receipt that says 115 has to know to type 100. */
            hint={t('exp.netHint')}
          >
            <TextInput
              id="exp-net"
              value={net}
              onChange={setNet}
              inputMode="decimal"
              error={fields.net_amount}
            />
          </Field>
        </div>

        {head && !head.input_vat_recoverable && (
          <p className="exp__warn">{t('exp.restrictedNote')}</p>
        )}

        <div className="exp__formrow">
          <Field label={t('common.date')} htmlFor="exp-when" required
            error={fields.expense_date}>
            <input
              id="exp-when"
              type="date"
              className="field__input"
              value={when}
              onChange={(e) => setWhen(e.target.value)}
            />
          </Field>

          <Field label={t('exp.paidFrom')} htmlFor="exp-paid" required
            error={fields.paid_from}>
            <select
              id="exp-paid"
              className="field__input"
              value={paidFrom}
              onChange={(e) => setPaidFrom(e.target.value as 'cash' | 'bank')}
            >
              <option value="cash">{t('exp.cash')}</option>
              <option value="bank">{t('exp.bank')}</option>
            </select>
          </Field>
        </div>

        <div className="exp__formrow">
          <Field label={t('common.description')} htmlFor="exp-desc">
            <TextInput id="exp-desc" value={description} onChange={setDescription} />
          </Field>
          <Field label={t('exp.reference')} htmlFor="exp-ref">
            <TextInput id="exp-ref" value={reference} onChange={setReference} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('exp.record')}
          busy={busy}
          disabled={!headId || !net.trim()}
          onCancel={onCancel}
        />
      </form>
    </section>
  );
}
