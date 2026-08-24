// Connecting a till to ZATCA.
//
// # What this screen is for
//
// A Saudi shop owner opens this once per till, works through it, and never
// comes back unless something expires. So it leads with WHERE THEY ARE and
// WHAT TO DO NEXT, and puts the certificate detail underneath for the day
// somebody is diagnosing a refusal.
//
// The steps are numbered because the order is not obvious and getting it wrong
// wastes a one-time password: a compliance CSID must exist before a production
// one can be asked for, and the OTP is only spent on the first.
//
// # What this screen never holds
//
// The private key, and the CSID secret.
//
// The key is generated on the terminal and stays in its OS keystore --
// docs/system-design/01-invoice-zatca-engine.md §7 records that as a locked
// rule. What this screen sends is the certificate REQUEST, which carries the
// public half.
//
// The secret never reaches the browser at all: the server has no field for it
// on any response. That is stronger than filtering it here, because there is
// nothing to forget to filter.
//
// The OTP is held in component state for the seconds it takes to submit, and
// never written to localStorage, a URL, or anywhere it could outlive the form.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import {
  readOnboardingStatus,
  requestComplianceCsid,
  requestProductionCsid,
  renewProductionCsid,
  type CredentialSummary,
  type EgsUnit,
  type OnboardingStatus,
  type ZatcaEnvironment,
} from '../api/egs';
import { Field, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';

/** The environments, in the order a shop meets them. */
const ENVIRONMENTS: { id: ZatcaEnvironment; labelKey: 'zatca.envSandbox' | 'zatca.envSimulation' | 'zatca.envProduction' }[] = [
  { id: 'sandbox', labelKey: 'zatca.envSandbox' },
  { id: 'simulation', labelKey: 'zatca.envSimulation' },
  { id: 'production', labelKey: 'zatca.envProduction' },
];

export function ZatcaOnboarding({ unit }: { unit: EgsUnit }) {
  const t = useT();
  const { client, can } = useAuth();

  // Sandbox first. Nothing defaults to production: the cost of a mistake there
  // is a real OTP spent and a real registration, and neither is undoable.
  const [environment, setEnvironment] = useState<ZatcaEnvironment>('sandbox');

  const load = useCallback(
    () => readOnboardingStatus(client, unit.id, environment),
    [client, unit.id, environment],
  );
  const { remote, reload } = useRemote<OnboardingStatus>(load);

  const mayOnboard = can('einvoicing.onboard');

  return (
    <section className="zatca">
      <header className="zatca__head">
        <h2 className="ds-h2">{t('zatca.title')}</h2>
        <p className="ds-body-sm ds-muted">{t('zatca.intro')}</p>
      </header>

      <EnvironmentPicker value={environment} onChange={setEnvironment} />

      <RemoteBody remote={remote} onRetry={reload}>
        {(status: OnboardingStatus) => (
          <>
            <StatusPanel status={status} />

            {mayOnboard && needsRenewal(status) && (
              <RenewalForm
                unitId={unit.id}
                environment={environment}
                onDone={reload}
              />
            )}

            {mayOnboard ? (
              <Steps
                unit={unit}
                environment={environment}
                status={status}
                onDone={reload}
              />
            ) : (
              <p className="ds-body-sm ds-muted zatca__readonly">
                {t('zatca.ownerOnly')}
              </p>
            )}

            <CertificateDetail status={status} />
          </>
        )}
      </RemoteBody>
    </section>
  );
}

/** Which ZATCA stack. A segmented control rather than a dropdown: there are
 *  three, they are the most consequential choice on the screen, and a
 *  dropdown hides two of them behind a tap. */
function EnvironmentPicker({
  value,
  onChange,
}: {
  value: ZatcaEnvironment;
  onChange: (v: ZatcaEnvironment) => void;
}) {
  const t = useT();
  return (
    <div className="zatca__envs" role="group" aria-label={t('zatca.environment')}>
      {ENVIRONMENTS.map((e) => (
        <button
          key={e.id}
          type="button"
          className={`zatca__env${value === e.id ? ' zatca__env--on' : ''}`}
          aria-pressed={value === e.id}
          onClick={() => onChange(e.id)}
        >
          {t(e.labelKey)}
        </button>
      ))}
    </div>
  );
}

/** Where this till stands, and what to do next. The first thing on the screen
 *  because it is the only thing most visits need. */
function StatusPanel({ status }: { status: OnboardingStatus }) {
  const t = useT();

  const tone = status.connected
    ? status.needs_renewal
      ? 'ds-badge--warning'
      : 'ds-badge--success'
    : 'ds-badge--neutral';

  const label = status.connected
    ? status.needs_renewal
      ? t('zatca.stateRenewSoon')
      : t('zatca.stateConnected')
    : t('zatca.stateNotConnected');

  return (
    <div className="ds-panel zatca__status">
      <div className="zatca__statusHead">
        <span className={`ds-badge ${tone}`}>{label}</span>
      </div>
      {/* The server's wording, not a second copy of the same logic. Two
          clients disagreeing about what to do next is worse than either. */}
      <p className="zatca__next">{status.next_action}</p>
    </div>
  );
}

function Steps({
  unit,
  environment,
  status,
  onDone,
}: {
  unit: EgsUnit;
  environment: ZatcaEnvironment;
  status: OnboardingStatus;
  onDone: () => void;
}) {
  const t = useT();

  const complianceDone = status.compliance?.status === 'issued';
  const productionDone = status.production?.status === 'issued';

  return (
    <ol className="zatca__steps">
      <Step
        n={1}
        title={t('zatca.step1Title')}
        blurb={t('zatca.step1Blurb')}
        done={unit.csr_complete}
      >
        {!unit.csr_complete && (
          <p className="ds-body-sm zatca__warn">{t('zatca.step1Missing')}</p>
        )}
      </Step>

      <Step
        n={2}
        title={t('zatca.step2Title')}
        blurb={t('zatca.step2Blurb')}
        done={complianceDone}
      >
        <ComplianceForm
          unitId={unit.id}
          environment={environment}
          ready={unit.csr_complete}
          done={complianceDone}
          onDone={onDone}
        />
      </Step>

      <Step
        n={3}
        title={t('zatca.step3Title')}
        blurb={t('zatca.step3Blurb')}
        done={productionDone}
      >
        <ProductionForm
          unitId={unit.id}
          environment={environment}
          ready={complianceDone}
          done={productionDone}
          onDone={onDone}
        />
      </Step>
    </ol>
  );
}

function Step({
  n,
  title,
  blurb,
  done,
  children,
}: {
  n: number;
  title: string;
  blurb: string;
  done: boolean;
  children?: React.ReactNode;
}) {
  return (
    <li className={`zatca__step${done ? ' zatca__step--done' : ''}`}>
      <div className="zatca__stepMark" aria-hidden="true">
        {done ? '✓' : n}
      </div>
      <div className="zatca__stepBody">
        <h3 className="zatca__stepTitle">{title}</h3>
        <p className="ds-body-sm ds-muted">{blurb}</p>
        {children}
      </div>
    </li>
  );
}

/** Step 2: the certificate request and the one-time password.
 *
 *  The CSR is pasted rather than generated here, because generating it would
 *  mean generating a key here, and the key belongs on the terminal. The till
 *  produces both and shows the request; this screen carries it up. */
function ComplianceForm({
  unitId,
  environment,
  ready,
  done,
  onDone,
}: {
  unitId: string;
  environment: ZatcaEnvironment;
  ready: boolean;
  done: boolean;
  onDone: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [csr, setCsr] = useState('');
  const [otp, setOtp] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [errors, setErrors] = useState<{ csr?: string; otp?: string }>({});

  if (done) return null;

  const submit = async () => {
    const found: { csr?: string; otp?: string } = {};
    if (!csr.trim()) found.csr = t('zatca.csrRequired');
    if (!/^[0-9]{6}$/.test(otp.trim())) found.otp = t('zatca.otpFormat');
    setErrors(found);
    if (Object.keys(found).length > 0) return;

    setBusy(true);
    setFailure(null);
    try {
      await requestComplianceCsid(client, unitId, {
        environment,
        csr: csr.trim(),
        otp: otp.trim(),
      });
      // Cleared immediately: the code is spent, and a stale one in a field
      // invites somebody to submit it again and wonder why it is refused.
      setOtp('');
      setCsr('');
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
      // The OTP is cleared on failure too. It is single-use: whatever went
      // wrong, that code will not work a second time, and leaving it there
      // would have somebody retry with a code that cannot succeed.
      setOtp('');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="zatca__form">
      <Field
        label={t('zatca.csr')}
        htmlFor={`csr-${unitId}`}
        required
        error={errors.csr}
        hint={t('zatca.csrHint')}
      >
        <textarea
          id={`csr-${unitId}`}
          className="input zatca__csr"
          rows={4}
          value={csr}
          spellCheck={false}
          placeholder="-----BEGIN CERTIFICATE REQUEST-----"
          onChange={(e) => setCsr(e.target.value)}
          aria-invalid={errors.csr ? true : undefined}
        />
      </Field>

      <Field
        label={t('zatca.otp')}
        htmlFor={`otp-${unitId}`}
        required
        error={errors.otp}
        hint={t('zatca.otpHint')}
      >
        <TextInput
          id={`otp-${unitId}`}
          value={otp}
          onChange={(v) => setOtp(v.replace(/[^0-9]/g, ''))}
          error={errors.otp}
          inputMode="numeric"
          // The phone offers the code from the message rather than making
          // somebody memorise six digits and switch apps to read them.
          autoComplete="one-time-code"
          maxLength={6}
          placeholder="123456"
        />
      </Field>

      <FormError message={failure} />

      <button
        type="button"
        className="ds-btn ds-btn--primary"
        disabled={busy || !ready}
        onClick={() => void submit()}
      >
        {busy ? t('zatca.connecting') : t('zatca.connect')}
      </button>
      {!ready && (
        <p className="ds-body-sm ds-muted">{t('zatca.finishStep1First')}</p>
      )}
    </div>
  );
}

/** Step 3: promotion. No OTP -- the compliance credential is the proof one was
 *  already presented, so asking again would be asking for a code that is not
 *  needed and cannot be checked. */
function ProductionForm({
  unitId,
  environment,
  ready,
  done,
  onDone,
}: {
  unitId: string;
  environment: ZatcaEnvironment;
  ready: boolean;
  done: boolean;
  onDone: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [csr, setCsr] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [error, setError] = useState<string | undefined>();

  if (done) return null;

  const submit = async () => {
    if (!csr.trim()) {
      setError(t('zatca.csrRequired'));
      return;
    }
    setError(undefined);
    setBusy(true);
    setFailure(null);
    try {
      await requestProductionCsid(client, unitId, { environment, csr: csr.trim() });
      setCsr('');
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="zatca__form">
      <Field
        label={t('zatca.csr')}
        htmlFor={`prod-csr-${unitId}`}
        required
        error={error}
        hint={t('zatca.csrHint')}
      >
        <textarea
          id={`prod-csr-${unitId}`}
          className="input zatca__csr"
          rows={4}
          value={csr}
          spellCheck={false}
          placeholder="-----BEGIN CERTIFICATE REQUEST-----"
          onChange={(e) => setCsr(e.target.value)}
          aria-invalid={error ? true : undefined}
        />
      </Field>

      <FormError message={failure} />

      <button
        type="button"
        className="ds-btn ds-btn--primary"
        disabled={busy || !ready}
        onClick={() => void submit()}
      >
        {busy ? t('zatca.promoting') : t('zatca.promote')}
      </button>
      {!ready && <p className="ds-body-sm ds-muted">{t('zatca.finishStep2First')}</p>}
    </div>
  );
}

/** Renewal is offered when the certificate is expiring or already has.
 *
 *  An expired certificate still shows the form: that is exactly when somebody
 *  needs it, and hiding it because the deadline passed would be perverse. */
function needsRenewal(status: OnboardingStatus): boolean {
  if (status.production?.status !== 'issued') return false;
  return status.needs_renewal || !status.connected;
}

function RenewalForm({
  unitId,
  environment,
  onDone,
}: {
  unitId: string;
  environment: ZatcaEnvironment;
  onDone: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [csr, setCsr] = useState('');
  const [otp, setOtp] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [errors, setErrors] = useState<{ csr?: string; otp?: string }>({});

  const submit = async () => {
    const found: { csr?: string; otp?: string } = {};
    if (!csr.trim()) found.csr = t('zatca.csrRequired');
    if (!/^[0-9]{6}$/.test(otp.trim())) found.otp = t('zatca.otpFormat');
    setErrors(found);
    if (Object.keys(found).length > 0) return;

    setBusy(true);
    setFailure(null);
    try {
      await renewProductionCsid(client, unitId, {
        environment,
        csr: csr.trim(),
        otp: otp.trim(),
      });
      setOtp('');
      setCsr('');
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
      // Single-use, so it will not work a second time whatever went wrong.
      setOtp('');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ds-panel zatca__renew">
      <h3 className="zatca__stepTitle">{t('zatca.renewTitle')}</h3>
      <p className="ds-body-sm ds-muted">{t('zatca.renewBlurb')}</p>

      <div className="zatca__form">
        <Field
          label={t('zatca.csr')}
          htmlFor={`renew-csr-${unitId}`}
          required
          error={errors.csr}
          hint={t('zatca.csrHint')}
        >
          <textarea
            id={`renew-csr-${unitId}`}
            className="input zatca__csr"
            rows={4}
            value={csr}
            spellCheck={false}
            placeholder="-----BEGIN CERTIFICATE REQUEST-----"
            onChange={(e) => setCsr(e.target.value)}
            aria-invalid={errors.csr ? true : undefined}
          />
        </Field>

        <Field
          label={t('zatca.otp')}
          htmlFor={`renew-otp-${unitId}`}
          required
          error={errors.otp}
          hint={t('zatca.otpHint')}
        >
          <TextInput
            id={`renew-otp-${unitId}`}
            value={otp}
            onChange={(v) => setOtp(v.replace(/[^0-9]/g, ''))}
            error={errors.otp}
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            placeholder="123456"
          />
        </Field>

        <FormError message={failure} />

        <button
          type="button"
          className="ds-btn ds-btn--primary"
          disabled={busy}
          onClick={() => void submit()}
        >
          {busy ? t('zatca.renewing') : t('zatca.renew')}
        </button>
      </div>
    </div>
  );
}

/** The certificate detail, underneath. Nobody needs it on a good day; on a bad
 *  one it is the whole reason they came. */
function CertificateDetail({ status }: { status: OnboardingStatus }) {
  const t = useT();
  if (!status.compliance && !status.production) return null;

  return (
    <details className="zatca__detail">
      <summary className="zatca__detailSummary">{t('zatca.details')}</summary>
      <div className="ds-scroll-x">
        <table className="ds-table">
          <thead>
            <tr>
              <th scope="col">{t('zatca.credential')}</th>
              <th scope="col">{t('common.status')}</th>
              <th scope="col">{t('zatca.csid')}</th>
              <th scope="col">{t('zatca.expires')}</th>
              <th scope="col">{t('zatca.attempts')}</th>
            </tr>
          </thead>
          <tbody>
            <CredentialRow label={t('zatca.compliance')} c={status.compliance} />
            <CredentialRow label={t('zatca.production')} c={status.production} />
          </tbody>
        </table>
      </div>

      {/* ZATCA's own wording, unaltered. An onboarding refusal names the
          registration field that was wrong, and that is exactly what a
          summary would throw away. */}
      {[status.compliance, status.production].map(
        (c, i) =>
          c?.last_error && (
            <p key={i} className="zatca__error" role="alert">
              {c.last_error}
            </p>
          ),
      )}
    </details>
  );
}

function CredentialRow({ label, c }: { label: string; c: CredentialSummary | null }) {
  const t = useT();
  if (!c) {
    return (
      <tr>
        <th scope="row">{label}</th>
        <td colSpan={4} className="ds-muted">
          {t('zatca.notStarted')}
        </td>
      </tr>
    );
  }
  return (
    <tr>
      <th scope="row">{label}</th>
      <td>{c.status}</td>
      {/* Truncated in the middle: a CSID is long, and the ends are what
          somebody matches against ZATCA's portal. */}
      <td className="zatca__csid">{shorten(c.csid)}</td>
      <td>{c.expires_at ? c.expires_at.slice(0, 10) : '—'}</td>
      <td>{c.attempts}</td>
    </tr>
  );
}

function shorten(csid: string): string {
  if (csid.length <= 20) return csid;
  return `${csid.slice(0, 10)}…${csid.slice(-6)}`;
}
