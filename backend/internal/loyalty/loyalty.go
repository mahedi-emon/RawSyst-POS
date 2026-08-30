// Package loyalty is the points scheme and the tiers (blueprint B16).
//
// # Points are a liability, not a marketing figure
//
// A point a customer has earned is money the shop will hand over later. So
// earning posts — Loyalty Points Cost against Loyalty Points Liability — on the
// sale that earned it, spending it settles the liability through the tender it
// was used as, and expiry writes it back. A scheme whose balance lived only in
// a `points` column would be a real obligation the accounts never mentioned.
//
// # Points are whole numbers, rounded down
//
// B16's example is "100 SAR spent = 1 point". A 250 riyal sale earns two
// points, not two and a half. Fractional points are an argument with a customer
// that nobody wins, and rounding down means the shop never owes a third of a
// point and the customer is never told they have one.
//
// # A tier is derived, never stored
//
// Which tier somebody is in is a function of what they have spent, and storing
// it would mean a customer who crossed a threshold on Tuesday stayed Silver
// until something remembered to look. Computed on read, from the invoices.
package loyalty

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Why a points entry exists.
const (
	ReasonEarned   = "earned"
	ReasonRedeemed = "redeemed"
	ReasonExpired  = "expired"
	ReasonAdjusted = "adjusted"
	ReasonReversed = "reversed"
)

// Service runs the points scheme.
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

// Tier is one rung of the scheme.
type Tier struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	NameAr   string `json:"name_ar,omitempty"`
	MinSpend string `json:"min_spend"`
	// Discount is the percentage off a member of this tier gets. B16 calls
	// these "tier-based perks"; a percentage is the one perk the pricing engine
	// can apply without a person deciding.
	Discount string `json:"discount_percent,omitempty"`
}

// Program is a company's scheme.
type Program struct {
	Active        bool   `json:"is_active"`
	SpendPerPoint string `json:"spend_per_point"`
	PointValue    string `json:"point_value"`
	ExpiryMonths  *int   `json:"expiry_months,omitempty"`
	Tiers         []Tier `json:"tiers"`
	Currency      string `json:"currency"`

	// Exists says whether the company has set a scheme up at all. A company
	// with no scheme is not a company with a scheme set to zero: points cannot
	// be earned or spent, and the screen says so rather than showing a form
	// full of defaults that are not in force.
	Exists bool `json:"exists"`

	// Owed is what the shop currently owes in points, in money. The one figure
	// on this screen an owner actually asks about.
	Owed   string `json:"owed"`
	Points int    `json:"points_outstanding"`
}

// Entry is one movement of points.
type Entry struct {
	ID        uuid.UUID `json:"id"`
	Points    int       `json:"points"`
	Reason    string    `json:"reason"`
	InvoiceNo string    `json:"invoice_no,omitempty"`
	Spend     string    `json:"spend,omitempty"`
	ExpiresOn string    `json:"expires_on,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt string    `json:"created_at"`
}

// Card is what a shop knows about one customer's membership.
type Card struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Customer   string    `json:"customer"`

	Points int    `json:"points"`
	Worth  string `json:"worth"`

	// Tier, and what it would take to reach the next one. B16 asks for tiering;
	// a member who cannot see how far off the next rung is has been given a
	// badge rather than a reason to come back.
	Tier       string `json:"tier,omitempty"`
	NextTier   string `json:"next_tier,omitempty"`
	ToNextTier string `json:"to_next_tier,omitempty"`
	Discount   string `json:"discount_percent,omitempty"`

	LifetimeSpend string `json:"lifetime_spend"`
	Visits        int    `json:"visits"`
	LastPurchase  string `json:"last_purchase,omitempty"`
	Currency      string `json:"currency"`

	// Segment is B16's classification: new, returning, vip, high_value,
	// at_risk, wholesale, retail. Derived, because every one of those is a fact
	// about the invoices rather than something anybody types.
	Segment string `json:"segment"`

	// ExpiringSoon is points that will be lost within ninety days. The reason
	// somebody rings a customer.
	ExpiringSoon int     `json:"expiring_soon,omitempty"`
	Entries      []Entry `json:"entries,omitempty"`
}

// Balance is a customer's spendable points.
//
// Takes a tx because the till calls it inside the sale transaction: points
// checked on one connection and spent on another is a balance that was true
// once.
func Balance(
	ctx context.Context, tx pgx.Tx, customerID uuid.UUID,
) (int, error) {
	var points int
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(points), 0)::int
		FROM loyalty_entry WHERE customer_id = $1`, customerID).Scan(&points)
	return points, err
}

