// Reading a business's standing, and changing it.
//
// The read is on the request path for every write in the product, so it is
// cached. The writes are Super Admin acts and are audited individually.

package billing

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// StandingCacheTTL bounds how long a suspension takes to bite.
//
// Five seconds, matching `identity.GrantsCacheTTL`, and for the same reason
// given there: this is consulted in front of every write in the product, so it
// cannot be a query per request, and a long cache would mean a business
// suspended at nine is still trading at ten.
//
// It is a real window and it is not zero. A business suspended this instant may
// complete writes for up to five more seconds. That is written down rather than
// glossed, here and in docs/ACCESS-BOUNDARIES.md, because "immediate" is a
// claim this does not deliver.
const StandingCacheTTL = 5 * time.Second

type cachedStanding struct {
	standing Standing
	at       time.Time
}

// standingCache is per-tenant and lives on the service.
type standingCache struct {
	mu sync.RWMutex
	m  map[uuid.UUID]cachedStanding
}

// StandingOf reads where a business stands, through the cache.
//
// # Why it fails OPEN
//
// A database error returns the last known standing, or an active one if there
// is none. That is deliberate and it is the opposite of what the access checks
// do.
//
// Those refuse on doubt because the question is "may this person do this", and
// the safe answer to a question you cannot answer is no. This question is "has
// this customer paid", and the safe answer is not no. A blip on the billing
// table would otherwise stop every till in every shop on the platform from
// taking money — turning a read failure into an outage, and a commercial
// control into an availability risk.
//
// The tenant isolation, the permissions and the row-level security are all
// still in force either way. What fails open here is the commercial gate alone.
func (s *Service) StandingOf(ctx context.Context, tenantID uuid.UUID) Standing {
	if s == nil || s.pool == nil {
		return Standing{State: StandingActive, SignIn: true, Read: true, Write: true}
	}

	s.standing.mu.RLock()
	hit, ok := s.standing.m[tenantID]
	s.standing.mu.RUnlock()
	if ok && time.Since(hit.at) < StandingCacheTTL {
		return hit.standing
	}

	fresh, err := s.readStanding(ctx, tenantID)
	if err != nil {
		if ok {
			return hit.standing
		}
		return Standing{State: StandingActive, SignIn: true, Read: true, Write: true}
	}

	s.standing.mu.Lock()
	if s.standing.m == nil {
		s.standing.m = map[uuid.UUID]cachedStanding{}
	}
	s.standing.m[tenantID] = cachedStanding{standing: fresh, at: time.Now()}
	s.standing.mu.Unlock()
	return fresh
}

// ForgetStanding drops a tenant's cached standing, so a Super Admin who
// suspends a business does not then watch it trade for five more seconds.
//
// Called by every transition below. It is a courtesy rather than the guarantee:
// nothing outside this process is told, so a second API instance keeps its own
// copy for up to the TTL. See the note on StandingCacheTTL.
func (s *Service) ForgetStanding(tenantID uuid.UUID) {
	if s == nil {
		return
	}
	s.standing.mu.Lock()
	delete(s.standing.m, tenantID)
	s.standing.mu.Unlock()
}

// ReadStanding skips the cache, for the screen that shows the state and must
// not be five seconds behind the button somebody just pressed.
func (s *Service) ReadStanding(
	ctx context.Context, tenantID uuid.UUID,
) (Standing, error) {
	return s.readStanding(ctx, tenantID)
}

func (s *Service) readStanding(
	ctx context.Context, tenantID uuid.UUID,
) (Standing, error) {
	var tenantStatus, subStatus string
	var periodEnd *time.Time

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// A tenant with no subscription row reads as active with no end date.
		// After migration 0138 there are none, and if one appears the platform
		// dashboard counts it — this is not the place to start refusing.
		return tx.QueryRow(ctx, `
			SELECT t.status::text,
			       coalesce(s.status, 'active'),
			       s.current_period_end
			FROM tenant t
			LEFT JOIN subscription s ON s.tenant_id = t.id
			WHERE t.id = $1`, tenantID).
			Scan(&tenantStatus, &subStatus, &periodEnd)
	})
	if err != nil {
		return Standing{}, db.Translate(err, "")
	}
	return StandingOf(tenantStatus, subStatus, periodEnd, time.Now().UTC()), nil
}

// --- the Super Admin's controls ------------------------------------------

// Transition is a deliberate change to a business's standing.
type Transition struct {
	// Action is "suspend", "activate" or "deactivate".
	Action string
	// Reason is required for anything that stops a business trading. A
	// suspension nobody explained is one nobody can safely lift.
	Reason string
}

// SetTenantStanding suspends, reactivates or switches off a business.
//
// # Why these three and not a free-form status field
//
// Because two of them stop a business trading and one restarts it, and each
// needs a different thing said about it. A route that took `status` as a string
// would accept `deactivated` from a screen whose button said "suspend".
//
// # What it deliberately does not do
//
// Touch the subscription's own status. `tenant.status` is the switch an
// operator throws; `subscription.status` is what the commercial relationship
// says, and dunning owns it. Writing both from here would mean a manual
// suspension silently rewrote the billing record, and the next invoice payment
// would lift a suspension that had nothing to do with money — which is exactly
// the case `MarkPaid` guards against and would have been undone from here.
func (s *Service) SetTenantStanding(
	ctx context.Context, actorID, tenantID uuid.UUID, in Transition,
) (Standing, error) {
	var next string
	switch in.Action {
	case "suspend":
		next = "suspended"
	case "activate":
		next = "active"
	case "deactivate":
		next = "deactivated"
	default:
		return Standing{}, errs.New(errs.CodeInvalidInput,
			"A business can be suspended, activated, or deactivated.")
	}

	reason := strings.TrimSpace(in.Reason)
	if next != "active" && reason == "" {
		return Standing{}, errs.Validation(
			"Say why this business is being stopped.").
			WithField("reason",
				"Whoever lifts this later needs to know what it was for.")
	}

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var before string
		e := tx.QueryRow(ctx,
			`SELECT status::text FROM tenant WHERE id = $1 FOR UPDATE`,
			tenantID).Scan(&before)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That client was not found.")
		}
		if e != nil {
			return e
		}

		if before == next {
			// Not an error. Pressing suspend on a suspended business is a
			// person making sure, and answering "already suspended" as a
			// failure teaches them to distrust the screen.
			return nil
		}

		if _, e := tx.Exec(ctx,
			`UPDATE tenant SET status = $2::tenant_status WHERE id = $1`,
			tenantID, next); e != nil {
			return db.Translate(e, "That business could not be changed.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &tenantID, ActorID: &actorID,
			ActorLabel: audit.LabelFor(ctx, tx, actorID),
			Action:     "tenant_" + in.Action + "d",
			EntityType: "tenant", EntityID: &tenantID,
			Before: map[string]any{"status": before},
			After:  map[string]any{"status": next, "reason": reason},
		})
	})
	if err != nil {
		return Standing{}, db.Translate(err, "")
	}

	// So the operator who pressed it does not watch the business trade for
	// another five seconds in this process.
	s.ForgetStanding(tenantID)
	return s.readStanding(ctx, tenantID)
}
