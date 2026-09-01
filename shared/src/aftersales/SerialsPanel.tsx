// Serial and IMEI tracking, and warranty (blueprint B15).
//
// # The lookup is the screen
//
// Somebody is standing at a counter holding a phone with a serial on the back,
// and the question is "is this still under warranty and who bought it". That is
// one box and one answer, so it is at the top and everything else is below it.
//
// # Whether it is under warranty is the server's answer
//
// Derived from the date on the server rather than compared in the browser: a
// device with a wrong clock must not tell a customer their cover has expired,
// and a stored flag would be wrong every morning until a job ran.

import { useCallback, useState } from 'react';

import {
  listSerials,
  lookupSerial,
  type Serial,
  type SerialStatus,
} from '../api/aftersales';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormError, TextInput } from '../ui/Form';
import { shortDate } from '../ui/format';

export function SerialsPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [term, setTerm] = useState('');
  const [found, setFound] = useState<Serial | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [status, setStatus] = useState<string>('');

  const load = useCallback(
    () => listSerials(client, companyId, status ? { status } : {}),
    [client, companyId, status],
  );
  const { remote, reload } = useRemote(load);

  async function find(e: React.FormEvent) {
    e.preventDefault();
    setFailure(null);
    setFound(null);
    try {
      setFound(await lookupSerial(client, companyId, term.trim()));
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  return (
    <>
      <form className="ds-panel after__lookup" onSubmit={(e) => void find(e)}>
        <div className="ds-panel__body after__lookuprow">
          <Field label={t('after.findSerial')} htmlFor="sn-lookup">
            <TextInput
              id="sn-lookup"
              value={term}
              onChange={setTerm}
              placeholder={t('after.serialOrImei')}
            />
          </Field>
          <button className="ds-btn ds-btn--primary" type="submit">
            {t('after.look')}
          </button>
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {found && (
            <div className="after__answer" role="status">
              <p className="after__answertitle">
                {found.product ?? found.sku ?? found.serial_no}
              </p>
              <p>
                {/* The counter's whole question, answered in one line. */}
                {found.under_warranty
                  ? t('after.underWarrantyUntil', {
                      date: found.warranty_until
                        ? shortDate(found.warranty_until, locale)
                        : '',
                    })
                  : t('after.notUnderWarranty')}
              </p>
              <p className="ds-caption">
                {t(`after.serial.${found.status}` as Key)}
                {found.customer ? ` · ${found.customer}` : ''}
                {found.sold_at
                  ? ` · ${t('after.soldOn', {
                      date: shortDate(found.sold_at, locale),
                    })}`
                  : ''}
              </p>
            </div>
          )}
        </div>
      </form>

      <section className="ds-panel" aria-label={t('after.serials')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('after.serials')}</h2>
          <div className="after__actions">
            <select
              className="input"
              aria-label={t('common.status')}
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">{t('after.anyStatus')}</option>
              {(
                [
                  'in_stock',
                  'reserved',
                  'sold',
                  'returned',
                  'in_repair',
                  'scrapped',
                ] as SerialStatus[]
              ).map((s) => (
                <option key={s} value={s}>
                  {t(`after.serial.${s}` as Key)}
                </option>
              ))}
            </select>
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Serial[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('after.noSerialsTitle')}
                  body={t('after.noSerialsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('after.serialNo')}</th>
                      <th scope="col">{t('after.product')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('after.warranty')}</th>
                      <th scope="col">{t('crm.customer')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((s) => (
                      <tr key={s.id}>
                        <td>
                          <span className="after__serial">{s.serial_no}</span>
                        </td>
                        <td>
                          {s.product ?? '—'}
                          {s.sku && <span className="ds-caption">{s.sku}</span>}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${serialBadge(s.status)}`}
                          >
                            {t(`after.serial.${s.status}` as Key)}
                          </span>
                        </td>
                        <td>
                          {s.warranty_until ? (
                            <>
                              {shortDate(s.warranty_until, locale)}
                              <span className="ds-caption">
                                {t(
                                  s.under_warranty
                                    ? 'after.covered'
                                    : 'after.coverEnded',
                                )}
                              </span>
                            </>
                          ) : (
                            '—'
                          )}
                        </td>
                        <td>{s.customer ?? '—'}</td>
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

function serialBadge(status: SerialStatus): string {
  switch (status) {
    case 'in_stock':
      return 'success';
    case 'sold':
      return 'info';
    case 'scrapped':
      return 'danger';
    case 'in_repair':
      return 'warning';
    default:
      return 'neutral';
  }
}
