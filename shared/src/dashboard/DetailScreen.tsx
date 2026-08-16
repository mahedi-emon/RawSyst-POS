// The frame every drill-through sits in.
//
// One frame rather than four, so a detail screen always has the same shape: a
// way back, a title that names what you opened, the figure it opened from, and
// the same five states. Consistency here is not tidiness — it is what lets
// someone who has used one of these screens use all of them.
//
// # Getting back matters more than it looks
//
// A8 promises one click IN. One click out is the other half of that promise: a
// drill-through you have to navigate out of is a trap, and users stop clicking
// into things that trap them.

import type { ReactNode } from 'react';

import type { Remote } from './useRemote';

export function DetailScreen({
  title,
  subtitle,
  onBack,
  backLabel = 'Dashboard',
  onRefresh,
  refreshing,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  onBack: () => void;
  /** Where back goes, in the reader's words.
   *
   * Defaulted to Dashboard because that is where most of these were opened
   * from, but a purchase order was reached from the order list and saying
   * "Dashboard" there is simply wrong — a browser check read it out loud and
   * caught it. The label has to name the place, or the control is a guess. */
  backLabel?: string;
  onRefresh?: () => void;
  refreshing?: boolean;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <main className="detail">
      <header className="detail__head">
        <button className="detail__back" onClick={onBack}>
          {/* Logical, so it points the right way once the page mirrors into
              Arabic. The arrow is decorative; the word carries the meaning. */}
          <span aria-hidden="true" className="detail__backarrow">
            ←
          </span>
          {backLabel}
        </button>

        <div className="detail__titles">
          <h1 className="ds-h1">{title}</h1>
          {subtitle && <p className="ds-caption">{subtitle}</p>}
        </div>

        <div className="detail__actions">
          {actions}
          {onRefresh && (
            <button
              className="ds-btn ds-btn--quiet"
              onClick={onRefresh}
              disabled={refreshing}
            >
              {refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          )}
        </div>
      </header>

      {children}
    </main>
  );
}

/**
 * Renders the four states that are not "ready", and hands `ready` back.
 *
 * A render prop rather than four copies of the same switch. Each screen then
 * contains only its own layout, which is the part that genuinely differs.
 */
export function RemoteBody<T>({
  remote,
  onRetry,
  children,
}: {
  remote: Remote<T>;
  onRetry: () => void;
  children: (data: T) => ReactNode;
}) {
  switch (remote.state) {
    case 'loading':
      return <TableSkeleton />;

    case 'denied':
      return (
        <Panel
          title="You do not have access to this"
          body="Your role does not include permission to view these records. An owner can change that under Settings > People."
        />
      );

    case 'offline':
      // Neutral, not red. The system is working; the network is not.
      return (
        <Panel
          title="No connection to the server"
          body="These records are held on the server, so they need a connection. Selling is unaffected — the till keeps working offline."
          onRetry={onRetry}
        />
      );

    case 'error':
      return <Panel title="That did not load" body={remote.message} onRetry={onRetry} />;

    case 'ready':
      return <>{children(remote.data)}</>;
  }
}

function Panel({
  title,
  body,
  onRetry,
}: {
  title: string;
  body: string;
  onRetry?: () => void;
}) {
  return (
    <div className="ds-panel">
      <div className="ds-state">
        <p className="ds-state__title">{title}</p>
        <p className="ds-state__body">{body}</p>
        {onRetry && (
          <button className="ds-btn ds-btn--secondary" onClick={onRetry}>
            Try again
          </button>
        )}
      </div>
    </div>
  );
}

/** A skeleton shaped like a table, because that is what every one of these
 *  screens resolves into. Matching the real layout stops the page jumping when
 *  the data lands. */
function TableSkeleton() {
  return (
    <div className="ds-panel" aria-busy="true" aria-label="Loading">
      <div className="ds-panel__body">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div
            key={i}
            className="ds-skeleton"
            style={{ blockSize: 20, marginBlockEnd: 12, opacity: 1 - i * 0.12 }}
          />
        ))}
      </div>
    </div>
  );
}

/** Nothing to show, said in the screen's own words.
 *
 * Never a shrug. An empty list is a fact about the business — a quiet day, a
 * healthy stock room — and saying which one reassures the reader that the
 * screen is working. */
export function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="ds-state">
      <p className="ds-state__title">{title}</p>
      <p className="ds-state__body">{body}</p>
    </div>
  );
}
