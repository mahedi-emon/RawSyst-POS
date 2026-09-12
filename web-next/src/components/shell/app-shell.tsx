'use client';

// The shell every workspace sits in.
//
// # Three layouts, not one layout scaled down
//
// Desktop gets a persistent sidebar, because an owner moving between stock and
// money all morning should not open a menu each time. Below `lg` the sidebar
// becomes a drawer reached from the top bar, because 260px of navigation on a
// 768px screen is a third of the working area spent on something read once.
// Below `md` the top bar also carries the page title, since the sidebar that
// would otherwise say where you are is closed.
//
// The drawer is not a shrunken sidebar: it is full height, its targets are
// 44px rather than 36px, and it closes on navigation. A desktop sidebar behaves
// differently on all three counts and would be wrong on a phone.
//
// # The rail is dark, and that is a decision
//
// Navigation is chrome. Making it the darkest surface in the product pushes it
// behind the data rather than competing with it, and gives the one place a
// person looks for "where am I" a shape they can find without reading.

import { PRODUCT_NAME } from '@biz1core/shared/brand/brand';
import { Biz1coreLogo, Biz1coreMark } from '@biz1core/shared/brand/Logo';
import { ChevronLeft, Menu, X } from 'lucide-react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState, type ReactNode } from 'react';

import { useLocale, useT } from '@/lib/i18n/locale';
import {
  findActiveItem,
  type ResolvedSection,
} from '@/lib/nav/navigation';
import { cn } from '@/lib/utils';

import { NavTree } from './nav-tree';
import { UserMenu } from './user-menu';

export function AppShell({
  sections,
  /** "Biz1core" for a business; "Biz1core Platform" for the operator. */
  workspaceName,
  /** The business name, or the operator's own label. Shown under the mark. */
  contextName,
  /** Workspace-specific header controls -- the company switch, in a business. */
  headerExtra,
  /**
   * Shown at every width, unlike `headerExtra`.
   *
   * The bell carries a count somebody is meant to notice, and a control that
   * disappears below `sm` is one a shopkeeper on a phone never sees. It sits
   * beside the account menu, which is where every product this one competes
   * with puts it.
   */
  headerAlerts,
  children,
}: {
  sections: ResolvedSection[];
  workspaceName: string;
  contextName?: string;
  headerExtra?: ReactNode;
  headerAlerts?: ReactNode;
  children: ReactNode;
}) {
  const pathname = usePathname();
  const t = useT();
  const { direction } = useLocale();
  const [drawerOpen, setDrawerOpen] = useState(false);

  const active = findActiveItem(sections, pathname);

  // Closing on navigation is the behaviour a drawer needs and a sidebar must
  // not have. Keyed on the pathname rather than on the link's own click, so a
  // navigation from anywhere -- a breadcrumb, the back button -- also closes it.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawerOpen(false);
    };
    document.addEventListener('keydown', onKey);
    // The page behind a modal drawer must not scroll: on a phone it is the
    // difference between closing the drawer and losing your place.
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [drawerOpen]);

  return (
    <div className="min-h-dvh bg-ground">
      {/* The first thing a keyboard reaches. Visible only when focused, which
          is when it is useful. */}
      <a
        href="#main"
        className={cn(
          'sr-only focus:not-sr-only focus:fixed focus:top-2 focus:start-2 focus:z-50',
          'focus:rounded-sm focus:bg-surface focus:px-3 focus:py-2 focus:text-body',
          'focus:shadow-overlay',
        )}
      >
        {t('nx.shell.skip')}
      </a>

      {/* ---- desktop sidebar ---- */}
      <aside
        className={cn(
          'fixed inset-y-0 start-0 z-30 hidden w-[248px] lg:flex lg:flex-col',
          'bg-shell text-shell-fg',
          // Not on paper. A receipt printed from this product used to come out
          // with the navigation rail down the side of it -- the vendor's logo
          // and a shop's whole menu on a document a CUSTOMER is handed. The
          // receipt screen's own note says "everything that is not the receipt
          // is hidden at print time"; nothing was doing it.
          'print:hidden',
        )}
      >
        <BrandMark workspaceName={workspaceName} contextName={contextName} />
        <NavTree sections={sections} activeHref={active?.item.href} />
      </aside>

      {/* ---- mobile drawer ---- */}
      {drawerOpen && (
        <div className="lg:hidden">
          <div
            className="fixed inset-0 z-40 bg-[rgb(15_27_24/0.5)]"
            onClick={() => setDrawerOpen(false)}
            aria-hidden="true"
          />
          <aside
            role="dialog"
            aria-modal="true"
            aria-label={t('nx.shell.navigation')}
            className={cn(
              'fixed inset-y-0 start-0 z-50 flex w-[min(86vw,320px)] flex-col',
              'bg-shell text-shell-fg shadow-overlay scroll-trap',
            )}
          >
            <div className="flex items-center justify-between pe-2">
              <BrandMark
                workspaceName={workspaceName}
                contextName={contextName}
              />
              <button
                type="button"
                onClick={() => setDrawerOpen(false)}
                aria-label={t('nx.shell.closeNav')}
                className="grid size-11 place-items-center rounded-sm text-shell-fg hover:bg-shell-hover"
              >
                <X className="size-5" aria-hidden="true" />
              </button>
            </div>
            {/* Larger targets than the desktop tree: a thumb, not a cursor. */}
            <NavTree
              sections={sections}
              activeHref={active?.item.href}
              touch
            />
          </aside>
        </div>
      )}

      {/* ---- content column ---- */}
      <div className="lg:ms-[248px] print:ms-0">
        <header
          className={cn(
            'sticky top-0 z-20 flex h-14 items-center gap-2 px-3 lg:px-6',
            // See the rail: chrome does not print.
            'print:hidden',
            // Not transparent with a blur. A translucent bar over a table of
            // figures makes the top row of the table hard to read, which is
            // the row most likely to matter.
            'border-b border-line bg-surface',
          )}
        >
          <button
            type="button"
            onClick={() => setDrawerOpen(true)}
            aria-label={t('nx.shell.openNav')}
            className="grid size-10 place-items-center rounded-sm hover:bg-surface-hover lg:hidden"
          >
            <Menu className="size-5" aria-hidden="true" />
          </button>

          {/* On a phone the sidebar is closed, so the bar has to say where you
              are. On desktop the sidebar already does, so this is the section
              rather than a repeat of the page title. */}
          <div className="min-w-0 flex-1">
            {active && (
              <p className="truncate text-body font-medium text-fg lg:text-label lg:font-normal lg:text-muted">
                <span className="lg:hidden">{t(active.item.labelKey)}</span>
                <span className="hidden lg:inline">
                  {t(active.section.labelKey)}
                  <ChevronLeft
                    className={cn(
                      'mx-1 inline size-3 align-middle text-disabled',
                      direction === 'ltr' && 'rotate-180',
                    )}
                    aria-hidden="true"
                  />
                  <span className="text-fg">{t(active.item.labelKey)}</span>
                </span>
              </p>
            )}
          </div>

          {/* Hidden on the narrowest screens: a company switch and a page
              title cannot both fit at 360px, and the title is what tells
              somebody where they are. It returns at `sm`. */}
          {headerExtra && <div className="hidden sm:flex">{headerExtra}</div>}

          {headerAlerts}
          <UserMenu />
        </header>

        <main id="main" className="px-3 py-5 lg:px-6 lg:py-6 print:p-0">
          {children}
        </main>
      </div>
    </div>
  );
}

