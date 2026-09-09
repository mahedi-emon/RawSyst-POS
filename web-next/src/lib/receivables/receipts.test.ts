import { describe, expect, it } from 'vitest';

import {
  canReverseReceipt,
  partlySpent,
  receiptBlock,
  receiptState,
  type ListedReceipt,
} from './receipts';

const receipt = (over: Partial<ListedReceipt> = {}): ListedReceipt => ({
  id: 'r1',
  receipt_number: 'RCT-000041',
  customer_id: 'c1',
  customer: 'Al Noor Trading',
  received_on: '2026-08-16',
  method: 'bank_transfer',
  amount: '1150.00',
  unapplied: '1150.00',
  currency: 'SAR',
  reversal: false,
  reversed: false,
  ...over,
});

describe('whether a receipt can still be taken back', () => {
  it('lets a live, un-reversed receipt be reversed', () => {
    expect(canReverseReceipt(receipt())).toBe(true);
    expect(receiptBlock(receipt())).toBeNull();
  });

  it('sends somebody to the original rather than reversing a reversal', () => {
    expect(receiptBlock(receipt({ reversal: true }))).toBe('is_a_reversal');
  });

  it('refuses a second reversal of the same receipt', () => {
    // One reversing document per original, enforced by a unique index.
    expect(receiptBlock(receipt({ reversed: true }))).toBe('already_reversed');
  });
});

describe('how a receipt reads in the ledger', () => {
  it('keeps the three kinds of row apart', () => {
    expect(receiptState(receipt())).toBe('live');
    expect(receiptState(receipt({ reversed: true }))).toBe('reversed');
    expect(receiptState(receipt({ reversal: true }))).toBe('reversal');
  });
});

describe('whether an instalment has already been collected against it', () => {
  it('says so when part of the receipt has been spent', () => {
    // The server allows the reversal and the collection stays, so this is a
    // warning rather than a block — and hiding it would remove the one fact
    // that makes the decision worth pausing over.
    expect(partlySpent(receipt({ unapplied: '400.00' }))).toBe(true);
  });

  it('says nothing about an untouched receipt', () => {
    expect(partlySpent(receipt())).toBe(false);
  });

  it('says nothing about a reversal or a reversed receipt', () => {
    // Neither has an unapplied part — it is not money to spend — so the
    // figures differing there means nothing and would read as a warning.
    expect(partlySpent(receipt({ reversal: true, unapplied: '0.00' }))).toBe(false);
    expect(partlySpent(receipt({ reversed: true, unapplied: '0.00' }))).toBe(false);
  });
});
