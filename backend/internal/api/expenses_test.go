//go:build integration

// Cash expenses, derived from design 02 rule 5 rather than from the code.
//
// The rule is four lines long and every one of them is asserted here:
//
//	Dr  Expense Account
//	Dr  Input VAT Receivable    (only where recoverable — see below)
//	    Cr  Cash / Bank
//
// and the sentence under it, which is the whole reason this module needed a
// model rather than a posting rule:
//
//	Blueprint E2.3: entertainment, some vehicles, and fuel have restricted
//	input VAT recovery. Each expense head carries `input_vat_recoverable
//	BOOLEAN`; when false, the VAT is absorbed into the expense line rather
//	than claimed, so the VAT return is not overstated.
//
// Two things follow that a test written from the code would not ask.
//
// First, "absorbed" is a claim about the EXPENSE account, not only about the
// VAT account. The whole gross leaves the bank either way, so if the VAT is not
// claimed something else has to carry it, and the only candidate is the expense.
// A shop that spends 115 on fuel has spent 115 on fuel.
//
// Second, the credit is the gross in both cases. It is the one figure that does
// not move when recoverability changes, because what left the bank does not
// depend on what the tax authority will refund.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// expenseFixture is a company that can record an expense: a chart, the heads
// seeded with it, and an open period.
type expenseFixture struct {
	*shopFixture
	token string
}

func seedExpenses(t *testing.T, h *harness) *expenseFixture {
	t.Helper()
	f := h.seedShop(t, "owner")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed the chart: %v", err)
	}
	return &expenseFixture{shopFixture: f, token: f.token}
}

func (f *expenseFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// headNamed finds a seeded head by its code.
func headNamed(t *testing.T, h *harness, f *expenseFixture, code string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet, f.path("/api/v1/expenses/heads"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list heads: %s", readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["code"] == code {
			return row
		}
	}
	t.Fatalf("no seeded expense head with the code %q; the chart seed and the "+
		"head seed have drifted apart", code)
	return nil
}

// expenseAccountNamed finds a chart account by its code.
func expenseAccountNamed(t *testing.T, h *harness, f *expenseFixture, code string) string {
	t.Helper()
	resp := h.do(t, http.MethodGet, f.path("/api/v1/expenses/accounts"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list expense accounts: %s", readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["code"] == code {
			id, _ := row["id"].(string)
			return id
		}
	}
	t.Fatalf("no expense account with the code %q in the seeded chart", code)
	return ""
}

