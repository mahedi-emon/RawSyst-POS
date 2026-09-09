'use client';

// First-time setup, as blueprint A5 describes it.
//
// # Why a wizard, when every one of these settings has a screen already
//
// A5's whole point is that a non-technical shop owner completes it alone. The
// settings screens exist and each is correct; finding six of them in the right
// order is not something anybody does on their first morning with a product.
// Worse, until a company exists most of those screens have nothing to show:
// `useCompanyScope` returns null, no request fires, and a brand-new business
// opens onto an empty dashboard with no way to find out why.
//
// # The steps are the backend's, and so is the order
//
// `onboarding_step` is an enum in the database and `CompleteStep` refuses a
// step that is not the current one. This screen offers one at a time for that
// reason and not as a matter of taste: entering opening balances before the
// chart of accounts exists would fail as a confusing error rather than as a
// missing prerequisite.
//
// # Resumable, because the server holds the answers
//
// Every step is written to `PUT /onboarding/steps/{step}` before it is
// completed, and the route accepts a half-finished one on purpose — "an Owner
// filling in a form on a phone between customers should not lose their work to
// a validation error". Closing the laptop loses nothing. There is no local
// draft that outlives a refresh, because a wizard remembering more than the
// server would show somebody answers that were never saved.
//
// # Completing a step is not the same as committing it
//
// The wizard's JSONB is scratch space. Two routes turn it into real records —
// `POST /onboarding/company` creates the company that owns the books, and
// `POST /onboarding/stores` creates the branches and the stock location each
// one needs. Both are called here, and the company one only when no company
// exists: calling it twice would create a second company, or be refused by the
// plan ceiling.
//
// # Optional stays optional
//
// Employees, hardware and opening balances are optional in `validateStep` and
// they are optional here. A single-person shop is a real customer, hardware can
// be paired later from the terminal, and a new business legitimately starts
// with no opening balances. Each of those steps says so and offers the screen
// that does the work, rather than reimplementing it inside a wizard.
//
// # And once it is finished it stops asking
//
// A completed setup shows what was answered and a way back to the product. The
// banner in the shell disappears. Nothing forces this screen on anybody twice.

import { ArrowRight, Check, Plus, Trash2 } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList, type CompanyRecord } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  answersFor,
  businessProblem,
  emptyStore,
  isDone,
  likelyCurrency,
  needsCompany,
  storeProblems,
  storesProblem,
  stepsDone,
  timezonesFor,
  COUNTRIES,
  CURRENCIES,
  EMPTY_BUSINESS,
  STEPS,
  STEPS_TO_DO,
  type BusinessAnswers,
  type Progress,
  type Step,
  type StoreAnswers,
} from '@/lib/setup/wizard';
import { cn } from '@/lib/utils';

const STEP_LABEL: Record<Step, Key> = {
  business_info: 'nx.setup.stepBusiness',
  stores: 'nx.setup.stepStores',
  tax: 'nx.setup.stepTax',
  employees: 'nx.setup.stepPeople',
  hardware: 'nx.setup.stepHardware',
  opening_balances: 'nx.setup.stepBalances',
  finished: 'nx.setup.stepDone',
};

const COUNTRY_LABEL: Record<string, Key> = {
  sa: 'nx.setup.countrySA',
  bd: 'nx.setup.countryBD',
  us: 'nx.setup.countryUS',
};

const BUSINESS_PROBLEM: Record<string, Key> = {
  no_legal_name: 'nx.setup.needLegalName',
  no_country: 'nx.setup.needCountry',
  unsupported_country: 'nx.setup.badCountry',
  no_currency: 'nx.setup.needCurrency',
  unsupported_currency: 'nx.setup.badCurrency',
  no_vat_number: 'nx.setup.needVatNumber',
};

const STORES_PROBLEM: Record<string, Key> = {
  none_added: 'nx.setup.needOneStore',
  duplicate_code: 'nx.setup.duplicateCode',
  incomplete: 'nx.setup.storeIncomplete',
};

