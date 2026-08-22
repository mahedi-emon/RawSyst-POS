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
        message: explain(err),
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
              Try again
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
    const invalid = validateAmount(total, 'Opening float');
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
      setError(explain(err));
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
            Count the float into the drawer and enter what is there. This is the
            baseline every figure at close is measured against, so it is
            declared rather than assumed.
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
              label="Opening float"
              htmlFor="opening-float"
              required
              error={fieldError ?? undefined}
              hint={currency ? `In ${currency}` : undefined}
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
              <span className="ds-caption">
                The expected total is hidden until the drawer has been counted,
                so the count is a real one.
              </span>
            </span>
          </label>

          <FormError message={error} />

          <button
            className="ds-btn ds-btn--primary shift__go"
            onClick={() => void submit()}
            disabled={busy}
          >
            {busy ? 'Opening…' : 'Open the till'}
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
  const [report, setReport] = useState<ShiftReport | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [panel, setPanel] = useState<'none' | 'move' | 'close'>('none');
  const [xray, setXray] = useState<ShiftReport | null>(null);

  const refresh = useCallback(async () => {
    try {
      setReport(await peekShift(client, session.id));
      setProblem(null);
    } catch (err) {
      setProblem(explain(err));
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
            Open since {openedAtTime(session.opened_at)}
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
              This till closes blind. The expected total is not shown until the
              drawer has been counted — which is what makes the count worth
              taking.
            </p>
          )}

          <div className="shift__actions">
            <button
              className="ds-btn ds-btn--secondary"
              onClick={() => setPanel(panel === 'move' ? 'none' : 'move')}
            >
              Move cash
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
                    .catch((err) => setProblem(explain(err)));
                }}
              >
                X-report
              </button>
            )}

            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setPanel(panel === 'close' ? 'none' : 'close')}
            >
              Close the till
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

/** The shift so far. Deliberately does not include the expected drawer — that
 *  belongs to the X report and to the close, and putting it here would hand it
 *  to a cashier on every screen refresh. */
function Takings({ report, currency }: { report: ShiftReport; currency: string | null }) {
  const t = useT();
  const rows: Array<[string, string]> = [
    ['Opening float', report.opening_float],
    ['Cash takings', report.cash_takings],
    ['Card and other', report.non_cash_takings],
    ['Cash moved', report.cash_movements],
    ['Refunds', report.refund_total],
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
      <p className="ds-caption">
        A reading, not a close. Nothing changes and it may be taken again.
      </p>
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
    const found = validateMovement(amount, note);
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
      setFailure(explain(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="shift__form" aria-label="Move cash">
      <h2 className="ds-h3">{t('shift.moveCash')}</h2>

      <Field label="Why" htmlFor="move-reason" required>
        <select
          id="move-reason"
          className="field__input"
          value={reason}
          onChange={(e) => setReason(e.target.value as CashMovementReason)}
        >
          {MOVEMENT_REASONS.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      </Field>
      {chosen && <p className="ds-caption shift__hint">{chosen.hint}</p>}

      <Field
        label="How much"
        htmlFor="move-amount"
        required
        error={errors.amount}
        hint={currency ? `In ${currency}` : undefined}
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
          {outward ? 'Leaves the drawer: ' : 'Goes into the drawer: '}
          <strong className="num">
            {money(signed.replace('-', ''), { currency: currency ?? undefined })}
          </strong>
        </p>
      )}

      <Field label="What for" htmlFor="move-note" required error={errors.note}>
        <TextInput
          id="move-note"
          value={note}
          onChange={setNote}
          placeholder="Midday drop to the safe"
          error={errors.note}
        />
      </Field>

      <FormError message={failure} />

      <div className="shift__formactions">
        <button className="ds-btn ds-btn--quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button
          className="ds-btn ds-btn--primary"
          onClick={() => void submit()}
          disabled={busy}
        >
          {busy ? 'Recording…' : 'Record it'}
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
    const invalid = validateAmount(counted, 'Counted cash');
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
      setFailure(explain(err));
      setConfirming(false);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="shift__form" aria-label="Close the till">
      <h2 className="ds-h3">{t('shift.countDrawer')}</h2>
      <p className="ds-body-sm ds-muted">
        {blind
          ? 'Count what is physically in the drawer. The expected total is shown once you commit the count.'
          : 'Count what is physically in the drawer.'}
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
          label="Counted cash"
          htmlFor="counted-cash"
          required
          error={fieldError ?? undefined}
          hint={currency ? `In ${currency}` : undefined}
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
          Against expected: <strong>{running.word}</strong>{' '}
          <span className="num">
            {money(running.amount, { currency: currency ?? undefined })}
          </span>
        </p>
      )}

      <Field label="Note" htmlFor="close-note" error={undefined}>
        <TextInput
          id="close-note"
          value={note}
          onChange={setNote}
          placeholder="Anything worth recording about this shift"
        />
      </Field>

      <FormError message={failure} />

      {/* A Z report may happen exactly once, so it is confirmed. Not a modal:
          an inline step keeps the counted figure on screen while the cashier
          decides, and the figure is what they are confirming. */}
      {confirming ? (
        <div className="shift__confirm" role="alertdialog" aria-label="Confirm the close">
          <p className="ds-body-sm">
            Close shift with <strong className="num">
              {money(counted, { currency: currency ?? undefined })}
            </strong>{' '}
            counted? This can only be done once — the Z report is the till's
            declaration for the shift and cannot be taken again.
          </p>
          <div className="shift__formactions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setConfirming(false)}
              disabled={busy}
            >
              Keep counting
            </button>
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => void submit()}
              disabled={busy}
            >
              {busy ? 'Closing…' : 'Close the shift'}
            </button>
          </div>
        </div>
      ) : (
        <div className="shift__formactions">
          <button className="ds-btn ds-btn--quiet" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setConfirming(true)}
            disabled={busy}
          >
            Count is complete
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
              This difference is recorded against the shift and appears on the
              closing report. It is not written off here.
            </p>
          )}

          <button className="ds-btn ds-btn--primary shift__go" onClick={onDone}>
            Done
          </button>
        </div>
      </div>
    </main>
  );
}

/** Short, Over or Exact, in the largest type on the screen. */
function Outcome({ verdict: v, currency }: { verdict: Verdict; currency: string | null }) {
  if (v.kind === 'unknown') {
    return (
      <p className="shift__outcome shift__outcome--unknown" role="status">
        <span className="ds-caption">The drawer could not be reckoned</span>
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
        Count denominations
      </button>
      <button
        className={`ds-btn ds-btn--quiet${!counting ? ' shift__toggle--on' : ''}`}
        aria-pressed={!counting}
        onClick={() => onChange(false)}
      >
        Enter a total
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
                aria-label={`How many ${d.label}`}
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
function explain(err: unknown): string {
  if (err instanceof Offline) {
    // Named as a limitation with a reason, and with the reassurance that
    // matters most at this moment: the sales already taken are not lost.
    return (
      'A shift is opened and closed on the server, so this cannot be done ' +
      'until the connection is back. Sales already rung up are saved on this ' +
      'till and will be sent when it reconnects.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) {
      return (
        'This login is not allowed to do that. Opening and closing a till ' +
        'needs permission to take payments; the X-report needs permission to ' +
        'read reports.'
      );
    }
    if (err.status === 404) return 'That shift was not found on this till.';
    // 409 and 400 carry the server's own sentence, which is written for the
    // person reading it — "this till already has an open session (number 12)"
    // tells a cashier what to do in a way nothing here could improve on.
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}
