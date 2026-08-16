// The drill-through's own decisions.
//
// Three things here involve judgement rather than layout: which screen a
// server-supplied link means, how an invoice state is put into words, and how a
// quantity is trimmed for reading. Each has a way of going wrong that nobody
// reports as a bug — a dead link, a status that overstates, a quantity that
// loses a genuine fraction — so each is tested.

import { describe, expect, it } from 'vitest';

import { invoiceStateHint } from './InvoiceState';
import { targetForLink, trimQuantity, formatAge } from './drilldown';

describe('mapping a server link to a screen', () => {
  it('opens the compliance queue', () => {
    expect(targetForLink('/compliance')).toEqual({ screen: 'compliance' });
  });

  it('opens stock on the filter the link asked for', () => {
    expect(targetForLink('/inventory?filter=out')).toEqual({
      screen: 'stock',
      filter: 'out',
    });
    expect(targetForLink('/inventory?filter=low')).toEqual({
      screen: 'stock',
      filter: 'low',
    });
  });

  it('defaults an unqualified inventory link to low rather than out', () => {
    // "Low" is the softer claim. Telling an owner something has run out when
    // it has not is the worse of the two mistakes.
    expect(targetForLink('/inventory')).toEqual({ screen: 'stock', filter: 'low' });
  });

  it('returns null for a link it does not recognise', () => {
    // A dead link is worse than no link: the reader learns the list is
    // unreliable and stops trusting the rest of it.
    expect(targetForLink('/suppliers')).toBeNull();
    expect(targetForLink('')).toBeNull();
  });
});

describe('putting an invoice state into words', () => {
  it('never claims an unsubmitted invoice reported', () => {
    // The single most damaging thing this screen could say while the P1 gate
    // is open.
    const pending = invoiceStateHint('signed_pending_report');
    expect(pending).not.toMatch(/accepted/i);
    expect(pending).toMatch(/outstanding/i);
  });

  it('reassures about what is actually fine', () => {
    // The sale, the receipt and the books are all correct; only the reporting
    // is outstanding. Saying so precisely is more reassuring than vagueness.
    expect(invoiceStateHint('signed_pending_report')).toMatch(/recorded|valid/i);
  });

  it('says plainly when ZATCA has accepted something', () => {
    expect(invoiceStateHint('reported')).toMatch(/accepted/i);
    expect(invoiceStateHint('cleared')).toMatch(/accepted/i);
  });

  it('has nothing to say about a state it does not know', () => {
    // Rather than guessing at something reassuring.
    expect(invoiceStateHint('some_new_state')).toBe('');
  });
});

describe('reading a quantity', () => {
  it('drops the trailing zeros a numeric column carries', () => {
    // Stock lists read in whole units; "4.0000" is noise in a column of them.
    expect(trimQuantity('4.0000')).toBe('4');
    expect(trimQuantity('0.0000')).toBe('0');
    expect(trimQuantity('120.00')).toBe('120');
  });

  it('keeps a genuine fraction', () => {
    // Half a metre of fabric is a real quantity, not a rounding artefact.
    expect(trimQuantity('0.5000')).toBe('0.5');
    expect(trimQuantity('2.2500')).toBe('2.25');
  });

  it('keeps a negative, which is a real state where overselling is allowed', () => {
    expect(trimQuantity('-3.0000')).toBe('-3');
  });

  it('leaves an integer alone', () => {
    expect(trimQuantity('7')).toBe('7');
  });
});

describe('how long an invoice has waited', () => {
  it('reads naturally at every scale', () => {
    expect(formatAge(0)).toBe('under an hour');
    expect(formatAge(1)).toBe('1 hour');
    expect(formatAge(5)).toBe('5 hours');
    // Past two days, hours stop being meaningful to a reader.
    expect(formatAge(72)).toBe('3 days');
    expect(formatAge(100)).toBe('4 days');
  });

  it('crosses the escalation thresholds of design 08 §4 readably', () => {
    // Notice past 12, warning past 24, critical past 72.
    expect(formatAge(13)).toBe('13 hours');
    expect(formatAge(25)).toBe('25 hours');
    expect(formatAge(73)).toBe('3 days');
  });
});
