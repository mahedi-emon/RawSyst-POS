//go:build integration

// Taking a report away as a spreadsheet.
//
// `report.export` was seeded on the Owner and Accountant roles and guarded no
// route, so an owner who wanted the day's takings in a file could read them on
// a screen and retype them. These tests hold the export to the property that
// makes it worth having: the file says what the screen says.
package api

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
)

// exportCSV asks for one report and parses the file back.
func exportCSV(t *testing.T, h *harness, f *shopFixture, kind, query string) (
	*http.Response, [][]string,
) {
	t.Helper()
	url := "/api/v1/reports/" + kind + "/export?company_id=" +
		f.companyID.String()
	if query != "" {
		url += "&" + query
	}
	resp := h.do(t, http.MethodGet, url, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	body := readBody(t, resp)

	// The byte order mark is what makes Excel on Windows read this as UTF-8;
	// the CSV reader has to be given the file without it.
	if !strings.HasPrefix(body, "\ufeff") {
		t.Error("the file has no byte order mark, so Excel on Windows will " +
			"read every Arabic account name as mojibake")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff")))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	return resp, rows
}

// cellAfter finds a labelled row and returns its last non-empty cell.
//
// The label is not always in the first column: the statement exports indent
// their totals so they line up under the account column.
func cellAfter(rows [][]string, label string) string {
	for _, r := range rows {
		at := -1
		for i, c := range r {
			if c == label {
				at = i
				break
			}
		}
		if at < 0 {
			continue
		}
		for i := len(r) - 1; i > at; i-- {
			if r[i] != "" {
				return r[i]
			}
		}
	}
	return ""
}

// Every report offered can actually be exported, and says what it is.
func TestEveryOfferedReportExports(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, kind := range []string{"sales", "expenses", "stock",
		"trial-balance", "profit-and-loss", "balance-sheet"} {
		t.Run(kind, func(t *testing.T) {
			resp, rows := exportCSV(t, h, f, kind, "")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("export %s: %d %s", kind, resp.StatusCode,
					readBody(t, resp))
			}
			if got := resp.Header.Get("Content-Type"); !containsFold(got, "csv") {
				t.Errorf("Content-Type is %q, want a CSV type", got)
			}
			if got := resp.Header.Get("Content-Disposition"); !containsFold(
				got, ".csv") {
				t.Errorf("the browser is not told to save a file: %q", got)
			}
			if len(rows) < 3 {
				t.Fatalf("the file has %d rows; it should carry at least a "+
					"title, a period and a currency", len(rows))
			}
			// A column of money with no currency on it is a page of numbers,
			// and this product sells into three markets.
			if cellAfter(rows, "Currency") == "" {
				t.Error("the export names no currency")
			}
		})
	}
}

// The exported figures are the ones the screen shows.
//
// The property that makes the export worth having. An export with its own
// query is one that disagrees with the page it was taken from, eventually and
// quietly, and the person who finds out is reconciling to a bank.
func TestTheExportedTrialBalanceMatchesTheScreen(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.sellOne(t, f)

	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/trial-balance?company_id="+f.companyID.String(),
		f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trial balance: %s", readBody(t, resp))
	}
	screen := decodeJSONFrom(t, resp)

	_, rows := exportCSV(t, h, f, "trial-balance", "")
	for _, c := range []struct{ label, key string }{
		{"Total", "total_debit"},
		{"Difference", "difference"},
	} {
		want, _ := screen[c.key].(string)
		got := cellAfter(rows, c.label)
		if c.label == "Total" {
			// The totals row carries debit and credit; compare the debit.
			for _, r := range rows {
				if len(r) >= 4 && r[1] == "Total" {
					got = r[3]
				}
			}
		}
		if want == "" {
			t.Fatalf("the screen did not return %s", c.key)
		}
		if !amountsEqual(got, want) {
			t.Errorf("%s in the file is %q, the screen says %q — the export "+
				"and the page disagree", c.label, got, want)
		}
	}
}

// A report nobody offers cannot be asked for.
//
// The kind reaches a service method, so a free-text one would be a way to ask
// for a method that was never meant to be exported.
func TestAnUnknownReportCannotBeExported(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/everything/export?company_id="+f.companyID.String(),
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusUnprocessableEntity &&
		resp.StatusCode != http.StatusNotFound {
		t.Errorf("exporting an unknown report got %d, want a refusal",
			resp.StatusCode)
	}
}

// Adding an export route must not have broken the saved-report routes.
//
// `/reports/{kind}/export` and `/reports/saved/{savedID}` are the same shape,
// static and parameter segments swapped, which is exactly the case a router
// trie can get wrong.
func TestTheSavedReportRoutesStillResolve(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/saved?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("listing saved reports got %d, want 200: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Somebody without the permission cannot take the books away.
//
// An export is a copy of the business's figures leaving the product, so it is
// gated like reading them, not more loosely.
func TestACashierCannotExportTheBooks(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/trial-balance/export?company_id="+
			f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier exporting the trial balance got %d, want 403",
			resp.StatusCode)
	}
}
