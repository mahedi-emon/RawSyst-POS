// The root layout.
//
// # Three scripts, three faces, one design
//
// IBM Plex Sans and IBM Plex Sans Arabic are siblings drawn by the same team,
// so Arabic is not a Latin design with Arabic glyphs dropped into it -- the
// weights, the counters and the rhythm match. Noto Sans Bengali is the pairing
// for Bangla. All three are self-hosted by `next/font`, which is not a
// preference: a shop's till must open on a bad connection, and a face that
// arrives from a CDN or does not arrive at all is a screen that renders in a
// fallback nobody designed for.
//
// A previous version of this product named Inter and JetBrains Mono in its CSS
// and never loaded either, so every screen fell through to Segoe UI. Loading
// the faces here, at the root, is what stops that recurring.
//
// # `lang` and `dir` are set on <html>
//
// Not on a wrapper div. The stylesheets mirror from `dir` alone using logical
// properties, and that only works if `dir` is on an element containing
// everything -- including a dialog portalled to `document.body`, which a
// wrapper would sit outside of.

import type { Metadata, Viewport } from 'next';
import { IBM_Plex_Sans, IBM_Plex_Sans_Arabic, Noto_Sans_Bengali } from 'next/font/google';
import type { ReactNode } from 'react';

import { Providers } from './providers';
import '@/styles/globals.css';

const plexSans = IBM_Plex_Sans({
  subsets: ['latin', 'latin-ext'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-plex-sans',
  display: 'swap',
});

const plexArabic = IBM_Plex_Sans_Arabic({
  subsets: ['arabic'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-plex-arabic',
  display: 'swap',
});

const notoBengali = Noto_Sans_Bengali({
  subsets: ['bengali'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-noto-bengali',
  display: 'swap',
});

export const metadata: Metadata = {
  title: {
    default: 'RawSyst',
    template: '%s · RawSyst',
  },
  description:
    'Run the whole business from one place: selling, stock, buying, money and people.',
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  // Not locked. Pinch-zoom is how somebody reads a figure on a screen they
  // cannot quite see, and taking it away to stop a layout wobbling is a
  // trade nobody should make on a financial product.
  maximumScale: 5,
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#f6f7f6' },
    { media: '(prefers-color-scheme: dark)', color: '#0d1412' },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html
      lang="en"
      dir="ltr"
      // The locale provider rewrites both of these on the client when somebody
      // switches language. English and left-to-right are the defaults for the
      // server render, deliberately: browser sniffing used to start the product
      // in Arabic for most browsers in Saudi Arabia, in a language nobody had
      // chosen, and a first impression in the wrong language reads as a broken
      // install.
      className={`${plexSans.variable} ${plexArabic.variable} ${notoBengali.variable}`}
      suppressHydrationWarning
    >
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
