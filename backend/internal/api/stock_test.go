//go:build integration

// Stock operations, derived from blueprint B4 rather than from the code.
//
// Four sentences of B4 are asserted here, and one invariant from C13 that B4
// never mentions and that a stock module can break more easily than any other:
//
//	"Wastage / Damage logging: mandatory reason + category + automatic loss
//	 write-off posted to accounting."
//
//	"Stock Audit / Physical Count: compares System Quantity vs Physical
//	 Quantity, auto-generates a signed Adjustment Voucher (with reason, user,
//	 approval, timestamp) for any variance."
//
//	"Inter-Branch / Warehouse Stock Transfer workflow: Transfer Request ->
//	 Manager Approval -> Dispatch (In-Transit lock) -> Receiving Branch
//	 Confirms & Reconciles. Discrepancies are auto-flagged."
//
//	C13: "inventory valuation report must always tie exactly to the Inventory
//	 account balance in the General Ledger."
//
// The last one is why `TestTheValuationTiesThroughEveryLegOfATransfer` is the
// longest test in the file. A transfer posts NOTHING — the company is neither
// richer nor poorer for moving its own stock between its own rooms — so every
// opportunity to break the tie-out is on the valuation side, unwatched by any
// journal entry that would have refused to balance.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// stockFixture is a shop that can move stock: the provisioned chart (which is
// where 5400 Stock Write-off lives), a second location to transfer to, and
// stock on the shelf with a known cost.
type stockFixture struct {
	*shopFixture
	backRoomID uuid.UUID
	token      string
}

func seedStock(t *testing.T, h *harness) *stockFixture {
	t.Helper()
	f := h.seedShop(t, "owner")

	// The real chart, not a hand-built one. 0048's whole lesson: a fixture that
	// invents its own accounts proves the rule and steps over the join between
	// the rule and the chart every real company actually has.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed the chart: %v", err)
	}

	out := &stockFixture{shopFixture: f, token: f.token}
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name, kind)
			VALUES ($1,$2,$3,'BACK','Back room','store_room') RETURNING id`,
			f.tenantID, f.companyID, f.storeID).Scan(&out.backRoomID)
	}); err != nil {
		t.Fatalf("seed the back room: %v", err)
	}
	return out
}

func (f *stockFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// receiveStock puts qty on the shelf at a known unit cost, through the costing
// engine rather than by writing rows, so the layers and the pool are both real.
func (f *stockFixture) receiveStock(
	t *testing.T, h *harness, warehouseID uuid.UUID, qty, unitCost string,
) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		posted, e := receiveForTest(t, tx, f, warehouseID, qty, unitCost)
		if e != nil {
			return e
		}
		// The other half, so the ledger agrees with the stock from the start.
		// Without it every tie-out assertion in this file would start out
		// broken by the value of the opening stock.
		return postOpeningStock(t, tx, f, posted)
	}); err != nil {
		t.Fatalf("receive opening stock: %v", err)
	}
}

// onHand reads what the system believes is on a shelf.
func (f *stockFixture) onHand(
	t *testing.T, h *harness, warehouseID uuid.UUID,
) decimal.Decimal {
	t.Helper()
	var q decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT stock_on_hand($1, $2)`, f.variantID, warehouseID).Scan(&q)
	}); err != nil {
		t.Fatalf("read stock on hand: %v", err)
	}
	return q
}

// valuation is C13's stock figure; ledgerInventory is the account it must equal.
func (f *stockFixture) valuation(t *testing.T, h *harness) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT inventory_valuation($1)`, f.companyID).Scan(&v)
	}); err != nil {
		t.Fatalf("read inventory valuation: %v", err)
	}
	return v
}

func (f *stockFixture) ledger(t *testing.T, h *harness, code string) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN account a ON a.id = l.account_id
			WHERE a.company_id = $1 AND a.code = $2`,
			f.companyID, code).Scan(&d)
	}); err != nil {
		t.Fatalf("balance of %s: %v", code, err)
	}
	return d
}

