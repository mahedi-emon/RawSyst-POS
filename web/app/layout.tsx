// The back-office document.
//
// # Why this exists separately from the POS
//
// A till is a fixed appliance on a counter. A back office is a person on
// whatever they happen to have — a laptop at the shop, a phone in a taxi, a
// tablet at home on Sunday evening. Those are different delivery problems even
// though they are the same screens, and the Tauri POS cannot serve the second
// one: it is an installed desktop binary.
//
// So this is a web app, and the SCREENS are shared rather than rewritten. The
// dashboard, the drill-throughs and the buying module all live in
// @rawsyst/shared and are imported by both. Nothing in this directory contains
// business logic, and nothing in it may.
//
// # Direction and language are set on the document
//
// Not on a wrapper div. Setting `dir` on <html> is what makes the browser's own
// text selection, scrollbars and form controls mirror in Arabic, and blueprint
// G3 requires a full mirror rather than translated text in an LTR frame.

import type { Metadata, Viewport } from 'next';

import '@rawsyst/shared/design-system.css';
import '@rawsyst/shared/dashboard/dashboard.css';
import './back-office.css';

export const metadata: Metadata = {
  title: 'RawSyst',
  description: 'Retail operations',
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  // The theme colour follows the system, so the browser chrome does not sit in
  // light grey above a dark page.
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#FBFBFD' },
    { media: '(prefers-color-scheme: dark)', color: '#0E1014' },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" dir="ltr">
      <body>{children}</body>
    </html>
  );
}
