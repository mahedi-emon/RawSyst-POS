'use client';

// What a shop is told when its subscription has lapsed.
//
// # Why it is in the shell and not on each screen
//
// Because the answer is the same everywhere and the question gets asked at the
// worst possible moment: halfway through a sale, with a customer waiting. A
// shopkeeper who finds out from a failed request has already lost the typing.
//
// # Why the words come from the server
//
// The sentence is composed in `billing/standing.go` and sent down whole. The
// policy — which states stop writes, which reason outranks which, what to do
// about each — is tested there and exists once. Assembling the sentence here
// from a state word would put a second copy of the policy in TypeScript, and
// the two would disagree the first time either changed.

import { AlertTriangle, Lock } from 'lucide-react';
import Link from 'next/link';

import { useT } from '@/lib/i18n/locale';
import { useStanding } from '@/lib/api/standing';
import { cn } from '@/lib/utils';

export function SubscriptionBanner() {
  const t = useT();
  const { standing, blocks, warns } = useStanding();

  // Nothing to say, which is the overwhelmingly common case and should cost
  // the screen nothing.
  if (!blocks && !warns) return null;

  const stopped = blocks !== '';
  const Icon = stopped ? Lock : AlertTriangle;

  return (
    <div
      // `alert` rather than `status` when trading has stopped: this is not
      // ambient information, it is the reason the next thing they try will
      // fail.
      role={stopped ? 'alert' : 'status'}
      className={cn(
        'flex flex-wrap items-start gap-3 border-b px-4 py-3 text-body sm:px-6',
        stopped
          ? 'border-critical/30 bg-critical-subtle text-critical-fg'
          : 'border-caution/30 bg-caution-subtle text-caution-fg',
      )}
    >
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <p className="min-w-0 flex-1">{blocks || warns}</p>

      {/* Where to go about it. The subscription screen is a read, so it keeps
          working in every state this banner appears in. */}
      <Link
        href="/settings/subscription"
        className="shrink-0 font-medium underline underline-offset-2"
      >
        {standing.state === 'past_due'
          ? t('nx.sub.seeBill')
          : t('nx.sub.seeSubscription')}
      </Link>
    </div>
  );
}
