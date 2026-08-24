//go:build integration

// The three pillars meeting for the first time.
//
// A finalized sale must, in ONE transaction: write its lines and tenders, take
// a position on the ZATCA chain, and post a balanced journal entry. If any part
// fails the whole sale must fail, because the alternatives are all worse than
// refusing — a signed invoice with no accounting entry, a journal entry for a
// sale that does not exist, or an ICV consumed by nothing.
package sales

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

type shop struct {
	pool        *db.Pool
	chain       *zatca.Chain
	svc         *Service
	tenantID    uuid.UUID
	companyID   uuid.UUID
	storeID     uuid.UUID
	unitID      uuid.UUID
	periodID    uuid.UUID
	variantID   uuid.UUID
	warehouseID uuid.UUID

	cash, revenue, outputVAT, cogs, inventory uuid.UUID
	cardClearing                              uuid.UUID
	entryNo                                   int64
}

// The sales tests hash for real.
//
// There used to be a stub here returning "hash-" + the invoice UUID. It hid a
// whole class of bug: the moment the chain stopped passing InvoiceUUID and
// started passing the document, every hash became "hash-00000000-..." and
// collided on the unique index — which is what a stub keyed on the wrong field
// does. Hashing is an ordinary computation now, so these tests do it, and a
// sale whose document cannot be built fails here rather than in production.

func newShop(t *testing.T) *shop {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	chain := zatca.NewChain(pool, zatca.StandardHasher{})
	s := &shop{pool: pool, chain: chain, svc: NewService(chain), entryNo: 1}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Sale Test') RETURNING id`).
			Scan(&s.tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,'Sale Test Co','sa','SAR') RETURNING id`,
			s.tenantID).Scan(&s.companyID)
	})
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, s.tenantID)
			return err
		})
	})

	err = pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name, street, building_number, district, city, postal_code, country_code)
			VALUES ($1,$2,'MAIN','Main','Prince Sultan Road','2322','Al-Murabba','Riyadh','23333','SA') RETURNING id`,
			s.tenantID, s.companyID).Scan(&s.storeID); err != nil {
			return err
		}
		// Registered, not merely created. A unit with no legal name and no VAT
		// number cannot produce a UBL document, and since the chain hash is
		// taken over that document it cannot sell either — which is the rule
		// working, not a fixture detail. Every sale test needs a unit a shop
		// could actually trade on.
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit
			  (tenant_id, company_id, store_id, label, architecture,
			   csr_organization_name, csr_organization_identifier)
			VALUES ($1,$2,$3,'till','smart_pos','Demo Retail Co','311111111111113')
			RETURNING id`,
			s.tenantID, s.companyID, s.storeID).Scan(&s.unitID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,8,'2026-08-01','2026-08-31') RETURNING id`,
			s.tenantID, s.companyID).Scan(&s.periodID); err != nil {
			return err
		}

		mk := func(code, name, kind string, dst *uuid.UUID) error {
			return tx.QueryRow(ctx, `
				INSERT INTO account (tenant_id, company_id, code, name, type)
				VALUES ($1,$2,$3,$4,$5) RETURNING id`,
				s.tenantID, s.companyID, code, name, kind).Scan(dst)
		}
		for _, a := range []struct {
			code, name, kind string
			dst              *uuid.UUID
		}{
			{"1100", "Cash", "asset", &s.cash},
			{"4100", "Sales Revenue", "revenue", &s.revenue},
			{"2200", "Output VAT Payable", "liability", &s.outputVAT},
			{"5100", "Cost of Goods Sold", "expense", &s.cogs},
			// Money taken by card is owed by the acquirer until it settles, so
			// it is a receivable rather than cash in the till.
			{"1150", "Card Settlement Clearing", "asset", &s.cardClearing},
		} {
			if err := mk(a.code, a.name, a.kind, a.dst); err != nil {
				return err
			}
		}

		// Inventory must be a CONTROL account or the tie-out has nothing to
		// compare the stock valuation against — inventory_gl_difference finds no
		// balance and reports the whole valuation as a divergence.
		if err := tx.QueryRow(ctx, `
			INSERT INTO account
			  (tenant_id, company_id, code, name, type, is_control, control_of)
			VALUES ($1,$2,'1400','Inventory','asset',true,'inventory') RETURNING id`,
			s.tenantID, s.companyID).Scan(&s.inventory); err != nil {
			return err
		}

		// The posting service names accounts by role, so a company that has not
		// mapped its chart of accounts cannot trade.
		for role, id := range map[string]uuid.UUID{
			"cash": s.cash, "sales_revenue": s.revenue, "output_vat": s.outputVAT,
			"cogs": s.cogs, "inventory": s.inventory, "card_clearing": s.cardClearing,
		} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
				VALUES ($1,$2,$3,$4)`, s.tenantID, s.companyID, role, id); err != nil {
				return err
			}
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,'WH1','Shop Floor') RETURNING id`,
			s.tenantID, s.companyID, s.storeID).Scan(&s.warehouseID); err != nil {
			return err
		}

		// A product with a floor price, so the guard can be exercised.
		var productID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'ABAYA-EXEC','Executive Abaya','standard') RETURNING id`,
			s.tenantID, s.companyID).Scan(&productID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, barcode, attributes,
			   price_retail, price_floor, cost_standard)
			VALUES ($1,$2,$3,'ABAYA-EXEC-BLK-L','6281000000017',
			        '{"size":"L","color":"Black"}'::jsonb, 1150.00, 800.00, 600.00)
			RETURNING id`,
			s.tenantID, s.companyID, productID).Scan(&s.variantID)
	})
	if err != nil {
		t.Fatalf("seed shop: %v", err)
	}
	return s
}

