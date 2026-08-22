// The installable app (blueprint A7 and I4).
//
// A7: "The website will mainly be mobile-fast, usable like a phone app" —
// answered by an installable PWA, add to home screen, app-like navigation,
// offline caching on key screens. Design 00 lists it as the third front end
// beside the Tauri till and the browser back office, and describes it as the
// owner's surface: "live sales monitoring, expense approval, stock check,
// notifications — from phone, tablet, or iPad, anywhere".
//
// What it is NOT is a till. Design system §7 is explicit that the POS is not
// phone-supported, and blueprint E3.4 puts selling from a phone under SoftPOS /
// tap-to-phone, which is card-acceptance hardware and needs a SAMA-licensed
// payment provider. Installing this on a phone gives an owner their dashboard,
// not a checkout.

import type { MetadataRoute } from 'next';

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: 'RawSyst',
    short_name: 'RawSyst',
    description: 'Retail operations — sales, stock, accounting and VAT.',

    start_url: '/',
    // Standalone rather than fullscreen: an owner checking figures still wants
    // the system clock and battery, and fullscreen hides both.
    display: 'standalone',

    // Matches the theme-colour pair the layout already declares, so the
    // installed app does not sit in a different shade from the browser tab.
    background_color: '#FBFBFD',
    theme_color: '#FBFBFD',

    // Portrait is what a phone is held in, and the back office is now laid out
    // for it. Not locked, so a tablet in a stand still works.
    orientation: 'any',

    icons: [
      {
        src: '/icons/icon-192.png',
        sizes: '192x192',
        type: 'image/png',
        purpose: 'any',
      },
      {
        src: '/icons/icon-512.png',
        sizes: '512x512',
        type: 'image/png',
        purpose: 'any',
      },
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
