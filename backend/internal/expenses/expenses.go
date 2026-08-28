// Package expenses records what a business spends that is not stock.
//
// Blueprint C3 opens with the sentence this module answers: "the Owner must be
// able to see, in one click, exactly where every riyal is going." Rent,
// electricity, staff tea, the man who fixed the door — none of it arrives on a
// purchase order, none of it moves stock, and until this existed none of it
// reached the books at all unless a supplier happened to raise a bill.
//
// # The head decides two things, and the second one is a tax position
//
// An expense head is the category a shopkeeper picks — Rent, Utilities, Fuel —
// and it carries the account it posts to AND whether the VAT on it can be
// reclaimed. Blueprint E2.3 and design 02 rule 5: entertainment, some vehicles
// and fuel have restricted input VAT recovery, and where recovery is restricted
// "the VAT is absorbed into the expense rather than claimed, so the VAT return
// is not overstated".
//
// Absorbed, not discarded. The whole gross still leaves the bank, so the
// expense account carries the part the tax authority will not refund. A shop
// buying SAR 100 of fuel plus SAR 15 of VAT has spent SAR 115 on fuel, and its
// P&L should say so.
//
// # Why the split is stored rather than derived
//
// The recoverable and absorbed halves are written onto the line at the moment
// it is recorded. A head's flag can be corrected later — a shop that had fuel
// marked recoverable and learns otherwise will change it — and what was CLAIMED
// on a return that has already been filed must not change with it. Recomputing
// from today's flag would silently rewrite last quarter.
package expenses

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// moneyScale is two decimals, the scale a posted amount carries.
const moneyScale = 2

// lineScale is four, the scale the schema stores. Tax is computed here and
// rounded to it, so the stored figures and the ones the CHECK constraints
// compare are the same figures.
const lineScale = 4

// Service records expenses and the heads they are booked to.
type Service struct {
	pool  *db.Pool
	rules *registry.Service
}

// NewService builds the service.
func NewService(pool *db.Pool, rules *registry.Service) *Service {
	return &Service{pool: pool, rules: rules}
}

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// --- expense heads --------------------------------------------------------

// Head is a category expenses are booked to.
type Head struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	NameAr      string    `json:"name_ar,omitempty"`
	AccountID   uuid.UUID `json:"account_id"`
	AccountCode string    `json:"account_code"`
	AccountName string    `json:"account_name"`
	// InputVATRecoverable is the one field here that is a tax position rather
	// than a label, which is why changing it needs its own permission.
	InputVATRecoverable bool `json:"input_vat_recoverable"`
	IsActive            bool `json:"is_active"`
	// Spent is what has been booked to this head, so a "where is my money
	// going" list can be read without a second request.
	Spent string `json:"spent"`
}

// NewHead is a head being created.
type NewHead struct {
	Code                string
	Name                string
	NameAr              string
	AccountID           uuid.UUID
	InputVATRecoverable bool
}

