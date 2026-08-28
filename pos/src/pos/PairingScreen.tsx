// Setting this machine up as a till.
//
// The first thing anybody sees on a new terminal, and quite possibly the first
// thing they see of RawSyst at all. So it explains itself in one sentence,
// asks for one thing, and does not mention devices, credentials, enrolment or
// any other word a shop owner has no reason to know.
//
// # Eight characters, typed once, under time pressure
//
// The code is short-lived and single-use, so the whole screen is built around
// getting it in correctly the first time: one large box, uppercase as you type,
// the dash inserted for you, and a keyboard that opens on letters. What is
// typed is not validated locally beyond its length — the server decides, and a
// screen that guessed would refuse codes that are actually fine.
//
// # Every refusal says what to do next
//
// A wrong code, an expired one, a used one and a revoked terminal are four
// different situations with four different next actions, and the server names
// which. Collapsing them into "pairing failed" would leave somebody standing at
// a counter with no idea whether to retype, ask for a new code, or call their
// owner.

import { useEffect, useState } from 'react';
import { useT } from '@rawsyst/shared/i18n/locale';

import {
  available as keystoreAvailable,
  faultFrom,
  pair,
  type TerminalIdentity,
} from '../offline/credential';

/** How the code is displayed and typed: two groups of four. */
const GROUP = 4;
const LENGTH = 8;

/** Uppercases, strips anything that is not a code character, and re-inserts the
 *  dash — so a code read aloud, pasted, or typed in lower case all arrive the
 *  same. Nothing is mapped onto anything else: the alphabet already omits the
 *  characters people confuse. */
export function formatCode(raw: string): string {
  const flat = raw
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
    .slice(0, LENGTH);
  return flat.length > GROUP
    ? `${flat.slice(0, GROUP)}-${flat.slice(GROUP)}`
    : flat;
}

/** How many code characters have been typed, ignoring the dash. */
export function codeLength(display: string): number {
  return display.replace(/-/g, '').length;
}

export function PairingScreen({
  apiBaseUrl,
  onPaired,
}: {
  apiBaseUrl: string;
  onPaired: (identity: TerminalIdentity) => void;
}) {
  const t = useT();
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [advice, setAdvice] = useState<string | null>(null);
  const [canStore, setCanStore] = useState<boolean | null>(null);

  useEffect(() => {
    void keystoreAvailable().then(setCanStore);
  }, []);

  const complete = codeLength(code) === LENGTH;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!complete || busy) return;

    setBusy(true);
    setFailure(null);
    setAdvice(null);
    try {
      onPaired(await pair(apiBaseUrl, code));
    } catch (err) {
      const fault = faultFrom(err);
      switch (fault.kind) {
        case 'no_keystore':
          // The screen's own words rather than the module's. `credential.ts`
          // has no locale and its message is a developer's fallback; this is
          // the sentence the person standing at the till reads.
          setFailure(t('pair.noKeystore'));
          break;
        case 'offline':
          setFailure(fault.message);
          setAdvice(t('pair.stillValidOffline'));
          break;
        case 'refused':
          // What the SERVER said. It knows things this screen cannot guess —
          // that the terminal has been asking too often, that the code has
          // been used — and it writes them for the person standing at the
          // till. The advice below still applies to all of them.
          setFailure(fault.message);
          setAdvice(t('pair.checkEveryCharacter'));
          break;
        default:
          // The server answers a wrong, expired and already-used code
          // identically on purpose: telling them apart would say which codes
          // exist. So the screen offers the three things worth trying, in the
          // order they are worth trying them.
          setFailure(t('pair.codeNotAccepted'));
          setAdvice(t('pair.checkEveryCharacter'));
      }
      setBusy(false);
    }
  }

  return (
    <main className="setup">
      <form className="setup__card" onSubmit={(e) => void submit(e)} noValidate>
        <h1 className="setup__title">RawSyst</h1>
        <p className="setup__lede">
          {t('pair.setUpLede', { section: t('dev.terminals') })}
        </p>

        <label className="setup__label" htmlFor="pair-code">
          {t('pair.enterCode')}
        </label>
        <input
          id="pair-code"
          className="setup__code num"
          value={code}
          onChange={(e) => setCode(formatCode(e.target.value))}
          placeholder="ABCD-1234"
          // A code is Latin characters in both directions of script.
          dir="ltr"
          autoFocus
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          inputMode="text"
          aria-describedby="pair-help"
          aria-invalid={failure ? true : undefined}
          disabled={busy || canStore === false}
        />
        <p className="setup__help" id="pair-help">
          {t('pair.eightCharacters')}
        </p>

        {failure && (
          <div className="setup__failure" role="alert">
            <p>{failure}</p>
            {advice && <p className="setup__advice">{advice}</p>}
          </div>
        )}

        {canStore === false && (
          <p className="setup__failure" role="alert">
            {t('pair.noKeystore')}
          </p>
        )}

        <button
          className="button button--primary button--large setup__go"
          type="submit"
          disabled={!complete || busy || canStore === false}
        >
          {busy ? t('pair.settingUp') : t('pair.setUpThisTill')}
        </button>

        <p className="setup__foot">{t('pair.nothingUntilSetUp')}</p>
      </form>
    </main>
  );
}

/** What a paired terminal shows when the server will not let it work.
 *
 *  Its own screen rather than a banner: a revoked or switched-off till must not
 *  look like a working one with a message on it, because the next thing
 *  somebody does with a working-looking till is try to sell on it. */
export function TerminalBlocked({
  title,
  message,
  onRetry,
  busy,
}: {
  title: string;
  message: string;
  onRetry: () => void;
  busy: boolean;
}) {
  const t = useT();
  return (
    <main className="setup">
      <div className="setup__card">
        <h1 className="setup__title">{title}</h1>
        <p className="setup__lede">{message}</p>
        <button
          className="button button--large setup__go"
          onClick={onRetry}
          disabled={busy}
        >
          {busy ? t('pair.checking') : t('pair.checkAgain')}
        </button>
        <p className="setup__foot">
          {t('pair.ownerCanChange')} <strong>{t('dev.terminals')}</strong> in the back
          office.
        </p>
      </div>
    </main>
  );
}
