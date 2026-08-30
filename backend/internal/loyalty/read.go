package loyalty

// Reading the scheme, the members and what each of them is worth.

import (
	"context"
	"encoding/json"
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

// Program reads the scheme and what it currently owes.
func (s *Service) Program(ctx context.Context, scope Scope) (Program, error) {
	var out Program
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		p, e := Settings(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&p.Currency); e != nil {
			return e
		}

		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(points), 0)::int
			FROM loyalty_entry WHERE company_id = $1`, scope.CompanyID).
			Scan(&p.Points); e != nil {
			return e
		}

		owed := decimal.Zero
		if p.Exists {
			value, _ := decimal.NewFromString(p.PointValue)
			owed = value.Mul(decimal.NewFromInt(int64(p.Points)))
		}
		p.Owed = owed.StringFixed(2)

		out = p
		return nil
	})
	return out, err
}

// NewProgram is a scheme being set up or changed.
type NewProgram struct {
	Active        bool
	SpendPerPoint decimal.Decimal
	PointValue    decimal.Decimal
	ExpiryMonths  *int
	Tiers         []Tier
}

// SetProgram writes the scheme.
//
// Changing the rate does not revalue points already earned. Those were posted
// at the value in force when they were earned, and repricing them would move a
// liability that a closed month has already reported.
func (s *Service) SetProgram(
	ctx context.Context, scope Scope, in NewProgram,
) (Program, error) {
	if !in.SpendPerPoint.IsPositive() {
		return Program{}, errs.New(errs.CodeInvalidInput,
			"Say how much has to be spent to earn a point.")
	}
	if !in.PointValue.IsPositive() {
		return Program{}, errs.New(errs.CodeInvalidInput,
			"Say what a point is worth.")
	}
	if in.ExpiryMonths != nil && *in.ExpiryMonths <= 0 {
		return Program{}, errs.New(errs.CodeInvalidInput,
			"Points either expire after some months or they do not expire.")
	}
	for i, t := range in.Tiers {
		if strings.TrimSpace(t.Key) == "" || strings.TrimSpace(t.Name) == "" {
			return Program{}, errs.Newf(errs.CodeInvalidInput,
				"Tier %d needs a name.", i+1)
		}
		if _, err := decimal.NewFromString(strings.TrimSpace(t.MinSpend)); err != nil {
			return Program{}, errs.Newf(errs.CodeInvalidInput,
				"Tier %q does not say what it takes to reach it.", t.Name)
		}
	}

	// An empty list, not null. A scheme with no tiers is a scheme with no
	// tiers; `json.Marshal` of a nil slice is `null`, which the column refuses
	// because a null there would mean "unknown" rather than "none".
	rungs := in.Tiers
	if rungs == nil {
		rungs = []Tier{}
	}
	tiers, err := json.Marshal(rungs)
	if err != nil {
		return Program{}, err
	}

	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			INSERT INTO loyalty_program
			  (tenant_id, company_id, is_active, spend_per_point, point_value,
			   expiry_months, tiers, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (company_id) DO UPDATE SET
			  is_active = EXCLUDED.is_active,
			  spend_per_point = EXCLUDED.spend_per_point,
			  point_value = EXCLUDED.point_value,
			  expiry_months = EXCLUDED.expiry_months,
			  tiers = EXCLUDED.tiers,
			  updated_by = EXCLUDED.updated_by`,
			scope.TenantID, scope.CompanyID, in.Active, in.SpendPerPoint,
			in.PointValue, in.ExpiryMonths, tiers, scope.UserID); e != nil {
			return db.Translate(e, "That scheme could not be saved.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "loyalty_program_set",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"is_active":       in.Active,
				"spend_per_point": in.SpendPerPoint.String(),
				"point_value":     in.PointValue.String(),
				"tiers":           len(in.Tiers),
			},
		})
	})
	if err != nil {
		return Program{}, err
	}
	return s.Program(ctx, scope)
}

