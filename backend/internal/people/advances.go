// Salary advances and end-of-service accrual (blueprint C5, E6).
package people

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/market"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Advance is money lent against future wages.
type Advance struct {
	ID           uuid.UUID `json:"id"`
	Number       string    `json:"advance_no"`
	EmployeeID   uuid.UUID `json:"employee_id"`
	Employee     string    `json:"employee,omitempty"`
	Amount       string    `json:"amount"`
	Outstanding  string    `json:"outstanding"`
	Installments int       `json:"installments"`
	Currency     string    `json:"currency"`
	IssuedOn     string    `json:"issued_on"`
	Reason       string    `json:"reason,omitempty"`
}

// IssueAdvance lends against future wages and posts it.
//
// An advance is a LOAN, not a cost: the money leaves and the employee owes it
// back. Booking it as a wage expense would charge the month twice — once now
// and again when the payroll it is recovered from runs.
func (s *Service) IssueAdvance(
	ctx context.Context, scope Scope, employeeID, accountID uuid.UUID,
	amount decimal.Decimal, installments int, reason string,
) (Advance, error) {
	if !amount.IsPositive() {
		return Advance{}, errs.New(errs.CodeInvalidInput,
			"Say how much is being advanced.")
	}
	if installments <= 0 {
		installments = 1
	}

	var out Advance
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		var ok bool
		e := tx.QueryRow(ctx,
			`SELECT true FROM employee
			 WHERE id = $1 AND company_id = $2 AND status <> 'left'`,
			employeeID, scope.CompanyID).Scan(&ok)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That employee was not found, or has left.")
		}
		if e != nil {
			return e
		}

		role, e := accountRoleOf(ctx, tx, scope.CompanyID, accountID)
		if e != nil {
			return e
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "advance", "ADV")
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO salary_advance
			  (tenant_id, company_id, employee_id, advance_no, amount,
			   currency, installments, reason, money_account_id, approved_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
			scope.TenantID, scope.CompanyID, employeeID, number, amount,
			currency, installments, nullText(reason), accountID,
			scope.UserID).Scan(&id); e != nil {
			return e
		}

		entry, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       time.Now().UTC(),
			SourceType: "salary_advance", SourceID: id,
			RuleKey:      "payroll.advance",
			Currency:     currency,
			BaseCurrency: currency,
			FXRate:       decimal.NewFromInt(1),
			Memo:         "Salary advance " + number,
			PostedBy:     &scope.UserID,
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{"amount": amount},
			Groups: map[string]accounting.Group{
				"payment_account": {{Role: role, Amount: amount}},
			},
		})
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx,
			`UPDATE salary_advance SET journal_entry_id = $2 WHERE id = $1`,
			id, entry.EntryID); e != nil {
			return e
		}

		read, e := s.readAdvance(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Advances lists loans, open ones by default.
func (s *Service) Advances(
	ctx context.Context, scope Scope, includeSettled bool,
) ([]Advance, error) {
	out := []Advance{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, advanceSelect+`
			WHERE a.company_id = $1
			  AND ($2 OR advance_outstanding(a.id) > 0)
			ORDER BY a.issued_on DESC LIMIT 500`,
			scope.CompanyID, includeSettled)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			adv, e := scanAdvance(rows)
			if e != nil {
				return e
			}
			out = append(out, adv)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

const advanceSelect = `
	SELECT a.id, a.advance_no, a.employee_id, e.full_name, a.amount,
	       advance_outstanding(a.id), a.installments, a.currency,
	       a.issued_on, coalesce(a.reason, '')
	FROM salary_advance a
	JOIN employee e ON e.id = a.employee_id`

func (s *Service) readAdvance(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Advance, error) {
	row := tx.QueryRow(ctx, advanceSelect+`
		WHERE a.id = $1 AND a.company_id = $2`, id, companyID)
	out, err := scanAdvance(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Advance{}, errs.New(errs.CodeNotFound,
			"That advance was not found.")
	}
	return out, err
}

func scanAdvance(row scanner) (Advance, error) {
	var a Advance
	var amount, outstanding decimal.Decimal
	var issued time.Time
	if err := row.Scan(&a.ID, &a.Number, &a.EmployeeID, &a.Employee, &amount,
		&outstanding, &a.Installments, &a.Currency, &issued,
		&a.Reason); err != nil {
		return Advance{}, err
	}
	a.Amount = amount.StringFixed(2)
	a.Outstanding = outstanding.StringFixed(2)
	a.IssuedOn = issued.Format("2006-01-02")
	return a, nil
}

// --- End of service -------------------------------------------------------

// EOSBPosition is what the business owes one person if they left today.
type EOSBPosition struct {
	EmployeeID uuid.UUID `json:"employee_id"`
	Employee   string    `json:"employee"`
	Months     string    `json:"months_of_service"`
	Accrued    string    `json:"accrued"`
	Currency   string    `json:"currency"`
}

// AccrueEOSB charges one month's end-of-service benefit for everybody.
//
// Design 20 fixes this as MONTHLY: "eosb_accrual — monthly, not discovered at
// termination". A business that only computes the benefit when somebody
// resigns carries an unrecorded liability that grows for years and learns its
// size on the day it has to pay.
//
// The ACCRUAL RATE is Saudi labour law and belongs to the registry like every
// other legal value. `SA.EOSB.ENTITLEMENT` does not exist there yet, so this
// refuses rather than inventing a rate — the same discipline as GOSI. What DOES
// work today is everything around it: the schedule, the per-person service
// calculation, the posting, and the one-charge-per-month guarantee.
func (s *Service) AccrueEOSB(
	ctx context.Context, scope Scope, period time.Time,
) (int, error) {
	period = firstOfMonth(period)

	var charged int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var country, currency string
		if e := tx.QueryRow(ctx,
			`SELECT country, base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&country, &currency); e != nil {
			return e
		}

		days, err := s.eosbDaysPerYear(ctx, tx, scope.TenantID, country, period)
		if err != nil {
			return err
		}

		rows, e := tx.Query(ctx, `
			SELECT e.id, e.joined_on,
			       e.basic_salary + e.housing_allowance
			FROM employee e
			WHERE e.company_id = $1 AND e.status <> 'left'
			  AND e.joined_on < $2
			  AND NOT EXISTS (
			    SELECT 1 FROM eosb_accrual a
			    WHERE a.employee_id = e.id AND a.period = $3 AND a.amount > 0)
			ORDER BY e.full_name`,
			scope.CompanyID, period.AddDate(0, 1, 0), period)
		if e != nil {
			return e
		}
		type person struct {
			id     uuid.UUID
			joined time.Time
			wage   decimal.Decimal
		}
		var staff []person
		for rows.Next() {
			var p person
			if e := rows.Scan(&p.id, &p.joined, &p.wage); e != nil {
				rows.Close()
				return e
			}
			staff = append(staff, p)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, p := range staff {
			months := monthsBetween(p.joined, period)
			if !months.IsPositive() {
				continue
			}
			// One month's share of a year's entitlement, on the wage the
			// person is earning now.
			perDay := p.wage.Div(decimal.NewFromInt(30))
			amount := perDay.Mul(days).
				Div(decimal.NewFromInt(12)).Round(2)
			if !amount.IsPositive() {
				continue
			}

			var accrualID uuid.UUID
			if e := tx.QueryRow(ctx, `
				INSERT INTO eosb_accrual
				  (tenant_id, company_id, employee_id, period, amount,
				   wage_basis, months_of_service)
				VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
				scope.TenantID, scope.CompanyID, p.id, period, amount,
				p.wage, months).Scan(&accrualID); e != nil {
				return e
			}

			entry, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       period.AddDate(0, 1, -1),
				SourceType: "eosb_accrual", SourceID: accrualID,
				RuleKey:      "payroll.eosb_accrue",
				Currency:     currency,
				BaseCurrency: currency,
				FXRate:       decimal.NewFromInt(1),
				Memo:         "End-of-service accrual",
				PostedBy:     &scope.UserID,
			}, country, accounting.Transaction{
				Amounts: accounting.Amounts{"amount": amount},
			})
			if e != nil {
				return e
			}

			if _, e := tx.Exec(ctx,
				`UPDATE eosb_accrual SET journal_entry_id = $2 WHERE id = $1`,
				accrualID, entry.EntryID); e != nil {
				return e
			}
			charged++
		}
		return nil
	})
	return charged, db.Translate(err, "")
}

