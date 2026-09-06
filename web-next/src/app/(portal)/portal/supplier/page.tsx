'use client';

// A supplier signing in — F3.
//
// A password, not a one-time code, and the route's comment says why: accepting
// an order commits their business. A code sent to whoever happens to hold a
// phone is the wrong strength of proof for a commitment.

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

export default function SupplierSignInPage() {
  const t = useT();
  const router = useRouter();
  const { shop, token, ready, signIn } = usePortalSession();

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (ready && token) router.replace('/portal/supplier/home');
  }, [ready, token, router]);

  async function submit() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    try {
      const out = await portalApi.post<{ token: string; name: string }>(
        '/portal/supplier/session',
        { shop },
        { email, password },
      );
      signIn(out.token, out.name);
      router.replace('/portal/supplier/home');
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-md">
      <Panel
        title={t('nx.sp.supplierSignInTitle')}
        description={t('nx.sp.supplierSignInLead')}
      >
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <FormError message={error} />

          <Field name="email" label={t('nx.sp.email')} required>
            <Input
              type="email"
              dir="ltr"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoFocus
            />
          </Field>

          <Field name="password" label={t('nx.sp.password')} required>
            <Input
              type="password"
              dir="ltr"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          <Button
            type="submit"
            variant="primary"
            busy={busy}
            disabled={email.trim() === '' || password === ''}
          >
            {t('nx.sp.signIn')}
          </Button>
        </form>
      </Panel>

      <p className="mt-4 text-center text-caption text-muted">
        {t('nx.sp.customerPrompt')}{' '}
        <a className="underline" href="/portal">
          {t('nx.sp.customerSignIn')}
        </a>
      </p>
    </div>
  );
}
