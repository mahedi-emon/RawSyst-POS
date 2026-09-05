'use client';

// Who can sign in to the two portals, and how.
//
// # The two portals are not administered the same way, and that is deliberate
//
// A SUPPLIER contact is created by the shop: somebody here names the supplier,
// the person and their password, because accepting a purchase order commits
// that supplier's business and a one-time code to a phone is not enough
// authority for it. So supplier access is a list this screen manages.
//
// A CUSTOMER is not. They sign in with their phone and a one-time code, and
// `POST /portal/code` "answers the same way whether or not the number is on
// file, so the portal cannot be used to ask a shop who its customers are."
// There is no customer portal account for staff to create, disable or reset —
// not because the feature is unfinished, but because giving one to staff would
// turn the sign-in into a way of probing the shop's customer list.
//
// That asymmetry is the reason this screen exists as one page rather than two:
// the question "who can get in" has two different answers and both of them are
// worth stating, and a screen that showed only the supplier list would leave
// somebody hunting for a customer list that should not exist.
//
// # The password is set here and never shown again
//
// `POST /portal/contacts` takes the password the shop chooses for the contact.
// It is typed into a password field, never rendered back, and never logged.
// Revoking is the way to undo it: the route "turns the login off AND ends its
// sessions", because leaving a session alive would keep a revoked contact
// working until it expired.

import { KeyRound, Users } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface SupplierContact {
  id: string;
  supplier_id: string;
  supplier_name: string;
  full_name: string;
  email: string;
  is_active: boolean;
  invited_at: string;
  last_seen_at?: string;
}

interface Supplier {
  id: string;
  legal_name?: string;
  name?: string;
}

function PortalScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('portal.manage');

  const { data, isLoading, error, refetch } = useApiList<SupplierContact>(
    scope ? '/portal/contacts' : null,
    { company_id: scope?.company_id },
  );

  const suppliers = useApiList<Supplier>(
    scope && mayManage ? '/suppliers' : null,
    { company_id: scope?.company_id },
  );

  const [inviting, setInviting] = useState(false);
  const [supplierId, setSupplierId] = useState('');
  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];

  async function invite() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post(`/portal/contacts?company_id=${scope.company_id}`, {
        supplier_id: supplierId,
        full_name: fullName,
        email,
        password,
      });
      setInviting(false);
      setSupplierId('');
      setFullName('');
      setEmail('');
      // Cleared rather than kept: there is no reason for a chosen password to
      // stay in the page after it has been sent.
      setPassword('');
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(contact: SupplierContact) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.delete(`/portal/contacts/${contact.id}?company_id=${scope.company_id}`);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<SupplierContact>[] = [
    {
      key: 'person',
      header: t('nx.pt.person'),
      primary: true,
      cell: (c) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {c.full_name}
            {!c.is_active ? (
              <Badge tone="neutral">{t('nx.pt.revoked')}</Badge>
            ) : null}
          </span>
          <span className="text-caption text-muted" dir="ltr">
            {c.email}
          </span>
        </span>
      ),
    },
    {
      key: 'supplier',
      header: t('nx.pt.supplier'),
      cell: (c) => c.supplier_name,
    },
    {
      key: 'invited',
      header: t('nx.pt.invited'),
      width: 'w-28',
      cell: (c) => <time dateTime={c.invited_at}>{c.invited_at.slice(0, 10)}</time>,
    },
    {
      key: 'seen',
      header: t('nx.pt.lastSeen'),
      width: 'w-32',
      // Never signed in is a fact about the invitation, not a blank cell: it
      // usually means the email never arrived.
      cell: (c) =>
        c.last_seen_at ? (
          <time dateTime={c.last_seen_at} className="text-muted">
            {c.last_seen_at.slice(0, 10)}
          </time>
        ) : (
          <Badge tone="caution">{t('nx.pt.neverSignedIn')}</Badge>
        ),
    },
  ];

  if (mayManage) {
    columns.push({
      key: 'revoke',
      header: t('nx.pt.accessHeader'),
      width: 'w-28',
      cell: (c) =>
        !c.is_active ? (
          <span className="text-muted">—</span>
        ) : (
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => void revoke(c)}
          >
            {t('nx.pt.revokeAction')}
          </Button>
        ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.pt.title')}
        description={t('nx.pt.subtitle')}
        actions={
          mayManage && !inviting ? (
            <Button onClick={() => setInviting(true)}>{t('nx.pt.invite')}</Button>
          ) : null
        }
      />

      {/* The customer half, stated rather than left as an absence somebody
          hunts for. */}
      <Panel title={t('nx.pt.customersTitle')} className="mb-4">
        <div className="flex items-start gap-3">
          <KeyRound className="mt-0.5 size-5 shrink-0 text-muted" aria-hidden />
          <div>
            <p className="text-body text-fg">{t('nx.pt.customersHow')}</p>
            <p className="mt-2 text-body text-muted">{t('nx.pt.customersWhy')}</p>
            <p className="mt-3 text-body">
              <Link
                href="/aftersales/requests"
                className="font-medium underline underline-offset-2"
              >
                {t('nx.pt.customersRequests')}
              </Link>
            </p>
          </div>
        </div>
      </Panel>

      <FormError message={actionError} fields={fieldErrors} className="mb-4" />

      {inviting ? (
        <Panel title={t('nx.pt.inviteTitle')} className="mb-4">
          <p className="mb-4 text-body text-muted">{t('nx.pt.inviteExplain')}</p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void invite();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="supplier_id" label={t('nx.pt.supplier')}>
                <Select
                  value={supplierId}
                  onChange={(e) => setSupplierId(e.target.value)}
                  required
                >
                  <option value="">{t('nx.pt.chooseSupplier')}</option>
                  {(suppliers.data?.data ?? []).map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.legal_name ?? s.name ?? s.id}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="full_name" label={t('nx.pt.person')}>
                <Input
                  value={fullName}
                  onChange={(e) => setFullName(e.target.value)}
                  autoComplete="off"
                  required
                />
              </Field>
              <Field name="email" label={t('nx.pt.email')}>
                <Input
                  type="email"
                  dir="ltr"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoComplete="off"
                  required
                />
              </Field>
              <Field
                name="password"
                label={t('nx.pt.password')}
                hint={t('nx.pt.passwordHint')}
              >
                {/* A password the shop chooses for somebody else. Masked, not
                    autofilled from the operator's own saved credentials, and
                    never rendered back once sent. */}
                <Input
                  type="password"
                  dir="ltr"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                  required
                />
              </Field>
            </div>

            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.pt.saving')}>
                {t('nx.pt.sendInvite')}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setInviting(false);
                  setPassword('');
                }}
              >
                {t('nx.pt.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      <h2 className="mb-3 text-label font-medium text-fg">
        {t('nx.pt.suppliersTitle')}
      </h2>

      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Users}
          title={t('nx.pt.emptyTitle')}
          description={t('nx.pt.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<SupplierContact>
          rows={rows}
          columns={columns}
          rowKey={(c) => c.id}
          caption={t('nx.pt.caption')}
        />
      ) : null}
    </>
  );
}

export default function PortalPage() {
  return (
    <RequirePermission anyOf={['portal.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PortalScreen />
      </Suspense>
    </RequirePermission>
  );
}
