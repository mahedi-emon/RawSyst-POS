'use client';

// Who is signed in, and which workspace that puts them in.
//
// # One login, no role picker
//
// Nobody chooses "Admin" or "Cashier" on the way in. They type an email and a
// password; `GET /auth/me` answers with `is_super_admin`, a `tenant_id` and the
// permissions the server resolved for this request, and that answer decides the
// workspace. A person who is asked to classify themselves before signing in has
// been asked to know something the system already knows.
//
// # Why the permissions are re-read, not decoded from the token
//
// They are not in the token. The API resolves them server-side per request so a
// permission revoked at 10:00 stops working at 10:00, rather than lingering
// until a 15-minute token expires. For a system handling money that window is
// not acceptable, and this provider follows the same rule: it re-reads `/me`
// when the session changes rather than caching an answer for the tab's life.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

import { api } from '../api/client';
import { ApiError } from '../api/errors';
import { Grants, NO_GRANTS } from './permissions';

/** The `/auth/me` payload. */
interface MeResponse {
  user_id: string;
  tenant_id?: string;
  is_super_admin: boolean;
  permissions: string[];
  store_scope?: string[];
  amount_limit?: string;
}

/** Which of the product's two workspaces this person belongs in. */
export type Workspace = 'platform' | 'business';

export interface Identity {
  userId: string;
  /** The business. Absent for the platform operator, who is inside none. */
  businessId: string | null;
  workspace: Workspace;
  grants: Grants;
}

type Status = 'resolving' | 'signed-in' | 'signed-out';

interface SessionValue {
  status: Status;
  identity: Identity | null;
  /** Convenience: never null, so a guard can ask without a null check first. */
  grants: Grants;
  signOut: () => Promise<void>;
  /** Re-reads `/auth/me`. Called after anything that can change permissions. */
  reload: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('resolving');
  const [identity, setIdentity] = useState<Identity | null>(null);

  const load = useCallback(async () => {
    try {
      const me = await api.get<MeResponse>('/auth/me');
      setIdentity({
        userId: me.user_id,
        businessId: me.tenant_id ?? null,
        // The platform operator is the one account that is not inside a
        // business. Everyone else, owner and cashier alike, is.
        workspace: me.is_super_admin ? 'platform' : 'business',
        grants: new Grants(
          me.permissions,
          me.is_super_admin,
          me.store_scope ?? [],
          me.amount_limit ?? null,
        ),
      });
      setStatus('signed-in');
    } catch (e) {
      // Only a refusal means signed out. A network failure leaves the person
      // where they are, because the connection coming back should not require
      // signing in again.
      if (e instanceof ApiError && e.isUnauthenticated) {
        setIdentity(null);
        setStatus('signed-out');
        return;
      }
      setIdentity(null);
      setStatus('signed-out');
    }
  }, []);

  useEffect(() => {
    let cancelled = false;

    // A page load starts with no access token -- it lives in memory and the
    // memory is gone. The durable half is in a cookie this page cannot read, so
    // the only way to find out whether there is a session is to ask.
    void (async () => {
      const recovered = await api.bootstrap();
      if (cancelled) return;
      if (!recovered) {
        setStatus('signed-out');
        return;
      }
      await load();
    })();

    return () => {
      cancelled = true;
    };
  }, [load]);

  useEffect(
    () =>
      api.subscribe((signedIn) => {
        if (!signedIn) {
          setIdentity(null);
          setStatus('signed-out');
        }
      }),
    [],
  );

  const signOut = useCallback(async () => {
    await api.logout();
    setIdentity(null);
    setStatus('signed-out');
  }, []);

  const value = useMemo<SessionValue>(
    () => ({
      status,
      identity,
      grants: identity?.grants ?? NO_GRANTS,
      signOut,
      reload: load,
    }),
    [status, identity, signOut, load],
  );

  return <SessionContext value={value}>{children}</SessionContext>;
}

export function useSession(): SessionValue {
  const v = useContext(SessionContext);
  if (!v) {
    throw new Error('useSession must be used inside <SessionProvider>.');
  }
  return v;
}

/**
 * The permission set, for a component that only needs to ask "may they?".
 *
 * Separate from `useSession` so a button does not re-render when an unrelated
 * part of the session changes.
 */
export function useGrants(): Grants {
  return useSession().grants;
}
