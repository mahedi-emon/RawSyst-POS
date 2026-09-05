package accounting

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// The chart of accounts, as a screen reads it.
//
// The product had no way to list it. Every posting path resolves accounts by
// ROLE — `inventory`, `input_vat`, `accounts_payable` — which is right for a
// rule and useless to a person writing an adjustment: `POST
// /accounting/journals` takes an `account_id` per line, and nothing anywhere
// answered "which accounts are there".
//
// `/expenses/accounts` exists and is not this. It returns postable EXPENSE
// accounts only, and is gated on `expense.manage_heads` because choosing what
// a spending category posts to is a configuration decision. A journal touches
// any account in the chart and is read by anybody who may read the ledger.
//
// # Header accounts are included, and marked
//
// A chart that hid them would be a list rather than a chart: "5000 Operating
// Expenses" is what makes "5100 Cost of Goods Sold" legible, and a screen
// showing the second without the first shows a flat list of codes. They carry
// `is_postable: false`, and the journal screen offers only the ones that can
// hold an entry — posting to a header is how a chart silently stops adding up.
//
// # The balance, because a chart with no figures is a filing cabinet
//
// What each account holds today, in the company's own currency, as
// debits-less-credits. An asset with a credit balance and a liability with a
// debit one are both worth seeing at a glance, so the sign is carried rather
// than flipped per account type — the screen knows the type and can say what
// the sign means.

// Account is one line of the chart.
type Account struct {
	ID     uuid.UUID  `json:"id"`
	Code   string     `json:"code"`
	Name   string     `json:"name"`
	NameAr string     `json:"name_ar,omitempty"`
	Type   string     `json:"type"`
	Parent *uuid.UUID `json:"parent_id,omitempty"`

	// IsPostable says whether an entry may name it. A header may not.
	IsPostable bool `json:"is_postable"`
	// IsControl marks an account a sub-ledger must reconcile to exactly.
	// C9.3 makes three of these hard invariants: receivable, payable,
	// inventory.
	IsControl bool   `json:"is_control"`
	ControlOf string `json:"control_of,omitempty"`

	// Currency is empty when the account uses the company's own, which is the
	// ordinary case.
	Currency string `json:"currency,omitempty"`
	IsActive bool   `json:"is_active"`

	// Role is the posting rules' name for it, where one is mapped. This is
	// what makes the chart legible as a system rather than as a list: an owner
	// asking "where does VAT go" is asking which account holds `output_vat`.
	Role string `json:"role,omitempty"`

	// Balance is debits less credits, in the company's currency. Signed, and
	// not flipped by type: a liability with a debit balance is a thing worth
	// seeing, and normalising the sign would hide it.
	Balance string `json:"balance"`
}

// Chart lists the accounts a company has.
func (s *JournalService) Chart(
	ctx context.Context, scope JournalScope, includeInactive bool,
) ([]Account, error) {
	out := []Account{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name,
			       coalesce(a.translations->>'ar', ''),
			       a.type, a.parent_id, a.is_postable, a.is_control,
			       coalesce(a.control_of, ''), coalesce(a.currency, ''),
			       a.is_active,
			       coalesce(m.role, ''),
			       coalesce((
			         SELECT sum(l.base_debit - l.base_credit)
			         FROM journal_line l WHERE l.account_id = a.id
			       ), 0)
			FROM account a
			LEFT JOIN account_role_map m
			       ON m.account_id = a.id AND m.company_id = a.company_id
			WHERE a.company_id = $1 AND ($2 OR a.is_active)
			ORDER BY a.code`, scope.CompanyID, includeInactive)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var a Account
			var balance decimal.Decimal
			if e := rows.Scan(&a.ID, &a.Code, &a.Name, &a.NameAr, &a.Type,
				&a.Parent, &a.IsPostable, &a.IsControl, &a.ControlOf,
				&a.Currency, &a.IsActive, &a.Role, &balance); e != nil {
				return e
			}
			a.Balance = balance.StringFixed(2)
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}
