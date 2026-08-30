// RFQ, supplier quotes, comparison and award (blueprint B5.1).
//
// # Why the award is the only way a quote becomes an order
//
// AwardQuote calls CreateOrder — the same function a buyer typing an order by
// hand reaches. Nothing here writes a purchase_order row directly. That keeps
// one definition of what a purchase order is: numbering, company checks, total
// computation from lines, and the draft status a buyer must deliberately issue
// all happen once, so an order born of a quote cannot quietly differ from one
// that was not.
//
// # Losing quotes are kept
//
// B5.1 wants a "historical quote archive per supplier — useful for negotiating
// next time and for proving best-price sourcing during an audit". Losing quotes
// are marked rejected and stay. They are never orders, never committed spend,
// and never age into a payable, because they were never purchase_order rows to
// begin with.
package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// --- Raising an RFQ ------------------------------------------------------

// NewRFQ asks several suppliers to price the same list.
type NewRFQ struct {
	RequisitionID *uuid.UUID
	WarehouseID   uuid.UUID
	ClosesOn      *time.Time
	Notes         string
	SupplierIDs   []uuid.UUID
	Lines         []NewRFQLine
}

// NewRFQLine is one thing being priced. Unlike a requisition line the variant
// is required: every supplier must quote the same item for the comparison to
// mean anything.
type NewRFQLine struct {
	VariantID   uuid.UUID
	Description string
	Qty         decimal.Decimal
}

// RFQ is a request for quotation as read back.
type RFQ struct {
	ID            uuid.UUID  `json:"id"`
	Number        string     `json:"rfq_number"`
	Status        string     `json:"status"`
	RequisitionID *uuid.UUID `json:"requisition_id,omitempty"`
	WarehouseID   uuid.UUID  `json:"warehouse_id"`
	ClosesOn      string     `json:"closes_on,omitempty"`
	Currency      string     `json:"currency"`
	Notes         string     `json:"notes,omitempty"`

	AwardedQuoteID *uuid.UUID `json:"awarded_quote_id,omitempty"`
	AwardReason    string     `json:"award_reason,omitempty"`
	AwardedBy      string     `json:"awarded_by,omitempty"`
	AwardedAt      string     `json:"awarded_at,omitempty"`
	CancelReason   string     `json:"cancel_reason,omitempty"`

	CreatedAt string `json:"created_at"`
	IssuedAt  string `json:"issued_at,omitempty"`

	Lines   []RFQLineView    `json:"lines,omitempty"`
	Invited []RFQInvitedView `json:"invited,omitempty"`
}

// RFQLineView is one requested line.
type RFQLineView struct {
	ID          uuid.UUID `json:"id"`
	LineNo      int       `json:"line_no"`
	VariantID   uuid.UUID `json:"variant_id"`
	SKU         string    `json:"sku,omitempty"`
	Description string    `json:"description"`
	Qty         string    `json:"qty"`
}

// RFQInvitedView is a supplier who was asked, and whether they answered.
type RFQInvitedView struct {
	SupplierID    uuid.UUID `json:"supplier_id"`
	SupplierName  string    `json:"supplier_name"`
	InvitedAt     string    `json:"invited_at"`
	Quoted        bool      `json:"quoted"`
	DeclinedAt    string    `json:"declined_at,omitempty"`
	DeclineReason string    `json:"decline_reason,omitempty"`
}

