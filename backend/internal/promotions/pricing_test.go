// The pricing arithmetic, tested without a database.
//
// `best` and `discountFor` take a line and a set of promotions and return a
// number. Nothing about that needs Postgres, and a test that needed one would
// be slow enough that nobody would write the twentieth case — which on a
// pricing engine is exactly the case that is wrong.
//
// The floor tests are the ones that matter most. B1 calls the floor price "the
// lowest price a cashier may ever sell at, even after discount — enforced by
// the system, not just policy", and a promotion is a discount.
package promotions

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func line(qty, unit, floor string) Line {
	return Line{
		VariantID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Qty:        d(qty),
		UnitPrice:  d(unit),
		FloorPrice: d(floor),
	}
}

func promo(kind string, value, buy, get string, priority int) candidate {
	return candidate{
		id:       uuid.New(),
		name:     kind,
		kind:     kind,
		value:    d(value),
		buyQty:   d(buy),
		getQty:   d(get),
		priority: priority,
	}
}

// --- the four mechanisms --------------------------------------------------

func TestAPercentageComesOffTheWholeLine(t *testing.T) {
	got := best(line("4", "100", "0"),
		[]candidate{promo(KindPercentage, "10", "0", "0", 0)}, d("400"))

	if got.Discount != "40.0000" {
		t.Errorf("ten per cent of four at a hundred is 40, not %s", got.Discount)
	}
	if got.LineTotal != "360.0000" {
		t.Errorf("the line should come to 360, not %s", got.LineTotal)
	}
}

// A fixed amount comes off the LINE once, not off each unit.
//
// "Five riyals off shampoo" means five riyals. A customer buying six bottles
// getting thirty off is a promotion nobody costed.
func TestAFixedAmountComesOffOnce(t *testing.T) {
	got := best(line("6", "20", "0"),
		[]candidate{promo(KindAmount, "5", "0", "0", 0)}, d("120"))

	if got.Discount != "5.0000" {
		t.Errorf("five off a line is five however many units are on it, not %s",
			got.Discount)
	}
}

func TestAFixedAmountNeverExceedsTheLine(t *testing.T) {
	got := best(line("1", "3", "0"),
		[]candidate{promo(KindAmount, "5", "0", "0", 0)}, d("3"))

	if got.Discount != "3.0000" {
		t.Errorf("five off a three-riyal line takes three, not %s", got.Discount)
	}
	if got.LineTotal != "0.0000" {
		t.Errorf("the line should come to nothing, not %s", got.LineTotal)
	}
}

// Buy three get one free: for every FOUR in the basket, one is free.
func TestBuyThreeGetOneFreeCountsCompleteSets(t *testing.T) {
	p := []candidate{promo(KindBuyXGetY, "0", "3", "1", 0)}

	for _, c := range []struct {
		qty, discount string
		why           string
	}{
		{"3", "0.0000", "three is not a complete set of four"},
		{"4", "10.0000", "four is one set: one free"},
		{"7", "10.0000", "seven is one set and three over"},
		{"8", "20.0000", "eight is two sets: two free"},
	} {
		got := best(line(c.qty, "10", "0"), p, d("100"))
		if got.Discount != c.discount {
			t.Errorf("%s of %s: discount %s, want %s (%s)",
				c.qty, "10", got.Discount, c.discount, c.why)
		}
	}
}

// Three shirts for a flat hundred. The remainder sells at the ordinary price.
func TestABundlePriceAppliesPerCompleteBundle(t *testing.T) {
	p := []candidate{promo(KindBundlePrice, "100", "3", "0", 0)}

	// Four shirts at 40: three for 100 and one at 40. The discount is the 20
	// the bundle saved.
	got := best(line("4", "40", "0"), p, d("160"))
	if got.Discount != "20.0000" {
		t.Errorf("three at 40 is 120, bundled at 100, a 20 saving — not %s",
			got.Discount)
	}
	if got.LineTotal != "140.0000" {
		t.Errorf("100 for the bundle and 40 for the odd one is 140, not %s",
			got.LineTotal)
	}
}

// A bundle that costs more than the items is not a discount, and applying it
// would put the price UP.
func TestABundleThatCostsMoreIsNotApplied(t *testing.T) {
	got := best(line("3", "20", "0"),
		[]candidate{promo(KindBundlePrice, "100", "3", "0", 0)}, d("60"))

	if got.Discount != "0.0000" {
		t.Errorf("a bundle dearer than the items must not apply: %s", got.Discount)
	}
	if got.PromotionID != nil {
		t.Error("and it must not be named as the promotion that applied")
	}
}

// --- the floor ------------------------------------------------------------

// B1: "the lowest price a cashier may ever sell at, even after discount".
func TestAPromotionNeverBreachesTheFloorPrice(t *testing.T) {
	// A hundred-riyal shirt with an eighty-riyal floor, and a fifty per cent
	// campaign. The customer gets twenty off, not fifty.
	got := best(line("1", "100", "80"),
		[]candidate{promo(KindPercentage, "50", "0", "0", 0)}, d("100"))

	if got.Discount != "20.0000" {
		t.Fatalf("the floor allows 20 off, and the promotion wanted 50. "+
			"Discount was %s — a promotion that breaches the floor sells at a "+
			"loss silently, which is the thing B1 says the system must enforce "+
			"rather than leaving to policy.", got.Discount)
	}
	if got.LineTotal != "80.0000" {
		t.Errorf("the line should land exactly on the floor: %s", got.LineTotal)
	}
	if !got.FloorApplied {
		t.Error("and the clamp should be reported, so a shop can be told which " +
			"of its products the campaign could not fully reach")
	}
}

