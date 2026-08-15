package sales

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// Finalising a sale is where all three pillars meet.
//
// Blueprint J1 names them and warns that getting them wrong means rebuilding
// the system: the invoice chain, the posting engine, and sync idempotency. A
// sale touches all three at once, so this is the one place they must be proved
// to agree — and the reason everything below happens in ONE transaction.
//
// What each part of that atomicity buys:
//
//   - Stock moves and COGS posts together. Apart, the inventory valuation and
//     the Inventory control account drift, and C13's tie-out fails by an amount
//     nothing can explain.
//   - The ICV is claimed and the invoice is written together. Apart, a crash
//     between them leaves a gap in the ZATCA chain, which is not a bug that can
//     be repaired later: the counter must not reset and the hash chain must not
//     break.
//   - Revenue and its VAT post together. Apart, a VAT return can be prepared
//     from books that are missing tax on sales already reported to ZATCA.

// Tender is one way the customer paid.
type Tender struct {
	Method    string
	Amount    decimal.Decimal
	Reference string
}

// Terminal is where a sale happened.
type Terminal struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	StoreID   uuid.UUID

	// EGSUnitID owns the ZATCA counter and hash chain. It is per terminal, not
	// per store: E1.3 puts the CSID and the chain on the device itself.
	EGSUnitID uuid.UUID

	// WarehouseID is the stock this till sells from.
	WarehouseID uuid.UUID

	DeviceID *uuid.UUID

	// Country decides which posting rule applies. A market can have its own
	// entry shape without every other market needing a copy of it.
	Country string

	// CashSessionID ties takings to the shift that took them.
	//
	// A foreign key rather than a time window. Matching sales to a shift by
	// timestamp looks equivalent and is not: a drifting terminal clock, a sale
	// rung at 23:59:58 and committed at 00:00:01, or two shifts overlapping at
	// a handover all put money in the wrong shift — and the cashier who comes
	// up short has no way to prove it was not theirs.
	CashSessionID *uuid.UUID
}

// Sale is a rung-up sale awaiting finalisation.
type Sale struct {
	// InvoiceUUID is assigned ON THE DEVICE before any network call. A database
	// sequence cannot serve a sale rung up with no connectivity, and this is
	// what makes a sync retry idempotent: the same sale arriving twice carries
	// the same UUID and is recognised rather than duplicated.
	InvoiceUUID uuid.UUID

	// DocType is 'simplified' for B2C or 'standard' for B2B. The two follow
	// different ZATCA routes — reporting within 24 hours against clearance
	// before issue — so this is not cosmetic.
	DocType string

	IssuedAt time.Time
	Currency string
	FXRate   decimal.Decimal

	Input   SaleInput
	Lines   []SaleLineRef
	Tenders []Tender

	CashierID *uuid.UUID

	// StockPolicy decides what happens when a line exceeds what is on hand.
	// Blocking suits serialised or high-value goods; allowing suits a busy shop
	// where refusing a waiting customer is worse than a correction later (C13).
	StockPolicy inventory.NegativeStockPolicy
}

// SaleLineRef ties a priced line to the stock it draws on, one per line of
// Input.Lines in the same order.
type SaleLineRef struct {
	VariantID uuid.UUID

	// StandardCost is read only by companies costing at standard.
	StandardCost decimal.Decimal
}

// Finalized is what the till and the sync engine get back.
type Finalized struct {
	InvoiceID uuid.UUID
	Link      zatca.Link
	Computed  ComputedSale

	Revenue accounting.Result
	COGS    accounting.Result

	// Shortfalls names lines that sold past available stock, so the exception
	// report has something to show. Under allow_warn the sale still completed.
	Shortfalls []Shortfall

	// AlreadyRung is true when this InvoiceUUID had been finalised before and
	// nothing new was written. The caller treats it as success: the books
	// already say what it wanted them to say.
	AlreadyRung bool
}

// Shortfall is one line sold beyond what the stock record held.
type Shortfall struct {
	LineNo    int
	VariantID uuid.UUID
	ShortBy   decimal.Decimal
}

