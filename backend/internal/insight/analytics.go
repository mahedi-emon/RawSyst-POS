package insight

// Business analytics and forecasting (blueprint D2).
//
// # Every figure here is derived, and none of it is stored
//
// D2 asks for fast movers, dead stock, reorder prediction, a sales forecast,
// profitability and thirteen KPIs. Not one of those is a fact somebody records;
// each is a question about facts that already exist. Materialising them would
// create a second copy of the shop's numbers, and the day it drifted from the
// ledger there would be no way to say which was right.
//
// So this is queries. They are slower than a cached table would be, and that is
// the correct trade for figures an owner makes decisions on.
//
// # Velocity is units a day, over a window somebody chose
//
// The whole module rests on it: fast-moving, dead, reorder date and forecast are
// all velocity read four ways. It is measured over a window the caller names
// rather than all history, because a shirt that sold two hundred last winter and
// none since is not a fast mover, and the average over two years says it is.
//
// # A forecast is arithmetic, and it says so
//
// D2 asks for a "historical-sales-based demand estimate (architecture-ready for
// future ML models)". This is the historical part: velocity times days, with the
// window it was measured over reported alongside. It is not dressed up as a
// prediction, because an owner ordering stock against a number needs to know it
// is last month repeated rather than a model that considered anything else.

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Movement is one product and how it has been selling.
type Movement struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`
	Category  string    `json:"category,omitempty"`
	Brand     string    `json:"brand,omitempty"`

	SoldQty string `json:"sold_qty"`
	Revenue string `json:"revenue"`
	Profit  string `json:"profit"`
	OnHand  string `json:"on_hand"`

	// Velocity is units sold per day over the window. The number every other
	// figure in this module is read off.
	Velocity string `json:"velocity"`

	// DaysCover is how long the stock on hand lasts at that rate. Null when
	// nothing has sold — a shelf that has not moved does not have a number of
	// days, and reporting a very large one would read as "plenty left".
	DaysCover *int `json:"days_cover,omitempty"`

	// ReorderOn is D2's reorder prediction: the day stock reaches the level
	// the shop set. Empty when there is no reorder level, or nothing is
	// selling, because both mean there is nothing to predict.
	ReorderOn string `json:"reorder_on,omitempty"`

	// DaysSinceSold is what makes something dead stock. −1 when it has never
	// sold at all, which is worse than old and must not sort as recent.
	DaysSinceSold int `json:"days_since_sold"`

	Currency string `json:"currency"`
}

// KPIs are D2's owner dashboard figures.
type KPIs struct {
	From string `json:"from"`
	To   string `json:"to"`

	Revenue     string `json:"revenue"`
	GrossProfit string `json:"gross_profit"`
	GrossMargin string `json:"gross_margin_pct"`

	Orders         int    `json:"orders"`
	AverageOrder   string `json:"average_order_value"`
	UnitsPerOrder  string `json:"units_per_transaction"`
	DiscountRatio  string `json:"discount_ratio_pct"`
	ReturnRate     string `json:"return_rate_pct"`
	InventoryTurn  string `json:"inventory_turnover"`
	RepeatRate     string `json:"repeat_customer_pct"`
	CustomerValue  string `json:"customer_lifetime_value"`
	SalesPerStore  string `json:"sales_per_store"`
	SalesPerPerson string `json:"sales_per_employee"`
	Currency       string `json:"currency"`
}

// Ranked is one category, brand or product by what it actually contributed.
type Ranked struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Revenue string `json:"revenue"`
	Cost    string `json:"cost"`
	Profit  string `json:"profit"`
	// Margin is the ratio, which is what ranks a line rather than the absolute
	// profit: a category turning over ten million at two per cent is a
	// different business decision from one turning over a million at thirty.
	Margin   string `json:"margin_pct"`
	Units    string `json:"units"`
	Currency string `json:"currency"`
}

// Movers answers "what is selling and what is not".
//
// One query for both ends of D2's list, because fast-moving and dead stock are
// the same measurement sorted differently, and two queries would be two
// definitions of velocity free to disagree.
func (s *Service) Movers(
	ctx context.Context, scope Scope, days int,
) ([]Movement, error) {
	if days <= 0 || days > 730 {
		days = 90
	}

	out := []Movement{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			WITH sold AS (
			  SELECT l.variant_id,
			         sum(CASE WHEN i.doc_type = 'credit_note'
			                  THEN -l.qty ELSE l.qty END) AS qty,
			         sum(CASE WHEN i.doc_type = 'credit_note'
			                  THEN -l.net_amount ELSE l.net_amount END) AS revenue,
			         sum(CASE WHEN i.doc_type = 'credit_note'
			                  THEN -l.cogs_amount ELSE l.cogs_amount END) AS cost,
			         max(i.issued_at) FILTER (
			           WHERE i.doc_type <> 'credit_note') AS last_sold
			  FROM sales_invoice_line l
			  JOIN sales_invoice i ON i.id = l.invoice_id
			  WHERE i.company_id = $1
			    AND i.issued_at >= now() - make_interval(days => $2)
			  GROUP BY l.variant_id
			),
			held AS (
			  SELECT m.variant_id, sum(m.delta) AS on_hand
			  FROM stock_movement m
			  WHERE m.company_id = $1
			  GROUP BY m.variant_id
			)
			SELECT v.id, v.sku, p.name,
			       coalesce(c.name, ''), coalesce(b.name, ''),
			       coalesce(s.qty, 0), coalesce(s.revenue, 0),
			       coalesce(s.revenue, 0) - coalesce(s.cost, 0),
			       coalesce(h.on_hand, 0),
			       v.reorder_level,
			       coalesce(extract(day FROM now() - s.last_sold)::int, -1)
			FROM variant v
			JOIN product p ON p.id = v.product_id
			LEFT JOIN category c ON c.id = p.category_id
			LEFT JOIN brand b ON b.id = p.brand_id
			LEFT JOIN sold s ON s.variant_id = v.id
			LEFT JOIN held h ON h.variant_id = v.id
			WHERE v.company_id = $1 AND v.is_active
			ORDER BY coalesce(s.qty, 0) DESC, p.name
			LIMIT 500`, scope.CompanyID, days)
		if e != nil {
			return e
		}
		defer rows.Close()

		window := decimal.NewFromInt(int64(days))
		for rows.Next() {
			var m Movement
			var qty, revenue, profit, onHand decimal.Decimal
			var reorder *decimal.Decimal
			if e := rows.Scan(&m.VariantID, &m.SKU, &m.Product, &m.Category,
				&m.Brand, &qty, &revenue, &profit, &onHand, &reorder,
				&m.DaysSinceSold); e != nil {
				return e
			}

			m.Currency = currency
			m.SoldQty = qty.String()
			m.Revenue = revenue.StringFixed(2)
			m.Profit = profit.StringFixed(2)
			m.OnHand = onHand.String()

			velocity := decimal.Zero
			if qty.IsPositive() {
				velocity = qty.Div(window)
			}
			m.Velocity = velocity.Round(4).String()

			if velocity.IsPositive() && onHand.IsPositive() {
				cover := int(onHand.Div(velocity).IntPart())
				m.DaysCover = &cover

				// D2's reorder prediction. Only when the shop has SET a level:
				// predicting a date against a level nobody chose would be this
				// module inventing the shop's policy.
				if reorder != nil && onHand.GreaterThan(*reorder) {
					until := onHand.Sub(*reorder).Div(velocity)
					m.ReorderOn = time.Now().UTC().
						AddDate(0, 0, int(until.IntPart())).Format("2006-01-02")
				}
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Forecast is D2's demand estimate for one product.
type Forecast struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`

	WindowDays   int    `json:"window_days"`
	SoldInWindow string `json:"sold_in_window"`
	Velocity     string `json:"velocity"`

	ForecastDays int    `json:"forecast_days"`
	Expected     string `json:"expected_demand"`
	OnHand       string `json:"on_hand"`
	// Shortfall is what the shop would need to buy to meet it. Zero rather
	// than negative when there is enough: "you are 40 short" and "you have 40
	// spare" are different sentences and this field only says the first.
	Shortfall string `json:"shortfall"`

	// Basis says out loud what this number is. An owner ordering against a
	// forecast has to know it is last month repeated rather than a model that
	// considered a season, a promotion or the weather.
	Basis string `json:"basis"`
}

// Forecasts estimate demand over the coming period.
func (s *Service) Forecasts(
	ctx context.Context, scope Scope, windowDays, forecastDays int,
) ([]Forecast, error) {
	if windowDays <= 0 || windowDays > 730 {
		windowDays = 90
	}
	if forecastDays <= 0 || forecastDays > 365 {
		forecastDays = 30
	}

	movers, err := s.Movers(ctx, scope, windowDays)
	if err != nil {
		return nil, err
	}

	out := make([]Forecast, 0, len(movers))
	for _, m := range movers {
		velocity, _ := decimal.NewFromString(m.Velocity)
		if !velocity.IsPositive() {
			// Nothing sold in the window. A forecast of zero is true and
			// useless, and listing it would bury the products that do move.
			continue
		}
		expected := velocity.Mul(decimal.NewFromInt(int64(forecastDays))).
			Ceil()
		onHand, _ := decimal.NewFromString(m.OnHand)
		shortfall := expected.Sub(onHand)
		if shortfall.IsNegative() {
			shortfall = decimal.Zero
		}

		out = append(out, Forecast{
			VariantID: m.VariantID, SKU: m.SKU, Product: m.Product,
			WindowDays: windowDays, SoldInWindow: m.SoldQty,
			Velocity: m.Velocity, ForecastDays: forecastDays,
			Expected: expected.String(), OnHand: m.OnHand,
			Shortfall: shortfall.String(),
			Basis: "sales over the last " + decimal.NewFromInt(
				int64(windowDays)).String() + " days, repeated",
		})
	}
	return out, nil
}

// Ranking is profitability by category, brand or product.
func (s *Service) Ranking(
	ctx context.Context, scope Scope, by string, from, to time.Time,
) ([]Ranked, error) {
	group := `coalesce(c.id::text, ''), coalesce(c.name, 'Uncategorised')`
	switch by {
	case "brand":
		group = `coalesce(b.id::text, ''), coalesce(b.name, 'No brand')`
	case "product":
		group = `p.id::text, p.name`
	}

	out := []Ranked{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}

		// Credit notes are subtracted rather than excluded. A category whose
		// goods mostly come back is not a profitable category, and ranking on
		// gross sales would put it at the top.
		rows, e := tx.Query(ctx, `
			SELECT `+group+`,
			       sum(CASE WHEN i.doc_type = 'credit_note'
			                THEN -l.net_amount ELSE l.net_amount END) AS revenue,
			       sum(CASE WHEN i.doc_type = 'credit_note'
			                THEN -l.cogs_amount ELSE l.cogs_amount END) AS cost,
			       sum(CASE WHEN i.doc_type = 'credit_note'
			                THEN -l.qty ELSE l.qty END) AS units
			FROM sales_invoice_line l
			JOIN sales_invoice i ON i.id = l.invoice_id
			LEFT JOIN variant v ON v.id = l.variant_id
			LEFT JOIN product p ON p.id = v.product_id
			LEFT JOIN category c ON c.id = p.category_id
			LEFT JOIN brand b ON b.id = p.brand_id
			WHERE i.company_id = $1
			  AND i.issued_at >= $2 AND i.issued_at < $3
			GROUP BY 1, 2
			ORDER BY 3 DESC
			LIMIT 100`, scope.CompanyID, from, to)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var r Ranked
			var revenue, cost, units decimal.Decimal
			if e := rows.Scan(&r.ID, &r.Label, &revenue, &cost,
				&units); e != nil {
				return e
			}
			profit := revenue.Sub(cost)
			r.Revenue = revenue.StringFixed(2)
			r.Cost = cost.StringFixed(2)
			r.Profit = profit.StringFixed(2)
			r.Units = units.String()
			r.Currency = currency
			if revenue.IsPositive() {
				r.Margin = profit.Div(revenue).
					Mul(decimal.NewFromInt(100)).StringFixed(1)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}
