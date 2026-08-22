//go:build integration

// The nightly tie-out (design 08 §3 and §6, QA gate M1).
//
// Three invariants — AR, AP and stock valuation against their control accounts
// — were proved on every build and watched on no live tenant. These tests hold
// the job that now watches them to the two things that matter: that a company
// whose books agree is left alone, and that a company whose books do not agree
// produces an exception somebody will see.
//
// The divergences here are manufactured by posting a journal entry that moves a
// control account with nothing behind it in the sub-ledger. That is not an
// artificial case: it is exactly what a hand-written correction, a half-built
// import or a future module posting to the wrong role would do, and it is the
// shape of error the job exists to catch.
package api

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
)

// postUnbackedEntry moves a control account with no sub-ledger behind it.
//
// Balanced, so the trial balance still adds up and the deferred constraint is
// satisfied — the books are internally consistent and still wrong, which is the
// only kind of wrong a tie-out can find.
func postUnbackedEntry(
	t *testing.T, h *harness, f *shopFixture, controlRole, amount string,
) {
	t.Helper()
	value := decimal.RequireFromString(amount)

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := accounting.Post(t.Context(), tx, accounting.Entry{
			TenantID: f.tenantID, CompanyID: f.companyID,
			Date:       time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
			SourceType: "test_manual_entry", SourceID: uuid.New(),
			Memo: "a correction nobody put in the sub-ledger",
			Lines: []accounting.Line{
				{Role: controlRole, Side: accounting.Debit, Amount: value},
				{Role: "sales_revenue", Side: accounting.Credit, Amount: value},
			},
		})
		return e
	})
	if err != nil {
		t.Fatalf("post the unbacked entry: %v", err)
	}
}

