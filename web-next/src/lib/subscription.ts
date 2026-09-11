// How long a client has left, and how much that matters.
//
// One module because two screens ask the same question — the business list
// colours an expiry column, the billing screen colours a figure — and a rule
// about money and dates that exists twice is a rule that will disagree with
// itself the first time somebody adjusts one threshold.
//
// # What this deliberately does not decide
//
// Whether an expired subscription can still trade. It cannot answer that,
// because nothing in the product enforces expiry yet: `tenant.status` is
// written and read by nothing, and `billing.Allows` ignores the period end. So
// `expired` here means the date has passed and NOT that anybody has been cut
// off, and both screens say so in words next to the figure. A colour that
// implied enforcement would be read as reassurance.

/** The subscription fields these rules need, from either screen's shape. */
export interface Validity {
  /** The last day paid for. Absent on a lifetime plan, and on one whose end was
      never recorded — which `cycle` is here to tell apart. */
  expiresOn?: string;
  cycle?: string;
}

/** Inside this many days, an operator can still do something about it. */
export const EXPIRING_SOON_DAYS = 30;

/**
 * Whole days from today until a date, negative once it is past.
 *
 * A date-only string is parsed as UTC midnight by the language itself, which is
 * what a subscription date means: it runs in whole days and has no time zone.
 * Reading it as local midnight would move every boundary by up to a day, in the
 * direction that expires somebody early.
 */
export function daysUntil(date: string): number {
  const days = Math.round((new Date(date).getTime() - Date.now()) / 86_400_000);
  // `|| 0` because Math.round of a small negative fraction is -0, and a date
  // that ends today would otherwise be reported as "-0 days". Every comparison
  // below treats -0 and 0 alike, so this only affects what a person reads.
  return days || 0;
}

/**
 * How much attention a subscription's end date deserves.
 *
 * `undefined` for a date far enough away to be uninteresting, and for a
 * lifetime plan, which genuinely has no end.
 */
export function expiryTone(v: Validity): 'critical' | 'caution' | undefined {
  if (v.cycle === 'lifetime') return undefined;
  // A cycled plan with no end recorded is a gap somebody should close: nothing
  // can ever find it expired. It is not the same as having no end.
  if (!v.expiresOn) return 'caution';

  const days = daysUntil(v.expiresOn);
  if (days < 0) return 'critical';
  if (days <= EXPIRING_SOON_DAYS) return 'caution';
  return undefined;
}
