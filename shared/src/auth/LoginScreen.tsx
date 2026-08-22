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

export function LoginScreen() {
  const { signIn, status, tenantChoices, clearTenantChoices } = useAuth();

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
        setProblem(
          // Also shared, so it says "this device" rather than "this till". The
        // reassurance about queued sales stays: it is true on a terminal and
        // harmlessly irrelevant on a laptop, where nothing was queued anyway.
        'This device cannot reach the server. Check the network, or ask an ' +
            'owner. Nothing already recorded on this device has been lost.',
        );
      } else if (err instanceof RequestFailed && err.status === 401) {
        setProblem('That email and password do not match an account here.');
      } else if (err instanceof RequestFailed) {
        setProblem(err.message);
      } else {
        setProblem('Sign-in did not complete. Try again.');
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
  if (tenantChoices.length > 0) {
    return (
      <main className="login">
        <div className="login__card">
          <h1 className="login__title">RawSyst</h1>
          <p className="login__subtitle">{t('login.chooseTenant')}</p>
          <p className="login__hint">
            This email is used by more than one business. Choose the one you
            want to work in — you can sign out and pick another at any time.
          </p>

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
          >
            Use a different email
          </button>
        </div>
      </main>
    );
  }

  return (
    <main className="login">
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
      </form>
    </main>
  );
}
