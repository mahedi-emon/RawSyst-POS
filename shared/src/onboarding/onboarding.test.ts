import { describe, expect, it } from 'vitest';

import type { OnboardingProgress, OnboardingStep } from '../api/onboarding';
import {
  answersFor,
  isComplete,
  isReachable,
  outstandingSteps,
  readiness,
  STEPS,
  stepMeta,
  stepNumber,
  taxContextFor,
  emptyStore,
  validateBusiness,
  validateStores,
  validateTax,
  type BusinessInfo,
} from './onboarding';

function progress(over: Partial<OnboardingProgress> = {}): OnboardingProgress {
  return {
    current_step: 'business_info',
    completed_steps: [],
    step_data: {},
    finished: false,
    ...over,
  };
}

function business(over: Partial<BusinessInfo> = {}): BusinessInfo {
  return {
    legal_name: 'Olaya Trading Company',
    legal_name_ar: 'شركة العليا التجارية',
    trade_name: 'Olaya',
    country: 'sa',
    base_currency: 'SAR',
    timezone: 'Asia/Riyadh',
    cr_number: '1010101010',
    vat_registered: true,
    vat_number: '311111111111113',
    ...over,
  };
}

describe('the seven steps', () => {
  it('are blueprint A5’s, in order', () => {
    expect(STEPS.map((s) => s.key)).toEqual([
      'business_info',
      'stores',
      'tax',
      'employees',
      'hardware',
      'opening_balances',
      'finished',
    ]);
  });

  it('marks exactly the three optional ones', () => {
    // The server agrees: validateStep passes employees, hardware and opening
    // balances with nothing in them. A screen that required them would refuse
    // what the API accepts.
    expect(STEPS.filter((s) => s.optional).map((s) => s.key)).toEqual([
      'employees',
      'hardware',
      'opening_balances',
    ]);
  });

  it('numbers steps for the progress line', () => {
    expect(stepNumber('business_info')).toBe(1);
    expect(stepNumber('tax')).toBe(3);
    expect(stepNumber('finished')).toBe(7);
  });

  it('falls back rather than throwing on a step it does not know', () => {
    // The server owns the step list; a step added there before here must not
    // white-screen the wizard.
    expect(stepMeta('nonsense' as OnboardingStep).key).toBe('business_info');
    expect(stepNumber('nonsense' as OnboardingStep)).toBe(1);
  });
});

describe('what an owner may open', () => {
  it('lets them revisit a finished step and stay on the current one', () => {
    const p = progress({ completed_steps: ['business_info'], current_step: 'stores' });
    expect(isReachable(p, 'business_info')).toBe(true);
    expect(isReachable(p, 'stores')).toBe(true);
  });

  it('refuses a step the server has not reached', () => {
    // Jumping ahead would collect answers the server refuses, which reads as
    // the product losing the owner's work.
    const p = progress({ completed_steps: ['business_info'], current_step: 'stores' });
    expect(isReachable(p, 'opening_balances')).toBe(false);
    expect(isReachable(p, 'finished')).toBe(false);
  });

  it('reads back the answers already given, and copes with nothing', () => {
    const p = progress({ step_data: { business_info: { legal_name: 'Olaya' } } });
    expect(answersFor(p, 'business_info')).toEqual({ legal_name: 'Olaya' });
    expect(answersFor(p, 'stores')).toEqual({});
    expect(answersFor(progress({ step_data: null }), 'stores')).toEqual({});
  });

  it('treats a non-object step blob as no answers rather than crashing', () => {
    const p = progress({ step_data: { stores: 'not an object' } as never });
    expect(answersFor(p, 'stores')).toEqual({});
  });

  it('knows which steps are done', () => {
    const p = progress({ completed_steps: ['business_info', 'stores'] });
    expect(isComplete(p, 'stores')).toBe(true);
    expect(isComplete(p, 'tax')).toBe(false);
  });
});

describe('business details', () => {
  it('accepts a complete Saudi business', () => {
    expect(validateBusiness(business())).toEqual({});
  });

  it('requires what the server requires', () => {
    expect(validateBusiness(business({ legal_name: '  ' })).legal_name).toMatch(/legal name/i);
    expect(validateBusiness(business({ country: '' })).country).toMatch(/country/i);
    expect(validateBusiness(business({ base_currency: '' })).base_currency).toMatch(/currency/i);
  });

  it('requires a VAT number once the business says it is registered', () => {
    // Mirrors company_vat_number_required_when_registered, so the owner is
    // told here rather than by a constraint violation after committing.
    const missing = validateBusiness(business({ vat_registered: true, vat_number: '' }));
    expect(missing.vat_number).toMatch(/VAT registration number/);
  });

  it('asks for nothing when the business is not VAT registered', () => {
    expect(validateBusiness(business({ vat_registered: false, vat_number: '' }))).toEqual({});
  });

  it('checks the Saudi VAT format, and only for Saudi', () => {
    // 15 digits starting and ending with 3, as the EGS unit will later demand.
    // Catching it now means a shop is not told at e-invoicing setup that the
    // number they typed at step 1 was never usable.
    expect(validateBusiness(business({ vat_number: '123' })).vat_number).toMatch(/15 digits/);
    expect(validateBusiness(business({ vat_number: '411111111111114' })).vat_number)
      .toMatch(/15 digits/);

    // A US business registered for sales tax is not held to a Saudi format.
    const us = business({ country: 'us', base_currency: 'USD', vat_number: 'EIN-99' });
    expect(validateBusiness(us)).toEqual({});
  });
});

