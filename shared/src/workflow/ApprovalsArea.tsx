// The Approval Centre (blueprint D5, F1).
//
// # Three lists, because they answer three questions
//
// What is waiting for me, what happened to what I asked for, and what the rules
// are. A single list filtered by a dropdown would make the second question —
// which is the one a requester has — require knowing to filter.
//
// # The Centre gates and does not execute
//
// Approving something here does not perform it. The module that was stopped is
// retried by the person who asked, which keeps the engine out of every module's
// transaction. The screen says so on a granted request, because otherwise
// somebody approves an expense and waits for it to appear.

import { useCallback, useState } from 'react';

import {
  decideApproval,
  escalateApprovals,
  listApprovalRules,
  listApprovals,
  listDelegations,
  myApprovals,
  saveApprovalRule,
  setRuleActive,
  type ApprovalRequest,
  type ApprovalRule,
  type Delegation,
} from '../api/workflow';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
} from '../ui/Form';
import { money, shortDate } from '../ui/format';

type Tab = 'queue' | 'mine' | 'rules';

// The subjects the engine ships knowing about. A shop can watch anything the
// modules offer; these are the ones that exist today.
const SUBJECTS = [
  'expense',
  'purchase_order',
  'purchase_requisition',
  'discount',
  'stock_adjustment',
  'payroll',
  'refund',
] as const;

export function ApprovalsArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const mayManageRules = can('approval.manage_rules');
  const [tab, setTab] = useState<Tab>('queue');

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'queue', label: 'appr.queue', shown: true },
    { key: 'mine', label: 'appr.mine', shown: true },
    { key: 'rules', label: 'appr.rules', shown: mayManageRules },
  ];
  const visible = tabs.filter((x) => x.shown);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('appr.title')}</h1>
          <p className="ds-caption">{t('appr.intro')}</p>
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

      {tab === 'queue' && <QueuePanel companyId={companyId} />}
      {tab === 'mine' && <MinePanel companyId={companyId} />}
      {tab === 'rules' && mayManageRules && (
        <RulesPanel companyId={companyId} />
      )}
    </main>
  );
}

function QueuePanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayDecide = can('approval.decide');
  const load = useCallback(
    () => listApprovals(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [deciding, setDeciding] = useState<ApprovalRequest | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [moved, setMoved] = useState<number | null>(null);

  async function decide(r: ApprovalRequest, approve: boolean) {
    // A refusal has to say why: the person who asked needs to know what to
    // change, and "no" on its own is a conversation they now have to start.
    if (!approve && reason.trim() === '') {
      setDeciding(r);
      return;
    }
    setBusy(true);
    setFailure(null);
    try {
      await decideApproval(client, companyId, r.id, { approve, reason });
      setDeciding(null);
      setReason('');
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function escalate() {
    setBusy(true);
    setFailure(null);
    try {
      const out = await escalateApprovals(client, companyId);
      setMoved(out.escalated);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="ds-panel" aria-label={t('appr.queue')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('appr.waiting')}</h2>
          {mayDecide && (
            <button
              className="ds-btn ds-btn--quiet"
              disabled={busy}
              onClick={() => void escalate()}
            >
              {t('appr.escalateOverdue')}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {moved !== null && (
            <p className="ds-caption" role="status">
              {moved === 0
                ? t('appr.nothingOverdue')
                : t('appr.escalated', { count: String(moved) })}
            </p>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: ApprovalRequest[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('appr.nothingWaitingTitle')}
                  body={t('appr.nothingWaitingBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('appr.what')}</th>
                      <th scope="col" className="num">
                        {t('common.amount')}
                      </th>
                      <th scope="col">{t('appr.step')}</th>
                      <th scope="col">{t('appr.askedBy')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((r) => (
                      <tr
                        key={r.id}
                        className={
                          r.status === 'escalated'
                            ? 'appr__row--late'
                            : undefined
                        }
                      >
                        <td>
                          <span className="detail__strong">{r.summary}</span>
                          <span className="ds-caption">
                            {t(`appr.subject.${r.subject}` as Key)}
                            {r.rule_name ? ` · ${r.rule_name}` : ''}
                          </span>
                        </td>
                        <td className="num">
                          {r.amount
                            ? money(r.amount, { currency: r.currency })
                            : '—'}
                        </td>
                        <td>
                          {/* "2 of 3", so nobody wonders whether their
                              signature was the last one needed. */}
                          {t('appr.stepOf', {
                            at: String(r.current_step),
                            total: String(r.steps_total),
                          })}
                          {r.status === 'escalated' && (
                            <span className="ds-badge ds-badge--warning">
                              {t('appr.escalatedBadge')}
                            </span>
                          )}
                        </td>
                        <td>
                          {r.requested_by ?? '—'}
                          <span className="ds-caption">
                            {shortDate(r.requested_at, locale)}
                          </span>
                        </td>
                        <td>
                          {mayDecide && (
                            <div className="appr__rowactions">
                              <button
                                className="ds-btn ds-btn--primary"
                                disabled={busy}
                                onClick={() => void decide(r, true)}
                              >
                                {t('action.approve')}
                              </button>
                              <button
                                className="ds-btn ds-btn--quiet"
                                disabled={busy}
                                onClick={() => {
                                  setDeciding(r);
                                  setReason('');
                                }}
                              >
                                {t('action.decline')}
                              </button>
                            </div>
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

        {deciding && (
          <div className="ds-panel__body appr__decline">
            <p className="ds-caption">
              {t('appr.declineHint', { what: deciding.summary })}
            </p>
            <TextInput
              id="appr-reason"
              value={reason}
              onChange={setReason}
              placeholder={t('appr.whyDeclining')}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || reason.trim() === ''}
                onClick={() => void decide(deciding, false)}
              >
                {t('action.decline')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setDeciding(null)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </section>

      <DelegationsPanel companyId={companyId} />
    </>
  );
}

function MinePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [includeSettled, setIncludeSettled] = useState(true);
  const load = useCallback(
    () => myApprovals(client, companyId, includeSettled),
    [client, companyId, includeSettled],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('appr.mine')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('appr.mine')}</h2>
          <p className="ds-caption">{t('appr.mineHint')}</p>
        </div>
        <button
          className="ds-btn ds-btn--quiet"
          onClick={() => setIncludeSettled(!includeSettled)}
        >
          {t(includeSettled ? 'appr.showOpenOnly' : 'appr.showEverything')}
        </button>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: ApprovalRequest[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('appr.noneOfMineTitle')}
                body={t('appr.noneOfMineBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('appr.what')}</th>
                    <th scope="col" className="num">
                      {t('common.amount')}
                    </th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">{t('appr.asked')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((r) => (
                    <tr key={r.id}>
                      <td>
                        <span className="detail__strong">{r.summary}</span>
                        <span className="ds-caption">
                          {t(`appr.subject.${r.subject}` as Key)}
                        </span>
                        {/* The Centre gates, it does not execute. Somebody
                            whose request was granted has to go back and do the
                            thing, and being told that beats waiting. */}
                        {r.status === 'granted' && (
                          <span className="ds-caption">
                            {t('appr.grantedGoBack')}
                          </span>
                        )}
                      </td>
                      <td className="num">
                        {r.amount
                          ? money(r.amount, { currency: r.currency })
                          : '—'}
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${requestBadge(r.status)}`}
                        >
                          {t(`appr.status.${r.status}` as Key)}
                        </span>
                        {(r.decisions ?? []).map((d, i) => (
                          <span className="ds-caption" key={i}>
                            {d.decided_by ?? ''}
                            {d.reason ? `: ${d.reason}` : ''}
                          </span>
                        ))}
                      </td>
                      <td>{shortDate(r.requested_at, locale)}</td>
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

function requestBadge(status: string): string {
  switch (status) {
    case 'granted':
      return 'success';
    case 'refused':
      return 'danger';
    case 'escalated':
      return 'warning';
    default:
      return 'info';
  }
}

function DelegationsPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => listDelegations(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { data: Delegation[] }) =>
        payload.data.length === 0 ? (
          <></>
        ) : (
          <section className="ds-panel" aria-label={t('appr.cover')}>
            <div className="ds-panel__head">
              <h3 className="ds-h3">{t('appr.cover')}</h3>
            </div>
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('appr.from')}</th>
                    <th scope="col">{t('appr.to')}</th>
                    <th scope="col">{t('appr.while')}</th>
                    <th scope="col">{t('common.status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((d) => (
                    <tr key={d.id}>
                      <td>{d.from}</td>
                      <td>{d.to}</td>
                      <td>
                        {shortDate(d.starts_on, locale)} –{' '}
                        {shortDate(d.ends_on, locale)}
                      </td>
                      <td>
                        {/* Folded into one answer, so nobody compares two
                            dates against today in their head. */}
                        <span
                          className={`ds-badge ds-badge--${d.is_live ? 'info' : 'neutral'}`}
                        >
                          {t(d.is_live ? 'appr.coverLive' : 'appr.coverEnded')}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )
      }
    </RemoteBody>
  );
}

function RulesPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => listApprovalRules(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function toggle(rule: ApprovalRule) {
    setBusy(true);
    setFailure(null);
    try {
      await setRuleActive(client, companyId, rule.id, !rule.is_active);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {adding && (
        <RuleForm
          companyId={companyId}
          onCancel={() => setAdding(false)}
          onSaved={() => {
            setAdding(false);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('appr.rules')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('appr.rules')}</h2>
            <p className="ds-caption">{t('appr.rulesHint')}</p>
          </div>
          {!adding && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setAdding(true)}
            >
              {t('appr.addRule')}
            </button>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: ApprovalRule[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('appr.noRulesTitle')}
                  body={t('appr.noRulesBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('appr.rule')}</th>
                      <th scope="col">{t('appr.watches')}</th>
                      <th scope="col">{t('appr.doesWhat')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((rule) => (
                      <tr
                        key={rule.id}
                        className={
                          !rule.is_active ? 'detail__row--aside' : undefined
                        }
                      >
                        <td>
                          <span className="detail__strong">{rule.name}</span>
                          {rule.escalate_after_hours != null && (
                            <span className="ds-caption">
                              {t('appr.escalatesAfter', {
                                hours: String(rule.escalate_after_hours),
                              })}
                            </span>
                          )}
                        </td>
                        <td>
                          {t(`appr.subject.${rule.subject}` as Key)}
                          <span className="ds-caption appr__json">
                            {rule.condition}
                          </span>
                        </td>
                        <td>
                          {t(`appr.action.${rule.action}` as Key)}
                          <span className="ds-caption appr__json">
                            {rule.steps}
                          </span>
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${rule.is_active ? 'success' : 'neutral'}`}
                          >
                            {t(
                              rule.is_active
                                ? 'appr.inForce'
                                : 'appr.switchedOff',
                            )}
                          </span>
                        </td>
                        <td>
                          {/* Switched off rather than deleted: the requests it
                              raised name it, and deleting one would leave a
                              decision nobody can explain. */}
                          <button
                            className="ds-btn ds-btn--quiet"
                            disabled={busy}
                            onClick={() => void toggle(rule)}
                          >
                            {t(
                              rule.is_active
                                ? 'appr.switchOff'
                                : 'appr.switchOn',
                            )}
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
    </>
  );
}

function RuleForm({
  companyId,
  onCancel,
  onSaved,
}: {
  companyId: string;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [name, setName] = useState('');
  const [subject, setSubject] = useState<string>('expense');
  const [action, setAction] = useState<string>('require_approval');
  const [over, setOver] = useState('');
  const [role, setRole] = useState('owner');
  const [escalate, setEscalate] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await saveApprovalRule(client, companyId, {
        name,
        subject,
        action,
        // The condition and the chain are the engine's own shapes. The form
        // offers the one condition and the one step that cover almost every
        // rule a shop writes; anything richer is edited as data.
        condition: over.trim() === '' ? {} : { amount_over: over.trim() },
        steps: [{ role }],
        escalate_after_hours: escalate.trim() === '' ? null : Number(escalate),
      });
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="ds-panel appr__form"
      onSubmit={(e) => void submit(e)}
      noValidate
    >
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('appr.addRule')}</h2>
        <p className="ds-caption">{t('appr.addRuleHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('appr.rule')} htmlFor="ru-name" required>
            <TextInput id="ru-name" value={name} onChange={setName} />
          </Field>
          <Field label={t('appr.watches')} htmlFor="ru-subject" required>
            <SelectInput
              id="ru-subject"
              value={subject}
              onChange={setSubject}
              options={SUBJECTS.map((s) => ({ id: s }))}
              label={(s) => t(`appr.subject.${s.id}` as Key)}
            />
          </Field>
          <Field
            label={t('appr.overAmount')}
            hint={t('appr.overAmountHint')}
            htmlFor="ru-over"
          >
            <TextInput
              id="ru-over"
              value={over}
              onChange={setOver}
              inputMode="decimal"
            />
          </Field>
          <Field label={t('appr.doesWhat')} htmlFor="ru-action" required>
            <SelectInput
              id="ru-action"
              value={action}
              onChange={setAction}
              options={[
                { id: 'require_approval' },
                { id: 'require_pin' },
                { id: 'warn' },
                { id: 'block' },
              ]}
              label={(a) => t(`appr.action.${a.id}` as Key)}
            />
          </Field>
          <Field label={t('appr.decidedBy')} htmlFor="ru-role" required>
            <SelectInput
              id="ru-role"
              value={role}
              onChange={setRole}
              options={[
                { id: 'owner' },
                { id: 'store_manager' },
                { id: 'accountant' },
              ]}
              label={(r) => t(`appr.role.${r.id}` as Key)}
            />
          </Field>
          <Field
            label={t('appr.escalateAfter')}
            hint={t('appr.escalateAfterHint')}
            htmlFor="ru-escalate"
          >
            <TextInput
              id="ru-escalate"
              value={escalate}
              onChange={setEscalate}
              inputMode="numeric"
            />
          </Field>
        </div>
        <FormActions
          submitLabel={t('appr.addRule')}
          busy={busy}
          disabled={name.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
