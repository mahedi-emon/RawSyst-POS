'use client';

import Link from 'next/link';

import { useT } from '@/lib/i18n/locale';

// A URL that is not a screen.
//
// Distinct from the 404 a record gets when it belongs to another business:
// that one is answered by the API and shown by `ErrorState`, and its wording is
// careful not to confirm whether the record exists. This one is simply an
// address that is not part of the product, and can say so plainly.
//
// A client component so it can be read in Arabic and Bangla. A not-found page
// that is English-only is a page somebody meets when they are already lost.

export default function NotFound() {
  const t = useT();
  return (
    <main className="grid min-h-dvh place-items-center bg-ground px-4">
      <div className="max-w-md text-center">
        <p className="text-label font-semibold text-muted">
          {t('nx.notfound.eyebrow')}
        </p>
        <h1 className="mt-1 text-page font-semibold text-fg">
          {t('nx.notfound.title')}
        </h1>
        <p className="mt-2 text-body text-muted">{t('nx.notfound.body')}</p>
        <Link
          href="/"
          className="mt-5 inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-4 text-body font-medium hover:bg-surface-hover"
        >
          {t('nx.notfound.action')}
        </Link>
      </div>
    </main>
  );
}
