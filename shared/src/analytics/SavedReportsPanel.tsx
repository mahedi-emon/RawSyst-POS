// Saved and scheduled reports (blueprint D1), and the Saudization reading (E6).
//
// # A saved report shows what its window means today
//
// "Last quarter" is a phrase, not two dates: the same saved report run in
// October and in January covers different months, which is the whole point. So
// the row shows the phrase AND the dates it resolves to right now, because a
// reader who cannot see the dates will not trust the figure.
//
// # A schedule that failed says so where the schedule is
//
// Not in a log. The owner who set up the Monday report is the person who needs
// to know it has not gone out for three weeks, and the row is where they will
// look.

import { useCallback, useState } from 'react';

import {
  listSavedReports,
  removeSavedReport,
  saveReport,
  workforce,
  type SavedKind,
  type SavedPeriod,
  type SavedReport,
  type Workforce,
} from '../api/savedReports';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { LabelledSelect, LabelledText } from '../governance/fields';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';

const KINDS: SavedKind[] = [
  'profit_and_loss',
  'balance_sheet',
  'trial_balance',
  'cash_flow',
  'sales',
  'expenses',
  'stock',
  'vat_return',
  'receivables',
  'payables',
  'movers',
  'compliance',
];

const PERIODS: SavedPeriod[] = [
  'today',
  'this_week',
  'this_month',
  'last_month',
  'this_quarter',
  'last_quarter',
  'this_year',
  'last_year',
];

const BLANK: SavedReport = {
  name: '',
  kind: 'profit_and_loss',
  period: 'last_month',
  cadence: '',
  is_active: true,
};

