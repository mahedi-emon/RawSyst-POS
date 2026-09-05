//go:build integration

// The two statements that could be read and not saved.
//
// `GET /reports/{kind}/export` took six reports away as CSV. Two screens in the
// product had no export at all: the cash flow, and the tax return — which is
// the one statement a tax-registered business actually sits down with before
// filing, and the one most likely to be wanted as a file.
//
// The tax return is prepared by a different service from the one that writes
// the exports, and neither package knows about the other. So `reports` declares
// a one-method interface with a shape of its own and the conversion lives in
// `internal/api`, where both are already known — which also means the day the
// return gains a field, the adapter is what stops compiling, and somebody has
// to decide whether the file should carry it.
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// exported fetches one report as a file, the way the browser does.
func exported(t *testing.T, h *harness, f *shopFixture, kind string) (int, string, string) {
	t.Helper()
	now := time.Now().UTC()
	from := now.AddDate(0, -1, 0).Format("2006-01-02")
	to := now.Format("2006-01-02")
	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/"+kind+"/export?company_id="+f.companyID.String()+
			"&from="+from+"&to="+to, f.token, nil)
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Content-Disposition"), readBody(t, resp)
}

func TestEveryStatementOnAScreenCanBeTakenAway(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Every one of these is a screen somebody reads. A statement that can be
	// read and not saved is one that gets retyped into a spreadsheet.
	for _, kind := range []string{
		"trial-balance", "profit-and-loss", "balance-sheet", "cash-flow",
		"vat-return", "sales", "expenses", "stock",
	} {
		status, disposition, body := exported(t, h, f, kind)
		if status != http.StatusOK {
			t.Errorf("exporting %q answered %d: %s", kind, status,
				strings.TrimSpace(body)[:min(200, len(strings.TrimSpace(body)))])
			continue
		}
		if !strings.Contains(disposition, ".csv") {
			t.Errorf("exporting %q does not offer a file: %q", kind, disposition)
		}
	}
}

func TestTheCashFlowExportSaysWhichMethodItIs(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, _, body := exported(t, h, f, "cash-flow")
	if status != http.StatusOK {
		t.Fatalf("exporting the cash flow answered %d: %s", status, body)
	}

	// A file labelled only "Cash flow" is read as an IAS 7 indirect statement
	// by anybody who opens it in a spreadsheet. This one is direct, and the
	// screen says so, so the file has to as well.
	if !strings.Contains(body, "Method") {
		t.Errorf("the cash-flow export does not say which method it is:\n%s",
			firstLines(body, 8))
	}
	for _, want := range []string{"Opening", "Closing"} {
		if !strings.Contains(body, want) {
			t.Errorf("the cash-flow export has no %q line:\n%s", want,
				firstLines(body, 12))
		}
	}
}

func TestTheTaxReturnExportPutsItsCaveatsAboveTheFigures(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, disposition, body := exported(t, h, f, "vat-return")
	if status != http.StatusOK {
		t.Fatalf("exporting the tax return answered %d: %s", status, body)
	}
	if !strings.Contains(disposition, "vat-return") {
		t.Errorf("the file is not named for the return: %q", disposition)
	}

	// A CSV called "Tax return" that says nothing else invites the assumption
	// that it was submitted. It has to say it was not.
	if !strings.Contains(body, "Filed") {
		t.Errorf("the export does not say whether it has been filed:\n%s",
			firstLines(body, 10))
	}

	// And the caveats go ABOVE the totals. A spreadsheet is scrolled, printed
	// and forwarded; a caveat under the figures is a caveat somebody files
	// without reading, and this file exists to be filed from.
	caveats := strings.Index(body, "Not included, and why")
	totals := strings.Index(body, "Output tax")
	if caveats == -1 {
		// A return with nothing outstanding is a legitimate answer, but the
		// Saudi one has three, so this business should have them.
		t.Logf("this return reports nothing outstanding:\n%s", firstLines(body, 12))
	} else if totals != -1 && caveats > totals {
		t.Errorf("the export puts its caveats below the totals, where a reader "+
			"filing from it will not see them:\n%s", firstLines(body, 20))
	}
}

func TestAReportThatDoesNotExistIsRefusedByName(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// The kind reaches a service method, so a free-text one would be a way to
	// ask for a method that was never meant to be exported.
	status, _, body := exported(t, h, f, "everything")
	if status != http.StatusBadRequest {
		t.Fatalf("exporting a report that does not exist answered %d: %s",
			status, body)
	}
	if !strings.Contains(body, "everything") {
		t.Errorf("the refusal does not name what was asked for: %s", body)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