// stock puts goods on the shelf, so a sale has something to cost against.
func (s *shop) stock(t *testing.T, qty, unitCost string) {
	t.Helper()
	ctx := context.Background()

	err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if _, e := inventory.Receive(ctx, tx, inventory.Receipt{
			TenantID: s.tenantID, CompanyID: s.companyID,
			VariantID: s.variantID, WarehouseID: s.warehouseID,
			Qty: dec(qty), UnitCost: dec(unitCost), Reason: "opening",
		}); e != nil {
			return e
		}
		// The other half of the receipt, so the ledger knows about the stock the
		// valuation now holds.
		_, e := accounting.Post(ctx, tx, accounting.Entry{
			TenantID: s.tenantID, CompanyID: s.companyID, Date: aug15(),
			SourceType: "opening_stock", SourceID: uuid.New(), RuleKey: "stock.opening",
			Currency: "SAR", BaseCurrency: "SAR",
			Lines: []accounting.Line{
				{Role: "inventory", Side: accounting.Debit, Amount: dec(qty).Mul(dec(unitCost))},
				{Role: "cash", Side: accounting.Credit, Amount: dec(qty).Mul(dec(unitCost))},
			},
		})
		return e
	})
	if err != nil {
		t.Fatalf("stock the shelf: %v", err)
	}
}

func aug15() time.Time { return time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC) }

func (s *shop) terminal() Terminal {
	return Terminal{
		TenantID: s.tenantID, CompanyID: s.companyID, StoreID: s.storeID,
		EGSUnitID: s.unitID, WarehouseID: s.warehouseID,
	}
}

// sale builds a one-item sale paid by the given tenders.
func (s *shop) sale(qty, unitPrice string, tenders ...Tender) Sale {
	return Sale{
		InvoiceUUID: uuid.New(), DocType: "simplified",
		IssuedAt: aug15(), Currency: "SAR",
		Input: SaleInput{
			PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
			Lines: []LineInput{{
				Description: "Executive Abaya", Qty: dec(qty),
				UnitPrice: dec(unitPrice), TaxTreatment: "standard",
			}},
		},
		Lines:       []SaleLineRef{{VariantID: s.variantID}},
		Tenders:     tenders,
		StockPolicy: inventory.PolicyAllowWarn,
	}
}

