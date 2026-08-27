//go:build integration

// A settlement batch has to add up, and not only because Go added it up.
//
// 0057's comment on gross_amount says the figure is held on the header "so the
// arithmetic can be checked against the link table rather than trusted", and
// until 0069 nothing checked it. Three separate statements inside Record write
// the header, the link rows and the per-tender fee shares, and the only thing
// holding them together was that one function wrote all three.
//
// Two identities matter, for different reasons:
//
//	gross_amount = sum of the tenders in the batch
//	  What the journal entry is posted for. A tender missing from the link
//	  table credits the clearing account for money still recorded as pending,
//	  and design 02 §8's requirement that the account reach zero is never met
//	  again.
//
//	fee_amount = sum of the fee shares on those tenders
//	  The fee posts as one figure, so the ledger stays right and the error
//	  surfaces only in a margin-by-payment-method report. That is the sort of
//	  error that is found a year later, by an Owner who concludes the acquirer
//	  has been underpaying.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// batchArithmetic reads a recorded deposit back out of the tables rather than
// out of the response that wrote it.
func batchArithmetic(
	t *testing.T, h *harness, f *shopFixture, reference string,
) (gross, lineGross, fee, shares decimal.Decimal) {
	t.Helper()
	ctx := context.Background()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT b.gross_amount,
			       coalesce((SELECT sum(l.amount) FROM settlement_batch_tender l
			                 WHERE l.batch_id = b.id), 0),
			       b.fee_amount,
			       coalesce((SELECT sum(coalesce(t.fee_amount, 0))
			                 FROM settlement_batch_tender l
			                 JOIN sales_tender t ON t.id = l.tender_id
			                 WHERE l.batch_id = b.id), 0)
			FROM settlement_batch b
			WHERE b.company_id = $1 AND b.reference = $2`,
			f.companyID, reference).Scan(&gross, &lineGross, &fee, &shares)
	}); err != nil {
		t.Fatalf("reading the deposit back: %v", err)
	}
	return gross, lineGross, fee, shares
}

// A deposit across many payments, with a fee that divides into none of them
// evenly, still adds up in the tables afterwards.
//
// Seven payments rather than three: the shares are taken as differences of
// cumulative targets, so the accumulated rounding only becomes visible over a
// batch of some size, and three is not it.
func TestADepositAddsUpInTheTablesAfterwards(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	prices := []string{"33.33", "66.67", "100.00", "0.01", "12.34", "9.99", "77.66"}
	gross := decimal.Zero
	for _, p := range prices {
		sellByCard(t, h, f, p)
		gross = gross.Add(decimal.RequireFromString(p))
	}

	ids := []string{}
	for _, row := range pendingTenders(t, h, f, owner) {
		id, _ := row.(map[string]any)["tender_id"].(string)
		ids = append(ids, id)
	}
	if len(ids) != len(prices) {
		t.Fatalf("%d payments awaiting settlement, want %d", len(ids), len(prices))
	}

	// An acquirer fee of 2.87 on 300.00, which divides evenly into nothing.
	net := gross.Sub(decimal.RequireFromString("2.87"))
	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid": uuid.NewString(), "reference": "MADA-ADDSUP",
			"deposited_on": "2026-08-17",
			"net_amount":   net.StringFixed(2), "tender_ids": ids,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	header, lines, fee, shares := batchArithmetic(t, h, f, "MADA-ADDSUP")

	if !header.Equal(lines) {
		t.Errorf("the deposit header says it covers %s and its payments add up "+
			"to %s. The clearing account has been credited for money that is "+
			"still recorded as outstanding.", header, lines)
	}
	if !fee.Equal(shares) {
		t.Errorf("the acquirer charged %s and the payments carry %s of fee "+
			"between them; %s of card cost is attributed to no sale at all",
			fee, shares, fee.Sub(shares))
	}
	if !fee.Equal(decimal.RequireFromString("2.87")) {
		t.Errorf("fee = %s, want 2.87", fee)
	}
	if got := roleBalance(t, h, f, "card_clearing"); !got.IsZero() {
		t.Errorf("card clearing = %s after every payment in it was deposited, "+
			"want 0", got)
	}
}

// The database refuses a batch whose header disagrees with its lines.
//
// A test proves the code that exists is right today. A constraint keeps it
// right against the next change, an import, or a repair script — and the
// clearing account reaching zero is the whole reason this module exists.
func TestTheDatabaseRefusesADepositThatDoesNotAddUp(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	sellByCard(t, h, f, "100.00")

	ids := []string{}
	for _, row := range pendingTenders(t, h, f, owner) {
		id, _ := row.(map[string]any)["tender_id"].(string)
		ids = append(ids, id)
	}

	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid": uuid.NewString(), "reference": "MADA-TAMPER",
			"deposited_on": "2026-08-17", "net_amount": "97.50",
			"tender_ids": ids,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: %s", readBody(t, resp))
	}
	resp.Body.Close()

	ctx := context.Background()

	// Dropping a payment out of the batch leaves the header claiming more than
	// its lines cover.
	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var batchID uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT id FROM settlement_batch WHERE reference = 'MADA-TAMPER'`).
			Scan(&batchID); e != nil {
			return e
		}
		// A second, smaller batch built by hand with the wrong header. Editing
		// the first one would test the header instead of the link rows, and it
		// is the link rows the constraint hangs off.
		var other uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO settlement_batch
			  (tenant_id, company_id, uuid, reference, deposited_on,
			   gross_amount, fee_amount, net_amount)
			VALUES ($1,$2,$3,'MADA-HANDMADE','2026-08-18',500,0,500)
			RETURNING id`,
			f.tenantID, f.companyID, uuid.New()).Scan(&other); e != nil {
			return e
		}
		var tenderID uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT tender_id FROM settlement_batch_tender WHERE batch_id = $1`,
			batchID).Scan(&tenderID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO settlement_batch_tender (tenant_id, batch_id, tender_id, amount)
			VALUES ($1,$2,$3,100)`, f.tenantID, other, tenderID)
		return e
	})
	if err == nil {
		t.Fatal("SQL wrote a deposit claiming 500.00 whose payments come to " +
			"100.00; the clearing account can be credited for money nobody took")
	}
}
