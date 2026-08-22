import { describe, expect, it } from 'vitest';

import { columnLabels, labelForCell } from './cardTable';

// The mixed-script fixture QA gate M6 names, used here because a column heading
// is exactly the kind of string that gets mangled: it is read out of the DOM,
// trimmed, and written back as an attribute.
const MIXED = 'قميص رجالي Slim Fit — L';

describe('reading the column names off a table header', () => {
  it('trims and collapses the whitespace a JSX heading carries', () => {
    // A heading written across two lines in the source arrives with a newline
    // and an indent in its textContent. Stamped raw, it would render as a label
    // with a line break inside a flex row.
    expect(columnLabels(['  Invoice\n   number ', 'Amount'])).toEqual([
      'Invoice number',
      'Amount',
    ]);
  });

  it('leaves an unheaded column with no label', () => {
    // The actions column and the settlement screen's checkbox column have empty
    // headers. They must produce no label rather than an empty one, or every
    // card grows a blank row.
    expect(columnLabels(['Supplier', '', '   '])).toEqual(['Supplier', '', '']);
  });

  it('keeps a heading in either script intact', () => {
    expect(columnLabels([MIXED])).toEqual([MIXED]);
    expect(columnLabels(['المبلغ'])).toEqual(['المبلغ']);
  });
});

describe('matching a cell to its column', () => {
  const labels = ['Invoice', 'Taken', 'Amount'];

  it('lines up by position in the ordinary case', () => {
    expect(labelForCell(labels, 0, 0)).toBe('Invoice');
    expect(labelForCell(labels, 2, 0)).toBe('Amount');
  });

  it('shifts after a cell that spans columns', () => {
    // A row whose first cell spans two columns has its second cell sitting
    // under the THIRD heading. Ignoring the span would label a figure with the
    // name of the column beside it, which on a money table is worse than no
    // label at all.
    expect(labelForCell(labels, 1, 1)).toBe('Amount');
  });

  it('gives no label to a cell past the end of the header', () => {
    // A footer row with an extra cell, or a malformed table. Better an
    // unlabelled row than a crash or a wrong name.
    expect(labelForCell(labels, 5, 0)).toBe('');
    expect(labelForCell(labels, 2, 3)).toBe('');
  });
});
