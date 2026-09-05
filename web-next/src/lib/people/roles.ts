// Roles, and the permissions that make one.
//
// # Delegation must not become escalation
//
// `identity.manage_roles` is the most powerful permission the product has:
// anybody holding it can put into a role anything they hold themselves. The
// server's subset rule is what stops it being anything MORE than that — a
// permission the caller does not hold is refused with *"You cannot put
// something into a role that you do not have yourself"*.
//
// That rule is the server's and stays the server's. What this file does is let
// a screen show it BEFORE the refusal: `holds` arrives on every permission, so
// a box somebody cannot grant is drawn disabled and explained rather than
// ticked, submitted and rejected. The two must agree, and where they disagree
// the server is right.
//
// # A built-in role is copied, not edited
//
// The product ships thirteen roles and keeps them current: a module added next
// year adds its permissions to them. Editing one would freeze it at today's
// shape, so the server refuses and says *"copy it and edit the copy"*. The list
// route now says `is_system`, so the screen offers Copy rather than offering
// Edit and collecting the refusal.
//
// # Nothing here decides what somebody may do
//
// Every judgement below is presentational: how to group, what to warn about,
// whether a button is worth offering. Authorisation is the server's, on every
// request, and a screen that got any of this wrong would show the wrong buttons
// and change nothing about what the person can actually do.

/** One permission, as the role builder shows it. */
export interface PermissionOption {
  permission: string;
  /** The heading it sits under. Free text from the server. */
  section: string;
  label: string;
  label_ar?: string;
  label_bn?: string;
  /** The warning, where the permission deserves one. */
  caution?: string;
  /** Whether the CALLER holds it, and so may put it in a role. */
  holds: boolean;
}

/** A role a business can assign. */
export interface RoleOption {
  id: string;
  key: string;
  name: string;
  description?: string;
  permissions: string[];
  /** False when the caller does not hold everything in it. */
  assignable: boolean;
  withheld_permissions?: string[];
  /** One the product ships and keeps current. Copy it; do not edit it. */
  is_system: boolean;
  /** How many people hold it. It cannot be removed while anybody does. */
  in_use: number;
}

/** A role read on its own, for editing. */
export interface CustomRole {
  id: string;
  key: string;
  name: string;
  name_ar?: string;
  description?: string;
  permissions: string[];
  is_system: boolean;
  in_use: number;
}

/** Somebody who can sign in. */
export interface Person {
  id: string;
  email: string;
  full_name: string;
  phone?: string;
  status: string;
  must_change_password: boolean;
  last_login_at?: string;
  locked: boolean;
  roles: Assignment[];
}

/** One role somebody holds, and where it applies. */
export interface Assignment {
  id: string;
  role_id: string;
  role_key: string;
  role_name: string;
  company_id?: string | null;
  store_ids: string[];
  warehouse_ids: string[];
  amount_limit?: string;
  valid_from?: string | null;
  valid_until?: string | null;
}

/**
 * The sections, in the order a role is read.
 *
 * Selling first because that is what most roles are for, and system last
 * because almost no role needs it. A section the server invents that this
 * build has never heard of is appended rather than dropped — the server owns
 * the vocabulary, and a permission that vanished from the builder because its
 * heading was unfamiliar would be a permission nobody could grant.
 */
const SECTION_ORDER = [
  'selling',
  'stock',
  'buying',
  'customers',
  'money',
  'staff',
  'aftersales',
  'oversight',
  'system',
] as const;

/** A section with its permissions, ready to render. */
export interface PermissionSection {
  section: string;
  permissions: PermissionOption[];
}

/**
 * Groups the permission list into the sections a screen draws.
 *
 * Order within a section is the server's, which is deliberate: 0101 gives each
 * row a `sort_order` so "the dangerous ones do not land at the top by
 * alphabetical accident". Re-sorting here would throw that away.
 */
