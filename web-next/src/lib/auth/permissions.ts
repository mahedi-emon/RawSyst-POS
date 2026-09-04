// Permissions, as the backend defines them.
//
// # This is not the security boundary
//
// Every check in this file is a USER-INTERFACE decision. The Go service checks
// the same permission again on every request and is the only thing standing
// between a person and a record. Hiding a button is not authorisation; it is
// the courtesy of not offering someone a door that is locked.
//
// What that buys is worth having: an employee who can only ring up sales sees
// a product that is about ringing up sales, rather than a wall of modules that
// all refuse. But if this file were deleted the product would still be secure,
// and if a check here disagrees with the backend, the backend is right.
//
// # The list is generated
//
// `PERMISSIONS` comes from `contract.generated.ts`, which is read out of
// `backend/internal/api/router.go`. There is no hand-written list to drift.

import {
  PERMISSIONS,
  type Permission,
  FEATURE_OF_ROUTE_GROUP,
} from '../api/contract.generated';

export { PERMISSIONS, FEATURE_OF_ROUTE_GROUP };
export type { Permission };

const KNOWN = new Set<string>(PERMISSIONS);

/** True when a string is a permission the backend actually enforces. */
export function isKnownPermission(p: string): p is Permission {
  return KNOWN.has(p);
}

/**
 * What the signed-in person may do.
 *
 * Built once per session from `GET /auth/me` and passed down, rather than each
 * screen asking again. The server resolves permissions per request and does not
 * put them in the token precisely so a revocation takes effect immediately --
 * so this set is refreshed whenever the session is.
 */
export class Grants {
  private readonly held: ReadonlySet<string>;

  constructor(
    permissions: readonly string[],
    /** True for the platform operator, who is not inside any business. */
    readonly isPlatformOperator: boolean = false,
    /** The branches this person is confined to. Empty means every branch. */
    readonly storeScope: readonly string[] = [],
    /** An approval ceiling, as a decimal STRING. Never widened through a float. */
    readonly amountLimit: string | null = null,
  ) {
    this.held = new Set(permissions);
  }

  /** Holds this exact permission. */
  can(permission: Permission): boolean {
    return this.held.has(permission);
  }

  /** Holds at least one of these. Used by navigation, where a section is worth
   *  showing if any of its screens is reachable. */
  canAny(...permissions: readonly Permission[]): boolean {
    return permissions.some((p) => this.held.has(p));
  }

  /** Holds all of these. Used where a workflow genuinely needs both halves. */
  canAll(...permissions: readonly Permission[]): boolean {
    return permissions.every((p) => this.held.has(p));
  }

  /**
   * Holds anything at all in a module -- `sales`, `inventory`, `purchasing`.
   *
   * The cheap test navigation needs, so a section does not have to enumerate
   * every permission underneath it and then fall out of step when one is added.
   */
  canReachModule(module: string): boolean {
    const prefix = `${module}.`;
    for (const p of this.held) if (p.startsWith(prefix)) return true;
    return false;
  }

  /** Confined to particular branches, rather than seeing the whole business. */
  get isBranchConfined(): boolean {
    return this.storeScope.length > 0;
  }

  /** The raw list, for the account screen that shows a person what they hold. */
  list(): readonly string[] {
    return [...this.held].sort();
  }

  get size(): number {
    return this.held.size;
  }
}

/** Nobody, used before a session resolves and after it ends. */
export const NO_GRANTS = new Grants([]);

/**
 * The module a plan sells a route group under, if any.
 *
 * A business whose plan omits `payroll` is refused every `/payroll/*` route
 * with 402 regardless of permission. Navigation reads this so the section can
 * say "not included in your plan" -- which is a different sentence, and a
 * different remedy, from "you do not have permission".
 */
export function planFeatureFor(routeGroup: string): string | undefined {
  return FEATURE_OF_ROUTE_GROUP[routeGroup];
}
