// Payroll, WPS, GOSI and end of service (blueprint C6).
//
// # Drawing a run and paying it are three separate presses
//
// Draw, approve, pay. A payroll that computed and paid in one action would put
// a month of salaries out of the bank on a mistyped period, and the approval
// step is where somebody who is not the person who ran it reads the total.
//
// # A missing GOSI figure is named, never zero
//
// If no verified social-insurance rate exists, the run computes wages — which
// are wages — and says out loud that the contribution is not in it. A payroll
// that silently left GOSI out is a payroll that files wrong, and the shop finds
// out from the authority rather than from this screen.

import { useCallback, useState } from 'react';

import {
  accrueEndOfService,
  approvePayroll,
  endOfService,
  listPayrollRuns,
  payPayroll,
  readPayrollRun,
  runPayroll,
  wageFile,
  type EOSB,
  type PayrollRun,
} from '../api/hr';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

export function PayrollPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayRun = can('payroll.run');
  const [open, setOpen] = useState<string | null>(null);
  const [drawing, setDrawing] = useState(false);
  const [failure] = useState<string | null>(null);

  const load = useCallback(
    () => listPayrollRuns(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <RunDetail
        companyId={companyId}
        runId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      {drawing && (
        <RunForm
          companyId={companyId}
          onCancel={() => setDrawing(false)}
          onDrawn={(id) => {
            setDrawing(false);
            reload();
            setOpen(id);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('hr.payroll')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('hr.payrollRuns')}</h2>
          {mayRun && !drawing && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setDrawing(true)}
            >
              {t('hr.drawPayroll')}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: PayrollRun[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('hr.noPayrollTitle')}
                  body={t('hr.noPayrollBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('hr.period')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('hr.gross')}
                      </th>
                      <th scope="col" className="num">
                        {t('hr.deductions')}
                      </th>
                      <th scope="col" className="num">
                        {t('hr.net')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((r) => (
                      <tr key={r.id}>
                        <td>
                          <span className="detail__strong">{r.period}</span>
                          <span className="ds-caption">{r.run_no}</span>
                          {r.gosi_unavailable && (
                            <span className="ds-badge ds-badge--warning">
                              {t('hr.gosiMissing')}
                            </span>
                          )}
                        </td>
                        <td>
                          <span className={`ds-badge ds-badge--${runBadge(r.status)}`}>
                            {t(`hr.runStatus.${r.status}` as Key)}
                          </span>
                          {r.pay_date && (
                            <span className="ds-caption">
                              {shortDate(r.pay_date, locale)}
                            </span>
                          )}
                        </td>
                        <td className="num">
                          {money(r.gross_total, { currency: r.currency })}
                        </td>
                        <td className="num">
                          {money(r.deduction_total, { currency: r.currency })}
                        </td>
                        <td className="num">
                          {money(r.net_total, { currency: r.currency })}
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(r.id)}
                          >
                            {t('action.view')}
                          </button>
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

      <EndOfServicePanel companyId={companyId} />
    </>
  );
}

function runBadge(status: string): string {
  switch (status) {
    case 'paid':
      return 'success';
    case 'approved':
      return 'info';
    case 'draft':
      return 'warning';
    default:
      return 'neutral';
  }
}

function RunDetail({
  companyId,
  runId,
  onBack,
}: {
  companyId: string;
  runId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const mayRun = can('payroll.run');

  const load = useCallback(
    () => readPayrollRun(client, companyId, runId),
    [client, companyId, runId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [paying, setPaying] = useState(false);
  const [file, setFile] = useState<{ filename: string; content: string } | null>(
    null,
  );

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(r: PayrollRun) => (
        <>
          <section className="ds-panel">
            <div className="ds-panel__head">
              <div>
                <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                  {t('action.back')}
                </button>
                <h2 className="ds-h3">{t('hr.payrollFor', { period: r.period })}</h2>
                <p className="ds-caption">
                  {r.run_no} · {t(`hr.runStatus.${r.status}` as Key)}
                </p>
              </div>
              <div className="hr__actions">
                {mayRun && r.status === 'draft' && (
                  <button
                    className="ds-btn ds-btn--primary"
                    disabled={busy}
                    onClick={() =>
                      void run(() => approvePayroll(client, companyId, r.id))
                    }
                  >
                    {t('hr.approveRun')}
                  </button>
                )}
                {mayRun && r.status === 'approved' && (
                  <>
                    <button
                      className="ds-btn ds-btn--quiet"
                      disabled={busy}
                      onClick={() =>
                        void run(async () => {
                          setFile(await wageFile(client, companyId, r.id));
                        })
                      }
                    >
                      {t('hr.wageFile')}
                    </button>
                    <button
                      className="ds-btn ds-btn--primary"
                      onClick={() => setPaying(true)}
                    >
                      {t('hr.payRun')}
                    </button>
                  </>
                )}
              </div>
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />

              {/* Named rather than silently zero. A payroll that quietly left
                  social insurance out is one the shop files wrong. */}
              {r.gosi_unavailable && (
                <p className="hr__warning" role="status">
                  {t('hr.gosiMissingWhy', {
                    reason: r.gosi_blocked_reason ?? '',
                  })}
                </p>
              )}

              <dl className="hr__totals">
                <div>
                  <dt>{t('hr.gross')}</dt>
                  <dd className="num">
                    {money(r.gross_total, { currency: r.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('hr.deductions')}</dt>
                  <dd className="num">
                    {money(r.deduction_total, { currency: r.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('hr.employerGosi')}</dt>
                  <dd className="num">
                    {money(r.employer_gosi, { currency: r.currency })}
                  </dd>
                </div>
                <div className="hr__totals-final">
                  <dt>{t('hr.net')}</dt>
                  <dd className="num">
                    {money(r.net_total, { currency: r.currency })}
                  </dd>
                </div>
              </dl>
            </div>
          </section>

          {paying && (
            <PayForm
              companyId={companyId}
              run={r}
              onCancel={() => setPaying(false)}
              onPaid={() => {
                setPaying(false);
                reload();
              }}
            />
          )}

          {file && (
            <WageFileSheet file={file} onClose={() => setFile(null)} />
          )}

          <section className="ds-panel" aria-label={t('hr.payslips')}>
            <div className="ds-panel__head">
              <h2 className="ds-h3">{t('hr.payslips')}</h2>
            </div>
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('hr.person')}</th>
                    <th scope="col" className="num">
                      {t('hr.basic')}
                    </th>
                    <th scope="col" className="num">
                      {t('hr.allowances')}
                    </th>
                    <th scope="col" className="num">
                      {t('hr.gross')}
                    </th>
                    <th scope="col" className="num">
                      {t('hr.deductions')}
                    </th>
                    <th scope="col" className="num">
                      {t('hr.net')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {(r.payslips ?? []).map((p) => (
                    <tr key={p.id}>
                      <td>{p.employee}</td>
                      <td className="num">
                        {money(p.basic, { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(allowances(p), { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(p.gross, { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(p.deductions ?? '0', { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(p.net, { currency: r.currency })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </RemoteBody>
  );
}

// allowances adds the three that are not basic, in minor units.
//
// Summed here rather than sent as a field because it is presentation: the
// payslip carries each one, and a screen that has room for four columns rather
// than six adds them for the reader.
function allowances(p: {
  housing: string;
  transport: string;
  other_allowance: string;
}): string {
  const cents = [p.housing, p.transport, p.other_allowance].reduce((sum, v) => {
    const [whole = '0', frac = ''] = (v || '0').trim().split('.');
    const n = BigInt(whole || '0') * 100n + BigInt((frac + '00').slice(0, 2) || '0');
    return sum + n;
  }, 0n);
  return `${cents / 100n}.${String(cents % 100n).padStart(2, '0')}`;
}

function RunForm({
  companyId,
  onCancel,
  onDrawn,
}: {
  companyId: string;
  onCancel: () => void;
  onDrawn: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [period, setPeriod] = useState(() => new Date().toISOString().slice(0, 7));
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const run = await runPayroll(client, companyId, { period, note });
      onDrawn(run.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel hr__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('hr.drawPayroll')}</h2>
        <p className="ds-caption">{t('hr.drawPayrollHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('hr.period')} htmlFor="run-period" required>
            <input
              id="run-period"
              type="month"
              className="field__input"
              value={period}
              onChange={(e) => setPeriod(e.target.value)}
            />
          </Field>
          <Field label={t('common.note')} htmlFor="run-note">
            <TextInput id="run-note" value={note} onChange={setNote} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('hr.drawPayroll')}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function PayForm({
  companyId,
  run,
  onCancel,
  onPaid,
}: {
  companyId: string;
  run: PayrollRun;
  onCancel: () => void;
  onPaid: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [date, setDate] = useState(() => new Date().toISOString().slice(0, 10));
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await payPayroll(client, companyId, run.id, { pay_date: date });
      onPaid();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel hr__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('hr.payRun')}</h2>
        <p className="ds-caption">
          {t('hr.payRunHint', {
            amount: money(run.net_total, { currency: run.currency }),
          })}
        </p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('hr.payDate')} htmlFor="pay-date" required>
            <input
              id="pay-date"
              type="date"
              className="field__input"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </Field>
        </div>
        <FormActions submitLabel={t('hr.payRun')} busy={busy} onCancel={onCancel} />
      </div>
    </form>
  );
}

// WageFileSheet shows the WPS file so a person can copy it to their bank.
//
// Shown rather than downloaded, because the browser's download is blocked in
// some of the environments this runs in and a file that silently did not save
// is worse than one somebody can select and copy.
function WageFileSheet({
  file,
  onClose,
}: {
  file: { filename: string; content: string };
  onClose: () => void;
}) {
  const t = useT();
  return (
    <section className="ds-panel hr__wagefile">
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('hr.wageFile')}</h2>
        <div className="hr__actions">
          <span className="ds-caption">{file.filename}</span>
          <button className="ds-btn ds-btn--quiet" onClick={onClose}>
            {t('action.close')}
          </button>
        </div>
      </div>
      <div className="ds-panel__body">
        <p className="ds-caption">{t('hr.wageFileHint')}</p>
        <textarea
          className="field__input hr__wagetext"
          readOnly
          value={file.content}
          rows={12}
        />
      </div>
    </section>
  );
}

// EndOfServicePanel is C6's entitlement, accrued rather than guessed.
function EndOfServicePanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayRun = can('payroll.run');

  const load = useCallback(
    () => endOfService(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [accrued, setAccrued] = useState<string | null>(null);

  async function accrue() {
    setBusy(true);
    setFailure(null);
    try {
      const done = await accrueEndOfService(client, companyId);
      setAccrued(done.accrued);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('hr.endOfService')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('hr.endOfService')}</h2>
          <p className="ds-caption">{t('hr.endOfServiceHint')}</p>
        </div>
        {mayRun && (
          <button
            className="ds-btn ds-btn--quiet"
            disabled={busy}
            onClick={() => void accrue()}
          >
            {t('hr.accrueEosb')}
          </button>
        )}
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
        {accrued && (
          <p className="ds-caption" role="status">
            {t('hr.eosbAccrued', { amount: accrued })}
          </p>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: EOSB[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('hr.noEosbTitle')}
                body={t('hr.noEosbBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('hr.person')}</th>
                    <th scope="col" className="num">
                      {t('hr.monthsOfService')}
                    </th>
                    <th scope="col" className="num">
                      {t('hr.owedOnLeaving')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((e) => (
                    <tr key={e.employee_id}>
                      <td>{e.employee}</td>
                      <td className="num">{e.months_of_service}</td>
                      <td className="num">
                        {money(e.accrued, { currency: e.currency })}
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
