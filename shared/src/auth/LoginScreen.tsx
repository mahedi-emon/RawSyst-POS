// The first screen. Nothing else is reachable until this succeeds.
//
// Designed for the actual conditions: a shop floor, a cashier who may be
// wearing gloves, a screen that may be a cheap touch panel in bright light. So
// the targets are large, the contrast is high, and the error messages say what
// to do rather than what went wrong internally.

import { useState, type FormEvent } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from './session';
import { useT } from '../i18n/locale';
import { LanguageSwitch } from '../i18n/LanguageSwitch';
import { ForgotPassword } from './ForgotPassword';

export function LoginScreen() {
  const { signIn, status, tenantChoices, clearTenantChoices } = useAuth();

  // Recovery is a state of this screen rather than a route of its own.
  //
  // Both front ends render <LoginScreen /> with no props — the till because it
  // has no router at all, the back office because sign-in is what it shows
  // when there is no session. Making the caller own this state would mean
  // teaching both of them about a screen neither otherwise knows exists.
  const [recovering, setRecovering] = useState(false);

  const t = useT();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [problem, setProblem] = useState<string | null>(null);

  const busy = status === 'signing_in';

  async function submit(e: FormEvent) {
    e.preventDefault();
    await attempt();
  }

  /** Signs in again, this time naming the business.
   *
   * The whole check runs from scratch on the server against that tenant — the
   * choice is a filter on the lookup, not a token that skips anything. */
  async function choose(tenantId: string) {
    await attempt(tenantId);
  }

  async function attempt(tenantId?: string) {
    setProblem(null);
    try {
      await signIn(email.trim(), password, tenantId);
    } catch (err) {
      // The three cases a cashier can actually act on, told apart. "Something
      // went wrong" would leave them retrying a password that was never the
      // problem.
      if (err instanceof Offline) {
        // Also shared, so it says "this device" rather than "this till". The
        // reassurance about queued sales stays: it is true on a terminal and
        // harmlessly irrelevant on a laptop, where nothing was queued anyway.
        setProblem(t('login.offline'));
      } else if (err instanceof RequestFailed && err.status === 401) {
        setProblem(t('login.badCredentials'));
      } else if (err instanceof RequestFailed) {
        setProblem(err.message);
      } else {
        setProblem(t('login.failed'));
      }
    }
  }

  // The second step, and only when the server asked for it.
  //
  // One email can belong to several businesses — a bookkeeper serving two
  // shops, an owner with two companies. The password has already been checked
  // against every account it could mean; these are the ones it opened, so the
  // only thing still unknown is which the person meant.
  //
  // It is a list of buttons rather than a dropdown and a Continue: there are
  // two or three of them, and a dropdown would add a press for nothing.
  if (recovering) {
    return <ForgotPassword onBack={() => setRecovering(false)} />;
  }

  if (tenantChoices.length > 0) {
    return (
      <main className="login">
      {/* Before the password, not after it.
        *
        * The one screen in the product a person reaches before the interface
        * knows anything about them, and the only place a cashier who reads
        * Arabic or Bangla and not English can do anything at all. Leaving the
        * switch until after sign-in means asking them to read an English form
        * to reach the control that would have translated it. */}
      <div className="login__lang">
        <LanguageSwitch />
      </div>

        <div className="login__card">
          <h1 className="login__title">RawSyst</h1>
          <p className="login__subtitle">{t('login.chooseTenant')}</p>
          <p className="login__hint">{t('login.multiBusiness')}</p>

          {problem && (
            <p className="login__error" role="alert">
              {problem}
            </p>
          )}

          <ul className="login__tenants">
            {tenantChoices.map((choice) => (
              <li key={choice.tenant_id}>
                <button
                  className="button button--large button--primary login__tenant"
                  disabled={busy}
                  onClick={() => void choose(choice.tenant_id)}
                >
                  {choice.name}
                </button>
              </li>
            ))}
          </ul>

          <button
            className="button button--quiet"
            onClick={() => {
              clearTenantChoices();
              setProblem(null);
              // The password is cleared on the way back. Leaving it in a field
              // behind a screen the person has navigated away from is the sort
              // of thing that ends up in a screenshot.
              setPassword('');
            }}
          >{t('login.useAnotherEmail')}</button>
        </div>
      </main>
    );
  }

  return (
    <main className="login">
      {/* Before the password, not after it.
        *
        * The one screen in the product a person reaches before the interface
        * knows anything about them, and the only place a cashier who reads
        * Arabic or Bangla and not English can do anything at all. Leaving the
        * switch until after sign-in means asking them to read an English form
        * to reach the control that would have translated it. */}
      <div className="login__lang">
        <LanguageSwitch />
      </div>

      <form className="login__card" onSubmit={submit}>
        <h1 className="login__title">RawSyst</h1>
        {/* The screen is shared by the till and the back office, so it does
            not claim to be either. "Sign in to open the till" was on the back
            office until a browser check read it out loud — a buyer signing in
            to raise a purchase order is not opening a till. */}
        <p className="login__subtitle">{t('login.continue')}</p>

        <label className="field">
          <span className="field__label">{t('login.email')}</span>
          <input
            className="field__input"
            type="email"
            autoComplete="username"
            autoFocus
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={busy}
          />
        </label>

        <label className="field">
          <span className="field__label">{t('login.password')}</span>
          <input
            className="field__input"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={busy}
          />
        </label>

        {problem && (
          // aria-live so a screen reader announces the refusal rather than
          // leaving someone waiting for a form that silently did nothing.
          <p className="login__problem" role="alert" aria-live="assertive">
            {problem}
          </p>
        )}

        <button className="button button--primary button--large" disabled={busy}>
          {busy ? t('login.working') : t('login.submit')}
        </button>

        {/* The way back in, on the screen where somebody discovers they need
            it. A4.2 puts self-service ahead of the phone call to the platform
            operator, and a recovery route nobody can find is the phone call
            with extra steps. */}
        <button
          type="button"
          className="button button--quiet login__forgot"
          onClick={() => setRecovering(true)}
        >
          {t('recover.forgot')}
        </button>
      </form>
    </main>
  );
}
