// The cash drawer, UI spec §7.
//
// Four things happen here across a day: the float is counted in, cash moves to
// the safe, a supervisor takes a reading, and at close the cashier counts the
// drawer and is told Short, Over or Exact.
//
// # Nothing on this screen decides anything
//
// The expected drawer, the variance, whether a second Z is allowed and whether
// this login may see the expected figure at all are the server's answers. This
// asks and renders. That is not deference for its own sake: a till that worked
// out its own expected cash would be the party being measured doing the
// measuring, which is the one arrangement a drawer count cannot tolerate.
//
// # The network is required, and the screen says so
//
// Every other write at the counter survives a dead connection by queueing.
// These do not. The session number comes from a database counter and a partial
// unique index is what makes two open sessions on one till impossible; a queued
// open would claim a second session against a stale view of the drawer. So an
// offline till is told plainly that the shift cannot be opened or closed yet —
// and, importantly, that the sales it has already queued are safe.

import { useCallback, useEffect, useState } from 'react';
import type { Translate } from '@rawsyst/shared/i18n/strings';
import { useT } from '@rawsyst/shared/i18n/locale';

import { Offline, RequestFailed } from '@rawsyst/shared/api/client';
import { useAuth } from '@rawsyst/shared/auth/session';
import { listCompanies } from '@rawsyst/shared/api/companies';
import {
  closeShift,
  currentShift,
  openShift,
  peekShift,
  recordCashMovement,
  shiftXReport,
  type CashMovementReason,
  type ShiftReport,
  type ShiftSession,
} from '@rawsyst/shared/api/shift';
import { money } from '@rawsyst/shared/ui/format';
import { Field, FormError, TextInput } from '@rawsyst/shared/ui/Form';
import {
  denominationsFor,
  expectedIsWithheld,
  MOVEMENT_REASONS,
  openedAtTime,
  reportVerdict,
  signFor,
  tallyTotal,
  validateAmount,
  validateMovement,
  verdict,
  type Denomination,
  type Tally,
  type Verdict,
} from './shift';

type Loading =
  | { state: 'loading' }
  | { state: 'none' }
  | { state: 'open'; session: ShiftSession }
  | { state: 'failed'; message: string; offline: boolean };

