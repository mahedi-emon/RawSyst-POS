'use client';

// The navigation list.
//
// # A section header, not a collapsible accordion
//
// Sections are always open. An accordion saves vertical space and costs a click
// every time somebody moves between two modules, which in an ERP is most
// minutes of the day. The list is long, and scrolling past a section you are
// not using is faster than opening one you are.
//
// # A greyed item is not a hidden item
//
// An item the person lacks permission for is not rendered at all -- it is
// filtered out before this component sees it. An item the person HAS permission
// for but whose module the plan does not include IS rendered, greyed, with the
// reason on hover. Those are different situations with different remedies: one
// is a conversation with the owner, the other with sales.

import Link from 'next/link';


import { useT } from '@/lib/i18n/locale';
import type { ResolvedSection } from '@/lib/nav/navigation';
import { cn } from '@/lib/utils';

import { iconFor, Lock } from './nav-icons';

export function NavTree({
  sections,
  activeHref,
  /** Larger targets, for the drawer. A thumb needs 44px; a cursor does not. */
  touch = false,
}: {
  sections: ResolvedSection[];
  activeHref?: string;
  touch?: boolean;
}) {
  const t = useT();
  return (
    <nav
      aria-label={t('nx.shell.mainNav')}
      className="flex-1 overflow-y-auto overscroll-contain px-2 pb-6"
    >
      {sections.map((section) => {
        const SectionIcon = iconFor(section.icon);
        return (
          <div key={section.id} className="mt-4 first:mt-1">
            <h2
              className={cn(
                'flex items-center gap-2 px-2 pb-1.5 pt-2',
                'text-caption font-semibold text-shell-fg/70',
              )}
            >
              <SectionIcon className="size-3.5" aria-hidden={true} />
              {t(section.labelKey)}
            </h2>

            <ul>
              {section.items.map((item) => {
                const current = item.href === activeHref;
                const unavailable = !item.includedInPlan;

                if (unavailable) {
                  return (
                    <li key={item.id}>
                      <span
                        // Not a link and not focusable: there is nothing at the
                        // other end for this business. The title says why, so
                        // hovering answers the question the grey raises.
                        title={t('nx.nav.notInPlan')}
                        className={cn(
                          'flex items-center justify-between gap-2 rounded-sm ps-8 pe-2',
                          touch ? 'min-h-11 py-2.5' : 'min-h-8 py-1.5',
                          'text-body text-shell-fg/40',
                        )}
                      >
                        {t(item.labelKey)}
                        <Lock className="size-3.5" aria-hidden={true} />
                      </span>
                    </li>
                  );
                }

                return (
                  <li key={item.id}>
                    <Link
                      href={item.href}
                      aria-current={current ? 'page' : undefined}
                      className={cn(
                        'flex items-center rounded-sm ps-8 pe-2 text-body',
                        touch ? 'min-h-11 py-2.5' : 'min-h-8 py-1.5',
                        'transition-colors duration-[120ms]',
                        current
                          ? // The current item carries a brass edge on the
                            // inline start. One accent, in the one place a
                            // person looks to answer "where am I".
                            'bg-shell-active font-medium text-shell-fg-strong shadow-[inset_2px_0_0_0_var(--color-brass-500)] rtl:shadow-[inset_-2px_0_0_0_var(--color-brass-500)]'
                          : 'text-shell-fg hover:bg-shell-hover hover:text-shell-fg-strong',
                      )}
                    >
                      {t(item.labelKey)}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}
    </nav>
  );
}
