//go:build integration

// F4 group consolidation, D6 documents, D7 global search and D2 analytics.
//
// Four modules that between them carry 24 services and 19 routes and had
// isolation-only coverage — tests proving one tenant cannot read another's
// rows, and nothing proving the rows can be read at all. For a reporting
// surface that is the weaker half: a query naming a column that does not exist
// isolates perfectly and answers 500 to everybody equally. That is precisely
// how the batches route shipped broken.
//
// So these exercise each surface for real: create, read back, and check the
// scoping and the permission.
package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- F4, group consolidation ---------------------------------------------

// A group can be created, read back, and given a member.
//
// F4's core: several companies reported as one. Nothing had ever driven the
// write path.
func TestAGroupCanBeCreatedAndGivenAMember(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	created := h.do(t, http.MethodPost,
		"/api/v1/groups?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"name": "Test Holdings", "presentation_currency": "SAR",
		})
	defer created.Body.Close()
	if created.StatusCode != http.StatusOK && created.StatusCode != http.StatusCreated {
		t.Fatalf("create group: %d %s", created.StatusCode, readBody(t, created))
	}
	group, _ := decodeJSON(t, created)["group"].(map[string]any)
	if group == nil {
		t.Fatal("the response carries no group")
	}
	groupID, _ := group["id"].(string)
	if groupID == "" {
		t.Fatal("the created group has no id")
	}

	member := h.do(t, http.MethodPost,
		"/api/v1/groups/"+groupID+"/members?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"company_id": f.companyID.String(), "ownership_pct": "100",
			"is_parent": true,
		})
	member.Body.Close()
	if member.StatusCode != http.StatusOK && member.StatusCode != http.StatusCreated {
		t.Fatalf("add member: %d", member.StatusCode)
	}

	read := h.do(t, http.MethodGet,
		"/api/v1/groups/"+groupID+"?company_id="+f.companyID.String(),
		f.token, nil)
	defer read.Body.Close()
	if read.StatusCode != http.StatusOK {
		t.Fatalf("read group: %d %s", read.StatusCode, readBody(t, read))
	}
}

// A group's consolidated statement and intercompany view both answer.
//
// The two reads F4 exists for. Both aggregate across member companies, which
// is where a broken join would hide.
//
// The window is the current month because membership is dated: a company joins
// a group on a day, and consolidating a period before it joined correctly finds
// nothing to consolidate.
func TestAGroupStatementAndIntercompanyViewAnswer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	groupID := newGroupWithMember(t, h, f)

	today := time.Now().UTC()
	window := fmt.Sprintf("?from=%s&to=%s&company_id=%s",
		today.Format("2006-01-02"),
		today.AddDate(0, 0, 1).Format("2006-01-02"), f.companyID)

	for _, path := range []string{"/statement", "/intercompany"} {
		resp := h.do(t, http.MethodGet,
			"/api/v1/groups/"+groupID+path+window, f.token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d: %s", path, resp.StatusCode,
				readBody(t, resp))
		}
		resp.Body.Close()
	}
}

// One tenant's group is not another's.
func TestAGroupIsNotVisibleToAnotherTenant(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	groupID := newGroupWithMember(t, h, mine)

	resp := h.do(t, http.MethodGet,
		"/api/v1/groups/"+groupID+"?company_id="+theirs.companyID.String(),
		theirs.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("one tenant read another's group")
	}
}

// Managing a group takes more than reading one.
func TestOnlyAGroupManagerMayCreateAGroup(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost,
		"/api/v1/groups?company_id="+f.companyID.String(), f.token,
		map[string]any{"name": "Nope", "presentation_currency": "SAR"})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Error("a cashier created a company group")
	}
}

// newGroupWithMember builds a one-company group and returns its id.
func newGroupWithMember(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/groups?company_id="+f.companyID.String(), f.token,
		map[string]any{"name": "Group", "presentation_currency": "SAR"})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group: %d %s", resp.StatusCode, readBody(t, resp))
	}
	group, _ := decodeJSON(t, resp)["group"].(map[string]any)
	resp.Body.Close()
	id, _ := group["id"].(string)
	if id == "" {
		t.Fatal("the created group has no id")
	}

	m := h.do(t, http.MethodPost,
		"/api/v1/groups/"+id+"/members?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"company_id": f.companyID.String(), "ownership_pct": "100",
			"is_parent": true,
		})
	m.Body.Close()
	return id
}

