// Setting up a business, blueprint A5 / UI spec §6.
//
// Seven steps, designed so a non-technical shop owner completes setup alone.
// Progress is saved per step and the wizard is resumable: the server keeps the
// answers, so closing the browser at step 4 costs nothing.
//
// # The server owns the order and the rules
//
// Which step comes next is read from `next_step` on every response, never
// computed here. Whether a step is complete is the server's answer too — the
// validation in this screen exists to save a round trip and to put a message
// under the field it belongs to, and the same rules run in Go. A form that
// disagreed with the server would simply be wrong.
//
// # What this screen deliberately does NOT do
//
// It captures no ZATCA credential, OTP, CSR or certificate. Those belong to an
// EGS unit, behind `einvoicing.manage`, with the private key in the terminal's
// keystore — and the request formats for onboarding a unit are unverified
// release blockers, so a field offering to take an OTP would be inviting a
// client to hand over something this product cannot yet use. The wizard records
// the ZATCA obligation the taxpayer was notified of, and says where the rest is
// done.

import { useCallback, useEffect, useMemo, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import {
  commitOnboardingCompany,
  completeOnboardingStep,
  fetchOnboarding,
  saveOnboardingStep,
  type OnboardingProgress,
  type OnboardingStep,
} from '../api/onboarding';
import { Field, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  answersFor,
  isComplete,
  isReachable,
  outstandingSteps,
  readiness,
  STEPS,
  stepMeta,
  stepNumber,
  taxContextFor,
  validateBusiness,
  validateStores,
  validateTax,
  type BusinessInfo,
  type FieldErrors,
  type StoreInfo,
  emptyStore,
  type TaxInfo,
} from './onboarding';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; progress: OnboardingProgress }
  | { state: 'failed'; message: string; offline: boolean }
  | { state: 'denied' };

export function OnboardingWizard({ onFinished }: { onFinished?: () => void } = {}) {
  const t = useT();
  const { client, can } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [viewing, setViewing] = useState<OnboardingStep | null>(null);

  // identity.edit is what the save and complete routes require. Reading is
  // identity.view; someone with only that sees their progress and cannot
  // change it, which is the honest shape rather than hiding the screen.
  const mayEdit = can('identity.edit');

  const reload = useCallback(async () => {
    setLoad({ state: 'loading' });
    try {
      setLoad({ state: 'ready', progress: await fetchOnboarding(client) });
    } catch (err) {
      if (err instanceof RequestFailed && err.status === 403) {
        setLoad({ state: 'denied' });
        return;
      }
      setLoad({
        state: 'failed',
        message: explain(err, t),
        offline: err instanceof Offline,
      });
    }
  }, [client]);

  useEffect(() => {
    void reload();
  }, [reload]);

  if (load.state === 'loading') {
    return (
      <main className="setupw" aria-busy="true">
        <div className="ds-panel">
          <div className="ds-panel__body">
            <div className="ds-skeleton" style={{ blockSize: 200 }} />
          </div>
        </div>
      </main>
    );
  }

  if (load.state === 'denied') {
    return (
      <Shell>
        <div className="ds-state">
          <p className="ds-state__title">{t('setup.noSetupAccess')}</p>
          <p className="ds-state__body">
            Setting up a business needs permission to manage people and
            settings. An owner can change that under Settings &gt; People.
          </p>
        </div>
      </Shell>
    );
  }

  if (load.state === 'failed') {
    return (
      <Shell>
        <div className="ds-state">
          <p className="ds-state__title">
            {load.offline ? t('setup.unreachable') : t('setup.unreadable')}
          </p>
          <p className="ds-state__body">{load.message}</p>
          <button className="ds-btn ds-btn--secondary" onClick={() => void reload()}>
            {t('common.tryAgain')}
          </button>
        </div>
      </Shell>
    );
  }

  const { progress } = load;
  const active = viewing && isReachable(progress, viewing) ? viewing : progress.current_step;

  return (
    <main className="setupw">
      <header className="setupw__head">
        <div>
          <h1 className="ds-h1">{t('setup.setUpBusiness')}</h1>
          <p className="ds-body-sm ds-muted">{t('setup.sevenSteps')}</p>
        </div>
        <ReadinessBadge progress={progress} />
      </header>

      <StepRail
        progress={progress}
        active={active}
        onPick={(s) => setViewing(s)}
      />

      <div className="ds-panel setupw__panel">
        <div className="ds-panel__body">
          {!mayEdit && (
            <p className="setupw__readonly ds-body-sm" role="note">{t('setup.readOnly')}</p>
          )}
          <StepBody
            key={active}
            step={active}
            progress={progress}
            readOnly={!mayEdit}
            onAdvanced={(p) => {
              setLoad({ state: 'ready', progress: p });
              setViewing(null);
            }}
            onFinished={() => {
              void reload();
              onFinished?.();
            }}
          />
        </div>
      </div>
    </main>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="setupw">
      <div className="ds-panel">
        <div className="ds-panel__body">{children}</div>
      </div>
    </main>
  );
}

