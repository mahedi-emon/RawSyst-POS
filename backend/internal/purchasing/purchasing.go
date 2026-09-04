// Buying stock, and paying for it.
//
// Blueprint B5's chain, from the supplier through to the payment:
//
//	PO → GRN → Bill → three-way match → Payment
//
// Requisition, RFQ and quote comparison are an approval workflow that FEEDS a
// purchase order. Nothing downstream depends on them, so they are absent rather
// than half-present — a requisition screen that produced nothing an accountant
// could act on would be worse than no requisition screen.
//
// # The rule everything here obeys
//
// B5: only a goods receipt increases stock. A purchase order is an intention,
// and an intention that inflated inventory would let a shop show stock it has
// not received and cost it has not incurred. So `ReceiveGoods` is the only
// function in this package that the inventory engine ever hears from.
//
// # Nothing here reimplements anything
//
// Stock and cost layers go through inventory.Receive, exactly as every other
// receipt does. The journal goes through accounting.PostByRule against the
// `purchase.credit` and `payment.supplier` rules that migration 0025 already
// seeded. Idempotency reuses the UUID-assigned-before-the-call pattern the
// sale service uses. This package sequences those things and owns only the
// three-way match, which is genuinely new.

package purchasing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/workflow"
)

// Service owns the purchasing chain.
type Service struct {
	pool *db.Pool

	// approvals is F1's engine. Optional, for the reason given on the same
	// field in the expenses service: an installation without it issues orders
	// exactly as it always did.
	approvals *workflow.Service

	// rates resolves G2's exchange rates. Optional in the same way: a business
	// buying only in its own currency never needs one, and a bill in a foreign
	// currency is refused rather than booked at par when it is absent.
	rates *fx.Service

	// rules is the regulatory rule registry, and it is what says how much tax
	// a purchase carries.
	//
	// NOT optional in the way the two above are. Without it an order cannot be
	// priced at all, because the alternative is taking a rate from whoever is
	// calling -- and the expenses service, which is the same side of the
	// business, already says why that is unacceptable: "a client that could
	// state its own VAT rate could state what the return claims."
	rules *registry.Service
}

// WithRates wires G2's exchange rates, which a foreign-currency bill needs.
func (s *Service) WithRates(f *fx.Service) *Service {
	s.rates = f
	return s
}

// WithRules wires the regulatory rule registry, which prices every line.
func (s *Service) WithRules(r *registry.Service) *Service {
	s.rules = r
	return s
}

// WithApprovals wires F1's approval engine.
func (s *Service) WithApprovals(w *workflow.Service) *Service {
	s.approvals = w
	return s
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// taxRateFor is what a purchase line is taxed at, from the register.
//
// The rate is NEVER taken from the caller. That rule is not new here -- the
// expenses service states it on the same kind of document, and states why:
//
//	The RATE comes from the registry at the expense date, never from the
//	caller: a client that could state its own VAT rate could state what the
//	return claims. The TREATMENT comes from the caller, because only they know
//	whether the supplier charged VAT.
//
// Purchasing took a rate from the request body until now, and defaulted it to
// zero when it was absent, so a client sending nothing raised orders with no
// tax on them and a client sending "15" for fifteen per cent raised an order
// fifteen hundred per cent too large. Both are silent, and both end up on a
// document a supplier can hold the shop to and in the input tax the shop
// reclaims.
//
// The treatment is the caller's, and is checked against what the country
// allows on that date -- the same check catalog.checkTreatment makes when a
// product is set up.
//
// A treatment that charges nothing resolves to zero without asking the
// register, because there is no rate to record and no source to cite for "this
// is not taxed". A treatment the register cannot price -- the United States,
// where tax is set by state, county and city rather than nationally -- is
// refused by registry.TaxRate in its own words, which name what has to be
// recorded. Nothing is defaulted, because every default here would be an
// invented legal value.
func (s *Service) taxRateFor(
	ctx context.Context, tx pgx.Tx, scope Scope, country, treatment string,
	asOf time.Time,
) (string, decimal.Decimal, error) {
	if s.rules == nil {
		return "", decimal.Zero, errs.New(errs.CodeInternal,
			"The purchasing service was built without the regulatory rule registry.")
	}

	rules, err := catalog.TaxRulesFor(ctx, s.rules, tx, country, asOf, scope.TenantID)
	if err != nil {
		return "", decimal.Zero, err
	}

	treatment = strings.ToLower(strings.TrimSpace(treatment))
	if treatment == "" {
		treatment = "standard"
	}
	if !rules.Allows(treatment) {
		return "", decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"A line marked %q cannot be bought here: that is not a tax "+
				"treatment %s uses.", treatment, strings.ToUpper(country))
	}

	// Only the treatments that charge have a rate to look up.
	switch treatment {
	case "standard", "reduced":
		rate, e := s.rules.TaxRate(ctx, tx, country, treatment, asOf, scope.TenantID)
		if e != nil {
			return "", decimal.Zero, e
		}
		return treatment, rate, nil
	default:
		return treatment, decimal.Zero, nil
	}
}

