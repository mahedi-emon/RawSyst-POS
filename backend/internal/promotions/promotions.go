// Package promotions is the pricing engine (blueprint B9).
//
// # Eleven promotions, three mechanisms
//
// B9 lists percentage, fixed amount, buy-X-get-Y, bundle pricing, category,
// brand, product, customer type, coupons, seasonal campaigns, flash sales and
// employee discounts. They are not twelve features. They are four things a
// promotion can DO, crossed with what it applies to and when — and modelling
// them separately would produce twelve code paths through the till, of which
// the twelfth would be the one nobody tested.
//
// # The floor price is not negotiable
//
// B1 calls the floor "the lowest price a cashier may ever sell at, even after
// discount — enforced by the system, not just policy". A promotion is a
// discount, so it is bound by the same rule, and this engine will reduce a line
// to the floor and no further.
//
// That is deliberately a silent clamp rather than a refusal. A shop that has
// set a twenty per cent campaign and a floor that only allows fifteen on one
// product should sell that product at fifteen, not refuse to sell it — and the
// clamp is reported on the result so the screen can say what happened.
//
// # One promotion per line
//
// Two promotions on one line raises a question nobody has answered: is the
// second percentage taken off the original price or the discounted one? Every
// answer is defensible and they give different numbers, and a shop cannot check
// a receipt it cannot predict.
//
// So the best single promotion wins — highest `priority`, then the largest
// discount. Stacking is not a feature this engine has, and a promotion that
// looks like it should stack is two promotions somebody meant as one.
package promotions

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// moneyScale is the scale a discount is rounded to.
const moneyScale = 4

// The four things a promotion can do.
const (
	KindPercentage  = "percentage"
	KindAmount      = "amount"
	KindBuyXGetY    = "buy_x_get_y"
	KindBundlePrice = "bundle_price"
)

// Service manages promotions and prices lines against them.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Promotion is one campaign.
type Promotion struct {
	ID     uuid.UUID `json:"id"`
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	NameAr string    `json:"name_ar,omitempty"`
	Kind   string    `json:"kind"`

	Value  string `json:"value,omitempty"`
	BuyQty string `json:"buy_qty,omitempty"`
	GetQty string `json:"get_qty,omitempty"`

	CategoryID   *uuid.UUID `json:"category_id,omitempty"`
	BrandID      *uuid.UUID `json:"brand_id,omitempty"`
	VariantID    *uuid.UUID `json:"variant_id,omitempty"`
	CustomerType string     `json:"customer_type,omitempty"`
	// Applies is what the scope amounts to, in words, so a list can be read
	// without four joins and a reader's own inference.
	Applies string `json:"applies_to"`

	StartsOn    string     `json:"starts_on,omitempty"`
	EndsOn      string     `json:"ends_on,omitempty"`
	StoreID     *uuid.UUID `json:"store_id,omitempty"`
	MinPurchase string     `json:"min_purchase,omitempty"`

	CouponCode         string `json:"coupon_code,omitempty"`
	MaxUses            *int   `json:"max_uses,omitempty"`
	MaxUsesPerCustomer *int   `json:"max_uses_per_customer,omitempty"`

	Active   bool `json:"is_active"`
	Priority int  `json:"priority"`

	// Used and Given are what it has cost so far — D2's campaign figures, on
	// the row rather than behind a separate report, because "is this campaign
	// working" is asked while looking at the list.
	Used     int    `json:"times_used"`
	Given    string `json:"discount_given"`
	Sold     string `json:"sales_generated"`
	Currency string `json:"currency"`
}

// Line is one cart line being priced.
type Line struct {
	VariantID uuid.UUID
	Qty       decimal.Decimal

	// UnitPrice is what the line would cost without any promotion. The caller
	// supplies it because the tier a customer is on is the caller's business;
	// what this engine decides is what comes OFF it.
	UnitPrice decimal.Decimal

	// FloorPrice is B1's lowest permissible unit price, or zero for a product
	// that has none. A promotion never takes a line below it.
	FloorPrice decimal.Decimal

	CategoryID *uuid.UUID
	BrandID    *uuid.UUID
}

// Priced is what a promotion did to one line.
type Priced struct {
	VariantID uuid.UUID `json:"variant_id"`

	PromotionID   *uuid.UUID `json:"promotion_id,omitempty"`
	PromotionName string     `json:"promotion,omitempty"`

	// Discount is what came off the whole line, not off one unit.
	Discount string `json:"discount"`
	// LineTotal is what the line comes to after it.
	LineTotal string `json:"line_total"`

	// FloorApplied says the promotion was worth more than the floor allowed and
	// was clamped. Reported rather than swallowed: a shop running a campaign
	// that cannot apply to half its range should be told which half.
	FloorApplied bool `json:"floor_applied,omitempty"`
}

