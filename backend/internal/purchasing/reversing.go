package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/Biz1core/backend/internal/accounting"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/audit"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Putting a supplier payment right without editing it.
//
// The mirror of receivables.ReversePayment, and built the same way on purpose:
// a payment sent to the wrong supplier or allocated to the wrong bill is a fact
// about money that left. Design 02 §111 is absolute — "Corrections happen only
// by posting a reversing entry with reverses_id set. There is no code path — and
// no database permission — that edits posted history."
//
// So the original stays, and a NEW payment reverses it: posted through the same
// rule (`payment.supplier`) with the sides flipped, so the money comes back to
// the account it left and the payable is reinstated.
//
// # Where this genuinely differs from the customer side
//
// Receivables derives what is owed, so a reversing allocation is enough on its
// own. Payables STORES `purchase_bill.amount_paid` and flips a bill to 'paid'
// when it is settled, so both have to be unwound here.
//
// Rolling the status back asks a question the customer side never had to
// answer: back to WHAT? A bill reaches payment as 'matched' or as 'approved',
// and those record different histories — one where the three-way match agreed,
// one where somebody accepted a discrepancy by name. B5.2's control is
// decorative if reversing a payment silently converts the second into the
// first. The allocation therefore recorded what it found (migration 0050) and
// this puts back exactly that.
//
// # Full reversal only
//
// One reversing document per original, enforced by a unique index. A clerk who
// split a payment across three bills and got one wrong reverses the whole thing
// and pays again. Partial reversal would be a new business rule.

// ReversePaymentRequest names the document being put right.
type ReversePaymentRequest struct {
	// UUID is assigned by the client BEFORE the call, like every other
	// money-moving document here. A network failure after the server committed
	// would otherwise reverse the same payment twice.
	UUID      uuid.UUID
	PaymentID uuid.UUID

	// Reason is why the money is being taken back, in the words of whoever
	// decided it. It is not stored on the payment: the document is a fact
	// about money and carries no editorial. It goes into the audit trail,
	// which is the register somebody reads when they ask months later why a
	// supplier was paid and then unpaid — and the answer "PAY-000117 was
	// reversed" without a reason is the answer that starts the argument.
	Reason string
}

