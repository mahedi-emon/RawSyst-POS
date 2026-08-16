// Fourteen days of takings, at a glance.
//
// This exists because a day's sales figure means very little on its own. An
// owner seeing 48,290 cannot tell whether that is a good Tuesday without the
// fortnight around it, and the shape of a fortnight also shows the weekly
// rhythm a single comparison to yesterday hides.
//
// It is drawn as a plain SVG path with no library, no axis, no gridlines and no
// tooltip. A sparkline that needs a legend has stopped being a sparkline; the
// precise numbers live in the tile above it and in the report behind it.

import { money, shortDate } from '../ui/format';
import { sparkGeometry, toPlotValue } from './logic';
import type { TrendPoint } from '../api/dashboard';

/**
 * Renders the trend.
 *
 * Scaled from zero rather than from the minimum. A chart whose baseline is the
 * lowest day exaggerates every wobble into a cliff — the classic way a chart
 * misleads without containing a single false number.
 */
export function Sparkline({ points }: { points: TrendPoint[] }) {
  const geometry = sparkGeometry(points.map((p) => toPlotValue(p.total)));

  // Null covers both "too few points to draw a line" and "a fortnight of
  // zeros". The second matters: a flat rule along the bottom of the box reads
  // as a broken chart rather than as a quiet fortnight.
  if (!geometry) return null;

  const first = points[0];
  const latest = points[points.length - 1];

  return (
    <svg
      className="ds-sparkline"
      viewBox="0 0 100 28"
      preserveAspectRatio="none"
      role="img"
      // Read out rather than drawn for a screen reader: the shape is the point
      // for a sighted reader, and the endpoints are the point for everyone else.
      aria-label={
        first && latest
          ? `Sales from ${shortDate(first.date)} to ${shortDate(latest.date)}, ` +
            `latest ${money(latest.total)}`
          : 'Sales trend'
      }
    >
      <path
        d={geometry.path}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
      />
      {/* The most recent day, marked. It is the one the reader is looking for
          and the only point worth distinguishing. */}
      <circle
        cx={geometry.lastX}
        cy={geometry.lastY}
        r="2"
        fill="currentColor"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}