// Settings reads the scheme in force, without the money figures.
//
// Used by the sale path, which needs the accrual rate and nothing else.
func Settings(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (Program, error) {
	var p Program
	var spend, value decimal.Decimal
	var tiers []byte
	err := tx.QueryRow(ctx, `
		SELECT is_active, spend_per_point, point_value, expiry_months, tiers
		FROM loyalty_program WHERE company_id = $1`, companyID).
		Scan(&p.Active, &spend, &value, &p.ExpiryMonths, &tiers)
	if err == pgx.ErrNoRows {
		return Program{}, nil
	}
	if err != nil {
		return Program{}, err
	}
	p.Exists = true
	p.SpendPerPoint = spend.String()
	p.PointValue = value.String()
	if e := json.Unmarshal(tiers, &p.Tiers); e != nil {
		return Program{}, e
	}
	return p, nil
}

// Accrue awards the points a sale earned.
//
// Called from inside the sale transaction, so a sale that posts and points that
// do not is not a state this product can reach. Silent when there is no scheme,
// no customer, or the sale was too small to earn a whole point: none of those
// is a failure, and refusing a sale because the loyalty scheme had an opinion
// would be the worst possible trade.
func Accrue(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID uuid.UUID, customerID *uuid.UUID,
	invoiceID uuid.UUID, spend decimal.Decimal, country string,
	userID *uuid.UUID,
) (int, error) {
	if customerID == nil || !spend.IsPositive() {
		return 0, nil
	}

	program, err := Settings(ctx, tx, companyID)
	if err != nil {
		return 0, err
	}
	if !program.Exists || !program.Active {
		return 0, nil
	}

	rate, err := decimal.NewFromString(program.SpendPerPoint)
	if err != nil || !rate.IsPositive() {
		return 0, nil
	}
	// Rounded down. See the package note: the shop never owes a fraction of a
	// point and the customer is never told they have one.
	points := spend.Div(rate).Floor().IntPart()
	if points <= 0 {
		return 0, nil
	}

	value, err := decimal.NewFromString(program.PointValue)
	if err != nil {
		return 0, err
	}
	owed := value.Mul(decimal.NewFromInt(points))

	var expires any
	if program.ExpiryMonths != nil {
		expires = time.Now().UTC().AddDate(0, *program.ExpiryMonths, 0)
	}

	entryID := uuid.New()
	if _, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: tenantID, CompanyID: companyID,
		Date:       time.Now().UTC(),
		SourceType: "loyalty_accrual", SourceID: entryID,
		RuleKey: "loyalty.accrue", PostedBy: userID,
		Memo: "Loyalty points earned",
	}, country, accounting.Transaction{
		Amounts: accounting.Amounts{"amount": owed},
	}); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO loyalty_entry
		  (id, tenant_id, company_id, customer_id, points, reason,
		   invoice_id, spend, expires_on, created_by)
		VALUES ($1,$2,$3,$4,$5,'earned',$6,$7,$8,$9)`,
		entryID, tenantID, companyID, *customerID, points, invoiceID,
		spend, expires, userID); err != nil {
		return 0, db.Translate(err, "Those points could not be awarded.")
	}
	return int(points), nil
}

// Spend takes points off a customer, and refuses to take more than they have.
//
// Called from inside the sale transaction. The customer row is locked first, so
// two tills redeeming against the same member at the same moment cannot both
// see the full balance.
func Spend(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID, customerID uuid.UUID,
	amount decimal.Decimal, invoiceID *uuid.UUID, userID *uuid.UUID,
) error {
	if !amount.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"Points have to be spent in a positive amount.")
	}

	var name string
	if err := tx.QueryRow(ctx,
		`SELECT name FROM customer WHERE id = $1 FOR UPDATE`, customerID).
		Scan(&name); err != nil {
		if err == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound,
				"That customer is not on this company's books.")
		}
		return err
	}

	program, err := Settings(ctx, tx, companyID)
	if err != nil {
		return err
	}
	if !program.Exists || !program.Active {
		return errs.New(errs.CodeConflict,
			"This company does not run a loyalty scheme, so points cannot be spent.")
	}

	value, err := decimal.NewFromString(program.PointValue)
	if err != nil || !value.IsPositive() {
		return errs.New(errs.CodeInternal,
			"The loyalty scheme does not say what a point is worth.")
	}

	// How many points that much money costs. Rounded UP, because the shop must
	// never hand over more value than the points it takes: half a point short
	// is the customer's rounding, not the shop's.
	needed := amount.Div(value).Ceil().IntPart()

	held, err := Balance(ctx, tx, customerID)
	if err != nil {
		return err
	}
	if int64(held) < needed {
		return errs.Newf(errs.CodeConflict,
			"%s has %d points, worth %s, and this sale wants to use %s.",
			name, held, value.Mul(decimal.NewFromInt(int64(held))).StringFixed(2),
			amount.StringFixed(2))
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO loyalty_entry
		  (tenant_id, company_id, customer_id, points, reason, invoice_id,
		   created_by)
		VALUES ($1,$2,$3,$4,'redeemed',$5,$6)`,
		tenantID, companyID, customerID, -needed, invoiceID, userID)
	return db.Translate(err, "Those points could not be spent.")
}

