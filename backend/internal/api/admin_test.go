//go:build integration

// Approvals (D5, F1), notifications (D3), integration (H6), migration and
// export (H7), backups (H4) and support (H10).
//
// The tests worth having here are the ones about a promise that would be easy
// to quietly break: a key that can be read twice, an import that half-lands, a
// backup that reports itself healthy because it ran, and an inbox that shows
// somebody else's notifications.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func adminPath(f *shopFixture, base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// --- API keys -------------------------------------------------------------

// The key is in the creation response and nowhere else, ever.
func TestAnAPIKeyIsReadableExactlyOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/api-keys"), f.token,
		map[string]any{
			"name":        "Accounting system",
			"permissions": []string{"accounting.view"},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	minted := decodeJSON(t, resp)

	secret, _ := minted["secret"].(string)
	if !strings.HasPrefix(secret, "rsk_live_") || len(secret) < 40 {
		t.Fatalf("the minted key does not look like a key: %q", secret)
	}

	list := h.do(t, http.MethodGet, adminPath(f, "/api/v1/api-keys"), f.token, nil)
	body := readBody(t, list)
	if strings.Contains(body, secret) {
		t.Fatal("the key list contains the key itself. A product that can " +
			"show a key twice is a product where the key is recoverable from " +
			"the database.")
	}
	prefix, _ := minted["key_prefix"].(string)
	if !strings.Contains(body, prefix) {
		t.Errorf("the list shows no prefix, so two keys cannot be told apart: %s",
			truncate(body, 200))
	}
}

// A key may carry a subset of what its creator holds, and nothing else.
func TestAKeyCannotOutrankThePersonWhoMintedIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/api-keys"), f.token,
		map[string]any{
			"name": "Over-reaching key",
			// The first is real and held; the second is a verb an Owner does
			// not have, because no role does.
			"permissions": []string{"accounting.view", "platform.everything"},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	granted, _ := decodeJSON(t, resp)["permissions"].([]any)
	for _, p := range granted {
		if p == "platform.everything" {
			t.Fatal("a key was minted with a permission its creator does not " +
				"hold, which makes an API key an escalation route")
		}
	}
	if len(granted) != 1 {
		t.Errorf("the key carries %d permissions, want the one that was held",
			len(granted))
	}
}

// A revoked key stops working, and there is no way back.
func TestRevokingAKeyIsFinal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	minted := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/api-keys"), f.token, map[string]any{
			"name": "Temporary", "permissions": []string{"accounting.view"},
		}))
	id, _ := minted["id"].(string)

	if resp := h.do(t, http.MethodDelete,
		adminPath(f, "/api/v1/api-keys/"+id), f.token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	// A second revoke is refused rather than silently succeeding, so a screen
	// cannot report an action it did not take.
	if resp := h.do(t, http.MethodDelete,
		adminPath(f, "/api/v1/api-keys/"+id), f.token, nil); resp.StatusCode == http.StatusNoContent {
		t.Error("revoking an already-revoked key reported success")
	}
}

// --- webhooks -------------------------------------------------------------

// Plain HTTP would put a shop's sales over the wire in clear, and there is no
// setting that allows it.
func TestAWebhookMustBeHTTPS(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/webhooks"), f.token,
		map[string]any{
			"name": "Insecure", "url": "http://example.invalid/hook",
			"events": []string{"sale.completed"},
		})
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a plain-HTTP webhook was accepted")
	}
}

// An endpoint subscribed to an event this product does not send would never
// fire, and the shop would find out weeks later.
func TestAWebhookCannotSubscribeToAnEventThatDoesNotExist(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/webhooks"), f.token,
		map[string]any{
			"name": "Typo", "url": "https://example.invalid/hook",
			"events": []string{"sale.complete"},
		})
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("an endpoint subscribed to a misspelled event was accepted, " +
			"and would silently never fire")
	}
}

// --- import ---------------------------------------------------------------

