package fiscal

// The year-end closing routine (blueprint C10).
//
//	"Year-end closing routine: verify trial balance, post adjusting entries,
//	 close revenue and expense accounts into Retained Earnings, roll balances
//	 forward, generate the closing financial statement pack, and lock the year."
//
// # What closing a year actually does
//
// Revenue and expense accounts measure a PERIOD. At the end of the year their
// balances have said what they have to say, and the profit they represent
// belongs to the owners — so each is emptied into Retained Earnings by one
// entry, and the new year starts them at nil.
//
// Balance sheet accounts are not touched. Cash does not reset in January; it
// carries. "Roll balances forward" is what happens to them, and it happens by
// doing nothing, because this ledger is cumulative and every statement is drawn
// from it by date.
//
// # The three refusals
//
// The routine will not run unless the trial balance balances, every period in
// the year is closed, and the year has not already been closed. Each is a
// different kind of wrong and each is stated separately, because "cannot close
// the year" with no reason is the least actionable message a product can give
// somebody at the one moment of the year when they are under time pressure.
//
// # Why the closing entry is built by hand
//
// Every other posting in this product goes through a rule, and that is right:
// a rule is versioned data an auditor can be shown. This one cannot. A closing
// entry has one line per revenue and expense account the company happens to
// have, which is a number the rule engine's `for_each` could express only if
// something first worked out what to put in the group — and that something
// would be this code, holding the whole entry, at which point the rule adds a
// layer and no meaning.
//
// So it is posted through `accounting.Post` with explicit lines, and
// `source_type = 'year_end_close'` makes it findable and idempotent.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// YearEnd is what closing a year did.
type YearEnd struct {
	Year int `json:"fiscal_year"`

	// ClosedOn is the last day of the year, which is the date the closing entry
	// carries. Not today: an entry dated in the new year would put last year's
	// profit into this year's figures.
	ClosedOn string `json:"closed_on"`

	// Revenue and Expenses are what was cleared out, as positive figures.
	Revenue  string `json:"revenue_closed"`
	Expenses string `json:"expenses_closed"`

	// Profit is revenue less expenses — what moved to Retained Earnings.
	// Negative for a loss, which is a fact rather than a failure.
	Profit string `json:"profit_to_retained_earnings"`

	Accounts int    `json:"accounts_closed"`
	EntryNo  string `json:"entry_no,omitempty"`
	Currency string `json:"currency"`

	// AlreadyClosed marks a replay: the year was closed before and this is what
	// that close did.
	AlreadyClosed bool `json:"already_closed,omitempty"`
}

