'use client';

// The portal shell — F2 and F3.
//
// # A different product wearing the same clothes
//
// Nothing here is the back office. There is no sidebar, no company picker, no
// permission catalogue and no staff session: a portal caller is a customer or
// a supplier, and the only thing they can reach is their own record. The shell
// is a header, a row of tabs, and the page.
//
// # The shop is in the link, and then it is remembered
//
// Every portal route takes `tenant_id` and `company_id`, because a portal
// belongs to one shop — the customer of one branch of a group is not the
// customer of another. So the link a shop sends its customers carries both.
//
// Threading them through every href afterwards would put two ids in the
// address bar of every page and make one forgotten link a dead end, so they
// are read from the URL once and kept for the tab. A link arriving with
// different ids replaces them, which is what makes signing into a second shop
// work rather than silently reusing the first.
//
// # Mobile first, and not as a slogan
//
// F2 says "lightweight, mobile-friendly". A customer opens this on a phone,
// standing in a shop, to check whether something is still under warranty. The
// layout is one column at every width and only widens its measure on a large
// screen.

import Link from 'next/link';
import { usePathname, useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useState, type ReactNode } from 'react';

import { LanguageSwitch } from '@/components/shell/language-switch';
import { Button } from '@/components/ui/button';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import {
  PortalSessionProvider,
  usePortalSession,
  type Shop,
} from '@/lib/portal/session';

const SHOP_KEY = 'rawsyst.portal.shop';

function readRememberedShop(): Shop | null {
  try {
    const raw = sessionStorage.getItem(SHOP_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    const shop = parsed as Partial<Shop>;
    if (typeof shop.tenantId !== 'string' || typeof shop.companyId !== 'string') {
      return null;
    }
    return { tenantId: shop.tenantId, companyId: shop.companyId };
  } catch {
    return null;
  }
}

/** Resolves which shop this page belongs to: the link first, then the tab. */
function useShop(): { shop: Shop | null; ready: boolean } {
  const params = useSearchParams();
  const [state, setState] = useState<{ shop: Shop | null; ready: boolean }>({
    shop: null,
    ready: false,
  });

  const tenantId = params.get('tenant_id');
  const companyId = params.get('company_id');

  useEffect(() => {
    if (tenantId && companyId) {
      const shop = { tenantId, companyId };
      try {
        sessionStorage.setItem(SHOP_KEY, JSON.stringify(shop));
      } catch {
        // Held in state for this page either way.
      }
      setState({ shop, ready: true });
      return;
    }
    setState({ shop: readRememberedShop(), ready: true });
  }, [tenantId, companyId]);

  return state;
}

function Shell({ children }: { children: ReactNode }) {
  const t = useT();
  const pathname = usePathname();
  const { shop, token, name, signOut } = usePortalSession();

  const supplier = pathname.startsWith('/portal/supplier');

  const tabs = supplier
    ? [
        { href: '/portal/supplier/home', label: t('nx.sp.tabHome') },
        { href: '/portal/supplier/orders', label: t('nx.sp.tabOrders') },
        { href: '/portal/supplier/rfqs', label: t('nx.sp.tabRfqs') },
        { href: '/portal/supplier/bills', label: t('nx.sp.tabBills') },
      ]
    : [
        { href: '/portal/account', label: t('nx.sp.tabAccount') },
        { href: '/portal/invoices', label: t('nx.sp.tabInvoices') },
        { href: '/portal/orders', label: t('nx.sp.tabOrders') },
        { href: '/portal/returns', label: t('nx.sp.tabReturns') },
        { href: '/portal/warranty', label: t('nx.sp.tabWarranty') },
        { href: '/portal/addresses', label: t('nx.sp.tabAddresses') },
      ];

  async function endSession() {
    // Told to the server as well as forgotten here. A token that stays valid
    // after somebody presses "sign out" on a shared phone is not signed out.
    if (shop && token) {
      try {
        await portalApi.del('/portal/session', { shop, token });
      } catch {
        // The local half is what the next person on this phone meets.
      }
    }
    signOut();
  }

  return (
    <div className="min-h-dvh bg-ground">
      <header className="border-b border-line bg-surface">
        <div className="mx-auto flex max-w-3xl flex-wrap items-center gap-3 px-4 py-3">
          <span className="font-semibold">{t('nx.sp.title')}</span>
          <div className="ms-auto flex items-center gap-2">
            <LanguageSwitch />
            {token ? (
              <Button variant="ghost" size="sm" onClick={() => void endSession()}>
                {t('nx.sp.signOut')}
              </Button>
            ) : null}
          </div>
        </div>

        {token ? (
          <nav
            aria-label={t('nx.sp.title')}
            className="mx-auto max-w-3xl overflow-x-auto px-4"
          >
            <ul className="flex min-w-max gap-1 pb-2">
              {tabs.map((tab) => {
                const here = pathname === tab.href;
                return (
                  <li key={tab.href}>
                    <Link
                      href={tab.href}
                      aria-current={here ? 'page' : undefined}
                      className={[
                        'inline-block rounded-xs px-3 py-1.5 text-caption',
                        here
                          ? 'bg-primary-subtle font-medium text-primary-subtle-fg'
                          : 'text-muted hover:text-fg',
                      ].join(' ')}
                    >
                      {tab.label}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </nav>
        ) : null}
      </header>

      <main className="mx-auto max-w-3xl px-4 py-6">
        {name !== '' && token ? (
          <p className="mb-4 text-caption text-muted">
            {t('nx.sp.signedInAs', { name })}
          </p>
        ) : null}
        {children}
      </main>
    </div>
  );
}

function PortalFrame({ children }: { children: ReactNode }) {
  const t = useT();
  const { shop, ready } = useShop();

  if (!ready) return <div className="min-h-dvh bg-ground" aria-busy="true" />;

  // A link with no shop on it is not a portal page at all. Saying so plainly
  // beats a screen of empty lists that a customer would read as "the shop has
  // nothing of mine".
  if (!shop) {
    return (
      <div className="grid min-h-dvh place-items-center bg-ground px-4">
        <div className="max-w-md text-center">
          <h1 className="text-lg font-semibold">{t('nx.sp.noShopTitle')}</h1>
          <p className="mt-2 text-caption text-muted">{t('nx.sp.noShopBody')}</p>
        </div>
      </div>
    );
  }

  return (
    <PortalSessionProvider shop={shop}>
      <Shell>{children}</Shell>
    </PortalSessionProvider>
  );
}

export default function PortalLayout({ children }: { children: ReactNode }) {
  return (
    <Suspense fallback={<div className="min-h-dvh bg-ground" aria-busy="true" />}>
      <PortalFrame>{children}</PortalFrame>
    </Suspense>
  );
}