// H7's whole argument: a half-finished import is worse than none.
func TestAnImportStagesAndChecksBeforeItWritesAnything(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	before := customerCount(t, h, f)

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/imports"), f.token,
		map[string]any{
			"kind": "customers", "filename": "customers.csv",
			"mapping": map[string]string{"Name": "name", "Phone": "phone"},
			"csv": "Name,Phone\n" +
				"Layla Haddad,0500000001\n" +
				",0500000002\n" +
				"Omar Nasser,0500000003\n",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	batch := decodeJSON(t, resp)

	if batch["status"] != "validated" {
		t.Errorf("a staged file is %v, want validated", batch["status"])
	}
	if batch["valid_rows"] != float64(2) || batch["error_rows"] != float64(1) {
		t.Errorf("two good rows and one blank name should be 2 valid and 1 "+
			"in error, got %v and %v", batch["valid_rows"], batch["error_rows"])
	}
	// Nothing has moved yet. This is the preview step, and a shop must be able
	// to run it, read the errors and start again with their books untouched.
	if got := customerCount(t, h, f); got != before {
		t.Fatalf("staging a file created %d customers before anybody committed it",
			got-before)
	}

	id, _ := batch["id"].(string)
	committed := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports/"+id+"/commit"), f.token, nil)
	if committed.StatusCode != http.StatusOK {
		t.Fatalf("commit: status %d — %s",
			committed.StatusCode, readBody(t, committed))
	}
	done := decodeJSON(t, committed)
	if done["imported_rows"] != float64(2) {
		t.Errorf("%v rows were written, want the 2 that passed",
			done["imported_rows"])
	}
	if got := customerCount(t, h, f); got != before+2 {
		t.Errorf("%d customers were created, want 2", got-before)
	}
}

// The refused row keeps its reason, which is H7's Error Report.
func TestARefusedRowSaysWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	batch := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports"), f.token, map[string]any{
			"kind":    "customers",
			"mapping": map[string]string{"Name": "name", "Phone": "phone"},
			// A row that HAS fields and no name. A wholly blank line is
			// skipped rather than refused: a trailing newline is not an
			// error, and reporting one as a bad row would put a permanent
			// false failure on every file a spreadsheet exports.
			"csv": "Name,Phone\n,0500000002\nReal Customer,0500000003\n",
		}))
	rows, _ := batch["rows"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the batch came back with no rows: %v", batch)
	}
	// Refused rows first: the Error Report is why anybody opens this screen.
	first, _ := rows[0].(map[string]any)
	if first["status"] != "invalid" {
		t.Fatalf("the first row is %v, want the invalid one", first["status"])
	}
	if reason, _ := first["error"].(string); !strings.Contains(reason, "name") {
		t.Errorf("the reason does not name the field: %q", reason)
	}
}

// A file that names the same thing twice would import one and overwrite it
// with the other, leaving whichever happened to be last.
func TestAFileThatRepeatsItselfIsCaughtBeforeItIsWritten(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	batch := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports"), f.token, map[string]any{
			"kind":    "customers",
			"mapping": map[string]string{"Code": "code", "Name": "name"},
			"csv":     "Code,Name\nC1,First\nC1,Second\n",
		}))
	if batch["error_rows"] != float64(1) {
		t.Fatalf("a file naming C1 twice gave %v errors, want 1",
			batch["error_rows"])
	}
}

// Committing before checking would skip the step the whole design exists for.
func TestAnImportCannotBeWrittenWithoutBeingChecked(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	batch := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports"), f.token, map[string]any{
			"kind":    "customers",
			"mapping": map[string]string{"Name": "name", "Phone": "phone"},
			// A row that HAS fields and no name. A wholly blank line is
			// skipped rather than refused: a trailing newline is not an
			// error, and reporting one as a bad row would put a permanent
			// false failure on every file a spreadsheet exports.
			"csv": "Name,Phone\n,0500000002\nReal Customer,0500000003\n",
		}))
	id, _ := batch["id"].(string)

	first := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports/"+id+"/commit"), f.token, nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("commit: status %d — %s", first.StatusCode, readBody(t, first))
	}
	// And not twice. A repeated commit would duplicate every row.
	second := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/imports/"+id+"/commit"), f.token, nil)
	if second.StatusCode == http.StatusOK {
		t.Fatal("the same import was written twice, duplicating every row")
	}
}

