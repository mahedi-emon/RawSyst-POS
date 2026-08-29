// Invoices that have not finished reporting.
//
// The most delicate screen in the product, because it is the one an owner will
// look at when they are worried, and the honest answer right now is
// uncomfortable: nothing has been submitted, and nothing can be until the P1
// verification gate closes.
//
// # What this screen must not do
//
// It must not offer a Retry button. There is nothing to retry — the terminal
// refuses to sign because the canonicalisation and QR TLV encoding are
// unverified, and a button implying otherwise would have an owner clicking it
// for weeks.
//
// It must not show a spinner, a progress bar, or the word "processing". All
// three imply something is happening.
//
// It must not describe the sales as at risk. They are not: the money, the
// stock and the books are all recorded correctly and the receipts are valid.
// What is outstanding is the regulatory submission, and saying that precisely
// is more reassuring than vagueness, not less.

import { useCallback } from 'react';

import { fetchCompliance, type ComplianceRow } from '../api/drilldown';
import { useAuth } from '../auth/session';
import { money, longDate } from '../ui/format';
import { DetailScreen, EmptyState, RemoteBody } from './DetailScreen';
import { InvoiceState } from './InvoiceState';
import { useRemote } from './useRemote';
import { formatAge } from './drilldown';
import { useLocale, useT } from '../i18n/locale';

export function ComplianceScreen({
  companyId,
  onBack,
}: {
  companyId: string;
  onBack: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const load = useCallback(
    () => fetchCompliance(client, companyId),
    [client, companyId],
  );
  const { remote, reload, refreshing } = useRemote(load);

  return (
    <DetailScreen
      title={t('comp.reportingToZatca')}
      subtitle={t('comp.subtitle')}
      onBack={onBack}
      onRefresh={reload}
      refreshing={refreshing}
    >
      <RemoteBody remote={remote} onRetry={reload}>
        {(d) => (
          <>
            {/* The explanation comes FIRST, above the list. An owner reading a
                queue of unreported invoices needs to know why before they
                count them, or they spend the intervening seconds alarmed. */}
            {!d.signing_available && <GateNotice outstanding={d.outstanding} />}

            <div className="ds-panel">
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('common.outstanding')}</h2>
                <span className="ds-caption">
                  {d.outstanding} invoice{d.outstanding === 1 ? '' : 's'}
                  {d.oldest_hours > 0 && ` · oldest ${formatAge(d.oldest_hours)}`}
                </span>
              </div>

              <div className="ds-panel__body ds-scroll-x">
                {d.rows.length === 0 ? (
                  <EmptyState
                    title={t('comp.everythingReported')}
                    body={t('comp.nothingWaiting')}
                  />
                ) : (
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('comp.issued')}</th>
                        <th scope="col">{t('common.invoice')}</th>
                        <th scope="col" className="num">{t('comp.chain')}</th>
                        <th scope="col">{t('comp.waiting')}</th>
                        <th scope="col">{t('common.status')}</th>
                        <th scope="col" className="num">{t('common.amount')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.rows.map((row) => (
                        <Row key={row.invoice_id} row={row} currency={d.base_currency} />
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          </>
        )}
      </RemoteBody>
    </DetailScreen>
  );
}

/** Why the queue exists, stated plainly and without a button.
 *
 * Deliberately not styled as an error. It is the system behaving as designed:
 * refusing to produce a cryptographic document it cannot yet produce correctly
 * is the safe behaviour, and inventing one would be the unsafe one. */
function GateNotice({ outstanding }: { outstanding: number }) {
  const t = useT();
  return (
    <section className="ds-panel gate" aria-label={t('comp.whyWaiting')}>
      <div className="ds-panel__body">
        <h2 className="ds-h3">{t('comp.notActive')}</h2>

        <p className="gate__body">
          {outstanding === 0
            ? t('compliance.noneWaiting')
            : `${outstanding} invoice${outstanding === 1 ? ' is' : 's are'} held here.`}{' '}
          They have not been submitted because this installation cannot yet sign
          invoices: the signing specification has not completed verification, and
          the system will not produce a cryptographic document it cannot produce
          correctly.
        </p>

        <p className="gate__body">
          <strong>{t('comp.salesAreFine')}</strong>{' '}{t('comp.allRecorded')}</p>

        <p className="gate__body ds-muted">{t('comp.nothingToRetry')}</p>
      </div>
    </section>
  );
}

function Row({ row, currency }: { row: ComplianceRow; currency: string }) {
  const t = useT();
  const { locale } = useLocale();
  return (
    <tr>
      <td className="ds-date">{longDate(row.issued_at, locale)}</td>
      <td>
        <span className="detail__strong">
          {row.human_number || row.invoice_id.slice(0, 8)}
        </span>
        {row.doc_type === 'credit_note' && (
          <span className="ds-caption">{t('comp.creditNote')}</span>
        )}
      </td>
      {/* The chain position. A gap in the sequence is the exact signal tamper
          detection looks for, so an owner chasing a stuck invoice needs it. */}
      <td className="num">{row.icv > 0 ? row.icv : '—'}</td>
      <td>
        <Age hours={row.age_hours} />
      </td>
      <td>
        <InvoiceState state={row.state} />
      </td>
      <td className="num">{money(row.total_inclusive, { currency })}</td>
    </tr>
  );
}

/** How long it has waited, against the thresholds of design 08 §4: notice past
 *  12 hours, warning past 24, critical past 72. Word and colour together. */
function Age({ hours }: { hours: number }) {
  const tone =
    hours >= 72 ? 'ds-badge--danger' : hours >= 24 ? 'ds-badge--warning' : 'ds-badge--neutral';
  return <span className={`ds-badge ${tone}`}>{formatAge(hours)}</span>;
}
