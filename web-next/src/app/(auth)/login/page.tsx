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
// # Two businesses can carry the same name
//
// The choice payload is a tenant id and a name, and nothing else -- so when a
// group trades under one brand in two places, the list is the same word twice
// and the person cannot pick. The id is then the only thing that separates
// them, and a short leading fragment of it is shown for exactly the rows that
// collide. Shown on every row it would be noise; shown on none, the screen is
// a coin toss.
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
import { FormError } from '@/components/ui/form-error';
import { api, type BusinessChoice } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useSession } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

type Step =
  | { kind: 'credentials' }
  | { kind: 'choose_business'; businesses: BusinessChoice[] }
  | { kind: 'need_code' };

function SignInForm() {
  const router = useRouter();
  const params = useSearchParams();
  const t = useT();
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
      setError(messageFor(e, t));
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
      <FormError message={error} fields={fieldErrors} />

      {step.kind === 'credentials' && (
        <>
          <Field label={t('nx.auth.email')} error={fieldErrors.email} required>
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

          <Field label={t('nx.auth.password')} error={fieldErrors.password} required>
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
            {t('nx.auth.signIn')}
          </Button>

          <a
            href="/forgot-password"
            className="text-center text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.auth.forgot')}
          </a>
        </>
      )}

      {step.kind === 'choose_business' &&
        (() => {
          const duplicated = new Set(
            step.businesses
              .map((b) => b.name)
              .filter((name, i, all) => all.indexOf(name) !== i),
          );
          return (
            <>
              <div>
                <h2 className="text-card-title font-semibold text-fg">
                  {t('nx.auth.chooseBusiness')}
                </h2>
                <p className="mt-1 text-body text-muted">
                  {t('nx.auth.chooseBusinessBody')}
                </p>
                {duplicated.size > 0 && (
                  <p className="mt-2 text-caption text-muted">
                    {t('nx.auth.sameName')}
                  </p>
                )}
              </div>

              <ul className="flex flex-col gap-2">
                {step.businesses.map((b) => {
                  // Eight characters of a v4 uuid: enough to separate the rows
                  // on this screen, short enough to read aloud down a phone.
                  const ref = b.tenant_id.slice(0, 8);
                  const ambiguous = duplicated.has(b.name);
                  return (
                    <li key={b.tenant_id}>
                      <button
                        type="button"
                        onClick={() => {
                          setBusinessId(b.tenant_id);
                          void attempt({ tenant_id: b.tenant_id });
                        }}
                        disabled={busy}
                        // The name alone would be the same string twice, so the
                        // accessible name carries the reference as well.
                        aria-label={
                          ambiguous
                            ? `${b.name} — ${t('nx.auth.businessRef', { ref })}`
                            : undefined
                        }
                        className={cn(
                          'flex min-h-11 w-full flex-col justify-center rounded-sm border border-line-strong',
                          'bg-surface px-3 py-2 text-start text-body font-medium',
                          'hover:border-primary hover:bg-surface-selected',
                          'disabled:pointer-events-none disabled:opacity-55',
                        )}
                      >
                        <span>{b.name}</span>
                        {ambiguous && (
                          <span className="num text-caption font-normal text-muted">
                            {t('nx.auth.businessRef', { ref })}
                          </span>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </>
          );
        })()}

      {step.kind === 'need_code' && (
        <>
          <div>
            <h2 className="text-card-title font-semibold text-fg">
              {t('nx.auth.mfaTitle')}
            </h2>
            <p className="mt-1 text-body text-muted">
              {t('nx.auth.mfaBody')}
            </p>
          </div>

          <Field label={t('nx.auth.code')} error={fieldErrors.mfa_code} required>
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
            {t('nx.auth.continue')}
          </Button>
        </>
      )}
    </form>
  );
}

export default function LoginPage() {
  const t = useT();
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
          <h1 className="text-page font-semibold text-fg">
            {t('nx.auth.signIn')}
          </h1>
          <p className="mt-1 mb-5 text-body text-muted">
            {t('nx.auth.subtitle')}
          </p>

          <Suspense fallback={null}>
            <SignInForm />
          </Suspense>
        </div>
      </div>
    </main>
  );
}
