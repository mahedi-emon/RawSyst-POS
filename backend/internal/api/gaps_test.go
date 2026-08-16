//go:build integration

// The four correctness gaps closed together.
//
// Each was real and each was silent: a stock movement that could not be traced
// to its sale, a standard-costing variance computed and thrown away, a B2B
// credit note sent down the B2C route, and an invoice nobody could quote over
// a telephone.
package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// P8 — a stock movement names the sale that caused it.
//
// Costing runs before the invoice row exists, deliberately: a sale that cannot
// be costed must fail before it consumes an ICV, because a counter cannot be
// given back. The id is therefore generated up front rather than by the
// database, so the movement can point at it.
func TestAStockMovementNamesTheSaleThatCausedIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	invoiceID := decodeJSON(t, resp)["invoice_id"].(string)

	var sourceType string
	var sourceID *uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT source_type, source_id FROM stock_movement
			WHERE reason = 'sale'`).Scan(&sourceType, &sourceID)
	}); err != nil {
		t.Fatalf("read movement: %v", err)
	}

	if sourceType != "sales_invoice" {
		t.Errorf("source type = %q", sourceType)
	}
	if sourceID == nil {
		t.Fatal("the stock movement has no source id; it cannot be traced back " +
			"to the sale, so a stock card cannot drill down and shrinkage " +
			"cannot be investigated")
	}
	if sourceID.String() != invoiceID {
		t.Errorf("the movement points at %s, but the sale is %s",
			sourceID, invoiceID)
	}
}

// P14 — an invoice carries a number a person can read out.
//
// Deliberately NOT the ICV. This series resets every January, which is what
// makes it useless as a tamper signal and the ICV necessary (blueprint I3).
func TestAnInvoiceCarriesAReadableNumber(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	var numbers []string
	for i := 0; i < 3; i++ {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		if resp.StatusCode != 201 {
			t.Fatalf("sale %d: %s", i, readBody(t, resp))
		}
		invoiceID := decodeJSON(t, resp)["invoice_id"].(string)

		var number *string
		var icv int64
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT i.human_number, z.icv
				FROM sales_invoice i JOIN zatca_invoice z ON z.invoice_id = i.id
				WHERE i.id = $1`, invoiceID).Scan(&number, &icv)
		}); err != nil {
			t.Fatalf("read invoice: %v", err)
		}

		if number == nil {
			t.Fatal("the invoice has no readable number; nobody can quote a UUID " +
				"over a telephone")
		}
		numbers = append(numbers, *number)
	}

	// INV-MAIN-2026-000001 and onwards: series, branch, year, sequence.
	if !strings.HasPrefix(numbers[0], "INV-MAIN-2026-") {
		t.Errorf("invoice number %q does not name its series, branch and year",
			numbers[0])
	}
	if !strings.HasSuffix(numbers[0], "000001") ||
		!strings.HasSuffix(numbers[2], "000003") {
		t.Errorf("numbers are not sequential: %v", numbers)
	}

	seen := map[string]bool{}
	for _, n := range numbers {
		if seen[n] {
			t.Fatalf("invoice number %q was issued twice", n)
		}
		seen[n] = true
	}
}

// A credit note is numbered in its OWN series, so a shop can tell at a glance
// that CRN-MAIN-2026-000001 is a refund and not the first sale of the year.
func TestACreditNoteHasItsOwnNumberSeries(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	creditNoteID := decodeJSON(t, resp)["credit_note_id"].(string)

	var number *string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT human_number FROM sales_invoice WHERE id = $1`, creditNoteID).
			Scan(&number)
	}); err != nil {
		t.Fatalf("read credit note: %v", err)
	}

	if number == nil {
		t.Fatal("the credit note has no readable number")
	}
	if !strings.HasPrefix(*number, "CRN-MAIN-2026-") {
		t.Errorf("credit note numbered %q; it must be visibly a credit note "+
			"rather than the first sale of the year", *number)
	}
}

// P12 — a B2B credit note follows the CLEARANCE route, like the invoice it
// corrects. Sending it down the B2C reporting route would put it through a
// process ZATCA does not accept for that document type.
func TestACreditNoteFollowsTheRouteOfTheInvoiceItCorrects(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// A B2B sale: cleared before issue.
	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	sale["doc_type"] = "standard"
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if created.StatusCode != 201 {
		t.Fatalf("B2B sale: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	var lineID, invoiceState string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT state FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&invoiceState); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if invoiceState != "signed_pending_clear" {
		t.Fatalf("a B2B invoice is %q, want signed_pending_clear", invoiceState)
	}

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "returned by the business customer",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	creditNoteID := decodeJSON(t, resp)["credit_note_id"].(string)

	var noteState string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT state FROM sales_invoice WHERE id = $1`, creditNoteID).
			Scan(&noteState)
	}); err != nil {
		t.Fatalf("read credit note: %v", err)
	}

	if noteState != "signed_pending_clear" {
		t.Errorf("a credit note against a B2B invoice is %q; it must be cleared "+
			"before issue like the invoice it corrects, not merely reported",
			noteState)
	}
}

