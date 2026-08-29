// Customers, and collecting what they owe.
//
// The mirror of purchasing, and deliberately built the same way: a party record,
// what is outstanding derived rather than stored, receipts that settle named
// documents, ageing from the due date, and a control-account tie-out. Somebody
// who has read the purchasing package should recognise this one.
//
// # The receivable is derived, never stored
//
// 02-posting-engine.md §6.6, quoting C9.3:
//
//	SUM(customer open balances) == Accounts Receivable control balance
//
// A running total cached on the customer row would be a second source of truth,
// and the first thing a second source of truth does is disagree with the first.
// So `customer_open_invoices` computes it from the invoices and the receipts,
// and `receivable_gl_difference` is the invariant as a number.
//
// # Only the on-account part of a sale is a receivable
//
// A sale settled half in cash and half on account owes the second half. The
// outstanding figure therefore comes from the `customer_due` TENDERS rather than
// from the invoice total — using the total would show the whole amount as
// outstanding on every split payment, and the tie-out would fail on the first
// one.
//
// # Nothing here posts a sale
//
// A sale on account is posted by the sale service, through the rules it already
// uses, with `customer_due` mapping to accounts_receivable exactly as it did
// before this package existed. What is new is that the sale now names WHO owes
// it and refuses to exceed their limit. Receipts post through Rule 8
// (`payment.customer`), seeded since 0025 and until now uncalled.

package receivables

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

type Service struct {
	pool *db.Pool
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is which books a customer belongs to.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// --- Customers -----------------------------------------------------------

type NewCustomer struct {
	Code        string
	Name        string
	NameAr      string
	Type        string
	Phone       string
	Email       string
	VATNumber   string
	Address     string
	TermsDays   int
	CreditLimit *decimal.Decimal
	Notes       string
}

type Customer struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	NameAr    string    `json:"name_ar,omitempty"`
	Type      string    `json:"customer_type"`
	Phone     string    `json:"phone,omitempty"`
	Email     string    `json:"email,omitempty"`
	VATNumber string    `json:"vat_number,omitempty"`
	Address   string    `json:"address,omitempty"`
	TermsDays int       `json:"payment_terms_days"`
	// CreditLimit is empty when none is set, which means no credit at all —
	// distinct from a limit of zero, which is the same outcome stated
	// deliberately. The screen says which.
	CreditLimit string `json:"credit_limit,omitempty"`
	Notes       string `json:"notes,omitempty"`
	IsActive    bool   `json:"is_active"`

	// Balance is what they owe right now, derived. On the list because a
	// cashier about to put a sale on account needs it before they start, not
	// after.
	Balance string `json:"balance"`
	// Available is the limit less the balance, or empty when there is no limit.
	// The figure a cashier actually needs: "can I put 400 on this account".
	Available string `json:"available,omitempty"`

	// Currency is the company's, which is what the three figures above are in.
	//
	// Sent with them rather than left to the screen to find. The customers list
	// showed a balance, a limit and an available figure with no code on any of
	// them, which is legible only to somebody who already knows which country
	// the shop is in — and "can I put 400 on this account" is a question whose
	// answer depends on which 400.
	Currency string `json:"currency"`
}

