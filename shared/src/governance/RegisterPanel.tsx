// The written register: processing activities, retention, legal holds and the
// destruction log (blueprint E4.1).
//
// # Four lists that answer an auditor
//
// The panel next door holds what HAPPENS — a consent given, a request answered,
// a breach logged. This one holds what a shop has WRITTEN DOWN and can be asked
// to produce: the record of processing activities, how long each category of
// data is kept, what is under legal hold, and the permanent proof of what has
// been destroyed.
//
// # The destruction log has no verbs
//
// It is append-only in the database and read-only here. A destruction log that
// can be edited proves nothing at all, which is the whole reason it exists.

import { useCallback, useState } from 'react';

import {
  listActivities,
  listDestructions,
  listHolds,
  listRetention,
  placeHold,
  releaseHold,
  removeActivity,
  saveActivity,
  saveRetention,
  type Destruction,
  type LawfulBasis,
  type LegalHold,
  type ProcessingActivity,
  type RetentionPolicy,
} from '../api/governance';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';
import { LabelledSelect, LabelledText } from './fields';

type Tab = 'activities' | 'retention' | 'holds' | 'destructions';

export function RegisterPanel({ companyId }: { companyId: string }) {
  const t = useT();
  const [tab, setTab] = useState<Tab>('activities');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'activities', label: 'reg.activities' },
    { key: 'retention', label: 'reg.retention' },
    { key: 'holds', label: 'reg.holds' },
    { key: 'destructions', label: 'reg.destructions' },
  ];

  return (
    <>
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

      {tab === 'activities' && <Activities companyId={companyId} />}
      {tab === 'retention' && <Retention companyId={companyId} />}
      {tab === 'holds' && <Holds companyId={companyId} />}
      {tab === 'destructions' && <Destructions companyId={companyId} />}
    </>
  );
}

const BASES: LawfulBasis[] = [
  'consent',
  'contract',
  'legal_obligation',
  'legitimate_interest',
  'vital_interest',
  'public_interest',
];

const BLANK_ACTIVITY: ProcessingActivity = {
  name: '',
  purpose: '',
  lawful_basis: 'contract',
  data_categories: '',
  subject_categories: '',
  cross_border: false,
};

