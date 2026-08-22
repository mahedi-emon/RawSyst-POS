// Choosing who a sale is for.
//
// Opens on a keystroke, closes on Escape, and never takes focus away from the
// counter for longer than it has to. A cashier reaches this in the middle of
// serving somebody, so the whole interaction is: type part of a name or a phone
// number, see who matches and what they owe, press Enter.
//
// # The server first, the cache second
//
// The opposite of the scan path, deliberately. A barcode lookup must be instant
// because a scanner fires faster than a network answers, and a stale price is a
// correctable receipt. A credit figure is a decision about whether to let
// somebody owe more money, so it is worth a round trip — and when there is no
// network, the cached figure with an honest label beats refusing to trade.
//
// # What it shows is what a cashier needs to decide
//
// Not an address book. What each row carries is: who they are, what they owe
// now, and what is left on their account — because the question being asked at
// the counter is always "can this go on their tab".

import { useEffect, useRef, useState } from 'react';

import { Offline } from '@rawsyst/shared/api/client';
import { useAuth } from '@rawsyst/shared/auth/session';
import { listCustomers } from '@rawsyst/shared/api/receivables';
import type { Customers } from '../offline/customers';
import { fromCache, type CounterCustomer } from './customer';
import { useT } from '@rawsyst/shared/i18n/locale';

export function CustomerPicker({
  customers,
  onChoose,
  onClose,
}: {
  /** The local book. Null in a browser during development, where the server
   *  is the only source and the picker says so if it cannot be reached. */
  customers: Customers | null;
  onChoose: (customer: CounterCustomer) => void;
  onClose: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [term, setTerm] = useState('');
  const [results, setResults] = useState<CounterCustomer[]>([]);
  const [searching, setSearching] = useState(false);
  const [searched, setSearched] = useState(false);
  const [offline, setOffline] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [highlight, setHighlight] = useState(0);

  const box = useRef<HTMLInputElement>(null);
  useEffect(() => box.current?.focus(), []);

  // Debounced, because a cashier types a name faster than a server answers and
  // a request per keystroke would leave the last one racing the one before it.
  useEffect(() => {
    const t = term.trim();
    if (t.length < 2) {
      setResults([]);
      setSearched(false);
      return;
    }

    let cancelled = false;
    const timer = setTimeout(() => {
      void (async () => {
        setSearching(true);
        setFailure(null);
        try {
          const fresh = await listCustomers(client, '', t);
          if (cancelled) return;
          setResults(
            fresh
              .filter((c) => c.is_active)
              .map((c) => ({
                id: c.id,
                code: c.code,
                name: c.name,
                phone: c.phone ?? '',
                paymentTermsDays: c.payment_terms_days,
                creditLimit: c.credit_limit ?? '',
                balance: c.balance,
                available: c.available ?? '',
                isActive: c.is_active,
                stale: false,
              })),
          );
          setOffline(false);
        } catch (err) {
          if (cancelled) return;
          if (err instanceof Offline) {
            // Fall back to what this terminal already knows. Labelled, so
            // nobody mistakes a figure from this morning for one from now.
            const local = (await customers?.search(t)) ?? [];
            if (cancelled) return;
            setResults(local.map(fromCache));
            setOffline(true);
          } else {
            setFailure(
              err instanceof Error ? err.message : 'That search did not work.',
            );
            setResults([]);
          }
        } finally {
          if (!cancelled) {
            setSearching(false);
            setSearched(true);
            setHighlight(0);
          }
        }
      })();
    }, 250);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [term, client, customers]);

  function onKey(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
      return;
    }
    if (results.length === 0) return;

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlight((h) => Math.min(h + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlight((h) => Math.max(h - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const chosen = results[highlight];
      if (chosen) onChoose(chosen);
    }
  }

  return (
    <div
      className="picker__backdrop"
      role="presentation"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <section
        className="picker"
        role="dialog"
        aria-modal="true"
        aria-label={t('pick.chooseCustomer')}
        onKeyDown={onKey}
      >
        <header className="picker__head">
          <h2 className="picker__title">{t('pick.whoIsThisFor')}</h2>
          <button
            className="button button--quiet"
            onClick={onClose}
            aria-label={t('pick.closeWithout')}
          >
            {t('action.close')}
          </button>
        </header>

        <input
          ref={box}
          className="picker__search"
          value={term}
          placeholder={t('pick.namePhoneCode')}
          aria-label={t('pick.searchCustomer')}
          // The list is the live region; the box only announces itself.
          aria-controls="picker-results"
          onChange={(e) => setTerm(e.target.value)}
        />

        {offline && (
          <p className="picker__stale" role="status">
            Searching this terminal&rsquo;s own list — the server cannot be
            reached. Balances are as at the last sync.
          </p>
        )}
        {failure && (
          <p className="picker__failure" role="alert">
            {failure}
          </p>
        )}

        <ul className="picker__results" id="picker-results" role="listbox">
          {term.trim().length < 2 ? (
            <li className="picker__hint">
              {t('pick.twoLetters')}
            </li>
          ) : searching ? (
            <li className="picker__hint">{t('action.loading')}</li>
          ) : results.length === 0 && searched ? (
            <li className="picker__hint">
              Nobody matches &ldquo;{term.trim()}&rdquo;.
              {offline
                ? ' They may exist on the server but not on this terminal yet.'
                : ' A customer can be added in the back office.'}
            </li>
          ) : (
            results.map((c, i) => (
              <li key={c.id}>
                <button
                  className={`picker__row${i === highlight ? ' picker__row--on' : ''}`}
                  role="option"
                  aria-selected={i === highlight}
                  onMouseEnter={() => setHighlight(i)}
                  onClick={() => onChoose(c)}
                >
                  <span className="picker__who">
                    <span className="picker__name">{c.name}</span>
                    <span className="picker__meta">
                      {c.code}
                      {c.phone && ` · ${c.phone}`}
                    </span>
                  </span>
                  <span className="picker__credit">
                    <CreditSummary customer={c} />
                  </span>
                </button>
              </li>
            ))
          )}
        </ul>
      </section>
    </div>
  );
}

/** What each row says about the account, in the fewest words that decide it. */
function CreditSummary({ customer }: { customer: CounterCustomer }) {
  const t = useT();
  if (!customer.creditLimit) {
    return <span className="picker__nocredit">{t('pick.paysNow')}</span>;
  }
  const available = Number(customer.available || '0');
  return (
    <>
      <span className={`picker__available num${available > 0 ? '' : ' picker__available--none'}`}>
        {customer.available || '0.00'}
      </span>
      <span className="picker__meta">
        available{Number(customer.balance) > 0 && ` · owes ${customer.balance}`}
      </span>
    </>
  );
}
