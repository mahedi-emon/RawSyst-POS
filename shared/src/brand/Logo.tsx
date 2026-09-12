// The official Biz1core logo.
//
// # One drawing, everywhere
//
// The paths below come from `logo-paths.ts`, which is traced from the supplied
// artwork by `scripts/brand-assets.mjs`. That same generator writes every SVG,
// PNG and ICO under `web-next/public/brand/`. So the navigation rail, the
// sign-in page, the browser tab, the installed app and the Windows installer
// are the SAME logo rather than four similar ones -- which is the thing that
// cannot be achieved by pasting an icon into each place.
//
// # Why the wordmark is outlines and not text
//
// It was live text in a first attempt, and live text is a different logo on a
// machine without the typeface -- and cannot be a favicon or a Windows icon at
// all. As outlines "Biz1core" is the same eight letterforms at 14px in a
// collapsed rail and at 40px on a sign-in page, and the word cannot go missing
// or be misspelled because it is not a string being rendered.
//
// # The variants
//
//	full      leaf + bars + "Biz1core"           headers, rails, sign-in, footers
//	lockup    the above + the tagline            sign-in, About, a landing page
//	wordmark  "Biz1core" with no mark            a tight bar, a print header
//	mark      the B with its leaf and bars       collapsed rail, avatar slot, icons
//
// `mark` is a crop of the official logo -- a letter of the wordmark with the
// growth mark over it -- not a separate symbol. It is what every square icon is
// generated from.
//
// The tagline appears ONLY in `lockup`. It is a nine-word sentence: at rail
// width it wraps to three lines or truncates to "One Solution for Comp...", and
// a tagline nobody can read is worse than no tagline.

import type { CSSProperties } from 'react';

import { PRODUCT_NAME, PRODUCT_TAGLINE } from './brand';
import {
  BAR_RADIUS,
  BARS,
  LEAF,
  LETTER_B,
  METRICS,
  TAGLINE_PATHS,
  WORDMARK,
} from './logo-paths';

export type LogoVariant = 'full' | 'lockup' | 'wordmark' | 'mark';

export type LogoProps = {
  variant?: LogoVariant;
  /**
   * The drawn height in pixels -- the whole lockup for `lockup`, the wordmark's
   * cap-to-descender box for the others, the square for `mark`.
   *
   * One number scales everything, because the whole logo is one drawing in one
   * coordinate space. Nothing can drift out of proportion.
   */
  height?: number;
  /**
   * Set on a surface that is dark regardless of the page theme -- the
   * navigation rail is the case, in both themes. Navy on a dark rail is
   * invisible.
   */
  onDark?: boolean;
  /** Ignores both palettes and draws the whole logo in `currentColor`. */
  mono?: boolean;
  className?: string;
  style?: CSSProperties;
  /** Overrides the accessible name, e.g. where the logo is also the link home. */
  label?: string;
};

/** The ink each variant occupies, in the artwork's coordinate space. */
function boxFor(variant: LogoVariant) {
  switch (variant) {
    case 'mark':
      return METRICS.compactBox;
    case 'wordmark':
      return METRICS.wordBox;
    case 'lockup':
      return {
        x1: Math.min(METRICS.logoBox.x1, METRICS.taglineBox.x1),
        y1: METRICS.logoBox.y1,
        x2: Math.max(METRICS.logoBox.x2, METRICS.taglineBox.x2),
        y2: METRICS.taglineBox.y2,
      };
    default:
      return METRICS.logoBox;
  }
}

/**
 * The compact mark alone.
 *
 * Exported separately because a collapsed rail, a mobile header and a print
 * corner want the square without the word, and going through `<Biz1coreLogo
 * variant="mark">` there would be the same thing with more to read.
 */
export function Biz1coreMark(props: Omit<LogoProps, 'variant'>) {
  return <Biz1coreLogo {...props} variant="mark" />;
}

export function Biz1coreLogo({
  variant = 'full',
  height = 28,
  onDark = false,
  mono = false,
  className,
  style,
  label,
}: LogoProps) {
  const box = boxFor(variant);
  const w = box.x2 - box.x1;
  const h = box.y2 - box.y1;
  const width = Math.round((w / h) * height * 100) / 100;

  // `currentColor` in mono; otherwise the three brand values, which
  // `brand.css` swaps for the dark surface.
  const word = mono ? 'currentColor' : 'var(--biz1core-word)';
  const one = mono ? 'currentColor' : 'var(--biz1core-accent)';
  const leaf = mono ? 'currentColor' : 'var(--biz1core-leaf)';
  const bar = mono ? 'currentColor' : 'var(--biz1core-bar)';
  const tag = mono ? 'currentColor' : 'var(--biz1core-tag)';

  const name = label ?? (variant === 'lockup' ? `${PRODUCT_NAME} — ${PRODUCT_TAGLINE}` : PRODUCT_NAME);

  return (
    <svg
      viewBox={`${box.x1} ${box.y1} ${w} ${h}`}
      width={width}
      height={height}
      className={className}
      style={{ flex: '0 0 auto', display: 'block', ...style }}
      data-biz1core-logo=""
      data-on-dark={onDark ? 'true' : undefined}
      role="img"
      aria-label={name}
    >
      <title>{name}</title>

      {/* The word, then the mark over it -- the order the artwork uses: the
          leaf and the bars sit in front of the B. */}
      {variant === 'mark' ? (
        <path d={LETTER_B} fill={word} />
      ) : (
        WORDMARK.map((g, i) => (
          <path key={i} d={g.d} fill={g.accent ? one : word} />
        ))
      )}

      {variant !== 'wordmark' && (
        <>
          <path d={LEAF} fill={leaf} />
          {BARS.map((b, i) => (
            <rect key={i} x={b.x} y={b.y} width={b.w} height={b.h} rx={BAR_RADIUS} fill={bar} />
          ))}
        </>
      )}

      {variant === 'lockup' &&
        TAGLINE_PATHS.map((d, i) => <path key={i} d={d} fill={tag} />)}
    </svg>
  );
}

/** What a screen reader should call the logo when it is the link home. */
export const LOGO_LABEL = PRODUCT_NAME;
