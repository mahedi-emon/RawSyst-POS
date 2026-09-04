// Light production cost tracking (blueprint C3.1).
//
// A garment retailer buys cloth, has it stitched, packs it, and sells a shirt.
// Without this the cloth leaves stock as a write-off, the stitching and the
// packaging are two unrelated expenses, and the shirt appears in stock at a cost
// somebody guessed — so the margin on every locally-made item is wrong and
// nobody can say by how much.
//
// # The scope boundary is the design
//
// C3.1 is emphatic that this is cost TRACKING and not a manufacturing module:
// no bill of materials, no production orders, no work orders, no
// work-in-progress, no routing, no variance analysis. A batch names what went
// in and what came out, and the step between them is a division. Anything more
// would be the module the Blueprint explicitly says not to build.
//
// So a batch is recorded when it is FINISHED. There is no state machine and
// nothing is ever half-made, which is why there is no WIP account: nothing is
// in progress.
//
// # The arithmetic
//
//	unit cost = (material cost + labour + packaging) / quantity produced
//
// The material cost is never taken from the request. It comes back from the
// costing engine as the components are consumed, so the value leaving inventory
// is the value inventory says it held — under FIFO, weighted average or
// standard cost, whichever the company uses. A caller who could state the
// material cost could state a margin.
package stockops

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
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// consumedInput is a component that has been costed, held until the batch row
// it belongs to exists.
type consumedInput struct {
	variantID uuid.UUID
	qty       decimal.Decimal
	cost      decimal.Decimal
}

// ProductionInput is one component going into a batch.
type ProductionInput struct {
	VariantID uuid.UUID
	Qty       decimal.Decimal
}

// NewProductionBatch is a finished run being recorded.
type NewProductionBatch struct {
	UUID        uuid.UUID
	VariantID   uuid.UUID
	WarehouseID uuid.UUID
	Qty         decimal.Decimal

	Inputs []ProductionInput

	// The two costs C3.1 names beside the materials. Money, not stock.
	Labour    decimal.Decimal
	Packaging decimal.Decimal

	PaidFrom   string
	ProducedOn time.Time
	Note       string
}

// ProductionBatch is a finished run as a screen reads it.
type ProductionBatch struct {
	ID        uuid.UUID `json:"id"`
	BatchNo   string    `json:"batch_no"`
	VariantID uuid.UUID `json:"variant_id"`
	Variant   string    `json:"variant,omitempty"`
	Qty       string    `json:"qty_produced"`

	MaterialCost  string `json:"material_cost"`
	LabourCost    string `json:"labour_cost"`
	PackagingCost string `json:"packaging_cost"`
	TotalCost     string `json:"total_cost"`
	UnitCost      string `json:"unit_cost"`

	Currency   string            `json:"currency"`
	PaidFrom   string            `json:"paid_from"`
	ProducedOn string            `json:"produced_on"`
	Note       string            `json:"note,omitempty"`
	Inputs     []ProductionUsage `json:"inputs"`
}

// ProductionUsage is one component and what it actually cost.
type ProductionUsage struct {
	VariantID uuid.UUID `json:"variant_id"`
	Variant   string    `json:"variant,omitempty"`
	Qty       string    `json:"qty"`
	Cost      string    `json:"cost"`
}

