// Payroll, GOSI, commission, end-of-service and the WPS wage file
// (blueprint C6, E6).
package people

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// gosiRates is the shape of SA.GOSI.RATES in the registry.
//
// Mirrors the payload migration 0005 seeded, field for field. The values are
// `__VERIFY__` until somebody checks them against the Social Insurance Law and
// stamps the rule — see the package comment on why this module refuses rather
// than guesses.
type gosiRates struct {
	WageCap    string `json:"wage_cap"`
	Expatriate struct {
		Employee string `json:"employee"`
		Employer string `json:"employer"`
	} `json:"expatriate"`
	SaudiPreJul2024 struct {
		Employee string `json:"employee"`
		Employer string `json:"employer"`
	} `json:"saudi_pre_jul2024"`
	SaudiPostJul2024 struct {
		Employee string `json:"employee"`
		Employer string `json:"employer"`
	} `json:"saudi_post_jul2024"`
}

// contribution is one person's social insurance for one period.
type contribution struct {
	employee decimal.Decimal
	employer decimal.Decimal
}

// resolveGOSI computes an employee's contributions for a pay period.
//
// Three things decide the rate and all three are data: whether the employee is
// a Saudi national, when they were hired relative to July 2024, and the period
// being paid. Design 20 fixes the last one — "re-running an old month must
// produce the historically correct figure" — so the rule is resolved at the
// period, never at today.
//
// A `__VERIFY__` value raises errs.CodeUnverifiedRule naming the key, which is
// what a payroll clerk needs to know: the run is not broken, the rate has not
// been confirmed yet.
func (s *Service) resolveGOSI(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, country string,
	period time.Time, emp employeeForPay,
) (contribution, error) {
	if s.rules == nil {
		return contribution{}, errs.New(errs.CodeInternal,
			"The payroll service was built without the regulatory rule registry.")
	}

	q := registry.Query{
		Key:      "SA.GOSI.RATES",
		Country:  country,
		AsOf:     period,
		TenantID: tenantID,
		Tx:       tx,
	}

	var rates gosiRates
	if err := s.rules.Into(ctx, q, &rates); err != nil {
		return contribution{}, err
	}

	empRate, erRate := rates.Expatriate.Employee, rates.Expatriate.Employer
	if emp.isSaudi {
		// The Social Insurance Law changed for Saudi nationals hired from
		// July 2024. Which side of that line somebody joined on is a property
		// of their hiring date, not of today.
		if emp.joinedOn.Before(time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)) {
			empRate, erRate = rates.SaudiPreJul2024.Employee,
				rates.SaudiPreJul2024.Employer
		} else {
			empRate, erRate = rates.SaudiPostJul2024.Employee,
				rates.SaudiPostJul2024.Employer
		}
	}

	for _, v := range []struct{ raw, what string }{
		{empRate, "the employee share"},
		{erRate, "the employer share"},
		{rates.WageCap, "the wage ceiling"},
	} {
		if strings.TrimSpace(v.raw) == registry.Placeholder {
			return contribution{}, errs.Newf(errs.CodeUnverifiedRule,
				"%q has not been verified against its official source, so %s "+
					"cannot be calculated. A Super Admin must record the "+
					"verified value in Super Admin > Regulatory Registry "+
					"before payroll can include social insurance.",
				q.Key, v.what)
		}
	}

	employeeRate, err := decimal.NewFromString(empRate)
	if err != nil {
		return contribution{}, errs.Newf(errs.CodeInternal,
			"The employee share in %q is not a number.", q.Key)
	}
	employerRate, err := decimal.NewFromString(erRate)
	if err != nil {
		return contribution{}, errs.Newf(errs.CodeInternal,
			"The employer share in %q is not a number.", q.Key)
	}

	// The contributory wage is capped. Applying a rate to an uncapped salary
	// overstates what a senior employee costs and what is withheld from them.
	base := emp.contributoryWage
	if cap, e := decimal.NewFromString(rates.WageCap); e == nil && cap.IsPositive() {
		if base.GreaterThan(cap) {
			base = cap
		}
	}

	return contribution{
		employee: base.Mul(employeeRate).Round(2),
		employer: base.Mul(employerRate).Round(2),
	}, nil
}

// employeeForPay is what the run needs to know about one person.
type employeeForPay struct {
	id       uuid.UUID
	name     string
	iban     string
	isSaudi  bool
	joinedOn time.Time

	basic     decimal.Decimal
	housing   decimal.Decimal
	transport decimal.Decimal
	other     decimal.Decimal

	commissionEligible bool

	// contributoryWage is what GOSI is charged on. Basic plus housing is the
	// usual Saudi basis; it is computed here rather than stored so a change to
	// either component flows through without a second field to keep in step.
	contributoryWage decimal.Decimal
}

