'use client';

// Signed in, and holding nothing.
//
// A real state, not a defensive one: somebody's last role is taken away while
// they are signed in, or an owner creates an account and is called away before
// assigning anything. `landingFor` returns null for them, and without a screen
// to send them to the front door would redirect into itself.
//
// It says what happened and who can fix it, and offers the one action that
// makes sense. It does not apologise, and it does not pretend a page failed to
// load — nothing is broken, there is simply no work assigned yet.

import { KeyRound } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/states';
import { useSession } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';

export default function NowherePage() {
  const t = useT();
  const { signOut } = useSession();

  return (
    <div className="mx-auto max-w-md py-16">
      <EmptyState
        icon={KeyRound}
        title={t('nx.nowhere.title')}
        description={t('nx.nowhere.body')}
        action={
          <Button variant="secondary" onClick={() => void signOut()}>
            {t('nx.nowhere.signOut')}
          </Button>
        }
      />
    </div>
  );
}
