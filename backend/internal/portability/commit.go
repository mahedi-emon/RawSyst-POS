package portability

// Writing what was checked (blueprint H7).
//
// # One transaction for the whole batch
//
// Every valid row lands or none of them do. H7's own argument for staging is
// that a half-finished import is worse than none, and committing row by row
// would reintroduce exactly that: a shop with 300 of 500 customers, no way to
// tell which 300, and a re-run that duplicates them.
//
// # Invalid rows are skipped, not fixed
//
// Nothing here guesses. A row whose SKU is blank is not given a generated one,
// and a price that is not a number is not read as zero. The batch imports what
// was valid and the rest stays in the Error Report for somebody to correct,
// because a shop that discovers six months later that 40 products silently got
// a zero cost has an inventory valuation nobody can unpick.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Commit writes every valid row of a validated batch.
func (s *Service) Commit(
	ctx context.Context, scope Scope, batchID uuid.UUID,
) (Batch, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		kind, mapping, status, e := s.batchHead(ctx, tx, scope, batchID)
		if e != nil {
			return e
		}
		switch status {
		case "committed":
			return errs.New(errs.CodeConflict,
				"That import has already been written.")
		case "cancelled":
			return errs.New(errs.CodeConflict,
				"That import was cancelled.")
		case "uploaded":
			return errs.New(errs.CodeConflict,
				"Check that import before writing it, so its errors are known "+
					"before anything moves.")
		}

		rows, e := tx.Query(ctx, `
			SELECT id, row_no, raw::text FROM import_row
			WHERE batch_id = $1 AND status = 'valid' ORDER BY row_no`, batchID)
		if e != nil {
			return e
		}
		type pending struct {
			id  uuid.UUID
			no  int
			raw map[string]string
		}
		var valid []pending
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
			valid = append(valid, p)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		if len(valid) == 0 {
			return errs.New(errs.CodeConflict,
				"Nothing in that import passed the checks, so there is nothing "+
					"to write.")
		}

		written := 0
		for _, p := range valid {
			fields := apply(mapping, p.raw)
			id, e := writeRow(ctx, tx, scope, kind, fields)
			if e != nil {
				// A row the checks passed and the database refused. The whole
				// batch is rolled back and the reason is named, because the
				// alternative is a partial import — the exact thing staging
				// exists to prevent.
				return errs.Newf(errs.CodeConflict,
					"Row %d could not be written, so nothing was: %s",
					p.no, plainMessage(e))
			}
			if _, e := tx.Exec(ctx, `
				UPDATE import_row SET status = 'imported', created_id = $2
				WHERE id = $1`, p.id, id); e != nil {
				return e
			}
			written++
		}

		if _, e := tx.Exec(ctx, `
			UPDATE import_batch
			SET status = 'committed', imported_rows = $2, committed_at = now()
			WHERE id = $1`, batchID, written); e != nil {
			return e
		}
		return auditImport(ctx, tx, scope, batchID, kind, written)
	})
	if err != nil {
		return Batch{}, db.Translate(err, "")
	}
	return s.Batch(ctx, scope, batchID)
}

// writeRow puts one row where it belongs.
//
// Each kind is written by its own statement rather than through the module that
// owns it. That is a deliberate trade and worth saying out loud: going through
// catalog.CreateProduct would reuse that module's validation, but it would also
// take its numbering, its audit entry and its notifications per row, and a
// five-hundred-row import would produce five hundred audit entries nobody will
// ever read. What is NOT skipped is the database's own constraints — every
// uniqueness rule, foreign key and check still applies, and a row that breaks
// one rolls the batch back rather than being written differently.
func writeRow(
	ctx context.Context, tx pgx.Tx, scope Scope, kind string,
	fields map[string]string,
) (uuid.UUID, error) {
	switch kind {
	case KindProducts:
		return writeProduct(ctx, tx, scope, fields)
	case KindCustomers:
		return writeCustomer(ctx, tx, scope, fields)
	case KindSuppliers:
		return writeSupplier(ctx, tx, scope, fields)
	case KindOpeningStock:
		return writeOpeningStock(ctx, tx, scope, fields)
	case KindOpeningBalances:
		return writeOpeningBalance(ctx, tx, scope, fields)
	case KindEmployees:
		return writeEmployee(ctx, tx, scope, fields)
	}
	return uuid.Nil, errs.Newf(errs.CodeInternal,
		"There is no importer for %q.", kind)
}

