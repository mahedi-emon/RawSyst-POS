package sales

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// InvoiceView is a finalised invoice as a till or a back-office screen reads it.
//
// Money is carried as decimal here and serialised as STRINGS at the boundary
// (`07-api-conventions.md` §2). Cost and margin are deliberately absent: a
// cashier has no business seeing them, and a receipt has no use for them.
type InvoiceView struct {
	ID          uuid.UUID `json:"id"`
	UUID        uuid.UUID `json:"uuid"`
	DocType     string    `json:"doc_type"`
	HumanNumber *string   `json:"human_number"`
	State       string    `json:"state"`

	IssueDate string `json:"issue_date"`
	Currency  string `json:"currency"`

	SubtotalNet    string `json:"subtotal_net"`
	DiscountTotal  string `json:"discount_total"`
	TaxTotal       string `json:"tax_total"`
	TotalInclusive string `json:"total_inclusive"`

	Lines   []InvoiceLineView   `json:"lines"`
	Tenders []InvoiceTenderView `json:"tenders"`

	ZATCA *InvoiceChainView `json:"zatca"`

	// Who the invoice was raised for. Absent on the overwhelming majority of
	// sales — a shop does not ask a name to sell a bottle of water — and
	// present whenever any part of it went on account.
	Customer *InvoiceCustomerView `json:"customer"`

	// What has happened to this document since it was issued. UI spec §5 names
	// the audit trail as part of the screen, because the answer to "who
	// reprinted this and when" is the first thing asked in a dispute.
	Audit []InvoiceAuditView `json:"audit"`

	// ParentInvoiceID is set on a credit or debit note. A note without the
	// invoice it corrects has no meaning, and ZATCA requires the reference.
	ParentInvoiceID *uuid.UUID `json:"parent_invoice_id"`
}

type InvoiceLineView struct {
	LineNo               int        `json:"line_no"`
	VariantID            *uuid.UUID `json:"variant_id"`
	Description          string     `json:"description"`
	DescriptionAr        *string    `json:"description_ar"`
	Qty                  string     `json:"qty"`
	UnitPrice            string     `json:"unit_price"`
	LineDiscount         string     `json:"line_discount"`
	InvoiceDiscountAlloc string     `json:"invoice_discount_alloc"`
	TaxTreatment         string     `json:"tax_treatment"`
	TaxRate              string     `json:"tax_rate"`
	TaxAmount            string     `json:"tax_amount"`
	NetAmount            string     `json:"net_amount"`
	GrossAmount          string     `json:"gross_amount"`
}

type InvoiceTenderView struct {
	TenderNo         int     `json:"tender_no"`
	Method           string  `json:"method"`
	Amount           string  `json:"amount"`
	Reference        *string `json:"reference"`
	SettlementStatus string  `json:"settlement_status"`
}

// InvoiceChainView is the invoice's position on its terminal's ZATCA chain.
//
// The till needs the ICV and the PIH to build and sign the XML locally. Nothing
// here is secret — a hash and a counter — and the signing key that turns them
// into a stamp never leaves the terminal's OS keystore.
type InvoiceChainView struct {
	ICV           int64  `json:"icv"`
	PIH           string `json:"pih"`
	InvoiceHash   string `json:"invoice_hash"`
	SchemaVersion string `json:"schema_version"`

	// The QR payload the terminal produced, base64 TLV. Null until the
	// terminal has signed and handed the document back, which is the ordinary
	// state while the byte-level format is still an unverified release
	// blocker. The screen says "not signed yet" rather than showing a blank
	// code, because a QR that does not scan is worse than none.
	QRTLV *string `json:"qr_tlv"`

	// Submission, as far as it has gone. All null while reporting is gated.
	SubmittedAt  *string `json:"submitted_at"`
	ResponseCode *int    `json:"response_code"`
	RejectReason *string `json:"reject_reason"`
}

