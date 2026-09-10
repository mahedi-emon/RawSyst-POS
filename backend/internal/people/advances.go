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
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
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

		ent, err := s.eosbEntitlement(ctx, tx, scope.TenantID, country, period)
		if err != nil {
			return err
		}

		rows, e := tx.Query(ctx, `
			SELECT e.id, e.joined_on,
			       e.basic_salary, e.housing_allowance,
			       e.transport_allowance, e.other_allowance
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
			var basic, housing, transport, other decimal.Decimal
			if e := rows.Scan(&p.id, &p.joined,
				&basic, &housing, &transport, &other); e != nil {
				rows.Close()
				return e
			}
			w, e := ent.wage(basic, housing, transport, other)
			if e != nil {
				rows.Close()
				return e
			}
			p.wage = w
			staff = append(staff, p)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, p := range staff {
			// Service at the END of the month being charged, not at its start.
			//
			// The charge covers the whole of this month, so the service it is
			// charged against is the service completed by the end of it. Read
			// at the start instead, somebody who joined on the 10th of June was
			// charged nothing for July — a month they worked in full — and the
			// accrual ran permanently one month behind the settlement for
			// everybody who did not join on the 1st.
			//
			// It also settles the band question the same way `daysFor`
			// describes it: a person who crosses five years mid-month is into
			// their sixth by the end of it, and the month being charged belongs
			// to the higher band.
			months := monthsBetween(p.joined, period.AddDate(0, 1, -1))
			if !months.IsPositive() {
				continue
			}
			// One month's share of a year's entitlement, on the wage the
			// person is earning now, at the band this month falls in.
			//
			// The award is not one rate: Saudi labour law entitles less for
			// each of the first five years than for each year after them, and
			// charging the first-five rate for ever understates the liability
			// of exactly the long-serving people it is largest for. Which band
			// a month belongs to is decided by service AT THAT MONTH, so a
			// person crossing five years starts accruing at the higher rate
			// from the month they cross and no month already posted changes.
			perDay := p.wage.Div(decimal.NewFromInt(30))
			amount := perDay.Mul(ent.daysFor(months)).
				Div(decimal.NewFromInt(12)).Round(2)
			if !amount.IsPositive() {
				continue
			}

			// The id is minted here rather than by the database, because the
			// posting has to name the accrual it belongs to and the accrual
			// has to name the entry that recorded it — and only one of those
			// can be filled in afterwards.
			//
			// `eosb_accrual` is append-only: a `reject_always` trigger refuses
			// every UPDATE, so writing the row first and stamping the journal
			// entry onto it afterwards was a write that could never succeed.
			// It never had. The entitlement has always been a placeholder, so
			// the accrual refused for want of a rule long before it reached
			// this line, and the accrual has therefore never once run.
			accrualID := uuid.New()

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

			if _, e := tx.Exec(ctx, `
				INSERT INTO eosb_accrual
				  (id, tenant_id, company_id, employee_id, period, amount,
				   wage_basis, months_of_service, journal_entry_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				accrualID, scope.TenantID, scope.CompanyID, p.id, period,
				amount, p.wage, months, entry.EntryID); e != nil {
				return e
			}
			charged++
		}

		// The run is audited, not only its postings.
		//
		// Each charge already posts a journal entry naming who posted it, and
		// each writes an append-only `eosb_accrual` row. Neither answers "who
		// ran the accrual for August, and on which reading of the entitlement"
		// — and a month that was never run leaves no journal entry to ask,
		// which is precisely the case somebody investigates. Every other act in
		// this product that moves the ledger writes one of these; this did not.
		//
		// The entitlement is recorded with it. Correcting the rule later does
		// not rewrite months already posted, so the trail has to say what was
		// in force when each month was charged or a re-reading of the law
		// cannot be reconciled against the provision it produced.
		if charged == 0 {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "eosb_accrued",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"period":                   period.Format("2006-01"),
				"people":                   charged,
				"wage_basis":               ent.Basis,
				"days_per_year_first_five": ent.FirstFive.String(),
				"days_per_year_after_five": ent.AfterFive.String(),
			},
		})
	})
	return charged, db.Translate(err, "")
}

