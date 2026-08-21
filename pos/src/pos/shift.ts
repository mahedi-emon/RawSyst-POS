// What the shift screens work out for themselves.
//
// Everything with an accounting consequence is the server's: the expected
// drawer, the variance, the session number, whether a second Z is allowed.
// What is left here is the counting a cashier does with notes in their hand,
// and how the result is described to them.
//
// # Arithmetic in minor units
//
// A denomination tally is the one place on this screen that multiplies, and
// `19 * 0.05` is 0.9500000000000001 in float64. Counted in halalas as BigInt it
// is 95, exactly, every time. The total leaves as a decimal string because that
// is what crosses the API boundary; it is never a number in between.

import { major, minor } from '@rawsyst/shared/receivables/receivables';
import type { ShiftReport } from '@rawsyst/shared/api/shift';

// --- denominations --------------------------------------------------------

/** One row of the count: a face value, and how many of them are in the drawer. */
export interface Denomination {
  /** The face value as a decimal string, so it can be shown and totalled
   *  without ever being parsed into a float. */
  value: string;
  /** How it is written on the pad — "500" for a note, "0.25" for a coin. */
  label: string;
  kind: 'note' | 'coin';
}

/**
 * The notes and coins in circulation, per currency.
 *
 * Physical facts about money rather than a business rule, and kept here for the
 * same reason `format.ts` keeps month names and tender names: it is
 * presentation data that would otherwise be spelled differently on every screen
 * that needed it.
 *
 * Only the three currencies this product serves are listed. An unlisted
 * currency is not a failure — the count falls back to entering the total
 * directly, which is what a cashier does anyway when the pad does not match the
 * cash in front of them.
 */
export const DENOMINATIONS: Record<string, Denomination[]> = {
  SAR: [
    { value: '500', label: '500', kind: 'note' },
    { value: '200', label: '200', kind: 'note' },
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'note' },
    { value: '2', label: '2', kind: 'coin' },
    { value: '1', label: '1', kind: 'coin' },
    { value: '0.50', label: '50h', kind: 'coin' },
    { value: '0.25', label: '25h', kind: 'coin' },
    { value: '0.10', label: '10h', kind: 'coin' },
    { value: '0.05', label: '5h', kind: 'coin' },
  ],
  BDT: [
    { value: '1000', label: '1000', kind: 'note' },
    { value: '500', label: '500', kind: 'note' },
    { value: '200', label: '200', kind: 'note' },
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '20', label: '20', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'note' },
    { value: '2', label: '2', kind: 'note' },
    { value: '1', label: '1', kind: 'coin' },
  ],
  USD: [
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '20', label: '20', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'note' },
    { value: '1', label: '1', kind: 'note' },
    { value: '0.25', label: '25¢', kind: 'coin' },
    { value: '0.10', label: '10¢', kind: 'coin' },
    { value: '0.05', label: '5¢', kind: 'coin' },
    { value: '0.01', label: '1¢', kind: 'coin' },
  ],
};

/** The pad for a currency, or null when there is none to offer. */
export function denominationsFor(currency: string | null | undefined): Denomination[] | null {
  if (!currency) return null;
  return DENOMINATIONS[currency.trim().toUpperCase()] ?? null;
}

/** How many of each face value the cashier has counted, keyed by `value`. */
export type Tally = Record<string, number>;

/**
 * The drawer total from a tally, as a decimal string.
 *
 * A blank or malformed count is zero rather than an error: a cashier tabbing
 * down the pad leaves most rows empty, and refusing to total until every row
 * carries a number would make the pad unusable.
 */
export function tallyTotal(tally: Tally, pad: Denomination[]): string {
  let units = 0n;
  for (const d of pad) {
    const count = tally[d.value];
    if (!Number.isFinite(count) || count === undefined || count <= 0) continue;
    // Integer counts only — half a banknote is not a thing, and truncating
    // here keeps the multiplication exact.
    units += minor(d.value) * BigInt(Math.floor(count));
  }
  return major(units);
}

// --- what the count came to ----------------------------------------------

export type Verdict =
  | { kind: 'exact'; word: 'Exact'; amount: string }
  | { kind: 'over'; word: 'Over'; amount: string }
  | { kind: 'short'; word: 'Short'; amount: string }
  /** The expected figure is being withheld, so no verdict can be drawn. This
   *  is the ordinary state of a blind close before the count is committed. */
  | { kind: 'unknown'; word: null; amount: null };

/**
 * Short, Over or Exact — the three words UI spec §7 puts in large type.
 *
 * The amount is always positive; the word carries the direction. "Short 5.00"
 * reads at a glance where "-5.00" invites the reader to work out which way
 * round it is, and this is the number a cashier is about to be asked to explain.
 *
 * Derived from the server's own variance when it has stated one, so the screen
 * and the Z report can never disagree. Before a close, it is worked out from
 * expected against counted — which on a blind till is unavailable by design and
 * correctly yields `unknown`.
 */
