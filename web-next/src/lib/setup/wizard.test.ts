import { describe, expect, it } from 'vitest';

import {
  answersFor,
  businessProblem,
  emptyStore,
  isDone,
  isReachable,
  likelyCurrency,
  needsCompany,
  storeProblems,
  storesProblem,
  stepsDone,
  timezonesFor,
  EMPTY_BUSINESS,
  STEPS_TO_DO,
  type BusinessAnswers,
  type Progress,
  type StoreAnswers,
} from './wizard';

const business = (over: Partial<BusinessAnswers> = {}): BusinessAnswers => ({
  ...EMPTY_BUSINESS,
  legal_name: 'Al Noor Trading Company',
  country: 'sa',
  base_currency: 'SAR',
  timezone: 'Asia/Riyadh',
  ...over,
});

const store = (over: Partial<StoreAnswers> = {}): StoreAnswers => ({
  ...emptyStore('SA'),
  code: 'RYD',
  name: 'Riyadh shop',
  street: 'King Fahd Road',
  building_number: '2322',
  district: 'Al Olaya',
  city: 'Riyadh',
  postal_code: '23333',
  ...over,
});

const progress = (over: Partial<Progress> = {}): Progress => ({
  current_step: 'business_info',
  completed_steps: [],
  step_data: {},
  finished: false,
  ...over,
});

describe('the business step', () => {
  it('accepts a complete answer', () => {
    expect(businessProblem(business())).toBeNull();
  });

  it('asks for the registered legal name, which appears on every invoice', () => {
    expect(businessProblem(business({ legal_name: '   ' }))).toBe('no_legal_name');
  });

  it('refuses a country the regulatory register has never heard of', () => {
    // Not a general ISO list: the sale service resolves a VAT rate from
    // `country` at the date of every sale and refuses when it cannot. Accepting
    // a fourth country makes an owner who finishes setup and then cannot ring
    // anything up.
    expect(businessProblem(business({ country: '' }))).toBe('no_country');
    expect(businessProblem(business({ country: 'fr' }))).toBe('unsupported_country');
  });

  it('takes the country however it was typed', () => {
    expect(businessProblem(business({ country: 'SA' }))).toBeNull();
  });

  it('checks the currency separately from the country', () => {
    // A business in one country legitimately keeps its books in another
    // currency, so the two are separate facts and separate checks.
    expect(businessProblem(business({ base_currency: 'USD' }))).toBeNull();
    expect(businessProblem(business({ base_currency: 'EUR' }))).toBe(
      'unsupported_currency',
    );
    expect(businessProblem(business({ base_currency: '' }))).toBe('no_currency');
  });

  it('requires a VAT number from a business that says it is registered', () => {
    expect(businessProblem(business({ vat_registered: true }))).toBe('no_vat_number');
    expect(
      businessProblem(business({ vat_registered: true, vat_number: '310000000000003' })),
    ).toBeNull();
  });

  it('suggests the currency a shop in that country most likely uses', () => {
    expect(likelyCurrency('sa')).toBe('SAR');
    expect(likelyCurrency('bd')).toBe('BDT');
    expect(likelyCurrency('us')).toBe('USD');
    expect(likelyCurrency('fr')).toBe('');
  });

  it('offers the time zones of the market, and UTC', () => {
    expect(timezonesFor('sa')).toEqual(['Asia/Riyadh', 'UTC']);
    expect(timezonesFor('us')).toContain('America/Chicago');
    // A country with none listed still gets somewhere to put the clock.
    expect(timezonesFor('fr')).toEqual(['UTC']);
  });
});