/** Where setup stands, in one word. Says nothing about ZATCA: that is the
 *  company's `zatca_status` and each unit's `csid_status`, neither of which
 *  this wizard advances. */
function ReadinessBadge({ progress }: { progress: OnboardingProgress }) {
  const t = useT();
  const state = readiness(progress);
  const tone =
    state === 'done' ? 'ds-badge--success' : state === 'ready' ? 'ds-badge--info' : 'ds-badge--neutral';
  const word =
    state === 'done' ? t('setup.setUp') : state === 'ready' ? t('setup.readyToFinish') : t('setup.inProgress');
  return <span className={`ds-badge ${tone}`}>{word}</span>;
}

function StepRail({
  progress,
  active,
  onPick,
}: {
  progress: OnboardingProgress;
  active: OnboardingStep;
  onPick: (s: OnboardingStep) => void;
}) {
  const t = useT();
  return (
    <ol className="rail" aria-label={t('setup.steps')}>
      {STEPS.map((s, i) => {
        const done = isComplete(progress, s.key);
        const reachable = isReachable(progress, s.key);
        return (
          <li key={s.key} className="rail__item">
            <button
              className={`rail__step${active === s.key ? ' rail__step--on' : ''}${
                done ? ' rail__step--done' : ''
              }`}
              disabled={!reachable}
              aria-current={active === s.key ? 'step' : undefined}
              onClick={() => onPick(s.key)}
              // A step nobody can reach yet says why, rather than looking
              // broken.
              title={reachable ? undefined : t('setup.finishPrevious')}
            >
              <span className="rail__no" aria-hidden="true">
                {done ? '✓' : i + 1}
              </span>
              <span className="rail__title">{t(s.title)}</span>
            </button>
          </li>
        );
      })}
    </ol>
  );
}

// --- the steps ------------------------------------------------------------

function StepBody({
  step,
  progress,
  readOnly,
  onAdvanced,
  onFinished,
}: {
  step: OnboardingStep;
  progress: OnboardingProgress;
  readOnly: boolean;
  onAdvanced: (p: OnboardingProgress) => void;
  onFinished: () => void;
}) {
  const t = useT();
  const meta = stepMeta(step);

  return (
    <section className="setupw__step" aria-label={t(meta.title)}>
      <div className="setupw__intro">
        <p className="ds-caption setupw__count">
          {t('setup.stepOf')
            .replace('{n}', String(stepNumber(step)))
            .replace('{total}', String(STEPS.length))}
        </p>
        <h2 className="ds-h2">{t(meta.title)}</h2>
        <p className="ds-body-sm ds-muted setupw__purpose">{t(meta.purpose)}</p>
      </div>

      {step === 'business_info' && (
        <BusinessStep progress={progress} readOnly={readOnly} onAdvanced={onAdvanced} />
      )}
      {step === 'stores' && (
        <StoresStep progress={progress} readOnly={readOnly} onAdvanced={onAdvanced} />
      )}
      {step === 'tax' && (
        <TaxStep progress={progress} readOnly={readOnly} onAdvanced={onAdvanced} />
      )}
      {(step === 'employees' || step === 'hardware' || step === 'opening_balances') && (
        <OptionalStep
          step={step}
          progress={progress}
          readOnly={readOnly}
          onAdvanced={onAdvanced}
        />
      )}
      {step === 'finished' && (
        <FinishStep progress={progress} readOnly={readOnly} onFinished={onFinished} />
      )}
    </section>
  );
}