// eosbDaysPerYear resolves the statutory entitlement.
//
// E6 puts end-of-service under Saudi labour law, and E8 requires every legal
// parameter to be versioned data with an effective date rather than a number in
// code. The rule key is resolved here so that the day somebody records the
// verified entitlement, this starts working with no code change — and until
// then it refuses rather than guessing, because an accrual at the wrong rate
// understates a liability for years before anybody notices.
func (s *Service) eosbDaysPerYear(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, country string,
	period time.Time,
) (decimal.Decimal, error) {
	if s.rules == nil {
		return decimal.Zero, errs.New(errs.CodeInternal,
			"The payroll service was built without the regulatory rule registry.")
	}

	// Asked of the market first. EOSB is Saudi labour law; resolving it for a
	// company in another market found no rule and failed the accrual for a
	// benefit that market may not have — or may have on entirely different
	// terms. Declining to compute one is the honest answer, and applying Saudi
	// service bands to a foreign contract would be inventing a rule.
	if !market.EndOfServiceApplies(country) {
		return decimal.Zero, errs.Newf(errs.CodeUnverifiedRule,
			"This product has no end-of-service entitlement rule for %s, so "+
				"the benefit cannot be accrued here. The Saudi rule does not "+
				"apply outside the Kingdom.",
			strings.ToUpper(strings.TrimSpace(country)))
	}

	return s.rules.Decimal(ctx, registry.Query{
		Key:      "SA.EOSB.ENTITLEMENT",
		Country:  country,
		AsOf:     period,
		TenantID: tenantID,
		Tx:       tx,
	}, "days_per_year_first_five")
}

