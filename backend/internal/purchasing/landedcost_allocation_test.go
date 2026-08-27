package purchasing

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Splitting freight and duty across the lines of a delivery.
//
// The rules are stated here from the schema and the accounting requirement
// rather than read off the implementation:
//
//  1. The shares sum to exactly what was paid. Freight is capitalised into
//     unit costs, those unit costs are what the Inventory account is debited
//     with, and C13 requires the valuation to tie to that account exactly. A
//     hallala unaccounted for breaks the invariant as surely as a million.
//
//  2. No share is negative. `grn_line_landed_alloc_sane` is a CHECK, so a
//     negative share does not produce a slightly wrong number — it aborts the
//     transaction and the delivery cannot be recorded at all.
//
//  3. A line that kept nothing carries nothing. The shop is not warehousing
//     goods it sent straight back, and the caller skips such a line when
//     raising unit costs, so a share allocated to one is a share that vanishes
//     from the valuation while grn_line still claims it.
//
//  4. The shares are proportional to the weights, or the whole exercise is
//     just a way of making the arithmetic add up.

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decs(values ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, len(values))
	for _, v := range values {
		out = append(out, dec(v))
	}
	return out
}

// repeated builds n weights of the same size followed by an explicit tail.
func repeated(each string, n int, tail ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, n+len(tail))
	for i := 0; i < n; i++ {
		out = append(out, dec(each))
	}
	return append(out, decs(tail...)...)
}

// checkAllocation applies all four rules to any receipt shape.
func checkAllocation(
	t *testing.T, name, totalText string, weights, quantities []decimal.Decimal,
) []decimal.Decimal {
	t.Helper()

	total := dec(totalText)
	shares := allocateLandedCost(total, weights, quantities)

	if len(shares) != len(weights) {
		t.Fatalf("[%s] %d shares for %d lines", name, len(shares), len(weights))
	}

	sum := decimal.Zero
	for i, share := range shares {
		sum = sum.Add(share)

		if share.IsNegative() {
			t.Errorf("[%s] line %d was allocated %s of freight. "+
				"grn_line_landed_alloc_sane forbids a negative allocation, so "+
				"this does not merely misstate the cost -- the CHECK fires, the "+
				"receipt is rolled back, and a delivery that really arrived "+
				"cannot be entered however many times it is retried.",
				name, i, share)
		}
		if quantities[i].IsZero() && share.IsPositive() {
			t.Errorf("[%s] line %d kept nothing and was allocated %s of "+
				"freight. There are no units on that line to raise the cost of, "+
				"so the caller skips it and the allocation disappears from the "+
				"valuation while grn_line goes on claiming it.",
				name, i, share)
		}
	}

	if !sum.Equal(total) {
		t.Errorf("[%s] the shares sum to %s against %s of freight paid. The "+
			"whole allocation is capitalised into unit costs and debited to the "+
			"Inventory account, so anything unaccounted for parts the valuation "+
			"from the ledger and C13's tie-out fails by exactly this much.",
			name, sum, total)
	}
	return shares
}

// THE DEFECT THIS FILE FOUND.
//
// Seven lines, SAR 100 of freight, and the last line sent back damaged.
//
// Worked by hand: six lines of equal value share the freight six ways, and
// round(100 / 6, 4) is 16.6667 -- rounded UP, because the true share is
// 16.66666... Six of those come to 100.0002, already more than was paid. The
// seventh line was then handed 100 - 100.0002 = MINUS 0.0002.
//
// A negative allocation is not a rounding blemish here. It violates a CHECK
// constraint, which aborts the transaction that was recording the delivery.
func TestNoLineIsAllocatedNegativeFreight(t *testing.T) {
	// Six lines of one unit at SAR 1.00, then a line that kept nothing.
	checkAllocation(t, "six and a rejected line", "100.00",
		repeated("1", 6, "0"), repeated("1", 6, "0"))
}

// The same four rules across every receipt shape worth worrying about.
//
// Several of these round up on the repeated lines, which is the direction that
// breaks: rounding down leaves the last line MORE than its share, which is
// merely inaccurate, while rounding up takes it below zero.
func TestFreightAllocationHoldsAcrossReceiptShapes(t *testing.T) {
	cases := map[string]struct {
		total      string
		weights    []decimal.Decimal
		quantities []decimal.Decimal
	}{
		"three by thirds": {
			"500.00", decs("100", "100", "100"), decs("1", "1", "1"),
		},
		"six then rejected": {
			"100.00", repeated("1", 6, "0"), repeated("1", 6, "0"),
		},
		"six then a hallala": {
			"100.00", repeated("1", 6, "0.01"), repeated("1", 6, "1"),
		},
		"twelve equal": {
			"1000.00", repeated("1", 12), repeated("1", 12),
		},
		"seven equal": {
			"1.00", repeated("1", 7), repeated("1", 7),
		},
		"lopsided": {
			"250.00", decs("0.01", "9999.99"), decs("1", "1"),
		},
		"tiny freight many lines": {
			"0.01", repeated("1", 20), repeated("1", 20),
		},
		"one line": {
			"75.50", decs("400"), decs("4"),
		},
		"middle line rejected": {
			"100.00", decs("50", "0", "50"), decs("1", "0", "1"),
		},
		"only the first line kept": {
			"33.33", decs("10", "0", "0"), decs("1", "0", "0"),
		},
		"awkward thirds and a tail": {
			"7.77", repeated("3.33", 20, "0.01"), repeated("1", 20, "1"),
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			checkAllocation(t, name, c.total, c.weights, c.quantities)
		})
	}
}

