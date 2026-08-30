// The accounting calendar (blueprint C10).
//
// # A year as a strip of twelve
//
// C10 draws the year as a list of months with a tick against each closed one,
// and that shape is right: the question a person brings to this screen is "how
// far have we closed", which a grid answers at a glance and a table of dates
// does not.
//
// # Only one month offers a Close button
//
// Periods close in order — closing August while July is open leaves the
// year-to-date figures moving underneath the August statements — so the only
// month with a button is the earliest one still open. Putting a Close on every
// open month would mean most presses come back refused, and a screen whose
// buttons usually fail is a screen people stop believing.
//
// # Reopening asks for the reason before it asks for the press
//
// The reason box appears first and the button is disabled until there is enough
// in it. C10 makes the reason mandatory because reopening changes figures
// somebody has already reported; asking for it after the fact, in a dialog that
// appears once the decision is made, invites "correction" as an answer.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState } from '../dashboard/DetailScreen';
import { FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import { money, monthName } from '../ui/format';
import {
  closeFiscalYear,
  closePeriod,
  openFiscalYear,
  reopenPeriod,
  type FiscalCalendar,
  type Period,
  type YearEnd,
} from '../api/accounting';
import { closablePeriod, nextYearToOpen, reopenable } from './accounting';

/** The length the database refuses anything shorter than. */
const MIN_REASON = 10;

export function PeriodsPanel({
  companyId,
  calendar,
  mayClose,
  onChanged,
}: {
  companyId: string;
  calendar: FiscalCalendar | null;
  mayClose: boolean;
  onChanged: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  // The year-end routine takes the more restricted of the two period
  // permissions: closing a MONTH can be undone with a reason, and closing a
  // year empties revenue and expense into Retained Earnings and locks every
  // month in it beyond reopening.
  const mayReopen = can('accounting.reopen_period');

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [closingYear, setClosingYear] = useState<number | null>(null);
  const [closed, setClosed] = useState<YearEnd | null>(null);
  const closable = closablePeriod(calendar);

  async function closeYear(year: number) {
    setBusy(true);
    setFailure(null);
    try {
      const done = await closeFiscalYear(client, companyId, year);
      setClosed(done);
      setClosingYear(null);
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
      setClosingYear(null);
    } finally {
      setBusy(false);
    }
  }

  async function openNextYear() {
    setBusy(true);
    setFailure(null);
    try {
      await openFiscalYear(client, companyId, nextYearToOpen(calendar));
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  if (!calendar || calendar.years.length === 0) {
    return (
      <section className="ds-panel" aria-label={t('acct.periods')}>
        <div className="ds-panel__body">
          <EmptyState
            title={t('acct.noCalendarTitle')}
            body={t('acct.noCalendarBody')}
          />
        </div>
      </section>
    );
  }

  return (
    <>
      <section className="ds-panel" aria-label={t('acct.periods')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('acct.periods')}</h2>
            <p className="ds-caption">
              {t('acct.yearStarts', {
                month: monthName(calendar.fiscal_year_start_month, locale),
              })}
            </p>
          </div>
          {mayClose && (
            <button
              className="ds-btn ds-btn--quiet"
              disabled={busy}
              onClick={() => void openNextYear()}
            >
              {t('acct.openYear', { year: String(nextYearToOpen(calendar)) })}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {closed && (
            <div className="acct__yearresult" role="status">
              <p className="acct__reconciled">
                {t('acct.yearClosed', {
                  year: String(closed.fiscal_year),
                  profit: money(closed.profit_to_retained_earnings, {
                    currency: closed.currency,
                  }),
                })}
              </p>
            </div>
          )}

          {calendar.years.map((y) => (
            <YearStrip
              key={y.fiscal_year}
              companyId={companyId}
              year={y.fiscal_year}
              periods={y.periods}
              closableID={mayClose ? closable : null}
              mayClose={mayClose}
              onChanged={onChanged}
              // Offered only when every month of the year is closed and none is
              // locked. A button that is usually refused is a button people
              // stop believing, and this one is refused for four different
              // reasons.
              onCloseYear={
                mayReopen && readyToClose(y.periods)
                  ? () => setClosingYear(y.fiscal_year)
                  : undefined
              }
            />
          ))}

          {closingYear !== null && (
            <ConfirmYearEnd
              year={closingYear}
              busy={busy}
              onCancel={() => setClosingYear(null)}
              onConfirm={() => void closeYear(closingYear)}
            />
          )}
        </div>
      </section>
    </>
  );
}

function YearStrip({
  companyId,
  year,
  periods,
  closableID,
  mayClose,
  onChanged,
  onCloseYear,
}: {
  companyId: string;
  year: number;
  periods: Period[];
  closableID: string | null;
  mayClose: boolean;
  onChanged: () => void;
  onCloseYear?: () => void;
}) {
  const t = useT();
  return (
    <section className="acct__year" aria-label={String(year)}>
      <div className="acct__yearhead">
        <h3 className="ds-h3 acct__yearname">{year}</h3>
        {onCloseYear && (
          <button className="ds-btn ds-btn--quiet" onClick={onCloseYear}>
            {t('acct.closeYear')}
          </button>
        )}
      </div>
      <ol className="acct__months">
        {periods.map((p) => (
          <MonthTile
            key={p.id}
            companyId={companyId}
            period={p}
            closable={mayClose && p.id === closableID}
            onChanged={onChanged}
          />
        ))}
      </ol>
      {periods.length === 0 && <p className="ds-caption">{t('acct.noMonths')}</p>}
    </section>
  );
}

function MonthTile({
  companyId,
  period,
  closable,
  onChanged,
}: {
  companyId: string;
  period: Period;
  // Whether THIS month is the one that may be closed right now. Already
  // permission-aware — the strip works it out once — so the tile does not take
  // `mayClose` as well and cannot disagree with it.
  closable: boolean;
  onChanged: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [reopening, setReopening] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const mayReopen = can('accounting.reopen_period') && reopenable(period);

  async function act(what: 'close' | 'reopen') {
    setBusy(true);
    setFailure(null);
    try {
      if (what === 'close') {
        await closePeriod(client, companyId, period.id);
      } else {
        await reopenPeriod(client, companyId, period.id, reason);
        setReopening(false);
        setReason('');
      }
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li
      className={`acct__month acct__month--${period.state}${
        period.is_current ? ' acct__month--now' : ''
      }`}
    >
      <span className="acct__monthname">
        {monthName(period.period_no, locale)}
      </span>
      <span className="ds-caption">
        {t('acct.entryCount', { count: String(period.entries) })}
      </span>

      <span
        className={`ds-badge ds-badge--${
          period.state === 'open'
            ? 'warning'
            : period.state === 'closed'
              ? 'success'
              : 'muted'
        }`}
      >
        {t(
          period.state === 'open'
            ? 'acct.open'
            : period.state === 'closed'
              ? 'acct.closed'
              : 'acct.locked',
        )}
      </span>

      {period.closed_by && (
        <span className="ds-caption">
          {t('acct.closedBy', { who: period.closed_by })}
        </span>
      )}
      {period.reopen_reason && (
        <span className="ds-caption acct__reopened">
          {t('acct.reopenedBecause', { reason: period.reopen_reason })}
        </span>
      )}

      {failure && (
        <span className="form__error" role="alert">
          {failure}
        </span>
      )}

      {closable && (
        <button
          className="ds-btn ds-btn--quiet"
          disabled={busy}
          onClick={() => void act('close')}
        >
          {t('acct.close')}
        </button>
      )}

      {mayReopen && !reopening && (
        <button className="ds-btn ds-btn--quiet" onClick={() => setReopening(true)}>
          {t('acct.reopen')}
        </button>
      )}

      {reopening && (
        <div className="acct__reopenbox">
          <TextInput
            id={`reopen-${period.id}`}
            value={reason}
            onChange={setReason}
            placeholder={t('acct.reopenReasonHint')}
          />
          <div className="acct__reopenactions">
            <button
              className="ds-btn ds-btn--primary"
              // Disabled until there is a real sentence, so the refusal comes
              // from the screen rather than from a round trip. The database
              // refuses anything shorter than this too.
              disabled={busy || reason.trim().length < MIN_REASON}
              onClick={() => void act('reopen')}
            >
              {t('acct.reopen')}
            </button>
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => {
                setReopening(false);
                setReason('');
              }}
            >
              {t('action.cancel')}
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

/** Whether a year is ready for the year-end routine.
 *
 *  Every month closed, and none locked — locked means it has already been
 *  through. The server checks all of this again and refuses with a sentence;
 *  this is only about whether to offer the button, because a control that is
 *  usually refused is one people stop pressing. */
function readyToClose(periods: Period[]): boolean {
  return (
    periods.length > 0 &&
    periods.every((p) => p.state === 'closed')
  );
}

/** The confirmation.
 *
 *  Closing a year is the one act in this product that cannot be undone by
 *  anybody: it locks every month beyond reopening. So it is confirmed, and the
 *  confirmation says what will happen rather than asking "are you sure" — which
 *  is a question nobody has ever answered no to. */
function ConfirmYearEnd({
  year,
  busy,
  onCancel,
  onConfirm,
}: {
  year: number;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT();
  return (
    <div className="acct__confirm" role="alertdialog" aria-label={t('acct.closeYear')}>
      <p className="acct__confirmbody">
        {t('acct.closeYearWarning', { year: String(year) })}
      </p>
      <div className="acct__reopenactions">
        <button
          className="ds-btn ds-btn--primary"
          disabled={busy}
          onClick={onConfirm}
        >
          {t('acct.closeYearConfirm', { year: String(year) })}
        </button>
        <button className="ds-btn ds-btn--quiet" onClick={onCancel}>
          {t('action.cancel')}
        </button>
      </div>
    </div>
  );
}