/** Shared submit machinery: save the answers, then ask the server to complete
 *  the step. Two calls rather than one because saving must work even when the
 *  step is not yet valid — that is what makes the wizard resumable. */
function useStepSubmit(
  step: OnboardingStep,
  onAdvanced: (p: OnboardingProgress) => void,
) {
  const { client } = useAuth();
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [serverFields, setServerFields] = useState<FieldErrors>({});

  const save = useCallback(
    async (answers: unknown) => {
      setFailure(null);
      setBusy(true);
      try {
        await saveOnboardingStep(client, step, answers);
        return true;
      } catch (err) {
        setFailure(explain(err, t));
        return false;
      } finally {
        setBusy(false);
      }
    },
    [client, step],
  );

  const submit = useCallback(
    async (answers: unknown) => {
      setFailure(null);
      setServerFields({});
      setBusy(true);
      try {
        await saveOnboardingStep(client, step, answers);
        onAdvanced(await completeOnboardingStep(client, step));
      } catch (err) {
        // Field-level messages come back keyed as the form keys them, which is
        // why they are shown under their own input rather than as a banner.
        if (err instanceof RequestFailed && err.fields) setServerFields(err.fields);
        setFailure(explain(err, t));
      } finally {
        setBusy(false);
      }
    },
    [client, step, onAdvanced],
  );

  return { busy, failure, serverFields, save, submit };
}

function BusinessStep({
  progress,
  readOnly,
  onAdvanced,
}: {
  progress: OnboardingProgress;
  readOnly: boolean;
  onAdvanced: (p: OnboardingProgress) => void;
}) {
  const t = useT();
  const saved = answersFor(progress, 'business_info') as Partial<BusinessInfo>;
  const [v, setV] = useState<BusinessInfo>({
    legal_name: saved.legal_name ?? '',
    legal_name_ar: saved.legal_name_ar ?? '',
    trade_name: saved.trade_name ?? '',
    country: saved.country ?? 'sa',
    base_currency: saved.base_currency ?? 'SAR',
    timezone: saved.timezone ?? 'Asia/Riyadh',
    cr_number: saved.cr_number ?? '',
    vat_registered: saved.vat_registered ?? false,
    vat_number: saved.vat_number ?? '',
  });
  const [errors, setErrors] = useState<FieldErrors>({});
  const { busy, failure, serverFields, save, submit } = useStepSubmit(
    'business_info',
    onAdvanced,
  );
  const field = (k: string) => errors[k] ?? serverFields[k];
  const set = (k: keyof BusinessInfo, value: string | boolean) =>
    setV((c) => ({ ...c, [k]: value }));

  return (
    <>
      <Field label={t('setup.registeredLegalName')} htmlFor="legal-name" required error={field('legal_name')}
        hint={t('setup.legalNameHint')}>
        <TextInput id="legal-name" value={v.legal_name} onChange={(x) => set('legal_name', x)}
          error={field('legal_name')} autoFocus />
      </Field>

      <Field label={t('setup.legalNameArabic')} htmlFor="legal-name-ar"
        hint={t('setup.vatHint')}>
        <TextInput id="legal-name-ar" value={v.legal_name_ar}
          onChange={(x) => set('legal_name_ar', x)} />
      </Field>

      <Field label={t('setup.tradingName')} htmlFor="trade-name" hint={t('setup.tradingNameHint')}>
        <TextInput id="trade-name" value={v.trade_name} onChange={(x) => set('trade_name', x)} />
      </Field>

      <div className="setupw__pair">
        <Field label={t('common.country')} htmlFor="country" required error={field('country')}>
          <select id="country" className="field__input" value={v.country}
            onChange={(e) => set('country', e.target.value)}>
            <option value="sa">{t('setup.saudiArabia')}</option>
            <option value="bd">{t('setup.bangladesh')}</option>
            <option value="us">{t('setup.unitedStates')}</option>
          </select>
        </Field>

        <Field label={t('setup.booksKeptIn')} htmlFor="currency" required error={field('base_currency')}>
          <select id="currency" className="field__input" value={v.base_currency}
            onChange={(e) => set('base_currency', e.target.value)}>
            <option value="SAR">{t('ccy.sar')}</option>
            <option value="BDT">{t('ccy.bdt')}</option>
            <option value="USD">{t('ccy.usd')}</option>
          </select>
        </Field>
      </div>

      <Field label={t('setup.crNumber')} htmlFor="cr">
        <TextInput id="cr" value={v.cr_number} onChange={(x) => set('cr_number', x)} />
      </Field>

      <label className="setupw__check">
        <input type="checkbox" checked={v.vat_registered}
          onChange={(e) => set('vat_registered', e.target.checked)} />
        <span>
          <strong>{t('setup.vatRegistered')}</strong>
          <span className="ds-caption">
            {t('setup.vatNumberNote')}
          </span>
        </span>
      </label>

      {v.vat_registered && (
        <Field label={t('setup.vatRegNumber')} htmlFor="vat" required error={field('vat_number')}
          hint={v.country === 'sa' ? '15 digits, starting and ending with 3.' : undefined}>
          <TextInput id="vat" value={v.vat_number} onChange={(x) => set('vat_number', x)}
            inputMode="numeric" error={field('vat_number')} />
        </Field>
      )}

      <FormError message={failure} />
      <StepActions
        busy={busy}
        readOnly={readOnly}
        onSave={() => void save(v)}
        onContinue={() => {
          const found = validateBusiness(v, t);
          setErrors(found);
          if (Object.keys(found).length === 0) void submit(v);
        }}
      />
    </>
  );
}

