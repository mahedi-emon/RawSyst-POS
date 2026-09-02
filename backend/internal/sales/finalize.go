package sales

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
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/loyalty"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
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
	//
	// uuid.Nil in a market where e-invoicing does not apply. See OnAChain.
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

// OnAChain reports whether this terminal's invoices join a signed e-invoicing
// chain.
//
// Asked of the terminal rather than of the country at each call site, because
// by the time a sale is being finalised the question has already been settled
// once — in resolveTerminal, which is also where the refusal lives. A second
// country test further down could disagree with the first, and the way it would
// disagree is by trying to reserve an ICV on uuid.Nil.
func (t Terminal) OnAChain() bool { return t.EGSUnitID != uuid.Nil }

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

	// CustomerID names who this was sold to. Optional for a cash sale — a shop
	// does not ask for a name to sell a bottle of water — and REQUIRED the
	// moment any part of the sale goes on account, because a receivable nobody
	// owes cannot be collected and would break C9.3's tie-out.
	CustomerID *uuid.UUID

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

	// PointsEarned is what the loyalty scheme awarded, so the receipt can say
	// so. Zero when the shop runs no scheme, the sale was anonymous, or it was
	// too small to earn a whole point.
	PointsEarned int

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

	// promos records what a campaign actually gave away, in the sale's own
	// transaction. Optional, like the rest: a caller driving Finalize directly
	// may not have it, and a sale carrying no promotion never asks for it.
	promos *promotions.Service
}

