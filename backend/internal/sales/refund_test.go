//go:build integration

// A return is nine things happening at once (blueprint C14), and the two that
// get forgotten are always the same two. These tests hold the whole set to
// account against real sales — including that the shop gives back exactly what
// it charged, no more and no less, however the return is split up.
package sales

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
)

// ---------------------------------------------------------------------------
// Returns — C14's nine effects, against a real sale
// ---------------------------------------------------------------------------

func (s *shop) refund(ctx context.Context, ret Return) (Refunded, error) {
	var out Refunded
	err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = s.svc.ProcessReturn(ctx, tx, s.terminal(), ret)
		return e
	})
	return out, err
}

// lineIDs reads the invoice's line ids, which is what a return names.
func (s *shop) lineIDs(t *testing.T, invoiceID uuid.UUID) []uuid.UUID {
	t.Helper()
	var out []uuid.UUID
	if err := s.pool.TxAsTenant(context.Background(), s.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(context.Background(),
			`SELECT id FROM sales_invoice_line WHERE invoice_id=$1 ORDER BY line_no`,
			invoiceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				return e
			}
			out = append(out, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read line ids: %v", err)
	}
	return out
}

func (s *shop) balances(t *testing.T) (revenue, vat, cogs, tieOut decimal.Decimal) {
	t.Helper()
	ctx := context.Background()
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		net := func(account uuid.UUID, dst *decimal.Decimal) error {
			return tx.QueryRow(ctx, `
				SELECT coalesce(sum(base_credit - base_debit),0)
				FROM journal_line WHERE account_id=$1`, account).Scan(dst)
		}
		if e := net(s.revenue, &revenue); e != nil {
			return e
		}
		if e := net(s.outputVAT, &vat); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(base_debit - base_credit),0)
			FROM journal_line WHERE account_id=$1`, s.cogs).Scan(&cogs); e != nil {
			return e
		}
		var e error
		tieOut, e = inventory.GLDifference(ctx, tx, s.companyID)
		return e
	}); err != nil {
		t.Fatalf("read balances: %v", err)
	}
	return revenue, vat, cogs, tieOut
}

// A full return puts everything back exactly as it was: no revenue, no VAT
// owed, no cost of sale, and the stock on the shelf again.
func TestFullReturnUnwindsTheSaleCompletely(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, err := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	lines := s.lineIDs(t, sold.InvoiceID)

	got, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "wrong size",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	})
	if err != nil {
		t.Fatalf("ProcessReturn: %v", err)
	}

	// A credit note is an invoice to ZATCA and takes its own chain position.
	if got.Link.ICV != 2 {
		t.Errorf("the credit note took counter %d, want 2", got.Link.ICV)
	}

	revenue, vat, cogs, tieOut := s.balances(t)
	if !revenue.IsZero() {
		t.Errorf("revenue = %s after a full return, want 0", revenue)
	}
	if !vat.IsZero() {
		t.Errorf("output VAT = %s after a full return, want 0; the shop would "+
			"pay tax on a sale it refunded", vat)
	}
	if !cogs.IsZero() {
		t.Errorf("COGS = %s after a full return, want 0", cogs)
	}
	if !tieOut.IsZero() {
		t.Errorf("the stock valuation and the Inventory account are out by %s "+
			"after a return", tieOut)
	}

	var onHand decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if !onHand.Equal(dec("5")) {
		t.Errorf("stock on hand = %s after a full return, want the 5 it started "+
			"with", onHand)
	}
}

// C14 names nine effects. A return that carries out eight must say which one it
// did not, rather than reporting success.
//
// It was two until B16 was built: loyalty is now reversed on the way out, which
// is the point of that module. Commission is still to come.
func TestAReturnSaysWhichEffectsItHasNotCarriedOut(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, _ := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	lines := s.lineIDs(t, sold.InvoiceID)

	got, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "changed mind",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	})
	if err != nil {
		t.Fatalf("ProcessReturn: %v", err)
	}

	if got.Effects.Complete() {
		t.Error("the return claimed all nine effects while commission is not built")
	}
	if len(got.Outstanding) != 1 {
		t.Fatalf("%d effects outstanding, want 1: %v", len(got.Outstanding),
			got.Outstanding)
	}
	joined := strings.Join(got.Outstanding, "; ")
	if !strings.Contains(joined, "commission") {
		t.Errorf("the outstanding effect is not the one C14 calls easily "+
			"forgotten: %v", got.Outstanding)
	}
	// The other one is no longer outstanding, and saying so out loud is what
	// stops this test being quietly weakened back.
	if strings.Contains(joined, "loyalty") {
		t.Error("loyalty is reversed on a return now; it should not be listed " +
			"as outstanding")
	}
}

// Three one-unit returns of a line of three must give back exactly what was
// charged. Computing each independently leaves a hallala with the shop.
func TestSuccessivePartialReturnsGiveBackTheWholeAmount(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "9", "600.00")

	// 3 at 1000.01 inclusive is 3000.03, whose net of 2608.72 does not divide
	// by three. Each return computed on its own gives 869.57 and the third must
	// take the remainder of 869.58, or the shop keeps a hallala.
	sale := s.sale("3", "1000.01", Tender{Method: "cash", Amount: dec("3000.03")})
	sold, err := s.ring(ctx, sale)
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	lines := s.lineIDs(t, sold.InvoiceID)

	refundedTotal := decimal.Zero
	for i := 0; i < 3; i++ {
		got, err := s.refund(ctx, Return{
			CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
			IssuedAt: aug15(), Reason: "faulty",
			Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
			Refunds: []Refund{{Method: "cash",
				Amount: mustComputeRefund(t, s, sold.InvoiceID, lines[0])}},
		})
		if err != nil {
			t.Fatalf("return %d: %v", i+1, err)
		}
		refundedTotal = refundedTotal.Add(got.Computed.TotalInclusive)
	}

	if !refundedTotal.Equal(dec("3000.03")) {
		t.Errorf("three returns gave back %s against 3000.03 charged; the "+
			"difference stayed with the shop", refundedTotal)
	}

	revenue, vat, cogs, tieOut := s.balances(t)
	for _, c := range []struct {
		name string
		got  decimal.Decimal
	}{{"revenue", revenue}, {"output VAT", vat}, {"COGS", cogs}, {"tie-out", tieOut}} {
		if !c.got.IsZero() {
			t.Errorf("%s = %s after returning everything, want 0", c.name, c.got)
		}
	}
}

// mustComputeRefund asks the service what this return is worth, so the test
// refunds exactly that rather than guessing.
func mustComputeRefund(t *testing.T, s *shop, invoiceID, lineID uuid.UUID) decimal.Decimal {
	t.Helper()
	var amount decimal.Decimal
	ctx := context.Background()
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		originals, e := s.svc.returnableLines(ctx, tx, invoiceID)
		if e != nil {
			return e
		}
		computed, e := ComputeReturn(originals,
			[]ReturnRequest{{LineID: lineID, Qty: dec("1")}})
		if e != nil {
			return e
		}
		amount = computed.TotalInclusive
		return nil
	}); err != nil {
		t.Fatalf("compute refund: %v", err)
	}
	return amount
}

// Returning more than was sold must be impossible, whether in one go or by
// accumulating partial returns that each look reasonable.
func TestReturningMoreThanWasSoldIsRefused(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, _ := s.ring(ctx, s.sale("2", "1150.00",
		Tender{Method: "cash", Amount: dec("2300.00")}))
	lines := s.lineIDs(t, sold.InvoiceID)

	// Three at once.
	if _, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "too many",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("3")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("3450.00")}},
	}); err == nil {
		t.Fatal("three units were returned against a sale of two")
	}

	// Two, then one more.
	if _, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "all of it",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("2")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("2300.00")}},
	}); err != nil {
		t.Fatalf("returning the whole line was refused: %v", err)
	}
	if _, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "one too many",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	}); err == nil {
		t.Fatal("a third unit went back after the whole line had been returned")
	}
}

// A retried return must not refund the customer twice.
func TestTheSameReturnArrivingTwiceRefundsOnce(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, _ := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	lines := s.lineIDs(t, sold.InvoiceID)

	ret := Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "wrong colour",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	}

	first, err := s.refund(ctx, ret)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	second, err := s.refund(ctx, ret)
	if err != nil {
		t.Fatalf("the retry was rejected instead of recognised: %v", err)
	}

	if !second.AlreadyRefunded {
		t.Error("a replayed return was not recognised")
	}
	if second.CreditNoteID != first.CreditNoteID {
		t.Error("the retry issued a second credit note")
	}

	var refunds int
	var onHand decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_refund WHERE credit_note_id=$1`,
			first.CreditNoteID).Scan(&refunds); e != nil {
			return e
		}
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if refunds != 1 {
		t.Errorf("%d refunds paid out for one return", refunds)
	}
	if !onHand.Equal(dec("5")) {
		t.Errorf("stock on hand = %s; the replay put the goods back twice", onHand)
	}
}