func (s *Service) CreateCustomer(
	ctx context.Context, scope Scope, in NewCustomer,
) (Customer, error) {
	if err := validate(in, false); err != nil {
		return Customer{}, err
	}

	var out Customer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO customer
			  (tenant_id, company_id, code, name, name_ar, customer_type,
			   phone, email, vat_number, address, payment_terms_days,
			   credit_limit, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.Code, in.Name,
			nullText(in.NameAr), customerType(in.Type), nullText(in.Phone),
			nullText(in.Email), nullText(in.VATNumber), nullText(in.Address),
			in.TermsDays, in.CreditLimit, nullText(in.Notes),
			scope.UserID).Scan(&id); e != nil {
			return conflictMessage(db.Translate(e, ""),
				"A customer already uses the code "+in.Code+
					". Codes are how you find them, so each one has to be different.")
		}
		read, e := s.readCustomer(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// UpdateCustomer corrects a customer's details.
//
// The code is not editable, for the same reason a supplier's is not: it appears
// on documents already issued. The CREDIT LIMIT is deliberately not settable
// here either — it is a credit decision with its own permission, and bundling it
// into "edit details" would let anybody who can fix a phone number also decide
// how much the shop is willing to be owed.
func (s *Service) UpdateCustomer(
	ctx context.Context, scope Scope, customerID uuid.UUID, in NewCustomer,
) (Customer, error) {
	if err := validate(in, true); err != nil {
		return Customer{}, err
	}

	var out Customer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE customer
			SET name = $3, name_ar = $4, customer_type = $5, phone = $6,
			    email = $7, vat_number = $8, address = $9,
			    payment_terms_days = $10, notes = $11, updated_at = now()
			WHERE id = $1 AND company_id = $2`,
			customerID, scope.CompanyID, in.Name, nullText(in.NameAr),
			customerType(in.Type), nullText(in.Phone), nullText(in.Email),
			nullText(in.VATNumber), nullText(in.Address), in.TermsDays,
			nullText(in.Notes))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That customer was not found.")
		}
		read, e := s.readCustomer(ctx, tx, scope, customerID)
		out = read
		return e
	})
	return out, err
}

// SetCreditLimit decides how much this customer may owe at once.
//
// Its own operation with its own permission, because it is a credit decision
// rather than a clerical one. A nil limit removes the account entirely: the
// customer can still buy, but only for money that arrives with them.
//
// Lowering a limit below what is already outstanding is ALLOWED. The debt is
// already real and refusing to record the decision would not unmake it; what
// the lower limit does is stop the balance growing, which is exactly what
// somebody tightening a limit intends.
func (s *Service) SetCreditLimit(
	ctx context.Context, scope Scope, customerID uuid.UUID, limit *decimal.Decimal,
) (Customer, error) {
	if limit != nil && limit.IsNegative() {
		return Customer{}, errs.New(errs.CodeInvalidInput,
			"A credit limit cannot be negative.")
	}

	var out Customer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE customer SET credit_limit = $3, updated_at = now()
			WHERE id = $1 AND company_id = $2`,
			customerID, scope.CompanyID, limit)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That customer was not found.")
		}
		read, e := s.readCustomer(ctx, tx, scope, customerID)
		out = read
		return e
	})
	return out, err
}

