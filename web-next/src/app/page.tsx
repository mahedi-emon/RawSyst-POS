'use client';

// The front door.
//
// Nothing is rendered here. The session says which workspace this person
// belongs in and they are sent there, so `/` is never a screen somebody has to
// read or a menu they have to choose from. A platform operator and a cashier
// type the same address and arrive somewhere different, which is the whole
// point of the single sign-in.

import { useRouter } from 'next/navigation';
import { useEffect } from 'react';

import { useSession } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';

export default function Root() {
  const t = useT();
  const { status, identity } = useSession();
  const router = useRouter();

  useEffect(() => {
    if (status === 'signed-out') {
      router.replace('/login');
      return;
    }
    if (status === 'signed-in' && identity) {
      router.replace(identity.workspace === 'platform' ? '/platform' : '/dashboard');
    }
  }, [status, identity, router]);

  return (
    <div className="grid min-h-dvh place-items-center bg-ground" aria-busy="true">
      <p className="sr-only">{t('nx.root.opening')}</p>
    </div>
  );
}
