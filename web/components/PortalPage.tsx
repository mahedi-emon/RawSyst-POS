'use client';

// The client boundary for the portal.
//
// Deliberately NOT wrapped in AuthProvider. A portal caller holds a bearer
// token issued by the portal's own sign-in, not a staff session, and putting
// the staff provider around this tree would be the first step towards treating
// the two as interchangeable. They are not: they live in different tables, are
// carried differently, and open different routes.
//
// LocaleProvider is shared, because a customer reads Arabic or Bangla for
// exactly the same reasons a cashier does.

import { LocaleProvider, useT } from '@rawsyst/shared/i18n/locale';
import { PortalApp } from '@rawsyst/shared/portals/PortalApp';
import { useSearchParams } from 'next/navigation';

export function PortalPage() {
  const params = useSearchParams();
  const tenantId = params.get('tenant') ?? '';
  const companyId = params.get('company') ?? '';

  // A link without both is not a link to any shop's portal. Saying so plainly
  // is better than a sign-in box that refuses every attempt for a reason
  // nobody can see.
  if (!tenantId || !companyId) {
    return (
      <LocaleProvider>
        <BrokenLink />
      </LocaleProvider>
    );
  }

  return (
    <LocaleProvider>
      <PortalApp shop={{ tenantId, companyId }} />
    </LocaleProvider>
  );
}

/** A link that does not name a shop. Inside the provider so it is translated. */
function BrokenLink() {
  const t = useT();
  return (
    <main className="ptl__signin">
      <div className="ptl__card">
        <h1 className="ds-h1">{t('ptl.brokenLink')}</h1>
        <p className="ds-caption">{t('ptl.brokenLinkHint')}</p>
      </div>
    </main>
  );
}
