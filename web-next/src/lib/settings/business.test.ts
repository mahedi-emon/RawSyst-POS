import { describe, expect, it } from 'vitest';

import {
  addressLine,
  amendment,
  allPeriods,
  canClose,
  closeBlock,
  isCompanyField,
  localDay,
  periodOn,
  splitMissing,
  invoiceBlock,
  isSettled,
  settledReason,
  yearExists,
  type Branch,
  type Business,
  type FiscalYear,
  type Period,
} from './business';

function business(over: Partial<Business> = {}): Business {
  return {
    id: 'c1',
    legal_name: 'Demo Retail',
    legal_name_ar: '',
    trade_name: 'Demo Retail',
    country: 'sa',
    market: 'sa',
    market_name: 'Saudi Arabia',
    base_currency: 'SAR',
    timezone: 'Asia/Riyadh',
    cr_number: '',
    vat_registered: true,
    vat_number: '355766015445003',
    zatca_wave: '',
    zatca_deadline: '',
    zatca_status: 'not_started',
    b2b_offline_policy: 'block',
    negative_stock_policy: 'block',
    costing_method: 'wac',
    fiscal_year_start_month: 1,
    match_tolerance_pct: '2.0000',
    match_tolerance_amount: '50.0000',
    wps_bank_sarie_id: '',
    wps_establishment_id: '',
    wps_bank_account: '',
    mol_establishment_id: '',
    settled: {
      base_currency: 'Every figure in your books is already recorded in this currency.',
      country: 'The market is set when your account is created.',
    },
    ...over,
  };
}

function branch(over: Partial<Branch> = {}): Branch {
  return {
    id: 'b1',
    code: 'MAIN',
    name: 'Main Branch',
    is_active: true,
    street: 'King Fahd Road',
    building_number: '1234',
    district: 'Al Olaya',
    city: 'Riyadh',
    postal_code: '12211',
    country_code: '',
    effective_country_code: 'SA',
    can_invoice: true,
    incomplete: [],
    ...over,
  };
}

describe('isSettled and settledReason', () => {
  it('reads the map, never the value', () => {
    // A currency being set does not mean it is fixed: base_currency is
    // perfectly changeable on the day a company is created.
    const fresh = business({ settled: {} });
    expect(fresh.base_currency).toBe('SAR');
    expect(isSettled(fresh, 'base_currency')).toBe(false);
  });

  it('knows a field that has settled', () => {
    expect(isSettled(business(), 'base_currency')).toBe(true);
    expect(isSettled(business(), 'legal_name')).toBe(false);
  });

  it('hands back the sentence the server wrote', () => {
    expect(settledReason(business(), 'country')).toBe(
      'The market is set when your account is created.',
    );
    expect(settledReason(business(), 'legal_name')).toBeNull();
  });

  it('survives a payload with no settled map at all', () => {
    expect(isSettled({ ...business(), settled: undefined } as never, 'country')).toBe(
      false,
    );
  });
});

describe('amendment', () => {
  it('sends only what changed', () => {
    // The PUT is partial. Sending the whole record back would turn "I renamed
    // the shop" into "every other field is exactly as I found it".
    expect(
      amendment(business(), { trade_name: 'Demo Retail Co', legal_name: 'Demo Retail' }),
    ).toEqual({ trade_name: 'Demo Retail Co' });
  });

  it('SENDS a settled field, so the server can refuse it with its reason', () => {
    // The client's settled map was true when the page loaded. If a field
    // settles while the form is open, dropping it here would save everything
    // else and leave the person believing their change went through; sending
    // it earns a 409 carrying the sentence they need to read.
    expect(amendment(business(), { base_currency: 'USD' })).toEqual({
      base_currency: 'USD',
    });
  });

  it('never sends the settled map or the id back', () => {
    expect(
      amendment(business(), { id: 'other', settled: {} } as Partial<Business>),
    ).toEqual({});
  });

  it('sends an empty string, because that is how a field is cleared', () => {
    expect(amendment(business(), { cr_number: '' })).toEqual({});
    expect(
      amendment(business({ cr_number: '1010101010' }), { cr_number: '' }),
    ).toEqual({ cr_number: '' });
  });

  it('is empty when nothing was touched', () => {
    expect(amendment(business(), {})).toEqual({});
  });
});

