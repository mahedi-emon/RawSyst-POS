// Requisitions and requests for quotation (blueprint B5, B5.1).
//
// # Nothing here picks the winner
//
// The comparison marks the cheapest quote that could be accepted today, and
// that is a convenience for the eye. B5.1 wants a person to choose and to say
// why, because lead time and payment terms routinely outweigh price — so the
// award form asks for a reason and refuses without one.
//
// # A requisition is a request, not an order
//
// Somebody in a branch says what they need; somebody with the budget decides.
// The two live on the same tab because the second person's whole job here is
// reading the first person's list.

import { useCallback, useState } from 'react';

import {
  awardQuote,
  compareQuotes,
  decideRequisition,
  listRFQs,
  listRequisitions,
  type Comparison,
  type Quote,
  type RFQ,
  type Requisition,
} from '../api/sourcing';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

export function SourcingPanel({ companyId }: { companyId: string }) {
  const [open, setOpen] = useState<string | null>(null);

  if (open) {
    return (
      <ComparisonScreen
        companyId={companyId}
        rfqId={open}
        onBack={() => setOpen(null)}
      />
    );
  }

  return (
    <>
      <RequisitionsPanel companyId={companyId} />
      <RFQsPanel companyId={companyId} onOpen={setOpen} />
    </>
  );
}

function RequisitionsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayDecide = can('purchasing.approve_request');
  const [showAll, setShowAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(
    () => listRequisitions(client, companyId, showAll ? {} : { status: 'submitted' }),
    [client, companyId, showAll],
  );
  const { remote, reload } = useRemote(load);

  async function decide(r: Requisition, approve: boolean) {
    setBusy(true);
    setFailure(null);
    try {
      await decideRequisition(client, companyId, r.id, approve);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('src.requisitions')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('src.requisitions')}</h2>
          <p className="ds-caption">{t('src.requisitionsHint')}</p>
        </div>
        <button
          className="ds-btn ds-btn--quiet"
          onClick={() => setShowAll(!showAll)}
        >
          {t(showAll ? 'src.showWaiting' : 'src.showAllRequisitions')}
        </button>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { requisitions: Requisition[] }) =>
          payload.requisitions.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('src.noRequisitionsTitle')}
                body={t('src.noRequisitionsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('src.request')}</th>
                    <th scope="col">{t('src.whatFor')}</th>
                    <th scope="col">{t('src.neededBy')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.requisitions.map((r) => (
                    <tr key={r.id}>
                      <td>
                        <span className="detail__strong">{r.requisition_no}</span>
                        <span className="ds-caption">
                          {r.requested_by ?? ''} ·{' '}
                          {shortDate(r.requested_at, locale)}
                        </span>
                      </td>
                      <td>
                        {r.store_name ?? '—'}
                        {r.justification && (
                          <span className="ds-caption">{r.justification}</span>
                        )}
                        <span className="ds-caption">
                          {t('src.lineCount', {
                            count: String((r.lines ?? []).length),
                          })}
                        </span>
                      </td>
                      <td>
                        {r.needed_by ? shortDate(r.needed_by, locale) : '—'}
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${reqBadge(r.status)}`}
                        >
                          {t(`src.req.${r.status}` as Key)}
                        </span>
                        {r.decision_note && (
                          <span className="ds-caption">{r.decision_note}</span>
                        )}
                      </td>
                      <td>
                        {mayDecide && r.status === 'submitted' && (
                          <div className="src__rowactions">
                            <button
                              className="ds-btn ds-btn--primary"
                              disabled={busy}
                              onClick={() => void decide(r, true)}
                            >
                              {t('action.approve')}
                            </button>
                            <button
                              className="ds-btn ds-btn--quiet"
                              disabled={busy}
                              onClick={() => void decide(r, false)}
                            >
                              {t('action.decline')}
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </RemoteBody>
    </section>
  );
}

function reqBadge(status: string): string {
  switch (status) {
    case 'approved':
    case 'ordered':
      return 'success';
    case 'rejected':
    case 'cancelled':
      return 'danger';
    case 'submitted':
      return 'warning';
    default:
      return 'neutral';
  }
}

function RFQsPanel({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => listRFQs(client, companyId, {}),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('src.rfqs')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('src.rfqs')}</h2>
          <p className="ds-caption">{t('src.rfqsHint')}</p>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { rfqs: RFQ[] }) =>
          payload.rfqs.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('src.noRfqsTitle')}
                body={t('src.noRfqsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('src.rfq')}</th>
                    <th scope="col">{t('src.invited')}</th>
                    <th scope="col">{t('src.quotesIn')}</th>
                    <th scope="col">{t('src.closes')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.rfqs.map((r) => {
                    const invited = r.invited ?? [];
                    const quoted = invited.filter((i) => i.quoted).length;
                    return (
                      <tr key={r.id}>
                        <td>
                          <span className="detail__strong">{r.rfq_number}</span>
                          <span className="ds-caption">
                            {t('src.lineCount', {
                              count: String((r.lines ?? []).length),
                            })}
                          </span>
                        </td>
                        <td>{invited.length}</td>
                        <td>
                          {/* Three suppliers invited and one quote back is a
                              different situation from three back, and it is the
                              one that decides whether to wait. */}
                          {t('src.ofInvited', {
                            quoted: String(quoted),
                            invited: String(invited.length),
                          })}
                        </td>
                        <td>
                          {r.closes_on ? shortDate(r.closes_on, locale) : '—'}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${rfqBadge(r.status)}`}
                          >
                            {t(`src.rfqStatus.${r.status}` as Key)}
                          </span>
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => onOpen(r.id)}
                          >
                            {t('src.compare')}
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
        }
      </RemoteBody>
    </section>
  );
}

