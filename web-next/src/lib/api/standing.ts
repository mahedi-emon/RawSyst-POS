// Fetching where this business stands.
//
// Split from `lib/subscription-state.ts` so the rule in that file stays free of
// React and of the `@/` value imports the test runner cannot resolve — the same
// separation `origin.ts` and `proxy.ts` already use.

import { useApi } from '@/lib/api/hooks';
import type { Standing } from '@/lib/subscription-state';

interface StandingReply {
  standing: Standing;
  /** Why writes are refused. Empty when they are not. */
  blocks: string;
  /** A warning about something that has not happened yet. Empty when none. */
  warns: string;
}

/**
 * Assumed when the answer has not arrived, or cannot.
 *
 * Writes allowed, deliberately. A screen that greyed out every button while a
 * request was in flight would flicker on every navigation, and a network blip
 * would look like a suspension — telling a shopkeeper their subscription has
 * lapsed when it has not is worse than briefly offering a button the server
 * then refuses with a message that says exactly what happened.
 */
const TRADING: Standing = {
  state: 'active',
  subscription_status: 'active',
  tenant_status: 'active',
  sign_in_allowed: true,
  read_allowed: true,
  write_allowed: true,
};

/**
 * The business's commercial standing.
 *
 * Polled rather than read once: a suspension applied while somebody is working
 * should reach their screen without a reload. Two minutes, because this changes
 * about as often as a subscription does and the server refuses the write either
 * way.
 */
export function useStanding(): {
  standing: Standing;
  blocks: string;
  warns: string;
} {
  const { data } = useApi<StandingReply>('/subscription/standing', undefined, {
    refetchInterval: 120_000,
  });
  return {
    standing: data?.standing ?? TRADING,
    blocks: data?.blocks ?? '',
    warns: data?.warns ?? '',
  };
}

/** Whether this business may still change anything. */
export function useCanWrite(): boolean {
  return useStanding().standing.write_allowed;
}
