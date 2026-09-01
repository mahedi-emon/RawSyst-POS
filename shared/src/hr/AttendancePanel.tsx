// Attendance (blueprint C5).
//
// # A day is recorded once, per person
//
// Marking somebody present twice for a Tuesday is not two facts, and the
// server treats a second write for the same day as a correction of the first.
// The screen shows the day it is looking at rather than an endless list,
// because "who was in on the 14th" is the question people actually ask.

import { useCallback, useState } from 'react';

import {
  listAttendance,
  listEmployees,
  recordAttendance,
  type Attendance,
  type Employee,
} from '../api/hr';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, SelectInput, TextInput } from '../ui/Form';

const STATUSES = ['present', 'absent', 'late', 'leave', 'holiday'] as const;

export function AttendancePanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();

  const mayManage = can('hr.manage');
  const [day, setDay] = useState(today());
  const [recording, setRecording] = useState(false);

  const load = useCallback(
    () => listAttendance(client, companyId, { from: day, to: day }),
    [client, companyId, day],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      {recording && (
        <AttendanceForm
          companyId={companyId}
          day={day}
          onCancel={() => setRecording(false)}
          onRecorded={() => {
            setRecording(false);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('hr.attendance')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('hr.attendance')}</h2>
          <div className="hr__actions">
            <input
              type="date"
              className="field__input"
              aria-label={t('hr.day')}
              value={day}
              onChange={(e) => setDay(e.target.value)}
            />
            {mayManage && !recording && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setRecording(true)}
              >
                {t('hr.recordAttendance')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Attendance[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('hr.noAttendanceTitle')}
                  body={t('hr.noAttendanceBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('hr.person')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('hr.hours')}
                      </th>
                      <th scope="col" className="num">
                        {t('hr.overtime')}
                      </th>
                      <th scope="col" className="num">
                        {t('hr.lateBy')}
                      </th>
                      <th scope="col">{t('common.note')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((a) => (
                      <tr key={a.id}>
                        <td>{a.employee}</td>
                        <td>
                          <span className={`ds-badge ds-badge--${badgeFor(a.status)}`}>
                            {t(`hr.status.${a.status}` as Key)}
                          </span>
                        </td>
                        <td className="num">{a.hours_worked}</td>
                        <td className="num">
                          {a.overtime_hours === '0' ? '—' : a.overtime_hours}
                        </td>
                        <td className="num">
                          {a.late_minutes > 0
                            ? t('hr.minutes', { count: String(a.late_minutes) })
                            : '—'}
                        </td>
                        <td>{a.note ?? '—'}</td>
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

function badgeFor(status: string): string {
  switch (status) {
    case 'present':
      return 'success';
    case 'absent':
      return 'danger';
    case 'late':
      return 'warning';
    default:
      return 'neutral';
  }
}

function AttendanceForm({
  companyId,
  day,
  onCancel,
  onRecorded,
}: {
  companyId: string;
  day: string;
  onCancel: () => void;
  onRecorded: () => void;
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
  const [status, setStatus] = useState<string>('present');
  const [hours, setHours] = useState('8');
  const [overtime, setOvertime] = useState('0');
  const [late, setLate] = useState('0');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await recordAttendance(client, companyId, {
        employee_id: employeeID,
        on_date: day,
        status,
        hours_worked: hours,
        overtime_hours: overtime,
        late_minutes: Number(late) || 0,
        note,
      });
      onRecorded();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel hr__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('hr.recordAttendance')}</h2>
        <p className="ds-caption">{t('hr.attendanceHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('hr.person')} htmlFor="att-person" required>
            <SelectInput
              id="att-person"
              value={employeeID}
              onChange={setEmployeeID}
              options={people}
              label={(p) => `${p.full_name} (${p.employee_no})`}
              placeholder={t('hr.choosePerson')}
            />
          </Field>
          <Field label={t('common.status')} htmlFor="att-status" required>
            <SelectInput
              id="att-status"
              value={status}
              onChange={setStatus}
              options={STATUSES.map((s) => ({ id: s }))}
              label={(s) => t(`hr.status.${s.id}` as Key)}
            />
          </Field>
          <Field label={t('hr.hours')} htmlFor="att-hours">
            <TextInput
              id="att-hours"
              value={hours}
              onChange={setHours}
              inputMode="decimal"
            />
          </Field>
          <Field label={t('hr.overtime')} htmlFor="att-ot">
            <TextInput
              id="att-ot"
              value={overtime}
              onChange={setOvertime}
              inputMode="decimal"
            />
          </Field>
          <Field label={t('hr.lateBy')} htmlFor="att-late">
            <TextInput
              id="att-late"
              value={late}
              onChange={setLate}
              inputMode="numeric"
            />
          </Field>
          <Field label={t('common.note')} htmlFor="att-note">
            <TextInput id="att-note" value={note} onChange={setNote} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('hr.recordAttendance')}
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
