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

// Putting a receipt right without editing it.
//
// A payment allocated to the wrong invoice is a fact about money that arrived.
// Design 02 §2 and C9.1: facts are not edited. The original receipt stays, and
// a NEW one reverses it — posted through the same rule (`payment.customer`)
// with the sides flipped, so cash goes back out of the till and the receivable
// is reinstated. The customer's statement then shows both, which is how they
// can see how they got back to owing the amount.
//
// # Full reversal only
//
// P21 does not describe reversing part of a receipt. A clerk who split a
// payment across two invoices and got one of them wrong reverses the whole
// document and takes it again. Inventing a partial would be a new business
// rule, and the unique index on reverses_id is one-to-one for that reason.
//
// # The journal is posted here, through the engine
//
// Same as taking the payment: the rule names the shape, the engine writes the
// lines, and this package does not know the chart of accounts. A receivables
// package that wrote journal lines would put the posting rules in two places.

// ReverseReceipt names the document being put right.
type ReverseReceipt struct {
	// UUID is assigned by the client BEFORE the call, like every other
	// money-moving document. A network failure after the server committed
	// would otherwise reverse the same receipt twice.
	UUID      uuid.UUID
	ReceiptID uuid.UUID
}

// ReversePayment posts a new receipt that undoes an existing one.
//
// The original is not touched. A second call with the same UUID is the original
// reversal, not a new one. A second call with a different UUID against the
// same receipt is refused — one reversal per original.
func (s *Service) ReversePayment(
	ctx context.Context, scope Scope, in ReverseReceipt,
) (Receipt, error) {
	if in.UUID == uuid.Nil {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"A reversal must carry an identifier so a retry does not reverse twice.")
	}
	if in.ReceiptID == uuid.Nil {
		return Receipt{}, errs.New(errs.CodeInvalidInput,
			"Say which receipt to reverse.")
	}

	var out Receipt
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyTaken(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			if existing.ReversesID == nil || *existing.ReversesID != in.ReceiptID {
				return errs.New(errs.CodeConflict,
					"That identifier already belongs to a different receipt.")
			}
			out = existing
			out.AlreadyTaken = true
			return nil
		}

		orig, err := loadOriginal(ctx, tx, scope, in.ReceiptID)
		if err != nil {
			return err
		}

		var already uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM customer_receipt WHERE reverses_id = $1`,
			orig.id).Scan(&already)
		if err == nil {
			return errs.New(errs.CodeConflict,
				"That receipt has already been reversed.")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var currency, country, customer string
		if e := tx.QueryRow(ctx, `
			SELECT co.base_currency, co.country, c.name
			FROM company co
			JOIN customer c ON c.company_id = co.id
			WHERE co.id = $1 AND c.id = $2`,
			scope.CompanyID, orig.customerID).Scan(&currency, &country, &customer); e != nil {
			return e
		}

		number, e := claimReceiptNumber(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		receivedOn := time.Now().UTC()
		var receiptID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO customer_receipt
			  (tenant_id, company_id, customer_id, receipt_number, uuid,
			   received_on, method, reference, amount, currency, created_by,
			   reverses_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
			scope.TenantID, scope.CompanyID, orig.customerID, number, in.UUID,
			receivedOn, orig.method, nullText(orig.reference), orig.amount,
			currency, scope.UserID, orig.id).Scan(&receiptID); e != nil {
			return db.Translate(e, "That reversal could not be recorded.")
		}

		out = Receipt{
			ID: receiptID, ReceiptNumber: number, CustomerID: orig.customerID,
			Customer: customer, ReceivedOn: receivedOn.Format("2006-01-02"),
			Method: orig.method, Reference: orig.reference,
			Amount: orig.amount.StringFixed(2), Currency: currency,
			Settled:    []SettledInvoice{},
			ReversesID: &orig.id,
		}

		for _, a := range orig.allocations {
			if _, e := tx.Exec(ctx, `
				INSERT INTO customer_receipt_allocation
				  (tenant_id, receipt_id, invoice_id, amount)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, receiptID, a.invoiceID, a.amount); e != nil {
				return db.Translate(e, "That reversal could not be allocated.")
			}

			var outstanding decimal.Decimal
			var humanNumber string
			if e := tx.QueryRow(ctx, `
				SELECT o.outstanding, o.human_number
				FROM customer_open_invoices($2) o
				WHERE o.invoice_id = $1`,
				a.invoiceID, scope.CompanyID).Scan(&outstanding, &humanNumber); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.New(errs.CodeInternal,
						"A reversed allocation pointed at an invoice that is no longer on this account.")
				}
				return e
			}
			out.Settled = append(out.Settled, SettledInvoice{
				InvoiceID: a.invoiceID, HumanNumber: humanNumber,
				Amount:      a.amount.StringFixed(2),
				Outstanding: outstanding.StringFixed(2),
			})
		}

		rule, e := accounting.ResolveRule(ctx, tx, "payment.customer", country, receivedOn)
		if e != nil {
			return e
		}

		// The original entry with its sides flipped, read from the entry rather
		// than rebuilt from the rule.
		//
		// Rebuilding looks equivalent and is not, for two reasons. The rule is
		// resolved at TODAY's date, so a rule amended since the receipt was
		// taken produces a reversal shaped differently from the entry it claims
		// to undo, and the receipt's journal never nets to zero. And a receipt
		// that settled a foreign-currency invoice carries a realised exchange
		// gain or loss whose size depended on two rates on two days; no amount
		// of rule evaluation at reversal time recovers it, so reversing one leg
		// and not the other would leave the gain standing while the receipt it
		// arose from was undone.
		//
		// This is the same correction purchasing/reversing.go already carries
		// for supplier payments. The two sides of the ledger now behave alike.
		if orig.entryID == nil {
			return errs.New(errs.CodeConflict,
				"That receipt was never posted, so there is nothing to reverse.")
		}
		lines, e := accounting.LinesOf(ctx, tx, *orig.entryID)
		if e != nil {
			return e
		}

		result, e := accounting.Post(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: receivedOn, SourceType: "customer_receipt", SourceID: receiptID,
			PostedBy: &scope.UserID, RuleKey: "payment.customer",
			RuleVersion: rule.Version,
			Memo:        "Reversal of " + orig.number,
			ReversesID:  orig.entryID,
			Lines:       accounting.FlipSides(lines),
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

type originalReceipt struct {
	id          uuid.UUID
	customerID  uuid.UUID
	number      string
	method      string
	reference   string
	amount      decimal.Decimal
	entryID     *uuid.UUID
	allocations []originalAlloc
}

type originalAlloc struct {
	invoiceID uuid.UUID
	amount    decimal.Decimal
}

// loadOriginal reads the receipt being reversed and locks it, so two clerks
// reversing the same mis-allocation at the same moment cannot both succeed.
func loadOriginal(
	ctx context.Context, tx pgx.Tx, scope Scope, receiptID uuid.UUID,
) (originalReceipt, error) {
	var r originalReceipt
	var reversesID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, customer_id, receipt_number, method, coalesce(reference,''),
		       amount, journal_entry_id, reverses_id
		FROM customer_receipt
		WHERE id = $1 AND company_id = $2
		FOR UPDATE`,
		receiptID, scope.CompanyID).Scan(
		&r.id, &r.customerID, &r.number, &r.method, &r.reference,
		&r.amount, &r.entryID, &reversesID)
	if errors.Is(err, pgx.ErrNoRows) {
		return originalReceipt{}, errs.New(errs.CodeNotFound,
			"That receipt was not found.")
	}
	if err != nil {
		return originalReceipt{}, err
	}
	if reversesID != nil {
		return originalReceipt{}, errs.New(errs.CodeInvalidInput,
			"That receipt is itself a reversal. Reverse the original instead.")
	}
	if r.entryID == nil {
		return originalReceipt{}, errs.New(errs.CodeInternal,
			"That receipt was never posted, so it cannot be reversed.")
	}

	rows, err := tx.Query(ctx, `
		SELECT invoice_id, amount FROM customer_receipt_allocation
		WHERE receipt_id = $1 ORDER BY invoice_id`, receiptID)
	if err != nil {
		return originalReceipt{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a originalAlloc
		if err := rows.Scan(&a.invoiceID, &a.amount); err != nil {
			return originalReceipt{}, err
		}
		r.allocations = append(r.allocations, a)
	}
	if err := rows.Err(); err != nil {
		return originalReceipt{}, err
	}
	if len(r.allocations) == 0 {
		return originalReceipt{}, errs.New(errs.CodeInternal,
			"That receipt has no allocations to reverse.")
	}
	return r, nil
}
