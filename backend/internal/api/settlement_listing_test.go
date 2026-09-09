//go:build integration

// Reading a deposit back after it has been recorded.
//
// `POST /settlement/batches` answered with a batch id once, in a response, and
// `GET /settlement/batches/{id}` was the only way to see it again — so the
// detail route was reachable only by somebody who had kept the id. Nothing
// listed what had been matched, which is the first thing anybody reconciling a
// month's card takings needs.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func listBatches(
	t *testing.T, h *harness, f *shopFixture, token, query string,
) []map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches")+query, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list deposits: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := decodeJSON(t, resp)["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, row := range raw {
		if m, ok := row.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// recordDeposit banks one card sale and hands back the batch it made.
func recordDeposit(
	t *testing.T, h *harness, f *shopFixture, owner, price, net, on, ref string,
) map[string]any {
	t.Helper()
	sellByCard(t, h, f, price)
	pending := pendingTenders(t, h, f, owner)
	if len(pending) == 0 {
		t.Fatal("a card sale left nothing awaiting settlement")
	}
	ids := make([]string, 0, len(pending))
	for _, row := range pending {
		id, _ := row.(map[string]any)["tender_id"].(string)
		ids = append(ids, id)
	}

	resp := h.do(t, http.MethodPost,
		settlementPath(f, "/api/v1/settlement/batches"), owner, map[string]any{
			"uuid":         uuid.NewString(),
			"reference":    ref,
			"deposited_on": on,
			"net_amount":   net,
			"tender_ids":   ids,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record the deposit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// A deposit that has been recorded can be found again.
func TestARecordedDepositCanBeFoundAgain(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	made := recordDeposit(t, h, f, owner, "1000.00", "985.00", "2026-08-17",
		"MADA-20260817-001")
	batchID, _ := made["id"].(string)

	rows := listBatches(t, h, f, owner, "")
	var found map[string]any
	for _, r := range rows {
		if r["id"] == batchID {
			found = r
		}
	}
	if found == nil {
		t.Fatal("a deposit recorded a moment ago cannot be found in the list " +
			"of deposits, so nothing in the product can reach its detail")
	}

	if found["reference"] != "MADA-20260817-001" {
		t.Errorf("reference = %v; the line on the bank statement is the whole "+
			"point of recording a deposit", found["reference"])
	}
	if found["gross_amount"] != "1000.00" || found["fee_amount"] != "15.00" ||
		found["net_amount"] != "985.00" {
		t.Errorf("row = gross %v, fee %v, net %v; want 1000.00 / 15.00 / 985.00",
			found["gross_amount"], found["fee_amount"], found["net_amount"])
	}
	if found["tender_count"] != float64(1) {
		t.Errorf("tender_count = %v, want 1", found["tender_count"])
	}
	if found["posted"] != true {
		t.Error("a deposit that posted a journal entry reports itself unposted, " +
			"which would show an unreconciled deposit as reconciled")
	}
	if found["currency"] == nil || found["currency"] == "" {
		t.Error("a figure to be matched against a bank statement is stated " +
			"with no currency")
	}
}

// A freshly recorded deposit states its currency, like a replayed one.
//
// It did not. `readBatch` reads `base_currency` and the create path built the
// struct by hand without it, so recording a deposit answered with an empty
// currency and recording the SAME deposit twice answered with the right one.
func TestARecordedDepositStatesItsCurrencyStraightAway(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	made := recordDeposit(t, h, f, owner, "500.00", "492.50", "2026-08-18",
		"MADA-20260818-001")
	if made["currency"] == nil || made["currency"] == "" {
		t.Fatal("recording a deposit answers with no currency, so the screen " +
			"that shows it has nothing to print beside the figure")
	}
}

// The period filter narrows to the month somebody is reconciling.
func TestTheDepositListNarrowsToItsPeriod(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	recordDeposit(t, h, f, owner, "100.00", "98.00", "2026-08-05", "AUG-05")
	recordDeposit(t, h, f, owner, "200.00", "196.00", "2026-08-20", "AUG-20")

	rows := listBatches(t, h, f, owner, "&from=2026-08-10&to=2026-08-31")
	for _, r := range rows {
		if r["reference"] == "AUG-05" {
			t.Fatal("a deposit before the window is inside the results")
		}
	}
	seen := false
	for _, r := range rows {
		if r["reference"] == "AUG-20" {
			seen = true
		}
	}
	if !seen {
		t.Error("a deposit inside the window is missing from the results")
	}
}

// Reading deposits is `accounting.view`; recording one is `accounting.create`.
func TestAnAuditorReadsTheDepositsAndCannotRecordOne(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	recordDeposit(t, h, f, owner, "100.00", "97.00", "2026-08-11", "AUG-11")

	auditor := h.seedUserIn(t, f, "auditor")

	resp := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches"), auditor, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an auditor cannot read the deposits: %d", resp.StatusCode)
	}

	// The cashier holds neither, so the read is refused outright.
	refused := h.do(t, http.MethodGet,
		settlementPath(f, "/api/v1/settlement/batches"), f.token, nil)
	refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier read the bank deposits: %d", refused.StatusCode)
	}
}

// One business never sees another's deposits.
func TestTheDepositListIsConfinedToItsOwnCompany(t *testing.T) {
	h := newHarness(t)
	mine, mineOwner := settlingShop(t, h)
	theirs, theirOwner := settlingShop(t, h)
	made := recordDeposit(t, h, theirs, theirOwner, "300.00", "295.00",
		"2026-08-12", "THEIRS-12")
	theirBatch, _ := made["id"].(string)

	for _, r := range listBatches(t, h, mine, mineOwner, "") {
		if r["id"] == theirBatch {
			t.Fatal("one business can read another's bank deposits")
		}
	}

	// Naming their company id directly answers with nothing rather than with
	// their deposits. An unscoped actor passes `CanAccessCompany` for any id,
	// so what confines this is row-level security on the tenant — and that is
	// the property worth asserting.
	resp := h.do(t, http.MethodGet,
		"/api/v1/settlement/batches?company_id="+theirs.companyID.String(),
		mineOwner, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("naming another company answered %d", resp.StatusCode)
	}
	leaked, _ := decodeJSON(t, resp)["data"].([]any)
	if len(leaked) != 0 {
		t.Fatalf("naming another company's id returned %d of their deposits",
			len(leaked))
	}
}

// Recording a deposit names who typed the fee.
//
// The fee is the one figure in this module that comes from outside the system:
// somebody reads it off a bank statement. A deposit that wrote off two hundred
// riyals of card takings as "fee" named nobody at all.
func TestRecordingADepositIsAudited(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)
	made := recordDeposit(t, h, f, owner, "1000.00", "985.00", "2026-08-17",
		"MADA-AUDIT-1")
	batchID, _ := made["id"].(string)

	var label, fee string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(actor_label,''), coalesce(after_value->>'fee','')
			FROM audit_log
			WHERE action = 'settlement_batch_recorded' AND entity_id = $1`,
			batchID).Scan(&label, &fee)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if label == "" {
		t.Error("the deposit entry names nobody")
	}
	if fee != "15.00" {
		t.Errorf("the trail records the fee as %q, want 15.00", fee)
	}
}