// Run is a payroll run as read back.
type Run struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"run_no"`
	Period   string    `json:"period"`
	PayDate  string    `json:"pay_date,omitempty"`
	Status   string    `json:"status"`
	Currency string    `json:"currency"`

	Gross        string `json:"gross_total"`
	Deductions   string `json:"deduction_total"`
	Net          string `json:"net_total"`
	EmployerGOSI string `json:"employer_gosi"`

	// GOSIUnavailable says the run computed everything except social
	// insurance, because the rate is not verified yet. The run is still
	// usable — wages are wages — and the figure that is missing is named
	// rather than silently zero.
	GOSIUnavailable bool   `json:"gosi_unavailable,omitempty"`
	GOSIBlockedWhy  string `json:"gosi_blocked_reason,omitempty"`

	Note     string    `json:"note,omitempty"`
	Payslips []Payslip `json:"payslips,omitempty"`
}

// Payslip is one person's pay for one period.
type Payslip struct {
	ID         uuid.UUID `json:"id"`
	EmployeeID uuid.UUID `json:"employee_id"`
	Employee   string    `json:"employee"`

	Basic          string `json:"basic"`
	Housing        string `json:"housing"`
	Transport      string `json:"transport"`
	OtherAllowance string `json:"other_allowance"`
	Overtime       string `json:"overtime"`
	Commission     string `json:"commission"`
	Bonus          string `json:"bonus"`
	Gross          string `json:"gross"`

	AbsenceDeduction string `json:"absence_deduction"`
	GOSIEmployee     string `json:"gosi_employee"`
	AdvanceRecovery  string `json:"advance_recovery"`
	OtherDeduction   string `json:"other_deduction"`
	Deductions       string `json:"deductions"`

	Net          string `json:"net"`
	GOSIEmployer string `json:"gosi_employer"`
}

// Prepare computes a draft payroll run for a month.
//
// Nothing posts. A draft is a calculation somebody checks before the money
// moves, and C6's approval step exists precisely so the figures are reviewed
// while they can still be corrected.
func (s *Service) Prepare(
	ctx context.Context, scope Scope, period time.Time, note string,
) (Run, error) {
	period = firstOfMonth(period)

	var out Run
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var country, currency string
		if e := tx.QueryRow(ctx,
			`SELECT country, base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&country, &currency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		// A month already run is not silently recomputed: the first run may
		// have been approved and paid, and a second would pay everybody twice.
		var existing string
		e := tx.QueryRow(ctx, `
			SELECT status FROM payroll_run
			WHERE company_id = $1 AND period = $2 AND status <> 'cancelled'`,
			scope.CompanyID, period).Scan(&existing)
		if e == nil {
			return errs.Newf(errs.CodeConflict,
				"There is already a %s payroll run for %s.",
				existing, period.Format("January 2006"))
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		staff, e := staffForPeriod(ctx, tx, scope.CompanyID, period)
		if e != nil {
			return e
		}
		if len(staff) == 0 {
			return errs.New(errs.CodeInvalidInput,
				"Nobody was employed in that month.")
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "payroll", "PAY")
		if e != nil {
			return e
		}

		var runID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO payroll_run
			  (tenant_id, company_id, run_no, period, status, currency, note,
			   created_by)
			VALUES ($1,$2,$3,$4,'draft',$5,$6,$7) RETURNING id`,
			scope.TenantID, scope.CompanyID, number, period, currency,
			nullText(note), scope.UserID).Scan(&runID); e != nil {
			return e
		}

		gross, deductions, net := decimal.Zero, decimal.Zero, decimal.Zero
		employerGOSI := decimal.Zero

		// Social insurance is resolved ONCE per run rather than per employee:
		// the rule is the same for everybody in the period, and a run of two
		// hundred people should not report the same unverified-rule refusal
		// two hundred times.
		var gosiBlocked string

		for _, emp := range staff {
			slip, e := s.computeSlip(ctx, tx, scope, country, period, emp,
				&gosiBlocked)
			if e != nil {
				return e
			}

			var slipID uuid.UUID
			if e := tx.QueryRow(ctx, `
				INSERT INTO payslip
				  (tenant_id, run_id, employee_id, basic, housing, transport,
				   other_allowance, overtime, commission, bonus, gross,
				   absence_deduction, gosi_employee, advance_recovery,
				   other_deduction, deductions, net, gosi_employer)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
				        $16,$17,$18)
				RETURNING id`,
				scope.TenantID, runID, emp.id, slip.basic, slip.housing,
				slip.transport, slip.other, slip.overtime, slip.commission,
				slip.bonus, slip.gross, slip.absence, slip.gosiEmployee,
				slip.advanceRecovery, slip.otherDeduction, slip.deductions,
				slip.net, slip.gosiEmployer,
			).Scan(&slipID); e != nil {
				return e
			}

			// Which advance each recovery came off, so an employee can be
			// shown why their pay was short and the advance balance is a sum
			// rather than a stored figure.
			for _, r := range slip.recoveries {
				if _, e := tx.Exec(ctx, `
					INSERT INTO advance_recovery
					  (tenant_id, advance_id, payslip_id, amount)
					VALUES ($1,$2,$3,$4)`,
					scope.TenantID, r.advanceID, slipID, r.amount); e != nil {
					return e
				}
			}

			gross = gross.Add(slip.gross)
			deductions = deductions.Add(slip.deductions)
			net = net.Add(slip.net)
			employerGOSI = employerGOSI.Add(slip.gosiEmployer)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE payroll_run SET gross_total = $2, deduction_total = $3,
			  net_total = $4, employer_gosi = $5
			WHERE id = $1`, runID, gross, deductions, net, employerGOSI); e != nil {
			return e
		}

		read, e := s.readRun(ctx, tx, scope.CompanyID, runID)
		if e != nil {
			return e
		}
		if gosiBlocked != "" {
			read.GOSIUnavailable = true
			read.GOSIBlockedWhy = gosiBlocked
		}
		out = read
		return nil
	})
	return out, db.Translate(err, "")
}