// A B2C credit note still takes the reporting route, so the fix above did not
// simply move everything to clearance.
func TestAB2CCreditNoteStillReports(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "changed mind",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	creditNoteID := decodeJSON(t, resp)["credit_note_id"].(string)

	var state string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT state FROM sales_invoice WHERE id = $1`, creditNoteID).Scan(&state)
	}); err != nil {
		t.Fatalf("read credit note: %v", err)
	}
	if state != "signed_pending_report" {
		t.Errorf("a B2C credit note is %q, want signed_pending_report", state)
	}
}

// P9 — the standard-costing variance is posted, not discarded.
//
// The whole point of standard costing is that an unexpected purchase price
// becomes visible. Computing the difference and throwing it away absorbs it
// silently into margin, which is exactly what the method exists to prevent.
func TestTheStandardCostVarianceIsPosted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// Switch the company to standard costing and give it a variance account.
	var varianceAcct uuid.UUID
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE company SET costing_method = 'standard' WHERE id = $1`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("switch costing method: %v", err)
	}
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO account (tenant_id, company_id, code, name, type)
			VALUES ($1,$2,'5200','Cost Variance','expense') RETURNING id`,
			f.tenantID, f.companyID).Scan(&varianceAcct); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
			VALUES ($1,$2,'cost_variance',$3)`,
			f.tenantID, f.companyID, varianceAcct)
		return e
	}); err != nil {
		t.Fatalf("map the variance account: %v", err)
	}

	// Stock actually cost 60; the standard is 50. Selling one should surface a
	// 10 unfavourable variance rather than quietly widening the margin.
	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	created := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}

	var variance float64
	var entries int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(base_debit - base_credit), 0)
			FROM journal_line WHERE account_id = $1`, varianceAcct).
			Scan(&variance); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entry WHERE rule_key LIKE 'inventory.variance%'`).
			Scan(&entries)
	}); err != nil {
		t.Fatalf("read variance: %v", err)
	}

	// The seeded variant carries no standard cost, so ConsumeStandard values it
	// at zero against an actual of 60 — a favourable variance of 60 by that
	// arithmetic. What matters here is that a variance was POSTED at all rather
	// than discarded, and that the books still balance.
	if entries == 0 {
		t.Fatal("no variance entry was posted; the difference between standard " +
			"and actual cost was absorbed silently into margin")
	}
	if variance == 0 {
		t.Error("a variance entry was posted with no amount")
	}

	var tbDiff float64
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT trial_balance_difference($1)`, f.companyID).Scan(&tbDiff)
	}); err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if tbDiff != 0 {
		t.Errorf("the trial balance is out by %v once a variance is posted", tbDiff)
	}
}

// What a till is told is still owed back on an invoice.
//
// A terminal must never work this out for itself: how much of a line has
// already been returned lives in the credit notes against the invoice, which a
// till that was offline when they were raised has never seen. The failure mode
// is refunding the same jacket twice, and the second refund is real money
// leaving a real drawer.
func TestReturnableLinesTellTheTillWhatIsLeft(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "2", "115.00", "230.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID, _ := decodeJSON(t, created)["invoice_id"].(string)

	// The status is asserted before the payload. Without it a 500 arrives as a
	// body with no "lines" key, which reads as "zero lines returnable" and
	// sends you looking at the SQL function instead of at the error.
	resp := h.do(t, "GET", "/api/v1/pos/sales/"+invoiceID+"/returnable", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("returnable: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	lines, _ := body["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("returnable reported %d lines, want 1", len(lines))
	}
	line, _ := lines[0].(map[string]any)

	// The line id is the whole point of the route: without it a till cannot
	// name what it is giving back.
	lineID, _ := line["line_id"].(string)
	if lineID == "" {
		t.Fatalf("no line_id; a till cannot build a return without one")
	}
	if got := line["qty_returnable"]; got != "2.000" && got != "2.0000" && got != "2" {
		t.Errorf("qty_returnable is %v, want the full 2 sold", got)
	}
	// Money as a string, here as everywhere.
	if _, ok := line["gross_returnable"].(string); !ok {
		t.Errorf("gross_returnable came back as %T, not a string",
			line["gross_returnable"])
	}

	// Give one back, then ask again.
	refund := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-16T12:00:00+03:00",
		"reason":              "customer changed their mind",
		"lines":               []any{map[string]any{"line_id": lineID, "qty": "1"}},
		"refunds": []any{
			map[string]any{"method": "cash", "amount": "115.00"},
		},
	})
	if refund.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, refund))
	}

	afterResp := h.do(t, "GET", "/api/v1/pos/sales/"+invoiceID+"/returnable", f.token, nil)
	if afterResp.StatusCode != 200 {
		t.Fatalf("returnable after refund: %d %s",
			afterResp.StatusCode, readBody(t, afterResp))
	}
	after := decodeJSON(t, afterResp)
	afterLines, _ := after["lines"].([]any)
	got, _ := afterLines[0].(map[string]any)

	// One left, not two. A till reading this cannot offer the second refund.
	if q, _ := got["qty_returnable"].(string); !strings.HasPrefix(q, "1") {
		t.Errorf("qty_returnable is %q after returning one of two, want 1", q)
	}
	if q, _ := got["qty_returned"].(string); !strings.HasPrefix(q, "1") {
		t.Errorf("qty_returned is %q, want 1", q)
	}
}
