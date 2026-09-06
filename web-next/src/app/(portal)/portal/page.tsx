'use client';

// Signing in to a shop's customer portal — F2.
//
// # A phone and a code, not a password
//
// A shop's customers do not have accounts they chose a password for. They gave
// a phone number at a till. So the portal issues a one-time code, and
// `POST /portal/code` answers identically whether or not the number is on
// file — which means this screen must ALSO say the same thing either way. A
// message that changed would turn the portal into a way of asking a shop
// whether a particular person shops there, and it would do so through a screen
// that looked helpful.
//
// So the second step always renders. There is no "we could not find you".
//
// # Signed in already
//
// A tab that already holds a session is sent to the account rather than shown
// a form it does not need.

import { useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import { usePortalSession } from '@/lib/portal/session';

export default function PortalSignInPage() {
  const t = useT();
  const router = useRouter();
  const { shop, token, ready, signIn } = usePortalSession();

  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (ready && token) router.replace('/portal/account');
  }, [ready, token, router]);

  async function askForCode() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    try {
      await portalApi.post('/portal/code', { shop }, { phone });
      // Shown whatever the answer was. See the note above.
      setSent(true);
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function exchange() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    try {
      const out = await portalApi.post<{ token: string; name: string }>(
        '/portal/session',
        { shop },
        { phone, code },
      );
      signIn(out.token, out.name);
      router.replace('/portal/account');
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-md">
      <Panel title={t('nx.sp.signInTitle')} description={t('nx.sp.signInLead')}>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void (sent ? exchange() : askForCode());
          }}
        >
          <FormError message={error} />

          <Field name="phone" label={t('nx.sp.fPhone')} hint={t('nx.sp.fPhoneHint')} required>
            <Input
              type="tel"
              dir="ltr"
              inputMode="tel"
              autoComplete="tel"
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
              disabled={sent}
              autoFocus
            />
          </Field>

          {sent ? (
            <>
              <p className="rounded-xs border border-line bg-ground px-3 py-2 text-caption text-muted">
                {t('nx.sp.codeSent')}
              </p>
              <Field name="code" label={t('nx.sp.fCode')} required>
                <Input
                  dir="ltr"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  className="num"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  autoFocus
                />
              </Field>
            </>
          ) : null}

          <Button
            type="submit"
            variant="primary"
            busy={busy}
            disabled={phone.trim() === '' || (sent && code.trim() === '')}
          >
            {sent ? t('nx.sp.signIn') : t('nx.sp.sendCode')}
          </Button>

          {sent ? (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setSent(false);
                setCode('');
                setError(null);
              }}
            >
              {t('nx.sp.differentNumber')}
            </Button>
          ) : null}
        </form>
      </Panel>

      <p className="mt-4 text-center text-caption text-muted">
        {t('nx.sp.supplierPrompt')}{' '}
        <a className="underline" href="/portal/supplier">
          {t('nx.sp.supplierSignIn')}
        </a>
      </p>
    </div>
  );
}
