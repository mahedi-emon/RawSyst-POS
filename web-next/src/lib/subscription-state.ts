// Where this business stands, as the server sees it.
//
// # Why the server composes the sentence
//
// The banner shows text the API sends, not text assembled here from a state
// word. The policy — which states are read-only, which reason outranks which,
// what to tell somebody to do about it — lives in `billing/standing.go` and is
// tested there. A second copy in TypeScript would be a second policy, and the
// two would disagree the first time either changed.
//
// So this hook fetches a verdict and a sentence. It decides nothing.
//
// The hook that fetches it lives in `lib/api/standing.ts`. This file holds
// only the shapes and the one decision the client makes on its own, so it can
// be tested without React or a network in the way.
//
// # What this is NOT for
//
// Security. Every write the server refuses is refused whether or not this hook
// ran, by middleware in front of every tenant route. Hiding a button is a
// courtesy to somebody who would otherwise fill in a form and lose the typing;
// it is not a control, and a screen that forgot to call this is not a hole.

/** The states the server can report. */
export type SubscriptionState =
  | 'active'
  | 'trialing'
  | 'past_due'
  | 'expired'
  | 'suspended'
  | 'cancelled'
  | 'deactivated';

export interface Standing {
  state: SubscriptionState;
  subscription_status: string;
  tenant_status: string;
  expires_on?: string;
  sign_in_allowed: boolean;
  read_allowed: boolean;
  write_allowed: boolean;
}

/**
 * How loudly to say it.
 *
 * `past_due` is a warning about something that has not happened yet: the shop
 * is still trading and somebody should pay the bill. Everything else has
 * already stopped them.
 */
export function toneFor(state: SubscriptionState): 'caution' | 'critical' | null {
  switch (state) {
    case 'past_due':
      return 'caution';
    case 'expired':
    case 'suspended':
    case 'cancelled':
    case 'deactivated':
      return 'critical';
    default:
      return null;
  }
}
