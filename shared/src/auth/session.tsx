// Who is signed in, and what they may do.
//
// # Gating here is a convenience, never the security
//
// Every check in this file decides what a cashier SEES. None of it decides what
// they may do — the server refuses a restricted action whatever the interface
// showed, and QA gate M7 exists to prove it. Blueprint A6.2: "a hidden button
// in the UI is never treated as real security."
//
// So the value of this module is that a cashier is not shown a Refund button
// that would fail, not that the button being hidden protects anything.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

import { Client, type Me, type Session, type TenantChoice } from '../api/client';

interface AuthState {
  client: Client;
  me: Me | null;
  status: 'signed_out' | 'signing_in' | 'signed_in' | 'restoring';
  error: string | null;

  /** Signs in. Resolves having either opened a session or set `tenantChoices`,
   *  which the login screen turns into a question. */
  signIn(email: string, password: string, tenantId?: string): Promise<void>;
  signOut(): Promise<void>;

  /** The businesses this email and password opened, when there was more than
   *  one. Empty in the ordinary case, which is every single-tenant sign-in. */
  tenantChoices: TenantChoice[];
  /** Abandons the choice and returns to the email and password. */
  clearTenantChoices(): void;

  /** Whether the signed-in user holds a permission. */
  can(permission: string): boolean;
}

const AuthContext = createContext<AuthState | null>(null);

/** Where the session is kept between launches.
 *
 * The refresh token is durable by design — a till restarted mid-shift must not
 * make a cashier sign in again in front of a queue of customers. It lives in
 * the app's own storage rather than anywhere a browser extension or another
 * origin could reach, which is one of the reasons this is a desktop app.
 */
const SESSION_KEY = 'rawsyst.session';

function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

function saveSession(s: Session | null) {
  if (s) localStorage.setItem(SESSION_KEY, JSON.stringify(s));
  else localStorage.removeItem(SESSION_KEY);
}

export function AuthProvider({
  baseUrl,
  children,
}: {
  baseUrl: string;
  children: ReactNode;
}) {
  const client = useMemo(() => new Client(baseUrl, loadSession()), [baseUrl]);

  const [me, setMe] = useState<Me | null>(null);
  const [status, setStatus] = useState<AuthState['status']>(
    client.authenticated ? 'restoring' : 'signed_out',
  );
  const [error, setError] = useState<string | null>(null);
  const [tenantChoices, setTenantChoices] = useState<TenantChoice[]>([]);

  // A stored session is not trusted on its own. The token may have expired
  // while the till was switched off, and permissions may have changed since —
  // a cashier promoted or demoted overnight must see the new set, not the one
  // cached from yesterday.
  useEffect(() => {
    if (!client.authenticated) return;
    let cancelled = false;

    client
      .me()
      .then((profile) => {
        if (cancelled) return;
        setMe(profile);
        setStatus('signed_in');
      })
      .catch(() => {
        if (cancelled) return;
        client.setSession(null);
        saveSession(null);
        setStatus('signed_out');
      });

    return () => {
      cancelled = true;
    };
  }, [client]);

  const signIn = useCallback(
    async (email: string, password: string, tenantId?: string) => {
      setStatus('signing_in');
      setError(null);
      try {
        const outcome = await client.login(email, password, tenantId);

        // The server needs to know which business. No session exists yet, so
        // the status goes back to signed_out rather than to some intermediate
        // state — the login screen simply has one more question to ask.
        if (outcome.kind === 'choose_tenant') {
          setTenantChoices(outcome.tenants);
          setStatus('signed_out');
          return;
        }

        setTenantChoices([]);
        saveSession(outcome.session);
        const profile = await client.me();
        setMe(profile);
        setStatus('signed_in');
      } catch (e) {
        client.setSession(null);
        saveSession(null);
        setMe(null);
        setStatus('signed_out');
        setError(e instanceof Error ? e.message : 'Sign-in failed.');
        throw e;
      }
    },
    [client],
  );

  const clearTenantChoices = useCallback(() => setTenantChoices([]), []);

  const signOut = useCallback(async () => {
    await client.logout();
    saveSession(null);
    setMe(null);
    setTenantChoices([]);
    setStatus('signed_out');
  }, [client]);

  const can = useCallback(
    (permission: string) => me?.permissions.includes(permission) ?? false,
    [me],
  );

  const value = useMemo<AuthState>(
    () => ({
      client, me, status, error, signIn, signOut, can,
      tenantChoices, clearTenantChoices,
    }),
    [client, me, status, error, signIn, signOut, can,
      tenantChoices, clearTenantChoices],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside an AuthProvider');
  return ctx;
}

/** Renders its children only if the user holds the permission.
 *
 * Cosmetic, deliberately. The server is what refuses.
 */
export function Requires({
  permission,
  children,
  otherwise = null,
}: {
  permission: string;
  children: ReactNode;
  otherwise?: ReactNode;
}) {
  const { can } = useAuth();
  return <>{can(permission) ? children : otherwise}</>;
}