// recordExpense posts one expense of one line and returns the response.
func recordExpense(
	t *testing.T, h *harness, f *expenseFixture, headID, net, treatment string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "description": "test expense",
			"lines": []map[string]any{{
				"head_id": headID, "net_amount": net, "tax_treatment": treatment,
			}},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record expense: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// balanceOfAccount reads one account's movement, debits less credits.
func balanceOfAccount(
	t *testing.T, h *harness, f *expenseFixture, code string,
) decimal.Decimal {
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

// THE RULE, where the VAT can be reclaimed.
//
// SAR 1,000 of rent at 15%. The expense account takes the net, Input VAT
// Recoverable takes the tax, and Cash is credited the gross.
func TestARecoverableExpensePostsRuleFiveExactly(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	rentBefore := balanceOfAccount(t, h, f, "5200")
	vatBefore := balanceOfAccount(t, h, f, "2210")
	cashBefore := balanceOfAccount(t, h, f, "1100")

	head := headNamed(t, h, f, "RENT")
	if head["input_vat_recoverable"] != true {
		t.Fatalf("the seeded Rent head is not recoverable; E2.3 restricts "+
			"entertainment, vehicles and fuel, and rent is on none of those "+
			"lists: %v", head["input_vat_recoverable"])
	}
	headID, _ := head["id"].(string)

	out := recordExpense(t, h, f, headID, "1000.00", "standard")

	if out["subtotal_net"] != "1000.00" {
		t.Errorf("net = %v, want 1000.00", out["subtotal_net"])
	}
	if out["tax_total"] != "150.00" {
		t.Errorf("tax = %v, want 150.00 (15%% of 1000)", out["tax_total"])
	}
	if out["tax_recoverable"] != "150.00" {
		t.Errorf("recoverable VAT = %v, want all 150.00 of it", out["tax_recoverable"])
	}
	if out["tax_absorbed"] != "0.00" {
		t.Errorf("absorbed VAT = %v, want none: rent is recoverable",
			out["tax_absorbed"])
	}
	if out["total_inclusive"] != "1150.00" {
		t.Errorf("total = %v, want 1150.00", out["total_inclusive"])
	}

	if got := balanceOfAccount(t, h, f, "5200").Sub(rentBefore); !got.Equal(dec("1000")) {
		t.Errorf("Rent was debited %s, want 1000: the expense carries the net "+
			"and nothing else when the VAT is reclaimable", got)
	}
	if got := balanceOfAccount(t, h, f, "2210").Sub(vatBefore); !got.Equal(dec("150")) {
		t.Errorf("Input VAT Recoverable was debited %s, want 150", got)
	}
	if got := balanceOfAccount(t, h, f, "1100").Sub(cashBefore); !got.Equal(dec("-1150")) {
		t.Errorf("Cash moved %s, want -1150: the whole gross left the drawer", got)
	}
}

// THE SENTENCE UNDER THE RULE, which is why the head is a table.
//
// The same SAR 1,000 booked to a head whose VAT cannot be reclaimed. Nothing
// reaches Input VAT Recoverable, the expense carries the whole 1,150, and Cash
// is credited exactly what it was before — because what left the bank does not
// depend on what the tax authority will refund.
func TestANonRecoverableHeadAbsorbsTheVATIntoTheExpense(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	// A head of the kind E2.3 names. Pointed at Marketing's account, because
	// design 12's chart has no Entertainment account and inventing one to make
	// a test convenient is the invention P32 refused.
	account := expenseAccountNamed(t, h, f, "5230")
	created := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "ENTERTAIN", "name": "Client entertainment",
			"account_id": account, "input_vat_recoverable": false,
		})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create a restricted head: %s", readBody(t, created))
	}
	headID, _ := decodeJSON(t, created)["id"].(string)

	expenseBefore := balanceOfAccount(t, h, f, "5230")
	vatBefore := balanceOfAccount(t, h, f, "2210")
	cashBefore := balanceOfAccount(t, h, f, "1100")

	out := recordExpense(t, h, f, headID, "1000.00", "standard")

	if out["tax_total"] != "150.00" {
		t.Errorf("tax = %v, want 150.00: the supplier still charged it",
			out["tax_total"])
	}
	if out["tax_recoverable"] != "0.00" {
		t.Errorf("recoverable VAT = %v, want none. E2.3 restricts this "+
			"category, and claiming it would overstate the return",
			out["tax_recoverable"])
	}
	if out["tax_absorbed"] != "150.00" {
		t.Errorf("absorbed VAT = %v, want all 150.00", out["tax_absorbed"])
	}

	if got := balanceOfAccount(t, h, f, "5230").Sub(expenseBefore); !got.Equal(dec("1150")) {
		t.Errorf("the expense account was debited %s, want 1150. The VAT is "+
			"ABSORBED into the expense, not discarded: a shop that spends 1,150 "+
			"on entertainment has spent 1,150 on entertainment", got)
	}
	if got := balanceOfAccount(t, h, f, "2210").Sub(vatBefore); !got.IsZero() {
		t.Errorf("Input VAT Recoverable moved by %s on a restricted category; "+
			"the VAT return is now overstated by exactly that", got)
	}
	if got := balanceOfAccount(t, h, f, "1100").Sub(cashBefore); !got.Equal(dec("-1150")) {
		t.Errorf("Cash moved %s, want -1150. What left the drawer does not "+
			"depend on what can be reclaimed", got)
	}
}

