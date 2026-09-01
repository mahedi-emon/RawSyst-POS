// Serial/IMEI tracking, warranty and service work orders (blueprint B15).
package aftersales

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// --- Serial numbers ------------------------------------------------------

// Serial is one physical unit.
type Serial struct {
	ID        uuid.UUID `json:"id"`
	SerialNo  string    `json:"serial_no"`
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku,omitempty"`
	Product   string    `json:"product,omitempty"`
	Status    string    `json:"status"`

	WarehouseID *uuid.UUID `json:"warehouse_id,omitempty"`
	SupplierID  *uuid.UUID `json:"supplier_id,omitempty"`
	Supplier    string     `json:"supplier,omitempty"`
	InvoiceID   *uuid.UUID `json:"invoice_id,omitempty"`
	CustomerID  *uuid.UUID `json:"customer_id,omitempty"`
	Customer    string     `json:"customer,omitempty"`

	SoldAt        string `json:"sold_at,omitempty"`
	WarrantyUntil string `json:"warranty_until,omitempty"`
	// UnderWarranty is derived from the date, never stored: a flag would be
	// wrong every morning until a job ran, and the warranty desk is exactly
	// where a stale answer costs the shop money.
	UnderWarranty bool   `json:"under_warranty"`
	Note          string `json:"note,omitempty"`
}

// ReceiveSerials records units arriving into stock.
//
// The serials are recorded against a variant and, when known, the goods
// receipt that brought them in — which is what makes B15's lifecycle
// "Supplier → Purchase → Inventory" answerable later. It does NOT move stock:
// the GRN already did, and a serial is a name for a unit rather than a second
// copy of it.
func (s *Service) ReceiveSerials(
	ctx context.Context, scope Scope, variantID, warehouseID uuid.UUID,
	grnID, supplierID *uuid.UUID, serials []string,
) ([]Serial, error) {
	cleaned := make([]string, 0, len(serials))
	seen := map[string]bool{}
	for _, raw := range serials {
		v := strings.ToUpper(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if seen[v] {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"Serial %q appears twice in this delivery.", v)
		}
		seen[v] = true
		cleaned = append(cleaned, strings.TrimSpace(raw))
	}
	if len(cleaned) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"Give at least one serial number.")
	}

	out := []Serial{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		for _, sn := range cleaned {
			var id uuid.UUID
			e := tx.QueryRow(ctx, `
				INSERT INTO stock_serial
				  (tenant_id, company_id, variant_id, serial_no, status,
				   warehouse_id, grn_id, supplier_id)
				VALUES ($1,$2,$3,$4,'in_stock',$5,$6,$7)
				RETURNING id`,
				scope.TenantID, scope.CompanyID, variantID, sn,
				warehouseID, grnID, supplierID).Scan(&id)
			if e != nil {
				return db.Translate(e, "That serial number is already on file.")
			}
			read, e := s.readSerial(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out = append(out, read)
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// SellSerials marks units as gone to a customer and starts their warranty.
//
// The warranty end date is computed here and STORED, from the product's
// warranty months at the moment of sale. Deriving it later from the product
// would mean a shop that shortened its warranty terms in March retroactively
// cut the cover of everything sold in January.
func (s *Service) SellSerials(
	ctx context.Context, scope Scope, invoiceID uuid.UUID,
	customerID *uuid.UUID, serials []string,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		for _, raw := range serials {
			sn := strings.TrimSpace(raw)
			if sn == "" {
				continue
			}

			var id, variantID uuid.UUID
			var status string
			e := tx.QueryRow(ctx, `
				SELECT id, variant_id, status FROM stock_serial
				WHERE company_id = $1 AND upper(serial_no) = upper($2)
				FOR UPDATE`, scope.CompanyID, sn).Scan(&id, &variantID, &status)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.Newf(errs.CodeNotFound,
					"Serial %q is not in stock here.", sn)
			}
			if e != nil {
				return e
			}
			if status != "in_stock" && status != "reserved" {
				return errs.Newf(errs.CodeConflict,
					"Serial %q is %s, so it cannot be sold.", sn, status)
			}

			var months *int
			if e := tx.QueryRow(ctx, `
				SELECT p.warranty_months FROM variant v
				JOIN product p ON p.id = v.product_id
				WHERE v.id = $1`, variantID).Scan(&months); e != nil {
				return e
			}

			var until *time.Time
			if months != nil && *months > 0 {
				d := time.Now().UTC().AddDate(0, *months, 0)
				until = &d
			}

			if _, e := tx.Exec(ctx, `
				UPDATE stock_serial
				SET status = 'sold', invoice_id = $2, customer_id = $3,
				    sold_at = now(), warranty_until = $4
				WHERE id = $1`, id, invoiceID, customerID, until); e != nil {
				return e
			}
		}
		return nil
	})
	return db.Translate(err, "")
}