// Service finalises sales.
type Service struct {
	chain *zatca.Chain

	// pool is set by WithPool. It is optional so the domain methods below can
	// be driven directly inside a caller's own transaction — which is what the
	// sync worker will need when it replays a batch of offline sales as one
	// unit.
	pool *db.Pool

	// rules resolves legal values at the transaction date. Optional for the
	// same reason: a caller that has already resolved them can drive Finalize
	// directly.
	rules *registry.Service

	// submitter is read only to report whether submission is available. The
	// service never submits — that is the worker's job — but a terminal
	// uploading a signed document deserves a truthful answer about whether
	// anything can be sent.
	submitter zatca.Submitter
}

func NewService(chain *zatca.Chain) *Service {
	return &Service{chain: chain}
}

// WithRegistry gives the service the Regulatory Rule Registry, so a sale's VAT
// rate is looked up rather than supplied.
func (s *Service) WithRegistry(rules *registry.Service) *Service {
	s.rules = rules
	return s
}

// Finalize turns a rung-up sale into an invoice, a chain position, stock
// movements and journal entries — atomically, inside the caller's transaction.
//
// The order below is deliberate. Pricing and costing happen before anything is
// written, so a sale that cannot be costed fails before it consumes an ICV: a
// consumed counter cannot be given back, and a gap in the chain is permanent.
func (s *Service) Finalize(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale,
) (Finalized, error) {
	if existing, found, err := s.alreadyRung(ctx, tx, term, sale.InvoiceUUID); err != nil {
		return Finalized{}, err
	} else if found {
		return existing, nil
	}

	if len(sale.Lines) != len(sale.Input.Lines) {
		return Finalized{}, errs.Newf(errs.CodeInternal,
			"This sale priced %d lines but named stock for %d.",
			len(sale.Input.Lines), len(sale.Lines))
	}

	computed, err := Compute(sale.Input)
	if err != nil {
		return Finalized{}, err
	}
	if err := checkTendersCoverTheSale(computed.TotalInclusive, sale.Tenders); err != nil {
		return Finalized{}, err
	}

	// The invoice id is generated HERE rather than by the database, so the
	// stock movements written during costing can point at the sale that caused
	// them. Costing has to run first — a sale that cannot be costed must fail
	// before it consumes an ICV, because a counter cannot be given back and a
	// gap in the chain is permanent — and without an id up front those
	// movements would be untraceable.
	invoiceID := uuid.New()

	// What a sale costs comes from the costing engine and the company's method,
	// never from the till.
	costed, err := s.costLines(ctx, tx, term, sale, invoiceID)
	if err != nil {
		return Finalized{}, err
	}
	if err := computed.ApplyCosts(costed.Costs); err != nil {
		return Finalized{}, err
	}

	if err := s.writeInvoice(ctx, tx, term, sale, computed, invoiceID); err != nil {
		return Finalized{}, err
	}

	// The chain last among the writes, so nothing after it can fail and strand
	// a consumed counter.
	link, err := s.chain.Allocate(ctx, tx, term.EGSUnitID,
		zatca.Document{InvoiceUUID: sale.InvoiceUUID})
	if err != nil {
		return Finalized{}, err
	}
	if err := s.chain.Record(ctx, tx, invoiceID, term.TenantID, link); err != nil {
		return Finalized{}, err
	}

	revenue, cogs, err := s.post(ctx, tx, term, sale, computed, invoiceID, costed.Variance)
	if err != nil {
		return Finalized{}, err
	}

	// The obligation to report is written in the SAME transaction as the
	// invoice. A crash between the two would leave an invoice nobody knew to
	// submit — an unreported document that no queue, no alert and no dashboard
	// would ever mention, which is exactly the silent exposure E1.2 exists to
	// prevent.
	if term.DeviceID != nil {
		if err := jobs.QueueSubmission(
			ctx, tx, term.TenantID, invoiceID, *term.DeviceID); err != nil {
			return Finalized{}, err
		}
	}

	return Finalized{
		InvoiceID: invoiceID, Link: link, Computed: computed,
		Revenue: revenue, COGS: cogs, Shortfalls: costed.Shortfalls,
	}, nil
}

