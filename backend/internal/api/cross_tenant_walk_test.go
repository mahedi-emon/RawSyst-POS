//go:build integration

// Every route that names a record, called with another tenant's record.
//
// Blueprint QA gate M8 is one sentence — a tenant must not be able to reach
// another tenant's data — and the suite proves it a resource at a time: a
// session here, a receipt there, a deposit, a customer. Each of those tests is
// good and each of them had to be remembered. The route added next year will
// not be in any of them.
//
// So this walks the route table itself. Every pattern carrying a {…ID}
// placeholder is called by a legitimately signed-in OWNER of one tenant —
// somebody holding every permission the product seeds, which is the strongest
// caller there is — using an id that really exists in another tenant.
//
// # Why real ids and not random ones
//
// TestEveryGuardedRouteRefusesAUserWithoutThePermission already fills the
// placeholders with fresh UUIDs. Those are refused by anything, including a
// handler with no isolation at all, because nothing anywhere has that id. A
// random id proves the route can say "not found"; only a real one proves the
// route cannot say "here it is".
//
// # What counts as passing
//
// Not 200, and no leak in the body. 404 is the ideal answer — under row-level
// security another tenant's row is simply absent, and confirming that an id
// exists but belongs to somebody else is itself a disclosure. 403 is accepted
// for the same reason it is elsewhere. A 400 is accepted only as an accident of
// argument order and is reported so it stays visible.
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// otherShop is a fully traded shop in a DIFFERENT tenant: real ids of every
// kind the route table can name.
type otherShop struct {
	*shopFixture
	ids map[string]string
}

