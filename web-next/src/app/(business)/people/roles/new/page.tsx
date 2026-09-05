'use client';

// Building a role.
//
// # Copying is the supported way to start from a built-in
//
// `?copy=<id>` fills the draft from another role. The server refuses to edit a
// built-in — it keeps them current as the product grows, and an edited copy
// would freeze at today's shape — so copying is not a shortcut, it is the
// route. `copyOf` keeps only the permissions this person can actually grant, so
// a manager copying the Owner role gets a saveable draft rather than a refusal
// naming permissions they never chose.
//
// # The count is the thing worth showing while typing
//
// Not a progress bar and not a preview of every screen the role unlocks — the
// number of permissions, and which sections they are in. That is what somebody
// checks before handing a role to a person.

import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PermissionPicker } from '@/components/people/permission-picker';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  copyOf,
  roleProblem,
  ungrantable,
  type CustomRole,
  type PermissionOption,
  type RoleOption,
} from '@/lib/people/roles';

const PROBLEM: Record<string, Key> = {
  no_name: 'nx.role.needName',
  nothing_ticked: 'nx.role.needSomething',
  cannot_grant: 'nx.role.needHeld',
};

function NewRoleScreen() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const scope = useCompanyScope();
  const copyFrom = params.get('copy');

  const [name, setName] = useState('');
  const [nameAr, setNameAr] = useState('');
  const [description, setDescription] = useState('');
  const [chosen, setChosen] = useState<string[]>([]);
  const [seeded, setSeeded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const catalogue = useApiList<PermissionOption>(
    scope ? '/permissions' : null,
    scope ?? undefined,
  );
  const roles = useApiList<RoleOption>(
    scope && copyFrom ? '/people/roles' : null,
    scope ?? undefined,
  );

  const all = catalogue.data?.data ?? [];

  // Filled once, from the role being copied. Once, so a later refetch does not
  // wipe out what somebody has since unticked.
  useEffect(() => {
    if (seeded || !copyFrom || all.length === 0) return;
    const source = (roles.data?.data ?? []).find((r) => r.id === copyFrom);
    if (!source) return;
    setName(t('nx.role.copyOf', { name: source.name }));
    setDescription(source.description ?? '');
    setChosen(copyOf(source, all));
    setSeeded(true);
  }, [seeded, copyFrom, all, roles.data, t]);

  const state = roleProblem(name, chosen, all);
  const refused = ungrantable(chosen, all);

  async function save() {
    if (!scope || state !== 'none') return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<{ role: CustomRole }>(
        `/roles?company_id=${scope.company_id}`,
        {
          name: name.trim(),
          name_ar: nameAr.trim(),
          description: description.trim(),
          permissions: chosen,
        },
      );
      router.push(`/people/roles/${out.role.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={copyFrom ? t('nx.role.copyTitle') : t('nx.role.newTitle')}
        description={t('nx.role.newSubtitle')}
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0">
          <Panel className="mb-5" title={t('nx.role.naming')}>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                name="name"
                label={t('nx.role.name')}
                hint={t('nx.role.nameHint')}
                error={fieldErrors.name}
                required
              >
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </Field>
              <Field name="name_ar" label={t('nx.role.nameAr')} error={fieldErrors.name_ar}>
                <Input
                  value={nameAr}
                  onChange={(e) => setNameAr(e.target.value)}
                  dir="rtl"
                />
              </Field>
              <Field
                name="description"
                label={t('nx.role.description')}
                hint={t('nx.role.descriptionHint')}
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

          <h2 className="mb-3 text-card-title font-semibold text-fg">
            {t('nx.role.whatItCanDo')}
          </h2>
          {catalogue.isLoading && all.length === 0 ? (
            <div className="h-64" aria-busy="true" />
          ) : (
            <PermissionPicker all={all} chosen={chosen} onChange={setChosen} />
          )}
        </div>

        <aside>
          <Panel title={t('nx.role.summary')} className="lg:sticky lg:top-4">
            <p className="text-display font-semibold tracking-tight num">
              {chosen.length}
            </p>
            <p className="text-label text-muted">
              {t('nx.role.chosenOf', { total: String(all.length) })}
            </p>

            {refused.length > 0 ? (
              <p className="mt-4 max-w-prose text-caption text-critical-fg">
                {t('nx.role.wouldBeRefused', { count: String(refused.length) })}
              </p>
            ) : null}

            <div className="mt-5 flex flex-col gap-2">
              <Button
                variant="primary"
                onClick={() => void save()}
                disabled={busy || state !== 'none'}
              >
                {busy ? t('nx.role.saving') : t('nx.role.save')}
              </Button>
              <Button variant="ghost" onClick={() => router.push('/people/roles')}>
                {t('nx.role.cancel')}
              </Button>
            </div>

            {state !== 'none' ? (
              <p className="mt-3 text-caption text-muted">{t(PROBLEM[state] as Key)}</p>
            ) : null}
          </Panel>
        </aside>
      </div>
    </>
  );
}

export default function NewRolePage() {
  return (
    <RequirePermission anyOf={['identity.manage_roles']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewRoleScreen />
      </Suspense>
    </RequirePermission>
  );
}