// alreadyRung recognises a sale the device has sent before.
//
// This is Pillar 3's guarantee at the sale boundary. Sync delivers at least
// once, so the same sale arrives more than once as a matter of course, and
// recognising it is the difference between a shop's takings being right and
// being doubled.
func (s *Service) alreadyRung(
	ctx context.Context, tx pgx.Tx, term Terminal, invoiceUUID uuid.UUID,
) (Finalized, bool, error) {
	var out Finalized
	err := tx.QueryRow(ctx, `
		SELECT i.id, z.icv, z.pih, z.invoice_hash, z.schema_version
		FROM sales_invoice i
		JOIN zatca_invoice z ON z.invoice_id = i.id
		WHERE i.tenant_id = $1 AND i.uuid = $2`,
		term.TenantID, invoiceUUID).
		Scan(&out.InvoiceID, &out.Link.ICV, &out.Link.PIH,
			&out.Link.InvoiceHash, &out.Link.SchemaVersion)

	if errors.Is(err, pgx.ErrNoRows) {
		return Finalized{}, false, nil
	}
	if err != nil {
		return Finalized{}, false, err
	}

	out.Link.EGSUnitID = term.EGSUnitID
	out.AlreadyRung = true
	return out, true, nil
}

// costLines draws stock for every line and returns what each cost.
func (s *Service) costLines(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale, invoiceID uuid.UUID,
) (costing, error) {
	out := costing{Costs: make([]decimal.Decimal, len(sale.Lines))}

	// Every item this sale touches is locked up front, in a deterministic
	// order. Locking as each line is costed would let two sales that share two
	// items in opposite orders deadlock — one holding the Abaya and wanting the
	// Thobe while the other holds the Thobe and wants the Abaya. Postgres would
	// abort one, which is safe, but a cashier sees a sale fail for no reason
	// they can act on.
	variantIDs := make([]uuid.UUID, 0, len(sale.Lines))
	for _, ref := range sale.Lines {
		variantIDs = append(variantIDs, ref.VariantID)
	}
	if err := inventory.LockStock(ctx, tx, term.WarehouseID, variantIDs); err != nil {
		return costing{}, err
	}

	for i, ref := range sale.Lines {
		in := sale.Input.Lines[i]

		result, err := inventory.Consume(ctx, tx, inventory.Issue{
			TenantID: term.TenantID, CompanyID: term.CompanyID,
			VariantID: ref.VariantID, WarehouseID: term.WarehouseID,
			Qty: in.Qty.Abs(), Reason: "sale",
			// The invoice id is known before the invoice row exists, because it
			// is generated up front for exactly this reason. Without it a stock
			// movement could not be traced back to the sale that caused it, and
			// stock-card drill-down and shrinkage investigation both need that.
			SourceType: "sales_invoice", SourceID: &invoiceID,
			DeviceID:     term.DeviceID,
			StandardCost: ref.StandardCost,
		})
		if err != nil {
			return costing{}, err
		}

		description := in.Description
		if description == "" {
			description = "this item"
		}
		if err := inventory.CheckAvailability(sale.StockPolicy, result, description); err != nil {
			return costing{}, err
		}

		if result.ShortBy.IsPositive() {
			out.Shortfalls = append(out.Shortfalls, Shortfall{
				LineNo: i + 1, VariantID: ref.VariantID, ShortBy: result.ShortBy,
			})
		}
		out.Costs[i] = result.TotalCost

		// Standard costing books a fixed cost and the difference is the whole
		// point: an unexpected purchase price must become visible rather than
		// being absorbed into margin. Accumulated here and posted with the sale.
		out.Variance = out.Variance.Add(result.Variance)
	}

	return out, nil
}

// costing is what the costing engine produced for a whole sale.
type costing struct {
	Costs      []decimal.Decimal
	Shortfalls []Shortfall

	// Variance is non-zero only under standard costing. Positive means the
	// stock cost MORE than standard — an unfavourable variance, which is a
	// further expense; negative means it cost less.
	Variance decimal.Decimal
}

