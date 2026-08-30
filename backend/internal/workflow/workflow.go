// Package workflow is the configurable approval engine and the Approval
// Centre (blueprint F1, D5).
//
// # Why the rules are data
//
// F1's argument, in full: "Hard-coding approval rules means every client with a
// slightly different process needs custom development. A configurable engine
// means one codebase serves a 3-person shop and a 300-person chain."
//
// So a rule is a row: a SUBJECT it watches, a CONDITION as jsonb, an ACTION,
// and a list of STEPS naming who decides. Evaluate() reads them; nothing in
// this package knows what an expense or a purchase order is.
//
// # What an approval is allowed to do
//
// It gates. It does not perform. A module calls Evaluate before it commits and
// either proceeds, or records a request and stops — the approval never reaches
// back into the thing it approved to finish the job. That keeps the engine out
// of every module's transaction and means a rule change cannot break a posting
// path it knows nothing about.
package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service owns the approval engine.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Rule is one configured approval rule.
type Rule struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"is_active"`
	Subject   string    `json:"subject"`
	Condition string    `json:"condition"`
	Action    string    `json:"action"`
	Steps     string    `json:"steps"`
	Escalate  *int      `json:"escalate_after_hours,omitempty"`
	Priority  int       `json:"priority"`
}

// Facts are what a caller knows about the thing being approved.
//
// A flat map rather than a typed struct because the engine must serve subjects
// it has never heard of: an expense offers an amount and a head, a discount
// offers a percentage, a stock adjustment offers a quantity. A struct would
// need a field per subject and a migration per new one.
type Facts struct {
	Amount     *decimal.Decimal
	Percent    *decimal.Decimal
	Quantity   *decimal.Decimal
	StoreID    *uuid.UUID
	EmployeeID *uuid.UUID
	CategoryID *uuid.UUID
	At         time.Time
}

// Outcome is what the engine decided.
type Outcome struct {
	// Allowed is true when nothing blocks the caller. A rule that only warns
	// or notifies still allows.
	Allowed bool `json:"allowed"`

	// NeedsApproval is true when a request was raised and the caller must
	// stop. RequestID names it so the caller can show the person where it went.
	NeedsApproval bool       `json:"needs_approval"`
	RequestID     *uuid.UUID `json:"request_id,omitempty"`

	// NeedsPIN is F1's "second-person PIN at the POS": the till can proceed
	// immediately if a manager is standing there, which is the whole point of
	// the PIN variant over an asynchronous approval.
	NeedsPIN bool `json:"needs_pin,omitempty"`

	// Blocked is an outright refusal, with the rule's name so the person is
	// told which policy stopped them rather than being told "no".
	Blocked  bool       `json:"blocked,omitempty"`
	RuleID   *uuid.UUID `json:"rule_id,omitempty"`
	RuleName string     `json:"rule_name,omitempty"`
	Warning  string     `json:"warning,omitempty"`
}

// Evaluate decides what must happen before a subject may proceed.
//
// Called on the commit path of whatever is being approved, INSIDE that
// caller's transaction: the request and the thing it gates must appear
// together or not at all, or a shop ends up with an approval for an expense
// that was never recorded.
func (s *Service) Evaluate(
	ctx context.Context, tx pgx.Tx, scope Scope, subject string,
	subjectID uuid.UUID, summary string, facts Facts,
) (Outcome, error) {
	rules, err := activeRules(ctx, tx, scope.CompanyID, subject)
	if err != nil {
		return Outcome{}, err
	}

	for _, r := range rules {
		matched, err := matches(r.Condition, facts)
		if err != nil {
			return Outcome{}, err
		}
		if !matched {
			continue
		}

		switch r.Action {
		case "block":
			id := r.ID
			return Outcome{
				Blocked: true, RuleID: &id, RuleName: r.Name,
			}, nil

		case "warn":
			id := r.ID
			return Outcome{
				Allowed: true, RuleID: &id, RuleName: r.Name,
				Warning: r.Name,
			}, nil

		case "notify":
			// Someone is told; nothing is stopped.
			id := r.ID
			return Outcome{Allowed: true, RuleID: &id, RuleName: r.Name}, nil

		case "require_pin":
			id := r.ID
			return Outcome{
				NeedsPIN: true, RuleID: &id, RuleName: r.Name,
			}, nil

		case "require_approval":
			reqID, err := s.raise(ctx, tx, scope, r, subject, subjectID,
				summary, facts)
			if err != nil {
				return Outcome{}, err
			}
			id := r.ID
			return Outcome{
				NeedsApproval: true, RequestID: &reqID,
				RuleID: &id, RuleName: r.Name,
			}, nil
		}
	}

	// No rule matched. A shop that has configured nothing is a shop where
	// everything proceeds, which is the right default: an engine that blocked
	// by default would stop a three-person shop from trading on day one.
	return Outcome{Allowed: true}, nil
}

// raise records a request and returns its id.
func (s *Service) raise(
	ctx context.Context, tx pgx.Tx, scope Scope, r Rule,
	subject string, subjectID uuid.UUID, summary string, facts Facts,
) (uuid.UUID, error) {
	var escalateAt *time.Time
	if r.Escalate != nil {
		t := time.Now().UTC().Add(time.Duration(*r.Escalate) * time.Hour)
		escalateAt = &t
	}

	var amount *decimal.Decimal
	if facts.Amount != nil {
		amount = facts.Amount
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO approval_request
		  (tenant_id, company_id, rule_id, subject, subject_id, summary,
		   amount, status, current_step, requested_by, escalate_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',1,$8,$9)
		ON CONFLICT (subject, subject_id)
		  WHERE status IN ('pending', 'escalated')
		  DO UPDATE SET summary = excluded.summary
		RETURNING id`,
		scope.TenantID, scope.CompanyID, r.ID, subject, subjectID,
		summary, amount, scope.UserID, escalateAt).Scan(&id)
	return id, err
}