// eosbEntitlement is the statutory award, as the registry states it.
//
// # Why the whole rule rather than one field
//
// `SA.EOSB.ENTITLEMENT` carries a wage basis and two service bands, and this
// used to read one of them — `days_per_year_first_five` — and apply it to
// every year of every person's service. That is not the entitlement. Saudi
// labour law awards less for each of the first five years than for each year
// after them, so reading the first band alone UNDERSTATES what the business
// owes, and understates it most for the long-serving people whose award is
// largest. It would have done so silently, and the error only becomes visible
// on the day somebody with fifteen years leaves.
//
// The wage basis is read for the same reason. The award computed on basic pay
// and the award computed on basic plus housing are materially different
// answers for the same person, and which one is correct is a legal question
// the registry answers rather than a line of Go. It used to be hard-coded as
// basic plus housing, which was a guess wearing the clothes of a rule.
type eosbEntitlement struct {
	// Basis names the pay the award is computed on. The vocabulary is closed:
	// a value this software does not understand is refused rather than
	// approximated, because approximating it is how a wrong number reaches a
	// final settlement looking authoritative.
	Basis string `json:"wage_basis"`

	FirstFive decimal.Decimal `json:"-"`
	AfterFive decimal.Decimal `json:"-"`

	// Article 85's fractions, banded by length of service.
	//
	// Read for the accrual as well as the settlement, although only the
	// settlement applies one. A rule that resolves for the monthly charge and
	// then fails on the day somebody leaves reports itself as usable when it is
	// not, and the day it would fail is the day a final payment is being
	// calculated in front of the person waiting for it.
	ResignUnderTwo  decimal.Decimal `json:"-"`
	ResignTwoToFive decimal.Decimal `json:"-"`
	ResignFiveToTen decimal.Decimal `json:"-"`
	ResignOverTen   decimal.Decimal `json:"-"`

	// The fractions as the registry holds them, which is as the article states
	// them. A settlement says which share was applied, and "1/3" is what a
	// person can check against Article 85; the decimal it resolves to is the
	// same number and no help in that conversation.
	ResignUnderTwoText  string `json:"-"`
	ResignTwoToFiveText string `json:"-"`
	ResignFiveToTenText string `json:"-"`
	ResignOverTenText   string `json:"-"`
}

// The wage bases this software knows how to compute.
//
// Named as constants rather than written inline so the refusal below can list
// them, and so adding one is a deliberate edit in one place.
const (
	eosbBasisBasic         = "basic"
	eosbBasisBasicHousing  = "basic_plus_housing"
	eosbBasisAllAllowances = "basic_plus_all_allowances"
)

// daysFor is the entitlement for a year served at this much service.
//
// Sixty completed months is the boundary: a person who has served exactly five
// years is into their sixth, and the month being charged belongs to the higher
// band. Below it, the first-five rate.
func (e eosbEntitlement) daysFor(months decimal.Decimal) decimal.Decimal {
	if months.LessThan(decimal.NewFromInt(60)) {
		return e.FirstFive
	}
	return e.AfterFive
}

// The month boundaries the two articles band on, as completed months.
//
// Written out rather than inlined so the settlement and the accrual cannot
// drift apart: `daysFor` above and `resignationFraction` below both turn on
// sixty, and a five-year boundary written twice is a five-year boundary that
// gets corrected once.
const (
	eosbTwoYears  = 24
	eosbFiveYears = 60
	eosbTenYears  = 120
)

