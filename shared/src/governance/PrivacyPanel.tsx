// Consent, data-subject requests and incidents (blueprint E4.1).
//
// # Three lists, one screen
//
// They are the three things that HAPPEN under PDPL, as against the register
// next door which is what a shop has WRITTEN DOWN. A person answering a request
// usually needs to look at the consent beside it, and a breach notification
// names the categories the register describes.
//
// # Withdrawing is a button, deleting is not offered
//
// A consent row is never removed. The proof a shop needs is not "there is no
// row saying they agreed" — it is the row, the date, the channel, and the date
// they withdrew. So the only verb on a live grant is Withdraw.
//
// # The clocks are the point
//
// A request shows days left and an incident shows hours left, both negative
// once past. That number is the reason the screen exists: thirty days and
// seventy-two hours are short, and a queue that did not count would be a list.

import { useCallback, useState } from 'react';

import {
  closeIncident,
  closeSubjectRequest,
  extendSubjectRequest,
  listConsents,
  listIncidents,
  listSubjectRequests,
  logIncident,
  notifyIncident,
  openSubjectRequest,
  recordConsent,
  withdrawConsent,
  type Consent,
  type ConsentChannel,
  type ConsentPurpose,
  type Incident,
  type LawfulBasis,
  type RequestKind,
  type SubjectRequest,
} from '../api/governance';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { LabelledSelect, LabelledText } from './fields';
import { shortDate } from '../ui/format';

type Tab = 'requests' | 'incidents' | 'consents';

export function PrivacyPanel({ companyId }: { companyId: string }) {
  const t = useT();
  const [tab, setTab] = useState<Tab>('requests');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'requests', label: 'pri.requests' },
    { key: 'incidents', label: 'pri.incidents' },
    { key: 'consents', label: 'pri.consents' },
  ];

  return (
    <>
      <div className="segmented" role="group" aria-label={t('common.whatToShow')}>
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

      {tab === 'requests' && <RequestsList companyId={companyId} />}
      {tab === 'incidents' && <IncidentsList companyId={companyId} />}
      {tab === 'consents' && <ConsentsList companyId={companyId} />}
    </>
  );
}

// --- data subject requests ------------------------------------------------

const REQUEST_KINDS: RequestKind[] = [
  'access',
  'export',
  'correction',
  'deletion',
  'objection',
  'portability',
];

