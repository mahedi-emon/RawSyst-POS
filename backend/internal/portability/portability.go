// Package portability is the migration wizard and the data exports
// (blueprint H7).
//
// # Nothing is written until everything has been checked
//
// H7's flow is "Old system export → CSV/Excel → Field Mapping → Validation →
// Preview → Import → Error Report". The rows are staged and validated in full
// BEFORE a single one is written, because a half-finished import of a customer
// list is worse than no import: nobody knows which half, and the obvious fix —
// run it again — creates duplicates of everything that did land.
//
// So a batch has three separate acts. Upload stages the rows. Validate marks
// each one valid or invalid with a sentence the person who exported the file
// can act on. Commit writes only the valid ones, in one transaction, and
// records what each row became.
//
// # A row that cannot be imported is not silently skipped
//
// Every refused row keeps its original content and the reason. H7 calls this
// the Error Report, and it is the difference between "412 of 500 imported" —
// which tells a shop nothing — and a list they can correct and re-upload.
//
// # Export is a read, and it is scoped like every other read
//
// There is no export that reaches past the caller's company. `data.export`
// decides who may take a copy; it does not decide whose.
package portability

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// What can be brought in. The database constrains these too; the list is here
// so a screen can offer them and so an unknown kind is refused with a sentence
// rather than a constraint violation.
const (
	KindProducts        = "products"
	KindCustomers       = "customers"
	KindSuppliers       = "suppliers"
	KindOpeningStock    = "opening_stock"
	KindOpeningBalances = "opening_balances"
	KindEmployees       = "employees"
)

// Kinds is every import a shop can run, with the fields each needs.
var Kinds = []Shape{
	{
		Kind:     KindProducts,
		Label:    "Products",
		Required: []string{"sku", "name"},
		Optional: []string{"name_ar", "barcode", "category", "brand", "unit", "price_retail", "price_wholesale", "cost", "tax_treatment"},
	},
	{
		Kind:     KindCustomers,
		Label:    "Customers",
		Required: []string{"name"},
		Optional: []string{"code", "name_ar", "phone", "email", "vat_number", "address", "customer_type", "payment_terms_days", "credit_limit"},
	},
	{
		Kind:     KindSuppliers,
		Label:    "Suppliers",
		Required: []string{"name"},
		Optional: []string{"code", "name_ar", "phone", "email", "vat_number", "address", "payment_terms_days"},
	},
	{
		Kind:     KindOpeningStock,
		Label:    "Opening stock",
		Required: []string{"sku", "qty"},
		Optional: []string{"unit_cost", "warehouse_code"},
	},
	{
		Kind:     KindOpeningBalances,
		Label:    "Opening balances",
		Required: []string{"account_code", "debit", "credit"},
		Optional: []string{"memo"},
	},
	{
		Kind:     KindEmployees,
		Label:    "Employees",
		Required: []string{"full_name"},
		Optional: []string{"employee_no", "national_id", "phone", "email", "job_title", "department", "basic_salary", "joined_on"},
	},
}

