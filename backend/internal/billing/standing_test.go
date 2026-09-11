// Where a business stands, and what that stops it doing.
//
// No database. The precedence in `StandingOf` is the whole of Phase 5's policy,
// and it should be provable without a Postgres, a clock or an HTTP request in
// the way. The cases that matter most are the boundaries — the last day paid
// for, and which of two reasons wins when both apply — because those are the
// ones that decide whether a real shop can take money this morning.

package billing

import (
	"strings"
	"testing"
	"time"
)

func on(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateOnly, s)
	if err != nil {
		t.Fatalf("test wrote a date that is not one: %q", s)
	}
	return d
}

func ptr(t *testing.T, s string) *time.Time {
	d := on(t, s)
	return &d
}

// today is a fixed "now" every case below is measured against.
func today(t *testing.T) time.Time { return on(t, "2026-09-12") }

func TestATradingBusinessMayTrade(t *testing.T) {
	for _, status := range []string{"active", "trialing"} {
		got := StandingOf("active", status, ptr(t, "2027-01-01"), today(t))
		if !got.Write || !got.Read || !got.SignIn {
			t.Errorf("%s: %+v — a paid-up business must be able to work", status, got)
		}
		if got.State != status {
			t.Errorf("%s: state = %q", status, got.State)
		}
		if got.Blocks() != "" {
			t.Errorf("%s: says it is blocked: %q", status, got.Blocks())
		}
	}
}

// Late is not stopped.
//
// Dunning decides when a late payment becomes a suspension, using `grace_days`.
// Until it does, the shop keeps trading — a till that stopped taking money the
// day an invoice fell due would cost a client more than the invoice.
func TestAPastDueBusinessKeepsTrading(t *testing.T) {
	got := StandingOf("active", "past_due", ptr(t, "2027-01-01"), today(t))
	if !got.Write {
		t.Error("a past-due business was stopped from trading; dunning decides that, not this")
	}
	if got.State != StandingPastDue {
		t.Errorf("state = %q, want past_due — the warning depends on it", got.State)
	}
}

// The boundary, which is the case a real shop meets.
func TestTheLastDayPaidForIsStillAPaidDay(t *testing.T) {
	// Ends today: still trading, all day.
	if got := StandingOf("active", "active", ptr(t, "2026-09-12"), today(t)); !got.Write {
		t.Error("a subscription ending today stopped trading this morning. " +
			"The end date is inclusive: cutting somebody off on their last " +
			"paid day earns a refund and an apology.")
	}
	// Ended yesterday: expired.
	if got := StandingOf("active", "active", ptr(t, "2026-09-11"), today(t)); got.Write {
		t.Error("a subscription that ended yesterday is still trading")
	}
	// Ends tomorrow: trading.
	if got := StandingOf("active", "active", ptr(t, "2026-09-13"), today(t)); !got.Write {
		t.Error("a subscription ending tomorrow is not trading today")
	}
}

// Expiry beats the column, because the column can be stale and the calendar
// cannot.
func TestASubscriptionMarkedActiveStillExpires(t *testing.T) {
	got := StandingOf("active", "active", ptr(t, "2025-03-01"), today(t))
	if got.State != StandingExpired {
		t.Fatalf("state = %q, want expired. The row says active and the period "+
			"ended eighteen months ago; believing the column over the calendar "+
			"is how the figure an operator trusts stops matching the money.",
			got.State)
	}
	if got.Write {
		t.Error("an expired subscription can still change the business")
	}
	if !got.Read || !got.SignIn {
		t.Error("an expired business was locked out. It has to be able to see " +
			"what it owes and export its records.")
	}
}

// A lifetime subscription has no end date, which is what makes it one.
func TestALifetimeSubscriptionNeverExpires(t *testing.T) {
	got := StandingOf("active", "active", nil, on(t, "2099-01-01"))
	if got.State != StandingActive || !got.Write {
		t.Errorf("%+v — a subscription with no end date expired", got)
	}
}

// The precedence. Each of these has two reasons to stop, and the word the
// business is given decides which screen they go to.
func TestTheMostSeriousReasonIsTheOneReported(t *testing.T) {
	past := ptr(t, "2020-01-01")

	cases := []struct {
		name         string
		tenantStatus string
		subStatus    string
		end          *time.Time
		want         string
	}{
		// Switched off outranks everything. Whatever the subscription says,
		// nobody signs in.
		{"deactivated over expired", "deactivated", "active", past, StandingDeactivated},
		{"deactivated over suspended", "deactivated", "suspended", nil, StandingDeactivated},

		// A person decided this. Telling them their subscription expired would
		// send them to a payment screen that will not help.
		{"suspended over expired", "suspended", "active", past, StandingSuspended},
		{"subscription suspended over expired", "active", "suspended", past, StandingSuspended},

		// They left. Also not a payment problem.
		{"cancelled over expired", "active", "cancelled", past, StandingCancelled},

		// Only now does the calendar speak.
		{"expired over past_due", "active", "past_due", past, StandingExpired},
	}

	for _, c := range cases {
		got := StandingOf(c.tenantStatus, c.subStatus, c.end, today(t))
		if got.State != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got.State, c.want)
		}
	}
}

// Only one state stops somebody getting in at all.
func TestOnlyADeactivatedBusinessIsLockedOut(t *testing.T) {
	for _, c := range []struct {
		tenantStatus, subStatus string
		end                     *time.Time
	}{
		{"active", "active", ptr(t, "2020-01-01")}, // expired
		{"suspended", "active", nil},               // suspended
		{"active", "cancelled", nil},               // cancelled
		{"active", "past_due", nil},                // past due
	} {
		got := StandingOf(c.tenantStatus, c.subStatus, c.end, today(t))
		if !got.SignIn || !got.Read {
			t.Errorf("%+v locked somebody out. Everything short of "+
				"deactivation keeps reads: they have to be able to see what "+
				"they owe and take their data with them.", got)
		}
	}

	off := StandingOf("deactivated", "active", nil, today(t))
	if off.SignIn || off.Read || off.Write {
		t.Errorf("%+v — a deactivated business let somebody in", off)
	}
}

// Every refusal names its remedy, and they are not interchangeable.
func TestEachRefusalSaysWhatToDoAboutIt(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range []struct{ tenantStatus, subStatus string }{
		{"deactivated", "active"},
		{"suspended", "active"},
		{"active", "cancelled"},
	} {
		msg := StandingOf(c.tenantStatus, c.subStatus, nil, today(t)).Blocks()
		if msg == "" {
			t.Errorf("%v refuses and says nothing about why", c)
		}
		if seen[msg] {
			t.Errorf("%v gives the same sentence as another state; the "+
				"remedies differ and the words have to", c)
		}
		seen[msg] = true
	}

	// The expiry message names the date, because "renew it" is unanswerable
	// without knowing when it lapsed.
	expired := StandingOf("active", "active", ptr(t, "2026-03-03"), today(t))
	if msg := expired.Blocks(); msg == "" {
		t.Fatal("an expired subscription refuses silently")
	} else if want := "2026-03-03"; !strings.Contains(msg, want) {
		t.Errorf("the expiry message does not name the date: %q", msg)
	}
}

// A tenant with no subscription row reads as active rather than as stopped.
//
// After migration 0138 there are none. If one appears, refusing every write on
// the platform for it would be a worse failure than the missing row, and the
// dashboard already counts them so somebody can go and look.
func TestAMissingSubscriptionDoesNotStopABusiness(t *testing.T) {
	got := StandingOf("active", "active", nil, today(t))
	if !got.Write {
		t.Error("a business with no subscription row was stopped from trading")
	}
}
