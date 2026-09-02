// The notification centre (blueprint D3).
//
// # The bell knows its own number
//
// The count travels with the list, so the bell never shows a figure a second
// request has not caught up with — which is exactly the moment somebody is
// looking at it.
//
// # In-app cannot be switched off
//
// The preferences screen offers email, SMS and push. It does not offer in-app,
// because the centre is where a shop discovers why a submission failed, and a
// product that let somebody silence that would leave them with no way to find
// out.

import { useCallback, useEffect, useRef, useState } from 'react';

import {
  listNotifications,
  markAllRead,
  markRead,
  readPreferences,
  setPreference,
  type Notification,
  type NotificationPreference,
} from '../api/workflow';
import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { useLiveReload } from '../live/useLive';
import type { Key } from '../i18n/strings';
import { Icon } from '../ui/Icon';
import { shortDate } from '../ui/format';

export function NotificationBell({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState(false);
  const panel = useRef<HTMLDivElement | null>(null);

  const load = useCallback(
    () => listNotifications(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  // The bell refreshes when the server says something arrived, rather than
  // asking every ten seconds. A missed message costs nothing: the next read of
  // this list is correct regardless, which is why the socket is allowed to be
  // best-effort. See useLive.
  useLiveReload(client, ['notification.new'], reload, { companyId });

  const unread = remote.state === 'ready' ? remote.data.unread : 0;
  const notes: Notification[] = remote.state === 'ready' ? remote.data.data : [];

  // Escape closes it, and so does clicking outside. A panel that could only be
  // dismissed by pressing the bell again is a panel people leave open.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    function onClick(e: MouseEvent) {
      if (panel.current && !panel.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onClick);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onClick);
    };
  }, [open]);

  async function open1(n: Notification) {
    if (!n.is_read) {
      try {
        await markRead(client, companyId, n.id);
        reload();
      } catch {
        // A notification that would not mark itself read is not worth an error
        // in front of somebody: they have read it, which was the point.
      }
    }
  }

  return (
    <div className="bell" ref={panel}>
      <button
        className="bo__iconbtn bell__button"
        aria-label={t('note.bell', { count: String(unread) })}
        aria-expanded={open}
        onClick={() => {
          setOpen(!open);
          if (!open) reload();
        }}
      >
        <Icon name="alert" />
        {unread > 0 && (
          <span className="bell__count" aria-hidden="true">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div className="bell__panel" role="dialog" aria-label={t('note.title')}>
          <div className="bell__head">
            <h2 className="ds-h3">{t('note.title')}</h2>
            {unread > 0 && (
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => {
                  void markAllRead(client, companyId).then(reload);
                }}
              >
                {t('note.markAllRead')}
              </button>
            )}
          </div>

          {notes.length === 0 ? (
            <p className="bell__empty">{t('note.nothingYet')}</p>
          ) : (
            <ul className="bell__list">
              {notes.map((n) => (
                <li
                  key={n.id}
                  className={`bell__item bell__item--${n.severity}${
                    n.is_read ? '' : ' bell__item--unread'
                  }`}
                >
                  <button
                    className="bell__itembtn"
                    onClick={() => void open1(n)}
                  >
                    <span className="bell__itemtitle">{n.title}</span>
                    {n.body && <span className="ds-caption">{n.body}</span>}
                    <span className="ds-caption">
                      {t(`note.kind.${n.kind}` as Key)} ·{' '}
                      {shortDate(n.created_at, locale)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

// NotificationSettings is the preferences screen, offered from Setup.
export function NotificationSettings({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => readPreferences(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);

  async function change(
    p: NotificationPreference,
    patch: Partial<NotificationPreference>,
  ) {
    setBusy(true);
    try {
      await setPreference(client, companyId, {
        kind: p.kind,
        email: patch.email ?? p.email,
        sms: patch.sms ?? p.sms,
        push: patch.push ?? p.push,
      });
      reload();
    } finally {
      setBusy(false);
    }
  }

  if (remote.state !== 'ready') return null;

  return (
    <section className="ds-panel" aria-label={t('note.preferences')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('note.preferences')}</h2>
          {/* Said once, at the top, rather than as a disabled checkbox on every
              row that somebody would try to click. */}
          <p className="ds-caption">{t('note.inAppAlways')}</p>
        </div>
      </div>

      <div className="ds-panel__body ds-scroll-x">
        <table className="ds-table">
          <thead>
            <tr>
              <th scope="col">{t('note.tellMeAbout')}</th>
              <th scope="col">{t('note.email')}</th>
              <th scope="col">{t('note.sms')}</th>
              <th scope="col">{t('note.push')}</th>
            </tr>
          </thead>
          <tbody>
            {remote.data.data.map((p) => (
              <tr key={p.kind}>
                <td>{t(`note.kind.${p.kind}` as Key)}</td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={t('note.email')}
                    disabled={busy}
                    checked={p.email}
                    onChange={(e) => void change(p, { email: e.target.checked })}
                  />
                </td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={t('note.sms')}
                    disabled={busy}
                    checked={p.sms}
                    onChange={(e) => void change(p, { sms: e.target.checked })}
                  />
                </td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={t('note.push')}
                    disabled={busy}
                    checked={p.push}
                    onChange={(e) => void change(p, { push: e.target.checked })}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