// agreesOnRate refuses a request whose rate is not the one on file.
//
// `tax_rate` used to be the value the line was priced with. It is now an
// optional ASSERTION: send it and the server checks it, omit it and the
// register answers. Neither ignoring it nor removing it would do.
//
// Ignoring it silently is how a caller keeps sending 0.05 for a year and never
// learns that the rate went to 0.15 — their totals would quietly become right
// while their own screen kept showing the old figure.
//
// Removing it breaks every caller at once for a field most of them are sending
// correctly, and fifty-four assertions in this repository's own tests are
// sending exactly the rate the register holds.
//
// So: disagreement is an error that names both numbers, and agreement costs
// nothing. Zero is treated as "not stated", because the JSON has no way to
// distinguish an absent rate from a zero one and a zero-rated line legitimately
// resolves to zero anyway.
func agreesOnRate(stated, resolved decimal.Decimal, index int) error {
	if stated.IsZero() || stated.Equal(resolved) {
		return nil
	}
	return errs.Newf(errs.CodeInvalidInput,
		"Line %d states a tax rate of %s and the rate on file is %s. The rate "+
			"comes from the regulatory register, so either leave it out or "+
			"send the one in force.",
		index+1, stated.String(), resolved.String())
}

// countryOf is the market whose rules price this company's purchases.
func countryOf(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (string, error) {
	var country string
	err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, companyID).Scan(&country)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return country, err
}

// Scope is which books a purchase belongs to.
//
// Company rather than device: purchasing happens in a back office, not at a
// till, so there is no registered terminal to resolve it from. The handler
// checks the caller may access the company before anything here runs.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// --- Suppliers -----------------------------------------------------------

type NewSupplier struct {
	Code        string
	LegalName   string
	NameAr      string
	Contact     string
	Email       string
	Phone       string
	VATNumber   string
	CRNumber    string
	Country     string
	TermsDays   int
	CreditLimit *decimal.Decimal
	Notes       string
}

type Supplier struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	LegalName   string    `json:"legal_name"`
	NameAr      string    `json:"name_ar,omitempty"`
	Contact     string    `json:"contact_name,omitempty"`
	Email       string    `json:"email,omitempty"`
	Phone       string    `json:"phone,omitempty"`
	VATNumber   string    `json:"vat_number,omitempty"`
	CRNumber    string    `json:"cr_number,omitempty"`
	Country     string    `json:"country,omitempty"`
	TermsDays   int       `json:"payment_terms_days"`
	CreditLimit string    `json:"credit_limit,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	IsActive    bool      `json:"is_active"`
	// Outstanding is what is owed right now. Carried on the list because a
	// buyer choosing a supplier needs to know they are already 40,000 behind
	// on that account, and going to look it up separately means they will not.
	Outstanding string `json:"outstanding"`
}

func (s *Service) CreateSupplier(
	ctx context.Context, scope Scope, in NewSupplier,
) (Supplier, error) {
	if err := validateSupplier(in); err != nil {
		return Supplier{}, err
	}

	var out Supplier
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO supplier
			  (tenant_id, company_id, code, legal_name, name_ar, contact_name,
			   email, phone, vat_number, cr_number, country,
			   payment_terms_days, credit_limit, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,lower($11),$12,$13,$14,$15)
			RETURNING id, code, legal_name, coalesce(name_ar,''),
			          coalesce(contact_name,''), coalesce(email,''),
			          coalesce(phone,''), coalesce(vat_number,''),
			          coalesce(cr_number,''), coalesce(country,''),
			          payment_terms_days, coalesce(credit_limit::text,''),
			          coalesce(notes,''), is_active`,
			scope.TenantID, scope.CompanyID, in.Code, in.LegalName,
			nullText(in.NameAr), nullText(in.Contact), nullText(in.Email),
			nullText(in.Phone), nullText(in.VATNumber), nullText(in.CRNumber),
			nullText(in.Country), in.TermsDays, in.CreditLimit,
			nullText(in.Notes), scope.UserID,
		).Scan(&out.ID, &out.Code, &out.LegalName, &out.NameAr, &out.Contact,
			&out.Email, &out.Phone, &out.VATNumber, &out.CRNumber, &out.Country,
			&out.TermsDays, &out.CreditLimit, &out.Notes, &out.IsActive)
	})
	if err != nil {
		return Supplier{}, conflictMessage(
			db.Translate(err, "That supplier was not found."),
			"A supplier already uses the code "+in.Code+". Codes are how you find "+
				"them in a list, so each one has to be different.")
	}
	out.Outstanding = "0.00"
	return out, nil
}

