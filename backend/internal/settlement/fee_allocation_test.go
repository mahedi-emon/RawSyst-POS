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

// THE DEFECT THIS FILE FOUND ON ITS SECOND PASS.
//
// The per-tender cap that stops a share exceeding its own tender passes the
// held-back excess to the NEXT tender, because the next share is measured
// against a cumulative target. The last tender has no next one.
//
// So a batch whose final share is capped allocates less than the fee, and
// nothing said so: the batch header still records the whole fee, the ledger
// still posts the whole fee, and only the per-sale figures — which is what a
// margin-by-payment-method report is built from — quietly come up short.
//
// An acquirer taking very nearly the whole batch is what makes it reachable.
// That is a chargeback-heavy or a fraud-hold settlement rather than an ordinary
// day, which is exactly the settlement somebody will be reading line by line.
func TestEveryShareTogetherIsTheFeeOnTheStatement(t *testing.T) {
	// Amounts chosen so the cap bites: a fee within a hallala of the gross
	// leaves each share equal to its own tender, and the rounding of the
	// cumulative targets has nowhere to go but the cap.
	for _, tc := range []struct {
		name    string
		amounts []string
		fee     string
	}{
		{"the acquirer took almost all of it",
			[]string{"0.01", "0.01", "0.01", "100.00"}, "100.02"},
		{"a tiny tail after a large tender",
			[]string{"100.00", "0.01"}, "100.00"},
		{"three equal thirds of an odd fee",
			[]string{"33.33", "33.33", "33.34"}, "99.99"},
		{"many tenders and a fee that does not divide",
			repeat("3.33", 20, "0.01"), "66.60"},
		{"a fee of one hallala across a large batch",
			repeat("10.00", 50, "0.01"), "0.01"},
		{"the whole gross but a hallala",
			[]string{"5.00", "5.00", "5.00"}, "14.99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			covered, gross := batchOf(tc.amounts...)
			fee := dec(tc.fee)
			if fee.GreaterThanOrEqual(gross) {
				t.Fatalf("the case is not one Record can produce: a deposit is "+
					"positive, so the fee %s is always below the gross %s",
					fee, gross)
			}

			shares := allocateFee(fee, gross, covered)

			total := decimal.Zero
			for i, s := range shares {
				if s.IsNegative() {
					t.Errorf("tender %d carries a fee of %s; a negative fee "+
						"credits a sale with more than it was worth", i, s)
				}
				if s.GreaterThan(covered[i].amount) {
					t.Errorf("tender %d is worth %s and carries %s of fee; it "+
						"cannot cost more to receive than it was",
						i, covered[i].amount, s)
				}
				total = total.Add(s)
			}
			if !total.Equal(fee) {
				t.Errorf("the shares come to %s against a fee of %s on the bank "+
					"statement; the difference is %s that no sale is shown to "+
					"have paid", total, fee, fee.Sub(total))
			}
		})
	}
}

// The same three invariants, swept rather than hand-picked.
//
// The hand-written cases above all sit on the hallala grid, and on that grid
// the per-tender cap provably cannot bite: the exact share of a tender worth
// n hallalas is below n hallalas whenever the fee is below the gross, and
// rounding two cumulative targets to the same grid can separate them by at
// most one hallala more. So those cases exercise the arithmetic and cannot
// reach the edge of it.
//
// sales_tender.amount is numeric(18,4), not numeric(18,2). A tender worth
// three and a half hallalas is representable, arrives whenever a foreign
// currency is converted, and is exactly where the cap does bite — the share
// rounds up past an amount that is not itself a whole hallala, the cap holds
// it back, and on the LAST tender there is no later share to pass the excess
// to. The batch then reports a fee the per-sale figures do not add up to.
//
// A sweep rather than a worked example, because the case needs the fee, the
// gross and the position of one tender to line up, and a test that names one
// such alignment proves less than one that tries several thousand.
func TestTheSharesSumToTheFeeAtEveryScaleATenderCanCarry(t *testing.T) {
	// A deterministic walk, so a failure is reproducible and a green run is
	// not luck. The generator is a plain LCG rather than math/rand so the
	// sequence does not move with the Go version.
	seed := uint64(20260828)
	next := func(n uint64) uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return (seed >> 33) % n
	}

	for iteration := 0; iteration < 4000; iteration++ {
		count := int(next(6)) + 2
		covered := make([]coveredTender, 0, count)
		gross := decimal.Zero
		for i := 0; i < count; i++ {
			// Four decimal places, which is what the column holds, and small
			// enough that a fee near the gross leaves fractions of a hallala
			// in play.
			amount := decimal.New(int64(next(20000)+1), -4)
			covered = append(covered, coveredTender{amount: amount})
			gross = gross.Add(amount)
		}

		// The fee is what the acquirer kept, so it is the gross less a deposit
		// that Record insists is positive: strictly below the gross, and on
		// the hallala grid because that is the scale a bank statement is in.
		grossHallalas := gross.Shift(2).IntPart()
		if grossHallalas < 2 {
			continue
		}
		fee := decimal.New(int64(next(uint64(grossHallalas-1)))+1, -2)

		shares := allocateFee(fee, gross, covered)

		total := decimal.Zero
		for i, s := range shares {
			if s.IsNegative() {
				t.Fatalf("iteration %d: tender %d of %s carries a fee of %s",
					iteration, i, covered[i].amount, s)
			}
			if s.GreaterThan(covered[i].amount) {
				t.Fatalf("iteration %d: tender %d is worth %s and carries %s "+
					"of fee", iteration, i, covered[i].amount, s)
			}
			total = total.Add(s)
		}
		if !total.Equal(fee) {
			amounts := make([]string, len(covered))
			for i, c := range covered {
				amounts[i] = c.amount.String()
			}
			t.Fatalf("iteration %d: tenders %v with a gross of %s and a fee of "+
				"%s split into shares summing to %s, which is %s the bank "+
				"statement says was charged and no sale is shown to have paid",
				iteration, amounts, gross, fee, total, fee.Sub(total))
		}
	}
}
