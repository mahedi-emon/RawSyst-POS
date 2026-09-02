// The custom role builder (blueprint A6.2).
//
// # A permission the author cannot grant is shown, disabled, and explained
//
// The server refuses to put into a role anything the author does not hold
// themselves — otherwise the builder is a way straight past the assignment
// check. The screen shows those permissions greyed rather than hiding them,
// because an owner who cannot see why a box is missing will assume the product
// is broken; one who sees it disabled with a reason knows to ask.
//
// # The built-in roles are copied, not edited
//
// They are the product's, and the migrations extend them when a module is
// added. A shop that wants a different Cashier gets a copy, which keeps working
// when the next module arrives.
//
// # The cautions are beside the tick boxes, not in a preamble
//
// "Reopens a closed month and changes figures somebody has already reported" is
// only useful next to the thing it is about, at the moment somebody is about to
// tick it.

import { useCallback, useMemo, useState } from 'react';

import { listRoles, type RoleOption } from '../api/people';
import {
  listPermissions,
  readRole,
  removeRole,
  saveRole,
  type PermissionOption,
} from '../api/security';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { LabelledText } from '../governance/fields';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';

interface Draft {
  id?: string;
  name: string;
  description: string;
  permissions: Set<string>;
  clonedFrom?: string;
}

export function RoleBuilderPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const rolesLoad = useCallback(() => listRoles(client), [client]);
  const roles = useRemote(rolesLoad);

  const permsLoad = useCallback(
    () => listPermissions(client, companyId),
    [client, companyId],
  );
  const perms = useRemote(permsLoad);

  const [draft, setDraft] = useState<Draft | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const options: PermissionOption[] =
    perms.remote.state === 'ready' ? perms.remote.data.data : [];

  // Grouped for the screen. The server sorts within a section already; this
  // only preserves the order the sections first appear in.
  const sections = useMemo(() => {
    const out: Array<{ section: string; items: PermissionOption[] }> = [];
    for (const p of options) {
      let group = out.find((g) => g.section === p.section);
      if (!group) {
        group = { section: p.section, items: [] };
        out.push(group);
      }
      group.items.push(p);
    }
    return out;
  }, [options]);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      roles.reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function open(role: RoleOption, asCopy: boolean) {
    setFailure(null);
    try {
      const full = await readRole(client, companyId, role.id);
      setDraft({
        id: asCopy ? undefined : full.role.id,
        name: asCopy ? t('sec.copyOf', { name: full.role.name }) : full.role.name,
        description: full.role.description ?? '',
        permissions: new Set(full.role.permissions),
        clonedFrom: asCopy ? role.id : full.role.cloned_from,
      });
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  const toggle = (permission: string) =>
    setDraft((d) => {
      if (!d) return d;
      const next = new Set(d.permissions);
      if (next.has(permission)) next.delete(permission);
      else next.add(permission);
      return { ...d, permissions: next };
    });

  return (
    <>
      <section className="ds-panel" aria-label={t('sec.roles')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('sec.roles')}</h2>
            <p className="ds-caption">{t('sec.rolesHint')}</p>
          </div>
          <button
            className="ds-btn ds-btn--primary"
            onClick={() =>
              setDraft({
                name: '',
                description: '',
                permissions: new Set<string>(),
              })
            }
          >
            {t('sec.newRole')}
          </button>
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
        </div>

        <RemoteBody remote={roles.remote} onRetry={roles.reload}>
          {(list: RoleOption[]) => (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('sec.roleName')}</th>
                    <th scope="col">{t('sec.canDo')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((r) => (
                    <tr key={r.id}>
                      <td>
                        {r.name}
                        {!r.id.startsWith('__') && r.description && (
                          <span className="ds-caption"> {r.description}</span>
                        )}
                      </td>
                      <td className="num">{r.permissions.length}</td>
                      <td className="ds-table__actions">
                        <button
                          className="ds-btn ds-btn--quiet ds-btn--sm"
                          onClick={() => void open(r, false)}
                        >
                          {t('action.edit')}
                        </button>
                        <button
                          className="ds-btn ds-btn--quiet ds-btn--sm"
                          onClick={() => void open(r, true)}
                        >
                          {t('sec.copyIt')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </RemoteBody>
      </section>

      {draft && (
        <section className="ds-panel" aria-label={t('sec.newRole')}>
          <div className="ds-panel__head">
            <div>
              <h2 className="ds-h3">
                {draft.id ? t('sec.editingRole') : t('sec.newRole')}
              </h2>
              <p className="ds-caption">{t('sec.tickWhatItCanDo')}</p>
            </div>
          </div>

          <div className="ds-panel__body">
            <div className="pri__form">
              <LabelledText
                id="role-name"
                label={t('sec.roleName')}
                value={draft.name}
                onChange={(v) => setDraft({ ...draft, name: v })}
              />
              <LabelledText
                id="role-desc"
                label={t('sec.roleDescription')}
                value={draft.description}
                onChange={(v) => setDraft({ ...draft, description: v })}
              />
            </div>

            {sections.map((group) => (
              <fieldset className="sec__group" key={group.section}>
                <legend className="sec__groupName">
                  {t(`sec.section.${group.section}` as Key)}
                </legend>
                <ul className="sec__perms">
                  {group.items.map((p) => (
                    <li className="sec__perm" key={p.permission}>
                      <label
                        className={`ds-check${p.holds ? '' : ' sec__perm--withheld'}`}
                      >
                        <input
                          type="checkbox"
                          disabled={!p.holds}
                          checked={draft.permissions.has(p.permission)}
                          onChange={() => toggle(p.permission)}
                        />
                        <span>
                          <span className="sec__permLabel">{p.label}</span>
                          {p.caution && (
                            <span className="sec__caution">{p.caution}</span>
                          )}
                          {!p.holds && (
                            <span className="sec__caution">
                              {t('sec.youDoNotHoldThis')}
                            </span>
                          )}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
              </fieldset>
            ))}

            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={
                  busy ||
                  draft.name.trim() === '' ||
                  draft.permissions.size === 0
                }
                onClick={() =>
                  void run(async () => {
                    await saveRole(
                      client,
                      companyId,
                      {
                        name: draft.name,
                        description: draft.description || undefined,
                        permissions: Array.from(draft.permissions),
                        cloned_from: draft.clonedFrom,
                      },
                      draft.id,
                    );
                    setDraft(null);
                  })
                }
              >
                {t('action.save')}
              </button>
              {draft.id && (
                <button
                  className="ds-btn ds-btn--quiet"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await removeRole(client, companyId, draft.id as string);
                      setDraft(null);
                    })
                  }
                >
                  {t('action.remove')}
                </button>
              )}
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setDraft(null)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        </section>
      )}
    </>
  );
}
