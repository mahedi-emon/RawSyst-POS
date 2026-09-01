// Leave requests (blueprint C5).
//
// # Pending first, always
//
// Somebody opening this screen is either asking for leave or answering a
// request. Everything already decided is history, and history that sits above
// the decision somebody came here to make is history in the way.

import { useCallback, useState } from 'react';

import {
  decideLeave,
  listEmployees,
  listLeave,
  requestLeave,
  type Employee,
  type Leave,
} from '../api/hr';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, SelectInput, TextInput } from '../ui/Form';
import { shortDate } from '../ui/format';

const KINDS = ['annual', 'sick', 'unpaid', 'maternity', 'emergency'] as const;

export function LeavePanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayDecide = can('hr.manage');
  const [asking, setAsking] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(
    () => listLeave(client, companyId, showAll ? {} : { status: 'pending' }),
    [client, companyId, showAll],
  );
  const { remote, reload } = useRemote(load);

  async function decide(leave: Leave, approve: boolean) {
    setBusy(true);
    setFailure(null);
    try {
      await decideLeave(client, companyId, leave.id, { approve });
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {asking && (
        <LeaveForm
          companyId={companyId}
          onCancel={() => setAsking(false)}
          onAsked={() => {
            setAsking(false);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('hr.leave')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('hr.leave')}</h2>
          <div className="hr__actions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setShowAll(!showAll)}
            >
              {t(showAll ? 'hr.showPendingOnly' : 'hr.showAllLeave')}
            </button>
            {!asking && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setAsking(true)}
              >
                {t('hr.requestLeave')}
              </button>
            )}
          </div>
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Leave[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t(showAll ? 'hr.noLeaveTitle' : 'hr.nothingPendingTitle')}
                  body={t(showAll ? 'hr.noLeaveBody' : 'hr.nothingPendingBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('hr.person')}</th>
                      <th scope="col">{t('hr.leaveKind')}</th>
                      <th scope="col">{t('hr.dates')}</th>
                      <th scope="col" className="num">
                        {t('hr.days')}
                      </th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((l) => (
                      <tr key={l.id}>
                        <td>{l.employee}</td>
                        <td>
                          {t(`hr.leaveKind.${l.kind}` as Key)}
                          {/* Whether it is paid decides what payroll does with
                              it, so it belongs on the row rather than being
                              inferred from the kind. */}
                          <span className="ds-caption">
                            {t(l.is_paid ? 'hr.paidLeave' : 'hr.unpaidLeave')}
                          </span>
                        </td>
                        <td>
                          {shortDate(l.starts_on, locale)} –{' '}
                          {shortDate(l.ends_on, locale)}
                          {l.reason && (
                            <span className="ds-caption">{l.reason}</span>
                          )}
                        </td>
                        <td className="num">{l.days}</td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${leaveBadge(l.status)}`}
                          >
                            {t(`hr.leaveStatus.${l.status}` as Key)}
                          </span>
                          {l.decided_by && (
                            <span className="ds-caption">{l.decided_by}</span>
                          )}
                        </td>
                        <td>
                          {mayDecide && l.status === 'pending' && (
                            <div className="hr__rowactions">
                              <button
                                className="ds-btn ds-btn--primary"
                                disabled={busy}
                                onClick={() => void decide(l, true)}
                              >
                                {t('action.approve')}
                              </button>
                              <button
                                className="ds-btn ds-btn--quiet"
                                disabled={busy}
                                onClick={() => void decide(l, false)}
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
      </section>
    </>
  );
}

function leaveBadge(status: string): string {
  switch (status) {
    case 'approved':
      return 'success';
    case 'declined':
      return 'danger';
    case 'pending':
      return 'warning';
    default:
      return 'neutral';
  }
}

function LeaveForm({
  companyId,
  onCancel,
  onAsked,
}: {
  companyId: string;
  onCancel: () => void;
  onAsked: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => listEmployees(client, companyId, {}),
    [client, companyId],
  );
  const { remote } = useRemote(load);
  const people: Employee[] = remote.state === 'ready' ? remote.data.data : [];

  const [employeeID, setEmployeeID] = useState('');
  const [kind, setKind] = useState<string>('annual');
  const [from, setFrom] = useState(today());
  const [to, setTo] = useState(today());
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await requestLeave(client, companyId, {
        employee_id: employeeID,
        kind,
        starts_on: from,
        ends_on: to,
        reason,
      });
      onAsked();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel hr__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('hr.requestLeave')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('hr.person')} htmlFor="lv-person" required>
            <SelectInput
              id="lv-person"
              value={employeeID}
              onChange={setEmployeeID}
              options={people}
              label={(p) => `${p.full_name} (${p.employee_no})`}
              placeholder={t('hr.choosePerson')}
            />
          </Field>
          <Field label={t('hr.leaveKind')} htmlFor="lv-kind" required>
            <SelectInput
              id="lv-kind"
              value={kind}
              onChange={setKind}
              options={KINDS.map((k) => ({ id: k }))}
              label={(k) => t(`hr.leaveKind.${k.id}` as Key)}
            />
          </Field>
          <Field label={t('hr.from')} htmlFor="lv-from" required>
            <input
              id="lv-from"
              type="date"
              className="field__input"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>
          <Field label={t('hr.to')} htmlFor="lv-to" required>
            <input
              id="lv-to"
              type="date"
              className="field__input"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </Field>
          <Field label={t('hr.leaveReason')} htmlFor="lv-reason">
            <TextInput id="lv-reason" value={reason} onChange={setReason} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('hr.requestLeave')}
          busy={busy}
          disabled={employeeID === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}