function Activities({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => listActivities(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [draft, setDraft] = useState<ProcessingActivity | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

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

  const set = (patch: Partial<ProcessingActivity>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  return (
    <section className="ds-panel" aria-label={t('reg.activities')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('reg.activities')}</h2>
          <p className="ds-caption">{t('reg.activitiesHint')}</p>
        </div>
        {mayManage && (
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setDraft({ ...BLANK_ACTIVITY })}
          >
            {t('reg.addActivity')}
          </button>
        )}
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        {draft && (
          <div className="pri__form">
            <LabelledText
              id="ra-name"
              label={t('reg.activityName')}
              value={draft.name}
              onChange={(v) => set({ name: v })}
            />
            <LabelledText
              id="ra-purpose"
              label={t('reg.purpose')}
              value={draft.purpose}
              onChange={(v) => set({ purpose: v })}
            />
            <LabelledSelect
              id="ra-basis"
              label={t('pri.lawfulBasis')}
              value={draft.lawful_basis}
              onChange={(v) => set({ lawful_basis: v as LawfulBasis })}
              options={BASES.map((b) => ({
                value: b,
                label: t(`pri.basis.${b}` as Key),
              }))}
            />
            <LabelledText
              id="ra-data"
              label={t('reg.dataCategories')}
              value={draft.data_categories}
              onChange={(v) => set({ data_categories: v })}
            />
            <LabelledText
              id="ra-subjects"
              label={t('reg.subjectCategories')}
              value={draft.subject_categories}
              onChange={(v) => set({ subject_categories: v })}
            />
            <LabelledText
              id="ra-recipients"
              label={t('reg.recipients')}
              value={draft.recipients ?? ''}
              onChange={(v) => set({ recipients: v })}
            />
            <LabelledText
              id="ra-retention"
              label={t('reg.retentionNote')}
              value={draft.retention_note ?? ''}
              onChange={(v) => set({ retention_note: v })}
            />

            <label className="ds-check">
              <input
                type="checkbox"
                checked={draft.cross_border}
                onChange={(e) => set({ cross_border: e.target.checked })}
              />
              {t('reg.crossBorder')}
            </label>

            {/* Named only when it applies. A transfer outside the Kingdom has
                to say where the data goes and what protects it, and the server
                refuses one that does not. */}
            {draft.cross_border && (
              <>
                <LabelledText
                  id="ra-dest"
                  label={t('reg.destination')}
                  value={draft.destination_country ?? ''}
                  onChange={(v) => set({ destination_country: v })}
                />
                <LabelledText
                  id="ra-safeguard"
                  label={t('reg.safeguard')}
                  value={draft.transfer_safeguard ?? ''}
                  onChange={(v) => set({ transfer_safeguard: v })}
                />
              </>
            )}

            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || draft.name.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await saveActivity(client, companyId, draft);
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
        {(payload: { data: ProcessingActivity[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('reg.noActivitiesTitle')}
                body={t('reg.noActivitiesBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('reg.activityName')}</th>
                    <th scope="col">{t('reg.purpose')}</th>
                    <th scope="col">{t('pri.lawfulBasis')}</th>
                    <th scope="col">{t('reg.crossBorder')}</th>
                    <th scope="col">{t('reg.reviewed')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((a) => (
                    <tr key={a.id ?? a.name}>
                      <td>{a.name}</td>
                      <td>{a.purpose}</td>
                      <td>{t(`pri.basis.${a.lawful_basis}` as Key)}</td>
                      <td>
                        {a.cross_border ? (
                          <span className="ds-badge ds-badge--warning">
                            {a.destination_country}
                          </span>
                        ) : (
                          t('common.no')
                        )}
                      </td>
                      <td>
                        {a.reviewed_on ? shortDate(a.reviewed_on, locale) : '—'}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && (
                          <>
                            <button
                              className="ds-btn ds-btn--quiet ds-btn--sm"
                              onClick={() => setDraft(a)}
                            >
                              {t('action.edit')}
                            </button>
                            {a.id && (
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(() =>
                                    removeActivity(
                                      client,
                                      companyId,
                                      a.id as string,
                                    ),
                                  )
                                }
                              >
                                {t('action.remove')}
                              </button>
                            )}
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
  );
}

function Retention({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => listRetention(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [category, setCategory] = useState('');
  const [months, setMonths] = useState('24');
  const [action, setAction] = useState('anonymize');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function save() {
    setBusy(true);
    setFailure(null);
    try {
      await saveRetention(client, companyId, {
        data_category: category,
        retain_months: Number(months) || 0,
        action: action as RetentionPolicy['action'],
        is_active: true,
      });
      setCategory('');
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('reg.retention')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('reg.retention')}</h2>
          <p className="ds-caption">{t('reg.retentionHint')}</p>
        </div>
      </div>

      {mayManage && (
        <div className="ds-panel__body">
          <FormError message={failure} />
          <div className="pri__form">
            <LabelledText
              id="rt-cat"
              label={t('reg.dataCategory')}
              value={category}
              onChange={setCategory}
            />
            <LabelledText
              id="rt-months"
              label={t('reg.keepForMonths')}
              value={months}
              onChange={setMonths}
              inputMode="numeric"
            />
            <LabelledSelect
              id="rt-action"
              label={t('reg.thenWhat')}
              value={action}
              onChange={setAction}
              options={['archive', 'anonymize', 'destroy'].map((a) => ({
                value: a,
                label: t(`reg.action.${a}` as Key),
              }))}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || category.trim() === ''}
                onClick={() => void save()}
              >
                {t('action.save')}
              </button>
            </div>
          </div>
        </div>
      )}

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: RetentionPolicy[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('reg.noRetentionTitle')}
                body={t('reg.noRetentionBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('reg.dataCategory')}</th>
                    <th scope="col">{t('reg.keepForMonths')}</th>
                    <th scope="col">{t('reg.thenWhat')}</th>
                    <th scope="col">{t('cmp.lastRun')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((r) => (
                    <tr key={r.id ?? r.data_category}>
                      <td>{r.data_category}</td>
                      <td className="num">{r.retain_months}</td>
                      <td>{t(`reg.action.${r.action}` as Key)}</td>
                      <td>
                        {r.last_run_at
                          ? shortDate(r.last_run_at, locale)
                          : t('cmp.neverRun')}
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

function Holds({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => listHolds(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [name, setName] = useState('');
  const [reason, setReason] = useState('');
  const [category, setCategory] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

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
    <section className="ds-panel" aria-label={t('reg.holds')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('reg.holds')}</h2>
          <p className="ds-caption">{t('reg.holdsHint')}</p>
        </div>
      </div>

      {mayManage && (
        <div className="ds-panel__body">
          <FormError message={failure} />
          <div className="pri__form">
            <LabelledText
              id="lh-name"
              label={t('reg.holdName')}
              value={name}
              onChange={setName}
            />
            <LabelledText
              id="lh-reason"
              label={t('reg.holdReason')}
              value={reason}
              onChange={setReason}
            />
            <LabelledText
              id="lh-cat"
              label={t('reg.dataCategory')}
              value={category}
              onChange={setCategory}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={
                  busy ||
                  name.trim() === '' ||
                  reason.trim() === '' ||
                  category.trim() === ''
                }
                onClick={() =>
                  void run(async () => {
                    await placeHold(client, companyId, {
                      name,
                      reason,
                      data_category: category,
                    });
                    setName('');
                    setReason('');
                    setCategory('');
                  })
                }
              >
                {t('reg.placeHold')}
              </button>
            </div>
          </div>
        </div>
      )}

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: LegalHold[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('reg.noHoldsTitle')}
                body={t('reg.noHoldsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('reg.holdName')}</th>
                    <th scope="col">{t('reg.holdReason')}</th>
                    <th scope="col">{t('reg.covers')}</th>
                    <th scope="col">{t('reg.placed')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((h) => (
                    <tr key={h.id}>
                      <td>{h.name}</td>
                      <td>{h.reason}</td>
                      <td>{h.data_category || t('reg.onePerson')}</td>
                      <td>
                        {h.placed_at ? shortDate(h.placed_at, locale) : '—'}
                        {h.released_at && (
                          <span className="ds-badge ds-badge--neutral">
                            {t('reg.released')}
                          </span>
                        )}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && !h.released_at && h.id && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() =>
                              void run(() =>
                                releaseHold(
                                  client,
                                  companyId,
                                  h.id as string,
                                ),
                              )
                            }
                          >
                            {t('reg.release')}
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
  );
}

function Destructions({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => listDestructions(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('reg.destructions')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('reg.destructions')}</h2>
          <p className="ds-caption">{t('reg.destructionsHint')}</p>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Destruction[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('reg.noDestructionsTitle')}
                body={t('reg.noDestructionsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('reg.when')}</th>
                    <th scope="col">{t('reg.dataCategory')}</th>
                    <th scope="col">{t('reg.thenWhat')}</th>
                    <th scope="col">{t('reg.rows')}</th>
                    <th scope="col">{t('reg.why')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((d) => (
                    <tr key={d.id}>
                      <td>{shortDate(d.executed_at, locale)}</td>
                      <td>{d.data_category}</td>
                      <td>{t(`reg.action.${d.action}` as Key)}</td>
                      <td className="num">{d.row_count}</td>
                      <td>{d.reason}</td>
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