// A National-Address-complete fixture. Tests that mean to exercise the code
// check compose from this so the address check is not the one that fires.
const aStore = {
  ...emptyStore(),
  street: 'Prince Sultan Road',
  building_number: '2322',
  district: 'Al-Murabba',
  city: 'Riyadh',
  postal_code: '23333',
  country_code: 'SA',
};

describe('stores', () => {
  it('requires at least one, because every sale belongs to a store', () => {
    const found = validateStores([]);
    expect(found.form).toMatch(/at least one store/);
  });

  it('requires a name and a code on each', () => {
    const found = validateStores([{ ...emptyStore() }]);
    expect(found.rows[0]?.name).toBeTruthy();
    expect(found.rows[0]?.code).toMatch(/invoice numbers/);
  });

  it('refuses two stores sharing a code, naming the one that has it', () => {
    // Codes identify the store in every document number, so a duplicate would
    // make two branches' invoices indistinguishable.
    const found = validateStores([
      { ...aStore, code: 'RYD', name: 'Olaya' },
      { ...aStore, code: 'ryd', name: 'Malaz' },
    ]);
    expect(found.rows[1]?.code).toMatch(/Store 1 already uses RYD/);
    expect(found.rows[0]).toBeUndefined();
  });

  it('accepts distinct stores', () => {
    const found = validateStores([
      { ...aStore, code: 'RYD', name: 'Olaya' },
      { ...aStore, code: 'JED', name: 'Jeddah' },
    ]);
    expect(found.form).toBeUndefined();
    expect(Object.keys(found.rows)).toHaveLength(0);
  });
});

describe('the ZATCA obligation', () => {
  it('accepts blank, because a taxpayer may not have been notified yet', () => {
    // Blueprint E1.0: the software must never assume or assert a wave.
    expect(validateTax({ zatca_wave: '', zatca_deadline: '' })).toEqual({});
  });

  it('accepts a date as written on a notification', () => {
    expect(validateTax({ zatca_wave: 'Wave 12', zatca_deadline: '2026-01-01' })).toEqual({});
  });

  it('refuses only what is not a date at all', () => {
    // The product has no business telling a shop their deadline looks wrong —
    // only that it could not read it.
    expect(validateTax({ zatca_wave: '', zatca_deadline: 'next spring' }).zatca_deadline)
      .toMatch(/like 2026-01-01/);
  });

  it('applies ZATCA to Saudi businesses only', () => {
    expect(taxContextFor('sa')).toEqual({ fromRegistry: true, zatcaApplies: true, rtl: true });
    expect(taxContextFor('SA').zatcaApplies).toBe(true);
    expect(taxContextFor('bd').zatcaApplies).toBe(false);
    expect(taxContextFor('us').zatcaApplies).toBe(false);
  });
});

describe('how far along setup is', () => {
  it('is in progress until every required step is done', () => {
    expect(readiness(progress())).toBe('incomplete');
    expect(readiness(progress({ completed_steps: ['business_info'] }))).toBe('incomplete');
  });

  it('is ready once the required steps are done, optional or not', () => {
    // Employees, hardware and opening balances do not hold up a business.
    const p = progress({ completed_steps: ['business_info', 'stores', 'tax'] });
    expect(readiness(p)).toBe('ready');
    expect(outstandingSteps(p)).toHaveLength(0);
  });

  it('is done once the server says finished', () => {
    expect(readiness(progress({ finished: true }))).toBe('done');
  });

  it('names what is still outstanding rather than only refusing', () => {
    const p = progress({ completed_steps: ['business_info'] });
    expect(outstandingSteps(p).map((s) => s.key)).toEqual(['stores', 'tax']);
  });

  it('never reports readiness as a ZATCA position', () => {
    // Readiness is about SETUP. A company's ZATCA standing is zatca_status on
    // the company and csid_status on each unit, neither of which this wizard
    // advances — and saying "ready" has never meant a shop may issue a tax
    // invoice.
    const p = progress({ completed_steps: ['business_info', 'stores', 'tax'] });
    expect(readiness(p)).toBe('ready');
    expect(JSON.stringify(STEPS)).not.toMatch(/csid|otp|certificate/i);
  });
});
