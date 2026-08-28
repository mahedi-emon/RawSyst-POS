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
import { useT } from '@rawsyst/shared/i18n/locale';

export function TerminalBanner({ caps }: { caps: Capabilities | null }) {
  const t = useT();
  if (!caps || caps.signing_available) return null;

  return (
    <div className="banner banner--warning" role="status">
      <strong>{t('term.einvoicingNotActive')}</strong>{' '}
      {t('term.einvoicingBody')}
      {caps.key.status === 'not_started' && ` ${t('term.notOnboarded')}`}
    </div>
  );
}