// computedSlip is one payslip before it is written.
type computedSlip struct {
	basic, housing, transport, other       decimal.Decimal
	overtime, commission, bonus, gross     decimal.Decimal
	absence, gosiEmployee, advanceRecovery decimal.Decimal
	otherDeduction, deductions, net        decimal.Decimal
	gosiEmployer                           decimal.Decimal
	gosiVersion                            *int
	recoveries                             []recovery
}

type recovery struct {
	advanceID uuid.UUID
	amount    decimal.Decimal
}

func (s *Service) computeSlip(
	ctx context.Context, tx pgx.Tx, scope Scope, country string,
	period time.Time, emp employeeForPay, gosiBlocked *string,
) (computedSlip, error) {
	var c computedSlip
	c.basic, c.housing = emp.basic, emp.housing
	c.transport, c.other = emp.transport, emp.other

	// Overtime and absence both come from attendance, which approved leave
	// already wrote into — so there is one source rather than two that can
	// disagree about whether somebody was there.
	days, hours, err := attendanceFor(ctx, tx, emp.id, period)
	if err != nil {
		return c, err
	}

	monthly := emp.basic.Add(emp.housing).Add(emp.transport).Add(emp.other)
	workingDays := decimal.NewFromInt(int64(daysIn(period)))
	if days.absent > 0 && workingDays.IsPositive() {
		perDay := monthly.Div(workingDays).Round(4)
		c.absence = perDay.Mul(decimal.NewFromInt(days.absent)).Round(2)
	}
	if hours.IsPositive() && workingDays.IsPositive() {
		// An hour of overtime is worth an hour of pay. A statutory multiplier
		// is a legal value and would belong in the registry; none is applied
		// here rather than inventing one.
		perHour := monthly.Div(workingDays).Div(decimal.NewFromInt(8)).Round(4)
		c.overtime = perHour.Mul(hours).Round(2)
	}

	if emp.commissionEligible {
		c.commission, err = s.commissionFor(ctx, tx, scope, emp.id, period)
		if err != nil {
			return c, err
		}
	}

	c.gross = c.basic.Add(c.housing).Add(c.transport).Add(c.other).
		Add(c.overtime).Add(c.commission).Add(c.bonus)

	// Social insurance. A rate that has not been verified stops THIS figure
	// and nothing else: the wage is still owed and still posts, and the run
	// says plainly which number is missing.
	//
	// Asked of the MARKET first. GOSI is Saudi Arabia's scheme; resolving
	// `SA.GOSI.RATES` for a Bangladeshi company found no rule and the
	// registry's "no regulatory rule is on record" error came back through
	// `default:` below and killed the whole run — so a Bangladeshi shop could
	// not pay anybody at all. A market with no scheme this product knows simply
	// has no social-insurance line, which is different from having one of zero
	// and very different from being unable to run payroll.
	if market.SocialInsuranceApplies(country) {
		con, err := s.resolveGOSI(ctx, tx, scope.TenantID, country, period, emp)
		switch {
		case err == nil:
			c.gosiEmployee, c.gosiEmployer = con.employee, con.employer
		case errs.CodeOf(err) == errs.CodeUnverifiedRule:
			if *gosiBlocked == "" {
				*gosiBlocked = err.Error()
			}
		default:
			return c, err
		}
	}

	// Advances, oldest first. C5: "issue an advance and have it automatically
	// deducted from the next payroll run".
	rec, total, err := advancesToRecover(ctx, tx, emp.id,
		c.gross.Sub(c.absence).Sub(c.gosiEmployee))
	if err != nil {
		return c, err
	}
	c.recoveries, c.advanceRecovery = rec, total

	c.deductions = c.absence.Add(c.gosiEmployee).
		Add(c.advanceRecovery).Add(c.otherDeduction)
	c.net = c.gross.Sub(c.deductions)
	return c, nil
}

// attendanceDays is what the month's attendance adds up to.
type attendanceDays struct{ absent int64 }

func attendanceFor(
	ctx context.Context, tx pgx.Tx, employeeID uuid.UUID, period time.Time,
) (attendanceDays, decimal.Decimal, error) {
	var d attendanceDays
	var overtime decimal.Decimal
	end := period.AddDate(0, 1, 0)

	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'absent'),
		       coalesce(sum(overtime_hours), 0)
		FROM attendance
		WHERE employee_id = $1 AND on_date >= $2 AND on_date < $3`,
		employeeID, period, end).Scan(&d.absent, &overtime)
	return d, overtime, err
}

// advancesToRecover decides what comes off this payslip.
//
// Capped at what is actually being paid: a recovery that took more than the
// net would hand the employee a negative payslip, which is not a deduction but
// a demand.
func advancesToRecover(
	ctx context.Context, tx pgx.Tx, employeeID uuid.UUID, available decimal.Decimal,
) ([]recovery, decimal.Decimal, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.amount, a.installments, advance_outstanding(a.id)
		FROM salary_advance a
		WHERE a.employee_id = $1 AND advance_outstanding(a.id) > 0
		ORDER BY a.issued_on`, employeeID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	type adv struct {
		id           uuid.UUID
		amount       decimal.Decimal
		installments int
		outstanding  decimal.Decimal
	}
	var open []adv
	for rows.Next() {
		var a adv
		if e := rows.Scan(&a.id, &a.amount, &a.installments, &a.outstanding); e != nil {
			rows.Close()
			return nil, decimal.Zero, e
		}
		open = append(open, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, decimal.Zero, err
	}

	out := []recovery{}
	total := decimal.Zero
	left := available
	for _, a := range open {
		if !left.IsPositive() {
			break
		}
		per := a.amount.Div(decimal.NewFromInt(int64(a.installments))).Round(2)
		take := per
		// The final instalment takes whatever is left, so the advance clears
		// exactly rather than leaving a few hallalas outstanding forever.
		if a.outstanding.LessThan(take) {
			take = a.outstanding
		}
		if left.LessThan(take) {
			take = left
		}
		if !take.IsPositive() {
			continue
		}
		out = append(out, recovery{advanceID: a.id, amount: take})
		total = total.Add(take)
		left = left.Sub(take)
	}
	return out, total, nil
}