// openTieOutAlerts returns the level and detail of any standing tie-out alert.
func openTieOutAlerts(t *testing.T, h *harness, f *shopFixture) []string {
	t.Helper()
	var out []string
	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT level || ': ' || detail FROM compliance_alert
			WHERE company_id = $1 AND kind = 'accounting.tie_out'
			  AND cleared_at IS NULL`, f.companyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if e := rows.Scan(&s); e != nil {
				return e
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read the alerts: %v", err)
	}
	return out
}

func sweepTieOut(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := jobs.NewTieOutSweeper(h.pool).
		Run(t.Context(), jobs.Job{TenantID: &f.tenantID}); err != nil {
		t.Fatalf("tie-out sweep: %v", err)
	}
}

// A shop that has traded normally ties out, and hears nothing.
//
// The case that must not produce an alert. A tie-out that cried wolf on healthy
// books would be switched off within a week, and then it would not be there on
// the day it mattered.
func TestBooksThatAgreeRaiseNoAlert(t *testing.T) {
	h := newHarness(t)
	f, owner := settlingShop(t, h)

	// Real trade through the real routes: a card sale, and a deposit settling
	// it. Both move control accounts, so this is not the trivial empty case.
	sellByCard(t, h, f, "230.00")
	tenderID, _ := pendingTenders(t, h, f, owner)[0].(map[string]any)["tender_id"].(string)
	resp := h.do(t, "POST", settlementPath(f, "/api/v1/settlement/batches"), owner,
		map[string]any{
			"uuid": uuid.NewString(), "reference": "MADA-TIEOUT",
			"deposited_on": "2026-08-17", "net_amount": "225.00",
			"tender_ids": []string{tenderID},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("settle: %s", readBody(t, resp))
	}
	resp.Body.Close()

	sweepTieOut(t, h, f)

	if alerts := openTieOutAlerts(t, h, f); len(alerts) != 0 {
		t.Errorf("a shop whose books agree was alerted: %v", alerts)
	}
}

// A sub-ledger that disagrees with its control account is an exception.
//
// Blueprint C13: "any divergence is flagged as an exception". Critical, because
// the figure on a balance sheet is wrong until somebody looks.
func TestADivergedSubLedgerRaisesACriticalAlert(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   string
		expect string
		amount string
	}{
		{"receivables", "accounts_receivable", "Customer balances", "125.00"},
		{"inventory", "inventory", "Stock valuation", "40.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			f, _ := settlingShop(t, h)

			postUnbackedEntry(t, h, f, tc.role, tc.amount)
			sweepTieOut(t, h, f)

			alerts := openTieOutAlerts(t, h, f)
			if len(alerts) != 1 {
				t.Fatalf("%d open tie-out alerts, want 1 — %v", len(alerts), alerts)
			}
			alert := alerts[0]
			if !strings.Contains(alert, "critical") {
				t.Errorf("alert level is not critical: %s", alert)
			}
			if !strings.Contains(alert, tc.expect) {
				t.Errorf("the alert does not name %q, so nobody knows where to "+
					"look: %s", tc.expect, alert)
			}
			if !strings.Contains(alert, tc.amount) {
				t.Errorf("the alert does not say how far out it is: %s", alert)
			}
			// It must not claim to have done anything about it. A tie-out that
			// corrected the books would destroy the evidence of the cause.
			if !strings.Contains(alert, "Nothing has been changed") {
				t.Errorf("the alert does not say the books were left alone: %s", alert)
			}
		})
	}
}

// Two sub-ledgers out at once produce one alert naming both.
//
// One alert, because a second one about the same night tells an owner nothing
// the first did not — and the open-alert index permits only one per kind and
// level anyway, so a per-check alert would silently drop all but the first.
func TestTwoDivergencesProduceOneAlertNamingBoth(t *testing.T) {
	h := newHarness(t)
	f, _ := settlingShop(t, h)

	postUnbackedEntry(t, h, f, "accounts_receivable", "10.00")
	postUnbackedEntry(t, h, f, "inventory", "7.50")
	sweepTieOut(t, h, f)

	alerts := openTieOutAlerts(t, h, f)
	if len(alerts) != 1 {
		t.Fatalf("%d alerts, want exactly 1 — %v", len(alerts), alerts)
	}
	for _, want := range []string{"Customer balances", "Stock valuation", " and "} {
		if !strings.Contains(alerts[0], want) {
			t.Errorf("the alert does not mention %q: %s", want, alerts[0])
		}
	}
}

// The alert clears when the books agree again.
//
// An alert that outlives its problem teaches people to ignore the next one.
func TestTheTieOutAlertClearsWhenTheBooksAgreeAgain(t *testing.T) {
	h := newHarness(t)
	f, _ := settlingShop(t, h)

	postUnbackedEntry(t, h, f, "accounts_receivable", "60.00")
	sweepTieOut(t, h, f)
	if len(openTieOutAlerts(t, h, f)) != 1 {
		t.Fatal("the divergence raised no alert, so the rest of this proves nothing")
	}

	// Put it right the only way posted history allows: another entry, not an
	// edit. Design 02 §2 — corrections are reversing documents.
	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := accounting.Post(t.Context(), tx, accounting.Entry{
			TenantID: f.tenantID, CompanyID: f.companyID,
			Date:       time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
			SourceType: "test_manual_entry", SourceID: uuid.New(),
			Memo: "reversing the correction",
			Lines: []accounting.Line{
				{Role: "sales_revenue", Side: accounting.Debit,
					Amount: decimal.RequireFromString("60.00")},
				{Role: "accounts_receivable", Side: accounting.Credit,
					Amount: decimal.RequireFromString("60.00")},
			},
		})
		return e
	})
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	sweepTieOut(t, h, f)
	if alerts := openTieOutAlerts(t, h, f); len(alerts) != 0 {
		t.Errorf("the alert survived the books being put right: %v", alerts)
	}
}

// One tenant's sweep never reads or alerts on another's books.
//
// Row-level security is what enforces this, and the sweep runs in tenant
// context precisely so that it does. A sweep that ran as the platform would see
// everything and this test would fail.
func TestATieOutSweepStaysInsideItsTenant(t *testing.T) {
	h := newHarness(t)
	mine, _ := settlingShop(t, h)
	theirs, _ := settlingShop(t, h)

	// Their books are broken; mine are not.
	postUnbackedEntry(t, h, theirs, "accounts_receivable", "500.00")

	// Sweeping MY tenant must not notice, and must not alert on their company.
	sweepTieOut(t, h, mine)

	if alerts := openTieOutAlerts(t, h, mine); len(alerts) != 0 {
		t.Errorf("my tenant was alerted about another tenant's books: %v", alerts)
	}
	if alerts := openTieOutAlerts(t, h, theirs); len(alerts) != 0 {
		t.Errorf("a sweep of my tenant raised an alert in theirs: %v", alerts)
	}

	// And their own sweep does find it, or the check above passes merely
	// because nothing ever alerts.
	sweepTieOut(t, h, theirs)
	if alerts := openTieOutAlerts(t, h, theirs); len(alerts) != 1 {
		t.Errorf("their own sweep found %d alerts, want 1", len(alerts))
	}
}

// A company that has never been provisioned is skipped, not alerted.
//
// Every difference on a company with no chart is trivially zero, so it would
// pass anyway — but "nothing to reconcile" and "reconciled" are different
// statements, and a tenant mid-onboarding should produce no accounting noise.
func TestACompanyWithNoChartIsSkipped(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "owner")

	var bare uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,'Not Yet Onboarded','sa','SAR') RETURNING id`,
			f.tenantID).Scan(&bare)
	}); err != nil {
		t.Fatalf("seed a bare company: %v", err)
	}

	sweepTieOut(t, h, f)

	var alerts int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM compliance_alert
			WHERE company_id = $1 AND cleared_at IS NULL`, bare).Scan(&alerts)
	}); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if alerts != 0 {
		t.Errorf("%d alerts raised against a company with no chart", alerts)
	}
}

// A sweep with no tenant is a bug in the caller and must not retry forever.
func TestATieOutWithoutATenantIsPermanent(t *testing.T) {
	h := newHarness(t)
	err := jobs.NewTieOutSweeper(h.pool).Run(t.Context(), jobs.Job{})
	if err == nil {
		t.Fatal("a tie-out naming no tenant was accepted")
	}
	if !jobs.IsPermanent(err) {
		t.Errorf("the failure is retryable: %v — a job that can never succeed "+
			"must not occupy the queue until its attempts run out", err)
	}
}

// A shop that owes its suppliers money still ties out.
//
// The payables check is the one written for this job, so it is the one most
// likely to be wrong — and the way it would be wrong is the sign. Payables are
// a liability and credit-normal while receivables are an asset and debit-
// normal, so a check copied from the receivables one without flipping the
// subtraction reports every company with an unpaid bill as diverged by twice
// what it owes. That alert would be switched off in a week.
//
// So this leaves a real bill outstanding rather than paying it: an AP control
// balance and a supplier sub-ledger that must agree while both are non-zero.
func TestAShopThatOwesItsSuppliersStillTiesOut(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	poID, lineID := raiseOrder(t, h, f, "10", "100.00")
	received := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})
	if received.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, received))
	}
	received.Body.Close()

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	billed.Body.Close()

	// Deliberately unpaid. The point is a live, non-zero payable.
	if owed := payableBalance(t, h, f); owed.IsZero() {
		t.Fatal("the fixture left nothing owed, so the payables check is untested here")
	}

	sweepTieOut(t, h, f.shopFixture)

	if alerts := openTieOutAlerts(t, h, f.shopFixture); len(alerts) != 0 {
		t.Errorf("a shop with an ordinary unpaid supplier bill was reported as "+
			"out of balance: %v", alerts)
	}
}
