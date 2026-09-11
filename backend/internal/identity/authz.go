package identity

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Grants is everything a user is allowed to do, resolved for one request.
//
// Blueprint A6.2 layers four scope dimensions on top of the permission verbs:
// store, warehouse, transaction amount, and a validity window. A permission
// alone is never the whole answer — a manager may hold sales.refund and still
// be unable to refund in a branch that is not theirs.
type Grants struct {
	permissions map[string]struct{}

	// Empty slice means "every one". This is the common case — most users are
	// not scoped — so representing it as empty rather than as an exhaustive
	// list keeps the resolve query cheap and the check O(1).
	storeIDs     map[uuid.UUID]struct{}
	warehouseIDs map[uuid.UUID]struct{}

	// There is deliberately no company dimension here. It is enforced by
	// actor.CanAccessCompany against the claim the token carries, resolved once
	// at sign-in by Service.companyScopesFor. A second copy on Grants would be
	// the same rule in two places, and two mechanisms for one rule drift apart
	// -- which is exactly how the dimension came to be declared, stored, read
	// back and never actually enforced.

	// nil means no ceiling. Blueprint example: cashier up to SAR 50, manager up
	// to SAR 500, owner unlimited.
	amountLimit *decimal.Decimal

	isSuperAdmin bool
}

// Can reports whether the permission is held at all, ignoring scope.
func (g *Grants) Can(permission string) bool {
	if g == nil {
		return false
	}
	_, ok := g.permissions[permission]
	return ok
}

// CanInStore reports whether the permission is held for a specific store.
func (g *Grants) CanInStore(permission string, storeID uuid.UUID) bool {
	if !g.Can(permission) {
		return false
	}
	return g.InStore(storeID)
}

// InStore reports whether the actor is scoped to a store.
func (g *Grants) InStore(storeID uuid.UUID) bool {
	if g == nil {
		return false
	}
	if len(g.storeIDs) == 0 {
		return true // unscoped: every store
	}
	_, ok := g.storeIDs[storeID]
	return ok
}

// InWarehouse reports whether the actor is scoped to a warehouse.
func (g *Grants) InWarehouse(warehouseID uuid.UUID) bool {
	if g == nil {
		return false
	}
	if len(g.warehouseIDs) == 0 {
		return true
	}
	_, ok := g.warehouseIDs[warehouseID]
	return ok
}

// AmountLimit returns the ceiling, or nil when unlimited.
func (g *Grants) AmountLimit() *decimal.Decimal {
	if g == nil {
		return nil
	}
	return g.amountLimit
}

// WithinLimit reports whether an amount is inside the actor's ceiling.
//
// The comparison is on absolute value: a refund of −500 is as significant as a
// discount of 500, and a limit that only constrained one sign would be trivial
// to sidestep.
func (g *Grants) WithinLimit(amount decimal.Decimal) bool {
	if g == nil {
		return false
	}
	if g.amountLimit == nil {
		return true
	}
	return amount.Abs().LessThanOrEqual(*g.amountLimit)
}

// Permissions returns the held permissions, for the client to shape its UI.
//
// The client uses this to hide buttons. That is a convenience, never a control:
// every route re-checks server-side, which is what QA gate M7 tests by calling
// restricted routes directly as a Cashier.
func (g *Grants) Permissions() []string {
	if g == nil {
		return nil
	}
	out := make([]string, 0, len(g.permissions))
	for p := range g.permissions {
		out = append(out, p)
	}
	return out
}

