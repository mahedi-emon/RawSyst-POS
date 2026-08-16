// Loading something from the server, with the states that actually happen.
//
// Every detail screen needs the same five: still loading, loaded, refused for
// lack of permission, unreachable, and broken. Written once because five
// hand-rolled copies drift — and the ones that drift first are the states
// nobody demos, which are exactly the ones a real deployment spends its first
// week in.
//
// # Offline is not an error
//
// It is kept as its own state rather than folded into `error` because the two
// mean different things to the reader and deserve different words. A dashboard
// that cannot reach the server is a dashboard waiting; colouring that red
// teaches owners to ignore red.
//
// # Permission-denied is not an error either
//
// A 403 is the system working correctly. Showing it as "something went wrong"
// invites the user to retry, and to report a bug that is not one.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';

export type Remote<T> =
  | { state: 'loading' }
  | { state: 'ready'; data: T }
  | { state: 'denied' }
  | { state: 'offline' }
  | { state: 'error'; message: string };

export interface RemoteHandle<T> {
  remote: Remote<T>;
  reload: () => void;
  /** True while a reload runs over data already on screen. Lets a screen keep
   *  showing what it has instead of collapsing back to a skeleton — a table
   *  that blanks on every refresh loses the reader's place. */
  refreshing: boolean;
}

export function useRemote<T>(load: () => Promise<T>): RemoteHandle<T> {
  const [remote, setRemote] = useState<Remote<T>>({ state: 'loading' });
  const [refreshing, setRefreshing] = useState(false);

  const run = useCallback(
    async (isRefresh: boolean) => {
      if (isRefresh) setRefreshing(true);
      else setRemote({ state: 'loading' });

      try {
        setRemote({ state: 'ready', data: await load() });
      } catch (err) {
        if (err instanceof Offline) {
          setRemote({ state: 'offline' });
        } else if (err instanceof RequestFailed && (err.status === 403 || err.status === 401)) {
          setRemote({ state: 'denied' });
        } else if (err instanceof RequestFailed && err.status === 404) {
          // Not found is a legitimate answer here, not a fault: row-level
          // security reports another tenant's company this way, and so does a
          // company that has been removed.
          setRemote({ state: 'denied' });
        } else {
          setRemote({
            state: 'error',
            message: err instanceof Error ? err.message : 'That did not load.',
          });
        }
      } finally {
        setRefreshing(false);
      }
    },
    [load],
  );

  useEffect(() => {
    void run(false);
  }, [run]);

  const reload = useCallback(() => {
    void run(true);
  }, [run]);

  return { remote, reload, refreshing };
}