// TierFor says which rung a lifetime spend falls on, and what is left to the
// next one.
//
// The tiers are read in ascending order of threshold and the highest one the
// customer clears wins. A list that is not sorted is sorted here rather than
// trusted: a shop that typed Gold above Silver should still get the right
// answer.
func TierFor(tiers []Tier, lifetime decimal.Decimal) (at *Tier, next *Tier, gap decimal.Decimal) {
	sorted := make([]Tier, len(tiers))
	copy(sorted, tiers)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && thresholdOf(sorted[j]).LessThan(thresholdOf(sorted[j-1])); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	for i := range sorted {
		if lifetime.GreaterThanOrEqual(thresholdOf(sorted[i])) {
			at = &sorted[i]
			continue
		}
		next = &sorted[i]
		return at, next, thresholdOf(sorted[i]).Sub(lifetime)
	}
	return at, nil, decimal.Zero
}

func thresholdOf(t Tier) decimal.Decimal {
	d, err := decimal.NewFromString(strings.TrimSpace(t.MinSpend))
	if err != nil {
		return decimal.Zero
	}
	return d
}

// SegmentOf classifies a customer the way B16 asks.
//
// Order matters: a wholesale customer who has not been in for a year is
// at_risk, and being told they are "wholesale" would hide that. So the
// behavioural segments are tested before the ones that come off the customer
// record.
func SegmentOf(
	customerType string, visits int, lifetime decimal.Decimal,
	daysSinceLast int, vipThreshold decimal.Decimal,
) string {
	switch {
	case visits == 0:
		return "new"
	// A hundred and eighty days is two seasons in a clothing shop: long enough
	// that a regular customer has certainly missed one, short enough that
	// somebody can still be got back.
	case daysSinceLast > 180:
		return "at_risk"
	case vipThreshold.IsPositive() && lifetime.GreaterThanOrEqual(vipThreshold):
		return "vip"
	case visits >= 5:
		return "returning"
	case customerType == "wholesale":
		return "wholesale"
	default:
		return "retail"
	}
}

// Adjust corrects a customer's points by hand.
func (s *Service) Adjust(
	ctx context.Context, scope Scope, customerID uuid.UUID,
	points int, note string,
) (Card, error) {
	if points == 0 {
		return Card{}, errs.New(errs.CodeInvalidInput,
			"An adjustment of no points changes nothing.")
	}
	if strings.TrimSpace(note) == "" {
		return Card{}, errs.New(errs.CodeInvalidInput,
			"Say why the points are being adjusted.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if points < 0 {
			held, e := Balance(ctx, tx, customerID)
			if e != nil {
				return e
			}
			if held+points < 0 {
				return errs.Newf(errs.CodeConflict,
					"That customer has %d points, so %d cannot be taken off.",
					held, -points)
			}
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO loyalty_entry
			  (tenant_id, company_id, customer_id, points, reason, note,
			   created_by)
			VALUES ($1,$2,$3,$4,'adjusted',$5,$6)`,
			scope.TenantID, scope.CompanyID, customerID, points,
			strings.TrimSpace(note), scope.UserID); e != nil {
			return db.Translate(e, "Those points could not be adjusted.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "loyalty_points_adjusted",
			EntityType: "customer", EntityID: &customerID,
			After: map[string]any{
				"points": points, "note": strings.TrimSpace(note),
			},
		})
	})
	if err != nil {
		return Card{}, err
	}
	return s.Card(ctx, scope, customerID)
}