// EOSBPositions is what the business owes everybody today.
func (s *Service) EOSBPositions(
	ctx context.Context, scope Scope,
) ([]EOSBPosition, error) {
	out := []EOSBPosition{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT e.id, e.full_name, e.currency,
			       coalesce(sum(a.amount), 0),
			       coalesce(max(a.months_of_service), 0)
			FROM employee e
			LEFT JOIN eosb_accrual a ON a.employee_id = e.id
			WHERE e.company_id = $1 AND e.status <> 'left'
			GROUP BY e.id, e.full_name, e.currency
			ORDER BY e.full_name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p EOSBPosition
			var accrued, months decimal.Decimal
			if e := rows.Scan(&p.EmployeeID, &p.Employee, &p.Currency,
				&accrued, &months); e != nil {
				return e
			}
			p.Accrued = accrued.StringFixed(2)
			p.Months = months.String()
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// monthsBetween is completed months of service at a period.
func monthsBetween(from, to time.Time) decimal.Decimal {
	months := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if months < 0 {
		months = 0
	}
	return decimal.NewFromInt(int64(months))
}

// --- Commission rules -----------------------------------------------------

// CommissionRule is one scheme.
type CommissionRule struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	IsActive   bool       `json:"is_active"`
	Basis      string     `json:"basis"`
	EmployeeID *uuid.UUID `json:"employee_id,omitempty"`
	StoreID    *uuid.UUID `json:"store_id,omitempty"`
	Rate       string     `json:"rate"`
	Tiers      string     `json:"tiers"`
	From       string     `json:"effective_from"`
	To         string     `json:"effective_to,omitempty"`
}

// SetCommissionRule creates a scheme.
func (s *Service) SetCommissionRule(
	ctx context.Context, scope Scope, name, basis string,
	employeeID, storeID *uuid.UUID, rate decimal.Decimal, tiers string,
	from time.Time,
) (CommissionRule, error) {
	if strings.TrimSpace(name) == "" {
		return CommissionRule{}, errs.Validation("Give the scheme a name.").
			WithField("name", "So a payslip can say which one paid.")
	}
	if basis == "" {
		basis = "revenue"
	}
	if basis != "revenue" && basis != "profit" {
		return CommissionRule{}, errs.New(errs.CodeInvalidInput,
			"Commission is measured on revenue or on profit.")
	}
	if rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1)) {
		return CommissionRule{}, errs.Validation(
			"A commission rate is a fraction between nothing and everything.").
			WithField("rate", "0.02 is two per cent.")
	}
	if strings.TrimSpace(tiers) == "" {
		tiers = "[]"
	}

	var out CommissionRule
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO commission_rule
			  (tenant_id, company_id, name, basis, employee_id, store_id,
			   rate, tiers, effective_from, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10) RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(name), basis,
			employeeID, storeID, rate, tiers, from, scope.UserID).
			Scan(&id); e != nil {
			return db.Translate(e, "That commission scheme could not be saved.")
		}
		read, e := s.readCommissionRule(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// CommissionRules lists schemes.
func (s *Service) CommissionRules(
	ctx context.Context, scope Scope,
) ([]CommissionRule, error) {
	out := []CommissionRule{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, commissionSelect+`
			WHERE c.company_id = $1 ORDER BY c.name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanCommission(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

const commissionSelect = `
	SELECT c.id, c.name, c.is_active, c.basis, c.employee_id, c.store_id,
	       c.rate, c.tiers::text, c.effective_from, c.effective_to
	FROM commission_rule c`

func (s *Service) readCommissionRule(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (CommissionRule, error) {
	row := tx.QueryRow(ctx, commissionSelect+`
		WHERE c.id = $1 AND c.company_id = $2`, id, companyID)
	out, err := scanCommission(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommissionRule{}, errs.New(errs.CodeNotFound,
			"That commission scheme was not found.")
	}
	return out, err
}

func scanCommission(row scanner) (CommissionRule, error) {
	var c CommissionRule
	var rate decimal.Decimal
	var from time.Time
	var to *time.Time
	if err := row.Scan(&c.ID, &c.Name, &c.IsActive, &c.Basis, &c.EmployeeID,
		&c.StoreID, &rate, &c.Tiers, &from, &to); err != nil {
		return CommissionRule{}, err
	}
	c.Rate = rate.String()
	c.From = from.Format("2006-01-02")
	if to != nil {
		c.To = to.Format("2006-01-02")
	}
	return c, nil
}
