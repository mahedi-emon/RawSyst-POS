// The About page's browser title.
//
// A server component that renders nothing but its children, because
// `about/page.tsx` is `'use client'` -- it reads the string catalogue, so it
// can be read in Arabic and Bangla -- and a client component cannot export
// `metadata`. Without this the tab read "Biz1core", the same as the front
// door.
//
// `absolute`, so the root template does not make it "About Biz1core ·
// Biz1core".

import { PRODUCT_NAME } from '@biz1core/shared/brand/brand';
import type { Metadata } from 'next';
import type { ReactNode } from 'react';

export const metadata: Metadata = {
  title: { absolute: `About ${PRODUCT_NAME}` },
};

export default function AboutLayout({ children }: { children: ReactNode }) {
  return children;
}
