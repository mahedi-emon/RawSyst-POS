// Exchange rates (blueprint G2).
//
// A book is kept in one currency and a business may trade in several. Every
// figure that reaches the ledger is translated into the company's base
// currency, and this is where the number doing the translating comes from.
//
// Before 0113 it came from nowhere: every caller passed 1, and the sale path
// took whatever the till sent. A EUR invoice landed in a SAR book at par.
package fx

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service resolves and records exchange rates.
type Service struct{ pool *db.Pool }

// New builds the service.
func New(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking.
type Scope struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// Rate is one pair's rate on one day, as recorded.
type Rate struct {
	ID     uuid.UUID       `json:"id"`
	From   string          `json:"from_currency"`
	To     string          `json:"to_currency"`
	Rate   decimal.Decimal `json:"rate"`
	AsOf   string          `json:"as_of"`
	Source string          `json:"source"`
	Note   string          `json:"note,omitempty"`
}

// RateOn resolves the rate for a pair on a date.
//
// # The rate in force on the day, not today's
//
// An invoice is translated at the rate that applied when it was issued and
// keeps it for ever. Resolving at today's rate would make a reprint disagree
// with the original and would quietly restate closed periods every time
// somebody entered a new rate.
//
// The latest rate not AFTER the date wins, so a shop that enters a rate weekly
// is not obliged to enter one for a day it did not trade.
//
// # A pair with no rate refuses
//
// It does not fall back to 1. A missing rate is a setup step somebody can take;
// treating it as par would put a wrong figure in the ledger with nothing
// anywhere to indicate it, which is the same judgement the tax registry makes
// about an unrecorded rate.
//
// A currency against itself is 1 without consulting anything, because that is
// arithmetic rather than a market fact.
func (s *Service) RateOn(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, from, to string,
	on time.Time,
) (decimal.Decimal, error) {
	from, to = normalise(from), normalise(to)
	if from == "" || to == "" {
		return decimal.Zero, errs.New(errs.CodeInvalidInput,
			"Name both currencies to convert between.")
	}
	if from == to {
		return decimal.NewFromInt(1), nil
	}

	run := func(q pgx.Tx) (decimal.Decimal, error) {
		var rate decimal.Decimal
		err := q.QueryRow(ctx, `
			SELECT rate FROM exchange_rate
			WHERE tenant_id = $1 AND from_currency = $2 AND to_currency = $3
			  AND as_of <= $4::date
			ORDER BY as_of DESC
			LIMIT 1`, tenantID, from, to, on).Scan(&rate)
		if err == pgx.ErrNoRows {
			// Try the pair the other way up before giving in: a shop that
			// recorded SAR->USD has said what USD->SAR is, and asking them to
			// enter both directions would invite the two to disagree.
			var inverse decimal.Decimal
			e := q.QueryRow(ctx, `
				SELECT rate FROM exchange_rate
				WHERE tenant_id = $1 AND from_currency = $2 AND to_currency = $3
				  AND as_of <= $4::date
				ORDER BY as_of DESC
				LIMIT 1`, tenantID, to, from, on).Scan(&inverse)
			if e == nil && inverse.IsPositive() {
				return decimal.NewFromInt(1).Div(inverse), nil
			}
			return decimal.Zero, errs.Newf(errs.CodeUnverifiedRule,
				"No exchange rate from %s to %s is on file for %s. Enter the "+
					"rate with its source before booking in that currency — "+
					"a rate nobody recorded is not one this can assume.",
				from, to, on.Format("2 January 2006"))
		}
		if err != nil {
			return decimal.Zero, db.Translate(err, "")
		}
		return rate, nil
	}

	if tx != nil {
		return run(tx)
	}
	var out decimal.Decimal
	err := s.pool.TxAsTenant(ctx, tenantID, func(t pgx.Tx) error {
		var e error
		out, e = run(t)
		return e
	})
	return out, err
}

// Record enters or corrects a day's rate for a pair.
func (s *Service) Record(
	ctx context.Context, scope Scope, from, to string, rate decimal.Decimal,
	asOf time.Time, source, note string,
) (Rate, error) {
	from, to = normalise(from), normalise(to)
	if from == "" || to == "" {
		return Rate{}, errs.Validation("Name both currencies.").
			WithField("from_currency", "Three letters, as on a bank statement.")
	}
	if from == to {
		return Rate{}, errs.New(errs.CodeInvalidInput,
			"A currency is worth one of itself; there is no rate to record.")
	}
	if !rate.IsPositive() {
		return Rate{}, errs.Validation("A rate is a positive number.").
			WithField("rate", "How many units of the second currency one of "+
				"the first buys.")
	}
	if strings.TrimSpace(source) == "" {
		return Rate{}, errs.Validation("Say where the rate came from.").
			WithField("source", "A bank, a central bank reference, or the "+
				"rate agreed with the customer — so somebody reading the "+
				"books later can tell.")
	}

	var out Rate
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO exchange_rate
			  (tenant_id, from_currency, to_currency, rate, as_of, source,
			   note, entered_by)
			VALUES ($1,$2,$3,$4,$5,$6,nullif($7,''),$8)
			ON CONFLICT (tenant_id, from_currency, to_currency, as_of)
			DO UPDATE SET rate = excluded.rate, source = excluded.source,
			              note = excluded.note, entered_by = excluded.entered_by
			RETURNING id, from_currency, to_currency, rate,
			          to_char(as_of, 'YYYY-MM-DD'), source, coalesce(note,'')`,
			scope.TenantID, from, to, rate, asOf, strings.TrimSpace(source),
			strings.TrimSpace(note), scope.UserID).
			Scan(&out.ID, &out.From, &out.To, &out.Rate, &out.AsOf,
				&out.Source, &out.Note)
	})
	return out, db.Translate(err, "That exchange rate could not be saved.")
}

// Rates lists what has been recorded, most recent first.
func (s *Service) Rates(ctx context.Context, scope Scope) ([]Rate, error) {
	out := []Rate{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, from_currency, to_currency, rate,
			       to_char(as_of, 'YYYY-MM-DD'), source, coalesce(note,'')
			FROM exchange_rate
			WHERE tenant_id = $1
			ORDER BY as_of DESC, from_currency, to_currency
			LIMIT 500`, scope.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r Rate
			if e := rows.Scan(&r.ID, &r.From, &r.To, &r.Rate, &r.AsOf,
				&r.Source, &r.Note); e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

func normalise(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
