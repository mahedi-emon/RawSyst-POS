package assets

// Disposal, and reading the register.
//
// # The gain or loss is a measurement, never an input
//
// C7 asks for disposal "with automatic gain/loss-on-disposal calculation". The
// arithmetic is fixed and short:
//
//	book value = cost − everything depreciated so far
//	result     = proceeds − book value
//
// A person recording a disposal says what they got for it, and nothing else.
// They do not get to say whether it was a gain: that is derived, for the same
// reason cost of goods sold comes from the costing engine rather than from the
// till. A figure the party being measured supplies is not a measurement.
//
// # Both rules clear the asset out completely
//
// Whichever direction it went, the entry removes the cost from Fixed Assets and
// the accumulated depreciation from its contra account, so the balance sheet
// stops carrying an asset the business no longer has. Writing down the asset
// account alone would leave the accumulated depreciation behind for ever,
// slowly making the contra account meaningless.

import (
	"context"
	"errors"
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

// Disposal is an asset leaving the business.
type Disposal struct {
	AssetID uuid.UUID

	// Proceeds is what the business got for it. Zero for something scrapped,
	// which is a disposal like any other and usually a loss.
	Proceeds decimal.Decimal

	// MoneyAccountID is where the proceeds landed. Required when there are any,
	// refused when there are none: money that arrived went somewhere.
	MoneyAccountID *uuid.UUID

	DisposedOn time.Time
	Note       string
}

// Disposed is what a disposal did.
type Disposed struct {
	Asset     string `json:"asset"`
	BookValue string `json:"book_value"`
	Proceeds  string `json:"proceeds"`

	// Result is proceeds less book value: positive for a gain, negative for a
	// loss, and zero when it sold for exactly what the books said.
	Result   string `json:"result"`
	Currency string `json:"currency"`
}

// Dispose records an asset leaving and posts the result.
func (s *Service) Dispose(
	ctx context.Context, scope Scope, in Disposal,
) (Disposed, error) {
	if in.Proceeds.IsNegative() {
		return Disposed{}, errs.New(errs.CodeInvalidInput,
			"Proceeds cannot be less than nothing. An asset that cost money to "+
				"remove is a disposal for nothing plus an expense.")
	}
	if in.Proceeds.IsPositive() && in.MoneyAccountID == nil {
		return Disposed{}, errs.New(errs.CodeInvalidInput,
			"Say where the money went.")
	}

	var out Disposed
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var name, status string
		var cost decimal.Decimal
		var depreciated decimal.Decimal
		e := tx.QueryRow(ctx, `
			SELECT a.name, a.status, a.cost,
			       coalesce((SELECT sum(d.amount) FROM asset_depreciation d
			                 WHERE d.asset_id = a.id), 0)
			FROM fixed_asset a
			WHERE a.id = $1 AND a.company_id = $2
			FOR UPDATE OF a`,
			in.AssetID, scope.CompanyID).Scan(&name, &status, &cost, &depreciated)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That asset is not this business's.")
		}
		if e != nil {
			return e
		}
		if status == "disposed" {
			return errs.Newf(errs.CodeConflict,
				"%s has already been disposed of.", name)
		}

		bookValue := cost.Sub(depreciated)
		result := in.Proceeds.Sub(bookValue)

		disposedOn := in.DisposedOn
		if disposedOn.IsZero() {
			disposedOn = time.Now().UTC()
		}

		if _, e := tx.Exec(ctx, `
			UPDATE fixed_asset
			SET status = 'disposed', disposed_on = $2,
			    disposal_proceeds = $3, disposal_note = $4
			WHERE id = $1`,
			in.AssetID, disposedOn, in.Proceeds, nullIfBlank(in.Note)); e != nil {
			return db.Translate(e, "That disposal could not be recorded.")
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// The proceeds group. Empty when the asset was scrapped, which the rule
		// handles by expanding `for_each` into nothing — the entry is then the
		// accumulated depreciation and the loss against the cost, and it still
		// balances.
		proceeds := accounting.Group{}
		if in.Proceeds.IsPositive() && in.MoneyAccountID != nil {
			ledgerAccount, e := ledgerAccountOf(ctx, tx, scope, *in.MoneyAccountID)
			if e != nil {
				return e
			}
			proceeds = append(proceeds, accounting.GroupMember{
				AccountID: &ledgerAccount, Amount: in.Proceeds,
				Memo: "Proceeds from " + name,
			})
		}

		// Two rules, one per direction, and the difference is passed as its
		// absolute value. A single rule taking a signed figure would write a
		// negative debit where a credit belongs — the reasoning 0025, 0026 and
		// 0052 all give, applied a fourth time.
		ruleKey := "asset.disposal_loss"
		if result.IsPositive() {
			ruleKey = "asset.disposal_gain"
		}

		// An asset sold for exactly its book value has no gain and no loss, and
		// the rule would try to post a line for nothing. Both rules are written
		// with the difference line present, so the zero case takes the loss rule
		// with a zero amount — which the posting engine drops.
		if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: disposedOn, SourceType: "asset_disposal", SourceID: in.AssetID,
			RuleKey: ruleKey, PostedBy: &scope.UserID,
			Memo: "Disposal of " + name,
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{
				"cost":        cost,
				"depreciated": depreciated,
				"difference":  result.Abs(),
			},
			Groups: map[string]accounting.Group{"proceeds": proceeds},
		}); e != nil {
			return e
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "asset_disposed",
			EntityType: "fixed_asset", EntityID: &in.AssetID,
			After: map[string]any{
				"asset": name, "proceeds": in.Proceeds.StringFixed(2),
				"book_value": bookValue.StringFixed(2),
				"result":     result.StringFixed(2),
			},
		}); e != nil {
			return e
		}

		out = Disposed{
			Asset: name, BookValue: bookValue.StringFixed(2),
			Proceeds: in.Proceeds.StringFixed(2),
			Result:   result.StringFixed(2),
		}
		return readCurrency(ctx, tx, scope.CompanyID, &out.Currency)
	})
	return out, err
}

