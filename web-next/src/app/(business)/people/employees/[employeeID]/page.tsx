'use client';

// One member of staff.
//
// # The record stays after they go
//
// `POST /employees/{id}/leaving` records a departure; it does not delete
// anybody. Their name is on payslips that were paid and on attendance that was
// worked, and a record removed leaves those pointing at nothing. So "left" is a
// state on this screen, not an absence from it.
//
// # Editing is a mode, not the default
//
// This is read most of the time — a manager checking when somebody's permit
// runs out, an accountant checking an IBAN before a run. A form that is always
// live invites a stray keystroke into a field nobody meant to touch, so the
// screen reads until somebody says they are changing it.
//
// # Pay again absent, not zero
//
// `hr.view_pay` decides whether the salary fields arrive at all. Where they did
// not, the panel says the reader is not shown pay rather than drawing empty
// boxes: a blank salary box that silently discards what you type into it is
// worse than no box.

import { useParams, useRouter } from 'next/navigation';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  documentState,
  maySeePay,
  monthlyPay,
  type Advance,
  type EOSBPosition,
  type Employee,
} from '@/lib/people/staff';

interface Store {
  id: string;
  name: string;
}

/** A read-only pair. Label above, so the eye reads what before which. */
function Detail({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-label text-muted">{label}</dt>
      <dd className="mt-0.5 text-body text-fg">{value || '—'}</dd>
    </div>
  );
}

