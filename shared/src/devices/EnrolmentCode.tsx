// The one moment the enrolment code exists in readable form.
//
// Shown once and never again — only its hash is kept, exactly as a password is.
// So this screen is built around a single job: get eight characters from here
// onto a keyboard across the shop, correctly, before they expire.
//
// # Why it looks like this
//
// The code is set in large monospaced blocks with the groups kept apart,
// because somebody is reading it aloud or carrying it in their head to another
// machine, and "K7QP4M2X" is where a reader loses their place. The countdown is
// live and stated in words, because a code that quietly died while somebody
// walked to the till is the single most likely way this flow fails.

import { useEffect, useState } from 'react';

import type { IssuedCode } from '../api/devices';
import { codeGroups, countdown, secondsLeft } from './devices';
import { useT } from '../i18n/locale';

export function EnrolmentCode({
  issued,
  onReissue,
  onDone,
  busy,
}: {
  issued: IssuedCode;
  onReissue: () => void;
  onDone: () => void;
  busy: boolean;
}) {
  const t = useT();
  const [left, setLeft] = useState(() => secondsLeft(issued.expires_at));

  // Ticks once a second while this is on screen. Cheap, and the alternative is
  // a number that was true when the panel opened and is a lie a minute later.
  useEffect(() => {
    setLeft(secondsLeft(issued.expires_at));
    const timer = setInterval(() => setLeft(secondsLeft(issued.expires_at)), 1000);
    return () => clearInterval(timer);
  }, [issued.expires_at]);

  const expired = left <= 0;
  const nearlyGone = !expired && left <= 60;

  return (
    <div className="ds-panel">
      <div className="ds-panel__head">
        <h2 className="ds-h3">Set up {issued.terminal_label}</h2>
      </div>

      <div className="ds-panel__body pairing">
        <ol className="pairing__steps">
          <li>{t('dev.openOnNewTerminal')}</li>
          <li>
            Type this code into it{' '}
            <strong>exactly as shown</strong>.
          </li>
          <li>{t('dev.readyOnAccept')}</li>
        </ol>

        {/* The code itself. aria-label spells it out for a screen reader,
            because the visual grouping that helps a sighted reader would
            otherwise be read as two unrelated words. */}
        <p
          className={`pairing__code${expired ? ' pairing__code--dead' : ''}`}
          aria-label={`Enrolment code, ${issued.code.split('').join(' ')}`}
        >
          {codeGroups(issued.code).map((group, i) => (
            <span key={i} className="pairing__group">
              {group}
            </span>
          ))}
        </p>

        {expired ? (
          <p className="pairing__expiry pairing__expiry--dead" role="status">
            This code has expired. Get a new one — it takes a moment and the old
            one stops working.
          </p>
        ) : (
          <p
            className={`pairing__expiry${nearlyGone ? ' pairing__expiry--soon' : ''}`}
            role="status"
            aria-live="off"
          >
            {t('dev.expiresIn')} <span className="num">{countdown(left)}</span>. It works
            once, on one terminal.
          </p>
        )}

        <p className="pairing__note">
          Anybody who has this code can set up a terminal on your business until
          it expires. Do not send it where it will be kept.
        </p>

        <div className="form__actions">
          <button className="ds-btn ds-btn--primary" onClick={onDone}>
            {t('common.done')}
          </button>
          <button
            className="ds-btn ds-btn--quiet"
            onClick={onReissue}
            disabled={busy}
          >
            {busy ? 'Getting a new code…' : 'Get a new code'}
          </button>
        </div>
      </div>
    </div>
  );
}