// LookupSerial is the warranty desk's question: what is this, who owns it, and
// is it still covered.
func (s *Service) LookupSerial(
	ctx context.Context, scope Scope, serialNo string,
) (Serial, error) {
	var out Serial
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id FROM stock_serial
			WHERE company_id = $1 AND upper(serial_no) = upper($2)`,
			scope.CompanyID, strings.TrimSpace(serialNo)).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"Nothing on file carries that serial number.")
		}
		if e != nil {
			return e
		}
		read, e := s.readSerial(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Serials lists units, optionally filtered.
func (s *Service) Serials(
	ctx context.Context, scope Scope, status string, variantID *uuid.UUID,
) ([]Serial, error) {
	out := []Serial{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, serialSelect+`
			WHERE s.company_id = $1
			  AND ($2 = '' OR s.status = $2)
			  AND ($3::uuid IS NULL OR s.variant_id = $3)
			ORDER BY s.created_at DESC
			LIMIT 500`, scope.CompanyID, status, variantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			sr, e := scanSerial(rows)
			if e != nil {
				return e
			}
			out = append(out, sr)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

const serialSelect = `
	SELECT s.id, s.serial_no, s.variant_id, coalesce(v.sku, ''),
	       coalesce(p.name, ''), s.status, s.warehouse_id, s.supplier_id,
	       coalesce(sup.legal_name, ''), s.invoice_id, s.customer_id,
	       coalesce(c.name, ''), s.sold_at, s.warranty_until,
	       coalesce(s.note, '')
	FROM stock_serial s
	LEFT JOIN variant v    ON v.id = s.variant_id
	LEFT JOIN product p    ON p.id = v.product_id
	LEFT JOIN supplier sup ON sup.id = s.supplier_id
	LEFT JOIN customer c   ON c.id = s.customer_id`

func (s *Service) readSerial(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Serial, error) {
	row := tx.QueryRow(ctx, serialSelect+`
		WHERE s.id = $1 AND s.company_id = $2`, id, companyID)
	out, err := scanSerial(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Serial{}, errs.New(errs.CodeNotFound,
			"That serial number was not found.")
	}
	return out, err
}

func scanSerial(row scanner) (Serial, error) {
	var s Serial
	var soldAt *time.Time
	var warranty *time.Time
	if err := row.Scan(&s.ID, &s.SerialNo, &s.VariantID, &s.SKU, &s.Product,
		&s.Status, &s.WarehouseID, &s.SupplierID, &s.Supplier, &s.InvoiceID,
		&s.CustomerID, &s.Customer, &soldAt, &warranty, &s.Note); err != nil {
		return Serial{}, err
	}
	if soldAt != nil {
		s.SoldAt = soldAt.UTC().Format(time.RFC3339)
	}
	if warranty != nil {
		s.WarrantyUntil = warranty.Format("2006-01-02")
		s.UnderWarranty = !warranty.Before(todayUTC())
	}
	return s, nil
}

func todayUTC() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// --- Service and repair work orders --------------------------------------

// ServiceOrder is one repair job.
type ServiceOrder struct {
	ID     uuid.UUID `json:"id"`
	JobNo  string    `json:"job_no"`
	Kind   string    `json:"kind"`
	Status string    `json:"status"`

	CustomerID *uuid.UUID `json:"customer_id,omitempty"`
	Customer   string     `json:"customer,omitempty"`
	SerialID   *uuid.UUID `json:"serial_id,omitempty"`
	SerialNo   string     `json:"serial_no,omitempty"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty"`
	Product    string     `json:"product,omitempty"`

	FaultReported string `json:"fault_reported"`
	Diagnosis     string `json:"diagnosis,omitempty"`
	WorkDone      string `json:"work_done,omitempty"`

	PartsCost  string `json:"parts_cost"`
	LabourCost string `json:"labour_cost"`
	Charged    string `json:"charged"`

	PromisedOn string `json:"promised_on,omitempty"`
	ReceivedAt string `json:"received_at"`
	ClosedAt   string `json:"closed_at,omitempty"`

	// The currency the three cost figures are in. A job is costed in its
	// company's base currency; the counter still has to be told which one,
	// because "80.00 to pay" is a different sentence in Riyadh and Dhaka.
	Currency string `json:"currency"`

	Parts []ServicePart `json:"parts,omitempty"`
}

