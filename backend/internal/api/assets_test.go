//go:build integration

// Fixed assets (C7) and investors (C3.2), derived from the blueprint.
//
//	C7: "automated monthly straight-line depreciation with journal postings to
//	 the general ledger" and "Asset Disposal/Scrap: record sale or write-off
//	 with automatic gain/loss-on-disposal calculation."
//
//	C3.2: "Investment activity is kept fully separate from normal revenue in the
//	 accounting model — never mixed with sales income, so P&L stays clean."
//
// The last of those is the one worth a test that would be embarrassing to fail.
// A product that let a capital injection reach a revenue account would flatter
// turnover, margin and growth at once, and the error is very hard to unpick
// afterwards because it looks like a good year.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

type assetFixture struct {
	*shopFixture
	token  string
	cashID uuid.UUID
}

func seedAssets(t *testing.T, h *harness) *assetFixture {
	t.Helper()
	f := h.seedShop(t, "owner")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := provisioning.SeedChartOfAccounts(
			t.Context(), tx, f.tenantID, f.companyID); e != nil {
			return e
		}
		// The whole year. The shop fixture carries one period — the August it
		// trades in — and depreciation and disposals here happen in February
		// and March, which nothing could post into.
		_, e := tx.Exec(t.Context(), `SELECT open_fiscal_year($1, 2026)`, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("seed the chart and the calendar: %v", err)
	}

	out := &assetFixture{shopFixture: f, token: f.token}
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO money_account
			  (tenant_id, company_id, account_id, kind, name, currency)
			SELECT $1, $2, a.id, 'cash', a.name, c.base_currency
			FROM account_role_map r
			JOIN account a ON a.id = r.account_id
			JOIN company c ON c.id = a.company_id
			WHERE r.company_id = $2 AND r.role = 'cash'
			ON CONFLICT (account_id) DO UPDATE SET name = excluded.name
			RETURNING id`, f.tenantID, f.companyID).Scan(&out.cashID)
	}); err != nil {
		t.Fatalf("seed a cash account: %v", err)
	}
	return out
}

func (f *assetFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// --- C7: depreciation -----------------------------------------------------

// Straight line, and the arithmetic said out loud.
//
// A van at 60,000 with a 12,000 residual over 48 months is 1,000 a month. One
// run charges one month: Dr Depreciation, Cr Accumulated Depreciation.
func TestDepreciationChargesOneMonthStraightLine(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	f.addAsset(t, h, "Delivery van", "60000.00", "12000.00", 48, "2026-01-10")

	resp := h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("depreciate: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["total"] != "1000.00" {
		t.Errorf("60000 less 12000 over 48 months is 1000 a month, not %v",
			body["total"])
	}
	if got := f.balance(t, h, "5600"); !got.Equal(decimal.RequireFromString("1000")) {
		t.Errorf("Depreciation should hold 1000, not %s", got.StringFixed(2))
	}
	// The contra account, not the asset itself: a balance sheet showing cost
	// AND accumulated depreciation says what the shop paid and how much life is
	// left, and netting them throws the first away.
	if got := f.balance(t, h, "1590"); !got.Equal(decimal.RequireFromString("-1000")) {
		t.Errorf("Accumulated Depreciation should be credited 1000, not %s",
			got.StringFixed(2))
	}
	if got := f.balance(t, h, "1500"); !got.IsZero() {
		t.Errorf("the Fixed Assets account itself must not move on a "+
			"depreciation run: %s", got.StringFixed(2))
	}
}

// A run that happened twice would halve the asset's remaining life with nothing
// looking wrong — found when the van reaches zero a year early.
func TestDepreciatingTheSameMonthTwiceChargesItOnce(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	f.addAsset(t, h, "Delivery van", "60000.00", "12000.00", 48, "2026-01-10")

	h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})
	second := h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("a second run should be harmless, got %d — %s",
			second.StatusCode, readBody(t, second))
	}
	if got := decodeJSON(t, second)["assets_charged"]; got != float64(0) {
		t.Errorf("the second run charged %v assets; it should charge none", got)
	}
	if got := f.balance(t, h, "5600"); !got.Equal(decimal.RequireFromString("1000")) {
		t.Fatalf("Depreciation holds %s after running February twice. A "+
			"doubled charge halves the asset's life and is found when it "+
			"reaches zero a year early.", got.StringFixed(2))
	}
}

// An asset bought in March is not depreciated in February.
func TestAnAssetIsNotDepreciatedBeforeItWasOwned(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	f.addAsset(t, h, "Laptop", "6000.00", "0.00", 36, "2026-03-01")

	resp := h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("depreciate: %s", readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["assets_charged"]; got != float64(0) {
		t.Errorf("an asset bought in March was depreciated in February")
	}
}

// --- C7: disposal ---------------------------------------------------------

// "automatic gain/loss-on-disposal calculation"
//
// The van cost 60,000, has had 1,000 depreciated, and sells for 50,000. Book
// value is 59,000, so the shop lost 9,000 on it. Nobody types 9,000: it is
// derived, for the same reason cost of goods sold comes from the costing engine
// rather than from the till.
func TestDisposingBelowBookValuePostsTheLoss(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	assetID := f.addAsset(t, h, "Delivery van", "60000.00", "12000.00", 48, "2026-01-10")
	h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/assets/"+assetID+"/dispose"), f.token,
		map[string]any{
			"proceeds": "50000.00", "money_account_id": f.cashID.String(),
			"disposed_on": "2026-03-01", "note": "Sold to a courier firm",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispose: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["book_value"] != "59000.00" {
		t.Errorf("60000 less 1000 depreciated is a book value of 59000, not %v",
			body["book_value"])
	}
	if body["result"] != "-9000.00" {
		t.Errorf("selling a 59000 asset for 50000 is a 9000 loss, not %v",
			body["result"])
	}
	if got := f.balance(t, h, "5700"); !got.Equal(decimal.RequireFromString("9000")) {
		t.Errorf("Loss on Disposal should hold 9000, not %s", got.StringFixed(2))
	}
	// The whole asset is cleared out. Writing down the asset account alone
	// would leave the accumulated depreciation behind for ever.
	if got := f.balance(t, h, "1590"); !got.IsZero() {
		t.Errorf("Accumulated Depreciation should be cleared on disposal, "+
			"not %s", got.StringFixed(2))
	}
}

// The other direction, on the same arithmetic.
func TestDisposingAboveBookValuePostsTheGain(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	assetID := f.addAsset(t, h, "Delivery van", "60000.00", "12000.00", 48, "2026-01-10")
	h.do(t, http.MethodPost, f.path("/api/v1/assets/depreciate"),
		f.token, map[string]any{"month": "2026-02-15"})

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/assets/"+assetID+"/dispose"), f.token,
		map[string]any{
			"proceeds": "62000.00", "money_account_id": f.cashID.String(),
			"disposed_on": "2026-03-01",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispose: %s", readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["result"]; got != "3000.00" {
		t.Errorf("selling a 59000 asset for 62000 is a 3000 gain, not %v", got)
	}
	// Gain on Disposal, and NOT Sales Revenue: selling a van is not trading,
	// and folding it into turnover would overstate what the business does.
	if got := f.balance(t, h, "4900"); !got.Equal(decimal.RequireFromString("-3000")) {
		t.Errorf("Gain on Disposal should be credited 3000, not %s",
			got.StringFixed(2))
	}
	if got := f.balance(t, h, "4100"); !got.IsZero() {
		t.Errorf("Sales Revenue moved on an asset disposal: %s. Selling a van "+
			"is not turnover.", got.StringFixed(2))
	}
}

func TestAnAssetCannotBeDisposedTwice(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	assetID := f.addAsset(t, h, "Laptop", "6000.00", "0.00", 36, "2026-01-10")

	h.do(t, http.MethodPost, f.path("/api/v1/assets/"+assetID+"/dispose"),
		f.token, map[string]any{"proceeds": "0", "disposed_on": "2026-03-01"})
	second := h.do(t, http.MethodPost,
		f.path("/api/v1/assets/"+assetID+"/dispose"), f.token,
		map[string]any{"proceeds": "0", "disposed_on": "2026-03-01"})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("disposing twice should conflict, got %d — %s",
			second.StatusCode, readBody(t, second))
	}
}

// --- C3.2: investors ------------------------------------------------------

// The test that would be embarrassing to fail.
//
//	"Investment activity is kept fully separate from normal revenue ... never
//	 mixed with sales income, so P&L stays clean."
func TestCapitalNeverReachesTheProfitAndLoss(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	investorID := f.addInvestor(t, h, "Mahedi", "owner")

	beforeRevenue := f.balance(t, h, "4100")
	// The fixture's own opening-stock entry has already credited Cash, so the
	// claim is about what the contribution MOVES rather than what Cash holds.
	beforeCash := f.balance(t, h, "1100")

	resp := h.do(t, http.MethodPost, f.path("/api/v1/investors/movements"),
		f.token, map[string]any{
			"uuid": newUUID(), "investor_id": investorID,
			"direction": "contribution", "amount": "100000.00",
			"moved_on": "2026-02-01", "money_account_id": f.cashID.String(),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record a contribution: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}

	if got := f.balance(t, h, "4100").Sub(beforeRevenue); !got.IsZero() {
		t.Fatalf("a capital injection moved Sales Revenue by %s. It would "+
			"flatter turnover, margin and growth at once, and it looks like a "+
			"good year rather than like an error.", got.StringFixed(2))
	}
	if got := f.balance(t, h, "3100"); !got.Equal(decimal.RequireFromString("-100000")) {
		t.Errorf("Owner Capital should be credited the whole 100000, not %s",
			got.StringFixed(2))
	}
	if got := f.balance(t, h, "1100").Sub(beforeCash); !got.Equal(
		decimal.RequireFromString("100000")) {
		t.Errorf("Cash should rise by the whole 100000, not %s", got.StringFixed(2))
	}
}

// A withdrawal is the mirror, and also never touches the P&L.
func TestAWithdrawalReducesCapitalAndNotProfit(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	investorID := f.addInvestor(t, h, "Mahedi", "owner")

	f.move(t, h, investorID, "contribution", "100000.00")
	f.move(t, h, investorID, "withdrawal", "30000.00")

	if got := f.balance(t, h, "3100"); !got.Equal(decimal.RequireFromString("-70000")) {
		t.Errorf("Owner Capital should be down to 70000, not %s", got.StringFixed(2))
	}
	for _, code := range []string{"4100", "5100"} {
		if got := f.balance(t, h, code); !got.IsZero() {
			t.Errorf("%s moved on a capital withdrawal: %s", code, got.StringFixed(2))
		}
	}
}

// "each investor's proportional share"
func TestEachInvestorsShareIsOfWhatTheyPutIn(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	one := f.addInvestor(t, h, "Mahedi", "owner")
	two := f.addInvestor(t, h, "A partner", "investor")

	f.move(t, h, one, "contribution", "75000.00")
	f.move(t, h, two, "contribution", "25000.00")

	resp := h.do(t, http.MethodGet, f.path("/api/v1/investors"), f.token, nil)
	rows, _ := decodeJSON(t, resp)["data"].([]any)

	shares := map[string]any{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		shares[row["name"].(string)] = row["share_of_capital"]
	}
	if shares["Mahedi"] != "75.00" || shares["A partner"] != "25.00" {
		t.Errorf("75000 and 25000 is a 75/25 split, not %v and %v",
			shares["Mahedi"], shares["A partner"])
	}
}

// "each investor can (if given access) see only their own contribution/return
// history."
func TestAnInvestorCannotReadSomebodyElsesStatement(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)

	// Somebody who may VIEW the register and not manage it — an Auditor. An
	// Accountant holds `investor.manage`, which makes them staff running the
	// register, and staff may read anybody's statement; the confinement is
	// about the person who is only an investor.
	email, userID := h.newUserInTenant(t, f.tenantID, "auditor")
	mine := f.addInvestorFor(t, h, "A partner", userID)
	// And one linked to somebody else's login — the owner's.
	theirs := f.addInvestorFor(t, h, "The owner", f.userID)
	token := h.login(t, email)

	own := h.do(t, http.MethodGet,
		f.path("/api/v1/investors/"+mine+"/statement"), token, nil)
	if own.StatusCode != http.StatusOK {
		t.Fatalf("an investor should read their own statement, got %d — %s",
			own.StatusCode, readBody(t, own))
	}

	refused := h.do(t, http.MethodGet,
		f.path("/api/v1/investors/"+theirs+"/statement"), token, nil)
	if refused.StatusCode != http.StatusForbidden {
		t.Fatalf("one person read another's investment history: %d — %s",
			refused.StatusCode, readBody(t, refused))
	}
}

// --- helpers --------------------------------------------------------------

func (f *assetFixture) addAsset(
	t *testing.T, h *harness, name, cost, residual string, life int, acquired string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/assets"), f.token,
		map[string]any{
			"name": name, "category": "vehicle", "cost": cost,
			"residual_value": residual, "useful_life_months": life,
			"acquired_on": acquired,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add asset: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

func (f *assetFixture) addInvestor(
	t *testing.T, h *harness, name, kind string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/investors"), f.token,
		map[string]any{"name": name, "kind": kind})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add investor: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

func (f *assetFixture) addInvestorFor(
	t *testing.T, h *harness, name string, userID uuid.UUID,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/investors"), f.token,
		map[string]any{"name": name, "kind": "investor",
			"user_id": userID.String()})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add investor: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

func (f *assetFixture) move(
	t *testing.T, h *harness, investorID, direction, amount string,
) {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/investors/movements"),
		f.token, map[string]any{
			"uuid": newUUID(), "investor_id": investorID,
			"direction": direction, "amount": amount,
			"moved_on": "2026-02-01", "money_account_id": f.cashID.String(),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s: status %d — %s", direction, resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

func (f *assetFixture) balance(
	t *testing.T, h *harness, code string,
) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
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
