// The Super Admin control plane (blueprint H8, H10).
//
// # Counts, never a tenant's records
//
// Everything here is metadata: how many tenants, how many failed jobs, how many
// backups nobody has verified. The one place the boundary is deliberately
// crossed is a support ticket, because a ticket is text somebody wrote in order
// to be read by the platform.
//
// # A dead job has no retry button
//
// It exhausted its attempts on something a retry could never fix. A button that
// put it back would let an operator loop it forever while the real problem
// stayed unfixed, so only a FAILED job — one still in the retry cycle — offers
// one.

import { useCallback, useState } from 'react';

import {
  answerTicket,
  createBusiness,
  failedJobs,
  platformHealth,
  platformTenants,
  retryJob,
  supportQueue,
  type FailedJob,
  type PlatformHealth,
  type PlatformTenant,
  type ProvisionedBusiness,
  type Ticket,
} from '../api/admin';
import { RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key, Translate } from '../i18n/strings';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
  type FieldErrors,
} from '../ui/Form';
import { shortDate } from '../ui/format';

type Tab = 'health' | 'tenants' | 'jobs' | 'support';

export function PlatformArea() {
  const t = useT();
  const [tab, setTab] = useState<Tab>('health');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'health', label: 'plat.health' },
    { key: 'tenants', label: 'plat.tenants' },
    { key: 'jobs', label: 'plat.jobs' },
    { key: 'support', label: 'plat.support' },
  ];

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('plat.title')}</h1>
          <p className="ds-caption">{t('plat.intro')}</p>
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

      {tab === 'health' && <HealthPanel />}
      {tab === 'tenants' && <TenantsPanel />}
      {tab === 'jobs' && <JobsPanel />}
      {tab === 'support' && <QueuePanel />}
    </main>
  );
}

function HealthPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => platformHealth(client), [client]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(h: PlatformHealth) => (
        <>
          <section
            className={`ds-panel plat__banner${h.database_ok ? '' : ' plat__banner--down'}`}
            role="status"
          >
            <div className="ds-panel__body">
              <p className="plat__bannertitle">
                {h.database_ok ? t('plat.up') : t('plat.down')}
              </p>
              <p className="ds-caption">
                {t('plat.latency', { ms: String(h.database_latency_ms) })} ·{' '}
                {t('plat.checkedAt', {
                  when: shortDate(h.checked_at, locale),
                })}
              </p>
            </div>
          </section>

          <section className="ds-panel" aria-label={t('plat.health')}>
            <div className="ds-panel__body plat__tiles">
              <Tile label={t('plat.tenants')} value={String(h.tenants)} />
              <Tile
                label={t('plat.activeTenants')}
                value={String(h.active_tenants)}
                hint={t('plat.activeMeans')}
              />
              <Tile label={t('plat.companies')} value={String(h.companies)} />
              <Tile label={t('plat.users')} value={String(h.users)} />
              <Tile
                label={t('plat.activeUsers')}
                value={String(h.active_users_30d)}
              />
              <Tile label={t('plat.terminals')} value={String(h.terminals)} />

              <Tile label={t('plat.queued')} value={String(h.jobs_queued)} />
              <Tile label={t('plat.running')} value={String(h.jobs_running)} />
              <Tile
                label={t('plat.failed24h')}
                value={String(h.jobs_failed_24h)}
                bad={h.jobs_failed_24h > 0}
              />
              {/* Dead is not a bigger number of failed: it has stopped, and
                  nothing will touch it again unless somebody does. */}
              <Tile
                label={t('plat.dead')}
                value={String(h.jobs_dead)}
                bad={h.jobs_dead > 0}
              />

              <Tile
                label={t('plat.unreported')}
                value={String(h.submissions_pending)}
                bad={h.submissions_pending > 0}
              />
              <Tile
                label={t('plat.rejected')}
                value={String(h.submissions_failed)}
                bad={h.submissions_failed > 0}
              />

              <Tile
                label={t('plat.backedUp')}
                value={String(h.tenants_with_verified_backup)}
              />
              <Tile
                label={t('plat.unprotected')}
                value={String(h.tenants_without_verified_backup)}
                bad={h.tenants_without_verified_backup > 0}
                hint={t('plat.unprotectedMeans')}
              />

              <Tile
                label={t('plat.syncFailures')}
                value={String(h.sync_failures_24h)}
                bad={h.sync_failures_24h > 0}
              />
              <Tile
                label={t('plat.ticketsWaiting')}
                value={String(h.tickets_waiting_on_support)}
                bad={h.tickets_waiting_on_support > 0}
              />
            </div>
          </section>
        </>
      )}
    </RemoteBody>
  );
}