// --- export ---------------------------------------------------------------

func TestAnExportIsACSVDownload(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/exports/products"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type is %q, want text/csv", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "products.csv") {
		t.Errorf("Content-Disposition is %q, want a filename", got)
	}

	body := readBody(t, resp)
	// The byte order mark, without which Excel on an Arabic Windows renders
	// every Arabic name as mojibake.
	if !strings.HasPrefix(body, "\ufeff") {
		t.Error("the export has no byte order mark, so Excel will misread " +
			"every non-Latin name in it")
	}
	if !strings.Contains(body, "sku") {
		t.Errorf("the export has no header row: %s", truncate(body, 120))
	}
}

// --- backups --------------------------------------------------------------

// H4: a backup that cannot be restored is not a backup, and the dashboard must
// not report one as protection.
func TestABackupThatRanIsNotABackupThatRestores(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	started := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/backups"), f.token, nil))
	id, _ := started["id"].(string)

	if resp := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/backups/"+id+"/finish"), f.token,
		map[string]any{"location": "s3://backups/1.dump", "size_bytes": 4096}); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	health := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/backups/health"), f.token, nil))
	if health["at_risk"] != true {
		t.Fatal("a backup nobody has verified was reported as protection. " +
			"H4 is explicit: a backup that cannot be restored is not one.")
	}

	// Verified, and only now is the shop actually covered.
	if resp := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/backups/"+id+"/verify"), f.token,
		map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	after := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/backups/health"), f.token, nil))
	if after["at_risk"] != false {
		t.Errorf("a verified backup still reads at risk: %v", after["summary"])
	}
}

// A verification that failed must never read as untested, and never as success.
func TestAFailedVerificationIsNotProtection(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	started := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/backups"), f.token, nil))
	id, _ := started["id"].(string)
	h.do(t, http.MethodPost, adminPath(f, "/api/v1/backups/"+id+"/finish"),
		f.token, map[string]any{"location": "s3://backups/2.dump"}).Body.Close()

	verified := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/backups/"+id+"/verify"), f.token,
		map[string]any{"error": "the dump would not load"}))
	if verified["verified_at"] != nil && verified["verified_at"] != "" {
		t.Fatal("a backup that failed to restore was stamped as verified")
	}
	if verified["verify_error"] == "" {
		t.Error("the failure was not recorded, so it reads as untested")
	}
}

// --- support --------------------------------------------------------------

// The status follows the conversation. A queue that needs maintaining by hand
// is a queue that is wrong within a week.
func TestReplyingToATicketPutsItBackOnSupport(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	raised := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/support/tickets"), f.token, map[string]any{
			"subject": "The till will not print",
			"body":    "It has not printed since this morning.",
		}))
	id, _ := raised["id"].(string)
	if raised["status"] != "open" {
		t.Errorf("a new ticket is %v, want open", raised["status"])
	}
	if no, _ := raised["ticket_no"].(string); !strings.HasPrefix(no, "TKT-") {
		t.Errorf("the ticket number reads %q", no)
	}

	replied := decodeJSON(t, h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/support/tickets/"+id+"/reply"), f.token,
		map[string]any{"body": "It is still not printing."}))
	if replied["status"] != "waiting_on_support" {
		t.Errorf("after the customer replied the ticket is %v, want "+
			"waiting_on_support", replied["status"])
	}
	messages, _ := replied["messages"].([]any)
	if len(messages) != 1 {
		t.Errorf("%d messages on the ticket, want the one reply", len(messages))
	}
}

// --- notifications --------------------------------------------------------

