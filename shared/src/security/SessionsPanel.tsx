// Where the caller is signed in, and how to end one (blueprint H1).
//
// # The current session is labelled, not offered
//
// Somebody reviewing their sessions because they suspect a stolen one should
// not be able to sign themselves out by mistake while doing it. The row is
// there — hiding it would make the list disagree with reality — with the verb
// replaced by a label.

import { useCallback, useState } from 'react';

import {
  mySessions,
  revokeMySession,
  type ActiveSession,
} from '../api/security';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';

export function SessionsPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => mySessions(client), [client]);
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function end(id: string) {
    setBusy(true);
    setFailure(null);
    try {
      await revokeMySession(client, id);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('sec.sessions')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('sec.sessions')}</h2>
          <p className="ds-caption">{t('sec.sessionsHint')}</p>
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: ActiveSession[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('sec.noSessionsTitle')}
                body={t('sec.noSessionsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('sec.where')}</th>
                    <th scope="col">{t('sec.from')}</th>
                    <th scope="col">{t('sec.lastUsed')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((s) => (
                    <tr key={s.id}>
                      <td>
                        {s.device_label || t('sec.aBrowser')}
                        {s.current && (
                          <span className="ds-badge ds-badge--success">
                            {t('sec.thisOne')}
                          </span>
                        )}
                        {s.user_agent && (
                          <span className="ds-caption"> {s.user_agent}</span>
                        )}
                      </td>
                      <td>{s.ip || '—'}</td>
                      <td>
                        {shortDate(s.last_seen_at ?? s.created_at, locale)}
                      </td>
                      <td className="ds-table__actions">
                        {s.current ? (
                          <span className="ds-caption">
                            {t('sec.signOutFromTheBar')}
                          </span>
                        ) : (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() => void end(s.id)}
                          >
                            {t('sec.endIt')}
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
