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
import { BUSINESS_NAV, landingFor } from '@/lib/nav/navigation';

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
      if (identity.workspace === 'platform') {
        router.replace('/platform');
        return;
      }
      // NOT /dashboard for everybody. That screen reads GET
      // /dashboard/overview, which is accounting.view -- and a Cashier, a
      // Branch Manager and an Inventory Keeper all hold none of it. Verified
      // against a live cashier account, which resolves to nineteen permissions
      // and gets a 403 from that route. So: the first thing this person can
      // actually open, which for a cashier is the till.
      const home = landingFor(BUSINESS_NAV, identity.grants);
      router.replace(home ?? '/nowhere');
    }
  }, [status, identity, router]);

  return (
    <div className="grid min-h-dvh place-items-center bg-ground" aria-busy="true">
      <p className="sr-only">{t('nx.root.opening')}</p>
    </div>
  );
}
