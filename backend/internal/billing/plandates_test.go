// The subscription period, which is where an operator's typing meets a date.
//
// These run without a database on purpose. Every rule here is a decision about
// two strings and a clock, and the ones worth testing are the refusals -- a
// trial that ends before it begins, an expiry before the start, a lifetime plan
// given a renewal date. A refusal that is only ever exercised through HTTP and
// a live Postgres is a refusal nobody runs.
//
// The other half of what these pin down is quieter and matters more: what
// happens to a date the operator did NOT type. An empty start must not reset an
// existing subscription to today, and an empty expiry must be worked out from
// the start rather than from the clock. Both were wrong before, and neither
// produces an error when it is -- they produce a subscription with the wrong
// dates on it, which nobody notices until it is renewed or expired at the wrong
// moment.
package billing

import (
	"testing"
	"time"
)

// day parses a date the tests write, so a wrong literal fails here rather than
// silently becoming the zero time.
func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateOnly, s)
	if err != nil {
		t.Fatalf("test wrote a date that is not one: %q", s)
	}
	return d
}

func TestAMonthlyPlanEndsAMonthAfterItStarts(t *testing.T) {
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "monthly", StartedOn: "2026-01-15"},
		nil, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.StartedOn != "2026-01-15" {
		t.Errorf("start = %q, want the date that was typed", got.StartedOn)
	}
	// From the START, not from today. The old code took today unconditionally,
	// so backdating a start produced a period the operator did not ask for.
	if got.ExpiresOn != "2026-02-15" {
		t.Errorf("expiry = %q, want 2026-02-15 — a month after the start, not "+
			"a month after the day somebody happened to type it in",
			got.ExpiresOn)
	}
}

func TestAYearlyPlanEndsAYearAfterItStarts(t *testing.T) {
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "yearly", StartedOn: "2026-03-01"},
		nil, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ExpiresOn != "2027-03-01" {
		t.Errorf("expiry = %q, want 2027-03-01", got.ExpiresOn)
	}
}

func TestAnExplicitExpiryWins(t *testing.T) {
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "monthly", StartedOn: "2026-01-01", ExpiresOn: "2026-06-30"},
		nil, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ExpiresOn != "2026-06-30" {
		t.Errorf("expiry = %q, want the date the operator typed", got.ExpiresOn)
	}
}

func TestALifetimeSubscriptionHasNoExpiry(t *testing.T) {
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "lifetime", StartedOn: "2026-01-01"},
		nil, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ExpiresOn != "" {
		t.Errorf("expiry = %q, want none — that is what makes it a lifetime "+
			"subscription", got.ExpiresOn)
	}
}

func TestALifetimeSubscriptionRefusesAnExpiry(t *testing.T) {
	// Refused rather than ignored. An operator who typed a date into that box
	// believes something about the account, and silently dropping it leaves
	// them believing it.
	_, err := ResolvePlanDates(
		NewPlan{Cycle: "lifetime", ExpiresOn: "2027-01-01"},
		nil, day(t, "2026-09-11"))
	if err == nil {
		t.Fatal("a lifetime subscription was given a renewal date and accepted it")
	}
}

func TestASubscriptionCannotEndBeforeItStarts(t *testing.T) {
	for _, expiry := range []string{"2025-12-31", "2026-01-01"} {
		_, err := ResolvePlanDates(
			NewPlan{Cycle: "monthly", StartedOn: "2026-01-01", ExpiresOn: expiry},
			nil, day(t, "2026-09-11"))
		if err == nil {
			t.Errorf("expiry %q was accepted against a start of 2026-01-01; "+
				"a period of zero or negative days is not a subscription",
				expiry)
		}
	}
}

func TestATrialCannotEndBeforeTheSubscriptionStarts(t *testing.T) {
	_, err := ResolvePlanDates(
		NewPlan{Cycle: "monthly", StartedOn: "2026-06-01", TrialEndsOn: "2026-05-01"},
		nil, day(t, "2026-09-11"))
	if err == nil {
		t.Fatal("a trial ending a month before the subscription began was accepted")
	}
}

func TestSomethingThatIsNotADateIsRefused(t *testing.T) {
	for _, in := range []NewPlan{
		{Cycle: "monthly", StartedOn: "the first of June"},
		{Cycle: "monthly", ExpiresOn: "2026-13-45"},
		{Cycle: "monthly", TrialEndsOn: "next week"},
		// A timestamp, which is the most likely wrong thing to arrive here:
		// every other date in the product is written this way and a caller
		// sending one would otherwise be silently truncated or accepted.
		{Cycle: "monthly", StartedOn: "2026-06-01T00:00:00Z"},
	} {
		if _, err := ResolvePlanDates(in, nil, day(t, "2026-09-11")); err == nil {
			t.Errorf("accepted %+v as dates", in)
		}
	}
}

// The quiet one. A blank start box means "leave it alone".
func TestAnEmptyStartKeepsTheSubscriptionsOwnStart(t *testing.T) {
	existing := day(t, "2024-02-29")
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "yearly"}, &existing, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.StartedOn != "2024-02-29" {
		t.Fatalf("start = %q, want the subscription's own start. Resetting it "+
			"to today because somebody edited the price would silently rewrite "+
			"a two-year-old client's history", got.StartedOn)
	}
	// And the derived expiry follows the real start, not today.
	if got.ExpiresOn != "2025-02-28" && got.ExpiresOn != "2025-03-01" {
		t.Errorf("expiry = %q, want a year after 2024-02-29", got.ExpiresOn)
	}
}

// A subscription being written for the first time has no start to keep.
func TestAFirstSubscriptionWithNoDatesStartsToday(t *testing.T) {
	now := day(t, "2026-09-11")
	got, err := ResolvePlanDates(NewPlan{Cycle: "monthly"}, nil, now)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.StartedOn != "2026-09-11" {
		t.Errorf("start = %q, want today", got.StartedOn)
	}
	if got.ExpiresOn != "2026-10-11" {
		t.Errorf("expiry = %q, want 2026-10-11", got.ExpiresOn)
	}
}

// An unset expiry must still produce one, for any plan that has a cycle.
//
// This is the rule that makes expiry enforceable at all. A subscription with a
// NULL period end is one that nothing can ever find as expired, so a sweep
// looking for them would pass its own tests and enforce nothing.
func TestOnlyALifetimePlanIsAllowedToHaveNoEndDate(t *testing.T) {
	for _, cycle := range []string{"monthly", "yearly"} {
		got, err := ResolvePlanDates(NewPlan{Cycle: cycle}, nil, day(t, "2026-09-11"))
		if err != nil {
			t.Fatalf("%s: %v", cycle, err)
		}
		if got.ExpiresOn == "" {
			t.Errorf("a %s subscription was left with no end date at all; "+
				"nothing can ever find it expired", cycle)
		}
	}
}

// Whitespace is not a date, and it is what a box that was typed into and
// cleared actually sends.
func TestABoxThatWasClearedIsTreatedAsEmptyRatherThanInvalid(t *testing.T) {
	existing := day(t, "2025-05-05")
	got, err := ResolvePlanDates(
		NewPlan{Cycle: "monthly", StartedOn: "   ", ExpiresOn: "  "},
		&existing, day(t, "2026-09-11"))
	if err != nil {
		t.Fatalf("a cleared box was refused as an invalid date: %v", err)
	}
	if got.StartedOn != "2025-05-05" {
		t.Errorf("start = %q, want the existing start", got.StartedOn)
	}
}