// One receipt, several categories, and only one of them restricted.
//
// The case the model exists for: a fuel receipt that also covers a car wash is
// not two payments, and the recoverable half has to be separated line by line
// rather than for the document.
func TestOneExpenseSplitAcrossHeadsSplitsTheVATWithIt(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	restricted := expenseAccountNamed(t, h, f, "5230")
	created := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "FUEL", "name": "Fuel", "account_id": restricted,
			"input_vat_recoverable": false,
		})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create the fuel head: %s", readBody(t, created))
	}
	fuelID, _ := decodeJSON(t, created)["id"].(string)
	utilitiesID, _ := headNamed(t, h, f, "UTILITIES")["id"].(string)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15", "paid_from": "bank",
			"lines": []map[string]any{
				{"head_id": fuelID, "net_amount": "200.00", "tax_treatment": "standard"},
				{"head_id": utilitiesID, "net_amount": "800.00", "tax_treatment": "standard"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record a split expense: %s", readBody(t, resp))
	}
	out := decodeJSON(t, resp)

	// 200 × 15% = 30 absorbed; 800 × 15% = 120 reclaimed.
	if out["tax_recoverable"] != "120.00" {
		t.Errorf("recoverable = %v, want 120.00 — the utilities half only",
			out["tax_recoverable"])
	}
	if out["tax_absorbed"] != "30.00" {
		t.Errorf("absorbed = %v, want 30.00 — the fuel half only",
			out["tax_absorbed"])
	}
	if out["total_inclusive"] != "1150.00" {
		t.Errorf("total = %v, want 1150.00", out["total_inclusive"])
	}

	lines, _ := out["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("%d lines came back, want 2", len(lines))
	}
	for _, raw := range lines {
		l, _ := raw.(map[string]any)
		// The charge is what the expense account was debited: net plus whatever
		// could not be reclaimed. It is the figure a "where is my money going"
		// report sums, so it has to be on the line rather than recomputed.
		switch l["head"] {
		case "Fuel":
			if l["charge_amount"] != "230.00" {
				t.Errorf("fuel was charged %v, want 230.00 (200 + 30 absorbed)",
					l["charge_amount"])
			}
		case "Utilities":
			if l["charge_amount"] != "800.00" {
				t.Errorf("utilities was charged %v, want 800.00 — its VAT is "+
					"reclaimed, so it is not a cost", l["charge_amount"])
			}
		}
	}
}

// A zero-rated supplier charges no VAT, so there is none to split either way.
func TestAZeroRatedExpenseHasNoVATToReclaimOrAbsorb(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	headID, _ := headNamed(t, h, f, "RENT")["id"].(string)

	out := recordExpense(t, h, f, headID, "500.00", "zero_rated")

	if out["tax_total"] != "0.00" {
		t.Errorf("tax = %v on a zero-rated expense, want none", out["tax_total"])
	}
	if out["total_inclusive"] != "500.00" {
		t.Errorf("total = %v, want 500.00", out["total_inclusive"])
	}
}

// The recoverability flag can be corrected, and correcting it must not rewrite
// what was already claimed.
//
// A shop that had fuel marked recoverable and learns otherwise will change the
// head. The VAT return for the quarter already filed cannot change with it —
// which is why the split is stored on the line rather than derived from the
// flag when somebody asks.
func TestChangingAHeadDoesNotRewriteWhatWasAlreadyClaimed(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	account := expenseAccountNamed(t, h, f, "5230")
	created := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "FUEL2", "name": "Fuel", "account_id": account,
			"input_vat_recoverable": true,
		})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create the head: %s", readBody(t, created))
	}
	headID, _ := decodeJSON(t, created)["id"].(string)

	before := recordExpense(t, h, f, headID, "100.00", "standard")
	if before["tax_recoverable"] != "15.00" {
		t.Fatalf("the first expense claimed %v, want 15.00", before["tax_recoverable"])
	}

	// The shop learns fuel is restricted.
	fixed := h.do(t, http.MethodPut,
		f.path("/api/v1/expenses/heads/"+headID), f.token,
		map[string]any{
			"code": "FUEL2", "name": "Fuel", "account_id": account,
			"input_vat_recoverable": false,
		})
	if fixed.StatusCode != http.StatusOK {
		t.Fatalf("correct the head: %s", readBody(t, fixed))
	}

	// The one already recorded still says what it claimed.
	id, _ := before["id"].(string)
	reread := h.do(t, http.MethodGet, f.path("/api/v1/expenses/"+id), f.token, nil)
	if reread.StatusCode != http.StatusOK {
		t.Fatalf("re-read: %s", readBody(t, reread))
	}
	if got := decodeJSON(t, reread)["tax_recoverable"]; got != "15.00" {
		t.Errorf("the recorded expense now claims %v; correcting a head "+
			"rewrote a VAT return that had already been filed", got)
	}

	// And the next one absorbs.
	after := recordExpense(t, h, f, headID, "100.00", "standard")
	if after["tax_absorbed"] != "15.00" {
		t.Errorf("the expense recorded AFTER the correction absorbed %v, want "+
			"15.00 — the change has to take effect on new ones", after["tax_absorbed"])
	}
}

