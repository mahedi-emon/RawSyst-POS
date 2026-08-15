// What the till still owes the server, and whether it can reach it.
//
// Two facts, shown as two lines, because they are genuinely different
// questions and a cashier closing up needs both answered. "Nothing waiting"
// with no connection is fine. "23 waiting" with a good connection means the
// drain is in progress. "23 waiting" with no connection is the one worth
// noticing. Folding them into a single traffic light would make the first and
// the last look the same.
//
// A green tick that lies is worse than no indicator at all, so nothing here
// claims to have reached the server on the strength of anything but a push
// that came back.

import type { TerminalState } from '../offline/useTerminal';

export function QueueStatus({ terminal }: { terminal: TerminalState }) {
  if (!terminal.ready) {
    // Running outside the Tauri shell, in a browser during development.
    // Said plainly rather than shown as healthy: nothing is durable here.
    return (
      <p className="queue queue--warn" role="status">
        No local storage on this terminal — sales are not being saved.
      </p>
    );
  }

  return (
    <div className="status">
      <NetworkLine terminal={terminal} />
      <QueueLine terminal={terminal} />
    </div>
  );
}

/** Reachability. Says nothing about the queue. */
function NetworkLine({ terminal }: { terminal: TerminalState }) {
  const { network, cached } = terminal;

  if (!network.checked) {
    return (
      <p className="queue" role="status">
        Checking the connection…
      </p>
    );
  }

  if (network.unauthenticated) {
    // The server is there and will not have us. A different problem from a
    // dead network, with a different fix, so it gets its own sentence.
    return (
      <p className="queue queue--bad" role="status" aria-live="polite">
        This terminal has been signed out by the server. Sign in again to send
        today's sales.
      </p>
    );
  }

  if (network.reachable) {
    return (
      <p className="queue queue--ok" role="status" aria-live="polite">
        Connected.
      </p>
    );
  }

  // Offline is stated with what still works, because for a cashier mid-queue
  // the useful information is not that the network is down but that they can
  // carry on selling.
  return (
    <p className="queue queue--warn" role="status" aria-live="polite">
      No connection — selling from {cached} cached item
      {cached === 1 ? '' : 's'}. Sales are saved on this terminal.
    </p>
  );
}

/** The backlog. Says nothing about reachability. */
function QueueLine({ terminal }: { terminal: TerminalState }) {
  const { pending, failed } = terminal.counts;

  if (failed > 0) {
    return (
      <p className="queue queue--bad" role="status" aria-live="polite">
        <strong>
          {failed} sale{failed === 1 ? '' : 's'} the server refused.
        </strong>{' '}
        They are still on this terminal. Ask an owner to look.
      </p>
    );
  }

  if (pending === 0) {
    return (
      <p className="queue queue--ok" role="status" aria-live="polite">
        Nothing waiting to send.
      </p>
    );
  }

  return (
    <p className="queue queue--warn" role="status" aria-live="polite">
      {pending} sale{pending === 1 ? '' : 's'} waiting to send
      {terminal.sending ? ' — sending now…' : ''}.
    </p>
  );
}