// resignationFraction is Article 85's share of the award, for somebody who
// RESIGNS at this much service. It returns the share and the literal the
// registry holds it as, so a settlement can quote the article's own words.
//
// # The three boundaries are not the same shape, and the article says so
//
// Read the article rather than assuming a convention:
//
//	"entitled to one third of the award after service of not less than two
//	consecutive years and NOT MORE THAN FIVE YEARS, to two thirds if his
//	service is IN EXCESS OF five consecutive years but less than 10 years,
//	and to the full award if his service amounts to 10 YEARS OR MORE."
//
// So two years is a floor the lower band includes, ten years is a floor the top
// band includes, and five years belongs to the LOWER band — "not more than
// five" takes it, and "in excess of five" does not. Two of those three are the
// same shape and the middle one is its mirror.
//
// This read `LessThan(eosbFiveYears)` until the article was ingested and the
// sentence sat next to the code. That put somebody resigning on their fifth
// anniversary exactly into the two-thirds band, which is a day early and,
// on a 20,000 wage and five years' service, 16,666.67 against the 8,333.33
// the article gives. One day either side of that date was already right; the
// date itself was not.
//
// The band above ten years is a figure like the other three. It arrived late:
// 0092 recorded three fractions, so service beyond ten years had none to apply
// and the software would have had to assume one. 0132 added the fourth, and
// this reads it rather than falling back on the whole award, because "the
// article does not band beyond ten years" is a reading of the article and not
// something a line of Go is entitled to decide.
func (e eosbEntitlement) resignationFraction(
	months decimal.Decimal,
) (decimal.Decimal, string) {
	switch {
	case months.LessThan(decimal.NewFromInt(eosbTwoYears)):
		return e.ResignUnderTwo, e.ResignUnderTwoText
	case months.LessThanOrEqual(decimal.NewFromInt(eosbFiveYears)):
		return e.ResignTwoToFive, e.ResignTwoToFiveText
	case months.LessThan(decimal.NewFromInt(eosbTenYears)):
		return e.ResignFiveToTen, e.ResignFiveToTenText
	default:
		return e.ResignOverTen, e.ResignOverTenText
	}
}

// bandedMonths splits a length of service across the two Article 84 bands.
//
// The first five years are entitled at one rate and everything after them at
// another, so a person with eight years' service is owed sixty months at the
// first rate and thirty-six at the second — not ninety-six at either.
func bandedMonths(months decimal.Decimal) (first, after decimal.Decimal) {
	boundary := decimal.NewFromInt(eosbFiveYears)
	if months.LessThanOrEqual(boundary) {
		return months, decimal.Zero
	}
	return boundary, months.Sub(boundary)
}

// award is the Article 84 award for a length of service, on a wage.
//
// # Why this agrees with the monthly accrual by construction
//
// `AccrueEOSB` charges, for one month, a thirtieth of the wage times the band's
// days divided by twelve. This is the same expression summed over the months,
// with the months in each band counted once — so the settlement and the sum of
// the charges are the same arithmetic and cannot disagree about the ENTITLEMENT.
//
// They can still differ in AMOUNT, and legitimately: each accrual is charged on
// the wage in force that month, and Article 84 computes the award on the LAST
// wage. A person whose pay rose is owed more than has been provided for, and
// the settlement reports that difference rather than hiding it. That difference
// is the whole reason a business accrues monthly instead of finding out at the
// door.
func (e eosbEntitlement) award(
	wage, months decimal.Decimal,
) decimal.Decimal {
	first, after := bandedMonths(months)
	perDay := wage.Div(decimal.NewFromInt(30))
	days := e.FirstFive.Mul(first).Add(e.AfterFive.Mul(after))
	return perDay.Mul(days).Div(decimal.NewFromInt(12)).Round(2)
}

// wage is the pay the award is computed on, for one person.
func (e eosbEntitlement) wage(
	basic, housing, transport, other decimal.Decimal,
) (decimal.Decimal, error) {
	switch e.Basis {
	case eosbBasisBasic:
		return basic, nil
	case eosbBasisBasicHousing:
		return basic.Add(housing), nil
	case eosbBasisAllAllowances:
		return basic.Add(housing).Add(transport).Add(other), nil
	default:
		return decimal.Zero, errs.Newf(errs.CodeUnverifiedRule,
			"The end-of-service rule states its wage basis as %q, and this "+
				"product does not know how to compute that. It understands "+
				"%q, %q and %q. Correct the wage basis in Super Admin > "+
				"Regulatory Registry.",
			e.Basis, eosbBasisBasic, eosbBasisBasicHousing,
			eosbBasisAllAllowances)
	}
}

