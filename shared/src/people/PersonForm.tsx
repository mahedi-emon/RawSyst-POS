// Adding somebody, and deciding what they may do.
//
// # Why the role picker shows what a role can do
//
// A6.2's whole point is that an Owner "decides exactly what every employee can
// see and do". A dropdown of twelve names does not let them decide anything:
// "Store Manager" and "Accountant" are labels, and the difference that matters
// — one can see the bank ledger, the other cannot — is invisible until somebody
// has already been given it.
//
// So choosing a role shows what it carries, in the reader's own language where
// the catalogue has the words and as the raw verb where it does not. That is
// the difference between delegating and guessing.
//
// # Why an unassignable role is shown at all
//
// A role the caller cannot hand over is listed, disabled, with the reason. A
// store manager who simply cannot see the Owner role will conclude the list is
// broken; one who sees it greyed out with "includes accounting.view, which your
// role does not" knows exactly what to ask for and whom to ask.
import { useState } from 'react';

import { useAuth } from '../auth/session';
import { Offline, RequestFailed } from '../api/client';
import { createPerson, type CreatedPerson, type RoleOption } from '../api/people';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

/** A permission's own name, where the catalogue has one.
 *
 * `sales.refund` reads as "Refund a sale" to somebody deciding whether to hand
 * it over, and as nothing at all to somebody who has never seen the codebase.
 * Where a key is missing the verb itself is shown rather than a blank — an
 * incomplete list is more useful than a shorter one that hides what is in it.
 */
function permissionLabel(permission: string, t: (k: Key) => string): string {
  const key = `perm.${permission}` as Key;
  const named = t(key);
  return named === key ? permission : named;
}

export function PersonForm({
  roles,
  onCreated,
  onCancel,
}: {
  roles: RoleOption[];
  onCreated: (made: CreatedPerson) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');
  const [phone, setPhone] = useState('');
  const [roleId, setRoleId] = useState('');
  const [amountLimit, setAmountLimit] = useState('');
  const [validUntil, setValidUntil] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});

  const chosen = roles.find((r) => r.id === roleId);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    setFields({});

    try {
      const made = await createPerson(client, {
        full_name: fullName,
        email,
        phone,
        role_id: roleId,
        amount_limit: amountLimit.trim(),
        // A window that ends is A6.2's "temporary and seasonal staff". Sent as
        // the end of the chosen day, so "until the 31st" includes the 31st —
        // a date alone would cut them off at midnight as it began.
        valid_until: validUntil ? `${validUntil}T23:59:59Z` : '',
      });
      onCreated(made);
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(t('people.saveOffline'));
      } else if (err instanceof RequestFailed && err.fields) {
        setFields(err.fields);
        setFailure(err.message);
      } else {
        setFailure(err instanceof Error ? err.message : t('common.didNotSave'));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('people.add')}</h2>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        <Field
          label={t('people.name')}
          htmlFor="person-name"
          required
          error={fields.full_name}
        >
          <TextInput
            id="person-name"
            value={fullName}
            onChange={setFullName}
            error={fields.full_name}
          />
        </Field>

        <Field
          label={t('people.email')}
          hint={t('people.emailHint')}
          htmlFor="person-email"
          required
          error={fields.email}
        >
          <TextInput
            id="person-email"
            value={email}
            onChange={setEmail}
            error={fields.email}
          />
        </Field>

        <Field label={t('people.phone')} htmlFor="person-phone">
          <TextInput id="person-phone" value={phone} onChange={setPhone} />
        </Field>

        <Field
          label={t('people.role')}
          hint={t('people.roleHint')}
          htmlFor="person-role"
          required
          error={fields.role_id}
        >
          <select
            id="person-role"
            className="input"
            value={roleId}
            aria-invalid={fields.role_id ? true : undefined}
            onChange={(e) => setRoleId(e.target.value)}
          >
            <option value="">{t('people.chooseRole')}</option>
            {roles.map((r) => (
              <option key={r.id} value={r.id} disabled={!r.assignable}>
                {r.name}
                {r.assignable ? '' : ` — ${t('people.notYoursToGive')}`}
              </option>
            ))}
          </select>
        </Field>

        {/* What the chosen role actually carries. A6.2's promise is that an
            Owner decides exactly what somebody can do; a list of names decides
            nothing. */}
        {chosen && (
          <div className="people__preview">
            <p className="ds-caption">
              {t('people.roleAllows', { role: chosen.name })}
            </p>
            {chosen.permissions.length === 0 ? (
              <p className="ds-body-sm ds-muted">{t('people.roleAllowsNothing')}</p>
            ) : (
              <ul className="people__perms">
                {chosen.permissions.map((p) => (
                  <li key={p}>
                    <span className="ds-badge">{permissionLabel(p, t)}</span>
                  </li>
                ))}
              </ul>
            )}
            {!chosen.assignable && (
              <p className="ds-body-sm people__withheld" role="note">
                {t('people.withheldBecause', {
                  permissions: (chosen.withheld_permissions ?? []).join(', '),
                })}
              </p>
            )}
          </div>
        )}

        {/* A6.2's scoped restrictions. Both optional, both left empty by
            default: most staff are neither capped nor temporary, and a form
            that pre-fills a limit invites somebody to accept one they did not
            choose. */}
        <Field
          label={t('people.amountLimit')}
          hint={t('people.amountLimitHint')}
          htmlFor="person-limit"
          error={fields.amount_limit}
        >
          <TextInput
            id="person-limit"
            value={amountLimit}
            onChange={setAmountLimit}
            inputMode="decimal"
            error={fields.amount_limit}
          />
        </Field>

        <Field
          label={t('people.validUntil')}
          hint={t('people.validUntilHint')}
          htmlFor="person-until"
          error={fields.valid_until}
        >
          <input
            id="person-until"
            className="input"
            type="date"
            value={validUntil}
            onChange={(e) => setValidUntil(e.target.value)}
          />
        </Field>

        <FormActions
          submitLabel={t('people.addAndIssue')}
          busy={busy}
          disabled={!chosen?.assignable}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
