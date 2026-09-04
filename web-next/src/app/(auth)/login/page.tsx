'use client';

// Signing in.
//
// # Nobody chooses a role
//
// There is no "sign in as Owner / Cashier / Admin". The person types what they
// know -- an email and a password -- and the server answers with who they are,
// which business they belong to and what they may do. Asking somebody to
// classify themselves at the door asks them to know something the system
// already knows, and gets it wrong the first time somebody is promoted.
//
// # Two challenges, not two errors
//
// The API can answer 200 with no token twice: once because the email opens
// accounts in more than one business, and once because the account has a second
// factor. Neither is a failure -- the password was right -- so neither is shown
// as one. They are steps, and the form moves to them.

import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { api, type BusinessChoice } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useSession } from '@/lib/auth/session';
import { cn } from '@/lib/utils';

type Step =
  | { kind: 'credentials' }
  | { kind: 'choose_business'; businesses: BusinessChoice[] }
  | { kind: 'need_code' };

function SignInForm() {
  const router = useRouter();
  const params = useSearchParams();
  const { reload } = useSession();

  const [step, setStep] = useState<Step>({ kind: 'credentials' });
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [businessId, setBusinessId] = useState<string | null>(null);
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  async function attempt(overrides: { tenant_id?: string; mfa_code?: string } = {}) {
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const outcome = await api.login({
        email,
        password,
        ...(businessId ? { tenant_id: businessId } : {}),
        ...overrides,
      });

      if (outcome.kind === 'choose_business') {
        setStep({ kind: 'choose_business', businesses: outcome.businesses });
        return;
      }
      if (outcome.kind === 'need_code') {
        setStep({ kind: 'need_code' });
        return;
      }

      // The session provider re-reads /auth/me, which is what decides the
      // workspace. Redirecting before it has is how somebody lands on a
      // business dashboard for a moment before being moved to the platform.
      await reload();

      const next = params.get('next');
      // Only a path from this app, never an absolute URL: `?next=https://…`
      // would turn the sign-in page into an open redirect.
      router.replace(next && next.startsWith('/') && !next.startsWith('//') ? next : '/');
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e));
    } finally {
      setBusy(false);
    }
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (step.kind === 'need_code') {
      void attempt({ mfa_code: code });
      return;
    }
    void attempt();
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-4" noValidate>
      {error && (
        <p
          role="alert"
          className={cn(
            'rounded-sm border border-critical/25 bg-critical-subtle',
            'px-3 py-2 text-body text-critical-fg',
          )}
        >
          {error}
        </p>
      )}

      {step.kind === 'credentials' && (
        <>
          <Field label="Email" error={fieldErrors.email} required>
            <Input
              type="email"
              name="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="username"
              autoFocus
              required
              placeholder="you@yourbusiness.com"
            />
          </Field>

          <Field label="Password" error={fieldErrors.password} required>
            <Input
              type="password"
              name="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </Field>

          <Button type="submit" variant="primary" size="lg" block busy={busy}>
            Sign in
          </Button>

          <a
            href="/forgot-password"
            className="text-center text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            I have forgotten my password
          </a>
        </>
      )}

      {step.kind === 'choose_business' && (
        <>
          <div>
            <h2 className="text-card-title font-semibold text-fg">
              Which business?
            </h2>
            <p className="mt-1 text-body text-muted">
              This sign-in opens more than one. Choose the one you want to work
              in — you can sign out and pick another at any time.
            </p>
          </div>

          <ul className="flex flex-col gap-2">
            {step.businesses.map((b) => (
              <li key={b.tenant_id}>
                <button
                  type="button"
                  onClick={() => {
                    setBusinessId(b.tenant_id);
                    void attempt({ tenant_id: b.tenant_id });
                  }}
                  disabled={busy}
                  className={cn(
                    'flex min-h-11 w-full items-center rounded-sm border border-line-strong',
                    'bg-surface px-3 text-start text-body font-medium',
                    'hover:border-primary hover:bg-surface-selected',
                    'disabled:pointer-events-none disabled:opacity-55',
                  )}
                >
                  {b.name}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}

      {step.kind === 'need_code' && (
        <>
          <div>
            <h2 className="text-card-title font-semibold text-fg">
              Enter your code
            </h2>
            <p className="mt-1 text-body text-muted">
              Open your authenticator app and type the six digits it is showing.
              A recovery code works here too.
            </p>
          </div>

          <Field label="Code" error={fieldErrors.mfa_code} required>
            <Input
              name="mfa_code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              // Not `type="number"`: a recovery code is not numeric, and a
              // number input strips a leading zero from a six-digit code.
              inputMode="numeric"
              autoComplete="one-time-code"
              autoFocus
              required
              numeric
            />
          </Field>

          <Button type="submit" variant="primary" size="lg" block busy={busy}>
            Continue
          </Button>
        </>
      )}
    </form>
  );
}

export default function LoginPage() {
  return (
    // The sign-in sits on the product's own chrome colour rather than on a
    // separate marketing surface, so the first thing somebody sees is the
    // colour they will navigate by all day.
    <main className="grid min-h-dvh place-items-center bg-shell px-4 py-10">
      <div className="w-full max-w-[26rem]">
        <div className="mb-6 flex items-center gap-2.5 text-shell-fg-strong">
          <svg viewBox="0 0 24 24" className="size-7" aria-hidden="true" fill="none">
            <path
              d="M4 5h16M4 12h10M4 19h16"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
            />
            <path
              d="M17 12h3"
              stroke="var(--color-brass-500)"
              strokeWidth="2"
              strokeLinecap="round"
            />
          </svg>
          <span className="text-section font-semibold">RawSyst</span>
        </div>

        <div className="rounded-lg border border-line bg-surface p-6 shadow-overlay">
          <h1 className="text-page font-semibold text-fg">Sign in</h1>
          <p className="mt-1 mb-5 text-body text-muted">
            Your account decides what you see. There is nothing to choose here.
          </p>

          <Suspense fallback={null}>
            <SignInForm />
          </Suspense>
        </div>
      </div>
    </main>
  );
}