// Basket is what the caller asks about.
type Basket struct {
	StoreID      *uuid.UUID
	CustomerID   *uuid.UUID
	CustomerType string
	CouponCode   string
	On           time.Time
	Lines        []Line
}

// Quote prices a basket against every live promotion.
//
// It does not record anything. The till asks this while a cart is being built,
// possibly many times, and a promotion is only redeemed when a sale is
// finalised — see `Redeem`.
func (s *Service) Quote(
	ctx context.Context, scope Scope, basket Basket,
) ([]Priced, error) {
	out := make([]Priced, 0, len(basket.Lines))

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		live, err := s.livePromotions(ctx, tx, scope, basket)
		if err != nil {
			return err
		}

		subtotal := decimal.Zero
		for _, l := range basket.Lines {
			subtotal = subtotal.Add(l.UnitPrice.Mul(l.Qty))
		}

		for _, l := range basket.Lines {
			out = append(out, best(l, live, subtotal))
		}
		return nil
	})
	return out, err
}

// candidate is a promotion as the engine reads it.
type candidate struct {
	id           uuid.UUID
	name         string
	kind         string
	value        decimal.Decimal
	buyQty       decimal.Decimal
	getQty       decimal.Decimal
	categoryID   *uuid.UUID
	brandID      *uuid.UUID
	variantID    *uuid.UUID
	customerType string
	minPurchase  decimal.Decimal
	priority     int
}

// best chooses one promotion for one line and works out what it takes off.
//
// The best is the highest priority, and among equal priorities the largest
// discount. Not the first that matches: a shop that sets up a ten per cent
// campaign and then a twenty per cent flash sale expects the customer to get
// twenty, and "whichever we looked at first" is not something a shopkeeper can
// predict or explain at the counter.
func best(l Line, live []candidate, subtotal decimal.Decimal) Priced {
	out := Priced{
		VariantID: l.VariantID,
		Discount:  "0.0000",
		LineTotal: l.UnitPrice.Mul(l.Qty).Round(moneyScale).StringFixed(4),
	}

	gross := l.UnitPrice.Mul(l.Qty)
	// The most that may come off before the floor is breached. A product with
	// no floor has none, and the whole line may go.
	maxDiscount := gross
	if l.FloorPrice.IsPositive() {
		maxDiscount = gross.Sub(l.FloorPrice.Mul(l.Qty))
		if maxDiscount.IsNegative() {
			// Already at or below the floor. Nothing may come off, and this is
			// not an error: it is a product priced at its floor.
			maxDiscount = decimal.Zero
		}
	}

	var chosen *candidate
	chosenDiscount := decimal.Zero
	chosenClamped := false

	for i := range live {
		p := &live[i]
		if !applies(p, l, subtotal) {
			continue
		}

		raw := discountFor(p, l, gross)
		if !raw.IsPositive() {
			continue
		}

		clamped := false
		if raw.GreaterThan(maxDiscount) {
			raw = maxDiscount
			clamped = true
		}
		if !raw.IsPositive() {
			continue
		}

		better := chosen == nil ||
			p.priority > chosen.priority ||
			(p.priority == chosen.priority && raw.GreaterThan(chosenDiscount))
		if better {
			chosen = p
			chosenDiscount = raw
			chosenClamped = clamped
		}
	}

	if chosen == nil {
		return out
	}

	id := chosen.id
	out.PromotionID = &id
	out.PromotionName = chosen.name
	out.Discount = chosenDiscount.Round(moneyScale).StringFixed(4)
	out.LineTotal = gross.Sub(chosenDiscount).Round(moneyScale).StringFixed(4)
	out.FloorApplied = chosenClamped
	return out
}

// applies says whether a promotion reaches this line at all.
func applies(p *candidate, l Line, subtotal decimal.Decimal) bool {
	if p.variantID != nil && *p.variantID != l.VariantID {
		return false
	}
	if p.categoryID != nil &&
		(l.CategoryID == nil || *p.categoryID != *l.CategoryID) {
		return false
	}
	if p.brandID != nil && (l.BrandID == nil || *p.brandID != *l.BrandID) {
		return false
	}
	// The minimum purchase is on the BASKET, not the line. A "spend 500, get
	// ten per cent off" campaign that tested the line would give nothing to
	// somebody buying five things at a hundred each, which is exactly the
	// customer it was written for.
	if p.minPurchase.IsPositive() && subtotal.LessThan(p.minPurchase) {
		return false
	}
	return true
}