// ring finalises a sale through the production service — the same call the HTTP
// layer will make. A harness that reimplemented the flow would prove only that
// the harness works.
func (s *shop) ring(ctx context.Context, sale Sale) (Finalized, error) {
	var out Finalized
	err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = s.svc.Finalize(ctx, tx, s.terminal(), sale)
		return e
	})
	return out, err
}

func saudiRules() (rate decimal.Decimal) { return dec("0.15") }

// One sale, all three pillars, one transaction.
func TestSaleWritesInvoiceChainAndJournalAtomically(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	s.stock(t, "5", "600.00")

	got, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	if err != nil {
		t.Fatalf("ring sale: %v", err)
	}
	invoiceID := got.InvoiceID

	if got.Link.ICV != 1 {
		t.Fatalf("first sale took counter %d, want 1", got.Link.ICV)
	}
	// The cost came from the costing engine drawing down real stock, not from
	// anything the till asserted.
	if !got.Computed.COGSTotal.Equal(dec("600")) {
		t.Fatalf("COGS = %s, want 600 from the stock actually drawn down",
			got.Computed.COGSTotal)
	}

	// The books balance, and gross profit is knowable now rather than at
	// month end.
	var tbDiff, revenue, vat, cogs decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT trial_balance_difference($1)`, s.companyID).Scan(&tbDiff); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT coalesce(sum(base_credit),0) FROM journal_line WHERE account_id=$1`,
			s.revenue).Scan(&revenue); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT coalesce(sum(base_credit),0) FROM journal_line WHERE account_id=$1`,
			s.outputVAT).Scan(&vat); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT coalesce(sum(base_debit),0) FROM journal_line WHERE account_id=$1`,
			s.cogs).Scan(&cogs)
	}); err != nil {
		t.Fatalf("read books: %v", err)
	}

	if !tbDiff.IsZero() {
		t.Fatalf("trial balance is out by %s after one sale", tbDiff)
	}
	if !revenue.Equal(dec("1000")) {
		t.Errorf("revenue = %s, want 1000", revenue)
	}
	if !vat.Equal(dec("150")) {
		t.Errorf("output VAT = %s, want 150", vat)
	}
	if !cogs.Equal(dec("600")) {
		t.Errorf("COGS = %s, want 600 — C13 requires it to post with the sale", cogs)
	}

	// The invoice, its chain position and its journal entry all exist and agree.
	var lineCount, tenderCount, chainCount, journalCount int
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_invoice_line WHERE invoice_id=$1`,
			invoiceID).Scan(&lineCount); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_tender WHERE invoice_id=$1`,
			invoiceID).Scan(&tenderCount); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM zatca_invoice WHERE invoice_id=$1`,
			invoiceID).Scan(&chainCount); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entry WHERE source_id=$1`,
			invoiceID).Scan(&journalCount)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if lineCount != 1 || tenderCount != 1 || chainCount != 1 {
		t.Fatalf("lines=%d tenders=%d chain=%d; all must be 1",
			lineCount, tenderCount, chainCount)
	}
	// Two entries: revenue and cost of sale. They are separate posting rules, so
	// each carries its own idempotency key and can be replayed on its own.
	if journalCount != 2 {
		t.Fatalf("%d journal entries for the sale, want 2 (revenue and COGS)",
			journalCount)
	}

	// The stock actually moved, which is what makes the COGS a measurement.
	var onHand decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if !onHand.Equal(dec("4")) {
		t.Fatalf("stock on hand = %s after selling 1 of 5, want 4", onHand)
	}

	// C13's hard invariant, at the end of a real sale.
	var diff decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var e error
		diff, e = inventory.GLDifference(ctx, tx, s.companyID)
		return e
	}); err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if !diff.IsZero() {
		t.Fatalf("the stock valuation and the Inventory account are out by %s "+
			"after one sale", diff)
	}
}

