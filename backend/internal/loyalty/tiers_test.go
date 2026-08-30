package loyalty

// Tiers and segments (blueprint B16). No database: both are pure functions of
// what a customer has spent, which is the whole reason neither is stored.

import (
	"testing"

	"github.com/shopspring/decimal"
)

func money(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func scheme() []Tier {
	return []Tier{
		{Key: "bronze", Name: "Bronze", MinSpend: "0"},
		{Key: "silver", Name: "Silver", MinSpend: "5000", Discount: "5"},
		{Key: "gold", Name: "Gold", MinSpend: "20000", Discount: "10"},
	}
}

func TestTheHighestRungCleared(t *testing.T) {
	at, next, gap := TierFor(scheme(), money("7500"))
	if at == nil || at.Key != "silver" {
		t.Fatalf("7500 puts a customer in %v, want silver", at)
	}
	if next == nil || next.Key != "gold" {
		t.Fatalf("the next rung is %v, want gold", next)
	}
	// A member who cannot see how far off the next rung is has been given a
	// badge rather than a reason to come back.
	if !gap.Equal(money("12500")) {
		t.Errorf("the gap to gold reads %s, want 12500", gap.String())
	}
}

func TestTheTopRungHasNothingAbove(t *testing.T) {
	at, next, _ := TierFor(scheme(), money("40000"))
	if at == nil || at.Key != "gold" {
		t.Fatalf("40000 puts a customer in %v, want gold", at)
	}
	if next != nil {
		t.Errorf("gold has %q above it, and it is the top rung", next.Key)
	}
}

// A shop that typed Gold above Silver should still get the right answer. The
// list comes out of a jsonb column somebody edited by hand.
func TestTiersOutOfOrderStillResolve(t *testing.T) {
	jumbled := []Tier{
		{Key: "gold", Name: "Gold", MinSpend: "20000"},
		{Key: "bronze", Name: "Bronze", MinSpend: "0"},
		{Key: "silver", Name: "Silver", MinSpend: "5000"},
	}
	at, next, _ := TierFor(jumbled, money("6000"))
	if at == nil || at.Key != "silver" {
		t.Fatalf("6000 resolved to %v against a jumbled list, want silver", at)
	}
	if next == nil || next.Key != "gold" {
		t.Fatalf("the next rung is %v, want gold", next)
	}
}

func TestASchemeWithNoTiers(t *testing.T) {
	at, next, _ := TierFor(nil, money("999999"))
	if at != nil || next != nil {
		t.Errorf("a scheme with no tiers put somebody in one: %v / %v", at, next)
	}
}

func TestSegments(t *testing.T) {
	vip := money("20000")

	for _, c := range []struct {
		name          string
		customerType  string
		visits        int
		lifetime      string
		daysSinceLast int
		want          string
	}{
		{"never bought anything", "retail", 0, "0", -1, "new"},
		{"a regular", "retail", 8, "3000", 12, "returning"},
		{"the best customer", "retail", 30, "50000", 3, "vip"},
		{"one purchase, long ago", "retail", 1, "400", 400, "at_risk"},
		{"a trade account", "wholesale", 2, "900", 20, "wholesale"},
		{"one recent purchase", "retail", 1, "400", 20, "retail"},
		// A wholesale customer who has not been in for a year is at risk, and
		// being told they are "wholesale" would hide that.
		{"a trade account gone quiet", "wholesale", 40, "80000", 400, "at_risk"},
	} {
		got := SegmentOf(c.customerType, c.visits, money(c.lifetime),
			c.daysSinceLast, vip)
		if got != c.want {
			t.Errorf("%s: segment %q, want %q", c.name, got, c.want)
		}
	}
}

// A shop that has set no tiers has no VIP threshold, and everybody would clear
// a threshold of zero.
func TestNobodyIsVipWithoutAThreshold(t *testing.T) {
	if got := SegmentOf("retail", 2, money("1000000"), 1, decimal.Zero); got == "vip" {
		t.Error("a scheme with no tiers made somebody VIP")
	}
}