// activeRules reads a subject's rules, most specific first.
func activeRules(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, subject string,
) ([]Rule, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, is_active, subject, condition::text, action,
		       steps::text, escalate_after_hours, priority
		FROM approval_rule
		WHERE company_id = $1 AND subject = $2 AND is_active
		ORDER BY priority DESC, created_at`, companyID, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Name, &r.IsActive, &r.Subject,
			&r.Condition, &r.Action, &r.Steps, &r.Escalate,
			&r.Priority); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// condition is a rule's trigger, decoded.
type condition struct {
	AmountOver   *string `json:"amount_over"`
	AmountUnder  *string `json:"amount_under"`
	PercentOver  *string `json:"percent_over"`
	QuantityOver *string `json:"quantity_over"`
	StoreID      *string `json:"store_id"`
	EmployeeID   *string `json:"employee_id"`
	CategoryID   *string `json:"category_id"`
	// F1's "time of day", as hours in the company's own clock.
	AfterHour  *int `json:"after_hour"`
	BeforeHour *int `json:"before_hour"`
}

// matches decides whether a rule's condition holds.
//
// Every clause present must hold — they are ANDed. A rule with several
// triggers means "all of these", which is how F1's worked examples read:
// "IF Expense > SAR 5,000" and a store, not either.
//
// A clause naming a fact the caller did not supply does NOT match. That is the
// safe direction: a rule about a store cannot fire on a subject with no store,
// because the alternative is a rule silently applying everywhere.
func matches(raw string, f Facts) (bool, error) {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return true, nil
	}

	var c condition
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return false, errs.New(errs.CodeInternal,
			"An approval rule's condition could not be read.")
	}

	cmp := func(bound *string, actual *decimal.Decimal, over bool) (bool, error) {
		if bound == nil {
			return true, nil
		}
		if actual == nil {
			return false, nil
		}
		limit, err := decimal.NewFromString(*bound)
		if err != nil {
			return false, errs.New(errs.CodeInternal,
				"An approval rule's threshold is not a number.")
		}
		if over {
			return actual.GreaterThan(limit), nil
		}
		return actual.LessThan(limit), nil
	}

	for _, check := range []struct {
		bound  *string
		actual *decimal.Decimal
		over   bool
	}{
		{c.AmountOver, f.Amount, true},
		{c.AmountUnder, f.Amount, false},
		{c.PercentOver, f.Percent, true},
		{c.QuantityOver, f.Quantity, true},
	} {
		ok, err := cmp(check.bound, check.actual, check.over)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}

	for _, check := range []struct {
		want   *string
		actual *uuid.UUID
	}{
		{c.StoreID, f.StoreID},
		{c.EmployeeID, f.EmployeeID},
		{c.CategoryID, f.CategoryID},
	} {
		if check.want == nil {
			continue
		}
		if check.actual == nil || check.actual.String() != *check.want {
			return false, nil
		}
	}

	if c.AfterHour != nil && f.At.Hour() < *c.AfterHour {
		return false, nil
	}
	if c.BeforeHour != nil && f.At.Hour() >= *c.BeforeHour {
		return false, nil
	}
	return true, nil
}
