// The Biz1core logo.
//
// # Why the wordmark is live text and not a picture
//
// A logo that is an image has to be drawn at one weight, one size and one
// colour, and then it is either blurry or heavy everywhere else. The name
// "Biz1core" is set here in the typeface the application already loads, so it
// is vector-crisp at 14px in a collapsed rail and at 40px on a sign-in page,
// it inherits the text colour of whatever surface it sits on -- the light
// page, the dark navigation rail, a printed page, a monochrome fax -- and it
// costs no network request and produces no layout shift.
//
// That also removes the failure the brief warns about: the word cannot be
// missing, distorted or misspelled, because it is the string `PRODUCT_NAME`
// rendered as text.
//
// # The mark
//
// Three ascending bars with a curve rising clear above them: the figures a
// business keeps, and the direction it wants them to go. It is four shapes.
// That is deliberate -- a favicon is 16 CSS pixels across, and anything with
// more detail than this becomes a grey smudge at that size. There is no
// gradient, no shadow and no bevel, for the same reason.
//
// The bars and the curve carry the two brand accents; the word carries
// `currentColor`. So the logo has colour where colour survives (a solid fill
// at 16px) and no colour where it would fight the surface (text on a dark
// rail).
//
// # Variants
//
//	mark      the bars and the curve alone -- a collapsed rail, an avatar slot
//	wordmark  mark + "Biz1core" -- headers, rails, sign-in, footers
//	lockup    mark + "Biz1core" + the tagline -- sign-in, About, a landing page
//
// The tagline is a nine-word sentence. It is NOT offered in the `wordmark`
// variant on purpose: at rail width it would wrap to three lines or truncate
// to "One Solution for Comp...", and a tagline nobody can read is worse than
// no tagline.

import type { CSSProperties } from 'react';

import { PRODUCT_NAME, PRODUCT_TAGLINE } from './brand';

export type LogoVariant = 'mark' | 'wordmark' | 'lockup';

/**
 * How much taller the mark is drawn than the word's em size.
 *
 * Not 1. The mark's ink runs on a diagonal and is mostly empty space, so a
 * mark matched to the cap height of eight bold letters reads as a scribble
 * beside them -- which is exactly how it looked at 1.0 on the sign-in page.
 * A little over a third more height balances the two. The same ratio is used
 * by the standalone SVG files, so the component and the files look like one
 * logo.
 */
const MARK_TO_WORD = 1.375;

export type LogoProps = {
  variant?: LogoVariant;
  /**
   * The logo's nominal height in pixels. Every other dimension is derived from
   * it, so one number scales the whole lockup and the optical relationship
   * between the parts never changes.
   *
   * For `variant="mark"` it is exactly the mark's height. For the two that
   * carry the word it is the word's em size, and the mark is drawn a little
   * larger than that -- see `MARK_TO_WORD`.
   */
  size?: number;
  /**
   * Set on a surface that is dark regardless of the page theme -- the
   * navigation rail is the case. It lightens the two accents, which are picked
   * for contrast against a white page and go muddy on a dark one.
   */
  onDark?: boolean;
  className?: string;
  style?: CSSProperties;
  /**
   * Overrides the accessible name. Only useful where the logo is also the
   * "home" control and the label should say so.
   */
  label?: string;
  /**
   * A second line under the word -- the business's own name, at the top of its
   * own navigation rail.
   *
   * It uses the same slot the tagline uses in the `lockup` variant, so the
   * rail head is the logo with a caption rather than a second lockup built
   * beside it. Set it OR use `variant="lockup"`; the tagline wins if both are
   * given, because a lockup was asked for explicitly.
   */
  sub?: string | null;
};

/**
 * The mark alone.
 *
 * Exported separately because a collapsed rail, a mobile header and a print
 * corner all want the four shapes without the word, and reaching for
 * `<Biz1coreLogo variant="mark" />` in those places would wrap it in a flex
 * row that does nothing.
 */