export function verdict(
  expected: string | null | undefined,
  counted: string | null | undefined,
): Verdict {
  if (expected === null || expected === undefined || expected === '') {
    return { kind: 'unknown', word: null, amount: null };
  }
  if (counted === null || counted === undefined || counted === '') {
    return { kind: 'unknown', word: null, amount: null };
  }

  const difference = minor(counted) - minor(expected);
  if (difference === 0n) return { kind: 'exact', word: 'Exact', amount: major(0n) };
  if (difference > 0n) return { kind: 'over', word: 'Over', amount: major(difference) };
  return { kind: 'short', word: 'Short', amount: major(-difference) };
}

/** The verdict a closed report states, taken from the server's own variance so
 *  the screen cannot drift from the signed record. */
export function reportVerdict(report: ShiftReport): Verdict {
  if (report.variance === undefined || report.variance === '') {
    return verdict(report.expected_cash, report.counted_cash);
  }
  const difference = minor(report.variance);
  if (difference === 0n) return { kind: 'exact', word: 'Exact', amount: major(0n) };
  if (difference > 0n) return { kind: 'over', word: 'Over', amount: major(difference) };
  return { kind: 'short', word: 'Short', amount: major(-difference) };
}

/**
 * Whether the cashier is being shown the target before they count.
 *
 * Blueprint B7: on a blind close they must not be. The server decides by
 * omitting the field, and this only reports what arrived — a screen that
 * inferred the answer from `blind_close` instead would be guessing at a control
 * the server already enforces, and would be wrong the moment the two disagreed.
 */
export function expectedIsWithheld(report: ShiftReport): boolean {
  return report.expected_cash === undefined || report.expected_cash === '';
}

// --- validation -----------------------------------------------------------

/** A decimal a money field will accept: digits, optionally a point and up to
 *  two more. Deliberately no sign — neither an opening float nor a counted
 *  drawer can be negative, and the server refuses both. */
const MONEY = /^\d+(\.\d{1,2})?$/;

export function validateAmount(raw: string, field: string): string | null {
  const value = raw.trim();
  if (value === '') return `${field} is required.`;
  if (!MONEY.test(value)) return `${field} must be an amount, like 200.00.`;
  return null;
}

/** A cash movement is signed, so this accepts one — but never zero, which the
 *  server refuses as "a cash movement of nothing is not a movement". */
const SIGNED_MONEY = /^-?\d+(\.\d{1,2})?$/;

export function validateMovement(
  amountRaw: string,
  noteRaw: string,
): { amount?: string; note?: string } {
  const errors: { amount?: string; note?: string } = {};

  const amount = amountRaw.trim();
  if (amount === '') {
    errors.amount = 'Say how much moved.';
  } else if (!SIGNED_MONEY.test(amount)) {
    errors.amount = 'Enter an amount, like 100.00.';
  } else if (minor(amount) === 0n) {
    errors.amount = 'A movement of nothing is not a movement.';
  }

  // Mirrors the server's own rule rather than replacing it: an unexplained hand
  // in the till is exactly what the cash movement record exists to make visible.
  if (noteRaw.trim().length < 3) {
    errors.note = 'Say why the money moved. This is what the record is for.';
  }

  return errors;
}

/** How each reason reads on screen. The values are migration 0024's enum and
 *  are not this module's to extend. */
export const MOVEMENT_REASONS: Array<{ value: string; label: string; hint: string }> = [
  { value: 'safe_drop', label: 'Drop to the safe', hint: 'Taking cash out of the drawer' },
  { value: 'float_in', label: 'Float in', hint: 'Adding change to the drawer' },
  { value: 'petty_cash', label: 'Petty cash', hint: 'A small expense paid from the till' },
  { value: 'supplier_paid', label: 'Paid a supplier', hint: 'A delivery settled in cash' },
  { value: 'correction', label: 'Correction', hint: 'Fixing an earlier entry' },
];

/**
 * Which way an amount should be signed for a reason.
 *
 * A cashier types "100" and means a hundred riyals left the drawer; asking them
 * to type "-100" for a safe drop and "100" for a float is how a shift ends up
 * two hundred out. The sign is derived from the reason instead, and the screen
 * says which direction it is applying.
 */
export function signFor(reason: string, amount: string): string {
  const magnitude = minor(amount) < 0n ? -minor(amount) : minor(amount);
  const outward = reason === 'safe_drop' || reason === 'petty_cash' || reason === 'supplier_paid';
  return major(outward ? -magnitude : magnitude);
}

/** The clock time a shift opened, for the status line. Dates are not shown —
 *  a shift that spans midnight is still "the shift that opened at 21:40", and
 *  the session number is the unambiguous handle. */
export function openedAtTime(iso: string): string {
  const match = /T(\d{2}:\d{2})/.exec(iso);
  return match?.[1] ?? '';
}
