package receivables

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Taking money in, and knowing who owes what.

// --- The credit-limit check ----------------------------------------------

// CreditDecision is whether a sale may go on account.
type CreditDecision struct {
	Allowed bool
	// Balance and Limit are carried so a refusal can say the numbers rather
	// than just "no" — a cashier standing in front of a customer needs to be
	// able to explain it.
	Balance decimal.Decimal
	Limit   *decimal.Decimal
	Reason  string
}

// CheckCredit decides whether `amount` may be added to a customer's account.
//
// 11-pos-and-sales.md §5: "customer_due posts to AR and is refused when it
// would breach the customer's credit limit (B16)." Refused — so this blocks
// rather than warns, and the till has no override.
//
// A customer with NO limit set cannot buy on credit at all. That is the safe
// reading of an absent value: a record somebody typed in a hurry at the counter
// must not come with an unlimited account attached, and "no limit recorded" is
// far more likely to mean nobody decided than to mean unlimited trust.
//
// Runs inside the caller's transaction, on a LOCKED customer row. Without the
// lock two tills could each check the same headroom and both pass, which is how
// a limit of 1,000 quietly becomes 2,000.
func CheckCredit(
	ctx context.Context, tx pgx.Tx, customerID uuid.UUID, amount decimal.Decimal,
) (CreditDecision, error) {
	var name string
	var limit *decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT name, credit_limit FROM customer WHERE id = $1 FOR UPDATE`,
		customerID).Scan(&name, &limit); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditDecision{}, errs.New(errs.CodeNotFound,
				"That customer was not found.")
		}
		return CreditDecision{}, err
	}

	var balance decimal.Decimal
	if err := tx.QueryRow(ctx,
		`SELECT customer_balance($1)`, customerID).Scan(&balance); err != nil {
		return CreditDecision{}, err
	}

	if limit == nil {
		return CreditDecision{
			Allowed: false, Balance: balance,
			Reason: name + " has no credit account, so this sale cannot go on " +
				"account. Take payment now, or ask an owner to set a credit limit.",
		}, nil
	}

	after := balance.Add(amount)
	if after.GreaterThan(*limit) {
		return CreditDecision{
			Allowed: false, Balance: balance, Limit: limit,
			Reason: name + " owes " + balance.StringFixed(2) + " against a limit of " +
				limit.StringFixed(2) + ". This sale would take them to " +
				after.StringFixed(2) + ".",
		}, nil
	}

	return CreditDecision{Allowed: true, Balance: balance, Limit: limit}, nil
}

// --- Receipts ------------------------------------------------------------

type Allocation struct {
	InvoiceID uuid.UUID
	Amount    decimal.Decimal
}

type NewReceipt struct {
	UUID        uuid.UUID
	CustomerID  uuid.UUID
	Method      string
	Reference   string
	ReceivedOn  time.Time
	Allocations []Allocation
}

type Receipt struct {
	ID            uuid.UUID        `json:"id"`
	ReceiptNumber string           `json:"receipt_number"`
	CustomerID    uuid.UUID        `json:"customer_id"`
	Customer      string           `json:"customer"`
	ReceivedOn    string           `json:"received_on"`
	Method        string           `json:"method"`
	Reference     string           `json:"reference,omitempty"`
	Amount        string           `json:"amount"`
	Currency      string           `json:"currency"`
	Settled       []SettledInvoice `json:"settled"`
	AlreadyTaken  bool             `json:"already_taken"`
	// ReversesID is set when this document exists to put another receipt right.
	// Empty on a live payment. The original is not edited.
	ReversesID *uuid.UUID `json:"reverses_id,omitempty"`
}

type SettledInvoice struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	HumanNumber string    `json:"human_number,omitempty"`
	Amount      string    `json:"amount"`
	Outstanding string    `json:"outstanding"`
}

// TakePayment records money received from a customer against named invoices.
//
// The allocation is explicit rather than oldest-first. A customer paying is
// usually paying specific invoices they have agreed, and guessing which would
// produce a statement they dispute — which is what turns a payment into a week
// of phone calls.
//
// Posts through Rule 8 (`payment.customer`), seeded since 0025: debit whatever
// the money arrived as, credit accounts receivable.
func (s *Service) TakePayment(
	ctx context.Context, scope Scope, in NewReceipt,
) (Receipt, error) {
	if len(in.Allocations) == 0 {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"Say which invoices this payment settles.")
	}
	if in.UUID == uuid.Nil {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"A receipt must carry an identifier so a retry does not take the money twice.")
	}
	if trim(in.Method) == "" {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"Say how the customer paid.")
	}

	var out Receipt
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyTaken(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyTaken = true
			return nil
		}

		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		var customer string
		if e := tx.QueryRow(ctx,
			`SELECT name FROM customer WHERE id = $1 AND company_id = $2`,
			in.CustomerID, scope.CompanyID).Scan(&customer); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That customer was not found.")
			}
			return e
		}

		receivedOn := in.ReceivedOn
		if receivedOn.IsZero() {
			receivedOn = time.Now().UTC()
		}

		total := decimal.Zero
		for _, a := range in.Allocations {
			if !a.Amount.IsPositive() {
				return errs.New(errs.CodeInvalidInput,
					"A payment of nothing is not a payment.")
			}
			total = total.Add(a.Amount)
		}

		number, e := claimReceiptNumber(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var receiptID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO customer_receipt
			  (tenant_id, company_id, customer_id, receipt_number, uuid,
			   received_on, method, reference, amount, currency, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.CustomerID, number, in.UUID,
			receivedOn, in.Method, nullText(in.Reference), total, currency,
			scope.UserID).Scan(&receiptID); e != nil {
			return db.Translate(e, "That receipt has already been recorded.")
		}

		out = Receipt{
			ID: receiptID, ReceiptNumber: number, CustomerID: in.CustomerID,
			Customer: customer, ReceivedOn: receivedOn.Format("2006-01-02"),
			Method: in.Method, Reference: in.Reference,
			Amount: total.StringFixed(2), Currency: currency,
			Settled: []SettledInvoice{},
		}

		for _, a := range in.Allocations {
			// Locked before it is read. Two receipts allocated to the same
			// invoice at the same moment would otherwise both see the old
			// outstanding figure and between them overpay it.
			var outstanding decimal.Decimal
			var humanNumber string
			if e := tx.QueryRow(ctx, `
				SELECT o.outstanding, o.human_number
				FROM sales_invoice i
				JOIN customer_open_invoices($2) o ON o.invoice_id = i.id
				WHERE i.id = $1 AND i.customer_id = $3
				FOR UPDATE OF i`,
				a.InvoiceID, scope.CompanyID, in.CustomerID).
				Scan(&outstanding, &humanNumber); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.New(errs.CodeInvalidInput,
						"One of those invoices is not an open account sale for this customer.")
				}
				return e
			}

			if a.Amount.GreaterThan(outstanding) {
				return errs.Newf(errs.CodeInvalidInput,
					"Invoice %s has %s outstanding, less than the %s allocated to it.",
					invoiceLabel(humanNumber, a.InvoiceID),
					outstanding.StringFixed(2), a.Amount.StringFixed(2))
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO customer_receipt_allocation
				  (tenant_id, receipt_id, invoice_id, amount)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, receiptID, a.InvoiceID, a.Amount); e != nil {
				return db.Translate(e,
					"That invoice is allocated twice on the same receipt.")
			}

			out.Settled = append(out.Settled, SettledInvoice{
				InvoiceID: a.InvoiceID, HumanNumber: humanNumber,
				Amount:      a.Amount.StringFixed(2),
				Outstanding: outstanding.Sub(a.Amount).StringFixed(2),
			})
		}

		// Rule 8, unchanged and until now uncalled: debit whatever the money
		// arrived as, credit accounts receivable.
		result, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: receivedOn, SourceType: "customer_receipt", SourceID: receiptID,
			PostedBy: &scope.UserID, RuleKey: "payment.customer",
			Memo: "Received from " + customer,
		}, country, accounting.Transaction{
			Amounts: map[string]decimal.Decimal{"amount": total},
			Groups: map[string]accounting.Group{
				"payments": {{
					Role: tenderRole(in.Method), Amount: total, Memo: in.Method,
				}},
			},
		})
		if e != nil {
			return e
		}

		_, e = tx.Exec(ctx,
			`UPDATE customer_receipt SET journal_entry_id = $2 WHERE id = $1`,
			receiptID, result.EntryID)
		return e
	})
	return out, err
}

// tenderRole maps how the customer paid onto the account the money arrived in.
//
// Deliberately its own function rather than shared with the sale path. A card
// payment taken at the counter clears through the acquirer's settlement account;
// a customer settling an old invoice by card is doing the same, so this agrees
// with the sale side — but a bank transfer against an invoice is far commoner
// here than at a till, and the two are free to diverge without one silently
// changing the other.
func tenderRole(method string) string {
	switch method {
	case "cash":
		return "cash"
	case "bank_transfer", "cheque", "sadad":
		return "bank"
	case "store_credit":
		return "store_credit_liability"
	default:
		// Every card and wallet scheme clears through the acquirer.
		return "card_clearing"
	}
}

func invoiceLabel(humanNumber string, id uuid.UUID) string {
	if humanNumber != "" {
		return humanNumber
	}
	return id.String()[:8]
}

func (s *Service) alreadyTaken(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Receipt, bool, error) {
	var r Receipt
	err := tx.QueryRow(ctx, `
		SELECT cr.id, cr.receipt_number, cr.customer_id, c.name,
		       cr.received_on::text, cr.method, coalesce(cr.reference,''),
		       round(cr.amount, 2)::text, cr.currency, cr.reverses_id
		FROM customer_receipt cr
		JOIN customer c ON c.id = cr.customer_id
		WHERE cr.tenant_id = $1 AND cr.uuid = $2`,
		scope.TenantID, docUUID,
	).Scan(&r.ID, &r.ReceiptNumber, &r.CustomerID, &r.Customer, &r.ReceivedOn,
		&r.Method, &r.Reference, &r.Amount, &r.Currency, &r.ReversesID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}

	rows, err := tx.Query(ctx, `
		SELECT a.invoice_id, coalesce(i.human_number,''), round(a.amount, 2)::text
		FROM customer_receipt_allocation a
		JOIN sales_invoice i ON i.id = a.invoice_id
		WHERE a.receipt_id = $1`, r.ID)
	if err != nil {
		return Receipt{}, false, err
	}
	defer rows.Close()

	r.Settled = []SettledInvoice{}
	for rows.Next() {
		var si SettledInvoice
		if err := rows.Scan(&si.InvoiceID, &si.HumanNumber, &si.Amount); err != nil {
			return Receipt{}, false, err
		}
		si.Outstanding = "0.00"
		r.Settled = append(r.Settled, si)
	}
	return r, true, rows.Err()
}

// --- The ledger ----------------------------------------------------------

// LedgerRow is one movement on a customer's account.
type LedgerRow struct {
	Date      string `json:"date"`
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Charged   string `json:"charged,omitempty"`
	Received  string `json:"received,omitempty"`
	// Balance is the running total AFTER this row, so a customer reading a
	// statement can see how they got where they are rather than only where.
	Balance string `json:"balance"`
	DueDate string `json:"due_date,omitempty"`
	// SourceID is the receipt or invoice this row is. Needed to reverse a
	// payment from the statement without a second lookup.
	SourceID   string `json:"source_id,omitempty"`
	ReversesID string `json:"reverses_id,omitempty"`
	// Reversed is true once another receipt has put this one right. The
	// original row stays; the button to reverse it does not.
	Reversed bool `json:"reversed,omitempty"`
}

type Ledger struct {
	Customer     Customer    `json:"customer"`
	Rows         []LedgerRow `json:"rows"`
	Closing      string      `json:"closing"`
	BaseCurrency string      `json:"base_currency"`
}

// LedgerFor is the khata: every charge and every receipt, in order, with a
// running balance.
//
// B6's formula for the supplier side stated the shape and this mirrors it:
// opening plus what was charged, less what was received, is the closing
// balance. The running balance is computed here rather than in SQL so the
// arithmetic is in one place and cannot disagree with the total.
func (s *Service) LedgerFor(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) (Ledger, error) {
	out := Ledger{Rows: []LedgerRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.BaseCurrency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		customer, e := s.readCustomer(ctx, tx, scope, customerID)
		if e != nil {
			return e
		}
		out.Customer = customer

		// Charges and receipts in one ordered stream. A statement that listed
		// them separately would make a customer add up two columns to find out
		// what they owe.
		rows, e := tx.Query(ctx, `
			SELECT d.happened_at::date::text, d.kind, d.reference,
			       d.charged::text, d.received::text, coalesce(d.due,''),
			       coalesce(d.source_id::text,''), coalesce(d.reverses_id::text,''),
			       d.reversed
			FROM (
			  SELECT i.issued_at AS happened_at, 1 AS seq, 'sale' AS kind,
			         coalesce(i.human_number, left(i.id::text, 8)) AS reference,
			         o.on_account AS charged, 0::numeric AS received,
			         o.due_date::text AS due,
			         i.id AS source_id, NULL::uuid AS reverses_id, false AS reversed
			  FROM customer_open_invoices($1) o
			  JOIN sales_invoice i ON i.id = o.invoice_id
			  WHERE o.customer_id = $2

			  UNION ALL

			  -- Returns credited back to the account. Its own row rather than
			  -- folded into the sale, because a customer reading a statement
			  -- needs to see that the reduction was goods coming back and not a
			  -- payment they never made.
			  SELECT cn.issued_at, 2, 'credit',
			         coalesce(cn.human_number, left(cn.id::text, 8)),
			         0::numeric, f.amount, NULL,
			         cn.id, NULL::uuid, false
			  FROM sales_invoice cn
			  JOIN sales_refund f ON f.credit_note_id = cn.id
			  WHERE cn.company_id = $1 AND cn.doc_type = 'credit_note'
			    AND f.method = 'customer_due'
			    AND EXISTS (
			      SELECT 1 FROM sales_invoice oi
			      WHERE oi.id = cn.parent_invoice_id AND oi.customer_id = $2
			    )

			  UNION ALL

			  SELECT cr.created_at, 3,
			         CASE WHEN cr.reverses_id IS NULL THEN 'receipt' ELSE 'reversal' END,
			         cr.receipt_number,
			         CASE WHEN cr.reverses_id IS NULL THEN 0::numeric ELSE cr.amount END,
			         CASE WHEN cr.reverses_id IS NULL THEN cr.amount ELSE 0::numeric END,
			         NULL,
			         cr.id, cr.reverses_id,
			         EXISTS (
			           SELECT 1 FROM customer_receipt r WHERE r.reverses_id = cr.id
			         )
			  FROM customer_receipt cr
			  WHERE cr.customer_id = $2 AND cr.company_id = $1
			) d
			ORDER BY d.happened_at, d.seq`, scope.CompanyID, customerID)
		if e != nil {
			return e
		}
		defer rows.Close()

		running := decimal.Zero
		for rows.Next() {
			var r LedgerRow
			var charged, received, due, sourceID, reversesID string
			var reversed bool
			if e := rows.Scan(&r.Date, &r.Kind, &r.Reference,
				&charged, &received, &due, &sourceID, &reversesID, &reversed); e != nil {
				return e
			}

			chargedAmount := decimal.RequireFromString(charged)
			receivedAmount := decimal.RequireFromString(received)
			running = running.Add(chargedAmount).Sub(receivedAmount)

			if chargedAmount.IsPositive() {
				r.Charged = chargedAmount.StringFixed(2)
			}
			if receivedAmount.IsPositive() {
				r.Received = receivedAmount.StringFixed(2)
			}
			r.Balance = running.StringFixed(2)
			r.DueDate = due
			r.SourceID = sourceID
			r.ReversesID = reversesID
			r.Reversed = reversed
			out.Rows = append(out.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		out.Closing = running.StringFixed(2)
		return nil
	})
	return out, err
}

// GLDifference is C9.3's hard invariant as a number: the sum of what customers
// owe, less the Accounts Receivable control account balance, which must be zero.
//
// Deliberately the same shape and the same name as inventory.GLDifference, so
// the nightly job, the acceptance test and a support engineer looking at a live
// tenant all ask one question and get one answer — and so a second invariant
// does not arrive with a second way of asking about it.
func GLDifference(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (decimal.Decimal, error) {
	var d decimal.Decimal
	err := tx.QueryRow(ctx,
		`SELECT receivable_gl_difference($1)`, companyID).Scan(&d)
	return d, err
}

// --- Open invoices and ageing --------------------------------------------

type OpenInvoice struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	HumanNumber string    `json:"human_number,omitempty"`
	IssueDate   string    `json:"issue_date"`
	DueDate     string    `json:"due_date"`
	OnAccount   string    `json:"on_account"`
	// Credited is what came back off the account through a return. Shown
	// separately from Received so a customer querying their balance can see the
	// difference between money they paid and goods they brought back.
	Credited    string `json:"credited"`
	Received    string `json:"received"`
	Outstanding string `json:"outstanding"`
}

// OpenFor lists what a customer still owes, invoice by invoice. This is what a
// receipt form allocates against.
func (s *Service) OpenFor(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) ([]OpenInvoice, error) {
	out := []OpenInvoice{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT invoice_id, human_number, issue_date::text, due_date::text,
			       round(on_account, 2)::text, round(credited, 2)::text,
			       round(received, 2)::text, round(outstanding, 2)::text
			FROM customer_open_invoices($1)
			WHERE customer_id = $2 AND outstanding > 0
			ORDER BY due_date, issue_date`, scope.CompanyID, customerID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var inv OpenInvoice
			if e := rows.Scan(&inv.InvoiceID, &inv.HumanNumber, &inv.IssueDate,
				&inv.DueDate, &inv.OnAccount, &inv.Credited, &inv.Received,
				&inv.Outstanding); e != nil {
				return e
			}
			out = append(out, inv)
		}
		return rows.Err()
	})
	return out, err
}

