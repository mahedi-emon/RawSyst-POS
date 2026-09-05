'use client';

// The search box in the header.
//
// Deliberately just a way in. The results live at `/search`, which is a real
// page with a real URL — so a search is a link somebody can send a colleague,
// the back button returns to the results rather than to an empty overlay, and
// the header carries one input rather than a dropdown that has to reposition
// itself in Arabic.
//
// Hidden below `md`: at 360px the header already holds a page title, a company
// switch and a user menu, and a fourth control would push the title out. The
// page itself remains reachable from its URL and from navigation, so nothing is
// lost on a phone — only the shortcut is.

import { Search } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useState, type FormEvent } from 'react';

import { useT } from '@/lib/i18n/locale';

export function GlobalSearch() {
  const t = useT();
  const router = useRouter();
  const [q, setQ] = useState('');

  function submit(e: FormEvent) {
    e.preventDefault();
    const term = q.trim();
    if (term === '') return;
    router.push(`/search?q=${encodeURIComponent(term)}`);
  }

  return (
    <form onSubmit={submit} role="search" className="hidden md:block">
      <label className="relative flex items-center">
        <span className="sr-only">{t('nx.gs.label')}</span>
        {/* `start` rather than `left`, so the icon moves to the right edge in
            Arabic without a second rule. */}
        <Search
          className="pointer-events-none absolute start-2.5 size-4 text-muted"
          aria-hidden="true"
        />
        <input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('nx.gs.placeholder')}
          className="h-9 w-56 rounded-md border border-input bg-input-bg ps-8 pe-3 text-body text-fg placeholder:text-subtle focus:outline-none focus-visible:ring-2 focus-visible:ring-focus"
        />
      </label>
    </form>
  );
}
