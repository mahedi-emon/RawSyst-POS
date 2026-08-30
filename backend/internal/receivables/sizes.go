package receivables

// Fitting history (blueprint B16).
//
// A customer walks in, and the staff know their collar size without asking.
// B16 calls this "fashion-specific, high-value", and it is the one feature in
// the CRM module that a shop assistant uses every single day.
//
// It is a set of rows rather than columns on the customer, because a person has
// a shirt size AND a trouser size AND a shoe size, each confirmed on a different
// day. And one row per garment rather than a history of them: staff need to
// know what the customer is NOW, and a screen showing "large (2024), extra
// large (2026)" makes them do the reading.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Size is one confirmed measurement.
type Size struct {
	ID      uuid.UUID `json:"id"`
	Garment string    `json:"garment"`
	Size    string    `json:"size"`
	// Measurements are the numbers behind the size, when somebody took them:
	// {"collar": "16", "sleeve": "34", "unit": "in"}. Kept beside the size
	// rather than instead of it — a customer knows they are a large, and a
	// tailor knows what large means on them.
	Measurements map[string]string `json:"measurements,omitempty"`
	Note         string            `json:"note,omitempty"`
	ConfirmedOn  string            `json:"confirmed_on"`
	RecordedBy   string            `json:"recorded_by,omitempty"`
}

// Sizes reads what a shop knows about how a customer is built.
func (s *Service) Sizes(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) ([]Size, error) {
	out := []Size{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Asked about first, so a customer on another shop's books is refused
		// rather than answered with an empty list. "This customer has no sizes
		// recorded" and "this customer is not yours" are different sentences,
		// and only one of them is true.
		if e := customerBelongsHere(ctx, tx, scope, customerID); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT s.id, s.garment, s.size, s.measurements,
			       coalesce(s.note, ''),
			       to_char(s.confirmed_on, 'YYYY-MM-DD'),
			       coalesce(u.full_name, '')
			FROM customer_size s
			LEFT JOIN app_user u ON u.id = s.recorded_by
			WHERE s.customer_id = $1 AND s.company_id = $2
			ORDER BY lower(s.garment)`, customerID, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var sz Size
			var measurements []byte
			if e := rows.Scan(&sz.ID, &sz.Garment, &sz.Size, &measurements,
				&sz.Note, &sz.ConfirmedOn, &sz.RecordedBy); e != nil {
				return e
			}
			if e := json.Unmarshal(measurements, &sz.Measurements); e != nil {
				return e
			}
			out = append(out, sz)
		}
		return rows.Err()
	})
	return out, err
}

// NewSize is a size being recorded or corrected.
type NewSize struct {
	Garment      string
	Size         string
	Measurements map[string]string
	Note         string
}

// RecordSize writes a customer's size for one garment.
//
// An upsert on the garment, so a customer who has gone up a size has a
// corrected row rather than two rows leaving staff to guess which is current.
// `confirmed_on` moves to today on every write, because that date is what makes
// the size worth trusting.
func (s *Service) RecordSize(
	ctx context.Context, scope Scope, customerID uuid.UUID, in NewSize,
) ([]Size, error) {
	garment := strings.TrimSpace(in.Garment)
	size := strings.TrimSpace(in.Size)
	if garment == "" {
		return nil, errs.New(errs.CodeInvalidInput, "Say what the size is for.")
	}
	if size == "" {
		return nil, errs.New(errs.CodeInvalidInput, "Say what the size is.")
	}

	measurements := in.Measurements
	if measurements == nil {
		measurements = map[string]string{}
	}
	encoded, err := json.Marshal(measurements)
	if err != nil {
		return nil, err
	}

	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := customerBelongsHere(ctx, tx, scope, customerID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO customer_size
			  (tenant_id, company_id, customer_id, garment, size,
			   measurements, note, confirmed_on, recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,current_date,$8)
			ON CONFLICT (customer_id, lower(garment)) DO UPDATE SET
			  size = EXCLUDED.size,
			  measurements = EXCLUDED.measurements,
			  note = EXCLUDED.note,
			  confirmed_on = current_date,
			  recorded_by = EXCLUDED.recorded_by`,
			scope.TenantID, scope.CompanyID, customerID, garment, size,
			encoded, nullText(in.Note), scope.UserID)
		return db.Translate(e, "That size could not be saved.")
	})
	if err != nil {
		return nil, err
	}
	return s.Sizes(ctx, scope, customerID)
}

// ForgetSize removes a garment from a customer's record.
func (s *Service) ForgetSize(
	ctx context.Context, scope Scope, customerID, sizeID uuid.UUID,
) ([]Size, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			DELETE FROM customer_size
			WHERE id = $1 AND customer_id = $2 AND company_id = $3`,
			sizeID, customerID, scope.CompanyID)
		if e != nil {
			return db.Translate(e, "That size could not be removed.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That size is not recorded.")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Sizes(ctx, scope, customerID)
}

// customerBelongsHere refuses a customer id that is not on these books.
//
// Row-level security already stops another TENANT's row being read, but a
// second company inside the same tenant is a sister shop, and the walk that
// audits every record-naming route treats "answered with nothing" as having
// handed the record over.
func customerBelongsHere(
	ctx context.Context, tx pgx.Tx, scope Scope, customerID uuid.UUID,
) error {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM customer
		               WHERE id = $1 AND company_id = $2)`,
		customerID, scope.CompanyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errs.New(errs.CodeNotFound,
			"That customer is not on this company's books.")
	}
	return nil
}
