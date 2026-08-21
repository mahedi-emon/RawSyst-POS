//go:build integration

// The POS surface, end to end over real HTTP.
//
// These are the tests that matter most for this layer, because everything
// behind it was already proved: what is new here is the boundary. A sale
// arriving as JSON must produce the same atomic result as a sale driven
// directly through the service — and, more importantly, a till must not be able
// to widen its own authority by what it puts in the request body.
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
)

// shopFixture is a tenant that can actually trade: a company with a country and
// a currency, a store, a stock location, an onboarded terminal, a chart of
// accounts mapped to roles, an open period, and something on the shelf.
type shopFixture struct {
	tenantID    uuid.UUID
	companyID   uuid.UUID
	storeID     uuid.UUID
	warehouseID uuid.UUID
	egsUnitID   uuid.UUID
	deviceID    uuid.UUID
	variantID   uuid.UUID
	userID      uuid.UUID
	sessionID   uuid.UUID
	token       string
	// email is carried so a test can sign in as this user through the real
	// login route rather than only with a minted token.
	email string
}

// seedShop is a shop mid-shift: everything seedShopBeforeOpening builds, plus
// the open cash session a till needs before it can sell.
//
// Split from the seeding itself so a test can exercise the real HTTP open. The
// session here is opened in-process on purpose — a fixture that went through
// the router would make every POS test depend on the shift routes, and the
// point of the split is that one test proves that path rather than all of them
// assuming it.
func (h *harness) seedShop(t *testing.T, roleKey string) *shopFixture {
	t.Helper()
	f := h.seedShopBeforeOpening(t, roleKey)

	// A till cannot sell without an open session: there would be no drawer
	// anyone had counted into, and a cash difference found later could not be
	// attributed to anybody.
	session, err := h.shift.Open(t.Context(), f.tenantID, f.deviceID, f.userID,
		decimal.RequireFromString("200.00"), true)
	if err != nil {
		t.Fatalf("open till session: %v", err)
	}
	f.sessionID = session.ID
	return f
}

