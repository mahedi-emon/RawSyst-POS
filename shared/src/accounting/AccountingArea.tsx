// The books (blueprint C9.3, C10, D4).
//
// Four things that were built and had no screen at all: the trial balance, the
// profit and loss, the balance sheet and the cash flow statement all had routes
// and nothing in either front end ever called them. A shop could not see its
// own profit.
//
// Two more that did not exist until 0080: the accounting calendar, and a way to
// read the audit trail.
//
// # Why they are one section
//
// A statement is a claim about a period, and the period's state is what decides
// whether the claim is stable — a P&L for a month that is still open will be
// different tomorrow. Putting the calendar on another screen would mean a
// person reads a figure without knowing whether it can still move.
//
// So the period picker sits at the top of the section and the statements below
// it say, on their face, whether the months they cover are closed.

import { useCallback, useMemo, useState } from 'react';

import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { fiscalCalendar, type FiscalCalendar } from '../api/accounting';
import { StatementsPanel } from './StatementsPanel';
import { PeriodsPanel } from './PeriodsPanel';
import { AuditTrailPanel } from './AuditTrailPanel';
import { VATReturnPanel } from './VATReturnPanel';
import { TreasuryPanel } from './TreasuryPanel';
import { ReconcilePanel } from './ReconcilePanel';
import { monthToDate } from './accounting';

type Tab = 'statements' | 'treasury' | 'reconcile' | 'vat' | 'periods' | 'trail';

export function AccountingArea({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();

  const mayClose = can('accounting.close_period');

  const [tab, setTab] = useState<Tab>('statements');
  const [period, setPeriod] = useState(monthToDate());

  // Loaded here and handed down, because the statements need to know which of
  // the months they cover are closed and the periods panel needs the same
  // list. Two requests for one calendar is one too many.
  const load = useCallback(
    () => fiscalCalendar(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);
  const calendar: FiscalCalendar | null =
    remote.state === 'ready' ? remote.data : null;

  const tabs = useMemo<Array<{ key: Tab; label: Key }>>(
    () => [
      { key: 'statements', label: 'acct.statements' },
      // Beside the statements, because a bank balance is the one figure on a
      // balance sheet somebody can check against an outside party.
      { key: 'treasury', label: 'treasury.title' },
      { key: 'reconcile', label: 'recon.title' },
      { key: 'vat', label: 'acct.vatReturn' },
      { key: 'periods', label: 'acct.periods' },
      { key: 'trail', label: 'acct.trail' },
    ],
    [],
  );

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('acct.title')}</h1>
          <p className="ds-caption">{t('acct.intro')}</p>
        </div>

        <div className="detail__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            {tabs.map((x) => (
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

      {tab === 'statements' && (
        <StatementsPanel
          companyId={companyId}
          period={period}
          onPeriod={setPeriod}
          calendar={calendar}
        />
      )}
      {tab === 'treasury' && <TreasuryPanel companyId={companyId} />}
      {tab === 'reconcile' && <ReconcilePanel companyId={companyId} />}
      {tab === 'vat' && (
        <VATReturnPanel
          companyId={companyId}
          period={period}
          onPeriod={setPeriod}
        />
      )}
      {tab === 'periods' && (
        <PeriodsPanel
          companyId={companyId}
          calendar={calendar}
          mayClose={mayClose}
          onChanged={reload}
        />
      )}
      {tab === 'trail' && <AuditTrailPanel />}
    </main>
  );
}
