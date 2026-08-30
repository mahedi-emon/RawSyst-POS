package assets

// Investors and their money (blueprint C3.2).
//
// # The one rule that matters
//
//	"Investment activity is kept fully separate from normal revenue in the
//	 accounting model — never mixed with sales income, so P&L stays clean."
//
// Money an owner puts into the business is not something the business earned.
// It is equity: it appears on the balance sheet, it never touches the profit
// and loss, and a product that allowed it to be recorded as income would
// flatter every figure a business is judged by — turnover, margin, growth — in
// a way that is very hard to unpick later.
//
// This module has no route that can post to a revenue account, and the two
// rules it uses name `owner_capital` on one side and a money account on the
// other. There is no path from here into the P&L at all.
//
// # Proportional share
//
// C3.2 asks for "each investor's proportional share". It is computed from what
// each has put in less what they have taken out, over the total across
// everybody — not stored, because a stored percentage is wrong the moment
// anybody contributes again and nobody remembers to recompute it.
//
// It is a share of CAPITAL CONTRIBUTED, and the screen says so. A share of
// ownership is a legal fact that lives in a shareholders' agreement, and a
// product that printed a percentage next to somebody's name without saying
// which of the two it meant would be inviting a serious misunderstanding.

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

// Investor is somebody with money in the business.
type Investor struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	NameAr string    `json:"name_ar,omitempty"`
	Kind   string    `json:"kind"`
	Email  string    `json:"email,omitempty"`
	Phone  string    `json:"phone,omitempty"`
	Note   string    `json:"note,omitempty"`
	Active bool      `json:"is_active"`

	Contributed string `json:"contributed"`
	Withdrawn   string `json:"withdrawn"`
	// Net is contributed less withdrawn: what this person currently has in.
	Net      string `json:"net"`
	Currency string `json:"currency"`

	// Share is this person's part of the total capital contributed, as a
	// percentage to two places. A share of CAPITAL, not of ownership — the
	// second is a legal fact that lives in an agreement, not in a database.
	Share string `json:"share_of_capital"`
}

// Movement is money in or out.
type Movement struct {
	ID        uuid.UUID `json:"id"`
	Direction string    `json:"direction"`
	Amount    string    `json:"amount"`
	MovedOn   string    `json:"moved_on"`
	Account   string    `json:"account,omitempty"`
	Reference string    `json:"reference,omitempty"`
	Note      string    `json:"note,omitempty"`
	Currency  string    `json:"currency"`
}

// NewInvestor is somebody being added.
type NewInvestor struct {
	Name   string
	NameAr string
	Kind   string
	Email  string
	Phone  string
	Note   string
	UserID *uuid.UUID
}

// NewMovement is money going in or coming out.
type NewMovement struct {
	// UUID is assigned by the caller, so a retry after a lost response does not
	// record the same injection twice.
	UUID uuid.UUID

	InvestorID     uuid.UUID
	Direction      string
	Amount         decimal.Decimal
	MovedOn        time.Time
	MoneyAccountID uuid.UUID
	Reference      string
	Note           string
}

