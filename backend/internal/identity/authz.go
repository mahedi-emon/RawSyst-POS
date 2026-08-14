package identity

import (
	"context"
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
	companyIDs   map[uuid.UUID]struct{}

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

// InCompany reports whether the actor is scoped to a legal entity.
func (g *Grants) InCompany(companyID uuid.UUID) bool {
	if g == nil {
		return false
	}
	if len(g.companyIDs) == 0 {
		return true
	}
	_, ok := g.companyIDs[companyID]
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

func NewAuthorizer(pool *db.Pool) *Authorizer {
	return &Authorizer{
		pool:  pool,
		cache: make(map[uuid.UUID]cachedGrants, 64),
		ttl:   grantsCacheTTL,
	}
}

// Invalidate drops a user's cached grants. Called whenever a role assignment
// changes, so a deliberate revocation is immediate rather than merely soon.
func (a *Authorizer) Invalidate(userID uuid.UUID) {
	a.mu.Lock()
	delete(a.cache, userID)
	a.mu.Unlock()
}

// InvalidateAll drops every cached grant. Called when a role's own permission
// set changes, which affects every user holding it.
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

	// The platform plane has no tenant roles. Its authority comes from the
	// verified IsSuperAdmin claim, and migration 0006 limits what that reaches
	// to administration tables — business data stays out of reach regardless.
	if act.IsSuperAdmin {
		return &Grants{isSuperAdmin: true, permissions: map[string]struct{}{}}, nil
	}

	a.mu.RLock()
	entry, hit := a.cache[act.UserID]
	a.mu.RUnlock()
	if hit && time.Since(entry.cachedAt) < a.ttl {
		return entry.grants, nil
	}

	g := &Grants{
		permissions:  make(map[string]struct{}, 32),
		storeIDs:     make(map[uuid.UUID]struct{}),
		warehouseIDs: make(map[uuid.UUID]struct{}),
		companyIDs:   make(map[uuid.UUID]struct{}),
	}

	// Scope is a UNION across assignments, and an unscoped assignment wins.
	// Someone holding both "manager of Olaya" and "auditor, all branches"
	// should see all branches: the wider grant was given deliberately.
	unscopedStore, unscopedWarehouse, unscopedCompany := false, false, false
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

			if companyID == nil {
				unscopedCompany = true
			} else {
				g.companyIDs[*companyID] = struct{}{}
			}
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
	if unscopedCompany {
		g.companyIDs = map[uuid.UUID]struct{}{}
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