/**
 * The logo at the top of the rail, and the link home.
 *
 * # Two names, and which one is the logo
 *
 * `workspaceName` is the product in a business workspace and the product plus
 * "Console" in the operator's. Where it is exactly the product name, the
 * Biz1core logo is drawn -- the mark and the wordmark, set in the typeface the
 * rail already uses. Where it is a longer label the logo becomes the compact
 * mark with the label beside it, because "Biz1core Console" set at wordmark
 * weight overflows 248px and truncates to "Biz1core Conso...".
 *
 * # Why the tagline is not here
 *
 * It is nine words. At rail width it wraps to three lines and pushes the first
 * navigation group below the fold on a laptop. It belongs on the sign-in page
 * and in About, where there is room to read it.
 */
function BrandMark({
  workspaceName,
  contextName,
}: {
  workspaceName: string;
  contextName?: string;
}) {
  const t = useT();
  const isProduct = workspaceName === PRODUCT_NAME;

  return (
    <Link
      href="/"
      className="flex h-14 flex-col justify-center gap-0.5 px-4 text-shell-fg-strong"
      // The link goes home; the logo says which product. Both, because
      // "Biz1core" alone does not tell somebody on a screen reader that
      // activating it navigates.
      aria-label={`${workspaceName} — ${t('nx.shell.home')}`}
    >
      {isProduct ? (
        // The full logo -- leaf, bars and the whole wordmark. The rail is
        // 248px and the logo is 124px at this height, so the word fits without
        // being reduced to the mark. The rail is also the darkest surface in
        // the product in BOTH themes, so the logo is TOLD it is on a dark one
        // rather than left to read the page theme, which in light mode would
        // hand it the navy wordmark.
        <Biz1coreLogo height={25} onDark />
      ) : (
        <span className="flex items-center gap-2">
          <Biz1coreMark height={24} onDark />
          <span className="min-w-0 truncate text-lede font-semibold leading-tight">
            {workspaceName}
          </span>
        </span>
      )}
      {/* The business's own name under the product's. A shopkeeper's rail
          should say whose shop this is; the operator's says "Console". */}
      {contextName && (
        <span className="truncate text-caption leading-tight text-shell-fg">
          {contextName}
        </span>
      )}
    </Link>
  );
}
