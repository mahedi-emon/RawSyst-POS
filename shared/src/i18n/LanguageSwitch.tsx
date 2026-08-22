// The language switch.
//
// A two-state toggle rather than a dropdown, because there are two locales and
// a select for two options is a click more than it needs to be. Each option is
// written in its own language — a shop assistant looking for Arabic looks for
// العربية, not for the word "Arabic" spelled in English.
//
// It lives in the top bar of both front ends, which is the one piece of chrome
// present on every screen including the sign-in page. Somebody who cannot read
// the interface has to be able to change it before signing in.

import { useLocale } from './locale';
import type { Locale } from './strings';

export function LanguageSwitch({ className }: { className?: string }) {
  const { locale, setLocale, t } = useLocale();

  const options: { value: Locale; label: string }[] = [
    { value: 'en', label: t('language.english') },
    { value: 'ar', label: t('language.arabic') },
  ];

  return (
    <div
      className={`lang${className ? ' ' + className : ''}`}
      role="group"
      aria-label={t('language.label')}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          className={`lang__opt${locale === o.value ? ' lang__opt--on' : ''}`}
          // The pressed state is what a screen reader announces; the class only
          // paints it. Colour is never the only signal (design system §3).
          aria-pressed={locale === o.value}
          // Named in its own language so the announcement matches the label.
          lang={o.value}
          onClick={() => setLocale(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
