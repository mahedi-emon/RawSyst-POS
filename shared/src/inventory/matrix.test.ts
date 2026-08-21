import { describe, expect, it } from 'vitest';

import type { MatrixCell } from '../api/catalog';
import {
  availableAxes,
  buildGrid,
  cellKey,
  DEAD_AFTER_DAYS,
  isDead,
  readCell,
  summarise,
  trimQty,
} from './matrix';

const TODAY = new Date('2026-08-21T00:00:00Z');

/** Days before TODAY, as the server formats a date. */
function daysAgo(n: number): string {
  return new Date(TODAY.getTime() - n * 86_400_000).toISOString().slice(0, 10);
}

function cell(over: Partial<MatrixCell> = {}): MatrixCell {
  return {
    id: 'v1',
    sku: 'ABAYA-BLK-L',
    attributes: { colour: 'Black', size: 'L' },
    price: '449.00',
    is_active: true,
    on_hand: '19',
    last_sold_at: daysAgo(3),
    ...over,
  };
}

describe('how a cell reads', () => {
  it('leaves a healthy cell plain, with no label', () => {
    // Spec §4 gives "In stock" no treatment. A grid where every cell carries a
    // word has emphasised nothing.
    const got = readCell(cell({ reorder_level: '5' }), TODAY);
    expect(got.state).toBe('in_stock');
    expect(got.label).toBe('');
  });

  it('calls a cell Out when nothing is left', () => {
    const got = readCell(cell({ on_hand: '0' }), TODAY);
    expect(got).toMatchObject({ state: 'out', label: 'Out' });
  });

  it('treats negative stock as out rather than as a quantity', () => {
    // Stock can go below zero where the policy allows it. A cell reading "-3"
    // with no label would look like a healthy line holding minus three.
    expect(readCell(cell({ on_hand: '-3' }), TODAY).state).toBe('out');
  });

  it('calls a cell Low at or below the reorder level', () => {
    expect(readCell(cell({ on_hand: '5', reorder_level: '5' }), TODAY).label).toBe('Low');
    expect(readCell(cell({ on_hand: '4', reorder_level: '5' }), TODAY).label).toBe('Low');
    expect(readCell(cell({ on_hand: '6', reorder_level: '5' }), TODAY).label).toBe('');
  });

  it('never calls a cell Low when nobody has said what low means', () => {
    // Inventing a threshold would send a shop ordering against a number the
    // product made up.
    const got = readCell(cell({ on_hand: '1', reorder_level: undefined }), TODAY);
    expect(got.state).toBe('in_stock');
  });

  it('calls a cell Dead after ninety days without a sale', () => {
    expect(readCell(cell({ last_sold_at: daysAgo(89) }), TODAY).state).toBe('in_stock');
    expect(readCell(cell({ last_sold_at: daysAgo(DEAD_AFTER_DAYS) }), TODAY).label).toBe('Dead');
    expect(readCell(cell({ last_sold_at: daysAgo(200) }), TODAY).label).toBe('Dead');
  });

  it('shows a combination the product does not have as empty, not as out', () => {
    // A shop stocks Black in S–XXL and Maroon in M–L. The gaps are the point of
    // the grid, and calling them "Out" would invent a shortage.
    const got = readCell(undefined, TODAY);
    expect(got.state).toBe('empty');
    expect(got.label).toBe('');
  });

  it('carries a word for every state that is not normal', () => {
    // Spec §4 and the design system both: colour is never the only signal.
    for (const c of [
      cell({ on_hand: '0' }),
      cell({ on_hand: '2', reorder_level: '5' }),
      cell({ last_sold_at: daysAgo(120) }),
    ]) {
      expect(readCell(c, TODAY).label).not.toBe('');
    }
  });

  it('reads quantities aloud without the column scale', () => {
    // numeric(18,4) means the server sends "19.0000". A screen reader saying
    // "nineteen point zero zero zero zero in stock" is worse than useless on a
    // grid somebody is scanning.
    const got = readCell(cell({ on_hand: '19.0000', reorder_level: '5.0000' }), TODAY);
    expect(got.description).toBe('19 in stock');

    const low = readCell(cell({ on_hand: '3.0000', reorder_level: '5.0000' }), TODAY);
    expect(low.description).toContain('3 in stock');
    expect(low.description).toContain('reorder level of 5');
    expect(low.description).not.toContain('0000');
  });

  it('describes every cell in words for a screen reader', () => {
    // The state has to survive somebody who cannot see the cell at all, not
    // merely somebody who cannot tell two tints apart.
    for (const c of [
      cell({ reorder_level: '5' }),
      cell({ on_hand: '0' }),
      cell({ on_hand: '2', reorder_level: '5' }),
      cell({ last_sold_at: daysAgo(120) }),
    ]) {
      expect(readCell(c, TODAY).description).not.toBe('');
    }
  });
});

describe('which state wins', () => {
  it('prefers Out over Dead', () => {
    // A line with nothing left is a line to reorder or retire; "Out" is the
    // half somebody can act on today.
    const got = readCell(cell({ on_hand: '0', last_sold_at: daysAgo(300) }), TODAY);
    expect(got.label).toBe('Out');
  });

  it('prefers Dead over Low', () => {
    // Reordering something that has not moved in three months is the mistake
    // this grid exists to prevent.
    const got = readCell(
      cell({ on_hand: '2', reorder_level: '5', last_sold_at: daysAgo(120) }),
      TODAY,
    );
    expect(got.label).toBe('Dead');
  });
});