export function Biz1coreMark({
  size = 24,
  onDark = false,
  className,
  style,
  label,
}: Omit<LogoProps, 'variant'>) {
  return (
    <svg
      viewBox="0 0 32 32"
      width={size}
      height={size}
      className={className}
      style={{ flex: '0 0 auto', ...style }}
      data-biz1core-mark=""
      data-on-dark={onDark ? 'true' : undefined}
      // A mark with no word beside it carries the name; a mark beside the word
      // would read it out twice, and the callers that pass no label are the
      // ones rendering the word themselves.
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      fill="none"
    >
      {/* The figures. Rounded, because a square-cut bar at 16px aliases into a
          different width on every other row of pixels. */}
      <rect x="3.6" y="22" width="5.4" height="5.5" rx="1.6" fill="var(--biz1core-mark-bar)" />
      <rect x="11.3" y="18" width="5.4" height="9.5" rx="1.6" fill="var(--biz1core-mark-bar)" />
      <rect x="19" y="13.5" width="5.4" height="14" rx="1.6" fill="var(--biz1core-mark-bar)" />
      {/* The direction. It clears every bar top by at least two units and runs
          past the tallest one, which is what makes it read as growth rather
          than as a line through a chart. */}
      <path
        d="M3.5 18C9 17 12.5 13 15.5 9.5S23 4 28.5 5"
        stroke="var(--biz1core-mark-curve)"
        strokeWidth="3"
        strokeLinecap="round"
      />
    </svg>
  );
}

/**
 * The logo.
 *
 * Renders as an inline flex row, so it drops into a rail, a header, a sign-in
 * card or a footer without a wrapper. It sets no margin and no padding: the
 * surface it sits on owns its own spacing.
 */
export function Biz1coreLogo({
  variant = 'wordmark',
  size = 24,
  onDark = false,
  className,
  style,
  label,
  sub,
}: LogoProps) {
  if (variant === 'mark') {
    return (
      <Biz1coreMark
        size={size}
        onDark={onDark}
        className={className}
        style={style}
        label={label ?? PRODUCT_NAME}
      />
    );
  }

  const word = Math.round(size * 0.82);
  const mark = Math.round(word * MARK_TO_WORD);
  const tagline = Math.max(11, Math.round(size * 0.42));

  // The tagline if a lockup was asked for, otherwise whatever caption the
  // caller supplied. Nothing, if neither.
  const second = variant === 'lockup' ? PRODUCT_TAGLINE : (sub ?? null);

  return (
    <span
      className={className}
      data-biz1core-logo=""
      data-on-dark={onDark ? 'true' : undefined}
      style={{
        display: 'inline-flex',
        // Centred on the whole block, including a caption or the tagline, so
        // the mark reads as being beside the lockup rather than hung off the
        // top of it. The standalone SVG files centre it the same way.
        alignItems: 'center',
        gap: `${Math.round(size * 0.32)}px`,
        minWidth: 0,
        color: 'inherit',
        ...style,
      }}
    >
      <Biz1coreMark size={mark} onDark={onDark} />
      <span style={{ display: 'block', minWidth: 0 }}>
        <span
          style={{
            display: 'block',
            // Truncation rather than a wrap. The name is one word; a rail too
            // narrow for it should clip it, not stack "Biz" over "1core".
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            fontSize: `${word}px`,
            // 700 rather than 800: the name is eight characters and sits next
            // to a chart mark, and a heavier weight makes the two compete.
            fontWeight: 700,
            // Grotesks set a touch loose at display sizes. Closing it up is
            // what makes eight characters read as one word.
            letterSpacing: '-0.018em',
            lineHeight: 1.05,
            whiteSpace: 'nowrap',
            color: 'currentColor',
          }}
        >
          Biz<span style={{ color: 'var(--biz1core-accent)' }}>1</span>core
        </span>
        {second && (
          <span
            style={{
              display: 'block',
              marginTop: `${Math.max(1, Math.round(size * 0.06))}px`,
              fontSize: `${tagline}px`,
              fontWeight: 500,
              // Opened up, the opposite of the word above it. A small line set
              // loose reads as a subtitle; set tight it reads as a caption
              // somebody forgot to finish.
              letterSpacing: '0.01em',
              lineHeight: 1.35,
              opacity: 0.72,
              color: 'currentColor',
              // A shop's registered name can be long. One line, clipped: the
              // rail head is a fixed 56px and a second line would push the
              // navigation down.
              ...(variant === 'lockup'
                ? null
                : { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
            }}
          >
            {second}
          </span>
        )}
      </span>
    </span>
  );
}

/** What a screen reader should call the logo when it is the link home. */
export const LOGO_LABEL = PRODUCT_NAME;
