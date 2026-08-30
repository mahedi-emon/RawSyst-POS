// Sourcing: requisition, RFQ, supplier quotes and the award (blueprint B5,
// B5.1).
//
// This is the front half of the purchasing chain, and it ends where the back
// half begins: an award produces a purchase order through CreateOrder, the same
// function a buyer raising an order by hand reaches. There is deliberately no
// second path that writes a purchase_order row, because a PO born of a quote
// and a PO typed in by hand must be the same document in every respect that
// matters downstream — receiving, three-way matching and payment all read one
// shape.
//
// # What is compared, and what is not
//
// B5.1 wants price, total, VAT, lead time, payment terms and quality notes side
// by side. It does NOT want the system to pick a winner. Cheapest is often the
// wrong answer — a supplier who delivers in three days against one who takes
// three weeks, or one who bills on 60 days against cash on delivery — so the
// comparison presents the axes and a person decides, on the record, with a
// reason. Compare() therefore computes and ranks nothing beyond arithmetic the
// quotes already state.
package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// --- Requisitions --------------------------------------------------------

// NewRequisition is somebody asking for stock.
//
// No prices. B5's requisition is "any authorized staff can request stock"; the
// person who notices an empty shelf is not the person who negotiates cost, and
// asking them for one produces either a guess or an abandoned request.
type NewRequisition struct {
	StoreID     *uuid.UUID
	WarehouseID *uuid.UUID
	NeededBy    *time.Time
	Why         string
	Lines       []NewRequisitionLine
}

// NewRequisitionLine is one thing wanted.
//
// VariantID is optional: a shop that needs something it has never bought
// describes it in words, and the buyer turns that into a catalogue entry when
// they source it. Requiring a variant would mean nobody could ask for anything
// new.
type NewRequisitionLine struct {
	VariantID   *uuid.UUID
	Description string
	Qty         decimal.Decimal
	Note        string
}

// Requisition is a request as it is read back.
type Requisition struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"requisition_no"`
	Status   string    `json:"status"`
	NeededBy string    `json:"needed_by,omitempty"`
	Why      string    `json:"justification,omitempty"`

	StoreID     *uuid.UUID `json:"store_id,omitempty"`
	StoreName   string     `json:"store_name,omitempty"`
	WarehouseID *uuid.UUID `json:"warehouse_id,omitempty"`

	RequestedBy  string            `json:"requested_by,omitempty"`
	RequestedAt  string            `json:"requested_at"`
	DecidedBy    string            `json:"decided_by,omitempty"`
	DecidedAt    string            `json:"decided_at,omitempty"`
	DecisionNote string            `json:"decision_note,omitempty"`
	Lines        []RequisitionLine `json:"lines,omitempty"`
}

// RequisitionLine is one line as read back.
type RequisitionLine struct {
	ID          uuid.UUID  `json:"id"`
	LineNo      int        `json:"line_no"`
	VariantID   *uuid.UUID `json:"variant_id,omitempty"`
	SKU         string     `json:"sku,omitempty"`
	Description string     `json:"description"`
	Qty         string     `json:"qty_requested"`
	Note        string     `json:"note,omitempty"`
}

// RaiseRequisition records a request for stock.
//
// It is created 'submitted' rather than 'draft'. A draft that nobody can see is
// a request that never reaches an approver, and the shelf stays empty while the
// requester believes they have asked. Editing before submitting is a refinement
// worth having only once somebody complains they cannot.
func (s *Service) RaiseRequisition(
	ctx context.Context, scope Scope, in NewRequisition,
) (Requisition, error) {
	if len(in.Lines) == 0 {
		return Requisition{}, errs.New(errs.CodeInvalidInput,
			"A request needs at least one line.")
	}
	for i, l := range in.Lines {
		if !l.Qty.IsPositive() {
			return Requisition{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no quantity.", i+1)
		}
		if l.VariantID == nil && strings.TrimSpace(l.Description) == "" {
			return Requisition{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d names neither a product nor what is wanted.", i+1)
		}
	}

	var out Requisition
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.StoreID != nil {
			if e := checkBelongs(ctx, tx, "store", *in.StoreID, scope.CompanyID,
				"That store was not found."); e != nil {
				return e
			}
		}
		if in.WarehouseID != nil {
			if e := checkBelongs(ctx, tx, "warehouse", *in.WarehouseID,
				scope.CompanyID, "That warehouse was not found."); e != nil {
				return e
			}
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "requisition", "REQ")
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO purchase_requisition
			  (tenant_id, company_id, requisition_no, store_id, warehouse_id,
			   status, needed_by, justification, requested_by)
			VALUES ($1,$2,$3,$4,$5,'submitted',$6,$7,$8) RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.StoreID, in.WarehouseID,
			in.NeededBy, nullText(in.Why), scope.UserID,
		).Scan(&id); e != nil {
			return e
		}

		for i, l := range in.Lines {
			desc := strings.TrimSpace(l.Description)
			if desc == "" && l.VariantID != nil {
				// Fall back to the catalogue's own words, so a line always
				// says what it is when it is read back months later.
				_ = tx.QueryRow(ctx,
					`SELECT coalesce(v.sku, '') FROM variant v WHERE v.id = $1`,
					*l.VariantID).Scan(&desc)
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO requisition_line
				  (tenant_id, requisition_id, line_no, variant_id, description,
				   qty_requested, note)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				scope.TenantID, id, i+1, l.VariantID, desc, l.Qty,
				nullText(l.Note)); e != nil {
				return e
			}
		}

		read, e := s.readRequisition(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return Requisition{}, db.Translate(err, "")
	}
	return out, nil
}

// DecideRequisition approves or rejects a request.
//
// A rejection must say why. The requester has to be able to act on the answer —
// order less, order later, or stop asking — and "rejected" alone tells them
// none of those.
func (s *Service) DecideRequisition(
	ctx context.Context, scope Scope, id uuid.UUID, approve bool, note string,
) (Requisition, error) {
	if !approve && strings.TrimSpace(note) == "" {
		return Requisition{}, errs.Validation(
			"Say why the request is being turned down.").
			WithField("decision_note",
				"The person who asked needs to know what to do instead.")
	}

	status := "approved"
	if !approve {
		status = "rejected"
	}

	var out Requisition
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE purchase_requisition
			SET status = $3, decided_by = $4, decided_at = now(),
			    decision_note = $5
			WHERE id = $1 AND company_id = $2 AND status = 'submitted'`,
			id, scope.CompanyID, status, scope.UserID, nullText(note))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return requisitionNotDecidable(ctx, tx, scope.CompanyID, id)
		}

		read, e := s.readRequisition(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return Requisition{}, db.Translate(err, "")
	}
	return out, nil
}

