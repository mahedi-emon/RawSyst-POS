'use client';

// The account control in the top bar: language, and signing out.
//
// Kept to what a person actually reaches for from a header. Everything else
// about an account -- sessions, second factor, password -- lives on the account
// screen, because a menu that holds fifteen things is a menu nobody reads.

import { Bell, Check, Globe, LogOut, ShieldCheck, User } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';

import { useSession } from '@/lib/auth/session';
import { LOCALES, useLocale, useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

export function UserMenu() {
  const { signOut, identity } = useSession();
  const t = useT();
  const { locale, setLocale } = useLocale();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('nx.shell.account')}
        className={cn(
          'grid size-10 place-items-center rounded-sm',
          'hover:bg-surface-hover',
          open && 'bg-surface-hover',
        )}
      >
        <User className="size-5 text-muted" aria-hidden="true" />
      </button>

      {open && (
        <div
          role="menu"
          className={cn(
            // Anchored to the inline end so it flips side in Arabic without a
            // second rule.
            'absolute end-0 top-12 z-50 w-60 rounded-md border border-line',
            'bg-surface p-1 shadow-overlay',
          )}
        >
          <div className="px-2.5 py-2">
            <p className="text-label font-medium text-fg">
              {identity?.workspace === 'platform'
                ? t('nx.shell.platformOperator')
                : t('nx.shell.signedIn')}
            </p>
            <p className="mt-0.5 text-caption text-muted">
              {identity
                ? t('nx.shell.permissions', { count: identity.grants.size })
                : t('nx.shell.resolvingAccess')}
            </p>
          </div>

          {/* Two screens about the person rather than about the business, so
              they belong beside the language and the sign-out rather than in a
              sidebar section next to Tax and Devices. They are also the only
              two screens with no permission to name — every route behind them
              resolves the caller from their own token and has no user
              parameter — and the sidebar requires one of every item it holds,
              for the good reason that an item with no permission shows to
              somebody holding nothing.

              Hidden for the platform operator, who has no tenant: the
              notification and session routes are tenant-scoped. */}
          {identity?.workspace !== 'platform' && (
            <>
              <div className="my-1 h-px bg-line" role="separator" />
              {(
                [
                  ['/settings/security', 'nx.nav.biz.settings.security', ShieldCheck],
                  ['/notifications', 'nx.nav.biz.settings.notifications', Bell],
                ] as const
              ).map(([href, labelKey, Icon]) => (
                <Link
                  key={href}
                  href={href}
                  role="menuitem"
                  onClick={() => setOpen(false)}
                  className={cn(
                    'flex w-full items-center gap-2 rounded-sm px-2.5',
                    'min-h-9 text-body hover:bg-surface-hover',
                  )}
                >
                  <Icon className="size-4" aria-hidden="true" />
                  {t(labelKey)}
                </Link>
              ))}
            </>
          )}

          <div className="my-1 h-px bg-line" role="separator" />

          <p className="flex items-center gap-2 px-2.5 py-1.5 text-caption font-semibold text-subtle">
            <Globe className="size-3.5" aria-hidden="true" />
            {t('nx.shell.language')}
          </p>
          {LOCALES.map((l) => (
            <button
              key={l.value}
              type="button"
              role="menuitemradio"
              aria-checked={l.value === locale}
              onClick={() => {
                setLocale(l.value);
                setOpen(false);
              }}
              className={cn(
                'flex w-full items-center justify-between gap-2 rounded-sm px-2.5',
                'min-h-9 text-body hover:bg-surface-hover',
              )}
            >
              {/* The native name first: somebody looking for Bangla is looking
                  for বাংলা, not for the word "Bengali" spelled in a script they
                  may not read. */}
              <span>
                {l.native}
                <span className="ms-1.5 text-caption text-subtle">
                  {l.english}
                </span>
              </span>
              {l.value === locale && (
                <Check className="size-4 text-primary" aria-hidden="true" />
              )}
            </button>
          ))}

          <div className="my-1 h-px bg-line" role="separator" />

          <button
            type="button"
            role="menuitem"
            onClick={() => void signOut()}
            className={cn(
              'flex w-full items-center gap-2 rounded-sm px-2.5',
              'min-h-9 text-body text-critical-fg hover:bg-critical-subtle',
            )}
          >
            <LogOut className="size-4" aria-hidden="true" />
            {t('nav.signOut')}
          </button>
        </div>
      )}
    </div>
  );
}
