//go:build integration

// The WPS wage file, checked against the Ministry's own specification.
//
// Source: MHRSD, "WPS Wages File Specification", retrieved 2026-09-03 from
// https://www.hrsd.gov.sa/sites/default/files/2017-06/WPS%20Wages%20File%20Technical%20Specification.pdf
//
// The product previously carried two wage-file formats, `mudad_xml` and `sif`,
// and both were invented — neither appears in any Ministry document. They never
// reached a bank only because `SA.WPS.WAGE_FILE_FORMAT` was unverified, so the
// generator refused before it could write one.
//
// These tests hold the real layout to what the document says: §1.7 for the file
// shape, table 2 for the ten Header Group fields, table 4 for the fourteen
// Content Group fields.
package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// wpsReadyCompany gives the fixture the establishment numbers the Header Group
// requires, and an employee who can actually be paid.
func wpsReadyCompany(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE company
			SET wps_bank_sarie_id = 'RJHI', wps_establishment_id = '1234567890',
			    wps_bank_account = 'SA0380000000608010167519',
			    mol_establishment_id = '123456789'
			WHERE id = $1`, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("set the WPS establishment details: %v", err)
	}
}

// wageFileOf runs payroll, approves it, and returns the generated file.
func wageFileOf(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	company := "?company_id=" + f.companyID.String()

	run := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": "2026-08"})
	if run.StatusCode != http.StatusCreated {
		t.Fatalf("payroll: %d %s", run.StatusCode, readBody(t, run))
	}
	runID, _ := decodeJSONFrom(t, run)["id"].(string)
	run.Body.Close()

	h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/approve"+company, f.token, nil).Body.Close()

	file := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/wage-file"+company, f.token, nil)
	defer file.Body.Close()
	if file.StatusCode != http.StatusOK && file.StatusCode != http.StatusCreated {
		t.Fatalf("wage file: %d %s", file.StatusCode, readBody(t, file))
	}
	body := decodeJSON(t, file)
	content, _ := body["content"].(string)
	if content == "" {
		if wf, ok := body["wage_file"].(map[string]any); ok {
			content, _ = wf["content"].(string)
		}
	}
	return content
}

// The file has the shape §1.7 describes.
//
// "output file type should be always txt file (CSV) type. Values in the file
// are separated by delimiter TAB", one Header Group at the top appearing once,
// then one Content row per employee, and a leading "-" marking the end of data.
func TestTheWageFileHasTheShapeTheSpecificationDescribes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	wpsReadyCompany(t, h, f)
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	content := wageFileOf(t, h, f)
	if content == "" {
		t.Fatal("the wage file is empty")
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("the file has %d lines; want a header, a row and a "+
			"terminator", len(lines))
	}
	if lines[len(lines)-1] != "-" {
		t.Errorf("the file ends with %q, want \"-\" — §1.7 says a leading "+
			"dash marks the end of the file's data", lines[len(lines)-1])
	}

	// Table 2: ten Header Group fields.
	header := strings.Split(lines[0], "\t")
	if len(header) != 10 {
		t.Errorf("the header has %d fields, want the 10 of table 2: %q",
			len(header), lines[0])
	}
	// Table 4: fourteen Content Group fields.
	row := strings.Split(lines[1], "\t")
	if len(row) != 14 {
		t.Errorf("a content row has %d fields, want the 14 of table 4: %q",
			len(row), lines[1])
	}
}

// The header carries the establishment, its bank and the currency.
//
// Table 2 in order: [DEST-ID] [ESTB-ID] [BANK-ACC] [32A-CCY] [32A-VAL]
// [32A-AMT] [D-DATE] [FILE-REF] [FILE-REJCDE] [MOL-ESTBID].
func TestTheWageFileHeaderCarriesTheEstablishmentAndItsBank(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	wpsReadyCompany(t, h, f)
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	header := strings.Split(
		strings.SplitN(wageFileOf(t, h, f), "\n", 2)[0], "\t")
	if len(header) != 10 {
		t.Fatalf("header has %d fields: %v", len(header), header)
	}

	if header[0] != "RJHI" {
		t.Errorf("[DEST-ID] = %q, want the bank's SARIE code", header[0])
	}
	if header[3] != "SAR" {
		t.Errorf("[32A-CCY] = %q, want SAR — §1.7 allows no other currency",
			header[3])
	}
	if len(header[4]) != 8 {
		t.Errorf("[32A-VAL] = %q, want 8 digits YYYYMMDD", header[4])
	}
	// [D-DATE] and [FILE-REJCDE] are optional and bank-only respectively.
	if header[6] != "" {
		t.Errorf("[D-DATE] = %q, want empty — it is optional and guessing a "+
			"debit date would instruct the bank to move money on a day "+
			"nobody chose", header[6])
	}
	if header[8] != "" {
		t.Errorf("[FILE-REJCDE] = %q, want empty — table 2 marks it reserved "+
			"for bank use", header[8])
	}
	if header[9] != "123456789" {
		t.Errorf("[MOL-ESTBID] = %q, want the Ministry establishment ID",
			header[9])
	}
}

// The header total equals the sum of the rows.
//
// Table 2 on [32A-AMT]: "This must be equal to the sum of the individual
// transaction amount fields [32B-AMT]... If the WPS Payment Message File fails
// this validation check, the file will be rejected by the Establishment's Bank
// without further processing."
func TestTheWageFileTotalEqualsTheSumOfItsRows(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	wpsReadyCompany(t, h, f)
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)
	h.hire(t, f, "Omar Farouk", "4500.00", nil)

	lines := strings.Split(strings.TrimRight(wageFileOf(t, h, f), "\n"), "\n")
	header := strings.Split(lines[0], "\t")

	sum := 0.0
	rows := 0
	for _, line := range lines[1:] {
		if line == "-" {
			continue
		}
		fields := strings.Split(line, "\t")
		var net float64
		if _, err := fmtSscan(fields[0], &net); err != nil {
			t.Fatalf("[32B-AMT] %q is not a number", fields[0])
		}
		sum += net
		rows++

		// Table 4 marks these reserved for the bank.
		for i, tag := range map[int]string{
			5: "[RET-CODE]", 11: "[TRN-REF]", 12: "[TRN-STATUS]",
			13: "[TRN-DATE]",
		} {
			if fields[i] != "" {
				t.Errorf("%s = %q, want empty — an establishment writing it "+
					"would be claiming the payment had been executed",
					tag, fields[i])
			}
		}
	}
	if rows != 2 {
		t.Fatalf("the file has %d employee rows, want 2", rows)
	}

	var total float64
	if _, err := fmtSscan(header[5], &total); err != nil {
		t.Fatalf("[32A-AMT] %q is not a number", header[5])
	}
	if total != sum {
		t.Errorf("[32A-AMT] is %.2f and the rows sum to %.2f — the bank "+
			"rejects the whole file on this check", total, sum)
	}
}

// A business with no WPS numbers is told which ones are missing.
//
// The Header Group cannot be written without them, and a file that reaches the
// bank without [MOL-ESTBID] comes back as a rejection days later, by which time
// payday has passed.
func TestAWageFileRefusesWhenTheEstablishmentHasNoWPSNumbers(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.hire(t, f, "Aisha Rahman", "6000.00", nil)
	company := "?company_id=" + f.companyID.String()

	run := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": "2026-08"})
	if run.StatusCode != http.StatusCreated {
		t.Fatalf("payroll: %d", run.StatusCode)
	}
	runID, _ := decodeJSONFrom(t, run)["id"].(string)
	run.Body.Close()
	h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/approve"+company, f.token, nil).Body.Close()

	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/wage-file"+company, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Fatal("a wage file was written for a business with no WPS numbers")
	}
	if body := readBody(t, resp); !containsFold(body, "Ministry of Labour") {
		t.Errorf("the refusal does not name what is missing: %s", body)
	}
}

// fmtSscan parses a decimal string into a float, for arithmetic in tests only.
// The product never uses floats for money; this only checks a total adds up.
func fmtSscan(s string, out *float64) (int, error) {
	return fmt.Sscanf(s, "%f", out)
}
