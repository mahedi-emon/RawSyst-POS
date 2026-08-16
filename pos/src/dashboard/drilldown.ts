// The drill-through's judgement calls, separated from its rendering.
//
// Three things here decide something rather than lay something out, and each
// has a failure mode nobody files a bug for: a dead link teaches the reader
// that the list is unreliable, a mis-trimmed quantity loses a genuine fraction,
// and a mis-worded age misreports how overdue something is. They live here so
// they can be tested.

import type { DrillTarget } from './Dashboard';

/**
 * Maps a server-supplied link onto a screen.
 *
 * The server states facts and returns a path; deciding which screen a path
 * means is the client's job, because the client is what has screens. An
 * unrecognised path yields null and the row renders as plain text rather than
 * as a link that goes nowhere.
 */
export function targetForLink(link: string): DrillTarget | null {
  if (!link) return null;

  if (link.startsWith('/compliance')) return { screen: 'compliance' };

  if (link.startsWith('/inventory')) {
    // Defaults to "low" rather than "out". Telling an owner something has run
    // out when it has not is the worse of the two mistakes: one sends them to
    // reorder unnecessarily, the other has them stop selling something they
    // still have.
    return { screen: 'stock', filter: link.includes('filter=out') ? 'out' : 'low' };
  }

  return null;
}

/**
 * Trims a quantity for reading.
 *
 * Quantities come back at the column's full scale — "4.0000" — and a stock list
 * reading in whole units is far easier to scan. Trailing zeros go; any genuine
 * fraction stays, because half a metre of fabric is a real quantity rather than
 * a rounding artefact.
 */
export function trimQuantity(raw: string): string {
  if (!raw.includes('.')) return raw;
  const trimmed = raw.replace(/0+$/, '').replace(/\.$/, '');
  return trimmed === '' || trimmed === '-' ? '0' : trimmed;
}

/**
 * How long an invoice has waited, in words.
 *
 * Hours up to two days, then days. Past 48 hours the hour count stops meaning
 * anything to a reader — "97 hours" needs arithmetic before it means "four
 * days overdue", and this screen is read by people who are already worried.
 */
export function formatAge(hours: number): string {
  if (hours < 1) return 'under an hour';
  if (hours < 48) return `${hours} hour${hours === 1 ? '' : 's'}`;
  return `${Math.floor(hours / 24)} days`;
}
