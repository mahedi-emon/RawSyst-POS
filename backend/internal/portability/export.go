package portability

// Taking a copy out (blueprint H7).
//
// # CSV, written straight to the response
//
// Streamed rather than assembled in memory: a shop with sixty thousand invoice
// lines should not need the server to hold all of them at once, and a download
// that starts immediately is a download somebody believes is working.
//
// # A BOM, deliberately
//
// Excel on a Windows machine set to Arabic reads a UTF-8 file without a byte
// order mark as the local code page, and every Arabic name in it comes out as
// mojibake. Three bytes at the front is the whole fix, and this product's
// customers are in Riyadh and Dhaka.
//
// # Nothing here reaches past the caller's company
//
// Every query names `company_id = $1`. `data.export` decides who may take a
// copy; it does not decide whose.

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Export is one thing a shop can take a copy of.
type Export struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// File is what the download is called, so a person with four exports in
	// their downloads folder can tell them apart.
	File string `json:"filename"`
	sql  string
}

// Exports is everything on offer.
//
// Each is a query rather than a call into the module that owns the data, for
// the same reason the importers write their own statements: an export is a flat
// table, and reassembling one from a module's nested read type would produce a
// different shape every time somebody changed that type.
var Exports = []Export{
	{
		Kind: "products", Label: "Products and prices", File: "products.csv",
		sql: `
			SELECT v.sku AS sku, p.name AS name,
			       coalesce(p.translations->>'ar', '') AS name_ar,
			       coalesce(v.barcode, '') AS barcode,
			       coalesce(c.name, '') AS category,
			       coalesce(b.name, '') AS brand,
			       v.price_retail::text AS price_retail,
			       coalesce(v.price_wholesale::text, '') AS price_wholesale,
			       coalesce(v.cost_standard::text, '') AS cost,
			       p.tax_treatment AS tax_treatment,
			       v.is_active::text AS is_active
			FROM variant v
			JOIN product p ON p.id = v.product_id
			LEFT JOIN category c ON c.id = p.category_id
			LEFT JOIN brand b ON b.id = p.brand_id
			WHERE v.company_id = $1
			ORDER BY p.name, v.sku`,
	},
	{
		Kind: "customers", Label: "Customers and balances", File: "customers.csv",
		sql: `
			SELECT c.code AS code, c.name AS name,
			       coalesce(c.name_ar, '') AS name_ar,
			       c.customer_type AS customer_type,
			       coalesce(c.phone, '') AS phone,
			       coalesce(c.email, '') AS email,
			       coalesce(c.vat_number, '') AS vat_number,
			       coalesce(c.address, '') AS address,
			       c.payment_terms_days::text AS payment_terms_days,
			       coalesce(c.credit_limit::text, '') AS credit_limit,
			       c.is_active::text AS is_active
			FROM customer c
			WHERE c.company_id = $1
			ORDER BY c.name`,
	},
	{
		Kind: "suppliers", Label: "Suppliers", File: "suppliers.csv",
		sql: `
			SELECT s.code AS code, s.legal_name AS name,
			       coalesce(s.name_ar, '') AS name_ar,
			       coalesce(s.phone, '') AS phone,
			       coalesce(s.email, '') AS email,
			       coalesce(s.vat_number, '') AS vat_number,
			       coalesce(s.cr_number, '') AS cr_number,
			       coalesce(s.contact_name, '') AS contact_name,
			       coalesce(s.country, '') AS country,
			       s.payment_terms_days::text AS payment_terms_days,
			       s.is_active::text AS is_active
			FROM supplier s
			WHERE s.company_id = $1
			ORDER BY s.legal_name`,
	},
	{
		Kind: "stock", Label: "Stock on hand", File: "stock-on-hand.csv",
		sql: `
			SELECT v.sku AS sku, p.name AS product,
			       w.code AS location,
			       sum(m.delta)::text AS qty,
			       sum(m.value_delta)::text AS value
			FROM stock_movement m
			JOIN variant v ON v.id = m.variant_id
			JOIN product p ON p.id = v.product_id
			JOIN warehouse w ON w.id = m.warehouse_id
			WHERE m.company_id = $1
			GROUP BY v.sku, p.name, w.code
			HAVING sum(m.delta) <> 0
			ORDER BY p.name, v.sku, w.code`,
	},
	{
		Kind: "sales", Label: "Sales, line by line", File: "sales.csv",
		sql: `
			SELECT to_char(i.issued_at, 'YYYY-MM-DD') AS date,
			       coalesce(i.human_number, '') AS invoice_no,
			       i.doc_type AS document,
			       coalesce(c.name, '') AS customer,
			       l.line_no::text AS line,
			       coalesce(v.sku, '') AS sku,
			       l.description AS description,
			       l.qty::text AS qty,
			       l.unit_price::text AS unit_price,
			       l.net_amount::text AS net,
			       l.tax_amount::text AS tax,
			       l.gross_amount::text AS gross,
			       i.currency AS currency
			FROM sales_invoice_line l
			JOIN sales_invoice i ON i.id = l.invoice_id
			LEFT JOIN customer c ON c.id = i.customer_id
			LEFT JOIN variant v ON v.id = l.variant_id
			WHERE i.company_id = $1
			ORDER BY i.issued_at DESC, l.line_no`,
	},
	{
		Kind: "journal", Label: "Journal entries", File: "journal.csv",
		sql: `
			SELECT to_char(e.entry_date, 'YYYY-MM-DD') AS date,
			       e.entry_no AS entry_no,
			       e.source_type AS source,
			       a.code AS account_code, a.name AS account,
			       l.base_debit::text AS debit,
			       l.base_credit::text AS credit,
			       coalesce(l.memo, coalesce(e.memo, '')) AS memo
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account a ON a.id = l.account_id
			WHERE e.company_id = $1
			ORDER BY e.entry_date DESC, e.entry_no, a.code`,
	},
	{
		Kind: "trial_balance", Label: "Trial balance", File: "trial-balance.csv",
		sql: `
			SELECT a.code AS account_code, a.name AS account, a.type AS type,
			       sum(l.base_debit)::text AS debit,
			       sum(l.base_credit)::text AS credit,
			       (sum(l.base_debit) - sum(l.base_credit))::text AS balance
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account a ON a.id = l.account_id
			WHERE e.company_id = $1
			GROUP BY a.code, a.name, a.type
			ORDER BY a.code`,
	},
}

