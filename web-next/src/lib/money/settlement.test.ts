import { describe, expect, it } from 'vitest';

import {
  bankable,
  depositProblem,
  feeShare,
  health,
  impliedFee,
  FEE_WORTH_QUESTIONING,
  missingFields,
  netOf,
  nextStep,
  publicFields,
  secretField,
  takingCards,
  totalOf,
  type Gateway,
  type PendingTender,
  type DepositDraft,
  type Provider,
} from './settlement';

const gateway = (over: Partial<Gateway> = {}): Gateway => ({
  id: 'g1',
  provider: 'terminal',
  label: 'Counter card machine',
  mode: 'live',
  settings: { address: '192.0.2.7:8080', terminal_id: 'T-01' },
  methods: ['mada', 'visa'],
  is_active: false,
  has_secret: false,
  ...over,
});

describe('whether a card connection works', () => {
  it('does not call a connection broken because nobody has asked it', () => {
    // last_check_ok is absent until a check runs. `!g.last_check_ok` would
    // report a brand new connection as not answering.
    expect(health(gateway())).toBe('never_checked');
    expect(health(gateway({ last_check_ok: true }))).toBe('answering');
    expect(health(gateway({ last_check_ok: false }))).toBe('not_answering');
  });

  it('names the check as the next step rather than offering a switch that refuses', () => {
    // The server will not activate a live connection that has never answered.
    // Offering the toggle anyway teaches the rule by failing.
    expect(nextStep(gateway())).toBe('check');
    expect(nextStep(gateway({ last_check_ok: false }))).toBe('check');
    expect(nextStep(gateway({ last_check_ok: true }))).toBe('activate');
    expect(nextStep(gateway({ last_check_ok: true, is_active: true }))).toBe('ready');
  });

  it('never calls a test connection ready, however healthy', () => {
    // It takes no real money. Calling it ready is how a shop opens believing
    // it can accept cards.
    expect(
      nextStep(gateway({ mode: 'test', last_check_ok: true, is_active: true })),
    ).toBe('testing');
  });

  it('requires all three before saying the shop can take a card', () => {
    // Active and live and answering. Any two is a connection that fails at
    // the counter with a customer waiting.
    expect(takingCards([gateway({ is_active: true, last_check_ok: true })])).toBe(true);
    expect(takingCards([gateway({ is_active: true, last_check_ok: false })])).toBe(false);
    expect(takingCards([gateway({ is_active: false, last_check_ok: true })])).toBe(false);
    expect(
      takingCards([gateway({ mode: 'test', is_active: true, last_check_ok: true })]),
    ).toBe(false);
    expect(takingCards([])).toBe(false);
  });
});

describe('the fields a provider asks for', () => {
  const moyasar: Provider = {
    key: 'moyasar',
    name: 'Moyasar',
    methods: ['mada', 'visa'],
    fields: [
      { key: 'publishable_key', label: 'Publishable key', secret: false },
      { key: 'secret_key', label: 'Secret key', secret: true },
    ],
  };
  const machine: Provider = {
    key: 'terminal',
    name: 'A card machine on the counter',
    methods: ['mada'],
    fields: [
      { key: 'address', label: 'Address on the network', secret: false },
      { key: 'terminal_id', label: 'Terminal ID', secret: false },
    ],
  };

  it('separates the sealed half from what can be shown', () => {
    expect(publicFields(moyasar).map((f) => f.key)).toEqual(['publishable_key']);
    expect(secretField(moyasar)?.key).toBe('secret_key');
    expect(secretField(machine)).toBeNull();
  });

  it('states the refusal before the server gives it', () => {
    expect(missingFields(machine, { address: '10.0.0.4' }, '', true)).toEqual([
      'terminal_id',
    ]);
    expect(
      missingFields(machine, { address: '10.0.0.4', terminal_id: 'T-1' }, '', true),
    ).toEqual([]);
  });

  it('asks for the key on a new connection and not on an edit', () => {
    // A screen that cannot read a key back cannot send it again, so a blank
    // box on an edit means "leave what is there". Demanding it would force
    // somebody to re-enter a key they cannot see to change a label.
    expect(missingFields(moyasar, { publishable_key: 'pk' }, '', true)).toEqual([
      'secret_key',
    ]);
    expect(missingFields(moyasar, { publishable_key: 'pk' }, '', false)).toEqual([]);
  });

  it('does not accept whitespace as a filled-in field', () => {
    expect(missingFields(machine, { address: '  ', terminal_id: 'T-1' }, '', true))
      .toEqual(['address']);
  });
});

