// The dashboard's two judgement calls.
//
// A list that buries the urgent row and a chart that exaggerates are both
// failures nobody reports as bugs — they simply mislead. So both are tested.

import { describe, expect, it } from 'vitest';

import { sortAttention, sparkGeometry, toPlotValue } from './logic';
import type { Attention } from '../api/dashboard';

function item(severity: string, title = severity): Attention {
  return {
    severity: severity as Attention['severity'],
    kind: title,
    title,
    detail: '',
    count: 1,
    link: '',
  };
}

describe('ordering what needs attention', () => {
  it('puts compliance above convenience', () => {
    // An unreported invoice has a legal deadline; a low stock level has an
    // inconvenience. Showing them in arrival order would bury the first.
    const sorted = sortAttention([
      item('notice', 'low stock'),
      item('critical', 'unreported invoices'),
      item('warning', 'out of stock'),
    ]);

    expect(sorted.map((i) => i.title)).toEqual([
      'unreported invoices',
      'out of stock',
      'low stock',
    ]);
  });

  it('is stable within a severity', () => {
    // Two equally urgent rows must not reshuffle between refreshes; an owner
    // glancing twice should see the same list.
    const sorted = sortAttention([
      item('warning', 'first'),
      item('warning', 'second'),
      item('warning', 'third'),
    ]);

    expect(sorted.map((i) => i.title)).toEqual(['first', 'second', 'third']);
  });

  it('sorts an unknown severity last, never first', () => {
    // A severity added server-side must not silently jump ahead of a known
    // critical simply because this build has not been taught it.
    const sorted = sortAttention([
      item('catastrophic', 'unknown'),
      item('critical', 'known'),
    ]);

    expect(sorted[0]?.title).toBe('known');
  });

  it('does not mutate what it was given', () => {
    const original = [item('notice'), item('critical')];
    const copy = [...original];
    sortAttention(original);
    expect(original).toEqual(copy);
  });

  it('handles an empty list', () => {
    expect(sortAttention([])).toEqual([]);
  });
});

describe('scaling the sparkline', () => {
  it('measures from zero, not from the lowest day', () => {
    // The classic way a chart misleads without containing a false number: a
    // baseline at the minimum turns a 5% dip into a cliff.
    const g = sparkGeometry([100, 95, 100], 100, 28);

    expect(g).not.toBeNull();
    // 95 against a peak of 100 sits near the top, not at the bottom.
    expect(g!.path).toContain('50.00,1.40');
    // And the low point is nowhere near the floor.
    expect(g!.path).not.toContain(',28.00');
  });

  it('puts a zero day on the floor and the peak at the top', () => {
    const g = sparkGeometry([0, 50, 100], 100, 28);

    expect(g!.path).toBe('M0.00,28.00 L50.00,14.00 L100.00,0.00');
    expect(g!.peak).toBe(100);
  });

  it('marks the most recent point', () => {
    const g = sparkGeometry([10, 20, 5], 100, 28);

    expect(g!.lastX).toBe(100);
    // 5 of a peak of 20 is a quarter up from the floor.
    expect(g!.lastY).toBeCloseTo(21, 5);
  });

  it('draws nothing rather than a misleading flat line', () => {
    // An all-zero fortnight rendered as a rule along the bottom reads as a
    // broken chart, not as a quiet fortnight.
    expect(sparkGeometry([0, 0, 0])).toBeNull();
    // And a single point has no line to draw.
    expect(sparkGeometry([100])).toBeNull();
    expect(sparkGeometry([])).toBeNull();
  });

  it('never plots above the box on a negative value', () => {
    // A credit-note-heavy day can push a total negative. It clamps to the
    // floor rather than escaping the viewBox.
    const g = sparkGeometry([-50, 100], 100, 28);
    expect(g!.path).toBe('M0.00,28.00 L100.00,0.00');
  });

  it('spaces fourteen days evenly across the width', () => {
    const values = Array.from({ length: 14 }, (_, i) => i + 1);
    const g = sparkGeometry(values, 100, 28);

    const xs = g!.path.split(' ').map((p) => Number(p.slice(1).split(',')[0]));
    expect(xs[0]).toBe(0);
    expect(xs[13]).toBe(100);
    // Evenly spaced, so a fortnight does not bunch at one end.
    expect(xs[1]! - xs[0]!).toBeCloseTo(xs[13]! - xs[12]!, 5);
  });
});

describe('converting an amount for plotting', () => {
  it('reads a decimal string', () => {
    expect(toPlotValue('1234.56')).toBeCloseTo(1234.56, 2);
    expect(toPlotValue('0')).toBe(0);
    expect(toPlotValue('-45.00')).toBe(-45);
  });

  it('treats anything unreadable as zero rather than NaN', () => {
    // NaN in an SVG path silently blanks the whole chart.
    expect(toPlotValue('')).toBe(0);
    expect(toPlotValue('not a number')).toBe(0);
  });
});