// writeInvoice records the header, the lines and the tenders.
func (s *Service) writeInvoice(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale,
	computed ComputedSale, invoiceID uuid.UUID,
) error {
	rate := sale.FXRate
	if rate.IsZero() {
		rate = decimal.NewFromInt(1)
	}

	// The friendly number a customer and a shop assistant refer to. Claimed
	// separately from the ICV and never derived from it: blueprint I3 warns
	// that letting a custom invoice number drive the tamper-evident counter is
	// exactly the mistake to avoid, so the numbering engine has no access to
	// ICV allocation and this one can be reformatted freely.
	humanNumber, err := claimHumanNumber(ctx, tx, term.StoreID, sale.IssuedAt, sale.DocType)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO sales_invoice
		  (id, tenant_id, company_id, store_id, device_id, uuid, doc_type,
		   issue_date, issued_at, currency, fx_rate,
		   subtotal_net, discount_total, tax_total, total_inclusive, state,
		   cash_session_id, human_number)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		invoiceID, term.TenantID, term.CompanyID, term.StoreID, term.DeviceID,
		sale.InvoiceUUID, sale.DocType,
		sale.IssuedAt, sale.IssuedAt, sale.Currency, rate,
		computed.SubtotalNet, computed.DiscountTotal,
		computed.TaxTotal, computed.TotalInclusive, initialState(sale.DocType),
		term.CashSessionID, humanNumber); err != nil {
		return db.Translate(err, "That sale could not be recorded.")
	}

	for i, l := range computed.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_invoice_line
			  (tenant_id, invoice_id, line_no, variant_id, description, description_ar,
			   qty, unit_price, line_discount, invoice_discount_alloc, tax_treatment,
			   tax_rate, tax_amount, net_amount, gross_amount, cogs_amount)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			term.TenantID, invoiceID, l.LineNo, sale.Lines[i].VariantID,
			l.Description, nullText(l.DescriptionAr),
			l.Qty, l.UnitPrice, l.LineDiscount, l.InvoiceDiscountAlloc,
			l.TaxTreatment, l.TaxRate, l.TaxAmount,
			l.NetAmount, l.GrossAmount, l.COGSAmount); err != nil {
			return db.Translate(err,
				"A line on that sale could not be recorded.")
		}
	}

	for i, td := range sale.Tenders {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_tender
			  (tenant_id, invoice_id, tender_no, method, amount, reference)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			term.TenantID, invoiceID, i+1, td.Method, td.Amount,
			nullText(td.Reference)); err != nil {
			return db.Translate(err,
				"A payment on that sale could not be recorded.")
		}
	}

	return nil
}

// initialState says which ZATCA route a document takes.
//
// B2C is reported within 24 hours, so the receipt is handed over immediately
// and the report follows. B2B must be CLEARED before it may be issued at all.
// Getting this backwards means either handing a customer an invoice that ZATCA
// has not authorised, or making a shopper wait at a till for a round trip.
func initialState(docType string) string {
	if docType == "standard" {
		return "signed_pending_clear"
	}
	return "signed_pending_report"
}