// WithPromotions lets a finalised sale record the campaigns it redeemed.
//
// Without this the redemption table stays empty, and every usage limit — which
// is enforced by counting that table — counts zero for ever.
func (s *Service) WithPromotions(p *promotions.Service) *Service {
	s.promos = p
	return s
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

	// Credit is checked BEFORE the chain is touched. A refused sale must not
	// consume an ICV: the counter cannot be handed back and a gap in the ZATCA
	// chain is permanent, so every reason to refuse has to be found first.
	if err := s.checkCredit(ctx, tx, sale); err != nil {
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

	humanNumber, err := s.writeInvoice(ctx, tx, term, sale, computed, invoiceID)
	if err != nil {
		return Finalized{}, err
	}

	// Stored value is drawn down BEFORE the chain, for the same reason credit
	// is checked before it: a customer who does not have the store credit they
	// are trying to spend must not cost the shop an ICV it can never hand back.
	if err := s.settleStoredValue(ctx, tx, term, sale, invoiceID); err != nil {
		return Finalized{}, err
	}

	// The chain last among the writes, so nothing after it can fail and strand
	// a consumed counter.
	//
	// Three steps rather than one, because the document carries its own ICV and
	// PIH and the hash is over the document: reserve the position, build the
	// UBL with it, then hash what was built. A sale whose unit is not
	// registered enough to produce a document fails HERE, before the counter
	// moves — a consumed ICV cannot be handed back and a gap is permanent.
	// Skipped whole in a market with no e-invoicing obligation. Not stubbed,
	// not given a placeholder ICV: an invoice off a chain has no position on
	// one, and inventing a number would put a figure in `zatca_invoice` that
	// looks like a chain and is not.
	var link zatca.Link
	if term.OnAChain() {
		icv, pih, err := s.chain.Reserve(ctx, tx, term.EGSUnitID)
		if err != nil {
			return Finalized{}, err
		}
		document, err := buildSaleDocument(ctx, tx, term, sale, computed, humanNumber, icv, pih)
		if err != nil {
			return Finalized{}, err
		}
		link, err = s.chain.LinkFor(ctx, term.EGSUnitID, icv, pih, document)
		if err != nil {
			return Finalized{}, err
		}
		if err := s.chain.Record(ctx, tx, invoiceID, term.TenantID, link); err != nil {
			return Finalized{}, err
		}
	}

	revenue, cogs, err := s.post(ctx, tx, term, sale, computed, invoiceID, costed.Variance)
	if err != nil {
		return Finalized{}, err
	}

	// What the campaigns gave away, recorded in the SAME transaction as the
	// sale — for the reason the audit writer is. A redemption that commits
	// without its invoice, or an invoice whose redemptions rolled back, both
	// make the campaign figures wrong in a way nobody would notice until
	// somebody asked what a campaign cost.
	if err := s.recordRedemptions(ctx, tx, term, sale, computed, invoiceID); err != nil {
		return Finalized{}, err
	}

	// Points are earned in the same transaction as the sale, so a sale that
	// posted and points that did not is not a state this product can reach.
	earned, err := loyalty.Accrue(ctx, tx, term.TenantID, term.CompanyID,
		sale.CustomerID, invoiceID, earnable(computed.TotalInclusive, sale.Tenders),
		term.Country, sale.CashierID)
	if err != nil {
		return Finalized{}, err
	}

	// The obligation to report is written in the SAME transaction as the
	// invoice. A crash between the two would leave an invoice nobody knew to
	// submit — an unreported document that no queue, no alert and no dashboard
	// would ever mention, which is exactly the silent exposure E1.2 exists to
	// prevent.
	//
	// Only where there is somewhere to report TO. Queueing a submission for a
	// market with no e-invoicing authority would fill the retry queue with work
	// that can never succeed, and bury the Saudi submissions that matter among
	// it.
	if term.DeviceID != nil && term.OnAChain() {
		if err := jobs.QueueSubmission(
			ctx, tx, term.TenantID, invoiceID, *term.DeviceID); err != nil {
			return Finalized{}, err
		}
	}

	return Finalized{
		InvoiceID: invoiceID, Link: link, Computed: computed,
		Revenue: revenue, COGS: cogs, Shortfalls: costed.Shortfalls,
		PointsEarned: earned,
	}, nil
}

// recordRedemptions writes what each campaign gave away on this sale.
//
// # Why the line carries the promotion rather than the server recomputing it
//
// The till quotes a basket against the live campaigns while the cart is built,
// shows the customer a price, and takes the money. Recomputing here could
// disagree with what the customer was shown — a campaign that expired between
// the quote and the tender, a limit another till reached first — and the
// customer is already holding the receipt. So the sale reports which campaign
// it applied, in the same way it reports the discount itself, and the price
// floor remains the backstop against a till asserting a discount it should not.
//
// # A sale with no promotion costs nothing here
//
// The common case is no campaign at all, and it does no work and needs no
// promotions service. Only a sale that actually names one requires the service
// to be wired, which keeps a caller driving Finalize directly — the sync
// replay, the tests — from having to supply a dependency it will never use.
func (s *Service) recordRedemptions(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale,
	computed ComputedSale, invoiceID uuid.UUID,
) error {
	var given []promotions.Redemption
	for i, l := range computed.Lines {
		id := sale.Input.Lines[i].PromotionID
		if id == nil {
			continue
		}
		// The discount the campaign gave is the line discount, not the invoice
		// discount allocated across the sale: the latter is the shop's own
		// reduction and belongs to no campaign.
		discount := l.LineDiscount
		if !discount.IsPositive() {
			continue
		}
		given = append(given, promotions.Redemption{
			PromotionID: *id,
			InvoiceID:   &invoiceID,
			CustomerID:  sale.CustomerID,
			Discount:    discount,
			LineTotal:   l.GrossAmount,
		})
	}
	if len(given) == 0 {
		return nil
	}

	if s.promos == nil {
		// Refused rather than skipped. A sale claiming a campaign on a service
		// that cannot record one would leave the limit uncounted and the cost
		// unreported — exactly the silent hole this method exists to close.
		return errs.New(errs.CodeInternal,
			"This sale names a promotion, but the sales service was built "+
				"without the promotions service, so the redemption could not "+
				"be recorded.")
	}

	return s.promos.Redeem(ctx, tx, promotions.Scope{
		TenantID: term.TenantID, CompanyID: term.CompanyID,
	}, given)
}

// settleStoredValue draws down the store credit, gift cards and points a sale
// was paid with.
//
// Until this existed, `store_credit` and `loyalty_points` were accepted tenders
// with nothing behind them: a cashier could settle a sale with credit a customer
// had never been given, the sale would post, and the liability would go negative
// with nobody told. The journal has always been right about the ACCOUNT; what it
// could not know is whether that particular customer had a balance.
//
// A gift card is named by the tender's reference — the number printed on the
// card. Without one, store credit is the customer's own wallet, which means the
// sale has to say who the customer is.
func (s *Service) settleStoredValue(
	ctx context.Context, tx pgx.Tx, term Terminal, sale Sale, invoiceID uuid.UUID,
) error {
	for _, t := range sale.Tenders {
		switch t.Method {
		case "store_credit":
			if code := strings.TrimSpace(t.Reference); code != "" {
				cardID, err := wallet.FindCard(ctx, tx, term.CompanyID, code)
				if err != nil {
					return err
				}
				if err := wallet.DrawCard(ctx, tx, term.TenantID, term.CompanyID,
					cardID, t.Amount, sale.Currency, &invoiceID,
					sale.CashierID); err != nil {
					return err
				}
				continue
			}
			if sale.CustomerID == nil {
				return errs.New(errs.CodeInvalidInput,
					"Store credit belongs to somebody. Choose the customer, or "+
						"scan the gift card.")
			}
			if err := wallet.Draw(ctx, tx, term.TenantID, term.CompanyID,
				*sale.CustomerID, t.Amount, sale.Currency, &invoiceID,
				sale.CashierID); err != nil {
				return err
			}

		case "loyalty_points":
			if sale.CustomerID == nil {
				return errs.New(errs.CodeInvalidInput,
					"Points belong to a member. Choose the customer first.")
			}
			if err := loyalty.Spend(ctx, tx, term.TenantID, term.CompanyID,
				*sale.CustomerID, t.Amount, &invoiceID,
				sale.CashierID); err != nil {
				return err
			}
		}
	}
	return nil
}

// earnable is what a sale earns points ON.
//
// The total, less anything settled with points. Earning points on money that
// was itself points is a scheme that pays interest on its own liability: spend
// a hundred points, earn some back, spend those. Small per sale and unbounded
// over a year.
func earnable(total decimal.Decimal, tenders []Tender) decimal.Decimal {
	out := total
	for _, t := range tenders {
		if t.Method == "loyalty_points" {
			out = out.Sub(t.Amount)
		}
	}
	if out.IsNegative() {
		return decimal.Zero
	}
	return out
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
) (string, error) {
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
		return "", err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO sales_invoice
		  (id, tenant_id, company_id, store_id, device_id, uuid, doc_type,
		   issue_date, issued_at, currency, fx_rate,
		   subtotal_net, discount_total, tax_total, total_inclusive, state,
		   cash_session_id, human_number, customer_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		invoiceID, term.TenantID, term.CompanyID, term.StoreID, term.DeviceID,
		sale.InvoiceUUID, sale.DocType,
		sale.IssuedAt, sale.IssuedAt, sale.Currency, rate,
		computed.SubtotalNet, computed.DiscountTotal,
		computed.TaxTotal, computed.TotalInclusive, initialState(sale.DocType),
		term.CashSessionID, humanNumber, sale.CustomerID); err != nil {
		return "", db.Translate(err, "That sale could not be recorded.")
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
			return "", db.Translate(err,
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
			return "", db.Translate(err,
				"A payment on that sale could not be recorded.")
		}
	}

	return humanNumber, nil
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

// checkCredit refuses a sale that puts more on a customer's account than they
// are allowed to owe.
//
// 11-pos-and-sales.md §5 is explicit that customer_due "is refused when it would
// breach the customer's credit limit (B16)" — refused, not warned about, so
// there is no override at the till. An owner who wants the sale raises the limit,
// which is a separate permission and leaves a record of who decided.
//
// The policy itself lives in the receivables package, which owns the customer
// record and the balance. Asking it rather than reimplementing the rule here
// keeps one answer to "may they owe this?" — the same reason costing lives in
// the inventory package rather than at the till.
func (s *Service) checkCredit(ctx context.Context, tx pgx.Tx, sale Sale) error {
	onAccount := decimal.Zero
	for _, t := range sale.Tenders {
		if t.Method == "customer_due" {
			onAccount = onAccount.Add(t.Amount)
		}
	}
	if !onAccount.IsPositive() {
		return nil
	}

	if sale.CustomerID == nil {
		return errs.New(errs.CodeInvalidInput,
			"A sale on account has to say who owes it. Choose a customer, or take payment now.")
	}

	decision, err := receivables.CheckCredit(ctx, tx, *sale.CustomerID, onAccount)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		// CodeConflict rather than CodeInvalidInput: nothing the till sent is
		// malformed. The state of the account is what refuses the sale, and a
		// cashier needs to be told the numbers, not that they mistyped.
		return errs.New(errs.CodeConflict, decision.Reason)
	}
	return nil
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
	case ExchangeClearing:
		// The offsetting half of an exchange. Rises on the credit note and
		// falls on the invoice inside the same transaction, so the account is
		// always zero between exchanges and a balance on it is a bug with a
		// name. Deliberately not store_credit, which is a real balance a real
		// customer can come back and spend.
		return ExchangeClearing
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
