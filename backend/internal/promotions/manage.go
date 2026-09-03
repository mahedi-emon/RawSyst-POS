package promotions

// Setting campaigns up, and recording what they did.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// NewPromotion is a campaign being set up.
type NewPromotion struct {
	Code   string
	Name   string
	NameAr string
	Kind   string

	Value  decimal.Decimal
	BuyQty decimal.Decimal
	GetQty decimal.Decimal

	CategoryID   *uuid.UUID
	BrandID      *uuid.UUID
	VariantID    *uuid.UUID
	CustomerType string

	StartsOn    *time.Time
	EndsOn      *time.Time
	StoreID     *uuid.UUID
	MinPurchase decimal.Decimal

	CouponCode         string
	MaxUses            *int
	MaxUsesPerCustomer *int

	Priority int
}

// List reads the campaigns, with what each has cost so far.
func (s *Service) List(
	ctx context.Context, scope Scope, includeFinished bool,
) ([]Promotion, error) {
	out := []Promotion{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.code, p.name, coalesce(p.name_ar, ''), p.kind,
			       p.value, p.buy_qty, p.get_qty,
			       p.category_id, p.brand_id, p.variant_id,
			       coalesce(p.customer_type, ''),
			       coalesce(to_char(p.starts_on, 'YYYY-MM-DD'), ''),
			       coalesce(to_char(p.ends_on, 'YYYY-MM-DD'), ''),
			       p.store_id, p.min_purchase,
			       coalesce(p.coupon_code, ''), p.max_uses, p.max_uses_per_customer,
			       p.is_active, p.priority, c.base_currency,
			       coalesce(cat.name, ''), coalesce(b.name, ''), coalesce(pr.name, ''),
			       (SELECT count(*) FROM promotion_redemption r
			        WHERE r.promotion_id = p.id),
			       coalesce((SELECT sum(r.discount) FROM promotion_redemption r
			                 WHERE r.promotion_id = p.id), 0),
			       coalesce((SELECT sum(r.line_total) FROM promotion_redemption r
			                 WHERE r.promotion_id = p.id), 0)
			FROM promotion p
			JOIN company c ON c.id = p.company_id
			LEFT JOIN category cat ON cat.id = p.category_id
			LEFT JOIN brand b ON b.id = p.brand_id
			LEFT JOIN variant v ON v.id = p.variant_id
			LEFT JOIN product pr ON pr.id = v.product_id
			WHERE p.company_id = $1
			  AND ($2 OR (p.is_active
			              AND (p.ends_on IS NULL OR p.ends_on >= current_date)))
			ORDER BY p.is_active DESC, p.priority DESC, p.code`,
			scope.CompanyID, includeFinished)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var p Promotion
			var value, buyQty, getQty, minPurchase decimal.Decimal
			var categoryName, brandName, productName string
			var given, sold decimal.Decimal

			if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.NameAr, &p.Kind,
				&value, &buyQty, &getQty, &p.CategoryID, &p.BrandID, &p.VariantID,
				&p.CustomerType, &p.StartsOn, &p.EndsOn, &p.StoreID, &minPurchase,
				&p.CouponCode, &p.MaxUses, &p.MaxUsesPerCustomer,
				&p.Active, &p.Priority, &p.Currency,
				&categoryName, &brandName, &productName,
				&p.Used, &given, &sold); err != nil {
				return err
			}

			p.Value = trimmed(value)
			p.BuyQty = trimmed(buyQty)
			p.GetQty = trimmed(getQty)
			p.MinPurchase = trimmed(minPurchase)
			p.Given = given.StringFixed(2)
			p.Sold = sold.StringFixed(2)
			p.Applies = appliesTo(productName, brandName, categoryName, p.CustomerType)
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// appliesTo says what a promotion's scope amounts to, in the order a person
// would name it: the narrowest thing first.
//
// Built here rather than in the screen because it is a fact about the row, and
// two front ends would otherwise each write their own version of this sentence
// and disagree about the order.
func appliesTo(product, brand, category, customerType string) string {
	parts := []string{}
	switch {
	case product != "":
		parts = append(parts, product)
	case brand != "":
		parts = append(parts, brand)
	case category != "":
		parts = append(parts, category)
	}
	if customerType != "" {
		parts = append(parts, customerType)
	}
	return strings.Join(parts, " · ")
}

func trimmed(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

// Create sets a campaign up.
func (s *Service) Create(
	ctx context.Context, scope Scope, in NewPromotion,
) (Promotion, error) {
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	name := strings.TrimSpace(in.Name)

	if code == "" {
		return Promotion{}, errs.Validation("Give the campaign a short code.").
			WithField("code", "How it will appear on a report.")
	}
	if name == "" {
		return Promotion{}, errs.Validation("Give the campaign a name.").
			WithField("name", "What a customer would call it.")
	}
	if err := checkKind(in); err != nil {
		return Promotion{}, err
	}

	// A cap on a promotion nobody has to type a code for cannot be counted.
	// The schema refuses it too; this says why.
	if in.CouponCode == "" &&
		(in.MaxUses != nil || in.MaxUsesPerCustomer != nil) {
		return Promotion{}, errs.New(errs.CodeInvalidInput,
			"A limit on how many times a promotion may be used only makes sense "+
				"with a coupon code. Without one the promotion simply applies, "+
				"and there is nothing to count.")
	}

	var out Promotion
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO promotion
			  (tenant_id, company_id, code, name, name_ar, kind, value,
			   buy_qty, get_qty, category_id, brand_id, variant_id,
			   customer_type, starts_on, ends_on, store_id, min_purchase,
			   coupon_code, max_uses, max_uses_per_customer, priority, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
			        $18,$19,$20,$21,$22)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, code, name, nullIfBlank(in.NameAr),
			in.Kind, zeroIsNull(in.Value), zeroIsNull(in.BuyQty),
			zeroIsNull(in.GetQty), in.CategoryID, in.BrandID, in.VariantID,
			nullIfBlank(in.CustomerType), in.StartsOn, in.EndsOn, in.StoreID,
			zeroIsNull(in.MinPurchase),
			nullIfBlank(strings.ToUpper(in.CouponCode)),
			in.MaxUses, in.MaxUsesPerCustomer, in.Priority,
			scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That campaign could not be set up.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "promotion_created",
			EntityType: "promotion", EntityID: &id,
			After: map[string]any{"code": code, "name": name, "kind": in.Kind},
		}); e != nil {
			return e
		}

		read, e := s.read(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// checkKind refuses a promotion that could not be applied, in words.
//
// The schema refuses it too, and that CHECK is the guarantee. This exists so a
// person filling in a form is told which field is missing rather than which
// constraint failed.
func checkKind(in NewPromotion) error {
	switch in.Kind {
	case KindPercentage:
		if !in.Value.IsPositive() || in.Value.GreaterThan(decimal.NewFromInt(100)) {
			return errs.Validation("Say what percentage comes off.").
				WithField("value", "Between one and a hundred.")
		}
	case KindAmount:
		if !in.Value.IsPositive() {
			return errs.Validation("Say how much comes off.").
				WithField("value", "The amount taken off each line it applies to.")
		}
	case KindBuyXGetY:
		if !in.BuyQty.IsPositive() || !in.GetQty.IsPositive() {
			return errs.Validation("Say how many they buy and how many they get.").
				WithField("buy_qty", "Buy three, get one free is three and one.")
		}
	case KindBundlePrice:
		if in.Value.IsNegative() || !in.BuyQty.IsPositive() {
			return errs.Validation("Say how many, and for how much.").
				WithField("buy_qty", "Three shirts for a hundred is three and a hundred.")
		}
	default:
		return errs.Newf(errs.CodeInvalidInput,
			"%q is not a kind of promotion this product knows how to apply.",
			in.Kind)
	}
	return nil
}

// SetActive stops a campaign or starts it again.
//
// A campaign is never deleted. Its redemptions are what D2's analytics are
// drawn from, and a deleted campaign would take a month of discount history
// with it — which is exactly the history somebody is looking at when they ask
// whether the discounting is working.
func (s *Service) SetActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE promotion SET is_active = $3 WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That campaign is not this business's.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "promotion_" + map[bool]string{true: "resumed", false: "stopped"}[active],
			EntityType: "promotion", EntityID: &id,
		})
	})
}

// Redemption is one discount a promotion actually gave.
type Redemption struct {
	PromotionID uuid.UUID
	InvoiceID   *uuid.UUID
	CustomerID  *uuid.UUID
	Discount    decimal.Decimal
	LineTotal   decimal.Decimal
}

// Redeem records what promotions did on a finalised sale.
//
// Called inside the transaction that finalises the sale, for the reason the
// audit writer is: a redemption that commits without its invoice, or an invoice
// whose redemptions rolled back, both make the campaign figures wrong in a way
// nobody would notice until somebody asked what a campaign cost.
//
// # The caps are enforced HERE, not only in Quote
//
// `Quote` filters campaigns whose `max_uses` is already spent by counting this
// table, which is right for showing a cashier what applies and is not a control
// on anything. Two tills quoting the same last-use coupon a moment apart both
// see it as available, and both used to redeem it: a coupon good for one use
// was good for as many as there were counters. Nothing failed and nothing was
// logged — the campaign simply cost more than it was authorised to.
//
// So the campaign row is locked before its redemptions are counted. Anything
// else racing on the same campaign waits for this transaction, sees the
// redemption this one wrote, and is refused. The lock is per campaign, so tills
// selling under different campaigns never queue behind each other, and a sale
// with no promotion — nearly all of them — never reaches this code at all.
//
// This is the same shape as the credit limit in receivables, and for the same
// reason: a limit checked outside the transaction that spends against it is a
// suggestion.
func (s *Service) Redeem(
	ctx context.Context, tx pgx.Tx, scope Scope, given []Redemption,
) error {
	for _, r := range given {
		if !r.Discount.IsPositive() {
			continue
		}

		var maxUses, maxPerCustomer *int
		var label string
		err := tx.QueryRow(ctx, `
			SELECT max_uses, max_uses_per_customer,
			       coalesce(nullif(coupon_code, ''), name)
			FROM promotion
			WHERE id = $1 AND company_id = $2
			FOR UPDATE`, r.PromotionID, scope.CompanyID).
			Scan(&maxUses, &maxPerCustomer, &label)
		if errors.Is(err, pgx.ErrNoRows) {
			// Named by a till but not this company's. Refused rather than
			// skipped: the sale claimed a discount from it.
			return errs.New(errs.CodeNotFound,
				"That campaign is not this business's.")
		}
		if err != nil {
			return db.Translate(err, "")
		}

		if maxUses != nil {
			var used int
			if e := tx.QueryRow(ctx, `
				SELECT count(*) FROM promotion_redemption
				WHERE promotion_id = $1`, r.PromotionID).Scan(&used); e != nil {
				return db.Translate(e, "")
			}
			if used >= *maxUses {
				return errs.Newf(errs.CodeConflict,
					"%q has been used %d times, which is all it was issued "+
						"for. Ring the sale up without it.", label, used)
			}
		}

		if maxPerCustomer != nil && r.CustomerID != nil {
			var used int
			if e := tx.QueryRow(ctx, `
				SELECT count(*) FROM promotion_redemption
				WHERE promotion_id = $1 AND customer_id = $2`,
				r.PromotionID, *r.CustomerID).Scan(&used); e != nil {
				return db.Translate(e, "")
			}
			if used >= *maxPerCustomer {
				return errs.Newf(errs.CodeConflict,
					"This customer has already used %q %d times, which is "+
						"all they may. Ring the sale up without it.",
					label, used)
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO promotion_redemption
			  (tenant_id, company_id, promotion_id, invoice_id, customer_id,
			   discount, line_total)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			scope.TenantID, scope.CompanyID, r.PromotionID, r.InvoiceID,
			r.CustomerID, r.Discount, r.LineTotal); err != nil {
			return db.Translate(err, "A discount could not be recorded.")
		}
	}
	return nil
}

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Promotion, error) {
	var p Promotion
	var value, buyQty, getQty, minPurchase decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.code, p.name, coalesce(p.name_ar, ''), p.kind,
		       p.value, p.buy_qty, p.get_qty, p.category_id, p.brand_id,
		       p.variant_id, coalesce(p.customer_type, ''),
		       coalesce(to_char(p.starts_on, 'YYYY-MM-DD'), ''),
		       coalesce(to_char(p.ends_on, 'YYYY-MM-DD'), ''),
		       p.store_id, p.min_purchase, coalesce(p.coupon_code, ''),
		       p.max_uses, p.max_uses_per_customer, p.is_active, p.priority,
		       c.base_currency
		FROM promotion p
		JOIN company c ON c.id = p.company_id
		WHERE p.id = $1 AND p.company_id = $2`,
		id, scope.CompanyID).
		Scan(&p.ID, &p.Code, &p.Name, &p.NameAr, &p.Kind, &value, &buyQty,
			&getQty, &p.CategoryID, &p.BrandID, &p.VariantID, &p.CustomerType,
			&p.StartsOn, &p.EndsOn, &p.StoreID, &minPurchase, &p.CouponCode,
			&p.MaxUses, &p.MaxUsesPerCustomer, &p.Active, &p.Priority,
			&p.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Promotion{}, errs.New(errs.CodeNotFound,
			"That campaign is not this business's.")
	}
	p.Value = trimmed(value)
	p.BuyQty = trimmed(buyQty)
	p.GetQty = trimmed(getQty)
	p.MinPurchase = trimmed(minPurchase)
	p.Given = "0.00"
	p.Sold = "0.00"
	return p, err
}

func zeroIsNull(d decimal.Decimal) any {
	if d.IsZero() {
		return nil
	}
	return d
}
