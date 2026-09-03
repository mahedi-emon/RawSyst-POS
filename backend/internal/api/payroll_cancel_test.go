//go:build integration

// Cancelling a payroll run, and resolving GOSI at the month being paid.
//
// 0091 modelled a cancelled run and made the month's uniqueness index partial
// so a cancelled one releases its month — and nothing could set the status. A
// month approved on the wrong figures was permanent: the entries stayed, and
// the month could never be run again. 0119 and people.Cancel close that, and
// these are the tests that hold it shut.
package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// runFootprint totals what every entry carrying this run's id did to each
// account. After a cancellation every one of them must be zero: that is what
// "reversed" means, and it is stronger than counting entries.
func runFootprint(t *testing.T, h *harness, f *shopFixture, runID string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			rows, e := tx.Query(context.Background(), `
				SELECT a.code, sum(l.debit - l.credit)::text
				FROM journal_line l
				JOIN journal_entry e ON e.id = l.entry_id
				JOIN account a ON a.id = l.account_id
				WHERE e.source_id = $1
				GROUP BY a.code`, runID)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var code, net string
				if e := rows.Scan(&code, &net); e != nil {
					return e
				}
				out[code] = net
			}
			return rows.Err()
		}); err != nil {
		t.Fatalf("read the run's ledger footprint: %v", err)
	}
	return out
}

func assertFootprintIsNil(t *testing.T, footprint map[string]string) {
	t.Helper()
	if len(footprint) == 0 {
		t.Fatal("the run touched no accounts at all, so this proves nothing " +
			"about reversal")
	}
	for code, net := range footprint {
		if !amountsEqual(net, "0") {
			t.Errorf("account %s is left %s out after the cancellation; the "+
				"run's entries have not been fully reversed", code, net)
		}
	}
}

// cashAccount opens a money account the payroll can be paid from.
func cashAccount(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	company := "?company_id=" + f.companyID.String()

	var cashLedger string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT a.id::text FROM account a
				JOIN account_role_map m ON m.account_id = a.id
				WHERE a.company_id = $1 AND m.role = 'cash'`,
				f.companyID).Scan(&cashLedger)
		}); err != nil {
		t.Fatalf("find the cash ledger account: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/treasury/accounts"+company,
		f.token, map[string]any{
			"kind": "cash", "name": "Payroll Cash", "currency": "SAR",
			"account_id": cashLedger,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open a cash account: %s", readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)["id"].(string)
}

// prepareRun computes a month and returns its id.
func prepareRun(t *testing.T, h *harness, f *shopFixture, period string) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), f.token,
		map[string]any{"period": period})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare %s: %s", period, readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)["id"].(string)
}

func postRun(t *testing.T, h *harness, f *shopFixture, path string,
	body map[string]any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/payroll/"+path+"?company_id="+f.companyID.String(),
		f.token, body)
}

// --- the reversal ---------------------------------------------------------

// An approved run can be cancelled, and its entries come back out.
func TestCancellingAnApprovedRunReversesWhatItPosted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	runID := prepareRun(t, h, f, "2026-08")
	if resp := postRun(t, h, f, runID+"/approve", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}

	// It really did post something, or the reversal below proves nothing.
	before := runFootprint(t, h, f, runID)
	if len(before) == 0 {
		t.Fatal("approving posted nothing at all")
	}

	resp := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "August attendance was entered twice"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if got, _ := decodeJSONFrom(t, resp)["status"].(string); got != "cancelled" {
		t.Errorf("status after cancelling is %q, want cancelled", got)
	}

	assertFootprintIsNil(t, runFootprint(t, h, f, runID))
	assertTrialBalanceBalances(t, h, f)
}

// A paid run reverses the payment as well as the accrual.
//
// Three entries go in — the wage accrual, the employer's own social insurance
// and the money leaving — and all three have to come back out. Reversing the
// accrual alone would leave the bank short with nothing owed.
func TestCancellingAPaidRunReversesThePaymentToo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Saud Al-Qahtani", "8000.00", map[string]any{
		"is_saudi": true, "nationality": "SA",
	})
	account := cashAccount(t, h, f)

	runID := prepareRun(t, h, f, "2026-08")
	if resp := postRun(t, h, f, runID+"/approve", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}
	if resp := postRun(t, h, f, runID+"/pay", map[string]any{
		"account_id": account, "paid_on": "2026-09-01",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("pay: %s", readBody(t, resp))
	}

	resp := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "Paid against the wrong bank account"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %s", resp.StatusCode, readBody(t, resp))
	}

	assertFootprintIsNil(t, runFootprint(t, h, f, runID))
	assertTrialBalanceBalances(t, h, f)

	// The reversal is recorded as one, not as a fresh unrelated entry.
	var reversals int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT count(*) FROM journal_entry
				WHERE source_id = $1 AND reverses_id IS NOT NULL`, runID).
				Scan(&reversals)
		}); err != nil {
		t.Fatalf("count reversals: %v", err)
	}
	if reversals != 3 {
		t.Errorf("%d entries point at what they undo, want 3 — the accrual, "+
			"the employer's insurance and the payment", reversals)
	}
}