const FIELD_PROBLEM: Record<string, Key> = {
  no_name: 'nx.setup.storeNeedsName',
  no_code: 'nx.setup.storeNeedsCode',
  required: 'nx.setup.fromNationalAddress',
  four_digits: 'nx.setup.exactlyFourDigits',
  five_digits: 'nx.setup.exactlyFiveDigits',
  two_letters: 'nx.setup.twoLetterCode',
};

/** What the tax step records. The company commit reads both. */
interface TaxAnswers {
  zatca_wave: string;
  zatca_deadline: string;
  acknowledged: boolean;
}

/** What the optional steps record: that somebody looked, and what they decided. */
interface NoteAnswers {
  note: string;
  skipped: boolean;
}

function SetupScreen() {
  const t = useT();
  const mayEdit = useGrants().can('identity.edit');

  const progress = useApi<Progress>('/onboarding', undefined, {
    // Setup is a sequence of writes this screen makes itself, so nothing else
    // moves it. Re-read after every step rather than on a timer.
    staleTime: 0,
  });
  const companies = useApiList<CompanyRecord>('/companies', undefined, {
    staleTime: 0,
  });

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  const [businessDraft, setBusinessDraft] = useState<BusinessAnswers>(EMPTY_BUSINESS);
  const [storeDrafts, setStoreDrafts] = useState<StoreAnswers[]>([]);
  const [taxDraft, setTaxDraft] = useState<TaxAnswers>({
    zatca_wave: '',
    zatca_deadline: '',
    acknowledged: false,
  });
  const [peopleDraft, setPeopleDraft] = useState<NoteAnswers>({ note: '', skipped: false });

  const state = progress.data;
  const company = companies.data?.data?.[0] ?? null;
  const current = (state?.current_step ?? 'business_info') as Step;

  // The answers the server already holds, loaded into the forms once they
  // arrive. Not merged on every render: that would fight with typing.
  useEffect(() => {
    if (!state) return;
    const saved = answersFor<Partial<BusinessAnswers>>(state, 'business_info');
    if (saved) setBusinessDraft((was) => ({ ...was, ...saved }));
    const savedStores = answersFor<{ stores?: StoreAnswers[] }>(state, 'stores');
    if (savedStores?.stores?.length) setStoreDrafts(savedStores.stores);
    const savedTax = answersFor<Partial<TaxAnswers>>(state, 'tax');
    if (savedTax) setTaxDraft((was) => ({ ...was, ...savedTax }));
    const savedPeople = answersFor<Partial<NoteAnswers>>(state, 'employees');
    if (savedPeople) setPeopleDraft((was) => ({ ...was, ...savedPeople }));
  }, [state]);

  /** Writes a step's answers. Accepts anything: the server validates at Continue. */
  async function saveStep(step: Step, answers: unknown): Promise<void> {
    await api.put(`/onboarding/steps/${step}`, answers);
  }

  /**
   * Saves, completes, and runs whatever has to happen at that boundary.
   *
   * The commits are here rather than inside a step because they are the point
   * at which scratch answers become real records, and both are conditional:
   * the company only when there is not one, the stores every time, because
   * that route upserts on (company, code) and is re-runnable by design.
   */
  async function advance(step: Step, answers: unknown) {
    setBusy(true);
    setError(null);
    setFields({});
    setNote(null);
    try {
      await saveStep(step, answers);
      await api.post(`/onboarding/steps/${step}/complete`, {});

      if (step === 'business_info' && needsCompany(companies.data?.data?.length ?? 0)) {
        await api.post<{ company_id: string }>('/onboarding/company', {});
        await companies.refetch();
      }
      if (step === 'stores') {
        const made = companies.data?.data?.[0] ?? (await companies.refetch()).data?.data?.[0];
        if (made) {
          await api.post('/onboarding/stores', { company_id: made.id });
        }
      }
      if (step === 'tax' && company) {
        // The wave and the deadline belong on the company, and the company was
        // created before this step. `PUT /companies/{id}` is a partial
        // amendment, so only what was answered is sent.
        const change: Record<string, string> = {};
        if (taxDraft.zatca_wave.trim()) change.zatca_wave = taxDraft.zatca_wave.trim();
        if (taxDraft.zatca_deadline.trim()) {
          change.zatca_deadline = taxDraft.zatca_deadline.trim();
        }
        if (Object.keys(change).length > 0) {
          await api.put(`/companies/${company.id}`, change);
        }
      }

      await progress.refetch();
      setNote(t('nx.setup.stepDoneNote', { step: t(STEP_LABEL[step]) }));
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /** Saves without advancing, so a half-finished form survives the browser closing. */
  async function keep(step: Step, answers: unknown) {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      await saveStep(step, answers);
      setNote(t('nx.setup.saved'));
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (progress.error) {
    return (
      <>
        <PageHeader title={t('nx.setup.title')} description={t('nx.setup.subtitle')} />
        <ErrorState error={progress.error} onRetry={() => void progress.refetch()} />
      </>
    );
  }

  if (!state) {
    return (
      <>
        <PageHeader title={t('nx.setup.title')} description={t('nx.setup.subtitle')} />
        <Skeleton className="h-64" />
      </>
    );
  }

  const done = stepsDone(state);

  return (
    <>
      <PageHeader
        title={t('nx.setup.title')}
        description={state.finished ? t('nx.setup.finishedSubtitle') : t('nx.setup.subtitle')}
        actions={
          state.finished ? (
            <Button asChild variant="primary">
              <Link href="/dashboard">{t('nx.setup.openProduct')}</Link>
            </Button>
          ) : undefined
        }
      />

      <FormError message={error} fields={fields} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {/* Where somebody is, as a list rather than a bar: a bar says how far and
          a list says what is left, and what is left is the useful half. */}
      <ol className="mb-6 flex flex-wrap gap-2" aria-label={t('nx.setup.progressLabel')}>
        {STEPS.filter((s) => s !== 'finished').map((step, i) => {
          const complete = state.finished || isDone(state, step);
          const here = !state.finished && step === current;
          return (
            <li key={step}>
              <span
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-xs border px-2 py-1 text-caption',
                  complete && 'border-positive/25 bg-positive-subtle text-positive-fg',
                  here && 'border-primary/25 bg-primary-subtle text-primary-subtle-fg font-medium',
                  !complete && !here && 'border-line bg-surface-sunken text-muted',
                )}
                aria-current={here ? 'step' : undefined}
              >
                {complete ? (
                  <Check className="size-3.5" aria-hidden="true" />
                ) : (
                  <span className="num">{i + 1}</span>
                )}
                {t(STEP_LABEL[step])}
              </span>
            </li>
          );
        })}
      </ol>

      <p className="mb-6 text-body text-muted" aria-live="polite">
        {state.finished
          ? t('nx.setup.allDone')
          : t('nx.setup.stepsLeft', {
              done: String(done),
              total: String(STEPS_TO_DO),
            })}
      </p>

      {!mayEdit ? (
        <Panel className="mb-6">
          <p className="max-w-prose text-body text-muted">{t('nx.setup.readOnly')}</p>
        </Panel>
      ) : null}

      {/* --- 1. the business ------------------------------------------- */}
      {(current === 'business_info' || state.finished || isDone(state, 'business_info')) && (
        <StepPanel
          step="business_info"
          active={!state.finished && current === 'business_info'}
          complete={state.finished || isDone(state, 'business_info')}
          title={t('nx.setup.businessTitle')}
          description={t('nx.setup.businessHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field
              name="legal_name"
              label={t('nx.setup.legalName')}
              hint={t('nx.setup.legalNameHint')}
              error={fields.legal_name}
              required
              className="sm:col-span-2"
            >
              <Input
                value={businessDraft.legal_name}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, legal_name: e.target.value })
                }
              />
            </Field>
            <Field name="legal_name_ar" label={t('nx.setup.legalNameAr')}>
              <Input
                dir="rtl"
                value={businessDraft.legal_name_ar}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, legal_name_ar: e.target.value })
                }
              />
            </Field>
            <Field
              name="trade_name"
              label={t('nx.setup.tradeName')}
              hint={t('nx.setup.tradeNameHint')}
            >
              <Input
                value={businessDraft.trade_name}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, trade_name: e.target.value })
                }
              />
            </Field>
            <Field
              name="country"
              label={t('nx.setup.country')}
              hint={t('nx.setup.countryHint')}
              error={fields.country}
              required
            >
              <Select
                value={businessDraft.country}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) => {
                  // The currency and the time zone follow the country as a
                  // suggestion, not a rule: both stay editable, because a
                  // business in one country legitimately keeps its books in
                  // another currency.
                  const country = e.target.value;
                  setBusinessDraft({
                    ...businessDraft,
                    country,
                    base_currency: businessDraft.base_currency || likelyCurrency(country),
                    timezone: businessDraft.timezone || (timezonesFor(country)[0] ?? ''),
                  });
                }}
              >
                <option value="">{t('nx.setup.chooseCountry')}</option>
                {COUNTRIES.map((c) => (
                  <option key={c} value={c}>
                    {t(COUNTRY_LABEL[c] as Key)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="base_currency"
              label={t('nx.setup.currency')}
              hint={t('nx.setup.currencyHint')}
              error={fields.base_currency}
              required
            >
              <Select
                value={businessDraft.base_currency}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, base_currency: e.target.value })
                }
              >
                <option value="">{t('nx.setup.chooseCurrency')}</option>
                {CURRENCIES.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="timezone"
              label={t('nx.setup.timezone')}
              hint={t('nx.setup.timezoneHint')}
            >
              <Select
                value={businessDraft.timezone}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, timezone: e.target.value })
                }
              >
                <option value="">{t('nx.setup.chooseTimezone')}</option>
                {timezonesFor(businessDraft.country).map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="cr_number"
              label={t('nx.setup.crNumber')}
              hint={t('nx.setup.crNumberHint')}
            >
              <Input
                className="num"
                dir="ltr"
                value={businessDraft.cr_number}
                disabled={!mayEdit || isDone(state, 'business_info')}
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, cr_number: e.target.value })
                }
              />
            </Field>
            <Field
              name="vat_number"
              label={t('nx.setup.vatNumber')}
              hint={t('nx.setup.vatNumberHint')}
              error={fields.vat_number}
              required={businessDraft.vat_registered}
            >
              <Input
                className="num"
                dir="ltr"
                value={businessDraft.vat_number}
                disabled={
                  !mayEdit || !businessDraft.vat_registered || isDone(state, 'business_info')
                }
                onChange={(e) =>
                  setBusinessDraft({ ...businessDraft, vat_number: e.target.value })
                }
              />
            </Field>
          </div>

          <div className="mt-4">
            <Checkbox
              label={t('nx.setup.vatRegistered')}
              hint={t('nx.setup.vatRegisteredHint')}
              checked={businessDraft.vat_registered}
              disabled={!mayEdit || isDone(state, 'business_info')}
              onChange={(e) =>
                setBusinessDraft({ ...businessDraft, vat_registered: e.target.checked })
              }
            />
          </div>

          {!state.finished && current === 'business_info' && mayEdit ? (
            <StepActions
              busy={busy}
              blocked={businessProblem(businessDraft) !== null}
              blockedBecause={
                businessProblem(businessDraft)
                  ? t(BUSINESS_PROBLEM[businessProblem(businessDraft) as string] as Key)
                  : null
              }
              onKeep={() => void keep('business_info', businessDraft)}
              onContinue={() => void advance('business_info', businessDraft)}
            />
          ) : null}
        </StepPanel>
      )}

      {/* --- 2. the branches ------------------------------------------- */}
      {(current === 'stores' || state.finished || isDone(state, 'stores')) && (
        <StepPanel
          step="stores"
          active={!state.finished && current === 'stores'}
          complete={state.finished || isDone(state, 'stores')}
          title={t('nx.setup.storesTitle')}
          description={t('nx.setup.storesHint')}
        >
          <p className="mb-4 max-w-prose text-body text-muted">
            {t('nx.setup.addressWhy')}
          </p>

          <ul className="flex flex-col gap-4">
            {storeDrafts.map((s, i) => {
              const problems = storeProblems(s);
              const locked = !mayEdit || isDone(state, 'stores');
              return (
                <li key={i} className="rounded-sm border border-line p-4">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <span className="text-body font-medium text-fg">
                      {s.name.trim() || t('nx.setup.branchN', { n: String(i + 1) })}
                    </span>
                    {!locked && storeDrafts.length > 1 ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          setStoreDrafts(storeDrafts.filter((_, at) => at !== i))
                        }
                        aria-label={t('nx.setup.removeBranch', { n: String(i + 1) })}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    ) : null}
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                    {(
                      [
                        ['name', 'nx.setup.branchName', 'nx.setup.branchNameHint'],
                        ['code', 'nx.setup.branchCode', 'nx.setup.branchCodeHint'],
                        ['street', 'nx.setup.street', null],
                        ['building_number', 'nx.setup.buildingNumber', null],
                        ['additional_number', 'nx.setup.additionalNumber', null],
                        ['district', 'nx.setup.district', null],
                        ['city', 'nx.setup.city', null],
                        ['postal_code', 'nx.setup.postalCode', null],
                        ['country_code', 'nx.setup.countryCode', null],
                      ] as const
                    ).map(([field, label, hint]) => (
                      <Field
                        key={field}
                        name={`${field}_${i}`}
                        label={t(label)}
                        hint={hint ? t(hint) : undefined}
                        error={
                          problems[field] ? t(FIELD_PROBLEM[problems[field]!] as Key) : undefined
                        }
                        required={
                          field !== 'additional_number' && field !== 'country_code'
                        }
                      >
                        <Input
                          value={s[field]}
                          disabled={locked}
                          dir={field === 'code' || field === 'country_code' ? 'ltr' : undefined}
                          className={
                            field === 'building_number' ||
                            field === 'additional_number' ||
                            field === 'postal_code'
                              ? 'num'
                              : undefined
                          }
                          onChange={(e) =>
                            setStoreDrafts(
                              storeDrafts.map((row, at) =>
                                at === i ? { ...row, [field]: e.target.value } : row,
                              ),
                            )
                          }
                        />
                      </Field>
                    ))}
                  </div>
                </li>
              );
            })}
          </ul>

          {!state.finished && current === 'stores' && mayEdit ? (
            <>
              <Button
                variant="secondary"
                className="mt-4"
                onClick={() =>
                  setStoreDrafts([
                    ...storeDrafts,
                    emptyStore(businessDraft.country || company?.country || ''),
                  ])
                }
              >
                <Plus aria-hidden="true" />
                {storeDrafts.length === 0
                  ? t('nx.setup.addFirstBranch')
                  : t('nx.setup.addAnotherBranch')}
              </Button>

              <StepActions
                busy={busy}
                blocked={storesProblem(storeDrafts) !== null}
                blockedBecause={
                  storesProblem(storeDrafts)
                    ? t(STORES_PROBLEM[storesProblem(storeDrafts) as string] as Key)
                    : null
                }
                onKeep={() => void keep('stores', { stores: storeDrafts })}
                onContinue={() => void advance('stores', { stores: storeDrafts })}
              />
            </>
          ) : null}
        </StepPanel>
      )}

      {/* --- 3. tax ---------------------------------------------------- */}
      {(current === 'tax' || state.finished || isDone(state, 'tax')) && (
        <StepPanel
          step="tax"
          active={!state.finished && current === 'tax'}
          complete={state.finished || isDone(state, 'tax')}
          title={t('nx.setup.taxTitle')}
          description={t('nx.setup.taxHint')}
        >
          {/* The rate is not typed in anywhere. It is resolved from the
              regulatory register at the date of every document, which is the
              only way a rate change on a Tuesday leaves Monday's invoices
              alone. */}
          <p className="max-w-prose text-body text-muted">{t('nx.setup.rateIsLoaded')}</p>

          {(businessDraft.country || company?.country) === 'sa' ? (
            <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <Field
                name="zatca_wave"
                label={t('nx.setup.zatcaWave')}
                hint={t('nx.setup.zatcaWaveHint')}
              >
                <Input
                  className="num"
                  dir="ltr"
                  value={taxDraft.zatca_wave}
                  disabled={!mayEdit || isDone(state, 'tax')}
                  onChange={(e) => setTaxDraft({ ...taxDraft, zatca_wave: e.target.value })}
                />
              </Field>
              <Field
                name="zatca_deadline"
                label={t('nx.setup.zatcaDeadline')}
                hint={t('nx.setup.zatcaDeadlineHint')}
              >
                <Input
                  type="date"
                  className="num"
                  value={taxDraft.zatca_deadline}
                  disabled={!mayEdit || isDone(state, 'tax')}
                  onChange={(e) =>
                    setTaxDraft({ ...taxDraft, zatca_deadline: e.target.value })
                  }
                />
              </Field>
            </div>
          ) : (
            <p className="mt-3 max-w-prose text-body text-muted">
              {t('nx.setup.noZatcaHere')}
            </p>
          )}

          <p className="mt-4 max-w-prose text-caption text-muted">
            {t('nx.setup.taxLater')}
          </p>

          {!state.finished && current === 'tax' && mayEdit ? (
            <StepActions
              busy={busy}
              blocked={false}
              blockedBecause={null}
              onKeep={() => void keep('tax', taxDraft)}
              onContinue={() => void advance('tax', { ...taxDraft, acknowledged: true })}
            />
          ) : null}
        </StepPanel>
      )}

      {/* --- 4. people, 5. hardware, 6. opening balances ---------------- */}
      {(current === 'employees' || state.finished || isDone(state, 'employees')) && (
        <StepPanel
          step="employees"
          active={!state.finished && current === 'employees'}
          complete={state.finished || isDone(state, 'employees')}
          title={t('nx.setup.peopleTitle')}
          description={t('nx.setup.peopleHint')}
        >
          <p className="max-w-prose text-body text-muted">{t('nx.setup.peopleOptional')}</p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button asChild variant="secondary">
              <Link href="/people/users">{t('nx.setup.openUsers')}</Link>
            </Button>
            <Button asChild variant="secondary">
              <Link href="/people/employees/new">{t('nx.setup.openEmployees')}</Link>
            </Button>
          </div>
          {!state.finished && current === 'employees' && mayEdit ? (
            <StepActions
              busy={busy}
              blocked={false}
              blockedBecause={null}
              onKeep={() => void keep('employees', peopleDraft)}
              onContinue={() => void advance('employees', { ...peopleDraft, skipped: true })}
              continueLabel={t('nx.setup.continueOptional')}
            />
          ) : null}
        </StepPanel>
      )}

      {(current === 'hardware' || state.finished || isDone(state, 'hardware')) && (
        <StepPanel
          step="hardware"
          active={!state.finished && current === 'hardware'}
          complete={state.finished || isDone(state, 'hardware')}
          title={t('nx.setup.hardwareTitle')}
          description={t('nx.setup.hardwareHint')}
        >
          <p className="max-w-prose text-body text-muted">{t('nx.setup.hardwareOptional')}</p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button asChild variant="secondary">
              <Link href="/settings/devices">{t('nx.setup.openDevices')}</Link>
            </Button>
            <Button asChild variant="secondary">
              <Link href="/products/labels">{t('nx.setup.openLabels')}</Link>
            </Button>
          </div>
          {!state.finished && current === 'hardware' && mayEdit ? (
            <StepActions
              busy={busy}
              blocked={false}
              blockedBecause={null}
              onKeep={() => void keep('hardware', { skipped: false })}
              onContinue={() => void advance('hardware', { skipped: true })}
              continueLabel={t('nx.setup.continueOptional')}
            />
          ) : null}
        </StepPanel>
      )}

      {(current === 'opening_balances' ||
        state.finished ||
        isDone(state, 'opening_balances')) && (
        <StepPanel
          step="opening_balances"
          active={!state.finished && current === 'opening_balances'}
          complete={state.finished || isDone(state, 'opening_balances')}
          title={t('nx.setup.balancesTitle')}
          description={t('nx.setup.balancesHint')}
        >
          <p className="max-w-prose text-body text-muted">{t('nx.setup.balancesOptional')}</p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button asChild variant="secondary">
              <Link href="/products">{t('nx.setup.openProducts')}</Link>
            </Button>
            <Button asChild variant="secondary">
              <Link href="/stock/adjustments/new">{t('nx.setup.openStock')}</Link>
            </Button>
            <Button asChild variant="secondary">
              <Link href="/money/journals/new">{t('nx.setup.openJournal')}</Link>
            </Button>
          </div>
          {!state.finished && current === 'opening_balances' && mayEdit ? (
            <StepActions
              busy={busy}
              blocked={false}
              blockedBecause={null}
              onKeep={() => void keep('opening_balances', { skipped: false })}
              onContinue={() => void advance('opening_balances', { skipped: true })}
              continueLabel={t('nx.setup.finishSetup')}
            />
          ) : null}
        </StepPanel>
      )}

      {state.finished ? (
        <Panel className="mt-6" title={t('nx.setup.doneTitle')}>
          <p className="max-w-prose text-body text-muted">{t('nx.setup.doneBody')}</p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button asChild variant="primary">
              <Link href="/dashboard">{t('nx.setup.openProduct')}</Link>
            </Button>
            <Button asChild variant="secondary">
              <Link href="/settings/business">{t('nx.setup.openSettings')}</Link>
            </Button>
          </div>
        </Panel>
      ) : null}
    </>
  );
}