// ServicePart is one component fitted.
type ServicePart struct {
	ID        uuid.UUID `json:"id"`
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku,omitempty"`
	Qty       string    `json:"qty"`
	UnitCost  string    `json:"unit_cost"`
	IssuedAt  string    `json:"issued_at"`
}

// NewServiceOrder books something in for repair.
type NewServiceOrder struct {
	CustomerID *uuid.UUID
	StoreID    *uuid.UUID
	SerialNo   string
	VariantID  *uuid.UUID
	Kind       string
	Fault      string
	PromisedOn *time.Time
}

// BookIn receives an item for repair.
//
// When a serial number is given the warranty is checked and the job's kind is
// decided from it — but only as a DEFAULT. A manager may still book a job as
// goodwill on an expired warranty, and recording that choice is the point:
// "we chose to cover this" is a different fact from "this was covered".
func (s *Service) BookIn(
	ctx context.Context, scope Scope, in NewServiceOrder,
) (ServiceOrder, error) {
	if strings.TrimSpace(in.Fault) == "" {
		return ServiceOrder{}, errs.Validation(
			"Say what is wrong with it.").
			WithField("fault_reported",
				"The technician needs to know what to look for.")
	}
	kind := in.Kind
	if kind == "" {
		kind = "paid"
	}
	if kind != "warranty" && kind != "paid" && kind != "goodwill" {
		return ServiceOrder{}, errs.New(errs.CodeInvalidInput,
			"A job is covered by warranty, paid for, or done as goodwill.")
	}

	var out ServiceOrder
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var serialID *uuid.UUID
		variantID := in.VariantID
		customerID := in.CustomerID
		var invoiceID *uuid.UUID

		if strings.TrimSpace(in.SerialNo) != "" {
			var sid, vid uuid.UUID
			var cid, iid *uuid.UUID
			var warranty *time.Time
			e := tx.QueryRow(ctx, `
				SELECT id, variant_id, customer_id, invoice_id, warranty_until
				FROM stock_serial
				WHERE company_id = $1 AND upper(serial_no) = upper($2)`,
				scope.CompanyID, strings.TrimSpace(in.SerialNo)).
				Scan(&sid, &vid, &cid, &iid, &warranty)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"Nothing on file carries that serial number.")
			}
			if e != nil {
				return e
			}
			serialID, variantID, invoiceID = &sid, &vid, iid
			if customerID == nil {
				customerID = cid
			}

			// The default, when the caller did not say. An in-warranty unit is
			// a warranty job unless somebody decides otherwise.
			if in.Kind == "" && warranty != nil && !warranty.Before(todayUTC()) {
				kind = "warranty"
			}
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "service", "JOB")
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO service_order
			  (tenant_id, company_id, job_no, store_id, customer_id, serial_id,
			   variant_id, invoice_id, kind, status, fault_reported,
			   promised_on, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'received',$10,$11,$12)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.StoreID, customerID,
			serialID, variantID, invoiceID, kind, strings.TrimSpace(in.Fault),
			in.PromisedOn, scope.UserID).Scan(&id); e != nil {
			return e
		}

		// The unit is with the workshop, not on the shelf and not with the
		// customer. Without this a serial in for repair would still read as
		// sold and a second job could be opened on it.
		if serialID != nil {
			if _, e := tx.Exec(ctx,
				`UPDATE stock_serial SET status = 'in_repair' WHERE id = $1`,
				*serialID); e != nil {
				return e
			}
		}

		read, e := s.readServiceOrder(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// IssuePart fits a component to a job.
//
// Stock leaves here, costed by the engine like any other issue. On a warranty
// or goodwill job it posts: the shop absorbs the cost, so Inventory falls and
// Warranty & Service Cost rises. On a PAID job it does not post here — the
// customer is being charged, and the sale that charges them carries both the
// revenue and the cost of goods, so posting here as well would take the part
// out of inventory twice.
func (s *Service) IssuePart(
	ctx context.Context, scope Scope, jobID, variantID, warehouseID uuid.UUID,
	qty decimal.Decimal,
) (ServiceOrder, error) {
	if !qty.IsPositive() {
		return ServiceOrder{}, errs.New(errs.CodeInvalidInput,
			"Say how many were fitted.")
	}

	var out ServiceOrder
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var kind, status string
		e := tx.QueryRow(ctx, `
			SELECT kind, status FROM service_order
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			jobID, scope.CompanyID).Scan(&kind, &status)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That job was not found.")
		}
		if e != nil {
			return e
		}
		if status == "delivered" || status == "cancelled" {
			return errs.Newf(errs.CodeConflict,
				"That job is %s, so parts cannot be added to it.", status)
		}

		res, e := inventory.Consume(ctx, tx, inventory.Issue{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			VariantID: variantID, WarehouseID: warehouseID, Qty: qty,
			Reason: "internal_use", SourceType: "service_order",
			SourceID: &jobID, Note: "Part fitted on a repair",
		})
		if e != nil {
			return e
		}

		unitCost := decimal.Zero
		if qty.IsPositive() {
			unitCost = res.TotalCost.Div(qty).Round(4)
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO service_part
			  (tenant_id, service_id, variant_id, qty, unit_cost, issued_by)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			scope.TenantID, jobID, variantID, qty, unitCost,
			scope.UserID); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE service_order SET parts_cost = parts_cost + $2
			WHERE id = $1`, jobID, res.TotalCost); e != nil {
			return e
		}

		// The shop is absorbing it, so the cost has to land somewhere.
		if kind == "warranty" || kind == "goodwill" {
			if e := postWarrantyPart(ctx, tx, scope, jobID, res.TotalCost); e != nil {
				return e
			}
		}

		read, e := s.readServiceOrder(ctx, tx, scope.CompanyID, jobID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

func postWarrantyPart(
	ctx context.Context, tx pgx.Tx, scope Scope, jobID uuid.UUID,
	amount decimal.Decimal,
) error {
	if !amount.IsPositive() {
		return nil
	}
	var country, currency string
	if err := tx.QueryRow(ctx,
		`SELECT country, base_currency FROM company WHERE id = $1`,
		scope.CompanyID).Scan(&country, &currency); err != nil {
		return err
	}

	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: time.Now().UTC(),
		// The job is the source, and the rule key completes the idempotency
		// key — so a part issued twice on one job posts twice, correctly,
		// while a retry of the same request does not.
		SourceType: "service_part", SourceID: uuid.New(),
		RuleKey:      "service.warranty_part",
		Currency:     currency,
		BaseCurrency: currency,
		FXRate:       decimal.NewFromInt(1),
		Memo:         "Part fitted under warranty",
		PostedBy:     &scope.UserID,
	}, country, accounting.Transaction{
		Amounts: map[string]decimal.Decimal{"amount": amount},
	})
	return err
}

// UpdateJob records progress on a repair.
type JobUpdate struct {
	Status     string
	Diagnosis  string
	WorkDone   string
	LabourCost *decimal.Decimal
	Charged    *decimal.Decimal
	// ReplacementSerial is set when the unit was swapped rather than fixed.
	// B15 wants "every replacement logged with old/new serial, reason,
	// approver", and this is the new one.
	ReplacementSerial string
}

// UpdateJob advances a repair.
func (s *Service) UpdateJob(
	ctx context.Context, scope Scope, jobID uuid.UUID, in JobUpdate,
) (ServiceOrder, error) {
	var out ServiceOrder
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var kind, current string
		var serialID *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT kind, status, serial_id FROM service_order
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			jobID, scope.CompanyID).Scan(&kind, &current, &serialID)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That job was not found.")
		}
		if e != nil {
			return e
		}
		if current == "delivered" || current == "cancelled" {
			return errs.Newf(errs.CodeConflict,
				"That job is %s and cannot be changed.", current)
		}

		status := in.Status
		if status == "" {
			status = current
		}
		if in.Charged != nil && kind == "warranty" && in.Charged.IsPositive() {
			return errs.Validation(
				"A warranty job cannot charge the customer.").
				WithField("charged",
					"Book it as a paid or goodwill job instead, so the "+
						"warranty cost stays honest.")
		}

		var replacementID *uuid.UUID
		if strings.TrimSpace(in.ReplacementSerial) != "" {
			var rid uuid.UUID
			var rstatus string
			e := tx.QueryRow(ctx, `
				SELECT id, status FROM stock_serial
				WHERE company_id = $1 AND upper(serial_no) = upper($2)
				FOR UPDATE`,
				scope.CompanyID, strings.TrimSpace(in.ReplacementSerial)).
				Scan(&rid, &rstatus)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That replacement serial number is not in stock.")
			}
			if e != nil {
				return e
			}
			if rstatus != "in_stock" {
				return errs.Newf(errs.CodeConflict,
					"The replacement unit is %s, so it cannot be given out.",
					rstatus)
			}
			replacementID = &rid
			status = "replaced"
		}

		var closedAt *time.Time
		if status == "delivered" || status == "cancelled" {
			now := time.Now().UTC()
			closedAt = &now
		}

		if _, e := tx.Exec(ctx, `
			UPDATE service_order SET
			  status = $3,
			  diagnosis = coalesce($4, diagnosis),
			  work_done = coalesce($5, work_done),
			  labour_cost = coalesce($6, labour_cost),
			  charged = coalesce($7, charged),
			  replacement_serial_id = coalesce($8, replacement_serial_id),
			  closed_at = coalesce($9, closed_at)
			WHERE id = $1 AND company_id = $2`,
			jobID, scope.CompanyID, status, nullText(in.Diagnosis),
			nullText(in.WorkDone), in.LabourCost, in.Charged, replacementID,
			closedAt); e != nil {
			return db.Translate(e, "That job could not be updated.")
		}

		// The unit's own status follows the job. A delivered repair goes back
		// to the customer; a replaced one is scrapped and the new unit takes
		// its place with them.
		if serialID != nil {
			switch status {
			case "delivered":
				if _, e := tx.Exec(ctx,
					`UPDATE stock_serial SET status = 'sold' WHERE id = $1`,
					*serialID); e != nil {
					return e
				}
			case "irreparable", "replaced":
				if _, e := tx.Exec(ctx,
					`UPDATE stock_serial SET status = 'scrapped' WHERE id = $1`,
					*serialID); e != nil {
					return e
				}
			}
		}
		if replacementID != nil {
			// The replacement goes to whoever owned the original.
			if _, e := tx.Exec(ctx, `
				UPDATE stock_serial r
				SET status = 'sold', sold_at = now(),
				    customer_id = o.customer_id, invoice_id = o.invoice_id,
				    warranty_until = o.warranty_until
				FROM service_order j
				LEFT JOIN stock_serial o ON o.id = j.serial_id
				WHERE r.id = $1 AND j.id = $2`,
				*replacementID, jobID); e != nil {
				return e
			}
		}

		read, e := s.readServiceOrder(ctx, tx, scope.CompanyID, jobID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Jobs lists repairs.
func (s *Service) Jobs(
	ctx context.Context, scope Scope, status string,
) ([]ServiceOrder, error) {
	out := []ServiceOrder{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, jobSelect+`
			WHERE j.company_id = $1 AND ($2 = '' OR j.status = $2)
			ORDER BY j.received_at DESC LIMIT 500`, scope.CompanyID, status)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			j, e := scanJob(rows)
			if e != nil {
				return e
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ReadJob returns one repair with its parts.
func (s *Service) ReadJob(
	ctx context.Context, scope Scope, id uuid.UUID,
) (ServiceOrder, error) {
	var out ServiceOrder
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readServiceOrder(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

const jobSelect = `
	SELECT j.id, j.job_no, j.kind, j.status, j.customer_id,
	       coalesce(c.name, ''), j.serial_id, coalesce(s.serial_no, ''),
	       j.variant_id, coalesce(p.name, ''), j.fault_reported,
	       coalesce(j.diagnosis, ''), coalesce(j.work_done, ''),
	       j.parts_cost, j.labour_cost, j.charged,
	       j.promised_on, j.received_at, j.closed_at, co.base_currency
	FROM service_order j
	JOIN company co          ON co.id = j.company_id
	LEFT JOIN customer c     ON c.id = j.customer_id
	LEFT JOIN stock_serial s ON s.id = j.serial_id
	LEFT JOIN variant v      ON v.id = j.variant_id
	LEFT JOIN product p      ON p.id = v.product_id`

func (s *Service) readServiceOrder(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (ServiceOrder, error) {
	row := tx.QueryRow(ctx, jobSelect+`
		WHERE j.id = $1 AND j.company_id = $2`, id, companyID)
	out, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceOrder{}, errs.New(errs.CodeNotFound,
			"That job was not found.")
	}
	if err != nil {
		return ServiceOrder{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT sp.id, sp.variant_id, coalesce(v.sku, ''), sp.qty,
		       sp.unit_cost, sp.issued_at
		FROM service_part sp
		LEFT JOIN variant v ON v.id = sp.variant_id
		WHERE sp.service_id = $1 ORDER BY sp.issued_at`, id)
	if err != nil {
		return ServiceOrder{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ServicePart
		var qty, cost decimal.Decimal
		var at time.Time
		if e := rows.Scan(&p.ID, &p.VariantID, &p.SKU, &qty, &cost, &at); e != nil {
			return ServiceOrder{}, e
		}
		p.Qty = qty.String()
		p.UnitCost = cost.StringFixed(4)
		p.IssuedAt = at.UTC().Format(time.RFC3339)
		out.Parts = append(out.Parts, p)
	}
	return out, rows.Err()
}

func scanJob(row scanner) (ServiceOrder, error) {
	var j ServiceOrder
	var parts, labour, charged decimal.Decimal
	var promised *time.Time
	var received time.Time
	var closed *time.Time
	if err := row.Scan(&j.ID, &j.JobNo, &j.Kind, &j.Status, &j.CustomerID,
		&j.Customer, &j.SerialID, &j.SerialNo, &j.VariantID, &j.Product,
		&j.FaultReported, &j.Diagnosis, &j.WorkDone, &parts, &labour,
		&charged, &promised, &received, &closed,
		&j.Currency); err != nil {
		return ServiceOrder{}, err
	}
	j.PartsCost = parts.StringFixed(2)
	j.LabourCost = labour.StringFixed(2)
	j.Charged = charged.StringFixed(2)
	j.ReceivedAt = received.UTC().Format(time.RFC3339)
	if promised != nil {
		j.PromisedOn = promised.Format("2006-01-02")
	}
	if closed != nil {
		j.ClosedAt = closed.UTC().Format(time.RFC3339)
	}
	j.Parts = []ServicePart{}
	return j, nil
}
