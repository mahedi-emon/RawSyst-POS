'use client';

// The roles a business can hand out.
//
// # A built-in role is copied, never edited
//
// The product ships thirteen and keeps them current: a module added next year
// adds its permissions to them. Editing one would freeze it at today's shape,
// so the server refuses with *"copy it and edit the copy"* — and this screen
// offers Copy rather than offering Edit and collecting that refusal. The list
// route says `is_system`, which is what makes the honest button possible.
//
// # A role nobody can hand over is shown, not hidden
//
// `assignable` is false when the reader does not hold everything in the role,
// and `withheld_permissions` says which. Filtering those out would leave an
// owner looking for a role they can see in the seed data and cannot find here;
// showing it greyed with the reason is the difference between a rule and a bug.
//
// # Remove says why it cannot
//
// Two reasons and both the server's: a built-in role is the product's, and a
// role somebody holds cannot go because removing it would strip them of
// everything they can do. A button that is offered and then refused teaches
// people that buttons here do not mean anything.

import { ShieldCheck } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { removalBlock, type RoleOption } from '@/lib/people/roles';
import { FormError } from '@/components/ui/form-error';

function RolesScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const { data, isLoading, error, refetch } = useApiList<RoleOption>(
    scope ? '/people/roles' : null,
    scope ?? undefined,
  );
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const rows = data?.data ?? [];

  async function remove(role: RoleOption) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.delete(`/roles/${role.id}?company_id=${scope.company_id}`);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<RoleOption>[] = [
    {
      key: 'name',
      header: t('nx.role.colName'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{r.name}</span>
          {r.description ? (
            <span className="max-w-prose text-caption text-muted">
              {r.description}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'kind',
      header: t('nx.role.colKind'),
      width: 'w-32',
      cell: (r) =>
        r.is_system ? (
          <Badge>{t('nx.role.builtIn')}</Badge>
        ) : (
          <Badge tone="info">{t('nx.role.yourOwn')}</Badge>
        ),
    },
    {
      key: 'permissions',
      header: t('nx.role.colPermissions'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (r) => <span className="num text-muted">{r.permissions.length}</span>,
    },
    {
      key: 'holders',
      header: t('nx.role.colHolders'),
      numeric: true,
      width: 'w-28',
      cell: (r) => <span className="num">{r.in_use}</span>,
    },
    {
      key: 'state',
      header: t('nx.role.colState'),
      width: 'w-56',
      cell: (r) => {
        if (!r.assignable) {
          return (
            <span className="flex flex-col gap-1">
              <Badge tone="caution">{t('nx.role.cannotHandOver')}</Badge>
              <span className="text-caption text-muted">
                {t('nx.role.withheld', {
                  count: String(r.withheld_permissions?.length ?? 0),
                })}
              </span>
            </span>
          );
        }
        return <span className="text-muted">—</span>;
      },
    },
    {
      key: 'actions',
      header: t('nx.role.colActions'),
      width: 'w-52',
      cell: (r) => {
        const block = removalBlock(r);
        return (
          <span className="flex flex-wrap gap-2">
            {r.is_system ? (
              <Button
                size="sm"
                onClick={() => router.push(`/people/roles/new?copy=${r.id}`)}
              >
                {t('nx.role.copy')}
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={() => router.push(`/people/roles/${r.id}`)}
              >
                {t('nx.role.edit')}
              </Button>
            )}
            {block === 'none' ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void remove(r)}
              >
                {t('nx.role.remove')}
              </Button>
            ) : (
              // The reason, not a disabled button with no explanation.
              <span className="self-center text-caption text-muted">
                {block === 'built_in'
                  ? t('nx.role.cannotRemoveBuiltIn')
                  : t('nx.role.cannotRemoveHeld', { count: String(r.in_use) })}
              </span>
            )}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.role.title')}
        description={t('nx.role.subtitle')}
        actions={
          <Button variant="primary" onClick={() => router.push('/people/roles/new')}>
            {t('nx.role.build')}
          </Button>
        }
      />

      <FormError message={actionError} className="mb-4" />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.role.emptyTitle')}
          description={t('nx.role.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.role.caption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      ) : null}
    </>
  );
}

export default function RolesPage() {
  return (
    <RequirePermission anyOf={['identity.manage_roles']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RolesScreen />
      </Suspense>
    </RequirePermission>
  );
}