// A rejected line must carry nothing even when it is not the last one, which is
// the case the old remainder rule happened to get right and so never noticed.
func TestRejectedLinesCarryNoFreightWhereverTheySit(t *testing.T) {
	weights := decs("100", "0", "100", "0", "100")
	quantities := decs("1", "0", "1", "0", "1")

	shares := checkAllocation(t, "rejects interleaved", "90.00",
		weights, quantities)

	for _, i := range []int{1, 3} {
		if !shares[i].IsZero() {
			t.Errorf("line %d was sent back in full and carries %s of freight",
				i, shares[i])
		}
	}
}

// Proportionality, so the rules above are not satisfied by allocating nothing
// to everyone and the whole lot to one line.
func TestFreightFollowsWhatEachLineIsWorth(t *testing.T) {
	// 600 of goods, 60 of freight: a flat tenth of each line's value.
	shares := checkAllocation(t, "proportional", "60.00",
		decs("100", "200", "300"), decs("1", "2", "3"))

	for i, want := range []string{"10", "20", "30"} {
		if !shares[i].Equal(dec(want)) {
			t.Errorf("line %d carries %s of the freight, want %s",
				i, shares[i], want)
		}
	}
}

// Free goods still cost money to ship.
//
// Under the value basis every weight is zero, so value cannot divide the cost
// at all. Quantity still can, and it is the right answer: freight belongs to the
// units carried, whatever they were invoiced at.
//
// The old code put the whole cost on the FIRST line, which is wrong even when
// that line kept goods — it loads one line with freight that belongs to all of
// them — and silently loses the freight entirely when the first line was the
// rejected one.
func TestFreightOnFreeGoodsIsSpreadByQuantity(t *testing.T) {
	// Three lines of free samples, 2, 3 and 5 units. Every value weight is zero.
	shares := checkAllocation(t, "free samples", "100.00",
		decs("0", "0", "0"), decs("2", "3", "5"))

	for i, want := range []string{"20", "30", "50"} {
		if !shares[i].Equal(dec(want)) {
			t.Errorf("line %d of free goods carries %s of the freight, want %s "+
				"-- freight belongs to the units carried, not to whichever line "+
				"happens to be first", i, shares[i], want)
		}
	}
}

// Free goods where the first line was rejected: the case that used to lose the
// freight outright, because the whole cost went to a line the caller then
// skipped for having no units.
func TestFreightOnFreeGoodsSkipsTheRejectedFirstLine(t *testing.T) {
	shares := checkAllocation(t, "free, first rejected", "100.00",
		decs("0", "0", "0"), decs("0", "1", "1"))

	if !shares[0].IsZero() {
		t.Errorf("the rejected first line carries %s of freight", shares[0])
	}
}

// A consignment refused in full.
//
// Nothing was kept, so there is no stock to capitalise freight into and no unit
// cost that could carry it. Allocating it anyway would write a figure into
// grn_line that the valuation never sees, which is worse than allocating
// nothing: it reads as though the cost was accounted for.
//
// The freight was still really paid, and where it belongs instead — an expense
// for a delivery that was refused — is an accounting decision this function is
// not the place to take.
func TestNothingIsAllocatedWhenTheWholeDeliveryIsRejected(t *testing.T) {
	shares := allocateLandedCost(dec("100.00"),
		decs("0", "0"), decs("0", "0"))

	for i, share := range shares {
		if !share.IsZero() {
			t.Errorf("line %d kept nothing and carries %s of freight", i, share)
		}
	}
}

// No freight is not freight, and the caller indexes the result alongside the
// lines, so a share is still owed for each one.
func TestZeroFreightAllocatesNothingToEveryLine(t *testing.T) {
	for _, total := range []string{"0", "-1.00"} {
		shares := allocateLandedCost(dec(total),
			decs("100", "200"), decs("1", "2"))
		if len(shares) != 2 {
			t.Fatalf("freight %s: %d shares for 2 lines", total, len(shares))
		}
		for i, share := range shares {
			if !share.IsZero() {
				t.Errorf("freight %s: line %d got %s, want nothing",
					total, i, share)
			}
		}
	}
}

// A receipt with no lines at all must not panic on the empty slice, which the
// old code's `out[len(weights)-1]` would have done if it ever got past the
// guard.
func TestAnEmptyReceiptAllocatesNothing(t *testing.T) {
	if shares := allocateLandedCost(dec("100"), nil, nil); len(shares) != 0 {
		t.Errorf("%d shares for no lines", len(shares))
	}
}
