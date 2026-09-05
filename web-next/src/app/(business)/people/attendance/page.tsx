'use client';

// Who was in, and who asked to be away.
//
// # One screen, because payroll reads one source
//
// Approved leave becomes attendance on the server — "so payroll reads one
// source". Splitting the two across separate screens would let a manager grant
// a week off on one page and mark the same week present on another, and the
// run would quietly pay both. They are two tabs of the same thing here, and
// granting leave is visible from where the days are recorded.
//
// # A day, not a clock
//
// The route takes one row per person per day and upserts on that pair, because
// two rows would double-count the hours a payroll run reads. This screen edits
// a grid of days for that reason: the unit the server stores is the unit a
// person edits.
//
// # Absence is not free
//
// A day marked absent is docked from that month's pay — the payroll run
// computes it from exactly these rows. So the status control says what it
// costs rather than being a neutral dropdown, and nothing here is saved until
// somebody presses save.

import { CalendarCheck } from 'lucide-react';
import { Suspense, useMemo, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  ATTENDANCE_STATUS,
  daysBetween,
  leaveProblem,
  type AttendanceDay,
  type Employee,
  type LeaveProblem,
  type LeaveRequest,
} from '@/lib/people/staff';
import { useUrlState } from '@/lib/url-state';

const STATUS_LABEL: Record<string, Key> = {
  present: 'nx.att.present',
  absent: 'nx.att.absent',
  leave: 'nx.att.onLeave',
  holiday: 'nx.att.holiday',
  rest: 'nx.att.rest',
};

// Keyed by the closed union rather than by string, so adding a reason to
// leaveProblem without a sentence to show for it fails to compile instead of
// showing a person an empty refusal.
const LEAVE_PROBLEM: Record<Exclude<LeaveProblem, 'none'>, Key> = {
  no_employee: 'nx.att.needEmployee',
  no_kind: 'nx.att.needKind',
  no_dates: 'nx.att.needDates',
  backwards: 'nx.att.needForwards',
};

/** The kinds of leave. Free text on the wire; these are the usual ones. */
const LEAVE_KINDS = ['annual', 'sick', 'unpaid', 'maternity', 'hajj'] as const;

function monthBounds(month: string): { from: string; to: string } {
  const [y, m] = month.split('-').map(Number);
  if (!y || !m) return { from: '', to: '' };
  const last = new Date(y, m, 0).getDate();
  return { from: `${month}-01`, to: `${month}-${String(last).padStart(2, '0')}` };
}