function Tile({
  label,
  value,
  hint,
  bad,
}: {
  label: string;
  value: string;
  hint?: string;
  bad?: boolean;
}) {
  return (
    <div className={`plat__tile${bad ? ' plat__tile--bad' : ''}`}>
      <span className="plat__tilelabel">{label}</span>
      <span className="plat__tilevalue">{value}</span>
      {hint && <span className="ds-caption">{hint}</span>}
    </div>
  );
}

/**
 * The markets a business can be sold into.
 *
 * The same three the backend accepts, and it is the backend that decides: a
 * fourth added there without being added here shows a shorter list, which is a
 * missing option rather than a wrong one. A country whose tax rules are not in
 * the regulatory register would otherwise produce an owner who finishes setup
 * and then cannot ring up a sale.
 */
const MARKETS: Array<{ id: string; label: Key }> = [
  { id: 'sa', label: 'plat.marketSa' },
  { id: 'bd', label: 'plat.marketBd' },
  { id: 'us', label: 'plat.marketUs' },
];

const PLAN_TIERS = ['starter', 'professional', 'business', 'enterprise'];
const DATA_REGIONS = ['sa', 'eu', 'asia', 'other'];

function TenantsPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => platformTenants(client), [client]);
  const { remote, reload } = useRemote(load);
  const [creating, setCreating] = useState(false);

  return (
    <section className="ds-panel" aria-label={t('plat.tenants')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('plat.tenants')}</h2>
          <p className="ds-caption">{t('plat.tenantsHint')}</p>
        </div>

        {!creating && (
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setCreating(true)}
          >
            {t('plat.newBusiness')}
          </button>
        )}
      </div>

      {creating && (
        <NewBusinessForm
          onClose={() => setCreating(false)}
          onCreated={reload}
        />
      )}

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: PlatformTenant[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('plat.noTenantsTitle')}
                body={t('plat.noTenantsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('plat.tenant')}</th>
                    <th scope="col">{t('plat.market')}</th>
                    <th scope="col" className="num">
                      {t('plat.companies')}
                    </th>
                    <th scope="col" className="num">
                      {t('plat.users')}
                    </th>
                    <th scope="col">{t('plat.lastActivity')}</th>
                    <th scope="col">{t('plat.backup')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((x) => (
                    <tr key={x.id}>
                      <td>
                        <span className="detail__strong">{x.name}</span>
                        <span className="ds-caption">
                          {x.plan_tier ?? ''}
                          {x.status ? ` · ${x.status}` : ''}
                        </span>
                      </td>
                      <td>{marketName(t, x.market)}</td>
                      <td className="num">{x.companies}</td>
                      <td className="num">{x.users}</td>
                      <td>
                        {/* The one figure that separates a customer from a
                            signup. */}
                        {x.last_activity
                          ? shortDate(x.last_activity, locale)
                          : t('plat.neverTraded')}
                      </td>
                      <td>
                        {x.backup_verified_at ? (
                          <span className="ds-badge ds-badge--success">
                            {shortDate(x.backup_verified_at, locale)}
                          </span>
                        ) : (
                          <span className="ds-badge ds-badge--danger">
                            {t('plat.neverVerified')}
                          </span>
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

/** A market code as a reader's word. Unknown codes render as themselves rather
 *  than as an empty cell — a tenant with a market this build does not know
 *  about is a fact worth seeing, not one worth hiding. */
function marketName(t: Translate, market: string | undefined): string {
  if (!market) return '—';
  const known = MARKETS.find((m) => m.id === market);
  return known ? t(known.label) : market.toUpperCase();
}

/**
 * Provisioning a client, which is the one thing the platform operator does that
 * writes into a tenant.
 *
 * # The password is shown once because it exists once
 *
 * The server hashes it before replying and keeps nothing readable, so this
 * response is the only copy. That shapes the screen: the result replaces the
 * form rather than appearing beside it, there is no refetch that could bring it
 * back, and the operator has to dismiss it deliberately. Reloading the list
 * behind it would be a way to lose it by accident, so the reload happens when
 * the panel closes.
 *
 * # The market is the field that cannot be corrected later
 *
 * `plan_tier` and `data_region` are commercial settings. The market decides
 * which country's tax rules every future sale is calculated with, and the Owner
 * is held to it during setup — so it is asked with a hint saying so, and it does
 * not pre-select.
 */
function NewBusinessForm({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [name, setName] = useState('');
  const [market, setMarket] = useState('');
  const [planTier, setPlanTier] = useState('starter');
  const [dataRegion, setDataRegion] = useState('sa');
  const [ownerName, setOwnerName] = useState('');
  const [ownerEmail, setOwnerEmail] = useState('');

  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [done, setDone] = useState<ProvisionedBusiness | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFormError(null);
    setErrors({});
    try {
      const out = await createBusiness(client, {
        name,
        market,
        plan_tier: planTier,
        data_region: dataRegion,
        owner_name: ownerName,
        owner_email: ownerEmail,
      });
      setDone(out);
    } catch (err) {
      // Field messages go under their own fields; anything else goes to the
      // top. The server keys them exactly as the form does.
      if (err instanceof RequestFailed) {
        setErrors(err.fields ?? {});
        setFormError(err.fields ? null : err.message);
      } else {
        setFormError(String(err));
      }
    } finally {
      setBusy(false);
    }
  }

  function close() {
    // Reload here rather than at creation, so the new row never arrives
    // underneath the one-time password and pushes it out of view.
    if (done) onCreated();
    onClose();
  }

  if (done) {
    return (
      <div className="ds-panel__body">
        <section className="ds-panel" aria-label={t('plat.created')}>
          <div className="ds-panel__body">
            <h3 className="ds-h3">{t('plat.created')}</h3>
            <p className="ds-caption">{done.detail}</p>

            <dl className="plat__once">
              <dt className="field__label">{t('plat.ownerEmail')}</dt>
              <dd className="detail__strong">{done.owner_email}</dd>

              <dt className="field__label">{t('plat.tempPassword')}</dt>
              {/* Not a text input: nothing here should look editable, and a
                  value that is never sent back should not sit in a form
                  control that a password manager would offer to save. */}
              <dd className="detail__strong">{done.temporary_password}</dd>
            </dl>

            <p className="field__hint">{t('plat.tempPasswordHint')}</p>

            <div className="form__actions">
              <button className="ds-btn ds-btn--primary" onClick={close}>
                {t('action.done')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => {
                  onCreated();
                  setDone(null);
                  setName('');
                  setMarket('');
                  setOwnerName('');
                  setOwnerEmail('');
                }}
              >
                {t('plat.createAnother')}
              </button>
            </div>
          </div>
        </section>
      </div>
    );
  }

  return (
    <div className="ds-panel__body">
      <form className="form" onSubmit={submit}>
        <p className="ds-caption">{t('plat.newBusinessHint')}</p>

        <FormError message={formError} />

        <div className="form__grid">
          <Field
            label={t('plat.businessName')}
            htmlFor="biz-name"
            hint={t('plat.businessNameHint')}
            required
            error={errors.name}
          >
            <TextInput
              id="biz-name"
              value={name}
              onChange={setName}
              error={errors.name}
            />
          </Field>

          <Field
            label={t('plat.market')}
            htmlFor="biz-market"
            hint={t('plat.marketHint')}
            required
            error={errors.market}
          >
            <SelectInput
              id="biz-market"
              value={market}
              onChange={setMarket}
              options={MARKETS}
              label={(o) => t(o.label)}
              placeholder={t('common.choose')}
              error={errors.market}
            />
          </Field>

          <Field
            label={t('plat.plan')}
            htmlFor="biz-plan"
            required
            error={errors.plan_tier}
          >
            <SelectInput
              id="biz-plan"
              value={planTier}
              onChange={setPlanTier}
              options={PLAN_TIERS.map((id) => ({ id }))}
              label={(o) => o.id}
              error={errors.plan_tier}
            />
          </Field>

          <Field
            label={t('plat.region')}
            htmlFor="biz-region"
            hint={t('plat.regionHint')}
            required
            error={errors.data_region}
          >
            <SelectInput
              id="biz-region"
              value={dataRegion}
              onChange={setDataRegion}
              options={DATA_REGIONS.map((id) => ({ id }))}
              label={(o) => o.id}
              error={errors.data_region}
            />
          </Field>

          <Field
            label={t('plat.ownerName')}
            htmlFor="biz-owner"
            required
            error={errors.owner_name}
          >
            <TextInput
              id="biz-owner"
              value={ownerName}
              onChange={setOwnerName}
              error={errors.owner_name}
            />
          </Field>

          <Field
            label={t('plat.ownerEmail')}
            htmlFor="biz-email"
            hint={t('plat.ownerEmailHint')}
            required
            error={errors.owner_email}
          >
            <TextInput
              id="biz-email"
              value={ownerEmail}
              onChange={setOwnerEmail}
              error={errors.owner_email}
            />
          </Field>
        </div>

        <FormActions
          submitLabel={t('plat.create')}
          busy={busy}
          onCancel={onClose}
        />
      </form>
    </div>
  );
}

function JobsPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => failedJobs(client), [client]);
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function retry(j: FailedJob) {
    setBusy(true);
    setFailure(null);
    try {
      await retryJob(client, j.id);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('plat.jobs')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('plat.jobs')}</h2>
          <p className="ds-caption">{t('plat.jobsHint')}</p>
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: FailedJob[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('plat.noFailedTitle')}
                body={t('plat.noFailedBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('plat.job')}</th>
                    <th scope="col">{t('plat.tenant')}</th>
                    <th scope="col" className="num">
                      {t('plat.attempts')}
                    </th>
                    <th scope="col">{t('plat.whatWentWrong')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((j) => (
                    <tr key={j.id}>
                      <td>
                        <span className="detail__strong">{j.kind}</span>
                        <span className="ds-caption">
                          {shortDate(j.failed_at, locale)}
                        </span>
                        <span
                          className={`ds-badge ds-badge--${j.status === 'dead' ? 'danger' : 'warning'}`}
                        >
                          {t(`plat.jobState.${j.status}` as Key)}
                        </span>
                      </td>
                      <td>{j.tenant ?? '—'}</td>
                      <td className="num">{j.attempts}</td>
                      <td className="plat__error">{j.last_error ?? '—'}</td>
                      <td>
                        {/* Only a failed job. See the file note. */}
                        {j.status === 'failed' && (
                          <button
                            className="ds-btn ds-btn--quiet"
                            disabled={busy}
                            onClick={() => void retry(j)}
                          >
                            {t('plat.retry')}
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

function QueuePanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [includeClosed, setIncludeClosed] = useState(false);
  const load = useCallback(
    () => supportQueue(client, includeClosed),
    [client, includeClosed],
  );
  const { remote, reload } = useRemote(load);

  const [answering, setAnswering] = useState<Ticket | null>(null);
  const [reply, setReply] = useState('');
  const [resolve, setResolve] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function send(tk: Ticket) {
    setBusy(true);
    setFailure(null);
    try {
      await answerTicket(client, tk.id, {
        body: reply,
        status: resolve ? 'resolved' : 'waiting_on_customer',
      });
      setAnswering(null);
      setReply('');
      setResolve(false);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('plat.support')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('plat.support')}</h2>
          <p className="ds-caption">{t('plat.supportHint')}</p>
        </div>
        <button
          className="ds-btn ds-btn--quiet"
          onClick={() => setIncludeClosed(!includeClosed)}
        >
          {t(includeClosed ? 'plat.showOpen' : 'plat.showAll')}
        </button>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Ticket[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('plat.noTicketsTitle')}
                body={t('plat.noTicketsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body">
              <ul className="plat__tickets">
                {payload.data.map((tk) => (
                  <li key={tk.id} className="plat__ticket">
                    <div className="plat__tickethead">
                      <span className="detail__strong">{tk.subject}</span>
                      <span
                        className={`ds-badge ds-badge--${priorityBadge(tk.priority)}`}
                      >
                        {t(`adm.priority.${tk.priority}` as Key)}
                      </span>
                      <span className="ds-badge ds-badge--neutral">
                        {t(`adm.ticketStatus.${tk.status}` as Key)}
                      </span>
                    </div>
                    <p className="ds-caption">
                      {tk.ticket_no} · {tk.tenant ?? ''} ·{' '}
                      {shortDate(tk.updated_at, locale)}
                    </p>
                    <p>{tk.body}</p>

                    {(tk.messages ?? []).map((m) => (
                      <p key={m.id} className="plat__reply">
                        <strong>
                          {m.author ?? (m.from_platform ? t('adm.support') : '')}:{' '}
                        </strong>
                        {m.body}
                      </p>
                    ))}

                    {answering?.id === tk.id ? (
                      <div className="plat__answer">
                        <TextInput
                          id={`ans-${tk.id}`}
                          value={reply}
                          onChange={setReply}
                          placeholder={t('plat.writeAnAnswer')}
                        />
                        <label className="adm__check">
                          <input
                            type="checkbox"
                            checked={resolve}
                            onChange={(e) => setResolve(e.target.checked)}
                          />
                          <span>{t('plat.marksResolved')}</span>
                        </label>
                        <div className="form__actions">
                          <button
                            className="ds-btn ds-btn--primary"
                            disabled={busy || reply.trim() === ''}
                            onClick={() => void send(tk)}
                          >
                            {t('plat.answer')}
                          </button>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setAnswering(null)}
                          >
                            {t('action.cancel')}
                          </button>
                        </div>
                      </div>
                    ) : (
                      <button
                        className="ds-btn ds-btn--quiet"
                        onClick={() => {
                          setAnswering(tk);
                          setReply('');
                          setResolve(false);
                        }}
                      >
                        {t('plat.answer')}
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )
        }
      </RemoteBody>
    </section>
  );
}

function priorityBadge(priority: string): string {
  switch (priority) {
    case 'urgent':
      return 'danger';
    case 'high':
      return 'warning';
    case 'low':
      return 'neutral';
    default:
      return 'info';
  }
}
