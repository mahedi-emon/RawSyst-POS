'use client';

// Who is inside a business.
//
// # The questions this answers
//
// "How many of your five seats are actually in use." "Which of your staff has
// never signed in." "Who am I about to reset a password for." An operator was
// answering all three by asking the client to read them out, because the
// control plane could only COUNT the people in a business.
//
// # What it deliberately does not show
//
// Roles. A tenant's own role definitions are not readable from the platform
// plane — `role` and `user_role_assignment` were deliberately left without the
// platform predicate, and a Go test keeps it that way. Decorating a support
// list is not a good enough reason to widen that, so this says who exists and
// not what they may do.
//
// And no password hash. There is nothing to reveal in one, but a support screen
// that fetched it would put it in a log the first time somebody dumped a
// response.

import { ShieldCheck, Users } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Member {
  id: string;
  full_name: string;
  email: string;
  status: string;
  last_login_at?: string;
  created_at: string;
  mfa_enabled: boolean;
}

/** A timestamp trimmed to its date. The hour somebody signed in is not the
    question being asked. */
function day(value?: string): string | null {
  return value ? value.slice(0, 10) : null;
}

export function MembersPanel({ tenantId }: { tenantId: string }) {
  const t = useT();
  const { data } = useApi<{ data: Member[] }>(
    tenantId ? `/platform/tenants/${tenantId}/users` : null,
  );
  const rows = data?.data ?? [];

  const columns: Column<Member>[] = [
    {
      key: 'name',
      header: t('nx.plat.mbName'),
      primary: true,
      cell: (m) => (
        <span className="flex min-w-0 flex-col">
          <span className="truncate">{m.full_name}</span>
          {/* Latin and left-to-right: an address is not prose. */}
          <span dir="ltr" className="truncate text-caption text-subtle">
            {m.email}
          </span>
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.plat.mbStatus'),
      width: 'w-32',
      cell: (m) =>
        m.status === 'active' ? (
          <span className="text-muted">{t('nx.plat.mbActive')}</span>
        ) : (
          // `invited` means a one-time password nobody has used yet, which is
          // the answer when a client says nobody can get in.
          <Badge tone={m.status === 'invited' ? 'caution' : 'critical'}>
            {m.status}
          </Badge>
        ),
    },
    {
      key: 'mfa',
      header: t('nx.plat.mbMfa'),
      secondary: true,
      width: 'w-24',
      // An operator asked to reset a password needs to know what else is
      // protecting the account.
      cell: (m) =>
        m.mfa_enabled ? (
          <ShieldCheck className="size-4 text-positive-fg" aria-label="on" />
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'last',
      header: t('nx.plat.mbLastSeen'),
      width: 'w-36',
      cell: (m) => {
        const d = day(m.last_login_at);
        // Never signed in is a fact about the account, not a missing value.
        return d ? (
          <time dateTime={d} className="text-muted">
            {d}
          </time>
        ) : (
          <Badge tone="caution">{t('nx.plat.mbNeverSignedIn')}</Badge>
        );
      },
    },
  ];

  return (
    <Panel
      title={t('nx.plat.mbTitle')}
      description={t('nx.plat.mbDesc')}
      className="mb-4"
    >
      {rows.length === 0 ? (
        <EmptyState
          icon={Users}
          title={t('nx.plat.mbEmptyTitle')}
          description={t('nx.plat.mbEmptyDesc')}
        />
      ) : (
        <DataTable<Member>
          rows={rows}
          columns={columns}
          rowKey={(m) => m.id}
          caption={t('nx.plat.mbCaption')}
        />
      )}
    </Panel>
  );
}
