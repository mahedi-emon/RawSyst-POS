import { describe, expect, it } from 'vitest';
import { en } from '../i18n/strings';
import type { Key } from '../i18n/strings';

// describeUnit, architectureName and missingCsrFields return catalogue KEYS
// now, because the text they used to hold could not be translated from a module
// constant. Resolving them through the real English catalogue keeps these
// assertions about the words a shop owner actually reads.
const englishT = (key: Key) => en[key];


import { emptyCsr, type Csr, type EgsUnit } from '../api/egs';
import type { Terminal } from '../api/devices';
import {
  architectureName,
  describeUnit,
  isVatGroup,
  missingCsrFields,
  organizationUnitProblem,
  sellingBlocked,
  unitsForStore,
  vatNumberProblem,
} from './egs';

const unit = (over: Partial<EgsUnit> = {}): EgsUnit => ({
  id: 'u1',
  label: 'Main branch',
  architecture: 'smart_pos',
  store_id: 's1',
  store: 'Main Branch',
  csr: { ...emptyCsr },
  csid_status: 'not_started',
  terminals: 0,
  invoices: 0,
  csr_complete: false,
  ...over,
});

const till = (over: Partial<Terminal> = {}): Terminal => ({
  id: 'd1',
  store_id: 's1',
  store: 'Main Branch',
  terminal_label: 'Till 2',
  status: 'pending',
  pending_code: false,
  ...over,
});

const fullCsr: Csr = {
  common_name: 'Till 2',
  egs_serial_number: '1-RawSyst|2-POS|3-000001',
  organization_identifier: '300000000000003',
  organization_unit: 'Main Branch',
  organization_name: 'Test Trading Co',
  country: 'SA',
  invoice_type: '1100',
  location: 'Riyadh',
  industry: 'Retail',
};

describe('what a unit’s ZATCA state means', () => {
  it('does not call a unit that has never been registered an error', () => {
    // Onboarding is not built. A shop has done nothing wrong by not having
    // done it, and a red badge here would send them looking for a fault.
    const d = describeUnit(unit());
    expect(d.tone).toBe('neutral');
    expect(d.next).toBeUndefined();
  });

  it('says what to do next only when there is something to do', () => {
    expect(describeUnit(unit({ csid_status: 'live' })).next).toBeUndefined();
    expect(describeUnit(unit({ csid_status: 'compliance_csid' })).next).toBeTruthy();
    expect(describeUnit(unit({ csid_status: 'expired' })).next).toBeTruthy();
  });

  it('says a revoked unit keeps its past invoices', () => {
    // Retention runs for years after a certificate dies, and somebody reading
    // "revoked" needs to know they have not lost their records.
    const d = describeUnit(unit({ csid_status: 'revoked' }));
    expect(d.tone).toBe('danger');
    expect(englishT(d.next!)).toMatch(/stay valid/i);
  });

  it('names the architectures in words rather than in schema values', () => {
    expect(englishT(architectureName('smart_pos') as Key)).not.toBe('smart_pos');
    expect(englishT(architectureName('centralized_server') as Key)).toMatch(/whole business/i);
  });
});

describe('the nine CSR fields', () => {
  it('lists every one that is still blank, by the name the form uses', () => {
    expect(missingCsrFields(emptyCsr)).toHaveLength(9);
    expect(missingCsrFields(emptyCsr).map(englishT)).toContain('VAT number');
  });

  it('reports nothing outstanding once all nine are filled', () => {
    expect(missingCsrFields(fullCsr)).toEqual([]);
  });

  it('treats whitespace as blank, so a stray space is not a filled field', () => {
    expect(missingCsrFields({ ...fullCsr, industry: '   ' }).map(englishT)).toEqual([
      'Industry',
    ]);
  });
});

describe('the two formats the database also enforces', () => {
  it('accepts a well-formed Saudi VAT number', () => {
    expect(vatNumberProblem('300000000000003')).toBeNull();
  });

  it('refuses one that does not start and end with 3, or is the wrong length', () => {
    expect(vatNumberProblem('100000000000001')).toBeTruthy();
    expect(vatNumberProblem('30000000003')).toBeTruthy();
  });

  it('says nothing about an empty field, which is allowed until onboarding', () => {
    expect(vatNumberProblem('')).toBeNull();
  });

  it('recognises a VAT group from the 11th digit', () => {
    expect(isVatGroup('300000000010003')).toBe(true);
    expect(isVatGroup('300000000000003')).toBe(false);
  });

  it('requires the member tax number, not a branch name, for a VAT group', () => {
    const group: Csr = {
      ...fullCsr,
      organization_identifier: '300000000010003',
      organization_unit: 'Main Branch',
    };
    expect(organizationUnitProblem(group)).toBeTruthy();
    expect(
      organizationUnitProblem({ ...group, organization_unit: '1234567890' }),
    ).toBeNull();
  });

  it('leaves a branch name alone when the VAT number is not a group', () => {
    expect(organizationUnitProblem(fullCsr)).toBeNull();
  });
});

describe('which units a terminal may sign under', () => {
  const central = unit({ id: 'central', store_id: undefined, store: undefined,
    architecture: 'centralized_server' });
  const mine = unit({ id: 'mine', store_id: 's1' });
  const theirs = unit({ id: 'theirs', store_id: 's2' });

  it('offers a central unit to every branch', () => {
    expect(unitsForStore([central, theirs], 's1').map((u) => u.id)).toEqual(['central']);
  });

  it('does not offer another branch’s unit, because the server refuses it', () => {
    // A button that always refuses teaches people to distrust the rest.
    expect(unitsForStore([mine, theirs], 's1').map((u) => u.id)).toEqual(['mine']);
  });

  it('offers only central units before a branch has been chosen', () => {
    expect(unitsForStore([central, mine], '').map((u) => u.id)).toEqual(['central']);
  });

  it('keeps the unit a terminal already has, even after it moved branch', () => {
    // A till that moved keeps its chain. Dropping its own unit off the list
    // would read as an instruction to repoint it, which is the one thing that
    // would break the chain.
    expect(unitsForStore([mine, theirs], 's1', 'theirs').map((u) => u.id)).toEqual([
      'mine',
      'theirs',
    ]);
  });
});

describe('why a terminal cannot sell', () => {
  it('says so when the terminal has no e-invoicing unit', () => {
    // The exact failure Z1 exists to close: the till refuses the sale and
    // nothing on the setup path had mentioned it.
    expect(sellingBlocked(till())).toMatch(/e-invoicing unit/i);
  });

  it('says nothing once a unit is linked', () => {
    expect(sellingBlocked(till({ egs_unit_id: 'u1' }))).toBeNull();
  });

  it('says nothing about a revoked terminal, which has a reason of its own', () => {
    expect(sellingBlocked(till({ status: 'revoked' }))).toBeNull();
  });
});