// Payment that does not cover the sale must take the whole transaction down —
// including the ICV. A counter consumed by a sale that did not happen leaves a
// gap, which is the signal ZATCA's tamper detection looks for.
func TestUnderpaidSaleConsumesNoCounter(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	s.stock(t, "5", "600.00")

	// One hallala short.
	_, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1149.99")}))
	if err == nil {
		t.Fatal("a sale was issued without being paid in full")
	}
	if !strings.Contains(err.Error(), "0.01") {
		t.Fatalf("the refusal does not say how far short: %v", err)
	}

	var counter int64
	if e := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT last_icv FROM egs_unit WHERE id=$1`, s.unitID).Scan(&counter)
	}); e != nil {
		t.Fatalf("read counter: %v", e)
	}
	if counter != 0 {
		t.Fatalf("the counter advanced to %d on a failed sale, leaving a gap in "+
			"the chain", counter)
	}
}

// Split payment across three tenders, with Mada recorded as its own method
// rather than folded into a generic card.
func TestSplitTenderAcrossThreeMethods(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	s.stock(t, "5", "600.00")

	got, err := s.ring(ctx, s.sale("1", "1000.00",
		Tender{Method: "cash", Amount: dec("200.00")},
		Tender{Method: "mada", Amount: dec("300.00")},
		Tender{Method: "tabby", Amount: dec("500.00")},
	))
	if err != nil {
		t.Fatalf("split payment: %v", err)
	}
	invoiceID := got.InvoiceID

	var methods []string
	if e := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT method FROM sales_tender WHERE invoice_id=$1 ORDER BY tender_no`,
			invoiceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var m string
			if e := rows.Scan(&m); e != nil {
				return e
			}
			methods = append(methods, m)
		}
		return rows.Err()
	}); e != nil {
		t.Fatalf("read tenders: %v", e)
	}

	want := []string{"cash", "mada", "tabby"}
	if len(methods) != 3 {
		t.Fatalf("got %d tenders, want 3", len(methods))
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("tender %d = %q, want %q — each method must keep its identity "+
				"so per-method margin is real", i+1, methods[i], want[i])
		}
	}
}

// Blueprint B1: the floor is "enforced by the system, not just policy", and it
// must survive the service being bypassed entirely.
func TestPriceFloorHoldsAgainstDirectSQL(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	// The variant's floor is 800. Sell at 500 net, straight into the table.
	err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var invoiceID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, store_id, uuid, doc_type, issue_date, issued_at,
			   currency, subtotal_net, tax_total, total_inclusive, state)
			VALUES ($1,$2,$3,$4,'simplified','2026-08-15',now(),'SAR',500,75,575,'draft')
			RETURNING id`,
			s.tenantID, s.companyID, s.storeID, uuid.New()).Scan(&invoiceID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO sales_invoice_line
			  (tenant_id, invoice_id, line_no, variant_id, description, qty,
			   unit_price, tax_treatment, tax_rate, tax_amount, net_amount, gross_amount)
			VALUES ($1,$2,1,$3,'Executive Abaya',1,575,'standard',0.15,75,500,575)`,
			s.tenantID, invoiceID, s.variantID)
		return e
	})
	if err == nil {
		t.Fatal("a sale below the minimum price was written directly to the database")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "minimum") {
		t.Fatalf("the refusal does not mention the minimum price: %v", err)
	}
	if !strings.Contains(err.Error(), "800") {
		t.Fatalf("the refusal does not name the floor: %v", err)
	}
}

