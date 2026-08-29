// Who works here.
//
// Blueprint A5 hands the Owner a business and expects them to staff it; A6 is
// the model they do it with. This is that screen.
//
// # The one-time password is the whole design constraint
//
// Creating somebody returns a password that exists in exactly one place — the
// response to the request that made it — because the stored form is an argon2id
// hash. A4.2 calls the irreversibility "a security requirement, not just a
// policy choice", which means a screen that shows it for three seconds and
// fades it away has thrown away the only copy.
//
// So it is shown in a panel that stays until the person dismisses it, says
// plainly that it will not be shown again, and offers a copy button. Everything
// else on the screen waits behind it.
import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { FormError } from '../ui/Form';
import { longDate } from '../ui/format';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  listPeople,
  listRoles,
  removeAssignment,
  resetPersonPassword,
  setPersonActive,
  type CreatedPerson,
  type Person,
  type RoleOption,
} from '../api/people';
import { PersonForm } from './PersonForm';
import { OneTimePassword } from './OneTimePassword';

interface Loaded {
  people: Person[];
  roles: RoleOption[];
}

/** How a person's state reads, and which tone it carries. */
export function describePerson(p: Person): { label: Key; tone: string } {
  // Order matters: a suspended person who is also locked is suspended, because
  // that is the one somebody has to act on.
  if (p.status === 'disabled') return { label: 'people.suspended', tone: 'muted' };
  if (p.locked) return { label: 'people.locked', tone: 'warning' };
  if (p.must_change_password) return { label: 'people.notSignedInYet', tone: 'info' };
  return { label: 'people.active', tone: 'success' };
}

