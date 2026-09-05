import { describe, expect, it } from 'vitest';

import {
  needsEnrolmentCode,
  needsSigningUnit,
  perSheet,
  printProblem,
  settingsChange,
  terminalState,
  warrantyState,
  type LabelTemplate,
  type Serial,
  type Terminal,
  type TerminalSettings,
} from './hardware';

function terminal(over: Partial<Terminal> = {}): Terminal {
  return {
    id: 'd1',
    store_id: 's1',
    store: 'Main Branch',
    terminal_label: 'Till 1',
    status: 'active',
    binding: 'session',
    pending_code: false,
    ...over,
  };
}

describe('terminalState', () => {
  it('reads a working till', () => {
    expect(terminalState(terminal())).toBe('working');
  });

  it('keeps the two meanings of pending apart', () => {
    // On a PAIRED till, pending is the normal state between registering the
    // counter and the machine enrolling: nothing is wrong. On a SESSION till
    // it should never last, because such a counter is authorised by whoever
    // opens it. One word for both would send somebody to fix a working till.
    expect(terminalState(terminal({ status: 'pending', binding: 'paired' }))).toBe(
      'awaiting_machine',
    );
    expect(terminalState(terminal({ status: 'pending', binding: 'session' }))).toBe(
      'awaiting_registration',
    );
  });

  it('reads revoked and suspended apart', () => {
    expect(terminalState(terminal({ status: 'revoked' }))).toBe('revoked');
    expect(terminalState(terminal({ status: 'disabled' }))).toBe('suspended');
  });
});

describe('needsEnrolmentCode', () => {
  it('offers a code only where there is a machine to type it into', () => {
    expect(
      needsEnrolmentCode(terminal({ status: 'pending', binding: 'paired' })),
    ).toBe(true);
    // A session counter has nothing to enrol.
    expect(
      needsEnrolmentCode(terminal({ status: 'pending', binding: 'session' })),
    ).toBe(false);
  });

  it('does not offer one to a till that has already enrolled', () => {
    expect(needsEnrolmentCode(terminal({ binding: 'paired' }))).toBe(false);
  });
});

describe('needsSigningUnit', () => {
  it('is Saudi Arabia and nowhere else', () => {
    expect(needsSigningUnit('sa')).toBe(true);
    expect(needsSigningUnit('SA')).toBe(true);
    // A market with no e-invoicing obligation must not be shown a field it has
    // nothing to put in.
    expect(needsSigningUnit('bd')).toBe(false);
    expect(needsSigningUnit('us')).toBe(false);
  });
});

describe('settingsChange', () => {
  const original: TerminalSettings = {
    device_id: 'd1',
    drawer_enabled: true,
    require_customer: false,
    max_held_carts: 10,
  };

  it('sends only what moved', () => {
    // The route leaves an absent field alone precisely so a screen changing
    // the printer need not restate the discount rule.
    expect(
      settingsChange(original, { ...original, printer_name: 'Counter 1' }),
    ).toEqual({ printer_name: 'Counter 1' });
  });

  it('never sends the device id back', () => {
    expect(settingsChange(original, { ...original, device_id: 'other' })).toEqual({});
  });

  it('is empty when nothing was touched', () => {
    expect(settingsChange(original, { ...original })).toEqual({});
  });

  it('sends a boolean turned off, which is a real change', () => {
    // `false` must not be mistaken for absent: turning the drawer off is
    // exactly the setting somebody came to this screen to change.
    expect(
      settingsChange(original, { ...original, drawer_enabled: false }),
    ).toEqual({ drawer_enabled: false });
  });
});

describe('perSheet', () => {
  function template(over: Partial<LabelTemplate> = {}): LabelTemplate {
    return {
      id: 't1',
      name: 'Shelf tag',
      kind: 'sheet',
      width_mm: '70',
      height_mm: '37',
      margin_mm: '5',
      gap_mm: '2',
      fields: [],
      is_default: false,
      ...over,
    };
  }

  it('prefers the figure the server states', () => {
    expect(perSheet(template({ per_sheet: 24, columns: 3, rows: 8 }))).toBe(24);
  });

  it('multiplies the grid when it must', () => {
    expect(perSheet(template({ columns: 3, rows: 8 }))).toBe(24);
  });

  it('is null for a roll rather than 1', () => {
    // A thermal roll does not come in sheets, and "1 per sheet" invites
    // somebody to work out how many sheets they need.
    expect(perSheet(template({ kind: 'thermal' }))).toBeNull();
  });
});

describe('printProblem', () => {
  const ready = {
    templateID: 't1',
    variantIDs: ['v1'],
    categoryID: '',
    brandID: '',
    search: '',
    copies: '1',
  };

  it('passes a run that names a template and something to print', () => {
    expect(printProblem(ready)).toBe('none');
  });

  it('needs a template', () => {
    expect(printProblem({ ...ready, templateID: '' })).toBe('no_template');
  });

  it('refuses a run that names nothing', () => {
    // An empty selection reads as "every product in the shop", which is a
    // reasonable reading and a very expensive mistake.
    expect(printProblem({ ...ready, variantIDs: [] })).toBe('nothing_selected');
  });

  it('accepts a category, a brand or a search as the selection', () => {
    expect(printProblem({ ...ready, variantIDs: [], categoryID: 'c1' })).toBe('none');
    expect(printProblem({ ...ready, variantIDs: [], brandID: 'b1' })).toBe('none');
    expect(printProblem({ ...ready, variantIDs: [], search: 'abaya' })).toBe('none');
  });

  it('refuses a blank or zero copy count', () => {
    // Number('') is 0, so a blank box would otherwise pass a >= 0 check and
    // print nothing while reporting success.
    expect(printProblem({ ...ready, copies: '' })).toBe('no_copies');
    expect(printProblem({ ...ready, copies: '0' })).toBe('no_copies');
    expect(printProblem({ ...ready, copies: '2.5' })).toBe('no_copies');
  });
});

describe('warrantyState', () => {
  function serial(over: Partial<Serial> = {}): Serial {
    return {
      id: 's1',
      serial_no: 'SN-0001',
      variant_id: 'v1',
      status: 'sold',
      under_warranty: true,
      sold_at: '2026-01-15',
      warranty_until: '2027-01-15',
      ...over,
    };
  }

  it('reads the server flag rather than the date', () => {
    // under_warranty is derived on the server from the date, never stored,
    // because a flag would be wrong every morning until a job ran -- and the
    // warranty desk is where a stale answer costs the shop money.
    expect(warrantyState(serial())).toBe('covered');
    expect(warrantyState(serial({ under_warranty: false }))).toBe('expired');
  });

  it('knows a unit that has not been sold', () => {
    expect(warrantyState(serial({ sold_at: undefined }))).toBe('unsold');
  });

  it('knows a sale that carried no warranty at all', () => {
    expect(warrantyState(serial({ warranty_until: undefined }))).toBe('none');
  });
});
