'use client';

// Editing a role the business made.
//
// # A built-in one opens read-only, and says why
//
// Reaching this screen for a built-in role is not an error — a link can be
// shared, a URL typed. It shows the role and offers Copy, because that is what
// the server will accept: it keeps the shipped roles current as the product
// grows, and an edited copy would freeze at today's shape.
//
// # Changing what a role can do changes what people can do
//
// `in_use` says how many hold it. Unticking a permission takes it away from all
// of them at once, which is the one thing about this screen worth saying out
// loud before somebody presses save.

import { useParams, useRouter } from 'next/navigation';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PermissionPicker } from '@/components/people/permission-picker';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  roleProblem,
  ungrantable,
  type CustomRole,
  type PermissionOption,
} from '@/lib/people/roles';

const PROBLEM: Record<string, Key> = {
  no_name: 'nx.role.needName',
  nothing_ticked: 'nx.role.needSomething',
  cannot_grant: 'nx.role.needHeld',
};

function EditRoleScreen() {
  const t = useT();
  const router = useRouter();
  const params = useParams<{ roleID: string }>();
  const scope = useCompanyScope();

  const { data, isLoading, error, refetch } = useApi<{ role: CustomRole }>(
    scope ? `/roles/${params.roleID}` : null,
    scope ?? undefined,
  );
  const catalogue = useApiList<PermissionOption>(
    scope ? '/permissions' : null,
    scope ?? undefined,
  );

  const [name, setName] = useState('');
  const [nameAr, setNameAr] = useState('');
  const [description, setDescription] = useState('');
  const [chosen, setChosen] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);

  const role = data?.role;
  const all = catalogue.data?.data ?? [];

  useEffect(() => {
    if (loaded || !role) return;
    setName(role.name);
    setNameAr(role.name_ar ?? '');
    setDescription(role.description ?? '');
    setChosen(role.permissions);
    setLoaded(true);
  }, [loaded, role]);

  const state = roleProblem(name, chosen, all);
  const refused = ungrantable(chosen, all);

  async function save() {
    if (!scope || !role || state !== 'none') return;
    setBusy(true);
    setSaveError(null);
    setFieldErrors({});
    setSaved(false);
    try {
      await api.put(`/roles/${role.id}?company_id=${scope.company_id}`, {
        name: name.trim(),
        name_ar: nameAr.trim(),
        description: description.trim(),
        permissions: chosen,
      });
      setSaved(true);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading && !data) return <div className="h-64" aria-busy="true" />;
  if (!role) return null;

  return (
    <>
      <PageHeader
        title={role.name}
        description={
          role.is_system ? t('nx.role.builtInSubtitle') : t('nx.role.editSubtitle')
        }
        actions={
          role.is_system ? (
            <Button
              variant="primary"
              onClick={() => router.push(`/people/roles/new?copy=${role.id}`)}
            >
              {t('nx.role.copy')}
            </Button>
          ) : null
        }
      />

      <FormError message={saveError} fields={fieldErrors} className="mb-4" />

      <div className="mb-5 flex flex-wrap items-center gap-2">
        {role.is_system ? (
          <Badge>{t('nx.role.builtIn')}</Badge>
        ) : (
          <Badge tone="info">{t('nx.role.yourOwn')}</Badge>
        )}
        <Badge tone={role.in_use > 0 ? 'caution' : 'neutral'}>
          {t('nx.role.heldBy', { count: String(role.in_use) })}
        </Badge>
        {saved ? <Badge tone="positive">{t('nx.role.saved')}</Badge> : null}
      </div>

      {role.is_system ? (
        <section
          className="mb-6 rounded-md border border-line bg-surface-sunken p-4"
          aria-labelledby="builtin-note"
        >
          <h2 id="builtin-note" className="text-card-title font-semibold text-fg">
            {t('nx.role.builtInTitle')}
          </h2>
          <p className="mt-1 max-w-prose text-body text-muted">
            {t('nx.role.builtInBody')}
          </p>
        </section>
      ) : null}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0">
          {!role.is_system ? (
            <Panel className="mb-5" title={t('nx.role.naming')}>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field
                  name="name"
                  label={t('nx.role.name')}
                  error={fieldErrors.name}
                  required
                >
                  <Input value={name} onChange={(e) => setName(e.target.value)} />
                </Field>
                <Field name="name_ar" label={t('nx.role.nameAr')}>
                  <Input
                    value={nameAr}
                    onChange={(e) => setNameAr(e.target.value)}
                    dir="rtl"
                  />
                </Field>
                <Field
                  name="description"
                  label={t('nx.role.description')}
                  className="sm:col-span-2"
                >
                  <Textarea
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    rows={2}
                  />
                </Field>
              </div>
            </Panel>
          ) : null}

          <h2 className="mb-3 text-card-title font-semibold text-fg">
            {t('nx.role.whatItCanDo')}
          </h2>
          <PermissionPicker
            all={all}
            chosen={chosen}
            onChange={setChosen}
            disabled={role.is_system}
          />
        </div>

        <aside>
          <Panel title={t('nx.role.summary')} className="lg:sticky lg:top-4">
            <p className="num text-display font-semibold tracking-tight">
              {chosen.length}
            </p>
            <p className="text-label text-muted">
              {t('nx.role.chosenOf', { total: String(all.length) })}
            </p>

            {!role.is_system && role.in_use > 0 ? (
              // The one thing worth saying out loud before saving.
              <p className="mt-4 max-w-prose text-caption text-caution-fg">
                {t('nx.role.changesAffect', { count: String(role.in_use) })}
              </p>
            ) : null}

            {refused.length > 0 ? (
              <p className="mt-3 max-w-prose text-caption text-critical-fg">
                {t('nx.role.wouldBeRefused', { count: String(refused.length) })}
              </p>
            ) : null}

            <div className="mt-5 flex flex-col gap-2">
              {!role.is_system ? (
                <Button
                  variant="primary"
                  onClick={() => void save()}
                  disabled={busy || state !== 'none'}
                >
                  {busy ? t('nx.role.saving') : t('nx.role.save')}
                </Button>
              ) : null}
              <Button variant="ghost" onClick={() => router.push('/people/roles')}>
                {t('nx.role.backToRoles')}
              </Button>
            </div>

            {!role.is_system && state !== 'none' ? (
              <p className="mt-3 text-caption text-muted">{t(PROBLEM[state] as Key)}</p>
            ) : null}
          </Panel>
        </aside>
      </div>
    </>
  );
}

export default function EditRolePage() {
  return (
    <RequirePermission anyOf={['identity.manage_roles']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <EditRoleScreen />
      </Suspense>
    </RequirePermission>
  );
}
