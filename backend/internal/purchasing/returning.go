package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Sending goods back to a supplier.
//
// B5: "Purchase Return (to Supplier): for defective/excess stock —
// auto-generates a Debit Note and instantly deducts inventory."
//
// # Against the bill, and why that is not a limitation
//
// Goods refused at the door never entered stock: `grn_line.qty_rejected`
// records them and the accrual was never raised for them. This is the other
// case — a fault found after the invoice arrived and the pallet was put away —
// and it is the case that needs a document, because there is now a payable to
// reduce and input tax already claimed to give back.
//
// # Two figures, deliberately kept apart
//
// The supplier owes back what they charged: the bill's price, at the bill's
// tax rate. The stock leaves at what the valuation says those units are worth,
// which is the costing engine's answer and can differ — landed cost was added
// on receipt, or the shop has bought the same item since at another price.
//
// Forcing the two together would mean either claiming the wrong amount from the
// supplier or leaving the stock report disagreeing with the balance sheet. So
// both are posted as they are and the gap goes to `cost_variance`, which is the
// account the receipt's own cost correction already uses.
//
// # There is no draft
//
// The stock has left the building. A return that could sit unposted would be a
// shelf that says it holds goods which are on a lorry, so the document is
// written, the stock moves and the entry posts in one transaction or none of
// it happens.

// ReturnLine is one line of goods going back.
type ReturnLine struct {
	BillLineID uuid.UUID
	Qty        decimal.Decimal
}

// NewReturn is a claim being raised.
type NewReturn struct {
	// UUID is assigned before the call, so a clerk pressing the button twice on
	// a bad connection claims once and takes the stock out once.
	UUID uuid.UUID

	BillID uuid.UUID

	// WarehouseID is where the stock leaves from. Optional: a company with one
	// stock location has nothing to say, and the receipt's warehouse is used.
	WarehouseID *uuid.UUID

	ReturnedOn time.Time
	Reason     string
	Lines      []ReturnLine
}

// PurchaseReturnLine is one line as a screen reads it.
type PurchaseReturnLine struct {
	BillLineID  uuid.UUID  `json:"bill_line_id"`
	VariantID   *uuid.UUID `json:"variant_id,omitempty"`
	LineNo      int        `json:"line_no"`
	Description string     `json:"description"`
	Qty         string     `json:"qty"`
	UnitCost    string     `json:"unit_cost"`

	TaxTreatment string `json:"tax_treatment"`
	// A FRACTION, as everywhere: "0.150000" is fifteen per cent.
	TaxRate string `json:"tax_rate"`

	NetAmount   string `json:"net_amount"`
	TaxAmount   string `json:"tax_amount"`
	GrossAmount string `json:"gross_amount"`

	// What the units were carrying in stock. Shown apart from the claim
	// because they are two different facts and only one of them is what the
	// supplier owes.
	StockValue string `json:"stock_value"`
}

// PurchaseReturn is a raised claim.
type PurchaseReturn struct {
	ID        uuid.UUID `json:"id"`
	ReturnNo  string    `json:"return_no"`
	BillID    uuid.UUID `json:"bill_id"`
	BillRef   string    `json:"bill_ref,omitempty"`
	Supplier  string    `json:"supplier"`
	Warehouse string    `json:"warehouse,omitempty"`

	ReturnedOn string `json:"returned_on"`
	Reason     string `json:"reason"`
	Currency   string `json:"currency"`

	SubtotalNet string `json:"subtotal_net"`
	TaxTotal    string `json:"tax_total"`
	Total       string `json:"total_inclusive"`

	// What left the shelf, at cost. Equal to the net in the ordinary case and
	// different whenever landed cost or a later purchase moved the valuation.
	StockValue string `json:"stock_value"`
	// The gap between the two, signed. Positive when the claim exceeds what the
	// stock was carrying.
	Variance string `json:"variance"`

	CreatedBy string `json:"created_by,omitempty"`

	Lines []PurchaseReturnLine `json:"lines,omitempty"`

	// AlreadyReturned is set when this UUID had been used before and the stored
	// claim is being returned instead of a second one.
	AlreadyReturned bool `json:"already_returned"`
}

// Returnable is one bill line and what is left of it.
type Returnable struct {
	BillLineID  uuid.UUID  `json:"bill_line_id"`
	LineNo      int        `json:"line_no"`
	Description string     `json:"description"`
	VariantID   *uuid.UUID `json:"variant_id,omitempty"`

	QtyBilled     string `json:"qty_billed"`
	QtyReturned   string `json:"qty_returned"`
	QtyReturnable string `json:"qty_returnable"`

	UnitCost     string `json:"unit_cost"`
	TaxTreatment string `json:"tax_treatment"`
	TaxRate      string `json:"tax_rate"`
}

