// Choosing what to order.
//
// A search box rather than a dropdown. A shop with four hundred variants makes
// a <select> unusable, and the buyer almost always knows part of the SKU or the
// name — typing three characters is faster than scrolling under any
// circumstances, and it is the only thing that works at all past a few dozen.
//
// # It reads the catalogue, not the cost book
//
// The snapshot endpoint carries id, SKU and name, and deliberately no cost: a
// buyer types the cost they negotiated, which is a different figure from what
// the shop last paid, and `catalog.view_cost_price` is a permission this screen
// does not need and does not ask for.
//
// # Loaded once, filtered locally
//
// A request per keystroke would put a query on the database for every letter
// somebody types. The catalogue is small enough to hold — the till already
// caches the whole thing to sell offline — so it is fetched once and filtered
// in memory, which also makes the list feel instant.

import { useEffect, useMemo, useState } from 'react';

import { useAuth } from '../auth/session';
import type { Client } from '../api/client';
import { useT } from '../i18n/locale';

interface Pickable {
  id: string;
  sku: string;
  name: string;
  description: string;
}

/** The wire shape of GET /api/v1/catalog/snapshot, narrowed to what a buyer
 *  needs to identify an item. */
interface SnapshotRow {
  id: string;
  sku: string;
  name: string;
  barcode?: string;
  is_active: boolean;
}

async function loadCatalogue(client: Client, companyId: string): Promise<Pickable[]> {
  const body = await client.send<{ items: SnapshotRow[] }>(
    'GET',
    `/api/v1/catalog/snapshot?company_id=${companyId}&limit=1000`,
  );
  return (body.items ?? [])
    // Withdrawn items are excluded here, unlike on the till. A cashier needs to
    // be told an item they are holding is off sale; a buyer ordering more of
    // something the shop has stopped selling is simply a mistake.
    .filter((r) => r.is_active)
    .map((r) => ({
      id: r.id,
      sku: r.sku,
      name: r.name,
      description: r.name ? `${r.name} (${r.sku})` : r.sku,
    }));
}

export function VariantPicker({
  companyId,
  value,
  description,
  onPick,
}: {
  companyId: string;
  value: string;
  description: string;
  onPick: (v: Pickable) => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const [catalogue, setCatalogue] = useState<Pickable[] | null>(null);
  const [term, setTerm] = useState('');
  const [open, setOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    loadCatalogue(client, companyId)
      .then((items) => {
        if (!cancelled) setCatalogue(items);
      })
      .catch(() => {
        if (!cancelled) setCatalogue([]);
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyId]);

  const matches = useMemo(() => {
    if (!catalogue || term.trim().length < 2) return [];
    const needle = term.trim().toLowerCase();
    return catalogue
      .filter(
        (v) =>
          v.sku.toLowerCase().includes(needle) ||
          v.name.toLowerCase().includes(needle),
      )
      // Capped, because a two-letter search against a large catalogue matches
      // most of it and a list of four hundred is no more useful than none.
      .slice(0, 12);
  }, [catalogue, term]);

  // Chosen already: show what it is, with a way to change it. A search box
  // still holding a query after the buyer has picked something invites them to
  // wonder whether it took.
  if (value) {
    return (
      <div className="picker picker--chosen">
        <span className="detail__strong">{description}</span>
        <button
          type="button"
          className="ds-btn ds-btn--quiet"
          onClick={() => {
            onPick({ id: '', sku: '', name: '', description: '' });
            setTerm('');
            setOpen(true);
          }}
        >
          {t('common.change')}
        </button>
      </div>
    );
  }

  return (
    <div className="picker">
      <input
        className="input"
        value={term}
        placeholder={catalogue === null ? 'Loading items…' : 'Search by name or SKU'}
        disabled={catalogue === null}
        aria-label={t('purch.searchCatalogue')}
        onChange={(e) => {
          setTerm(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
      />

      {open && term.trim().length >= 2 && (
        <ul className="picker__list" role="listbox">
          {matches.length === 0 ? (
            <li className="picker__empty">
              {catalogue?.length === 0
                ? 'No items in this catalogue yet.'
                : `Nothing matches “${term.trim()}”.`}
            </li>
          ) : (
            matches.map((v) => (
              <li key={v.id}>
                <button
                  type="button"
                  className="picker__option"
                  onClick={() => {
                    onPick(v);
                    setOpen(false);
                    setTerm('');
                  }}
                >
                  <span className="detail__strong">{v.name || v.sku}</span>
                  <span className="ds-caption">{v.sku}</span>
                </button>
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  );
}
