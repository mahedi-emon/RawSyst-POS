package accounting

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// Splitting a converted total across the legs of one journal entry.
//
// The rules are taken from migration 0015 — the CHECK constraints on
// journal_line and the deferred assert_entry_balanced trigger — rather than
// read off the implementation:
//
//  1. The shares sum to exactly the total. The same converted figure is
//     allocated to both sides, and assert_entry_balanced compares
//     sum(base_debit) to sum(base_credit) at commit. A hallala adrift is an
//     entry that will not commit.
//
//  2. Every share is STRICTLY positive. journal_line_base_one_side wants
//     base_debit > 0 on a debit line, and journal_line_sides_agree wants
//     (debit > 0) = (base_debit > 0). A line carrying a real amount whose base
//     share came out zero satisfies neither.
//
//  3. No share is negative, by journal_line_amounts_non_negative.
//
//  4. The shares follow the proportions, or the exercise is just a way of
//     making the arithmetic add up.
//
// Rules 2 and 3 are not about accuracy. They are CHECK constraints, so breaking
// either aborts the transaction: the sale, the receipt or the payment being
// recorded does not happen, and it will not happen on the retry either.

func money(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func moneys(values ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, len(values))
	for _, v := range values {
		out = append(out, money(v))
	}
	return out
}

// equalParts builds n legs of the same size followed by an explicit tail.
func equalParts(each string, n int, tail ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, n+len(tail))
	for i := 0; i < n; i++ {
		out = append(out, money(each))
	}
	return append(out, moneys(tail...)...)
}

// checkBaseAllocation applies all four rules to any entry shape.
func checkBaseAllocation(
	t *testing.T, name, totalText string, parts []decimal.Decimal,
) []decimal.Decimal {
	t.Helper()

	total := money(totalText)
	shares, err := allocate(total, parts)
	if err != nil {
		t.Fatalf("[%s] allocating %s across %d legs: %v", name, total, len(parts), err)
	}
	if len(shares) != len(parts) {
		t.Fatalf("[%s] %d shares for %d legs", name, len(shares), len(parts))
	}

	sum := decimal.Zero
	for i, share := range shares {
		sum = sum.Add(share)

		if share.IsNegative() {
			t.Errorf("[%s] leg %d was allocated %s of the base total. "+
				"journal_line_amounts_non_negative forbids a negative base "+
				"amount, so this does not misstate the entry -- the CHECK fires "+
				"and whatever was being recorded is rolled back.",
				name, i, share)
		}
		if share.IsZero() {
			t.Errorf("[%s] leg %d has a real amount of %s and a base amount of "+
				"nothing. journal_line_base_one_side wants the base amount "+
				"strictly positive and journal_line_sides_agree wants it on the "+
				"same side as the transaction amount; zero fails both, and the "+
				"entry cannot be written at all.", name, i, parts[i])
		}
	}

	if !sum.Equal(total) {
		t.Errorf("[%s] the shares sum to %s against a converted total of %s. "+
			"Both sides are allocated from the same total, so a discrepancy here "+
			"is one side of the entry disagreeing with the other in base "+
			"currency, and assert_entry_balanced refuses it at commit.",
			name, sum, total)
	}
	return shares
}

// THE FIRST DEFECT THIS FILE FOUND.
//
// Seven tenders on a foreign-currency sale, six of them equal and one of a
// single hallala.
//
// Worked by hand: the legs sum to 6.01 and convert to 9.05. Rounding each leg's
// own share gives round(9.05 x 1 / 6.01, 2) = 1.51 for each of the six -- rounded
// UP, because the true share is 1.505823... Six of those come to 9.06, already
// more than the whole, and the seventh leg was handed 9.05 - 9.06 = MINUS 0.01.
//
// A negative base amount violates journal_line_amounts_non_negative, which
// aborts the posting, which aborts the sale.
func TestNoJournalLegIsAllocatedANegativeBaseAmount(t *testing.T) {
	shares := checkBaseAllocation(t, "six tenders and a hallala", "9.05",
		equalParts("1.00", 6, "0.01"))

	// Stated as the whole expected allocation as well as through the invariant,
	// because this is the arithmetic the old code got wrong and the shape of the
	// right answer is easy to lose.
	for i, want := range []string{
		"1.51", "1.50", "1.51", "1.50", "1.51", "1.50", "0.02",
	} {
		if !shares[i].Equal(money(want)) {
			t.Errorf("leg %d carries %s, want %s", i, shares[i], want)
		}
	}
}

