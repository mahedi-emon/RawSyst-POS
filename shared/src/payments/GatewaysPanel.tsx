// The card providers a shop has connected (blueprint E3.3, E3.4).
//
// # Test is the most important button on this screen
//
// A key with a typo in it looks exactly like a key without one until a customer
// is standing at the counter. So the product talks to the acquirer with the
// stored credentials, writes down what came back, and shows it here — and a
// live connection cannot be switched on at all until that has passed, which the
// table's own rule enforces rather than this screen's good intentions.
//
// # Switched off is not deleted
//
// A connection money has gone through cannot be removed: the attempts are the
// record of what the acquirer said, and they have to survive. Switching it off
// is what stops the till reaching for it.

import { useCallback, useState } from 'react';

import {
  checkGateway,
  listGateways,
  listPaymentProviders,
  removeGateway,
  saveGateway,
  type Gateway,
  type PaymentProvider,
} from '../api/payments';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { FormError } from '../ui/Form';
import { shortDate, tenderName } from '../ui/format';
import { GatewayForm } from './GatewayForm';

export function GatewaysPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    async () => ({
      gateways: (await listGateways(client, companyId)).gateways,
      providers: (await listPaymentProviders(client)).providers,
    }),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [editing, setEditing] = useState<Gateway | null>(null);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const mayManage = can('gateway.manage');

  async function run(id: string, work: () => Promise<unknown>) {
    setBusy(id);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(null);
    }
  }

  function check(g: Gateway) {
    void run(g.id, () => checkGateway(client, companyId, g.id));
  }

  function toggle(g: Gateway) {
    void run(g.id, () =>
      saveGateway(
        client,
        companyId,
        {
          provider: g.provider,
          label: g.label,
          mode: g.mode,
          settings: g.settings,
          // Empty: the stored key stays where it is.
          secret: '',
          methods: g.methods,
          is_active: !g.is_active,
        },
        g.id,
      ),
    );
  }

  function remove(g: Gateway) {
    void run(g.id, () => removeGateway(client, companyId, g.id));
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { gateways: Gateway[]; providers: PaymentProvider[] }) => (
        <>
          {(adding || editing) && mayManage && (
            <GatewayForm
              companyId={companyId}
              providers={payload.providers}
              editing={editing ?? undefined}
              onDone={() => {
                setAdding(false);
                setEditing(null);
                reload();
              }}
              onCancel={() => {
                setAdding(false);
                setEditing(null);
              }}
            />
          )}

          <section className="ds-panel" aria-label={t('pay.connections')}>
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{t('pay.connections')}</h2>
                <p className="ds-caption">{t('pay.connectionsHint')}</p>
              </div>
              {mayManage && !adding && !editing && (
                <button
                  className="ds-btn ds-btn--primary ds-btn--sm"
                  onClick={() => setAdding(true)}
                >
                  {t('pay.connect')}
                </button>
              )}
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />
            </div>

            {payload.gateways.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('pay.noneTitle')}
                  body={t('pay.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('pay.connection')}</th>
                      <th scope="col">{t('pay.methods')}</th>
                      <th scope="col">{t('pay.lastTest')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.gateways.map((g) => {
                      const provider = payload.providers.find(
                        (p) => p.key === g.provider,
                      );
                      return (
                        <tr key={g.id}>
                          <td>
                            <span className="pay__name">{g.label}</span>
                            <span className="ds-caption">
                              {' '}
                              {provider?.name ?? g.provider}
                            </span>
                            <span
                              className={`ds-badge ds-badge--${
                                g.mode === 'live' ? 'info' : 'neutral'
                              }`}
                            >
                              {g.mode === 'live'
                                ? t('pay.modeLive')
                                : t('pay.modeTest')}
                            </span>
                            {g.is_active ? (
                              <span className="ds-badge ds-badge--success">
                                {t('pay.on')}
                              </span>
                            ) : (
                              <span className="ds-badge ds-badge--neutral">
                                {t('pay.off')}
                              </span>
                            )}
                          </td>
                          <td>
                            {g.methods.length === 0
                              ? '—'
                              : g.methods
                                  .map((m) => tenderName(m, t))
                                  .join(', ')}
                          </td>
                          <td>
                            {g.last_checked_at ? (
                              <>
                                <span
                                  className={`ds-badge ds-badge--${
                                    g.last_check_ok ? 'success' : 'danger'
                                  }`}
                                >
                                  {g.last_check_ok
                                    ? t('pay.answered')
                                    : t('pay.refused')}
                                </span>{' '}
                                {shortDate(g.last_checked_at, locale)}
                                {g.last_check_note && (
                                  <span className="pay__note">
                                    {g.last_check_note}
                                  </span>
                                )}
                              </>
                            ) : (
                              <span className="ds-caption">
                                {t('pay.neverTested')}
                              </span>
                            )}
                          </td>
                          <td className="ds-table__actions">
                            {mayManage && (
                              <>
                                <button
                                  className="ds-btn ds-btn--quiet ds-btn--sm"
                                  disabled={busy === g.id}
                                  onClick={() => check(g)}
                                >
                                  {t('pay.test')}
                                </button>
                                <button
                                  className="ds-btn ds-btn--quiet ds-btn--sm"
                                  disabled={busy === g.id}
                                  onClick={() => setEditing(g)}
                                >
                                  {t('action.edit')}
                                </button>
                                <button
                                  className="ds-btn ds-btn--quiet ds-btn--sm"
                                  disabled={busy === g.id}
                                  onClick={() => toggle(g)}
                                >
                                  {g.is_active ? t('pay.off') : t('pay.on')}
                                </button>
                                <button
                                  className="ds-btn ds-btn--quiet ds-btn--sm"
                                  disabled={busy === g.id}
                                  onClick={() => remove(g)}
                                >
                                  {t('action.remove')}
                                </button>
                              </>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </RemoteBody>
  );
}
