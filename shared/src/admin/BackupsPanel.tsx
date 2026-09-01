// Backups (blueprint H4).
//
// # The headline is the last VERIFIED backup
//
// H4: a backup that cannot be restored is not a backup. The most recent
// successful RUN is a more comforting number and a less true one, so the tile
// reads the verified date and says plainly when there is none.
//
// # Verifying is a person's act, recorded
//
// This product does not take the backup — that is the operator's job with
// pg_dump or a managed snapshot. What it owns is the record, including whether
// anybody has proved the thing restores, which is the fact that makes a silent
// backup failure visible instead of discovered during a restore.

import { useCallback, useState } from 'react';

import {
  backupHealth,
  listBackups,
  startBackup,
  verifyBackup,
  type Backup,
  type BackupHealth,
} from '../api/admin';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError, TextInput } from '../ui/Form';
import { shortDate } from '../ui/format';

export function BackupsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayRun = can('backup.run');

  const healthLoad = useCallback(
    () => backupHealth(client, companyId),
    [client, companyId],
  );
  const health = useRemote(healthLoad);

  const load = useCallback(
    () => listBackups(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [verifying, setVerifying] = useState<Backup | null>(null);
  const [problem, setProblem] = useState('');

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
      health.reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {health.remote.state === 'ready' && (
        <HealthTile health={health.remote.data} />
      )}

      <section className="ds-panel" aria-label={t('adm.backups')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('adm.backups')}</h2>
            <p className="ds-caption">{t('adm.backupsHint')}</p>
          </div>
          {mayRun && (
            <button
              className="ds-btn ds-btn--primary"
              disabled={busy}
              onClick={() => void run(() => startBackup(client, companyId))}
            >
              {t('adm.recordBackup')}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {verifying && (
            <div className="adm__verify">
              <p className="ds-caption">
                {t('adm.verifyHint', {
                  when: shortDate(verifying.started_at, locale),
                })}
              </p>
              <TextInput
                id="bk-problem"
                value={problem}
                onChange={setProblem}
                placeholder={t('adm.whatWentWrong')}
              />
              <div className="form__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await verifyBackup(client, companyId, verifying.id, '');
                      setVerifying(null);
                      setProblem('');
                    })
                  }
                >
                  {t('adm.itRestored')}
                </button>
                <button
                  className="ds-btn ds-btn--quiet"
                  disabled={busy || problem.trim() === ''}
                  onClick={() =>
                    void run(async () => {
                      await verifyBackup(
                        client,
                        companyId,
                        verifying.id,
                        problem,
                      );
                      setVerifying(null);
                      setProblem('');
                    })
                  }
                >
                  {t('adm.itDidNot')}
                </button>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setVerifying(null)}
                >
                  {t('action.cancel')}
                </button>
              </div>
            </div>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Backup[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('adm.noBackupsTitle')}
                  body={t('adm.noBackupsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('adm.taken')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('adm.verified')}</th>
                      <th scope="col">{t('adm.whereItWent')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((b) => (
                      <tr key={b.id}>
                        <td>
                          {shortDate(b.started_at, locale)}
                          <span className="ds-caption">
                            {t(`adm.backupKind.${b.kind}` as Key)}
                            {b.requested_by ? ` · ${b.requested_by}` : ''}
                          </span>
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${backupBadge(b.status)}`}
                          >
                            {t(`adm.backupStatus.${b.status}` as Key)}
                          </span>
                          {b.error && (
                            <span className="ds-caption adm__error">{b.error}</span>
                          )}
                        </td>
                        <td>
                          {/* Three states, not two: proved, proved broken, and
                              nobody has looked. The third must never read as
                              the first. */}
                          {b.verified_at ? (
                            <span className="ds-badge ds-badge--success">
                              {shortDate(b.verified_at, locale)}
                            </span>
                          ) : b.verify_error ? (
                            <span className="ds-badge ds-badge--danger">
                              {t('adm.wouldNotRestore')}
                            </span>
                          ) : (
                            <span className="ds-badge ds-badge--warning">
                              {t('adm.notChecked')}
                            </span>
                          )}
                          {b.verify_error && (
                            <span className="ds-caption adm__error">
                              {b.verify_error}
                            </span>
                          )}
                        </td>
                        <td>
                          <span className="ds-caption adm__url">
                            {b.location ?? '—'}
                          </span>
                          {b.size_bytes != null && (
                            <span className="ds-caption">
                              {readableSize(b.size_bytes)}
                            </span>
                          )}
                        </td>
                        <td>
                          {mayRun && b.status === 'succeeded' && (
                            <button
                              className="ds-btn ds-btn--quiet"
                              onClick={() => {
                                setVerifying(b);
                                setProblem('');
                              }}
                            >
                              {t('adm.recordVerification')}
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
    </>
  );
}

function HealthTile({ health }: { health: BackupHealth }) {
  const t = useT();
  const { locale } = useLocale();

  return (
    <section
      className={`ds-panel adm__health${health.at_risk ? ' adm__health--risk' : ''}`}
      role="status"
    >
      <div className="ds-panel__body">
        <p className="adm__healthtitle">
          {health.at_risk ? t('adm.atRisk') : t('adm.protected')}
        </p>
        <p>{health.summary}</p>
        {health.last_verified_at && (
          <p className="ds-caption">
            {t('adm.lastVerified', {
              when: shortDate(health.last_verified_at, locale),
            })}
          </p>
        )}
        {health.recent_failures > 0 && (
          <p className="ds-caption">
            {t('adm.failedThisWeek', {
              count: String(health.recent_failures),
            })}
          </p>
        )}
      </div>
    </section>
  );
}

function backupBadge(status: string): string {
  switch (status) {
    case 'succeeded':
      return 'success';
    case 'failed':
      return 'danger';
    default:
      return 'info';
  }
}

// readableSize renders bytes the way a person reads them off a disk.
function readableSize(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${units[i]}`;
}
