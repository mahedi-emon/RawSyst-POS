// Which language the interface is in, and which way it runs.
//
// One provider for both front ends. The Tauri till and the Next.js back office
// are two front ends of one product (blueprint J2), and a cashier who switches
// the till to Arabic and an owner who switches the browser expect the same
// words — so the catalogue, the switch and the persistence live here rather
// than twice.
//
// # It sets `dir` on the document, not on a wrapper
//
// Design system §6 rule 1 is that layout mirrors from `dir` alone, because the
// stylesheets use logical properties. That only works if `dir` is on an element
// containing everything, including anything portalled to `document.body`. So
// this writes `documentElement.dir` and `documentElement.lang` rather than
// wrapping the tree in a `<div dir>`, which would leave a dialog rendered
// outside it running the wrong way.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

import { catalogues, directionOf, type Key, type Locale } from './strings';

const STORAGE_KEY = 'rawsyst.locale';

interface LocaleValue {
  locale: Locale;
  direction: 'ltr' | 'rtl';
  setLocale: (l: Locale) => void;
  /** Translate. Missing keys cannot happen — the catalogue is exhaustive by
   *  type — so there is no fallback branch to get wrong. */
  t: (key: Key, params?: Record<string, string | number>) => string;
}

const LocaleContext = createContext<LocaleValue | null>(null);

/**
 * Reads the stored preference, then the browser's.
 *
 * A Saudi shop's staff should not have to set this on every terminal, and a
 * browser already reporting Arabic is a better first guess than English.
 * Wrapped because storage throws in a private window rather than returning
 * null, and a till that will not start because it could not read a preference
 * is worse than one that starts in English.
 */
function initialLocale(): Locale {
  try {
    const stored = globalThis.localStorage?.getItem(STORAGE_KEY);
    if (stored === 'ar' || stored === 'en') return stored;
  } catch {
    // Ignored on purpose: see above.
  }
  try {
    const preferred = globalThis.navigator?.language ?? '';
    if (preferred.toLowerCase().startsWith('ar')) return 'ar';
  } catch {
    // Ignored on purpose.
  }
  return 'en';
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  // Starts at the default and settles in an effect rather than reading storage
  // during render, so the server-rendered markup and the first client render
  // agree. Next.js hydration fails loudly otherwise.
  const [locale, setLocaleState] = useState<Locale>('en');

  useEffect(() => {
    setLocaleState(initialLocale());
  }, []);

  useEffect(() => {
    const root = globalThis.document?.documentElement;
    if (!root) return;
    root.lang = locale;
    root.dir = directionOf(locale);
  }, [locale]);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    try {
      globalThis.localStorage?.setItem(STORAGE_KEY, next);
    } catch {
      // A preference that cannot be remembered is still worth honouring for
      // this session.
    }
  }, []);

  const value = useMemo<LocaleValue>(() => {
    const table = catalogues[locale];
    return {
      locale,
      direction: directionOf(locale),
      setLocale,
      t: (key, params) => interpolate(table[key], params),
    };
  }, [locale, setLocale]);

  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

/**
 * The locale, for a component that needs to translate.
 *
 * Falls back to English rather than throwing when there is no provider above
 * it. A missing provider is a wiring mistake that should be visible in review,
 * not a blank screen in a shop — and every string still renders.
 */
export function useLocale(): LocaleValue {
  const held = useContext(LocaleContext);
  if (held) return held;
  return {
    locale: 'en',
    direction: 'ltr',
    setLocale: () => {},
    t: (key, params) => interpolate(catalogues.en[key], params),
  };
}

/** Just the translate function, which is what most components want. */
export function useT() {
  return useLocale().t;
}

/** `{name}` substitution. Deliberately the whole of it: anything needing more
 *  than a named placeholder is a sentence that should be its own key. */
function interpolate(
  text: string,
  params?: Record<string, string | number>,
): string {
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  );
}
