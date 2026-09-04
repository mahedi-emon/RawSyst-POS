'use client';

// Panels, badges and page furniture.
//
// A panel is a bounded region with a heading. It is NOT the unit the whole
// product is built from -- most screens are a page header and a table, with no
// panel anywhere. Reaching for a panel to hold a single number is how a screen
// turns into a wall of identical cards that all say very little.

import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

export function Panel({
  title,
  description,
  actions,
  footer,
  /** Removes the body padding, for a panel whose whole body is a table. */
  flush,
  className,
  children,
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  footer?: ReactNode;
  flush?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section
      className={cn(
        'rounded-md border border-line bg-surface shadow-raised',
        className,
      )}
    >
      {(title || actions) && (
        <header
          className={cn(
            'flex flex-wrap items-start justify-between gap-3 px-4 py-3',
            // The rule only appears when there is a body under it to separate.
            'border-b border-line',
          )}
        >
          <div className="min-w-0">
            {title && (
              <h2 className="text-card-title font-semibold text-fg">{title}</h2>
            )}
            {description && (
              <p className="mt-0.5 text-label text-muted">{description}</p>
            )}
          </div>
          {actions && (
            <div className="flex shrink-0 items-center gap-2">{actions}</div>
          )}
        </header>
      )}

      <div className={cn(!flush && 'p-4')}>{children}</div>

      {footer && (
        <footer className="border-t border-line px-4 py-3 text-label text-muted">
          {footer}
        </footer>
      )}
    </section>
  );
}

/**
 * The page header.
 *
 * Title, one sentence of context, and the actions for the page. The sentence is
 * not decoration: on a screen called "Adjustments" it is where a person finds
 * out whether they are looking at stock or at journal corrections.
 */
export function PageHeader({
  title,
  description,
  actions,
  breadcrumb,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-3 pb-5 md:flex-row md:items-start md:justify-between">
      <div className="min-w-0">
        {breadcrumb}
        <h1 className="text-page font-semibold text-fg">{title}</h1>
        {description && (
          <p className="mt-1 max-w-[68ch] text-body text-muted">{description}</p>
        )}
      </div>
      {actions && (
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {actions}
        </div>
      )}
    </header>
  );
}

const TONES = {
  neutral: 'bg-surface-sunken text-muted border-line',
  positive: 'bg-positive-subtle text-positive-fg border-positive/25',
  caution: 'bg-caution-subtle text-caution-fg border-caution/25',
  critical: 'bg-critical-subtle text-critical-fg border-critical/25',
  info: 'bg-info-subtle text-info-fg border-info/25',
  primary: 'bg-primary-subtle text-primary-subtle-fg border-primary/25',
} as const;

export type Tone = keyof typeof TONES;

/**
 * A state, not a decoration.
 *
 * Every badge in this product names something the record actually is -- Draft,
 * Posted, Overdue, Recalled. A badge that repeats the column it sits in is
 * noise, and colour used without a state behind it teaches people to ignore
 * colour.
 */
export function Badge({
  tone = 'neutral',
  children,
  className,
}: {
  tone?: Tone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-xs border px-1.5 py-0.5',
        'text-caption font-medium whitespace-nowrap',
        TONES[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

/**
 * A headline figure.
 *
 * Used sparingly: a dashboard that is nine of these has told the owner nothing,
 * because nothing on it is more important than anything else. The label goes
 * ABOVE the figure, so the eye reads what it is before how much it is.
 */
export function Figure({
  label,
  value,
  currency,
  caption,
  tone,
  href,
}: {
  label: ReactNode;
  value: ReactNode;
  /** Set quieter than the figure, so the number reads first. */
  currency?: string;
  caption?: ReactNode;
  tone?: 'positive' | 'critical';
  /** Makes the whole figure a drill-through. A KPI you cannot open is trivia. */
  href?: string;
}) {
  const body = (
    <>
      <p className="text-label text-muted">{label}</p>
      <p
        className={cn(
          'num mt-1 flex items-baseline gap-1.5 text-display font-semibold tracking-tight',
          tone === 'positive' && 'text-positive-fg',
          tone === 'critical' && 'text-critical-fg',
        )}
      >
        {currency && (
          <span className="text-lede font-medium text-muted">{currency}</span>
        )}
        {value}
      </p>
      {caption && <p className="mt-1 text-caption text-subtle">{caption}</p>}
    </>
  );

  if (!href) return <div>{body}</div>;

  return (
    <a
      href={href}
      className="block rounded-sm -m-2 p-2 transition-colors hover:bg-surface-hover"
    >
      {body}
    </a>
  );
}
