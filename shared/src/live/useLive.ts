// Listening for what changed, without asking every few seconds (design 03).
//
// # Why this is not polling
//
// A back office with a notification bell that polls every ten seconds makes
// eight and a half thousand requests a day per open tab and is still ten
// seconds late. A till that polls for stock changes is worse: it is asking a
// question whose answer is almost always "nothing", on the one connection that
// matters when a customer is waiting.
//
// # Nothing here is a source of truth
//
// A message says WHAT changed, never what the new value is. The consumer
// re-reads the endpoint it already uses, where the permission check and
// row-level security still apply — so a screen that misses a message because
// its socket was reconnecting is behind rather than wrong, and its next read
// corrects it.
//
// That is deliberate and it is what makes the socket safe to drop. Nothing in
// this product may be built so that it only works when the socket is up.
//
// # Reconnecting
//
// A shop's internet goes away. A laptop lid closes. A deploy restarts the API.
// All three look the same from here, and all three are followed by everything
// coming back — so the socket reconnects with a backoff that starts at a
// second and stops at half a minute, with jitter so forty tills coming back
// after an outage do not arrive together.

import { useCallback, useEffect, useRef, useState } from 'react';

import type { Client } from '../api/client';

export interface LiveMessage {
  /** What happened: stock.moved, notification.new, shift.closed. */
  kind: string;
  payload?: Record<string, unknown>;
  /** When the server sent it, ISO 8601. */
  at: string;
}

export type LiveState = 'connecting' | 'open' | 'closed';

/** The marker the server looks for before reading the token beside it. */
const AUTH_MARKER = 'rawsyst.auth';

const FIRST_RETRY_MS = 1000;
const MAX_RETRY_MS = 30000;

export interface LiveOptions {
  /** Narrows the socket to one company. Omit to watch the whole tenant. */
  companyId?: string;
  /** Called for every message. */
  onMessage?: (m: LiveMessage) => void;
  /**
   * Off switch. A screen that does not want a socket passes false rather than
   * conditionally calling the hook, which React does not allow.
   */
  enabled?: boolean;
}

/**
 * Opens the live socket and keeps it open.
 *
 * Returns the connection state, which a screen may show — quietly. "Live" is
 * worth a small dot; "DISCONNECTED" in red is not, because the product works
 * without this and telling somebody it is broken when it is not is how a
 * feature becomes a support call.
 */
export function useLive(client: Client, options: LiveOptions = {}): LiveState {
  const { companyId, onMessage, enabled = true } = options;
  const [state, setState] = useState<LiveState>('closed');

  // Held in a ref so a changed callback does not tear down the socket. A
  // consumer that passes an inline arrow function — which is all of them —
  // would otherwise reconnect on every render.
  const handler = useRef(onMessage);
  handler.current = onMessage;

  const path = companyId
    ? `/api/v1/live?company_id=${encodeURIComponent(companyId)}`
    : '/api/v1/live';

  useEffect(() => {
    if (!enabled) {
      setState('closed');
      return;
    }
    if (typeof WebSocket === 'undefined') {
      // Server-side rendering, or a runtime without one. Not an error.
      return;
    }

    let socket: WebSocket | null = null;
    let retryMs = FIRST_RETRY_MS;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let stopped = false;

    const connect = () => {
      if (stopped) return;

      const target = client.socketTarget(path);
      if (!target) {
        // Not signed in yet. Try again rather than giving up: the sign-in and
        // the first render of a screen that wants a socket race routinely.
        timer = setTimeout(connect, FIRST_RETRY_MS);
        return;
      }

      setState('connecting');
      try {
        // Two subprotocols: the marker, then the token. See the note above on
        // why the token is not in the query string.
        socket = new WebSocket(target.url, [AUTH_MARKER, target.token]);
      } catch {
        scheduleRetry();
        return;
      }

      socket.onopen = () => {
        if (stopped) return;
        setState('open');
        // Reset only on a socket that actually opened. Resetting on the
        // attempt would turn a server that accepts and immediately closes into
        // a reconnect loop at one a second.
        retryMs = FIRST_RETRY_MS;
      };

      socket.onmessage = (event) => {
        if (typeof event.data !== 'string') return;
        let message: LiveMessage;
        try {
          message = JSON.parse(event.data) as LiveMessage;
        } catch {
          return;
        }
        // A malformed message is dropped rather than handed on. The socket is
        // best-effort by design, so there is nothing to report and nothing to
        // recover.
        if (typeof message.kind !== 'string') return;
        handler.current?.(message);
      };

      socket.onerror = () => {
        // Nothing. `onclose` always follows, and doing the work in both would
        // schedule two reconnects for one failure.
      };

      socket.onclose = () => {
        socket = null;
        if (stopped) return;
        setState('closed');
        scheduleRetry();
      };
    };

    const scheduleRetry = () => {
      if (stopped) return;
      // Jitter, so forty tills coming back after the shop's internet returns
      // do not all reconnect in the same millisecond.
      const jitter = Math.random() * retryMs * 0.3;
      timer = setTimeout(connect, retryMs + jitter);
      retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
    };

    connect();

    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      if (socket) {
        // The handlers are cleared first: a close this code asked for must not
        // schedule a reconnect for a screen that has gone.
        socket.onopen = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        socket.close();
      }
      setState('closed');
    };
  }, [client, path, enabled]);

  return state;
}

/**
 * Re-runs `reload` when one of the named kinds arrives.
 *
 * The shape almost every consumer wants: a screen already knows how to load
 * itself, and a push is a reason to do it again rather than a second way to
 * get the data. Debounced, because a goods receipt of forty lines is forty
 * messages and forty reloads would be worse than the polling this replaces.
 */
export function useLiveReload(
  client: Client,
  kinds: string[],
  reload: () => void,
  options: LiveOptions = {},
): LiveState {
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reloadRef = useRef(reload);
  reloadRef.current = reload;

  // Joined, so a caller passing a fresh array literal every render — which is
  // all of them — does not rebuild the callback and reopen the socket.
  const wanted = kinds.join(',');

  const onMessage = useCallback(
    (m: LiveMessage) => {
      if (!wanted.split(',').includes(m.kind)) return;
      if (pending.current) clearTimeout(pending.current);
      pending.current = setTimeout(() => reloadRef.current(), 400);
    },
    [wanted],
  );

  useEffect(
    () => () => {
      if (pending.current) clearTimeout(pending.current);
    },
    [],
  );

  return useLive(client, { ...options, onMessage });
}
