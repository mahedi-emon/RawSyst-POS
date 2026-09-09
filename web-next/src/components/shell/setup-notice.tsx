'use client';

// A line above the page, while first-time setup is unfinished.
//
// # Why the product needs this and not a forced redirect
//
// A tenant is provisioned with an Owner and no company. Until the company
// exists `useCompanyScope` returns null, so no screen fires a request and the
// product opens onto an empty dashboard with nothing saying why. A nav entry
// alone is discovery only for somebody who thinks to look in Settings on their
// first morning, which is exactly the person A5 says must be able to finish
// alone.
//
// A redirect would be the other extreme. Setup is resumable and most of it is
// optional, and a wizard that reopens itself every time somebody navigates is
// one people learn to escape rather than to finish.
//
// So: one line, at the top, that says what is left and links to it. It
// disappears the moment setup is finished and never comes back.
//
// # It asks nothing of somebody who cannot answer it
//
// `GET /onboarding` is `identity.view`, which a cashier does not hold. The
// request is not made at all in that case — a 403 on every page load would be
// an audit entry per navigation for a question that was never this person's to
// answer.

import { ArrowRight } from 'lucide-react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';

import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';
import { stepsDone, STEPS_TO_DO, type Progress } from '@/lib/setup/wizard';

export function SetupNotice() {
  const t = useT();
  const pathname = usePathname();
  const mayView = useGrants().can('identity.view');

  const { data } = useApi<Progress>(mayView ? '/onboarding' : null, undefined, {
    // A tenant provisioned before the wizard existed, or one whose progress row
    // is missing, answers 404. That is not a state to retry on every page.
    retry: false,
    staleTime: 60_000,
  });

  // Nothing to say when it is finished, when it could not be read, or when
  // somebody is already on the wizard.
  if (!data || data.finished || pathname.startsWith('/setup')) return null;

  const done = stepsDone(data);

  return (
    <div
      role="status"
      className="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-md border border-caution/25 bg-caution-subtle px-4 py-3"
    >
      <p className="text-body text-caution-fg">
        {t('nx.setup.noticeBody', {
          done: String(done),
          total: String(STEPS_TO_DO),
        })}
      </p>
      <Link
        href="/setup"
        className="inline-flex items-center gap-1.5 text-body font-medium text-caution-fg underline underline-offset-4"
      >
        {t('nx.setup.noticeAction')}
        <ArrowRight className="size-4" aria-hidden="true" />
      </Link>
    </div>
  );
}
