'use client';

// The three screens somebody reaches before they are inside a workspace:
// signing in, asking for a recovery code, and choosing a password after a
// one-time one.
//
// # Why this layout exists
//
// The shell was pasted into two of the three -- the dark ground, the 26rem
// column, the logo lockup, the "Built by" line -- and the third had none of
// it. `change-password` rendered a bare `<form>` with no page chrome at all:
// no ground, no column, no logo. It is reached immediately after a first
// sign-in, so the first screen of somebody's first session was the one screen
// that did not look like the product.
//
// One layout, three pages that are now only their card. The logo cannot be on
// two of three, and cannot drift in size between them, because there is one
// copy of it.
//
// # The lockup, with the tagline
//
// This is the one place in the product with room for nine words and the one
// place where somebody may not yet know what they are signing in to. Every
// other surface takes a variant without it.

import type { ReactNode } from 'react';

import { AuthBrand, BuiltBy } from '@/components/shell/product-brand';

export default function AuthLayout({ children }: { children: ReactNode }) {
  return (
    // The dark chrome colour rather than a separate marketing surface, so the
    // first thing somebody sees is the colour they will navigate by all day.
    <main className="grid min-h-dvh place-items-center bg-shell px-4 py-10">
      <div className="w-full max-w-[26rem]">
        <AuthBrand />
        {children}
        {/* One line, under the card. The brief allows the developer's name on
            the sign-in page where it fits the design, and this is where it
            fits: these screens have nothing else below the fold, and somebody
            signing in for the first time is the one person with a reason to
            know who stands behind the product.

            It appears nowhere inside the workspace. A shopkeeper working in
            their own software all day does not need the vendor's name under
            every screen. */}
        <div className="mt-6 text-center">
          <BuiltBy className="text-shell-fg" />
        </div>
      </div>
    </main>
  );
}
