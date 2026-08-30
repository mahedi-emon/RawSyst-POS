// Package assets is the fixed asset register and its depreciation (C7), and
// the investor register (C3.2).
//
// # Why an asset is not an expense
//
// A shop that buys a delivery van has not spent the money in the sense a shop
// that buys electricity has: it still has the van. So the cost sits on the
// balance sheet and reaches the profit and loss a little at a time over the
// years the van is useful.
//
// Getting that wrong is a real error in both directions. Capitalising a repair
// flatters this year's profit; expensing a van understates the assets a bank is
// lending against. This module does not decide which is which — a person does,
// by recording something here or as an expense — but it makes the accounting
// consequences of that decision automatic once it is made.
//
// # Straight line, and nothing else
//
// C7 asks for "automated monthly straight-line depreciation with journal
// postings to the general ledger". Straight line is one division: cost less
// residual, over the useful life in months. Reducing balance, units of
// production and the rest are not asked for and are not offered, because a
// method column with one value in it is a column somebody will one day set to a
// method the code does not implement.
//
// # The month is the unit, and it is charged once
//
// Depreciation is charged per asset per calendar month, and the database
// enforces one charge per asset per month with a unique index. A run that
// happened twice would halve the asset's remaining life with nothing looking
// wrong — the kind of error found when a van reaches zero a year early.
package assets

import (
	"context"
	"errors"
	"fmt"
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

// moneyScale is two decimals, the scale a posted amount carries.
const moneyScale = 2

// Service manages the asset and investor registers.
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

// Asset is one thing the business owns.
type Asset struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"asset_no"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar,omitempty"`
	Category string    `json:"category"`

	Store         string `json:"store,omitempty"`
	Custodian     string `json:"custodian,omitempty"`
	SerialNumber  string `json:"serial_number,omitempty"`
	WarrantyUntil string `json:"warranty_until,omitempty"`

	AcquiredOn string `json:"acquired_on"`
	Cost       string `json:"cost"`
	Residual   string `json:"residual_value"`
	LifeMonths int    `json:"useful_life_months"`
	Currency   string `json:"currency"`

	// Depreciated is what has been charged so far; BookValue is cost less that.
	// Both summed from the depreciation ledger rather than stored, for the
	// reason a bank balance is summed from the journal: a second copy of a
	// number is a number that can disagree.
	Depreciated string `json:"depreciated"`
	BookValue   string `json:"book_value"`

	// MonthlyCharge is what one month costs. Shown because it is the figure a
	// person sanity-checks the life against — "forty a month for a laptop"
	// reads wrong in a way "sixty months" does not.
	MonthlyCharge string `json:"monthly_charge"`

	// DepreciatedTo is the last month charged, as its first day. Empty until
	// the first charge.
	DepreciatedTo string `json:"depreciated_to,omitempty"`
	// MonthsDue is how many months are waiting to be charged.
	MonthsDue int `json:"months_due"`

	Status           string `json:"status"`
	DisposedOn       string `json:"disposed_on,omitempty"`
	DisposalProceeds string `json:"disposal_proceeds,omitempty"`
	DisposalNote     string `json:"disposal_note,omitempty"`
}

// NewAsset is something being added to the register.
type NewAsset struct {
	Name          string
	NameAr        string
	Category      string
	StoreID       *uuid.UUID
	CustodianID   *uuid.UUID
	SerialNumber  string
	WarrantyUntil *time.Time

	AcquiredOn time.Time
	Cost       decimal.Decimal
	Residual   decimal.Decimal
	LifeMonths int
}

// Register lists what the business owns.
func (s *Service) Register(
	ctx context.Context, scope Scope, includeDisposed bool,
) ([]Asset, error) {
	out := []Asset{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, assetSelect+`
			WHERE a.company_id = $1 AND ($2 OR a.status = 'in_use')
			ORDER BY a.asset_no`, scope.CompanyID, includeDisposed)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			asset, e := scanAsset(rows)
			if e != nil {
				return e
			}
			out = append(out, asset)
		}
		return rows.Err()
	})
	return out, err
}

