'use client';

// Choosing a customer at the till.
//
// A shop does not ask a name to sell a bottle of water, so most sales have no
// customer and this never opens. It matters for the two cases that do: a
// wholesale customer, who is charged a different price the moment they are
// named, and a sale going on account, which the server refuses without one.
//
// So the list leads with what a cashier at that moment actually needs -- what
// this customer owes and how much credit is left -- rather than with an address.

import { Search, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { formatMoney } from '@/lib/format/money';
import { cn } from '@/lib/utils';

export interface PosCustomer {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  customer_type: string;
  phone?: string;
  balance: string;
  /** Empty when no limit is set, which means no credit at all. */
  available?: string;
  credit_limit?: string;
  currency?: string;
}

export function CustomerPicker({
  onPick,
  onClose,
}: {
  onPick: (c: PosCustomer) => void;
  onClose: () => void;
}) {
  const t = useT();
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // 200ms: long enough that typing a name is one request rather than eight,
  // short enough that it does not feel like waiting.
  useEffect(() => {
    const id = setTimeout(() => setDebounced(search), 200);
    return () => clearTimeout(id);
  }, [search]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const { data, isLoading } = useApiList<PosCustomer>(
    scope ? '/customers' : null,
    { ...scope, search: debounced },
  );

  const customers = data?.data ?? [];

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-[rgb(15_27_24/0.5)] p-4 pt-[8vh]"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t('nx.pos.chooseCustomer')}
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[70vh] w-full max-w-lg flex-col overflow-hidden rounded-lg bg-surface shadow-overlay"
      >
        <div className="relative flex shrink-0 items-center border-b border-line">
          <Search
            className="pointer-events-none absolute start-3 size-4 text-muted"
            aria-hidden="true"
          />
          <input
            ref={inputRef}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('nx.pos.custSearch')}
            aria-label={t('nx.pos.custSearchLabel')}
            className="h-12 w-full bg-transparent ps-10 pe-11 text-body text-fg placeholder:text-disabled focus-visible:outline-none"
          />
          <button
            type="button"
            onClick={onClose}
            aria-label={t('nx.common.close')}
            className="absolute end-1 grid size-10 place-items-center rounded-sm text-muted hover:bg-surface-hover"
          >
            <X className="size-4" aria-hidden="true" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {isLoading && (
            <p className="p-4 text-body text-muted">{t('nx.pos.looking')}</p>
          )}

          {!isLoading && customers.length === 0 && (
            <p className="p-4 text-body text-muted">
              {debounced
                ? t('nx.pos.custNoMatch')
                : t('nx.pos.custStartTyping')}
            </p>
          )}

          <ul>
            {customers.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  onClick={() => onPick(c)}
                  className={cn(
                    'flex min-h-14 w-full items-center gap-3 border-b border-line px-4',
                    'text-start hover:bg-surface-hover',
                  )}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body font-medium text-fg">
                      {c.name}
                    </span>
                    <span className="block text-caption text-muted">
                      {c.code}
                      {c.phone ? ` · ${c.phone}` : ''}
                      {c.customer_type === 'wholesale'
                        ? ` · ${t('nx.pos.wholesale')}`
                        : ''}
                    </span>
                  </span>
                  <span className="shrink-0 text-end">
                    <span className="num block text-body tabular-nums">
                      {formatMoney(c.balance, {
                        currency: c.currency ?? currency,
                        market,
                        bare: true,
                      })}
                    </span>
                    <span className="block text-caption text-subtle">
                      {/* Empty means no limit is set, which means no credit at
                          all -- different from a limit of zero, and the
                          cashier needs to know which. */}
                      {c.available
                        ? t('nx.pos.available', {
                            amount: formatMoney(c.available, {
                              currency: c.currency ?? currency,
                              market,
                              bare: true,
                            }),
                          })
                        : t('nx.pos.noCredit')}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