func validateSupplier(in NewSupplier) error {
	e := errs.Validation("Some supplier details are missing.")
	bad := false

	if trim(in.Code) == "" {
		e.WithField("code", "Give the supplier a short code you will recognise.")
		bad = true
	}
	if trim(in.LegalName) == "" {
		e.WithField("legal_name", "Enter the supplier's registered name.")
		bad = true
	}
	if in.TermsDays < 0 || in.TermsDays > 365 {
		e.WithField("payment_terms_days",
			"Payment terms run from 0 to 365 days.")
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

// ListSuppliers returns the company's suppliers with what is owed to each.
func (s *Service) ListSuppliers(
	ctx context.Context, scope Scope, search string, includeInactive bool,
) ([]Supplier, error) {
	out := []Supplier{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT s.id, s.code, s.legal_name, coalesce(s.name_ar,''),
			       coalesce(s.contact_name,''), coalesce(s.email,''),
			       coalesce(s.phone,''), coalesce(s.vat_number,''),
			       coalesce(s.cr_number,''), coalesce(s.country,''),
			       s.payment_terms_days, coalesce(s.credit_limit::text,''),
			       coalesce(s.notes,''), s.is_active,
			       coalesce((
			         SELECT sum(b.total_inclusive - b.amount_paid)
			         FROM purchase_bill b
			         WHERE b.supplier_id = s.id
			           AND b.status IN ('matched','blocked','approved')
			       ), 0)::text
			FROM supplier s
			WHERE s.company_id = $1
			  AND ($2 OR s.is_active)
			  AND ($3 = '' OR s.legal_name ILIKE '%'||$3||'%'
			                OR s.code ILIKE '%'||$3||'%')
			ORDER BY s.legal_name`,
			scope.CompanyID, includeInactive, search)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var sup Supplier
			if e := rows.Scan(&sup.ID, &sup.Code, &sup.LegalName, &sup.NameAr,
				&sup.Contact, &sup.Email, &sup.Phone, &sup.VATNumber,
				&sup.CRNumber, &sup.Country, &sup.TermsDays, &sup.CreditLimit,
				&sup.Notes, &sup.IsActive, &sup.Outstanding); e != nil {
				return e
			}
			out = append(out, sup)
		}
		return rows.Err()
	})
	return out, err
}

// --- Purchase orders -----------------------------------------------------

type OrderLine struct {
	VariantID    uuid.UUID
	Description  string
	Qty          decimal.Decimal
	UnitCost     decimal.Decimal
	TaxTreatment string
	TaxRate      decimal.Decimal
}

type NewOrder struct {
	SupplierID  uuid.UUID
	WarehouseID uuid.UUID
	ExpectedOn  *time.Time
	Notes       string
	Lines       []OrderLine
}

type Order struct {
	ID          uuid.UUID       `json:"id"`
	PONumber    string          `json:"po_number"`
	SupplierID  uuid.UUID       `json:"supplier_id"`
	Supplier    string          `json:"supplier"`
	WarehouseID uuid.UUID       `json:"warehouse_id"`
	Status      string          `json:"status"`
	OrderedOn   string          `json:"ordered_on"`
	ExpectedOn  string          `json:"expected_on,omitempty"`
	Currency    string          `json:"currency"`
	SubtotalNet string          `json:"subtotal_net"`
	TaxTotal    string          `json:"tax_total"`
	Total       string          `json:"total_inclusive"`
	Notes       string          `json:"notes,omitempty"`
	Lines       []OrderLineView `json:"lines,omitempty"`
}

type OrderLineView struct {
	ID             uuid.UUID `json:"id"`
	LineNo         int       `json:"line_no"`
	VariantID      uuid.UUID `json:"variant_id"`
	Description    string    `json:"description"`
	QtyOrdered     string    `json:"qty_ordered"`
	QtyReceived    string    `json:"qty_received"`
	QtyOutstanding string    `json:"qty_outstanding"`
	QtyBilled      string    `json:"qty_billed"`
	UnitCost       string    `json:"unit_cost"`
	TaxTreatment   string    `json:"tax_treatment"`
	// TaxRate is a FRACTION, like everywhere else: 0.15 is fifteen per cent,
	// matching sales_line_rate_sane. Returned because PUT rewrites a draft's
	// lines wholesale, so an editor has to send back what it read -- and
	// without this it would send nothing and reset the line to zero.
	TaxRate string `json:"tax_rate"`
	// TracksBatches is whether receiving this line must name the supplier's
	// lot. inventory.Receive requires one for a tracked variant and refuses one
	// for anything else, and without this a receiving screen can only find out
	// which is which by submitting the delivery and reading the error back.
	TracksBatches bool   `json:"tracks_batches"`
	NetAmount     string `json:"net_amount"`
	TaxAmount     string `json:"tax_amount"`
	GrossAmount   string `json:"gross_amount"`
}

// CreateOrder raises a purchase order.
//
// Totals are computed here from the lines rather than accepted from the
// caller. A client that could state its own total could authorise a different
// amount from the one its lines add up to, and the PO is what a supplier holds
// the shop to.
func (s *Service) CreateOrder(
	ctx context.Context, scope Scope, in NewOrder,
) (Order, error) {
	if len(in.Lines) == 0 {
		return Order{}, errs.New(errs.CodeInvalidInput,
			"A purchase order needs at least one line.")
	}
	for i, line := range in.Lines {
		if !line.Qty.IsPositive() {
			return Order{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no quantity.", i+1)
		}
		if line.UnitCost.IsNegative() {
			return Order{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has a negative cost.", i+1)
		}
	}

	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var e error
		out, e = s.createOrderInTx(ctx, tx, scope, in, nil, nil, nil)
		return e
	})
	if err != nil {
		return Order{}, db.Translate(err, "")
	}
	return out, nil
}

// createOrderInTx is the one place a purchase_order row is written.
//
// Both callers reach it: a buyer raising an order by hand, and an RFQ award
// turning the winning quote into an order (B5.1, "Purchase Order generated
// automatically from the winning quote"). A second implementation for the
// award path would mean two definitions of what a purchase order is —
// numbering, company checks, totals computed from lines, and the draft status
// a buyer must deliberately issue — and receiving, three-way matching and
// payment all read one shape downstream.
//
// rfqID, quoteID and requisitionID record where the order came from, and are
// nil for an order that came from nowhere but a buyer's judgement.
func (s *Service) createOrderInTx(
	ctx context.Context, tx pgx.Tx, scope Scope, in NewOrder,
	rfqID, quoteID, requisitionID *uuid.UUID,
) (Order, error) {
	var currency, country string
	if e := tx.QueryRow(ctx,
		`SELECT base_currency, country FROM company WHERE id = $1`,
		scope.CompanyID).Scan(&currency, &country); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return Order{}, errs.New(errs.CodeNotFound, "That company was not found.")
		}
		return Order{}, e
	}

	// The date the rules are read at. An order raised today is priced at
	// today's rates, and reading them at "now" for an order dated otherwise
	// would reprice history -- the same reason a sale resolves at its issue
	// date rather than at the moment somebody reprints it.
	pricedAt := time.Now().UTC()
	if in.ExpectedOn != nil {
		pricedAt = *in.ExpectedOn
	}

	// The supplier and warehouse must belong to this company. Row-level
	// security only proves they belong to the TENANT, and a group holding
	// two companies would otherwise let one order against the other's
	// supplier and receive into the other's warehouse.
	if e := checkBelongs(ctx, tx, "supplier", in.SupplierID, scope.CompanyID,
		"That supplier was not found."); e != nil {
		return Order{}, e
	}
	if e := checkBelongs(ctx, tx, "warehouse", in.WarehouseID, scope.CompanyID,
		"That warehouse was not found."); e != nil {
		return Order{}, e
	}

	number, e := claimNumber(ctx, tx, scope.CompanyID, "po", "PO")
	if e != nil {
		return Order{}, e
	}

	var poID uuid.UUID
	if e := tx.QueryRow(ctx, `
		INSERT INTO purchase_order
		  (tenant_id, company_id, supplier_id, warehouse_id, po_number,
		   expected_on, currency, notes, created_by,
		   rfq_id, quote_id, requisition_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		scope.TenantID, scope.CompanyID, in.SupplierID, in.WarehouseID,
		number, in.ExpectedOn, currency, nullText(in.Notes), scope.UserID,
		rfqID, quoteID, requisitionID,
	).Scan(&poID); e != nil {
		return Order{}, e
	}

	subtotal, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
	for i, line := range in.Lines {
		// From the register, never from the caller. See taxRateFor.
		treatment, rate, e := s.taxRateFor(ctx, tx, scope, country,
			line.TaxTreatment, pricedAt)
		if e != nil {
			// Returned as it came. The registry's message names the rule that
			// has to be recorded and where; wrapping it in "line 3 could not
			// be priced" would replace the only sentence anybody can act on.
			return Order{}, e
		}
		if e := agreesOnRate(line.TaxRate, rate, i); e != nil {
			return Order{}, e
		}

		net := line.Qty.Mul(line.UnitCost).Round(4)
		lineTax := net.Mul(rate).Round(4)
		gross := net.Add(lineTax)

		if _, e := tx.Exec(ctx, `
			INSERT INTO po_line
			  (tenant_id, po_id, line_no, variant_id, description,
			   qty_ordered, unit_cost, tax_treatment, tax_rate,
			   net_amount, tax_amount, gross_amount)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			scope.TenantID, poID, i+1, line.VariantID, line.Description,
			line.Qty, line.UnitCost, treatment, rate,
			net, lineTax, gross); e != nil {
			return Order{}, e
		}

		subtotal = subtotal.Add(net)
		tax = tax.Add(lineTax)
		total = total.Add(gross)
	}

	if _, e := tx.Exec(ctx, `
		UPDATE purchase_order
		SET subtotal_net = $2, tax_total = $3, total_inclusive = $4
		WHERE id = $1`, poID, subtotal, tax, total); e != nil {
		return Order{}, e
	}

	return s.readOrder(ctx, tx, poID)
}

// IssueOrder freezes the order and sends it.
//
// A separate step from creation because a draft is editable and an issued order
// is a commitment a supplier can hold the shop to. Receiving against a draft is
// refused for the same reason.
func (s *Service) IssueOrder(
	ctx context.Context, scope Scope, poID uuid.UUID,
) (Order, error) {
	// Set inside the transaction when a rule requires sign-off, acted on after
	// it has rolled back: the request must survive the refusal, the issue must
	// not.
	var pending workflow.Outcome

	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// F1's engine, on the commit path: "IF Purchase Order > SAR 50,000 →
		// Owner Approval". Issuing is the right gate rather than creating —
		// a draft order commits the shop to nothing, and an approval that
		// stopped a buyer writing one down would stop them doing their job.
		if e := s.gateIssue(ctx, tx, scope, poID, &pending); e != nil {
			return e
		}

		tag, e := tx.Exec(ctx, `
			UPDATE purchase_order
			SET status = 'issued', issued_at = now(), issued_by = $3
			WHERE id = $1 AND company_id = $2 AND status = 'draft'`,
			poID, scope.CompanyID, scope.UserID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			// Distinguishing "not found" from "already issued" needs a second
			// read, and it is worth it: they lead a buyer to do very different
			// things next.
			var status string
			if e := tx.QueryRow(ctx,
				`SELECT status FROM purchase_order WHERE id = $1 AND company_id = $2`,
				poID, scope.CompanyID).Scan(&status); errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That purchase order was not found.")
			} else if e != nil {
				return e
			}
			return errs.Newf(errs.CodeConflict,
				"That order is %s and cannot be issued again.", status)
		}

		read, e := s.readOrder(ctx, tx, poID)
		out = read
		return e
	})

	// The transaction has ended. If a rule asked for sign-off, the issue has
	// rolled back and the REQUEST is written now, in its own transaction, so it
	// survives the refusal the buyer is about to receive.
	if errors.Is(err, errNeedsApproval) && pending.Pending != nil {
		if _, e := s.approvals.Raise(ctx, workflow.Scope{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			UserID: scope.UserID,
		}, *pending.Pending); e != nil {
			return Order{}, e
		}
		return Order{}, errs.Newf(errs.CodeComplianceBlocked,
			"%s needs sign-off before this order goes to the supplier. It is "+
				"waiting in the approval centre.", pending.RuleName)
	}

	return out, err
}

func (s *Service) readOrder(
	ctx context.Context, tx pgx.Tx, poID uuid.UUID,
) (Order, error) {
	var o Order
	var expected *string
	var notes string

	if err := tx.QueryRow(ctx, `
		SELECT p.id, p.po_number, p.supplier_id, s.legal_name, p.warehouse_id,
		       p.status, p.ordered_on::text, p.expected_on::text, p.currency,
		       p.subtotal_net::text, p.tax_total::text, p.total_inclusive::text,
		       coalesce(p.notes, '')
		FROM purchase_order p
		JOIN supplier s ON s.id = p.supplier_id
		WHERE p.id = $1`, poID,
	).Scan(&o.ID, &o.PONumber, &o.SupplierID, &o.Supplier, &o.WarehouseID,
		&o.Status, &o.OrderedOn, &expected, &o.Currency,
		&o.SubtotalNet, &o.TaxTotal, &o.Total, &notes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Order{}, errs.New(errs.CodeNotFound,
				"That purchase order was not found.")
		}
		return Order{}, err
	}
	if expected != nil {
		o.ExpectedOn = *expected
	}
	o.Notes = notes

	lines, err := outstandingLines(ctx, tx, poID)
	if err != nil {
		return Order{}, err
	}
	o.Lines = lines
	return o, nil
}

// outstandingLines reads po_outstanding, which reports quantity and money
// together: a receiving screen needs the first and a match needs the second.
func outstandingLines(
	ctx context.Context, tx pgx.Tx, poID uuid.UUID,
) ([]OrderLineView, error) {
	rows, err := tx.Query(ctx, `
		SELECT po_line_id, line_no, variant_id, description,
		       qty_ordered::text, qty_received::text, qty_outstanding::text,
		       qty_billed::text, unit_cost::text,
		       tax_treatment, tax_rate::text,
		       net_amount::text, tax_amount::text, gross_amount::text,
		       tracks_batches
		FROM po_outstanding($1)`, poID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []OrderLineView{}
	for rows.Next() {
		var l OrderLineView
		if err := rows.Scan(&l.ID, &l.LineNo, &l.VariantID, &l.Description,
			&l.QtyOrdered, &l.QtyReceived, &l.QtyOutstanding, &l.QtyBilled,
			&l.UnitCost, &l.TaxTreatment, &l.TaxRate,
			&l.NetAmount, &l.TaxAmount, &l.GrossAmount,
			&l.TracksBatches); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReadOrder returns one order with its lines.
func (s *Service) ReadOrder(
	ctx context.Context, scope Scope, poID uuid.UUID,
) (Order, error) {
	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The company check is explicit rather than left to row-level
		// security, which only proves the row belongs to the tenant.
		var companyID uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT company_id FROM purchase_order WHERE id = $1`, poID).
			Scan(&companyID); errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That purchase order was not found.")
		} else if e != nil {
			return e
		}
		if companyID != scope.CompanyID {
			return errs.New(errs.CodeNotFound, "That purchase order was not found.")
		}

		read, e := s.readOrder(ctx, tx, poID)
		out = read
		return e
	})
	return out, err
}

// ListOrders returns the order book, newest first.
func (s *Service) ListOrders(
	ctx context.Context, scope Scope, status string, limit int,
) ([]Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	out := []Order{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `
			SELECT p.id, p.po_number, p.supplier_id, s.legal_name, p.warehouse_id,
			       p.status, p.ordered_on::text, coalesce(p.expected_on::text,''),
			       p.currency, p.subtotal_net::text, p.tax_total::text,
			       p.total_inclusive::text
			FROM purchase_order p
			JOIN supplier s ON s.id = p.supplier_id
			WHERE p.company_id = $1 AND ($2 = '' OR p.status = $2)
			ORDER BY p.ordered_on DESC, p.po_number DESC
			LIMIT $3`, scope.CompanyID, status, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var o Order
			if e := rows.Scan(&o.ID, &o.PONumber, &o.SupplierID, &o.Supplier,
				&o.WarehouseID, &o.Status, &o.OrderedOn, &o.ExpectedOn,
				&o.Currency, &o.SubtotalNet, &o.TaxTotal, &o.Total); e != nil {
				return e
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// --- shared --------------------------------------------------------------

// requireCompany proves the company is visible to this tenant.
//
// Row-level security hides another tenant's company entirely, so without this
// a cross-tenant request returns an empty list and a 200 — which reads as "a
// company with no purchase orders" rather than "not your company". Empty is the
// more dangerous of the two answers, because it looks like information.
func requireCompany(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) error {
	var found bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM company WHERE id = $1`, companyID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return err
}

func checkBelongs(
	ctx context.Context, tx pgx.Tx, table string,
	id, companyID uuid.UUID, missing string,
) error {
	// The table name is a compile-time constant at every call site, never user
	// input — there is no interpolation of anything a caller supplied.
	var found bool
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT true FROM %s WHERE id = $1 AND company_id = $2`, table),
		id, companyID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, missing)
	}
	return err
}

// claimNumber allocates the next document number under a row lock.
//
// Never max()+1, which collides when two buyers raise an order at the same
// moment, and never a sequence, which is not transactional and leaves a
// permanent gap when a transaction rolls back. A gap in a purchase order series
// is what an auditor asks about.
func claimNumber(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, kind, prefix string,
) (string, error) {
	var n int64
	if err := tx.QueryRow(ctx,
		`SELECT claim_purchase_no($1, $2)`, companyID, kind).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%06d", prefix, time.Now().UTC().Format("2006"), n), nil
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

// --- Warehouses ----------------------------------------------------------

// Warehouse is somewhere stock can be received into.
type Warehouse struct {
	ID    uuid.UUID `json:"id"`
	Code  string    `json:"code"`
	Name  string    `json:"name"`
	Store string    `json:"store,omitempty"`
}

// WarehousesFor lists where a purchase order can be delivered.
//
// A buyer has to pick one and cannot be expected to know its id. The list is
// scoped to the company for the same reason every other purchasing query is:
// a group holding two companies must not be able to order goods into the
// other one's stockroom.
func (s *Service) WarehousesFor(
	ctx context.Context, scope Scope,
) ([]Warehouse, error) {
	out := []Warehouse{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `
			SELECT w.id, w.code, w.name, coalesce(st.name, '')
			FROM warehouse w
			LEFT JOIN store st ON st.id = w.store_id
			WHERE w.company_id = $1
			ORDER BY w.name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var w Warehouse
			if e := rows.Scan(&w.ID, &w.Code, &w.Name, &w.Store); e != nil {
				return e
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// --- Editing a draft -----------------------------------------------------

// ReplaceOrderLines rewrites a DRAFT order's lines.
//
// Draft only, and that is the whole point of the status existing. An issued
// order is a commitment a supplier can hold the shop to, and one that could be
// edited afterwards would let somebody change what was agreed after goods had
// been received against it — which is the same class of problem as editing a
// finalized invoice, and gets the same answer: no.
//
// The lines are replaced wholesale rather than patched. A draft has no
// receipts and no bills pointing at its lines, so there is nothing to orphan,
// and a diffing edit would be considerably more code for a case that only ever
// happens before anything depends on it.
func (s *Service) ReplaceOrderLines(
	ctx context.Context, scope Scope, poID uuid.UUID, in NewOrder,
) (Order, error) {
	if len(in.Lines) == 0 {
		return Order{}, errs.New(errs.CodeInvalidInput,
			"A purchase order needs at least one line.")
	}

	var out Order
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		if e := tx.QueryRow(ctx, `
			SELECT status FROM purchase_order
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			poID, scope.CompanyID).Scan(&status); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That purchase order was not found.")
			}
			return e
		}
		if status != "draft" {
			return errs.Newf(errs.CodeConflict,
				"That order is %s. Only a draft can be changed — an issued order "+
					"is a commitment the supplier can hold you to.", status)
		}

		if e := checkBelongs(ctx, tx, "supplier", in.SupplierID, scope.CompanyID,
			"That supplier was not found."); e != nil {
			return e
		}
		if e := checkBelongs(ctx, tx, "warehouse", in.WarehouseID, scope.CompanyID,
			"That warehouse was not found."); e != nil {
			return e
		}

		country, e := countryOf(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		pricedAt := time.Now().UTC()
		if in.ExpectedOn != nil {
			pricedAt = *in.ExpectedOn
		}

		if _, e := tx.Exec(ctx, `DELETE FROM po_line WHERE po_id = $1`, poID); e != nil {
			return e
		}

		subtotal, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
		for i, line := range in.Lines {
			if !line.Qty.IsPositive() {
				return errs.Newf(errs.CodeInvalidInput, "Line %d has no quantity.", i+1)
			}
			if line.UnitCost.IsNegative() {
				return errs.Newf(errs.CodeInvalidInput, "Line %d has a negative cost.", i+1)
			}

			// Re-resolved on every edit rather than carried from the draft,
			// so a rate that changed between raising and amending is the one
			// the amended order carries.
			treatment, rate, e := s.taxRateFor(ctx, tx, scope, country,
				line.TaxTreatment, pricedAt)
			if e != nil {
				return e
			}
			if e := agreesOnRate(line.TaxRate, rate, i); e != nil {
				return e
			}

			net := line.Qty.Mul(line.UnitCost).Round(4)
			lineTax := net.Mul(rate).Round(4)
			gross := net.Add(lineTax)

			if _, e := tx.Exec(ctx, `
				INSERT INTO po_line
				  (tenant_id, po_id, line_no, variant_id, description,
				   qty_ordered, unit_cost, tax_treatment, tax_rate,
				   net_amount, tax_amount, gross_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				scope.TenantID, poID, i+1, line.VariantID, line.Description,
				line.Qty, line.UnitCost, treatment, rate,
				net, lineTax, gross); e != nil {
				return e
			}

			subtotal = subtotal.Add(net)
			tax = tax.Add(lineTax)
			total = total.Add(gross)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE purchase_order
			SET supplier_id = $2, warehouse_id = $3, expected_on = $4, notes = $5,
			    subtotal_net = $6, tax_total = $7, total_inclusive = $8
			WHERE id = $1`,
			poID, in.SupplierID, in.WarehouseID, in.ExpectedOn,
			nullText(in.Notes), subtotal, tax, total); e != nil {
			return e
		}

		read, e := s.readOrder(ctx, tx, poID)
		out = read
		return e
	})
	return out, err
}

