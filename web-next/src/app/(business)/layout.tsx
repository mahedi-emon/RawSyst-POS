'use client';

// The business workspace.
//
// Everything a shop does is under here. The navigation is built from what this
// particular person may reach, so a cashier and an owner signing into the same
// business get two different products -- not one product with most of it
// greyed out.

import type { ReactNode } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { AppShell } from '@/components/shell/app-shell';
import { CompanySwitch } from '@/components/shell/company-switch';
import { usePlanFeatures } from '@/lib/api/hooks';
import { useSession } from '@/lib/auth/session';
import { CompanyProvider, useCompany } from '@/lib/company/company-context';
import { BUSINESS_NAV, resolveNavigation } from '@/lib/nav/navigation';

function BusinessShell({ children }: { children: ReactNode }) {
  const { grants } = useSession();
  const features = usePlanFeatures();
  const { company } = useCompany();

  const sections = resolveNavigation(BUSINESS_NAV, grants, features);

  // The trading name if the business has set one, the legal name otherwise. A
  // shop calls itself by its trading name and would not recognise the entity on
  // its VAT certificate at the top of a sidebar.
  const contextName = company?.trade_name || company?.legal_name;

  return (
    <AppShell
      sections={sections}
      workspaceName="RawSyst"
      contextName={contextName}
      headerExtra={<CompanySwitch />}
    >
      {children}
    </AppShell>
  );
}

export default function BusinessLayout({ children }: { children: ReactNode }) {
  return (
    <RequireWorkspace workspace="business">
      <CompanyProvider>
        <BusinessShell>{children}</BusinessShell>
      </CompanyProvider>
    </RequireWorkspace>
  );
}
