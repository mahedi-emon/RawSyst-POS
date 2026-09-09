'use client';

// Who administers the platform (A4).
//
// # Why this screen had to exist
//
// A platform administrator is a user with no tenant, and exactly one thing in
// the product could create one: `cmd/bootstrap`, which refuses the moment one
// exists. That refusal is what makes it safe to leave a copy on a server, and
// it also meant a deployment had one administrator for ever. Its own error
// text said "use Super Admin to add another" and Super Admin had no such
// screen -- so adding a colleague, removing somebody who left, or correcting
// the address the first account was created under all ended in SQL against
// production.
//
// # An address is corrected, not replaced
//
// The workaround was to make a second administrator and abandon the first.
// That leaves two accounts with full platform authority where one person was
// intended, and the abandoned one still works: a live credential nobody
// watches. So the email is edited in place, and the audit log carries the
// before and the after.
//
// # What the list deliberately shows
//
// Never signed in, and no second factor. Both are properties of an account
// that can read every tenant's billing and change what the product believes
// the law to be, and neither is visible anywhere else.

import { ShieldCheck } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Operator {
  id: string;
  email: string;
  full_name: string;
  status: string;
  must_change_password: boolean;
  mfa_enabled: boolean;
  last_login_at: string | null;
  created_at: string;
  self: boolean;
}