// mustTie is C13, asserted. Called at every point in a transfer where the two
// figures could have parted.
func (f *stockFixture) mustTie(t *testing.T, h *harness, where string) {
	t.Helper()
	v := f.valuation(t, h)
	l := f.ledger(t, h, "1400")
	if !v.Equal(l) {
		t.Fatalf("%s: the stock valuation is %s and the Inventory account is "+
			"%s. C13 requires them to be equal, exactly.",
			where, v.StringFixed(2), l.StringFixed(2))
	}
}

// --- B4: wastage ----------------------------------------------------------

// "automatic loss write-off posted to accounting"
//
// Ten units at 60.00 are on the shelf. Three are dropped. The shop is 180.00
// poorer, Inventory falls by 180.00, and the 180.00 lands in an expense
// account where a P&L will show it — which is the whole difference between
// logging damage and absorbing it.
func TestWastageWritesTheValueOffToAnExpense(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	beforeStock := f.ledger(t, h, "1400")
	beforeWriteoff := f.ledger(t, h, "5400")
	f.mustTie(t, h, "before the wastage")

	resp := h.do(t, http.MethodPost, f.path("/api/v1/stock/adjustments"), f.token,
		map[string]any{
			"uuid": newUUID(), "location_id": f.warehouseID.String(),
			"kind": "wastage", "reason": "damaged",
			"note":  "Three fell off the trolley in the stock room.",
			"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "-3"}},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record wastage: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("7")) {
		t.Errorf("seven should be left on the shelf, not %s", got.String())
	}
	if got := body["value"]; got != "-180.00" {
		t.Errorf("the voucher should be worth -180.00, not %v", got)
	}

	if moved := f.ledger(t, h, "1400").Sub(beforeStock); !moved.Equal(decimal.RequireFromString("-180")) {
		t.Errorf("Inventory should fall by 180.00, not %s", moved.StringFixed(2))
	}
	if moved := f.ledger(t, h, "5400").Sub(beforeWriteoff); !moved.Equal(decimal.RequireFromString("180")) {
		t.Errorf("Stock Write-off should rise by 180.00, not %s. Damage that "+
			"reaches no expense account never appears in a P&L, which is "+
			"exactly how shrinkage gets buried.", moved.StringFixed(2))
	}
	f.mustTie(t, h, "after the wastage")
}

// "mandatory reason + category"
func TestAWastageWithoutAnExplanationIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"no reason category", map[string]any{
			"uuid": newUUID(), "location_id": f.warehouseID.String(),
			"kind": "wastage", "reason": "",
			"note":  "Three fell off the trolley.",
			"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "-3"}},
		}},
		{"an invented category", map[string]any{
			"uuid": newUUID(), "location_id": f.warehouseID.String(),
			"kind": "wastage", "reason": "shrinkage",
			"note":  "Three fell off the trolley.",
			"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "-3"}},
		}},
		{"no note", map[string]any{
			"uuid": newUUID(), "location_id": f.warehouseID.String(),
			"kind": "wastage", "reason": "damaged", "note": "",
			"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "-3"}},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do(t, http.MethodPost,
				f.path("/api/v1/stock/adjustments"), f.token, c.body)
			if resp.StatusCode < 400 {
				t.Fatalf("expected a refusal, got %d — %s",
					resp.StatusCode, readBody(t, resp))
			}
		})
	}
}