export function bySection(all: PermissionOption[]): PermissionSection[] {
  const groups = new Map<string, PermissionOption[]>();
  for (const p of all) {
    const list = groups.get(p.section);
    if (list) list.push(p);
    else groups.set(p.section, [p]);
  }

  const known = SECTION_ORDER.filter((s) => groups.has(s));
  const unknown = [...groups.keys()].filter(
    (s) => !(SECTION_ORDER as readonly string[]).includes(s),
  ).sort();

  return [...known, ...unknown].map((section) => ({
    section,
    permissions: groups.get(section) ?? [],
  }));
}

/** What is wrong with a role somebody is building. */
export type RoleProblem = 'no_name' | 'nothing_ticked' | 'cannot_grant' | 'none';

/**
 * Whether a role can be saved, and why not.
 *
 * `cannot_grant` mirrors the server's subset rule so the refusal is prevented
 * rather than explained. A role with nothing in it is refused here rather than
 * on the server, because it is not a refusal — it is a form somebody has not
 * finished, and an empty role would let somebody sign in able to do nothing,
 * which reads to them as a broken account.
 */
export function roleProblem(
  name: string,
  chosen: readonly string[],
  all: readonly PermissionOption[],
): RoleProblem {
  if (name.trim() === '') return 'no_name';
  if (chosen.length === 0) return 'nothing_ticked';
  const grantable = new Set(all.filter((p) => p.holds).map((p) => p.permission));
  if (chosen.some((c) => !grantable.has(c))) return 'cannot_grant';
  return 'none';
}

/** Which of these a role holds that the caller cannot grant. */
export function ungrantable(
  chosen: readonly string[],
  all: readonly PermissionOption[],
): string[] {
  const grantable = new Set(all.filter((p) => p.holds).map((p) => p.permission));
  return chosen.filter((c) => !grantable.has(c));
}

/**
 * Whether a role can be removed, and why not.
 *
 * Both reasons are the server's: a built-in role is the product's, and a role
 * anybody holds cannot go because removing it would strip them of everything
 * they can do. Offering Remove and collecting the refusal teaches people that
 * buttons in this product do not mean anything.
 */
export type RemovalBlock = 'built_in' | 'in_use' | 'none';

export function removalBlock(role: Pick<RoleOption, 'is_system' | 'in_use'>): RemovalBlock {
  if (role.is_system) return 'built_in';
  if (role.in_use > 0) return 'in_use';
  return 'none';
}

/**
 * A starting point for a role copied from another.
 *
 * Only the permissions the caller can actually grant. Copying the Owner role as
 * somebody who is not the owner would otherwise produce a draft that cannot be
 * saved, with the refusal naming permissions the person never chose.
 */
export function copyOf(
  role: Pick<RoleOption, 'permissions'>,
  all: readonly PermissionOption[],
): string[] {
  const grantable = new Set(all.filter((p) => p.holds).map((p) => p.permission));
  return role.permissions.filter((p) => grantable.has(p));
}

/**
 * How many of a section's permissions a draft holds.
 *
 * Shown beside the heading so a collapsed section still says whether anything
 * inside it is ticked.
 */
export function tickedIn(
  section: PermissionSection,
  chosen: readonly string[],
): number {
  const set = new Set(chosen);
  return section.permissions.filter((p) => set.has(p.permission)).length;
}

/** The permissions in a section that this caller may grant. */
export function grantableIn(section: PermissionSection): string[] {
  return section.permissions.filter((p) => p.holds).map((p) => p.permission);
}

/**
 * Whether somebody can sign in at all right now.
 *
 * Three separate states the people list has to keep apart: suspended is an
 * administrator's decision, locked is the sign-in system's after too many
 * failures, and a one-time password is an account that works but has not been
 * collected yet. Collapsing them into "inactive" loses the only one that tells
 * an owner what to do next.
 */
export type SignInState = 'suspended' | 'locked' | 'never_signed_in' | 'active';

export function signInState(p: Person): SignInState {
  if (p.status !== 'active') return 'suspended';
  if (p.locked) return 'locked';
  if (p.must_change_password) return 'never_signed_in';
  return 'active';
}