// ReturnableLines says what is left to send back on one bill.
//
// Read from the server, never worked out by a screen: earlier returns are rows
// a client may never have seen, and a client that computed this would
// eventually claim the same pallet twice.
func (s *Service) ReturnableLines(
	ctx context.Context, scope Scope, billID uuid.UUID,
) ([]Returnable, error) {
	out := []Returnable{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM purchase_bill
			               WHERE id = $1 AND company_id = $2)`,
			billID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return errs.New(errs.CodeNotFound, "That bill was not found.")
		}

		rows, e := tx.Query(ctx, `
			SELECT bill_line_id, line_no, description, variant_id,
			       qty_billed, qty_returned, qty_returnable,
			       unit_cost, tax_treatment, tax_rate
			FROM bill_line_returnable
			WHERE bill_id = $1
			ORDER BY line_no`, billID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r Returnable
			var billed, returned, returnable, cost, rate decimal.Decimal
			if e := rows.Scan(&r.BillLineID, &r.LineNo, &r.Description,
				&r.VariantID, &billed, &returned, &returnable, &cost,
				&r.TaxTreatment, &rate); e != nil {
				return e
			}
			r.QtyBilled = billed.String()
			r.QtyReturned = returned.String()
			r.QtyReturnable = returnable.String()
			r.UnitCost = cost.String()
			r.TaxRate = rate.String()
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// ReturnGoods sends goods back and raises the debit note.
func (s *Service) ReturnGoods(
	ctx context.Context, scope Scope, in NewReturn,
) (PurchaseReturn, error) {
	if in.UUID == uuid.Nil {
		return PurchaseReturn{}, errs.New(errs.CodeInvalidInput,
			"A return must carry an identifier so a retry does not claim twice.")
	}
	if len(in.Lines) == 0 {
		return PurchaseReturn{}, errs.New(errs.CodeInvalidInput,
			"A return needs at least one line.")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) < 3 {
		// The same rule a customer return carries, for the same reason: an
		// unexplained return is how the value of a pallet goes missing between
		// a clerk and a driver.
		return PurchaseReturn{}, errs.Validation(
			"Say why these goods are going back.").
			WithField("reason",
				"It appears on the debit note the supplier will read.")
	}
	if in.ReturnedOn.IsZero() {
		in.ReturnedOn = time.Now().UTC()
	}

	var out PurchaseReturn
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The retry check first, before anything is written.
		if existing, found, e := s.alreadyReturned(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyReturned = true
			return nil
		}

		var supplierID uuid.UUID
		var supplier, currency, supplierRef string
		if e := tx.QueryRow(ctx, `
			SELECT b.supplier_id, s.legal_name, b.currency,
			       coalesce(b.supplier_ref, '')
			FROM purchase_bill b
			JOIN supplier s ON s.id = b.supplier_id
			WHERE b.id = $1 AND b.company_id = $2`,
			in.BillID, scope.CompanyID).
			Scan(&supplierID, &supplier, &currency, &supplierRef); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That bill was not found.")
			}
			return e
		}

		warehouseID, e := s.warehouseForReturn(ctx, tx, scope, in)
		if e != nil {
			return e
		}

		// The company's own answer to "may stock go below zero", read from the
		// company and never from the request -- the same rule a sale follows,
		// and for the same reason: a caller that named its own policy could
		// send back goods at a shop that had forbidden it.
		var policy inventory.NegativeStockPolicy
		if e := tx.QueryRow(ctx,
			`SELECT negative_stock_policy FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&policy); e != nil {
			return e
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "purchase_return", "PRT")
		if e != nil {
			return e
		}

		// The id is minted HERE rather than returned by an insert, so the
		// header can be written once and complete.
		//
		// A purchase_return is immutable the moment it exists -- the stock has
		// left the building, so there is no state in which the document is
		// half-written and correctable. Inserting it first and filling in the
		// totals afterwards means an UPDATE, and the trigger refuses that,
		// correctly. The stock movements below carry this id in `source_id`,
		// which is a plain column with no foreign key, so they can be written
		// before the row they point at.
		returnID := uuid.New()

		net := decimal.Zero
		tax := decimal.Zero
		gross := decimal.Zero
		stockValue := decimal.Zero

		type pending struct {
			billLineID uuid.UUID
			variantID  *uuid.UUID
			lineNo     int
			desc       string
			qty        decimal.Decimal
			unitCost   decimal.Decimal
			treatment  string
			rate       decimal.Decimal
			net        decimal.Decimal
			tax        decimal.Decimal
			stock      decimal.Decimal
		}
		var written []pending

		for i, line := range in.Lines {
			if !line.Qty.IsPositive() {
				return errs.Newf(errs.CodeInvalidInput,
					"Line %d is for no quantity.", i+1)
			}

			var description, treatment string
			var variantID *uuid.UUID
			var unitCost, rate, returnable decimal.Decimal
			if e := tx.QueryRow(ctx, `
				SELECT r.description, r.variant_id, r.unit_cost,
				       r.tax_treatment, r.tax_rate, r.qty_returnable
				FROM bill_line_returnable r
				WHERE r.bill_line_id = $1 AND r.bill_id = $2`,
				line.BillLineID, in.BillID).
				Scan(&description, &variantID, &unitCost, &treatment, &rate,
					&returnable); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.Newf(errs.CodeInvalidInput,
						"Line %d is not on that bill.", i+1)
				}
				return e
			}

			// Cumulative across every earlier return, which is the whole point
			// of reading it from the view rather than from the bill.
			if line.Qty.GreaterThan(returnable) {
				return errs.Newf(errs.CodeConflict,
					"Only %s of %q is left to send back; %s was asked for.",
					returnable.String(), description, line.Qty.String())
			}

			lineNet := line.Qty.Mul(unitCost).Round(4)
			lineTax := lineNet.Mul(rate).Round(4)

			// The stock leaves at whatever the valuation holds. A line with no
			// variant is a charge rather than goods -- freight on the invoice,
			// say -- and there is nothing on a shelf to move.
			lineStock := decimal.Zero
			if variantID != nil {
				cost, e := inventory.Consume(ctx, tx, inventory.Issue{
					TenantID: scope.TenantID, CompanyID: scope.CompanyID,
					VariantID: *variantID, WarehouseID: warehouseID,
					Qty:        line.Qty,
					Reason:     "purchase_return",
					SourceType: "purchase_return", SourceID: &returnID,
					Note: in.Reason,
				})
				if e != nil {
					return e
				}
				// `Consume` REPORTS a shortfall rather than refusing one,
				// because whether stock may go below zero is the company's
				// policy and not the stock package's business. Applying it
				// here is not optional: without it a return from a location
				// holding none of the item took nothing out, valued the goods
				// at zero, and still claimed the full amount from the supplier
				// -- posting the whole claim to variance and reading, on the
				// screen, as a successful return of goods that were not there.
				//
				// Found by raising one against a back room that had never held
				// the item.
				if e := inventory.CheckAvailability(policy, cost, description); e != nil {
					return e
				}
				lineStock = cost.TotalCost
			}

			written = append(written, pending{
				billLineID: line.BillLineID, variantID: variantID,
				lineNo: i + 1, desc: description, qty: line.Qty,
				unitCost: unitCost, treatment: treatment, rate: rate,
				net: lineNet, tax: lineTax, stock: lineStock,
			})

			net = net.Add(lineNet)
			tax = tax.Add(lineTax)
			gross = gross.Add(lineNet).Add(lineTax)
			stockValue = stockValue.Add(lineStock)
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO purchase_return
			  (id, tenant_id, company_id, uuid, return_no, bill_id, supplier_id,
			   warehouse_id, returned_on, reason, currency, created_by,
			   subtotal_net, tax_total, total_inclusive, stock_value)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::date,$10,$11,$12,$13,$14,$15,$16)`,
			returnID, scope.TenantID, scope.CompanyID, in.UUID, number,
			in.BillID, supplierID, warehouseID, in.ReturnedOn, in.Reason,
			currency, scope.UserID, net, tax, gross, stockValue); e != nil {
			return db.Translate(e, "That return could not be recorded.")
		}

		for _, l := range written {
			if _, e := tx.Exec(ctx, `
				INSERT INTO purchase_return_line
				  (tenant_id, return_id, bill_line_id, variant_id, line_no,
				   description, qty, unit_cost, tax_treatment, tax_rate,
				   net_amount, tax_amount, gross_amount, stock_value)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				scope.TenantID, returnID, l.billLineID, l.variantID, l.lineNo,
				l.desc, l.qty, l.unitCost, l.treatment, l.rate,
				l.net, l.tax, l.net.Add(l.tax), l.stock); e != nil {
				return db.Translate(e, "That return line could not be recorded.")
			}
		}

		if e := s.postReturn(ctx, tx, scope, returnID, in.ReturnedOn,
			gross, tax, stockValue,
			"Returned to "+supplier+" "+number); e != nil {
			return e
		}

		// What the supplier owes back comes off the payable, so the bill's own
		// outstanding follows the claim rather than waiting for their credit
		// note to arrive.
		if e := s.creditBill(ctx, tx, in.BillID, gross); e != nil {
			return e
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "purchase_return_raised",
			EntityType: "purchase_return", EntityID: &returnID,
			After: map[string]any{
				"return_no": number, "supplier": supplier,
				"total": gross.StringFixed(2), "reason": in.Reason,
			},
		}); e != nil {
			return e
		}

		read, e := s.readReturn(ctx, tx, scope, returnID)
		out = read
		return e
	})
	if err != nil {
		return PurchaseReturn{}, db.Translate(err, "")
	}
	return out, nil
}

