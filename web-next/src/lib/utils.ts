import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * Joins class names and lets a caller's class win over the component's own.
 *
 * `twMerge` is what makes the second half true: without it, passing
 * `className="px-6"` to a component that sets `px-3` produces both, and which
 * one applies depends on the order Tailwind happened to emit them in.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
