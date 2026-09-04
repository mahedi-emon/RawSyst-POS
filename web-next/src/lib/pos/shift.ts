'use client';

// The till session.
//
// # A sale needs an open drawer
//
// Live validation found this: `POST /pos/sales` answers 409 with "This till has
// no open session. Count the drawer and open a session before ringing up
// sales." until a shift exists. The till had no such step, so every first sale
// of the day would have failed with a message a cashier could do nothing about.
//
// # The opening float is declared, not assumed
//
// A till that starts from an assumed float has no baseline to reconcile
// against, so the count is asked for rather than defaulted. `blind_close`
// withholds the expected figure at close, and is per session rather than per
// till because a trainee and a supervisor may run the same counter on the same
// day.

import { useCallback, useEffect, useState } from 'react';

import { api } from '../api/client';
import { ApiError } from '../api/errors';

export interface Shift {
  id: string;
  session_no: number;
  device_id: string;
  store_id: string;
  state: string;
  opened_at: string;
  opening_float: string;
  blind_close: boolean;
}

type ShiftState =
  | { kind: 'checking' }
  | { kind: 'closed' }
  | { kind: 'open'; shift: Shift }
  | { kind: 'failed'; error: unknown };

export function useShift() {
  const [state, setState] = useState<ShiftState>({ kind: 'checking' });
  const [opening, setOpening] = useState(false);

  /**
   * Finds the session this counter is already in.
   *
   * A 404 here is the ordinary state of a till before the day starts, not a
   * fault -- so it resolves to `closed` rather than to an error. Anything else
   * is a real failure and says so.
   */
  const refresh = useCallback(async () => {
    try {
      const shift = await api.get<Shift>('/shifts/current');
      setState({ kind: 'open', shift });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        setState({ kind: 'closed' });
        return;
      }
      setState({ kind: 'failed', error: e });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const open = useCallback(
    async (openingFloat: string, blindClose: boolean) => {
      setOpening(true);
      try {
        const shift = await api.post<Shift>('/shifts', {
          opening_float: openingFloat,
          blind_close: blindClose,
        });
        setState({ kind: 'open', shift });
      } catch (e) {
        setState({ kind: 'failed', error: e });
      } finally {
        setOpening(false);
      }
    },
    [],
  );

  /**
   * The cashier's own view of the session, for the close screen.
   *
   * `GET /shifts/{id}` and not `/x-report`. The X report is gated on
   * `report.view`, which a Cashier deliberately does not hold: somebody who can
   * read the expected drawer before counting it can make the drawer agree, and
   * the variance then reads zero on every shift. Peek withholds the expected
   * figure on a blind-close till and is reachable with `sales.receive_payment`,
   * which is exactly the right pairing for this screen.
   */
  const peek = useCallback(async (sessionID: string) => {
    return api.get<ShiftReport>(`/shifts/${sessionID}`);
  }, []);

  /** Moves cash out to the safe, or a float back in. Signed; zero is refused. */
  const drop = useCallback(
    async (sessionID: string, amount: string, reason: string, note: string) => {
      await api.post(`/shifts/${sessionID}/cash-drop`, {
        amount,
        reason,
        note,
      });
    },
    [],
  );

  return { state, open, opening, refresh, peek, drop, setState };
}

/** The X or Z reckoning of a session. The same shape serves both. */
export interface ShiftReport {
  session_no: number;
  state: string;
  opened_at: string;
  closed_at?: string;
  opening_float: string;
  invoice_count: number;
  gross_sales: string;
  net_sales: string;
  tax_total: string;
  refund_total: string;
  /** Withheld on a blind close, along with the figures it derives from. */
  cash_takings?: string;
  non_cash_takings?: string;
  cash_movements?: string;
  expected_cash?: string;
  counted_cash?: string;
  variance?: string;
}