// THE SECOND DEFECT THIS FILE FOUND, and the likelier one.
//
// A hallala tender on a sale in a currency weaker than half the base currency.
//
// BDT 12,000.00 taken by a Saudi company at 0.0308 to the taka is SAR 369.60.
// The customer paid BDT 11,999.90 by card and BDT 0.10 in cash. That last leg is
// worth SAR 0.00308 -- under half a base hallala, so it rounds to nothing.
//
// The arithmetic is right and the entry is still unwritable, because
// journal_line_base_one_side wants a base amount strictly above zero. ANY rate
// below 0.5 does this to a small leg, which is every weaker currency a shop
// might take -- and this product is built for companies trading in Saudi
// Arabia, Bangladesh and the United States at once.
func TestALegTooSmallToConvertStillGetsABaseAmount(t *testing.T) {
	shares := checkBaseAllocation(t, "a hallala of change", "369.60",
		moneys("11999.90", "0.10"))

	if !shares[1].Equal(minLedgerUnit) {
		t.Errorf("the BDT 0.10 leg carries %s, want %s -- the smallest amount "+
			"the ledger can express, since it cannot carry nothing",
			shares[1], minLedgerUnit)
	}
	if !shares[0].Equal(money("369.59")) {
		t.Errorf("the card leg carries %s, want 369.59: what the small leg was "+
			"raised by has to come out of somewhere or the sides stop matching",
			shares[0])
	}
}

// Several legs too small to convert, all of them raised, all paid for by the one
// leg big enough to afford it.
func TestSeveralUnconvertibleLegsAreAllRaised(t *testing.T) {
	shares := checkBaseAllocation(t, "three hallalas of change", "5.00",
		moneys("1000.00", "0.01", "0.01", "0.01"))

	if !shares[0].Equal(money("4.97")) {
		t.Errorf("the large leg carries %s, want 4.97 -- three hallalas less "+
			"than the whole, which is what raising three legs cost", shares[0])
	}
}

// When no single leg can afford the whole raise, it is taken from more than one.
//
// Eighteen hallala legs and two large ones, converting to SAR 0.20 in total.
// Every small leg needs a hallala it has not got, which is 0.18, and neither
// large leg has that much to spare on its own.
func TestTheRaiseIsTakenFromMoreThanOneLegWhenItHasTo(t *testing.T) {
	parts := append(equalParts("1.00", 18), moneys("500.00", "500.00")...)
	shares := checkBaseAllocation(t, "eighteen small and two large", "0.20", parts)

	// Twenty legs and twenty hallalas: there is exactly one allocation that
	// satisfies the constraints, and this is it.
	for i, share := range shares {
		if !share.Equal(minLedgerUnit) {
			t.Errorf("leg %d carries %s, want %s", i, share, minLedgerUnit)
		}
	}
}

