// One search box, and the keyboard-first command menu (blueprint D7).
//
// # It searches what the caller can already open
//
// The server runs each branch of the query only if the caller holds the
// permission that guards it, so a cashier searching a name gets the product and
// not the employee. Nothing here filters after the fact — the rows are never
// read in the first place.
//
// # Commands and results share one box
//
// D7 asks for a search box and a keyboard-first command menu. They are the same
// gesture: somebody presses a key, types a few characters, and presses Enter.
// Two boxes would mean guessing which one a person wants before they type.
//
// # Typing does not fire a request per keystroke
//
// The query waits until somebody stops typing. Without that, "abaya" is five
// searches of which four are thrown away, and on a shop's connection the last
// one is not necessarily the one that answers last.

import { useEffect, useMemo, useRef, useState } from 'react';

import { search, type SearchHit } from '../api/studio';
import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Icon } from '../ui/Icon';
import { money } from '../ui/format';

/** One place the palette can jump to. */
export interface Command {
  /** The section key the shell switches to. */
  section: string;
  label: string;
  /** The permission it needs, or empty when anybody signed in may open it. */
  permission: string;
}

export function CommandPalette({
  companyId,
  commands,
  onGo,
  onClose,
}: {
  companyId: string;
  commands: Command[];
  onGo: (section: string) => void;
  onClose: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();

  const [term, setTerm] = useState('');
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [busy, setBusy] = useState(false);
  const [cursor, setCursor] = useState(0);
  const box = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    box.current?.focus();
  }, []);

  // The jumps this person can actually make. A menu offering a screen that
  // would refuse them is a menu that teaches people not to trust it.
  const jumps = useMemo(
    () =>
      commands.filter(
        (c) =>
          (c.permission === '' || can(c.permission)) &&
          (term.trim() === '' ||
            c.label.toLowerCase().includes(term.trim().toLowerCase())),
      ),
    [commands, can, term],
  );

  // Debounced. See the file note.
  useEffect(() => {
    const q = term.trim();
    if (q.length < 2) {
      setHits([]);
      return;
    }
    let cancelled = false;
    setBusy(true);
    const timer = setTimeout(() => {
      search(client, companyId, q)
        .then((out) => {
          if (!cancelled) setHits(out.data);
        })
        .catch(() => {
          if (!cancelled) setHits([]);
        })
        .finally(() => {
          if (!cancelled) setBusy(false);
        });
    }, 180);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [client, companyId, term]);

  const rows: Array<{ kind: 'jump' | 'hit'; jump?: Command; hit?: SearchHit }> =
    useMemo(
      () => [
        ...jumps.map((j) => ({ kind: 'jump' as const, jump: j })),
        ...hits.map((h) => ({ kind: 'hit' as const, hit: h })),
      ],
      [jumps, hits],
    );

  useEffect(() => {
    setCursor(0);
  }, [term]);

  function choose(i: number) {
    const row = rows[i];
    if (!row) return;
    if (row.kind === 'jump' && row.jump) {
      onGo(row.jump.section);
      onClose();
      return;
    }
    // A result opens the section it belongs to. Deep-linking to the record
    // itself would need a route per kind, and the sections all open on a list
    // the search term will still be in.
    if (row.hit) {
      const section = sectionForKind(row.hit.kind);
      if (section) onGo(section);
      onClose();
    }
  }

  function onKey(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      onClose();
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setCursor((c) => Math.min(c + 1, Math.max(rows.length - 1, 0)));
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      choose(cursor);
    }
  }

  return (
    <div className="palette__scrim" role="presentation" onMouseDown={onClose}>
      <div
        className="palette"
        role="dialog"
        aria-label={t('find.title')}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="palette__box">
          <Icon name="search" />
          <input
            ref={box}
            className="palette__input"
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            onKeyDown={onKey}
            placeholder={t('find.placeholder')}
            aria-label={t('find.title')}
          />
          {busy && <span className="ds-caption">{t('find.looking')}</span>}
        </div>

        {rows.length === 0 ? (
          <p className="palette__empty">
            {term.trim().length < 2 ? t('find.typeMore') : t('find.nothing')}
          </p>
        ) : (
          <ul className="palette__list" role="listbox">
            {rows.map((row, i) => (
              <li key={i}>
                <button
                  className={`palette__row${i === cursor ? ' palette__row--on' : ''}`}
                  role="option"
                  aria-selected={i === cursor}
                  onMouseEnter={() => setCursor(i)}
                  onClick={() => choose(i)}
                >
                  {row.kind === 'jump' ? (
                    <>
                      <span className="palette__kind">{t('find.goTo')}</span>
                      <span className="palette__label">{row.jump!.label}</span>
                    </>
                  ) : (
                    <>
                      <span className="palette__kind">
                        {t(`find.kind.${row.hit!.kind}` as Key)}
                      </span>
                      <span className="palette__label">{row.hit!.label}</span>
                      {row.hit!.detail && (
                        <span className="palette__detail">
                          {row.hit!.detail}
                        </span>
                      )}
                      {row.hit!.amount && (
                        <span className="palette__amount">
                          {money(row.hit!.amount, {
                            currency: row.hit!.currency,
                          })}
                        </span>
                      )}
                    </>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}

        <p className="palette__hint">{t('find.keys')}</p>
      </div>
    </div>
  );
}

// sectionForKind says where a result lives.
//
// A map rather than a route per record, because every section opens on a list
// that the search term will still find — and a deep link per kind would be
// seven routes that each need their own detail screen to exist first.
function sectionForKind(kind: string): string | null {
  switch (kind) {
    case 'product':
      return 'inventory';
    case 'customer':
      return 'customers';
    case 'supplier':
      return 'buying';
    case 'invoice':
      return 'dashboard';
    case 'order':
      return 'salesorders';
    case 'employee':
      return 'staff';
    case 'serial':
      return 'aftersales';
  }
  return null;
}