function StoresStep({
  progress,
  readOnly,
  onAdvanced,
}: {
  progress: OnboardingProgress;
  readOnly: boolean;
  onAdvanced: (p: OnboardingProgress) => void;
}) {
  const t = useT();
  const saved = answersFor(progress, 'stores') as { stores?: StoreInfo[] };
  const [stores, setStores] = useState<StoreInfo[]>(
    saved.stores?.length ? saved.stores : [emptyStore()],
  );
  const [rows, setRows] = useState<Record<number, FieldErrors>>({});
  const [formError, setFormError] = useState<string | undefined>();
  const { busy, failure, save, submit } = useStepSubmit('stores', onAdvanced);

  const answers = useMemo(() => ({ stores }), [stores]);
  const edit = (i: number, k: keyof StoreInfo, value: string) =>
    setStores((c) => c.map((s, j) => (j === i ? { ...s, [k]: value } : s)));

  return (
    <>
      {stores.map((s, i) => (
        <div className="setupw__row" key={i}>
          <Field label={t('setup.storeName')} htmlFor={`store-name-${i}`} required error={rows[i]?.name}>
            <TextInput id={`store-name-${i}`} value={s.name}
              onChange={(x) => edit(i, 'name', x)} error={rows[i]?.name}
              placeholder={t('setup.branchExample')} />
          </Field>
          <Field label={t('common.shortCode')} htmlFor={`store-code-${i}`} required error={rows[i]?.code}
            hint={t('setup.storeCodeHint')}>
            <TextInput id={`store-code-${i}`} value={s.code}
              onChange={(x) => edit(i, 'code', x.toUpperCase())} error={rows[i]?.code}
              placeholder="RYD" />
          </Field>

          <div className="setupw__pair">
            <Field label={t('setup.street')} htmlFor={`store-street-${i}`} required error={rows[i]?.street}
              hint={t('setup.streetHint')}>
              <TextInput id={`store-street-${i}`} value={s.street}
                onChange={(x) => edit(i, 'street', x)} error={rows[i]?.street}
                placeholder={t('setup.streetExample')} />
            </Field>
            <Field label={t('setup.buildingNumber')} htmlFor={`store-bn-${i}`} required error={rows[i]?.building_number}
              hint={t('setup.buildingNumberHint')}>
              <TextInput id={`store-bn-${i}`} value={s.building_number} inputMode="numeric"
                onChange={(x) => edit(i, 'building_number', x)} error={rows[i]?.building_number}
                placeholder="2322" />
            </Field>
          </div>
          <div className="setupw__pair">
            <Field label={t('setup.district')} htmlFor={`store-district-${i}`} required error={rows[i]?.district}
              hint={t('setup.districtHint')}>
              <TextInput id={`store-district-${i}`} value={s.district}
                onChange={(x) => edit(i, 'district', x)} error={rows[i]?.district}
                placeholder={t('setup.districtExample')} />
            </Field>
            <Field label={t('setup.city')} htmlFor={`store-city-${i}`} required error={rows[i]?.city}
              hint={t('setup.cityHint')}>
              <TextInput id={`store-city-${i}`} value={s.city}
                onChange={(x) => edit(i, 'city', x)} error={rows[i]?.city}
                placeholder="Riyadh" />
            </Field>
          </div>
          <div className="setupw__pair">
            <Field label={t('setup.postalCode')} htmlFor={`store-zip-${i}`} required error={rows[i]?.postal_code}
              hint={t('setup.postalCodeHint')}>
              <TextInput id={`store-zip-${i}`} value={s.postal_code} inputMode="numeric"
                onChange={(x) => edit(i, 'postal_code', x)} error={rows[i]?.postal_code}
                placeholder="23333" />
            </Field>
            <Field label={t('setup.additionalNumber')} htmlFor={`store-addl-${i}`} error={rows[i]?.additional_number}
              hint={t('setup.additionalNumberHint')}>
              <TextInput id={`store-addl-${i}`} value={s.additional_number} inputMode="numeric"
                onChange={(x) => edit(i, 'additional_number', x)} error={rows[i]?.additional_number}
                placeholder="2223" />
            </Field>
          </div>
          {stores.length > 1 && (
            <button className="ds-btn ds-btn--quiet setupw__drop"
              onClick={() => setStores((c) => c.filter((_, j) => j !== i))}>
              {t('common.remove')}
            </button>
          )}
        </div>
      ))}

      <button className="ds-btn ds-btn--secondary"
        onClick={() => setStores((c) => [...c, emptyStore()])}>
        {t('setup.addAnotherStore')}
      </button>

      {formError && <p className="field__error" role="alert">{formError}</p>}
      <FormError message={failure} />
      <StepActions
        busy={busy}
        readOnly={readOnly}
        onSave={() => void save(answers)}
        onContinue={() => {
          const found = validateStores(stores, t);
          setRows(found.rows);
          setFormError(found.form);
          if (!found.form && Object.keys(found.rows).length === 0) void submit(answers);
        }}
      />
    </>
  );
}

