'use client';

// Who can sign in, and what they may do.
//
// # Four states, not two
//
// Suspended, locked, never-signed-in and working are four different situations
// with four different next actions: an administrator restores the first, the
// sign-in system's counter clears the second, somebody collects a one-time
// password for the third. Collapsing them into "inactive" loses the only part
// an owner can act on.
//
// # A one-time password is shown once
//
// A4.2 makes the irreversibility "a security requirement, not just a policy
// choice": it is hashed like every other and cannot be retrieved. So the screen
// shows it in a panel that stays until dismissed, and says plainly that closing
// it loses the password rather than implying it can be found again later.
//
// # Somebody is never deleted
//
// Their name is on the invoices they rang up. Suspending stops them signing in
// and leaves the record, which is the same reasoning that keeps a departed
// employee's payslips readable.

import { UserPlus, Users } from 'lucide-react';
import { useRouter } from 'next/navigation';
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
import { useT, type Key } from '@/lib/i18n/locale';
import { signInState, type Person, type RoleOption } from '@/lib/people/roles';

const STATE_LABEL: Record<string, Key> = {
  suspended: 'nx.usr.suspended',
  locked: 'nx.usr.locked',
  never_signed_in: 'nx.usr.neverSignedIn',
  active: 'nx.usr.working',
};

const STATE_TONE: Record<string, 'neutral' | 'positive' | 'caution' | 'critical'> = {
  suspended: 'neutral',
  locked: 'critical',
  never_signed_in: 'caution',
  active: 'positive',
};

/** The one-time password, shown once and never again. */
function OneTimePassword({
  email,
  password,
  onDismiss,
}: {
  email: string;
  password: string;
  onDismiss: () => void;
}) {
  const t = useT();
  return (
    <section
      className="mb-6 rounded-md border border-caution/25 bg-caution-subtle p-4"
      aria-labelledby="otp-title"
    >
      <h2 id="otp-title" className="text-card-title font-semibold text-caution-fg">
        {t('nx.usr.otpTitle', { email })}
      </h2>
      <p className="mt-1 max-w-prose text-body text-caution-fg">
        {t('nx.usr.otpBody')}
      </p>
      <p className="num mt-3 rounded-sm border border-line bg-surface px-3 py-2 text-card-title font-semibold select-all">
        {password}
      </p>
      <div className="mt-3">
        <Button size="sm" onClick={onDismiss}>
          {t('nx.usr.otpDismiss')}
        </Button>
      </div>
    </section>
  );
}

function UsersScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayCreate = grants.can('identity.create');
  const mayAssign = grants.can('identity.manage_roles');

  const people = useApiList<Person>(scope ? '/people' : null, scope ?? undefined);
  const roles = useApiList<RoleOption>(
    scope ? '/people/roles' : null,
    scope ?? undefined,
  );

  const [adding, setAdding] = useState(false);
  const [email, setEmail] = useState('');
  const [fullName, setFullName] = useState('');
  const [phone, setPhone] = useState('');
  const [roleID, setRoleID] = useState('');
  const [issued, setIssued] = useState<{ email: string; password: string } | null>(
    null,
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const rows = people.data?.data ?? [];
  // Only the roles this person can actually hand over. One they cannot is
  // shown on the roles screen with its reason; offering it here would collect
  // a refusal at the moment somebody is being added.
  const assignable = (roles.data?.data ?? []).filter((r) => r.assignable);

  async function add() {
    if (!scope || !email.trim() || !fullName.trim() || !roleID) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<{
        person: { email: string };
        temporary_password: string;
      }>(`/people?company_id=${scope.company_id}`, {
        email: email.trim(),
        full_name: fullName.trim(),
        phone: phone.trim(),
        role_id: roleID,
        company_id: scope.company_id,
      });
      setIssued({
        email: out.person.email,
        password: out.temporary_password,
      });
      setEmail('');
      setFullName('');
      setPhone('');
      setAdding(false);
      void people.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(person: Person, active: boolean) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/people/${person.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      void people.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function resetPassword(person: Person) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      const out = await api.post<{ temporary_password: string }>(
        `/people/${person.id}/reset-password?company_id=${scope.company_id}`,
        {},
      );
      setIssued({ email: person.email, password: out.temporary_password });
      void people.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Person>[] = [
    {
      key: 'person',
      header: t('nx.usr.colPerson'),
      primary: true,
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{p.full_name}</span>
          <span className="text-caption text-muted">{p.email}</span>
        </span>
      ),
    },
    {
      key: 'roles',
      header: t('nx.usr.colRoles'),
      cell: (p) =>
        p.roles.length === 0 ? (
          // A person with no role can sign in and do nothing, which reads to
          // them as a broken account rather than a pending one.
          <Badge tone="caution">{t('nx.usr.noRole')}</Badge>
        ) : (
          <span className="flex flex-wrap gap-1.5">
            {p.roles.map((a) => (
              <Badge key={a.id}>{a.role_name}</Badge>
            ))}
          </span>
        ),
    },
    {
      key: 'state',
      header: t('nx.usr.colState'),
      width: 'w-40',
      cell: (p) => {
        const state = signInState(p);
        return <Badge tone={STATE_TONE[state]}>{t(STATE_LABEL[state] as Key)}</Badge>;
      },
    },
    {
      key: 'seen',
      header: t('nx.usr.colLastSeen'),
      secondary: true,
      width: 'w-40',
      cell: (p) =>
        p.last_login_at ? (
          <time dateTime={p.last_login_at} className="num text-muted">
            {p.last_login_at.slice(0, 10)}
          </time>
        ) : (
          <span className="text-muted">{t('nx.usr.never')}</span>
        ),
    },
    {
      key: 'actions',
      header: t('nx.usr.colActions'),
      width: 'w-56',
      cell: (p) =>
        mayCreate ? (
          <span className="flex flex-wrap gap-2">
            {p.status === 'active' ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void setActive(p, false)}
              >
                {t('nx.usr.suspend')}
              </Button>
            ) : (
              <Button size="sm" disabled={busy} onClick={() => void setActive(p, true)}>
                {t('nx.usr.restore')}
              </Button>
            )}
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => void resetPassword(p)}
            >
              {t('nx.usr.resetPassword')}
            </Button>
          </span>
        ) : null,
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.usr.title')}
        description={t('nx.usr.subtitle')}
        actions={
          <>
            {mayAssign ? (
              <Button onClick={() => router.push('/people/roles')}>
                {t('nx.usr.manageRoles')}
              </Button>
            ) : null}
            {mayCreate ? (
              <Button variant="primary" onClick={() => setAdding((v) => !v)}>
                <UserPlus aria-hidden="true" className="size-4" />
                {t('nx.usr.add')}
              </Button>
            ) : null}
          </>
        }
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {issued ? (
        <OneTimePassword
          email={issued.email}
          password={issued.password}
          onDismiss={() => setIssued(null)}
        />
      ) : null}

      {adding ? (
        <Panel
          className="mb-5"
          title={t('nx.usr.addTitle')}
          description={t('nx.usr.addHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              name="full_name"
              label={t('nx.usr.fullName')}
              error={fieldErrors.full_name}
              required
            >
              <Input value={fullName} onChange={(e) => setFullName(e.target.value)} />
            </Field>
            <Field
              name="email"
              label={t('nx.usr.email')}
              hint={t('nx.usr.emailHint')}
              error={fieldErrors.email}
              required
            >
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Field name="phone" label={t('nx.usr.phone')} error={fieldErrors.phone}>
              <Input type="tel" value={phone} onChange={(e) => setPhone(e.target.value)} />
            </Field>
            <Field
              name="role_id"
              label={t('nx.usr.role')}
              hint={t('nx.usr.roleHint')}
              error={fieldErrors.role_id}
              required
            >
              <Select value={roleID} onChange={(e) => setRoleID(e.target.value)}>
                <option value="">{t('nx.usr.chooseRole')}</option>
                {assignable.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <div className="mt-4 flex gap-3">
            <Button
              variant="primary"
              disabled={busy || !email.trim() || !fullName.trim() || !roleID}
              onClick={() => void add()}
            >
              {busy ? t('nx.usr.adding') : t('nx.usr.addPerson')}
            </Button>
            <Button variant="ghost" onClick={() => setAdding(false)}>
              {t('nx.usr.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {people.error ? (
        <ErrorState error={people.error} onRetry={() => void people.refetch()} />
      ) : null}
      {people.isLoading && !people.data ? <TableSkeleton columns={5} /> : null}

      {!people.isLoading && !people.error && rows.length === 0 ? (
        <EmptyState
          icon={Users}
          title={t('nx.usr.emptyTitle')}
          description={t('nx.usr.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.usr.caption')}
          columns={columns}
          rows={rows}
          rowKey={(p) => p.id}
        />
      ) : null}
    </>
  );
}

export default function UsersPage() {
  return (
    <RequirePermission anyOf={['identity.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <UsersScreen />
      </Suspense>
    </RequirePermission>
  );
}
