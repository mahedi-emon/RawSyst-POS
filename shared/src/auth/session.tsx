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
 * make a cashier sign in again in front of a queue of customers.
 *
 * # The honest position on where it lives
 *
 * In the Tauri POS this is the app's own storage: its own webview, no
 * extensions, no other origin, and the machine is a till in a shop. That is a
 * reasonable place for a durable token.
 *
 * In the BROWSER back office it is ordinary localStorage, and the reasoning
 * above does not carry over. An extension or a successful XSS can read it. The
 * comment here used to claim the desktop justification for both, which was
 * true of one caller and quietly wrong about the other — the kind of note that
 * stops somebody asking the question again.
 *
 * It is an accepted risk rather than an oversight: moving to an httpOnly
 * cookie means the API issuing and rotating it, a CSRF defence for every
 * mutating route, and a different story for the desktop app that does not
 * carry cookies the same way. Worth doing, not worth doing carelessly. What is
 * NOT stored here is anything ZATCA issues: the CSID secret never reaches the
 * browser at all, because no API response carries a field for it.
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
  signInWith,
}: {
  baseUrl: string;
  children: ReactNode;
  /**
   * How to exchange a password for a session, when the ordinary way will not do.
   *
   * The POS passes one. A till has to sign in with its device secret attached
   * so the session is bound to the terminal and every sale records which one
   * rang it up — and that secret lives in the OS keystore where this layer
   * cannot reach it, so the exchange happens in the Tauri shell instead.
   *
   * Absent everywhere else, which is every browser: the back office has no
   * terminal to bind to.
   */
  /** Returning null means "not a terminal after all" — sign in the ordinary
   *  way. That is a browser during development, never a real till. */
  signInWith?: (email: string, password: string) => Promise<Session | null>;
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
        // On a terminal the exchange happens in the shell, because the device
        // secret it must carry is not reachable from here. A till belongs to
        // one business through its pairing, so the tenant-choice branch below
        // cannot arise on this path.
        const bound = signInWith ? await signInWith(email, password) : null;
        if (bound) {
          const session = bound;
          setTenantChoices([]);
          client.setSession(session);
          saveSession(session);
          const profile = await client.me();
          setMe(profile);
          setStatus('signed_in');
          return;
        }

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
  }, [client, signInWith]);

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
