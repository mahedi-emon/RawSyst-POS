import { describe, expect, it } from 'vitest';

import {
  headerColumns,
  missingColumns,
  nextAction,
  partial,
  unmapped,
  whatFailed,
  type ImportBatch,
  type Shape,
} from './transfer';

const shape: Shape = {
  kind: 'products',
  label: 'Products',
  required: ['sku', 'name'],
  optional: ['name_ar', 'price', 'barcode'],
};

const batch = (over: Partial<ImportBatch> = {}): ImportBatch => ({
  id: 'b1',
  kind: 'products',
  status: 'validated',
  mapping: {},
  total_rows: 100,
  valid_rows: 100,
  error_rows: 0,
  imported_rows: 0,
  created_at: '2026-09-06T04:00:00Z',
  ...over,
});

describe('what happens to a batch next', () => {
  it('checks before it writes', () => {
    expect(nextAction(batch({ status: 'uploaded' }))).toBe('check');
    expect(nextAction(batch({ status: 'validated' }))).toBe('commit');
  });

  it('will not offer to commit a file where nothing passed', () => {
    // Committing writes nothing and reads as success, which is the worst of
    // the outcomes: the person believes their catalogue is loaded.
    expect(nextAction(batch({ valid_rows: 0, error_rows: 100 }))).toBe('nothing_valid');
  });

  it('has nothing to offer on one that is finished or abandoned', () => {
    expect(nextAction(batch({ status: 'committed' }))).toBe('done');
    expect(nextAction(batch({ status: 'cancelled' }))).toBe('stopped');
    expect(nextAction(batch({ status: 'failed' }))).toBe('stopped');
  });

  it('says when some rows would be left behind', () => {
    // "Import 1,847 of 2,000" is a decision. "Import" is a button somebody
    // presses without knowing 153 rows are about to be dropped.
    expect(partial(batch({ valid_rows: 1847, error_rows: 153 }))).toBe(true);
    expect(partial(batch({ valid_rows: 2000, error_rows: 0 }))).toBe(false);
    expect(partial(batch({ valid_rows: 0, error_rows: 2000 }))).toBe(false);
  });
});

describe('a file measured against the shape the server publishes', () => {
  it('names a required column the file does not have', () => {
    expect(missingColumns(shape, ['sku', 'price'])).toEqual(['name']);
    expect(missingColumns(shape, ['sku', 'name', 'price'])).toEqual([]);
  });

  it('names columns the importer will ignore', () => {
    // Not an error. But a column that was MEANT to map and was spelled
    // differently looks exactly like a column nobody wanted.
    expect(unmapped(shape, ['sku', 'name', 'Price', 'internal_ref'])).toEqual([
      'Price',
      'internal_ref',
    ]);
  });

  it('reads a header line the way a spreadsheet wrote it', () => {
    expect(headerColumns('sku, name ,"name_ar", price')).toEqual([
      'sku',
      'name',
      'name_ar',
      'price',
    ]);
    expect(headerColumns('sku,,name')).toEqual(['sku', 'name']);
  });
});

describe('what went wrong, without drowning the reader', () => {
  it('shows the first few failures and no more', () => {
    const rows = Array.from({ length: 40 }, (_, i) => ({
      row_no: i + 1,
      raw: {},
      status: 'invalid',
      error: 'sku is already used',
    }));
    expect(whatFailed(batch({ rows }))).toHaveLength(10);
    expect(whatFailed(batch({ rows }), 3)).toHaveLength(3);
  });

  it('ignores the rows that were fine', () => {
    expect(
      whatFailed(
        batch({
          rows: [
            { row_no: 1, raw: {}, status: 'valid' },
            { row_no: 2, raw: {}, status: 'invalid', error: 'no name' },
          ],
        }),
      ).map((r) => r.row_no),
    ).toEqual([2]);
  });

  it('says nothing when the rows were not fetched', () => {
    // A batch listing carries no rows. Reading their absence as "nothing
    // failed" would contradict error_rows on the same object.
    expect(whatFailed(batch({ error_rows: 5 }))).toEqual([]);
  });
});