// ledgerAccountOf is the chart account behind a money account.
func ledgerAccountOf(
	ctx context.Context, tx pgx.Tx, scope Scope, moneyAccountID uuid.UUID,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT account_id FROM money_account
		WHERE id = $1 AND company_id = $2 AND is_active`,
		moneyAccountID, scope.CompanyID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errs.New(errs.CodeNotFound,
			"That account is not one this business can receive money into.")
	}
	return id, err
}

// --- reading --------------------------------------------------------------

// assetSelect is the one query shape the register and the single read share, so
// the two cannot drift into reporting different book values.
const assetSelect = `
	SELECT a.id, a.asset_no, a.name, coalesce(a.name_ar, ''), a.category,
	       coalesce(st.name, ''), coalesce(u.full_name, ''),
	       coalesce(a.serial_number, ''),
	       coalesce(to_char(a.warranty_until, 'YYYY-MM-DD'), ''),
	       to_char(a.acquired_on, 'YYYY-MM-DD'),
	       a.cost, a.residual_value, a.useful_life_months, c.base_currency,
	       coalesce((SELECT sum(d.amount) FROM asset_depreciation d
	                 WHERE d.asset_id = a.id), 0),
	       coalesce(to_char(a.depreciated_to, 'YYYY-MM-DD'), ''),
	       a.status,
	       coalesce(to_char(a.disposed_on, 'YYYY-MM-DD'), ''),
	       coalesce(a.disposal_proceeds::text, ''),
	       coalesce(a.disposal_note, '')
	FROM fixed_asset a
	JOIN company c ON c.id = a.company_id
	LEFT JOIN store st ON st.id = a.store_id
	LEFT JOIN app_user u ON u.id = a.custodian_id`

func scanAsset(rows pgx.Rows) (Asset, error) {
	var a Asset
	var cost, residual, depreciated decimal.Decimal
	if err := rows.Scan(&a.ID, &a.Number, &a.Name, &a.NameAr, &a.Category,
		&a.Store, &a.Custodian, &a.SerialNumber, &a.WarrantyUntil,
		&a.AcquiredOn, &cost, &residual, &a.LifeMonths, &a.Currency,
		&depreciated, &a.DepreciatedTo, &a.Status, &a.DisposedOn,
		&a.DisposalProceeds, &a.DisposalNote); err != nil {
		return Asset{}, err
	}
	fill(&a, cost, residual, depreciated)
	return a, nil
}

// fill works out the figures that are derived rather than stored.
func fill(a *Asset, cost, residual, depreciated decimal.Decimal) {
	a.Cost = cost.StringFixed(2)
	a.Residual = residual.StringFixed(2)
	a.Depreciated = depreciated.StringFixed(2)
	a.BookValue = cost.Sub(depreciated).StringFixed(2)

	if a.LifeMonths > 0 {
		a.MonthlyCharge = cost.Sub(residual).
			Div(decimal.NewFromInt(int64(a.LifeMonths))).
			Round(moneyScale).StringFixed(2)
	}

	// How many months are waiting. Counted from the last charge, or from
	// acquisition when there has never been one, up to the month just gone —
	// the current month is not due until it is over.
	if a.Status != "in_use" {
		return
	}
	from := a.DepreciatedTo
	if from == "" {
		from = a.AcquiredOn
		if start, err := time.Parse("2006-01-02", from); err == nil {
			// Acquisition month itself is chargeable, so count from the month
			// before it to make the arithmetic below one rule rather than two.
			from = start.AddDate(0, -1, 0).Format("2006-01-02")
		}
	}
	last, err := time.Parse("2006-01-02", from)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	months := (now.Year()-last.Year())*12 + int(now.Month()) - int(last.Month()) - 1
	if months < 0 {
		months = 0
	}
	// Never more than the life has left in it.
	charged := int(decimal.RequireFromString(a.Depreciated).
		Div(decimal.RequireFromString(nonZero(a.MonthlyCharge))).
		IntPart())
	if remaining := a.LifeMonths - charged; months > remaining {
		months = max(0, remaining)
	}
	a.MonthsDue = months
}

func nonZero(s string) string {
	if s == "" || s == "0.00" {
		return "1"
	}
	return s
}

func (s *Service) readAsset(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Asset, error) {
	rows, err := tx.Query(ctx, assetSelect+`
		WHERE a.id = $1 AND a.company_id = $2`, id, scope.CompanyID)
	if err != nil {
		return Asset{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Asset{}, errs.New(errs.CodeNotFound,
			"That asset is not this business's.")
	}
	return scanAsset(rows)
}

// Asset reads one.
func (s *Service) Asset(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Asset, error) {
	var out Asset
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		a, e := s.readAsset(ctx, tx, scope, id)
		out = a
		return e
	})
	return out, err
}

var _ = strings.TrimSpace
