import { describe, expect, it } from 'vitest';

import {
  barcodeProblem,
  blankDraft,
  draftOf,
  fieldsFor,
  isTextField,
  perSheet,
  templateBody,
  templateProblem,
  type LabelTemplate,
  type TemplateDraft,
} from './studio';

const draft = (over: Partial<TemplateDraft> = {}): TemplateDraft => ({
  name: 'Shelf ticket',
  kind: 'thermal',
  width_mm: '50',
  height_mm: '25',
  columns: '',
  rows: '',
  margin_mm: '1',
  gap_mm: '0',
  is_default: false,
  fields: [
    { field: 'name', size: 7 },
    { field: 'price', size: 9, bold: true },
    { field: 'barcode', height: 10 },
  ],
  ...over,
});

describe('what a label may carry', () => {
  it('offers a garment its own fields and a card the person on it', () => {
    // `customer_name` on a hang tag would print the product name, because a
    // print run is built from a variant. `attributes` on a loyalty card would
    // print a colour and a size for a person.
    expect(fieldsFor('thermal')).toContain('price');
    expect(fieldsFor('thermal')).not.toContain('customer_name');
    expect(fieldsFor('loyalty_card')).toContain('customer_name');
    expect(fieldsFor('loyalty_card')).not.toContain('attributes');
  });

  it('knows which lines take a point size and which take a height', () => {
    expect(isTextField('name')).toBe(true);
    expect(isTextField('price')).toBe(true);
    expect(isTextField('barcode')).toBe(false);
    expect(isTextField('logo')).toBe(false);
  });
});

describe('what stops a layout being saved', () => {
  it('accepts an ordinary thermal ticket', () => {
    expect(templateProblem(draft())).toBeNull();
  });

  it('names what is missing in the order somebody fixes it', () => {
    expect(templateProblem(draft({ name: '  ' }))).toBe('no_name');
    expect(templateProblem(draft({ kind: 'billboard' }))).toBe('unknown_kind');
    expect(templateProblem(draft({ width_mm: '0' }))).toBe('bad_width');
    expect(templateProblem(draft({ height_mm: 'tall' }))).toBe('bad_height');
    expect(templateProblem(draft({ fields: [] }))).toBe('no_fields');
  });

  it('refuses a field the printer has no data for', () => {
    // The failure this prevents is silent: the layout is right, one line is
    // blank, and nobody finds out until the tags are on a rail.
    expect(
      templateProblem(draft({ fields: [{ field: 'supplier_telephone', size: 7 }] })),
    ).toBe('unprintable_field');
  });

  it('asks a sheet how the labels sit on it, and a roll nothing', () => {
    const sheet = draft({ kind: 'a4_sheet', columns: '', rows: '' });
    expect(templateProblem(sheet)).toBe('needs_grid');
    expect(templateProblem({ ...sheet, columns: '3', rows: '8' })).toBeNull();
    // A roll with no grid is complete, which the thermal case above shows.
  });

  it('refuses a grid of nothing across', () => {
    expect(
      templateProblem(draft({ kind: 'a4_sheet', columns: '0', rows: '8' })),
    ).toBe('needs_grid');
  });
});

describe('the request the editor sends', () => {
  it('sends a grid only for a sheet', () => {
    expect(templateBody(draft())).not.toHaveProperty('columns');
    const sheet = templateBody(draft({ kind: 'a4_sheet', columns: '3', rows: '8' }));
    expect(sheet.columns).toBe(3);
    expect(sheet.rows).toBe(8);
  });

  it('defaults an empty margin to zero rather than sending an empty string', () => {
    const body = templateBody(draft({ margin_mm: '', gap_mm: '' }));
    expect(body.margin_mm).toBe('0');
    expect(body.gap_mm).toBe('0');
  });

  it('trims the name, because a trailing space is a different label', () => {
    expect(templateBody(draft({ name: '  Shelf ticket  ' })).name).toBe('Shelf ticket');
  });
});

describe('opening an existing layout in the editor', () => {
  const template: LabelTemplate = {
    id: 't1',
    name: 'A4 sheet of 24',
    kind: 'a4_sheet',
    width_mm: '63.50',
    height_mm: '33.90',
    columns: 3,
    rows: 8,
    margin_mm: '8.00',
    gap_mm: '2.50',
    fields: [{ field: 'name', size: 7 }],
    is_default: true,
    per_sheet: 24,
  };

  it('carries the grid across as text the boxes can hold', () => {
    const d = draftOf(template);
    expect(d.columns).toBe('3');
    expect(d.rows).toBe('8');
    expect(d.is_default).toBe(true);
  });

  it('copies the field list rather than sharing it with the table behind', () => {
    const d = draftOf(template);
    d.fields[0]!.size = 99;
    expect(template.fields[0]?.size).toBe(7);
  });
});

describe('a new layout starts at the size the hardware comes in', () => {
  it('gives a thermal roll 50 by 25, which is what the box says', () => {
    const d = blankDraft('thermal');
    expect(d.width_mm).toBe('50');
    expect(d.height_mm).toBe('25');
    // Not saveable yet: it has no name, which is the one thing only the shop
    // can supply.
    expect(templateProblem(d)).toBe('no_name');
  });

  it('gives an A4 sheet a grid, so it is complete once it is named', () => {
    const named = { ...blankDraft('a4_sheet'), name: 'Sheet of 24' };
    expect(templateProblem(named)).toBeNull();
    expect(perSheet({ columns: Number(named.columns), rows: Number(named.rows) })).toBe(24);
  });

  it('starts a loyalty card with the person on it, not a colour and a size', () => {
    expect(blankDraft('loyalty_card').fields.map((f) => f.field)).toEqual([
      'customer_name',
      'barcode',
    ]);
  });
});

describe('how many fit on a sheet', () => {
  it('reports nothing for a roll, which does not come in sheets', () => {
    expect(perSheet({})).toBeNull();
    expect(perSheet({ columns: 3 })).toBeNull();
  });
});

describe('overriding a barcode by hand', () => {
  const onScreen = [
    { variant_id: 'v1', barcode: '5901234123457' },
    { variant_id: 'v2', barcode: '4006381333931' },
  ];

  it('accepts a code no product on screen carries', () => {
    expect(barcodeProblem('1234567890128', '5901234123457', onScreen, 'v1')).toBeNull();
  });

  it('refuses an empty code', () => {
    expect(barcodeProblem('   ', '5901234123457', onScreen, 'v1')).toBe('empty');
  });

  it('says nothing has changed rather than sending a no-op', () => {
    // The server recognises the no-op and records nothing, so the button would
    // appear to do nothing at all.
    expect(barcodeProblem('5901234123457', '5901234123457', onScreen, 'v1')).toBe(
      'unchanged',
    );
  });

  it('catches an obvious clash before the server has to', () => {
    // A hint, not the rule: this only sees what is on screen. The unique index
    // is what actually makes a barcode unique.
    expect(barcodeProblem('4006381333931', '5901234123457', onScreen, 'v1')).toBe(
      'taken_here',
    );
  });

  it('does not call a product a clash with itself', () => {
    expect(barcodeProblem('5901234123457', '', onScreen, 'v1')).toBeNull();
  });
});