// An invoice header that disagrees with its own lines must not be storable. A
// stored total that contradicts its parts is how an invoice passes every check
// and still shows the wrong number to a customer.
func TestHeaderMustAgreeWithItsLines(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var invoiceID uuid.UUID
		// Header claims 1150; the line will say 2300.
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, store_id, uuid, doc_type, issue_date, issued_at,
			   currency, subtotal_net, tax_total, total_inclusive, state)
			VALUES ($1,$2,$3,$4,'simplified','2026-08-15',now(),'SAR',1000,150,1150,
			        'signed_pending_report')
			RETURNING id`,
			s.tenantID, s.companyID, s.storeID, uuid.New()).Scan(&invoiceID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO sales_invoice_line
			  (tenant_id, invoice_id, line_no, description, qty, unit_price,
			   tax_treatment, tax_rate, tax_amount, net_amount, gross_amount)
			VALUES ($1,$2,1,'Two abayas',2,1150,'standard',0.15,300,2000,2300)`,
			s.tenantID, invoiceID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO sales_tender (tenant_id, invoice_id, tender_no, method, amount)
			VALUES ($1,$2,1,'cash',1150)`, s.tenantID, invoiceID)
		return e
	})
	if err == nil {
		t.Fatal("an invoice whose header disagrees with its lines was saved")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "add up") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A signed invoice's lines are immutable, like the invoice itself.
func TestInvoiceLinesAreImmutable(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	s.stock(t, "5", "600.00")

	got, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	invoiceID := got.InvoiceID

	for _, stmt := range []string{
		`UPDATE sales_invoice_line SET net_amount = 1 WHERE invoice_id = $1`,
		`DELETE FROM sales_invoice_line WHERE invoice_id = $1`,
		`DELETE FROM sales_tender WHERE invoice_id = $1`,
	} {
		e := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, invoiceID)
			return err
		})
		if e == nil {
			t.Errorf("succeeded but should not have: %s", stmt)
		}
	}
}

// Selling into a closed period must fail, and take the counter with it.
func TestSaleIntoAClosedPeriodIsRefused(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	s.stock(t, "5", "600.00") // while the month is still open

	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE fiscal_period SET state='closed', closed_at=now() WHERE id=$1`,
			s.periodID)
		return e
	}); err != nil {
		t.Fatalf("close period: %v", err)
	}

	_, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	if err == nil {
		t.Fatal("a sale posted into a closed period")
	}

	var counter int64
	if e := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT last_icv FROM egs_unit WHERE id=$1`, s.unitID).Scan(&counter)
	}); e != nil {
		t.Fatalf("read counter: %v", e)
	}
	if counter != 0 {
		t.Fatalf("the counter advanced to %d although the sale failed", counter)
	}
}

// Pillar 3 at the sale boundary. Sync delivers at least once, so the same sale
// arrives more than once as a matter of course. Recognising it is the
// difference between a shop's takings being right and being doubled.
func TestTheSameSaleArrivingTwiceIsRungOnce(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sale := s.sale("1", "1150.00", Tender{Method: "cash", Amount: dec("1150.00")})

	first, err := s.ring(ctx, sale)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	second, err := s.ring(ctx, sale) // same InvoiceUUID, as a retry carries
	if err != nil {
		t.Fatalf("the retry was rejected instead of recognised: %v", err)
	}

	if !second.AlreadyRung {
		t.Error("a replayed sale was not recognised")
	}
	if second.InvoiceID != first.InvoiceID {
		t.Errorf("the retry created a second invoice: %s then %s",
			first.InvoiceID, second.InvoiceID)
	}
	if second.Link.ICV != first.Link.ICV {
		t.Errorf("the retry took counter %d after the original took %d",
			second.Link.ICV, first.Link.ICV)
	}

	var invoices, counter int64
	var onHand, revenue decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_invoice WHERE company_id=$1`,
			s.companyID).Scan(&invoices); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT last_icv FROM egs_unit WHERE id=$1`, s.unitID).Scan(&counter); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT coalesce(sum(base_credit),0) FROM journal_line WHERE account_id=$1`,
			s.revenue).Scan(&revenue); e != nil {
			return e
		}
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read: %v", err)
	}

	if invoices != 1 {
		t.Errorf("%d invoices for one sale", invoices)
	}
	if counter != 1 {
		t.Errorf("the counter reached %d for one sale; a replay consumed a "+
			"position on the chain", counter)
	}
	if !revenue.Equal(dec("1000")) {
		t.Errorf("revenue = %s, want 1000; the replay was counted twice", revenue)
	}
	if !onHand.Equal(dec("4")) {
		t.Errorf("stock on hand = %s, want 4; the replay moved stock again", onHand)
	}
}