export function ShiftScreen({ onClosed }: { onClosed?: () => void } = {}) {
  const t = useT();
  const { client, can } = useAuth();

  const [shift, setShift] = useState<Loading>({ state: 'loading' });
  const [currency, setCurrency] = useState<string | null>(null);
  const [closed, setClosed] = useState<ShiftReport | null>(null);

  const load = useCallback(async () => {
    setShift({ state: 'loading' });
    try {
      const session = await currentShift(client);
      setShift(session ? { state: 'open', session } : { state: 'none' });
    } catch (err) {
      setShift({
        state: 'failed',
        message: explain(err, t),
        offline: err instanceof Offline,
      });
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  // The currency decides which denominations the count pad offers. Read from
  // the company rather than assumed: this product serves three currencies and a
  // pad of Saudi notes in a Dhaka shop is worse than no pad at all. Failing to
  // get it is not an error — the count falls back to typing the total.
  useEffect(() => {
    let cancelled = false;
    listCompanies(client)
      .then((found) => {
        if (!cancelled) setCurrency(found[0]?.base_currency ?? null);
      })
      .catch(() => {
        if (!cancelled) setCurrency(null);
      });
    return () => {
      cancelled = true;
    };
  }, [client]);

  if (closed) {
    return (
      <ZReport
        report={closed}
        currency={currency}
        onDone={() => {
          setClosed(null);
          void load();
          onClosed?.();
        }}
      />
    );
  }

  if (shift.state === 'loading') {
    return (
      <main className="shift" aria-busy="true">
        <div className="ds-panel">
          <div className="ds-panel__body">
            <div className="ds-skeleton" style={{ blockSize: 140 }} />
          </div>
        </div>
      </main>
    );
  }

  if (shift.state === 'failed') {
    return (
      <main className="shift">
        <div className="ds-panel">
          <div className="ds-state">
            <p className="ds-state__title">
              {shift.offline ? 'This till cannot reach the server' : 'The shift could not be read'}
            </p>
            <p className="ds-state__body">{shift.message}</p>
            <button className="ds-btn ds-btn--secondary" onClick={() => void load()}>
              {t('common.tryAgain')}
            </button>
          </div>
        </div>
      </main>
    );
  }

  if (shift.state === 'none') {
    return <OpenShift currency={currency} onOpened={() => void load()} />;
  }

  return (
    <CurrentShift
      session={shift.session}
      currency={currency}
      mayTakeXReport={can('report.view')}
      onChanged={() => void load()}
      onClosed={setClosed}
    />
  );
}

// --- opening --------------------------------------------------------------

function OpenShift({
  currency,
  onOpened,
}: {
  currency: string | null;
  onOpened: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const pad = denominationsFor(currency);

  const [tally, setTally] = useState<Tally>({});
  const [typed, setTyped] = useState('');
  const [counting, setCounting] = useState(pad !== null);
  const [blind, setBlind] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const total = counting && pad ? tallyTotal(tally, pad) : typed;

  async function submit() {
    const invalid = validateAmount(total, t('shift.openingFloat'), t);
    if (invalid) {
      setFieldError(invalid);
      return;
    }
    setFieldError(null);
    setError(null);
    setBusy(true);
    try {
      await openShift(client, { opening_float: total, blind_close: blind });
      onOpened();
    } catch (err) {
      setError(explain(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="shift">
      <div className="ds-panel shift__panel">
        <div className="ds-panel__head">
          <h1 className="ds-h1">{t('shift.openTill')}</h1>
        </div>
        <div className="ds-panel__body">
          <p className="ds-body-sm ds-muted shift__lede">
            {t('shift.openTillLede')}
          </p>

          {pad && (
            <CountToggle counting={counting} onChange={setCounting} />
          )}

          {counting && pad ? (
            <DenominationPad
              pad={pad}
              tally={tally}
              onChange={setTally}
              currency={currency}
              total={total}
            />
          ) : (
            <Field
              label={t('shift.openingFloat')}
              htmlFor="opening-float"
              required
              error={fieldError ?? undefined}
              hint={currency ? t('shift.inCurrency', { currency }) : undefined}
            >
              <TextInput
                id="opening-float"
                value={typed}
                onChange={setTyped}
                inputMode="decimal"
                placeholder="200.00"
                error={fieldError ?? undefined}
                autoFocus
              />
            </Field>
          )}

          {counting && pad && fieldError && (
            <p className="field__error" role="alert">
              {fieldError}
            </p>
          )}

          {/* Blind close is a shop's arrangement, taken per session because a
              trainee and a supervisor may run the same till on the same day.
              Default on: it is the control, and defaulting a control off makes
              it decorative. */}
          <label className="shift__check">
            <input
              type="checkbox"
              checked={blind}
              onChange={(e) => setBlind(e.target.checked)}
            />
            <span>
              <strong>{t('shift.blindClose')}</strong>
              <span className="ds-caption">{t('shift.blindCloseHint')}</span>
            </span>
          </label>

          <FormError message={error} />

          <button
            className="ds-btn ds-btn--primary shift__go"
            onClick={() => void submit()}
            disabled={busy}
          >
            {busy ? t('shift.opening') : t('shift.openTill')}
          </button>
        </div>
      </div>
    </main>
  );
}

// --- the open session -----------------------------------------------------

function CurrentShift({
  session,
  currency,
  mayTakeXReport,
  onChanged,
  onClosed,
}: {
  session: ShiftSession;
  currency: string | null;
  mayTakeXReport: boolean;
  onChanged: () => void;
  onClosed: (report: ShiftReport) => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [report, setReport] = useState<ShiftReport | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [panel, setPanel] = useState<'none' | 'move' | 'close'>('none');
  const [xray, setXray] = useState<ShiftReport | null>(null);

  const refresh = useCallback(async () => {
    try {
      setReport(await peekShift(client, session.id));
      setProblem(null);
    } catch (err) {
      setProblem(explain(err, t));
    }
  }, [client, session.id]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <main className="shift">
      <div className="ds-panel shift__panel">
        <div className="ds-panel__head">
          <h1 className="ds-h1">Shift {session.session_no}</h1>
          <span className="ds-badge ds-badge--success">
            {t('shift.openSince', { time: openedAtTime(session.opened_at) })}
          </span>
        </div>

        <div className="ds-panel__body">
          {problem && <FormError message={problem} />}

          {report ? (
            <Takings report={report} currency={currency} />
          ) : (
            <div className="ds-skeleton" style={{ blockSize: 120 }} />
          )}

          {report && expectedIsWithheld(report) && (
            <p className="shift__blind ds-body-sm" role="note">
              {t('shift.closesBlind')}
            </p>
          )}

          <div className="shift__actions">
            <button
              className="ds-btn ds-btn--secondary"
              onClick={() => setPanel(panel === 'move' ? 'none' : 'move')}
            >
              {t('shift.moveCash')}
            </button>

            {/* Shown only to a login holding report.view. Cosmetic: the server
                refuses the route outright to anyone else, which is what keeps
                a blind close blind. */}
            {mayTakeXReport && (
              <button
                className="ds-btn ds-btn--secondary"
                onClick={() => {
                  shiftXReport(client, session.id)
                    .then(setXray)
                    .catch((err) => setProblem(explain(err, t)));
                }}
              >
                X-report
              </button>
            )}

            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setPanel(panel === 'close' ? 'none' : 'close')}
            >
              {t('shift.closeTheTill')}
            </button>
          </div>

          {xray && (
            <XReport report={xray} currency={currency} onDismiss={() => setXray(null)} />
          )}

          {panel === 'move' && (
            <MoveCash
              sessionId={session.id}
              currency={currency}
              onDone={() => {
                setPanel('none');
                void refresh();
                onChanged();
              }}
              onCancel={() => setPanel('none')}
            />
          )}

          {panel === 'close' && report && (
            <CloseShift
              sessionId={session.id}
              report={report}
              currency={currency}
              onClosed={onClosed}
              onCancel={() => setPanel('none')}
            />
          )}
        </div>
      </div>
    </main>
  );
}

/** The shift so far.
 *
 *  On a blind-close till the server sends no takings figures at all, and this
 *  renders what it was sent rather than filling the gaps with zeroes. The
 *  earlier version of this panel listed "Opening float", "Cash takings" and
 *  "Cash moved" one under the other, under a comment saying the expected drawer
 *  was deliberately withheld — and those three numbers added together ARE the
 *  expected drawer. A blind close that publishes the addends is not one. */
function Takings({ report, currency }: { report: ShiftReport; currency: string | null }) {
  const t = useT();
  const rows: Array<[string, string]> = [
    [t('shift.openingFloat'), report.opening_float],
    ...(report.cash_takings === undefined
      ? []
      : ([[t('shift.cashTakings'), report.cash_takings]] as Array<
          [string, string]
        >)),
    ...(report.non_cash_takings === undefined
      ? []
      : ([[t('shift.cardAndOther'), report.non_cash_takings]] as Array<
          [string, string]
        >)),
    ...(report.cash_movements === undefined
      ? []
      : ([[t('shift.cashMoved'), report.cash_movements]] as Array<
          [string, string]
        >)),
    [t('shift.refunds'), report.refund_total],
  ];

  return (
    <dl className="shift__figures">
      <div className="shift__figure">
        <dt className="ds-caption">{t('shift.sales')}</dt>
        <dd className="num">{report.invoice_count}</dd>
      </div>
      {rows.map(([label, value]) => (
        <div className="shift__figure" key={label}>
          <dt className="ds-caption">{label}</dt>
          <dd className="num">{money(value, { currency: currency ?? undefined })}</dd>
        </div>
      ))}
    </dl>
  );
}

// --- the supervisor's reading --------------------------------------------

function XReport({
  report,
  currency,
  onDismiss,
}: {
  report: ShiftReport;
  currency: string | null;
  onDismiss: () => void;
}) {
  const t = useT();
  return (
    <section className="shift__xray" aria-label="X report">
      <div className="shift__xrayhead">
        <h2 className="ds-h3">X-report — shift {report.session_no}</h2>
        <button className="ds-btn ds-btn--quiet" onClick={onDismiss}>
          Hide
        </button>
      </div>
      <p className="ds-caption">{t('shift.readingNotAClose')}</p>
      <Takings report={report} currency={currency} />
      <p className="shift__expected">
        <span className="ds-caption">{t('shift.expectedInDrawer')}</span>
        <strong className="num">
          {money(report.expected_cash, { currency: currency ?? undefined })}
        </strong>
      </p>
    </section>
  );
}

// --- cash in and out ------------------------------------------------------

function MoveCash({
  sessionId,
  currency,
  onDone,
  onCancel,
}: {
  sessionId: string;
  currency: string | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const [reason, setReason] = useState<CashMovementReason>('safe_drop');
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [errors, setErrors] = useState<{ amount?: string; note?: string }>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const chosen = MOVEMENT_REASONS.find((r) => r.value === reason);
  const signed = amount.trim() ? signFor(reason, amount) : '';
  const outward = signed.startsWith('-');

  async function submit() {
    const found = validateMovement(amount, note, t);
    setErrors(found);
    if (found.amount || found.note) return;

    setFailure(null);
    setBusy(true);
    try {
      // The sign comes from the reason, not from what the cashier typed. They
      // type "100" and mean a hundred left the drawer; asking for the sign is
      // how a shift ends up two hundred out.
      await recordCashMovement(client, sessionId, {
        amount: signFor(reason, amount),
        reason,
        note: note.trim(),
      });
      onDone();
    } catch (err) {
      setFailure(explain(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="shift__form" aria-label={t('shift.moveCashLabel')}>
      <h2 className="ds-h3">{t('shift.moveCash')}</h2>

      <Field label={t('shift.why')} htmlFor="move-reason" required>
        <select
          id="move-reason"
          className="field__input"
          value={reason}
          onChange={(e) => setReason(e.target.value as CashMovementReason)}
        >
          {MOVEMENT_REASONS.map((r) => (
            <option key={r.value} value={r.value}>
              {t(r.label)}
            </option>
          ))}
        </select>
      </Field>
      {chosen && <p className="ds-caption shift__hint">{t(chosen.hint)}</p>}

      <Field
        label={t('shift.howMuch')}
        htmlFor="move-amount"
        required
        error={errors.amount}
        hint={currency ? t('shift.inCurrency', { currency }) : undefined}
      >
        <TextInput
          id="move-amount"
          value={amount}
          onChange={setAmount}
          inputMode="decimal"
          placeholder="100.00"
          error={errors.amount}
          autoFocus
        />
      </Field>

      {/* Says which way the money is going before it goes. A cashier who reads
          "leaves the drawer" and meant the opposite catches it here rather
          than at close. */}
      {signed && (
        <p className="shift__direction ds-body-sm" role="status">
          {outward ? t('shift.leavesDrawer') : t('shift.goesIntoDrawer')}{' '}
          <strong className="num">
            {money(signed.replace('-', ''), { currency: currency ?? undefined })}
          </strong>
        </p>
      )}

      <Field
        label={t('shift.whatFor')}
        htmlFor="move-note"
        required
        error={errors.note}
      >
        <TextInput
          id="move-note"
          value={note}
          onChange={setNote}
          placeholder={t('shift.notePlaceholderMove')}
          error={errors.note}
        />
      </Field>

      <FormError message={failure} />

      <div className="shift__formactions">
        <button className="ds-btn ds-btn--quiet" onClick={onCancel} disabled={busy}>
          {t('action.cancel')}
        </button>
        <button
          className="ds-btn ds-btn--primary"
          onClick={() => void submit()}
          disabled={busy}
        >
          {busy ? t('till.recording') : t('shift.recordIt')}
        </button>
      </div>
    </section>
  );
}

// --- closing --------------------------------------------------------------

function CloseShift({
  sessionId,
  report,
  currency,
  onClosed,
  onCancel,
}: {
  sessionId: string;
  report: ShiftReport;
  currency: string | null;
  onClosed: (report: ShiftReport) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const pad = denominationsFor(currency);

  const [tally, setTally] = useState<Tally>({});
  const [typed, setTyped] = useState('');
  const [counting, setCounting] = useState(pad !== null);
  const [note, setNote] = useState('');
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  const counted = counting && pad ? tallyTotal(tally, pad) : typed;
  const blind = expectedIsWithheld(report);

  // On a till that does not close blind the cashier may see where they stand
  // as they count. On one that does, this is `unknown` and nothing is shown —
  // which is the entire point of the setting.
  const running = verdict(report.expected_cash, counted);

  async function submit() {
    const invalid = validateAmount(counted, t('shift.countedCash'), t);
    if (invalid) {
      setFieldError(invalid);
      setConfirming(false);
      return;
    }
    setFieldError(null);
    setFailure(null);
    setBusy(true);
    try {
      onClosed(await closeShift(client, sessionId, { counted_cash: counted, note: note.trim() }));
    } catch (err) {
      setFailure(explain(err, t));
      setConfirming(false);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="shift__form" aria-label={t('shift.closeTheTill')}>
      <h2 className="ds-h3">{t('shift.countDrawer')}</h2>
      <p className="ds-body-sm ds-muted">
        {blind
          ? t('shift.countPhysicallyBlind')
          : t('shift.countPhysically')}
      </p>

      {pad && <CountToggle counting={counting} onChange={setCounting} />}

      {counting && pad ? (
        <DenominationPad
          pad={pad}
          tally={tally}
          onChange={setTally}
          currency={currency}
          total={counted}
        />
      ) : (
        <Field
          label={t('shift.countedCash')}
          htmlFor="counted-cash"
          required
          error={fieldError ?? undefined}
          hint={currency ? t('shift.inCurrency', { currency }) : undefined}
        >
          <TextInput
            id="counted-cash"
            value={typed}
            onChange={setTyped}
            inputMode="decimal"
            placeholder="0.00"
            error={fieldError ?? undefined}
            autoFocus
          />
        </Field>
      )}

      {counting && pad && fieldError && (
        <p className="field__error" role="alert">
          {fieldError}
        </p>
      )}

      {running.kind !== 'unknown' && (
        <p className="shift__running ds-body-sm" role="status" aria-live="polite">
          {t('shift.againstExpected')} <strong>{running.word}</strong>{' '}
          <span className="num">
            {money(running.amount, { currency: currency ?? undefined })}
          </span>
        </p>
      )}

      <Field label={t('shift.note')} htmlFor="close-note" error={undefined}>
        <TextInput
          id="close-note"
          value={note}
          onChange={setNote}
          placeholder={t('shift.notePlaceholder')}
        />
      </Field>

      <FormError message={failure} />

      {/* A Z report may happen exactly once, so it is confirmed. Not a modal:
          an inline step keeps the counted figure on screen while the cashier
          decides, and the figure is what they are confirming. */}
      {confirming ? (
        <div className="shift__confirm" role="alertdialog" aria-label={t('shift.confirmTheClose')}>
          <p className="ds-body-sm">
            {t('shift.confirmClose', {
              counted: money(counted, { currency: currency ?? undefined }),
            })}
          </p>
          <div className="shift__formactions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setConfirming(false)}
              disabled={busy}
            >
              {t('shift.keepCounting')}
            </button>
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => void submit()}
              disabled={busy}
            >
              {busy ? t('shift.closing') : t('shift.closeTheShift')}
            </button>
          </div>
        </div>
      ) : (
        <div className="shift__formactions">
          <button className="ds-btn ds-btn--quiet" onClick={onCancel} disabled={busy}>
            {t('action.cancel')}
          </button>
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setConfirming(true)}
            disabled={busy}
          >
            {t('shift.countIsComplete')}
          </button>
        </div>
      )}
    </section>
  );
}

/** The Z report. UI spec §7: expected against actual, and the word, in large
 *  type. */
function ZReport({
  report,
  currency,
  onDone,
}: {
  report: ShiftReport;
  currency: string | null;
  onDone: () => void;
}) {
  const t = useT();
  const result = reportVerdict(report);

  return (
    <main className="shift">
      <div className="ds-panel shift__panel">
        <div className="ds-panel__head">
          <h1 className="ds-h1">Z-report — shift {report.session_no}</h1>
          <span className="ds-badge ds-badge--neutral">Closed</span>
        </div>

        <div className="ds-panel__body">
          <Outcome verdict={result} currency={currency} />

          <dl className="shift__figures shift__figures--z">
            <div className="shift__figure">
              <dt className="ds-caption">{t('shift.expected')}</dt>
              <dd className="num">
                {money(report.expected_cash, { currency: currency ?? undefined })}
              </dd>
            </div>
            <div className="shift__figure">
              <dt className="ds-caption">{t('shift.counted')}</dt>
              <dd className="num">
                {money(report.counted_cash, { currency: currency ?? undefined })}
              </dd>
            </div>
          </dl>

          <Takings report={report} currency={currency} />

          {/* Blueprint C8: a variance is information, not noise. It is not
              absorbed and it is not hidden. */}
          {result.kind !== 'exact' && result.kind !== 'unknown' && (
            <p className="shift__variance ds-body-sm">
              {t('shift.differenceRecorded')}
            </p>
          )}

          <button className="ds-btn ds-btn--primary shift__go" onClick={onDone}>
            {t('common.done')}
          </button>
        </div>
      </div>
    </main>
  );
}

/** Short, Over or Exact, in the largest type on the screen. */
function Outcome({ verdict: v, currency }: { verdict: Verdict; currency: string | null }) {
  const t = useT();
  if (v.kind === 'unknown') {
    return (
      <p className="shift__outcome shift__outcome--unknown" role="status">
        <span className="ds-caption">{t('shift.drawerNotReckoned')}</span>
      </p>
    );
  }

  return (
    <p
      className={`shift__outcome shift__outcome--${v.kind}`}
      role="status"
      aria-live="polite"
    >
      {/* The word and the amount together, and colour is never the only
          signal — the word says it for anyone who cannot see the difference. */}
      <span className="ds-display">{v.word}</span>
      {v.kind !== 'exact' && (
        <span className="ds-h2 num">
          {money(v.amount, { currency: currency ?? undefined })}
        </span>
      )}
    </p>
  );
}

// --- the count pad --------------------------------------------------------

function CountToggle({
  counting,
  onChange,
}: {
  counting: boolean;
  onChange: (v: boolean) => void;
}) {
  const t = useT();
  return (
    <div className="shift__toggle" role="group" aria-label={t('shift.howToCount')}>
      <button
        className={`ds-btn ds-btn--quiet${counting ? ' shift__toggle--on' : ''}`}
        aria-pressed={counting}
        onClick={() => onChange(true)}
      >
        {t('shift.countDenominations')}
      </button>
      <button
        className={`ds-btn ds-btn--quiet${!counting ? ' shift__toggle--on' : ''}`}
        aria-pressed={!counting}
        onClick={() => onChange(false)}
      >
        {t('shift.enterATotal')}
      </button>
    </div>
  );
}

function DenominationPad({
  pad,
  tally,
  onChange,
  currency,
  total,
}: {
  pad: Denomination[];
  tally: Tally;
  onChange: (t: Tally) => void;
  currency: string | null;
  total: string;
}) {
  const t = useT();
  return (
    <div className="denom">
      <div className="denom__grid">
        {pad.map((d, index) => {
          const count = tally[d.value];
          const line = count && count > 0 ? tallyTotal({ [d.value]: count }, [d]) : null;
          return (
            <label className="denom__row" key={d.value}>
              <span className={`denom__face denom__face--${d.kind}`}>{d.label}</span>
              <input
                className="denom__count"
                type="number"
                min={0}
                step={1}
                inputMode="numeric"
                aria-label={t('shift.howMany', { denomination: d.label })}
                value={count === undefined || Number.isNaN(count) ? '' : String(count)}
                autoFocus={index === 0}
                onChange={(e) => {
                  const next = { ...tally };
                  const raw = e.target.value.trim();
                  if (raw === '') delete next[d.value];
                  else next[d.value] = Math.max(0, Math.floor(Number(raw)));
                  onChange(next);
                }}
              />
              <span className="denom__line num">
                {line ? money(line, { currency: currency ?? undefined }) : ''}
              </span>
            </label>
          );
        })}
      </div>

      <p className="denom__total" role="status" aria-live="polite">
        <span className="ds-caption">{t('shift.counted')}</span>
        <strong className="ds-h2 num">
          {money(total, { currency: currency ?? undefined })}
        </strong>
      </p>
    </div>
  );
}

// --- failures -------------------------------------------------------------

/** Turns a failure into something a cashier can act on. */
function explain(err: unknown, t: Translate): string {
  if (err instanceof Offline) {
    // Named as a limitation with a reason, and with the reassurance that
    // matters most at this moment: the sales already taken are not lost.
    return t('shift.offlineFull');
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('shift.notAllowedFull');
    if (err.status === 404) return t('shift.notFoundOnTill');
    // 409 and 400 carry the server's own sentence, which is written for the
    // person reading it — "this till already has an open session (number 12)"
    // tells a cashier what to do in a way nothing here could improve on.
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}