// Heads lists this company's expense categories.
func (s *Service) Heads(
	ctx context.Context, scope Scope, includeRetired bool,
) ([]Head, error) {
	out := []Head{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT h.id, h.code, h.name, coalesce(h.name_ar, ''),
			       h.account_id, a.code, a.name,
			       h.input_vat_recoverable, h.is_active,
			       coalesce((
			         SELECT sum(l.charge_amount)
			         FROM expense_line l
			         JOIN expense x ON x.id = l.expense_id
			         WHERE l.head_id = h.id AND x.company_id = h.company_id
			       ), 0)
			FROM expense_head h
			JOIN account a ON a.id = h.account_id
			WHERE h.company_id = $1 AND ($2 OR h.is_active)
			ORDER BY h.is_active DESC, h.name`,
			scope.CompanyID, includeRetired)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var h Head
			var spent decimal.Decimal
			if e := rows.Scan(&h.ID, &h.Code, &h.Name, &h.NameAr,
				&h.AccountID, &h.AccountCode, &h.AccountName,
				&h.InputVATRecoverable, &h.IsActive, &spent); e != nil {
				return e
			}
			h.Spent = spent.StringFixed(moneyScale)
			out = append(out, h)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// CreateHead adds a category.
//
// The account is checked by a database trigger rather than here: it must be an
// expense account, of this company, that entries can be posted to. Three
// conditions a foreign key cannot express and every one of which produces a
// journal entry that balances and is wrong.
func (s *Service) CreateHead(
	ctx context.Context, scope Scope, in NewHead,
) (Head, error) {
	if strings.TrimSpace(in.Code) == "" {
		return Head{}, errs.Validation("Give this category a short code you will recognise.").
			WithField("code", "A code is required.")
	}
	if strings.TrimSpace(in.Name) == "" {
		return Head{}, errs.Validation("Give this category a name.").
			WithField("name", "A name is required.")
	}
	if in.AccountID == uuid.Nil {
		return Head{}, errs.Validation("Say which account this category posts to.").
			WithField("account_id", "An expense account is required.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			INSERT INTO expense_head
			  (tenant_id, company_id, code, name, name_ar, account_id,
			   input_vat_recoverable, created_by)
			VALUES ($1,$2,btrim($3),btrim($4),nullif(btrim($5),''),$6,$7,$8)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.Code, in.Name, in.NameAr,
			in.AccountID, in.InputVATRecoverable, scope.UserID).Scan(&id)
		return db.Translate(e,
			"That expense category could not be created.")
	})
	if err != nil {
		return Head{}, conflictAsDuplicate(err, in.Code)
	}
	return s.head(ctx, scope, id)
}

// UpdateHead changes a category.
//
// The code is not editable, for the reason a customer code is not: it is what a
// year of expense reports refer to, and changing it silently rewrites what they
// mean. Recoverability IS editable — a shop that learns fuel cannot be
// reclaimed must be able to say so — and it takes effect from the next expense,
// never on ones already recorded.
func (s *Service) UpdateHead(
	ctx context.Context, scope Scope, id uuid.UUID, in NewHead,
) (Head, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Head{}, errs.Validation("Give this category a name.").
			WithField("name", "A name is required.")
	}
	if in.AccountID == uuid.Nil {
		return Head{}, errs.Validation("Say which account this category posts to.").
			WithField("account_id", "An expense account is required.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE expense_head
			SET name = btrim($3), name_ar = nullif(btrim($4),''),
			    account_id = $5, input_vat_recoverable = $6
			WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, in.Name, in.NameAr, in.AccountID,
			in.InputVATRecoverable)
		if e != nil {
			return db.Translate(e, "That expense category could not be changed.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That expense category was not found.")
		}
		return nil
	})
	if err != nil {
		return Head{}, err
	}
	return s.head(ctx, scope, id)
}

// SetHeadActive retires or restores a category.
//
// Retired, never deleted: expenses already booked to it have to keep saying
// what they were for. A retired head is simply not offered when recording a new
// one.
func (s *Service) SetHeadActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE expense_head SET is_active = $3
			WHERE id = $1 AND company_id = $2`, id, scope.CompanyID, active)
		if e != nil {
			return db.Translate(e, "That expense category could not be changed.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That expense category was not found.")
		}
		return nil
	})
}

func (s *Service) head(ctx context.Context, scope Scope, id uuid.UUID) (Head, error) {
	heads, err := s.Heads(ctx, scope, true)
	if err != nil {
		return Head{}, err
	}
	for _, h := range heads {
		if h.ID == id {
			return h, nil
		}
	}
	return Head{}, errs.New(errs.CodeNotFound, "That expense category was not found.")
}

// ExpenseAccount is one account a head may post to.
type ExpenseAccount struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

// Accounts lists the expense accounts a head may be pointed at.
//
// Postable ones only. A heading exists to group accounts and cannot carry a
// balance, so offering it would produce a head that fails the moment somebody
// records their first expense against it.
func (s *Service) Accounts(ctx context.Context, scope Scope) ([]ExpenseAccount, error) {
	out := []ExpenseAccount{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, code, name FROM account
			WHERE company_id = $1 AND type = 'expense' AND is_postable
			ORDER BY code`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a ExpenseAccount
			if e := rows.Scan(&a.ID, &a.Code, &a.Name); e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

func conflictAsDuplicate(err error, code string) error {
	if errs.CodeOf(err) == errs.CodeConflict {
		return errs.Newf(errs.CodeConflict,
			"There is already an expense category with the code %q.",
			strings.ToUpper(strings.TrimSpace(code)))
	}
	return err
}
