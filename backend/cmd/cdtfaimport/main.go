// Turning CDTFA's published rate workbook into the platform's import contract.
//
// The California Department of Tax and Fee Administration publishes "Sales &
// Use Tax Rates" as a spreadsheet each quarter, at
// https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm — there is no
// API and the file name carries the effective date
// (SalesTaxRates07-01-26.xlsx). So converting it is a step somebody performs,
// and this is that step, written down and repeatable rather than done by hand.
//
// # What the file says, and what the platform stores
//
// Each row is a LOCATION and its COMBINED rate: "Adelanto, 0.0775, San
// Bernardino, City". The 7.75% already includes California's 7.25% statewide
// rate and every district applying at that address.
//
// The platform sums a jurisdiction chain instead — country, then state, then
// the location. Importing 7.75% under a state already carrying 7.25% would
// charge 15%. So each location's own share is the DIFFERENCE between what
// CDTFA publishes for it and the statewide rate CDTFA publishes separately:
//
//	Adelanto  0.0775 - 0.0725 = 0.0050
//	Alameda   0.1075 - 0.0725 = 0.0350
//
// That is arithmetic on two official figures, not a judgement about what any
// district levies. The chain then sums back to exactly the rate CDTFA
// published, which is the number a shop is audited against.
//
// # Why locations hang off the state and not off their county
//
// CDTFA's city figure ALREADY includes any county district applying there. A
// city placed under its county would add the county's share twice. The county
// rows in the file are locations in their own right — the rate for the
// unincorporated parts of that county — so they sit beside the cities rather
// than above them.
//
// # It decides nothing
//
// Output is the import payload, unverified by default. A person still reviews
// it and records it through the platform, which is where verification is
// asserted. Nothing here writes to a database.
package main

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// stateBase is California's statewide rate, published on the same CDTFA page
// as the workbook: "The statewide tax rate is 7.25%."
//
// A flag rather than a constant because it changes on its own schedule, and a
// run against a future file must be able to say so.
var stateBase = flag.String("state-base", "0.0725",
	"the statewide rate CDTFA publishes, subtracted to leave each location's own share")

func main() {
	in := flag.String("file", "", "CDTFA SalesTaxRates workbook (.xlsx)")
	from := flag.String("effective-from", "", "date the schedule takes effect, YYYY-MM-DD")
	doc := flag.String("document", "", "the publication, as it should be recorded")
	url := flag.String("url", "https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm",
		"where it was published")
	flag.Parse()

	if *in == "" || *from == "" || *doc == "" {
		fmt.Fprintln(os.Stderr,
			"usage: cdtfaimport -file SalesTaxRates07-01-26.xlsx "+
				"-effective-from 2026-07-01 -document \"...\"")
		os.Exit(2)
	}

	rows, err := readSheet(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	base, err := decimal.NewFromString(*stateBase)
	if err != nil {
		fmt.Fprintln(os.Stderr, "state base:", err)
		os.Exit(1)
	}

	payload := map[string]any{
		"country":          "us",
		"treatment":        "taxable",
		"effective_from":   *from,
		"source_authority": "cdtfa",
		"source_document":  *doc,
		"source_url":       *url,
		"notes": "Each location's share is its CDTFA combined rate less the " +
			"statewide " + base.String() + ", so the jurisdiction chain sums " +
			"back to the published combined figure.",
	}

	out := []map[string]any{
		{"level": "country", "code": "US", "name": "United States", "rate": "0"},
		{"level": "state", "code": "CA", "name": "California",
			"parent_code": "US", "rate": base.String()},
	}

	seen := map[string]bool{"US": true, "CA": true}
	var noRate, duplicate []string
	for _, r := range rows {
		if len(r) < 4 || r[0] == "" || r[0] == "Location" {
			continue
		}
		combined, err := decimal.NewFromString(r[1])
		if err != nil {
			// CDTFA publishes a handful of counties with the Rate cell empty
			// and a note saying to look up the city or the unincorporated
			// area instead. There is no county-wide figure to import, and
			// inventing one — or quietly treating the county as zero — would
			// undercharge every sale in it. Named on the way out, not counted.
			noRate = append(noRate, r[0])
			continue
		}
		// The workbook stores rates as IEEE-754 doubles, and 7.25% is not
		// exactly representable in binary: Alpine County arrives as
		// 0.072499999999999995. Left alone it reads as a rate BELOW the
		// statewide base and the run aborts.
		//
		// Every rate CDTFA publishes is exact to five decimal places — the
		// finest district increment is 0.125% — so rounding to six recovers
		// the published figure exactly and discards only the storage error.
		combined = combined.Round(6)
		share := combined.Sub(base)
		if share.IsNegative() {
			// A location publishing BELOW the statewide rate would mean the
			// state base has changed and this run is using the wrong one.
			// Refused rather than clamped: a negative district share would be
			// a fabricated fact, and a silently clamped one would hide that
			// the whole file is being read against a stale base.
			fmt.Fprintf(os.Stderr,
				"%s publishes %s, below the statewide %s — check -state-base "+
					"against the CDTFA page for this quarter\n",
				r[0], combined, base)
			os.Exit(1)
		}

		code := codeFor(r[0])
		if seen[code] {
			duplicate = append(duplicate, r[0])
			continue
		}
		seen[code] = true

		out = append(out, map[string]any{
			"level":       levelFor(r[3]),
			"code":        code,
			"name":        r[0],
			"parent_code": "CA",
			"rate":        share.String(),
		})
	}

	payload["rows"] = out
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%d locations\n", len(out)-2)
	for _, name := range noRate {
		fmt.Fprintf(os.Stderr,
			"  no rate published: %s — CDTFA gives no county-wide figure, so "+
				"a shop there belongs to its city or unincorporated area\n",
			name)
	}
	for _, name := range duplicate {
		fmt.Fprintf(os.Stderr, "  duplicate location: %s\n", name)
	}
}

// levelFor maps CDTFA's Type column onto the platform's levels.
func levelFor(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "county") {
		return "county"
	}
	return "city"
}

