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

import type { MetadataRoute } from 'next';

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: 'RawSyst',
    short_name: 'RawSyst',
    description: 'Run the whole business from one place: selling, stock, buying, money and people.',

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

    icons: [
      { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
      { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
      // Inset, so a launcher can crop to a circle or a squircle without
      // clipping the mark.
      {
        src: '/icons/icon-512-maskable.png',
        sizes: '512x512',
        type: 'image/png',
        purpose: 'maskable',
      },
    ],
  };
}
