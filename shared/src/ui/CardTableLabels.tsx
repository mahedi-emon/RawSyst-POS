// Keeps every table's card-list labels in step with its header.
//
// Mounted once per application, next to the locale provider. It watches the
// document for tables appearing or changing and stamps each body cell with the
// column heading it sits under, which is what the card-list CSS reads at phone
// widths. See ui/cardTable.ts for why the labels are stamped rather than
// written into 174 cells by hand.
//
// It writes only `data-label`, only on `<td>` elements inside `.ds-table`, and
// only when the value would change. It never moves, adds or removes a node, so
// it cannot fight React's reconciliation — React does not track attributes it
// did not set.

import { useEffect } from 'react';

import { stampCardLabels } from './cardTable';

export function CardTableLabels() {
  useEffect(() => {
    const doc = globalThis.document;
    if (!doc || typeof MutationObserver === 'undefined') return;

    const stampAll = () => {
      for (const table of Array.from(doc.querySelectorAll('table.ds-table'))) {
        stampCardLabels(table as HTMLTableElement);
      }
    };

    stampAll();

    // Batched into an animation frame. A screen loading its data can mutate the
    // DOM hundreds of times in a tick, and stamping on each one would turn a
    // table render into a layout thrash for no visible gain.
    let queued = 0;
    const observer = new MutationObserver(() => {
      if (queued) return;
      queued = requestAnimationFrame(() => {
        queued = 0;
        stampAll();
      });
    });

    observer.observe(doc.body, { childList: true, subtree: true });

    return () => {
      observer.disconnect();
      if (queued) cancelAnimationFrame(queued);
    };
  }, []);

  return null;
}