// RaiseRFQ creates the request and invites the suppliers.
//
// At least two suppliers, because B5.1 exists to compare them. One supplier is
// not a comparison, it is a purchase order with extra steps — and a buyer who
// genuinely wants one supplier should raise the order directly rather than
// producing a sourcing record that suggests a choice was made when none was.
func (s *Service) RaiseRFQ(
	ctx context.Context, scope Scope, in NewRFQ,
) (RFQ, error) {
	if len(in.Lines) == 0 {
		return RFQ{}, errs.New(errs.CodeInvalidInput,
			"A request for quotation needs at least one line.")
	}
	if len(in.SupplierIDs) < 2 {
		return RFQ{}, errs.Validation(
			"Ask at least two suppliers.").
			WithField("supplier_ids",
				"A quotation from a single supplier is not a comparison. "+
					"Raise a purchase order directly instead.")
	}
	for i, l := range in.Lines {
		if !l.Qty.IsPositive() {
			return RFQ{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no quantity.", i+1)
		}
	}

	var out RFQ
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		if e := checkBelongs(ctx, tx, "warehouse", in.WarehouseID,
			scope.CompanyID, "That warehouse was not found."); e != nil {
			return e
		}
		for _, sup := range in.SupplierIDs {
			if e := checkBelongs(ctx, tx, "supplier", sup, scope.CompanyID,
				"That supplier was not found."); e != nil {
				return e
			}
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "rfq", "RFQ")
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO rfq
			  (tenant_id, company_id, rfq_number, requisition_id, warehouse_id,
			   status, closes_on, currency, notes, created_by, issued_at)
			VALUES ($1,$2,$3,$4,$5,'issued',$6,$7,$8,$9, now()) RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.RequisitionID,
			in.WarehouseID, in.ClosesOn, currency, nullText(in.Notes),
			scope.UserID,
		).Scan(&id); e != nil {
			return e
		}

		for i, l := range in.Lines {
			desc := strings.TrimSpace(l.Description)
			if desc == "" {
				_ = tx.QueryRow(ctx,
					`SELECT coalesce(sku, '') FROM variant WHERE id = $1`,
					l.VariantID).Scan(&desc)
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO rfq_line
				  (tenant_id, rfq_id, line_no, variant_id, description, qty)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				scope.TenantID, id, i+1, l.VariantID, desc, l.Qty); e != nil {
				return e
			}
		}

		for _, sup := range in.SupplierIDs {
			if _, e := tx.Exec(ctx, `
				INSERT INTO rfq_invitation (tenant_id, rfq_id, supplier_id)
				VALUES ($1,$2,$3) ON CONFLICT (rfq_id, supplier_id) DO NOTHING`,
				scope.TenantID, id, sup); e != nil {
				return e
			}
		}

		// The requisition is now being sourced, so it stops appearing on the
		// list of approved requests nobody has acted on.
		if in.RequisitionID != nil {
			if _, e := tx.Exec(ctx, `
				UPDATE purchase_requisition SET status = 'sourcing'
				WHERE id = $1 AND company_id = $2 AND status = 'approved'`,
				*in.RequisitionID, scope.CompanyID); e != nil {
				return e
			}
		}

		read, e := s.readRFQ(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return RFQ{}, db.Translate(err, "")
	}
	return out, nil
}

// --- Recording a quote ---------------------------------------------------

// NewQuote is a supplier's answer.
type NewQuote struct {
	RFQID       uuid.UUID
	SupplierID  uuid.UUID
	QuoteNumber string
	ReceivedOn  *time.Time
	ValidUntil  *time.Time

	LeadTimeDays *int
	TermsDays    *int
	QualityNote  string
	Notes        string

	Lines []NewQuoteLine
}

// NewQuoteLine is one priced answer to one RFQ line.
type NewQuoteLine struct {
	RFQLineID    uuid.UUID
	Qty          decimal.Decimal
	UnitCost     decimal.Decimal
	TaxTreatment string
	TaxRate      decimal.Decimal
	Note         string
}