function TaxStep({
  progress,
  readOnly,
  onAdvanced,
}: {
  progress: OnboardingProgress;
  readOnly: boolean;
  onAdvanced: (p: OnboardingProgress) => void;
}) {
  const t = useT();
  const business = answersFor(progress, 'business_info') as Partial<BusinessInfo>;
  const country = business.country ?? 'sa';
  const ctx = taxContextFor(country);

  const saved = answersFor(progress, 'tax') as Partial<TaxInfo>;
  const [v, setV] = useState<TaxInfo>({
    zatca_wave: saved.zatca_wave ?? '',
    zatca_deadline: saved.zatca_deadline ?? '',
  });
  const [errors, setErrors] = useState<FieldErrors>({});
  const { busy, failure, serverFields, save, submit } = useStepSubmit('tax', onAdvanced);
  const field = (k: string) => errors[k] ?? serverFields[k];

  return (
    <>
      {ctx.fromRegistry && (
        <div className="setupw__loaded">
          <h3 className="ds-h3">{t('setup.loadedForYou')}</h3>
          <ul className="setupw__facts">
            <li>
              <strong>{t('common.vat')}</strong> is applied at the rate in force on the day of
              each sale, resolved from the regulatory register rather than typed
              in here — so a rate change does not need a settings edit.
            </li>
            <li>
              <strong>{t('tpl.arabic')}</strong> is enabled, and invoices render
              right-to-left.
            </li>
            <li>
              <strong>{t('egs.einvoicing')}</strong> applies. Setting up the units that
              sign your invoices is done under E-invoicing once this business
              exists.
            </li>
          </ul>
        </div>
      )}

      {ctx.zatcaApplies ? (
        <>
          {/* Blueprint E1.0: the software must never assume or assert a
              taxpayer's wave. It comes from ZATCA to the taxpayer directly, and
              the copy has to make that unmistakable — a shop that believed
              RawSyst knew their deadline would plan around a date nobody
              official gave them. */}
          <p className="setupw__notice" role="note">
            {t('setup.theseComeFrom')} <strong>your own ZATCA notification</strong>.
            RawSyst does not know your wave or your date and never assumes one.
            Leave them blank if you have not been notified yet.
          </p>

          <Field label={t('setup.zatcaWave')} htmlFor="wave"
            hint={t('setup.waveHint')}>
            <TextInput id="wave" value={v.zatca_wave}
              onChange={(x) => setV((c) => ({ ...c, zatca_wave: x }))} />
          </Field>

          <Field label={t('setup.integrationDeadline')} htmlFor="deadline" error={field('zatca_deadline')}
            hint={t('setup.vatDateHint')}>
            <TextInput id="deadline" value={v.zatca_deadline} type="date"
              onChange={(x) => setV((c) => ({ ...c, zatca_deadline: x }))}
              error={field('zatca_deadline')} />
          </Field>
        </>
      ) : (
        <p className="ds-body-sm ds-muted">{t('setup.taxFromRegister')}</p>
      )}

      <FormError message={failure} />
      <StepActions
        busy={busy}
        readOnly={readOnly}
        onSave={() => void save(v)}
        onContinue={() => {
          const found = validateTax(v, t);
          setErrors(found);
          if (Object.keys(found).length === 0) void submit(v);
        }}
      />
    </>
  );
}