export function SavedReportsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const maySave = can('report.save');

  const load = useCallback(
    () => listSavedReports(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [draft, setDraft] = useState<SavedReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const set = (patch: Partial<SavedReport>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

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
    <>
      <section className="ds-panel" aria-label={t('rep.saved')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('rep.saved')}</h2>
            <p className="ds-caption">{t('rep.savedHint')}</p>
          </div>
          {maySave && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setDraft({ ...BLANK })}
            >
              {t('rep.keepOne')}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />

          {draft && (
            <div className="pri__form">
              <LabelledText
                id="sr-name"
                label={t('rep.reportName')}
                value={draft.name}
                onChange={(v) => set({ name: v })}
              />
              <LabelledSelect
                id="sr-kind"
                label={t('rep.whichReport')}
                value={draft.kind}
                onChange={(v) => set({ kind: v as SavedKind })}
                options={KINDS.map((k) => ({
                  value: k,
                  label: t(`rep.kind.${k}` as Key),
                }))}
              />
              <LabelledSelect
                id="sr-period"
                label={t('rep.overWhat')}
                hint={t('rep.overWhatHint')}
                value={draft.period}
                onChange={(v) => set({ period: v as SavedPeriod })}
                options={PERIODS.map((p) => ({
                  value: p,
                  label: t(`rep.period.${p}` as Key),
                }))}
              />
              <LabelledSelect
                id="sr-cadence"
                label={t('rep.howOften')}
                hint={t('rep.howOftenHint')}
                value={draft.cadence ?? ''}
                onChange={(v) =>
                  set({ cadence: v as SavedReport['cadence'] })
                }
                options={[
                  { value: '', label: t('rep.onlyWhenIAsk') },
                  { value: 'daily', label: t('rep.daily') },
                  { value: 'weekly', label: t('rep.weekly') },
                  { value: 'monthly', label: t('rep.monthly') },
                ]}
              />

              {draft.cadence === 'weekly' && (
                <LabelledSelect
                  id="sr-dow"
                  label={t('rep.whichDay')}
                  value={String(draft.day_of_week ?? 1)}
                  onChange={(v) => set({ day_of_week: Number(v) })}
                  options={[0, 1, 2, 3, 4, 5, 6].map((d) => ({
                    value: String(d),
                    label: t(`rep.day.${d}` as Key),
                  }))}
                />
              )}
              {draft.cadence === 'monthly' && (
                <LabelledText
                  id="sr-dom"
                  label={t('rep.whichDate')}
                  hint={t('rep.whichDateHint')}
                  value={String(draft.day_of_month ?? 1)}
                  onChange={(v) => set({ day_of_month: Number(v) || 1 })}
                  inputMode="numeric"
                />
              )}
              {draft.cadence !== '' && (
                <LabelledText
                  id="sr-to"
                  label={t('rep.sendItTo')}
                  hint={t('rep.sendItToHint')}
                  value={draft.recipients ?? ''}
                  onChange={(v) => set({ recipients: v })}
                />
              )}

              <div className="form__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || draft.name.trim() === ''}
                  onClick={() =>
                    void run(async () => {
                      await saveReport(client, companyId, draft);
                      setDraft(null);
                    })
                  }
                >
                  {t('action.save')}
                </button>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setDraft(null)}
                >
                  {t('action.cancel')}
                </button>
              </div>
            </div>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: SavedReport[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('rep.noneTitle')}
                  body={t('rep.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('rep.reportName')}</th>
                      <th scope="col">{t('rep.whichReport')}</th>
                      <th scope="col">{t('rep.covering')}</th>
                      <th scope="col">{t('rep.howOften')}</th>
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
                          {r.name}
                          {r.last_run_error && (
                            <span className="ds-badge ds-badge--danger">
                              {t('rep.lastRunFailed')}
                            </span>
                          )}
                        </td>
                        <td>{t(`rep.kind.${r.kind}` as Key)}</td>
                        <td>
                          {t(`rep.period.${r.period}` as Key)}
                          {r.from && r.to && (
                            <span className="ds-caption">
                              {' '}
                              {shortDate(r.from, locale)} –{' '}
                              {shortDate(r.to, locale)}
                            </span>
                          )}
                        </td>
                        <td>
                          {r.cadence ? (
                            <>
                              {t(`rep.${r.cadence}` as Key)}
                              {r.last_run_at && (
                                <span className="ds-caption">
                                  {' '}
                                  {shortDate(r.last_run_at, locale)}
                                </span>
                              )}
                            </>
                          ) : (
                            t('rep.onlyWhenIAsk')
                          )}
                        </td>
                        <td className="ds-table__actions">
                          {maySave && (
                            <>
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                onClick={() => setDraft(r)}
                              >
                                {t('action.edit')}
                              </button>
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(() =>
                                    removeSavedReport(
                                      client,
                                      companyId,
                                      r.id as string,
                                    ),
                                  )
                                }
                              >
                                {t('action.remove')}
                              </button>
                            </>
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

      <WorkforcePanel companyId={companyId} />
    </>
  );
}

/** E6's Saudization reading. */
function WorkforcePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => workforce(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('rep.workforce')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('rep.workforce')}</h2>
          <p className="ds-caption">{t('rep.workforceHint')}</p>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { workforce: Workforce }) => {
          const w = payload.workforce;
          return (
            <div className="ds-panel__body">
              <dl className="sub__facts">
                <Fact label={t('rep.saudiShare')} value={`${w.saudi_share}%`} />
                <Fact label={t('rep.saudiStaff')} value={String(w.saudi)} />
                <Fact label={t('rep.otherStaff')} value={String(w.non_saudi)} />
                <Fact label={t('rep.everybody')} value={String(w.total)} />
              </dl>

              {(w.expiring_soon > 0 || w.expired > 0) && (
                <p className="grp__caveat" role="status">
                  {t('rep.papersLapsing', {
                    soon: String(w.expiring_soon),
                    gone: String(w.expired),
                  })}
                </p>
              )}

              {w.by_department.length > 0 && (
                <div className="ds-scroll-x">
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('rep.department')}</th>
                        <th scope="col" className="num">
                          {t('rep.everybody')}
                        </th>
                        <th scope="col" className="num">
                          {t('rep.saudiStaff')}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {w.by_department.map((d) => (
                        <tr key={d.department}>
                          <td>{d.department}</td>
                          <td className="num">{d.total}</td>
                          <td className="num">{d.saudi}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              <p className="ds-caption">{t('rep.noBandHere')}</p>
            </div>
          );
        }}
      </RemoteBody>
    </section>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="sub__fact">
      <dt>{label}</dt>
      <dd className="num">{value}</dd>
    </div>
  );
}
