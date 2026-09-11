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
import { isSameOrigin } from '@/lib/origin';
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
        // An operator who signed in on the business hostname, where the
        // control plane no longer lives. Send them to it.
        //
        // The address comes from `/auth/me` and is answered only to a platform
        // operator, so it reaches somebody who has already proved who they
        // are. Compiling it into the bundle instead would ship the console's
        // hostname to every shop in the world, which publishes the one thing
        // that is deliberately not linked anywhere.
        //
        // `assign` rather than the router: this is a different ORIGIN, and
        // Next's client router cannot navigate to one. It also leaves the
        // business page in history, so nothing loops — coming back lands here
        // again and goes forward again, which is the intended destination
        // rather than a cycle.
        //
        // Only when we are NOT already there. This same page is what the
        // console serves at its own root, so assigning the console's address
        // from the console would load this page again, which would assign it
        // again — a loop with no exit, on the one screen an operator always
        // starts from.
        //
        // Compared by origin rather than by hostname: the configured value is
        // a full address and `location.origin` is the same shape, so scheme
        // and port are included on both sides and a development console on a
        // port does not read as a different site.
        if (identity.consoleUrl && !isSameOrigin(identity.consoleUrl, window.location.origin)) {
          window.location.assign(identity.consoleUrl);
          return;
        }
        // No separate console configured: the two halves share this origin,
        // which is how every deployment worked before the split and how a
        // developer's machine works now.
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
