'use client';

// Route protection.
//
// # What this is, and what it is not
//
// This stops a screen from RENDERING and, more importantly, from FETCHING. A
// person who types `/people/payroll` without the payroll permission sees the
// refusal instead of the screen, and no request for payroll data is made at
// all. That is worth doing: an unauthorised request would be refused by the Go
// service, but it would still be a refused request in the audit log, and the
// screen would flash a table skeleton on its way to an error.
//
// It is NOT the security boundary. The Go service checks the same permission on
// every route and is the only thing actually protecting the data. If this file
// were deleted the product would still be secure; it would merely be ruder.
//
// # Waiting is not the same as refused
//
// While the session is resolving nobody has any permissions yet, so a naive
// check refuses everyone for a moment and the product flashes "access denied"
// on every page load. `status` is checked before `grants`, always.

import { useRouter } from 'next/navigation';
import { useEffect, type ReactNode } from 'react';

import type { Permission } from '@/lib/auth/permissions';
import { useSession, type Workspace } from '@/lib/auth/session';
import { useT } from '@/lib/i18n/locale';
import { AccessDenied } from '@/components/ui/states';

/** The quiet state while the session resolves. Not a spinner: it is usually
 *  one network round trip, and a spinner that appears for 200ms is a flash. */
function Resolving() {
  const t = useT();
  return (
    <div className="grid min-h-dvh place-items-center bg-ground" aria-busy="true">
      <p className="sr-only">{t('nx.guard.checking')}</p>
    </div>
  );
}

/**
 * Requires a signed-in session, and the right workspace.
 *
 * A platform operator opening a business URL, or a shop employee opening a
 * platform URL, is sent to their own workspace rather than refused: they are
 * signed in perfectly well, they are simply in the wrong half of the product,
 * and a redirect is the honest answer to a wrong turn.
 */
export function RequireWorkspace({
  workspace,
  children,
}: {
  workspace: Workspace;
  children: ReactNode;
}) {
  const { status, identity } = useSession();
  const router = useRouter();

  useEffect(() => {
    if (status === 'signed-out') {
      // `next` so the person lands where they were going rather than on a
      // dashboard, having lost the link somebody sent them.
      const next = encodeURIComponent(
        window.location.pathname + window.location.search,
      );
      router.replace(`/login?next=${next}`);
      return;
    }
    if (status === 'signed-in' && identity && identity.workspace !== workspace) {
      router.replace(identity.workspace === 'platform' ? '/platform' : '/dashboard');
    }
  }, [status, identity, workspace, router]);

  if (status !== 'signed-in' || !identity) return <Resolving />;
  if (identity.workspace !== workspace) return <Resolving />;

  return <>{children}</>;
}

/**
 * Requires one of the named permissions.
 *
 * Any one of them opens the screen, matching how navigation decides whether to
 * offer it, so a link that appears always leads somewhere.
 */
export function RequirePermission({
  anyOf,
  children,
  /** Where "go back to where you can work" points. */
  backHref,
}: {
  anyOf: readonly Permission[];
  children: ReactNode;
  backHref?: string;
}) {
  const { status, grants } = useSession();

  if (status === 'resolving') return <Resolving />;

  if (anyOf.length > 0 && !grants.canAny(...anyOf)) {
    // Named plainly so the person can repeat it to whoever grants it. The
    // permission strings are the ones the roles screen shows, so this is not
    // internal jargon leaking out -- it is the same word in both places.
    return <AccessDenied needed={anyOf[0]} backHref={backHref} />;
  }

  return <>{children}</>;
}

/**
 * Shows its children only if the person may perform the action.
 *
 * For a button inside a screen they can otherwise see -- a Refund control on an
 * invoice a cashier is allowed to read. Renders nothing when refused rather
 * than rendering a disabled control, because a disabled button invites somebody
 * to work out how to enable it.
 */
export function Can({
  permission,
  children,
  /** Shown instead, where the absence would be confusing. */
  fallback = null,
}: {
  permission: Permission | readonly Permission[];
  children: ReactNode;
  fallback?: ReactNode;
}) {
  const { grants } = useSession();
  const needed = Array.isArray(permission) ? permission : [permission as Permission];
  return grants.canAny(...needed) ? <>{children}</> : <>{fallback}</>;
}
