// Staff, attendance, leave, payroll and end of service (blueprint C5, C6).
//
// # The expiring-document warning is above the tabs, not inside one
//
// C5 asks for Iqama and ID expiry alerting, and an expired residency permit
// stops somebody working — it is a legal problem, not an HR housekeeping task.
// So it sits at the top of the whole section where it is seen by anybody who
// opens Staff for any reason, rather than on a tab somebody has to think to
// visit.
//
// # Pay is a separate tab because it is a separate permission
//
// `hr.view` is the directory. `hr.view_pay` is what somebody earns, and
// `payroll.run` moves the money. A person who can see who works here and cannot
// see what they are paid gets a section with three tabs rather than a fourth
// that refuses them.

import { useCallback, useState } from 'react';

import { expiringIDs, type Employee } from '../api/hr';
import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { shortDate } from '../ui/format';
import { AttendancePanel } from './AttendancePanel';
import { DirectoryPanel } from './DirectoryPanel';
import { LeavePanel } from './LeavePanel';
import { PayrollPanel } from './PayrollPanel';

type Tab = 'directory' | 'attendance' | 'leave' | 'payroll';

export function HRArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const maySeePay = can('payroll.view');
  const [tab, setTab] = useState<Tab>('directory');

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'directory', label: 'hr.directory', shown: true },
    { key: 'attendance', label: 'hr.attendance', shown: true },
    { key: 'leave', label: 'hr.leave', shown: true },
    { key: 'payroll', label: 'hr.payroll', shown: maySeePay },
  ];
  const visible = tabs.filter((x) => x.shown);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('hr.title')}</h1>
          <p className="ds-caption">{t('hr.intro')}</p>
        </div>

        <div className="detail__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            {visible.map((x) => (
              <button
                key={x.key}
                className={`segmented__btn${tab === x.key ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === x.key}
                onClick={() => setTab(x.key)}
              >
                {t(x.label)}
              </button>
            ))}
          </div>
        </div>
      </header>

      <ExpiringDocuments companyId={companyId} onOpen={() => setTab('directory')} />

      {tab === 'directory' && <DirectoryPanel companyId={companyId} />}
      {tab === 'attendance' && <AttendancePanel companyId={companyId} />}
      {tab === 'leave' && <LeavePanel companyId={companyId} />}
      {tab === 'payroll' && maySeePay && <PayrollPanel companyId={companyId} />}
    </main>
  );
}

// ExpiringDocuments is C5's alert, and it says nothing when there is nothing
// to say.
//
// An empty banner that reads "0 documents expiring" is a banner people learn to
// scroll past, and the one time it matters they scroll past that too.
function ExpiringDocuments({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => expiringIDs(client, companyId, 60),
    [client, companyId],
  );
  const { remote } = useRemote(load);

  if (remote.state !== 'ready' || remote.data.data.length === 0) return null;

  const people = remote.data.data;
  const expired = people.filter((p) => p.id_expired);

  return (
    <section
      className={`hr__alert${expired.length > 0 ? ' hr__alert--stopped' : ''}`}
      role="status"
    >
      <div className="hr__alertsay">
        <p className="hr__alerttitle">
          {expired.length > 0
            ? t('hr.idsExpired', { count: String(expired.length) })
            : t('hr.idsExpiring', { count: String(people.length) })}
        </p>
        <p className="ds-caption">
          {expired.length > 0 ? t('hr.idsExpiredWhy') : t('hr.idsExpiringWhy')}
        </p>
      </div>

      <ul className="hr__alertlist">
        {people.slice(0, 6).map((p: Employee) => (
          <li key={p.id}>
            <span className="detail__strong">{p.full_name}</span>
            <span className="ds-caption">
              {p.id_expires_on ? shortDate(p.id_expires_on, locale) : ''}
            </span>
          </li>
        ))}
      </ul>

      <button className="ds-btn ds-btn--quiet" onClick={onOpen}>
        {t('hr.openDirectory')}
      </button>
    </section>
  );
}
