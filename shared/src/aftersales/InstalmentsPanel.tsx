// Instalment plans (blueprint B14).
//
// # Quoting is free and opening is not
//
// A customer at the counter asking what twelve months would cost gets an
// answer without anybody being committed to anything. The quote is its own
// action, behind `installment.view`, because a cashier has to be able to say
// the number out loud without creating an agreement.
//
// # The last instalment takes the remainder
//
// 1,000 over three is not 333.33 three times — that is 999.99, and the hallala
// that goes missing is a debt the customer never clears and a receivable that
// never closes. The quote shows the final payment separately so nobody is
// surprised by it.

import { useCallback, useState } from 'react';

import {
  cancelPlan,
  listPlans,
  quoteInstallments,
  readPlan,
  type InstallmentPlan,
  type QuotedPlan,
} from '../api/aftersales';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

export function InstalmentsPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const [open, setOpen] = useState<string | null>(null);
  const [showSettled, setShowSettled] = useState(false);

  const load = useCallback(
    () => listPlans(client, companyId, {}),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <PlanDetail
        companyId={companyId}
        planId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      <QuoteCard companyId={companyId} />

      <section className="ds-panel" aria-label={t('after.instalments')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('after.plans')}</h2>
          <button
            className="ds-btn ds-btn--quiet"
            onClick={() => setShowSettled(!showSettled)}
          >
            {t(showSettled ? 'after.showRunningPlans' : 'after.showAllPlans')}
          </button>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: InstallmentPlan[] }) => {
            const rows = showSettled
              ? payload.data
              : payload.data.filter((p) => p.status === 'active');

            return rows.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('after.noPlansTitle')}
                  body={t('after.noPlansBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('after.plan')}</th>
                      <th scope="col">{t('crm.customer')}</th>
                      <th scope="col" className="num">
                        {t('after.financed')}
                      </th>
                      <th scope="col" className="num">
                        {t('after.monthly')}
                      </th>
                      <th scope="col" className="num">
                        {t('after.outstanding')}
                      </th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((p) => (
                      <tr key={p.id}>
                        <td>
                          <span className="detail__strong">{p.plan_no}</span>
                          <span className="ds-caption">
                            {t('after.overMonths', {
                              count: String(p.tenure_months),
                            })}
                          </span>
                        </td>
                        <td>
                          {p.customer ?? '—'}
                          {p.guarantor_name && (
                            <span className="ds-caption">
                              {t('after.guarantor', { name: p.guarantor_name })}
                            </span>
                          )}
                        </td>
                        <td className="num">
                          {money(p.financed, { currency: p.currency })}
                        </td>
                        <td className="num">
                          {money(p.installment_amount, { currency: p.currency })}
                        </td>
                        <td className="num">
                          {money(p.outstanding, { currency: p.currency })}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${planBadge(p.status)}`}
                          >
                            {t(`after.plan.${p.status}` as Key)}
                          </span>
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(p.id)}
                          >
                            {t('action.view')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          }}
        </RemoteBody>
      </section>
    </>
  );
}

function planBadge(status: string): string {
  switch (status) {
    case 'settled':
      return 'success';
    case 'defaulted':
      return 'danger';
    case 'cancelled':
      return 'neutral';
    default:
      return 'info';
  }
}

// QuoteCard answers the counter's question without committing anybody.
function QuoteCard({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const [principal, setPrincipal] = useState('');
  const [down, setDown] = useState('0');
  const [rate, setRate] = useState('0');
  const [months, setMonths] = useState('12');
  const [quote, setQuote] = useState<QuotedPlan | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function ask(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      setQuote(
        await quoteInstallments(client, companyId, {
          principal,
          down_payment: down,
          markup_rate: rate,
          tenure_months: Number(months) || 0,
        }),
      );
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel after__quote" onSubmit={(e) => void ask(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('after.whatWouldItCost')}</h2>
        <p className="ds-caption">{t('after.quoteHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('after.price')} htmlFor="q-principal" required>
            <TextInput
              id="q-principal"
              value={principal}
              onChange={setPrincipal}
              inputMode="decimal"
            />
          </Field>
          <Field label={t('after.downPayment')} htmlFor="q-down">
            <TextInput
              id="q-down"
              value={down}
              onChange={setDown}
              inputMode="decimal"
            />
          </Field>
          <Field
            label={t('after.markupRate')}
            hint={t('after.markupRateHint')}
            htmlFor="q-rate"
          >
            <TextInput
              id="q-rate"
              value={rate}
              onChange={setRate}
              inputMode="decimal"
            />
          </Field>
          <Field label={t('after.months')} htmlFor="q-months" required>
            <TextInput
              id="q-months"
              value={months}
              onChange={setMonths}
              inputMode="numeric"
            />
          </Field>
        </div>

        <button
          className="ds-btn ds-btn--primary"
          type="submit"
          disabled={busy || principal.trim() === ''}
        >
          {t('after.quote')}
        </button>

        {quote && (
          <dl className="after__quoteout" role="status">
            <div>
              <dt>{t('after.monthly')}</dt>
              <dd className="num">{money(quote.installment_amount)}</dd>
            </div>
            <div>
              <dt>{t('after.finalPayment')}</dt>
              {/* Shown separately, because the last one takes the remainder
                  and a customer told "300 a month" who is billed 300.02 at the
                  end has been surprised by their own agreement. */}
              <dd className="num">{money(quote.final_payment)}</dd>
            </div>
            <div>
              <dt>{t('after.markup')}</dt>
              <dd className="num">{money(quote.markup_amount)}</dd>
            </div>
            <div className="after__quotetotal">
              <dt>{t('after.totalPayable')}</dt>
              <dd className="num">{money(quote.total_payable)}</dd>
            </div>
          </dl>
        )}
      </div>
    </form>
  );
}

function PlanDetail({
  companyId,
  planId,
  onBack,
}: {
  companyId: string;
  planId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('installment.manage');
  const load = useCallback(
    () => readPlan(client, companyId, planId),
    [client, companyId, planId],
  );
  const { remote, reload } = useRemote(load);

  const [cancelling, setCancelling] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function abandon(p: InstallmentPlan) {
    setBusy(true);
    setFailure(null);
    try {
      await cancelPlan(client, companyId, p.id, reason);
      setCancelling(false);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(p: InstallmentPlan) => (
        <>
          <section className="ds-panel">
            <div className="ds-panel__head">
              <div>
                <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                  {t('action.back')}
                </button>
                <h2 className="ds-h3">{p.plan_no}</h2>
                <p className="ds-caption">
                  {p.customer ?? ''} ·{' '}
                  {t(`after.plan.${p.status}` as Key)}
                </p>
              </div>
              {mayManage && p.status === 'active' && !cancelling && (
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setCancelling(true)}
                >
                  {t('after.cancelPlan')}
                </button>
              )}
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />

              <dl className="after__facts">
                <div>
                  <dt>{t('after.price')}</dt>
                  <dd className="num">
                    {money(p.principal, { currency: p.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.downPayment')}</dt>
                  <dd className="num">
                    {money(p.down_payment, { currency: p.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.markup')}</dt>
                  <dd className="num">
                    {money(p.markup_amount, { currency: p.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.monthly')}</dt>
                  <dd className="num">
                    {money(p.installment_amount, { currency: p.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.outstanding')}</dt>
                  <dd className="num">
                    {money(p.outstanding, { currency: p.currency })}
                  </dd>
                </div>
                {p.guarantor_name && (
                  <div>
                    <dt>{t('after.guarantorName')}</dt>
                    <dd>
                      {p.guarantor_name}
                      {p.guarantor_phone ? ` · ${p.guarantor_phone}` : ''}
                    </dd>
                  </div>
                )}
              </dl>

              {cancelling && (
                <div className="after__advance">
                  <TextInput
                    id="pl-reason"
                    value={reason}
                    onChange={setReason}
                    placeholder={t('after.whyCancelling')}
                  />
                  <div className="form__actions">
                    <button
                      className="ds-btn ds-btn--primary"
                      disabled={busy || reason.trim() === ''}
                      onClick={() => void abandon(p)}
                    >
                      {t('after.cancelPlan')}
                    </button>
                    <button
                      className="ds-btn ds-btn--quiet"
                      onClick={() => setCancelling(false)}
                    >
                      {t('action.cancel')}
                    </button>
                  </div>
                </div>
              )}
            </div>
          </section>

          <section className="ds-panel" aria-label={t('after.schedule')}>
            <div className="ds-panel__head">
              <h3 className="ds-h3">{t('after.schedule')}</h3>
            </div>
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">#</th>
                    <th scope="col">{t('after.dueOn')}</th>
                    <th scope="col" className="num">
                      {t('common.amount')}
                    </th>
                    <th scope="col" className="num">
                      {t('after.paid')}
                    </th>
                    <th scope="col" className="num">
                      {t('after.lateFee')}
                    </th>
                    <th scope="col">{t('common.status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {(p.schedule ?? []).map((d) => (
                    <tr
                      key={d.id}
                      className={d.state === 'overdue' ? 'after__row--late' : undefined}
                    >
                      <td>{d.seq}</td>
                      <td>{shortDate(d.due_on, locale)}</td>
                      <td className="num">
                        {money(d.amount, { currency: p.currency })}
                      </td>
                      <td className="num">
                        {money(d.paid, { currency: p.currency })}
                      </td>
                      <td className="num">
                        {d.late_fee === '0.00' || d.late_fee === '0'
                          ? '—'
                          : money(d.late_fee, { currency: p.currency })}
                      </td>
                      <td>
                        <span className={`ds-badge ds-badge--${dueBadge(d.state)}`}>
                          {t(`after.due.${d.state}` as Key)}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </RemoteBody>
  );
}

function dueBadge(state: string): string {
  switch (state) {
    case 'paid':
      return 'success';
    case 'overdue':
      return 'danger';
    case 'partial':
      return 'warning';
    case 'waived':
      return 'neutral';
    default:
      return 'info';
  }
}