function rfqBadge(status: string): string {
  switch (status) {
    case 'awarded':
      return 'success';
    case 'cancelled':
      return 'danger';
    case 'comparing':
      return 'warning';
    default:
      return 'info';
  }
}

// ComparisonScreen is B5.1's side-by-side.
//
// Price, lead time and payment terms in one row each, because those are the
// three things that decide it and a buyer comparing them in two windows is a
// buyer who compares only price.
function ComparisonScreen({
  companyId,
  rfqId,
  onBack,
}: {
  companyId: string;
  rfqId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayAward = can('purchasing.award_rfq');
  const load = useCallback(
    () => compareQuotes(client, companyId, rfqId),
    [client, companyId, rfqId],
  );
  const { remote, reload } = useRemote(load);

  const [awarding, setAwarding] = useState<Quote | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [awarded, setAwarded] = useState<string | null>(null);

  async function award(quote: Quote) {
    setBusy(true);
    setFailure(null);
    try {
      const out = await awardQuote(client, companyId, rfqId, quote.id, reason);
      setAwarded(out.order.po_number);
      setAwarding(null);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(c: Comparison) => (
        <section className="ds-panel">
          <div className="ds-panel__head">
            <div>
              <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                {t('action.back')}
              </button>
              <h2 className="ds-h3">{c.rfq.rfq_number}</h2>
              <p className="ds-caption">
                {t(`src.rfqStatus.${c.rfq.status}` as Key)}
                {c.rfq.closes_on
                  ? ` · ${t('src.closesOn', {
                      date: shortDate(c.rfq.closes_on, locale),
                    })}`
                  : ''}
              </p>
            </div>
          </div>

          <div className="ds-panel__body">
            <FormError message={failure} />
            {awarded && (
              <p className="ds-caption" role="status">
                {t('src.awardedOrder', { number: awarded })}
              </p>
            )}
            {c.rfq.award_reason && (
              <p className="src__awardreason">
                {t('src.awardedBecause', { reason: c.rfq.award_reason })}
              </p>
            )}
          </div>

          {c.quotes.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('src.noQuotesTitle')}
                body={t('src.noQuotesBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('src.supplier')}</th>
                    <th scope="col" className="num">
                      {t('src.total')}
                    </th>
                    <th scope="col" className="num">
                      {t('src.leadTime')}
                    </th>
                    <th scope="col" className="num">
                      {t('src.paymentTerms')}
                    </th>
                    <th scope="col">{t('src.quality')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {c.quotes.map((q) => (
                    <tr
                      key={q.id}
                      className={
                        q.status === 'awarded' ? 'src__row--won' : undefined
                      }
                    >
                      <td>
                        <span className="detail__strong">{q.supplier_name}</span>
                        <span className="ds-caption">
                          {q.quote_number ?? ''}
                          {q.revision > 1
                            ? ` · ${t('src.revision', { n: String(q.revision) })}`
                            : ''}
                        </span>
                        {q.expired && (
                          <span className="ds-badge ds-badge--warning">
                            {t('src.quoteExpired')}
                          </span>
                        )}
                      </td>
                      <td className="num">
                        {money(q.total_inclusive, { currency: q.currency })}
                        {/* Marked, never ranked. B5.1 wants a person to
                            choose, because lead time and terms routinely
                            outweigh price. */}
                        {c.lowest_quote_id === q.id && (
                          <span className="ds-caption">{t('src.lowest')}</span>
                        )}
                      </td>
                      <td className="num">
                        {q.lead_time_days != null
                          ? t('src.days', { n: String(q.lead_time_days) })
                          : '—'}
                      </td>
                      <td className="num">
                        {q.payment_terms_days != null
                          ? t('src.days', { n: String(q.payment_terms_days) })
                          : '—'}
                      </td>
                      <td>{q.quality_note ?? '—'}</td>
                      <td>
                        {mayAward &&
                          c.rfq.status !== 'awarded' &&
                          !q.expired &&
                          q.status === 'received' && (
                            <button
                              className="ds-btn ds-btn--quiet"
                              onClick={() => {
                                setAwarding(q);
                                setReason('');
                              }}
                            >
                              {t('src.award')}
                            </button>
                          )}
                        {q.status === 'awarded' && (
                          <span className="ds-badge ds-badge--success">
                            {t('src.won')}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {awarding && (
            <div className="ds-panel__body src__award">
              <p className="ds-caption">
                {t('src.awardHint', { supplier: awarding.supplier_name })}
              </p>
              <TextInput
                id="src-reason"
                value={reason}
                onChange={setReason}
                placeholder={t('src.whyThisOne')}
              />
              <div className="form__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || reason.trim() === ''}
                  onClick={() => void award(awarding)}
                >
                  {t('src.awardAndOrder')}
                </button>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setAwarding(null)}
                >
                  {t('action.cancel')}
                </button>
              </div>
            </div>
          )}
        </section>
      )}
    </RemoteBody>
  );
}
