import { describe, expect, it } from 'vitest';

import {
  addsUp,
  closesAt,
  daysApart,
  isSignedOff,
  likelyMatches,
  parseStatement,
  totalOf,
  unmatchedLines,
  type LedgerLine,
  type StatementLine,
} from './statement';

describe('reading a pasted bank statement', () => {
  it('reads the documented four columns', () => {
    const { lines, problems } = parseStatement(
      '2026-08-05,Card settlement,MADA-001,1250.00\n' +
        '2026-08-06,Bank charge,CHG-9,-25.00',
    );
    expect(problems).toEqual([]);
    expect(lines).toEqual([
      {
        value_date: '2026-08-05',
        description: 'Card settlement',
        reference: 'MADA-001',
        amount: '1250.00',
      },
      {
        value_date: '2026-08-06',
        description: 'Bank charge',
        reference: 'CHG-9',
        amount: '-25.00',
      },
    ]);
  });

  it('reads three columns, because a reference is optional', () => {
    const { lines, problems } = parseStatement('2026-08-05,Deposit,60.00');
    expect(problems).toEqual([]);
    expect(lines[0]).toMatchObject({ description: 'Deposit', reference: '', amount: '60.00' });
  });

  it('prefers tabs, because that is what a spreadsheet pastes', () => {
    // A description with a comma in it -- "Rent, August" -- splits into two
    // fields on a comma and into one on a tab. The paste from a spreadsheet is
    // tab-separated, so tabs win when both are present.
    const { lines, problems } = parseStatement('2026-08-05\tRent, August\tREF-1\t-4000.00');
    expect(problems).toEqual([]);
    expect(lines[0]?.description).toBe('Rent, August');
  });

  it('strips the quotes a CSV puts round a field', () => {
    const { lines } = parseStatement('"2026-08-05","Deposit","REF-1","60.00"');
    expect(lines[0]).toMatchObject({ value_date: '2026-08-05', amount: '60.00' });
  });

  it('ignores the blank row a spreadsheet ends with', () => {
    const { lines, problems } = parseStatement('2026-08-05,Deposit,60.00\n\n');
    expect(lines).toHaveLength(1);
    expect(problems).toEqual([]);
  });

  it('refuses a date that is not ISO rather than guessing', () => {
    // 03/04/2026 is April in Dhaka and March in California. Picking one would
    // file the line in the wrong month for half the world, silently.
    const { lines, problems } = parseStatement('03/04/2026,Deposit,60.00');
    expect(lines).toEqual([]);
    expect(problems).toEqual([
      { row: 1, key: 'nx.rec.rowBadDate', text: '03/04/2026' },
    ]);
  });

  it('names the row that is wrong rather than refusing the paste', () => {
    // Two hundred rows with one bad date is one correction, not a retype.
    const { lines, problems } = parseStatement(
      '2026-08-05,Deposit,60.00\n' +
        'nonsense\n' +
        '2026-08-07,Charge,-5.00\n' +
        '2026-08-08,Broken,not-a-number',
    );
    expect(lines).toHaveLength(2);
    expect(problems.map((p) => [p.row, p.key])).toEqual([
      [2, 'nx.rec.rowTooFew'],
      [4, 'nx.rec.rowBadAmount'],
    ]);
  });

  it('refuses a zero line, which the column refuses too', () => {
    const { problems } = parseStatement('2026-08-05,Nothing happened,0.00');
    expect(problems[0]?.key).toBe('nx.rec.rowZero');
  });

  it('refuses a line with no description', () => {
    const { problems } = parseStatement('2026-08-05,,60.00');
    expect(problems[0]?.key).toBe('nx.rec.rowNoDescription');
  });

  it('keeps the amount as a string and never as a number', () => {
    // 0.1 + 0.2 is not 0.3 in binary floating point, and this is a figure
    // somebody will reconcile against a bank.
    const { lines } = parseStatement(
      '2026-08-05,A,0.10\n2026-08-06,B,0.20',
    );
    expect(totalOf(lines)).toBe('0.30');
  });

  it('reads a thousands separator, because banks print one', () => {
    const { lines, problems } = parseStatement('2026-08-05\tDeposit\t"12,500.00"');
    expect(problems).toEqual([]);
    expect(lines[0]?.amount).toBe('12500.00');
  });
});