describe('the store step', () => {
  it('accepts a branch with its whole National Address', () => {
    expect(storeProblems(store())).toEqual({});
    expect(storesProblem([store()])).toBeNull();
  });

  it('refuses a shop with no branch, because every sale is recorded against one', () => {
    expect(storesProblem([])).toBe('none_added');
  });

  it('reports every missing field at once rather than one at a time', () => {
    // Six refusals delivered one round trip apart is how somebody abandons a
    // wizard.
    const problems = storeProblems(emptyStore('SA'));
    expect(Object.keys(problems).sort()).toEqual([
      'building_number',
      'city',
      'code',
      'district',
      'name',
      'postal_code',
      'street',
    ]);
  });

  it('holds the building number to four digits and the postal code to five', () => {
    expect(storeProblems(store({ building_number: '232' })).building_number).toBe(
      'four_digits',
    );
    expect(storeProblems(store({ postal_code: '2333' })).postal_code).toBe('five_digits');
  });

  it('accepts an absent additional number and refuses a wrong one', () => {
    expect(storeProblems(store({ additional_number: '' }))).toEqual({});
    expect(storeProblems(store({ additional_number: '12' })).additional_number).toBe(
      'four_digits',
    );
  });

  it('refuses two branches sharing a code', () => {
    // The code appears in every document number, so a duplicate corrupts the
    // numbering rather than merely confusing a list.
    expect(storesProblem([store(), store({ name: 'Second shop' })])).toBe(
      'duplicate_code',
    );
    expect(
      storesProblem([store(), store({ code: 'JED', name: 'Jeddah shop' })]),
    ).toBeNull();
  });

  it('compares codes without caring how they were typed', () => {
    expect(storesProblem([store(), store({ code: 'ryd' })])).toBe('duplicate_code');
  });

  it('reports an incomplete branch even when the codes are fine', () => {
    expect(storesProblem([store({ city: '' })])).toBe('incomplete');
  });
});

describe('reading the progress', () => {
  it('finds the answers already saved for a step', () => {
    const p = progress({ step_data: { business_info: { legal_name: 'Al Noor' } } });
    expect(answersFor<BusinessAnswers>(p, 'business_info')?.legal_name).toBe('Al Noor');
    expect(answersFor(p, 'stores')).toBeNull();
    expect(answersFor(undefined, 'stores')).toBeNull();
  });

  it('survives a tenant whose scratch space is empty', () => {
    expect(answersFor(progress({ step_data: null }), 'business_info')).toBeNull();
  });

  it('opens the current step and every step already done', () => {
    const p = progress({
      current_step: 'tax',
      completed_steps: ['business_info', 'stores'],
    });
    expect(isReachable(p, 'business_info')).toBe(true);
    expect(isReachable(p, 'tax')).toBe(true);
    // Not a jump forward: entering opening balances before the chart of
    // accounts exists fails as a confusing error rather than as a missing
    // prerequisite, and the route refuses it anyway.
    expect(isReachable(p, 'opening_balances')).toBe(false);
    expect(isDone(p, 'stores')).toBe(true);
  });

  it('opens every step once setup is finished, so answers can still be read', () => {
    const p = progress({ finished: true, current_step: 'finished' });
    expect(isReachable(p, 'business_info')).toBe(true);
    expect(isReachable(p, 'opening_balances')).toBe(true);
    // `finished` is a marker, not a step with a form behind it.
    expect(isReachable(p, 'finished')).toBe(false);
  });

  it('counts how far along, and counts a finished setup as all of it', () => {
    expect(stepsDone(progress())).toBe(0);
    expect(
      stepsDone(progress({ completed_steps: ['business_info', 'stores'] })),
    ).toBe(2);
    expect(stepsDone(progress({ finished: true }))).toBe(STEPS_TO_DO);
    expect(STEPS_TO_DO).toBe(6);
  });
});

describe('whether the company still has to be created', () => {
  it('asks how many exist rather than remembering that it called the route', () => {
    // Completing the business step does not create the company; a separate
    // route does, and calling it twice would create a second company or be
    // refused by the plan ceiling.
    expect(needsCompany(0)).toBe(true);
    expect(needsCompany(1)).toBe(false);
  });
});