// CloseYear runs C10's year-end routine.
func (s *Service) CloseYear(
	ctx context.Context, scope Scope, year int,
) (YearEnd, error) {
	var out YearEnd
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// --- the three refusals ------------------------------------------

		var total, closed, locked int
		var lastDay time.Time
		if e := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE state <> 'open'),
			       count(*) FILTER (WHERE state = 'locked'),
			       max(ends_on)
			FROM fiscal_period
			WHERE company_id = $1 AND fiscal_year = $2`,
			scope.CompanyID, year).Scan(&total, &closed, &locked, &lastDay); e != nil {
			return e
		}

		if total == 0 {
			return errs.Newf(errs.CodeNotFound,
				"There is no accounting year %d to close.", year)
		}
		if locked == total {
			// Already done. Reported as what it did rather than as an error:
			// two people pressing the button on the last day of the year is
			// the ordinary case, and the second deserves the same answer.
			read, e := s.readClose(ctx, tx, scope, year, lastDay)
			if e != nil {
				return e
			}
			read.AlreadyClosed = true
			out = read
			return nil
		}
		if closed < total {
			return errs.Newf(errs.CodeConflict,
				"%d of the %d months in %d are still open. Close them in order "+
					"first — the year-end entry is drawn from figures that must "+
					"not be able to move afterwards.",
				total-closed, total, year)
		}

		// The trial balance, checked before anything is posted. Closing a year
		// whose books do not balance would carry the imbalance into Retained
		// Earnings, where it becomes part of the opening position of every year
		// that follows and is very much harder to find.
		var difference decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN account a ON a.id = l.account_id
			WHERE a.company_id = $1`, scope.CompanyID).Scan(&difference); e != nil {
			return e
		}
		if !difference.IsZero() {
			return errs.Newf(errs.CodeConflict,
				"The books do not balance: debits exceed credits by %s. A "+
					"year-end close would carry that difference into Retained "+
					"Earnings, where it becomes part of every year afterwards.",
				difference.StringFixed(2))
		}

		// --- what is being closed ----------------------------------------

		type balance struct {
			id      uuid.UUID
			code    string
			name    string
			kind    string
			balance decimal.Decimal
		}

		rows, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, a.type,
			       coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM account a
			LEFT JOIN journal_line l ON l.account_id = a.id
			LEFT JOIN journal_entry en ON en.id = l.entry_id
			                          AND en.entry_date <= $2
			WHERE a.company_id = $1 AND a.type IN ('revenue', 'expense')
			GROUP BY a.id, a.code, a.name, a.type
			HAVING coalesce(sum(l.base_debit - l.base_credit), 0) <> 0
			ORDER BY a.code`, scope.CompanyID, lastDay)
		if e != nil {
			return e
		}
		var balances []balance
		for rows.Next() {
			var b balance
			if e := rows.Scan(&b.id, &b.code, &b.name, &b.kind, &b.balance); e != nil {
				rows.Close()
				return e
			}
			balances = append(balances, b)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		// Revenue is credit-normal, so its ledger balance is negative;
		// expense is debit-normal and positive. Reported as the positive
		// figures a person expects to read.
		revenue, expenses := decimal.Zero, decimal.Zero
		lines := make([]accounting.Line, 0, len(balances)+1)
		for _, b := range balances {
			if b.kind == "revenue" {
				revenue = revenue.Sub(b.balance)
			} else {
				expenses = expenses.Add(b.balance)
			}

			// Each account is posted the exact opposite of what it holds,
			// which leaves it at nil. Never a computed "should be" figure:
			// the opposite of the balance is the only amount that empties it.
			account := b.id
			side := accounting.Credit
			amount := b.balance
			if amount.IsNegative() {
				side = accounting.Debit
				amount = amount.Neg()
			}
			lines = append(lines, accounting.Line{
				AccountID: &account, Side: side, Amount: amount,
				Memo: "Closing " + b.name,
			})
		}

		profit := revenue.Sub(expenses)

		if len(lines) == 0 {
			// A year with no revenue and no expenses. Nothing to close, and the
			// year is locked anyway — a company that traded not at all still
			// has a year that is over.
			if e := lockYear(ctx, tx, scope, year); e != nil {
				return e
			}
			out = YearEnd{
				Year: year, ClosedOn: lastDay.Format("2006-01-02"),
				Revenue: "0.00", Expenses: "0.00", Profit: "0.00",
			}
			return readCurrencyInto(ctx, tx, scope.CompanyID, &out.Currency)
		}

		// And the other side: the profit, into Retained Earnings.
		//
		// Credit for a profit, debit for a loss. Written as a role rather than
		// an account id, because unlike the revenue and expense accounts this
		// one IS a fixed position in the chart and 0025's vocabulary already
		// names it.
		reSide := accounting.Credit
		reAmount := profit
		if profit.IsNegative() {
			reSide = accounting.Debit
			reAmount = profit.Neg()
		}
		if !reAmount.IsZero() {
			lines = append(lines, accounting.Line{
				Role: "retained_earnings", Side: reSide, Amount: reAmount,
				Memo: fmt.Sprintf("Result for %d", year),
			})
		}

		result, e := accounting.Post(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       lastDay,
			SourceType: "year_end_close",
			// The year is the identity. Deterministic from the company and the
			// year so a second attempt finds the first rather than posting a
			// second closing entry, which would double the profit in Retained
			// Earnings and be extremely hard to spot afterwards.
			SourceID: closeID(scope.CompanyID, year),
			PostedBy: &scope.UserID,
			Memo:     fmt.Sprintf("Year-end close %d", year),
			Lines:    lines,
		})
		if e != nil {
			return e
		}

		if e := lockYear(ctx, tx, scope, year); e != nil {
			return e
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "year_closed",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"fiscal_year": year,
				"profit":      profit.StringFixed(2),
				"accounts":    len(balances),
			},
		}); e != nil {
			return e
		}

		out = YearEnd{
			Year: year, ClosedOn: lastDay.Format("2006-01-02"),
			Revenue: revenue.StringFixed(2), Expenses: expenses.StringFixed(2),
			Profit: profit.StringFixed(2), Accounts: len(balances),
			EntryNo: fmt.Sprintf("%d", result.EntryNo),
		}
		return readCurrencyInto(ctx, tx, scope.CompanyID, &out.Currency)
	})
	return out, err
}

// lockYear puts every period of the year beyond reopening.
//
// Locked rather than closed, and the difference matters: `Reopen` refuses a
// locked period by name, because the revenue and expense accounts behind it
// have been emptied and putting a transaction back would leave the closing
// entry wrong with nothing saying so.
func lockYear(
	ctx context.Context, tx pgx.Tx, scope Scope, year int,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE fiscal_period SET state = 'locked'
		WHERE company_id = $1 AND fiscal_year = $2 AND state = 'closed'`,
		scope.CompanyID, year)
	return err
}

// closeID is the identity of one company's close of one year.
//
// Derived rather than random, so a second attempt at the same close finds the
// first through the posting engine's own idempotency key. A random id would
// post a second closing entry, which doubles the profit carried into Retained
// Earnings — an error that balances, survives every check this product makes,
// and is found a year later by an accountant.
func closeID(companyID uuid.UUID, year int) uuid.UUID {
	return uuid.NewSHA1(companyID, []byte(fmt.Sprintf("year_end_close:%d", year)))
}

// readClose reports a close that already happened.
func (s *Service) readClose(
	ctx context.Context, tx pgx.Tx, scope Scope, year int, lastDay time.Time,
) (YearEnd, error) {
	out := YearEnd{Year: year, ClosedOn: lastDay.Format("2006-01-02")}

	var entryNo *int64
	var profit decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT e.entry_no,
		       coalesce((SELECT sum(l.base_credit - l.base_debit)
		                 FROM journal_line l
		                 JOIN account a ON a.id = l.account_id
		                 JOIN account_role_map m ON m.account_id = a.id
		                 WHERE l.entry_id = e.id AND m.role = 'retained_earnings'), 0)
		FROM journal_entry e
		WHERE e.company_id = $1 AND e.source_type = 'year_end_close'
		  AND e.source_id = $2`,
		scope.CompanyID, closeID(scope.CompanyID, year)).Scan(&entryNo, &profit)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return YearEnd{}, err
	}
	if entryNo != nil {
		out.EntryNo = fmt.Sprintf("%d", *entryNo)
	}
	out.Profit = profit.StringFixed(2)
	out.Revenue = "0.00"
	out.Expenses = "0.00"
	return out, readCurrencyInto(ctx, tx, scope.CompanyID, &out.Currency)
}

func readCurrencyInto(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, into *string,
) error {
	e := tx.QueryRow(ctx,
		`SELECT base_currency FROM company WHERE id = $1`, companyID).Scan(into)
	if errors.Is(e, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return e
}