// warehouseForReturn decides where the stock leaves from.
//
// Named by the caller when they name one, and otherwise the single active
// location — the same rule a sale follows, and refused the same way when a
// company has two and has not said which.
func (s *Service) warehouseForReturn(
	ctx context.Context, tx pgx.Tx, scope Scope, in NewReturn,
) (uuid.UUID, error) {
	if in.WarehouseID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM warehouse
			               WHERE id = $1 AND company_id = $2 AND is_active)`,
			*in.WarehouseID, scope.CompanyID).Scan(&ok); err != nil {
			return uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, errs.New(errs.CodeNotFound,
				"That stock location is not this business's.")
		}
		return *in.WarehouseID, nil
	}

	var found []uuid.UUID
	rows, err := tx.Query(ctx, `
		SELECT id FROM warehouse
		WHERE company_id = $1 AND is_active ORDER BY code`, scope.CompanyID)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if e := rows.Scan(&id); e != nil {
			return uuid.Nil, e
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}

	switch len(found) {
	case 0:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"This business has no stock location, so there is nothing to send back from.")
	case 1:
		return found[0], nil
	default:
		return uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This business keeps stock in more than one place, so the return "+
				"must say which one the goods are leaving from.")
	}
}

// postReturn books the claim, the stock and whatever is left over.
func (s *Service) postReturn(
	ctx context.Context, tx pgx.Tx, scope Scope, returnID uuid.UUID,
	on time.Time, gross, tax, stockValue decimal.Decimal, memo string,
) error {
	var country string
	if err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
		Scan(&country); err != nil {
		return err
	}

	// Payable falls by the whole claim; stock and input tax come off at what
	// each was carrying. Whatever those three leave over is the variance, and
	// it is a real figure rather than a plug: it is the difference between what
	// the supplier charged for these units and what the shop's valuation says
	// they were worth.
	variance := gross.Sub(tax).Sub(stockValue)
	debit := decimal.Zero
	credit := decimal.Zero
	if variance.IsNegative() {
		// The stock was carrying more than the claim: sending it back loses the
		// difference, so the variance account takes the charge.
		debit = variance.Neg()
	} else {
		credit = variance
	}

	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: on, SourceType: "purchase_return", SourceID: returnID,
		PostedBy: &scope.UserID, RuleKey: "purchase.return",
		Memo: memo,
	}, country, accounting.Transaction{
		Amounts: map[string]decimal.Decimal{
			"total_inclusive": gross,
			"tax_amount":      tax,
			"stock_value":     stockValue,
			"variance_debit":  debit,
			"variance_credit": credit,
		},
	})
	return err
}

// creditBill reduces what is still owed on the bill.
//
// Into `amount_credited`, which 0128 added, and never into `amount_paid`. A
// credit is not a payment: writing it there told the supplier portal and the
// ageing report that goods taken back had been paid for, and on a bill that
// was paid before the return it pushed the balance below zero, which the
// payment screen then offered as something to settle.
//
// The status follows what is left once both are counted. A bill settled only
// because it was credited is still settled.
func (s *Service) creditBill(
	ctx context.Context, tx pgx.Tx, billID uuid.UUID, amount decimal.Decimal,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE purchase_bill
		SET amount_credited = amount_credited + $2,
		    status = CASE
		      WHEN amount_paid + amount_credited + $2 >= total_inclusive
		        THEN 'paid'
		      WHEN status = 'paid' THEN 'approved'
		      ELSE status
		    END
		WHERE id = $1`, billID, amount)
	return db.Translate(err, "That bill could not be credited.")
}