// fold maps the accented letters that appear in Californian place names onto
// their ASCII form, for the code only.
var fold = map[rune]rune{
	'Á': 'A', 'À': 'A', 'Ä': 'A', 'Â': 'A', 'Ã': 'A', 'Å': 'A',
	'É': 'E', 'È': 'E', 'Ë': 'E', 'Ê': 'E',
	'Í': 'I', 'Ì': 'I', 'Ï': 'I', 'Î': 'I',
	'Ó': 'O', 'Ò': 'O', 'Ö': 'O', 'Ô': 'O', 'Õ': 'O',
	'Ú': 'U', 'Ù': 'U', 'Ü': 'U', 'Û': 'U',
	'Ñ': 'N', 'Ç': 'C',
}

// codeFor makes a stable jurisdiction code out of a location name.
//
// CDTFA publishes names, not codes. The code has to survive a reload of next
// quarter's file unchanged, or every location would be created afresh each
// quarter and the old rates would never be superseded.
func codeFor(name string) string {
	var b strings.Builder
	b.WriteString("CA-")
	for _, r := range strings.ToUpper(name) {
		// CDTFA writes "La Cañada Flintridge". Folding the accent keeps the
		// code readable and ASCII; the location's NAME keeps the ñ, because
		// that is how the authority spells it and how it must appear on a
		// return.
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	code := b.String()
	if len(code) > 60 {
		code = code[:60]
	}
	return code
}

// --- the workbook ---------------------------------------------------------

func readSheet(path string) ([][]string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()

	shared, err := sharedStrings(z)
	if err != nil {
		return nil, err
	}

	var sheet io.ReadCloser
	for _, f := range z.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			if sheet, err = f.Open(); err != nil {
				return nil, err
			}
			break
		}
	}
	if sheet == nil {
		return nil, fmt.Errorf("no worksheet in %s", path)
	}
	defer sheet.Close()

	var doc struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline struct {
					Text []string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.NewDecoder(sheet).Decode(&doc); err != nil {
		return nil, err
	}

	out := make([][]string, 0, len(doc.Rows))
	for _, row := range doc.Rows {
		// A spreadsheet omits empty cells rather than writing them, so
		// appending in document order shifts every later column left: the
		// county rows CDTFA publishes with no Rate would put the county NAME
		// where the rate belongs and the Notes where the Type belongs. Each
		// cell carries its own reference ("C119"), so place it by that.
		var cells []string
		for _, c := range row.Cells {
			var text string
			switch {
			case c.Type == "s":
				i, err := strconv.Atoi(c.Value)
				if err == nil && i >= 0 && i < len(shared) {
					text = shared[i]
				}
			case len(c.Inline.Text) > 0:
				text = strings.Join(c.Inline.Text, "")
			default:
				text = c.Value
			}
			at := columnOf(c.Ref)
			for len(cells) <= at {
				cells = append(cells, "")
			}
			cells[at] = text
		}
		out = append(out, cells)
	}
	return out, nil
}

// columnOf turns a cell reference ("C119") into a zero-based column index.
func columnOf(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A') + 1
	}
	if n == 0 {
		return 0
	}
	return n - 1
}

func sharedStrings(z *zip.ReadCloser) ([]string, error) {
	for _, f := range z.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		var doc struct {
			Items []struct {
				Text []string `xml:"t"`
			} `xml:"si"`
		}
		if err := xml.NewDecoder(rc).Decode(&doc); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(doc.Items))
		for _, si := range doc.Items {
			out = append(out, strings.TrimSpace(strings.Join(si.Text, "")))
		}
		return out, nil
	}
	return nil, nil
}
