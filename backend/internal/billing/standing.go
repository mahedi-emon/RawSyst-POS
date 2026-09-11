// Whether a business may still trade, and why not.
//
// # The gap this closes
//
// `subscription.status` and `tenant.status` have both been written since the
// billing tables were built. Dunning moves a subscription to `past_due` and
// then `suspended`, sets the tenant to `suspended`, and records it. Paying the
// last outstanding invoice lifts both. The plan editor writes a status and a
// period end and validates the dates.
//
// Every one of those wrote. Nothing read. Before this file, `tenant.status` was
// consulted by exactly two queries in the whole product, and both were counters
// on the platform dashboard. A business marked suspended signed in and traded
// exactly as before, and a subscription whose period ended eighteen months ago
// was indistinguishable from one paid up this morning.
//
// # Why expiry is computed rather than stored
//
// A subscription whose `current_period_end` has passed is expired at midnight,
// with nothing running. No job, no sweep, no row to update — which means no
// window in which a lapsed subscription is still trading because the nightly
// task has not fired yet, and nothing to go wrong on the night a worker is down.
//
// It also means expiry cannot drift from the date an operator typed. The date
// IS the rule.
//
// # Expired is not suspended
//
// Four different situations, four different words, because the remedies differ
// and an operator reading one needs to know which conversation to have:
//
//   - expired      — the calendar ran out. Renew it.
//   - past_due     — an invoice is late but the grace period has not run out.
//   - suspended    — somebody or something stopped this account deliberately.
//   - cancelled    — the client left.
//   - deactivated  — the tenant itself is switched off. Nobody signs in.
//
// `grace_days` belongs to the unpaid-invoice path and is deliberately not
// applied here. It is the concession on a late payment, not a general licence to
// keep trading past the date on the agreement.

package billing

import "time"

// The states a business can be in, in the order of how much they stop.
const (
	StandingActive      = "active"
	StandingTrialing    = "trialing"
	StandingPastDue     = "past_due"
	StandingExpired     = "expired"
	StandingSuspended   = "suspended"
	StandingCancelled   = "cancelled"
	StandingDeactivated = "deactivated"
)

// Standing is what the product may let a business do right now.
type Standing struct {
	// State is one of the constants above: the single word for the situation.
	State string `json:"state"`

	// Status and TenantStatus are what the two tables actually say, kept
	// alongside the verdict so a screen can explain it rather than only obey
	// it. A business told "read only" asks why, and "your subscription ended
	// on the 3rd of March" is the answer.
	Status       string `json:"subscription_status"`
	TenantStatus string `json:"tenant_status"`

	// ExpiresOn is the last day paid for, empty for a lifetime subscription.
	ExpiresOn string `json:"expires_on,omitempty"`

	// The three questions anything ever asks of this.
	SignIn bool `json:"sign_in_allowed"`
	Read   bool `json:"read_allowed"`
	Write  bool `json:"write_allowed"`
}

// StandingOf works out where a business stands, from what the two tables say
// and what day it is.
//
// Pure, and separated from the query for that reason: the precedence below is
// the whole policy, and it should be testable without a database, a clock, or
// an HTTP request in the way.
//
// # The order matters
//
// Most severe first, and each rung answers a different question. A tenant that
// is switched off is switched off whatever its subscription says. A deliberate
// suspension outranks a date, because somebody decided it. A cancelled client
// outranks an expiry for the same reason. Only then does the calendar speak.
//
// Reversing any two of these produces a product that tells somebody their
// subscription expired when in fact a person suspended them, which sends them
// to a payment screen that will not help.
func StandingOf(
	tenantStatus, subStatus string, periodEnd *time.Time, today time.Time,
) Standing {
	out := Standing{
		Status: subStatus, TenantStatus: tenantStatus,
		SignIn: true, Read: true,
	}
	if periodEnd != nil {
		out.ExpiresOn = periodEnd.Format(dateOnly)
	}

	switch {
	case tenantStatus == "deactivated":
		// The only state that stops a person signing in at all. Everything
		// else lets them in to see where they stand.
		out.State = StandingDeactivated
		out.SignIn, out.Read = false, false

	case tenantStatus == "suspended" || subStatus == "suspended":
		out.State = StandingSuspended

	case subStatus == "cancelled":
		out.State = StandingCancelled

	case expired(periodEnd, today):
		// Whatever the row says its status is. A subscription marked `active`
		// whose period ended last March is not active, and believing the
		// column over the calendar is how the figure an operator trusts stops
		// matching the money.
		out.State = StandingExpired

	case subStatus == StandingPastDue:
		// Late, not stopped. Dunning decides when a late payment becomes a
		// suspension, using `grace_days`; until it does, the shop keeps
		// trading and is warned.
		out.State = StandingPastDue
		out.Write = true

	case subStatus == StandingTrialing:
		out.State = StandingTrialing
		out.Write = true

	default:
		out.State = StandingActive
		out.Write = true
	}

	return out
}

// expired is whether the last day paid for is behind us.
//
// No period end means a lifetime subscription, which never expires — that is
// what makes it one. The comparison is on whole days and the end date is
// INCLUSIVE: a subscription running until the 3rd is still running all day on
// the 3rd, and a business cut off on the morning of its last paid day would be
// owed a refund and an apology.
func expired(periodEnd *time.Time, today time.Time) bool {
	if periodEnd == nil {
		return false
	}
	end := periodEnd.UTC().Truncate(24 * time.Hour)
	now := today.UTC().Truncate(24 * time.Hour)
	return now.After(end)
}

// Warns is what to tell a business that is still trading but should not relax.
//
// Separate from Blocks because the two are different in kind and a screen shows
// them differently: this is a warning about something that has not happened
// yet, and Blocks explains something that already has. Folding them into one
// string would make a shop that is trading normally read a sentence in the same
// red as one that has been cut off.
//
// Empty when there is nothing to say.
func (s Standing) Warns() string {
	if s.State != StandingPastDue {
		return ""
	}
	return "There is an unpaid subscription invoice. The business is still " +
		"trading, and will be suspended if it stays unpaid past the agreed " +
		"grace period."
}

// Blocks says why a write was refused, in words for the person who tried.
//
// Empty when nothing is blocking. Each names the remedy, because a refusal that
// does not is a support ticket.
func (s Standing) Blocks() string {
	switch s.State {
	case StandingDeactivated:
		return "This business has been switched off. Contact RawSyst."
	case StandingSuspended:
		return "This business is suspended, so it cannot be changed. You can " +
			"still read your records and export them. Settle the account or " +
			"contact RawSyst to lift it."
	case StandingCancelled:
		return "This subscription has been cancelled, so the business cannot " +
			"be changed. You can still read your records and export them."
	case StandingExpired:
		if s.ExpiresOn != "" {
			return "This subscription ended on " + s.ExpiresOn + ", so the " +
				"business cannot be changed. You can still read your records " +
				"and export them. Renew it to carry on trading."
		}
		return "This subscription has ended, so the business cannot be " +
			"changed. You can still read your records and export them."
	}
	return ""
}