// Card money is not cash. It is owed by the acquirer and arrives days later
// minus a fee, so debiting Cash would show a shop holding money it does not
// have and leave nothing for the settlement reconciliation to match.
func TestCardMoneyLandsInClearingNotCash(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	if _, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("150.00")},
		Tender{Method: "mada", Amount: dec("1000.00")},
	)); err != nil {
		t.Fatalf("ring: %v", err)
	}

	var cash, clearing decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(base_debit),0) FROM journal_line
			WHERE account_id=$1 AND entry_id IN (
			  SELECT id FROM journal_entry WHERE source_type='sales_invoice')`,
			s.cash).Scan(&cash); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT coalesce(sum(base_debit),0) FROM journal_line WHERE account_id=$1`,
			s.cardClearing).Scan(&clearing)
	}); err != nil {
		t.Fatalf("read books: %v", err)
	}

	if !cash.Equal(dec("150")) {
		t.Errorf("cash debited %s, want only the 150 actually in the till", cash)
	}
	if !clearing.Equal(dec("1000")) {
		t.Errorf("card clearing debited %s, want 1000", clearing)
	}
}

// Whatever fails, nothing survives. Stock is consumed early and the counter is
// claimed late, so a failure in the journal has to unwind both — otherwise the
// shop loses stock to a sale that never happened, or the chain gains a gap.
func TestAFailedSaleLeavesNoTraceAnywhere(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	// Remove the COGS mapping, so the sale fails at the very last step — after
	// stock has moved, the invoice is written and the counter is claimed.
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`DELETE FROM account_role_map WHERE company_id=$1 AND role='cogs'`,
			s.companyID)
		return e
	}); err != nil {
		t.Fatalf("unmap cogs: %v", err)
	}

	if _, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")})); err == nil {
		t.Fatal("a sale completed with no account to post its cost to")
	}

	var invoices, counter int64
	var onHand decimal.Decimal
	var movements int
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_invoice WHERE company_id=$1`,
			s.companyID).Scan(&invoices); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT last_icv FROM egs_unit WHERE id=$1`, s.unitID).Scan(&counter); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_movement WHERE company_id=$1 AND reason='sale'`,
			s.companyID).Scan(&movements); e != nil {
			return e
		}
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read: %v", err)
	}

	if invoices != 0 {
		t.Errorf("%d invoices survived a failed sale", invoices)
	}
	if counter != 0 {
		t.Errorf("the counter advanced to %d on a failed sale, leaving a gap in "+
			"the chain that cannot be repaired", counter)
	}
	if movements != 0 {
		t.Errorf("%d stock movements survived; the shop lost stock to a sale "+
			"that never happened", movements)
	}
	if !onHand.Equal(dec("5")) {
		t.Errorf("stock on hand = %s, want the 5 it started with", onHand)
	}
}

// A sale of goods the shop does not have is the shop's decision, not the
// engine's. Under block it is refused; under allow_warn it completes and is
// reported, because refusing a customer standing at the till is worse than a
// correction later (C13).
func TestSellingBeyondStockFollowsTheCompanyPolicy(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "1", "600.00")

	blocked := s.sale("2", "1150.00", Tender{Method: "cash", Amount: dec("2300.00")})
	blocked.StockPolicy = inventory.PolicyBlock

	if _, err := s.ring(ctx, blocked); err == nil {
		t.Fatal("a block-policy shop sold two of something it had one of")
	}

	allowed := s.sale("2", "1150.00", Tender{Method: "cash", Amount: dec("2300.00")})
	allowed.StockPolicy = inventory.PolicyAllowWarn

	got, err := s.ring(ctx, allowed)
	if err != nil {
		t.Fatalf("an allow_warn shop was blocked: %v", err)
	}
	if len(got.Shortfalls) != 1 {
		t.Fatalf("%d shortfalls reported, want 1 for the exception report",
			len(got.Shortfalls))
	}
	if !got.Shortfalls[0].ShortBy.Equal(dec("1")) {
		t.Errorf("short by %s, want 1", got.Shortfalls[0].ShortBy)
	}
}
