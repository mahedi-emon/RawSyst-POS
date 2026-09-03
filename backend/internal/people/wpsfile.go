// The WPS wage file, written to the Ministry's own specification.
//
// Source: HRSD, "WPS Wages File Specification", retrieved 2026-09-03 from
// https://www.hrsd.gov.sa/sites/default/files/2017-06/WPS%20Wages%20File%20Technical%20Specification.pdf
//
// Until now this product had two wage-file formats — `mudad_xml` and `sif` —
// and both were invented. Neither appears in any Ministry document. They were
// unreachable because `SA.WPS.WAGE_FILE_FORMAT` was never verified, which is
// the only reason a guessed layout never reached a bank.
//
// This is the real thing, and every rule below is quoted from the section of
// the specification that states it.
package people

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// FormatWPSTab is the registry value naming the Ministry's layout.
//
// Named for what it is rather than for a portal: §1.7 calls it the "Wages
// output file", tab-delimited text. Mudad is where a file is uploaded, not a
// second format.
const FormatWPSTab = "wps_tab"

// wpsEstablishment is the Header Group's subject: the employer and its bank.
type wpsEstablishment struct {
	sarieBankID  string // [DEST-ID]   4!A
	establishID  string // [ESTB-ID]   10d
	bankAccount  string // [BANK-ACC]  24!X
	molEstablish string // [MOL-ESTBID] 2d-15d
}

// wpsLine is one Content/Repeating Group row: one employee, one transfer.
type wpsLine struct {
	net        decimal.Decimal // [32B-AMT] 15d
	account    string          // [59-ACC]  34X
	name       string          // [59-NAME] 4*35z
	bank       string          // [57-BANK] 4*35x
	basic      decimal.Decimal // [MOL-BAS] 12d
	housing    decimal.Decimal // [MOL-HAL] 12d
	other      decimal.Decimal // [MOL-OEA] 12d
	deductions decimal.Decimal // [MOL-DED] 12d
	molID      string          // [MOL-ID]  10d
}

// buildWPSFile renders a payroll run as a WPS wages file.
//
// # Shape
//
// §1.7: "output file type should be always txt file (CSV) type. Values in the
// file are separated by delimiter TAB". One Header Group at the top, appearing
// exactly once, then one Content row per employee.
//
// §1.7 also states that a "-" at the beginning of a line marks the end of file
// data, so the file is terminated with one.
//
// # What is deliberately absent
//
// [FILE-REJCDE], [RET-CODE], [TRN-REF], [TRN-STATUS] and [TRN-DATE] are marked
// "Reserved for Bank use only" in tables 2 and 4. An establishment writes them
// empty; the bank fills them in the file it returns. Writing anything there
// would be claiming a payment had been executed.
//
// [D-DATE] and [70-DET] are optional and left empty rather than guessed.
func buildWPSFile(
	ctx context.Context, tx pgx.Tx, runID, companyID uuid.UUID,
	valueDate time.Time, fileRef string,
) (string, error) {
	est, err := wpsEstablishmentOf(ctx, tx, companyID)
	if err != nil {
		return "", err
	}

	lines, total, err := wpsLinesOf(ctx, tx, runID)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", errs.New(errs.CodeConflict,
			"That payroll run has no payslips, so there is nothing to pay.")
	}

	var b strings.Builder

	// Header Group, table 2, in the document's order.
	//
	// [32A-AMT] "must be equal to the sum of the individual transaction amount
	// fields [32B-AMT]" — the receiving bank validates it and rejects the whole
	// file if it disagrees, so it is summed from the rows rather than taken
	// from the run's own total.
	fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		est.sarieBankID,              // [DEST-ID]
		est.establishID,              // [ESTB-ID]
		est.bankAccount,              // [BANK-ACC]
		"SAR",                        // [32A-CCY] §1.7: SAR only
		valueDate.Format("20060102"), // [32A-VAL] YYYYMMDD
		total.StringFixed(2),         // [32A-AMT]
		"",                           // [D-DATE] optional
		fileRef,                      // [FILE-REF] unique per establishment
		"",                           // [FILE-REJCDE] bank only
		est.molEstablish,             // [MOL-ESTBID]
	)

	// Content/Repeating Group, table 4.
	for _, l := range lines {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			l.net.StringFixed(2),        // [32B-AMT]
			l.account,                   // [59-ACC]
			wpsText(l.name, 35*4),       // [59-NAME] 4*35z
			wpsText(l.bank, 35*4),       // [57-BANK] 4*35x
			"",                          // [70-DET] optional
			"",                          // [RET-CODE] bank only
			l.basic.StringFixed(2),      // [MOL-BAS]
			l.housing.StringFixed(2),    // [MOL-HAL]
			l.other.StringFixed(2),      // [MOL-OEA]
			l.deductions.StringFixed(2), // [MOL-DED]
			l.molID,                     // [MOL-ID]
			"",                          // [TRN-REF] bank only
			"",                          // [TRN-STATUS] bank only
			"",                          // [TRN-DATE] bank only
		)
	}

	// §1.7: a leading "-" marks the end of the file's data.
	b.WriteString("-\n")
	return b.String(), nil
}

