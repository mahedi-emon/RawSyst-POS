// The audit trail (blueprint D4).
//
// D4 fixes six fields — who, what, when, where, before, after — and says the
// log "cannot be edited or deleted by any user, including Owner, to preserve
// evidentiary integrity". The database enforces that with a trigger. This
// screen is the other half: a record nobody can read is not evidence either.
//
// # The before and after are shown raw
//
// A row's `before` and `after` are the JSON the writer recorded, and they are
// rendered as they are rather than summarised into a sentence. This is
// evidence. A prettier rendering would mean the reader sees an interpretation
// of the record rather than the record, and the interpretation is written by
// the same codebase whose behaviour is being audited.
//
// # The filter offers the verbs that are actually there
//
// The list of actions comes back with the trail rather than being a fixed list
// in this file. A fixed list goes stale the day a module is added, and the
// person looking for what happened is exactly the person who cannot be expected
// to guess the vocabulary.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { shortDate } from '../ui/format';
import { auditTrail, type AuditRecord, type AuditTrail } from '../api/accounting';

export function AuditTrailPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [action, setAction] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');

  const load = useCallback(
    () =>
      auditTrail(client, {
        action: action || undefined,
        from: from || undefined,
        to: to || undefined,
      }),
    [client, action, from, to],
  );
  const { remote, reload } = useRemote(load);

  const actions = remote.state === 'ready' ? remote.data.actions : [];

  return (
    <>
      <section className="ds-panel acct__controls">
        <div className="ds-panel__body acct__controlrow">
          <Field label={t('audit.whatHappened')} htmlFor="audit-action">
            <select
              id="audit-action"
              className="input"
              value={action}
              onChange={(e) => setAction(e.target.value)}
            >
              <option value="">{t('audit.everything')}</option>
              {actions.map((a) => (
                <option key={a} value={a}>
                  {actionLabel(a, t)}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('common.from')} htmlFor="audit-from">
            <input
              id="audit-from"
              type="date"
              className="field__input"
              value={from}
              max={to || undefined}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>

          <Field label={t('common.to')} htmlFor="audit-to">
            <input
              id="audit-to"
              type="date"
              className="field__input"
              value={to}
              min={from || undefined}
              onChange={(e) => setTo(e.target.value)}
            />
          </Field>
        </div>
      </section>

      <section className="ds-panel" aria-label={t('acct.trail')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('acct.trail')}</h2>
          {/* Said on the screen rather than only in the schema. Somebody
              wondering whether a row could have been tidied away should be able
              to read the answer here. */}
          <p className="ds-caption">{t('audit.appendOnly')}</p>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(trail: AuditTrail) =>
            trail.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('audit.noneTitle')}
                  body={t('audit.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('audit.when')}</th>
                      <th scope="col">{t('audit.who')}</th>
                      <th scope="col">{t('audit.whatHappened')}</th>
                      <th scope="col">{t('audit.toWhat')}</th>
                      <th scope="col">{t('audit.where')}</th>
                      <th scope="col">{t('audit.detail')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {trail.data.map((r, i) => (
                      <Row key={r.occurred_at + r.action + i} record={r} locale={locale} />
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

function Row({
  record,
  locale,
}: {
  record: AuditRecord;
  locale: Parameters<typeof shortDate>[1];
}) {
  const t = useT();
  const detail = summarise(record);

  return (
    <tr>
      <td>{shortDate(record.occurred_at, locale)}</td>
      <td>
        {/* An action with nobody against it is the system acting on its own —
            a scheduled job, a sweep. Saying so is better than an empty cell,
            which reads as a record that lost its name. */}
        {record.actor || (
          <span className="ds-caption">{t('audit.bySystem')}</span>
        )}
      </td>
      <td className="detail__strong">{actionLabel(record.action, t)}</td>
      <td>
        <span>{entityLabel(record.entity_type, t)}</span>
        {record.entity_id && (
          <span className="ds-caption audit__id">{record.entity_id}</span>
        )}
      </td>
      <td>
        {record.ip}
        {record.device && <span className="ds-caption">{record.device}</span>}
      </td>
      <td>
        {detail && (
          // The record as recorded. See the note at the top: a friendlier
          // rendering would be an interpretation written by the same codebase
          // whose behaviour is being audited.
          <pre className="audit__json">{detail}</pre>
        )}
      </td>
    </tr>
  );
}

/** The before and after, as they were written.
 *
 *  Both when both are there, because half the value of an audit row is the
 *  difference between them. */
function summarise(record: AuditRecord): string {
  const parts: string[] = [];
  if (record.before !== undefined && record.before !== null) {
    parts.push(JSON.stringify(record.before));
  }
  if (record.after !== undefined && record.after !== null) {
    parts.push(JSON.stringify(record.after));
  }
  return parts.join('\n');
}

/** An action in words, falling back to its own name.
 *
 *  The same fallback the staff screen uses for a permission: a verb recorded by
 *  a module whose words are not in the catalogue yet should read as itself
 *  rather than vanish. In an audit trail that matters more than anywhere else —
 *  a row that renders as blank is a row somebody will assume is a bug rather
 *  than an action. */
function actionLabel(action: string, t: (k: Key) => string): string {
  const key = `audit.action.${action}` as Key;
  const named = t(key);
  return named === key ? action.replace(/_/g, ' ') : named;
}

function entityLabel(entity: string, t: (k: Key) => string): string {
  const key = `audit.entity.${entity}` as Key;
  const named = t(key);
  return named === key ? entity.replace(/_/g, ' ') : named;
}
