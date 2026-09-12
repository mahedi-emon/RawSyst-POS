'use client';

// Getting back in.
//
// # This screen was linked from sign-in and did not exist
//
// The login form has always carried a "forgot your password" link and there was
// no page behind it, so an owner locked out of their own business had a dead
// link and no way back. Both routes were live the whole time.
//
// # It never says whether the address is on file
//
// `POST /auth/forgot-password` answers 204 either way, and this screen has to
// keep that promise: telling somebody "no account with that email" turns the
// form into a way of testing which addresses belong to a business. So the
// confirmation is conditional in its wording — if that address is on file, a
// code is on its way — and it reads the same for an address that exists and one
// that never did.
//
// # One page, two steps
//
// The code arrives by email and is typed back here, so sending it and using it
// belong on the same screen; a second page would lose anybody who closed the
// tab. The step is local state rather than a route, and the address carries
// forward so nobody types it twice.

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';

type Step = 'ask' | 'reset' | 'done';

function ForgotForm() {
  const t = useT();
  const router = useRouter();

  const [step, setStep] = useState<Step>('ask');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  async function sendCode(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      await api.post('/auth/forgot-password', { email: email.trim() });
      // Always the same next step. A branch here on whether the address was
      // found would leak exactly what the route refuses to.
      setStep('reset');
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function reset(e: FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setFieldErrors({ new_password: t('nx.fp.mismatch') });
      setError(t('nx.fp.notReset'));
      return;
    }
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      await api.post('/auth/reset-password', {
        email: email.trim(),
        code: code.trim(),
        new_password: password,
      });
      setStep('done');
      // Cleared the moment they are no longer needed.
      setPassword('');
      setConfirm('');
      setCode('');
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (step === 'done') {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-body text-fg">{t('nx.fp.doneBody')}</p>
        <Button
          variant="primary"
          size="lg"
          block
          onClick={() => router.push('/login')}
        >
          {t('nx.fp.backToSignIn')}
        </Button>
      </div>
    );
  }

  if (step === 'reset') {
    return (
      <form onSubmit={reset} className="flex flex-col gap-4" noValidate>
        <FormError message={error} fields={fieldErrors} />

        {/* Conditional, and identical whether or not the address exists. */}
        <p className="text-body text-muted">{t('nx.fp.sentBody', { email })}</p>

        <Field name="code" label={t('nx.fp.code')} hint={t('nx.fp.codeHint')}>
          <Input
            dir="ltr"
            inputMode="numeric"
            autoComplete="one-time-code"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            required
          />
        </Field>

        <Field name="new_password" label={t('nx.fp.newPassword')}>
          <Input
            type="password"
            dir="ltr"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>

        <Field name="confirm" label={t('nx.fp.confirm')}>
          <Input
            type="password"
            dir="ltr"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
          />
        </Field>

        <Button type="submit" variant="primary" size="lg" block busy={busy}>
          {t('nx.fp.setPassword')}
        </Button>

        <button
          type="button"
          onClick={() => {
            setStep('ask');
            setError(null);
            setFieldErrors(null);
          }}
          className="text-center text-label text-muted underline underline-offset-4 hover:text-fg"
        >
          {t('nx.fp.wrongEmail')}
        </button>
      </form>
    );
  }

  return (
    <form onSubmit={sendCode} className="flex flex-col gap-4" noValidate>
      <FormError message={error} fields={fieldErrors} />

      <Field name="email" label={t('nx.fp.email')} hint={t('nx.fp.emailHint')}>
        <Input
          type="email"
          dir="ltr"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
      </Field>

      <Button type="submit" variant="primary" size="lg" block busy={busy}>
        {t('nx.fp.sendCode')}
      </Button>

      <Link
        href="/login"
        className="text-center text-label text-muted underline underline-offset-4 hover:text-fg"
      >
        {t('nx.fp.backToSignIn')}
      </Link>
    </form>
  );
}

export default function ForgotPasswordPage() {
  const t = useT();
  return (
    // The ground, the column, the logo and the "Built by" line are the (auth)
    // layout's. This page is its card.
    <div className="rounded-lg border border-line bg-surface p-6 shadow-overlay">
      <h1 className="text-page font-semibold text-fg">{t('nx.fp.title')}</h1>
      <p className="mt-1 mb-5 text-body text-muted">{t('nx.fp.subtitle')}</p>
      <ForgotForm />
    </div>
  );
}
