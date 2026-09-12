'use client';

// The portal's own session, which is not the staff session.
//
// # Why none of the staff client is reused
//
// A staff session is an in-memory access token refreshed from an httpOnly
// cookie, scoped to a tenant the server resolves from the token itself. A
// portal session is none of those things:
//
//   - the token is a bearer string the portal routes read from the header,
//     and there is no refresh cookie behind it
//   - the shop is named in the QUERY STRING (`tenant_id`, `company_id`),
//     because a portal belongs to one shop and the customer of one branch of a
//     group is not the customer of another
//   - a portal caller is not staff at all: there is no permission catalogue,
//     no company picker, and nothing it holds can become a staff grant
//
// Sharing the staff client would have meant teaching it a second identity
// model, and the first thing a second identity model does in a shared client
// is leak one into the other.
//
// # Where the token is kept, and what that costs
//
// `sessionStorage`, per tab. There is no httpOnly cookie to refresh from —
// `POST /portal/session` answers a bearer token and nothing else — so keeping
// it in memory alone would sign the customer out on every page navigation,
// which on a phone is every time they tap anything.
//
// `sessionStorage` rather than `localStorage` deliberately: it dies with the
// tab, so a shared phone in a shop does not carry the last customer's session
// into the next person's. And it is scoped to the SHOP as well as the tab, so
// two shops open in two tabs cannot read each other's token.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

/** Which shop this portal page belongs to. Both come from the link. */
export interface Shop {
  tenantId: string;
  companyId: string;
}

interface PortalSession {
  shop: Shop | null;
  token: string | null;
  name: string;
  /** Null until the first render has read sessionStorage. */
  ready: boolean;
  signIn: (token: string, name: string) => void;
  signOut: () => void;
}

const Ctx = createContext<PortalSession | null>(null);

/** The storage key, scoped to the shop so two shops cannot read each other. */
function keyFor(shop: Shop, kind: 'token' | 'name'): string {
  return `biz1core.portal.${shop.tenantId}.${shop.companyId}.${kind}`;
}

function read(key: string): string | null {
  try {
    return sessionStorage.getItem(key);
  } catch {
    // A browser refusing storage is a browser this portal still has to work
    // in for the length of one page.
    return null;
  }
}

export function PortalSessionProvider({
  shop,
  children,
}: {
  shop: Shop | null;
  children: ReactNode;
}) {
  const [token, setToken] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!shop) {
      setReady(true);
      return;
    }
    setToken(read(keyFor(shop, 'token')));
    setName(read(keyFor(shop, 'name')) ?? '');
    setReady(true);
  }, [shop]);

  const signIn = useCallback(
    (next: string, who: string) => {
      setToken(next);
      setName(who);
      if (!shop) return;
      try {
        sessionStorage.setItem(keyFor(shop, 'token'), next);
        sessionStorage.setItem(keyFor(shop, 'name'), who);
      } catch {
        // Signed in for this page either way; the state above is the source.
      }
    },
    [shop],
  );

  const signOut = useCallback(() => {
    setToken(null);
    setName('');
    if (!shop) return;
    try {
      sessionStorage.removeItem(keyFor(shop, 'token'));
      sessionStorage.removeItem(keyFor(shop, 'name'));
    } catch {
      // Nothing to clear that we can reach.
    }
  }, [shop]);

  const value = useMemo<PortalSession>(
    () => ({ shop, token, name, ready, signIn, signOut }),
    [shop, token, name, ready, signIn, signOut],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function usePortalSession(): PortalSession {
  const value = useContext(Ctx);
  if (!value) {
    throw new Error('usePortalSession outside a PortalSessionProvider');
  }
  return value;
}
