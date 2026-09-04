'use client';

// Standing at a counter.
//
// # The counter is in the token, never in a request body
//
// Every POS route reads the till from the `did` claim the server signed, and
// never from what the request says. A cashier who could name a counter could
// ring a sale onto another counter's shift, another shop's stock and -- where
// the market has one -- another terminal's invoice chain, all inside their own
// business where row-level security has no reason to object.
//
// So the counter is chosen ONCE, by `POST /pos/counter-sessions`, and from then
// on it is in the token. This module holds that exchange and the access token
// it returns.
//
// # Why the counter is remembered but the token is not
//
// The counter-bound token lives in memory like every other access token. When
// the tab reloads, the ordinary refresh gives back a token WITHOUT the device
// claim -- the refresh cookie knows the session, not the till. So the counter
// id is kept in session storage and the exchange is repeated on load. The
// cashier does not re-choose; the till re-binds itself.
//
// Session storage, not local: closing the tab ends the shift's binding, which
// is the correct default for a counter somebody walked away from.

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

const COUNTER_KEY = 'rawsyst.counter';

export interface Counter {
  id: string;
  store_id: string;
  store: string;
  terminal_label: string;
  status: string;
  binding: string;
  egs_unit_id?: string;
  egs_unit?: string;
}

interface CounterSessionResponse {
  access_token: string;
  expires_at: string;
  counter: Counter;
}

type State =
  | { kind: 'choosing' }
  | { kind: 'opening' }
  | { kind: 'open'; counter: Counter }
  | { kind: 'failed'; error: unknown };

interface CounterValue {
  state: State;
  open: (deviceId: string) => Promise<void>;
  leave: () => void;
}

const CounterContext = createContext<CounterValue | null>(null);

export function CounterProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ kind: 'choosing' });

  const open = useCallback(async (deviceId: string) => {
    setState({ kind: 'opening' });
    try {
      const res = await api.post<CounterSessionResponse>('/pos/counter-sessions', {
        device_id: deviceId,
      });
      // From here every request carries the counter. The token replaces the
      // ordinary one rather than sitting beside it, because a till that could
      // choose which token to send could ring a sale onto no counter at all.
      api.setAccessToken(res.access_token);
      try {
        sessionStorage.setItem(COUNTER_KEY, deviceId);
      } catch {
        // A private window cannot store it. The till still works; it will ask
        // which counter again after a reload, which is a small cost.
      }
      setState({ kind: 'open', counter: res.counter });
    } catch (error) {
      setState({ kind: 'failed', error });
    }
  }, []);

  const leave = useCallback(() => {
    try {
      sessionStorage.removeItem(COUNTER_KEY);
    } catch {
      // See above.
    }
    // The token keeps its device claim until the next refresh, which is
    // harmless: leaving the till is a navigation, not a security boundary, and
    // the boundary that matters is the server checking the claim on each call.
    setState({ kind: 'choosing' });
  }, []);

  // Re-binds after a reload, so a cashier who refreshed mid-shift is put back
  // at their own counter rather than at a chooser.
  useEffect(() => {
    let remembered: string | null = null;
    try {
      remembered = sessionStorage.getItem(COUNTER_KEY);
    } catch {
      // See above.
    }
    if (remembered) void open(remembered);
  }, [open]);

  const value = useMemo<CounterValue>(
    () => ({ state, open, leave }),
    [state, open, leave],
  );

  return <CounterContext value={value}>{children}</CounterContext>;
}

export function useCounter(): CounterValue {
  const v = useContext(CounterContext);
  if (!v) throw new Error('useCounter must be used inside <CounterProvider>.');
  return v;
}