// seedShopBeforeOpening is the same shop with its drawer not yet counted, which
// is the state a till is in when the first cashier of the day signs in.
func (h *harness) seedShopBeforeOpening(t *testing.T, roleKey string) *shopFixture {
	t.Helper()
	ctx := t.Context()

	email := h.seedUserWithRole(t, roleKey)

	f := &shopFixture{email: email}
	err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT id, tenant_id FROM app_user WHERE email = $1`, email).
			Scan(&f.userID, &f.tenantID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,'POS Test Co','sa','SAR') RETURNING id`,
			f.tenantID).Scan(&f.companyID)
	})
	if err != nil {
		t.Fatalf("seed company: %v", err)
	}

	err = h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'MAIN','Main') RETURNING id`,
			f.tenantID, f.companyID).Scan(&f.storeID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,'WH1','Shop Floor') RETURNING id`,
			f.tenantID, f.companyID, f.storeID).Scan(&f.warehouseID); e != nil {
			return e
		}

		if e := tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1,$2,$3,'till-1','smart_pos') RETURNING id`,
			f.tenantID, f.companyID, f.storeID).Scan(&f.egsUnitID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO device
			  (tenant_id, company_id, store_id, terminal_label, status, egs_unit_id)
			VALUES ($1,$2,$3,'Till 1','active',$4) RETURNING id`,
			f.tenantID, f.companyID, f.storeID, f.egsUnitID).Scan(&f.deviceID); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,8,'2026-08-01','2026-08-31')`,
			f.tenantID, f.companyID); e != nil {
			return e
		}

		// Chart of accounts. Inventory must be a CONTROL account or the stock
		// valuation has nothing to tie back to.
		accounts := []struct{ code, name, kind, role string }{
			{"1100", "Cash", "asset", "cash"},
			{"1150", "Card Settlement Clearing", "asset", "card_clearing"},
			// The offsetting half of an exchange. A liability, because during
			// one it holds what the shop owes for goods already taken back but
			// not yet swapped; zero between exchanges.
			{"2350", "Exchange Clearing", "liability", "exchange_clearing"},
			{"4100", "Sales Revenue", "revenue", "sales_revenue"},
			{"2200", "Output VAT Payable", "liability", "output_vat"},
			{"5100", "Cost of Goods Sold", "expense", "cogs"},
		}
		for _, a := range accounts {
			var id uuid.UUID
			if e := tx.QueryRow(ctx, `
				INSERT INTO account (tenant_id, company_id, code, name, type)
				VALUES ($1,$2,$3,$4,$5) RETURNING id`,
				f.tenantID, f.companyID, a.code, a.name, a.kind).Scan(&id); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
				VALUES ($1,$2,$3,$4)`, f.tenantID, f.companyID, a.role, id); e != nil {
				return e
			}
		}
		var inventoryAcct uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO account
			  (tenant_id, company_id, code, name, type, is_control, control_of)
			VALUES ($1,$2,'1400','Inventory','asset',true,'inventory') RETURNING id`,
			f.tenantID, f.companyID).Scan(&inventoryAcct); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
			VALUES ($1,$2,'inventory',$3)`,
			f.tenantID, f.companyID, inventoryAcct); e != nil {
			return e
		}

		var productID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
			VALUES ($1,$2,'ABAYA','Abaya','standard') RETURNING id`,
			f.tenantID, f.companyID).Scan(&productID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO variant (tenant_id, company_id, product_id, sku, price_retail)
			VALUES ($1,$2,$3,'ABAYA-BLK-L',115.00) RETURNING id`,
			f.tenantID, f.companyID, productID).Scan(&f.variantID); e != nil {
			return e
		}

		// Stock on the shelf, with the ledger side to match.
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,10,'opening',600)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO cost_layer
			  (tenant_id, company_id, variant_id, warehouse_id,
			   qty_received, qty_remaining, unit_cost)
			VALUES ($1,$2,$3,$4,10,10,60)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_valuation
			  (tenant_id, company_id, variant_id, warehouse_id, qty_on_hand, total_value)
			VALUES ($1,$2,$3,$4,10,600)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID); e != nil {
			return e
		}

		// The ledger side of that stock. Without it the Inventory account sits
		// at zero while the valuation holds 600, and the tie-out fails for a
		// reason that has nothing to do with the sale under test — stock that
		// arrived without an accounting entry.
		var periodID, entryID, inventoryID, cashID uuid.UUID
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
			`SELECT account_id FROM account_role_map
			 WHERE company_id = $1 AND role = 'cash'`, f.companyID).Scan(&cashID); e != nil {
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
		if _, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'SAR',600,0,600,0)`,
			f.tenantID, entryID, inventoryID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,2,$3,'SAR',0,600,0,600)`,
			f.tenantID, entryID, cashID)
		return e
	})
	if err != nil {
		t.Fatalf("seed shop: %v", err)
	}

	f.token = h.tokenForDevice(t, f)
	return f
}

// tokenForDevice mints an access token bound to the terminal, which is what a
// POS carries. A till's authority comes from the device in its token, never
// from what it puts in a request body.
func (h *harness) tokenForDevice(t *testing.T, f *shopFixture) string {
	t.Helper()
	token, _, err := h.tokens.IssueAccess(actor.Actor{
		UserID:   f.userID,
		TenantID: f.tenantID,
		DeviceID: f.deviceID,
	})
	if err != nil {
		t.Fatalf("issue device token: %v", err)
	}
	return token
}

