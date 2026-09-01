// The customer and supplier portal, blueprint F2 and F3.
//
// A route of its own rather than a section of the back office, because the
// person reading it does not work for the shop and must never see the shop's
// own screens. It carries no AuthProvider: a portal session is a bearer token
// held by the portal itself, and putting a staff session provider around it
// would be the beginning of confusing the two.
//
// The shop is named in the link: /portal?tenant=…&company=… is what a shop
// sends its customers. Reading it from the query string rather than from a
// subdomain keeps a deployment to one host, which is what a small shop can
// actually be given.

import { Suspense } from 'react';

import { PortalPage } from '@/components/PortalPage';

export default function Page() {
  return (
    <Suspense>
      <PortalPage />
    </Suspense>
  );
}