// Shape is what one kind of import expects.
type Shape struct {
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

// Service stages, validates and commits imports, and writes exports.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Batch is one import, at whatever stage it has reached.
type Batch struct {
	ID       uuid.UUID         `json:"id"`
	Kind     string            `json:"kind"`
	Filename string            `json:"filename,omitempty"`
	Status   string            `json:"status"`
	Mapping  map[string]string `json:"mapping"`

	Total    int `json:"total_rows"`
	Valid    int `json:"valid_rows"`
	Errors   int `json:"error_rows"`
	Imported int `json:"imported_rows"`

	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	CommittedAt string `json:"committed_at,omitempty"`

	Rows []Row `json:"rows,omitempty"`
}

// Row is one line of the uploaded file.
type Row struct {
	No     int               `json:"row_no"`
	Raw    map[string]string `json:"raw"`
	Status string            `json:"status"`
	Error  string            `json:"error,omitempty"`
}

// Upload stages a CSV.
//
// The header row names the incoming columns; `mapping` says which of them feeds
// which field. An unmapped column is kept in the row's raw content rather than
// dropped, so somebody who mapped one field wrongly can fix the mapping and
// re-validate without uploading the file again.
func (s *Service) Upload(
	ctx context.Context, scope Scope, kind, filename string,
	mapping map[string]string, body io.Reader,
) (Batch, error) {
	shape, ok := shapeOf(kind)
	if !ok {
		return Batch{}, errs.Newf(errs.CodeInvalidInput,
			"There is nothing called %q that can be imported.", kind)
	}

	reader := csv.NewReader(body)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"That file has no header row, so there is nothing to map.")
	}
	for i := range header {
		// The byte order mark a spreadsheet puts in front of the first
		// header. Left on, the first column is called "\ufeffName" and
		// nothing anybody maps will ever match it.
		header[i] = strings.TrimSpace(
			strings.TrimPrefix(header[i], "\ufeff"))
	}

	// A mapping the file cannot satisfy is refused now rather than producing
	// five hundred identical row errors.
	if missing := unmapped(shape, header, mapping); len(missing) > 0 {
		return Batch{}, errs.Newf(errs.CodeInvalidInput,
			"This file has nothing mapped to %s, and an import needs %s.",
			strings.Join(missing, ", "), strings.Join(shape.Required, ", "))
	}

	type staged struct {
		no  int
		raw map[string]string
	}
	var rows []staged
	for n := 1; ; n++ {
		record, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return Batch{}, errs.Newf(errs.CodeInvalidInput,
				"Row %d could not be read: %v", n, e)
		}
		if blank(record) {
			continue
		}
		raw := map[string]string{}
		for i, column := range header {
			if i < len(record) {
				raw[column] = strings.TrimSpace(record[i])
			}
		}
		rows = append(rows, staged{no: n, raw: raw})
		if len(rows) > 20000 {
			return Batch{}, errs.New(errs.CodeInvalidInput,
				"That file has more than 20,000 rows. Split it and import "+
					"the parts, so a failure is something you can find.")
		}
	}
	if len(rows) == 0 {
		return Batch{}, errs.New(errs.CodeInvalidInput,
			"That file has a header and no rows.")
	}

	encodedMapping, err := json.Marshal(mapping)
	if err != nil {
		return Batch{}, err
	}

	var batchID uuid.UUID
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO import_batch
			  (tenant_id, company_id, kind, filename, mapping, total_rows,
			   created_by)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, kind, nullText(filename),
			string(encodedMapping), len(rows), scope.UserID).
			Scan(&batchID); e != nil {
			return db.Translate(e, "That file could not be staged.")
		}

		for _, r := range rows {
			raw, e := json.Marshal(r.raw)
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO import_row (tenant_id, batch_id, row_no, raw)
				VALUES ($1,$2,$3,$4::jsonb)`,
				scope.TenantID, batchID, r.no, string(raw)); e != nil {
				return db.Translate(e, "A row could not be staged.")
			}
		}
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return s.Validate(ctx, scope, batchID)
}

// Validate checks every staged row and says which ones cannot be imported.
//
// Writes nothing outside the batch. This is H7's preview step: a shop should be
// able to run it, read the errors, fix the file and start again without
// anything in their books having moved.
func (s *Service) Validate(
	ctx context.Context, scope Scope, batchID uuid.UUID,
) (Batch, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		kind, mapping, status, e := s.batchHead(ctx, tx, scope, batchID)
		if e != nil {
			return e
		}
		if status == "committed" {
			return errs.New(errs.CodeConflict,
				"That import has already been written and cannot be checked again.")
		}

		rows, e := tx.Query(ctx, `
			SELECT id, row_no, raw::text FROM import_row
			WHERE batch_id = $1 ORDER BY row_no`, batchID)
		if e != nil {
			return e
		}
		type pending struct {
			id  uuid.UUID
			no  int
			raw map[string]string
		}
		var staged []pending
		for rows.Next() {
			var p pending
			var raw string
			if e := rows.Scan(&p.id, &p.no, &raw); e != nil {
				rows.Close()
				return e
			}
			if e := json.Unmarshal([]byte(raw), &p.raw); e != nil {
				rows.Close()
				return e
			}
			staged = append(staged, p)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		shape, _ := shapeOf(kind)
		seen := map[string]int{}
		valid, bad := 0, 0

		for _, p := range staged {
			fields := apply(mapping, p.raw)
			problem := check(shape, fields)

			// A file that names the same SKU twice would import one and
			// overwrite it with the other, and the shop would be left with
			// whichever happened to be last. Caught here rather than by a
			// unique constraint, so the message names both rows.
			if problem == "" {
				if key := identityOf(kind, fields); key != "" {
					if first, repeated := seen[key]; repeated {
						problem = fmt.Sprintf(
							"%q is already on row %d of this file.", key, first)
					} else {
						seen[key] = p.no
					}
				}
			}

			state := "valid"
			if problem != "" {
				state = "invalid"
				bad++
			} else {
				valid++
			}
			if _, e := tx.Exec(ctx, `
				UPDATE import_row SET status = $2, error = $3 WHERE id = $1`,
				p.id, state, nullText(problem)); e != nil {
				return e
			}
		}

		_, e = tx.Exec(ctx, `
			UPDATE import_batch
			SET status = 'validated', valid_rows = $2, error_rows = $3
			WHERE id = $1`, batchID, valid, bad)
		return e
	})
	if err != nil {
		return Batch{}, db.Translate(err, "")
	}
	return s.Batch(ctx, scope, batchID)
}

// Batches lists what has been imported, newest first.
func (s *Service) Batches(ctx context.Context, scope Scope) ([]Batch, error) {
	out := []Batch{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, batchSelect+`
			WHERE b.company_id = $1
			ORDER BY b.created_at DESC
			LIMIT 100`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			b, e := scanBatch(rows)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Batch reads one, with its rows.
//
// Refused rows first. H7's Error Report is the reason anybody opens this
// screen, and putting them after four hundred successful rows would mean
// scrolling to find out what went wrong.
func (s *Service) Batch(
	ctx context.Context, scope Scope, batchID uuid.UUID,
) (Batch, error) {
	var out Batch
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, batchSelect+`
			WHERE b.id = $1 AND b.company_id = $2`, batchID, scope.CompanyID)
		b, e := scanBatch(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That import was not found.")
		}
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT row_no, raw::text, status, coalesce(error, '')
			FROM import_row WHERE batch_id = $1
			ORDER BY (status = 'invalid') DESC, row_no
			LIMIT 500`, batchID)
		if e != nil {
			return e
		}
		defer rows.Close()
		b.Rows = []Row{}
		for rows.Next() {
			var r Row
			var raw string
			if e := rows.Scan(&r.No, &raw, &r.Status, &r.Error); e != nil {
				return e
			}
			if e := json.Unmarshal([]byte(raw), &r.Raw); e != nil {
				return e
			}
			b.Rows = append(b.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		out = b
		return nil
	})
	return out, db.Translate(err, "")
}

