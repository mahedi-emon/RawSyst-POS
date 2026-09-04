'use client';

// Screen state that lives in the URL.
//
// # Why a filter belongs in the address bar
//
// A list that has been searched or filtered is a different view of the product,
// and three things stop working when that view is held only in component state:
// somebody cannot send it to a colleague, the back button does not undo it, and
// a refresh throws it away. The old back office had all three problems because
// it navigated by `useState`; rebuilding on real URLs and then keeping the
// filters out of them would have reintroduced two thirds of it.
//
// # `replace`, not `push`
//
// Typing five characters into a search box must not put five entries in the
// history. `replace` keeps the address bar current and leaves Back meaning
// "the screen before this one", which is what somebody pressing it wants.
//
// # The input stays local
//
// The URL is updated on a debounce; the text field is driven by local state so
// every keystroke paints immediately. Reading the field from the URL would put
// a router round trip between the key and the character.

import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useCallback } from 'react';

/**
 * Reads and writes one query parameter.
 *
 * An empty value removes the parameter rather than leaving `?q=` behind, so a
 * cleared filter produces the same URL as never having set one -- which is
 * what makes two people who reached the screen differently share a link.
 */
export function useUrlState(
  key: string,
  fallback = '',
): [string, (next: string) => void] {
  const params = useSearchParams();
  const pathname = usePathname();
  const router = useRouter();

  const value = params.get(key) ?? fallback;

  const set = useCallback(
    (next: string) => {
      const updated = new URLSearchParams(params.toString());
      if (next === '' || next === fallback) updated.delete(key);
      else updated.set(key, next);

      const qs = updated.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [params, pathname, router, key, fallback],
  );

  return [value, set];
}

/** The same, for a flag. Present means on; absent means off. */
export function useUrlFlag(key: string): [boolean, (next: boolean) => void] {
  const [raw, setRaw] = useUrlState(key);
  return [raw === 'true', (next) => setRaw(next ? 'true' : '')];
}