export function PeopleScreen() {
  const t = useT();
  const { locale } = useLocale();
  const { client, can } = useAuth();

  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<CreatedPerson | null>(null);
  const [resetFor, setResetFor] = useState<{ person: Person; password: string } | null>(null);
  const [showLeavers, setShowLeavers] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const mayEdit = can('identity.create');
  const mayAssign = can('identity.manage_roles');

  // The list and the roles together. The form cannot open without something to
  // assign, and fetching them apart would let it open against a stale set.
  const load = useCallback(async (): Promise<Loaded> => {
    const [people, roles] = await Promise.all([
      listPeople(client, showLeavers),
      listRoles(client),
    ]);
    return { people, roles };
  }, [client, showLeavers]);
  const { remote, reload } = useRemote(load);

  async function toggleActive(person: Person, active: boolean) {
    setBusy(person.id);
    setFailure(null);
    try {
      await setPersonActive(client, person.id, active);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(null);
    }
  }

  async function reissue(person: Person) {
    setBusy(person.id);
    setFailure(null);
    try {
      const out = await resetPersonPassword(client, person.id);
      // Held on screen, not flashed. It is the only copy.
      setResetFor({ person, password: out.temporary_password });
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(null);
    }
  }

  async function dropRole(assignmentId: string) {
    setFailure(null);
    try {
      await removeAssignment(client, assignmentId);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  // A password on screen is the only copy of it, so nothing else is shown
  // until somebody has confirmed they have written it down.
  if (issued) {
    return (
      <main className="detail">
        <OneTimePassword
          name={issued.person.full_name}
          email={issued.person.email}
          password={issued.temporary_password}
          onDone={() => {
            setIssued(null);
            reload();
          }}
        />
      </main>
    );
  }

  if (resetFor) {
    return (
      <main className="detail">
        <OneTimePassword
          name={resetFor.person.full_name}
          email={resetFor.person.email}
          password={resetFor.password}
          onDone={() => setResetFor(null)}
        />
      </main>
    );
  }

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('people.title')}</h1>
          <p className="ds-caption">{t('people.intro')}</p>
        </div>

        <div className="detail__actions">
          {mayEdit && mayAssign && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setCreating(true)}
            >
              {t('people.add')}
            </button>
          )}
        </div>
      </header>

      {failure && <FormError message={failure} />}

      <RemoteBody remote={remote} onRetry={reload}>
        {(loaded) =>
          creating ? (
            <PersonForm
              roles={loaded.roles}
              onCreated={(made) => {
                setCreating(false);
                setIssued(made);
              }}
              onCancel={() => setCreating(false)}
            />
          ) : (
            <div className="ds-panel">
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('people.everybody')}</h2>
                <label className="supplier__toggle">
                  <input
                    type="checkbox"
                    checked={showLeavers}
                    onChange={(e) => setShowLeavers(e.target.checked)}
                  />
                  <span className="ds-caption">{t('people.includeLeavers')}</span>
                </label>
              </div>

              {loaded.people.length === 0 ? (
                <div className="ds-panel__body">
                  <EmptyState
                    title={t('people.noneYet')}
                    body={t('people.noneYetBody')}
                  />
                </div>
              ) : (
                <div className="ds-panel__body ds-scroll-x">
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('people.person')}</th>
                        <th scope="col">{t('people.canDo')}</th>
                        <th scope="col">{t('people.state')}</th>
                        <th scope="col" className="ds-date">
                          {t('people.lastSignedIn')}
                        </th>
                        {mayEdit && <th scope="col" />}
                      </tr>
                    </thead>
                    <tbody>
                      {loaded.people.map((person) => {
                        const state = describePerson(person);
                        const gone = person.status === 'disabled';
                        return (
                          <tr key={person.id} className={gone ? 'detail__row--aside' : undefined}>
                            <td>
                              <span className="detail__strong">{person.full_name}</span>
                              <span className="ds-caption">{person.email}</span>
                            </td>

                            <td>
                              {person.roles.length === 0 ? (
                                <span className="ds-subtle">{t('people.noRole')}</span>
                              ) : (
                                <ul className="people__roles">
                                  {person.roles.map((role) => (
                                    <li key={role.id}>
                                      <span className="ds-badge">{role.role_name}</span>
                                      {/* A6.2's scopes, shown only when they
                                          narrow something — an unscoped role
                                          listing "every store, every warehouse,
                                          no limit" is noise on every row. */}
                                      {role.amount_limit && (
                                        <span className="ds-caption">
                                          {t('people.upTo', { amount: role.amount_limit })}
                                        </span>
                                      )}
                                      {role.valid_until && (
                                        <span className="ds-caption">
                                          {t('people.until', {
                                            date: longDate(role.valid_until, locale),
                                          })}
                                        </span>
                                      )}
                                      {mayAssign && !gone && (
                                        <button
                                          className="ds-btn ds-btn--quiet"
                                          onClick={() => void dropRole(role.id)}
                                        >
                                          {t('people.removeRole')}
                                        </button>
                                      )}
                                    </li>
                                  ))}
                                </ul>
                              )}
                            </td>

                            <td>
                              <span className={`ds-badge ds-badge--${state.tone}`}>
                                {t(state.label)}
                              </span>
                            </td>

                            <td className="ds-date">
                              {person.last_login_at ? (
                                longDate(person.last_login_at, locale)
                              ) : (
                                <span className="ds-subtle">—</span>
                              )}
                            </td>

                            {mayEdit && (
                              <td>
                                <div className="supplier__actions">
                                  {!gone && (
                                    <button
                                      className="ds-btn ds-btn--quiet"
                                      disabled={busy === person.id}
                                      onClick={() => void reissue(person)}
                                    >
                                      {t('people.newPassword')}
                                    </button>
                                  )}
                                  <button
                                    className="ds-btn ds-btn--quiet"
                                    disabled={busy === person.id}
                                    onClick={() => void toggleActive(person, gone)}
                                  >
                                    {gone ? t('people.restore') : t('people.suspend')}
                                  </button>
                                </div>
                              </td>
                            )}
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )
        }
      </RemoteBody>
    </main>
  );
}