// Quote is a supplier quote as read back.
type Quote struct {
	ID           uuid.UUID `json:"id"`
	RFQID        uuid.UUID `json:"rfq_id"`
	SupplierID   uuid.UUID `json:"supplier_id"`
	SupplierName string    `json:"supplier_name"`
	QuoteNumber  string    `json:"quote_number,omitempty"`
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`

	ReceivedOn string `json:"received_on"`
	ValidUntil string `json:"valid_until,omitempty"`
	// Expired is derived by comparing valid_until to today rather than stored.
	// A quote does not become a different document at midnight; it simply
	// stops being usable, and a stored flag would need a job to keep true.
	Expired bool `json:"expired"`

	Currency     string `json:"currency"`
	LeadTimeDays *int   `json:"lead_time_days,omitempty"`
	TermsDays    *int   `json:"payment_terms_days,omitempty"`
	QualityNote  string `json:"quality_note,omitempty"`

	SubtotalNet string `json:"subtotal_net"`
	TaxTotal    string `json:"tax_total"`
	Total       string `json:"total_inclusive"`
	Notes       string `json:"notes,omitempty"`

	Lines []QuoteLineView `json:"lines,omitempty"`
}

// QuoteLineView is one priced line.
type QuoteLineView struct {
	ID           uuid.UUID `json:"id"`
	RFQLineID    uuid.UUID `json:"rfq_line_id"`
	LineNo       int       `json:"line_no"`
	VariantID    uuid.UUID `json:"variant_id"`
	SKU          string    `json:"sku,omitempty"`
	Description  string    `json:"description"`
	Qty          string    `json:"qty"`
	UnitCost     string    `json:"unit_cost"`
	TaxTreatment string    `json:"tax_treatment"`
	NetAmount    string    `json:"net_amount"`
	TaxAmount    string    `json:"tax_amount"`
	GrossAmount  string    `json:"gross_amount"`
}

// RecordQuote files a supplier's reply.
//
// A second reply from the same supplier is a REVISION, not an overwrite: the
// earlier one is marked superseded and kept. B5.1 asks for "quote versioning if
// a supplier revises", and a supplier who drops their price after hearing a
// competitor's is exactly the history an audit wants — an UPDATE would erase it.
//
// Totals are computed from the lines here rather than taken from the caller,
// for the same reason a purchase order's are: a stated total that disagrees
// with its lines is the discrepancy the three-way match exists to catch, and
// letting one in at the quote stage seeds it upstream of every later control.
func (s *Service) RecordQuote(
	ctx context.Context, scope Scope, in NewQuote,
) (Quote, error) {
	if len(in.Lines) == 0 {
		return Quote{}, errs.New(errs.CodeInvalidInput,
			"A quote needs at least one line.")
	}
	for i, l := range in.Lines {
		if !l.Qty.IsPositive() {
			return Quote{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no quantity.", i+1)
		}
		if l.UnitCost.IsNegative() {
			return Quote{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has a negative cost.", i+1)
		}
	}

	var out Quote
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, currency string
		e := tx.QueryRow(ctx,
			`SELECT status, currency FROM rfq WHERE id = $1 AND company_id = $2
			 FOR UPDATE`,
			in.RFQID, scope.CompanyID).Scan(&status, &currency)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That request for quotation was not found.")
		}
		if e != nil {
			return e
		}
		// An awarded RFQ is settled. Accepting a quote afterwards would invite
		// re-opening a decision that has already produced a purchase order.
		if status == "awarded" || status == "cancelled" {
			return errs.Newf(errs.CodeConflict,
				"That request for quotation is %s, so no further quotes can be "+
					"recorded against it.", status)
		}

		if e := checkBelongs(ctx, tx, "supplier", in.SupplierID,
			scope.CompanyID, "That supplier was not found."); e != nil {
			return e
		}

		// Only invited suppliers may quote. An uninvited quote appearing in the
		// comparison is how a buyer ends up choosing between offers the shop
		// never solicited.
		var invited bool
		if e := tx.QueryRow(ctx, `
			SELECT true FROM rfq_invitation
			WHERE rfq_id = $1 AND supplier_id = $2`,
			in.RFQID, in.SupplierID).Scan(&invited); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeInvalidInput,
					"That supplier was not asked to quote on this request.")
			}
			return e
		}

		// Supersede the supplier's previous live quote, if any.
		revision := 1
		var supersedes *uuid.UUID
		var prevID uuid.UUID
		var prevRev int
		e = tx.QueryRow(ctx, `
			SELECT id, revision FROM supplier_quote
			WHERE rfq_id = $1 AND supplier_id = $2 AND status = 'received'
			FOR UPDATE`, in.RFQID, in.SupplierID).Scan(&prevID, &prevRev)
		switch {
		case e == nil:
			revision = prevRev + 1
			supersedes = &prevID
			if _, e := tx.Exec(ctx,
				`UPDATE supplier_quote SET status = 'superseded' WHERE id = $1`,
				prevID); e != nil {
				return e
			}
		case errors.Is(e, pgx.ErrNoRows):
			// First reply from this supplier.
		default:
			return e
		}

		receivedOn := time.Now().UTC()
		if in.ReceivedOn != nil {
			receivedOn = *in.ReceivedOn
		}

		var quoteID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO supplier_quote
			  (tenant_id, company_id, rfq_id, supplier_id, quote_number,
			   revision, supersedes_id, status, received_on, valid_until,
			   currency, lead_time_days, payment_terms_days, quality_note,
			   notes, recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'received',$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.RFQID, in.SupplierID,
			nullText(in.QuoteNumber), revision, supersedes, receivedOn,
			in.ValidUntil, currency, in.LeadTimeDays, in.TermsDays,
			nullText(in.QualityNote), nullText(in.Notes), scope.UserID,
		).Scan(&quoteID); e != nil {
			return e
		}

		subtotal, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
		for i, l := range in.Lines {
			// The RFQ line must belong to THIS rfq. Without the check a quote
			// could answer another request's line, and the comparison would
			// silently pair unrelated items.
			var variantID uuid.UUID
			var desc string
			e := tx.QueryRow(ctx, `
				SELECT variant_id, description FROM rfq_line
				WHERE id = $1 AND rfq_id = $2`,
				l.RFQLineID, in.RFQID).Scan(&variantID, &desc)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.Newf(errs.CodeInvalidInput,
					"Line %d answers a line that is not on this request.", i+1)
			}
			if e != nil {
				return e
			}

			net := l.Qty.Mul(l.UnitCost).Round(4)
			lineTax := net.Mul(l.TaxRate).Round(4)
			gross := net.Add(lineTax)

			treatment := l.TaxTreatment
			if treatment == "" {
				treatment = "standard"
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO supplier_quote_line
				  (tenant_id, quote_id, rfq_line_id, line_no, variant_id,
				   description, qty, unit_cost, tax_treatment, tax_rate,
				   net_amount, tax_amount, gross_amount, note)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				scope.TenantID, quoteID, l.RFQLineID, i+1, variantID, desc,
				l.Qty, l.UnitCost, treatment, l.TaxRate, net, lineTax, gross,
				nullText(l.Note)); e != nil {
				return e
			}

			subtotal = subtotal.Add(net)
			tax = tax.Add(lineTax)
			total = total.Add(gross)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE supplier_quote
			SET subtotal_net = $2, tax_total = $3, total_inclusive = $4
			WHERE id = $1`, quoteID, subtotal, tax, total); e != nil {
			return e
		}

		// The first quote in moves the RFQ on, so the list distinguishes
		// "asked, waiting" from "answers arriving".
		if _, e := tx.Exec(ctx, `
			UPDATE rfq SET status = 'comparing'
			WHERE id = $1 AND status = 'issued'`, in.RFQID); e != nil {
			return e
		}

		read, e := s.readQuote(ctx, tx, scope.CompanyID, quoteID)
		out = read
		return e
	})
	if err != nil {
		return Quote{}, db.Translate(err, "")
	}
	return out, nil
}

