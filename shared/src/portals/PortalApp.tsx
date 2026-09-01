// The portal a customer or a supplier actually opens (blueprint F2, F3).
//
// # One page, two audiences, one sign-in box
//
// A shop publishes one link. Whoever follows it is asked for a phone number or
// an email address, and which they give decides which portal they get. Two
// separate links would be two things a shop has to explain, and the person who
// follows the wrong one gets a dead end.
//
// # The token lives in this component, not in a cookie
//
// A portal session is a bearer token, held in memory and in one localStorage
// key per shop. Deliberately not a cookie: the staff session IS a cookie, and
// two different credentials on the same origin sharing that mechanism is how a
// confusion bug gets written. They cannot be confused here because they are not
// the same kind of thing.
//
// A dropped token means signing in again, which for a portal costs one text
// message. That is the right trade against the alternative.

import { useCallback, useEffect, useState } from 'react';

import {
  exchangeCode,
  requestCode,
  signOut,
  supplierSignIn,
  type PortalShop,
} from '../api/portal';
import { LabelledText } from '../governance/fields';
import { useT } from '../i18n/locale';
import { FormError } from '../ui/Form';
import { CustomerPortal } from './CustomerPortal';
import { SupplierPortal } from './SupplierPortal';

/** Which portal a token belongs to. Decided at sign-in, not guessed. */
type Side = 'customer' | 'supplier';

interface Held {
  token: string;
  name: string;
  side: Side;
}

const keyFor = (shop: PortalShop) => `rawsyst.portal.${shop.companyId}`;

function remember(shop: PortalShop, held: Held | null) {
  try {
    if (held) {
      localStorage.setItem(keyFor(shop), JSON.stringify(held));
    } else {
      localStorage.removeItem(keyFor(shop));
    }
  } catch {
    // A browser that refuses storage still works; the session simply lasts
    // until the tab is closed.
  }
}

function recall(shop: PortalShop): Held | null {
  try {
    const raw = localStorage.getItem(keyFor(shop));
    return raw ? (JSON.parse(raw) as Held) : null;
  } catch {
    return null;
  }
}

export function PortalApp({ shop }: { shop: PortalShop }) {
  const t = useT();
  const [held, setHeld] = useState<Held | null>(null);

  useEffect(() => {
    setHeld(recall(shop));
  }, [shop]);

  const leave = useCallback(() => {
    if (held) void signOut(shop, held.token).catch(() => undefined);
    remember(shop, null);
    setHeld(null);
  }, [held, shop]);

  if (!held) {
    return (
      <SignIn
        shop={shop}
        onIn={(next) => {
          remember(shop, next);
          setHeld(next);
        }}
      />
    );
  }

  return (
    <div className="ptl">
      <header className="ptl__bar">
        <span className="ptl__who">{held.name}</span>
        <button className="ds-btn ds-btn--quiet ds-btn--sm" onClick={leave}>
          {t('ptl.signOut')}
        </button>
      </header>

      {held.side === 'customer' ? (
        <CustomerPortal shop={shop} token={held.token} onExpired={leave} />
      ) : (
        <SupplierPortal shop={shop} token={held.token} onExpired={leave} />
      )}
    </div>
  );
}

function SignIn({
  shop,
  onIn,
}: {
  shop: PortalShop;
  onIn: (held: Held) => void;
}) {
  const t = useT();
  const [side, setSide] = useState<Side>('customer');
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [sent, setSent] = useState(false);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
    } catch (err) {
      setFailure(
        (err instanceof Error && err.message) || t('common.didNotWork'),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="ptl__signin">
      <div className="ptl__card">
        <h1 className="ds-h1">{t('ptl.welcome')}</h1>
        <p className="ds-caption">{t('ptl.welcomeHint')}</p>

        <div className="segmented" role="group" aria-label={t('ptl.whoAreYou')}>
          <button
            className={`segmented__btn${side === 'customer' ? ' segmented__btn--on' : ''}`}
            aria-pressed={side === 'customer'}
            onClick={() => setSide('customer')}
          >
            {t('ptl.iAmACustomer')}
          </button>
          <button
            className={`segmented__btn${side === 'supplier' ? ' segmented__btn--on' : ''}`}
            aria-pressed={side === 'supplier'}
            onClick={() => setSide('supplier')}
          >
            {t('ptl.iAmASupplier')}
          </button>
        </div>

        <FormError message={failure} />

        {side === 'customer' ? (
          <>
            <LabelledText
              id="ptl-phone"
              label={t('ptl.yourPhone')}
              hint={t('ptl.yourPhoneHint')}
              value={phone}
              onChange={setPhone}
              inputMode="tel"
            />
            {sent && (
              <LabelledText
                id="ptl-code"
                label={t('ptl.theCode')}
                hint={t('ptl.theCodeHint')}
                value={code}
                onChange={setCode}
                inputMode="numeric"
              />
            )}
            <div className="form__actions">
              {!sent ? (
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || phone.trim() === ''}
                  onClick={() =>
                    void run(async () => {
                      await requestCode(shop, phone);
                      setSent(true);
                    })
                  }
                >
                  {t('ptl.sendCode')}
                </button>
              ) : (
                <>
                  <button
                    className="ds-btn ds-btn--primary"
                    disabled={busy || code.trim() === ''}
                    onClick={() =>
                      void run(async () => {
                        const out = await exchangeCode(shop, phone, code);
                        onIn({ ...out, side: 'customer' });
                      })
                    }
                  >
                    {t('ptl.signIn')}
                  </button>
                  <button
                    className="ds-btn ds-btn--quiet"
                    disabled={busy}
                    onClick={() => {
                      setSent(false);
                      setCode('');
                    }}
                  >
                    {t('ptl.differentNumber')}
                  </button>
                </>
              )}
            </div>
          </>
        ) : (
          <>
            <LabelledText
              id="ptl-email"
              label={t('ptl.yourEmail')}
              value={email}
              onChange={setEmail}
              inputMode="email"
            />
            <LabelledText
              id="ptl-password"
              label={t('ptl.yourPassword')}
              value={password}
              onChange={setPassword}
              type="password"
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || email.trim() === '' || password === ''}
                onClick={() =>
                  void run(async () => {
                    const out = await supplierSignIn(shop, email, password);
                    onIn({ ...out, side: 'supplier' });
                  })
                }
              >
                {t('ptl.signIn')}
              </button>
            </div>
          </>
        )}
      </div>
    </main>
  );
}