// discountFor is what one promotion takes off one line, before the floor.
func discountFor(p *candidate, l Line, gross decimal.Decimal) decimal.Decimal {
	switch p.kind {
	case KindPercentage:
		return gross.Mul(p.value).Div(decimal.NewFromInt(100))

	case KindAmount:
		// A fixed amount comes off the LINE once, not off each unit. "Five
		// riyals off shampoo" means five riyals, and a customer buying six
		// bottles does not get thirty off — that would be a promotion nobody
		// costed.
		if p.value.GreaterThan(gross) {
			return gross
		}
		return p.value

	case KindBuyXGetY:
		// Buy three get one free: for every four in the basket, one is free.
		// The free ones are the cheapest, which here is trivially the same unit
		// price — the distinction matters when this grows to mixed baskets and
		// is worth writing down now.
		group := p.buyQty.Add(p.getQty)
		if !group.IsPositive() {
			return decimal.Zero
		}
		sets := l.Qty.Div(group).Floor()
		free := sets.Mul(p.getQty)
		if free.GreaterThan(l.Qty) {
			free = l.Qty
		}
		return free.Mul(l.UnitPrice)

	case KindBundlePrice:
		// Three shirts for a flat hundred. Applies once per complete bundle;
		// the remainder is sold at the ordinary price, which is why the
		// discount is computed against the bundles only.
		if !p.buyQty.IsPositive() {
			return decimal.Zero
		}
		sets := l.Qty.Div(p.buyQty).Floor()
		if !sets.IsPositive() {
			return decimal.Zero
		}
		bundled := sets.Mul(p.buyQty)
		ordinary := bundled.Mul(l.UnitPrice)
		charged := sets.Mul(p.value)
		if charged.GreaterThanOrEqual(ordinary) {
			// The bundle costs more than the items do. Not a discount, and
			// applying it would put the price UP.
			return decimal.Zero
		}
		return ordinary.Sub(charged)
	}
	return decimal.Zero
}

// livePromotions reads everything that could apply to this basket right now.
func (s *Service) livePromotions(
	ctx context.Context, tx pgx.Tx, scope Scope, basket Basket,
) ([]candidate, error) {
	on := basket.On
	if on.IsZero() {
		on = time.Now().UTC()
	}
	coupon := strings.ToUpper(strings.TrimSpace(basket.CouponCode))

	rows, err := tx.Query(ctx, `
		SELECT p.id, p.name, p.kind,
		       coalesce(p.value, 0), coalesce(p.buy_qty, 0), coalesce(p.get_qty, 0),
		       p.category_id, p.brand_id, p.variant_id,
		       coalesce(p.customer_type, ''), coalesce(p.min_purchase, 0),
		       p.priority
		FROM promotion p
		WHERE p.company_id = $1
		  AND p.is_active
		  AND (p.starts_on IS NULL OR p.starts_on <= $2::date)
		  AND (p.ends_on   IS NULL OR p.ends_on   >= $2::date)
		  AND (p.store_id  IS NULL OR p.store_id  = $3)
		  AND (p.customer_type IS NULL OR p.customer_type = $4)
		  -- A coupon promotion applies only when its code was typed. An
		  -- automatic one applies whatever was typed.
		  AND (p.coupon_code IS NULL OR upper(p.coupon_code) = $5)
		  -- And the caps, counted from what has actually been redeemed.
		  AND (p.max_uses IS NULL OR p.max_uses > (
		        SELECT count(*) FROM promotion_redemption r
		        WHERE r.promotion_id = p.id))
		  AND (p.max_uses_per_customer IS NULL OR $6::uuid IS NULL
		       OR p.max_uses_per_customer > (
		        SELECT count(*) FROM promotion_redemption r
		        WHERE r.promotion_id = p.id AND r.customer_id = $6))
		ORDER BY p.priority DESC, p.id`,
		scope.CompanyID, on, basket.StoreID, nullIfBlank(basket.CustomerType),
		coupon, basket.CustomerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.name, &c.kind, &c.value, &c.buyQty,
			&c.getQty, &c.categoryID, &c.brandID, &c.variantID,
			&c.customerType, &c.minPurchase, &c.priority); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