describe('money that has to add up', () => {
  it('subtracts the acquirer fee without going near a float', () => {
    expect(netOf('100.00', '2.30')).toBe('97.70');
    expect(netOf('0.30', '0.10')).toBe('0.20');
    // The classic. 0.1 + 0.2 in binary is the reason this is integer maths.
    expect(netOf('0.30', '0.20')).toBe('0.10');
  });

  it('handles a fee larger than the deposit rather than pretending', () => {
    expect(netOf('1.00', '1.50')).toBe('-0.50');
  });

  it('refuses an amount that is not an amount instead of reading it as zero', () => {
    // Number('') is 0. An empty fee silently becoming zero is a deposit that
    // reconciles against the wrong figure and looks correct doing it.
    expect(netOf('100.00', '')).toBe('');
    expect(netOf('', '2.30')).toBe('');
    expect(netOf('100.00', 'free')).toBe('');
  });

  it('adds up what is being banked, and says nothing if a line is unreadable', () => {
    const t = (amount: string, over: Partial<PendingTender> = {}): PendingTender => ({
      tender_id: Math.random().toString(),
      invoice_id: 'i1',
      invoice_number: 'INV-1',
      issued_at: '2026-09-06',
      method: 'card',
      amount,
      currency: 'SAR',
      ...over,
    });
    expect(totalOf([t('10.00'), t('2.50'), t('0.05')])).toBe('12.55');
    // A total that quietly dropped a line is worse than no total.
    expect(totalOf([t('10.00'), t('oops')])).toBe('');
    expect(totalOf([])).toBe('0.00');
  });

  it('leaves out what has already been banked', () => {
    const t = (over: Partial<PendingTender>): PendingTender => ({
      tender_id: Math.random().toString(),
      invoice_id: 'i1',
      invoice_number: 'INV-1',
      issued_at: '2026-09-06',
      method: 'card',
      amount: '10.00',
      currency: 'SAR',
      ...over,
    });
    // already_recorded is omitted rather than false on a fresh tender, so the
    // flag is read truthy-only. Offering a settled one banks it twice.
    expect(bankable([t({}), t({ already_recorded: true })])).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// Recording a deposit
// ---------------------------------------------------------------------------

const tender = (amount: string): PendingTender => ({
  tender_id: `t-${amount}`,
  invoice_id: 'i1',
  invoice_number: 'INV-1',
  issued_at: '2026-08-16T10:00:00Z',
  method: 'mada',
  amount,
  currency: 'SAR',
});

const draft = (over: Partial<DepositDraft> = {}): DepositDraft => ({
  reference: 'MADA-20260817-001',
  depositedOn: '2026-08-17',
  netAmount: '985.00',
  selected: [tender('1000.00')],
  ...over,
});

describe('what stops a deposit being recorded', () => {
  it('accepts the blueprint\u2019s own example', () => {
    // "A customer pays SAR 1,000 by card, but the bank deposits only SAR 985
    // two days later."
    expect(depositProblem(draft())).toBeNull();
  });

  it('names what is missing in the order somebody fixes it', () => {
    expect(depositProblem(draft({ selected: [] }))).toBe('nothing_selected');
    expect(depositProblem(draft({ reference: '  ' }))).toBe('no_reference');
    expect(depositProblem(draft({ depositedOn: '' }))).toBe('no_date');
    expect(depositProblem(draft({ netAmount: '' }))).toBe('no_amount');
    expect(depositProblem(draft({ netAmount: 'nine hundred' }))).toBe('not_a_number');
  });

  it('refuses a deposit larger than the payments it covers', () => {
    // The server's words: an acquirer paying more than was taken is a separate
    // event, not a fee. Learning that from a refusal costs a round trip and
    // reads as the product being broken.
    expect(depositProblem(draft({ netAmount: '1200.00' }))).toBe('net_above_gross');
  });

  it('refuses a deposit of nothing, which is a chargeback', () => {
    expect(depositProblem(draft({ netAmount: '0.00' }))).toBe('net_not_positive');
    expect(depositProblem(draft({ netAmount: '-5.00' }))).toBe('net_not_positive');
  });

  it('lets the whole gross be deposited when the acquirer took no fee', () => {
    expect(depositProblem(draft({ netAmount: '1000.00' }))).toBeNull();
    expect(impliedFee(draft({ netAmount: '1000.00' }))).toBe('0.00');
  });
});

describe('the fee the deposit implies', () => {
  it('is what was taken less what arrived', () => {
    expect(impliedFee(draft())).toBe('15.00');
  });

  it('adds the selection up in minor units rather than through a float', () => {
    const many = draft({
      selected: [tender('0.10'), tender('0.20')],
      netAmount: '0.29',
    });
    expect(impliedFee(many)).toBe('0.01');
  });

  it('reports the share, so twenty per cent is visible before it posts', () => {
    expect(feeShare(draft())).toBeCloseTo(0.015);
    expect(feeShare(draft({ netAmount: '800.00' }))).toBeCloseTo(0.2);
    expect(feeShare(draft({ netAmount: '800.00' }))!).toBeGreaterThan(
      FEE_WORTH_QUESTIONING,
    );
  });

  it('has no share to report when there is nothing to divide by', () => {
    expect(feeShare(draft({ selected: [] }))).toBeNull();
    expect(feeShare(draft({ netAmount: 'x' }))).toBeNull();
  });
});