// InvoiceCustomerView names the buyer. Only the name and the id: a screen
// showing one invoice does not need the credit limit and the ledger with it,
// and the customer screen already answers those.
type InvoiceCustomerView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// InvoiceAuditView is one thing that happened to this document.
//
// `actor_label` is denormalised in the audit log on purpose — it survives the
// user being deleted, so a trail does not become a list of missing people.
type InvoiceAuditView struct {
	Action     string  `json:"action"`
	ActorLabel *string `json:"actor_label"`
	OccurredAt string  `json:"occurred_at"`
	Device     *string `json:"device_label"`
}

// Read returns one finalised invoice.
//
// Scoped by tenant through row-level security, so another tenant's invoice
// reads as absent rather than forbidden — a 403 would confirm the record
// exists, which leaks across the tenant boundary (`07-api-conventions.md` §3).
func (s *Service) Read(
	ctx context.Context, tenantID, invoiceID uuid.UUID,
) (InvoiceView, error) {
	if s.pool == nil {
		return InvoiceView{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	var out InvoiceView
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var subtotal, discount, tax, total decimal.Decimal
		var issueDay string

		e := tx.QueryRow(ctx, `
			SELECT id, uuid, doc_type, human_number, state,
			       to_char(issue_date, 'YYYY-MM-DD'), currency,
			       subtotal_net, discount_total, tax_total, total_inclusive,
			       parent_invoice_id
			FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&out.ID, &out.UUID, &out.DocType, &out.HumanNumber, &out.State,
				&issueDay, &out.Currency,
				&subtotal, &discount, &tax, &total, &out.ParentInvoiceID)

		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That invoice was not found.")
		}
		if e != nil {
			return e
		}

		out.IssueDate = issueDay
		out.SubtotalNet = subtotal.String()
		out.DiscountTotal = discount.String()
		out.TaxTotal = tax.String()
		out.TotalInclusive = total.String()

		if e := readInvoiceLines(ctx, tx, invoiceID, &out); e != nil {
			return e
		}
		if e := readInvoiceTenders(ctx, tx, invoiceID, &out); e != nil {
			return e
		}
		if e := readInvoiceChain(ctx, tx, invoiceID, &out); e != nil {
			return e
		}
		if e := readInvoiceCustomer(ctx, tx, invoiceID, &out); e != nil {
			return e
		}
		return readInvoiceAudit(ctx, tx, invoiceID, &out)
	})
	return out, err
}

func readInvoiceLines(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, out *InvoiceView) error {
	rows, err := tx.Query(ctx, `
		SELECT line_no, variant_id, description, description_ar, qty, unit_price,
		       line_discount, invoice_discount_alloc, tax_treatment, tax_rate,
		       tax_amount, net_amount, gross_amount
		FROM sales_invoice_line WHERE invoice_id = $1 ORDER BY line_no`, invoiceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	out.Lines = []InvoiceLineView{}
	for rows.Next() {
		var l InvoiceLineView
		var qty, price, lineDisc, allocDisc, rate, taxAmt, net, gross decimal.Decimal
		if err := rows.Scan(&l.LineNo, &l.VariantID, &l.Description, &l.DescriptionAr,
			&qty, &price, &lineDisc, &allocDisc, &l.TaxTreatment, &rate,
			&taxAmt, &net, &gross); err != nil {
			return err
		}
		l.Qty, l.UnitPrice = qty.String(), price.String()
		l.LineDiscount, l.InvoiceDiscountAlloc = lineDisc.String(), allocDisc.String()
		l.TaxRate, l.TaxAmount = rate.String(), taxAmt.String()
		l.NetAmount, l.GrossAmount = net.String(), gross.String()
		out.Lines = append(out.Lines, l)
	}
	return rows.Err()
}

func readInvoiceTenders(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, out *InvoiceView) error {
	rows, err := tx.Query(ctx, `
		SELECT tender_no, method, amount, reference, settlement_status
		FROM sales_tender WHERE invoice_id = $1 ORDER BY tender_no`, invoiceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	out.Tenders = []InvoiceTenderView{}
	for rows.Next() {
		var t InvoiceTenderView
		var amount decimal.Decimal
		if err := rows.Scan(&t.TenderNo, &t.Method, &amount, &t.Reference,
			&t.SettlementStatus); err != nil {
			return err
		}
		t.Amount = amount.String()
		out.Tenders = append(out.Tenders, t)
	}
	return rows.Err()
}

func readInvoiceChain(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, out *InvoiceView) error {
	var c InvoiceChainView
	err := tx.QueryRow(ctx, `
		SELECT icv, pih, invoice_hash, schema_version, qr_tlv,
		       to_char(submitted_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
		       response_code, reject_reason
		FROM zatca_invoice WHERE invoice_id = $1`, invoiceID).
		Scan(&c.ICV, &c.PIH, &c.InvoiceHash, &c.SchemaVersion, &c.QRTLV,
			&c.SubmittedAt, &c.ResponseCode, &c.RejectReason)

	if errors.Is(err, pgx.ErrNoRows) {
		// A draft has no chain position, which is correct: an ICV is consumed
		// only when a legal document is issued.
		return nil
	}
	if err != nil {
		return err
	}
	out.ZATCA = &c
	return nil
}

// readInvoiceCustomer names the buyer, when there is one.
func readInvoiceCustomer(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, out *InvoiceView) error {
	var c InvoiceCustomerView
	err := tx.QueryRow(ctx, `
		SELECT c.id, c.name
		FROM sales_invoice i JOIN customer c ON c.id = i.customer_id
		WHERE i.id = $1`, invoiceID).Scan(&c.ID, &c.Name)

	if errors.Is(err, pgx.ErrNoRows) {
		// A walk-in. The common case, and not a gap.
		return nil
	}
	if err != nil {
		return err
	}
	out.Customer = &c
	return nil
}

// readInvoiceAudit reads what has happened to this document.
//
// Newest first, and capped: a document reprinted two hundred times has a
// problem the screen cannot solve by listing all of them, and the cap keeps one
// pathological invoice from dominating the response.
func readInvoiceAudit(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, out *InvoiceView) error {
	out.Audit = []InvoiceAuditView{}

	rows, err := tx.Query(ctx, `
		SELECT action, actor_label,
		       to_char(occurred_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), device_label
		FROM audit_log
		WHERE entity_type = 'sales_invoice' AND entity_id = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT 50`, invoiceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var a InvoiceAuditView
		if e := rows.Scan(&a.Action, &a.ActorLabel, &a.OccurredAt, &a.Device); e != nil {
			return e
		}
		out.Audit = append(out.Audit, a)
	}
	return rows.Err()
}

// ReturnableLine is one line of an invoice, with how much of it is still
// returnable.
//
// Quantity AND money, because a partial return needs both: refunding
// proportionally requires knowing what the line was sold for after its share
// of the invoice discount, and a till recomputing that from the unit price
// would drift from the invoice by a hallala at a time.
type ReturnableLine struct {
	LineID          string `json:"line_id"`
	LineNo          int    `json:"line_no"`
	VariantID       string `json:"variant_id,omitempty"`
	Description     string `json:"description"`
	QtySold         string `json:"qty_sold"`
	QtyReturned     string `json:"qty_returned"`
	QtyReturnable   string `json:"qty_returnable"`
	UnitPrice       string `json:"unit_price"`
	TaxTreatment    string `json:"tax_treatment"`
	TaxRate         string `json:"tax_rate"`
	NetAmount       string `json:"net_amount"`
	TaxAmount       string `json:"tax_amount"`
	GrossAmount     string `json:"gross_amount"`
	NetReturnable   string `json:"net_returnable"`
	TaxReturnable   string `json:"tax_returnable"`
	GrossReturnable string `json:"gross_returnable"`
}

// Returnable reports what is still owed back on an invoice.
//
// A till must never work this out for itself. Whether a line has already been
// returned, and how much of its money went with it, lives in the credit notes
// against the invoice — which a terminal that was offline when they were
// raised has never seen. Asking the server is the only way to avoid refunding
// the same jacket twice.
func (s *Service) Returnable(
	ctx context.Context, tenantID, invoiceID uuid.UUID,
) ([]ReturnableLine, error) {
	out := []ReturnableLine{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT line_id, line_no, coalesce(variant_id::text, ''), description,
			       qty_sold::text, qty_returned::text, qty_returnable::text,
			       unit_price::text, tax_treatment, tax_rate::text,
			       net_amount::text, tax_amount::text,
			       (net_amount + tax_amount)::text AS gross_amount,
			       -- What is LEFT: what the line was sold for, less what has
			       -- already gone back. The function reports both halves and
			       -- deliberately does not subtract them for us, because the
			       -- return path needs the two separately to allocate a partial
			       -- return proportionally.
			       (net_amount - net_returned)::text AS net_returnable,
			       (tax_amount - tax_returned)::text AS tax_returnable,
			       ((net_amount - net_returned)
			        + (tax_amount - tax_returned))::text AS gross_returnable
			FROM returnable_lines($1)
			ORDER BY line_no`, invoiceID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var l ReturnableLine
			if e := rows.Scan(&l.LineID, &l.LineNo, &l.VariantID, &l.Description,
				&l.QtySold, &l.QtyReturned, &l.QtyReturnable,
				&l.UnitPrice, &l.TaxTreatment, &l.TaxRate,
				&l.NetAmount, &l.TaxAmount, &l.GrossAmount,
				&l.NetReturnable, &l.TaxReturnable, &l.GrossReturnable); e != nil {
				return e
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

// Reprint records that a copy of an invoice was printed.
//
// UI spec §5: "Reprint is available and logged — reprinting is not reissuing."
// That sentence is the whole feature. A reprint produces no new document, no
// new number and no new ICV; it is a copy of something already issued, and the
// only thing that changes in the system is that somebody now knows a copy went
// out and who asked for it.
//
// The audit row is written directly rather than through identity's helper,
// which is unexported — provisioning already does the same for tenant creation.
// Blueprint D4 fixes the six fields, so the shape is not this function's to
// choose.
func (s *Service) Reprint(
	ctx context.Context, tenantID, invoiceID, userID uuid.UUID,
) error {
	if s.pool == nil {
		return errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	return s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Read first, in the same transaction. Logging a reprint of an invoice
		// that does not exist would put a fiction in the audit trail, and an
		// audit trail that can be written to about nothing is not evidence.
		var humanNumber *string
		e := tx.QueryRow(ctx,
			`SELECT human_number FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&humanNumber)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That invoice was not found.")
		}
		if e != nil {
			return e
		}

		// The label is read here rather than passed in, because audit_log
		// denormalises it on purpose: it has to survive the user being
		// deleted, so the trail does not become a list of missing people.
		var actorLabel string
		if e := tx.QueryRow(ctx,
			`SELECT coalesce(nullif(btrim(full_name), ''), email)
			 FROM app_user WHERE id = $1`, userID).Scan(&actorLabel); e != nil &&
			!errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		after := map[string]any{}
		if humanNumber != nil {
			after["human_number"] = *humanNumber
		}
		payload, e := json.Marshal(after)
		if e != nil {
			return e
		}

		_, e = tx.Exec(ctx, `
			INSERT INTO audit_log
			  (tenant_id, actor_id, actor_label, action, entity_type, entity_id,
			   after_value)
			VALUES ($1,$2,$3,'invoice_reprinted','sales_invoice',$4,$5)`,
			tenantID, userID, nullIfBlank(actorLabel), invoiceID, payload)
		return e
	})
}

func nullIfBlank(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