// conflictMessage replaces the generic duplicate wording with something the
// person reading it can act on.
//
// db.Translate maps every unique violation to "That record already exists.",
// which is correct as a default and useless on a form: it does not say WHICH
// record or which field. A browser check surfaced it — the supplier form
// refused a duplicate code with a sentence that gave the buyer nothing to
// change.
func conflictMessage(err error, message string) error {
	if e := errs.As(err); e != nil && e.Code == errs.CodeConflict {
		return errs.New(errs.CodeConflict, message)
	}
	return err
}

// gateIssue asks the approval engine whether this order may be issued.
//
// The amount is the order's own total, read here rather than taken from the
// caller: a gate that trusted a number the caller supplied would be a gate a
// caller could walk past.
func (s *Service) gateIssue(
	ctx context.Context, tx pgx.Tx, scope Scope, poID uuid.UUID,
	pending *workflow.Outcome,
) error {
	if s.approvals == nil {
		return nil
	}

	var total decimal.Decimal
	var orderNo, supplier string
	e := tx.QueryRow(ctx, `
		SELECT coalesce(sum(l.gross_amount), 0),
		       o.po_number, coalesce(s.legal_name, '')
		FROM purchase_order o
		LEFT JOIN po_line l ON l.po_id = o.id
		LEFT JOIN supplier s ON s.id = o.supplier_id
		WHERE o.id = $1 AND o.company_id = $2
		GROUP BY o.po_number, s.legal_name`,
		poID, scope.CompanyID).Scan(&total, &orderNo, &supplier)
	if errors.Is(e, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That purchase order was not found.")
	}
	if e != nil {
		return e
	}

	summary := "Purchase order " + orderNo
	if supplier != "" {
		summary += " to " + supplier
	}

	out, err := s.approvals.Evaluate(ctx, tx, workflow.Scope{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		UserID: scope.UserID,
	}, "purchase_order", poID, summary, workflow.Facts{
		// No store: a purchase order names a WAREHOUSE, not a shop floor, and
		// offering the warehouse id as a store would make a store-scoped rule
		// match something it was not written for.
		Amount: &total, At: time.Now().UTC(),
	})
	if err != nil {
		return err
	}

	switch {
	case out.Blocked:
		return errs.Newf(errs.CodeForbidden,
			"%s does not allow this order to be issued.", out.RuleName)
	case out.NeedsApproval:
		// Handed back so IssueOrder can write the request AFTER this
		// transaction rolls back. Written inside it, the request would roll
		// back with the refusal and the approval centre would stay empty --
		// see workflow.Pending.
		*pending = out
		return errNeedsApproval
	case out.NeedsPIN:
		return errs.Newf(errs.CodeComplianceBlocked,
			"%s needs a second person to authorise this before the order goes "+
				"to the supplier.", out.RuleName)
	}
	return nil
}

// errNeedsApproval unwinds the issuing transaction without committing it, while
// IssueOrder raises the request afterwards.
var errNeedsApproval = errors.New("purchase order needs approval")
