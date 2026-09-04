'use client';

// The point of sale.
//
// Outside the back-office shell on purpose. A till has no sidebar, no
// breadcrumb and no page header: every pixel of chrome is a pixel not showing
// the sale, and a cashier navigating the ERP mid-transaction is a cashier who
// has lost their place. There is one way back, and it is a deliberate act.
//
// It is still the same application, the same sign-in and the same session --
// which is the point of the single website. It simply looks like what it is.

import type { ReactNode } from 'react';

import { RequirePermission, RequireWorkspace } from '@/components/auth/guard';
import { CompanyProvider } from '@/lib/company/company-context';
import { CounterProvider } from '@/lib/pos/counter';

export default function PosLayout({ children }: { children: ReactNode }) {
  return (
    <RequireWorkspace workspace="business">
      {/* `sales.create` is the permission the POS routes themselves require,
          so somebody without it is refused here rather than at the first scan
          with a queue waiting. */}
      <RequirePermission anyOf={['sales.create']}>
        <CompanyProvider>
          <CounterProvider>{children}</CounterProvider>
        </CompanyProvider>
      </RequirePermission>
    </RequireWorkspace>
  );
}