// A total that will not stretch to one unit a leg has no valid allocation at
// all, and saying so is the whole improvement.
//
// The alternative is three legs of which one gets nothing, a CHECK violation,
// and "that accounting entry could not be written" -- which tells whoever is
// standing at the till nothing they can act on.
func TestATotalTooSmallToDivideIsRefusedWithAReason(t *testing.T) {
	_, err := allocate(money("0.02"), moneys("1.00", "1.00", "1.00"))
	if err == nil {
		t.Fatal("SAR 0.02 was split across three legs, which cannot be done: " +
			"the ledger's smallest amount is a hallala and every leg needs one")
	}

	// The message has to name the figure and the number of legs, or it is not
	// actionable either.
	for _, want := range []string{"0.02", "3", "0.01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Exactly one unit a leg is the boundary and must still work.
func TestATotalOfExactlyOneUnitPerLegIsAllocated(t *testing.T) {
	shares := checkBaseAllocation(t, "at the floor", "0.02",
		moneys("1.00", "99.00"))

	for i, share := range shares {
		if !share.Equal(minLedgerUnit) {
			t.Errorf("leg %d carries %s, want %s", i, share, minLedgerUnit)
		}
	}
}

// The domestic case, which is nearly every entry in the product: no conversion,
// so each leg's base amount is its own amount and nothing is approximated.
//
// If this ever stops holding, every tie-out in the system moves -- inventory to
// the Inventory account, customer balances to receivables, supplier balances to
// payables -- because all of them read the base columns.
func TestWithNoConversionEveryLegKeepsItsOwnAmount(t *testing.T) {
	parts := moneys("87.01", "13.02")
	shares := checkBaseAllocation(t, "domestic", "100.03", parts)

	for i, share := range shares {
		if !share.Equal(parts[i]) {
			t.Errorf("leg %d has an amount of %s and a base amount of %s; with "+
				"no conversion they must be the same figure", i, parts[i], share)
		}
	}
}

// Proportionality, so the rules above are not satisfied by handing every leg a
// hallala and the remainder to the last one.
func TestBaseAmountsFollowTheProportionsOfTheLegs(t *testing.T) {
	shares := checkBaseAllocation(t, "proportional", "60.00",
		moneys("100.00", "200.00", "300.00"))

	for i, want := range []string{"10.00", "20.00", "30.00"} {
		if !shares[i].Equal(money(want)) {
			t.Errorf("leg %d carries %s of the base total, want %s",
				i, shares[i], want)
		}
	}
}

// The four rules across every entry shape worth worrying about.
//
// The rates are real ones: 3.75 is the riyal's peg to the dollar, 0.0308 is
// roughly the taka, and the eight-decimal ones are the shape rate_table holds.
func TestBaseAllocationHoldsAcrossEntryShapes(t *testing.T) {
	cases := map[string]struct {
		total string
		parts []decimal.Decimal
	}{
		"one leg each side": {
			"375.24", moneys("100.03"),
		},
		"a sale and its tax": {
			"375.00", moneys("87.00", "13.00"),
		},
		"three tenders": {
			"100.00", equalParts("1.00", 3),
		},
		"seven equal tenders": {
			"1.00", equalParts("1.00", 7),
		},
		"six tenders and change": {
			"9.05", equalParts("1.00", 6, "0.01"),
		},
		"twelve equal tenders": {
			"1000.00", equalParts("1.00", 12),
		},
		"a weak currency": {
			"369.60", moneys("11999.90", "0.10"),
		},
		"a strong currency": {
			"37512.35", moneys("9999.99", "0.01"),
		},
		"lopsided": {
			"250.00", moneys("0.01", "9999.99"),
		},
		"awkward thirds and a tail": {
			"7.77", equalParts("3.33", 20, "0.01"),
		},
		"twenty tiny legs": {
			"0.25", equalParts("0.01", 20),
		},
		"one large leg and nine hallalas": {
			"5.00", equalParts("0.01", 9, "1000.00"),
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			checkBaseAllocation(t, name, c.total, c.parts)
		})
	}
}

// A leg with no amount cannot be apportioned by, and must not be worked around.
//
// usableLines drops the zeroes and refuses the negatives before Post ever gets
// here, so nothing reaches this today. It is refused rather than absorbed
// because the obvious workaround -- the whole total on one leg and nothing on
// the rest -- writes exactly the zero base amount the floor exists to prevent.
func TestALegWithNoAmountIsRefusedRatherThanWorkedAround(t *testing.T) {
	for _, parts := range [][]decimal.Decimal{
		moneys("100.00", "0"),
		moneys("0", "0"),
		moneys("100.00", "-5.00"),
	} {
		if _, err := allocate(money("100.00"), parts); err == nil {
			t.Errorf("allocating across %v was accepted", parts)
		}
	}
}

// No legs at all allocates nothing, and must not index past the end of an empty
// slice, which is what the old code's out[len(out)-1] would have done.
func TestAnEntryWithNoLegsAllocatesNothing(t *testing.T) {
	shares, err := allocate(money("100.00"), nil)
	if err != nil {
		t.Fatalf("allocating across no legs: %v", err)
	}
	if len(shares) != 0 {
		t.Errorf("%d shares for no legs", len(shares))
	}
}