type AgeingRow struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Customer   string    `json:"customer"`
	NotDue     string    `json:"not_due"`
	Days0To30  string    `json:"days_0_30"`
	Days31To60 string    `json:"days_31_60"`
	Days61To90 string    `json:"days_61_90"`
	Days90Plus string    `json:"days_90_plus"`
	Total      string    `json:"total"`
}

type Ageing struct {
	AsOf         string      `json:"as_of"`
	Rows         []AgeingRow `json:"rows"`
	Total        string      `json:"total"`
	BaseCurrency string      `json:"base_currency"`
}

// AgeingAt is who owes what, and for how long.
//
// From the DUE date, exactly as the supplier side. A 30-day invoice raised 20
// days ago is not overdue, and ageing from issue would put it in a chasing
// queue it does not belong in.
func (s *Service) AgeingAt(
	ctx context.Context, scope Scope, asOf time.Time,
) (Ageing, error) {
	out := Ageing{AsOf: asOf.Format("2006-01-02"), Rows: []AgeingRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.BaseCurrency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT customer_id, customer_name,
			       round(not_due, 2)::text, round(days_0_30, 2)::text,
			       round(days_31_60, 2)::text, round(days_61_90, 2)::text,
			       round(days_90_plus, 2)::text, round(total, 2)::text
			FROM customer_ageing($1, $2)`, scope.CompanyID, asOf)
		if e != nil {
			return e
		}
		defer rows.Close()

		total := decimal.Zero
		for rows.Next() {
			var r AgeingRow
			if e := rows.Scan(&r.CustomerID, &r.Customer, &r.NotDue,
				&r.Days0To30, &r.Days31To60, &r.Days61To90, &r.Days90Plus,
				&r.Total); e != nil {
				return e
			}
			total = total.Add(decimal.RequireFromString(r.Total))
			out.Rows = append(out.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		out.Total = total.StringFixed(2)
		return nil
	})
	return out, err
}

// --- The terminal's copy --------------------------------------------------

// SellableCustomer is what a till needs to attach a sale to somebody and to
// decide, offline, whether it may go on their account.
//
// No notes, no address, no email. A cache on every till in the shop should hold
// what selling needs and nothing more, for the same reason the catalogue cache
// holds no cost price.
type SellableCustomer struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	NameAr    string    `json:"name_ar,omitempty"`
	Type      string    `json:"customer_type"`
	Phone     string    `json:"phone,omitempty"`
	TermsDays int       `json:"payment_terms_days"`

	// CreditLimit empty means no account, so no credit sale. Balance and
	// Available are as at this pull — the server checks them again and is the
	// authority, exactly as it is for a cached price.
	CreditLimit string `json:"credit_limit,omitempty"`
	Balance     string `json:"balance"`
	Available   string `json:"available,omitempty"`

	IsActive  bool   `json:"is_active"`
	UpdatedAt string `json:"updated_at"`
}

// Snapshot serves the customers a till caches.
//
// Cursored on (updated_at, id) exactly like the catalogue, so the same route
// serves the first full download and every later delta, and a till that has
// been off for a week pulls the difference rather than the whole book.
//
// Retired customers travel in the delta rather than being filtered out, for the
// same reason withdrawn variants do: a row silently omitted stays in the till's
// cache forever, and the cashier keeps selling to somebody the shop has stopped
// dealing with.
//
// # The balance rides along, and is stale by design
//
// A customer's balance moves with every sale and receipt, not with their
// updated_at, so a delta cannot keep it current. That is accepted rather than
// worked around: the balance here is what was true at the last pull, the till
// uses it only to decide what to OFFER, and the server re-checks the real
// figure under a row lock before any sale goes on account. Same bargain as the
// cached price — a stale row costs a corrected answer at the counter, never a
// wrong receivable.
func (s *Service) Snapshot(
	ctx context.Context, scope Scope, since string, sinceID *uuid.UUID, limit int,
) ([]SellableCustomer, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	out := []SellableCustomer{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT c.id, c.code, c.name, coalesce(c.name_ar,''), c.customer_type,
			       coalesce(c.phone,''), c.payment_terms_days,
			       coalesce(round(c.credit_limit, 2)::text, ''),
			       round(customer_balance(c.id), 2)::text,
			       c.is_active,
			       to_char(c.updated_at, 'YYYY-MM-DD"T"HH24:MI:SS.USOF:00')
			FROM customer c
			WHERE c.company_id = $1
			  AND ($2::timestamptz IS NULL
			       OR c.updated_at > $2::timestamptz
			       OR (c.updated_at = $2::timestamptz AND c.id > $3::uuid))
			ORDER BY c.updated_at, c.id
			LIMIT $4`,
			scope.CompanyID, nullText(since), sinceID, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var c SellableCustomer
			if e := rows.Scan(&c.ID, &c.Code, &c.Name, &c.NameAr, &c.Type,
				&c.Phone, &c.TermsDays, &c.CreditLimit, &c.Balance,
				&c.IsActive, &c.UpdatedAt); e != nil {
				return e
			}
			c.Available = headroom(c.CreditLimit, c.Balance)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}
