'use client';

// The product's own identity on the screens that are not a workspace: the
// sign-in page, password recovery, the error and not-found screens, and the
// About page.
//
// # Why these are shared rather than pasted
//
// Before this file the mark was four hand-drawn SVG lines copied into the
// sign-in page, the forgot-password page and the navigation rail, and the
// product's name was a literal string in each. Three copies of a logo is three
// places for a rebrand to miss, and the rebrand did miss: the copies had
// already drifted in size. One component, three callers.

import { Biz1coreLogo } from '@biz1core/shared/brand/Logo';
import { OWNER, PRODUCT_NAME, REPOSITORY_URL } from '@biz1core/shared/brand/brand';
import { ExternalLink } from 'lucide-react';
import Link from 'next/link';

import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

/**
 * The lockup above a sign-in card.
 *
 * The full lockup, tagline included: this is the one screen in the product
 * with room for it and the one screen where somebody may not yet know what
 * they are signing in to.
 *
 * These screens sit on `bg-shell`, the darkest surface in the product in both
 * themes, so the logo is told it is on a dark surface rather than left to read
 * the page theme.
 */
export function AuthBrand({ className }: { className?: string }) {
  return (
    <div className={cn('mb-6 text-shell-fg-strong', className)}>
      <Biz1coreLogo variant="lockup" size={30} onDark />
    </div>
  );
}

/**
 * The mark and the name, for a screen with no session and no chrome -- the
 * error boundary and the not-found page.
 *
 * No tagline. Somebody who has just hit a broken page is not reading a
 * strapline, and a product that answers an error with marketing reads badly.
 */
export function ErrorBrand({ className }: { className?: string }) {
  return (
    <div className={cn('flex justify-center text-fg', className)}>
      <Biz1coreLogo size={26} />
    </div>
  );
}

/**
 * Who built the product, as one quiet line.
 *
 * Used at the foot of the sign-in page and the About page. Deliberately not in
 * the application shell: a shopkeeper working in their own software all day
 * does not need the vendor's name under every screen, and the brief is
 * explicit that the interface must not become crowded.
 *
 * Never on an invoice, a receipt or a statement. Those are the SHOP's
 * documents, they carry the shop's own logo, and the developer's name has no
 * business on paper a customer takes away.
 */
export function BuiltBy({ className }: { className?: string }) {
  const t = useT();
  return (
    // No colour of its own. This line appears on the dark sign-in ground and
    // on the light About page, and a component that named a colour would be
    // wrong on one of them -- so the surface says what colour it is and this
    // inherits it.
    <p className={cn('text-caption', className)}>
      {t('nx.about.builtByShort')}{' '}
      <a
        href={OWNER.website}
        target="_blank"
        // `noopener` is the security half -- without it the opened page gets a
        // handle on this one through `window.opener`. `noreferrer` is the
        // privacy half. Both, on every external link in this file.
        rel="noopener noreferrer"
        className="font-medium underline decoration-current/40 underline-offset-2 hover:decoration-current"
      >
        {OWNER.name}
      </a>
    </p>
  );
}

/**
 * The About panel: what the product is, and who is behind it.
 *
 * One component so the About page and any future landing page render the same
 * thing rather than two descriptions that disagree.
 */
export function AboutBiz1core() {
  const t = useT();

  const links = [
    { href: OWNER.website, label: t('nx.about.website'), value: hostOf(OWNER.website) },
    { href: OWNER.linkedin, label: t('nx.about.linkedin'), value: 'in/mahediemon' },
    { href: OWNER.github, label: t('nx.about.github'), value: 'mahedi-emon' },
    { href: REPOSITORY_URL, label: t('nx.about.repository'), value: 'mahedi-emon/Biz1core' },
  ];

  return (
    <div className="mx-auto w-full max-w-2xl">
      <div className="text-fg">
        <Biz1coreLogo variant="lockup" size={34} />
      </div>

      <p className="mt-6 text-lede text-fg">{t('nx.about.what')}</p>

      <div className="mt-8 rounded-lg border border-line bg-surface p-5">
        <p className="text-caption font-semibold uppercase tracking-wide text-subtle">
          {t('nx.about.builtBy')}
        </p>
        <p className="mt-2 text-section font-semibold text-fg">{OWNER.name}</p>
        <p className="mt-0.5 text-body text-muted">{t('nx.about.role')}</p>

        <ul className="mt-5 grid gap-2 sm:grid-cols-2">
          {links.map((l) => (
            <li key={l.href}>
              <a
                href={l.href}
                target="_blank"
                rel="noopener noreferrer"
                // The visible text is the destination, not "click here": a
                // screen reader listing the links on this page then reads four
                // distinct names instead of the same word four times.
                className={cn(
                  'flex min-h-11 items-center justify-between gap-3 rounded-sm',
                  'border border-line bg-surface-hover px-3 text-body',
                  'hover:border-line-strong hover:bg-surface',
                )}
              >
                <span className="min-w-0">
                  <span className="block text-caption text-subtle">{l.label}</span>
                  <span className="block truncate font-medium text-fg">{l.value}</span>
                </span>
                <ExternalLink className="size-4 shrink-0 text-subtle" aria-hidden="true" />
              </a>
            </li>
          ))}
        </ul>
      </div>

      <Link
        href="/"
        className={cn(
          'mt-8 inline-flex h-10 items-center rounded-sm border border-line-strong',
          'bg-surface px-4 text-body font-medium hover:bg-surface-hover',
        )}
      >
        {t('nx.about.back', { product: PRODUCT_NAME })}
      </Link>
    </div>
  );
}

/** `https://example.com/` -> `example.com`. The bit worth showing. */
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}