/** Employees, hardware and opening balances. All three are optional and the
 *  server agrees — it validates them as passing with nothing in them — so this
 *  says what they are for and lets the Owner move on. Building each of them out
 *  is its own milestone; pretending otherwise with a dead form would be worse
 *  than saying so. */
function OptionalStep({
  step,
  progress,
  readOnly,
  onAdvanced,
}: {
  step: OnboardingStep;
  progress: OnboardingProgress;
  readOnly: boolean;
  onAdvanced: (p: OnboardingProgress) => void;
}) {
  const t = useT();
  const { busy, failure, submit } = useStepSubmit(step, onAdvanced);
  const done = isComplete(progress, step);

  const later: Record<string, string> = {
    employees:
      'People are added under Settings once the business exists, and each one gets a role that decides what they can reach.',
    hardware:
      'A till pairs itself: register it under Terminals, and the terminal redeems a one-time code. Nothing needs to be configured here first.',
    opening_balances:
      'What the business already owns and owes can be entered in the accounts once this is finished. A business starting fresh has none.',
  };

  return (
    <>
      <p className="ds-body-sm">{later[step]}</p>
      <p className="ds-caption setupw__skip">
        {t('setup.optionalStep')}
      </p>
      <FormError message={failure} />
      <div className="setupw__actions">
        <button className="ds-btn ds-btn--primary" disabled={busy || readOnly}
          onClick={() => void submit({})}>
          {busy ? 'Saving…' : done ? t('action.continue') : t('setup.skipForNow')}
        </button>
      </div>
    </>
  );
}