// --- D6, documents --------------------------------------------------------

// The document register lists, and takes a permission to write.
func TestTheDocumentRegisterListsAndIsPermissioned(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	list := h.do(t, http.MethodGet,
		"/api/v1/documents?company_id="+f.companyID.String(), f.token, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list documents: %d %s", list.StatusCode, readBody(t, list))
	}

	cashier := h.seedShop(t, "cashier")
	denied := h.do(t, http.MethodPost,
		"/api/v1/documents?company_id="+cashier.companyID.String(),
		cashier.token, map[string]any{"title": "Lease"})
	defer denied.Body.Close()
	if denied.StatusCode == http.StatusOK || denied.StatusCode == http.StatusCreated {
		t.Error("a cashier filed a company document")
	}
}

// One tenant's documents are not another's.
//
// Cross-tenant reads are row-level security's job rather than the handler's, so
// the register may answer 200 for a company id it cannot see. What must never
// happen is rows coming back: an empty answer is confinement working, a
// populated one is a breach.
func TestDocumentsAreScopedToTheirCompany(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/documents?company_id="+theirs.companyID.String(),
		mine.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return // refused outright, which is the stronger answer
	}
	body, _ := decodeJSON(t, resp)["documents"].([]any)
	if len(body) != 0 {
		t.Errorf("one tenant listed %d of another's documents", len(body))
	}
}

// --- D7, global search ----------------------------------------------------

// Search finds a product by name.
//
// D7 is a lens over what the caller can already reach. The fixture's product
// is called Abaya, so searching for it must return something.
func TestGlobalSearchFindsAProduct(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/search?q=Abaya&company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !strings.Contains(strings.ToLower(body), "abaya") {
		t.Errorf("searching for the fixture's own product found nothing: %s",
			body)
	}
}

// A search that matches nothing answers empty rather than failing.
func TestGlobalSearchForNothingAnswersEmpty(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/search?q=zzqqxxnotathing&company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a search matching nothing answered %d", resp.StatusCode)
	}
}

// Search does not reach across a tenant boundary.
func TestGlobalSearchDoesNotCrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/search?q=Abaya&company_id="+theirs.companyID.String(),
		mine.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		body := readBody(t, resp)
		if strings.Contains(strings.ToLower(body), "abaya") {
			t.Error("search returned another tenant's catalogue")
		}
	}
}

// --- D2, analytics --------------------------------------------------------

// Every analytics read answers for a shop with a sale on the books.
//
// Four aggregations over the same period. They had been touched only by the
// studio test, which does not assert they run against real movement.
func TestTheAnalyticsReadsAnswerForAShopWithSales(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed a sale: %d", resp.StatusCode)
	}

	window := "&from=2026-08-01&to=2026-08-31&company_id=" + f.companyID.String()
	for _, path := range []string{
		"/api/v1/analytics/kpis", "/api/v1/analytics/movers",
		"/api/v1/analytics/forecast", "/api/v1/analytics/profitability",
	} {
		read := h.do(t, http.MethodGet, path+"?"+strings.TrimPrefix(window, "&"),
			f.token, nil)
		if read.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d: %s", path, read.StatusCode,
				readBody(t, read))
		}
		read.Body.Close()
	}
}

// Analytics takes the reporting permission.
func TestAnalyticsNeedsTheReportingPermission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodGet,
		"/api/v1/analytics/kpis?from=2026-08-01&to=2026-08-31&company_id="+
			f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a cashier read the analytics dashboard")
	}
}

