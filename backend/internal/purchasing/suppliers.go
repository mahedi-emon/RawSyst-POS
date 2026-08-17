package purchasing

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// --- Editing a supplier --------------------------------------------------

// UpdateSupplier changes a supplier's details.
//
// The CODE is deliberately not editable. It is how a buyer finds them in a
// list, it appears on purchase orders already issued, and renaming it would
// silently change what those documents refer to. A supplier who has genuinely
// been replaced is deactivated and a new one added, which leaves the history
// readable.
//
// Payment terms ARE editable, and changing them does not touch bills already
// raised: a due date is computed once, from the terms in force when the bill was
// entered. Renegotiating terms in March must not retrospectively make a January
// invoice overdue.
func (s *Service) UpdateSupplier(
	ctx context.Context, scope Scope, supplierID uuid.UUID, in NewSupplier,
) (Supplier, error) {
	// Validated with the code filled in from the stored row, because the caller
	// is not allowed to send one and the shared validator requires it.
	check := in
	check.Code = "unchanged"
	if err := validateSupplier(check); err != nil {
		return Supplier{}, err
	}

	var out Supplier
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE supplier
			SET legal_name = $3, name_ar = $4, contact_name = $5, email = $6,
			    phone = $7, vat_number = $8, cr_number = $9, country = lower($10),
			    payment_terms_days = $11, credit_limit = $12, notes = $13,
			    updated_at = now()
			WHERE id = $1 AND company_id = $2`,
			supplierID, scope.CompanyID, in.LegalName, nullText(in.NameAr),
			nullText(in.Contact), nullText(in.Email), nullText(in.Phone),
			nullText(in.VATNumber), nullText(in.CRNumber), nullText(in.Country),
			in.TermsDays, in.CreditLimit, nullText(in.Notes))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That supplier was not found.")
		}

		read, e := s.readSupplier(ctx, tx, scope, supplierID)
		out = read
		return e
	})
	return out, err
}

// SetSupplierActive takes a supplier off the list, or puts them back.
//
// Never a delete. They are referenced by purchase orders, receipts, bills and
// payments, all of which are history, and a supplier row that vanished would
// leave those documents pointing at nothing. Deactivating hides them from the
// pickers a buyer chooses from and leaves every existing document intact.
//
// Deactivating with money still owed is REFUSED. An inactive supplier drops out
// of the lists a buyer works from, and an outstanding balance that nobody can
// see is a bill that never gets paid — which is how a shop loses a supplier
// rather than merely a record.
func (s *Service) SetSupplierActive(
	ctx context.Context, scope Scope, supplierID uuid.UUID, active bool,
) (Supplier, error) {
	var out Supplier
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var name string
		var outstanding decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT s.legal_name,
			       coalesce((
			         SELECT sum(b.total_inclusive - b.amount_paid)
			         FROM purchase_bill b
			         WHERE b.supplier_id = s.id
			           AND b.status IN ('matched','blocked','approved')
			       ), 0)
			FROM supplier s
			WHERE s.id = $1 AND s.company_id = $2`,
			supplierID, scope.CompanyID).Scan(&name, &outstanding); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That supplier was not found.")
			}
			return e
		}

		if !active && outstanding.IsPositive() {
			return errs.Newf(errs.CodeConflict,
				"%s is still owed %s. Settle it first — an inactive supplier "+
					"disappears from the lists you work from, and a balance "+
					"nobody can see is a bill that never gets paid.",
				name, outstanding.StringFixed(2))
		}

		if _, e := tx.Exec(ctx, `
			UPDATE supplier SET is_active = $3, updated_at = now()
			WHERE id = $1 AND company_id = $2`,
			supplierID, scope.CompanyID, active); e != nil {
			return e
		}

		read, e := s.readSupplier(ctx, tx, scope, supplierID)
		out = read
		return e
	})
	return out, err
}

func (s *Service) readSupplier(
	ctx context.Context, tx pgx.Tx, scope Scope, supplierID uuid.UUID,
) (Supplier, error) {
	var sup Supplier
	err := tx.QueryRow(ctx, `
		SELECT s.id, s.code, s.legal_name, coalesce(s.name_ar,''),
		       coalesce(s.contact_name,''), coalesce(s.email,''),
		       coalesce(s.phone,''), coalesce(s.vat_number,''),
		       coalesce(s.cr_number,''), coalesce(s.country,''),
		       s.payment_terms_days, coalesce(s.credit_limit::text,''),
		       coalesce(s.notes,''), s.is_active,
		       coalesce((
		         SELECT sum(b.total_inclusive - b.amount_paid)
		         FROM purchase_bill b
		         WHERE b.supplier_id = s.id
		           AND b.status IN ('matched','blocked','approved')
		       ), 0)::text
		FROM supplier s
		WHERE s.id = $1 AND s.company_id = $2`,
		supplierID, scope.CompanyID,
	).Scan(&sup.ID, &sup.Code, &sup.LegalName, &sup.NameAr, &sup.Contact,
		&sup.Email, &sup.Phone, &sup.VATNumber, &sup.CRNumber, &sup.Country,
		&sup.TermsDays, &sup.CreditLimit, &sup.Notes, &sup.IsActive,
		&sup.Outstanding)
	if errors.Is(err, pgx.ErrNoRows) {
		return Supplier{}, errs.New(errs.CodeNotFound, "That supplier was not found.")
	}
	return sup, err
}