// commissionFor computes what an employee earned on the period's sales.
//
// C6 wants rules "by employee, product, category, store, total revenue, or
// profit; flat percentage or tiered thresholds". The most specific active rule
// wins — an employee-specific rule beats a store rule beats a company-wide one
// — because a shop that writes a special rate for its best salesperson means it
// to override the general one, not to stack with it.
//
// # Attribution
//
// A sale belongs to the person who rang it up, recorded on
// `sales_invoice.cashier_id` (0112). Until that column existed this function
// joined on `i.created_by`, which was never a column at all: the query errored
// and payroll returned a 500 for every commission-eligible employee.
//
// A CREDIT NOTE is attributed to whoever made the ORIGINAL sale, not to
// whoever stood at the till for the return. C14 effect 7 says to reverse the
// commission attributed to the original sale, and taking it off the refunding
// cashier instead would dock the wrong person and leave the seller paid for
// goods that came back. That is what the join through `parent_invoice_id`
// below is for.
//
// # Signed, so a return actually reverses
//
// A credit note's lines carry POSITIVE net and cost amounts, with the direction
// held in the document type — so summing both kinds together made a refund
// INCREASE commission. Selling something for 100 and refunding it in full paid
// as if 200 had been sold. The CASE below is what makes a return subtract.
//
// # Only documents that legally exist
//
// Draft and cancelled invoices are excluded. A draft has consumed no invoice
// counter and is not a sale yet; a cancelled one never became one. Every other
// state — including `rejected`, which keeps its number and is corrected by a
// credit note — is a real sale, and its correction arrives as its own document.
//
// # The rule's own scope decides which takings it covers
//
// `store_id`, `category_id`, `brand_id` and `variant_id` were previously read
// only to RANK candidate rules and never to filter the takings, so a scheme
// written for one branch paid on the whole company. A null means "any", as the
// table's comment says.
func (s *Service) commissionFor(
	ctx context.Context, tx pgx.Tx, scope Scope, employeeID uuid.UUID,
	period time.Time,
) (decimal.Decimal, error) {
	end := period.AddDate(0, 1, 0)

	// The scheme in force for this employee this month, most specific first.
	var basis string
	var rate decimal.Decimal
	var tiersRaw []byte
	var storeID, categoryID, brandID, variantID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT basis, rate, tiers, store_id, category_id, brand_id, variant_id
		FROM commission_rule
		WHERE company_id = $1 AND is_active
		  AND effective_from <= $3
		  AND (effective_to IS NULL OR effective_to >= $3)
		  AND (employee_id IS NULL OR employee_id = $2)
		ORDER BY (employee_id IS NOT NULL) DESC,
		         (store_id IS NOT NULL) DESC,
		         effective_from DESC
		LIMIT 1`, scope.CompanyID, employeeID, period).
		Scan(&basis, &rate, &tiersRaw, &storeID, &categoryID, &brandID, &variantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, nil
	}
	if err != nil {
		return decimal.Zero, err
	}

	// What they sold, net of what came back, within the scheme's scope.
	//
	// `orig` is the invoice a credit note corrects; a sale is its own origin.
	// Attribution and the store both come from there, so a return follows the
	// sale it reverses rather than the till it was processed at.
	var revenue, cost decimal.Decimal
	err = tx.QueryRow(ctx, `
		SELECT
		  coalesce(sum(CASE WHEN i.doc_type = 'credit_note'
		                    THEN -l.net_amount ELSE l.net_amount END), 0),
		  coalesce(sum(CASE WHEN i.doc_type = 'credit_note'
		                    THEN -l.cogs_amount ELSE l.cogs_amount END), 0)
		FROM sales_invoice i
		LEFT JOIN sales_invoice orig ON orig.id = i.parent_invoice_id
		JOIN sales_invoice_line l ON l.invoice_id = i.id
		JOIN employee e ON e.user_id = coalesce(orig.cashier_id, i.cashier_id)
		LEFT JOIN variant v ON v.id = l.variant_id
		LEFT JOIN product p ON p.id = v.product_id
		WHERE e.id = $1 AND i.company_id = $2
		  AND i.issued_at >= $3 AND i.issued_at < $4
		  AND i.state NOT IN ('draft', 'cancelled')
		  AND ($5::uuid IS NULL OR coalesce(orig.store_id, i.store_id) = $5)
		  AND ($6::uuid IS NULL OR p.category_id = $6)
		  AND ($7::uuid IS NULL OR p.brand_id    = $7)
		  AND ($8::uuid IS NULL OR l.variant_id  = $8)`,
		employeeID, scope.CompanyID, period, end,
		storeID, categoryID, brandID, variantID).Scan(&revenue, &cost)
	if err != nil {
		return decimal.Zero, err
	}

	base := revenue
	if basis == "profit" {
		base = revenue.Sub(cost)
	}
	// Nothing sold, or more came back than went out. Commission is not
	// negative: a month of net returns owes the employee nothing, and taking
	// money off a salary for it would be a deduction nobody has authorised.
	if !base.IsPositive() {
		return decimal.Zero, nil
	}

	applied, err := rateFromTiers(tiersRaw, base, rate)
	if err != nil {
		return decimal.Zero, err
	}
	return base.Mul(applied).Round(2), nil
}

// rateFromTiers picks the band a figure falls into.
//
// C6's example: 2% once sales exceed SAR 50,000 in a month. The highest band
// whose threshold the figure has reached applies to the WHOLE amount, which is
// how commission schemes are written and understood — a marginal calculation
// would be a different scheme and would surprise the salesperson.
func rateFromTiers(
	raw []byte, base, flat decimal.Decimal,
) (decimal.Decimal, error) {
	if len(raw) == 0 || string(raw) == "[]" {
		return flat, nil
	}

	var tiers []struct {
		From string `json:"from"`
		Rate string `json:"rate"`
	}
	if err := json.Unmarshal(raw, &tiers); err != nil {
		return decimal.Zero, errs.New(errs.CodeInternal,
			"A commission rule's tiers could not be read.")
	}
	if len(tiers) == 0 {
		return flat, nil
	}

	applied := decimal.Zero
	for _, t := range tiers {
		from, e1 := decimal.NewFromString(t.From)
		r, e2 := decimal.NewFromString(t.Rate)
		if e1 != nil || e2 != nil {
			return decimal.Zero, errs.New(errs.CodeInternal,
				"A commission tier is not a number.")
		}
		if base.GreaterThanOrEqual(from) && r.GreaterThan(applied) {
			applied = r
		}
	}
	return applied, nil
}

// staffForPeriod is everybody who was employed during the month.
//
// Somebody who left mid-month is still owed for the days they worked, and
// somebody who joined mid-month is owed from the day they started — so the
// filter is an overlap with the period, not "active today".
func staffForPeriod(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, period time.Time,
) ([]employeeForPay, error) {
	end := period.AddDate(0, 1, 0)
	rows, err := tx.Query(ctx, `
		SELECT id, full_name, coalesce(iban, ''), is_saudi, joined_on,
		       basic_salary, housing_allowance, transport_allowance,
		       other_allowance, commission_eligible
		FROM employee
		WHERE company_id = $1
		  AND joined_on < $3
		  AND (left_on IS NULL OR left_on >= $2)
		ORDER BY full_name`, companyID, period, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []employeeForPay{}
	for rows.Next() {
		var e employeeForPay
		if err := rows.Scan(&e.id, &e.name, &e.iban, &e.isSaudi, &e.joinedOn,
			&e.basic, &e.housing, &e.transport, &e.other,
			&e.commissionEligible); err != nil {
			return nil, err
		}
		// The usual Saudi contributory basis. Computed rather than stored so a
		// change to either component flows through with nothing to keep in step.
		e.contributoryWage = e.basic.Add(e.housing)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Approve signs a run off and posts what it owes.
//
// The wage is EARNED here, not paid: expense and liability. Paying it is a
// separate act on a later day, which is why C6 separates them and why the
// month a shop pays late still shows the staff it employed.
func (s *Service) Approve(
	ctx context.Context, scope Scope, runID uuid.UUID,
) (Run, error) {
	var out Run
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, currency, country string
		var period time.Time
		var gross, deductions, net, employerGOSI decimal.Decimal

		e := tx.QueryRow(ctx, `
			SELECT r.status, r.currency, c.country, r.period, r.gross_total,
			       r.deduction_total, r.net_total, r.employer_gosi
			FROM payroll_run r
			JOIN company c ON c.id = r.company_id
			WHERE r.id = $1 AND r.company_id = $2 FOR UPDATE OF r`,
			runID, scope.CompanyID).Scan(&status, &currency, &country, &period,
			&gross, &deductions, &net, &employerGOSI)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That payroll run was not found.")
		}
		if e != nil {
			return e
		}
		if status != "draft" {
			return errs.Newf(errs.CodeConflict,
				"That run is %s, so it cannot be approved again.", status)
		}

		gosiEmployee, advanceRecovery := decimal.Zero, decimal.Zero
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(gosi_employee), 0),
			       coalesce(sum(advance_recovery), 0)
			FROM payslip WHERE run_id = $1`, runID).
			Scan(&gosiEmployee, &advanceRecovery); e != nil {
			return e
		}

		entry, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			// Dated the last day of the month worked, not the day somebody
			// pressed approve: the cost belongs to the period that incurred it.
			Date:       period.AddDate(0, 1, -1),
			SourceType: "payroll_run", SourceID: runID,
			RuleKey:      "payroll.accrue",
			Currency:     currency,
			BaseCurrency: currency,
			FXRate:       decimal.NewFromInt(1),
			Memo:         "Payroll " + period.Format("January 2006"),
			PostedBy:     &scope.UserID,
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{
				"gross":            gross,
				"gosi_employee":    gosiEmployee,
				"advance_recovery": advanceRecovery,
				"net":              net,
			},
		})
		if e != nil {
			return e
		}

		// The employer's own contribution: a second entry because it is a
		// different cost with a different rule, and folding it into the wage
		// entry would make "what did staff cost" unanswerable from the ledger.
		if employerGOSI.IsPositive() {
			if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       period.AddDate(0, 1, -1),
				SourceType: "payroll_run_employer", SourceID: runID,
				RuleKey:      "payroll.employer_gosi",
				Currency:     currency,
				BaseCurrency: currency,
				FXRate:       decimal.NewFromInt(1),
				Memo:         "Employer social insurance",
				PostedBy:     &scope.UserID,
			}, country, accounting.Transaction{
				Amounts: accounting.Amounts{"amount": employerGOSI},
			}); e != nil {
				return e
			}
		}

		if _, e := tx.Exec(ctx, `
			UPDATE payroll_run
			SET status = 'approved', approved_by = $2, approved_at = now(),
			    journal_entry_id = $3
			WHERE id = $1`, runID, scope.UserID, entry.EntryID); e != nil {
			return e
		}

		read, e := s.readRun(ctx, tx, scope.CompanyID, runID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Pay settles an approved run.
func (s *Service) Pay(
	ctx context.Context, scope Scope, runID, accountID uuid.UUID,
	on time.Time,
) (Run, error) {
	var out Run
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, currency, country string
		var net decimal.Decimal
		e := tx.QueryRow(ctx, `
			SELECT r.status, r.currency, c.country, r.net_total
			FROM payroll_run r
			JOIN company c ON c.id = r.company_id
			WHERE r.id = $1 AND r.company_id = $2 FOR UPDATE OF r`,
			runID, scope.CompanyID).Scan(&status, &currency, &country, &net)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That payroll run was not found.")
		}
		if e != nil {
			return e
		}
		if status != "approved" {
			return errs.Newf(errs.CodeConflict,
				"That run is %s. Only an approved run can be paid.", status)
		}

		role, e := accountRoleOf(ctx, tx, scope.CompanyID, accountID)
		if e != nil {
			return e
		}

		if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       on,
			SourceType: "payroll_payment", SourceID: runID,
			RuleKey:      "payroll.pay",
			Currency:     currency,
			BaseCurrency: currency,
			FXRate:       decimal.NewFromInt(1),
			Memo:         "Wages paid",
			PostedBy:     &scope.UserID,
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{"amount": net},
			Groups: map[string]accounting.Group{
				"payment_account": {{Role: role, Amount: net}},
			},
		}); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE payroll_run
			SET status = 'paid', paid_at = now(), pay_date = $3,
			    money_account_id = $4
			WHERE id = $1 AND company_id = $2`,
			runID, scope.CompanyID, on, accountID); e != nil {
			return e
		}

		read, e := s.readRun(ctx, tx, scope.CompanyID, runID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// accountRoleOf finds which posting role a money account carries.
func accountRoleOf(
	ctx context.Context, tx pgx.Tx, companyID, accountID uuid.UUID,
) (string, error) {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT m.role
		FROM money_account a
		JOIN account_role_map m ON m.account_id = a.account_id
		WHERE a.id = $1 AND a.company_id = $2`,
		accountID, companyID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound,
			"That cash or bank account was not found.")
	}
	return role, err
}

