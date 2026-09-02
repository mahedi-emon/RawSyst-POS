// A QR drawn as inline SVG.
//
// Inline rather than a canvas or an image: it scales to whatever the layout
// gives it without going soft, it prints, and it needs no ref and no effect —
// the matrix is a pure function of the payload, so this is a pure function of
// its props.
//
// # Black on white, always
//
// Not the theme's colours. A phone camera reading a QR wants maximum contrast
// and expects dark-on-light; a code rendered in a dark theme's foreground on
// its background is one that scans slowly or not at all. The tokens lint
// tolerates this for the same reason the label sheet is exempt: the surface is
// deliberately outside the theme.

import { useMemo } from 'react';

import { encodeQR } from './qr';

export function QRCode({
  value,
  /** Modules of white space around the code. Four is what the standard asks
   *  for, and readers genuinely need it. */
  quiet = 4,
}: {
  value: string;
  quiet?: number;
}) {
  const matrix = useMemo(() => {
    try {
      return encodeQR(value);
    } catch {
      // A payload too long to encode. The screen shows the typed secret
      // beside this, so returning nothing leaves somebody a working path
      // rather than a broken picture.
      return null;
    }
  }, [value]);

  if (!matrix) return null;

  const size = matrix.length + quiet * 2;

  // One path for every dark module, which keeps the DOM to a single element
  // rather than several hundred rects.
  const path = matrix
    .flatMap((row, r) =>
      row.map((dark, c) =>
        dark ? `M${c + quiet} ${r + quiet}h1v1h-1z` : '',
      ),
    )
    .join('');

  return (
    <svg
      className="sec__qr"
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      /* The payload is a secret, so it is not put in the accessible name. A
         screen reader user enrols by typing the secret shown beside this. */
      aria-label="QR code"
      shapeRendering="crispEdges"
    >
      <rect width={size} height={size} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