// SetCustomerActive hides a customer, or brings them back.
//
// Never a delete: invoices and receipts refer to them. Refused while they still
// owe money, for the same reason a supplier is — an inactive customer drops out
// of the lists a cashier searches, and a debt nobody can see is a debt nobody
// collects.
func (s *Service) SetCustomerActive(
	ctx context.Context, scope Scope, customerID uuid.UUID, active bool,
) (Customer, error) {
	var out Customer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var name string
		var balance decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT c.name, customer_balance(c.id)
			FROM customer c WHERE c.id = $1 AND c.company_id = $2`,
			customerID, scope.CompanyID).Scan(&name, &balance); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That customer was not found.")
			}
			return e
		}

		if !active && balance.IsPositive() {
			return errs.Newf(errs.CodeConflict,
				"%s still owes %s. Collect it first — an inactive customer "+
					"disappears from the lists you search, and a debt nobody can "+
					"see is a debt nobody collects.", name, balance.StringFixed(2))
		}

		if _, e := tx.Exec(ctx, `
			UPDATE customer SET is_active = $3, updated_at = now()
			WHERE id = $1 AND company_id = $2`,
			customerID, scope.CompanyID, active); e != nil {
			return e
		}
		read, e := s.readCustomer(ctx, tx, scope, customerID)
		out = read
		return e
	})
	return out, err
}

func (s *Service) ListCustomers(
	ctx context.Context, scope Scope, search string, includeInactive bool,
) ([]Customer, error) {
	out := []Customer{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, customerSelect+`
			WHERE c.company_id = $1
			  AND ($2 OR c.is_active)
			  AND ($3 = '' OR c.name ILIKE '%'||$3||'%'
			                OR c.code ILIKE '%'||$3||'%'
			                OR c.phone ILIKE '%'||$3||'%')
			ORDER BY c.name`, scope.CompanyID, includeInactive, search)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			c, e := scanCustomer(rows)
			if e != nil {
				return e
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) ReadCustomer(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) (Customer, error) {
	var out Customer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readCustomer(ctx, tx, scope, customerID)
		out = read
		return e
	})
	return out, err
}

// customerSelect is shared so the list and the single read cannot drift into
// reporting different balances for the same customer.
const customerSelect = `
	SELECT c.id, c.code, c.name, coalesce(c.name_ar,''), c.customer_type,
	       coalesce(c.phone,''), coalesce(c.email,''), coalesce(c.vat_number,''),
	       coalesce(c.address,''), c.payment_terms_days,
	       -- Two decimals at the boundary. numeric(18,4)::text hands back
	       -- "500.0000" and a zero sum hands back "0", and a screen showing one
	       -- formatted amount beside one raw one looks like a bug to a reader.
	       coalesce(round(c.credit_limit, 2)::text,''),
	       coalesce(c.notes,''), c.is_active,
	       round(customer_balance(c.id), 2)::text,
	       co.base_currency
	FROM customer c
	JOIN company co ON co.id = c.company_id`

type scanner interface {
	Scan(dest ...any) error
}

func scanCustomer(row scanner) (Customer, error) {
	var c Customer
	if err := row.Scan(&c.ID, &c.Code, &c.Name, &c.NameAr, &c.Type,
		&c.Phone, &c.Email, &c.VATNumber, &c.Address, &c.TermsDays,
		&c.CreditLimit, &c.Notes, &c.IsActive, &c.Balance,
		&c.Currency); err != nil {
		return Customer{}, err
	}
	c.Available = headroom(c.CreditLimit, c.Balance)
	return c, nil
}

// headroom is what is left on the account.
//
// Empty when there is no limit, because "unlimited" and "nothing left" must not
// render the same. Never negative: a balance that has drifted past the limit —
// which a lowered limit does deliberately — leaves nothing available rather than
// a negative allowance somebody might read as credit.
func headroom(limit, balance string) string {
	if limit == "" {
		return ""
	}
	left := decimal.RequireFromString(limit).Sub(decimal.RequireFromString(balance))
	if left.IsNegative() {
		return "0.00"
	}
	return left.StringFixed(2)
}

func (s *Service) readCustomer(
	ctx context.Context, tx pgx.Tx, scope Scope, customerID uuid.UUID,
) (Customer, error) {
	row := tx.QueryRow(ctx, customerSelect+`
		WHERE c.id = $1 AND c.company_id = $2`, customerID, scope.CompanyID)
	c, err := scanCustomer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, errs.New(errs.CodeNotFound, "That customer was not found.")
	}
	return c, err
}

func validate(in NewCustomer, editing bool) error {
	e := errs.Validation("Some customer details are missing.")
	bad := false

	if !editing && trim(in.Code) == "" {
		e.WithField("code", "Give the customer a short code you will recognise.")
		bad = true
	}
	if trim(in.Name) == "" {
		e.WithField("name", "Enter the customer's name.")
		bad = true
	}
	if in.TermsDays < 0 || in.TermsDays > 365 {
		e.WithField("payment_terms_days", "Payment terms run from 0 to 365 days.")
		bad = true
	}
	switch in.Type {
	case "", "retail", "wholesale", "vip":
	default:
		e.WithField("customer_type", "Choose retail, wholesale or VIP.")
		bad = true
	}
	if in.CreditLimit != nil && in.CreditLimit.IsNegative() {
		e.WithField("credit_limit", "A credit limit cannot be negative.")
		bad = true
	}
	if bad {
		return e
	}
	return nil
}

func customerType(t string) string {
	if t == "" {
		return "retail"
	}
	return t
}

// --- shared --------------------------------------------------------------

func requireCompany(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) error {
	var found bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM company WHERE id = $1`, companyID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return err
}

// conflictMessage replaces the generic duplicate wording with something the
// person reading it can act on. Same helper as the purchasing side, for the same
// reason: "That record already exists" names neither the record nor the field.
func conflictMessage(err error, message string) error {
	if e := errs.As(err); e != nil && e.Code == errs.CodeConflict {
		return errs.New(errs.CodeConflict, message)
	}
	return err
}

func claimReceiptNumber(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n int64
	if err := tx.QueryRow(ctx, `
		UPDATE company SET next_receipt_no = next_receipt_no + 1
		WHERE id = $1 RETURNING next_receipt_no - 1`, companyID).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("RCT-%s-%06d", time.Now().UTC().Format("2006"), n), nil
}

func nullText(s string) any {
	if trim(s) == "" {
		return nil
	}
	return s
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}
