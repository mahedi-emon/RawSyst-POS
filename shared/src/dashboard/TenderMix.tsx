// How today's takings were paid.
//
// A real operational fact, not decoration. Each method carries a different fee
// and a different settlement delay, so the mix moving is something an owner
// needs to see — and it is the reason this is a bar per method rather than a
// pie: a pie of six near-equal slices is unreadable, and the question here is
// "how much through each", which bars answer directly.
//
// # Methods are never merged
//
// Mada's interchange is materially lower than a scheme card's. Folding them
// into "card" would misstate margin, and E3.1 requires per-tender visibility
// precisely so that an owner can see it. The copy under the chart says so,
// because otherwise the first thing a new user asks is why Visa and Mastercard
// are not one row.

import { money, tenderName } from '../ui/format';
import type { TenderSlice } from '../api/dashboard';

export function TenderMix({
  tenders,
  currency,
}: {
  tenders: TenderSlice[];
  currency: string;
}) {
  const peak = tenders.reduce((max, t) => Math.max(max, width(t.total)), 0);

  return (
    <section className="ds-panel" aria-label="How today was paid">
      <div className="ds-panel__head">
        <h2 className="ds-h3">How today was paid</h2>
      </div>

      <div className="ds-panel__body">
        {tenders.length === 0 ? (
          <div className="ds-state">
            <p className="ds-state__title">Nothing taken yet today</p>
            <p className="ds-state__body">
              The split across cash, cards and wallets appears here as sales are
              rung up.
            </p>
          </div>
        ) : (
          <>
            <ul className="mix">
              {tenders.map((t) => (
                <li className="mix__row" key={t.method}>
                  <span className="mix__name">{tenderName(t.method)}</span>

                  {/* The bar is a background on the track, not an image: it
                      scales with the container and needs no redraw on resize. */}
                  <span className="mix__track" aria-hidden="true">
                    <span
                      className="mix__bar"
                      style={{
                        inlineSize: peak > 0 ? `${(width(t.total) / peak) * 100}%` : '0%',
                      }}
                    />
                  </span>

                  <span className="mix__amount num">
                    {money(t.total, { currency })}
                  </span>
                  <span className="mix__count ds-caption">
                    {t.count} sale{t.count === 1 ? '' : 's'}
                  </span>
                </li>
              ))}
            </ul>

            <p className="ds-caption mix__why">
              Card schemes are listed separately because their fees differ. Merging
              them into one “card” row would misstate your margin.
            </p>
          </>
        )}
      </div>
    </section>
  );
}

/** Decimal string to a number, for BAR WIDTH only.
 *
 * Never for a displayed figure — every amount on this screen stays a string
 * from the server to the DOM. A bar is accurate to a pixel; money is not.
 */
function width(amount: string): number {
  const n = Number(amount);
  return Number.isFinite(n) && n > 0 ? n : 0;
}