/** One step, with its state written on it rather than implied by position. */
function StepPanel({
  step,
  active,
  complete,
  title,
  description,
  children,
}: {
  step: Step;
  active: boolean;
  complete: boolean;
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    <Panel
      className={cn('mb-5', active && 'border-primary/40')}
      title={title}
      description={description}
      actions={
        complete ? (
          <Badge tone="positive">
            <Check className="size-3" aria-hidden="true" />
            {t('nx.setup.stepComplete')}
          </Badge>
        ) : active ? (
          <Badge tone="primary">{t('nx.setup.stepNow')}</Badge>
        ) : null
      }
    >
      <div id={`step-${step}`}>{children}</div>
    </Panel>
  );
}

/** Save, and continue. Save alone exists because a wizard has to be resumable. */
function StepActions({
  busy,
  blocked,
  blockedBecause,
  onKeep,
  onContinue,
  continueLabel,
}: {
  busy: boolean;
  blocked: boolean;
  blockedBecause: string | null;
  onKeep: () => void;
  onContinue: () => void;
  continueLabel?: string;
}) {
  const t = useT();
  return (
    <>
      {blockedBecause ? (
        <p className="mt-4 text-caption text-muted">{blockedBecause}</p>
      ) : null}
      <div className="mt-4 flex flex-wrap gap-3">
        <Button
          variant="primary"
          busy={busy}
          busyLabel={t('nx.setup.working')}
          disabled={blocked}
          onClick={onContinue}
        >
          {continueLabel ?? t('nx.setup.continue')}
          <ArrowRight aria-hidden="true" />
        </Button>
        {/* Saving without continuing is the whole reason this is resumable:
            the route accepts a half-finished step on purpose. */}
        <Button variant="secondary" busy={busy} onClick={onKeep}>
          {t('nx.setup.keepForLater')}
        </Button>
      </div>
    </>
  );
}

export default function SetupPage() {
  return (
    <RequirePermission anyOf={['identity.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SetupScreen />
      </Suspense>
    </RequirePermission>
  );
}
