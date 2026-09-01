// What a client's plan includes, what they are using of it, and what they have
// been billed (blueprint H5).
//
// # A limit is shown against what is used
//
// "5 users" is a number nobody acts on. "4 of 5 users" is the one that tells an
// owner why the next invitation will be refused, which is the question this
// screen exists to answer before somebody raises a support ticket.
//
// # A module granted outside the plan says so
//
// H5's commercial flexibility is a Starter client who was given Warranty
// Management. The row shows it as included AND as an exception, with the reason
// the platform recorded — because an owner who cannot see why they have a
// module cannot tell whether it is about to be taken away.

import { useCallback } from 'react';

import {
  getEntitlements,
  getSubscription,
  listSubscriptionInvoices,
  type Entitlement,
  type Subscription,
  type SubscriptionInvoice,
} from '../api/billing';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { money, shortDate } from '../ui/format';

export function SubscriptionPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => getSubscription(client), [client]);
  const { remote, reload } = useRemote(load);

  const entLoad = useCallback(() => getEntitlements(client), [client]);
  const ent = useRemote(entLoad);

  const invLoad = useCallback(
    () => listSubscriptionInvoices(client),
    [client],
  );
  const inv = useRemote(invLoad);

  return (
    <>
      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { subscription: Subscription }) => {
          const s = payload.subscription;
          const l = s.limits;
          return (
            <section className="ds-panel" aria-label={t('sub.title')}>
              <div className="ds-panel__head">
                <div>
                  <h2 className="ds-h3">{t(`sub.tier.${s.tier}` as Key)}</h2>
                  <p className="ds-caption">
                    {t(`sub.cycle.${s.cycle}` as Key)} ·{' '}
                    {money(s.price, { currency: s.currency })}
                  </p>
                </div>
                <span
                  className={`ds-badge ds-badge--${
                    s.status === 'active' || s.status === 'trialing'
                      ? 'success'
                      : s.status === 'past_due'
                        ? 'warning'
                        : 'danger'
                  }`}
                >
                  {t(`sub.status.${s.status}` as Key)}
                </span>
              </div>

              <div className="ds-panel__body">
                {s.days_overdue !== undefined && (
                  <p className="sub__warning" role="status">
                    {t('sub.overdueBy', {
                      n: String(s.days_overdue),
                      amount: money(s.outstanding, { currency: s.currency }),
                    })}
                  </p>
                )}

                <dl className="sub__facts">
                  <Fact label={t('sub.since')} value={shortDate(s.started_on, locale)} />
                  {s.current_period_end && (
                    <Fact
                      label={t('sub.renews')}
                      value={shortDate(s.current_period_end, locale)}
                    />
                  )}
                  {s.trial_ends_on && (
                    <Fact
                      label={t('sub.trialEnds')}
                      value={shortDate(s.trial_ends_on, locale)}
                    />
                  )}
                  <Fact
                    label={t('sub.outstanding')}
                    value={money(s.outstanding, { currency: s.currency })}
                  />
                </dl>

                <h3 className="ds-h3">{t('sub.limits')}</h3>
                <div className="sub__limits">
                  <Meter
                    label={t('sub.companies')}
                    used={l.companies}
                    max={l.max_companies}
                  />
                  <Meter
                    label={t('sub.stores')}
                    used={l.stores}
                    max={l.max_stores}
                  />
                  <Meter
                    label={t('sub.users')}
                    used={l.users}
                    max={l.max_users}
                  />
                  <Meter
                    label={t('sub.terminals')}
                    used={l.terminals}
                    max={l.max_terminals}
                  />
                </div>
              </div>
            </section>
          );
        }}
      </RemoteBody>

      <section className="ds-panel" aria-label={t('sub.modules')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('sub.modules')}</h2>
            <p className="ds-caption">{t('sub.modulesHint')}</p>
          </div>
        </div>
        <RemoteBody remote={ent.remote} onRetry={ent.reload}>
          {(payload: { data: Entitlement[] }) => (
            <div className="ds-panel__body">
              <ul className="sub__features">
                {payload.data.map((e) => (
                  <li
                    key={e.feature}
                    className={`sub__feature${e.allowed ? '' : ' sub__feature--off'}`}
                  >
                    <span className="sub__featureName">
                      {t(`sub.feature.${e.feature}` as Key)}
                    </span>
                    {e.allowed && !e.in_plan && (
                      <span className="ds-badge ds-badge--success">
                        {t('sub.grantedToYou')}
                      </span>
                    )}
                    {!e.allowed && e.in_plan && (
                      <span className="ds-badge ds-badge--warning">
                        {t('sub.turnedOff')}
                      </span>
                    )}
                    {e.expires_on && (
                      <span className="ds-caption">
                        {t('sub.until', {
                          when: shortDate(e.expires_on, locale),
                        })}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </RemoteBody>
      </section>

      <section className="ds-panel" aria-label={t('sub.invoices')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('sub.invoices')}</h2>
            <p className="ds-caption">{t('sub.invoicesHint')}</p>
          </div>
        </div>
        <RemoteBody remote={inv.remote} onRetry={inv.reload}>
          {(payload: { data: SubscriptionInvoice[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('sub.noInvoicesTitle')}
                  body={t('sub.noInvoicesBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('sub.invoiceNo')}</th>
                      <th scope="col">{t('sub.period')}</th>
                      <th scope="col">{t('sub.amount')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('sub.due')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((i) => (
                      <tr key={i.id}>
                        <td>{i.invoice_no}</td>
                        <td>
                          {shortDate(i.period_start, locale)} –{' '}
                          {shortDate(i.period_end, locale)}
                        </td>
                        <td className="num">
                          {money(i.amount, { currency: i.currency })}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${
                              i.status === 'paid'
                                ? 'success'
                                : i.overdue
                                  ? 'danger'
                                  : 'neutral'
                            }`}
                          >
                            {t(`sub.inv.${i.status}` as Key)}
                          </span>
                        </td>
                        <td>{shortDate(i.due_on, locale)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="sub__fact">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

/** Used against the ceiling. See the file note on why both numbers are shown. */
function Meter({
  label,
  used,
  max,
}: {
  label: string;
  used: number;
  max: number;
}) {
  const share = max > 0 ? Math.min(used / max, 1) : 0;
  return (
    <div className="sub__meter">
      <div className="sub__meterHead">
        <span>{label}</span>
        <span className="num">
          {used} / {max}
        </span>
      </div>
      <div
        className="sub__meterTrack"
        role="meter"
        aria-valuenow={used}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-label={label}
      >
        <div
          className={`sub__meterFill${share >= 1 ? ' sub__meterFill--full' : ''}`}
          style={{ inlineSize: `${Math.round(share * 100)}%` }}
        />
      </div>
    </div>
  );
}
