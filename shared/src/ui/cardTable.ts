// Turning a table into a card list on a phone.
//
// Design system §7 is specific about this: "Layout transformations, not hidden
// content. A table becomes a card list on phone; it does not lose columns. If a
// figure matters at 1680 px it matters at 375 px — the Owner checking margin on
// their phone at the airport is a real, stated use case."
//
// Until now every table was wrapped in an overflow-x container instead, which
// is the opposite of what that paragraph asks for: the columns are still there
// and the owner has to swipe sideways through a financial table on a phone to
// find them.
//
// # Why the labels are stamped rather than written
//
// A CSS card list shows each cell as a labelled row, and CSS cannot read the
// `<th>` text into the `<td>` — it needs the label on the cell as an attribute.
// Writing them by hand meant 174 attributes across 16 files, each one a chance
// to label a figure with the wrong column heading, which on a money table is
// worse than not transforming at all.
//
// So the label comes from the table's own header at runtime. There is exactly
// one definition of what a column is called — the `<th>` — and the cards cannot
// disagree with the table they came from.
//
// If this never runs, tables stay tables inside their scroll container, which
// is what they did before. It degrades to the old behaviour rather than to a
// broken one.

/**
 * The label for each column, given the header row's text.
 *
 * Trimmed, and empty for a column that has no heading — an actions column, or
 * the checkbox column on the settlement screen. A card row with the label "" is
 * rendered without a label rather than with a stray colon.
 */
export function columnLabels(headings: readonly string[]): string[] {
  return headings.map((h) => h.replace(/\s+/g, ' ').trim());
}

/**
 * Which label belongs to a cell.
 *
 * Cells and headings line up by position until a cell spans more than one
 * column, at which point everything after it shifts. Returning the heading at
 * the running offset keeps them aligned; a spanning cell takes the first
 * heading it covers, which is the one that names it.
 */
export function labelForCell(
  labels: readonly string[],
  cellIndex: number,
  spansBefore: number,
): string {
  const at = cellIndex + spansBefore;
  return at < labels.length ? (labels[at] ?? '') : '';
}

/**
 * Stamps `data-label` on every body cell of one table.
 *
 * Idempotent: a cell that already carries the right label is left alone, so
 * running this after every render costs a comparison per cell and no DOM write.
 * That matters because it runs from a MutationObserver.
 */
export function stampCardLabels(table: HTMLTableElement): void {
  const head = table.tHead;
  if (!head || head.rows.length === 0) return;

  // The LAST header row, not the first. A table with a grouped header puts the
  // real column names on the row nearest the body.
  const headingRow = head.rows[head.rows.length - 1];
  if (!headingRow) return;

  const labels = columnLabels(
    Array.from(headingRow.cells).map((c) => c.textContent ?? ''),
  );
  if (labels.length === 0) return;

  for (const body of Array.from(table.tBodies)) {
    for (const row of Array.from(body.rows)) {
      let spansBefore = 0;
      for (let i = 0; i < row.cells.length; i++) {
        const cell = row.cells[i];
        if (!cell) continue;

        // A row heading inside the body — the grouped method rows on the
        // settlement screen — labels itself and needs no prefix.
        if (cell.tagName === 'TH') {
          spansBefore += Math.max(1, cell.colSpan) - 1;
          continue;
        }

        const label = labelForCell(labels, i, spansBefore);
        if (label && cell.getAttribute('data-label') !== label) {
          cell.setAttribute('data-label', label);
        }
        spansBefore += Math.max(1, cell.colSpan) - 1;
      }
    }
  }
}