// Every trigger, not only the ones already stored: a screen showing three
// settings because three rows exist hides the eleven somebody needs.
func TestPreferencesOfferEveryTrigger(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet, "/api/v1/notifications/preferences",
		f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preferences: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	prefs, _ := decodeJSON(t, resp)["data"].([]any)
	if len(prefs) < 14 {
		t.Fatalf("only %d triggers are offered; D3 lists fourteen", len(prefs))
	}
}

// In-app cannot be switched off, whatever the request says. The centre is
// where a shop discovers why a submission failed.
func TestInAppNotificationsCannotBeSilenced(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, "/api/v1/notifications/preferences",
		f.token, map[string]any{
			"kind": "low_stock", "in_app": false, "email": true,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	prefs, _ := decodeJSON(t, resp)["data"].([]any)
	for _, p := range prefs {
		row, _ := p.(map[string]any)
		if row["kind"] == "low_stock" {
			if row["in_app"] != true {
				t.Fatal("in-app notifications were switched off, which would " +
					"leave a shop with no way to discover a failed submission")
			}
			if row["email"] != true {
				t.Error("the email choice was not kept")
			}
		}
	}
}

// The bell and the list agree, because the count travels with the list.
func TestAnAnnouncementReachesTheInbox(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if resp := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/notifications/announce"), f.token,
		map[string]any{
			"title": "Stocktake on Friday",
			"body":  "The shop closes at four.",
		}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("announce: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	inbox := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/notifications"), f.token, nil))
	if inbox["unread"] != float64(1) {
		t.Fatalf("the bell reads %v after one announcement", inbox["unread"])
	}
	notes, _ := inbox["data"].([]any)
	if len(notes) != 1 {
		t.Fatalf("%d notifications in the inbox, want 1", len(notes))
	}

	first, _ := notes[0].(map[string]any)
	id, _ := first["id"].(string)
	if resp := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/notifications/"+id+"/read"), f.token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mark read: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	after := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/notifications/unread"), f.token, nil))
	if after["unread"] != float64(0) {
		t.Errorf("the bell still reads %v after everything was read",
			after["unread"])
	}
}

// --- approvals ------------------------------------------------------------

// Watching and deciding are separate verbs, and the rule editor is a third.
func TestAnApprovalRuleCanBeSavedAndSwitchedOff(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/approval-rules"),
		f.token, map[string]any{
			"name":    "Expenses over 5,000",
			"subject": "expense",
			"action":  "require_approval",
			"condition": map[string]any{
				"amount_over": "5000",
			},
			"steps": []map[string]any{{"role": "owner"}},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save a rule: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	rule := decodeJSON(t, resp)
	if rule["is_active"] != true {
		t.Error("a new rule is not in force, so it would decide nothing")
	}

	id, _ := rule["id"].(string)
	if off := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/approval-rules/"+id+"/active"), f.token,
		map[string]any{"is_active": false}); off.StatusCode != http.StatusNoContent {
		t.Fatalf("switch off: status %d — %s", off.StatusCode, readBody(t, off))
	}

	// Switched off rather than deleted: the requests it raised name it, and
	// deleting one would leave a decision nobody can explain.
	rules, _ := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/approval-rules"), f.token, nil))["data"].([]any)
	if len(rules) != 1 {
		t.Fatalf("%d rules after switching one off, want it to still be there",
			len(rules))
	}
}

// Turning something down without saying why leaves the person who asked with
// nothing to change.
func TestARefusalHasToSayWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// There is no request to decide, so the missing reason must be caught
	// before the lookup: the message a person gets should be about what they
	// left out, not about a record they did not name.
	resp := h.do(t, http.MethodPost,
		adminPath(f, "/api/v1/approvals/"+newUUID().String()+"/decide"),
		f.token, map[string]any{"approve": false})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a refusal with no reason was accepted")
	}
	if body := readBody(t, resp); !strings.Contains(strings.ToLower(body), "why") {
		t.Errorf("the refusal does not ask for a reason: %s", truncate(body, 160))
	}
}

func customerCount(t *testing.T, h *harness, f *shopFixture) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*)::int FROM customer WHERE company_id = $1`,
			f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	return n
}