describe('addressLine', () => {
  it('reads in order and drops the empty parts', () => {
    expect(addressLine(branch())).toBe(
      '1234, King Fahd Road, Al Olaya, Riyadh, 12211',
    );
  });

  it('leaves no gap where a part is missing', () => {
    // A hole in an address reads as a typo. `incomplete` is what says a part
    // is missing, in the server's own words.
    expect(addressLine(branch({ district: '', postal_code: '' }))).toBe(
      '1234, King Fahd Road, Riyadh',
    );
  });
});

describe('invoiceBlock', () => {
  it('is empty for a branch that can invoice', () => {
    // Even with NO country code of its own. The document layer falls back to
    // the company's country, so deriving this from country_code would report
    // every seeded branch unable to invoice -- including ones with a hundred
    // invoices already behind them.
    const b = branch({ country_code: '', effective_country_code: 'SA' });
    expect(b.can_invoice).toBe(true);
    expect(invoiceBlock(b)).toEqual([]);
  });

  it('names what is missing when it cannot', () => {
    expect(
      invoiceBlock(branch({ can_invoice: false, incomplete: ['postal_code'] })),
    ).toEqual(['postal_code']);
  });
});

function period(over: Partial<Period> = {}): Period {
  return {
    id: 'p1',
    fiscal_year: 2026,
    period_no: 1,
    starts_on: '2026-01-01',
    ends_on: '2026-01-31',
    state: 'open',
    entries: 0,
    ...over,
  };
}

const YEAR: FiscalYear[] = [
  {
    fiscal_year: 2026,
    periods: [
      period({ id: 'jan', period_no: 1, starts_on: '2026-01-01', ends_on: '2026-01-31' }),
      period({ id: 'feb', period_no: 2, starts_on: '2026-02-01', ends_on: '2026-02-28' }),
      period({ id: 'mar', period_no: 3, starts_on: '2026-03-01', ends_on: '2026-03-31' }),
    ],
  },
];

describe('isCompanyField', () => {
  it('knows which two live on the business record', () => {
    // They are still typed on this screen -- the identity form above owns
    // them -- so this says WHICH form, not whether they can be edited.
    expect(isCompanyField('cr_number')).toBe(true);
    expect(isCompanyField('vat_number')).toBe(true);
  });

  it('leaves the disclosure own fields alone', () => {
    expect(isCompanyField('return_policy_ar')).toBe(false);
    expect(isCompanyField('cooling_off_days')).toBe(false);
  });
});

describe('splitMissing', () => {
  it('sends each missing field to the form that actually owns it', () => {
    const { here, identity, fixed } = splitMissing({
      missing: ['cr_number', 'return_policy_ar', 'contact', 'vat_number'],
    });
    expect(here).toEqual(['return_policy_ar', 'contact']);
    // Above, on this same screen -- not "somewhere else". Telling somebody the
    // box does not exist, a few hundred pixels below the box, is worse than
    // saying nothing.
    expect(identity).toEqual(['cr_number', 'vat_number']);
    expect(fixed).toEqual([]);
  });

  it('moves a field to fixed once it settles', () => {
    // vat_number settles when e-invoicing starts, and then the reason is the
    // whole point: it cannot be corrected because invoices are signed under it.
    const { identity, fixed } = splitMissing(
      { missing: ['cr_number', 'vat_number'] },
      { vat_number: 'Invoices have been signed under this number.' },
    );
    expect(identity).toEqual(['cr_number']);
    expect(fixed).toEqual(['vat_number']);
  });

  it('never puts cr_number in fixed, because it does not settle', () => {
    const { identity, fixed } = splitMissing({ missing: ['cr_number'] }, {});
    expect(identity).toEqual(['cr_number']);
    expect(fixed).toEqual([]);
  });

  it('is empty on a compliant storefront', () => {
    expect(splitMissing({ missing: [] })).toEqual({
      here: [],
      identity: [],
      fixed: [],
    });
  });

  it('survives a payload with no list at all', () => {
    expect(splitMissing({} as never)).toEqual({
      here: [],
      identity: [],
      fixed: [],
    });
  });
});