// Card reads one customer's membership, tier and history.
func (s *Service) Card(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) (Card, error) {
	var out Card
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		program, e := Settings(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		card, e := s.cardFor(ctx, tx, scope, program, customerID)
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT l.id, l.points, l.reason,
			       coalesce(i.human_number, ''), l.spend,
			       coalesce(to_char(l.expires_on, 'YYYY-MM-DD'), ''),
			       coalesce(l.note, ''), coalesce(u.full_name, ''),
			       to_char(l.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
			FROM loyalty_entry l
			LEFT JOIN sales_invoice i ON i.id = l.invoice_id
			LEFT JOIN app_user u ON u.id = l.created_by
			WHERE l.customer_id = $1
			ORDER BY l.created_at DESC
			LIMIT 200`, customerID)
		if e != nil {
			return e
		}
		defer rows.Close()

		card.Entries = []Entry{}
		for rows.Next() {
			var en Entry
			var spend *decimal.Decimal
			if e := rows.Scan(&en.ID, &en.Points, &en.Reason, &en.InvoiceNo,
				&spend, &en.ExpiresOn, &en.Note, &en.CreatedBy,
				&en.CreatedAt); e != nil {
				return e
			}
			if spend != nil {
				en.Spend = spend.StringFixed(2)
			}
			card.Entries = append(card.Entries, en)
		}
		if e := rows.Err(); e != nil {
			return e
		}

		out = card
		return nil
	})
	return out, err
}

// Members lists everybody the scheme knows about, best first.
//
// Ordered by lifetime spend rather than by points: an owner opening this screen
// is looking for their best customers, and points are what those customers have
// not spent yet.
func (s *Service) Members(ctx context.Context, scope Scope) ([]Card, error) {
	out := []Card{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		program, e := Settings(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, historySQL+`
			ORDER BY h.lifetime DESC, c.name
			LIMIT 500`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			card, e := scanCard(rows, program, currency)
			if e != nil {
				return e
			}
			out = append(out, card)
		}
		return rows.Err()
	})
	return out, err
}

// Expire writes back the points whose date has passed.
//
// Expiry is FIFO within a customer: the oldest unspent points go first, which
// is how every scheme a customer has met behaves and the only order that makes
// "your points expire in March" a true statement.
func (s *Service) Expire(ctx context.Context, scope Scope) (int, error) {
	var expired int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		program, e := Settings(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		if !program.Exists {
			return nil
		}
		value, e := decimal.NewFromString(program.PointValue)
		if e != nil {
			return e
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// Per customer: how many points were earned with a date that has passed,
		// less everything that has already been taken off them. A customer who
		// spent their points before they expired has nothing to lose, and a
		// customer who spent some has lost only the rest — which is what
		// subtracting every negative entry from the expired earnings gives,
		// because points are spent oldest first.
		rows, e := tx.Query(ctx, `
			WITH earned_and_gone AS (
			  SELECT customer_id,
			         sum(points) FILTER (
			           WHERE points > 0 AND expires_on IS NOT NULL
			             AND expires_on < current_date)::int AS lapsed,
			         sum(points)::int AS balance
			  FROM loyalty_entry
			  WHERE company_id = $1
			  GROUP BY customer_id
			)
			SELECT customer_id, least(coalesce(lapsed, 0), balance)::int
			FROM earned_and_gone
			WHERE coalesce(lapsed, 0) > 0 AND balance > 0`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		type lapse struct {
			customerID uuid.UUID
			points     int
		}
		var lapsed []lapse
		for rows.Next() {
			var l lapse
			if e := rows.Scan(&l.customerID, &l.points); e != nil {
				return e
			}
			if l.points > 0 {
				lapsed = append(lapsed, l)
			}
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(lapsed) == 0 {
			return nil
		}

		total := 0
		for _, l := range lapsed {
			if _, e := tx.Exec(ctx, `
				INSERT INTO loyalty_entry
				  (tenant_id, company_id, customer_id, points, reason, created_by)
				VALUES ($1,$2,$3,$4,'expired',$5)`,
				scope.TenantID, scope.CompanyID, l.customerID, -l.points,
				scope.UserID); e != nil {
				return db.Translate(e, "Those points could not be expired.")
			}
			total += l.points
		}

		sweepID := uuid.New()
		if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       time.Now().UTC(),
			SourceType: "loyalty_expiry", SourceID: sweepID,
			RuleKey: "loyalty.expire", PostedBy: &scope.UserID,
			Memo: "Loyalty points expired",
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{
				"amount": value.Mul(decimal.NewFromInt(int64(total))),
			},
		}); e != nil {
			return e
		}

		expired = total
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "loyalty_points_expired",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"points": total, "customers": len(lapsed),
			},
		})
	})
	return expired, err
}

func (s *Service) cardFor(
	ctx context.Context, tx pgx.Tx, scope Scope, program Program,
	customerID uuid.UUID,
) (Card, error) {
	var currency string
	if err := tx.QueryRow(ctx,
		`SELECT base_currency FROM company WHERE id = $1`,
		scope.CompanyID).Scan(&currency); err != nil {
		return Card{}, err
	}

	row := tx.QueryRow(ctx, historySQL+` AND c.id = $2`, scope.CompanyID, customerID)
	card, err := scanCard(row, program, currency)
	if err == pgx.ErrNoRows {
		return Card{}, errs.New(errs.CodeNotFound,
			"That customer is not on this company's books.")
	}
	return card, err
}

// scannable is what both a single row and a row from a set can do. It exists so
// one customer and a list of them are read by the same code — two copies of
// this query would eventually give two different answers to "what tier is
// this person".
type scannable interface {
	Scan(dest ...any) error
}

// historySQL is the customer, their points, and what their buying history says
// about them. `$1` is the company.
//
// Credit notes are subtracted rather than ignored: a customer who bought
// forty thousand riyals of clothes and returned thirty-eight has not spent
// forty, and a tier awarded on the gross figure is a tier the shop gave away.
const historySQL = `
	SELECT c.id, c.name, c.customer_type,
	       coalesce(l.points, 0)::int,
	       coalesce(l.expiring, 0)::int,
	       coalesce(h.lifetime, 0),
	       coalesce(h.visits, 0)::int,
	       coalesce(to_char(h.last_purchase, 'YYYY-MM-DD'), ''),
	       coalesce(extract(day FROM now() - h.last_purchase)::int, -1)
	FROM customer c
	LEFT JOIN LATERAL (
	  SELECT sum(points)::int AS points,
	         sum(points) FILTER (
	           WHERE points > 0 AND expires_on IS NOT NULL
	             AND expires_on <= current_date + 90)::int AS expiring
	  FROM loyalty_entry e WHERE e.customer_id = c.id
	) l ON true
	LEFT JOIN LATERAL (
	  SELECT sum(CASE WHEN i.doc_type = 'credit_note'
	                  THEN -i.total_inclusive ELSE i.total_inclusive END) AS lifetime,
	         count(*) FILTER (WHERE i.doc_type <> 'credit_note') AS visits,
	         max(i.issued_at) FILTER (WHERE i.doc_type <> 'credit_note')
	           AS last_purchase
	  FROM sales_invoice i WHERE i.customer_id = c.id
	) h ON true
	WHERE c.company_id = $1`

func scanCard(row scannable, program Program, currency string) (Card, error) {
	var c Card
	var customerType string
	var lifetime decimal.Decimal
	var daysSinceLast int
	if err := row.Scan(&c.CustomerID, &c.Customer, &customerType, &c.Points,
		&c.ExpiringSoon, &lifetime, &c.Visits, &c.LastPurchase,
		&daysSinceLast); err != nil {
		return Card{}, err
	}

	c.Currency = currency
	c.LifetimeSpend = lifetime.StringFixed(2)

	worth := decimal.Zero
	if program.Exists {
		value, _ := decimal.NewFromString(program.PointValue)
		worth = value.Mul(decimal.NewFromInt(int64(c.Points)))
	}
	c.Worth = worth.StringFixed(2)

	at, next, gap := TierFor(program.Tiers, lifetime)
	if at != nil {
		c.Tier = at.Name
		c.Discount = at.Discount
	}
	if next != nil {
		c.NextTier = next.Name
		c.ToNextTier = gap.StringFixed(2)
	}

	// The top rung is what "VIP" means to this shop, so the segment reads the
	// threshold the shop set rather than a number invented here.
	c.Segment = SegmentOf(customerType, c.Visits, lifetime, daysSinceLast,
		highestThreshold(program.Tiers))

	return c, nil
}

func highestThreshold(tiers []Tier) decimal.Decimal {
	highest := decimal.Zero
	for _, t := range tiers {
		if v := thresholdOf(t); v.GreaterThan(highest) {
			highest = v
		}
	}
	return highest
}