// ReversePayment posts a new payment that undoes an existing one.
//
// The original is not touched. A second call with the same UUID returns the
// reversal already made rather than making another; a different UUID against
// the same payment is refused.
func (s *Service) ReversePayment(
	ctx context.Context, scope Scope, in ReversePaymentRequest,
) (Payment, error) {
	if in.UUID == uuid.Nil {
		return Payment{}, errs.New(errs.CodeInvalidInput,
			"A reversal must carry an identifier so a retry does not reverse twice.")
	}
	if in.PaymentID == uuid.Nil {
		return Payment{}, errs.New(errs.CodeInvalidInput,
			"Say which payment to reverse.")
	}

	var out Payment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// A retry of the same reversal is the same reversal. A retry that
		// carries a UUID already spent on something else is a client bug, and
		// saying so is better than quietly reversing a second document.
		if existing, found, e := s.alreadyPaid(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			if existing.ReversesID == nil || *existing.ReversesID != in.PaymentID {
				return errs.New(errs.CodeConflict,
					"That identifier already belongs to a different payment.")
			}
			out = existing
			out.AlreadyPaid = true
			return nil
		}

		orig, err := loadOriginalPayment(ctx, tx, scope, in.PaymentID)
		if err != nil {
			return err
		}

		// Reversing a reversal would let a clerk walk a balance anywhere by
		// alternating documents. Undoing a reversal means paying again, which
		// leaves both facts on the record.
		if orig.reversesID != nil {
			return errs.New(errs.CodeConflict,
				"That document is itself a reversal and cannot be reversed. "+
					"Record a new payment instead.")
		}

		var already uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM supplier_payment WHERE reverses_id = $1`,
			orig.id).Scan(&already)
		if err == nil {
			return errs.New(errs.CodeConflict,
				"That payment has already been reversed.")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var currency, country, supplier string
		if e := tx.QueryRow(ctx, `
			SELECT co.base_currency, co.country, sup.legal_name
			FROM company co
			JOIN supplier sup ON sup.company_id = co.id
			WHERE co.id = $1 AND sup.id = $2`,
			scope.CompanyID, orig.supplierID).
			Scan(&currency, &country, &supplier); e != nil {
			return e
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "payment", "PAY")
		if e != nil {
			return e
		}

		paidOn := time.Now().UTC()
		var paymentID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO supplier_payment
			  (tenant_id, company_id, supplier_id, payment_number, uuid,
			   paid_on, method, reference, amount, currency, created_by,
			   reverses_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
			scope.TenantID, scope.CompanyID, orig.supplierID, number, in.UUID,
			paidOn, orig.method, nullText(orig.reference), orig.amount,
			currency, scope.UserID, orig.id).Scan(&paymentID); e != nil {
			return db.Translate(e, "That reversal could not be recorded.")
		}

		out = Payment{
			ID: paymentID, PaymentNumber: number, SupplierID: orig.supplierID,
			Supplier: supplier, PaidOn: paidOn.Format("2006-01-02"),
			Method: orig.method, Reference: orig.reference,
			Amount: orig.amount.StringFixed(2), Currency: currency,
			Settled:    []SettledBill{},
			ReversesID: &orig.id,
		}

		for _, a := range orig.allocations {
			if _, e := tx.Exec(ctx, `
				INSERT INTO supplier_payment_allocation
				  (tenant_id, payment_id, bill_id, amount, bill_status_before)
				VALUES ($1,$2,$3,$4,$5)`,
				scope.TenantID, paymentID, a.billID, a.amount,
				nullText(a.statusBefore)); e != nil {
				return db.Translate(e,
					"That reversal could not be allocated.")
			}

			// The two stored facts, unwound together. The status goes back to
			// what the original allocation recorded; a payment taken before
			// migration 0050 has no record of it, and for those the only honest
			// answer is 'approved' — the state a bill must be in to be paid at
			// all, and the one that does not claim the match agreed.
			restore := a.statusBefore
			if restore == "" {
				restore = "approved"
			}

			var newStatus string
			var outstanding decimal.Decimal
			var ref string
			if e := tx.QueryRow(ctx, `
				UPDATE purchase_bill
				SET amount_paid = amount_paid - $2,
				    status = CASE WHEN status = 'paid' THEN $3::text ELSE status END
				WHERE id = $1
				RETURNING status, total_inclusive - amount_paid, supplier_ref`,
				a.billID, a.amount, restore).
				Scan(&newStatus, &outstanding, &ref); e != nil {
				return e
			}

			out.Settled = append(out.Settled, SettledBill{
				BillID: a.billID, SupplierRef: ref,
				Amount:      a.amount.StringFixed(2),
				Outstanding: outstanding.StringFixed(2),
				Status:      newStatus,
			})
		}

		// The original entry with its sides flipped: credit accounts payable,
		// putting back what is owed, and debit whatever the money returned to.
		//
		// Read from the entry rather than rebuilt from rule 7. Re-deriving
		// looks equivalent and is not — a payment that settled a foreign bill
		// carries a realised exchange gain or loss whose size depended on two
		// rates on two days, and no amount of rule evaluation at reversal time
		// recovers it. Reversing one leg of that entry and not the other would
		// leave the gain standing while the payment it arose from was undone.
		rule, e := accounting.ResolveRule(ctx, tx, "payment.supplier", country, paidOn)
		if e != nil {
			return e
		}
		if orig.entryID == nil {
			return errs.New(errs.CodeConflict,
				"That payment was never posted, so there is nothing to reverse.")
		}
		lines, e := accounting.LinesOf(ctx, tx, *orig.entryID)
		if e != nil {
			return e
		}

		result, e := accounting.Post(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: paidOn, SourceType: "supplier_payment", SourceID: paymentID,
			PostedBy: &scope.UserID, RuleKey: "payment.supplier",
			RuleVersion: rule.Version,
			Memo:        "Reversal of " + orig.number,
			ReversesID:  orig.entryID,
			Lines:       accounting.FlipSides(lines),
		})
		if e != nil {
			return e
		}

		if _, e = tx.Exec(ctx,
			`UPDATE supplier_payment SET journal_entry_id = $2 WHERE id = $1`,
			paymentID, result.EntryID); e != nil {
			return e
		}

		// Recorded here as well as in the journal, because the two answer
		// different questions. The journal says the ledger was put back; the
		// trail says WHO decided to put it back and why, which is what an
		// investigation reads first. The neighbouring acts on this ledger —
		// approving a blocked bill, overriding a match — have been audited
		// since B5.2, and taking money back off a supplier is not a smaller
		// act than either.
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "supplier_payment_reversed",
			EntityType: "supplier_payment", EntityID: &orig.id,
			Before: map[string]any{
				"payment_number": orig.number,
				"amount":         orig.amount.StringFixed(2),
				"currency":       currency,
				"supplier":       supplier,
			},
			After: map[string]any{
				"reversal_number": number,
				"reversal_id":     paymentID.String(),
				"reason":          strings.TrimSpace(in.Reason),
			},
		})
	})
	return out, err
}

// originalPayment is the document being reversed, read whole.
type originalPayment struct {
	id          uuid.UUID
	supplierID  uuid.UUID
	number      string
	method      string
	reference   string
	amount      decimal.Decimal
	entryID     *uuid.UUID
	reversesID  *uuid.UUID
	allocations []originalAllocation
}

type originalAllocation struct {
	billID uuid.UUID
	amount decimal.Decimal
	// statusBefore is what the bill was when the original payment touched it.
	// Empty for payments taken before migration 0050 recorded it.
	statusBefore string
}

func loadOriginalPayment(
	ctx context.Context, tx pgx.Tx, scope Scope, paymentID uuid.UUID,
) (originalPayment, error) {
	var p originalPayment
	err := tx.QueryRow(ctx, `
		SELECT id, supplier_id, payment_number, method, coalesce(reference,''),
		       amount, journal_entry_id, reverses_id
		FROM supplier_payment
		WHERE id = $1 AND company_id = $2`,
		paymentID, scope.CompanyID).
		Scan(&p.id, &p.supplierID, &p.number, &p.method, &p.reference,
			&p.amount, &p.entryID, &p.reversesID)

	if errors.Is(err, pgx.ErrNoRows) {
		// Another tenant's payment reads as absent under row-level security,
		// which is the right answer: its existence is not this caller's
		// business.
		return originalPayment{}, errs.New(errs.CodeNotFound,
			"That payment was not found.")
	}
	if err != nil {
		return originalPayment{}, err
	}

	if p.entryID == nil {
		// A payment with no journal entry never posted. Reversing it would
		// write a correction against nothing.
		return originalPayment{}, errs.New(errs.CodeConflict,
			"That payment was never posted, so there is nothing to reverse.")
	}

	rows, err := tx.Query(ctx, `
		SELECT bill_id, amount, coalesce(bill_status_before, '')
		FROM supplier_payment_allocation
		WHERE payment_id = $1
		ORDER BY bill_id`, p.id)
	if err != nil {
		return originalPayment{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var a originalAllocation
		if err := rows.Scan(&a.billID, &a.amount, &a.statusBefore); err != nil {
			return originalPayment{}, err
		}
		p.allocations = append(p.allocations, a)
	}
	if err := rows.Err(); err != nil {
		return originalPayment{}, err
	}

	return p, nil
}

// --- Listing what has been paid -------------------------------------------

// PaymentFilter narrows the payment list.
type PaymentFilter struct {
	SupplierID *uuid.UUID
	From       *time.Time
	To         *time.Time
	Method     string
	// ReversibleOnly drops what cannot be undone — a reversal, and a payment
	// something has already undone. Offering either in a picker teaches the
	// user that the picker lies, which is the same reason the receipts list
	// carries `unapplied`.
	ReversibleOnly bool
	Limit          int
}

// ListedPayment is one supplier payment as it appears in a list.
//
// Deliberately not `Payment`: that struct carries the full allocation detail,
// which a list of two hundred would fetch two hundred times. What a person
// choosing a payment to reverse needs is who was paid, when, how much, against
// which bills, and whether it can still be undone.
type ListedPayment struct {
	ID            uuid.UUID `json:"id"`
	PaymentNumber string    `json:"payment_number"`
	SupplierID    uuid.UUID `json:"supplier_id"`
	Supplier      string    `json:"supplier"`
	PaidOn        string    `json:"paid_on"`
	Method        string    `json:"method"`
	Reference     string    `json:"reference,omitempty"`
	Amount        string    `json:"amount"`
	Currency      string    `json:"currency"`

	// Reversal is true when this document exists to undo another payment, and
	// Reversed when another one has undone THIS one. Both are shown: a
	// reversed payment is not one anybody may reverse again, and hiding either
	// would make a figure in the payables ledger unexplainable.
	Reversal bool `json:"reversal"`
	Reversed bool `json:"reversed"`

	// Bills names what the payment settled, as short references rather than
	// ids: a person reading a list of payments recognises the supplier's own
	// invoice number and nothing else.
	Bills []string `json:"bills"`

	// Posted is false for a payment that never reached the journal. There is
	// nothing to reverse in that case and the service says so, so the list
	// says so first.
	Posted bool `json:"posted"`
}

// ListPayments answers what has been paid, newest first.
//
// # Why this exists
//
// `POST /purchasing/payments/{id}/reverse` has been live since payables landed
// and nothing could name a payment id: the product could pay a supplier and
// undo the payment, and offered no way to find the one to undo. The reverse
// route was reachable only by somebody with a database client. This is its
// missing half, and the mirror of `receivables.ListReceipts`.
func (s *Service) ListPayments(
	ctx context.Context, scope Scope, f PaymentFilter,
) ([]ListedPayment, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	out := []ListedPayment{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT sp.id, sp.payment_number, sp.supplier_id, s.legal_name,
			       sp.paid_on::text, sp.method, coalesce(sp.reference,''),
			       round(sp.amount, 2)::text, sp.currency,
			       sp.reverses_id IS NOT NULL,
			       EXISTS (
			         SELECT 1 FROM supplier_payment r WHERE r.reverses_id = sp.id
			       ),
			       sp.journal_entry_id IS NOT NULL,
			       coalesce((
			         SELECT array_agg(b.supplier_ref ORDER BY b.supplier_ref)
			         FROM supplier_payment_allocation a
			         JOIN purchase_bill b ON b.id = a.bill_id
			         WHERE a.payment_id = sp.id
			       ), '{}')
			FROM supplier_payment sp
			JOIN supplier s ON s.id = sp.supplier_id
			WHERE sp.company_id = $1
			  AND ($2::uuid IS NULL OR sp.supplier_id = $2)
			  AND ($3::date IS NULL OR sp.paid_on >= $3)
			  AND ($4::date IS NULL OR sp.paid_on <= $4)
			  AND ($5 = '' OR sp.method = $5)
			  AND (NOT $6 OR (
			        sp.reverses_id IS NULL
			    AND sp.journal_entry_id IS NOT NULL
			    AND NOT EXISTS (
			          SELECT 1 FROM supplier_payment r WHERE r.reverses_id = sp.id
			        )
			  ))
			ORDER BY sp.paid_on DESC, sp.created_at DESC
			LIMIT $7`,
			scope.CompanyID, f.SupplierID, f.From, f.To, f.Method,
			f.ReversibleOnly, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var p ListedPayment
			if e := rows.Scan(&p.ID, &p.PaymentNumber, &p.SupplierID, &p.Supplier,
				&p.PaidOn, &p.Method, &p.Reference, &p.Amount, &p.Currency,
				&p.Reversal, &p.Reversed, &p.Posted, &p.Bills); e != nil {
				return e
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "Those payments could not be read.")
	}
	return out, nil
}