// Cancel abandons a batch that has not been written.
func (s *Service) Cancel(
	ctx context.Context, scope Scope, batchID uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE import_batch SET status = 'cancelled'
				WHERE id = $1 AND company_id = $2
				  AND status IN ('uploaded', 'validated')`,
				batchID, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeConflict,
					"That import was not found, or has already been written.")
			}
			return nil
		}), "")
}

const batchSelect = `
	SELECT b.id, b.kind, coalesce(b.filename, ''), b.status, b.mapping::text,
	       b.total_rows, b.valid_rows, b.error_rows, b.imported_rows,
	       coalesce(u.full_name, ''), b.created_at, b.committed_at
	FROM import_batch b
	LEFT JOIN app_user u ON u.id = b.created_by`

type scanner interface{ Scan(dest ...any) error }

func scanBatch(row scanner) (Batch, error) {
	var b Batch
	var mapping string
	var created time.Time
	var committed *time.Time
	if err := row.Scan(&b.ID, &b.Kind, &b.Filename, &b.Status, &mapping,
		&b.Total, &b.Valid, &b.Errors, &b.Imported, &b.CreatedBy, &created,
		&committed); err != nil {
		return Batch{}, err
	}
	if err := json.Unmarshal([]byte(mapping), &b.Mapping); err != nil {
		return Batch{}, err
	}
	b.CreatedAt = created.UTC().Format(time.RFC3339)
	if committed != nil {
		b.CommittedAt = committed.UTC().Format(time.RFC3339)
	}
	return b, nil
}

func (s *Service) batchHead(
	ctx context.Context, tx pgx.Tx, scope Scope, batchID uuid.UUID,
) (kind string, mapping map[string]string, status string, err error) {
	var encoded string
	err = tx.QueryRow(ctx, `
		SELECT kind, mapping::text, status FROM import_batch
		WHERE id = $1 AND company_id = $2`, batchID, scope.CompanyID).
		Scan(&kind, &encoded, &status)
	if err == pgx.ErrNoRows {
		return "", nil, "", errs.New(errs.CodeNotFound,
			"That import was not found.")
	}
	if err != nil {
		return "", nil, "", err
	}
	err = json.Unmarshal([]byte(encoded), &mapping)
	return kind, mapping, status, err
}

// apply turns an incoming row into the fields this product names.
func apply(mapping map[string]string, raw map[string]string) map[string]string {
	out := map[string]string{}
	for column, field := range mapping {
		if v, ok := raw[column]; ok {
			out[field] = strings.TrimSpace(v)
		}
	}
	return out
}

// check is what a row must satisfy, in words the person who exported the file
// can act on.
func check(shape Shape, fields map[string]string) string {
	for _, required := range shape.Required {
		if strings.TrimSpace(fields[required]) == "" {
			return fmt.Sprintf("%s is empty, and every row needs one.", required)
		}
	}
	for field, value := range fields {
		if value == "" || !numeric(field) {
			continue
		}
		if _, err := decimal.NewFromString(value); err != nil {
			return fmt.Sprintf("%s reads %q, which is not a number.", field, value)
		}
	}
	if v := fields["joined_on"]; v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return fmt.Sprintf(
				"joined_on reads %q; dates have to be written 2026-08-31.", v)
		}
	}
	return ""
}

func numeric(field string) bool {
	switch field {
	case "qty", "unit_cost", "cost", "price_retail", "price_wholesale",
		"credit_limit", "payment_terms_days", "basic_salary", "debit", "credit":
		return true
	}
	return false
}

// identityOf is what makes two rows the same thing, for duplicate detection.
func identityOf(kind string, fields map[string]string) string {
	switch kind {
	case KindProducts, KindOpeningStock:
		return strings.ToUpper(fields["sku"])
	case KindOpeningBalances:
		return fields["account_code"]
	case KindEmployees:
		if v := fields["employee_no"]; v != "" {
			return strings.ToUpper(v)
		}
	case KindCustomers, KindSuppliers:
		if v := fields["code"]; v != "" {
			return strings.ToUpper(v)
		}
	}
	return ""
}

// unmapped names the required fields nothing in this file feeds.
func unmapped(shape Shape, header []string, mapping map[string]string) []string {
	known := map[string]bool{}
	for _, h := range header {
		known[h] = true
	}
	fed := map[string]bool{}
	for column, field := range mapping {
		if known[column] {
			fed[field] = true
		}
	}
	var missing []string
	for _, required := range shape.Required {
		if !fed[required] {
			missing = append(missing, required)
		}
	}
	sort.Strings(missing)
	return missing
}

func shapeOf(kind string) (Shape, bool) {
	for _, s := range Kinds {
		if s.Kind == kind {
			return s, true
		}
	}
	return Shape{}, false
}

func blank(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

// auditImport records what an import did, so a shop can tell an imported
// customer from one somebody typed.
func auditImport(
	ctx context.Context, tx pgx.Tx, scope Scope, batchID uuid.UUID,
	kind string, imported int,
) error {
	return audit.Write(ctx, tx, audit.Entry{
		TenantID: &scope.TenantID, ActorID: &scope.UserID,
		ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
		Action:     "data_imported",
		EntityType: "import_batch", EntityID: &batchID,
		After: map[string]any{"kind": kind, "rows": imported},
	})
}
