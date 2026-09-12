// The control plane's own browser title.
//
// A server component that renders nothing but its children, for one reason:
// `(platform)/platform/layout.tsx` is `'use client'` -- it reads the session
// to build the navigation -- and a client component cannot export `metadata`.
// So every operator screen inherited the business template and a tab read
// "Backups · Biz1core", which is the shop's product name on the vendor's own
// console.
//
// This group sits above it and is a server component, so it can. The result is
// "Backups · Biz1core Console".

import { CONSOLE_NAME } from '@biz1core/shared/brand/brand';
import type { Metadata } from 'next';
import type { ReactNode } from 'react';

export const metadata: Metadata = {
  title: {
    // `absolute`, not `default`. A nested `default` is still run through the
    // PARENT's template, so the operator's own root read "Biz1core Console ·
    // Biz1core" -- verified in a browser, not assumed. `absolute` stops at
    // this segment; `template` still applies to the pages below it.
    absolute: CONSOLE_NAME,
    template: `%s · ${CONSOLE_NAME}`,
  },
};

export default function PlatformGroupLayout({ children }: { children: ReactNode }) {
  return children;
}