// Investors lists who has money in, with each one's share.
func (s *Service) Investors(
	ctx context.Context, scope Scope, includeRetired bool,
) ([]Investor, error) {
	out := []Investor{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT i.id, i.name, coalesce(i.name_ar, ''), i.kind,
			       coalesce(i.email::text, ''), coalesce(i.phone, ''),
			       coalesce(i.note, ''), i.is_active, c.base_currency,
			       coalesce((SELECT sum(m.amount) FROM investment m
			                 WHERE m.investor_id = i.id
			                   AND m.direction = 'contribution'), 0),
			       coalesce((SELECT sum(m.amount) FROM investment m
			                 WHERE m.investor_id = i.id
			                   AND m.direction = 'withdrawal'), 0)
			FROM investor i
			JOIN company c ON c.id = i.company_id
			WHERE i.company_id = $1 AND ($2 OR i.is_active)
			ORDER BY i.kind, i.name`,
			scope.CompanyID, includeRetired)
		if err != nil {
			return err
		}
		defer rows.Close()

		type row struct {
			investor    Investor
			contributed decimal.Decimal
		}
		var rowsOut []row
		totalContributed := decimal.Zero

		for rows.Next() {
			var r row
			var contributed, withdrawn decimal.Decimal
			if err := rows.Scan(&r.investor.ID, &r.investor.Name,
				&r.investor.NameAr, &r.investor.Kind, &r.investor.Email,
				&r.investor.Phone, &r.investor.Note, &r.investor.Active,
				&r.investor.Currency, &contributed, &withdrawn); err != nil {
				return err
			}
			r.contributed = contributed
			r.investor.Contributed = contributed.StringFixed(2)
			r.investor.Withdrawn = withdrawn.StringFixed(2)
			r.investor.Net = contributed.Sub(withdrawn).StringFixed(2)
			totalContributed = totalContributed.Add(contributed)
			rowsOut = append(rowsOut, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// The share, computed here rather than stored. A stored percentage is
		// wrong the moment anybody contributes again, and nobody remembers to
		// recompute it.
		hundred := decimal.NewFromInt(100)
		for _, r := range rowsOut {
			inv := r.investor
			if totalContributed.IsPositive() {
				inv.Share = r.contributed.Div(totalContributed).
					Mul(hundred).Round(2).StringFixed(2)
			} else {
				inv.Share = "0.00"
			}
			out = append(out, inv)
		}
		return nil
	})
	return out, err
}

// AddInvestor puts somebody in the register.
func (s *Service) AddInvestor(
	ctx context.Context, scope Scope, in NewInvestor,
) (Investor, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Investor{}, errs.Validation("Give the investor a name.").
			WithField("name", "The person or company putting money in.")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "investor"
	}
	if kind != "owner" && kind != "investor" {
		return Investor{}, errs.New(errs.CodeInvalidInput,
			"An investor is either an owner or an outside investor.")
	}

	var out Investor
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO investor
			  (tenant_id, company_id, name, name_ar, kind, email, phone, note, user_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			scope.TenantID, scope.CompanyID, name, nullIfBlank(in.NameAr),
			kind, nullIfBlank(in.Email), nullIfBlank(in.Phone),
			nullIfBlank(in.Note), in.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That investor could not be added.")
		}
		out = Investor{
			ID: id, Name: name, NameAr: in.NameAr, Kind: kind,
			Email: in.Email, Phone: in.Phone, Note: in.Note, Active: true,
			Contributed: "0.00", Withdrawn: "0.00", Net: "0.00", Share: "0.00",
		}
		return readCurrency(ctx, tx, scope.CompanyID, &out.Currency)
	})
	return out, err
}

// Record puts money in or takes it out, and posts it to equity.
func (s *Service) Record(
	ctx context.Context, scope Scope, in NewMovement,
) (Movement, error) {
	if in.UUID == uuid.Nil {
		return Movement{}, errs.New(errs.CodeInvalidInput,
			"An investment must carry an identifier so a retry does not record "+
				"it twice.")
	}
	if !in.Amount.IsPositive() {
		return Movement{}, errs.New(errs.CodeInvalidInput,
			"Say how much is moving.")
	}
	if in.Direction != "contribution" && in.Direction != "withdrawal" {
		return Movement{}, errs.New(errs.CodeInvalidInput,
			"Money is either being put in or taken out.")
	}

	var out Movement
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM investment WHERE id = $1 AND company_id = $2)`,
			in.UUID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			read, e := s.readMovement(ctx, tx, scope, in.UUID)
			out = read
			return e
		}

		var investorName string
		e := tx.QueryRow(ctx, `
			SELECT name FROM investor
			WHERE id = $1 AND company_id = $2 AND is_active`,
			in.InvestorID, scope.CompanyID).Scan(&investorName)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That investor is not one this business has on record.")
		}
		if e != nil {
			return e
		}

		ledgerAccount, e := ledgerAccountOf(ctx, tx, scope, in.MoneyAccountID)
		if e != nil {
			return e
		}

		movedOn := in.MovedOn
		if movedOn.IsZero() {
			movedOn = time.Now().UTC()
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// Rule 12 for money in, its mirror for money out. Neither touches a
		// revenue or expense account, which is C3.2's whole requirement: the
		// P&L never sees any of this.
		ruleKey, group := "equity.contribution", "destination"
		if in.Direction == "withdrawal" {
			ruleKey, group = "equity.withdrawal", "source"
		}

		result, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: movedOn, SourceType: "investment", SourceID: in.UUID,
			RuleKey: ruleKey, PostedBy: &scope.UserID,
			Memo: movementMemo(in.Direction, investorName),
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{"amount": in.Amount},
			Groups: map[string]accounting.Group{
				group: {{
					AccountID: &ledgerAccount, Amount: in.Amount,
					Memo: investorName,
				}},
			},
		})
		if e != nil {
			return e
		}

		// Written once, complete. The immutability trigger refuses an update
		// that attaches the entry afterwards — the same lesson the stock
		// voucher and the money transfer both had to learn.
		if _, e := tx.Exec(ctx, `
			INSERT INTO investment
			  (id, tenant_id, company_id, investor_id, direction, amount,
			   moved_on, money_account_id, reference, note,
			   journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			in.UUID, scope.TenantID, scope.CompanyID, in.InvestorID,
			in.Direction, in.Amount, movedOn, in.MoneyAccountID,
			nullIfBlank(in.Reference), nullIfBlank(in.Note),
			result.EntryID, scope.UserID); e != nil {
			return db.Translate(e, "That investment could not be recorded.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "investment_recorded",
			EntityType: "investor", EntityID: &in.InvestorID,
			After: map[string]any{
				"investor": investorName, "direction": in.Direction,
				"amount": in.Amount.StringFixed(2),
			},
		}); e != nil {
			return e
		}

		read, e := s.readMovement(ctx, tx, scope, in.UUID)
		out = read
		return e
	})
	return out, err
}

// Statement is one investor's history. C3.2 asks for it by name, and for it to
// be readable by that investor and nobody else — which the route enforces.
func (s *Service) Statement(
	ctx context.Context, scope Scope, investorID uuid.UUID,
) ([]Movement, error) {
	out := []Movement{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.direction, m.amount,
			       to_char(m.moved_on, 'YYYY-MM-DD'),
			       coalesce(a.name, ''), coalesce(m.reference, ''),
			       coalesce(m.note, ''), c.base_currency
			FROM investment m
			JOIN company c ON c.id = m.company_id
			LEFT JOIN money_account a ON a.id = m.money_account_id
			WHERE m.company_id = $1 AND m.investor_id = $2
			ORDER BY m.moved_on DESC, m.created_at DESC`,
			scope.CompanyID, investorID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m Movement
			var amount decimal.Decimal
			if err := rows.Scan(&m.ID, &m.Direction, &amount, &m.MovedOn,
				&m.Account, &m.Reference, &m.Note, &m.Currency); err != nil {
				return err
			}
			m.Amount = amount.StringFixed(2)
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) readMovement(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Movement, error) {
	var m Movement
	var amount decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT m.id, m.direction, m.amount,
		       to_char(m.moved_on, 'YYYY-MM-DD'),
		       coalesce(a.name, ''), coalesce(m.reference, ''),
		       coalesce(m.note, ''), c.base_currency
		FROM investment m
		JOIN company c ON c.id = m.company_id
		LEFT JOIN money_account a ON a.id = m.money_account_id
		WHERE m.id = $1 AND m.company_id = $2`,
		id, scope.CompanyID).
		Scan(&m.ID, &m.Direction, &amount, &m.MovedOn, &m.Account,
			&m.Reference, &m.Note, &m.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Movement{}, errs.New(errs.CodeNotFound,
			"That investment is not this business's.")
	}
	m.Amount = amount.StringFixed(2)
	return m, err
}

func movementMemo(direction, investor string) string {
	if direction == "withdrawal" {
		return "Withdrawal by " + investor
	}
	return "Capital from " + investor
}

// MayReadStatement enforces C3.2's confinement.
//
//	"each investor can (if given access) see only their own contribution/return
//	 history."
//
// Two kinds of reader reach this. Somebody running the business — an Owner, an
// Accountant, an Auditor — may read any investor's statement, because that is
// what managing the capital of a business involves. An investor who has been
// given a login may read exactly one: their own.
//
// The distinction is made on `investor.manage` rather than on a role name. A
// person who may CHANGE the register is by definition running it; a person who
// may only view is either staff or an investor, and the check below separates
// those two by asking whether the statement belongs to them.
func (s *Service) MayReadStatement(
	ctx context.Context, scope Scope, investorID uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var linked *uuid.UUID
		e := tx.QueryRow(ctx,
			`SELECT user_id FROM investor WHERE id = $1 AND company_id = $2`,
			investorID, scope.CompanyID).Scan(&linked)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That investor is not one this business has on record.")
		}
		if e != nil {
			return e
		}

		// Not linked to anybody's login, so nobody can be reading "their own".
		// Staff reach this through the route's permission; an investor with no
		// login cannot reach it at all.
		if linked == nil {
			return nil
		}

		// Linked to somebody else's login. Whether the caller may read it
		// depends on whether they run the register, which the caller's own
		// grants answer — checked here rather than in the handler so the rule
		// travels with the data it protects.
		if *linked == scope.UserID {
			return nil
		}

		var mayManage bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM user_role_assignment ura
			  JOIN role_permission rp ON rp.role_id = ura.role_id
			  WHERE ura.user_id = $1 AND rp.permission = 'investor.manage')`,
			scope.UserID).Scan(&mayManage); e != nil {
			return e
		}
		if !mayManage {
			return errs.New(errs.CodeForbidden,
				"You can see your own investment history, and nobody else's.")
		}
		return nil
	})
}