// DeclineToQuote records that a supplier was asked and said no.
//
// Worth recording: "asked and declined" and "asked and never replied" lead a
// buyer to different conclusions about a supplier, and a missing quote row
// cannot tell them apart.
func (s *Service) DeclineToQuote(
	ctx context.Context, scope Scope, rfqID, supplierID uuid.UUID, reason string,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE rfq_invitation i
			SET declined_at = now(), decline_reason = $3
			FROM rfq r
			WHERE i.rfq_id = $1 AND i.supplier_id = $2
			  AND r.id = i.rfq_id AND r.company_id = $4`,
			rfqID, supplierID, nullText(reason), scope.CompanyID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That supplier was not asked to quote on this request.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// --- Comparison ----------------------------------------------------------

// Comparison is the side-by-side view B5.1 asks for.
type Comparison struct {
	RFQ RFQ `json:"rfq"`
	// Quotes carries every live quote. Superseded and rejected ones are left
	// out of the comparison but remain readable through the supplier archive:
	// comparing against a price the supplier has already withdrawn would be
	// comparing against something nobody is offering.
	Quotes []Quote `json:"quotes"`
	// Lowest names the cheapest live, unexpired quote by total. It is a
	// convenience for the eye, NOT a recommendation: B5.1 requires a person to
	// choose and to say why, because lead time and payment terms routinely
	// outweigh price.
	LowestQuoteID *uuid.UUID `json:"lowest_quote_id,omitempty"`
}

// Compare returns the quotes against one RFQ.
func (s *Service) Compare(
	ctx context.Context, scope Scope, rfqID uuid.UUID,
) (Comparison, error) {
	var out Comparison
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		head, e := s.readRFQ(ctx, tx, scope.CompanyID, rfqID)
		if e != nil {
			return e
		}
		out.RFQ = head
		out.Quotes = []Quote{}

		rows, e := tx.Query(ctx, `
			SELECT q.id FROM supplier_quote q
			WHERE q.rfq_id = $1 AND q.company_id = $2
			  AND q.status IN ('received', 'awarded')
			ORDER BY q.total_inclusive`, rfqID, scope.CompanyID)
		if e != nil {
			return e
		}
		ids := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, id := range ids {
			q, e := s.readQuote(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out.Quotes = append(out.Quotes, q)
			// Ordered by total ascending, so the first unexpired one is the
			// cheapest that could actually be accepted today.
			if out.LowestQuoteID == nil && !q.Expired {
				idCopy := q.ID
				out.LowestQuoteID = &idCopy
			}
		}
		return nil
	})
	if err != nil {
		return Comparison{}, db.Translate(err, "")
	}
	return out, nil
}

// --- Award ---------------------------------------------------------------

// Awarded is the result: the winning quote and the order it produced.
type Awarded struct {
	RFQ   RFQ   `json:"rfq"`
	Order Order `json:"order"`
}

// AwardQuote picks the winner and raises the purchase order.
//
// The reason is mandatory. B5.1 requires the choice to be recorded, and the
// question an auditor asks is never "which was cheapest" — the totals answer
// that — but "why did you not take it". A blank reason makes the whole sourcing
// record unable to answer the one question it exists for.
//
// An expired quote cannot be awarded: the supplier is no longer offering that
// price, and an order raised against it would be a commitment the shop has no
// grounds to expect them to honour.
func (s *Service) AwardQuote(
	ctx context.Context, scope Scope, rfqID, quoteID uuid.UUID, reason string,
) (Awarded, error) {
	if strings.TrimSpace(reason) == "" {
		return Awarded{}, errs.Validation(
			"Say why this supplier was chosen.").
			WithField("award_reason",
				"Cheapest is not always the reason, and this is the record "+
					"an audit reads.")
	}

	var out Awarded
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		var warehouseID uuid.UUID
		var requisitionID *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT status, warehouse_id, requisition_id FROM rfq
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			rfqID, scope.CompanyID).Scan(&status, &warehouseID, &requisitionID)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That request for quotation was not found.")
		}
		if e != nil {
			return e
		}
		if status == "awarded" {
			return errs.New(errs.CodeConflict,
				"That request for quotation has already been awarded.")
		}
		if status == "cancelled" {
			return errs.New(errs.CodeConflict,
				"That request for quotation was cancelled.")
		}

		var supplierID uuid.UUID
		var quoteStatus string
		var validUntil *time.Time
		e = tx.QueryRow(ctx, `
			SELECT supplier_id, status, valid_until FROM supplier_quote
			WHERE id = $1 AND rfq_id = $2 AND company_id = $3 FOR UPDATE`,
			quoteID, rfqID, scope.CompanyID).
			Scan(&supplierID, &quoteStatus, &validUntil)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That quote was not found on this request.")
		}
		if e != nil {
			return e
		}
		if quoteStatus != "received" {
			return errs.Newf(errs.CodeConflict,
				"That quote is %s, so it cannot be awarded.", quoteStatus)
		}
		if validUntil != nil && validUntil.Before(today()) {
			return errs.Newf(errs.CodeConflict,
				"That quote expired on %s. Ask the supplier to re-quote before "+
					"ordering against it.", validUntil.Format("2 Jan 2006"))
		}

		// The order's lines are the quote's lines: the quantities and prices
		// the supplier actually offered, not the ones that were asked for.
		lines, e := quoteLinesForOrder(ctx, tx, quoteID)
		if e != nil {
			return e
		}

		order, e := s.createOrderInTx(ctx, tx, scope, NewOrder{
			SupplierID:  supplierID,
			WarehouseID: warehouseID,
			Notes:       "Raised from " + rfqNumberOf(ctx, tx, rfqID),
			Lines:       lines,
		}, &rfqID, &quoteID, requisitionID)
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE supplier_quote SET status = 'awarded' WHERE id = $1`,
			quoteID); e != nil {
			return e
		}
		// Every other live quote on this request lost. They are kept, marked,
		// and never become orders.
		if _, e := tx.Exec(ctx, `
			UPDATE supplier_quote SET status = 'rejected'
			WHERE rfq_id = $1 AND id <> $2 AND status = 'received'`,
			rfqID, quoteID); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE rfq
			SET status = 'awarded', awarded_quote_id = $2, award_reason = $3,
			    awarded_by = $4, awarded_at = now()
			WHERE id = $1`,
			rfqID, quoteID, strings.TrimSpace(reason), scope.UserID); e != nil {
			return e
		}

		if requisitionID != nil {
			if _, e := tx.Exec(ctx, `
				UPDATE purchase_requisition SET status = 'ordered'
				WHERE id = $1 AND company_id = $2 AND status = 'sourcing'`,
				*requisitionID, scope.CompanyID); e != nil {
				return e
			}
		}

		head, e := s.readRFQ(ctx, tx, scope.CompanyID, rfqID)
		if e != nil {
			return e
		}
		out = Awarded{RFQ: head, Order: order}
		return nil
	})
	if err != nil {
		return Awarded{}, db.Translate(err, "")
	}
	return out, nil
}

// CancelRFQ abandons a request without awarding it.
func (s *Service) CancelRFQ(
	ctx context.Context, scope Scope, rfqID uuid.UUID, reason string,
) error {
	if strings.TrimSpace(reason) == "" {
		return errs.Validation("Say why the request is being cancelled.").
			WithField("cancel_reason",
				"The suppliers who were asked will want an answer.")
	}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE rfq SET status = 'cancelled', cancel_reason = $3
			WHERE id = $1 AND company_id = $2
			  AND status IN ('draft', 'issued', 'comparing')`,
			rfqID, scope.CompanyID, strings.TrimSpace(reason))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var status string
			e := tx.QueryRow(ctx,
				`SELECT status FROM rfq WHERE id = $1 AND company_id = $2`,
				rfqID, scope.CompanyID).Scan(&status)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That request for quotation was not found.")
			}
			if e != nil {
				return e
			}
			return errs.Newf(errs.CodeConflict,
				"That request for quotation is %s and cannot be cancelled.",
				status)
		}
		return nil
	})
	return db.Translate(err, "")
}