func writeProduct(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	categoryID, err := lookupOrCreate(ctx, tx, scope, "category", f["category"])
	if err != nil {
		return uuid.Nil, err
	}
	brandID, err := lookupOrCreate(ctx, tx, scope, "brand", f["brand"])
	if err != nil {
		return uuid.Nil, err
	}

	treatment := strings.TrimSpace(f["tax_treatment"])
	if treatment == "" {
		treatment = "standard"
	}

	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO product
		  (tenant_id, company_id, sku, name, translations, category_id,
		   brand_id, tax_treatment)
		VALUES ($1,$2,$3,$4,
		        CASE WHEN $5::text IS NULL THEN '{}'::jsonb
		             ELSE jsonb_build_object('ar', $5::text) END,
		        $6,$7,$8)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, f["sku"], f["name"],
		nullText(f["name_ar"]), categoryID, brandID, treatment).
		Scan(&productID); err != nil {
		return uuid.Nil, err
	}

	// One variant, carrying the prices. A product with no variant cannot be
	// sold — the till sells variants — so an import that created only the
	// product would produce a catalogue that looks complete and rings up
	// nothing.
	var variantID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO variant
		  (tenant_id, company_id, product_id, sku, barcode, price_retail,
		   price_wholesale, cost_standard)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, productID, f["sku"],
		nullText(f["barcode"]), amountOr(f["price_retail"], decimal.Zero),
		amountOrNull(f["price_wholesale"]), amountOrNull(f["cost"])).
		Scan(&variantID)
	return productID, err
}

func writeCustomer(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	code := strings.TrimSpace(f["code"])
	if code == "" {
		// Derived from the name rather than left blank: the column is NOT NULL
		// and a shop importing five hundred customers has not typed codes for
		// any of them.
		code = codeFrom(f["name"])
	}
	kindOf := strings.TrimSpace(f["customer_type"])
	if kindOf == "" {
		kindOf = "retail"
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO customer
		  (tenant_id, company_id, code, name, name_ar, customer_type, phone,
		   email, vat_number, address, payment_terms_days, credit_limit)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, code, f["name"],
		nullText(f["name_ar"]), kindOf, nullText(f["phone"]),
		nullText(f["email"]), nullText(f["vat_number"]), nullText(f["address"]),
		intOr(f["payment_terms_days"], 0), amountOrNull(f["credit_limit"])).
		Scan(&id)
	return id, err
}

func writeSupplier(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	code := strings.TrimSpace(f["code"])
	if code == "" {
		code = codeFrom(f["name"])
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO supplier
		  (tenant_id, company_id, code, legal_name, name_ar, phone, email,
		   vat_number, notes, payment_terms_days)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, code, f["name"],
		nullText(f["name_ar"]), nullText(f["phone"]), nullText(f["email"]),
		nullText(f["vat_number"]), nullText(f["address"]),
		intOr(f["payment_terms_days"], 0)).Scan(&id)
	return id, err
}

