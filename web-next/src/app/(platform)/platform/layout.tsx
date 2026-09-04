'use client';

// The platform workspace.
//
// Same website, same sign-in, different job. An operator running the service
// does not switch product or choose a mode: `is_super_admin` on their session
// puts them here, and `RequireWorkspace` sends anybody else back to their shop.
//
// There is no company switch in this header. The platform operator is not
// inside a business and has no books to look at -- and by design, no access to
// any tenant's trading data at all. `TestPlatformAdminHasNoBusinessDataAccess`
// in the Go suite is what keeps that true.

import type { ReactNode } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { AppShell } from '@/components/shell/app-shell';
import { useSession } from '@/lib/auth/session';
import { PLATFORM_NAV, visibleNavigation } from '@/lib/nav/navigation';

function PlatformShell({ children }: { children: ReactNode }) {
  const { grants } = useSession();
  // Same rule as the business shell: only what exists.
  const sections = visibleNavigation(PLATFORM_NAV, grants);

  return (
    <AppShell
      sections={sections}
      workspaceName="RawSyst"
      contextName="Platform operations"
    >
      {children}
    </AppShell>
  );
}

export default function PlatformLayout({ children }: { children: ReactNode }) {
  return (
    <RequireWorkspace workspace="platform">
      <PlatformShell>{children}</PlatformShell>
    </RequireWorkspace>
  );
}