func (s *Service) alreadyReturned(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (PurchaseReturn, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM purchase_return WHERE company_id = $1 AND uuid = $2`,
		scope.CompanyID, docUUID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurchaseReturn{}, false, nil
	}
	if err != nil {
		return PurchaseReturn{}, false, err
	}
	// The WHOLE document, not just its id. A retry after a lost response is the
	// reason this path exists, and answering with an id and zeros would tell a
	// clerk they had claimed nothing.
	out, err := s.readReturn(ctx, tx, scope, id)
	return out, true, err
}

// Returns lists them, newest first.
func (s *Service) Returns(
	ctx context.Context, scope Scope, billID *uuid.UUID,
) ([]PurchaseReturn, error) {
	out := []PurchaseReturn{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT r.id, r.return_no, r.bill_id, coalesce(b.supplier_ref, ''),
			       s.legal_name, coalesce(w.name, ''),
			       to_char(r.returned_on, 'YYYY-MM-DD'), r.reason, r.currency,
			       r.subtotal_net, r.tax_total, r.total_inclusive,
			       r.stock_value, coalesce(u.full_name, '')
			FROM purchase_return r
			JOIN purchase_bill b ON b.id = r.bill_id
			JOIN supplier s      ON s.id = r.supplier_id
			LEFT JOIN warehouse w ON w.id = r.warehouse_id
			LEFT JOIN app_user u  ON u.id = r.created_by
			WHERE r.company_id = $1
			  AND ($2::uuid IS NULL OR r.bill_id = $2::uuid)
			ORDER BY r.returned_on DESC, r.created_at DESC
			LIMIT 200`, scope.CompanyID, billID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanReturn(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// Return reads one, with its lines.
func (s *Service) Return(
	ctx context.Context, scope Scope, id uuid.UUID,
) (PurchaseReturn, error) {
	var out PurchaseReturn
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readReturn(ctx, tx, scope, id)
		out = read
		return e
	})
	if err != nil {
		return PurchaseReturn{}, db.Translate(err, "")
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanReturn(row scanner) (PurchaseReturn, error) {
	var r PurchaseReturn
	var net, tax, gross, stock decimal.Decimal
	if err := row.Scan(&r.ID, &r.ReturnNo, &r.BillID, &r.BillRef, &r.Supplier,
		&r.Warehouse, &r.ReturnedOn, &r.Reason, &r.Currency,
		&net, &tax, &gross, &stock, &r.CreatedBy); err != nil {
		return PurchaseReturn{}, err
	}
	r.SubtotalNet = net.StringFixed(2)
	r.TaxTotal = tax.StringFixed(2)
	r.Total = gross.StringFixed(2)
	r.StockValue = stock.StringFixed(2)
	r.Variance = net.Sub(stock).StringFixed(2)
	return r, nil
}

func (s *Service) readReturn(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (PurchaseReturn, error) {
	row := tx.QueryRow(ctx, `
		SELECT r.id, r.return_no, r.bill_id, coalesce(b.supplier_ref, ''),
		       s.legal_name, coalesce(w.name, ''),
		       to_char(r.returned_on, 'YYYY-MM-DD'), r.reason, r.currency,
		       r.subtotal_net, r.tax_total, r.total_inclusive,
		       r.stock_value, coalesce(u.full_name, '')
		FROM purchase_return r
		JOIN purchase_bill b ON b.id = r.bill_id
		JOIN supplier s      ON s.id = r.supplier_id
		LEFT JOIN warehouse w ON w.id = r.warehouse_id
		LEFT JOIN app_user u  ON u.id = r.created_by
		WHERE r.id = $1 AND r.company_id = $2`, id, scope.CompanyID)

	out, err := scanReturn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurchaseReturn{}, errs.New(errs.CodeNotFound,
			"That return was not found.")
	}
	if err != nil {
		return PurchaseReturn{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT bill_line_id, variant_id, line_no, description, qty, unit_cost,
		       tax_treatment, tax_rate, net_amount, tax_amount, gross_amount,
		       stock_value
		FROM purchase_return_line
		WHERE return_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return PurchaseReturn{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PurchaseReturnLine
		var qty, cost, rate, net, tax, gross, stock decimal.Decimal
		if e := rows.Scan(&l.BillLineID, &l.VariantID, &l.LineNo,
			&l.Description, &qty, &cost, &l.TaxTreatment, &rate,
			&net, &tax, &gross, &stock); e != nil {
			return PurchaseReturn{}, e
		}
		l.Qty = qty.String()
		l.UnitCost = cost.String()
		l.TaxRate = rate.String()
		l.NetAmount = net.StringFixed(2)
		l.TaxAmount = tax.StringFixed(2)
		l.GrossAmount = gross.StringFixed(2)
		l.StockValue = stock.StringFixed(2)
		out.Lines = append(out.Lines, l)
	}
	return out, rows.Err()
}
