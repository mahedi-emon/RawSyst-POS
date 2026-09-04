'use client';

// Which language the interface is in, and which way it runs.
//
// # The catalogue is reused, not rewritten
//
// `@rawsyst/shared/i18n/strings` holds around 3,800 keys with a complete
// English and Arabic catalogue and a partial Bangla one. That is real
// translated work; throwing it away to start a new catalogue would have thrown
// away the Arabic. What is NOT reused is the old provider, which settled the
// locale in an effect because it had no server to ask -- this one reads a
// cookie the server can also read, so the very first painted frame is already
// in the right language and the right direction.
//
// # English is the default, and nothing sniffs the browser
//
// This used to fall back to `navigator.language` and start in Arabic for any
// browser reporting Arabic, which is most browsers in Saudi Arabia. Shops
// opened the product in a language nobody had chosen, and a first impression in
// the wrong language reads as a broken install. The only thing that switches
// the language is somebody pressing the switch; it is then remembered per
// device, which is what a business actually wants -- the till in the shop runs
// Arabic, the owner's laptop runs English, and neither has to be set twice.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

import { en, type Key, type Locale } from '@rawsyst/shared/i18n/strings';

export type { Key, Locale };

/** Text direction, derived rather than stored -- there is one right answer. */
export function directionOf(locale: Locale): 'ltr' | 'rtl' {
  return locale === 'ar' ? 'rtl' : 'ltr';
}

/** Each language named in its own script, plus its English name for support. */
export const LOCALES: ReadonlyArray<{
  value: Locale;
  native: string;
  english: string;
}> = [
  { value: 'en', native: 'English', english: 'English' },
  { value: 'ar', native: 'العربية', english: 'Arabic' },
  { value: 'bn', native: 'বাংলা', english: 'Bangla' },
];

/**
 * A cookie, not localStorage.
 *
 * The server render needs to know the language before any JavaScript runs, or
 * the first frame is English and then flips -- which is worse than a slow load,
 * because the flip happens after the person has started reading.
 */
const COOKIE = 'rawsyst_locale';

function readStoredLocale(): Locale | null {
  if (typeof document === 'undefined') return null;
  const match = document.cookie.match(/(?:^|; )rawsyst_locale=([^;]+)/);
  const value = match?.[1];
  return value === 'ar' || value === 'bn' || value === 'en' ? value : null;
}

interface LocaleValue {
  locale: Locale;
  direction: 'ltr' | 'rtl';
  setLocale: (l: Locale) => void;
  /**
   * Translate.
   *
   * English is the fallback for a key Bangla has not reached yet, which is the
   * honest behaviour: the alternative is showing the key, and a shopkeeper
   * seeing `stock.transfer.dispatch` has been shown a fault rather than a
   * missing translation.
   */
  t: (key: Key, params?: Record<string, string | number>) => string;
}

const LocaleContext = createContext<LocaleValue | null>(null);

/** Fills `{name}` placeholders. Values are inserted verbatim, never parsed. */
function interpolate(
  template: string,
  params?: Record<string, string | number>,
): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  );
}

export function LocaleProvider({
  initialLocale = 'en',
  children,
}: {
  initialLocale?: Locale;
  children: ReactNode;
}) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);
  const [catalogue, setCatalogue] = useState<Partial<Record<Key, string>>>(en);

  // The non-English catalogues are loaded on demand. English ships in the main
  // bundle because it is the default and the fallback; pulling all three into
  // every till's first load would be most of a megabyte of text nobody reads.
  useEffect(() => {
    let cancelled = false;
    if (locale === 'en') {
      setCatalogue(en);
      return;
    }
    void import('@rawsyst/shared/i18n/strings').then((m) => {
      if (cancelled) return;
      setCatalogue(locale === 'ar' ? m.ar : m.bn);
    });
    return () => {
      cancelled = true;
    };
  }, [locale]);

  // Picks up a preference stored on a previous visit. Runs once, and only
  // changes anything when the stored value differs from what the server
  // rendered.
  useEffect(() => {
    const stored = readStoredLocale();
    if (stored && stored !== locale) setLocaleState(stored);
    // Deliberately once: this recovers a preference, it does not track one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const direction = directionOf(locale);
    document.documentElement.lang = locale;
    document.documentElement.dir = direction;
  }, [locale]);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    try {
      // A year, and scoped to the whole site so the server sees it on every
      // navigation. Not a secret, so no need for httpOnly.
      document.cookie = `${COOKIE}=${next}; path=/; max-age=31536000; samesite=lax`;
    } catch {
      // Storage can throw in a private window. A language that does not persist
      // is a smaller problem than a product that will not switch language.
    }
  }, []);

  const value = useMemo<LocaleValue>(
    () => ({
      locale,
      direction: directionOf(locale),
      setLocale,
      t: (key, params) => interpolate(catalogue[key] ?? en[key] ?? key, params),
    }),
    [locale, catalogue, setLocale],
  );

  return <LocaleContext value={value}>{children}</LocaleContext>;
}

export function useLocale(): LocaleValue {
  const v = useContext(LocaleContext);
  if (!v) throw new Error('useLocale must be used inside <LocaleProvider>.');
  return v;
}

/** Just the translate function, for a component that needs nothing else. */
export function useT(): LocaleValue['t'] {
  return useLocale().t;
}
