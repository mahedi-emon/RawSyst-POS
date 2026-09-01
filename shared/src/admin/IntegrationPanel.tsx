// Webhooks and API keys (blueprint H6).
//
// # The endpoint list carries how it has been behaving
//
// Queued, failed, when it last delivered, and the last error. A list of URLs
// would leave somebody testing them one at a time to find the broken one — and
// the broken one is why they opened this screen.
//
// # A minted key is shown once, deliberately
//
// In a panel that has to be dismissed, with the reason written next to it. A
// key quietly listed in a table is a key somebody assumes they can come back
// for, and this product cannot show it again: what it keeps is a hash.

import { useCallback, useState } from 'react';

import {
  createWebhook,
  listAPIKeys,
  listWebhooks,
  mintAPIKey,
  revokeAPIKey,
  setWebhookActive,
  webhookDeliveries,
  type APIKey,
  type Minted,
  type WebhookDelivery,
  type WebhookEndpoint,
} from '../api/admin';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { shortDate } from '../ui/format';

export function IntegrationPanel({ companyId }: { companyId: string }) {
  return (
    <>
      <WebhooksPanel companyId={companyId} />
      <KeysPanel companyId={companyId} />
    </>
  );
}

function WebhooksPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('integration.manage');
  const load = useCallback(
    () => listWebhooks(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [history, setHistory] = useState<{
    id: string;
    rows: WebhookDelivery[];
  } | null>(null);

  async function toggle(w: WebhookEndpoint) {
    setBusy(true);
    setFailure(null);
    try {
      await setWebhookActive(client, companyId, w.id, !w.is_active);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function showHistory(w: WebhookEndpoint) {
    setFailure(null);
    try {
      const out = await webhookDeliveries(client, companyId, w.id);
      setHistory({ id: w.id, rows: out.data });
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { data: WebhookEndpoint[]; events: string[] }) => (
        <>
          {adding && (
            <WebhookForm
              companyId={companyId}
              events={payload.events}
              onCancel={() => setAdding(false)}
              onSaved={() => {
                setAdding(false);
                reload();
              }}
            />
          )}

          <section className="ds-panel" aria-label={t('adm.webhooks')}>
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{t('adm.webhooks')}</h2>
                <p className="ds-caption">{t('adm.webhooksHint')}</p>
              </div>
              {mayManage && !adding && (
                <button
                  className="ds-btn ds-btn--primary"
                  onClick={() => setAdding(true)}
                >
                  {t('adm.addEndpoint')}
                </button>
              )}
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />
            </div>

            {payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('adm.noEndpointsTitle')}
                  body={t('adm.noEndpointsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('adm.endpoint')}</th>
                      <th scope="col">{t('adm.events')}</th>
                      <th scope="col">{t('adm.behaviour')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((w) => (
                      <tr
                        key={w.id}
                        className={!w.is_active ? 'detail__row--aside' : undefined}
                      >
                        <td>
                          <span className="detail__strong">{w.name}</span>
                          <span className="ds-caption adm__url">{w.url}</span>
                        </td>
                        <td>
                          <span className="ds-caption">
                            {w.events.join(', ')}
                          </span>
                        </td>
                        <td>
                          {/* The reason anybody opens this screen. */}
                          {w.failed > 0 ? (
                            <span className="ds-badge ds-badge--danger">
                              {t('adm.failedCount', { count: String(w.failed) })}
                            </span>
                          ) : w.queued > 0 ? (
                            <span className="ds-badge ds-badge--warning">
                              {t('adm.queuedCount', { count: String(w.queued) })}
                            </span>
                          ) : (
                            <span className="ds-badge ds-badge--success">
                              {t('adm.delivering')}
                            </span>
                          )}
                          {w.last_delivered_at && (
                            <span className="ds-caption">
                              {t('adm.lastDelivered', {
                                when: shortDate(w.last_delivered_at, locale),
                              })}
                            </span>
                          )}
                          {w.last_error && (
                            <span className="ds-caption adm__error">
                              {w.last_error}
                            </span>
                          )}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${w.is_active ? 'info' : 'neutral'}`}
                          >
                            {t(w.is_active ? 'adm.on' : 'adm.off')}
                          </span>
                        </td>
                        <td>
                          <div className="adm__rowactions">
                            <button
                              className="ds-btn ds-btn--quiet"
                              onClick={() => void showHistory(w)}
                            >
                              {t('adm.deliveries')}
                            </button>
                            {mayManage && (
                              <button
                                className="ds-btn ds-btn--quiet"
                                disabled={busy}
                                onClick={() => void toggle(w)}
                              >
                                {t(w.is_active ? 'adm.switchOff' : 'adm.switchOn')}
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            {history && (
              <div className="ds-panel__body ds-scroll-x">
                <h3 className="ds-h3">{t('adm.deliveries')}</h3>
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.when')}</th>
                      <th scope="col">{t('adm.event')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('adm.attempts')}
                      </th>
                      <th scope="col">{t('adm.answer')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {history.rows.map((d) => (
                      <tr key={d.id}>
                        <td>{shortDate(d.created_at, locale)}</td>
                        <td>{d.event}</td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${deliveryBadge(d.status)}`}
                          >
                            {d.status}
                          </span>
                        </td>
                        <td className="num">{d.attempts}</td>
                        <td>
                          {d.response_status ?? '—'}
                          {d.last_error && (
                            <span className="ds-caption adm__error">
                              {d.last_error}
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setHistory(null)}
                >
                  {t('action.close')}
                </button>
              </div>
            )}
          </section>
        </>
      )}
    </RemoteBody>
  );
}

function deliveryBadge(status: string): string {
  switch (status) {
    case 'delivered':
      return 'success';
    case 'abandoned':
    case 'failed':
      return 'danger';
    default:
      return 'warning';
  }
}

function WebhookForm({
  companyId,
  events,
  onCancel,
  onSaved,
}: {
  companyId: string;
  events: string[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [name, setName] = useState('');
  const [url, setUrl] = useState('https://');
  const [chosen, setChosen] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await createWebhook(client, companyId, { name, url, events: chosen });
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel adm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('adm.addEndpoint')}</h2>
        <p className="ds-caption">{t('adm.httpsOnly')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('adm.endpointName')} htmlFor="wh-name" required>
            <TextInput id="wh-name" value={name} onChange={setName} />
          </Field>
          <Field label={t('adm.url')} htmlFor="wh-url" required>
            <TextInput id="wh-url" value={url} onChange={setUrl} />
          </Field>
        </div>

        <fieldset className="adm__events">
          <legend className="field__label">{t('adm.events')}</legend>
          {/* Offered from the server's own list, so a screen cannot offer an
              event the server would refuse. */}
          {events.map((ev) => (
            <label key={ev} className="adm__check">
              <input
                type="checkbox"
                checked={chosen.includes(ev)}
                onChange={(e) =>
                  setChosen((prev) =>
                    e.target.checked
                      ? [...prev, ev]
                      : prev.filter((x) => x !== ev),
                  )
                }
              />
              <span>{ev}</span>
            </label>
          ))}
        </fieldset>

        <FormActions
          submitLabel={t('adm.addEndpoint')}
          busy={busy}
          disabled={name.trim() === '' || chosen.length === 0}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function KeysPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('integration.manage');
  const load = useCallback(
    () => listAPIKeys(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [minting, setMinting] = useState(false);
  const [minted, setMinted] = useState<Minted | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function revoke(k: APIKey) {
    setBusy(true);
    setFailure(null);
    try {
      await revokeAPIKey(client, companyId, k.id);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { data: APIKey[]; grantable: string[] }) => (
        <>
          {/* Shown once, and it has to be dismissed. See the file note. */}
          {minted && (
            <section className="ds-panel adm__minted">
              <div className="ds-panel__body">
                <p className="adm__mintedwhy">{t('adm.keyShownOnce')}</p>
                <code className="adm__secret">{minted.secret}</code>
                <button
                  className="ds-btn ds-btn--primary"
                  onClick={() => {
                    setMinted(null);
                    reload();
                  }}
                >
                  {t('adm.keptIt')}
                </button>
              </div>
            </section>
          )}

          {minting && !minted && (
            <KeyForm
              companyId={companyId}
              grantable={payload.grantable}
              onCancel={() => setMinting(false)}
              onMinted={(k) => {
                setMinting(false);
                setMinted(k);
              }}
            />
          )}

          <section className="ds-panel" aria-label={t('adm.apiKeys')}>
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{t('adm.apiKeys')}</h2>
                <p className="ds-caption">{t('adm.apiKeysHint')}</p>
              </div>
              {mayManage && !minting && (
                <button
                  className="ds-btn ds-btn--primary"
                  onClick={() => setMinting(true)}
                >
                  {t('adm.newKey')}
                </button>
              )}
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />
            </div>

            {payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('adm.noKeysTitle')}
                  body={t('adm.noKeysBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('adm.key')}</th>
                      <th scope="col">{t('adm.mayDo')}</th>
                      <th scope="col">{t('adm.lastUsed')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((k) => (
                      <tr
                        key={k.id}
                        className={!k.is_live ? 'detail__row--aside' : undefined}
                      >
                        <td>
                          <span className="detail__strong">{k.name}</span>
                          {/* Enough to tell two keys apart, never enough to
                              reconstruct one. */}
                          <span className="ds-caption adm__prefix">
                            {k.key_prefix}…
                          </span>
                        </td>
                        <td>
                          <span className="ds-caption">
                            {k.permissions.join(', ')}
                          </span>
                        </td>
                        <td>
                          {k.last_used_at
                            ? shortDate(k.last_used_at, locale)
                            : t('adm.neverUsed')}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${k.is_live ? 'success' : 'neutral'}`}
                          >
                            {t(k.is_live ? 'adm.live' : 'adm.revoked')}
                          </span>
                        </td>
                        <td>
                          {mayManage && k.is_live && (
                            <button
                              className="ds-btn ds-btn--quiet"
                              disabled={busy}
                              onClick={() => void revoke(k)}
                            >
                              {t('adm.revoke')}
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
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

function KeyForm({
  companyId,
  grantable,
  onCancel,
  onMinted,
}: {
  companyId: string;
  grantable: string[];
  onCancel: () => void;
  onMinted: (k: Minted) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [name, setName] = useState('');
  const [chosen, setChosen] = useState<string[]>([]);
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      onMinted(
        await mintAPIKey(client, companyId, {
          name,
          permissions: chosen,
          expires_on: expires || undefined,
        }),
      );
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel adm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('adm.newKey')}</h2>
        <p className="ds-caption">{t('adm.newKeyHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('adm.keyName')} htmlFor="ak-name" required>
            <TextInput id="ak-name" value={name} onChange={setName} />
          </Field>
          <Field label={t('adm.keyExpires')} htmlFor="ak-expires">
            <input
              id="ak-expires"
              type="date"
              className="field__input"
              value={expires}
              onChange={(e) => setExpires(e.target.value)}
            />
          </Field>
        </div>

        <fieldset className="adm__events">
          <legend className="field__label">{t('adm.mayDo')}</legend>
          {/* Only what the caller holds themselves, sent by the server. A form
              that offered more would offer an escalation and then be refused. */}
          <p className="field__hint">{t('adm.onlyWhatYouHold')}</p>
          {grantable.map((p) => (
            <label key={p} className="adm__check">
              <input
                type="checkbox"
                checked={chosen.includes(p)}
                onChange={(e) =>
                  setChosen((prev) =>
                    e.target.checked ? [...prev, p] : prev.filter((x) => x !== p),
                  )
                }
              />
              <span>{p}</span>
            </label>
          ))}
        </fieldset>

        <FormActions
          submitLabel={t('adm.newKey')}
          busy={busy}
          disabled={name.trim() === '' || chosen.length === 0}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
