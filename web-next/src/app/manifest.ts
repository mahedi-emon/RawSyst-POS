// The installable back office (blueprint A7 and I4).
//
// A7: "the website will mainly be mobile-fast, usable like a phone app" —
// answered by an installable PWA: add to home screen, app-like navigation, and
// the owner's surface on a phone or tablet.
//
// What it is NOT is a till. The design system is explicit that the POS is not
// phone-supported, and E3.4 puts selling from a phone under SoftPOS, which is
// card-acceptance hardware and needs a licensed payment provider. Installing
// this on a phone gives an owner their business, not a checkout.
//
// Carried over from the previous front end, which had this and the icons while
// `web-next` had neither — so the product a customer actually runs had no app
// identity at all: a default globe in the browser tab and nothing to install.

import {
  PRODUCT_DESCRIPTION,
  PRODUCT_NAME,
  PRODUCT_TAGLINE_SHORT,
} from '@biz1core/shared/brand/brand';
import type { MetadataRoute } from 'next';

export default function manifest(): MetadataRoute.Manifest {
  return {
    // The long name is what an install prompt and a store listing show; the
    // short name is what fits under an icon on a home screen, where anything
    // past about twelve characters is replaced with an ellipsis.
    name: `${PRODUCT_NAME} — ${PRODUCT_TAGLINE_SHORT}`,
    short_name: PRODUCT_NAME,
    description: PRODUCT_DESCRIPTION,

    start_url: '/',
    // Standalone rather than fullscreen: an owner checking figures still wants
    // the system clock and the battery, and fullscreen hides both.
    display: 'standalone',

    // The same pair the layout declares, so the installed app does not sit in
    // a different shade from the browser tab.
    background_color: '#FBFBFD',
    theme_color: '#FBFBFD',

    // Portrait is what a phone is held in. Not locked, so a tablet in a stand
    // still works.
    orientation: 'any',

    // Every size an installing browser asks for, all of them the same drawing:
    // the compact Biz1core mark, generated from the traced logo by
    // `scripts/brand-assets.mjs`. A launcher picks the size closest to what it
    // needs, and a set with gaps in it gets a rescaled, softened mark instead.
    icons: [
      ...([72, 96, 128, 144, 152, 192, 256, 384, 512] as const).map((s) => ({
        src: `/icons/icon-${s}.png`,
        sizes: `${s}x${s}`,
        type: 'image/png',
        purpose: 'any' as const,
      })),
      // Inset, so a launcher can crop to a circle or a squircle without
      // clipping the mark. Both sizes, because Android picks the maskable set
      // independently of the `any` set and falls back to a cropped `any` icon
      // when the one it wants is missing.
      {
        src: '/icons/icon-192-maskable.png',
        sizes: '192x192',
        type: 'image/png',
        purpose: 'maskable',
      },
      {
        src: '/icons/icon-512-maskable.png',
        sizes: '512x512',
        type: 'image/png',
        purpose: 'maskable',
      },
    ],
  };
}
