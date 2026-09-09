'use client';

// Amending somebody, and changing what they may do.
//
// # Why this had to be built
//
// The people screen could add somebody, suspend them, restore them and issue a
// new one-time password. It could not correct a name, change a sign-in address,
// give somebody a second role, or take a role away — and the routes for all
// four had existed since A6.2.
//
// The role half is the part that matters. `identity.manage_roles` was a
// grantable permission whose only exercise was choosing a role at the moment a
// person was created. Promote a cashier to a supervisor, move somebody between
// branches, take an approval limit away from a leaver still on the payroll:
// none of it was possible without deleting the person, which the product
// refuses to do because their name is on the invoices they rang up.
//
// # A role is not a checkbox, it is a grant with a shape
//
// `POST /people/{id}/roles` takes the role and where it applies: a company, a
// set of stores, a set of warehouses, an amount limit and a time window. The
// form asks for the company and the limit, which are the two an owner actually
// sets; the rest are left to the assignment as the server creates it. Offering
// six empty boxes for scopes most businesses never narrow is how a screen stops
// being used.
//
// # Taking the last role away
//
// The server refuses to remove your OWN last assignment — that leaves somebody
// signed in and unable to act. The refusal arrives as a sentence and is shown
// as one; this screen does not try to predict it, because the rule is about who
// is asking and the server is the one that knows.

import { Trash2 } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import type { Assignment, Person, RoleOption } from '@/lib/people/roles';

/** Where an assignment applies, in one line. */
function scopeOf(a: Assignment, t: ReturnType<typeof useT>): string {
  const parts: string[] = [];
  if (a.store_ids.length > 0) {
    parts.push(t('nx.usr.scopeStores', { n: String(a.store_ids.length) }));
  }
  if (a.warehouse_ids.length > 0) {
    parts.push(t('nx.usr.scopeWarehouses', { n: String(a.warehouse_ids.length) }));
  }
  if (a.amount_limit) parts.push(t('nx.usr.scopeLimit', { amount: a.amount_limit }));
  if (a.valid_until) parts.push(t('nx.usr.scopeUntil', { date: a.valid_until.slice(0, 10) }));
  return parts.join(' · ');
}

export function PersonPanel({
  person,
  companyId,
  assignable,
  mayAmend,
  mayAssign,
  onChanged,
  onClose,
}: {
  person: Person;
  companyId: string;
  assignable: readonly RoleOption[];
  mayAmend: boolean;
  mayAssign: boolean;
  onChanged: () => void;
  onClose: () => void;
}) {
  const t = useT();

  const [fullName, setFullName] = useState(person.full_name);
  const [emailAddr, setEmailAddr] = useState(person.email);
  const [phone, setPhone] = useState(person.phone ?? '');
  const [roleID, setRoleID] = useState('');
  const [amountLimit, setAmountLimit] = useState('');

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const q = `?company_id=${companyId}`;

  async function run(what: string, fn: () => Promise<void>) {
    setBusy(what);
    setError(null);
    setFieldErrors({});
    try {
      await fn();
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  const amended =
    fullName.trim() !== person.full_name ||
    emailAddr.trim() !== person.email ||
    phone.trim() !== (person.phone ?? '');

  return (
    <Panel
      className="mb-5"
      title={t('nx.usr.amendTitle', { name: person.full_name })}
      description={t('nx.usr.amendHint')}
      actions={
        <Button variant="ghost" onClick={onClose}>
          {t('nx.usr.close')}
        </Button>
      }
    >
      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {mayAmend ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
              hint={t('nx.usr.emailAmendHint')}
              error={fieldErrors.email}
              required
            >
              <Input
                type="email"
                value={emailAddr}
                onChange={(e) => setEmailAddr(e.target.value)}
              />
            </Field>
            <Field name="phone" label={t('nx.usr.phone')} error={fieldErrors.phone}>
              <Input
                type="tel"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
              />
            </Field>
          </div>
          <div className="mt-4">
            <Button
              variant="primary"
              disabled={busy !== null || !amended || !fullName.trim() || !emailAddr.trim()}
              onClick={() =>
                void run('details', async () => {
                  await api.put(`/people/${person.id}${q}`, {
                    full_name: fullName.trim(),
                    email: emailAddr.trim(),
                    phone: phone.trim(),
                  });
                })
              }
            >
              {busy === 'details' ? t('nx.usr.saving') : t('nx.usr.saveDetails')}
            </Button>
          </div>
        </>
      ) : null}

      <h3 className="mt-6 mb-1 text-card-title font-semibold text-fg">
        {t('nx.usr.rolesHeld')}
      </h3>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.usr.rolesHeldHint')}
      </p>

      {person.roles.length === 0 ? (
        // Not an empty table. Somebody with no role can sign in and do
        // nothing, which reads to them as a broken account.
        <p className="mb-4 text-body text-caution-fg">{t('nx.usr.noRoleAtAll')}</p>
      ) : (
        <ul className="mb-4 flex flex-col gap-2">
          {person.roles.map((a) => {
            const where = scopeOf(a, t);
            return (
              <li
                key={a.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-sm border border-line px-3 py-2"
              >
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="font-medium">{a.role_name}</span>
                  {where ? (
                    <span className="text-caption text-muted">{where}</span>
                  ) : (
                    <span className="text-caption text-muted">
                      {t('nx.usr.scopeWholeBusiness')}
                    </span>
                  )}
                </span>
                {mayAssign ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy !== null}
                    onClick={() =>
                      void run(a.id, async () => {
                        await api.delete(`/people/roles/${a.id}${q}`);
                      })
                    }
                  >
                    <Trash2 aria-hidden="true" className="size-4" />
                    {t('nx.usr.removeRole')}
                  </Button>
                ) : (
                  <Badge>{a.role_key}</Badge>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {mayAssign ? (
        <div className="grid items-end gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field
            name="role_id"
            label={t('nx.usr.giveRole')}
            hint={t('nx.usr.giveRoleHint')}
            error={fieldErrors.role_id}
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
          <Field
            name="amount_limit"
            label={t('nx.usr.amountLimit')}
            hint={t('nx.usr.amountLimitHint')}
            error={fieldErrors.amount_limit}
          >
            <Input
              value={amountLimit}
              onChange={(e) => setAmountLimit(e.target.value)}
              inputMode="decimal"
              className="num"
            />
          </Field>
          <div>
            <Button
              disabled={busy !== null || !roleID}
              onClick={() =>
                void run('assign', async () => {
                  await api.post(`/people/${person.id}/roles${q}`, {
                    role_id: roleID,
                    company_id: companyId,
                    amount_limit: amountLimit.trim(),
                  });
                  setRoleID('');
                  setAmountLimit('');
                })
              }
            >
              {busy === 'assign' ? t('nx.usr.saving') : t('nx.usr.addRole')}
            </Button>
          </div>
        </div>
      ) : null}
    </Panel>
  );
}