// A wastage that ADDS stock would post a write-off against a rise in inventory.
// It balances, and it is a lie.
func TestAWastageCannotAddStock(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/stock/adjustments"), f.token,
		map[string]any{
			"uuid": newUUID(), "location_id": f.warehouseID.String(),
			"kind": "wastage", "reason": "damaged", "note": "Found two more.",
			"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "2"}},
		})
	if resp.StatusCode < 400 {
		t.Fatalf("adding stock as wastage should be refused, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// The voucher carries the caller's own identifier, so a lost response that is
// retried does not write the damage off twice.
func TestRecordingTheSameVoucherTwiceWritesItOffOnce(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	body := map[string]any{
		"uuid": newUUID(), "location_id": f.warehouseID.String(),
		"kind": "wastage", "reason": "damaged", "note": "Dropped a box.",
		"lines": []map[string]any{{"variant_id": f.variantID.String(), "delta": "-3"}},
	}

	first := h.do(t, http.MethodPost, f.path("/api/v1/stock/adjustments"), f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first attempt: %s", readBody(t, first))
	}
	firstBody := decodeJSON(t, first)

	second := h.do(t, http.MethodPost, f.path("/api/v1/stock/adjustments"), f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("a retry should return the original with 200, got %d — %s",
			second.StatusCode, readBody(t, second))
	}
	secondBody := decodeJSON(t, second)

	if secondBody["already_recorded"] != true {
		t.Error("the retry should say it is a replay")
	}
	if firstBody["adjustment_no"] != secondBody["adjustment_no"] {
		t.Errorf("the retry took a second voucher number: %v then %v",
			firstBody["adjustment_no"], secondBody["adjustment_no"])
	}
	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("7")) {
		t.Errorf("three units were written off twice: %s left", got.String())
	}
}

// --- B4: the physical count -----------------------------------------------

// "compares System Quantity vs Physical Quantity ... for any variance"
func TestACountPostsTheDifferenceAndNotTheTotal(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	countID := f.openCount(t, h)

	// Eight on the shelf where the system says ten.
	f.saveCount(t, h, countID, "8")
	posted := f.postCount(t, h, countID)

	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("8")) {
		t.Errorf("the shelf should now say eight, not %s", got.String())
	}
	if got := posted["value"]; got != "-120.00" {
		t.Errorf("two units at 60.00 is a 120.00 write-off, not %v", got)
	}
	f.mustTie(t, h, "after the count")
}

// A count that finds MORE than the records expected posts the mirror of a
// write-off, against the same account — never as income. 0052's reasoning, for
// stock: an unexplained surplus is as much a control failure as a shortfall.
func TestACountThatFindsStockDoesNotBookIncome(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	before := f.ledger(t, h, "5400")

	countID := f.openCount(t, h)
	f.saveCount(t, h, countID, "12")
	posted := f.postCount(t, h, countID)

	if got := posted["value"]; got != "120.00" {
		t.Errorf("two found units at 60.00 is 120.00, not %v", got)
	}
	// Credited to the write-off account, so the month's shrinkage figure is net.
	if moved := f.ledger(t, h, "5400").Sub(before); !moved.Equal(decimal.RequireFromString("-120")) {
		t.Errorf("Stock Write-off should be credited 120.00, not %s", moved.StringFixed(2))
	}
	f.mustTie(t, h, "after finding stock")
}

// "Discrepancies are auto-flagged" — pointed at the discrepancy that is not one.
//
// The sheet is opened when the system says ten. Two are sold while the count is
// going on. The counter finds eight, which is correct, and the variance is
// zero — measured against the system figure AT POSTING, not at opening. A
// system that measured against the opening figure would write off two units
// that a customer paid for.
func TestStockSoldDuringACountIsNotBlamedOnTheCounter(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	countID := f.openCount(t, h)

	// Two leave the shelf while the counting is happening.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return consumeForTest(t, tx, f, f.warehouseID, "2")
	}); err != nil {
		t.Fatalf("sell two mid-count: %v", err)
	}

	f.saveCount(t, h, countID, "8")
	posted := f.postCount(t, h, countID)

	if got := posted["value"]; got != "0.00" {
		t.Errorf("the counter found exactly what was there; the variance "+
			"should be 0.00, not %v", got)
	}
	lines, _ := posted["lines"].([]any)
	if len(lines) == 0 {
		t.Fatal("the posted count has no lines")
	}
	line, _ := lines[0].(map[string]any)
	if line["moved_while_counting"] != true {
		t.Error("the line should be flagged: the system figure changed between " +
			"the sheet being opened and being posted")
	}
	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("8")) {
		t.Errorf("the shelf should still say eight, not %s", got.String())
	}
}

