'use client';

// About Biz1core.
//
// # Why it is here and not under Settings
//
// Settings is what a business configures. This is not configuration -- it is
// who made the product -- and every item in the sidebar has to name a
// permission that opens it, which this has none of. So it sits outside the
// workspace route groups, reached from the account menu, and it renders
// without the navigation rail: it is a page about the product rather than a
// page of it.
//
// Both hostnames serve it. An operator on the console and a shopkeeper on the
// business origin reach the same page, which is why `/about` is listed in
// `origin.ts`'s shared prefixes.
//
// # What is deliberately not on it
//
// An e-mail address, a telephone number, an address. The brief asks for the
// four public links and says not to expose anything further, and a page that
// published a private contact detail would be a rebrand that leaked something.
// A business wanting help uses Settings > Support, which opens a ticket
// against their own account.

import { useT } from '@/lib/i18n/locale';

import { AboutBiz1core } from '@/components/shell/product-brand';

export default function AboutPage() {
  const t = useT();
  return (
    <main className="min-h-dvh bg-ground px-4 py-10 lg:py-16">
      {/* The page heading exists for a screen reader and for the browser's
          own outline. Visually the lockup below is the heading -- a page
          titled "About Biz1core" directly above the Biz1core logo says the
          same thing twice. */}
      <h1 className="sr-only">{t('nx.about.title')}</h1>
      <AboutBiz1core />
    </main>
  );
}