// A retried receipt is recorded once.
func TestARetriedExpenseIsRecordedOnce(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	headID, _ := headNamed(t, h, f, "RENT")["id"].(string)

	body := map[string]any{
		"uuid": newUUID(), "expense_date": "2026-08-15", "paid_from": "cash",
		"lines": []map[string]any{{
			"head_id": headID, "net_amount": "1000.00", "tax_treatment": "standard",
		}},
	}

	rentBefore := balanceOfAccount(t, h, f, "5200")

	first := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first: %s", readBody(t, first))
	}
	number, _ := decodeJSON(t, first)["expense_no"].(string)

	second := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("the retry returned %d, want 200: %s",
			second.StatusCode, readBody(t, second))
	}
	replay := decodeJSON(t, second)
	if again, _ := replay["expense_no"].(string); again != number {
		t.Errorf("the retry issued %q, not the original %q", again, number)
	}
	if taken, _ := replay["already_recorded"].(bool); !taken {
		t.Error("the retry did not say the expense had already been recorded")
	}

	if got := balanceOfAccount(t, h, f, "5200").Sub(rentBefore); !got.Equal(dec("1000")) {
		t.Errorf("Rent was debited %s across a receipt and its retry, want "+
			"1000: the electricity bill was paid into the books twice", got)
	}
}

// A head must post to an expense account, of this company, that can be posted
// to. Three conditions a foreign key cannot express, each of which produces a
// journal entry that balances and is wrong.
func TestAHeadCannotPointAtAnAccountThatIsNotAnExpense(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	var cashID string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT id::text FROM account WHERE company_id = $1 AND code = '1100'`,
			f.companyID).Scan(&cashID)
	}); err != nil {
		t.Fatalf("find Cash: %v", err)
	}

	resp := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "BADHEAD", "name": "Points at Cash", "account_id": cashID,
			"input_vat_recoverable": true,
		})
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("an expense category was pointed at Cash (status %d). Every "+
			"expense booked to it would credit and debit the same drawer.",
			resp.StatusCode)
	}
}

// A category belonging to another business cannot be booked to.
func TestAnExpenseCannotBeBookedToAnotherCompanysCategory(t *testing.T) {
	h := newHarness(t)
	mine := seedExpenses(t, h)
	theirs := seedExpenses(t, h)

	otherHead, _ := headNamed(t, h, theirs, "RENT")["id"].(string)

	resp := h.do(t, http.MethodPost, mine.path("/api/v1/expenses"), mine.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15", "paid_from": "cash",
			"lines": []map[string]any{{
				"head_id": otherHead, "net_amount": "100.00",
				"tax_treatment": "standard",
			}},
		})
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("an expense was booked to another business's category "+
			"(status %d)", resp.StatusCode)
	}
}

// A retired category takes nothing new.
func TestARetiredCategoryRefusesNewExpenses(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	headID, _ := headNamed(t, h, f, "MARKETING")["id"].(string)

	retired := h.do(t, http.MethodPost,
		f.path("/api/v1/expenses/heads/"+headID+"/active"), f.token,
		map[string]any{"active": false})
	if retired.StatusCode != http.StatusNoContent {
		t.Fatalf("retire: %s", readBody(t, retired))
	}

	resp := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15", "paid_from": "cash",
			"lines": []map[string]any{{
				"head_id": headID, "net_amount": "100.00", "tax_treatment": "standard",
			}},
		})
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("an expense was booked to a retired category (status %d)",
			resp.StatusCode)
	}
}

// The books still balance, and the trial balance still ties.
//
// The gate every posting in this product has to pass: an entry that balances in
// isolation is not enough, because a rule can balance and still put money in
// the wrong place.
func TestExpensesLeaveTheTrialBalanceBalanced(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	rentID, _ := headNamed(t, h, f, "RENT")["id"].(string)
	recordExpense(t, h, f, rentID, "1000.00", "standard")

	account := expenseAccountNamed(t, h, f, "5230")
	created := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "FUEL3", "name": "Fuel", "account_id": account,
			"input_vat_recoverable": false,
		})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create the head: %s", readBody(t, created))
	}
	fuelID, _ := decodeJSON(t, created)["id"].(string)
	recordExpense(t, h, f, fuelID, "300.00", "standard")

	var debits, credits decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(l.base_debit), 0), coalesce(sum(l.base_credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE e.company_id = $1`, f.companyID).Scan(&debits, &credits)
	}); err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !debits.Equal(credits) {
		t.Errorf("debits %s against credits %s after two expenses", debits, credits)
	}
}