describe('canClose and closeBlock', () => {
  const periods = allPeriods(YEAR);

  it('closes the earliest open period', () => {
    expect(canClose(periods, periods[0]!)).toBe(true);
  });

  it('refuses to close March while February is open', () => {
    // Closing March with February open leaves a hole somebody can still post
    // into, and the trial balance for the closed quarter keeps moving.
    expect(canClose(periods, periods[2]!)).toBe(false);
    expect(closeBlock(periods, periods[2]!)).toBe('earlier_open');
  });

  it('allows March once the two before it are closed', () => {
    const settled = [
      { ...periods[0]!, state: 'closed' },
      { ...periods[1]!, state: 'closed' },
      periods[2]!,
    ];
    expect(canClose(settled, settled[2]!)).toBe(true);
    expect(closeBlock(settled, settled[2]!)).toBe('none');
  });

  it('says a closed period is closed rather than blocked', () => {
    // Two different sentences: "already done" and "do February first".
    const closed = { ...periods[0]!, state: 'closed' };
    expect(closeBlock([closed, ...periods.slice(1)], closed)).toBe(
      'already_closed',
    );
  });

  it('looks across years, not only within one', () => {
    const twoYears = allPeriods([
      { fiscal_year: 2025, periods: [period({ id: 'dec25', fiscal_year: 2025, period_no: 12 })] },
      ...YEAR,
    ]);
    // December 2025 is still open, so nothing in 2026 may close.
    expect(canClose(twoYears, twoYears[1]!)).toBe(false);
  });
});

describe('allPeriods', () => {
  it('runs oldest first across years', () => {
    const rows = allPeriods([
      ...YEAR,
      { fiscal_year: 2025, periods: [period({ id: 'dec25', fiscal_year: 2025, period_no: 12 })] },
    ]);
    expect(rows[0]?.id).toBe('dec25');
    expect(rows.at(-1)?.id).toBe('mar');
  });

  it('is empty for a business with no calendar yet', () => {
    expect(allPeriods([])).toEqual([]);
  });
});

describe('periodOn', () => {
  const periods = allPeriods(YEAR);

  it('finds the period a day falls in', () => {
    expect(periodOn(periods, '2026-02-14')?.id).toBe('feb');
  });

  it('includes the last day of a period', () => {
    // A period runs to the end of its last day.
    expect(periodOn(periods, '2026-01-31')?.id).toBe('jan');
  });

  it('includes the first day', () => {
    expect(periodOn(periods, '2026-03-01')?.id).toBe('mar');
  });

  it('is null outside the calendar', () => {
    expect(periodOn(periods, '2027-06-01')).toBeNull();
  });
});

describe('yearExists', () => {
  it('knows a year already on the calendar', () => {
    expect(yearExists(YEAR, 2026)).toBe(true);
    expect(yearExists(YEAR, 2027)).toBe(false);
  });
});

describe('localDay', () => {
  it('reads the local calendar date, not a UTC timestamp', () => {
    // Nine in the morning in Dhaka is still the previous day in UTC, and a
    // screen that highlighted the wrong month would be wrong in a way nobody
    // notices.
    expect(localDay(new Date(2026, 8, 1, 9, 0, 0))).toBe('2026-09-01');
  });

  it('pads a single-digit month and day', () => {
    expect(localDay(new Date(2026, 0, 5))).toBe('2026-01-05');
  });
});
