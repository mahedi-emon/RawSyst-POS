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

import {
  PRODUCT_DESCRIPTION,
  PRODUCT_NAME,
  PRODUCT_TAGLINE,
  PRODUCT_TITLE,
} from '@biz1core/shared/brand/brand';
import type { Metadata, Viewport } from 'next';
import { IBM_Plex_Sans, IBM_Plex_Sans_Arabic, Noto_Sans_Bengali } from 'next/font/google';
import type { ReactNode } from 'react';

import { Providers } from './providers';
import '@/styles/globals.css';
// The two accents the logo is drawn in. Loaded at the root rather than beside
// the component, because the mark appears on the sign-in page, in the rail, in
// the footer and on the error screens -- and a brand colour that arrives with
// the third of those flashes on the first two.
import '@biz1core/shared/brand/brand.css';

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
    // The bare name on the landing page. A tab reading
    // "Biz1core | Complete Business Management" next to nine other tabs is
    // seven words of which one identifies it, so the qualifier is kept for
    // the places that are read cold -- a search result, a shared link, a
    // bookmark -- and left out of the tab.
    default: PRODUCT_NAME,
    template: `%s · ${PRODUCT_NAME}`,
  },
  description: PRODUCT_DESCRIPTION,
  applicationName: PRODUCT_NAME,

  // The browser tab and the home screen. Without these the product a customer
  // runs showed a default globe in the tab and could not be installed at all,
  // while the front end it replaced had both.
  //
  // The SVG is listed first and is what a current browser uses: one file, crisp
  // at every size, correct on a high-density display. The PNGs stay for Safari
  // and for anything that asks for a raster.
  icons: {
    icon: [
      { url: '/brand/app-icon.svg', type: 'image/svg+xml' },
      { url: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
      { url: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
    ],
    shortcut: '/favicon.ico',
    apple: '/icons/icon-192.png',
  },
  manifest: '/manifest.webmanifest',
  appleWebApp: { capable: true, title: PRODUCT_NAME, statusBarStyle: 'default' },

  // What a link to this product looks like when somebody pastes it into a chat
  // or a ticket. Without these it is a bare URL, which is how an internal tool
  // looks rather than a product.
  //
  // No absolute image URL is named: this deployment does not know its own
  // public address at build time (that is `BIZ1CORE_APP_URL`, read at run
  // time), and a hard-coded domain here would be a broken preview on every
  // deployment but one.
  openGraph: {
    type: 'website',
    siteName: PRODUCT_NAME,
    title: PRODUCT_TITLE,
    description: `${PRODUCT_TAGLINE} ${PRODUCT_DESCRIPTION}`,
  },
  twitter: {
    card: 'summary',
    title: PRODUCT_TITLE,
    description: PRODUCT_TAGLINE,
  },
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