// Marking an entry takes it out of the consolidated statement (F4).
//
// F4 says inter-company transactions are "tracked and eliminated in
// consolidation". Elimination was built and tracking had no way in: `POST
// /groups/intercompany` was live and uncalled, so nothing was ever marked,
// nothing was ever eliminated, and a group that invoiced itself reported the
// internal sale as group revenue — a plausible-looking figure it did not earn.
//
// This asserts the property the feature exists for, rather than that the route
// answers: the count of eliminated entries rises when an entry is marked, and
// falls again when it is unmarked.
func TestMarkingAnEntryEliminatesItFromTheGroupStatement(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	groupID := newGroupWithMember(t, h, f)

	// A SECOND company in the same group, because a company cannot trade with
	// itself: `intercompany_not_self` refuses it, correctly. The whole idea is
	// a sale from one entity in the group to another.
	var sister uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1, 'Sister Co', 'sa', 'SAR') RETURNING id`,
			f.tenantID).Scan(&sister)
	}); err != nil {
		t.Fatalf("create the sister company: %v", err)
	}
	joined := h.do(t, http.MethodPost,
		"/api/v1/groups/"+groupID+"/members?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"company_id": sister.String(), "ownership_pct": "100",
		})
	if joined.StatusCode != http.StatusOK && joined.StatusCode != http.StatusCreated {
		t.Fatalf("add the sister to the group: %d %s",
			joined.StatusCode, readBody(t, joined))
	}
	joined.Body.Close()

	debit, credit := twoAccounts(t, h, f)
	posted := postJournal(t, h, f,
		journalBody(debit, credit, "500.00", "500.00", "A sale to our sister company."))
	if posted.StatusCode != http.StatusOK && posted.StatusCode != http.StatusCreated {
		t.Fatalf("post a journal: %d %s", posted.StatusCode, readBody(t, posted))
	}
	// The LEDGER entry, not the manual journal's own id. `intercompany_entry`
	// references the former, and the two are different uuids on the same
	// response -- which is exactly why the form picks from the register rather
	// than asking somebody to copy one across.
	entryID, _ := decodeJSONFrom(t, posted)["journal_entry_id"].(string)
	if entryID == "" {
		t.Fatal("the posted journal has no ledger entry id to mark")
	}

	company := "?company_id=" + f.companyID.String()
	window := "&from=2026-01-01&to=2026-12-31&statement=profit_and_loss"

	eliminated := func() float64 {
		resp := h.do(t, http.MethodGet,
			"/api/v1/groups/"+groupID+"/statement"+company+window, f.token, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("group statement: %d %s", resp.StatusCode, readBody(t, resp))
		}
		body := decodeJSON(t, resp)
		wrapped, _ := body["statement"].(map[string]any)
		if wrapped == nil {
			t.Fatalf("the response carries no statement: %v", body)
		}
		n, ok := wrapped["eliminated_entries"].(float64)
		if !ok {
			t.Fatalf("the statement carries no eliminated_entries: %v", wrapped)
		}
		return n
	}

	before := eliminated()

	mark := h.do(t, http.MethodPost, "/api/v1/groups/intercompany"+company, f.token,
		map[string]any{
			"entry_id":        entryID,
			"counterparty_id": sister.String(),
			"kind":            "sale",
			"note":            "Sold to our sister company.",
		})
	if mark.StatusCode != http.StatusOK && mark.StatusCode != http.StatusNoContent {
		t.Fatalf("mark the entry: %d %s", mark.StatusCode, readBody(t, mark))
	}
	mark.Body.Close()

	if after := eliminated(); after != before+1 {
		t.Errorf("eliminated entries went %v -> %v, want one more; a marked "+
			"entry that is still counted is group revenue the group did not earn",
			before, after)
	}

	// It appears in the list of what was left out, which is what makes the
	// difference explainable to whoever reads the statement.
	listed := h.do(t, http.MethodGet,
		"/api/v1/groups/"+groupID+"/intercompany"+company+"&from=2026-01-01&to=2026-12-31",
		f.token, nil)
	body := readBody(t, listed)
	listed.Body.Close()
	if !containsText(body, entryID) {
		t.Errorf("the marked entry is not in the intercompany list: %s", body)
	}

	// Unmarking is the same route, and puts the figure back.
	unmark := h.do(t, http.MethodPost, "/api/v1/groups/intercompany"+company, f.token,
		map[string]any{"entry_id": entryID, "unmark": true})
	if unmark.StatusCode != http.StatusOK && unmark.StatusCode != http.StatusNoContent {
		t.Fatalf("unmark: %d %s", unmark.StatusCode, readBody(t, unmark))
	}
	unmark.Body.Close()

	if after := eliminated(); after != before {
		t.Errorf("after unmarking, eliminated entries is %v, want %v back", after, before)
	}
}

// A cashier cannot decide what leaves the group's figures.
func TestOnlyAGroupManagerMayMarkAnEntryIntercompany(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")

	resp := h.do(t, http.MethodPost,
		"/api/v1/groups/intercompany?company_id="+f.companyID.String(), cashier,
		map[string]any{
			"entry_id":        newUUID().String(),
			"counterparty_id": f.companyID.String(),
			"kind":            "sale",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier marking an entry answered %d, want 403", resp.StatusCode)
	}
}