function thisMonth(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

function AttendanceScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('hr.manage');

  // The tab lives in the URL, so a manager sending "look at the leave
  // requests" sends a link that opens on them.
  const [rawTab, setTab] = useUrlState('view', 'days');
  const tab: 'days' | 'leave' = rawTab === 'leave' ? 'leave' : 'days';
  const [month, setMonth] = useUrlState('month', thisMonth());
  const { from, to } = monthBounds(month);

  const staff = useApiList<Employee>(scope ? '/employees' : null, scope ?? undefined);
  const days = useApiList<AttendanceDay>(
    scope && from ? '/attendance' : null,
    scope ? { ...scope, from, to } : undefined,
  );
  const leave = useApiList<LeaveRequest>(scope ? '/leave' : null, scope ?? undefined);

  // --- recording a day ---------------------------------------------------
  const [employeeID, setEmployeeID] = useState('');
  const [onDate, setOnDate] = useState('');
  const [status, setStatus] = useState<string>('present');
  const [hours, setHours] = useState('8');
  const [overtime, setOvertime] = useState('0');
  const [lateMins, setLateMins] = useState('0');
  const [note, setNote] = useState('');

  // --- asking for leave --------------------------------------------------
  const [leaveEmployee, setLeaveEmployee] = useState('');
  const [kind, setKind] = useState<string>('annual');
  const [isPaid, setIsPaid] = useState(true);
  const [startsOn, setStartsOn] = useState('');
  const [endsOn, setEndsOn] = useState('');
  const [leaveDays, setLeaveDays] = useState('');
  const [reason, setReason] = useState('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const people = staff.data?.data ?? [];
  const rows = days.data?.data ?? [];
  const requests = leave.data?.data ?? [];
  const pending = requests.filter((r) => r.status === 'requested');

  const problem = leaveProblem({
    employeeID: leaveEmployee,
    kind,
    from: startsOn,
    to: endsOn,
  });

  // The default the person can overwrite: whether a weekend inside a holiday
  // counts is a company's own rule and this cannot know it.
  const suggested = useMemo(() => daysBetween(startsOn, endsOn), [startsOn, endsOn]);

  async function recordDay() {
    if (!scope || !employeeID || !onDate) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.post(`/attendance?company_id=${scope.company_id}`, {
        days: [
          {
            employee_id: employeeID,
            on_date: onDate,
            status,
            hours_worked: hours.trim(),
            overtime_hours: overtime.trim(),
            late_minutes: Number(lateMins) || 0,
            note: note.trim(),
          },
        ],
      });
      setNote('');
      void days.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function askForLeave() {
    if (!scope || problem !== 'none') return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.post(`/leave?company_id=${scope.company_id}`, {
        employee_id: leaveEmployee,
        kind,
        is_paid: isPaid,
        starts_on: startsOn,
        ends_on: endsOn,
        days: (leaveDays || suggested).trim(),
        reason: reason.trim(),
      });
      setReason('');
      setLeaveDays('');
      void leave.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function decide(id: string, approve: boolean) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`/leave/${id}/decision?company_id=${scope.company_id}`, {
        approve,
      });
      void leave.refetch();
      // Approved leave writes attendance, so the days behind this screen have
      // just changed too.
      void days.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const dayColumns: Column<AttendanceDay>[] = [
    {
      key: 'date',
      header: t('nx.att.colDate'),
      primary: true,
      width: 'w-32',
      cell: (d) => (
        <time dateTime={d.on_date} className="num">
          {d.on_date}
        </time>
      ),
    },
    {
      key: 'employee',
      header: t('nx.att.colEmployee'),
      cell: (d) => d.employee ?? '—',
    },
    {
      key: 'status',
      header: t('nx.att.colStatus'),
      width: 'w-32',
      cell: (d) =>
        d.status === 'absent' ? (
          <Badge tone="caution">{t(STATUS_LABEL[d.status] ?? 'nx.att.present')}</Badge>
        ) : (
          <span>{t(STATUS_LABEL[d.status] ?? 'nx.att.present')}</span>
        ),
    },
    {
      key: 'hours',
      header: t('nx.att.colHours'),
      numeric: true,
      width: 'w-24',
      cell: (d) => <span className="num">{d.hours_worked}</span>,
    },
    {
      key: 'overtime',
      header: t('nx.att.colOvertime'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (d) => <span className="num text-muted">{d.overtime_hours}</span>,
    },
    {
      key: 'late',
      header: t('nx.att.colLate'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (d) => <span className="num text-muted">{d.late_minutes}</span>,
    },
  ];

  const leaveColumns: Column<LeaveRequest>[] = [
    {
      key: 'employee',
      header: t('nx.att.colEmployee'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{r.employee ?? '—'}</span>
          <span className="text-caption text-muted">{r.kind}</span>
        </span>
      ),
    },
    {
      key: 'when',
      header: t('nx.att.colWhen'),
      width: 'w-52',
      cell: (r) => (
        <span className="num">
          {r.starts_on} — {r.ends_on}
        </span>
      ),
    },
    {
      key: 'days',
      header: t('nx.att.colDays'),
      numeric: true,
      width: 'w-24',
      cell: (r) => <span className="num">{r.days}</span>,
    },
    {
      key: 'paid',
      header: t('nx.att.colPaid'),
      width: 'w-28',
      cell: (r) =>
        r.is_paid ? (
          <span>{t('nx.att.paid')}</span>
        ) : (
          <span className="text-muted">{t('nx.att.unpaid')}</span>
        ),
    },
    {
      key: 'status',
      header: t('nx.att.colStatus'),
      width: 'w-56',
      cell: (r) => {
        if (r.status === 'requested') {
          return mayManage ? (
            <span className="flex gap-2">
              <Button size="sm" onClick={() => void decide(r.id, true)} disabled={busy}>
                {t('nx.att.grant')}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => void decide(r.id, false)}
                disabled={busy}
              >
                {t('nx.att.refuse')}
              </Button>
            </span>
          ) : (
            <Badge tone="caution">{t('nx.att.requested')}</Badge>
          );
        }
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={r.status === 'approved' ? 'positive' : 'neutral'}>
              {r.status === 'approved' ? t('nx.att.approved') : t('nx.att.refused')}
            </Badge>
            {r.decided_by ? (
              <span className="text-caption text-muted">{r.decided_by}</span>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.att.title')} description={t('nx.att.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <Tabs
        label={t('nx.att.tabsLabel')}
        value={tab}
        onChange={setTab}
        items={[
          { id: 'days', label: t('nx.att.tabDays') },
          {
            id: 'leave',
            label: t('nx.att.tabLeave'),
            badge: pending.length > 0 ? String(pending.length) : undefined,
          },
        ]}
      />

      {tab === 'days' ? (
        <TabPanel id="days">
          <div className="mb-5 flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-label text-muted">{t('nx.att.month')}</span>
              <Input
                type="month"
                value={month}
                onChange={(e) => setMonth(e.target.value)}
                className="w-auto"
              />
            </label>
          </div>

          {mayManage ? (
            <Panel
              className="mb-5"
              title={t('nx.att.recordTitle')}
              description={t('nx.att.recordHint')}
            >
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Field name="employee_id" label={t('nx.att.colEmployee')} required>
                  <Select
                    value={employeeID}
                    onChange={(e) => setEmployeeID(e.target.value)}
                  >
                    <option value="">{t('nx.att.choosePerson')}</option>
                    {people.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.full_name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field name="on_date" label={t('nx.att.colDate')} required>
                  <Input
                    type="date"
                    value={onDate}
                    onChange={(e) => setOnDate(e.target.value)}
                  />
                </Field>
                <Field
                  name="status"
                  label={t('nx.att.colStatus')}
                  hint={status === 'absent' ? t('nx.att.absentCosts') : undefined}
                >
                  <Select value={status} onChange={(e) => setStatus(e.target.value)}>
                    {ATTENDANCE_STATUS.map((s) => (
                      <option key={s} value={s}>
                        {t(STATUS_LABEL[s] ?? 'nx.att.present')}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field name="hours_worked" label={t('nx.att.colHours')}>
                  <Input
                    value={hours}
                    onChange={(e) => setHours(e.target.value)}
                    inputMode="decimal"
                    className="num text-end"
                  />
                </Field>
                <Field name="overtime_hours" label={t('nx.att.colOvertime')}>
                  <Input
                    value={overtime}
                    onChange={(e) => setOvertime(e.target.value)}
                    inputMode="decimal"
                    className="num text-end"
                  />
                </Field>
                <Field name="late_minutes" label={t('nx.att.colLate')}>
                  <Input
                    value={lateMins}
                    onChange={(e) => setLateMins(e.target.value)}
                    inputMode="numeric"
                    className="num text-end"
                  />
                </Field>
                <Field name="note" label={t('nx.att.note')} className="sm:col-span-2">
                  <Input value={note} onChange={(e) => setNote(e.target.value)} />
                </Field>
              </div>
              <div className="mt-4">
                <Button
                  variant="primary"
                  onClick={() => void recordDay()}
                  disabled={busy || !employeeID || !onDate}
                >
                  {t('nx.att.recordDay')}
                </Button>
              </div>
            </Panel>
          ) : null}

          {days.error ? (
            <ErrorState error={days.error} onRetry={() => void days.refetch()} />
          ) : null}
          {days.isLoading && !days.data ? <TableSkeleton columns={6} /> : null}

          {!days.isLoading && rows.length === 0 ? (
            <EmptyState
              icon={CalendarCheck}
              title={t('nx.att.emptyTitle')}
              description={t('nx.att.emptyDesc')}
            />
          ) : null}

          {rows.length > 0 ? (
            <DataTable
              caption={t('nx.att.caption')}
              columns={dayColumns}
              rows={rows}
              rowKey={(d) => d.id}
            />
          ) : null}
        </TabPanel>
      ) : (
        <TabPanel id="leave">
          <Panel
            className="mb-5"
            title={t('nx.att.askTitle')}
            description={t('nx.att.askHint')}
          >
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <Field name="employee_id" label={t('nx.att.colEmployee')} required>
                <Select
                  value={leaveEmployee}
                  onChange={(e) => setLeaveEmployee(e.target.value)}
                >
                  <option value="">{t('nx.att.choosePerson')}</option>
                  {people.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.full_name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="kind" label={t('nx.att.kind')} required>
                <Select value={kind} onChange={(e) => setKind(e.target.value)}>
                  {LEAVE_KINDS.map((k) => (
                    <option key={k} value={k}>
                      {t(`nx.att.kind.${k}` as Key)}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="is_paid" label={t('nx.att.colPaid')}>
                <Select
                  value={isPaid ? 'paid' : 'unpaid'}
                  onChange={(e) => setIsPaid(e.target.value === 'paid')}
                >
                  <option value="paid">{t('nx.att.paid')}</option>
                  <option value="unpaid">{t('nx.att.unpaid')}</option>
                </Select>
              </Field>
              <Field name="starts_on" label={t('nx.att.startsOn')} required>
                <Input
                  type="date"
                  value={startsOn}
                  onChange={(e) => setStartsOn(e.target.value)}
                />
              </Field>
              <Field name="ends_on" label={t('nx.att.endsOn')} required>
                <Input
                  type="date"
                  value={endsOn}
                  onChange={(e) => setEndsOn(e.target.value)}
                />
              </Field>
              <Field
                name="days"
                label={t('nx.att.colDays')}
                hint={suggested ? t('nx.att.daysHint', { days: suggested }) : undefined}
              >
                <Input
                  value={leaveDays}
                  onChange={(e) => setLeaveDays(e.target.value)}
                  placeholder={suggested}
                  inputMode="decimal"
                  className="num text-end"
                />
              </Field>
              <Field name="reason" label={t('nx.att.reason')} className="sm:col-span-2">
                <Textarea
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  rows={2}
                />
              </Field>
            </div>
            <div className="mt-4 flex items-center gap-3">
              <Button
                variant="primary"
                onClick={() => void askForLeave()}
                disabled={busy || problem !== 'none'}
              >
                {t('nx.att.ask')}
              </Button>
              {problem !== 'none' ? (
                <p className="text-caption text-muted">{t(LEAVE_PROBLEM[problem])}</p>
              ) : null}
            </div>
          </Panel>

          {leave.error ? (
            <ErrorState error={leave.error} onRetry={() => void leave.refetch()} />
          ) : null}
          {leave.isLoading && !leave.data ? <TableSkeleton columns={5} /> : null}

          {!leave.isLoading && requests.length === 0 ? (
            <EmptyState
              icon={CalendarCheck}
              title={t('nx.att.noLeaveTitle')}
              description={t('nx.att.noLeaveDesc')}
            />
          ) : null}

          {requests.length > 0 ? (
            <DataTable
              caption={t('nx.att.leaveCaption')}
              columns={leaveColumns}
              rows={requests}
              rowKey={(r) => r.id}
            />
          ) : null}
        </TabPanel>
      )}
    </>
  );
}

export default function AttendancePage() {
  return (
    <RequirePermission anyOf={['hr.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AttendanceScreen />
      </Suspense>
    </RequirePermission>
  );
}
