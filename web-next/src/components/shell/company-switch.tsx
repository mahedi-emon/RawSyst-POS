'use client';

// Which company's books are on screen.
//
// Rendered only when there is more than one. A shop with a single legal entity
// has nothing to choose, and a select with one option is a control that teaches
// somebody to look for a decision that does not exist.
//
// The currency is shown beside the name because switching company switches the
// currency every figure on the page is denominated in, and that is a bigger
// change than the name alone suggests.

import { Building2 } from 'lucide-react';

import { useCompany } from '@/lib/company/company-context';
import { cn } from '@/lib/utils';

export function CompanySwitch() {
  const { company, companies, setCompany } = useCompany();

  if (companies.length <= 1 || !company) return null;

  return (
    <div className="relative flex items-center">
      <Building2
        className="pointer-events-none absolute start-2.5 size-4 text-muted"
        aria-hidden="true"
      />
      <select
        value={company.id}
        onChange={(e) => setCompany(e.target.value)}
        aria-label="Company"
        className={cn(
          'h-9 max-w-[14rem] truncate rounded-sm border border-line',
          'bg-surface ps-8 pe-2 text-label text-fg',
          'hover:bg-surface-hover',
        )}
      >
        {companies.map((c) => (
          <option key={c.id} value={c.id}>
            {(c.trade_name || c.legal_name) + ' · ' + c.base_currency}
          </option>
        ))}
      </select>
    </div>
  );
}