function FinishStep({
  progress,
  readOnly,
  onFinished,
}: {
  progress: OnboardingProgress;
  readOnly: boolean;
  onFinished: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [created, setCreated] = useState<string | null>(null);

  const business = answersFor(progress, 'business_info') as Partial<BusinessInfo>;
  const stores = (answersFor(progress, 'stores') as { stores?: StoreInfo[] }).stores ?? [];
  const outstanding = outstandingSteps(progress);

  if (created) {
    return (
      <div className="ds-state">
        <p className="ds-state__title">
          {t('setup.businessIsSetUp', {
            // `Partial<BusinessInfo>`, because the wizard's answers are read
            // back from whatever has been saved so far. By this screen the
            // business has been created and the name cannot be missing, but
            // the type does not know that and a template reading "undefined is
            // set up" is a worse outcome than a generic sentence.
            business: business.legal_name ?? t('common.theBusiness'),
          })}
        </p>
        <p className="ds-state__body">{t('setup.nextSteps')}</p>
        <button className="ds-btn ds-btn--primary" onClick={onFinished}>
          {t('setup.goToBackOffice')}
        </button>
      </div>
    );
  }

  return (
    <>
      {outstanding.length > 0 ? (
        <div className="ds-state">
          <p className="ds-state__title">{t('setup.stepsOpen')}</p>
          <p className="ds-state__body">
            {t('setup.finishFirst', {
              steps: outstanding.map((s) => t(s.title)).join(' • '),
            })}
          </p>
        </div>
      ) : (
        <>
          <dl className="setupw__review">
            <div><dt className="ds-caption">{t('setup.legalName')}</dt><dd>{business.legal_name || '—'}</dd></div>
            <div><dt className="ds-caption">{t('common.country')}</dt><dd>{(business.country ?? '').toUpperCase() || '—'}</dd></div>
            <div><dt className="ds-caption">{t('setup.booksIn')}</dt><dd>{business.base_currency || '—'}</dd></div>
            <div>
              <dt className="ds-caption">{t('common.vat')}</dt>
              <dd>{business.vat_registered ? business.vat_number || t('setup.registered') : t('setup.notRegistered')}</dd>
            </div>
            <div>
              <dt className="ds-caption">{t('setup.stores')}</dt>
              <dd>{stores.length > 0 ? stores.map((s) => `${s.name} (${s.code})`).join(', ') : '—'}</dd>
            </div>
          </dl>

          <p className="setupw__notice" role="note">{t('setup.createsChart')}</p>

          <FormError message={failure} />
          <div className="setupw__actions">
            <button className="ds-btn ds-btn--primary" disabled={busy || readOnly}
              onClick={() => {
                setFailure(null);
                setBusy(true);
                commitOnboardingCompany(client)
                  .then((r) => setCreated(r.company_id))
                  .catch((err) => setFailure(explain(err, t)))
                  .finally(() => setBusy(false));
              }}>
              {busy ? t('setup.creating') : t('setup.createBusiness')}
            </button>
          </div>
        </>
      )}
    </>
  );
}

function StepActions({
  busy,
  readOnly,
  onSave,
  onContinue,
}: {
  busy: boolean;
  readOnly: boolean;
  onSave: () => void;
  onContinue: () => void;
}) {
  const t = useT();
  return (
    <div className="setupw__actions">
      {/* Saving without continuing is the whole reason the wizard is
          resumable. It never validates, because a half-filled step must be
          allowed to persist. */}
      <button className="ds-btn ds-btn--quiet" disabled={busy || readOnly} onClick={onSave}>
        {t('setup.saveAndComeBack')}
      </button>
      <button className="ds-btn ds-btn--primary" disabled={busy || readOnly} onClick={onContinue}>
        {busy ? 'Saving…' : t('action.continue')}
      </button>
    </div>
  );
}

/** Turns a failure into something an owner can act on. */
function explain(err: unknown, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    return t('setup.offlineFull');
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) {
      return t('setup.notAllowed');
    }
    if (err.status === 409) {
      // The server's own sentence — "Setup is already finished for this
      // business" — says more than anything this could substitute.
      return err.message;
    }
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}
