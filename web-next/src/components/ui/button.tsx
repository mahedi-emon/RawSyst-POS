'use client';

// The button.
//
// # Sizes are set by where the finger is, not by taste
//
// `md` is 36px, which is right for a mouse on a back-office screen. `lg` is
// 44px because that is the smallest target a thumb hits reliably, and it is the
// default in the POS and on anything a phone will show. A 32px row button was
// shipped here once and the audit caught it; the sizes below are the correction
// and they are not a matter of preference.
//
// # Busy, not just disabled
//
// A button that has been pressed and is waiting says so and stays the same
// width, so the layout does not jump and a person does not press it twice. That
// is the whole reason `busy` exists separately from `disabled`.

import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import { Loader2 } from 'lucide-react';
import type { ButtonHTMLAttributes, ReactNode, Ref } from 'react';

import { cn } from '@/lib/utils';

const button = cva(
  [
    'relative inline-flex items-center justify-center gap-2 whitespace-nowrap',
    'rounded-sm font-medium select-none',
    // 120ms is under the threshold at which a press feels delayed, and the
    // reduced-motion rule in globals.css removes it entirely for anyone who
    // has asked their system for that.
    'transition-colors duration-[120ms]',
    'disabled:pointer-events-none disabled:opacity-55',
    // Icons are sized here rather than at every call site, and are never
    // allowed to shrink a label out of the way.
    '[&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg]:size-4',
  ],
  {
    variants: {
      variant: {
        // One primary action per screen. If two things on a screen are both
        // this colour, one of them is not the primary action.
        primary:
          'bg-primary text-primary-fg hover:bg-primary-hover active:bg-primary-active',
        // The workhorse. Most buttons in an ERP are this.
        secondary:
          'bg-secondary text-secondary-fg border border-line-strong hover:bg-secondary-hover',
        // For a row of actions where a border on each would build a cage.
        ghost: 'text-fg hover:bg-surface-hover',
        // Deleting, voiding, revoking. Never used for merely "cancel".
        destructive:
          'bg-destructive text-destructive-fg hover:bg-destructive-hover',
        // Reads as text, behaves as a button. For "Show all 42" inside a panel.
        link: 'text-primary underline underline-offset-4 hover:text-primary-hover',
      },
      size: {
        sm: 'h-8 px-2.5 text-label',
        md: 'h-9 px-3.5 text-body',
        lg: 'h-11 px-5 text-lede',
        // Square, for an icon alone. Still 36 and 44, for the same reason.
        icon: 'size-9',
        'icon-lg': 'size-11',
      },
      block: { true: 'w-full', false: '' },
    },
    defaultVariants: { variant: 'secondary', size: 'md', block: false },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {
  /** Renders as the child element -- a Link, usually -- keeping the styling. */
  asChild?: boolean;
  /** Waiting on something. Shows a spinner, keeps the width, blocks a second press. */
  busy?: boolean;
  /** Announced while busy. Defaults to the button's own label being enough. */
  busyLabel?: string;
  /**
   * Focus target.
   *
   * Declared explicitly rather than relying on React 19 treating `ref` as an
   * ordinary prop, so the type is right for a caller that needs to move focus
   * -- the till moves it to "Next sale" the moment a sale completes.
   */
  ref?: Ref<HTMLButtonElement>;
  children?: ReactNode;
}

export function Button({
  className,
  variant,
  size,
  block,
  asChild = false,
  busy = false,
  busyLabel,
  disabled,
  children,
  type = 'button',
  ref,
  ...props
}: ButtonProps) {
  const Component = asChild ? Slot : 'button';

  return (
    <Component
      ref={ref}
      // Defaults to `button`. A button inside a form with no type submits it,
      // which is how a "Add a line" control ends up saving the record.
      type={asChild ? undefined : type}
      className={cn(button({ variant, size, block }), className)}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      {...props}
    >
      {busy && (
        <>
          {/* Overlaid rather than inserted, so the label keeps the button at
              its original width and nothing on the row moves. */}
          <span className="absolute inset-0 grid place-items-center">
            <Loader2 className="animate-spin" aria-hidden="true" />
          </span>
          <span className="sr-only">{busyLabel ?? 'Working'}</span>
        </>
      )}
      <span
        className={cn('inline-flex items-center gap-2', busy && 'invisible')}
      >
        {children}
      </span>
    </Component>
  );
}

export { button as buttonVariants };
