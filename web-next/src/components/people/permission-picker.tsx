'use client';

// The permission picker, shared by building a role and editing one.
//
// # A hundred and three tick boxes is a list nobody reads
//
// So they are grouped by the server's own sections, each collapsible, each
// saying how many inside it are ticked — a collapsed section still tells you
// whether anything in it is on. Selling first because that is what most roles
// are for; system last because almost no role needs it.
//
// # A box somebody cannot tick is disabled and explained
//
// `identity.manage_roles` lets somebody put into a role anything they hold
// THEMSELVES, and the server refuses the rest: *"You cannot put something into
// a role that you do not have yourself"*. Hiding those boxes would leave a
// manager unable to see why a permission they know exists is not on the list.
// Disabling them with the reason beside is the difference between a rule and a
// missing feature.
//
// # A caution is not decoration
//
// Twenty-seven permissions carry a sentence from the server saying what makes
// them dangerous — "Nothing can be posted into a closed month", "Every salary
// is visible to anybody holding this". They sit beside the box, in the server's
// words, because rewording a warning is how it stops meaning what it said.

import { ChevronDown } from 'lucide-react';
import { useState } from 'react';

import { useT, type Key } from '@/lib/i18n/locale';
import {
  bySection,
  grantableIn,
  tickedIn,
  type PermissionOption,
} from '@/lib/people/roles';
import { cn } from '@/lib/utils';

const SECTION_LABEL: Record<string, Key> = {
  selling: 'nx.perm.selling',
  stock: 'nx.perm.stock',
  buying: 'nx.perm.buying',
  customers: 'nx.perm.customers',
  money: 'nx.perm.money',
  staff: 'nx.perm.staff',
  aftersales: 'nx.perm.aftersales',
  oversight: 'nx.perm.oversight',
  system: 'nx.perm.system',
};

export function PermissionPicker({
  all,
  chosen,
  onChange,
  disabled,
}: {
  all: PermissionOption[];
  chosen: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  const t = useT();
  const sections = bySection(all);
  // Open the first section, closed elsewhere: a hundred boxes open at once is
  // the list this grouping exists to avoid.
  const [open, setOpen] = useState<Set<string>>(
    () => new Set(sections[0] ? [sections[0].section] : []),
  );
  const ticked = new Set(chosen);

  function toggle(permission: string) {
    onChange(
      ticked.has(permission)
        ? chosen.filter((p) => p !== permission)
        : [...chosen, permission],
    );
  }

  function setWholeSection(names: string[], on: boolean) {
    const set = new Set(chosen);
    for (const n of names) {
      if (on) set.add(n);
      else set.delete(n);
    }
    onChange([...set]);
  }

  return (
    <div className="flex flex-col gap-2">
      {sections.map((section) => {
        const isOpen = open.has(section.section);
        const count = tickedIn(section, chosen);
        const grantable = grantableIn(section);
        const label = SECTION_LABEL[section.section];

        return (
          <section
            key={section.section}
            className="rounded-md border border-line bg-surface"
          >
            <h3>
              <button
                type="button"
                aria-expanded={isOpen}
                aria-controls={`section-${section.section}`}
                onClick={() =>
                  setOpen((current) => {
                    const next = new Set(current);
                    if (next.has(section.section)) next.delete(section.section);
                    else next.add(section.section);
                    return next;
                  })
                }
                className={cn(
                  'flex min-h-11 w-full items-center gap-3 px-4 py-2.5 text-start',
                  'hover:bg-surface-sunken',
                )}
              >
                <ChevronDown
                  aria-hidden="true"
                  className={cn(
                    'size-4 shrink-0 text-muted transition-transform',
                    // Rotating rather than swapping the glyph, so the direction
                    // reads the same in Arabic.
                    !isOpen && 'ltr:-rotate-90 rtl:rotate-90',
                  )}
                />
                <span className="flex-1 font-medium text-fg">
                  {label ? t(label) : section.section}
                </span>
                {/* A collapsed section still says whether anything is on. */}
                <span className="num text-caption text-muted">
                  {count > 0
                    ? t('nx.perm.someOf', {
                        count: String(count),
                        total: String(section.permissions.length),
                      })
                    : t('nx.perm.noneOf', {
                        total: String(section.permissions.length),
                      })}
                </span>
              </button>
            </h3>

            {isOpen ? (
              <div
                id={`section-${section.section}`}
                className="border-t border-line px-4 py-3"
              >
                {grantable.length > 0 && !disabled ? (
                  <div className="mb-3 flex gap-3">
                    <button
                      type="button"
                      onClick={() => setWholeSection(grantable, true)}
                      className="text-caption text-primary underline underline-offset-2 hover:no-underline"
                    >
                      {t('nx.perm.tickAll')}
                    </button>
                    <button
                      type="button"
                      onClick={() => setWholeSection(grantable, false)}
                      className="text-caption text-muted underline underline-offset-2 hover:no-underline"
                    >
                      {t('nx.perm.clearAll')}
                    </button>
                  </div>
                ) : null}

                <ul className="flex flex-col gap-2.5">
                  {section.permissions.map((p) => {
                    const id = `perm-${p.permission}`;
                    const blocked = !p.holds;
                    return (
                      <li key={p.permission}>
                        <div className="flex items-start gap-2.5">
                          <input
                            id={id}
                            type="checkbox"
                            checked={ticked.has(p.permission)}
                            disabled={disabled || blocked}
                            onChange={() => toggle(p.permission)}
                            aria-describedby={
                              p.caution || blocked ? `${id}-note` : undefined
                            }
                            className={cn(
                              'mt-0.5 size-4 shrink-0 rounded-xs border border-input',
                              'accent-primary disabled:opacity-50',
                            )}
                          />
                          <div className="min-w-0">
                            <label
                              htmlFor={id}
                              className={cn(
                                'text-body',
                                blocked ? 'text-muted' : 'text-fg',
                              )}
                            >
                              {p.label}
                            </label>
                            {p.caution || blocked ? (
                              <p
                                id={`${id}-note`}
                                className={cn(
                                  'mt-0.5 max-w-prose text-caption',
                                  blocked ? 'text-muted' : 'text-caution-fg',
                                )}
                              >
                                {blocked ? t('nx.perm.youDoNotHold') : p.caution}
                              </p>
                            ) : null}
                          </div>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </div>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}
