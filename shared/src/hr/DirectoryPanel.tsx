// Who works here (blueprint C5).
//
// # A person who has left stays on the list, greyed
//
// Their end-of-service entitlement is computed from the leaving date, their
// payslips are still theirs, and an employment record that vanished when
// somebody resigned would take a year of payroll history with it. So leaving is
// a date, not a delete, and the row stays where anybody looking for it will
// find it.

import { useCallback, useState } from 'react';

import {
  addEmployee,
  listEmployees,
  recordLeaving,
  updateEmployee,
  type Employee,
  type EmployeeBody,
} from '../api/hr';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { localName, money, shortDate } from '../ui/format';

export function DirectoryPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('hr.manage');
  const maySeePay = can('hr.view_pay');

  const [search, setSearch] = useState('');
  const [includeLeft, setIncludeLeft] = useState(false);
  const [editing, setEditing] = useState<Employee | null>(null);
  const [adding, setAdding] = useState(false);
  const [leaving, setLeaving] = useState<Employee | null>(null);

  const load = useCallback(
    () => listEmployees(client, companyId, { search, include_left: includeLeft }),
    [client, companyId, search, includeLeft],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      {(adding || editing) && (
        <PersonForm
          companyId={companyId}
          existing={editing}
          onCancel={() => {
            setAdding(false);
            setEditing(null);
          }}
          onSaved={() => {
            setAdding(false);
            setEditing(null);
            reload();
          }}
        />
      )}

      {leaving && (
        <LeavingForm
          companyId={companyId}
          person={leaving}
          onCancel={() => setLeaving(null)}
          onRecorded={() => {
            setLeaving(null);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('hr.directory')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('hr.directory')}</h2>
          <div className="hr__actions">
            <TextInput
              id="hr-search"
              value={search}
              onChange={setSearch}
              placeholder={t('hr.searchPeople')}
            />
            <label className="hr__check">
              <input
                type="checkbox"
                checked={includeLeft}
                onChange={(e) => setIncludeLeft(e.target.checked)}
              />
              <span>{t('hr.showLeavers')}</span>
            </label>
            {mayManage && !adding && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setAdding(true)}
              >
                {t('hr.addPerson')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Employee[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('hr.nobodyTitle')}
                  body={t('hr.nobodyBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('hr.person')}</th>
                      <th scope="col">{t('hr.role')}</th>
                      <th scope="col">{t('hr.documents')}</th>
                      <th scope="col">{t('hr.joined')}</th>
                      {maySeePay && (
                        <th scope="col" className="num">
                          {t('hr.basicSalary')}
                        </th>
                      )}
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((p) => (
                      <tr
                        key={p.id}
                        className={p.left_on ? 'detail__row--aside' : undefined}
                      >
                        <td>
                          <span className="detail__strong">
                            {localName(locale, p.full_name, p.name_ar)}
                          </span>
                          <span className="ds-caption">
                            {p.employee_no}
                            {p.phone ? ` · ${p.phone}` : ''}
                          </span>
                        </td>
                        <td>
                          {p.position ?? '—'}
                          {p.department && (
                            <span className="ds-caption">{p.department}</span>
                          )}
                          {p.store_name && (
                            <span className="ds-caption">{p.store_name}</span>
                          )}
                        </td>
                        <td>
                          {/* An expired residency permit stops somebody
                              working. It is a legal fact, not a reminder. */}
                          {p.id_expired ? (
                            <span className="ds-badge ds-badge--danger">
                              {t('hr.idExpired')}
                            </span>
                          ) : p.id_expiring_soon ? (
                            <span className="ds-badge ds-badge--warning">
                              {t('hr.idExpiringSoon')}
                            </span>
                          ) : p.id_expires_on ? (
                            <span className="ds-caption">
                              {shortDate(p.id_expires_on, locale)}
                            </span>
                          ) : (
                            <span aria-hidden="true">—</span>
                          )}
                          {p.is_saudi && (
                            <span className="ds-caption">{t('hr.saudi')}</span>
                          )}
                        </td>
                        <td>
                          {shortDate(p.joined_on, locale)}
                          {p.left_on && (
                            <span className="ds-caption">
                              {t('hr.leftOn', {
                                date: shortDate(p.left_on, locale),
                              })}
                            </span>
                          )}
                        </td>
                        {maySeePay && (
                          <td className="num">
                            {/* Absent, not zero: a caller without the pay
                                permission gets no field at all, and a dash is
                                the honest rendering of that. */}
                            {p.basic_salary
                              ? money(p.basic_salary, { currency: p.currency })
                              : '—'}
                          </td>
                        )}
                        <td>
                          {mayManage && !p.left_on && (
                            <div className="hr__rowactions">
                              <button
                                className="ds-btn ds-btn--quiet"
                                onClick={() => setEditing(p)}
                              >
                                {t('action.edit')}
                              </button>
                              <button
                                className="ds-btn ds-btn--quiet"
                                onClick={() => setLeaving(p)}
                              >
                                {t('hr.recordLeaving')}
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

function PersonForm({
  companyId,
  existing,
  onCancel,
  onSaved,
}: {
  companyId: string;
  existing: Employee | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const maySeePay = can('hr.view_pay');

  const [form, setForm] = useState<EmployeeBody>(() => ({
    full_name: existing?.full_name ?? '',
    name_ar: existing?.name_ar ?? '',
    employee_no: existing?.employee_no ?? '',
    phone: existing?.phone ?? '',
    email: existing?.email ?? '',
    position: existing?.position ?? '',
    department: existing?.department ?? '',
    national_id: existing?.national_id ?? '',
    iqama_no: existing?.iqama_no ?? '',
    id_expires_on: existing?.id_expires_on ?? '',
    gosi_number: existing?.gosi_number ?? '',
    nationality: existing?.nationality ?? '',
    is_saudi: existing?.is_saudi ?? false,
    iban: existing?.iban ?? '',
    bank_name: existing?.bank_name ?? '',
    joined_on: existing?.joined_on ?? today(),
    basic_salary: existing?.basic_salary ?? '',
    housing_allowance: existing?.housing_allowance ?? '',
    transport_allowance: existing?.transport_allowance ?? '',
    commission_eligible: existing?.commission_eligible ?? false,
  }));
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const set = (patch: Partial<EmployeeBody>) =>
    setForm((prev) => ({ ...prev, ...patch }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const body: EmployeeBody = { ...form };
      // Blank strings are absent, not empty values. A phone number cleared to
      // "" should remove the number rather than store two quotation marks.
      for (const key of Object.keys(body) as Array<keyof EmployeeBody>) {
        if (body[key] === '') delete body[key];
      }
      if (existing) await updateEmployee(client, companyId, existing.id, body);
      else await addEmployee(client, companyId, body);
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel hr__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">
          {existing ? t('hr.editPerson') : t('hr.addPerson')}
        </h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('hr.fullName')} htmlFor="p-name" required>
            <TextInput
              id="p-name"
              value={form.full_name}
              onChange={(v) => set({ full_name: v })}
            />
          </Field>
          <Field label={t('hr.nameAr')} htmlFor="p-namear">
            <TextInput
              id="p-namear"
              value={form.name_ar ?? ''}
              onChange={(v) => set({ name_ar: v })}
            />
          </Field>
          <Field
            label={t('hr.employeeNo')}
            hint={t('hr.employeeNoHint')}
            htmlFor="p-no"
          >
            <TextInput
              id="p-no"
              value={form.employee_no ?? ''}
              onChange={(v) => set({ employee_no: v })}
            />
          </Field>
          <Field label={t('hr.role')} htmlFor="p-position">
            <TextInput
              id="p-position"
              value={form.position ?? ''}
              onChange={(v) => set({ position: v })}
            />
          </Field>
          <Field label={t('hr.department')} htmlFor="p-dept">
            <TextInput
              id="p-dept"
              value={form.department ?? ''}
              onChange={(v) => set({ department: v })}
            />
          </Field>
          <Field label={t('common.phone')} htmlFor="p-phone">
            <TextInput
              id="p-phone"
              value={form.phone ?? ''}
              onChange={(v) => set({ phone: v })}
              inputMode="tel"
            />
          </Field>
          <Field label={t('common.email')} htmlFor="p-email">
            <TextInput
              id="p-email"
              value={form.email ?? ''}
              onChange={(v) => set({ email: v })}
            />
          </Field>
          <Field label={t('hr.joined')} htmlFor="p-joined" required>
            <input
              id="p-joined"
              type="date"
              className="field__input"
              value={form.joined_on ?? ''}
              onChange={(e) => set({ joined_on: e.target.value })}
            />
          </Field>

          <Field label={t('hr.nationalId')} htmlFor="p-nid">
            <TextInput
              id="p-nid"
              value={form.national_id ?? ''}
              onChange={(v) => set({ national_id: v })}
            />
          </Field>
          <Field
            label={t('hr.iqama')}
            hint={t('hr.iqamaHint')}
            htmlFor="p-iqama"
          >
            <TextInput
              id="p-iqama"
              value={form.iqama_no ?? ''}
              onChange={(v) => set({ iqama_no: v })}
            />
          </Field>
          <Field
            label={t('hr.idExpires')}
            hint={t('hr.idExpiresHint')}
            htmlFor="p-idexp"
          >
            <input
              id="p-idexp"
              type="date"
              className="field__input"
              value={form.id_expires_on ?? ''}
              onChange={(e) => set({ id_expires_on: e.target.value })}
            />
          </Field>
          <Field label={t('hr.gosiNumber')} htmlFor="p-gosi">
            <TextInput
              id="p-gosi"
              value={form.gosi_number ?? ''}
              onChange={(v) => set({ gosi_number: v })}
            />
          </Field>
          <Field label={t('hr.nationality')} htmlFor="p-nat">
            <TextInput
              id="p-nat"
              value={form.nationality ?? ''}
              onChange={(v) => set({ nationality: v })}
            />
          </Field>
          <Field
            label={t('hr.iban')}
            hint={t('hr.ibanHint')}
            htmlFor="p-iban"
          >
            <TextInput
              id="p-iban"
              value={form.iban ?? ''}
              onChange={(v) => set({ iban: v })}
            />
          </Field>

          {maySeePay && (
            <>
              <Field label={t('hr.basicSalary')} htmlFor="p-basic">
                <TextInput
                  id="p-basic"
                  value={form.basic_salary ?? ''}
                  onChange={(v) => set({ basic_salary: v })}
                  inputMode="decimal"
                />
              </Field>
              <Field label={t('hr.housing')} htmlFor="p-housing">
                <TextInput
                  id="p-housing"
                  value={form.housing_allowance ?? ''}
                  onChange={(v) => set({ housing_allowance: v })}
                  inputMode="decimal"
                />
              </Field>
              <Field label={t('hr.transport')} htmlFor="p-transport">
                <TextInput
                  id="p-transport"
                  value={form.transport_allowance ?? ''}
                  onChange={(v) => set({ transport_allowance: v })}
                  inputMode="decimal"
                />
              </Field>
            </>
          )}
        </div>

        <div className="hr__checks">
          <label className="hr__check">
            <input
              type="checkbox"
              checked={form.is_saudi ?? false}
              onChange={(e) => set({ is_saudi: e.target.checked })}
            />
            {/* Not a formality: GOSI is charged at different rates for a Saudi
                national and an expatriate, so payroll reads this. */}
            <span>{t('hr.isSaudi')}</span>
          </label>
          {maySeePay && (
            <label className="hr__check">
              <input
                type="checkbox"
                checked={form.commission_eligible ?? false}
                onChange={(e) => set({ commission_eligible: e.target.checked })}
              />
              <span>{t('hr.commissionEligible')}</span>
            </label>
          )}
        </div>

        <FormActions
          submitLabel={existing ? t('action.saveChanges') : t('hr.addPerson')}
          busy={busy}
          disabled={form.full_name.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

// LeavingForm records a leaving date.
//
// The date is what end-of-service is computed from — C6's entitlement runs on
// months of service — so it is asked for rather than assumed to be today: a
// resignation recorded three weeks late would otherwise overpay.
function LeavingForm({
  companyId,
  person,
  onCancel,
  onRecorded,
}: {
  companyId: string;
  person: Employee;
  onCancel: () => void;
  onRecorded: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [date, setDate] = useState(today());
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await recordLeaving(client, companyId, person.id, {
        left_on: date,
        reason,
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
        <h2 className="ds-h3">
          {t('hr.recordLeavingFor', { name: person.full_name })}
        </h2>
        <p className="ds-caption">{t('hr.leavingHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('hr.lastDay')} htmlFor="leave-date" required>
            <input
              id="leave-date"
              type="date"
              className="field__input"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </Field>
          <Field label={t('hr.leavingReason')} htmlFor="leave-reason">
            <TextInput id="leave-reason" value={reason} onChange={setReason} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('hr.recordLeaving')}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}
