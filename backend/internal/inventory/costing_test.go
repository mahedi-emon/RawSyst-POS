package inventory

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func layer(qty, cost string) Layer {
	return Layer{ID: uuid.New(), QtyRemaining: dec(qty), UnitCost: dec(cost)}
}

// A weighted-average pool is written as quantity and total value, never as an
// average, because the total is what the code treats as authoritative.
func pool(qty, value string) Pool {
	return Pool{QtyOnHand: dec(qty), TotalValue: dec(value)}
}

// FIFO takes the oldest stock first, which is the whole point: goods bought
// cheaply last year must not be valued at this year's price.
func TestFIFOConsumesOldestFirst(t *testing.T) {
	layers := []Layer{
		layer("10", "50.00"), // oldest
		layer("10", "60.00"),
	}

	got, err := ConsumeFIFO(layers, dec("4"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	if !got.TotalCost.Equal(dec("200")) {
		t.Fatalf("cost = %s, want 200 (4 x 50 from the oldest layer)", got.TotalCost)
	}
	if len(got.Consumed) != 1 {
		t.Fatalf("drew on %d layers, want 1", len(got.Consumed))
	}
}

// A sale larger than one layer spans several, each at its own cost.
func TestFIFOSpansLayers(t *testing.T) {
	layers := []Layer{
		layer("10", "50.00"),
		layer("10", "60.00"),
	}

	got, err := ConsumeFIFO(layers, dec("15"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	// 10 x 50 + 5 x 60 = 800
	if !got.TotalCost.Equal(dec("800")) {
		t.Fatalf("cost = %s, want 800", got.TotalCost)
	}
	if len(got.Consumed) != 2 {
		t.Fatalf("drew on %d layers, want 2", len(got.Consumed))
	}
	if !got.Consumed[0].Qty.Equal(dec("10")) || !got.Consumed[1].Qty.Equal(dec("5")) {
		t.Fatalf("took %s then %s, want 10 then 5",
			got.Consumed[0].Qty, got.Consumed[1].Qty)
	}
}

// The consumptions must sum to the total charged, or the valuation and the
// ledger drift apart and C13's tie-out fails by an unexplainable amount.
func TestFIFOConsumptionsSumToTheTotal(t *testing.T) {
	layers := []Layer{
		layer("3", "33.3333"),
		layer("3", "16.6667"),
	}

	got, err := ConsumeFIFO(layers, dec("5"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}

	sum := decimal.Zero
	for _, c := range got.Consumed {
		sum = sum.Add(c.Cost)
	}
	if !sum.Equal(got.TotalCost) {
		t.Fatalf("layer costs sum to %s but the total charged is %s", sum, got.TotalCost)
	}
}

// Weighted average charges the pooled cost, not any one receipt's.
func TestWACChargesTheAverage(t *testing.T) {
	// Two receipts of 10, at 50 and at 60: 20 units holding 1,100, average 55.
	p := pool("20", "1100")

	got, err := ConsumeWAC(p, dec("4"))
	if err != nil {
		t.Fatalf("ConsumeWAC: %v", err)
	}
	if !got.TotalCost.Equal(dec("220")) {
		t.Fatalf("cost = %s, want 220 (4 x 55)", got.TotalCost)
	}
}

// The guarantee that makes the tie-out hold: what the REPORTED valuation gives
// up equals what is charged to the ledger.
//
// It used to be stated as "what leaves the pool", and that was right until the
// ledger's precision was taken into account. The pool moves at cost precision,
// because design 02 §335 makes the total authoritative and the average derived
// from it — nudging the total to make a journal amount convenient is what that
// warning is about. The valuation reports that pool at MONEY precision, and the
// ledger holds money, so the figure charged is the difference between the
// rounded value before and after (P34).
//
// The distinction is not academic. Charging the rounded cost instead left a
// residue on every sale whose value did not land on a hallala, and the residues
// accumulated: a pool built from 33.3333, 16.6667 and 99.9999 parted company
// with its ledger on the second sale.
func TestWACPoolGivesUpExactlyWhatIsCharged(t *testing.T) {
	for _, tc := range []struct{ name, qty, value, take string }{
		{"even", "20", "1100", "15"},
		{"awkward average", "7", "466.6669", "5"},
		{"average recurs", "3", "100", "1"},
		{"single unit", "1", "33.3333", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := pool(tc.qty, tc.value)

			got, err := ConsumeWAC(p, dec(tc.take))
			if err != nil {
				t.Fatalf("ConsumeWAC: %v", err)
			}

			// What the valuation reports before and after, which is what
			// inventory_valuation sums and what the tie-out compares.
			released := p.TotalValue.Round(2).Sub(got.PoolValueAfter.Round(2))
			if !released.Equal(got.TotalCost) {
				t.Fatalf("the reported valuation gave up %s but %s was charged "+
					"to the ledger; the balance sheet would part company with "+
					"the stock report by %s",
					released, got.TotalCost, released.Sub(got.TotalCost))
			}

			// And the pool itself keeps its precision, so the next sale's
			// average is computed from an untouched total.
			if got.PoolValueAfter.Exponent() > -4 && !got.PoolValueAfter.IsZero() {
				t.Errorf("the pool was rounded to %s; the average derived from "+
					"it would drift", got.PoolValueAfter)
			}
			if !got.PoolQtyAfter.Equal(p.QtyOnHand.Sub(dec(tc.take))) {
				t.Fatalf("quantity left = %s, want %s",
					got.PoolQtyAfter, p.QtyOnHand.Sub(dec(tc.take)))
			}
		})
	}
}

// Emptying the pool must leave nothing behind. A residue here is stock that
// does not exist still carrying value on the balance sheet — and because it can
// never be sold, nothing later removes it.
func TestWACEmptiesToExactlyZero(t *testing.T) {
	// 3 units holding 100: an average of 33.3333 that does not divide evenly.
	p := pool("3", "100")

	got, err := ConsumeWAC(p, dec("3"))
	if err != nil {
		t.Fatalf("ConsumeWAC: %v", err)
	}
	if !got.TotalCost.Equal(dec("100")) {
		t.Fatalf("taking all the stock cost %s but the pool held 100", got.TotalCost)
	}
	if !got.PoolValueAfter.IsZero() || !got.PoolQtyAfter.IsZero() {
		t.Fatalf("the emptied pool still holds %s across %s units",
			got.PoolValueAfter, got.PoolQtyAfter)
	}
	if got.ShortBy.IsPositive() {
		t.Fatalf("taking exactly the stock on hand reported a shortfall of %s",
			got.ShortBy)
	}
}

// A receipt blends into the average from the totals, so successive receipts do
// not compound each other's rounding.
func TestReceivingBlendsTheAverage(t *testing.T) {
	p, err := ReceiveIntoPool(Pool{}, dec("10"), dec("50.00"))
	if err != nil {
		t.Fatalf("ReceiveIntoPool: %v", err)
	}
	if p, err = ReceiveIntoPool(p, dec("10"), dec("60.00")); err != nil {
		t.Fatalf("ReceiveIntoPool: %v", err)
	}

	if !p.QtyOnHand.Equal(dec("20")) || !p.TotalValue.Equal(dec("1100")) {
		t.Fatalf("pool = %s units at %s total, want 20 at 1100",
			p.QtyOnHand, p.TotalValue)
	}
	if !p.Average().Equal(dec("55")) {
		t.Fatalf("average = %s, want 55", p.Average())
	}
}

// An average that recurs must not drift as receipts accumulate. Blending from
// the stored average instead of the totals is how that drift starts.
func TestRecurringAverageDoesNotDrift(t *testing.T) {
	p := Pool{}
	for i := 0; i < 100; i++ {
		var err error
		if p, err = ReceiveIntoPool(p, dec("3"), dec("33.3333")); err != nil {
			t.Fatalf("ReceiveIntoPool: %v", err)
		}
	}

	// 300 units at 33.3333 is 9,999.99 exactly — no drift permitted.
	if !p.QtyOnHand.Equal(dec("300")) {
		t.Fatalf("quantity = %s, want 300", p.QtyOnHand)
	}
	if !p.TotalValue.Equal(dec("9999.99")) {
		t.Fatalf("value = %s, want 9999.99; the average has drifted over %d "+
			"receipts", p.TotalValue, 100)
	}
}

func TestReceivingRefusesNonsense(t *testing.T) {
	if _, err := ReceiveIntoPool(Pool{}, dec("0"), dec("10")); err == nil {
		t.Error("a receipt of nothing was accepted")
	}
	if _, err := ReceiveIntoPool(Pool{}, dec("-1"), dec("10")); err == nil {
		t.Error("a receipt of a negative quantity was accepted")
	}
	if _, err := ReceiveIntoPool(Pool{}, dec("1"), dec("-10")); err == nil {
		t.Error("a receipt at a negative cost was accepted")
	}
}

// Standard costing books the fixed cost and exposes the difference, so an
// unexpected purchase price shows up rather than being absorbed into margin.
func TestStandardCostReportsVariance(t *testing.T) {
	layers := []Layer{layer("10", "55.00")} // actually cost 55

	got, err := ConsumeStandard(layers, dec("2"), dec("50.00"))
	if err != nil {
		t.Fatalf("ConsumeStandard: %v", err)
	}
	if !got.TotalCost.Equal(dec("100")) {
		t.Fatalf("cost booked = %s, want 100 (2 x standard 50)", got.TotalCost)
	}
	// Actual 110, booked 100: 10 unfavourable.
	if !got.Variance.Equal(dec("10")) {
		t.Fatalf("variance = %s, want 10", got.Variance)
	}
}

// Selling more than is on hand must value the shortfall at something defensible
// and say how much was uncovered. Costing it at zero would understate COGS and
// flatter profit, which is the dangerous direction to be wrong in.
func TestSellingBeyondStockIsCostedAndReported(t *testing.T) {
	layers := []Layer{layer("2", "50.00")}

	got, err := ConsumeFIFO(layers, dec("5"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	if !got.ShortBy.Equal(dec("3")) {
		t.Fatalf("short by %s, want 3", got.ShortBy)
	}
	if got.TotalCost.LessThanOrEqual(dec("100")) {
		t.Fatalf("cost = %s; the three uncovered units were costed at nothing, "+
			"which overstates profit", got.TotalCost)
	}
	// 2 x 50 covered + 3 x 50 at the last known cost.
	if !got.TotalCost.Equal(dec("250")) {
		t.Fatalf("cost = %s, want 250", got.TotalCost)
	}
	// And it says WHICH estimate it used. C13 calls that figure provisional and
	// requires the next receipt to correct it, so a caller that could not see
	// it would have nothing to measure the correction against.
	if !got.ShortUnitCost.Equal(dec("50.00")) {
		t.Fatalf("the uncovered units were charged at %s, want the last known "+
			"cost of 50", got.ShortUnitCost)
	}
}

// The estimate FIFO falls back on is the LAST layer's cost, not the first.
//
// With two layers at different prices the older one is exhausted first, so by
// the time the sale runs out of stock the most recent cost is the closest thing
// to what the next delivery will charge.
func TestTheFIFOFallbackIsTheMostRecentCost(t *testing.T) {
	layers := []Layer{layer("2", "50.00"), layer("2", "80.00")}

	got, err := ConsumeFIFO(layers, dec("6"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	if !got.ShortBy.Equal(dec("2")) {
		t.Fatalf("short by %s, want 2", got.ShortBy)
	}
	if !got.ShortUnitCost.Equal(dec("80.00")) {
		t.Fatalf("the uncovered units were charged at %s, want 80",
			got.ShortUnitCost)
	}
	// 2 x 50 + 2 x 80 covered, then 2 x 80 uncovered.
	if !got.TotalCost.Equal(dec("420")) {
		t.Fatalf("cost = %s, want 420", got.TotalCost)
	}
}

// Standard costing charges every unit at standard, uncovered ones included, so
// that is what the correction must be measured against — not the FIFO fallback
// the actual-cost pass happened to use underneath.
func TestAStandardCostedShortfallIsProvisionalAtStandard(t *testing.T) {
	layers := []Layer{layer("2", "50.00")}

	got, err := ConsumeStandard(layers, dec("5"), dec("45.00"))
	if err != nil {
		t.Fatalf("ConsumeStandard: %v", err)
	}
	if !got.ShortBy.Equal(dec("3")) {
		t.Fatalf("short by %s, want 3", got.ShortBy)
	}
	if !got.ShortUnitCost.Equal(dec("45.00")) {
		t.Fatalf("the uncovered units were charged at %s, want the standard "+
			"cost of 45", got.ShortUnitCost)
	}
}

// A sale that stock covers reports no provisional cost at all. A non-zero
// figure here would have the settlement correcting units nobody guessed at.
func TestACoveredSaleReportsNoProvisionalCost(t *testing.T) {
	got, err := ConsumeFIFO([]Layer{layer("10", "50.00")}, dec("4"))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	if !got.ShortUnitCost.IsZero() {
		t.Fatalf("a fully covered sale reported a provisional cost of %s",
			got.ShortUnitCost)
	}
}

func TestSellingFromEmptyStock(t *testing.T) {
	got, err := ConsumeWAC(Pool{}, dec("3"))
	if err != nil {
		t.Fatalf("ConsumeWAC: %v", err)
	}
	if !got.ShortBy.Equal(dec("3")) {
		t.Fatalf("short by %s, want 3", got.ShortBy)
	}
	// The average of nothing is undefined, so nothing is charged and the caller
	// is told the whole quantity was uncovered.
	if !got.TotalCost.IsZero() {
		t.Fatalf("cost = %s from an empty pool, want 0", got.TotalCost)
	}
	// Provisionally zero, which is the largest correction there is: the whole
	// cost of those goods is recognised only when they arrive.
	if !got.ShortUnitCost.IsZero() {
		t.Fatalf("charged %s per unit against an empty pool, want 0",
			got.ShortUnitCost)
	}
}

// Selling past the pool costs the covered part at what was actually held and
// the rest at the pool's own average, and reports the shortfall.
func TestWACBeyondStockIsCostedAndReported(t *testing.T) {
	p := pool("2", "100") // average 50

	got, err := ConsumeWAC(p, dec("5"))
	if err != nil {
		t.Fatalf("ConsumeWAC: %v", err)
	}
	if !got.ShortBy.Equal(dec("3")) {
		t.Fatalf("short by %s, want 3", got.ShortBy)
	}
	if !got.TotalCost.Equal(dec("250")) {
		t.Fatalf("cost = %s, want 250 (100 held + 3 x 50)", got.TotalCost)
	}
	if !got.PoolValueAfter.IsZero() {
		t.Fatalf("the exhausted pool still holds %s", got.PoolValueAfter)
	}
	if !got.ShortUnitCost.Equal(dec("50")) {
		t.Fatalf("the uncovered units were charged at %s, want the pool average "+
			"of 50", got.ShortUnitCost)
	}
}

// The policy decides what a shortfall means. Blocking is right for high-value
// goods; allowing is right for a busy shop where refusing a waiting customer is
// worse than a correction later.
func TestNegativeStockPolicy(t *testing.T) {
	short := CostResult{ShortBy: dec("3")}
	fine := CostResult{ShortBy: decimal.Zero}

	if err := CheckAvailability(PolicyBlock, fine, "Abaya"); err != nil {
		t.Fatalf("a sale with enough stock was blocked: %v", err)
	}

	err := CheckAvailability(PolicyBlock, short, "Abaya")
	if err == nil {
		t.Fatal("block policy allowed a sale beyond available stock")
	}
	// The cashier has a customer waiting and needs to know what to do.
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "Abaya") {
		t.Errorf("the refusal does not say how short or of what: %v", err)
	}

	if err := CheckAvailability(PolicyAllowWarn, short, "Abaya"); err != nil {
		t.Fatalf("allow_warn blocked a sale: %v", err)
	}
}

// Valuation and quantity read consistently from the same layers, which is what
// the tie-out to the Inventory control account compares.
func TestValuationMatchesItsLayers(t *testing.T) {
	layers := []Layer{
		layer("10", "50.00"),
		layer("5", "60.00"),
		{ID: uuid.New(), QtyRemaining: decimal.Zero, UnitCost: dec("70.00")}, // spent
	}

	if got := OnHand(layers); !got.Equal(dec("15")) {
		t.Errorf("on hand = %s, want 15 (a spent layer holds nothing)", got)
	}
	if got := Valuation(layers); !got.Equal(dec("800")) {
		t.Errorf("valuation = %s, want 800", got)
	}
}

// Consuming everything must leave the valuation at zero, or stock that no
// longer exists keeps a value on the balance sheet.
func TestConsumingEverythingLeavesNothing(t *testing.T) {
	layers := []Layer{
		layer("4", "25.00"),
		layer("6", "35.00"),
	}
	total := Valuation(layers)

	got, err := ConsumeFIFO(layers, OnHand(layers))
	if err != nil {
		t.Fatalf("ConsumeFIFO: %v", err)
	}
	if !got.TotalCost.Equal(total) {
		t.Fatalf("consuming all stock cost %s but it was valued at %s; the "+
			"difference would sit on the balance sheet forever",
			got.TotalCost, total)
	}
	if got.ShortBy.IsPositive() {
		t.Fatalf("consuming exactly the stock on hand reported a shortfall of %s",
			got.ShortBy)
	}
}

func TestUnknownMethodIsRefused(t *testing.T) {
	_, err := Compute(Request{
		Method: "lifo",
		Qty:    dec("1"),
		Layers: []Layer{layer("1", "1")},
	})
	if err == nil {
		t.Fatal("an unrecognised costing method was accepted")
	}
	if !strings.Contains(err.Error(), "lifo") {
		t.Errorf("the refusal does not name the method: %v", err)
	}
}

func TestNonPositiveQuantityIsRefused(t *testing.T) {
	layers := []Layer{layer("10", "50.00")}
	for _, qty := range []string{"0", "-1"} {
		if _, err := ConsumeFIFO(layers, dec(qty)); err == nil {
			t.Errorf("FIFO accepted a quantity of %s", qty)
		}
		if _, err := ConsumeWAC(pool("10", "500"), dec(qty)); err == nil {
			t.Errorf("WAC accepted a quantity of %s", qty)
		}
	}
}

// The three methods can disagree about cost — that is their purpose — but each
// must be internally consistent.
func TestMethodsDisagreeButEachReconciles(t *testing.T) {
	layers := []Layer{
		layer("10", "40.00"),
		layer("10", "60.00"),
	}

	// The same stock, seen the two ways: 20 units holding 1,000.
	p := pool("20", "1000")

	fifo, _ := ConsumeFIFO(layers, dec("15"))
	wac, _ := ConsumeWAC(p, dec("15"))

	// FIFO: 10x40 + 5x60 = 700. WAC: 15 x 50 = 750.
	if !fifo.TotalCost.Equal(dec("700")) {
		t.Errorf("FIFO = %s, want 700", fifo.TotalCost)
	}
	if !wac.TotalCost.Equal(dec("750")) {
		t.Errorf("WAC = %s, want 750", wac.TotalCost)
	}
	if fifo.TotalCost.Equal(wac.TotalCost) {
		t.Error("FIFO and WAC produced the same cost on rising prices; one of " +
			"them is not doing what it claims")
	}

	// Each must still reconcile against its own store — that is what keeps the
	// valuation tied to the ledger whichever method a company chooses.
	sum := decimal.Zero
	for _, c := range fifo.Consumed {
		sum = sum.Add(c.Cost)
	}
	if !sum.Equal(fifo.TotalCost) {
		t.Errorf("fifo: layer costs sum to %s but %s was charged", sum, fifo.TotalCost)
	}
	if released := p.TotalValue.Sub(wac.PoolValueAfter); !released.Equal(wac.TotalCost) {
		t.Errorf("wac: the pool gave up %s but %s was charged", released, wac.TotalCost)
	}
}

// A layer is valued as quantity times unit cost, so restoring stock at
// value/qty is only exact when that division comes out even. When it does not,
// the parts must still sum to the whole — the same rule as everywhere else.
func TestRestoredLayersSumToTheValueExactly(t *testing.T) {
	for _, tc := range []struct{ name, qty, value string }{
		{"divides evenly", "3", "180.00"},
		{"recurring", "3", "199.99"},
		{"single unit", "1", "66.6667"},
		{"fractional", "2.5", "100.01"},
		{"large awkward", "7", "1000.00"},
		{"zero value", "4", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qty, value := dec(tc.qty), dec(tc.value)

			parts := splitIntoLayers(qty, value)

			sumQty, sumValue := decimal.Zero, decimal.Zero
			for _, p := range parts {
				sumQty = sumQty.Add(p.qty)
				sumValue = sumValue.Add(p.qty.Mul(p.unitCost))
			}

			if !sumQty.Equal(qty) {
				t.Errorf("the layers hold %s units, want %s", sumQty, qty)
			}
			if !sumValue.Equal(value) {
				t.Errorf("the layers are worth %s but %s came back; the valuation "+
					"and the ledger would part company by %s",
					sumValue, value, sumValue.Sub(value))
			}
			for _, p := range parts {
				if p.unitCost.IsNegative() {
					t.Errorf("a restored layer has a negative unit cost of %s", p.unitCost)
				}
			}
		})
	}
}
