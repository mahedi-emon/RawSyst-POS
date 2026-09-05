'use client';

// Taking somebody on.
//
// # Grouped by who needs it, not by what the table looks like
//
// The record has twenty-odd fields and they are filled by different people at
// different moments: a manager knows the name and the job on day one, HR adds
// the residency permit when it arrives, finance adds the bank details before
// the first run. One long column of twenty boxes makes all three look equally
// urgent and equally overdue, so they are four panels and only the first is
// required to save.
//
// # Pay is here only for somebody who may see pay
//
// `POST /employees` accepts the salary fields but the route says setting them
// "additionally needs hr.view_pay". Offering the boxes to somebody who cannot
// hold them collects a number that is then silently dropped, and the person
// walks away believing the salary is recorded. So the panel is not rendered.
//
// # No rate, no allowance, no deduction is invented here
//
// Social insurance, end-of-service, the wage-file layout — every legal value
// comes from the regulatory register on the server. This form collects facts
// about a person and nothing else.

import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import type { Employee } from '@/lib/people/staff';

interface Store {
  id: string;
  name: string;
}

function today() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
    d.getDate(),
  ).padStart(2, '0')}`;
}

function HireScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const grants = useGrants();
  const maySeePay = grants.can('hr.view_pay');

  const [fullName, setFullName] = useState('');
  const [nameAr, setNameAr] = useState('');
  const [position, setPosition] = useState('');
  const [department, setDepartment] = useState('');
  const [storeID, setStoreID] = useState('');
  const [joinedOn, setJoinedOn] = useState(today);
  const [phone, setPhone] = useState('');
  const [email, setEmail] = useState('');

  const [nationality, setNationality] = useState('');
  const [isSaudi, setIsSaudi] = useState(false);
  const [nationalID, setNationalID] = useState('');
  const [iqamaNo, setIqamaNo] = useState('');
  const [idExpiresOn, setIDExpiresOn] = useState('');
  const [gosiNumber, setGOSINumber] = useState('');
  const [qiwaContract, setQiwaContract] = useState('');

  const [iban, setIBAN] = useState('');
  const [bankName, setBankName] = useState('');

  const [basic, setBasic] = useState('');
  const [housing, setHousing] = useState('');
  const [transport, setTransport] = useState('');
  const [otherAllowance, setOtherAllowance] = useState('');
  const [commissionEligible, setCommissionEligible] = useState(false);
  const [notes, setNotes] = useState('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const stores = useApiList<Store>(scope ? '/stores' : null, scope ?? undefined);

  const ready = fullName.trim() !== '' && joinedOn !== '';

  async function hire() {
    if (!scope || !ready) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<Employee>(
        `/employees?company_id=${scope.company_id}`,
        {
          full_name: fullName.trim(),
          name_ar: nameAr.trim(),
          position: position.trim(),
          department: department.trim(),
          store_id: storeID,
          joined_on: joinedOn,
          phone: phone.trim(),
          email: email.trim(),
          nationality: nationality.trim(),
          is_saudi: isSaudi,
          national_id: nationalID.trim(),
          iqama_no: iqamaNo.trim(),
          id_expires_on: idExpiresOn,
          gosi_number: gosiNumber.trim(),
          qiwa_contract_no: qiwaContract.trim(),
          iban: iban.trim(),
          bank_name: bankName.trim(),
          // Sent only when this caller may hold them. The server would drop
          // them anyway; not sending is the honest version of the same rule.
          ...(maySeePay
            ? {
                basic_salary: basic.trim(),
                housing_allowance: housing.trim(),
                transport_allowance: transport.trim(),
                other_allowance: otherAllowance.trim(),
                commission_eligible: commissionEligible,
              }
            : {}),
          notes: notes.trim(),
        },
      );
      router.push(`/people/employees/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.hire.title')} description={t('nx.hire.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <div className="flex max-w-3xl flex-col gap-5">
        <Panel title={t('nx.hire.who')} description={t('nx.hire.whoHint')}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="full_name"
              label={t('nx.hire.fullName')}
              error={fieldErrors.full_name}
              required
              className="sm:col-span-2"
            >
              <Input value={fullName} onChange={(e) => setFullName(e.target.value)} />
            </Field>
            <Field
              name="name_ar"
              label={t('nx.hire.nameAr')}
              hint={t('nx.hire.nameArHint')}
              error={fieldErrors.name_ar}
              className="sm:col-span-2"
            >
              <Input
                value={nameAr}
                onChange={(e) => setNameAr(e.target.value)}
                dir="rtl"
              />
            </Field>
            <Field name="position" label={t('nx.hire.position')} error={fieldErrors.position}>
              <Input value={position} onChange={(e) => setPosition(e.target.value)} />
            </Field>
            <Field
              name="department"
              label={t('nx.hire.department')}
              error={fieldErrors.department}
            >
              <Input
                value={department}
                onChange={(e) => setDepartment(e.target.value)}
              />
            </Field>
            <Field name="store_id" label={t('nx.hire.store')} error={fieldErrors.store_id}>
              <Select value={storeID} onChange={(e) => setStoreID(e.target.value)}>
                <option value="">{t('nx.hire.noStore')}</option>
                {(stores.data?.data ?? []).map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="joined_on"
              label={t('nx.hire.joinedOn')}
              hint={t('nx.hire.joinedOnHint')}
              error={fieldErrors.joined_on}
              required
            >
              <Input
                type="date"
                value={joinedOn}
                onChange={(e) => setJoinedOn(e.target.value)}
              />
            </Field>
            <Field name="phone" label={t('nx.hire.phone')} error={fieldErrors.phone}>
              <Input
                type="tel"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
              />
            </Field>
            <Field name="email" label={t('nx.hire.email')} error={fieldErrors.email}>
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
          </div>
        </Panel>

        <Panel title={t('nx.hire.papers')} description={t('nx.hire.papersHint')}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="nationality"
              label={t('nx.hire.nationality')}
              error={fieldErrors.nationality}
            >
              <Input
                value={nationality}
                onChange={(e) => setNationality(e.target.value)}
                maxLength={2}
                placeholder="SA"
                className="uppercase"
              />
            </Field>
            <div className="flex items-end">
              <Checkbox
                label={t('nx.hire.isSaudi')}
                hint={t('nx.hire.isSaudiHint')}
                checked={isSaudi}
                onChange={(e) => setIsSaudi(e.target.checked)}
              />
            </div>
            <Field
              name="national_id"
              label={t('nx.hire.nationalID')}
              error={fieldErrors.national_id}
            >
              <Input
                value={nationalID}
                onChange={(e) => setNationalID(e.target.value)}
                inputMode="numeric"
                className="num"
              />
            </Field>
            <Field name="iqama_no" label={t('nx.hire.iqama')} error={fieldErrors.iqama_no}>
              <Input
                value={iqamaNo}
                onChange={(e) => setIqamaNo(e.target.value)}
                inputMode="numeric"
                className="num"
              />
            </Field>
            <Field
              name="id_expires_on"
              label={t('nx.hire.idExpires')}
              hint={t('nx.hire.idExpiresHint')}
              error={fieldErrors.id_expires_on}
            >
              <Input
                type="date"
                value={idExpiresOn}
                onChange={(e) => setIDExpiresOn(e.target.value)}
              />
            </Field>
            <Field
              name="gosi_number"
              label={t('nx.hire.gosiNumber')}
              hint={t('nx.hire.gosiNumberHint')}
              error={fieldErrors.gosi_number}
            >
              <Input
                value={gosiNumber}
                onChange={(e) => setGOSINumber(e.target.value)}
                inputMode="numeric"
                className="num"
              />
            </Field>
            <Field
              name="qiwa_contract_no"
              label={t('nx.hire.qiwa')}
              error={fieldErrors.qiwa_contract_no}
            >
              <Input
                value={qiwaContract}
                onChange={(e) => setQiwaContract(e.target.value)}
                className="num"
              />
            </Field>
          </div>
        </Panel>

        <Panel title={t('nx.hire.bank')} description={t('nx.hire.bankHint')}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="iban"
              label={t('nx.hire.iban')}
              error={fieldErrors.iban}
              className="sm:col-span-2"
            >
              <Input
                value={iban}
                onChange={(e) => setIBAN(e.target.value.toUpperCase())}
                className="num"
              />
            </Field>
            <Field name="bank_name" label={t('nx.hire.bankName')} error={fieldErrors.bank_name}>
              <Input value={bankName} onChange={(e) => setBankName(e.target.value)} />
            </Field>
          </div>
        </Panel>

        {maySeePay ? (
          <Panel title={t('nx.hire.pay')} description={t('nx.hire.payHint')}>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                name="basic_salary"
                label={t('nx.hire.basic')}
                error={fieldErrors.basic_salary}
              >
                <Input
                  value={basic}
                  onChange={(e) => setBasic(e.target.value)}
                  inputMode="decimal"
                  className="num text-end"
                />
              </Field>
              <Field
                name="housing_allowance"
                label={t('nx.hire.housing')}
                error={fieldErrors.housing_allowance}
              >
                <Input
                  value={housing}
                  onChange={(e) => setHousing(e.target.value)}
                  inputMode="decimal"
                  className="num text-end"
                />
              </Field>
              <Field
                name="transport_allowance"
                label={t('nx.hire.transport')}
                error={fieldErrors.transport_allowance}
              >
                <Input
                  value={transport}
                  onChange={(e) => setTransport(e.target.value)}
                  inputMode="decimal"
                  className="num text-end"
                />
              </Field>
              <Field
                name="other_allowance"
                label={t('nx.hire.otherAllowance')}
                error={fieldErrors.other_allowance}
              >
                <Input
                  value={otherAllowance}
                  onChange={(e) => setOtherAllowance(e.target.value)}
                  inputMode="decimal"
                  className="num text-end"
                />
              </Field>
              <div className="sm:col-span-2">
                <Checkbox
                  label={t('nx.hire.commissionEligible')}
                  hint={t('nx.hire.commissionEligibleHint')}
                  checked={commissionEligible}
                  onChange={(e) => setCommissionEligible(e.target.checked)}
                />
              </div>
            </div>
          </Panel>
        ) : null}

        <Panel title={t('nx.hire.notes')}>
          <Field name="notes" label={t('nx.hire.notesLabel')} error={fieldErrors.notes}>
            <Textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={3}
            />
          </Field>
        </Panel>

        <div className="flex items-center gap-3">
          <Button variant="primary" onClick={() => void hire()} disabled={!ready || busy}>
            {busy ? t('nx.hire.saving') : t('nx.hire.save')}
          </Button>
          <Button variant="ghost" onClick={() => router.back()}>
            {t('nx.hire.cancel')}
          </Button>
        </div>
      </div>
    </>
  );
}

export default function HirePage() {
  return (
    <RequirePermission anyOf={['hr.manage']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <HireScreen />
      </Suspense>
    </RequirePermission>
  );
}
