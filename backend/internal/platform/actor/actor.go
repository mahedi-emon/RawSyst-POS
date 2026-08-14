// Package actor carries the authenticated caller through a request.
//
// The Actor is the single source of truth for "who is asking" and is consumed
// by two very different layers:
//
//   - the database layer, which stamps app.tenant_id so PostgreSQL row-level
//     security can enforce isolation (see docs/system-design/04);
//   - the authorization layer, which checks permissions and scope.
//
// It is deliberately a small, immutable value. Nothing derived from a request
// body may ever influence it — only a verified token.
package actor

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey int

const ctxKeyActor ctxKey = iota

// Actor is the authenticated caller.
type Actor struct {
	UserID    uuid.UUID
	SessionID uuid.UUID

	// TenantID is uuid.Nil for a platform Super Admin, who sits above every
	// tenant and therefore belongs to none.
	TenantID uuid.UUID

	// IsSuperAdmin gates the platform control plane. It never grants access to
	// tenant business data: a Super Admin administers billing, feature
	// availability and platform health, and does not interfere in a tenant's
	// day-to-day records.
	IsSuperAdmin bool

	// CompanyIDs limits the actor to specific legal entities within the tenant.
	// Empty means every company in the tenant.
	CompanyIDs []uuid.UUID

	// DeviceID is set when the caller is a POS terminal rather than a person.
	DeviceID uuid.UUID
}

// IsAuthenticated reports whether a caller was identified at all.
func (a Actor) IsAuthenticated() bool {
	return a.UserID != uuid.Nil || a.DeviceID != uuid.Nil
}

// IsDevice reports whether the caller is a POS terminal.
func (a Actor) IsDevice() bool { return a.DeviceID != uuid.Nil }

// CanAccessCompany reports whether the actor is scoped to a given company.
func (a Actor) CanAccessCompany(companyID uuid.UUID) bool {
	if len(a.CompanyIDs) == 0 {
		return true // unscoped: every company in the tenant
	}
	for _, id := range a.CompanyIDs {
		if id == companyID {
			return true
		}
	}
	return false
}

// Into stores the actor on the context.
func Into(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKeyActor, a)
}

// From retrieves the actor. The zero Actor is returned when unauthenticated,
// which fails IsAuthenticated and carries uuid.Nil as its tenant — so a
// missing actor can never widen access.
func From(ctx context.Context) Actor {
	if a, ok := ctx.Value(ctxKeyActor).(Actor); ok {
		return a
	}
	return Actor{}
}