// requisitionNotDecidable says why an update matched nothing. Distinguishing
// "gone" from "already decided" is worth the second read: they lead an approver
// to do very different things next.
func requisitionNotDecidable(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) error {
	var status string
	e := tx.QueryRow(ctx,
		`SELECT status FROM purchase_requisition WHERE id = $1 AND company_id = $2`,
		id, companyID).Scan(&status)
	if errors.Is(e, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That request was not found.")
	}
	if e != nil {
		return e
	}
	return errs.Newf(errs.CodeConflict,
		"That request is %s, so it cannot be decided again.", status)
}

// ListRequisitions returns requests, newest first.
func (s *Service) ListRequisitions(
	ctx context.Context, scope Scope, status string,
) ([]Requisition, error) {
	out := []Requisition{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT r.id, r.requisition_no, r.status, r.needed_by,
			       coalesce(r.justification, ''), r.store_id,
			       coalesce(st.name, ''), r.warehouse_id,
			       coalesce(u.full_name, ''), r.requested_at,
			       coalesce(d.full_name, ''), r.decided_at,
			       coalesce(r.decision_note, '')
			FROM purchase_requisition r
			LEFT JOIN store st   ON st.id = r.store_id
			LEFT JOIN app_user u ON u.id = r.requested_by
			LEFT JOIN app_user d ON d.id = r.decided_by
			WHERE r.company_id = $1 AND ($2 = '' OR r.status = $2)
			ORDER BY r.requested_at DESC
			LIMIT 500`, scope.CompanyID, status)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			r, e := scanRequisition(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// ReadRequisition returns one request with its lines.
func (s *Service) ReadRequisition(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Requisition, error) {
	var out Requisition
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readRequisition(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	if err != nil {
		return Requisition{}, db.Translate(err, "")
	}
	return out, nil
}

func (s *Service) readRequisition(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Requisition, error) {
	row := tx.QueryRow(ctx, `
		SELECT r.id, r.requisition_no, r.status, r.needed_by,
		       coalesce(r.justification, ''), r.store_id,
		       coalesce(st.name, ''), r.warehouse_id,
		       coalesce(u.full_name, ''), r.requested_at,
		       coalesce(d.full_name, ''), r.decided_at,
		       coalesce(r.decision_note, '')
		FROM purchase_requisition r
		LEFT JOIN store st   ON st.id = r.store_id
		LEFT JOIN app_user u ON u.id = r.requested_by
		LEFT JOIN app_user d ON d.id = r.decided_by
		WHERE r.id = $1 AND r.company_id = $2`, id, companyID)

	out, err := scanRequisition(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Requisition{}, errs.New(errs.CodeNotFound,
			"That request was not found.")
	}
	if err != nil {
		return Requisition{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.line_no, l.variant_id, coalesce(v.sku, ''),
		       l.description, l.qty_requested, coalesce(l.note, '')
		FROM requisition_line l
		LEFT JOIN variant v ON v.id = l.variant_id
		WHERE l.requisition_id = $1
		ORDER BY l.line_no`, id)
	if err != nil {
		return Requisition{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var l RequisitionLine
		var qty decimal.Decimal
		if e := rows.Scan(&l.ID, &l.LineNo, &l.VariantID, &l.SKU,
			&l.Description, &qty, &l.Note); e != nil {
			return Requisition{}, e
		}
		l.Qty = qty.String()
		out.Lines = append(out.Lines, l)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan function
// serves the single read and the list.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRequisition(row rowScanner) (Requisition, error) {
	var r Requisition
	var neededBy, decidedAt *time.Time
	var requestedAt time.Time
	if err := row.Scan(&r.ID, &r.Number, &r.Status, &neededBy, &r.Why,
		&r.StoreID, &r.StoreName, &r.WarehouseID, &r.RequestedBy,
		&requestedAt, &r.DecidedBy, &decidedAt, &r.DecisionNote); err != nil {
		return Requisition{}, err
	}
	r.RequestedAt = requestedAt.UTC().Format(time.RFC3339)
	if neededBy != nil {
		r.NeededBy = neededBy.Format("2006-01-02")
	}
	if decidedAt != nil {
		r.DecidedAt = decidedAt.UTC().Format(time.RFC3339)
	}
	r.Lines = []RequisitionLine{}
	return r, nil
}