// Runs lists payroll runs.
func (s *Service) Runs(ctx context.Context, scope Scope) ([]Run, error) {
	out := []Run{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, runSelect+`
			WHERE r.company_id = $1 ORDER BY r.period DESC LIMIT 120`,
			scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanRun(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ReadRun returns one run with its payslips.
func (s *Service) ReadRun(
	ctx context.Context, scope Scope, runID uuid.UUID,
) (Run, error) {
	var out Run
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readRun(ctx, tx, scope.CompanyID, runID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

const runSelect = `
	SELECT r.id, r.run_no, r.period, r.pay_date, r.status, r.currency,
	       r.gross_total, r.deduction_total, r.net_total, r.employer_gosi,
	       coalesce(r.note, '')
	FROM payroll_run r`

func (s *Service) readRun(
	ctx context.Context, tx pgx.Tx, companyID, runID uuid.UUID,
) (Run, error) {
	row := tx.QueryRow(ctx, runSelect+`
		WHERE r.id = $1 AND r.company_id = $2`, runID, companyID)
	out, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, errs.New(errs.CodeNotFound,
			"That payroll run was not found.")
	}
	if err != nil {
		return Run{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT p.id, p.employee_id, e.full_name, p.basic, p.housing,
		       p.transport, p.other_allowance, p.overtime, p.commission,
		       p.bonus, p.gross, p.absence_deduction, p.gosi_employee,
		       p.advance_recovery, p.other_deduction, p.deductions, p.net,
		       p.gosi_employer
		FROM payslip p
		JOIN employee e ON e.id = p.employee_id
		WHERE p.run_id = $1 ORDER BY e.full_name`, runID)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Payslip
		var basic, housing, transport, other, ot, comm, bonus, gross decimal.Decimal
		var absence, gosiEmp, adv, otherDed, deds, net, gosiEr decimal.Decimal
		if e := rows.Scan(&p.ID, &p.EmployeeID, &p.Employee, &basic, &housing,
			&transport, &other, &ot, &comm, &bonus, &gross, &absence,
			&gosiEmp, &adv, &otherDed, &deds, &net, &gosiEr); e != nil {
			return Run{}, e
		}
		p.Basic, p.Housing = basic.StringFixed(2), housing.StringFixed(2)
		p.Transport, p.OtherAllowance = transport.StringFixed(2), other.StringFixed(2)
		p.Overtime, p.Commission = ot.StringFixed(2), comm.StringFixed(2)
		p.Bonus, p.Gross = bonus.StringFixed(2), gross.StringFixed(2)
		p.AbsenceDeduction = absence.StringFixed(2)
		p.GOSIEmployee = gosiEmp.StringFixed(2)
		p.AdvanceRecovery = adv.StringFixed(2)
		p.OtherDeduction = otherDed.StringFixed(2)
		p.Deductions, p.Net = deds.StringFixed(2), net.StringFixed(2)
		p.GOSIEmployer = gosiEr.StringFixed(2)
		out.Payslips = append(out.Payslips, p)
	}
	return out, rows.Err()
}

func scanRun(row scanner) (Run, error) {
	var r Run
	var period time.Time
	var payDate *time.Time
	var gross, deds, net, erGOSI decimal.Decimal
	if err := row.Scan(&r.ID, &r.Number, &period, &payDate, &r.Status,
		&r.Currency, &gross, &deds, &net, &erGOSI, &r.Note); err != nil {
		return Run{}, err
	}
	r.Period = period.Format("2006-01")
	if payDate != nil {
		r.PayDate = payDate.Format("2006-01-02")
	}
	r.Gross = gross.StringFixed(2)
	r.Deductions = deds.StringFixed(2)
	r.Net = net.StringFixed(2)
	r.EmployerGOSI = erGOSI.StringFixed(2)
	r.Payslips = []Payslip{}
	return r, nil
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func daysIn(period time.Time) int {
	return firstOfMonth(period).AddDate(0, 1, -1).Day()
}

// --- WPS ------------------------------------------------------------------

// WageFile is a generated Mudad submission.
type WageFile struct {
	ID            uuid.UUID `json:"id"`
	RunID         uuid.UUID `json:"run_id"`
	Status        string    `json:"status"`
	FormatVersion *int      `json:"format_version,omitempty"`
	EmployeeCount int       `json:"employee_count"`
	TotalAmount   string    `json:"total_amount"`
	Checksum      string    `json:"checksum,omitempty"`
	Content       string    `json:"content,omitempty"`
	GeneratedAt   string    `json:"generated_at"`
}

// GenerateWageFile builds the WPS submission for an approved run.
//
// The LAYOUT is `SA.WPS.WAGE_FILE_FORMAT` in the registry and is still
// `__VERIFY__`. C6 is explicit that Mudad uses "its own XML-based format —
// distinct from the pipe-delimited SIF format used in some other GCC states",
// and that is precisely the kind of claim Part N requires to be verified
// against the primary source before code depends on it.
//
// So this refuses, naming the rule, rather than emitting a plausible file. A
// wage file in the wrong layout is not a partial success: it is rejected by
// Mudad, and a rejected submission can freeze a company's portal access.
//
// Everything around the file is built and works: the run is validated, the
// employees and amounts are assembled, and the consistency check design 20
// requires runs first. The moment somebody stamps the verified format, this
// produces a file from data that is already proven correct.
func (s *Service) GenerateWageFile(
	ctx context.Context, scope Scope, runID uuid.UUID,
) (WageFile, error) {
	var out WageFile
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, country string
		var period time.Time
		var net decimal.Decimal
		e := tx.QueryRow(ctx, `
			SELECT r.status, c.country, r.period, r.net_total
			FROM payroll_run r
			JOIN company c ON c.id = r.company_id
			WHERE r.id = $1 AND r.company_id = $2`,
			runID, scope.CompanyID).Scan(&status, &country, &period, &net)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That payroll run was not found.")
		}
		if e != nil {
			return e
		}
		if status == "draft" {
			return errs.New(errs.CodeConflict,
				"Approve the run before generating a wage file: a draft is a "+
					"calculation, not a payment instruction.")
		}

		// Design 20's consistency check, run BEFORE the file is built.
		// "A mismatch between the Qiwa contract, the GOSI wage and the actual
		// bank transfer can freeze portal access, so it is caught locally
		// first." Missing details are cheap to fix here and expensive to fix
		// after a rejection.
		problems, e := wageFileProblems(ctx, tx, runID)
		if e != nil {
			return e
		}
		if len(problems) > 0 {
			return errs.Newf(errs.CodeInvalidInput,
				"The wage file cannot be built yet: %s",
				strings.Join(problems, "; "))
		}

		var count int
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM payslip WHERE run_id = $1`,
			runID).Scan(&count); e != nil {
			return e
		}

		// Asked of the market first. Mudad is Saudi Arabia's wage protection
		// system; producing its file for another market would generate a
		// document no authority asked for and none would accept.
		if !market.WageProtectionApplies(country) {
			return errs.Newf(errs.CodeUnverifiedRule,
				"There is no wage-protection file format on record for %s. "+
					"Mudad is Saudi Arabia's system and does not apply here.",
				strings.ToUpper(strings.TrimSpace(country)))
		}

		// The format itself. Unverified means no file.
		var spec struct {
			Format  string `json:"format"`
			Version string `json:"version"`
		}
		if e := s.rules.Into(ctx, registry.Query{
			Key:      "SA.WPS.WAGE_FILE_FORMAT",
			Country:  country,
			AsOf:     period,
			TenantID: scope.TenantID,
			Tx:       tx,
		}, &spec); e != nil {
			return e
		}
		if strings.TrimSpace(spec.Format) == registry.Placeholder {
			return errs.New(errs.CodeUnverifiedRule,
				"The Mudad wage-file layout (SA.WPS.WAGE_FILE_FORMAT) has not "+
					"been verified against its official source, so a file "+
					"cannot be produced. Everything else about this run is "+
					"ready: the figures, the bank details and the consistency "+
					"check have all passed. A Super Admin must record the "+
					"verified format in Super Admin > Regulatory Registry.")
		}

		// The Ministry's own layout is built whole rather than row by row:
		// its Header Group carries a total the receiving bank validates
		// against the sum of the rows, so the file cannot be assembled one
		// line at a time. See wpsfile.go.
		var content string
		if spec.Format == FormatWPSTab {
			// [FILE-REF] must be unique for the establishment across every
			// file it ever sends — a duplicate rejects the whole file — so it
			// is the run's own id, which is unique by construction.
			content, e = buildWPSFile(ctx, tx, runID, scope.CompanyID,
				period, "RUN"+strings.ReplaceAll(runID.String(), "-", "")[:13])
		} else {
			content, e = buildWageFile(ctx, tx, runID, spec.Format)
		}
		if e != nil {
			return e
		}
		sum := sha256.Sum256([]byte(content))

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO wps_file
			  (tenant_id, company_id, run_id, status, employee_count,
			   total_amount, content, checksum, generated_by)
			VALUES ($1,$2,$3,'generated',$4,$5,$6,$7,$8) RETURNING id`,
			scope.TenantID, scope.CompanyID, runID, count, net, content,
			hex.EncodeToString(sum[:]), scope.UserID).Scan(&id); e != nil {
			return e
		}

		out = WageFile{
			ID: id, RunID: runID, Status: "generated",
			EmployeeCount: count, TotalAmount: net.StringFixed(2),
			Checksum:    hex.EncodeToString(sum[:]),
			Content:     content,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		}
		return nil
	})
	return out, db.Translate(err, "")
}

// wageFileProblems is the local consistency check.
func wageFileProblems(
	ctx context.Context, tx pgx.Tx, runID uuid.UUID,
) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.full_name,
		       coalesce(e.iban, '') = '' AS no_iban,
		       coalesce(e.gosi_number, '') = '' AS no_gosi,
		       coalesce(e.national_id, '') = '' AND coalesce(e.iqama_no, '') = ''
		         AS no_id
		FROM payslip p
		JOIN employee e ON e.id = p.employee_id
		WHERE p.run_id = $1
		  AND (coalesce(e.iban, '') = ''
		       OR coalesce(e.gosi_number, '') = ''
		       OR (coalesce(e.national_id, '') = ''
		           AND coalesce(e.iqama_no, '') = ''))
		ORDER BY e.full_name
		LIMIT 20`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		var noIBAN, noGOSI, noID bool
		if e := rows.Scan(&name, &noIBAN, &noGOSI, &noID); e != nil {
			return nil, e
		}
		var missing []string
		if noIBAN {
			missing = append(missing, "a bank account")
		}
		if noGOSI {
			missing = append(missing, "a GOSI registration number")
		}
		if noID {
			missing = append(missing, "a national ID or Iqama number")
		}
		out = append(out, fmt.Sprintf("%s has no %s",
			name, strings.Join(missing, " and no ")))
	}
	return out, rows.Err()
}

// buildWageFile renders the payload in the verified layout.
//
// Reached only once the format is verified — GenerateWageFile refuses before
// this is called otherwise. The layout name comes from the registry, so adding
// a second one (a different country's WPS, say) is a registry entry and a case
// here rather than a change to everything above.
func buildWageFile(
	ctx context.Context, tx pgx.Tx, runID uuid.UUID, format string,
) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.employee_no, e.full_name, coalesce(e.iban, ''),
		       coalesce(e.gosi_number, ''),
		       coalesce(nullif(e.national_id, ''), e.iqama_no, ''),
		       p.basic, p.housing + p.transport + p.other_allowance,
		       p.deductions, p.net
		FROM payslip p
		JOIN employee e ON e.id = p.employee_id
		WHERE p.run_id = $1
		ORDER BY e.employee_no`, runID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var b strings.Builder
	for rows.Next() {
		var no, name, iban, gosi, id string
		var basic, allowances, deductions, net decimal.Decimal
		if e := rows.Scan(&no, &name, &iban, &gosi, &id, &basic, &allowances,
			&deductions, &net); e != nil {
			return "", e
		}
		// The field ORDER and the separator are what the verified format
		// defines. Written here as the registry names it rather than as a
		// guess: an unrecognised format is refused rather than approximated.
		switch format {
		case "mudad_xml":
			fmt.Fprintf(&b,
				"<Record><EmployeeId>%s</EmployeeId><Name>%s</Name>"+
					"<Iban>%s</Iban><GosiId>%s</GosiId><NationalId>%s</NationalId>"+
					"<BasicWage>%s</BasicWage><Allowances>%s</Allowances>"+
					"<Deductions>%s</Deductions><NetWage>%s</NetWage></Record>\n",
				no, name, iban, gosi, id, basic.StringFixed(2),
				allowances.StringFixed(2), deductions.StringFixed(2),
				net.StringFixed(2))
		case "sif":
			fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%s\n",
				no, name, iban, gosi, id, basic.StringFixed(2),
				deductions.StringFixed(2), net.StringFixed(2))
		default:
			return "", errs.Newf(errs.CodeUnverifiedRule,
				"The wage-file format %q is recorded in the registry but this "+
					"build does not know how to write it.", format)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}
