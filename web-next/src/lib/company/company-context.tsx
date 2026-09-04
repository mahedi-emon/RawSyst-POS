'use client';

// Which set of books is on screen.
//
// # Why this exists at all
//
// A RawSyst business is `Group -> Company -> Store -> Terminal`. One owner can
// hold several legal companies, and each keeps SEPARATE books, a separate tax
// registration and its own invoice sequence. So "revenue this month" is not a
// question the product can answer until it knows which company is being asked
// about -- and the API says so: `company_id` is required on every dashboard,
// statement and report route, and omitting it is a 400 rather than a default.
//
// That makes the company a first-class piece of application state, not a filter
// on one screen. It sits here, near the root, and every figure in the product
// is denominated by it.
//
// # The currency comes with it
//
// Each company has its own `base_currency`, so switching company switches the
// currency every amount is shown in. Nothing in this product formats money
// without asking this context first; a hardcoded currency symbol would be wrong
// in two of the three markets RawSyst sells into.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

import { useCompanies, type CompanyRecord } from '../api/hooks';
import { marketOf, type MarketCode } from '../format/money';

const STORAGE_KEY = 'rawsyst.company';

interface CompanyValue {
  /** Null while the list is loading, or if this person can reach none. */
  company: CompanyRecord | null;
  companies: CompanyRecord[];
  setCompany: (id: string) => void;
  /** Convenience, read by every money figure on screen. */
  currency: string;
  market: MarketCode;
  loading: boolean;
}

const CompanyContext = createContext<CompanyValue | null>(null);

export function CompanyProvider({ children }: { children: ReactNode }) {
  const { data, isLoading } = useCompanies();
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const companies = useMemo(() => data?.data ?? [], [data]);

  useEffect(() => {
    if (companies.length === 0) return;
    // A remembered choice, but only if it is still a company this person can
    // reach. Access can be narrowed, and restoring a company they have since
    // been confined out of would produce 404s on every screen.
    let stored: string | null = null;
    try {
      stored = globalThis.localStorage?.getItem(STORAGE_KEY) ?? null;
    } catch {
      // Storage throws in a private window. A forgotten preference is a much
      // smaller problem than a product that will not open.
    }
    const valid = stored && companies.some((c) => c.id === stored);
    setSelectedId((current) => current ?? (valid ? stored : (companies[0]?.id ?? null)));
  }, [companies]);

  const setCompany = useCallback((id: string) => {
    setSelectedId(id);
    try {
      globalThis.localStorage?.setItem(STORAGE_KEY, id);
    } catch {
      // See above.
    }
  }, []);

  const company = companies.find((c) => c.id === selectedId) ?? null;

  const value = useMemo<CompanyValue>(
    () => ({
      company,
      companies,
      setCompany,
      // Empty rather than a guess. A screen that renders before the company
      // resolves shows an em dash, not an amount in the wrong currency.
      currency: company?.base_currency ?? '',
      market: marketOf(company?.country),
      loading: isLoading || (companies.length > 0 && company === null),
    }),
    [company, companies, setCompany, isLoading],
  );

  return <CompanyContext value={value}>{children}</CompanyContext>;
}

export function useCompany(): CompanyValue {
  const v = useContext(CompanyContext);
  if (!v) throw new Error('useCompany must be used inside <CompanyProvider>.');
  return v;
}

/**
 * The query parameter every report route needs.
 *
 * Returns null until a company is known, and `useApi` treats a null path as
 * "not yet" -- so a screen never fires a request that would come back 400 for a
 * missing company.
 */
export function useCompanyScope(): { company_id: string } | null {
  const { company } = useCompany();
  return company ? { company_id: company.id } : null;
}