// Nothing can be returned against a credit note, or a refund could itself be
// refunded.
func TestReturningAgainstACreditNoteIsRefused(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, _ := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	lines := s.lineIDs(t, sold.InvoiceID)

	note, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "faulty",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	})
	if err != nil {
		t.Fatalf("first return: %v", err)
	}

	noteLines := s.lineIDs(t, note.CreditNoteID)
	if _, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: note.CreditNoteID,
		IssuedAt: aug15(), Reason: "refund the refund",
		Requests: []ReturnRequest{{LineID: noteLines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1150.00")}},
	}); err == nil {
		t.Fatal("a credit note was returned against, refunding a refund")
	}
}

// A refund that does not settle the credit note exactly must fail, and take the
// whole return with it.
func TestAReturnRefundedShortLeavesNoTrace(t *testing.T) {
	s := newShop(t)
	ctx := context.Background()
	s.stock(t, "5", "600.00")

	sold, _ := s.ring(ctx, s.sale("1", "1150.00",
		Tender{Method: "cash", Amount: dec("1150.00")}))
	lines := s.lineIDs(t, sold.InvoiceID)

	if _, err := s.refund(ctx, Return{
		CreditNoteUUID: uuid.New(), OriginalInvoiceID: sold.InvoiceID,
		IssuedAt: aug15(), Reason: "short refund",
		Requests: []ReturnRequest{{LineID: lines[0], Qty: dec("1")}},
		Refunds:  []Refund{{Method: "cash", Amount: dec("1149.99")}},
	}); err == nil {
		t.Fatal("a credit note was issued without being refunded in full")
	}

	var notes int
	var counter int64
	var onHand decimal.Decimal
	if err := s.pool.TxAsTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_invoice WHERE doc_type='credit_note'`).
			Scan(&notes); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT last_icv FROM egs_unit WHERE id=$1`, s.unitID).Scan(&counter); e != nil {
			return e
		}
		var e error
		onHand, e = inventory.OnHandAt(ctx, tx, s.variantID, s.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("read: %v", err)
	}

	if notes != 0 {
		t.Errorf("%d credit notes survived a failed return", notes)
	}
	if counter != 1 {
		t.Errorf("the counter is at %d; the failed return consumed a position "+
			"on the chain", counter)
	}
	if !onHand.Equal(dec("4")) {
		t.Errorf("stock on hand = %s; the failed return put goods back", onHand)
	}
}