describe('dead stock', () => {
  it('needs stock: an empty line is not dead, it is out', () => {
    expect(isDead(cell({ on_hand: '0', last_sold_at: daysAgo(400) }), TODAY)).toBe(false);
  });

  it('counts a never-sold line that holds stock as dead', () => {
    // It has sat there since the day it arrived, which is the worst case rather
    // than an unknown one.
    expect(isDead(cell({ last_sold_at: undefined }), TODAY)).toBe(true);
  });

  it('does not call a new line with no stock dead', () => {
    // Every grid would light up grey the day it was created.
    expect(isDead(cell({ on_hand: '0', last_sold_at: undefined }), TODAY)).toBe(false);
  });

  it('ignores a date it cannot read rather than guessing', () => {
    expect(isDead(cell({ last_sold_at: 'not a date' }), TODAY)).toBe(false);
  });
});

describe('laying out the grid', () => {
  const cells = [
    cell({ id: '1', sku: 'A-BLK-S', attributes: { colour: 'Black', size: 'S' }, on_hand: '14' }),
    cell({ id: '2', sku: 'A-BLK-M', attributes: { colour: 'Black', size: 'M' }, on_hand: '22' }),
    cell({ id: '3', sku: 'A-NVY-S', attributes: { colour: 'Navy', size: 'S' }, on_hand: '9' }),
  ];

  it('keeps the order values first appear, not alphabetical', () => {
    // S, M, L, XL, XXL is the order a person reads. Alphabetical gives
    // L, M, S, XL, XXL, which is worse than useless on the one screen where
    // scanning a row quickly is the entire point.
    const grid = buildGrid(
      [
        cell({ attributes: { colour: 'Black', size: 'S' } }),
        cell({ attributes: { colour: 'Black', size: 'M' } }),
        cell({ attributes: { colour: 'Black', size: 'L' } }),
        cell({ attributes: { colour: 'Black', size: 'XL' } }),
      ],
      'colour',
      'size',
    );
    expect(grid.columns.values).toEqual(['S', 'M', 'L', 'XL']);
  });

  it('places each variant at its own row and column', () => {
    const grid = buildGrid(cells, 'colour', 'size');
    expect(grid.rows.values).toEqual(['Black', 'Navy']);
    expect(grid.cells.get(cellKey('Black', 'M'))?.sku).toBe('A-BLK-M');
    expect(grid.cells.get(cellKey('Navy', 'S'))?.sku).toBe('A-NVY-S');
    // Navy M was never stocked.
    expect(grid.cells.get(cellKey('Navy', 'M'))).toBeUndefined();
  });

  it('reports attributes it could not fit on the grid', () => {
    // A grid shows two dimensions. Folding a third silently would put two
    // different variants in one cell and show one of them.
    const grid = buildGrid(
      [cell({ attributes: { colour: 'Black', size: 'L', season: 'Winter' } })],
      'colour',
      'size',
    );
    expect(grid.extraAxes).toEqual(['season']);
  });

  it('offers axes most-used first', () => {
    // A product where every variant has a size and three have a season should
    // open on size.
    const mixed = [
      cell({ attributes: { size: 'S', colour: 'Black' } }),
      cell({ attributes: { size: 'M', colour: 'Black' } }),
      cell({ attributes: { size: 'L' } }),
    ];
    expect(availableAxes(mixed)).toEqual(['size', 'colour']);
  });

  it('copes with a product whose variants carry no attributes at all', () => {
    const grid = buildGrid([cell({ attributes: {} })], 'colour', 'size');
    expect(grid.rows.values).toEqual([]);
    expect(grid.cells.size).toBe(0);
  });
});

describe('the summary under the grid', () => {
  it('counts the total and the shape behind it', () => {
    // Spec §4's whole argument: a standard POS shows "126 in stock" and hides
    // that Black XL is out while Maroon XXL has not moved in three months.
    const got = summarise(
      [
        cell({ on_hand: '14', reorder_level: '5' }),
        cell({ on_hand: '0' }),
        cell({ on_hand: '3', reorder_level: '5' }),
        cell({ on_hand: '2', last_sold_at: daysAgo(120) }),
      ],
      TODAY,
    );
    expect(got).toEqual({ total: 19, out: 1, low: 1, dead: 1, variants: 4 });
  });

  it('is all zeros for a product with no variants', () => {
    expect(summarise([], TODAY)).toEqual({
      total: 0, out: 0, low: 0, dead: 0, variants: 0,
    });
  });
});

describe('quantities in a cell', () => {
  it('drops trailing zeros a decimal column would carry', () => {
    expect(trimQty('19.0000')).toBe('19');
    expect(trimQty('19')).toBe('19');
    expect(trimQty('0.0000')).toBe('0');
  });

  it('keeps a genuine fraction, because some things sell by weight', () => {
    expect(trimQty('1.5000')).toBe('1.5');
    expect(trimQty('0.2500')).toBe('0.25');
  });
});
