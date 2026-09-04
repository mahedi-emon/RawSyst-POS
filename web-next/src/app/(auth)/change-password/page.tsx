'use client';

// Choosing your own password, before anything else.
//
// `POST /people` issues a one-time password and answers
// `must_change_password: true` at sign-in. Until this screen existed the client
// parsed that flag and no screen acted on it — so an employee signed in with a
// password their manager had read off a screen, went straight to work, and that
// password stayed valid for ever. The person who issued it could sign in as
// them indefinitely, and every sale that account rang up carried their name.
//
// # Changing it signs you out, and the screen says so first
//
// `handleChangePassword` revokes every session including this one, and its own
// comment is why this is written down: "saying so is the difference between a
// deliberate security step and an apparent bug". So the warning is above the
// button, not in the response.
//
// # The two boxes are checked here, the rest is not
//
// Matching is something the browser can answer instantly and the server would
// spend a round trip on. Everything else about a password — length, reuse,
// whether it is the same one back — is the server's to judge, and guessing at
// its rules here would mean two rulebooks that drift.

import { useRouter } from 'next/navigation';
import { useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useSession } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';

export default function ChangePasswordPage() {
  const t = useT();
  const router = useRouter();
  const { signOut } = useSession();

  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const mismatch = again !== '' && next !== again;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (mismatch) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.post('/auth/change-password', {
        current_password: current,
        new_password: next,
      });
      // The server has already revoked this session. Clearing the client's own
      // idea of it too, so the login screen does not bounce straight back on a
      // token that no longer works.
      await signOut();
      router.replace('/login?changed=1');
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFieldErrors(err.fields);
      setError(messageFor(err, t));
      setBusy(false);
    }
  }

  return (
    <form className="flex flex-col gap-5" onSubmit={submit}>
      <div>
        <h1 className="text-page-title font-semibold text-fg">
          {t('nx.pw.title')}
        </h1>
        <p className="mt-1 text-body text-muted">{t('nx.pw.body')}</p>
      </div>

      <FormError message={error} />

      <Field
        label={t('nx.pw.current')}
        error={fieldErrors.current_password}
        required
      >
        <Input
          type="password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          autoComplete="current-password"
          autoFocus
        />
      </Field>

      <Field label={t('nx.pw.new')} error={fieldErrors.new_password} required>
        <Input
          type="password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          autoComplete="new-password"
        />
      </Field>

      <Field
        label={t('nx.pw.confirm')}
        error={mismatch ? t('nx.pw.mismatch') : undefined}
        required
      >
        <Input
          type="password"
          value={again}
          onChange={(e) => setAgain(e.target.value)}
          autoComplete="new-password"
        />
      </Field>

      {/* Above the button, because it changes what pressing it does. */}
      <p className="text-caption text-muted">{t('nx.pw.signOutWarning')}</p>

      <Button
        type="submit"
        variant="primary"
        busy={busy}
        disabled={current === '' || next === '' || mismatch}
      >
        {t('nx.pw.save')}
      </Button>
    </form>
  );
}