// --- Reads ---------------------------------------------------------------

// ListRFQs returns requests for quotation, newest first.
func (s *Service) ListRFQs(
	ctx context.Context, scope Scope, status string,
) ([]RFQ, error) {
	out := []RFQ{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, rfqSelect+`
			WHERE r.company_id = $1 AND ($2 = '' OR r.status = $2)
			ORDER BY r.created_at DESC
			LIMIT 500`, scope.CompanyID, status)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanRFQ(rows)
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

// QuotesFromSupplier is B5.1's historical archive: every quote a supplier has
// given, won or lost, so the next negotiation starts from what they charged
// last time.
func (s *Service) QuotesFromSupplier(
	ctx context.Context, scope Scope, supplierID uuid.UUID,
) ([]Quote, error) {
	out := []Quote{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The supplier must belong to THIS company. Without the check a caller
		// naming another shop's supplier gets an empty list and a 200, which
		// reads as "that supplier has never quoted" rather than "that supplier
		// is none of your business" — and an empty answer is a worse disclosure
		// than a refusal, because it invites the caller to believe it.
		if e := checkBelongs(ctx, tx, "supplier", supplierID, scope.CompanyID,
			"That supplier was not found."); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT id FROM supplier_quote
			WHERE supplier_id = $1 AND company_id = $2
			ORDER BY received_on DESC, created_at DESC
			LIMIT 200`, supplierID, scope.CompanyID)
		if e != nil {
			return e
		}
		ids := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		for _, id := range ids {
			q, e := s.readQuote(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out = append(out, q)
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

const rfqSelect = `
	SELECT r.id, r.rfq_number, r.status, r.requisition_id, r.warehouse_id,
	       r.closes_on, r.currency, coalesce(r.notes, ''),
	       r.awarded_quote_id, coalesce(r.award_reason, ''),
	       coalesce(u.full_name, ''), r.awarded_at,
	       coalesce(r.cancel_reason, ''), r.created_at, r.issued_at
	FROM rfq r
	LEFT JOIN app_user u ON u.id = r.awarded_by`

func (s *Service) readRFQ(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (RFQ, error) {
	row := tx.QueryRow(ctx, rfqSelect+`
		WHERE r.id = $1 AND r.company_id = $2`, id, companyID)
	out, err := scanRFQ(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RFQ{}, errs.New(errs.CodeNotFound,
			"That request for quotation was not found.")
	}
	if err != nil {
		return RFQ{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.line_no, l.variant_id, coalesce(v.sku, ''),
		       l.description, l.qty
		FROM rfq_line l
		LEFT JOIN variant v ON v.id = l.variant_id
		WHERE l.rfq_id = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return RFQ{}, err
	}
	for rows.Next() {
		var l RFQLineView
		var qty decimal.Decimal
		if e := rows.Scan(&l.ID, &l.LineNo, &l.VariantID, &l.SKU,
			&l.Description, &qty); e != nil {
			rows.Close()
			return RFQ{}, e
		}
		l.Qty = qty.String()
		out.Lines = append(out.Lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RFQ{}, err
	}

	inv, err := tx.Query(ctx, `
		SELECT i.supplier_id, s.legal_name, i.invited_at, i.declined_at,
		       coalesce(i.decline_reason, ''),
		       EXISTS (SELECT 1 FROM supplier_quote q
		               WHERE q.rfq_id = i.rfq_id
		                 AND q.supplier_id = i.supplier_id
		                 AND q.status IN ('received','awarded'))
		FROM rfq_invitation i
		JOIN supplier s ON s.id = i.supplier_id
		WHERE i.rfq_id = $1 ORDER BY s.legal_name`, id)
	if err != nil {
		return RFQ{}, err
	}
	defer inv.Close()
	for inv.Next() {
		var v RFQInvitedView
		var invitedAt time.Time
		var declinedAt *time.Time
		if e := inv.Scan(&v.SupplierID, &v.SupplierName, &invitedAt,
			&declinedAt, &v.DeclineReason, &v.Quoted); e != nil {
			return RFQ{}, e
		}
		v.InvitedAt = invitedAt.UTC().Format(time.RFC3339)
		if declinedAt != nil {
			v.DeclinedAt = declinedAt.UTC().Format(time.RFC3339)
		}
		out.Invited = append(out.Invited, v)
	}
	return out, inv.Err()
}

func scanRFQ(row rowScanner) (RFQ, error) {
	var r RFQ
	var closesOn *time.Time
	var awardedAt, issuedAt *time.Time
	var createdAt time.Time
	if err := row.Scan(&r.ID, &r.Number, &r.Status, &r.RequisitionID,
		&r.WarehouseID, &closesOn, &r.Currency, &r.Notes, &r.AwardedQuoteID,
		&r.AwardReason, &r.AwardedBy, &awardedAt, &r.CancelReason,
		&createdAt, &issuedAt); err != nil {
		return RFQ{}, err
	}
	r.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if closesOn != nil {
		r.ClosesOn = closesOn.Format("2006-01-02")
	}
	if awardedAt != nil {
		r.AwardedAt = awardedAt.UTC().Format(time.RFC3339)
	}
	if issuedAt != nil {
		r.IssuedAt = issuedAt.UTC().Format(time.RFC3339)
	}
	r.Lines = []RFQLineView{}
	r.Invited = []RFQInvitedView{}
	return r, nil
}

func (s *Service) readQuote(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Quote, error) {
	var q Quote
	var receivedOn time.Time
	var validUntil *time.Time
	var subtotal, tax, total decimal.Decimal

	err := tx.QueryRow(ctx, `
		SELECT q.id, q.rfq_id, q.supplier_id, s.legal_name,
		       coalesce(q.quote_number, ''), q.revision, q.status,
		       q.received_on, q.valid_until, q.currency,
		       q.lead_time_days, q.payment_terms_days,
		       coalesce(q.quality_note, ''), q.subtotal_net, q.tax_total,
		       q.total_inclusive, coalesce(q.notes, '')
		FROM supplier_quote q
		JOIN supplier s ON s.id = q.supplier_id
		WHERE q.id = $1 AND q.company_id = $2`, id, companyID).
		Scan(&q.ID, &q.RFQID, &q.SupplierID, &q.SupplierName, &q.QuoteNumber,
			&q.Revision, &q.Status, &receivedOn, &validUntil, &q.Currency,
			&q.LeadTimeDays, &q.TermsDays, &q.QualityNote, &subtotal, &tax,
			&total, &q.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Quote{}, errs.New(errs.CodeNotFound, "That quote was not found.")
	}
	if err != nil {
		return Quote{}, err
	}

	q.ReceivedOn = receivedOn.Format("2006-01-02")
	if validUntil != nil {
		q.ValidUntil = validUntil.Format("2006-01-02")
		q.Expired = validUntil.Before(today())
	}
	q.SubtotalNet = subtotal.StringFixed(2)
	q.TaxTotal = tax.StringFixed(2)
	q.Total = total.StringFixed(2)
	q.Lines = []QuoteLineView{}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.rfq_line_id, l.line_no, l.variant_id,
		       coalesce(v.sku, ''), l.description, l.qty, l.unit_cost,
		       l.tax_treatment, l.net_amount, l.tax_amount, l.gross_amount
		FROM supplier_quote_line l
		LEFT JOIN variant v ON v.id = l.variant_id
		WHERE l.quote_id = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return Quote{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var l QuoteLineView
		var qty, cost, net, lineTax, gross decimal.Decimal
		if e := rows.Scan(&l.ID, &l.RFQLineID, &l.LineNo, &l.VariantID, &l.SKU,
			&l.Description, &qty, &cost, &l.TaxTreatment, &net, &lineTax,
			&gross); e != nil {
			return Quote{}, e
		}
		l.Qty = qty.String()
		l.UnitCost = cost.StringFixed(4)
		l.NetAmount = net.StringFixed(2)
		l.TaxAmount = lineTax.StringFixed(2)
		l.GrossAmount = gross.StringFixed(2)
		q.Lines = append(q.Lines, l)
	}
	return q, rows.Err()
}

// quoteLinesForOrder turns the winning quote's lines into order lines.
func quoteLinesForOrder(
	ctx context.Context, tx pgx.Tx, quoteID uuid.UUID,
) ([]OrderLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT variant_id, description, qty, unit_cost, tax_treatment, tax_rate
		FROM supplier_quote_line
		WHERE quote_id = $1 ORDER BY line_no`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []OrderLine{}
	for rows.Next() {
		var l OrderLine
		if e := rows.Scan(&l.VariantID, &l.Description, &l.Qty, &l.UnitCost,
			&l.TaxTreatment, &l.TaxRate); e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func rfqNumberOf(ctx context.Context, tx pgx.Tx, rfqID uuid.UUID) string {
	var n string
	_ = tx.QueryRow(ctx, `SELECT rfq_number FROM rfq WHERE id = $1`,
		rfqID).Scan(&n)
	return n
}

// today is the current date with the clock stripped, for comparing against a
// DATE column. Comparing a date to a timestamp would make a quote valid until
// today expire at midnight this morning rather than tonight.
func today() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
