// What the acquirer actually said, failures included (blueprint E3.3).
//
// # This is not the tender list
//
// `sales_tender` records what the SHOP believes happened. This records what the
// PROVIDER said, and a declined card has no tender row at all — which is
// exactly why it needs a screen of its own. "Why did that card decline three
// times" is a question only this table answers.
//
// # The provider's own code, untranslated
//
// A decline code is quoted verbatim to a support line and is meaningless
// paraphrased. It is shown as it came back, beside the message, rather than
// mapped onto a friendlier word that would lose the only detail the bank cares
// about.

import { useCallback, useState } from 'react';

import {
  listPaymentAttempts,
  refundAttempt,
  type PaymentAttempt,
} from '../api/payments';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { money, shortDate, tenderName } from '../ui/format';

const TONE: Record<PaymentAttempt['status'], string> = {
  initiated: 'neutral',
  authorised: 'info',
  captured: 'success',
  failed: 'danger',
  cancelled: 'neutral',
  refunded: 'warning',
};

const LABEL: Record<PaymentAttempt['status'], Key> = {
  initiated: 'pay.stInitiated',
  authorised: 'pay.stAuthorised',
  captured: 'pay.stCaptured',
  failed: 'pay.stFailed',
  cancelled: 'pay.stCancelled',
  refunded: 'pay.stRefunded',
};

export function AttemptsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => listPaymentAttempts(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const mayRefund = can('sales.refund');

  async function sendBack(a: PaymentAttempt) {
    setBusy(a.id);
    setFailure(null);
    try {
      // The whole charge. A partial refund is a decision about a sale, and it
      // belongs on the sale rather than on the acquirer's record of it.
      await refundAttempt(client, companyId, a.id, a.amount);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('pay.attempts')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('pay.attempts')}</h2>
          <p className="ds-caption">{t('pay.attemptsHint')}</p>
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { attempts: PaymentAttempt[] }) =>
          payload.attempts.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('pay.noAttemptsTitle')}
                body={t('pay.noAttemptsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('pay.when')}</th>
                    <th scope="col">{t('pay.method')}</th>
                    <th scope="col" className="num">
                      {t('common.amount')}
                    </th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">{t('pay.providerSaid')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.attempts.map((a) => (
                    <tr key={a.id}>
                      <td>{shortDate(a.created_at, locale)}</td>
                      <td>{tenderName(a.method, t)}</td>
                      <td className="num">
                        {money(a.amount, { currency: a.currency })}
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${TONE[a.status]}`}
                        >
                          {t(LABEL[a.status])}
                        </span>
                      </td>
                      <td>
                        {a.provider_code && (
                          <span className="pay__code">{a.provider_code}</span>
                        )}
                        {a.provider_message && (
                          <span className="ds-caption">
                            {' '}
                            {a.provider_message}
                          </span>
                        )}
                        {a.provider_ref && (
                          <span className="pay__note">{a.provider_ref}</span>
                        )}
                      </td>
                      <td className="ds-table__actions">
                        {mayRefund && a.status === 'captured' && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy === a.id}
                            onClick={() => void sendBack(a)}
                          >
                            {t('pay.sendBack')}
                          </button>
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
