// What the till still owes the server.
//
// A cashier closing up needs one question answered plainly: has today's
// takings reached the server? "23 sales waiting" is the difference between
// going home and staying to investigate, and a green tick that lies is worse
// than no indicator at all.

import type { QueueState } from '../offline/useQueue';

export function QueueStatus({ queue }: { queue: QueueState }) {
  if (!queue.ready) {
    // Running outside the Tauri shell, in a browser during development.
    // Said plainly rather than shown as healthy: nothing is durable here.
    return (
      <p className="queue queue--warn" role="status">
        No local storage on this terminal — sales are not being saved.
      </p>
    );
  }

  const { pending, failed } = queue.counts;

  if (failed > 0) {
    return (
      <p className="queue queue--bad" role="status" aria-live="polite">
        <strong>{failed} sale{failed === 1 ? '' : 's'} the server refused.</strong>{' '}
        They are still on this terminal. Ask an owner to look.
      </p>
    );
  }

  if (pending === 0) {
    return (
      <p className="queue queue--ok" role="status" aria-live="polite">
        {queue.online ? 'Everything has reached the server.' : 'Nothing waiting to send.'}
      </p>
    );
  }

  return (
    <p className="queue queue--warn" role="status" aria-live="polite">
      {pending} sale{pending === 1 ? '' : 's'} waiting to send
      {queue.sending ? ' — sending now…' : queue.online ? '' : ' — no connection'}.
    </p>
  );
}
