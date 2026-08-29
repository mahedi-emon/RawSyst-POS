// The people who work here, and what each of them may do.
//
// Blueprint A5 makes the Owner self-sufficient after Super Admin creates their
// first login; A6 is the model they delegate with. Everything a shop needs to
// staff its tills lives behind these calls.
//
// # A password is returned exactly once
//
// `createPerson` and `resetPersonPassword` both hand back a one-time password
// in their response and never again. It is stored as an argon2id hash — A4.2
// calls the irreversibility "a security requirement, not just a policy choice"
// — so a screen that loses it has to issue a new one. Every screen that calls
// these must therefore SHOW the password until the person dismisses it, not
// flash it in a toast.
import type { Client } from './client';

/** One role somebody holds, and where it applies. */
export interface Assignment {
  id: string;
  role_id: string;
  role_key: string;
  role_name: string;

  /** Null means every company in the business. */
  company_id: string | null;

  /** Empty means every store, and every warehouse. Not "none". */
  store_ids: string[];
  warehouse_ids: string[];

  /** The transaction ceiling from A6.2. Empty means unlimited. */
  amount_limit?: string;

  valid_from: string | null;
  valid_until: string | null;
}

/** Somebody who works here. */
export interface Person {
  id: string;
  email: string;
  full_name: string;
  phone?: string;

  /** invited · active · suspended · disabled */
  status: string;

  /** True for somebody created and not yet signed in. Shown, because "I never
   *  got my password" and "I have not used it yet" look identical otherwise. */
  must_change_password: boolean;

  last_login_at: string | null;

  /** Set while a run of failed sign-ins is being served out. Distinct from
   *  suspended: this one clears itself. */
  locked: boolean;

  roles: Assignment[];
}

/** A role that can be given out. */
export interface RoleOption {
  id: string;
  key: string;
  name: string;
  description?: string;

  /** What the role carries, so a screen can show what is being handed over
   *  rather than a name alone. */
  permissions: string[];

  /** False when the caller does not hold everything the role does. */
  assignable: boolean;

  /** Which permissions are missing. Sent so a greyed-out role can say why. */
  withheld_permissions?: string[];
}

/** A new member of staff, or a change to what one may do. */
export interface PersonBody {
  email?: string;
  full_name?: string;
  phone?: string;

  role_id?: string;
  company_id?: string;
  store_ids?: string[];
  warehouse_ids?: string[];
  amount_limit?: string;
  valid_from?: string;
  valid_until?: string;
}

/** A person and the one-time password to hand them. */
export interface CreatedPerson {
  person: Person;
  /** Shown once. Never retrievable. */
  temporary_password: string;
}

export function listPeople(
  client: Client,
  includeInactive = false,
): Promise<Person[]> {
  const q = includeInactive ? '?include_inactive=true' : '';
  return client
    .send<{ data: Person[] }>('GET', `/api/v1/people${q}`)
    .then((b) => b.data ?? []);
}

export function listRoles(client: Client): Promise<RoleOption[]> {
  return client
    .send<{ data: RoleOption[] }>('GET', '/api/v1/people/roles')
    .then((b) => b.data ?? []);
}

export function createPerson(
  client: Client,
  body: PersonBody,
): Promise<CreatedPerson> {
  return client
    .send<{ data: CreatedPerson }>('POST', '/api/v1/people', body)
    .then((b) => b.data);
}

export function updatePerson(
  client: Client,
  userId: string,
  body: PersonBody,
): Promise<void> {
  return client.send<void>('PUT', `/api/v1/people/${userId}`, body);
}

export function setPersonActive(
  client: Client,
  userId: string,
  active: boolean,
): Promise<void> {
  return client.send<void>('POST', `/api/v1/people/${userId}/active`, { active });
}

export function resetPersonPassword(
  client: Client,
  userId: string,
): Promise<{ temporary_password: string }> {
  return client
    .send<{ data: { temporary_password: string } }>(
      'POST',
      `/api/v1/people/${userId}/reset-password`,
    )
    .then((b) => b.data);
}

export function assignRole(
  client: Client,
  userId: string,
  body: PersonBody,
): Promise<void> {
  return client.send<void>('POST', `/api/v1/people/${userId}/roles`, body);
}

export function removeAssignment(
  client: Client,
  assignmentId: string,
): Promise<void> {
  return client.send<void>('DELETE', `/api/v1/people/roles/${assignmentId}`);
}
