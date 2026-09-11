// What became of the welcome message a new business owner was sent.
//
// # Why this is a module and not four lines in the screen
//
// Because it is the one lookup in the product that must never throw, and a
// lookup buried in JSX cannot be tested.
//
// The screen that reads it is the handover: the single place in the whole
// product that displays a password. `POST /platform/tenants` answers with a
// temporary credential once, it is not stored in readable form, and it cannot
// be fetched again. If that screen throws while rendering, the operator loses
// the credential to a blank page and the only remedy is resetting it on an
// account that was created seconds ago.
//
// So the failure mode to design against is not "shows the wrong word". It is
// "renders nothing at all because a value arrived that the map did not have".
// That value comes over the wire from a separate container, so a rolling
// deploy, a partial rollback, or an API a version ahead can all produce one.
//
// # Why the fallback is the pessimistic answer
//
// An unrecognised word resolves to "nobody was told, hand it over yourself".
//
// The alternative — assuming the message went out — is worse than the crash it
// replaces. It would tell an operator a client has been emailed their sign-in
// details when nothing was sent, and the operator would stop there. The client
// then waits for a message that is never coming. Being wrong in the direction
// of "do it yourself" costs somebody a phone call they did not need to make.

import type { Key } from '@/lib/i18n/locale';

/** A sentence to show, and how much alarm it deserves. */
export type MailOutcome = readonly [Key, 'ok' | 'warn'];

/**
 * Nothing reached anybody; the operator is the delivery mechanism.
 *
 * Both the answer for `not_configured` and the fallback for anything
 * unrecognised, deliberately the same: in each case the true statement is that
 * the owner has been told nothing.
 */
export const NOBODY_WAS_TOLD: MailOutcome = [
  'nx.plat.newMailNotConfigured',
  'warn',
];

/**
 * The four words the API can send today.
 *
 * Three of them mean the owner receives nothing. Only `queued` is a deployment
 * with a provider wired, and none exists yet — see `jobs/notify.go` for why
 * choosing a provider is a business decision this product has not made.
 */
const OUTCOMES: Record<string, MailOutcome> = {
  queued: ['nx.plat.newMailQueued', 'ok'],
  // The worker writes it to its log and marks the job done. Development.
  queued_for_logging: ['nx.plat.newMailLogged', 'warn'],
  // The worker refuses the job, which retries, escalates and appears in the
  // failed-jobs view. Visible rather than silent, which is the point.
  queued_no_provider: ['nx.plat.newMailNoProvider', 'warn'],
  not_configured: NOBODY_WAS_TOLD,
};

/** What to tell the operator about the welcome message. Never throws. */
export function mailOutcome(status: string | undefined | null): MailOutcome {
  if (!status) return NOBODY_WAS_TOLD;
  return OUTCOMES[status] ?? NOBODY_WAS_TOLD;
}
