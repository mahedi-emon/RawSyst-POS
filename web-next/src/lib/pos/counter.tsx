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
//
// # Which stock location the counter sells from
//
// A branch with one stock location needs no answer and is never asked: the
// server resolves it. A branch with two -- a shop floor and a back room, which
// is an ordinary shop -- makes every sale answer 400: "This branch has more
// than one stock location, so the sale must say which one it is selling from."
//
// The till was sending nothing at all, so in such a shop it could not ring up
// a sale, take a return or make an exchange. Found by driving a sale against a
// real server rather than by reading the code, which had no way to show it.
//
// The setting exists on the terminal (I5's default warehouse), but the route
// that exposes it is `devices.view` -- a manager's permission that no cashier
// holds. So the choice is made here, once, alongside the counter, and kept for
// the session in the same place for the same reason.

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
const WAREHOUSE_KEY = 'rawsyst.counter.warehouse';

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
  | {
      kind: 'open';
      counter: Counter;
      /**
       * Null until asked, and null forever in a branch with one location.
       *
       * Sent on every sale, return and exchange when it is set. When it is
       * not, the server resolves the branch's only location itself -- which
       * is the ordinary case and must not be turned into a question.
       */
      warehouseId: string | null;
    }
  | { kind: 'failed'; error: unknown };

interface CounterValue {
  state: State;
  open: (deviceId: string) => Promise<void>;
  /** Records which stock location this counter sells from, for the session. */
  sellFrom: (warehouseId: string) => void;
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
      let remembered: string | null = null;
      try {
        remembered = sessionStorage.getItem(WAREHOUSE_KEY);
      } catch {
        // See above.
      }
      setState({ kind: 'open', counter: res.counter, warehouseId: remembered });
    } catch (error) {
      setState({ kind: 'failed', error });
    }
  }, []);

  const sellFrom = useCallback((warehouseId: string) => {
    try {
      sessionStorage.setItem(WAREHOUSE_KEY, warehouseId);
    } catch {
      // A private window cannot store it, and the choice is then made again
      // after a reload. The sale still goes through either way.
    }
    setState((current) =>
      current.kind === 'open' ? { ...current, warehouseId } : current,
    );
  }, []);

  const leave = useCallback(() => {
    try {
      sessionStorage.removeItem(COUNTER_KEY);
      sessionStorage.removeItem(WAREHOUSE_KEY);
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
    () => ({ state, open, sellFrom, leave }),
    [state, open, sellFrom, leave],
  );

  return <CounterContext value={value}>{children}</CounterContext>;
}

export function useCounter(): CounterValue {
  const v = useContext(CounterContext);
  if (!v) throw new Error('useCounter must be used inside <CounterProvider>.');
  return v;
}
