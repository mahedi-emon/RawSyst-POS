'use client';

// Switching between views of one screen.
//
// # Why the state belongs in the URL
//
// Three things break when a tab is held in component state: the view cannot be
// sent to a colleague, Back does not undo it, and a refresh throws it away. So
// the caller keeps the value in a query parameter and passes it in; this
// renders the control and nothing else.
//
// # A real tablist, not buttons that look like one
//
// The WAI-ARIA pattern is a single tab stop with the arrow keys moving between
// tabs, which is what somebody using a keyboard expects the moment the control
// looks like this. Getting that wrong is worse than not using the pattern:
// `role="tab"` announces a promise about the keyboard that the component then
// has to keep.

import { useRef } from 'react';

import { cn } from '@/lib/utils';

export interface TabItem<Id extends string> {
  id: Id;
  label: string;
  /** A count or a state shown after the label — "3 due", "2 retired". */
  badge?: string;
}

export function Tabs<Id extends string>({
  label,
  items,
  value,
  onChange,
  className,
}: {
  /** Names the group for a screen reader. Required; there is no default. */
  label: string;
  items: readonly TabItem<Id>[];
  value: Id;
  onChange: (id: Id) => void;
  className?: string;
}) {
  const list = useRef<HTMLDivElement>(null);

  function move(delta: number) {
    const at = items.findIndex((i) => i.id === value);
    // Wraps, because a list this short reads as a ring rather than a line.
    const next = items[(at + delta + items.length) % items.length];
    if (!next) return;
    onChange(next.id);
    // Focus follows selection, which is the pattern for tabs whose panels are
    // cheap to render. The panel is already in the document either way.
    requestAnimationFrame(() => {
      list.current?.querySelector<HTMLButtonElement>(`#tab-${next.id}`)?.focus();
    });
  }

  return (
    <div
      ref={list}
      role="tablist"
      aria-label={label}
      className={cn(
        'flex flex-wrap items-center gap-1 border-b border-line',
        className,
      )}
      onKeyDown={(e) => {
        // Left and right, mirrored by the browser's own reading order in
        // Arabic: `ArrowRight` moves to the next tab visually either way.
        const rtl = getComputedStyle(e.currentTarget).direction === 'rtl';
        const first = items[0];
        const last = items[items.length - 1];
        if (!first || !last) return;
        if (e.key === 'ArrowRight') move(rtl ? -1 : 1);
        else if (e.key === 'ArrowLeft') move(rtl ? 1 : -1);
        else if (e.key === 'Home') onChange(first.id);
        else if (e.key === 'End') onChange(last.id);
        else return;
        e.preventDefault();
      }}
    >
      {items.map((item) => {
        const selected = item.id === value;
        return (
          <button
            key={item.id}
            id={`tab-${item.id}`}
            role="tab"
            type="button"
            aria-selected={selected}
            aria-controls={`panel-${item.id}`}
            // One tab stop for the whole group; the arrows do the rest.
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(item.id)}
            className={cn(
              // 44px tall, which is the touch target and not a coincidence.
              'relative -mb-px flex h-11 items-center gap-2 px-3',
              'text-body whitespace-nowrap',
              'transition-colors duration-[120ms]',
              // The underline carries the selection. Colour alone would leave
              // the state invisible to anybody who cannot separate the two.
              'border-b-2',
              selected
                ? 'border-primary font-medium text-fg'
                : 'border-transparent text-muted hover:text-fg',
            )}
          >
            {item.label}
            {item.badge ? (
              <span
                className={cn(
                  'rounded-xs px-1.5 py-0.5 text-caption font-medium',
                  selected
                    ? 'bg-primary-subtle text-primary-subtle-fg'
                    : 'bg-surface-sunken text-muted',
                )}
              >
                {item.badge}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

/** The region a tab controls. Labelled by its tab, as the pattern requires. */
export function TabPanel({
  id,
  className,
  children,
}: {
  id: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      id={`panel-${id}`}
      role="tabpanel"
      aria-labelledby={`tab-${id}`}
      // Reachable by keyboard so somebody tabbing out of the tablist lands in
      // the content rather than skipping past it to the first control.
      tabIndex={0}
      className={cn('pt-5 focus-visible:outline-none', className)}
    >
      {children}
    </div>
  );
}