// RecordProduction costs a finished batch and puts it into stock.
func (s *Service) RecordProduction(
	ctx context.Context, scope Scope, in NewProductionBatch,
) (ProductionBatch, error) {
	if err := validateProduction(in); err != nil {
		return ProductionBatch{}, err
	}

	var out ProductionBatch
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var existing uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id FROM production_batch
			WHERE company_id = $1 AND uuid = $2`,
			scope.CompanyID, in.UUID).Scan(&existing)
		if e == nil {
			var readErr error
			out, readErr = s.readProduction(ctx, tx, scope.CompanyID, existing)
			return readErr
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		// Every component and the finished item, locked up front in a
		// deterministic order — the same reason a sale does it. Two batches
		// sharing two components in opposite orders would otherwise deadlock.
		ids := make([]uuid.UUID, 0, len(in.Inputs)+1)
		for _, c := range in.Inputs {
			ids = append(ids, c.VariantID)
		}
		ids = append(ids, in.VariantID)
		if e := inventory.LockStock(ctx, tx, in.WarehouseID, ids); e != nil {
			return e
		}

		// Read once: it is a property of the company, not of a component, and
		// re-reading it per line would be a query per component for an answer
		// that cannot change inside one transaction.
		var policyText string
		if e := tx.QueryRow(ctx,
			`SELECT negative_stock_policy::text FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&policyText); e != nil {
			return e
		}
		policy := inventory.NegativeStockPolicy(policyText)

		batchID := uuid.New()
		materials := decimal.Zero
		consumed := make([]consumedInput, 0, len(in.Inputs))

		for _, c := range in.Inputs {
			// The cost comes back from the costing engine. Never from the
			// request: a caller who could state the material cost could state
			// a margin.
			result, e := inventory.Consume(ctx, tx, inventory.Issue{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: c.VariantID, WarehouseID: in.WarehouseID,
				Qty: c.Qty, Reason: "production_out",
				SourceType: "production_batch", SourceID: &batchID,
			})
			if e != nil {
				return e
			}
			// The company's negative-stock policy decides, exactly as it does
			// for a sale. Producing from cloth that is not there is the same
			// question as selling a shirt that is not there.
			if e := inventory.CheckAvailability(policy, result,
				"this component"); e != nil {
				return e
			}

			materials = materials.Add(result.TotalCost)
			// Held rather than written here: the input rows reference the
			// batch, and the batch row does not exist until its costs are
			// known — which is only after this loop has run.
			consumed = append(consumed, consumedInput{
				variantID: c.VariantID, qty: c.Qty, cost: result.TotalCost,
			})
		}

		added := in.Labour.Add(in.Packaging)
		total := materials.Add(added)
		// Banker-free division: the unit cost carries four places like every
		// other cost in this ledger, and the receipt multiplies it back so the
		// value posted is the value stored.
		unit := total.Div(in.Qty).Round(4)

		posted, e := inventory.Receive(ctx, tx, inventory.Receipt{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			VariantID: in.VariantID, WarehouseID: in.WarehouseID,
			Qty: in.Qty, UnitCost: unit,
			Reason:     "production_in",
			SourceType: "production_batch", SourceID: &batchID,
			Note: in.Note,
		})
		if e != nil {
			return e
		}

		var batchNo string
		if e := tx.QueryRow(ctx, `SELECT claim_stock_adjustment_no($1)`,
			scope.CompanyID).Scan(&batchNo); e != nil {
			return db.Translate(e, "A batch number could not be issued.")
		}
		batchNo = "PRD-" + strings.TrimPrefix(batchNo, "ADJ-")

		// The value actually posted into inventory, not the value computed:
		// rounding at the valuation is what the ledger must agree with, and
		// differencing it here is what keeps C13's tie-out exact.
		entry, e := s.postProduction(ctx, tx, scope, country, currency,
			batchID, batchNo, in, posted, materials, added)
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO production_batch
			  (id, tenant_id, company_id, uuid, batch_no, variant_id,
			   warehouse_id, qty_produced, material_cost, labour_cost,
			   packaging_cost, total_cost, unit_cost, currency, paid_from,
			   produced_on, note, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
			        $16::date,nullif(btrim($17),''),$18,$19)`,
			batchID, scope.TenantID, scope.CompanyID, in.UUID, batchNo,
			in.VariantID, in.WarehouseID, in.Qty, materials, in.Labour,
			in.Packaging, total, unit, currency, in.PaidFrom, in.ProducedOn,
			in.Note, entry, scope.UserID); e != nil {
			return db.Translate(e, "That batch could not be recorded.")
		}

		for _, c := range consumed {
			if _, e := tx.Exec(ctx, `
				INSERT INTO production_batch_input
				  (tenant_id, batch_id, variant_id, qty, cost)
				VALUES ($1,$2,$3,$4,$5)`,
				scope.TenantID, batchID, c.variantID, c.qty, c.cost); e != nil {
				return e
			}
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "production_batch_recorded",
			EntityType: "production_batch", EntityID: &batchID,
			After: map[string]any{
				"batch_no": batchNo, "qty": in.Qty.String(),
				"material_cost": materials.String(),
				"labour_cost":   in.Labour.String(),
				"packaging":     in.Packaging.String(),
				"unit_cost":     unit.String(),
			},
		}); e != nil {
			return e
		}

		var readErr error
		out, readErr = s.readProduction(ctx, tx, scope.CompanyID, batchID)
		return readErr
	})
	return out, db.Translate(err, "")
}

func validateProduction(in NewProductionBatch) error {
	if !in.Qty.IsPositive() {
		return errs.Validation("Say how many were produced.").
			WithField("qty_produced",
				"The unit cost is the batch's cost divided by this.")
	}
	if len(in.Inputs) == 0 && !in.Labour.Add(in.Packaging).IsPositive() {
		return errs.Validation(
			"A batch needs something in it: components used, or labour and " +
				"packaging paid for.")
	}
	if in.Labour.IsNegative() || in.Packaging.IsNegative() {
		return errs.Validation("A production cost cannot be negative.")
	}
	if in.PaidFrom != "cash" && in.PaidFrom != "bank" {
		return errs.Validation(
			"Say whether the labour and packaging were paid from cash or "+
				"from the bank.").WithField("paid_from", "cash or bank")
	}
	if in.WarehouseID == uuid.Nil || in.VariantID == uuid.Nil {
		return errs.Validation(
			"Say what was made and which stock location it went into.")
	}
	for i, c := range in.Inputs {
		if c.VariantID == uuid.Nil {
			return errs.Newf(errs.CodeInvalidInput,
				"Component %d does not name an item.", i+1)
		}
		if !c.Qty.IsPositive() {
			return errs.Newf(errs.CodeInvalidInput,
				"Component %d must have a quantity above zero.", i+1)
		}
		if c.VariantID == in.VariantID {
			return errs.New(errs.CodeInvalidInput,
				"A batch cannot be made out of itself.")
		}
	}
	return nil
}

// postProduction writes the three legs a finished batch produces.
//
// The value DEBITED to inventory is what `Receive` actually posted, not what
// the unit cost multiplied out to. Valuation rounds, and the ledger has to
// agree with the valuation rather than with the arithmetic that preceded it —
// which is what keeps C13's tie-out exact.
//
// The material leg is credited at the cost the components actually left at, so
// the two inventory legs net to exactly the work done.
func (s *Service) postProduction(
	ctx context.Context, tx pgx.Tx, scope Scope, country, currency string,
	batchID uuid.UUID, batchNo string, in NewProductionBatch,
	posted, materials, added decimal.Decimal,
) (*uuid.UUID, error) {
	if !posted.IsPositive() && !added.IsPositive() {
		// A batch of nothing: no components with value and no cost added.
		// Nothing to post, and an entry with two empty legs would be noise in
		// the journal rather than a record of anything.
		return nil, nil
	}

	role, err := s.moneyRole(ctx, tx, scope.CompanyID, in.PaidFrom)
	if err != nil {
		return nil, err
	}

	result, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date:       in.ProducedOn,
		SourceType: "production_batch", SourceID: batchID,
		RuleKey:      "production.batch",
		Currency:     currency,
		BaseCurrency: currency,
		FXRate:       decimal.NewFromInt(1),
		Memo:         "Production " + batchNo,
		PostedBy:     &scope.UserID,
	}, country, accounting.Transaction{
		Amounts: accounting.Amounts{
			"output":    posted,
			"materials": materials,
		},
		Groups: map[string]accounting.Group{
			"payment_account": {{Role: role, Amount: added}},
		},
	})
	if err != nil {
		return nil, err
	}
	return &result.EntryID, nil
}

// moneyRole maps 'cash' or 'bank' onto the posting role that names the account.
func (s *Service) moneyRole(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, paidFrom string,
) (string, error) {
	switch paidFrom {
	case "cash":
		return "cash", nil
	case "bank":
		return "bank", nil
	}
	return "", errs.New(errs.CodeInvalidInput,
		"Production costs are paid from cash or from the bank.")
}

// readProduction assembles one batch.
func (s *Service) readProduction(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (ProductionBatch, error) {
	var b ProductionBatch
	var note *string
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.batch_no, p.variant_id, v.sku, p.qty_produced::text,
		       p.material_cost::text, p.labour_cost::text,
		       p.packaging_cost::text, p.total_cost::text, p.unit_cost::text,
		       p.currency, p.paid_from,
		       to_char(p.produced_on, 'YYYY-MM-DD'), p.note
		FROM production_batch p
		JOIN variant v ON v.id = p.variant_id
		WHERE p.id = $1 AND p.company_id = $2`, id, companyID).
		Scan(&b.ID, &b.BatchNo, &b.VariantID, &b.Variant, &b.Qty,
			&b.MaterialCost, &b.LabourCost, &b.PackagingCost, &b.TotalCost,
			&b.UnitCost, &b.Currency, &b.PaidFrom, &b.ProducedOn, &note)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductionBatch{}, errs.New(errs.CodeNotFound,
			"That production batch was not found.")
	}
	if err != nil {
		return ProductionBatch{}, err
	}
	if note != nil {
		b.Note = *note
	}

	rows, err := tx.Query(ctx, `
		SELECT i.variant_id, v.sku, i.qty::text, i.cost::text
		FROM production_batch_input i
		JOIN variant v ON v.id = i.variant_id
		WHERE i.batch_id = $1
		ORDER BY v.sku`, id)
	if err != nil {
		return ProductionBatch{}, err
	}
	defer rows.Close()

	b.Inputs = []ProductionUsage{}
	for rows.Next() {
		var u ProductionUsage
		if err := rows.Scan(&u.VariantID, &u.Variant, &u.Qty,
			&u.Cost); err != nil {
			return ProductionBatch{}, err
		}
		b.Inputs = append(b.Inputs, u)
	}
	return b, rows.Err()
}

// ProductionBatchByID reads one.
func (s *Service) ProductionBatchByID(
	ctx context.Context, scope Scope, id uuid.UUID,
) (ProductionBatch, error) {
	var out ProductionBatch
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var e error
		out, e = s.readProduction(ctx, tx, scope.CompanyID, id)
		return e
	})
	return out, db.Translate(err, "")
}

// ProductionBatches lists them, newest first.
func (s *Service) ProductionBatches(
	ctx context.Context, scope Scope, limit int,
) ([]ProductionBatch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []ProductionBatch{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id FROM production_batch
			WHERE company_id = $1
			ORDER BY produced_on DESC, batch_no DESC
			LIMIT $2`, scope.CompanyID, limit)
		if e != nil {
			return e
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		for _, id := range ids {
			b, e := s.readProduction(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return nil
	})
	return out, db.Translate(err, "")
}