describe('whether the statement adds up before it is sent', () => {
  const lines = parseStatement(
    '2026-08-05,In,100.00\n2026-08-06,Out,-25.50',
  ).lines;

  it('says where the opening balance plus the lines lands', () => {
    expect(closesAt('1000.00', lines)).toBe('1074.50');
  });

  it('agrees with a closing balance that matches', () => {
    expect(addsUp('1000.00', lines, '1074.50')).toBe(true);
  });

  it('refuses one that does not, which is a truncated paste', () => {
    expect(addsUp('1000.00', lines, '1200.00')).toBe(false);
  });

  it('is not satisfied by no lines at all', () => {
    // "A statement with no lines on it proves nothing."
    expect(addsUp('0.00', [], '0.00')).toBe(false);
  });

  it('treats a half-typed closing balance as not yet an answer', () => {
    // Somebody typing "-" or "1074." must not be told their statement is
    // wrong between two keystrokes.
    expect(addsUp('1000.00', lines, '1074.')).toBe(false);
    expect(addsUp('1000.00', lines, '-')).toBe(false);
  });
});

describe('what is left to explain', () => {
  const lines: StatementLine[] = [
    { id: 'a', value_date: '2026-08-05', description: 'Deposit', amount: '60.00', matched_to: '104' },
    { id: 'b', value_date: '2026-08-06', description: 'Bank charge', amount: '-25.00' },
  ];

  it('lists the bank lines nobody has claimed', () => {
    expect(unmatchedLines({ lines }).map((l) => l.id)).toEqual(['b']);
  });

  it('separates being signed off from currently balancing', () => {
    // Two different questions. A statement signed off in March is still
    // signed off in April, and its difference is recomputed from today's
    // books — so the badge and the figure come from different fields.
    expect(isSignedOff({ status: 'reconciled' })).toBe(true);
    expect(isSignedOff({ status: 'draft' })).toBe(false);
  });
});

describe('suggesting a ledger line for a bank line', () => {
  const charge: StatementLine = {
    id: 'b',
    value_date: '2026-08-06',
    description: 'Bank charge',
    amount: '-25.00',
  };
  const ledger: LedgerLine[] = [
    { id: '1', entry_date: '2026-08-07', entry_no: '104', amount: '-25.00' },
    { id: '2', entry_date: '2026-08-20', entry_no: '111', amount: '-25.00' },
    { id: '3', entry_date: '2026-08-06', entry_no: '112', amount: '-30.00' },
  ];

  it('offers the exact amount within three days', () => {
    expect(likelyMatches(charge, ledger).map((l) => l.id)).toEqual(['1']);
  });

  it('leaves out the identical amount a fortnight later', () => {
    // The reason the window is narrow: a looser rule pairs a payment with the
    // following month's identical one, and somebody has to undo it.
    expect(likelyMatches(charge, ledger).map((l) => l.id)).not.toContain('2');
  });

  it('counts days without a timezone deciding the answer', () => {
    expect(daysApart('2026-08-06', '2026-08-09')).toBe(3);
    expect(daysApart('2026-08-09', '2026-08-06')).toBe(3);
    expect(daysApart('2026-08-06', '2026-08-06')).toBe(0);
  });

  it('suggests nothing rather than everything when a date is unreadable', () => {
    expect(daysApart('nonsense', '2026-08-06')).toBe(Number.POSITIVE_INFINITY);
    expect(likelyMatches({ ...charge, value_date: 'nonsense' }, ledger)).toEqual([]);
  });
});
