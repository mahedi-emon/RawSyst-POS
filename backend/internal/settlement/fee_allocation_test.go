package settlement

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Splitting an acquirer's fee across the tenders it covers.
//
// The arithmetic is stated here from first principles rather than copied from
// the implementation: each tender carries the share of the fee that its own
// amount bears to the batch, the shares sum back to the fee exactly, and no
// tender carries more fee than it was worth.
//
// The last of those three is the one that broke. Rounding each share on its
// own and handing the remainder to the final tender accumulates, and with
// twenty tenders of 3.33 against a fee of 7.77 the earlier shares round up to
// 7.80 between them, leaving the last tender a share of MINUS 0.03. A negative
// fee credits that sale with more than it was worth.

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// batchOf builds a batch from a list of tender amounts.
func batchOf(amounts ...string) (covered []coveredTender, gross decimal.Decimal) {
	gross = decimal.Zero
	for _, a := range amounts {
		covered = append(covered, coveredTender{amount: dec(a)})
		gross = gross.Add(dec(a))
	}
	return covered, gross
}

// repeat is the shape that found the defect: many identical tenders and one
// very small one to absorb whatever the rounding left behind.
func repeat(amount string, times int, tail string) []string {
	out := make([]string, 0, times+1)
	for i := 0; i < times; i++ {
		out = append(out, amount)
	}
	return append(out, tail)
}

// checkShares states the three rules and applies them to any batch.
func checkShares(t *testing.T, name string, amounts []string, feeText string) {
	t.Helper()

	covered, gross := batchOf(amounts...)
	fee := dec(feeText)
	if fee.GreaterThan(gross) {
		fee = gross
	}

	shares := allocateFee(fee, gross, covered)
	if len(shares) != len(covered) {
		t.Fatalf("[%s] %d shares for %d tenders", name, len(shares), len(covered))
	}

	sum := decimal.Zero
	for i, share := range shares {
		sum = sum.Add(share)

		if share.IsNegative() {
			t.Errorf("[%s] tender %d was allocated %s of fee. A negative fee "+
				"credits that sale with more than it was worth.", name, i, share)
		}
		if share.GreaterThan(covered[i].amount) {
			t.Errorf("[%s] tender %d is worth %s and was allocated %s of fee, "+
				"so the shop received less than nothing for that sale",
				name, i, covered[i].amount, share)
		}
	}

	if !sum.Equal(fee) {
		t.Errorf("[%s] the shares sum to %s against a fee of %s. The batch has "+
			"to account for the whole difference between what was taken and "+
			"what the acquirer deposited.", name, sum, fee)
	}
}

// THE DEFECT THIS FILE FOUND.
//
// Worked by hand: twenty tenders of 3.33 is 66.60, plus 0.01 makes a gross of
// 66.61. A fee of 7.77 gives each of the twenty 7.77 x 3.33 / 66.61 = 0.38843,
// which rounds to 0.39 — twenty of those is 7.80, already more than the fee.
// The last tender was then handed 7.77 - 7.80 = -0.03.
func TestNoTenderIsAllocatedANegativeFee(t *testing.T) {
	checkShares(t, "twenty and a hallala", repeat("3.33", 20, "0.01"), "7.77")
}

// The same three rules across every batch shape worth worrying about.
func TestFeeAllocationHoldsAcrossBatchShapes(t *testing.T) {
	cases := map[string]struct {
		amounts []string
		fee     string
	}{
		"thirds":               {[]string{"33.33", "33.33", "33.34"}, "10.00"},
		"tiny last":            {[]string{"99.99", "0.01"}, "10.00"},
		"tiny first":           {[]string{"0.01", "99.99"}, "10.00"},
		"one tender":           {[]string{"250.00"}, "6.25"},
		"identical tenders":    {repeat("10.00", 7, "10.00"), "3.00"},
		"awkward thirds":       {repeat("3.33", 20, "0.01"), "7.77"},
		"many small":           {repeat("1.01", 20, "0.03"), "5.00"},
		"fee is the whole lot": {[]string{"10.00", "20.00", "0.01"}, "30.01"},
		"fee of one hallala":   {repeat("5.00", 9, "5.00"), "0.01"},
		"large batch":          {repeat("1999.99", 12, "0.07"), "412.34"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			checkShares(t, name, c.amounts, c.fee)
		})
	}
}

// No fee is not a fee. Every share must be zero rather than nothing being
// returned, because the caller indexes the result alongside the tenders.
func TestAZeroFeeAllocatesNothingToEveryone(t *testing.T) {
	covered, gross := batchOf("10.00", "20.00", "30.00")

	for _, fee := range []string{"0", "-1.00"} {
		shares := allocateFee(dec(fee), gross, covered)
		if len(shares) != len(covered) {
			t.Fatalf("fee %s: %d shares for %d tenders", fee, len(shares), len(covered))
		}
		for i, s := range shares {
			if !s.IsZero() {
				t.Errorf("fee %s: tender %d got %s, want nothing", fee, i, s)
			}
		}
	}
}

// The proportions must still be right, not merely safe. A tender worth twice
// another carries twice the fee.
func TestTheFeeFollowsWhatEachTenderIsWorth(t *testing.T) {
	covered, gross := batchOf("100.00", "200.00", "300.00")
	shares := allocateFee(dec("60.00"), gross, covered)

	// 600 gross, 60 fee: a flat tenth, so 10, 20 and 30.
	for i, want := range []string{"10", "20", "30"} {
		if !shares[i].Equal(dec(want)) {
			t.Errorf("tender %d carries %s of the fee, want %s", i, shares[i], want)
		}
	}
}
