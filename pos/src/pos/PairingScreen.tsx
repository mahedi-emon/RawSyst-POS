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
          setFailure(fault.message);
          break;
        case 'offline':
          setFailure(fault.message);
          setAdvice('The code is still valid — try again once you are back online.');
          break;
        default:
          // The server answers a wrong, expired and already-used code
          // identically on purpose: telling them apart would say which codes
          // exist. So the screen offers the three things worth trying, in the
          // order they are worth trying them.
          setFailure(
            'That code was not accepted. Codes work once, on one terminal, ' +
              'and expire after 15 minutes.',
          );
          setAdvice(
            'Check every character, then ask for a new code in the back office ' +
              'under Terminals.',
          );
      }
      setBusy(false);
    }
  }

  return (
    <main className="setup">
      <form className="setup__card" onSubmit={(e) => void submit(e)} noValidate>
        <h1 className="setup__title">RawSyst</h1>
        <p className="setup__lede">
          Set this machine up as a till. In the back office, open{' '}
          <strong>Terminals</strong>, add this till and it will show you a code.
        </p>

        <label className="setup__label" htmlFor="pair-code">
          Enter the code
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
          Eight characters. The dash is added for you.
        </p>

        {failure && (
          <div className="setup__failure" role="alert">
            <p>{failure}</p>
            {advice && <p className="setup__advice">{advice}</p>}
          </div>
        )}

        {canStore === false && (
          <p className="setup__failure" role="alert">
            This is the development preview, which cannot store a terminal
            credential securely. Use the installed RawSyst application on the
            till.
          </p>
        )}

        <button
          className="button button--primary button--large setup__go"
          type="submit"
          disabled={!complete || busy || canStore === false}
        >
          {busy ? 'Setting up…' : 'Set up this till'}
        </button>

        <p className="setup__foot">
          Nothing can be sold on this machine until it is set up. Ask the owner
          if you do not have a code.
        </p>
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
          {busy ? 'Checking…' : 'Check again'}
        </button>
        <p className="setup__foot">
          An owner can change this under <strong>Terminals</strong> in the back
          office.
        </p>
      </div>
    </main>
  );
}
