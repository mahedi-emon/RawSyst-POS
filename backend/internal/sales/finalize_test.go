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

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

type shop struct {
	pool      *db.Pool
	chain     *zatca.Chain
	tenantID  uuid.UUID
	companyID uuid.UUID
	storeID   uuid.UUID
	unitID    uuid.UUID
	periodID  uuid.UUID
	variantID uuid.UUID

	cash, revenue, outputVAT, cogs, inventory uuid.UUID
	entryNo                                   int64
}

type stubHasher struct{}

func (stubHasher) SchemaVersion() string { return "1.2" }
func (stubHasher) Hash(_ context.Context, d zatca.Document) (string, error) {
	return "hash-" + d.InvoiceUUID.String(), nil
}

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

	s := &shop{pool: pool, chain: zatca.NewChain(pool, stubHasher{}), entryNo: 1}

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
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'MAIN','Main') RETURNING id`,
			s.tenantID, s.companyID).Scan(&s.storeID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1,$2,$3,'till','smart_pos') RETURNING id`,
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
			{"1400", "Inventory", "asset", &s.inventory},
		} {
			if err := mk(a.code, a.name, a.kind, a.dst); err != nil {
				return err
			}
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

// ring finalises a sale through all three pillars in one transaction.
func (s *shop) ring(
	ctx context.Context, computed ComputedSale, tenders []struct {
		method string
		amount decimal.Decimal
	},
) (invoiceID uuid.UUID, link zatca.Link, err error) {
	entryNo := s.entryNo
	s.entryNo++
	invoiceUUID := uuid.New()

	err = s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		// 1. the invoice header
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, store_id, uuid, doc_type, issue_date, issued_at,
			   currency, subtotal_net, discount_total, tax_total, total_inclusive, state)
			VALUES ($1,$2,$3,$4,'simplified','2026-08-15',now(),'SAR',$5,$6,$7,$8,
			        'signed_pending_report')
			RETURNING id`,
			s.tenantID, s.companyID, s.storeID, invoiceUUID,
			computed.SubtotalNet, computed.DiscountTotal,
			computed.TaxTotal, computed.TotalInclusive).Scan(&invoiceID); e != nil {
			return e
		}

		// 2. lines
		for _, l := range computed.Lines {
			if _, e := tx.Exec(ctx, `
				INSERT INTO sales_invoice_line
				  (tenant_id, invoice_id, line_no, variant_id, description, qty,
				   unit_price, line_discount, invoice_discount_alloc, tax_treatment,
				   tax_rate, tax_amount, net_amount, gross_amount, cogs_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
				s.tenantID, invoiceID, l.LineNo, s.variantID, l.Description,
				l.Qty, l.UnitPrice, l.LineDiscount, l.InvoiceDiscountAlloc,
				l.TaxTreatment, l.TaxRate, l.TaxAmount,
				l.NetAmount, l.GrossAmount, l.COGSAmount); e != nil {
				return e
			}
		}

		// 3. tenders
		for i, td := range tenders {
			if _, e := tx.Exec(ctx, `
				INSERT INTO sales_tender (tenant_id, invoice_id, tender_no, method, amount)
				VALUES ($1,$2,$3,$4,$5)`,
				s.tenantID, invoiceID, i+1, td.method, td.amount); e != nil {
				return e
			}
		}

		// 4. the ZATCA chain
		var e error
		link, e = s.chain.Allocate(ctx, tx, s.unitID, zatca.Document{InvoiceUUID: invoiceUUID})
		if e != nil {
			return e
		}
		if e := s.chain.Record(ctx, tx, invoiceID, s.tenantID, link); e != nil {
			return e
		}

		// 5. the journal — Rule 1 (revenue and VAT) and Rule 2 (COGS), which
		//    C9.2 requires to post simultaneously with the sale.
		var entryID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date,
			   source_type, source_id, rule_key)
			VALUES ($1,$2,$3,$4,'2026-08-15','sale',$5,'sale.cash') RETURNING id`,
			s.tenantID, s.companyID, s.periodID, entryNo, invoiceID).Scan(&entryID); e != nil {
			return e
		}

		type jl struct {
			account       uuid.UUID
			debit, credit decimal.Decimal
		}
		lines := []jl{
			{s.cash, computed.TotalInclusive, decimal.Zero},
			{s.revenue, decimal.Zero, computed.SubtotalNet},
		}
		if computed.TaxTotal.IsPositive() {
			lines = append(lines, jl{s.outputVAT, decimal.Zero, computed.TaxTotal})
		}
		if computed.COGSTotal.IsPositive() {
			lines = append(lines,
				jl{s.cogs, computed.COGSTotal, decimal.Zero},
				jl{s.inventory, decimal.Zero, computed.COGSTotal})
		}

		for i, l := range lines {
			if _, e := tx.Exec(ctx, `
				INSERT INTO journal_line
				  (tenant_id, entry_id, line_no, account_id, currency,
				   debit, credit, base_debit, base_credit)
				VALUES ($1,$2,$3,$4,'SAR',$5,$6,$5,$6)`,
				s.tenantID, entryID, i+1, l.account, l.debit, l.credit); e != nil {
				return e
			}
		}
		return nil
	})
	return invoiceID, link, err
}

func saudiRules() (rate decimal.Decimal) { return dec("0.15") }

// One sale, all three pillars, one transaction.
func TestSaleWritesInvoiceChainAndJournalAtomically(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	computed, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1150.00"), TaxTreatment: "standard",
			CostPerUnit: dec("600.00"),
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	invoiceID, link, err := s.ring(ctx, computed, []struct {
		method string
		amount decimal.Decimal
	}{{"cash", dec("1150.00")}})
	if err != nil {
		t.Fatalf("ring sale: %v", err)
	}

	if link.ICV != 1 {
		t.Fatalf("first sale took counter %d, want 1", link.ICV)
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
	if lineCount != 1 || tenderCount != 1 || chainCount != 1 || journalCount != 1 {
		t.Fatalf("lines=%d tenders=%d chain=%d journal=%d; all must be 1",
			lineCount, tenderCount, chainCount, journalCount)
	}
}

// Payment that does not cover the sale must take the whole transaction down —
// including the ICV. A counter consumed by a sale that did not happen leaves a
// gap, which is the signal ZATCA's tamper detection looks for.
func TestUnderpaidSaleConsumesNoCounter(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()

	computed, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1150.00"), TaxTreatment: "standard",
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// One hallala short.
	_, _, err = s.ring(ctx, computed, []struct {
		method string
		amount decimal.Decimal
	}{{"cash", dec("1149.99")}})
	if err == nil {
		t.Fatal("a sale was issued without being paid in full")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "paid in full") {
		t.Fatalf("unhelpful refusal: %v", err)
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

	computed, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1000.00"), TaxTreatment: "standard",
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	invoiceID, _, err := s.ring(ctx, computed, []struct {
		method string
		amount decimal.Decimal
	}{
		{"cash", dec("200.00")},
		{"mada", dec("300.00")},
		{"tabby", dec("500.00")},
	})
	if err != nil {
		t.Fatalf("split payment: %v", err)
	}

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

	computed, _ := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1150.00"), TaxTreatment: "standard",
		}},
	})
	invoiceID, _, err := s.ring(ctx, computed, []struct {
		method string
		amount decimal.Decimal
	}{{"cash", dec("1150.00")}})
	if err != nil {
		t.Fatalf("ring: %v", err)
	}

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

	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE fiscal_period SET state='closed', closed_at=now() WHERE id=$1`,
			s.periodID)
		return e
	}); err != nil {
		t.Fatalf("close period: %v", err)
	}

	computed, _ := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: saudiRules(), Rules: saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1150.00"), TaxTreatment: "standard",
		}},
	})
	_, _, err := s.ring(ctx, computed, []struct {
		method string
		amount decimal.Decimal
	}{{"cash", dec("1150.00")}})
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