// Uncounted lines are silence, not zero. A sheet abandoned halfway must not
// write off the aisles nobody reached.
func TestAnUncountedLineIsNotTreatedAsEmpty(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	// The second product has to exist BEFORE the sheet is opened: a count
	// sheet is a snapshot of the catalogue at the moment it was started, and a
	// product added afterwards is deliberately not on it.
	other := f.secondVariant(t, h)
	countID := f.openCount(t, h)
	resp := h.do(t, http.MethodPut,
		f.path("/api/v1/stock/counts/"+countID), f.token,
		map[string]any{"lines": []map[string]any{
			{"variant_id": other.String(), "counted_qty": "0"},
		}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("save count: %s", readBody(t, resp))
	}

	f.postCount(t, h, countID)

	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("10")) {
		t.Errorf("the uncounted product should be untouched at ten, not %s",
			got.String())
	}
}

// --- B4: the transfer -----------------------------------------------------

// C13, through every leg.
//
// This is the test the whole transfer design exists to pass. A transfer posts
// no journal entry at all, so the Inventory account never moves — which means
// the valuation must not move either, at ANY point, including the two days the
// stock spends on a lorry belonging to neither branch.
func TestTheValuationTiesThroughEveryLegOfATransfer(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	// Deliberately awkward. The fixture holds ten at a round 60.00; adding
	// seven at 33.3333 into the same weighted-average pool gives a unit cost
	// that does not divide into whole hallalas, so any second rounding on the
	// way through the transfer shows up as a broken tie-out rather than
	// hiding behind arithmetic that happens to come out even.
	f.receiveStock(t, h, f.warehouseID, "7", "33.3333")

	ledgerBefore := f.ledger(t, h, "1400")
	f.mustTie(t, h, "before the transfer")

	transferID := f.requestTransfer(t, h, f.warehouseID, f.backRoomID, "4")
	f.mustTie(t, h, "after the request")

	// Approved by somebody who did not raise it.
	approver := h.seedUserInTenant(t, f.tenantID, "store_manager")
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/approve"), approver, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}
	f.mustTie(t, h, "after the approval")

	resp = h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/dispatch"), f.token,
		map[string]any{"lines": []map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %s", readBody(t, resp))
	}

	// The moment the design exists for: stock has left one branch and arrived
	// at neither.
	if got := f.onHand(t, h, f.warehouseID); !got.Equal(decimal.RequireFromString("13")) {
		t.Errorf("thirteen should be left at the source, not %s", got.String())
	}
	if got := f.onHand(t, h, f.backRoomID); !got.IsZero() {
		t.Errorf("nothing has arrived yet, so the back room should hold "+
			"nothing, not %s", got.String())
	}
	f.mustTie(t, h, "while the stock is in transit")

	resp = h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/receive"), f.token,
		map[string]any{"lines": []map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receive: %s", readBody(t, resp))
	}

	if got := f.onHand(t, h, f.backRoomID); !got.Equal(decimal.RequireFromString("4")) {
		t.Errorf("four should have arrived, not %s", got.String())
	}
	f.mustTie(t, h, "after the receipt")

	// And the ledger never moved, because nothing was bought, sold or lost.
	if after := f.ledger(t, h, "1400"); !after.Equal(ledgerBefore) {
		t.Errorf("moving stock between two of the company's own rooms posted "+
			"to the Inventory account: %s became %s",
			ledgerBefore.StringFixed(2), after.StringFixed(2))
	}
}