function RequestsList({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const [openOnly, setOpenOnly] = useState(true);
  const load = useCallback(
    () => listSubjectRequests(client, companyId, openOnly),
    [client, companyId, openOnly],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [kind, setKind] = useState<RequestKind>('access');
  const [name, setName] = useState('');
  const [contact, setContact] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const [closing, setClosing] = useState<SubjectRequest | null>(null);
  const [outcome, setOutcome] = useState('fulfilled');
  const [note, setNote] = useState('');

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
    <section className="ds-panel" aria-label={t('pri.requests')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('pri.requests')}</h2>
          <p className="ds-caption">{t('pri.requestsHint')}</p>
        </div>
        <div className="ds-panel__actions">
          <label className="ds-check">
            <input
              type="checkbox"
              checked={openOnly}
              onChange={(e) => setOpenOnly(e.target.checked)}
            />
            {t('pri.openOnly')}
          </label>
          {mayManage && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setAdding((v) => !v)}
            >
              {t('pri.recordRequest')}
            </button>
          )}
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        {adding && (
          <div className="pri__form">
            <LabelledSelect
              id="dsr-kind"
              label={t('pri.whatTheyAsked')}
              value={kind}
              onChange={(v) => setKind(v as RequestKind)}
              options={REQUEST_KINDS.map((k) => ({
                value: k,
                label: t(`pri.kind.${k}` as Key),
              }))}
            />
            <LabelledText
              id="dsr-name"
              label={t('pri.whoIsAsking')}
              value={name}
              onChange={setName}
            />
            <LabelledText
              id="dsr-contact"
              label={t('pri.howToReachThem')}
              value={contact}
              onChange={setContact}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || name.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await openSubjectRequest(client, companyId, {
                      kind,
                      subject_type: 'customer',
                      subject_name: name,
                      subject_contact: contact,
                    });
                    setAdding(false);
                    setName('');
                    setContact('');
                  })
                }
              >
                {t('pri.startTheClock')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setAdding(false)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}

        {closing && (
          <div className="pri__form">
            <p className="ds-caption">
              {t('pri.closingHint', { no: closing.request_no })}
            </p>
            <LabelledSelect
              id="dsr-outcome"
              label={t('pri.outcome')}
              value={outcome}
              onChange={setOutcome}
              options={[
                { value: 'fulfilled', label: t('pri.fulfilled') },
                { value: 'partially_fulfilled', label: t('pri.partly') },
                { value: 'refused', label: t('pri.refused') },
              ]}
            />
            <LabelledText
              id="dsr-note"
              label={t('pri.reasonForThem')}
              value={note}
              onChange={setNote}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={
                  busy || (outcome !== 'fulfilled' && note.trim() === '')
                }
                onClick={() =>
                  void run(async () => {
                    await closeSubjectRequest(client, companyId, closing.id, {
                      outcome,
                      note,
                    });
                    setClosing(null);
                    setNote('');
                  })
                }
              >
                {t('pri.close')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setClosing(null)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: SubjectRequest[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('pri.noRequestsTitle')}
                body={t('pri.noRequestsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('pri.reference')}</th>
                    <th scope="col">{t('pri.whatTheyAsked')}</th>
                    <th scope="col">{t('pri.whoIsAsking')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">{t('pri.dueIn')}</th>
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
                      <td>{r.request_no}</td>
                      <td>{t(`pri.kind.${r.kind}` as Key)}</td>
                      <td>
                        {r.subject_name}
                        {r.subject_contact && (
                          <span className="ds-caption"> {r.subject_contact}</span>
                        )}
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${
                            r.closed_at
                              ? 'neutral'
                              : r.days_left < 0
                                ? 'danger'
                                : r.days_left < 7
                                  ? 'warning'
                                  : 'ok'
                          }`}
                        >
                          {t(`pri.status.${r.status}` as Key)}
                        </span>
                        {r.legal_hold_applied && (
                          <span className="ds-badge ds-badge--warning">
                            {t('pri.heldByLaw')}
                          </span>
                        )}
                      </td>
                      <td className="num">
                        {r.closed_at
                          ? shortDate(r.closed_at, locale)
                          : t('cmp.inDays', { n: String(r.days_left) })}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && !r.closed_at && (
                          <>
                            {!r.extended_to && (
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(() =>
                                    extendSubjectRequest(
                                      client,
                                      companyId,
                                      r.id,
                                      t('pri.extensionReason'),
                                    ),
                                  )
                                }
                              >
                                {t('pri.extend')}
                              </button>
                            )}
                            <button
                              className="ds-btn ds-btn--quiet ds-btn--sm"
                              onClick={() => setClosing(r)}
                            >
                              {t('pri.close')}
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
  );
}

// --- incidents ------------------------------------------------------------

function IncidentsList({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => listIncidents(client, companyId, false),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [title, setTitle] = useState('');
  const [what, setWhat] = useState('');
  const [categories, setCategories] = useState('');
  const [severity, setSeverity] = useState('medium');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const [closing, setClosing] = useState<Incident | null>(null);
  const [containment, setContainment] = useState('');

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
    <section className="ds-panel" aria-label={t('pri.incidents')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('pri.incidents')}</h2>
          <p className="ds-caption">{t('pri.incidentsHint')}</p>
        </div>
        {mayManage && (
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setAdding((v) => !v)}
          >
            {t('pri.logIncident')}
          </button>
        )}
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        {adding && (
          <div className="pri__form">
            <LabelledText
              id="inc-title"
              label={t('pri.incidentTitle')}
              value={title}
              onChange={setTitle}
            />
            <LabelledText
              id="inc-what"
              label={t('pri.whatHappened')}
              value={what}
              onChange={setWhat}
            />
            <LabelledText
              id="inc-cats"
              label={t('pri.whatDataCategories')}
              value={categories}
              onChange={setCategories}
            />
            <LabelledSelect
              id="inc-sev"
              label={t('pri.severity')}
              value={severity}
              onChange={setSeverity}
              options={['low', 'medium', 'high', 'critical'].map((s) => ({
                value: s,
                label: t(`pri.sev.${s}` as Key),
              }))}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || title.trim() === '' || what.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await logIncident(client, companyId, {
                      title,
                      what_happened: what,
                      data_categories: categories,
                      severity,
                    });
                    setAdding(false);
                    setTitle('');
                    setWhat('');
                    setCategories('');
                  })
                }
              >
                {t('pri.startTheCountdown')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setAdding(false)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}

        {closing && (
          <div className="pri__form">
            <LabelledText
              id="inc-cont"
              label={t('pri.containment')}
              value={containment}
              onChange={setContainment}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || containment.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await closeIncident(
                      client,
                      companyId,
                      closing.id,
                      containment,
                    );
                    setClosing(null);
                    setContainment('');
                  })
                }
              >
                {t('pri.closeIncident')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setClosing(null)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Incident[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('pri.noIncidentsTitle')}
                body={t('pri.noIncidentsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('pri.reference')}</th>
                    <th scope="col">{t('pri.incidentTitle')}</th>
                    <th scope="col">{t('pri.severity')}</th>
                    <th scope="col">{t('pri.notified')}</th>
                    <th scope="col">{t('pri.hoursLeft')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((i) => (
                    <tr key={i.id}>
                      <td>{i.incident_no}</td>
                      <td>{i.title}</td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${
                            i.severity === 'critical' || i.severity === 'high'
                              ? 'danger'
                              : i.severity === 'medium'
                                ? 'warning'
                                : 'neutral'
                          }`}
                        >
                          {t(`pri.sev.${i.severity}` as Key)}
                        </span>
                      </td>
                      <td>
                        {i.sdaia_notified_at
                          ? shortDate(i.sdaia_notified_at, locale)
                          : t('pri.notYet')}
                      </td>
                      <td className="num">
                        {i.closed_at ? '—' : String(i.hours_left)}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && !i.closed_at && (
                          <>
                            {!i.sdaia_notified_at && (
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(() =>
                                    notifyIncident(
                                      client,
                                      companyId,
                                      i.id,
                                      'sdaia',
                                    ),
                                  )
                                }
                              >
                                {t('pri.markAuthorityTold')}
                              </button>
                            )}
                            {!i.subjects_notified_at && (
                              <button
                                className="ds-btn ds-btn--quiet ds-btn--sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(() =>
                                    notifyIncident(
                                      client,
                                      companyId,
                                      i.id,
                                      'subjects',
                                    ),
                                  )
                                }
                              >
                                {t('pri.markPeopleTold')}
                              </button>
                            )}
                            <button
                              className="ds-btn ds-btn--quiet ds-btn--sm"
                              onClick={() => setClosing(i)}
                            >
                              {t('pri.close')}
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
  );
}

// --- consent --------------------------------------------------------------

const PURPOSES: ConsentPurpose[] = [
  'transactional',
  'marketing',
  'profiling',
  'loyalty',
  'credit_assessment',
];
const CHANNELS: ConsentChannel[] = [
  'sms',
  'email',
  'whatsapp',
  'phone',
  'post',
  'in_app',
  'any',
];
const BASES: LawfulBasis[] = [
  'consent',
  'contract',
  'legal_obligation',
  'legitimate_interest',
  'vital_interest',
  'public_interest',
];

function ConsentsList({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('privacy.manage');

  const [liveOnly, setLiveOnly] = useState(true);
  const load = useCallback(
    () => listConsents(client, companyId, { live: liveOnly }),
    [client, companyId, liveOnly],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [subjectId, setSubjectId] = useState('');
  const [basis, setBasis] = useState<LawfulBasis>('consent');
  const [purpose, setPurpose] = useState<ConsentPurpose>('marketing');
  const [channel, setChannel] = useState<ConsentChannel>('sms');
  const [proof, setProof] = useState('');
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
    <section className="ds-panel" aria-label={t('pri.consents')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('pri.consents')}</h2>
          <p className="ds-caption">{t('pri.consentsHint')}</p>
        </div>
        <div className="ds-panel__actions">
          <label className="ds-check">
            <input
              type="checkbox"
              checked={liveOnly}
              onChange={(e) => setLiveOnly(e.target.checked)}
            />
            {t('pri.liveOnly')}
          </label>
          {mayManage && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setAdding((v) => !v)}
            >
              {t('pri.recordConsent')}
            </button>
          )}
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        {adding && (
          <div className="pri__form">
            <LabelledText
              id="con-subject"
              label={t('pri.customerReference')}
              value={subjectId}
              onChange={setSubjectId}
            />
            <LabelledSelect
              id="con-basis"
              label={t('pri.lawfulBasis')}
              value={basis}
              onChange={(v) => setBasis(v as LawfulBasis)}
              options={BASES.map((b) => ({
                value: b,
                label: t(`pri.basis.${b}` as Key),
              }))}
            />
            <LabelledSelect
              id="con-purpose"
              label={t('pri.purpose')}
              value={purpose}
              onChange={(v) => setPurpose(v as ConsentPurpose)}
              options={PURPOSES.map((p) => ({
                value: p,
                label: t(`pri.purpose.${p}` as Key),
              }))}
            />
            <LabelledSelect
              id="con-channel"
              label={t('pri.channel')}
              value={channel}
              onChange={(v) => setChannel(v as ConsentChannel)}
              options={CHANNELS.map((c) => ({
                value: c,
                label: t(`pri.channel.${c}` as Key),
              }))}
            />
            <LabelledText
              id="con-proof"
              label={t('pri.proof')}
              value={proof}
              onChange={setProof}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={
                  busy || subjectId.trim() === '' || proof.trim() === ''
                }
                onClick={() =>
                  void run(async () => {
                    await recordConsent(client, companyId, {
                      subject_type: 'customer',
                      subject_id: subjectId.trim(),
                      lawful_basis: basis,
                      purpose,
                      channel,
                      proof,
                    });
                    setAdding(false);
                    setSubjectId('');
                    setProof('');
                  })
                }
              >
                {t('action.save')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setAdding(false)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Consent[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('pri.noConsentsTitle')}
                body={t('pri.noConsentsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('pri.person')}</th>
                    <th scope="col">{t('pri.purpose')}</th>
                    <th scope="col">{t('pri.channel')}</th>
                    <th scope="col">{t('pri.lawfulBasis')}</th>
                    <th scope="col">{t('pri.given')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((c) => (
                    <tr key={c.id}>
                      <td>{c.subject_name || c.subject_id.slice(0, 8)}</td>
                      <td>{t(`pri.purpose.${c.purpose}` as Key)}</td>
                      <td>{t(`pri.channel.${c.channel}` as Key)}</td>
                      <td>{t(`pri.basis.${c.lawful_basis}` as Key)}</td>
                      <td>
                        {shortDate(c.granted_at, locale)}
                        {c.withdrawn_at && (
                          <span className="ds-badge ds-badge--neutral">
                            {t('pri.withdrawn')}
                          </span>
                        )}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && c.granted && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() =>
                              void run(() =>
                                withdrawConsent(client, companyId, c.id),
                              )
                            }
                          >
                            {t('pri.withdraw')}
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