// oneItemSale is the request body a till sends. Money is a STRING throughout.
func oneItemSale(f *shopFixture, invoiceUUID uuid.UUID, qty, price, paid string) map[string]any {
	return map[string]any{
		"invoice_uuid": invoiceUUID.String(),
		"doc_type":     "simplified",
		"issued_at":    "2026-08-15T10:30:00Z",
		"lines": []map[string]any{{
			"variant_id":    f.variantID.String(),
			"description":   "Abaya",
			"qty":           qty,
			"unit_price":    price,
			"tax_treatment": "standard",
		}},
		"tenders": []map[string]any{{"method": "cash", "amount": paid}},
	}
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// A cashier rings up a sale over HTTP and everything lands together.
func TestSaleOverHTTPWritesInvoiceChainAndJournal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, uuid.New(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	// VAT-inclusive back-calculation: 115.00 gross is 100.00 net plus 15.00.
	if got := body["subtotal_net"]; got != "100" {
		t.Errorf("subtotal_net = %v, want \"100\"", got)
	}
	if got := body["tax_total"]; got != "15" {
		t.Errorf("tax_total = %v, want \"15\"", got)
	}

	// Money crossed the wire as strings, not JSON numbers. A float64 cannot
	// hold every decimal, and a till that parsed one would drift from the
	// server's numeric.
	if _, ok := body["total_inclusive"].(string); !ok {
		t.Errorf("total_inclusive came back as %T, not a string", body["total_inclusive"])
	}

	// The chain position the till needs to sign locally.
	chain, ok := body["zatca"].(map[string]any)
	if !ok {
		t.Fatal("no chain position returned; the till cannot sign without one")
	}
	if chain["icv"] != float64(1) {
		t.Errorf("icv = %v, want 1", chain["icv"])
	}
	if chain["pih"] == "" || chain["pih"] == nil {
		t.Error("no previous invoice hash returned")
	}

	// Cost and margin must never reach a till.
	if _, leaked := body["cogs_total"]; leaked {
		t.Error("the response leaked cost of sale to the terminal")
	}

	invoiceID := body["invoice_id"].(string)
	assertBooksAfterOneSale(t, h, f, invoiceID)
}

func assertBooksAfterOneSale(t *testing.T, h *harness, f *shopFixture, invoiceID string) {
	t.Helper()
	ctx := t.Context()

	var entries, movements int
	var tbDiff, stockDiff float64
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entry WHERE source_id = $1`, invoiceID).
			Scan(&entries); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_movement WHERE reason = 'sale'`).
			Scan(&movements); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT trial_balance_difference($1)`, f.companyID).Scan(&tbDiff); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT inventory_gl_difference($1)`, f.companyID).Scan(&stockDiff)
	}); err != nil {
		t.Fatalf("read books: %v", err)
	}

	// Revenue and cost of sale are separate posting rules, so a sale that has
	// something to cost produces two entries.
	if entries != 2 {
		t.Errorf("%d journal entries for the sale, want 2 (revenue and COGS)", entries)
	}
	if movements != 1 {
		t.Errorf("%d stock movements, want 1", movements)
	}
	if tbDiff != 0 {
		t.Errorf("trial balance is out by %v after one sale", tbDiff)
	}
	if stockDiff != 0 {
		t.Errorf("stock valuation and the Inventory account are out by %v", stockDiff)
	}
}

// Pillar 3 at the transport layer. A till that lost the response retries with
// the same invoice id and must get the original sale back, not a second one.
func TestRetryingASaleOverHTTPRingsItOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	body := oneItemSale(f, uuid.New(), "1", "115.00", "115.00")

	first := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d: %s", first.StatusCode, readBody(t, first))
	}
	firstBody := decodeJSON(t, first)

	second := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200: %s", second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("the retry did not announce itself as a replay")
	}
	secondBody := decodeJSON(t, second)

	if firstBody["invoice_id"] != secondBody["invoice_id"] {
		t.Errorf("the retry produced a different invoice: %v then %v",
			firstBody["invoice_id"], secondBody["invoice_id"])
	}

	var invoices, movements int
	var counter int64
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT count(*) FROM sales_invoice`).Scan(&invoices); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(),
			`SELECT count(*) FROM stock_movement WHERE reason='sale'`).
			Scan(&movements); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT last_icv FROM egs_unit LIMIT 1`).Scan(&counter)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}

	if invoices != 1 {
		t.Errorf("%d invoices after a retry; the customer was charged twice", invoices)
	}
	if movements != 1 {
		t.Errorf("%d stock movements after a retry; stock went down twice", movements)
	}
	if counter != 1 {
		t.Errorf("the counter is at %d after a retry, want 1", counter)
	}
}