// "Manager Approval" — a step the requester cannot perform for themselves.
func TestATransferCannotBeApprovedByThePersonWhoRaisedIt(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	transferID := f.requestTransfer(t, h, f.warehouseID, f.backRoomID, "4")

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/approve"), f.token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the requester approved their own transfer: %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// "Receiving Branch Confirms & Reconciles. Discrepancies are auto-flagged."
//
// Four are sent and three arrive. The missing one does not vanish and is not
// silently absorbed by the receiving branch: it is still the company's stock,
// still in the valuation, and still in transit — where somebody has to decide,
// with a reason attached, that it is gone.
func TestABranchReceivingLessLeavesTheRestInTransit(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	transferID := f.requestTransfer(t, h, f.warehouseID, f.backRoomID, "4")
	approver := h.seedUserInTenant(t, f.tenantID, "store_manager")
	h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/approve"), approver, nil)
	h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/dispatch"), f.token,
		map[string]any{"lines": []map[string]any{}})

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/receive"), f.token,
		map[string]any{"lines": []map[string]any{
			{"variant_id": f.variantID.String(), "qty": "3"},
		}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receive: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if got := body["short_by"]; got != "1" {
		t.Errorf("the transfer should report one unit short, not %v", got)
	}
	if got := f.onHand(t, h, f.backRoomID); !got.Equal(decimal.RequireFromString("3")) {
		t.Errorf("three arrived, so the back room should hold three, not %s",
			got.String())
	}
	// The missing unit is still somewhere, and the company is not poorer yet.
	f.mustTie(t, h, "after a short receipt")
}

// A lorry does not gain stock on the way.
func TestABranchCannotReceiveMoreThanWasSent(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	transferID := f.requestTransfer(t, h, f.warehouseID, f.backRoomID, "4")
	approver := h.seedUserInTenant(t, f.tenantID, "store_manager")
	h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/approve"), approver, nil)
	h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/dispatch"), f.token,
		map[string]any{"lines": []map[string]any{}})

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/receive"), f.token,
		map[string]any{"lines": []map[string]any{
			{"variant_id": f.variantID.String(), "qty": "5"},
		}})
	if resp.StatusCode < 400 {
		t.Fatalf("receiving more than was sent should be refused, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// The steps are in an order, and asking for one out of turn says which step the
// transfer is actually at rather than failing obscurely.
func TestATransferCannotSkipApproval(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	transferID := f.requestTransfer(t, h, f.warehouseID, f.backRoomID, "4")

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/transfers/"+transferID+"/dispatch"), f.token,
		map[string]any{"lines": []map[string]any{}})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dispatch before approval should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- stock locations ------------------------------------------------------

// Retiring a room does not empty it. The movements stay, the valuation keeps
// counting them, and the shop is left with stock the screens no longer show.
func TestALocationHoldingStockCannotBeRetired(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)
	f.receiveStock(t, h, f.backRoomID, "5", "20.00")

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/locations/"+f.backRoomID.String()+"/active"),
		f.token, map[string]any{"is_active": false})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("retiring a location holding stock should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// The in-transit location is system-owned. It must never appear in a list
// somebody picks a destination from.
func TestTheTransitLocationIsNotOfferedAsAChoice(t *testing.T) {
	h := newHarness(t)
	f := seedStock(t, h)

	resp := h.do(t, http.MethodGet, f.path("/api/v1/stock/locations"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list locations: %s", readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["kind"] == "transit" {
			t.Fatalf("the in-transit location is offered as a choice: %v", row)
		}
	}
	if len(rows) == 0 {
		t.Fatal("no stock locations at all, so the list proves nothing")
	}
}

// --- helpers --------------------------------------------------------------

func (f *stockFixture) openCount(t *testing.T, h *harness) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/stock/counts"), f.token,
		map[string]any{
			"location_id": f.warehouseID.String(),
			"note":        "Monthly count",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open count: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

func (f *stockFixture) saveCount(t *testing.T, h *harness, countID, qty string) {
	t.Helper()
	resp := h.do(t, http.MethodPut,
		f.path("/api/v1/stock/counts/"+countID), f.token,
		map[string]any{"lines": []map[string]any{
			{"variant_id": f.variantID.String(), "counted_qty": qty},
		}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("save count: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
}

func (f *stockFixture) postCount(t *testing.T, h *harness, countID string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/stock/counts/"+countID+"/post"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post count: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

func (f *stockFixture) requestTransfer(
	t *testing.T, h *harness, from, to uuid.UUID, qty string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/stock/transfers"), f.token,
		map[string]any{
			"from_location_id": from.String(),
			"to_location_id":   to.String(),
			"note":             "Restocking the shop floor",
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": qty},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("request transfer: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

// secondVariant is another product in the same catalogue, for tests that need
// a line they are not asserting about.
func (f *stockFixture) secondVariant(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		var productID uuid.UUID
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'P-SECOND','Second Product','standard') RETURNING id`,
			f.tenantID, f.companyID).Scan(&productID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			INSERT INTO variant (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,'SKU-SECOND','50.0000') RETURNING id`,
			f.tenantID, f.companyID, productID).Scan(&id)
	}); err != nil {
		t.Fatalf("seed a second variant: %v", err)
	}
	return id
}

// receiveForTest puts stock on a shelf through the real costing engine, so the
// layers, the pool and the movement are the ones the product would have made.
// Writing the rows by hand would prove the test's arithmetic rather than the
// engine's.
func receiveForTest(
	t *testing.T, tx pgx.Tx, f *stockFixture,
	warehouseID uuid.UUID, qty, unitCost string,
) (decimal.Decimal, error) {
	t.Helper()
	return inventory.Receive(t.Context(), tx, inventory.Receipt{
		TenantID: f.tenantID, CompanyID: f.companyID,
		VariantID: f.variantID, WarehouseID: warehouseID,
		Qty:      decimal.RequireFromString(qty),
		UnitCost: decimal.RequireFromString(unitCost),
		Reason:   "opening",
	})
}

// consumeForTest takes stock off a shelf and books the cost, which is what a
// sale does. Used where a test needs stock to move for a reason that is not the
// thing under test — somebody selling during a count.
func consumeForTest(
	t *testing.T, tx pgx.Tx, f *stockFixture, warehouseID uuid.UUID, qty string,
) error {
	t.Helper()
	res, err := inventory.Consume(t.Context(), tx, inventory.Issue{
		TenantID: f.tenantID, CompanyID: f.companyID,
		VariantID: f.variantID, WarehouseID: warehouseID,
		Qty:    decimal.RequireFromString(qty),
		Reason: "sale",
	})
	if err != nil {
		return err
	}
	return postStockEntry(t, tx, f, "5100", res.TotalCost.Neg())
}

// postOpeningStock books the ledger half of stock arriving.
func postOpeningStock(
	t *testing.T, tx pgx.Tx, f *stockFixture, value decimal.Decimal,
) error {
	t.Helper()
	return postStockEntry(t, tx, f, "1100", value)
}

// postStockEntry moves the Inventory account by `value` — signed — against the
// account named by `otherCode`.
//
// Written by hand rather than through `accounting.PostByRule` because these are
// fixture events, not product events: there is no posting rule for "a test put
// stock on a shelf", and inventing one would put a rule in the database that no
// real transaction ever uses.
func postStockEntry(
	t *testing.T, tx pgx.Tx, f *stockFixture, otherCode string, value decimal.Decimal,
) error {
	t.Helper()
	if value.IsZero() {
		return nil
	}
	ctx := t.Context()

	var periodID, entryID, inventoryID, otherID uuid.UUID
	if e := tx.QueryRow(ctx,
		`SELECT id FROM fiscal_period WHERE company_id = $1`, f.companyID).
		Scan(&periodID); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx,
		`SELECT account_id FROM account_role_map
		 WHERE company_id = $1 AND role = 'inventory'`, f.companyID).
		Scan(&inventoryID); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx,
		`SELECT id FROM account WHERE company_id = $1 AND code = $2`,
		f.companyID, otherCode).Scan(&otherID); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx, `
		INSERT INTO journal_entry
		  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
		VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-01','opening_stock')
		RETURNING id`,
		f.tenantID, f.companyID, periodID).Scan(&entryID); e != nil {
		return e
	}

	debit, credit := value, decimal.Zero
	if value.IsNegative() {
		debit, credit = decimal.Zero, value.Neg()
	}
	if _, e := tx.Exec(ctx, `
		INSERT INTO journal_line
		  (tenant_id, entry_id, line_no, account_id, currency,
		   debit, credit, base_debit, base_credit)
		VALUES ($1,$2,1,$3,'SAR',$4,$5,$4,$5)`,
		f.tenantID, entryID, inventoryID, debit, credit); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `
		INSERT INTO journal_line
		  (tenant_id, entry_id, line_no, account_id, currency,
		   debit, credit, base_debit, base_credit)
		VALUES ($1,$2,2,$3,'SAR',$4,$5,$4,$5)`,
		f.tenantID, entryID, otherID, credit, debit)
	return e
}
