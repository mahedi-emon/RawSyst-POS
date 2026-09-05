import { describe, expect, it } from 'vitest';

import {
  balanceOf,
  blankLine,
  isBlank,
  lineProblem,
  postableAccounts,
  postableLines,
  readiness,
  type Account,
  type DraftLine,
} from './journals';

function line(over: Partial<DraftLine> = {}): DraftLine {
  return { ...blankLine(), ...over };
}

function account(over: Partial<Account> = {}): Account {
  return {
    id: 'a1',
    code: '1100',
    name: 'Cash',
    type: 'asset',
    is_postable: true,
    is_control: false,
    is_active: true,
    balance: '0.00',
    ...over,
  };
}

describe('whether the entry balances', () => {
  it('reports the difference, not the two totals', () => {
    // The number a person has to close is the difference. Two totals leave
    // them doing the subtraction the server has already done.
    const b = balanceOf([
      line({ accountID: 'a1', debit: '100.00' }),
      line({ accountID: 'a2', credit: '60.00' }),
    ]);
    expect(b.debits).toBe('100.00');
    expect(b.credits).toBe('60.00');
    expect(b.difference).toBe('40.00');
    expect(b.balanced).toBe(false);
  });

  it('balances when the two sides are equal', () => {
    const b = balanceOf([
      line({ accountID: 'a1', debit: '100.00' }),
      line({ accountID: 'a2', credit: '100.00' }),
    ]);
    expect(b.difference).toBe('0.00');
    expect(b.balanced).toBe(true);
  });

  it('signs the difference, so which side is short is visible', () => {
    const b = balanceOf([
      line({ accountID: 'a1', debit: '60.00' }),
      line({ accountID: 'a2', credit: '100.00' }),
    ]);
    expect(b.difference).toBe('-40.00');
  });

  it('does not call an empty entry balanced', () => {
    // Zero equals zero, and a journal of nothing changes nothing.
    expect(balanceOf([blankLine(), blankLine()]).balanced).toBe(false);
  });

  it('keeps money as strings and never as numbers', () => {
    const b = balanceOf([
      line({ accountID: 'a1', debit: '0.10' }),
      line({ accountID: 'a2', debit: '0.20' }),
      line({ accountID: 'a3', credit: '0.30' }),
    ]);
    expect(b.debits).toBe('0.30');
    expect(b.balanced).toBe(true);
  });

  it('ignores an amount somebody is half-way through typing', () => {
    // decimal.js parses "1." as 1, so the difference would flicker while
    // somebody typed "1.5" -- on the one figure they are trying to close.
    expect(balanceOf([line({ accountID: 'a1', debit: '1.' })]).debits).toBe('0.00');
    expect(balanceOf([line({ accountID: 'a1', debit: '-' })]).debits).toBe('0.00');
  });

  it('counts the lines that are actually usable', () => {
    const b = balanceOf([
      line({ accountID: 'a1', debit: '10.00' }),
      line({ accountID: '', debit: '10.00' }),
      line({ accountID: 'a3', debit: '5.00', credit: '5.00' }),
      line({ accountID: 'a4', credit: '10.00' }),
    ]);
    expect(b.usable).toBe(2);
  });
});

describe('what is wrong with one line', () => {
  it('says nothing about a line nobody has touched', () => {
    expect(lineProblem(blankLine())).toBe('none');
  });

  it('wants an account', () => {
    expect(lineProblem(line({ debit: '10.00' }))).toBe('no_account');
  });

  it('wants an amount', () => {
    expect(lineProblem(line({ accountID: 'a1' }))).toBe('no_amount');
  });

  it('refuses both sides at once', () => {
    // "must have either a debit or a credit, and not both" -- both is
    // ambiguous and neither is empty.
    expect(lineProblem(line({ accountID: 'a1', debit: '10', credit: '10' }))).toBe(
      'both_sides',
    );
  });

  it('refuses a negative, because a negative debit is a credit', () => {
    expect(lineProblem(line({ accountID: 'a1', debit: '-10' }))).toBe('negative');
  });

  it('accepts one side with an account on it', () => {
    expect(lineProblem(line({ accountID: 'a1', credit: '10.00' }))).toBe('none');
  });
});

describe('what gets sent', () => {
  it('drops blank rows rather than sending them', () => {
    expect(
      postableLines([
        line({ accountID: 'a1', debit: '10.00' }),
        blankLine(),
        line({ accountID: 'a2', credit: '10.00' }),
      ]),
    ).toEqual([
      { account_id: 'a1', debit: '10.00' },
      { account_id: 'a2', credit: '10.00' },
    ]);
  });

  it('sends only the side that carries a figure', () => {
    // Sending "0" on the other side is sending both, which the server refuses
    // as ambiguous.
    const [only] = postableLines([line({ accountID: 'a1', debit: '10.00' })]);
    expect(only).not.toHaveProperty('credit');
  });

  it('carries a line memo when there is one', () => {
    const [withMemo] = postableLines([
      line({ accountID: 'a1', debit: '10.00', memo: 'Rent accrual' }),
    ]);
    expect(withMemo).toMatchObject({ memo: 'Rent accrual' });
  });

  it('knows a blank row from a touched one', () => {
    expect(isBlank(blankLine())).toBe(true);
    expect(isBlank(line({ memo: 'x' }))).toBe(false);
  });
});

describe('whether it can be posted', () => {
  const good = [
    line({ accountID: 'a1', debit: '100.00' }),
    line({ accountID: 'a2', credit: '100.00' }),
  ];

  it('is ready with a reason and a balanced pair', () => {
    expect(readiness(good, 'Accrue the rent')).toEqual({ ok: true });
  });

  it('insists on a reason, because the ledger is read a year later', () => {
    // "C10 requires a reason on every manual journal. It is what somebody
    // reading the ledger a year from now has to go on."
    expect(readiness(good, '  ')).toEqual({ ok: false, reason: 'no_reason' });
  });

  it('wants two lines', () => {
    expect(readiness([good[0] as DraftLine], 'Accrue')).toEqual({
      ok: false,
      reason: 'too_few',
    });
  });

  it('refuses while a line is wrong', () => {
    expect(
      readiness(
        [line({ accountID: 'a1', debit: '100.00' }), line({ debit: '100.00' })],
        'Accrue',
      ),
    ).toEqual({ ok: false, reason: 'line_problem' });
  });

  it('refuses while it does not balance', () => {
    expect(
      readiness(
        [
          line({ accountID: 'a1', debit: '100.00' }),
          line({ accountID: 'a2', credit: '60.00' }),
        ],
        'Accrue',
      ),
    ).toEqual({ ok: false, reason: 'unbalanced' });
  });

  it('ignores blank rows when counting', () => {
    expect(readiness([...good, blankLine(), blankLine()], 'Accrue')).toEqual({
      ok: true,
    });
  });
});

describe('which accounts a line may name', () => {
  it('leaves out a header, which holds nothing', () => {
    // "Posting to a header is how a chart of accounts silently stops adding
    // up." Offering one is offering that mistake.
    const chart = [
      account({ id: 'h', code: '5000', name: 'Operating Expenses', is_postable: false }),
      account({ id: 'p', code: '5100', name: 'Cost of Goods Sold' }),
    ];
    expect(postableAccounts(chart).map((a) => a.id)).toEqual(['p']);
  });

  it('leaves out a retired account', () => {
    const chart = [account(), account({ id: 'old', is_active: false })];
    expect(postableAccounts(chart).map((a) => a.id)).toEqual(['a1']);
  });
});
