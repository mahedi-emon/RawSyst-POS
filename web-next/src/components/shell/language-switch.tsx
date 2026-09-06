'use client';

// A language chooser small enough for a portal header.
//
// The back office puts language in the user menu, behind an avatar and a name.
// A portal has neither: the person using it is a customer standing in a shop,
// signed in with a phone number, and the one thing they may need before they
// can read anything is the language. So it is a control on the header rather
// than an item in a menu.
//
// A native `<select>` deliberately. It is the control every mobile operating
// system already presents well, it needs no portal and no focus trap, and this
// is a list of three.

import { LOCALES, useLocale, useT } from '@/lib/i18n/locale';
import type { Locale } from '@/lib/i18n/locale';

export function LanguageSwitch() {
  const t = useT();
  const { locale, setLocale } = useLocale();

  return (
    <label className="inline-flex items-center gap-1.5">
      <span className="sr-only">{t('nx.shell.language')}</span>
      <select
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        className={[
          'select-chevron h-9 appearance-none rounded-xs border border-input',
          'bg-input-bg ps-2.5 pe-7 text-caption',
        ].join(' ')}
      >
        {/* The native name, because somebody looking for Bangla is looking for
            বাংলা and not for the word "Bengali" in a script they may not read. */}
        {LOCALES.map((l) => (
          <option key={l.value} value={l.value}>
            {l.native}
          </option>
        ))}
      </select>
    </label>
  );
}