function EmployeeScreen() {
  const t = useT();
  const router = useRouter();
  const params = useParams<{ employeeID: string }>();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('hr.manage');
  const mayPay = grants.can('hr.view_pay');

  const { data, isLoading, error, refetch } = useApi<Employee>(
    scope ? `/employees/${params.employeeID}` : null,
    scope ?? undefined,
  );
  const stores = useApiList<Store>(scope ? '/stores' : null, scope ?? undefined);
  const advances = useApiList<Advance>(
    scope && mayPay ? '/advances' : null,
    scope ?? undefined,
  );
  const eosb = useApiList<EOSBPosition>(
    scope && grants.can('payroll.view') ? '/eosb' : null,
    scope ?? undefined,
  );

  const [editing, setEditing] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [draft, setDraft] = useState<Partial<Employee>>({});
  const [leftOn, setLeftOn] = useState('');
  const [leaveReason, setLeaveReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // The draft starts as what is on file, so an edit that changes one field
  // does not blank the other twenty.
  useEffect(() => {
    if (data) setDraft(data);
  }, [data]);

  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  async function save() {
    if (!scope || !data) return;
    setBusy(true);
    setFormError(null);
    setFieldErrors({});
    try {
      await api.put(`/employees/${data.id}?company_id=${scope.company_id}`, {
        full_name: draft.full_name ?? '',
        name_ar: draft.name_ar ?? '',
        position: draft.position ?? '',
        department: draft.department ?? '',
        store_id: draft.store_id ?? '',
        phone: draft.phone ?? '',
        email: draft.email ?? '',
        nationality: draft.nationality ?? '',
        is_saudi: draft.is_saudi ?? false,
        national_id: draft.national_id ?? '',
        iqama_no: draft.iqama_no ?? '',
        id_expires_on: draft.id_expires_on ?? '',
        gosi_number: draft.gosi_number ?? '',
        qiwa_contract_no: draft.qiwa_contract_no ?? '',
        iban: draft.iban ?? '',
        bank_name: draft.bank_name ?? '',
        joined_on: draft.joined_on ?? '',
        // Only when this caller may hold them: the route says changing pay
        // needs hr.view_pay, and sending blanks would ask the server to
        // overwrite a salary with nothing.
        ...(mayPay && maySeePay(data)
          ? {
              basic_salary: draft.basic_salary ?? '',
              housing_allowance: draft.housing_allowance ?? '',
              transport_allowance: draft.transport_allowance ?? '',
              other_allowance: draft.other_allowance ?? '',
              commission_eligible: draft.commission_eligible ?? false,
            }
          : {}),
        notes: draft.notes ?? '',
      });
      setEditing(false);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function recordLeaving() {
    if (!scope || !data) return;
    setBusy(true);
    setFormError(null);
    try {
      await api.post(`/employees/${data.id}/leaving?company_id=${scope.company_id}`, {
        left_on: leftOn,
        reason: leaveReason.trim(),
      });
      setLeaving(false);
      void refetch();
    } catch (e) {
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading && !data) return <div className="h-64" aria-busy="true" />;
  if (!data) return null;

  const state = documentState(data);
  const pay = monthlyPay(data);
  const theirs = (advances.data?.data ?? []).filter(
    (a) => a.employee_id === data.id,
  );
  const position = (eosb.data?.data ?? []).find((p) => p.employee_id === data.id);

  const advanceColumns: Column<Advance>[] = [
    {
      key: 'no',
      header: t('nx.emp.advNo'),
      primary: true,
      width: 'w-36',
      cell: (a) => <span className="num">{a.advance_no}</span>,
    },
    {
      key: 'issued',
      header: t('nx.emp.advIssued'),
      secondary: true,
      width: 'w-32',
      cell: (a) => (
        <time dateTime={a.issued_on} className="num text-muted">
          {a.issued_on}
        </time>
      ),
    },
    {
      key: 'amount',
      header: t('nx.emp.advAmount'),
      numeric: true,
      width: 'w-32',
      cell: (a) => <span className="num">{money(a.amount, a.currency)}</span>,
    },
    {
      key: 'outstanding',
      header: t('nx.emp.advOutstanding'),
      numeric: true,
      width: 'w-32',
      cell: (a) => (
        <span className="num font-medium">{money(a.outstanding, a.currency)}</span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={data.full_name}
        description={t('nx.emp.subtitle', { number: data.employee_no })}
        actions={
          mayManage && !data.left_on ? (
            <>
              <Button onClick={() => setEditing((v) => !v)}>
                {editing ? t('nx.emp.stopEditing') : t('nx.emp.edit')}
              </Button>
              <Button variant="ghost" onClick={() => setLeaving((v) => !v)}>
                {t('nx.emp.recordLeaving')}
              </Button>
            </>
          ) : null
        }
      />

      <FormError message={formError} fields={fieldErrors} className="mb-4" />

      <div className="mb-5 flex flex-wrap items-center gap-2">
        {data.left_on ? (
          <Badge>{t('nx.emp.leftOn', { date: data.left_on })}</Badge>
        ) : (
          <Badge tone="positive">{t('nx.staff.active')}</Badge>
        )}
        {state === 'expired' ? (
          <Badge tone="critical">{t('nx.staff.expired')}</Badge>
        ) : null}
        {state === 'expiring' ? (
          <Badge tone="caution">{t('nx.staff.expiringSoon')}</Badge>
        ) : null}
        {data.commission_eligible ? (
          <Badge tone="info">{t('nx.emp.onCommission')}</Badge>
        ) : null}
      </div>

      {leaving ? (
        <Panel
          className="mb-5"
          title={t('nx.emp.leavingTitle')}
          description={t('nx.emp.leavingHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="left_on" label={t('nx.emp.leftOnLabel')}>
              <Input
                type="date"
                value={leftOn}
                onChange={(e) => setLeftOn(e.target.value)}
              />
            </Field>
            <Field name="reason" label={t('nx.emp.leavingReason')}>
              <Input
                value={leaveReason}
                onChange={(e) => setLeaveReason(e.target.value)}
              />
            </Field>
          </div>
          <div className="mt-4 flex gap-3">
            <Button variant="primary" onClick={() => void recordLeaving()} disabled={busy}>
              {t('nx.emp.confirmLeaving')}
            </Button>
            <Button variant="ghost" onClick={() => setLeaving(false)}>
              {t('nx.emp.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-5">
          <Panel title={t('nx.emp.who')}>
            {editing ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field
                  name="full_name"
                  label={t('nx.hire.fullName')}
                  error={fieldErrors.full_name}
                  className="sm:col-span-2"
                >
                  <Input
                    value={draft.full_name ?? ''}
                    onChange={(e) => setDraft({ ...draft, full_name: e.target.value })}
                  />
                </Field>
                <Field name="name_ar" label={t('nx.hire.nameAr')} error={fieldErrors.name_ar}>
                  <Input
                    value={draft.name_ar ?? ''}
                    onChange={(e) => setDraft({ ...draft, name_ar: e.target.value })}
                    dir="rtl"
                  />
                </Field>
                <Field name="position" label={t('nx.hire.position')}>
                  <Input
                    value={draft.position ?? ''}
                    onChange={(e) => setDraft({ ...draft, position: e.target.value })}
                  />
                </Field>
                <Field name="department" label={t('nx.hire.department')}>
                  <Input
                    value={draft.department ?? ''}
                    onChange={(e) => setDraft({ ...draft, department: e.target.value })}
                  />
                </Field>
                <Field name="store_id" label={t('nx.hire.store')}>
                  <Select
                    value={draft.store_id ?? ''}
                    onChange={(e) => setDraft({ ...draft, store_id: e.target.value })}
                  >
                    <option value="">{t('nx.hire.noStore')}</option>
                    {(stores.data?.data ?? []).map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field name="phone" label={t('nx.hire.phone')}>
                  <Input
                    type="tel"
                    value={draft.phone ?? ''}
                    onChange={(e) => setDraft({ ...draft, phone: e.target.value })}
                  />
                </Field>
                <Field name="email" label={t('nx.hire.email')}>
                  <Input
                    type="email"
                    value={draft.email ?? ''}
                    onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                  />
                </Field>
              </div>
            ) : (
              <dl className="grid gap-4 sm:grid-cols-2">
                <Detail label={t('nx.hire.nameAr')} value={data.name_ar} />
                <Detail label={t('nx.hire.position')} value={data.position} />
                <Detail label={t('nx.hire.department')} value={data.department} />
                <Detail label={t('nx.hire.store')} value={data.store_name} />
                <Detail label={t('nx.hire.phone')} value={data.phone} />
                <Detail label={t('nx.hire.email')} value={data.email} />
                <Detail
                  label={t('nx.hire.joinedOn')}
                  value={
                    <time dateTime={data.joined_on} className="num">
                      {data.joined_on}
                    </time>
                  }
                />
              </dl>
            )}
          </Panel>

          <Panel title={t('nx.hire.papers')}>
            {editing ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field name="nationality" label={t('nx.hire.nationality')}>
                  <Input
                    value={draft.nationality ?? ''}
                    onChange={(e) => setDraft({ ...draft, nationality: e.target.value })}
                    maxLength={2}
                    className="uppercase"
                  />
                </Field>
                <div className="flex items-end">
                  <Checkbox
                    label={t('nx.hire.isSaudi')}
                    checked={draft.is_saudi ?? false}
                    onChange={(e) => setDraft({ ...draft, is_saudi: e.target.checked })}
                  />
                </div>
                <Field name="national_id" label={t('nx.hire.nationalID')}>
                  <Input
                    value={draft.national_id ?? ''}
                    onChange={(e) => setDraft({ ...draft, national_id: e.target.value })}
                    className="num"
                  />
                </Field>
                <Field name="iqama_no" label={t('nx.hire.iqama')}>
                  <Input
                    value={draft.iqama_no ?? ''}
                    onChange={(e) => setDraft({ ...draft, iqama_no: e.target.value })}
                    className="num"
                  />
                </Field>
                <Field name="id_expires_on" label={t('nx.hire.idExpires')}>
                  <Input
                    type="date"
                    value={draft.id_expires_on ?? ''}
                    onChange={(e) =>
                      setDraft({ ...draft, id_expires_on: e.target.value })
                    }
                  />
                </Field>
                <Field name="gosi_number" label={t('nx.hire.gosiNumber')}>
                  <Input
                    value={draft.gosi_number ?? ''}
                    onChange={(e) => setDraft({ ...draft, gosi_number: e.target.value })}
                    className="num"
                  />
                </Field>
                <Field name="qiwa_contract_no" label={t('nx.hire.qiwa')}>
                  <Input
                    value={draft.qiwa_contract_no ?? ''}
                    onChange={(e) =>
                      setDraft({ ...draft, qiwa_contract_no: e.target.value })
                    }
                    className="num"
                  />
                </Field>
                <Field name="iban" label={t('nx.hire.iban')} className="sm:col-span-2">
                  <Input
                    value={draft.iban ?? ''}
                    onChange={(e) =>
                      setDraft({ ...draft, iban: e.target.value.toUpperCase() })
                    }
                    className="num"
                  />
                </Field>
                <Field name="bank_name" label={t('nx.hire.bankName')}>
                  <Input
                    value={draft.bank_name ?? ''}
                    onChange={(e) => setDraft({ ...draft, bank_name: e.target.value })}
                  />
                </Field>
              </div>
            ) : (
              <dl className="grid gap-4 sm:grid-cols-2">
                <Detail label={t('nx.hire.nationality')} value={data.nationality} />
                <Detail
                  label={t('nx.hire.isSaudi')}
                  value={data.is_saudi ? t('nx.emp.yes') : t('nx.emp.no')}
                />
                <Detail label={t('nx.hire.nationalID')} value={data.national_id} />
                <Detail label={t('nx.hire.iqama')} value={data.iqama_no} />
                <Detail
                  label={t('nx.hire.idExpires')}
                  value={
                    data.id_expires_on ? (
                      <time dateTime={data.id_expires_on} className="num">
                        {data.id_expires_on}
                      </time>
                    ) : null
                  }
                />
                <Detail label={t('nx.hire.gosiNumber')} value={data.gosi_number} />
                <Detail label={t('nx.hire.qiwa')} value={data.qiwa_contract_no} />
                <Detail
                  label={t('nx.hire.iban')}
                  value={<span className="num">{data.iban}</span>}
                />
                <Detail label={t('nx.hire.bankName')} value={data.bank_name} />
              </dl>
            )}
          </Panel>

          {editing ? (
            <div className="flex gap-3">
              <Button variant="primary" onClick={() => void save()} disabled={busy}>
                {busy ? t('nx.emp.saving') : t('nx.emp.save')}
              </Button>
              <Button
                variant="ghost"
                onClick={() => {
                  setDraft(data);
                  setEditing(false);
                }}
              >
                {t('nx.emp.cancel')}
              </Button>
            </div>
          ) : null}

          {mayPay && theirs.length > 0 ? (
            <Panel flush title={t('nx.emp.advances')} description={t('nx.emp.advancesHint')}>
              <DataTable
                caption={t('nx.emp.advances')}
                columns={advanceColumns}
                rows={theirs}
                rowKey={(a) => a.id}
                className="rounded-none border-0"
              />
            </Panel>
          ) : null}
        </div>

        <aside className="flex flex-col gap-5">
          {maySeePay(data) ? (
            <Panel title={t('nx.emp.pay')}>
              {editing ? (
                <div className="grid gap-4">
                  <Field name="basic_salary" label={t('nx.hire.basic')}>
                    <Input
                      value={draft.basic_salary ?? ''}
                      onChange={(e) =>
                        setDraft({ ...draft, basic_salary: e.target.value })
                      }
                      inputMode="decimal"
                      className="num text-end"
                    />
                  </Field>
                  <Field name="housing_allowance" label={t('nx.hire.housing')}>
                    <Input
                      value={draft.housing_allowance ?? ''}
                      onChange={(e) =>
                        setDraft({ ...draft, housing_allowance: e.target.value })
                      }
                      inputMode="decimal"
                      className="num text-end"
                    />
                  </Field>
                  <Field name="transport_allowance" label={t('nx.hire.transport')}>
                    <Input
                      value={draft.transport_allowance ?? ''}
                      onChange={(e) =>
                        setDraft({ ...draft, transport_allowance: e.target.value })
                      }
                      inputMode="decimal"
                      className="num text-end"
                    />
                  </Field>
                  <Field name="other_allowance" label={t('nx.hire.otherAllowance')}>
                    <Input
                      value={draft.other_allowance ?? ''}
                      onChange={(e) =>
                        setDraft({ ...draft, other_allowance: e.target.value })
                      }
                      inputMode="decimal"
                      className="num text-end"
                    />
                  </Field>
                  <Checkbox
                    label={t('nx.hire.commissionEligible')}
                    checked={draft.commission_eligible ?? false}
                    onChange={(e) =>
                      setDraft({ ...draft, commission_eligible: e.target.checked })
                    }
                  />
                </div>
              ) : (
                <>
                  {pay ? (
                    <Figure
                      label={t('nx.emp.monthly')}
                      value={money(pay, data.currency)}
                    />
                  ) : null}
                  <dl className="mt-4 grid gap-3">
                    <Detail
                      label={t('nx.hire.basic')}
                      value={
                        <span className="num">
                          {money(data.basic_salary ?? '0', data.currency)}
                        </span>
                      }
                    />
                    <Detail
                      label={t('nx.hire.housing')}
                      value={
                        <span className="num">
                          {money(data.housing_allowance ?? '0', data.currency)}
                        </span>
                      }
                    />
                    <Detail
                      label={t('nx.hire.transport')}
                      value={
                        <span className="num">
                          {money(data.transport_allowance ?? '0', data.currency)}
                        </span>
                      }
                    />
                    <Detail
                      label={t('nx.hire.otherAllowance')}
                      value={
                        <span className="num">
                          {money(data.other_allowance ?? '0', data.currency)}
                        </span>
                      }
                    />
                  </dl>
                </>
              )}
            </Panel>
          ) : (
            // Said plainly rather than drawn as empty boxes: a reader without
            // hr.view_pay should know the figures exist and are not theirs,
            // not be shown a salary of nothing.
            <Panel title={t('nx.emp.pay')}>
              <p className="max-w-prose text-body text-muted">
                {t('nx.emp.payHidden')}
              </p>
            </Panel>
          )}

          {position ? (
            <Panel title={t('nx.emp.eosb')} description={t('nx.emp.eosbHint')}>
              <Figure
                label={t('nx.emp.eosbAccrued')}
                value={money(position.accrued, position.currency)}
                caption={t('nx.emp.eosbMonths', { months: position.months_of_service })}
              />
            </Panel>
          ) : null}
        </aside>
      </div>

      <div className="mt-6">
        <Button variant="ghost" onClick={() => router.push('/people/employees')}>
          {t('nx.emp.backToDirectory')}
        </Button>
      </div>
    </>
  );
}

export default function EmployeePage() {
  return (
    <RequirePermission anyOf={['hr.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <EmployeeScreen />
      </Suspense>
    </RequirePermission>
  );
}