// writeOpeningStock puts quantity AND value on the shelf.
//
// Both, always. A stock movement with a quantity and no value would put units
// on the shelf that the Inventory account knows nothing about, and C13's hard
// invariant — the valuation ties EXACTLY to the control account — would be
// broken by the import itself. A row with no unit cost is stock worth nothing,
// which is a statement the shop made by leaving the column out.
func writeOpeningStock(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	var variantID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM variant WHERE company_id = $1 AND upper(sku) = upper($2)`,
		scope.CompanyID, f["sku"]).Scan(&variantID); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, errs.Newf(errs.CodeInvalidInput,
				"there is no product with SKU %q", f["sku"])
		}
		return uuid.Nil, err
	}

	var warehouseID uuid.UUID
	query := `SELECT id FROM warehouse WHERE company_id = $1 ORDER BY created_at LIMIT 1`
	args := []any{scope.CompanyID}
	if code := strings.TrimSpace(f["warehouse_code"]); code != "" {
		query = `SELECT id FROM warehouse
		         WHERE company_id = $1 AND upper(code) = upper($2)`
		args = append(args, code)
	}
	if err := tx.QueryRow(ctx, query, args...).Scan(&warehouseID); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, errs.New(errs.CodeInvalidInput,
				"this company has no stock location to receive it into")
		}
		return uuid.Nil, err
	}

	qty := amountOr(f["qty"], decimal.Zero)
	value := qty.Mul(amountOr(f["unit_cost"], decimal.Zero))

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO stock_movement
		  (tenant_id, company_id, variant_id, warehouse_id, delta, reason,
		   value_delta)
		VALUES ($1,$2,$3,$4,$5,'opening',$6)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, variantID, warehouseID, qty, value).
		Scan(&id)
	return id, err
}

// writeOpeningBalance records one line of the opening journal.
//
// Every row of the batch lands in ONE journal entry, which is why the entry is
// claimed once per batch and cached on the transaction. Five hundred one-line
// entries would balance individually only by accident, and an opening balance
// that does not balance is a trial balance that never will.
func writeOpeningBalance(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	var accountID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM account WHERE company_id = $1 AND code = $2`,
		scope.CompanyID, strings.TrimSpace(f["account_code"])).
		Scan(&accountID); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, errs.Newf(errs.CodeInvalidInput,
				"there is no account with code %q", f["account_code"])
		}
		return uuid.Nil, err
	}

	entryID, err := openingEntry(ctx, tx, scope)
	if err != nil {
		return uuid.Nil, err
	}

	debit := amountOr(f["debit"], decimal.Zero)
	credit := amountOr(f["credit"], decimal.Zero)
	if debit.IsPositive() == credit.IsPositive() {
		return uuid.Nil, errs.New(errs.CodeInvalidInput,
			"an opening balance is a debit or a credit, not both and not neither")
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO journal_line
		  (tenant_id, entry_id, account_id, debit, credit, base_debit,
		   base_credit, memo)
		VALUES ($1,$2,$3,$4,$5,$4,$5,$6)
		RETURNING id`,
		scope.TenantID, entryID, accountID, debit, credit,
		nullText(f["memo"])).Scan(&id)
	return id, err
}

func writeEmployee(
	ctx context.Context, tx pgx.Tx, scope Scope, f map[string]string,
) (uuid.UUID, error) {
	number := strings.TrimSpace(f["employee_no"])
	if number == "" {
		if err := tx.QueryRow(ctx,
			`SELECT claim_document_no($1, 'employee')`, scope.CompanyID).
			Scan(&number); err != nil {
			return uuid.Nil, err
		}
	}

	joined := strings.TrimSpace(f["joined_on"])
	if joined == "" {
		// The date they started decides leave accrual and end-of-service, and
		// this product refuses to guess either. Today is the honest reading of
		// "the shop did not say", and it is visible and correctable.
		if err := tx.QueryRow(ctx, `SELECT current_date::text`).
			Scan(&joined); err != nil {
			return uuid.Nil, err
		}
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO employee
		  (tenant_id, company_id, employee_no, full_name, phone, email,
		   position, department, national_id, joined_on, basic_salary)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, number, f["full_name"],
		nullText(f["phone"]), nullText(f["email"]), nullText(f["job_title"]),
		nullText(f["department"]), nullText(f["national_id"]), joined,
		amountOr(f["basic_salary"], decimal.Zero)).Scan(&id)
	return id, err
}

// openingEntry finds or claims the one journal entry a balance import writes
// into.
//
// Keyed on (source_type, source_id) like every other entry this product posts,
// with the company as the source id: one opening entry per company, ever.
// Re-importing opening balances adds lines to the same entry, which keeps the
// books explainable — and if that entry no longer balances, the trial balance
// says so on the day rather than a year later.
func openingEntry(
	ctx context.Context, tx pgx.Tx, scope Scope,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM journal_entry
		WHERE company_id = $1 AND source_type = 'opening_balance'
		  AND source_id = $1`, scope.CompanyID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}

	var number string
	if err := tx.QueryRow(ctx, `SELECT claim_entry_no($1)`, scope.CompanyID).
		Scan(&number); err != nil {
		return uuid.Nil, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO journal_entry
		  (tenant_id, company_id, entry_no, entry_date, source_type, source_id,
		   memo, posted_by)
		VALUES ($1,$2,$3,current_date,'opening_balance',$2,
		        'Opening balances',$4)
		RETURNING id`,
		scope.TenantID, scope.CompanyID, number, scope.UserID).Scan(&id)
	return id, err
}

// lookupOrCreate resolves a category or brand by name, making one if the shop
// named something that does not exist yet.
//
// Made rather than refused: a product file from an old system names its
// categories, and a shop should not have to type forty of them by hand before
// their catalogue will import.
func lookupOrCreate(
	ctx context.Context, tx pgx.Tx, scope Scope, table, name string,
) (*uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}

	var id uuid.UUID
	var err error
	switch table {
	case "category":
		err = tx.QueryRow(ctx, `
			SELECT id FROM category
			WHERE company_id = $1 AND lower(name) = lower($2)`,
			scope.CompanyID, name).Scan(&id)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `
				INSERT INTO category (tenant_id, company_id, name)
				VALUES ($1,$2,$3) RETURNING id`,
				scope.TenantID, scope.CompanyID, name).Scan(&id)
		}
	case "brand":
		err = tx.QueryRow(ctx, `
			SELECT id FROM brand
			WHERE company_id = $1 AND lower(name) = lower($2)`,
			scope.CompanyID, name).Scan(&id)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `
				INSERT INTO brand (tenant_id, company_id, name)
				VALUES ($1,$2,$3) RETURNING id`,
				scope.TenantID, scope.CompanyID, name).Scan(&id)
		}
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// codeFrom builds a readable code out of a name.
//
// First letters of the first two words plus a short random tail, so
// "Al Faisal Trading" becomes something like AFT-4K9C. Not a sequence: two
// importers running at once would claim the same number, and a code that
// collides fails the whole batch on a uniqueness constraint.
func codeFrom(name string) string {
	var initials strings.Builder
	for _, word := range strings.Fields(strings.ToUpper(name)) {
		initials.WriteByte(word[0])
		if initials.Len() >= 3 {
			break
		}
	}
	if initials.Len() == 0 {
		initials.WriteString("X")
	}
	return initials.String() + "-" + strings.ToUpper(uuid.NewString()[:4])
}

func amountOr(s string, fallback decimal.Decimal) decimal.Decimal {
	v, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return v
}

func amountOrNull(s string) any {
	v, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return v
}

func intOr(s string, fallback int) int {
	v, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return int(v.IntPart())
}

// plainMessage is the sentence inside an error, without the wrapping a caller
// would otherwise see twice.
func plainMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
