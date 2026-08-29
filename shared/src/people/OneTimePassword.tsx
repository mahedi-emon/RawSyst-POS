// A password that exists in exactly one place, shown once.
//
// It is stored as an argon2id hash, so this response is the only copy that will
// ever exist. Blueprint A4.2 calls that irreversibility "a security requirement,
// not just a policy choice" — which makes a toast that fades after three
// seconds the wrong shape entirely: it throws the only copy away on a timer,
// while the person it belongs to is still walking over.
//
// So this holds the screen until somebody says they have it. That is a
// deliberate interruption. The alternative is an owner who has created an
// account nobody can sign in to and has to reset it immediately.
import { useState } from 'react';

import { useT } from '../i18n/locale';

export function OneTimePassword({
  name,
  email,
  password,
  onDone,
}: {
  name: string;
  email: string;
  password: string;
  onDone: () => void;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(password);
      setCopied(true);
    } catch {
      // A browser that refuses the clipboard is not a reason to fail. The
      // password is on screen and selectable; that is the fallback and it
      // always works.
    }
  }

  return (
    <div className="ds-panel otp">
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('people.passwordFor', { name })}</h2>
      </div>

      <div className="ds-panel__body">
        <p className="ds-body-sm ds-muted">{t('people.passwordShownOnce')}</p>

        <dl className="otp__facts">
          <div>
            <dt className="ds-caption">{t('people.signsInWith')}</dt>
            {/* `.num` for the tabular figures and the bidi isolation: an email
                and a generated password are both mixed-script runs that must
                not reorder next to Arabic. */}
            <dd className="num">{email}</dd>
          </div>
        </dl>

        <p className="otp__password num" aria-label={t('people.theirPassword')}>
          {password}
        </p>

        <div className="otp__actions">
          <button className="ds-btn ds-btn--secondary" onClick={() => void copy()}>
            {copied ? t('people.copied') : t('people.copy')}
          </button>
          <button className="ds-btn ds-btn--primary" onClick={onDone}>
            {t('people.written')}
          </button>
        </div>

        <p className="ds-caption otp__note">{t('people.mustChangeOnFirstUse')}</p>
      </div>
    </div>
  );
}