// "Where is my money going", which is the sentence blueprint C3 opens with.
func TestTheBreakdownSaysWhereTheMoneyWent(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	rentID, _ := headNamed(t, h, f, "RENT")["id"].(string)
	utilID, _ := headNamed(t, h, f, "UTILITIES")["id"].(string)
	recordExpense(t, h, f, rentID, "750.00", "standard")
	recordExpense(t, h, f, utilID, "250.00", "standard")

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/expenses")+"&from=2026-08-01&to=2026-08-31", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %s", readBody(t, resp))
	}
	out := decodeJSON(t, resp)

	if n, _ := out["count"].(float64); int(n) != 2 {
		t.Errorf("count = %v, want 2", out["count"])
	}
	byHead, _ := out["by_head"].([]any)
	if len(byHead) != 2 {
		t.Fatalf("%d categories in the breakdown, want 2", len(byHead))
	}

	// Ordered by what was spent, so the biggest is first. An owner asking where
	// the money went should not have to sort it themselves.
	first, _ := byHead[0].(map[string]any)
	if first["head"] != "Rent" {
		t.Errorf("the breakdown leads with %v, want Rent — the largest",
			first["head"])
	}
	if first["amount"] != "750.00" {
		t.Errorf("Rent = %v, want 750.00", first["amount"])
	}
	// 750 of 1000. The share is against what the CATEGORIES came to, not the
	// gross: the gross includes recoverable VAT, which is not a cost and
	// belongs to no category, so dividing by it would make the column not
	// reach 100.
	if first["share"] != "75" {
		t.Errorf("Rent's share = %v%%, want 75", first["share"])
	}
}

// A closed period refuses the expense and leaves nothing behind.
func TestAnExpenseInAClosedPeriodIsRefusedWhole(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	headID, _ := headNamed(t, h, f, "RENT")["id"].(string)

	ctx := context.Background()
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE fiscal_period SET state = 'closed'
			WHERE company_id = $1 AND fiscal_year = 2026 AND period_no = 8`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("close the period: %v", err)
	}

	docUUID := newUUID()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token,
		map[string]any{
			"uuid": docUUID, "expense_date": "2026-08-15", "paid_from": "cash",
			"lines": []map[string]any{{
				"head_id": headID, "net_amount": "100.00", "tax_treatment": "standard",
			}},
		})
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("an expense posted into a closed period (status %d)",
			resp.StatusCode)
	}

	// And no half-written document survived it.
	var n int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM expense WHERE company_id = $1 AND uuid = $2`,
			f.companyID, docUUID).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d expense rows survived a refused posting; the document and "+
			"its journal entry have to stand or fall together", n)
	}
}

var _ = uuid.Nil

// The VAT return counts an expense's RECOVERABLE tax and no more.
//
// Both halves of this matter and they fail in opposite directions.
//
// The ledger side reads the Input VAT account, which an expense now debits. If
// the document side counted only supplier bills, every return in a period where
// somebody paid the electricity would report a difference and refuse to file —
// a correct return blocked by an incomplete reconciliation.
//
// And if the document side counted an expense's tax_TOTAL instead of its
// recoverable part, a restricted category would reclaim by the side door
// exactly the VAT E2.3 says it may not.
func TestTheVATReturnReconcilesExpensesAndReclaimsOnlyWhatItMay(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	rentID, _ := headNamed(t, h, f, "RENT")["id"].(string)
	recordExpense(t, h, f, rentID, "1000.00", "standard") // 150 reclaimable

	account := expenseAccountNamed(t, h, f, "5230")
	created := h.do(t, http.MethodPost, f.path("/api/v1/expenses/heads"), f.token,
		map[string]any{
			"code": "FUEL4", "name": "Fuel", "account_id": account,
			"input_vat_recoverable": false,
		})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create the head: %s", readBody(t, created))
	}
	fuelID, _ := decodeJSON(t, created)["id"].(string)
	recordExpense(t, h, f, fuelID, "400.00", "standard") // 60, absorbed

	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/vat-return?company_id="+f.companyID.String()+
			"&from=2026-08-01&to=2026-08-31", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vat return: %s", readBody(t, resp))
	}
	out := decodeJSON(t, resp)

	if got := out["input_tax_total"]; got != "150.00" {
		t.Errorf("the return reclaims %v of input tax, want 150.00: the rent's "+
			"VAT and none of the fuel's", got)
	}
	if got := out["input_difference"]; got != "0.00" {
		t.Errorf("the input reconciliation is out by %v. The ledger counts an "+
			"expense's input VAT, so the document side has to count expenses "+
			"too — or every return where somebody paid the electricity refuses "+
			"to file.", got)
	}
	if reconciled, _ := out["reconciled"].(bool); !reconciled {
		t.Errorf("the return did not reconcile: %v", out["outstanding"])
	}
}