// wpsEstablishmentOf reads the Header Group's subject, refusing when the
// employer has not been given its WPS numbers.
//
// Refused rather than left blank: a file missing [DEST-ID] or [MOL-ESTBID] is
// rejected by the bank days later, by which time payday has passed. Saying so
// at generation names what to go and get.
func wpsEstablishmentOf(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (wpsEstablishment, error) {
	var e wpsEstablishment
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(wps_bank_sarie_id, ''), coalesce(wps_establishment_id, ''),
		       coalesce(wps_bank_account, ''), coalesce(mol_establishment_id, '')
		FROM company WHERE id = $1`, companyID).
		Scan(&e.sarieBankID, &e.establishID, &e.bankAccount,
			&e.molEstablish); err != nil {
		return wpsEstablishment{}, err
	}

	var missing []string
	for _, f := range []struct{ name, value string }{
		{"the bank's SARIE code", e.sarieBankID},
		{"the establishment's ID with its bank", e.establishID},
		{"the account wages are paid from", e.bankAccount},
		{"the Ministry of Labour establishment ID", e.molEstablish},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return wpsEstablishment{}, errs.Newf(errs.CodeInvalidInput,
			"This business has not been given %s, and the Wage Protection "+
				"System file cannot be written without it. Add it to the "+
				"company's details.", strings.Join(missing, ", "))
	}
	return e, nil
}

// wpsLinesOf reads one row per payslip, and the total the header must carry.
func wpsLinesOf(
	ctx context.Context, tx pgx.Tx, runID uuid.UUID,
) ([]wpsLine, decimal.Decimal, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.net, coalesce(e.iban, ''), e.full_name,
		       coalesce(e.bank_name, ''),
		       p.basic, p.housing, p.transport + p.other_allowance + p.overtime
		           + p.commission + p.bonus,
		       p.deductions,
		       coalesce(nullif(e.national_id, ''), e.iqama_no, '')
		FROM payslip p
		JOIN employee e ON e.id = p.employee_id
		WHERE p.run_id = $1
		ORDER BY e.employee_no`, runID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	defer rows.Close()

	var out []wpsLine
	total := decimal.Zero
	for rows.Next() {
		var l wpsLine
		if e := rows.Scan(&l.net, &l.account, &l.name, &l.bank, &l.basic,
			&l.housing, &l.other, &l.deductions, &l.molID); e != nil {
			return nil, decimal.Zero, e
		}
		if strings.TrimSpace(l.account) == "" {
			return nil, decimal.Zero, errs.Newf(errs.CodeInvalidInput,
				"%s has no bank account on file, and [59-ACC] is required on "+
					"every line of a Wage Protection System file.", l.name)
		}
		if strings.TrimSpace(l.molID) == "" {
			return nil, decimal.Zero, errs.Newf(errs.CodeInvalidInput,
				"%s has neither a national ID nor an Iqama number, and "+
					"[MOL-ID] is what ties the payment to the person in the "+
					"Wage Protection System.", l.name)
		}
		total = total.Add(l.net)
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// wpsText trims a free-text field to the length the specification allows.
//
// 4*35z means up to four lines of 35 characters. A tab would break the
// delimiter and a newline would break the row, so both are replaced rather
// than passed through — a name is not worth corrupting a payment file for.
func wpsText(s string, max int) string {
	s = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}