// post writes the two entries C9.2 requires of every sale.
//
// Two entries, not one. Revenue and cost of sale are separate posting rules,
// and the idempotency key is (source, source_id, rule_key) — so keeping them
// apart is what lets each be replayed and recognised on its own. It also means
// a company selling services, with nothing to cost, simply has no second entry
// rather than an entry with two empty legs.
func (s *Service) post(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale,
	computed ComputedSale, invoiceID uuid.UUID, variance decimal.Decimal,
) (revenue, cogs accounting.Result, err error) {
	base := accounting.Entry{
		TenantID: term.TenantID, CompanyID: term.CompanyID,
		Date: sale.IssuedAt, SourceType: "sales_invoice", SourceID: invoiceID,
		Currency: sale.Currency, FXRate: sale.FXRate, PostedBy: sale.CashierID,
		StoreID: &term.StoreID,
	}

	// The shape of every entry below lives in posting_rule, not here. This
	// supplies the numbers and the tender split; which accounts they land in is
	// the rule's business and the company's role mapping.
	tenders := make(accounting.Group, 0, len(sale.Tenders))
	for _, td := range sale.Tenders {
		tenders = append(tenders, accounting.GroupMember{
			Role: tenderRole(td.Method), Amount: td.Amount, Memo: td.Method,
		})
	}

	revenueEntry := base
	revenueEntry.RuleKey = "sale.revenue"
	revenueEntry.Memo = "Sale"

	revenue, err = accounting.PostByRule(ctx, tx, revenueEntry, term.Country,
		accounting.Transaction{
			Amounts: accounting.Amounts{
				"subtotal_net":    computed.SubtotalNet,
				"tax_total":       computed.TaxTotal,
				"total_inclusive": computed.TotalInclusive,
			},
			Groups: map[string]accounting.Group{"tenders": tenders},
		})
	if err != nil {
		return revenue, cogs, err
	}

	// Rule 2 — cost of sale, which C13 requires to post WITH the sale so gross
	// profit is a measurement rather than a month-end reconstruction. A company
	// selling services has nothing to cost and simply gets no such entry.
	if computed.COGSTotal.IsPositive() {
		cogsEntry := base
		cogsEntry.RuleKey = "sale.cogs"
		cogsEntry.Memo = "Cost of sale"

		cogs, err = accounting.PostByRule(ctx, tx, cogsEntry, term.Country,
			accounting.Transaction{
				Amounts: accounting.Amounts{"cogs_total": computed.COGSTotal},
			})
		if err != nil {
			return revenue, cogs, err
		}
	}

	// Rule 11 — the standard-costing variance, which is the entire reason a
	// company chooses standard costing. Computing it and discarding it means an
	// unexpected purchase price is absorbed silently into margin, which is the
	// outcome the method exists to prevent.
	//
	// Checked INDEPENDENTLY of cost of sale, not after it. A standard cost of
	// zero produces no COGS entry and a variance equal to the whole actual
	// cost, so hanging this off the COGS branch would discard the variance in
	// precisely the case where it is largest.
	if variance.IsZero() {
		return revenue, cogs, nil
	}

	varianceEntry := base
	varianceEntry.RuleKey = "inventory.variance"
	varianceEntry.Memo = "Standard cost variance"

	// The rule debits variance and credits inventory, which is right when stock
	// cost MORE than standard. A favourable variance is the same entry the
	// other way round, and posting a negative amount would make every report
	// that sums a column wrong — so the sides swap and the amount stays
	// positive.
	if variance.IsNegative() {
		varianceEntry.RuleKey = "inventory.variance_favourable"
		variance = variance.Neg()
	}

	if _, err = accounting.PostByRule(ctx, tx, varianceEntry, term.Country,
		accounting.Transaction{
			Amounts: accounting.Amounts{"variance": variance},
		}); err != nil {
		return revenue, cogs, err
	}
	return revenue, cogs, nil
}

// tenderRole says which account a payment method lands in.
//
// Only cash is cash. Card, wallet and buy-now-pay-later money is not in the
// bank at the moment of sale — it is owed by the acquirer and arrives days
// later, minus a fee. Debiting Cash for it would show a shop holding money it
// does not have, and would leave nothing for the settlement reconciliation to
// match against when the payout lands.
func tenderRole(method string) string {
	switch method {
	case "cash":
		return "cash"
	case "customer_due":
		return "accounts_receivable"
	case "store_credit":
		return "store_credit_liability"
	case "loyalty_points":
		return "loyalty_liability"
	case "bank_transfer", "cheque", "sadad":
		return "bank"
	default:
		// Every card and wallet scheme clears through the acquirer. They differ
		// in fee and timing, which the tender row records, not in what is owed
		// to the shop at the moment of sale.
		return "card_clearing"
	}
}

// checkTendersCoverTheSale refuses a sale the payments do not settle exactly.
//
// Not "at least": exactly. An overpayment is change owed, which is a tender of
// its own, and treating it as revenue overstates takings and the VAT on them.
func checkTendersCoverTheSale(total decimal.Decimal, tenders []Tender) error {
	if len(tenders) == 0 {
		return errs.New(errs.CodeInvalidInput,
			"This sale has no payment against it.")
	}

	paid := decimal.Zero
	for _, td := range tenders {
		if !td.Amount.IsPositive() {
			return errs.Newf(errs.CodeInvalidInput,
				"A payment of %s is not a payment.", td.Amount)
		}
		paid = paid.Add(td.Amount)
	}

	if !paid.Equal(total) {
		return errs.Newf(errs.CodeInvalidInput,
			"The payments come to %s against a total of %s, a difference of %s.",
			paid, total, paid.Sub(total))
	}
	return nil
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