// The floor is per unit, so the headroom grows with the quantity.
func TestTheFloorScalesWithTheQuantity(t *testing.T) {
	got := best(line("3", "100", "80"),
		[]candidate{promo(KindPercentage, "50", "0", "0", 0)}, d("300"))

	if got.Discount != "60.0000" {
		t.Errorf("three units with 20 of headroom each allows 60 off, not %s",
			got.Discount)
	}
}

// A product already priced at its floor takes no discount at all, and that is
// not an error.
func TestAProductAtItsFloorTakesNoDiscount(t *testing.T) {
	got := best(line("2", "80", "80"),
		[]candidate{promo(KindPercentage, "25", "0", "0", 0)}, d("160"))

	if got.Discount != "0.0000" {
		t.Errorf("a product at its floor has no room: %s", got.Discount)
	}
	if got.PromotionID != nil {
		t.Error("and no promotion should be named, because none applied")
	}
}

// A product with no floor may go to nothing.
func TestAProductWithNoFloorMayBeGivenAway(t *testing.T) {
	got := best(line("1", "50", "0"),
		[]candidate{promo(KindPercentage, "100", "0", "0", 0)}, d("50"))

	if got.LineTotal != "0.0000" {
		t.Errorf("a hundred per cent off with no floor is free: %s", got.LineTotal)
	}
}

// --- choosing between promotions ------------------------------------------

// The customer gets the best one, not the first one.
//
// A shop that sets up a ten per cent campaign and then a twenty per cent flash
// sale expects twenty, and "whichever we looked at first" is not something a
// shopkeeper can explain at the counter.
func TestTheBiggestDiscountWinsAtEqualPriority(t *testing.T) {
	got := best(line("1", "100", "0"), []candidate{
		promo(KindPercentage, "10", "0", "0", 0),
		promo(KindPercentage, "20", "0", "0", 0),
	}, d("100"))

	if got.Discount != "20.0000" {
		t.Errorf("the customer should get the better of the two: %s", got.Discount)
	}
}

// Priority beats size, because that is what priority is for: a shop that wants
// its clearance rule to win over a bigger loyalty discount says so.
func TestPriorityBeatsSize(t *testing.T) {
	small := promo(KindPercentage, "5", "0", "0", 10)
	large := promo(KindPercentage, "40", "0", "0", 0)

	got := best(line("1", "100", "0"), []candidate{large, small}, d("100"))
	if got.Discount != "5.0000" {
		t.Errorf("the higher-priority promotion should win: %s", got.Discount)
	}
}

// Never two at once. Two promotions on one line raises a question nobody has
// answered — is the second percentage off the original price or the discounted
// one — and a shop cannot check a receipt it cannot predict.
func TestPromotionsDoNotStack(t *testing.T) {
	got := best(line("1", "100", "0"), []candidate{
		promo(KindPercentage, "10", "0", "0", 0),
		promo(KindAmount, "15", "0", "0", 0),
	}, d("100"))

	// The better of the two on its own, not 10% then 15 off the remainder
	// (which would be 24.50) and not both off the original (25).
	if got.Discount != "15.0000" {
		t.Fatalf("discount was %s. Only one promotion may apply to a line: "+
			"stacking makes a receipt a customer cannot check.", got.Discount)
	}
}

// --- scope ----------------------------------------------------------------

func TestAPromotionScopedToAnotherProductDoesNotApply(t *testing.T) {
	other := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	p := promo(KindPercentage, "50", "0", "0", 0)
	p.variantID = &other

	got := best(line("1", "100", "0"), []candidate{p}, d("100"))
	if got.Discount != "0.0000" {
		t.Errorf("a promotion for a different product applied: %s", got.Discount)
	}
}

// The minimum purchase is a condition on the BASKET, not on the line.
//
// "Spend 500, get ten per cent off" tested against the line would give nothing
// to somebody buying five things at a hundred each — which is exactly the
// customer the campaign was written for.
func TestAMinimumPurchaseIsAgainstTheWholeBasket(t *testing.T) {
	p := promo(KindPercentage, "10", "0", "0", 0)
	p.minPurchase = d("500")

	// One line of 100, in a basket of 500.
	got := best(line("1", "100", "0"), []candidate{p}, d("500"))
	if got.Discount != "10.0000" {
		t.Errorf("the basket reached the minimum, so the line qualifies: %s",
			got.Discount)
	}

	// The same line in a basket of 200 does not.
	short := best(line("1", "100", "0"), []candidate{p}, d("200"))
	if short.Discount != "0.0000" {
		t.Errorf("the basket is under the minimum: %s", short.Discount)
	}
}

func TestALineWithNoPromotionIsUnchanged(t *testing.T) {
	got := best(line("3", "25", "0"), nil, d("75"))

	if got.Discount != "0.0000" || got.LineTotal != "75.0000" {
		t.Errorf("an untouched line should come to its own total: %s off, %s",
			got.Discount, got.LineTotal)
	}
	if got.PromotionID != nil {
		t.Error("and should name no promotion")
	}
}
