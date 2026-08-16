// What this terminal cannot do, said plainly.
//
// A cashier must never be left assuming invoices are reaching ZATCA when the
// signing seam is still gated. A shop could otherwise trade for a week before
// anyone discovered that nothing was reportable — and by then the backlog is a
// compliance problem rather than a configuration one.
//
// So the banner is permanent while signing is unavailable, not a toast that
// disappears. It also says what IS safe, because "e-invoicing unavailable" on
// its own reads as "stop selling", and that would be the wrong reaction: the
// sale, the stock and the books are all recorded correctly.

import type { Capabilities } from './terminal';

export function TerminalBanner({ caps }: { caps: Capabilities | null }) {
  if (!caps || caps.signing_available) return null;

  return (
    <div className="banner banner--warning" role="status">
      <strong>E-invoicing is not active on this terminal.</strong>{' '}
      Sales are recorded, stock and takings are correct, and every invoice is
      queued. None has been reported to ZATCA yet.
      {caps.key.status === 'not_started' && ' This terminal has not been onboarded.'}
    </div>
  );
}