// The security property that matters most here: a till cannot widen its own
// authority through the request body. It never names its company, store or EGS
// unit, and a stock location it does name is checked against its own branch.
func TestATillCannotSellFromAnotherBranchesStock(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	body := oneItemSale(mine, uuid.New(), "1", "115.00", "115.00")
	body["warehouse_id"] = theirs.warehouseID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", mine.token, body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode, readBody(t, resp))
	}

	// Their stock is untouched.
	var onHand float64
	if err := h.pool.TxAsTenant(t.Context(), theirs.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT stock_on_hand($1,$2)`,
			theirs.variantID, theirs.warehouseID).Scan(&onHand)
	}); err != nil {
		t.Fatalf("read their stock: %v", err)
	}
	if onHand != 10 {
		t.Fatalf("their stock is %v, want the 10 it started with", onHand)
	}
}

// A session with no terminal behind it cannot issue an invoice. Without a
// device there is no EGS unit, no counter and no chain — so the document could
// not be a legal one.
func TestASaleWithoutATerminalIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// A plain user token: same person, same permissions, no device.
	token, _, err := h.tokens.IssueAccess(actor.Actor{
		UserID: f.userID, TenantID: f.tenantID,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", token,
		oneItemSale(f, uuid.New(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, readBody(t, resp))
	}
}

// Payment that does not settle the sale exactly must fail, and take the ICV
// with it: a counter consumed by a sale that did not happen leaves the gap
// ZATCA's tamper detection looks for.
func TestAnUnderpaidSaleOverHTTPConsumesNoCounter(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, uuid.New(), "1", "115.00", "114.99"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
	}

	var counter int64
	var invoices int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT count(*) FROM sales_invoice`).Scan(&invoices); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT last_icv FROM egs_unit LIMIT 1`).Scan(&counter)
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if counter != 0 {
		t.Errorf("the counter advanced to %d on a failed sale", counter)
	}
	if invoices != 0 {
		t.Errorf("%d invoices survived a failed sale", invoices)
	}
}

// Malformed money is refused at the boundary with a message naming the field,
// not a parse error from three layers down.
func TestMalformedAmountsAreRefusedWithAUsefulMessage(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	body := oneItemSale(f, uuid.New(), "1", "not a number", "115.00")
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
	}
	if msg := readBody(t, resp); !strings.Contains(msg, "unit_price") {
		t.Errorf("the refusal does not name the field: %s", msg)
	}
}

// Reading a sale back is what a receipt reprint needs.
func TestReadingASaleBack(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	created := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, uuid.New(), "2", "115.00", "230.00"))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	resp := h.do(t, http.MethodGet, "/api/v1/pos/sales/"+invoiceID, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["doc_type"] != "simplified" {
		t.Errorf("doc_type = %v", body["doc_type"])
	}
	if body["currency"] != "SAR" {
		t.Errorf("currency = %v, want SAR from the company", body["currency"])
	}
	lines, _ := body["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("%d lines returned, want 1", len(lines))
	}
	tenders, _ := body["tenders"].([]any)
	if len(tenders) != 1 {
		t.Fatalf("%d tenders returned, want 1", len(tenders))
	}
}

// One tenant cannot read another's invoice. It reads as absent rather than
// forbidden — a 403 would confirm the record exists, which leaks across the
// boundary.
func TestOneTenantCannotReadAnothersInvoice(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	created := h.do(t, http.MethodPost, "/api/v1/pos/sales", theirs.token,
		oneItemSale(theirs, uuid.New(), "1", "115.00", "115.00"))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("their sale: %s", readBody(t, created))
	}
	theirInvoice := decodeJSON(t, created)["invoice_id"].(string)

	resp := h.do(t, http.MethodGet, "/api/v1/pos/sales/"+theirInvoice, mine.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not 403, which would confirm it exists): %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// newUUID names what it is at the call site: a device-assigned document id.
func newUUID() uuid.UUID { return uuid.New() }
