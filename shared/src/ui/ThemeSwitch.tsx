// Light or dark, as a decision somebody makes rather than one inherited.
//
// # Why this exists at all
//
// The stylesheet used to follow `prefers-color-scheme`, so the whole product —
// the back office AND the till — rendered dark on any machine whose operating
// system was set that way. That is wrong here for reasons that are not about
// taste:
//
//   - A till is a shared machine in a shop. How it looks should be the shop's
//     decision, taken once, not the decision of whoever last touched the
//     Windows display settings.
//   - The design brief targets AAA contrast on POS screens because showroom
//     lighting is unpredictable, and a dark interface is the harder half of
//     that bargain.
//
// So the product is light, and dark is a switch. The choice is stored per
// device: the shop's counter and the owner's laptop are different machines with
// different lighting, and neither should be set twice.
//
// # It writes the attribute the stylesheet reads
//
// `data-theme` on the root element, which is what design-system.css branches
// on. Nothing else in the product decides a colour.

import { useCallback, useEffect, useState } from 'react';

import { useT } from '../i18n/locale';
import { Icon } from './Icon';

type Theme = 'light' | 'dark';

const STORAGE_KEY = 'rawsyst.theme';

/** Reads the stored preference. Light unless somebody has said otherwise. */
function stored(): Theme {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'dark' ? 'dark' : 'light';
  } catch {
    // A private window, or a browser refusing storage. Light is the default and
    // failing to read it is not a reason to fail to render.
    return 'light';
  }
}

export function ThemeSwitch({ className }: { className?: string }) {
  const t = useT();
  const [theme, setTheme] = useState<Theme>('light');

  // Read after mount rather than during render. The server has no
  // localStorage, and a value that differs between the server's HTML and the
  // browser's first paint is a hydration mismatch.
  useEffect(() => {
    setTheme(stored());
  }, []);

  // The DOM follows the state; storage does not.
  //
  // Writing storage from an effect keyed on `theme` looks equivalent and is
  // not. Effects run in declaration order, so on mount this one ran with
  // `theme` still at its initial 'light' and wrote 'light' over a stored
  // 'dark' before the read above had been applied — and under React's
  // development double-mount the second mount then read back the value the
  // first had just clobbered. The same shape cost the navigation rail's pin
  // its memory; see BackOffice.tsx.
  //
  // A press is the only thing that changes this preference, so a press is
  // where it is saved. Nothing is left to run in an order.
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const next: Theme = theme === 'dark' ? 'light' : 'dark';

  const choose = useCallback((chosen: Theme) => {
    setTheme(chosen);
    try {
      localStorage.setItem(STORAGE_KEY, chosen);
    } catch {
      /* A browser refusing storage still gets the theme it asked for; it
         just does not get it again tomorrow. */
    }
  }, []);

  return (
    <button
      type="button"
      className={`bo__iconbtn${className ? ' ' + className : ''}`}
      // The label says what pressing it DOES, which is what a screen reader
      // user needs; the icon shows the same thing.
      aria-label={next === 'dark' ? t('theme.toDark') : t('theme.toLight')}
      title={next === 'dark' ? t('theme.toDark') : t('theme.toLight')}
      onClick={() => choose(next)}
    >
      <Icon name={theme === 'dark' ? 'sun' : 'moon'} size={18} />
    </button>
  );
}