// eosbEntitlement resolves the statutory entitlement.
//
// E6 puts end-of-service under Saudi labour law, and E8 requires every legal
// parameter to be versioned data with an effective date rather than a number in
// code. The rule key is resolved here so that the day somebody records the
// verified entitlement, this starts working with no code change — and until
// then it refuses rather than guessing, because an accrual at the wrong rate
// understates a liability for years before anybody notices.
func (s *Service) eosbEntitlement(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, country string,
	period time.Time,
) (eosbEntitlement, error) {
	var out eosbEntitlement

	if s.rules == nil {
		return out, errs.New(errs.CodeInternal,
			"The payroll service was built without the regulatory rule registry.")
	}

	// Asked of the market first. EOSB is Saudi labour law; resolving it for a
	// company in another market found no rule and failed the accrual for a
	// benefit that market may not have — or may have on entirely different
	// terms. Declining to compute one is the honest answer, and applying Saudi
	// service bands to a foreign contract would be inventing a rule.
	if !market.EndOfServiceApplies(country) {
		return out, errs.Newf(errs.CodeUnverifiedRule,
			"This product has no end-of-service entitlement rule for %s, so "+
				"the benefit cannot be accrued here. The Saudi rule does not "+
				"apply outside the Kingdom.",
			strings.ToUpper(strings.TrimSpace(country)))
	}

	q := registry.Query{
		Key:      "SA.EOSB.ENTITLEMENT",
		Country:  country,
		AsOf:     period,
		TenantID: tenantID,
		Tx:       tx,
	}

	// Every figure through Decimal, so an unfilled one refuses by the same
	// placeholder check as the first rather than parsing as zero. A zero band
	// would accrue nothing for the people it applies to and report success; a
	// zero resignation fraction would settle a leaver at nothing and look like
	// arithmetic.
	for _, f := range []struct {
		field string
		into  *decimal.Decimal
	}{
		{"days_per_year_first_five", &out.FirstFive},
		{"days_per_year_after_five", &out.AfterFive},
	} {
		v, err := s.rules.Decimal(ctx, q, f.field)
		if err != nil {
			return eosbEntitlement{}, err
		}
		*f.into = v
	}

	// Article 85's shares, read as fractions rather than as decimals.
	//
	// The article states a third and two thirds. A registry holding 0.3333 pays
	// 9,999.00 on a 30,000 award, and the shortfall is not an arithmetic error
	// — it is the registry having been told something very slightly untrue.
	// `Fraction` reads `1/3` and resolves it at a precision that rounds
	// correctly, and hands back the literal so the settlement can quote it.
	for _, f := range []struct {
		field string
		into  *decimal.Decimal
		text  *string
	}{
		{"resignation_fraction_under_two_years",
			&out.ResignUnderTwo, &out.ResignUnderTwoText},
		{"resignation_fraction_two_to_five_years",
			&out.ResignTwoToFive, &out.ResignTwoToFiveText},
		{"resignation_fraction_five_to_ten_years",
			&out.ResignFiveToTen, &out.ResignFiveToTenText},
		{"resignation_fraction_over_ten_years",
			&out.ResignOverTen, &out.ResignOverTenText},
	} {
		v, literal, err := s.rules.Fraction(ctx, q, f.field)
		if err != nil {
			return eosbEntitlement{}, err
		}
		*f.into, *f.text = v, literal
	}

	if err := s.rules.Into(ctx, q, &out); err != nil {
		return eosbEntitlement{}, err
	}
	if out.Basis == registry.Placeholder {
		return eosbEntitlement{}, errs.New(errs.CodeUnverifiedRule,
			"The end-of-service rule does not yet say which wage the award is "+
				"computed on. Record it in Super Admin > Regulatory Registry "+
				"before accruing the benefit.")
	}

	// Checked here rather than at the first payslip: a basis this software
	// cannot compute is a property of the RULE, and finding that out per
	// employee would report it as an employee's problem.
	if _, err := out.wage(
		decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero,
	); err != nil {
		return eosbEntitlement{}, err
	}

	// A fraction above 1 pays somebody more than the award it is a fraction OF.
	// The source-file path already refuses one, and this is the second door:
	// the registry screen writes a payload directly, and an override can be
	// written for one tenant. Both reach this line and neither goes through the
	// file's validation.
	for _, f := range []struct {
		field string
		value decimal.Decimal
	}{
		{"resignation_fraction_under_two_years", out.ResignUnderTwo},
		{"resignation_fraction_two_to_five_years", out.ResignTwoToFive},
		{"resignation_fraction_five_to_ten_years", out.ResignFiveToTen},
		{"resignation_fraction_over_ten_years", out.ResignOverTen},
	} {
		if f.value.GreaterThan(decimal.NewFromInt(1)) {
			return eosbEntitlement{}, errs.Newf(errs.CodeUnverifiedRule,
				"The end-of-service rule states %s as %s. It is a fraction of "+
					"the award, somewhere between 0 and 1 — a third is "+
					"0.3333, not 33. Correct it in Super Admin > Regulatory "+
					"Registry.",
				f.field, f.value.String())
		}
	}

	return out, nil
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

// How somebody's service ended. A closed vocabulary, because Article 85 applies
// to one of these and not the other, and guessing which from a free-text
// leaving note is how a settlement gets paid at a third of what is owed.
const (
	EOSBLeavingResignation = "resignation"
	EOSBLeavingTermination = "termination"
)

// EOSBSettlement is the statutory award owed to one person on the day they
// leave, with the whole of its working shown.
//
// # Why this is not the accrued figure
//
// `EOSBPositions` answers what has been PROVIDED FOR: the sum of the monthly
// charges. This answers what is OWED, which is a different question with a
// different formula, and the employee screen used to show the first under a
// heading that promised the second.
//
// Two things separate them. Article 84 computes the award on the LAST wage,
// while each accrual was charged on the wage in force that month, so anybody
// whose pay has risen is owed more than has been provided. And Article 85 pays
// a person who RESIGNS a fraction of that award — which is the whole of what
// the three resignation fractions in `SA.EOSB.ENTITLEMENT` are for, and which
// nothing in this product could apply until now.
//
// Every intermediate figure is carried rather than only the total. A final
// settlement is a number somebody has to be able to argue with: the person
// leaving is entitled to see which wage it was computed on, how their service
// split across the two bands, and what fraction was applied to it. A single
// figure with no working is a figure nobody can check.
type EOSBSettlement struct {
	EmployeeID uuid.UUID `json:"employee_id"`
	Employee   string    `json:"employee"`
	Currency   string    `json:"currency"`

	JoinedOn  string `json:"joined_on"`
	LeavingOn string `json:"leaving_on"`
	Reason    string `json:"reason"`

	MonthsOfService string `json:"months_of_service"`

	// The rule, as it stood at the leaving date. Echoed so the settlement says
	// which reading of the law produced it, and so re-running it later cannot
	// silently answer differently.
	WageBasis  string `json:"wage_basis"`
	Wage       string `json:"wage"`
	RuleAsOf   string `json:"rule_as_of"`
	VerifiedOn string `json:"rule_verified_on,omitempty"`

	// Article 84, banded.
	FirstBandMonths string `json:"first_band_months"`
	FirstBandDays   string `json:"first_band_days_per_year"`
	AfterBandMonths string `json:"after_band_months"`
	AfterBandDays   string `json:"after_band_days_per_year"`
	FullAward       string `json:"full_award"`

	// Article 85. `1` on a termination, where the article does not reduce the
	// award — stated rather than omitted, so the two cases read the same way
	// and a reader is never left wondering whether a fraction was forgotten.
	Fraction string `json:"resignation_fraction"`
	Award    string `json:"award"`

	// What has already been charged to the provision, and the gap.
	//
	// Negative shortfall means over-provided, and it is reported as a negative
	// rather than clamped: a provision that turns out too large is a real
	// accounting fact and a business that cannot see it cannot release it.
	Provision string `json:"provision"`
	Shortfall string `json:"shortfall"`
}

// EOSBSettlement computes what one person is owed on leaving.
//
// # Why the leaving date is an input rather than a lookup
//
// A settlement is normally worked out BEFORE the departure is recorded — that
// is the point of it, because the figure is what the last payslip has to carry.
// Reading `left_on` would mean the calculation only becomes available after the
// event it exists to prepare for. So the date is given, it defaults to a
// recorded leaving date when there is one, and it is refused if it falls before
// the person joined.
//
// # Why the reason is required and not inferred
//
// `Leave` writes the reason into a free-text note. Article 85 turns on whether
// somebody resigned, and reading that intent out of prose is a guess with a
// leaver's money on the end of it. The caller states it, from a closed
// vocabulary, and an unrecognised one is refused.
//
// # Why the rule is resolved at the leaving date
//
// The same discipline as the accrual. A settlement re-run next year for a
// person who left last March must give March's answer, so the entitlement is
// resolved AS OF the leaving date and a rule recorded afterwards does not
// rewrite it.
func (s *Service) EOSBSettlement(
	ctx context.Context, scope Scope, employeeID uuid.UUID,
	leavingOn time.Time, reason string,
) (EOSBSettlement, error) {
	var out EOSBSettlement

	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason != EOSBLeavingResignation && reason != EOSBLeavingTermination {
		return out, errs.Newf(errs.CodeInvalidInput,
			"Say how the service ended: %q or %q. The end-of-service award is "+
				"reduced by Article 85 when somebody resigns and is not when "+
				"they are dismissed, so the two are different amounts and this "+
				"cannot be assumed.",
			EOSBLeavingResignation, EOSBLeavingTermination)
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var country, currency string
		if e := tx.QueryRow(ctx,
			`SELECT country, base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&country, &currency); e != nil {
			return e
		}

		var joined time.Time
		var left *time.Time
		var basic, housing, transport, other decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT full_name, joined_on, left_on,
			       basic_salary, housing_allowance,
			       transport_allowance, other_allowance
			FROM employee
			WHERE id = $1 AND company_id = $2`,
			employeeID, scope.CompanyID).
			Scan(&out.Employee, &joined, &left,
				&basic, &housing, &transport, &other); e != nil {
			return e
		}

		if leavingOn.IsZero() {
			if left != nil {
				leavingOn = *left
			} else {
				leavingOn = time.Now().UTC().Truncate(24 * time.Hour)
			}
		}
		leavingOn = leavingOn.UTC().Truncate(24 * time.Hour)
		if leavingOn.Before(joined) {
			return errs.Newf(errs.CodeInvalidInput,
				"That leaving date is %s and they joined on %s. A settlement "+
					"cannot be computed for service that has not happened.",
				leavingOn.Format("2006-01-02"), joined.Format("2006-01-02"))
		}

		ent, e := s.eosbEntitlement(ctx, tx, scope.TenantID, country, leavingOn)
		if e != nil {
			return e
		}
		wage, e := ent.wage(basic, housing, transport, other)
		if e != nil {
			return e
		}

		months := monthsBetween(joined, leavingOn)
		first, after := bandedMonths(months)
		full := ent.award(wage, months)

		fraction, fractionText := decimal.NewFromInt(1), "1"
		if reason == EOSBLeavingResignation {
			fraction, fractionText = ent.resignationFraction(months)
		}

		var provision decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(amount), 0) FROM eosb_accrual
			WHERE employee_id = $1 AND company_id = $2`,
			employeeID, scope.CompanyID).Scan(&provision); e != nil {
			return e
		}

		award := full.Mul(fraction).Round(2)

		out.EmployeeID = employeeID
		out.Currency = currency
		out.JoinedOn = joined.Format("2006-01-02")
		out.LeavingOn = leavingOn.Format("2006-01-02")
		out.Reason = reason
		out.MonthsOfService = months.String()
		out.WageBasis = ent.Basis
		out.Wage = wage.StringFixed(2)
		out.RuleAsOf = leavingOn.Format("2006-01-02")
		out.FirstBandMonths = first.String()
		out.FirstBandDays = ent.FirstFive.String()
		out.AfterBandMonths = after.String()
		out.AfterBandDays = ent.AfterFive.String()
		out.FullAward = full.StringFixed(2)
		out.Fraction = fractionText
		out.Award = award.StringFixed(2)
		out.Provision = provision.StringFixed(2)
		out.Shortfall = award.Sub(provision).StringFixed(2)

		// The rule's own verification date, carried onto the settlement.
		//
		// A settlement is a document somebody may have to defend, and "which
		// reading of the Labour Law was this computed from, and had anybody
		// checked it" is the first question asked of one. Resolving the rule
		// again here is cheap: it is cached per request and this is a page
		// somebody opens once per leaver.
		rule, e := s.rules.Resolve(ctx, registry.Query{
			Key:      "SA.EOSB.ENTITLEMENT",
			Country:  country,
			AsOf:     leavingOn,
			TenantID: scope.TenantID,
			Tx:       tx,
		})
		if e != nil {
			return e
		}
		if rule.VerifiedOn != nil {
			out.VerifiedOn = rule.VerifiedOn.Format("2006-01-02")
		}
		return nil
	})
	if err != nil {
		return EOSBSettlement{}, db.Translate(err,
			"That employee was not found.")
	}
	return out, nil
}

// monthsBetween is completed months of service at a period.
func monthsBetween(from, to time.Time) decimal.Decimal {
	months := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())

	// The day of the month decides whether the last one is COMPLETED.
	//
	// Counting calendar months alone made somebody who joined on the 20th a
	// full month of service on the 1st, twelve days later. It was invisible
	// while the only caller was the accrual, which charges on the first of each
	// month and where a month too many changes only which band a charge falls
	// in. The settlement made it visible and made it money: a person who joined
	// on the 20th and resigned on the 5th of their second anniversary month was
	// moved into the next Article 85 band by a fortnight they had not worked.
	//
	// A leaving date whose day is EARLIER than the joining day has not
	// completed that month, so it does not count.
	if to.Day() < from.Day() {
		months--
	}
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
	// CategoryID, BrandID and VariantID narrow which TAKINGS the scheme pays
	// on -- a null is "any", as `commissionFor` reads them.
	//
	// They were columns the payroll engine filtered on and nothing could
	// write, so a shop could be told its scheme covered one department and had
	// no way to say so. Carried on the read as well as the write, because a
	// list that omits a rule's scope shows two schemes as identical when they
	// pay different money.
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	BrandID    *uuid.UUID `json:"brand_id,omitempty"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty"`
	Rate       string     `json:"rate"`
	Tiers      string     `json:"tiers"`
	From       string     `json:"effective_from"`
	To         string     `json:"effective_to,omitempty"`
}

// CommissionScope is the optional narrowing of a scheme.
//
// Its own type rather than five more positional arguments: SetCommissionRule
// already took seven, and a call site passing three consecutive `*uuid.UUID`
// values in the wrong order would compile and pay the wrong people.
type CommissionScope struct {
	EmployeeID *uuid.UUID
	StoreID    *uuid.UUID
	CategoryID *uuid.UUID
	BrandID    *uuid.UUID
	VariantID  *uuid.UUID
}

// SetCommissionRule creates a scheme.
func (s *Service) SetCommissionRule(
	ctx context.Context, scope Scope, name, basis string,
	on CommissionScope, rate decimal.Decimal, tiers string,
	from time.Time, to *time.Time,
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

	if to != nil && to.Before(from) {
		return CommissionRule{}, errs.Validation(
			"A scheme cannot end before it starts.").
			WithField("effective_to", "Leave it empty for a scheme with no end date.")
	}

	var out CommissionRule
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO commission_rule
			  (tenant_id, company_id, name, basis, employee_id, store_id,
			   category_id, brand_id, variant_id,
			   rate, tiers, effective_from, effective_to, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(name), basis,
			on.EmployeeID, on.StoreID, on.CategoryID, on.BrandID, on.VariantID,
			rate, tiers, from, to, scope.UserID).
			Scan(&id); e != nil {
			return db.Translate(e, "That commission scheme could not be saved.")
		}
		read, e := s.readCommissionRule(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// SetCommissionRuleActive switches a scheme on or off.
//
// Switched off rather than deleted, for the same reason an approval rule is:
// a payslip names the scheme that paid it, and deleting one would leave a
// figure on somebody's payslip that nothing in the product can explain.
//
// This is the only way to stop a scheme that has no end date, and until it
// existed a shop that configured the wrong rate on day one had no way to stop
// it paying -- `commissionFor` reads `is_active`, and nothing could clear it.
func (s *Service) SetCommissionRuleActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE commission_rule SET is_active = $3
			WHERE id = $1 AND company_id = $2`, id, scope.CompanyID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That commission scheme was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
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
	       c.category_id, c.brand_id, c.variant_id,
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
		&c.StoreID, &c.CategoryID, &c.BrandID, &c.VariantID,
		&rate, &c.Tiers, &from, &to); err != nil {
		return CommissionRule{}, err
	}
	c.Rate = rate.String()
	c.From = from.Format("2006-01-02")
	if to != nil {
		c.To = to.Format("2006-01-02")
	}
	return c, nil
}