// WriteExport streams one export as CSV.
//
// The writer is flushed before returning, so a caller that has already sent
// headers gets a complete file rather than one truncated at the last buffer.
func (s *Service) WriteExport(
	ctx context.Context, scope Scope, kind string, out io.Writer,
) error {
	export, ok := exportOf(kind)
	if !ok {
		return errs.Newf(errs.CodeInvalidInput,
			"There is nothing called %q to export.", kind)
	}

	// See the package note: without this, Excel on an Arabic Windows reads the
	// file as the local code page and every Arabic name is mojibake.
	if _, err := out.Write([]byte("\ufeff")); err != nil {
		return err
	}

	writer := csv.NewWriter(out)
	defer writer.Flush()

	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			rows, e := tx.Query(ctx, export.sql, scope.CompanyID)
			if e != nil {
				return e
			}
			defer rows.Close()

			// The header comes from the query's own column names, so a column
			// added to the SQL cannot go out unlabelled.
			fields := rows.FieldDescriptions()
			header := make([]string, len(fields))
			for i, f := range fields {
				header[i] = f.Name
			}
			if e := writer.Write(header); e != nil {
				return e
			}

			for rows.Next() {
				values, e := rows.Values()
				if e != nil {
					return e
				}
				record := make([]string, len(values))
				for i, v := range values {
					record[i] = cell(v)
				}
				if e := writer.Write(record); e != nil {
					return e
				}
			}
			if e := rows.Err(); e != nil {
				return e
			}
			writer.Flush()
			return writer.Error()
		}), "")
}

// cell renders one value for a spreadsheet.
//
// A null is an empty cell rather than the word "null", which is what a shop
// would otherwise find in the phone column of every customer who has no phone.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return protect(t)
	case []byte:
		return protect(string(t))
	default:
		return protect(fmt.Sprint(t))
	}
}

// protect stops a spreadsheet executing a cell.
//
// A value beginning =, +, - or @ is a formula to Excel and to Google Sheets,
// and an exported customer called "=cmd|'/c calc'!A1" is a real attack against
// whoever opens the file. Prefixing a single quote makes the cell text; it is
// visible in the formula bar and invisible in the sheet, which is the right
// trade against running somebody's shell.
func protect(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func exportOf(kind string) (Export, bool) {
	for _, e := range Exports {
		if strings.EqualFold(e.Kind, kind) {
			return e, true
		}
	}
	return Export{}, false
}