function OperatorsScreen() {
  const t = useT();
  const { data, isLoading, error, refetch } = useApiList<Operator>('/platform/operators');

  const [adding, setAdding] = useState(false);
  const [email, setEmail] = useState('');
  const [fullName, setFullName] = useState('');

  /** The address being corrected, keyed by operator id. */
  const [editing, setEditing] = useState<Operator | null>(null);
  const [newEmail, setNewEmail] = useState('');

  /** The operator whose password is being reset, and why. */
  const [resetting, setResetting] = useState<Operator | null>(null);
  const [resetReason, setResetReason] = useState('');

  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  // Shown once, then gone from the client for good. Nothing stores it: the
  // column holds an irreversible hash, so this is the only moment it exists in
  // readable form anywhere.
  const [issued, setIssued] = useState<{ email: string; password: string } | null>(null);

  const rows = data?.data ?? [];
  const activeCount = rows.filter((x) => x.status === 'active').length;

  function reset() {
    setAdding(false);
    setEditing(null);
    setResetting(null);
    setEmail('');
    setFullName('');
    setNewEmail('');
    setResetReason('');
    setFormError(null);
    setFieldErrors(null);
  }

  async function add() {
    setBusy(true);
    setFormError(null);
    setFieldErrors(null);
    try {
      const res = await api.post<{ temporary_password: string }>('/platform/operators', {
        email,
        full_name: fullName,
      });
      setIssued({ email: email.trim().toLowerCase(), password: res.temporary_password });
      reset();
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function saveEmail() {
    if (!editing) return;
    setBusy(true);
    setFormError(null);
    setFieldErrors(null);
    try {
      await api.put(`/platform/operators/${editing.id}/email`, { email: newEmail });
      reset();
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  // A new one-time password for a colleague who has lost theirs.
  //
  // `cmd/bootstrap` tells an operator to "reset a password there" and there was
  // nowhere: `POST /platform/users/{id}/reset-password` had no caller, so the
  // only recovery for a locked-out administrator was the forgotten-password
  // email flow, which is no help when the address itself is the problem.
  //
  // The reason is required and is not decoration. This hands somebody full
  // platform authority; the audit entry should say why rather than only that it
  // happened, and the server refuses an empty one.
  async function resetPassword() {
    if (!resetting) return;
    setBusy(true);
    setFormError(null);
    setFieldErrors(null);
    try {
      const res = await api.post<{ temporary_password: string }>(
        `/platform/users/${resetting.id}/reset-password`,
        { reason: resetReason.trim() },
      );
      setIssued({ email: resetting.email, password: res.temporary_password });
      reset();
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setStatus(op: Operator, status: 'active' | 'disabled') {
    setBusy(true);
    setFormError(null);
    try {
      await api.put(`/platform/operators/${op.id}/status`, { status });
      void refetch();
    } catch (e) {
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Operator>[] = [
    {
      key: 'name',
      header: t('nx.plat.opName'),
      primary: true,
      cell: (x) => (
        <span className="flex flex-wrap items-center gap-2">
          {x.full_name}
          {x.self ? <Badge tone="info">{t('nx.plat.opYou')}</Badge> : null}
          {/* A word, never colour alone: this list is read by whoever is on
              shift and printed for an access review. */}
          {x.status !== 'active' ? (
            <Badge tone="neutral">{t('nx.plat.opDisabled')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'email',
      header: t('nx.plat.opEmail'),
      cell: (x) => (
        <span dir="ltr" className="break-all">
          {x.email}
        </span>
      ),
    },
    {
      key: 'mfa',
      header: t('nx.plat.opMfa'),
      width: 'w-32',
      cell: (x) =>
        x.mfa_enabled ? (
          <Badge tone="positive">{t('nx.plat.opMfaOn')}</Badge>
        ) : (
          // An account with full platform authority and no second factor is
          // the row an access review is looking for.
          <Badge tone="caution">{t('nx.plat.opMfaOff')}</Badge>
        ),
    },
    {
      key: 'signin',
      header: t('nx.plat.opLastSignIn'),
      width: 'w-44',
      secondary: true,
      cell: (x) =>
        x.last_login_at ? (
          <time dateTime={x.last_login_at} className="num">
            {x.last_login_at.slice(0, 10)}
          </time>
        ) : (
          <Badge tone="caution">{t('nx.plat.opNeverSignedIn')}</Badge>
        ),
    },
    {
      key: 'actions',
      header: t('nx.plat.opActions'),
      width: 'w-56',
      cell: (x) => (
        <span className="flex flex-wrap gap-1">
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => {
              reset();
              setEditing(x);
              setNewEmail(x.email);
            }}
          >
            {t('nx.plat.opChangeEmail')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => {
              reset();
              setResetting(x);
            }}
          >
            {t('nx.plat.opResetPassword')}
          </Button>
          {x.status === 'active' ? (
            <Button
              size="sm"
              variant="ghost"
              // Both refusals are enforced by the service; disabling the
              // controls as well means the reader is not offered an action
              // that can only fail.
              disabled={busy || x.self || activeCount <= 1}
              onClick={() => void setStatus(x, 'disabled')}
            >
              {t('nx.plat.opDisable')}
            </Button>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => void setStatus(x, 'active')}
            >
              {t('nx.plat.opEnable')}
            </Button>
          )}
        </span>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.plat.opTitle')}
        description={t('nx.plat.opSubtitle')}
        actions={
          !adding && !editing && !resetting ? (
            <Button
              onClick={() => {
                reset();
                setAdding(true);
              }}
            >
              {t('nx.plat.opAdd')}
            </Button>
          ) : null
        }
      />

      {issued ? (
        <Panel title={t('nx.plat.opIssuedTitle')} className="mb-4">
          <p className="max-w-prose text-body text-muted">
            {t('nx.plat.opIssuedBody', { email: issued.email })}
          </p>
          <p
            dir="ltr"
            className="num mt-3 rounded-md border border-line bg-surface-sunken px-3 py-2 text-lede select-all"
          >
            {issued.password}
          </p>
          <div className="mt-4">
            <Button variant="ghost" onClick={() => setIssued(null)}>
              {t('nx.plat.opIssuedDone')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {adding ? (
        <Panel title={t('nx.plat.opAddTitle')} className="mb-4">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void add();
            }}
          >
            <FormError message={formError} fields={fieldErrors} className="mb-4" />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                name="full_name"
                label={t('nx.plat.opName')}
                hint={t('nx.plat.opNameHint')}
              >
                <Input
                  value={fullName}
                  onChange={(e) => setFullName(e.target.value)}
                  required
                />
              </Field>
              <Field
                name="email"
                label={t('nx.plat.opEmail')}
                hint={t('nx.plat.opEmailHint')}
              >
                <Input
                  type="email"
                  dir="ltr"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </Field>
            </div>
            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.plat.opSaving')}>
                {t('nx.plat.opAddSave')}
              </Button>
              <Button type="button" variant="ghost" onClick={reset}>
                {t('nx.plat.opCancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {editing ? (
        <Panel
          title={t('nx.plat.opEditTitle', { name: editing.full_name })}
          className="mb-4"
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void saveEmail();
            }}
          >
            <FormError message={formError} fields={fieldErrors} className="mb-4" />
            <Field
              name="email"
              label={t('nx.plat.opEmail')}
              hint={t('nx.plat.opChangeEmailHint')}
            >
              <Input
                type="email"
                dir="ltr"
                value={newEmail}
                onChange={(e) => setNewEmail(e.target.value)}
                required
              />
            </Field>
            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.plat.opSaving')}>
                {t('nx.plat.opChangeEmailSave')}
              </Button>
              <Button type="button" variant="ghost" onClick={reset}>
                {t('nx.plat.opCancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {resetting ? (
        <Panel
          title={t('nx.plat.opResetTitle', { name: resetting.full_name })}
          description={t('nx.plat.opResetHint')}
          className="mb-4"
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void resetPassword();
            }}
          >
            <FormError message={formError} fields={fieldErrors} className="mb-4" />
            <Field
              name="reason"
              label={t('nx.plat.opResetReason')}
              hint={t('nx.plat.opResetReasonHint')}
            >
              <Input
                value={resetReason}
                onChange={(e) => setResetReason(e.target.value)}
                required
              />
            </Field>
            <div className="mt-6 flex flex-wrap gap-2">
              <Button
                type="submit"
                busy={busy}
                busyLabel={t('nx.plat.opSaving')}
                disabled={resetReason.trim() === ''}
              >
                {t('nx.plat.opResetSave')}
              </Button>
              <Button type="button" variant="ghost" onClick={reset}>
                {t('nx.plat.opCancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {!adding && !editing && formError ? (
        <FormError message={formError} className="mb-4" />
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.plat.opEmptyTitle')}
          description={t('nx.plat.opEmptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Operator>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.plat.opCaption')}
        />
      ) : null}
    </>
  );
}

export default function OperatorsPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <OperatorsScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
