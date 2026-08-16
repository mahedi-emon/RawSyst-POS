// The dashboard's decisions, separated from its rendering.
//
// Two things on this screen involve judgement rather than layout: what order
// the attention list appears in, and how a fortnight of takings is scaled into
// 28 pixels. Both are easy to get subtly wrong in ways nobody notices — a chart
// that exaggerates, a list that buries the urgent row — so both live here where
// they can be tested rather than inside a component where they cannot.

import type { Attention } from '../api/dashboard';

const SEVERITY_ORDER: Record<string, number> = {
  critical: 0,
  warning: 1,
  notice: 2,
};

/**
 * Most severe first, and stable within a severity.
 *
 * Compliance outranks convenience because an unreported invoice has a legal
 * deadline attached to it and a low stock level has an inconvenience. The
 * server reports facts and sorts nothing; the priority is a product decision
 * and belongs here.
 *
 * An unrecognised severity sorts last rather than first. A new severity added
 * server-side should not silently jump the queue ahead of a known critical.
 */
export function sortAttention(items: Attention[]): Attention[] {
  return [...items].sort((a, b) => {
    const left = SEVERITY_ORDER[a.severity] ?? 9;
    const right = SEVERITY_ORDER[b.severity] ?? 9;
    return left - right;
  });
}

export interface SparkGeometry {
  path: string;
  lastX: number;
  lastY: number;
  peak: number;
}

/**
 * Scales a series into a path, from a zero baseline.
 *
 * From zero, not from the minimum. A chart whose baseline is the lowest day
 * turns a quiet Tuesday into a cliff — the classic way a chart misleads without
 * containing a single false number, and the reason this is worth a test.
 *
 * Returns null when there is nothing worth drawing: fewer than two points has
 * no line, and an all-zero fortnight would render as a flat rule along the
 * bottom that reads as a broken chart rather than as a quiet fortnight.
 */
export function sparkGeometry(
  values: number[],
  width = 100,
  height = 28,
): SparkGeometry | null {
  if (values.length < 2) return null;

  const peak = Math.max(...values, 0);
  if (peak <= 0) return null;

  const step = width / (values.length - 1);
  const y = (v: number) => height - (Math.max(v, 0) / peak) * height;

  const path = values
    .map((v, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(2)},${y(v).toFixed(2)}`)
    .join(' ');

  const last = values[values.length - 1] ?? 0;
  return { path, lastX: width, lastY: y(last), peak };
}

/**
 * A decimal string to a number, for GEOMETRY only.
 *
 * Safe here and nowhere else on this screen: the result becomes an SVG
 * coordinate accurate to a fraction of a pixel, never a figure anybody reads.
 * Every displayed amount stays a string from the server to the DOM, because
 * float64 cannot hold 0.15.
 */
export function toPlotValue(amount: string): number {
  const n = Number(amount);
  return Number.isFinite(n) ? n : 0;
}