// Add puts something in the register.
//
// It does NOT post. Buying an asset is a purchase, and the money left the
// business through whatever paid for it — a supplier bill, a cash payment —
// which this product already records. Posting again here would put the van on
// the balance sheet twice.
//
// What this does is start the depreciation clock.
func (s *Service) Add(
	ctx context.Context, scope Scope, in NewAsset,
) (Asset, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Asset{}, errs.Validation("Give the asset a name.").
			WithField("name", "What somebody would call it if it went missing.")
	}
	if !in.Cost.IsPositive() {
		return Asset{}, errs.New(errs.CodeInvalidInput,
			"Say what the asset cost.")
	}
	if in.Residual.IsNegative() || in.Residual.GreaterThanOrEqual(in.Cost) {
		return Asset{}, errs.New(errs.CodeInvalidInput,
			"What the asset will be worth at the end must be less than what it "+
				"cost, and not below nothing.")
	}
	if in.LifeMonths <= 0 {
		return Asset{}, errs.Validation("Say how long the asset will last.").
			WithField("useful_life_months",
				"In months. Sixty for a vehicle, thirty-six for a computer, is usual.")
	}

	var out Asset
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		number, e := claimAssetNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO fixed_asset
			  (tenant_id, company_id, asset_no, name, name_ar, category,
			   store_id, custodian_id, serial_number, warranty_until,
			   acquired_on, cost, residual_value, useful_life_months, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, strings.TrimSpace(in.Name),
			nullIfBlank(in.NameAr), strings.TrimSpace(in.Category),
			in.StoreID, in.CustodianID, nullIfBlank(in.SerialNumber),
			in.WarrantyUntil, in.AcquiredOn, in.Cost, in.Residual,
			in.LifeMonths, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That asset could not be added.")
		}

		read, e := s.readAsset(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// --- depreciation ---------------------------------------------------------

// Charged is what a depreciation run did.
type Charged struct {
	// Month is the first day of the month charged for.
	Month string `json:"month"`

	Assets int    `json:"assets_charged"`
	Total  string `json:"total"`

	Currency string `json:"currency"`

	// Skipped names assets that had nothing to charge and why, so a run that
	// charged four of six is explicable without opening each one.
	Skipped []string `json:"skipped,omitempty"`
}

// Depreciate charges one month across every asset that owes it.
//
// # Why a month at a time, and never "catch up to today"
//
// Each month's charge belongs in that month's profit and loss. A single
// catch-up entry dated today would put a year of depreciation into one month,
// which is wrong in every statement anybody draws afterwards — so a company
// that has not run it for a year runs it twelve times, and each entry lands
// where it belongs.
//
// The screen offers the next month due rather than making somebody pick.
func (s *Service) Depreciate(
	ctx context.Context, scope Scope, month time.Time,
) (Charged, error) {
	first := firstOfMonth(month)

	var out Charged
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		out = Charged{Month: first.Format("2006-01-02"), Total: "0.00"}
		if e := readCurrency(ctx, tx, scope.CompanyID, &out.Currency); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT a.id, a.asset_no, a.name, a.cost, a.residual_value,
			       a.useful_life_months, a.acquired_on, a.depreciated_to,
			       coalesce((SELECT sum(d.amount) FROM asset_depreciation d
			                 WHERE d.asset_id = a.id), 0)
			FROM fixed_asset a
			WHERE a.company_id = $1 AND a.status = 'in_use'
			ORDER BY a.asset_no
			FOR UPDATE OF a`, scope.CompanyID)
		if e != nil {
			return e
		}

		type due struct {
			id                 uuid.UUID
			number, name       string
			cost, residual     decimal.Decimal
			life               int
			acquired           time.Time
			depreciatedTo      *time.Time
			alreadyDepreciated decimal.Decimal
		}
		var candidates []due
		for rows.Next() {
			var d due
			if e := rows.Scan(&d.id, &d.number, &d.name, &d.cost, &d.residual,
				&d.life, &d.acquired, &d.depreciatedTo,
				&d.alreadyDepreciated); e != nil {
				rows.Close()
				return e
			}
			candidates = append(candidates, d)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		total := decimal.Zero
		charged := 0
		for _, d := range candidates {
			// Not yet owned. An asset bought in March is not depreciated in
			// February, and a run over an earlier month must skip it rather
			// than charge a month the business did not have it.
			if firstOfMonth(d.acquired).After(first) {
				out.Skipped = append(out.Skipped,
					fmt.Sprintf("%s — not owned in that month", d.number))
				continue
			}
			if d.depreciatedTo != nil && !d.depreciatedTo.Before(first) {
				out.Skipped = append(out.Skipped,
					fmt.Sprintf("%s — already charged for that month", d.number))
				continue
			}

			// Straight line: cost less residual, over the life. The LAST month
			// takes whatever is left rather than the computed figure, so the
			// asset lands exactly on its residual value instead of a few
			// hallalas either side — the same rounding-remainder rule the rest
			// of this product applies when a whole is split.
			depreciable := d.cost.Sub(d.residual)
			monthly := depreciable.Div(decimal.NewFromInt(int64(d.life))).
				Round(moneyScale)
			remaining := depreciable.Sub(d.alreadyDepreciated)

			if !remaining.IsPositive() {
				out.Skipped = append(out.Skipped,
					fmt.Sprintf("%s — fully depreciated", d.number))
				continue
			}
			amount := monthly
			if amount.GreaterThan(remaining) {
				amount = remaining
			}
			if !amount.IsPositive() {
				continue
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO asset_depreciation
				  (tenant_id, asset_id, charged_for, amount)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, d.id, first, amount); e != nil {
				return db.Translate(e,
					"That month has already been charged on one of these assets.")
			}
			if _, e := tx.Exec(ctx,
				`UPDATE fixed_asset SET depreciated_to = $2 WHERE id = $1`,
				d.id, first); e != nil {
				return e
			}

			total = total.Add(amount)
			charged++
		}

		out.Assets = charged
		out.Total = total.StringFixed(2)
		if charged == 0 {
			return nil
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// One entry for the month, not one per asset. A shop with sixty
		// assets would otherwise put sixty entries in its ledger every month,
		// and the per-asset detail is in `asset_depreciation` where it belongs.
		//
		// Dated the LAST day of the month, because that is the day the charge
		// is for. Dating it today would put an old month's depreciation into
		// the current month's profit and loss.
		if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       lastOfMonth(first),
			SourceType: "asset_depreciation",
			SourceID:   depreciationID(scope.CompanyID, first),
			RuleKey:    "asset.depreciation", PostedBy: &scope.UserID,
			Memo: "Depreciation for " + first.Format("January 2006"),
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{"amount": total},
		}); e != nil {
			return e
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "depreciation_charged",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"month": first.Format("2006-01"),
				"total": total.StringFixed(2), "assets": charged,
			},
		})
	})
	return out, err
}

// depreciationID is the identity of one company's charge for one month.
//
// Derived rather than random, so a second run of the same month finds the first
// through the posting engine's idempotency key rather than posting a second
// entry. The unique index on `asset_depreciation` catches it too; this catches
// it one layer earlier and without the transaction having to roll back.
func depreciationID(companyID uuid.UUID, month time.Time) uuid.UUID {
	return uuid.NewSHA1(companyID,
		[]byte("asset_depreciation:"+month.Format("2006-01")))
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func lastOfMonth(t time.Time) time.Time {
	return firstOfMonth(t).AddDate(0, 1, -1)
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

func claimAssetNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n string
	err := tx.QueryRow(ctx, `SELECT claim_asset_no($1)`, companyID).Scan(&n)
	return n, err
}

func readCurrency(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, into *string,
) error {
	e := tx.QueryRow(ctx,
		`SELECT base_currency FROM company WHERE id = $1`, companyID).Scan(into)
	if errors.Is(e, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return e
}
