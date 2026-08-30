// The way back in.
//
// Two steps on one screen: ask for a code, then use it. They are not two routes
// because somebody who has just been told "a code is on its way" should be
// looking at the box it goes in — sending them back to sign-in to find a second
// link is the point at which people give up and telephone.
//
// # What this screen must not say
//
// Not "check your email". The server answers identically for an address that
// exists and one that does not, deliberately, so the client cannot know a mail
// was sent — and often none was. "If that address is on an account, a code is on
// its way" is the true version and is what is shown.
//
// Not "wrong code" as distinct from "expired code". The server returns one
// refusal for wrong, expired, spent and unknown, so that somebody guessing
// cannot tell a near miss from a wrong address. A screen that helpfully
// elaborated would hand back exactly what the server withheld.
import { useState } from 'react';

import { useAuth } from './session';
import { completePasswordReset, requestPasswordReset } from '../api/recovery';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';

type Stage = 'asking' | 'entering' | 'done';

export function ForgotPassword({ onBack }: { onBack: () => void }) {
  const t = useT();
  const { client } = useAuth();

  const [stage, setStage] = useState<Stage>('asking');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function ask(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await requestPasswordReset(client, email);
      // Forward whatever the answer was. The server does not say whether the
      // address exists and this screen must not imply it did.
      setStage('entering');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function complete(e: React.FormEvent) {
    e.preventDefault();

    // Checked here as well as on the server, because this is the one field a
    // person cannot see themselves typing. Getting it wrong and being told so
    // after a round trip — with the code now spent — would be the worst
    // available outcome on this screen.
    if (password !== confirm) {
      setFailure(t('recover.passwordsDiffer'));
      return;
    }

    setBusy(true);
    setFailure(null);
    try {
      await completePasswordReset(client, email, code, password);
      setStage('done');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  if (stage === 'done') {
    return (
      <main className="login">
        <div className="login__card">
          <h1 className="login__title">{t('recover.changed')}</h1>
          <p className="login__subtitle">{t('recover.changedBody')}</p>
          <button className="button button--primary button--large" onClick={onBack}>
            {t('recover.backToSignIn')}
          </button>
        </div>
      </main>
    );
  }

  return (
    <main className="login">
      {stage === 'asking' ? (
        <form className="login__card" onSubmit={(e) => void ask(e)} noValidate>
          <h1 className="login__title">{t('recover.title')}</h1>
          <p className="login__subtitle">{t('recover.intro')}</p>

          <FormError message={failure} />

          <Field label={t('login.email')} htmlFor="recover-email" required>
            <TextInput
              id="recover-email"
              value={email}
              onChange={setEmail}
              type="email"
              autoComplete="username"
            />
          </Field>

          <FormActions
            submitLabel={t('recover.sendCode')}
            busy={busy}
            onCancel={onBack}
          />
        </form>
      ) : (
        <form className="login__card" onSubmit={(e) => void complete(e)} noValidate>
          <h1 className="login__title">{t('recover.enterCode')}</h1>
          {/* Says what is true and no more. The server answers the same for an
              address that exists and one that does not, so this screen cannot
              claim a mail was sent. */}
          <p className="login__subtitle">{t('recover.sentIfExists', { email })}</p>

          <FormError message={failure} />

          <Field
            label={t('recover.code')}
            hint={t('recover.codeHint')}
            htmlFor="recover-code"
            required
          >
            <TextInput
              id="recover-code"
              value={code}
              onChange={setCode}
              // A numeric keypad on a phone, and the browser's own one-time-code
              // autofill on a platform that offers it — which saves retyping six
              // digits from a notification.
              inputMode="numeric"
              autoComplete="one-time-code"
            />
          </Field>

          <Field
            label={t('recover.newPassword')}
            hint={t('recover.newPasswordHint')}
            htmlFor="recover-password"
            required
          >
            <TextInput
              id="recover-password"
              value={password}
              onChange={setPassword}
              type="password"
              autoComplete="new-password"
            />
          </Field>

          <Field
            label={t('recover.confirmPassword')}
            htmlFor="recover-confirm"
            required
          >
            <TextInput
              id="recover-confirm"
              value={confirm}
              onChange={setConfirm}
              type="password"
              autoComplete="new-password"
            />
          </Field>

          <FormActions
            submitLabel={t('recover.setPassword')}
            busy={busy}
            onCancel={() => setStage('asking')}
          />
        </form>
      )}
    </main>
  );
}