// The month is free again, which is the whole point of the partial index.
func TestTheMonthCanBeRunAgainAfterACancellation(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	first := prepareRun(t, h, f, "2026-08")
	if resp := postRun(t, h, f, first+"/approve", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}

	// Before cancelling, the month is locked.
	blocked := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), f.token,
		map[string]any{"period": "2026-08"})
	if blocked.StatusCode != http.StatusConflict {
		t.Errorf("a second August got %d, want 409 while the first stands",
			blocked.StatusCode)
	}
	blocked.Body.Close()

	if resp := postRun(t, h, f, first+"/cancel",
		map[string]any{"reason": "Wrong basic salary on one employee"},
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %s", readBody(t, resp))
	}

	second := prepareRun(t, h, f, "2026-08")
	if second == first {
		t.Fatal("re-running August returned the cancelled run")
	}
}

// Cancelling twice is refused rather than posting a second reversal.
func TestACancelledRunCannotBeCancelledAgain(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	runID := prepareRun(t, h, f, "2026-08")
	if resp := postRun(t, h, f, runID+"/approve", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}
	if resp := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "Duplicate run"},
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %s", readBody(t, resp))
	}

	again := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "Again"})
	defer again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Errorf("cancelling twice got %d, want 409", again.StatusCode)
	}
	assertFootprintIsNil(t, runFootprint(t, h, f, runID))
}

// A cancellation says why. The ledger carries the reversal; this carries the
// reason.
func TestACancellationMustSayWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	runID := prepareRun(t, h, f, "2026-08")
	resp := postRun(t, h, f, runID+"/cancel", map[string]any{"reason": "   "})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity &&
		resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a cancellation with no reason got %d, want a refusal",
			resp.StatusCode)
	}
}

// The advances a cancelled run recovered go back to being owed.
func TestCancellingReleasesTheAdvancesTheRunRecovered(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()
	employee := h.hire(t, f, "Omar Farouk", "4000.00", nil)
	account := cashAccount(t, h, f)

	resp := h.do(t, http.MethodPost, "/api/v1/advances"+company, f.token,
		map[string]any{
			"employee_id": employee, "account_id": account,
			"amount": "1000.00", "installments": 2, "reason": "Family expenses",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("advance: %s", readBody(t, resp))
	}
	advanceID := decodeJSONFrom(t, resp)["id"].(string)

	runID := prepareRun(t, h, f, "2026-08")
	if got := outstandingOf(t, h, f, advanceID); !amountsEqual(got, "500.00") {
		t.Fatalf("after the run recovered an instalment the advance is %s, "+
			"want 500.00", got)
	}

	if resp := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "Recovered from the wrong employee"},
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %s", readBody(t, resp))
	}

	if got := outstandingOf(t, h, f, advanceID); !amountsEqual(got, "1000.00") {
		t.Errorf("the advance is %s after the run was cancelled, want "+
			"1000.00 — it was never actually repaid", got)
	}
}

func outstandingOf(t *testing.T, h *harness, f *shopFixture, advanceID string) string {
	t.Helper()
	var out string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(),
				`SELECT advance_outstanding($1)::text`, advanceID).Scan(&out)
		}); err != nil {
		t.Fatalf("read the advance: %v", err)
	}
	return out
}

