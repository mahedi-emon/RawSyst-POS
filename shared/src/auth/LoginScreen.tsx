// The first screen. Nothing else is reachable until this succeeds.
//
// Designed for the actual conditions: a shop floor, a cashier who may be
// wearing gloves, a screen that may be a cheap touch panel in bright light. So
// the targets are large, the contrast is high, and the error messages say what
// to do rather than what went wrong internally.

import { useState, type FormEvent } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from './session';

export function LoginScreen() {
  const { signIn, status } = useAuth();

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [problem, setProblem] = useState<string | null>(null);

  const busy = status === 'signing_in';

  async function submit(e: FormEvent) {
    e.preventDefault();
    setProblem(null);
    try {
      await signIn(email.trim(), password);
    } catch (err) {
      // The three cases a cashier can actually act on, told apart. "Something
      // went wrong" would leave them retrying a password that was never the
      // problem.
      if (err instanceof Offline) {
        setProblem(
          'This till cannot reach the server. Check the network, or ask an ' +
            'owner — sales already recorded on this terminal are safe.',
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

  return (
    <main className="login">
      <form className="login__card" onSubmit={submit}>
        <h1 className="login__title">RawSyst POS</h1>
        <p className="login__subtitle">Sign in to open the till</p>

        <label className="field">
          <span className="field__label">Email</span>
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
          <span className="field__label">Password</span>
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
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </main>
  );
}
