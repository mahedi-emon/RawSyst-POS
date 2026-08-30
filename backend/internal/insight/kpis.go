package insight

// The thirteen figures D2 puts on the owner's dashboard.
//
// # One query, because they are one question
//
// Revenue, gross profit and margin come off the same rows; average order value
// and units per transaction come off the same count. Thirteen queries would
// read the invoice lines thirteen times and could return figures that do not
// add up — an average order value that does not divide the revenue by the
// orders, because the two ran a second apart and a sale landed between them.
//
// # A ratio with nothing underneath it is empty, not zero
//
// A shop with no sales in the period has no gross margin. Reporting 0.0% would
// put a number on a dashboard that reads as "we made nothing on everything we
// sold", which is a different and alarming statement. Empty is the honest one.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Dashboard computes D2's KPIs for a period.
func (s *Service) Dashboard(
	ctx context.Context, scope Scope, from, to time.Time,
) (KPIs, error) {
	out := KPIs{
		From: from.UTC().Format("2006-01-02"),
		To:   to.UTC().Format("2006-01-02"),
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.Currency); e != nil {
			return e
		}

		var revenue, cost, discount, units, returned decimal.Decimal
		var orders, creditNotes int
		if e := tx.QueryRow(ctx, `
			SELECT
			  coalesce(sum(CASE WHEN i.doc_type = 'credit_note'
			                    THEN -l.net_amount ELSE l.net_amount END), 0),
			  coalesce(sum(CASE WHEN i.doc_type = 'credit_note'
			                    THEN -l.cogs_amount ELSE l.cogs_amount END), 0),
			  coalesce(sum(l.line_discount + l.invoice_discount_alloc)
			           FILTER (WHERE i.doc_type <> 'credit_note'), 0),
			  coalesce(sum(l.qty) FILTER (WHERE i.doc_type <> 'credit_note'), 0),
			  coalesce(sum(l.net_amount)
			           FILTER (WHERE i.doc_type = 'credit_note'), 0),
			  count(DISTINCT i.id) FILTER (WHERE i.doc_type <> 'credit_note'),
			  count(DISTINCT i.id) FILTER (WHERE i.doc_type = 'credit_note')
			FROM sales_invoice_line l
			JOIN sales_invoice i ON i.id = l.invoice_id
			WHERE i.company_id = $1
			  AND i.issued_at >= $2 AND i.issued_at < $3`,
			scope.CompanyID, from, to).
			Scan(&revenue, &cost, &discount, &units, &returned, &orders,
				&creditNotes); e != nil {
			return e
		}

		profit := revenue.Sub(cost)
		out.Revenue = revenue.StringFixed(2)
		out.GrossProfit = profit.StringFixed(2)
		out.Orders = orders

		if revenue.IsPositive() {
			out.GrossMargin = percent(profit, revenue)
			// The discount ratio measures what was given away against what the
			// shop would have taken, so the denominator includes the discount.
			out.DiscountRatio = percent(discount, revenue.Add(discount))
			out.ReturnRate = percent(returned, revenue.Add(returned))
		}
		if orders > 0 {
			count := decimal.NewFromInt(int64(orders))
			out.AverageOrder = revenue.Div(count).StringFixed(2)
			out.UnitsPerOrder = units.Div(count).Round(2).String()
		}

		// Inventory turnover: cost of goods sold over the stock held. Held
		// rather than average-held, and the difference is worth naming — a
		// proper average needs an opening and a closing valuation, and this
		// product records movements rather than period snapshots. Against
		// today's holding it is the figure an owner can act on: how many times
		// over the shelf turned.
		var stockValue decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(value_delta), 0) FROM stock_movement
			WHERE company_id = $1`, scope.CompanyID).Scan(&stockValue); e != nil {
			return e
		}
		if stockValue.IsPositive() && cost.IsPositive() {
			out.InventoryTurn = cost.Div(stockValue).Round(2).String()
		}

		// Repeat-customer rate and lifetime value. Over the customers who
		// bought IN THE PERIOD, because "of the people who came in this month,
		// how many had been before" is the question an owner is asking.
		var buyers, repeat int
		var lifetime decimal.Decimal
		if e := tx.QueryRow(ctx, `
			WITH period_buyers AS (
			  SELECT DISTINCT i.customer_id
			  FROM sales_invoice i
			  WHERE i.company_id = $1 AND i.customer_id IS NOT NULL
			    AND i.doc_type <> 'credit_note'
			    AND i.issued_at >= $2 AND i.issued_at < $3
			),
			history AS (
			  SELECT i.customer_id, count(*) AS visits,
			         sum(i.total_inclusive) AS spend
			  FROM sales_invoice i
			  JOIN period_buyers b ON b.customer_id = i.customer_id
			  WHERE i.company_id = $1 AND i.doc_type <> 'credit_note'
			  GROUP BY i.customer_id
			)
			SELECT count(*)::int,
			       count(*) FILTER (WHERE visits > 1)::int,
			       coalesce(avg(spend), 0)
			FROM history`, scope.CompanyID, from, to).
			Scan(&buyers, &repeat, &lifetime); e != nil {
			return e
		}
		if buyers > 0 {
			out.RepeatRate = percent(decimal.NewFromInt(int64(repeat)),
				decimal.NewFromInt(int64(buyers)))
			out.CustomerValue = lifetime.StringFixed(2)
		}

		// Per store and per person. Divided by the stores that TRADED and the
		// people employed in the period, not by every row in the table: a
		// company with four branches of which one opened yesterday would
		// otherwise report a quarter of its takings per branch.
		var stores, staff int
		if e := tx.QueryRow(ctx, `
			SELECT
			  (SELECT count(DISTINCT i.store_id)::int FROM sales_invoice i
			   WHERE i.company_id = $1 AND i.issued_at >= $2 AND i.issued_at < $3),
			  (SELECT count(*)::int FROM employee e
			   WHERE e.company_id = $1 AND e.joined_on < $3
			     AND (e.left_on IS NULL OR e.left_on >= $2))`,
			scope.CompanyID, from, to).Scan(&stores, &staff); e != nil {
			return e
		}
		if stores > 0 {
			out.SalesPerStore = revenue.
				Div(decimal.NewFromInt(int64(stores))).StringFixed(2)
		}
		if staff > 0 {
			out.SalesPerPerson = revenue.
				Div(decimal.NewFromInt(int64(staff))).StringFixed(2)
		}
		return nil
	})
	return out, db.Translate(err, "")
}

// percent renders a ratio to one decimal place.
//
// Returns empty rather than "0.0" when there is nothing to divide by, so a
// dashboard shows a dash rather than a figure that reads as a measurement.
func percent(part, whole decimal.Decimal) string {
	if !whole.IsPositive() {
		return ""
	}
	return part.Div(whole).Mul(decimal.NewFromInt(100)).StringFixed(1)
}