// A wage file already handed to the bank cannot be cancelled away.
//
// The product can reverse its own ledger. It cannot recall a transfer somebody
// has already instructed, and pretending otherwise would leave the books
// saying the month never happened while the money was on its way.
func TestARunWhoseWageFileWentToTheBankCannotBeCancelled(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	// What the Ministry's format needs of the employer: its bank's SARIE code,
	// its establishment ID with that bank, the account wages leave from and
	// its Ministry of Labour establishment ID (0115).
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(), `
				UPDATE company
				SET wps_bank_sarie_id = 'RJHI', wps_establishment_id = '1234567',
				    wps_bank_account = 'SA0380000000608010167519',
				    mol_establishment_id = '1234567890'
				WHERE id = $1`, f.companyID)
			return e
		}); err != nil {
		t.Fatalf("give the company its WPS details: %v", err)
	}

	runID := prepareRun(t, h, f, "2026-08")
	if resp := postRun(t, h, f, runID+"/approve", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}
	resp := postRun(t, h, f, runID+"/wage-file", nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("draw the wage file: %d %s", resp.StatusCode,
			readBody(t, resp))
	}
	resp.Body.Close()

	// The file goes to Mudad. Recorded directly: submitting is a person
	// telling the product what they did outside it.
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(), `
				UPDATE wps_file SET status = 'submitted', submitted_at = now()
				WHERE run_id = $1`, runID)
			return e
		}); err != nil {
		t.Fatalf("mark the file submitted: %v", err)
	}

	refused := postRun(t, h, f, runID+"/cancel",
		map[string]any{"reason": "Changed our mind"})
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusConflict {
		t.Errorf("cancelling behind a submitted wage file got %d, want 409",
			refused.StatusCode)
	}
}

// --- GOSI resolves at the month being paid --------------------------------

// The GOSI rate is the one in force for the month worked, not the month run.
//
// 0117 records GOSI's figures from 2026-02-01 and closes the placeholder that
// stood before it. A run for January 2026 therefore resolves the placeholder
// and must report social insurance as uncalculable, while February resolves
// the recorded rates and deducts. If resolution used today's date instead,
// both would deduct and every historical month would be restated at whatever
// the rule says now.
func TestGOSIIsResolvedAtTheMonthWorkedNotTheMonthRun(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()
	h.hire(t, f, "Saud Al-Qahtani", "8000.00", map[string]any{
		"is_saudi": true, "nationality": "SA",
	})

	for _, c := range []struct {
		period    string
		available bool
		why       string
	}{
		{"2026-01", false, "before 0117 the rule held a placeholder"},
		{"2026-02", true, "0117 records GOSI's figures from 2026-02-01"},
	} {
		resp := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
			map[string]any{"period": c.period})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("prepare %s: %s", c.period, readBody(t, resp))
		}
		run := decodeJSONFrom(t, resp)

		blocked, _ := run["gosi_unavailable"].(bool)
		if blocked == c.available {
			t.Errorf("%s: social insurance available = %v, want %v — %s",
				c.period, !blocked, c.available, c.why)
		}
		if c.available {
			slips, _ := run["payslips"].([]any)
			slip, _ := slips[0].(map[string]any)
			// 9.75% of 8,000, the rate 0117 recorded.
			if got, _ := slip["gosi_employee"].(string); !amountsEqual(got,
				"780.00") {
				t.Errorf("%s: the employee's GOSI is %s, want 780.00",
					c.period, got)
			}
		}
	}
}

// A month resolves the same rate however long after it somebody runs it.
//
// The guarantee behind re-running a closed month: the figures come from the
// period, so a run prepared today for August and one prepared next year agree.
func TestRunningAnOldMonthTwiceGivesTheSameGOSI(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Saud Al-Qahtani", "8000.00", map[string]any{
		"is_saudi": true, "nationality": "SA",
	})

	gosiFor := func(period string) string {
		t.Helper()
		id := prepareRun(t, h, f, period)
		resp := h.do(t, http.MethodGet,
			"/api/v1/payroll/"+id+"?company_id="+f.companyID.String(),
			f.token, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("read run: %s", readBody(t, resp))
		}
		slips, _ := decodeJSONFrom(t, resp)["payslips"].([]any)
		slip, _ := slips[0].(map[string]any)
		out, _ := slip["gosi_employee"].(string)

		if r := postRun(t, h, f, id+"/cancel",
			map[string]any{"reason": "Recomputing the same month"},
		); r.StatusCode != http.StatusOK {
			t.Fatalf("cancel: %s", readBody(t, r))
		}
		return out
	}

	first := gosiFor("2026-08")
	second := gosiFor("2026-08")
	if !amountsEqual(first, second) {
		t.Errorf("August computed %s and then %s; the same month must "+
			"resolve the same rule", first, second)
	}
	if !amountsEqual(first, "780.00") {
		t.Errorf("August's GOSI is %s, want 780.00", first)
	}
	_ = time.Now
}