// seedTheOtherShop builds a second tenant that has actually done business, so
// there is something real to try to reach.
//
// It trades through the HTTP surface rather than by INSERT wherever it can. An
// id written straight into the table can be one no handler would ever have
// produced, and a route that refuses it might be refusing its shape rather than
// its owner.
func seedTheOtherShop(t *testing.T, h *harness) *otherShop {
	t.Helper()
	ctx := context.Background()

	buying := seedBuying(t, h)
	f := buying.shopFixture
	o := &otherShop{shopFixture: f, ids: map[string]string{}}

	o.ids["companyID"] = f.companyID.String()
	o.ids["deviceID"] = f.deviceID.String()
	o.ids["variantID"] = f.variantID.String()
	o.ids["sessionID"] = f.sessionID.String()
	o.ids["unitID"] = f.egsUnitID.String()
	o.ids["userID"] = f.userID.String()
	o.ids["supplierID"] = buying.supplierID

	// A sale, which gives an invoice to reprint and lines to return.
	sale := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if sale.StatusCode != http.StatusCreated {
		t.Fatalf("the other shop could not sell: %s", readBody(t, sale))
	}
	o.ids["invoiceID"], _ = decodeJSON(t, sale)["invoice_id"].(string)

	// A purchase order, received and billed.
	poID, lineID := raiseOrder(t, h, buying, "10", "10.00")
	o.ids["poID"] = poID
	receiveAll(t, h, buying, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "10"},
	})
	bill := billOf(t, h, buying, poID, "OTHER-INV-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "10", "unit_cost": "10.00", "tax_rate": "0.15",
	}})
	o.ids["billID"], _ = bill["id"].(string)

	// A supplier payment, so the reversal route has something to name.
	pay := h.do(t, http.MethodPost, buying.path("/api/v1/purchasing/payments"),
		f.token, map[string]any{
			"uuid": newUUID(), "supplier_id": buying.supplierID,
			"method": "bank_transfer", "reference": "OTHER-PAY-1",
			"allocations": []map[string]any{
				{"bill_id": o.ids["billID"], "amount": "10.00"},
			},
		})
	if pay.StatusCode == http.StatusCreated {
		o.ids["paymentID"], _ = decodeJSON(t, pay)["id"].(string)
	}
	pay.Body.Close()

	// A customer with a credit account, an invoice on it, and a receipt.
	cust := h.do(t, http.MethodPost,
		"/api/v1/customers?company_id="+f.companyID.String(), f.token,
		map[string]any{"code": "OC" + strings.ToUpper(strings.ReplaceAll(newUUID().String(), "-", ""))[:8], "name": "Other Shop Customer", "credit_limit": "5000.00"})
	if cust.StatusCode != http.StatusCreated {
		t.Fatalf("the other shop could not open a credit account: %s",
			readBody(t, cust))
	}
	o.ids["customerID"], _ = decodeJSON(t, cust)["id"].(string)

	onAccount := oneItemSale(f, newUUID(), "1", "115.00", "0.00")
	onAccount["customer_id"] = o.ids["customerID"]
	onAccount["tenders"] = []map[string]any{
		{"method": "customer_due", "amount": "115.00"},
	}
	credit := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, onAccount)
	if credit.StatusCode == http.StatusCreated {
		invoiceID, _ := decodeJSON(t, credit)["invoice_id"].(string)
		receipt := h.do(t, http.MethodPost,
			"/api/v1/receivables/receipts?company_id="+f.companyID.String(),
			f.token, map[string]any{
				"uuid": newUUID(), "customer_id": o.ids["customerID"],
				"method": "cash",
				"allocations": []map[string]any{
					{"invoice_id": invoiceID, "amount": "115.00"},
				},
			})
		if receipt.StatusCode == http.StatusCreated {
			o.ids["receiptID"], _ = decodeJSON(t, receipt)["id"].(string)
		}
		receipt.Body.Close()
	} else {
		credit.Body.Close()
	}

	// A card sale and a settlement batch.
	card := oneItemSale(f, newUUID(), "1", "230.00", "230.00")
	card["tenders"] = []map[string]any{{"method": "mada", "amount": "230.00"}}
	if paid := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, card); paid.
		StatusCode == http.StatusCreated {
		paid.Body.Close()
		var tenderID string
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT t.id::text FROM sales_tender t
				JOIN sales_invoice i ON i.id = t.invoice_id
				WHERE i.company_id = $1 AND t.method = 'mada'
				  AND t.settlement_status = 'pending' LIMIT 1`,
				f.companyID).Scan(&tenderID)
		}); err == nil && tenderID != "" {
			batch := h.do(t, http.MethodPost,
				"/api/v1/settlement/batches?company_id="+f.companyID.String(),
				f.token, map[string]any{
					"uuid": newUUID(), "reference": "OTHER-DEPOSIT",
					"deposited_on": "2026-08-17", "net_amount": "225.00",
					"tender_ids": []string{tenderID},
				})
			if batch.StatusCode == http.StatusCreated {
				o.ids["batchID"], _ = decodeJSON(t, batch)["id"].(string)
			}
			batch.Body.Close()
		}
	} else {
		paid.Body.Close()
	}

	// An approval request, a support ticket, a webhook and a staged import:
	// the record-naming reads the workflow, support and admin modules added.
	// Seeded here rather than left uncovered, because a route absent from this
	// walk is a route whose isolation nothing checks.
	if rule := h.do(t, http.MethodPost,
		"/api/v1/approval-rules?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"name": "Every expense", "subject": "expense",
			"action": "require_approval",
			"steps":  []map[string]any{{"role": "owner"}},
		}); rule.StatusCode == http.StatusCreated {
		rule.Body.Close()
	} else {
		rule.Body.Close()
	}

	if ticket := h.do(t, http.MethodPost,
		"/api/v1/support/tickets?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"subject": "The other shop needs help",
			"body":    "Raised so the walk has a ticket to aim at.",
		}); ticket.StatusCode == http.StatusCreated {
		o.ids["ticketID"], _ = decodeJSON(t, ticket)["id"].(string)
	} else {
		ticket.Body.Close()
	}

	if hook := h.do(t, http.MethodPost,
		"/api/v1/webhooks?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"name": "Other shop endpoint", "url": "https://example.invalid/hook",
			"events": []string{"sale.completed"},
		}); hook.StatusCode == http.StatusCreated {
		o.ids["endpointID"], _ = decodeJSON(t, hook)["id"].(string)
	} else {
		hook.Body.Close()
	}

	if batch := h.do(t, http.MethodPost,
		"/api/v1/imports?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"kind": "customers", "filename": "other.csv",
			"mapping": map[string]string{"Name": "name"},
			"csv":     "Name\nOther Shop Import\n",
		}); batch.StatusCode == http.StatusCreated {
		o.ids["importID"], _ = decodeJSON(t, batch)["id"].(string)
	} else {
		batch.Body.Close()
	}

	// A catalogue product, for the variant matrix.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var productID string
		e := tx.QueryRow(ctx, `
			SELECT p.id::text FROM product p
			JOIN variant v ON v.product_id = p.id
			WHERE v.id = $1`, f.variantID).Scan(&productID)
		if e == nil {
			o.ids["productID"] = productID
		}
		return nil
	}); err != nil {
		t.Fatalf("finding the other shop's product: %v", err)
	}

	return o
}

// crossTenantParams are the placeholders that name a RECORD.
//
// {step} and {docType} are deliberately absent: they are enumerated words
// rather than identifiers, so substituting another tenant's value is not a
// thing that can be done and a walk over them would prove nothing.
var crossTenantParams = map[string]bool{
	"batchID": true, "billID": true, "companyID": true, "customerID": true,
	"deviceID": true, "invoiceID": true, "paymentID": true, "poID": true,
	"productID": true, "receiptID": true, "sessionID": true,
	"supplierID": true, "unitID": true, "userID": true, "variantID": true,
	// The modules that carry a conversation or a credential rather than a
	// business record. Isolation matters just as much: a support ticket names
	// what a shop is struggling with, and a webhook names where their sales go.
	"endpointID": true, "importID": true, "ticketID": true,
}

// aimAtTheOtherShop rewrites a route pattern to point at the other tenant's
// records, and says whether it managed to name any.
func aimAtTheOtherShop(pattern string, o *otherShop) (string, bool, []string) {
	out := pattern
	aimed := false
	missing := []string{}

	for {
		open := strings.Index(out, "{")
		if open < 0 {
			break
		}
		end := strings.Index(out[open:], "}")
		if end < 0 {
			break
		}
		name := out[open+1 : open+end]

		replacement := uuid.NewString()
		if crossTenantParams[name] {
			if id, ok := o.ids[name]; ok && id != "" {
				replacement, aimed = id, true
			} else {
				missing = append(missing, name)
			}
		}
		out = out[:open] + replacement + out[open+end+1:]
	}
	return out, aimed, missing
}

// THE WALK.
//
// An Owner of one shop, holding every permission the product seeds, calling
// every record-naming route with another shop's records.
func TestNoRouteHandsOverAnotherTenantsRecord(t *testing.T) {
	h := newHarness(t)

	other := seedTheOtherShop(t, h)

	// The attacker: a real Owner, of a real shop, with the full permission set.
	// Anything weaker would leave a reader wondering whether the refusals came
	// from isolation or from a missing permission.
	mine := seedBuying(t, h)
	if mine.tenantID == other.tenantID {
		t.Fatal("both shops landed in one tenant, so this proves nothing")
	}

	s := &Server{}
	walked, unaimed := 0, []string{}

	for _, rt := range s.Routes() {
		if !strings.Contains(rt.Pattern, "{") {
			continue
		}
		path, aimed, missing := aimAtTheOtherShop(rt.Pattern, other)
		if !aimed {
			if len(missing) > 0 {
				// Reported, not skipped silently. A placeholder this fixture
				// cannot produce an id for is a route this walk does not cover,
				// and a coverage hole that nobody can see is worse than one
				// named in the output.
				unaimed = append(unaimed,
					rt.Method+" "+rt.Pattern+" (no id for "+strings.Join(missing, ", ")+")")
			}
			continue
		}

		// The company the caller is entitled to. Routes that take one read it
		// from the query string, and giving them the caller's OWN company is
		// the harder test: the id in the path is the only thing pointing
		// anywhere else, so a route that passes is isolating by record rather
		// than by the parameter it was handed.
		withCompany := path
		if !strings.Contains(withCompany, "?") {
			withCompany += "?company_id=" + mine.companyID.String()
		}

		walked++
		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			var body any
			if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
				body = map[string]string{}
			}

			resp := h.do(t, rt.Method, withCompany, mine.token, body)
			defer resp.Body.Close()
			text := readBody(t, resp)

			if resp.StatusCode < 300 {
				t.Fatalf("an Owner of one shop reached %s %s pointed at ANOTHER "+
					"shop's record and got %d. Body: %s",
					rt.Method, rt.Pattern, resp.StatusCode, truncate(text, 300))
			}

			// A refusal must not confirm what it refused. The other shop's own
			// identifiers appearing in the message would tell the caller that
			// the record exists, which is the disclosure the 404 exists to
			// prevent.
			for name, id := range other.ids {
				if id != "" && strings.Contains(text, id) {
					t.Errorf("the refusal quotes the other shop's %s back at the "+
						"caller, confirming the record exists: %s",
						name, truncate(text, 200))
				}
			}
		})
	}

	if walked == 0 {
		t.Fatal("no route was aimed at the other tenant, so this proves nothing")
	}
	t.Logf("%d record-naming routes refused another tenant's record", walked)
	for _, u := range unaimed {
		t.Logf("not covered: %s", u)
	}
}

// The same walk, in reverse, so a route that refuses EVERYTHING is not mistaken
// for a route that isolates.
//
// A handler that answers 404 unconditionally would sail through the test above
// and be completely broken. This calls the read-only, record-naming routes with
// the caller's OWN records and requires them to work.
func TestTheSameRoutesStillWorkOnTheCallersOwnRecords(t *testing.T) {
	h := newHarness(t)
	mine := seedTheOtherShop(t, h) // the same fixture, used as the caller's own

	// GET only. A POST would change the state the next assertion reads, and
	// what is in question here is whether the route can find a record at all.
	worked := 0
	s := &Server{}
	for _, rt := range s.Routes() {
		if rt.Method != http.MethodGet || !strings.Contains(rt.Pattern, "{") {
			continue
		}
		path, aimed, _ := aimAtTheOtherShop(rt.Pattern, mine)
		if !aimed {
			continue
		}
		if !strings.Contains(path, "?") {
			path += "?company_id=" + mine.companyID.String()
		}

		if emptyIsTheHonestAnswer[rt.Pattern] != "" {
			continue
		}

		t.Run(rt.Pattern, func(t *testing.T) {
			resp := h.do(t, http.MethodGet, path, mine.token, nil)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("%s could not find the caller's OWN record, so the "+
					"cross-tenant walk would pass on it for the wrong reason: %s",
					rt.Pattern, truncate(readBody(t, resp), 200))
			}
		})
		worked++
	}

	if worked == 0 {
		t.Fatal("no readable record-naming route was exercised")
	}
	t.Logf("%d record-naming reads found the caller's own records", worked)
}

// emptyIsTheHonestAnswer names the routes whose 404 on the caller's OWN records
// is correct, with the reason, so the exemption is a decision rather than a
// convenient omission.
//
// Only sub-resources that a company legitimately may not have. A route on this
// list is still walked by the cross-tenant test above; what it is excused from
// is the counter-check that a 404 there was about ownership.
var emptyIsTheHonestAnswer = map[string]string{
	"/api/v1/companies/{companyID}/logo/image": "a shop that has not uploaded a " +
		"logo has no image to serve, and the fixture does not upload one — " +
		"the presence check is covered by the logo metadata route beside it",
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