// StoreIDs returns the branches the actor is confined to, or nil when they are
// confined to none — which means every branch, not no branch.
//
// Reported to the client by /auth/me so a manager scoped to one branch is shown
// one branch rather than a store picker they cannot use. Like Permissions, this
// shapes the UI and controls nothing: the routes re-check.
func (g *Grants) StoreIDs() []uuid.UUID {
	if g == nil || len(g.storeIDs) == 0 {
		return nil
	}
	out := make([]uuid.UUID, 0, len(g.storeIDs))
	for id := range g.storeIDs {
		out = append(out, id)
	}
	// Sorted for the same reason the permission list is: two responses should be
	// diffable, and the payload byte-stable.
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// IsSuperAdmin reports whether this is the platform control plane.
func (g *Grants) IsSuperAdmin() bool { return g != nil && g.isSuperAdmin }

// --- resolution --------------------------------------------------------

type cachedGrants struct {
	grants   *Grants
	cachedAt time.Time
}

// Authorizer resolves grants for an actor.
type Authorizer struct {
	pool *db.Pool

	mu    sync.RWMutex
	cache map[uuid.UUID]cachedGrants
	ttl   time.Duration
}

// grantsCacheTTL bounds how long a revoked permission can keep working.
//
// Five seconds, not five minutes. The whole reason permissions are resolved per
// request rather than embedded in the token is that a revocation must take
// effect now; a long cache would reintroduce exactly the staleness the token
// design avoids. Five seconds still removes the per-request query from a POS
// terminal scanning items in a burst.
const grantsCacheTTL = 5 * time.Second

// GrantsCacheTTL is the same value, readable from outside the package.
//
// Exported so a test can assert the window rather than describe it. It is the
// honest bound on every revocation this product performs — a permission, a
// role, or a disabled account — because nothing calls Invalidate.
const GrantsCacheTTL = grantsCacheTTL

func NewAuthorizer(pool *db.Pool) *Authorizer {
	return &Authorizer{
		pool:  pool,
		cache: make(map[uuid.UUID]cachedGrants, 64),
		ttl:   grantsCacheTTL,
	}
}

// Invalidate drops a user's cached grants.
//
// Nothing in the running product calls this, and the comment here used to say
// it was called "whenever a role assignment changes, so a deliberate revocation
// is immediate rather than merely soon". That was not true and had not been for
// as long as the function existed.
//
// What it means in practice is that every revocation — a permission, a role, or
// an account being disabled — takes effect within `grantsCacheTTL` rather than
// on the next request. Five seconds is a defensible bound and is the one this
// product actually offers; "immediate" was a claim nothing delivered.
//
// The callers are tests, which narrow an assignment and need the change seen
// before the TTL elapses. Wiring it into the role-change paths would make the
// original comment true and is a deliberate change somebody should make on
// purpose, not a comment anybody should believe in the meantime.
func (a *Authorizer) Invalidate(userID uuid.UUID) {
	a.mu.Lock()
	delete(a.cache, userID)
	a.mu.Unlock()
}

// InvalidateAll drops every cached grant, for a change that affects everybody
// holding a role rather than one person.
//
// Called by tests only, on the same terms as Invalidate above.
func (a *Authorizer) InvalidateAll() {
	a.mu.Lock()
	a.cache = make(map[uuid.UUID]cachedGrants, 64)
	a.mu.Unlock()
}

// Resolve computes what the actor may do.
func (a *Authorizer) Resolve(ctx context.Context, act actor.Actor) (*Grants, error) {
	if !act.IsAuthenticated() {
		return nil, errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}

	a.mu.RLock()
	entry, hit := a.cache[act.UserID]
	a.mu.RUnlock()
	if hit && time.Since(entry.cachedAt) < a.ttl {
		return entry.grants, nil
	}

	// The platform plane has no tenant roles. Its authority comes from the
	// verified IsSuperAdmin claim, and migration 0006 limits what that reaches
	// to administration tables — business data stays out of reach regardless.
	//
	// It still goes through the account check below and through the cache,
	// which it did not before: it returned here immediately, so a disabled
	// PLATFORM OPERATOR — the highest-privilege account there is — kept every
	// power they had until their access token expired.
	if act.IsSuperAdmin {
		if err := a.requireAccountIsUsable(ctx, act); err != nil {
			return nil, err
		}
		g := &Grants{isSuperAdmin: true, permissions: map[string]struct{}{}}
		a.mu.Lock()
		a.cache[act.UserID] = cachedGrants{grants: g, cachedAt: time.Now()}
		a.mu.Unlock()
		return g, nil
	}

	// Whether the account behind this token still exists and may still be used.
	//
	// # Why this is here and not in the token
	//
	// It cannot be in the token. An access token is a signed statement about
	// the past, verified with a key and nothing else -- `TokenService.Verify`
	// never touches the database, deliberately. So a token issued to somebody
	// who was in good standing fifteen minutes ago is still cryptographically
	// perfect after they are disabled.
	//
	// Disabling somebody DOES revoke their sessions, so they cannot refresh and
	// cannot sign in again. But the access token they are already holding kept
	// working until it expired, and with the default fifteen-minute lifetime
	// that is a quarter of an hour in which a dismissed member of staff could
	// keep ringing up sales, moving stock, or reading the books.
	//
	// # Why it belongs in exactly this function
	//
	// Because this is the one place that already reads the database on every
	// request, and the file has already argued for what that is worth: the
	// comment on `grantsCacheTTL` says a revocation "must take effect now",
	// which is why permissions are resolved per request instead of baked into
	// the token.
	//
	// A revoked PERMISSION took effect in five seconds and a revoked ACCOUNT
	// took up to fifteen minutes. That was not a decision, it was the account
	// simply never being looked at. Now both are bounded by the same cache,
	// and it costs one column on a query that was already being run.
	if err := a.requireAccountIsUsable(ctx, act); err != nil {
		return nil, err
	}

	g := &Grants{
		permissions:  make(map[string]struct{}, 32),
		storeIDs:     make(map[uuid.UUID]struct{}),
		warehouseIDs: make(map[uuid.UUID]struct{}),
	}

	// Scope is a UNION across assignments, and an unscoped assignment wins.
	// Someone holding both "manager of Olaya" and "auditor, all branches"
	// should see all branches: the wider grant was given deliberately.
	unscopedStore, unscopedWarehouse := false, false
	var maxLimit *decimal.Decimal
	sawUnlimited := false
	assignments := 0

	err := a.pool.Tx(actor.Into(ctx, act), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT rp.permission, ura.company_id, ura.store_ids,
			       ura.warehouse_ids, ura.amount_limit
			FROM user_role_assignment ura
			JOIN role_permission rp ON rp.role_id = ura.role_id
			WHERE ura.user_id = $1
			  -- A role with a validity window is inert outside it. Seasonal and
			  -- temporary staff expire on their own rather than needing an
			  -- administrator to remember.
			  AND (ura.valid_from  IS NULL OR ura.valid_from  <= now())
			  AND (ura.valid_until IS NULL OR ura.valid_until  > now())`,
			act.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var (
				permission   string
				companyID    *uuid.UUID
				storeIDs     []uuid.UUID
				warehouseIDs []uuid.UUID
				amountLimit  *decimal.Decimal
			)
			if err := rows.Scan(&permission, &companyID, &storeIDs,
				&warehouseIDs, &amountLimit); err != nil {
				return err
			}
			assignments++
			g.permissions[permission] = struct{}{}

			// Read and discarded: the column is what companyScopesFor reads at
			// sign-in, and this resolver has no company check to feed.
			_ = companyID
			if len(storeIDs) == 0 {
				unscopedStore = true
			}
			for _, id := range storeIDs {
				g.storeIDs[id] = struct{}{}
			}
			if len(warehouseIDs) == 0 {
				unscopedWarehouse = true
			}
			for _, id := range warehouseIDs {
				g.warehouseIDs[id] = struct{}{}
			}

			if amountLimit == nil {
				sawUnlimited = true
			} else if maxLimit == nil || amountLimit.GreaterThan(*maxLimit) {
				v := *amountLimit
				maxLimit = &v
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}

	if unscopedStore {
		g.storeIDs = map[uuid.UUID]struct{}{}
	}
	if unscopedWarehouse {
		g.warehouseIDs = map[uuid.UUID]struct{}{}
	}
	if !sawUnlimited {
		g.amountLimit = maxLimit
	}

	// A user with no live assignment holds nothing. That is the correct outcome
	// for a suspended or newly-created account, and it fails closed: an empty
	// permission set denies every route.
	_ = assignments

	a.mu.Lock()
	a.cache[act.UserID] = cachedGrants{grants: g, cachedAt: time.Now()}
	a.mu.Unlock()

	return g, nil
}

// requireAccountIsUsable refuses a token whose account can no longer sign in.
//
// The same rule the sign-in path applies, applied to a session that is already
// running. `Service.SignIn` permits `active` and `invited` and refuses
// `suspended` and `disabled`; anything else is not a state a person can hold.
// Keeping the two in step matters more than the individual values: a state that
// stops somebody signing in but lets them keep working is the shape of the gap
// this closes.
//
// `invited` is deliberately allowed. Somebody holding a one-time password has
// to be able to reach the change-password screen, and that screen is an
// authenticated route like any other. Refusing them here would make a new
// account impossible to activate.
//
// A deleted account refuses too, by finding no row. That is the correct
// outcome and it fails closed.
func (a *Authorizer) requireAccountIsUsable(
	ctx context.Context, act actor.Actor,
) error {
	var status string
	err := a.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status::text FROM app_user WHERE id = $1`,
			act.UserID).Scan(&status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeUnauthenticated,
			"Your session is no longer valid. Please sign in again.")
	}
	if err != nil {
		return db.Translate(err, "")
	}

	switch status {
	case "active", "invited":
		return nil
	}

	// Unauthenticated rather than forbidden, and the difference is not
	// cosmetic. The browser client answers 401 by trying to refresh; the
	// refresh finds the session revoked and signs the person out. A 403 would
	// leave them sitting on a screen full of buttons that all fail.
	return errs.New(errs.CodeUnauthenticated,
		"This account has been disabled. Please sign in again, or ask "+
			"whoever looks after your RawSyst account.")
}

// All is every permission held, sorted.
//
// The one caller is the API-key screen: a key may carry a subset of what its
// creator holds, so the form has to offer exactly that set. Sorted rather than
// map order, because a list of forty permissions that reshuffles on every
// request is a list nobody can find anything in.
func (g *Grants) All() []string {
	if g == nil {
		return []string{}
	}
	out := make([]string, 0, len(g.permissions))
	for p := range g.permissions {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
