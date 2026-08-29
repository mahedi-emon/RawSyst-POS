// The language switch.
//
// # Why this became a dropdown
//
// It was two buttons side by side, which is right for two languages and wrong
// for three: a third pill makes the top bar a row of tabs competing with the
// navigation, and a fourth would not fit on a phone at all. A menu is one
// control whatever the list holds, which is what "make future languages easy to
// add" has to mean in the chrome as well as in the catalogue.
//
// # Each language names itself
//
// Somebody looking for Bangla is looking for বাংলা, not for the word "Bengali"
// spelled in a script they may not read. The English name and the region are
// carried underneath for the opposite case — a support engineer reading over a
// shopkeeper's shoulder needs to see which one is selected.
//
// It lives in the top bar of both front ends, which is the one piece of chrome
// present on every screen including the sign-in page: somebody who cannot read
// the interface has to be able to change it before signing in.

import { useEffect, useRef, useState } from 'react';

import { Icon } from '../ui/Icon';
import { useLocale } from './locale';
import { LOCALES, type Locale } from './strings';

export function LanguageSwitch({ className }: { className?: string }) {
  const { locale, setLocale, t } = useLocale();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement | null>(null);

  const current = LOCALES.find((l) => l.value === locale) ?? LOCALES[0]!;

  // A menu that stays open after you have chosen, or after you have plainly
  // moved on, is a menu somebody has to dismiss. Both ways out are here
  // because a keyboard user never reaches the pointer one.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  function choose(value: Locale) {
    setLocale(value);
    setOpen(false);
  }

  return (
    <div className={`lang${className ? ' ' + className : ''}`} ref={box}>
      <button
        type="button"
        className="lang__trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={t('language.choose')}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="globe" size={17} />
        {/* The current language in its own script, which is the whole point of
            the control. Hidden on a phone, where the globe is the affordance
            and the width is needed by the page title. */}
        <span className="lang__current" lang={current.value}>
          {current.native}
        </span>
      </button>

      {open && (
        <ul className="lang__menu" role="listbox" aria-label={t('language.label')}>
          {LOCALES.map((l) => (
            <li key={l.value}>
              <button
                type="button"
                role="option"
                aria-selected={l.value === locale}
                className={`lang__opt${l.value === locale ? ' lang__opt--on' : ''}`}
                // Named in its own language, so a screen reader announces it in
                // the voice the reader is looking for.
                lang={l.value}
                onClick={() => choose(l.value)}
              >
                <span className="lang__native">{l.native}</span>
                <span className="lang__region">{l.region}</span>
                {l.value === locale && (
                  <span className="lang__tick" aria-hidden="true">
                    <Icon name="check" size={15} />
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
