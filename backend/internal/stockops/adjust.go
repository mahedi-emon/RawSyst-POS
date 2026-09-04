package stockops

// Correcting stock, writing it off, and counting it.
//
// # One document, one journal entry, whichever way it points
//
// A voucher can contain lines in both directions — a count of a rail of shirts
// finds two extra mediums and three fewer larges — and the Inventory account
// moves by the NET of them. So the value of every line is worked out first and
// a single entry posts the total: `inventory.writeoff` when value was lost,
// `inventory.writeon` when it was found, and nothing at all when they cancel
// out, which is not a failure to post but an accurate statement that the
// company is no poorer than it was.
//
// Netting also gives the idempotency for free. `PostByRule` keys on
// (source type, source id, rule key), so the voucher id is the key and posting
// the same voucher twice finds the entry already there.
//
// # What a found unit is worth
//
// Stock leaving is valued by the costing engine, which knows exactly what it
// held. Stock appearing has no such history — nobody bought these units, they
// were simply there — and something has to decide what they are worth.
//
// `ConsumeFIFO` already faced this question, for units sold that the layers did
// not have, and answered it: "the most recent known cost, which is the closest
// available estimate". The same answer is used here, for the same reason, and
// with the same preference for erring high — valuing found stock at zero would
// flatter next month's margin when it sells.
//
// # A count surplus settles an old sale's guess
//
// Units appearing in a count are units arriving into the cost layers, which
// makes it a receipt in every sense the costing engine cares about. So it
// settles outstanding shortfalls exactly as a goods receipt does, and posts the
// correction through the same variance rules. Skipping that would leave C13's
// valuation deducting a shortfall that the found stock has just covered, and
// the tie-out would break by its value.

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

// The three kinds of voucher, matching 0079's CHECK.
const (
	KindAdjustment = "adjustment"
	KindWastage    = "wastage"
	KindCount      = "count"
)

// Reasons a voucher may give, per kind. B4 requires a category on wastage; the
// list is short on purpose, because a free-text reason box produces a hundred
// spellings of "damaged" and no report anybody can run.
var reasonsByKind = map[string]map[string]bool{
	KindWastage: {
		"damaged":      true,
		"expired":      true,
		"stolen":       true,
		"sample":       true,
		"internal_use": true,
		"other":        true,
	},
	KindAdjustment: {
		"correction": true,
		"found":      true,
		"data_entry": true,
		"other":      true,
	},
	KindCount: {"count": true},
}

func reasonList(kind string) string {
	out := make([]string, 0, len(reasonsByKind[kind]))
	for r := range reasonsByKind[kind] {
		out = append(out, r)
	}
	return strings.Join(sorted(out), ", ")
}

// NewAdjustment is a correction or a write-off being recorded.
type NewAdjustment struct {
	// UUID is assigned by the caller, so recording the same voucher twice
	// because a response was lost gives back the first one rather than writing
	// the damage off against stock a second time.
	UUID uuid.UUID

	WarehouseID uuid.UUID
	Kind        string
	Reason      string
	Note        string

	Lines []NewAdjustmentLine
}

// NewAdjustmentLine is one product and how much it moved by.
type NewAdjustmentLine struct {
	VariantID uuid.UUID

	// Delta is signed and stated by the person: −3 for three broken, +2 for two
	// that were on the shelf all along. It is a DIFFERENCE rather than a total,
	// which is what separates an adjustment from a count.
	Delta decimal.Decimal
}

// Adjustment is a recorded voucher.
type Adjustment struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"adjustment_no"`
	Kind     string    `json:"kind"`
	Reason   string    `json:"reason"`
	Note     string    `json:"note,omitempty"`
	Status   string    `json:"status"`
	Location string    `json:"location"`

	// Value is what the voucher moved the Inventory account by, signed:
	// negative for value destroyed, positive for value found. Zero for a count
	// that came out exactly right.
	Value    string `json:"value"`
	Currency string `json:"currency"`

	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at"`
	PostedAt  string `json:"posted_at,omitempty"`

	Lines []AdjustmentLine `json:"lines,omitempty"`

	// AlreadyRecorded marks a replay: the caller sent a voucher that had been
	// recorded before, and this is the original.
	AlreadyRecorded bool `json:"already_recorded,omitempty"`
}

// AdjustmentLine is one product's part of a voucher.
type AdjustmentLine struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Product   string    `json:"product"`

	SystemQty  string `json:"system_qty"`
	CountedQty string `json:"counted_qty,omitempty"`
	Delta      string `json:"delta"`
	Value      string `json:"value"`

	// MovedWhileCounting is set when the system quantity changed between the
	// count sheet being opened and being posted — somebody sold one mid-count.
	// B4 asks for discrepancies to be auto-flagged and this is the discrepancy
	// that is not a discrepancy.
	MovedWhileCounting bool `json:"moved_while_counting,omitempty"`
}

// RecordAdjustment writes a correction or a write-off and posts it.
func (s *Service) RecordAdjustment(
	ctx context.Context, scope Scope, in NewAdjustment,
) (Adjustment, error) {
	if in.UUID == uuid.Nil {
		return Adjustment{}, errs.New(errs.CodeInvalidInput,
			"A stock voucher must carry an identifier so a retry does not "+
				"record it twice.")
	}
	if in.Kind != KindAdjustment && in.Kind != KindWastage {
		return Adjustment{}, errs.New(errs.CodeInvalidInput,
			"A voucher recorded in one step is either an adjustment or a "+
				"wastage. A count is opened, filled in, and then posted.")
	}
	if !reasonsByKind[in.Kind][in.Reason] {
		return Adjustment{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a reason for a %s. Choose one of: %s.",
			in.Reason, in.Kind, reasonList(in.Kind))
	}
	if len(strings.TrimSpace(in.Note)) < 3 {
		return Adjustment{}, errs.Validation(
			"Say what happened, in a few words.").
			WithField("note",
				"A stock correction nobody explained is how shrinkage gets buried.")
	}
	if len(in.Lines) == 0 {
		return Adjustment{}, errs.New(errs.CodeInvalidInput,
			"Say which products moved.")
	}

	seen := map[uuid.UUID]bool{}
	for i, l := range in.Lines {
		if l.Delta.IsZero() {
			return Adjustment{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not change anything. Remove it, or say by how "+
					"much the stock is out.", i+1)
		}
		// A wastage is destruction. One that added stock would post a write-off
		// against a rise in inventory, which balances and is a lie.
		if in.Kind == KindWastage && l.Delta.IsPositive() {
			return Adjustment{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d adds stock, which is not wastage. Record it as an "+
					"adjustment instead.", i+1)
		}
		if seen[l.VariantID] {
			return Adjustment{}, errs.New(errs.CodeInvalidInput,
				"The same product appears twice. Put the whole difference on "+
					"one line.")
		}
		seen[l.VariantID] = true
	}

	var out Adjustment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// A replay of a voucher that already landed.
		var exists bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM stock_adjustment WHERE id = $1 AND company_id = $2)`,
			in.UUID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			read, e := s.readAdjustment(ctx, tx, scope, in.UUID)
			if e != nil {
				return e
			}
			read.AlreadyRecorded = true
			out = read
			return nil
		}

		if _, e := locationForWrite(ctx, tx, scope.CompanyID, in.WarehouseID); e != nil {
			return e
		}

		number, e := claimAdjustmentNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		// Written as a draft and posted at the end of this transaction, not
		// the start. 0079's freeze trigger refuses every UPDATE to a row that
		// is already posted, and attaching the journal entry is an UPDATE — so
		// a voucher born posted could never record which entry it produced.
		//
		// Which is the better arrangement anyway: `posted` now means the work
		// finished, rather than that it was attempted.
		now := time.Now().UTC()
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_adjustment
			  (id, tenant_id, company_id, warehouse_id, adjustment_no,
			   kind, reason, note, status, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft',$9)`,
			in.UUID, scope.TenantID, scope.CompanyID, in.WarehouseID, number,
			in.Kind, in.Reason, strings.TrimSpace(in.Note),
			scope.UserID); e != nil {
			return db.Translate(e, "That stock voucher could not be recorded.")
		}

		for _, l := range in.Lines {
			onHand, e := inventory.OnHandAt(ctx, tx, l.VariantID, in.WarehouseID)
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO stock_adjustment_line
				  (tenant_id, adjustment_id, variant_id,
				   system_qty_open, system_qty_posted, delta)
				VALUES ($1,$2,$3,$4,$4,$5)`,
				scope.TenantID, in.UUID, l.VariantID, onHand, l.Delta); e != nil {
				return db.Translate(e, "That stock voucher could not be recorded.")
			}
		}

		if e := s.applyAndPost(ctx, tx, scope, in.UUID, in.WarehouseID,
			in.Kind, now); e != nil {
			return e
		}
		if e := markPosted(ctx, tx, in.UUID, scope.UserID, now); e != nil {
			return e
		}

		read, e := s.readAdjustment(ctx, tx, scope, in.UUID)
		if e != nil {
			return e
		}
		out = read
		return nil
	})
	return out, err
}

// movedLine is one line after the stock has actually moved.
type movedLine struct {
	variantID uuid.UUID
	delta     decimal.Decimal
	value     decimal.Decimal
}

// applyAndPost moves the stock every line describes and posts the net.
//
// The order matters and is not arbitrary: the stock moves first and the journal
// is posted from what the costing engine actually recorded, never from a figure
// computed alongside it. That is the P34 rule — a caller doing its own
// arithmetic rounds a second time, and the second rounding is what parts the
// stock report from the balance sheet.
func (s *Service) applyAndPost(
	ctx context.Context, tx pgx.Tx, scope Scope,
	adjID, warehouseID uuid.UUID, kind string, when time.Time,
) error {
	rows, err := tx.Query(ctx, `
		SELECT variant_id, delta FROM stock_adjustment_line
		WHERE adjustment_id = $1 AND delta IS NOT NULL AND delta <> 0
		ORDER BY variant_id`, adjID)
	if err != nil {
		return err
	}
	var lines []movedLine
	for rows.Next() {
		var l movedLine
		if err := rows.Scan(&l.variantID, &l.delta); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 {
		// A count that found everything exactly right. Nothing moved, nothing
		// posts, and the voucher stands as the record that the count happened.
		return nil
	}

	// The reason the movement carries. 0020 reserved all three.
	reason := kind
	if kind == KindAdjustment {
		reason = "adjustment"
	}

	// Locking every variant before touching any of them, in a stable order, for
	// the reason `sales.Finalize` does it: two vouchers against overlapping
	// products would otherwise deadlock, and the loser is a person who has just
	// finished counting a rail.
	ids := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.variantID)
	}
	if err := inventory.LockStock(ctx, tx, warehouseID, ids); err != nil {
		return err
	}

	net := decimal.Zero        // signed: what the Inventory account moves by
	correction := decimal.Zero // shortfalls this voucher settled

	for i := range lines {
		l := &lines[i]

		if l.delta.IsNegative() {
			res, err := inventory.Consume(ctx, tx, inventory.Issue{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				VariantID: l.variantID, WarehouseID: warehouseID,
				Qty:        l.delta.Neg(),
				Reason:     reason,
				SourceType: "stock_adjustment", SourceID: &adjID,
				Note: "Stock voucher",
			})
			if err != nil {
				return err
			}
			l.value = res.TotalCost.Neg()
			net = net.Add(l.value)
			continue
		}

		unitCost, err := estimateUnitCost(ctx, tx, scope.CompanyID, l.variantID, warehouseID)
		if err != nil {
			return err
		}
		posted, err := inventory.Receive(ctx, tx, inventory.Receipt{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			VariantID: l.variantID, WarehouseID: warehouseID,
			Qty: l.delta, UnitCost: unitCost,
			Reason:     reason,
			SourceType: "stock_adjustment", SourceID: &adjID,
			Note: "Stock voucher",
		})
		if err != nil {
			return err
		}
		l.value = posted
		net = net.Add(posted)

		// These units are on the shelf now, so an earlier sale that went below
		// zero can stop guessing what it cost. Same call, same reason, as a
		// goods receipt — see the package note.
		settled, err := inventory.SettleShortfalls(ctx, tx,
			scope.CompanyID, l.variantID, warehouseID)
		if err != nil {
			return err
		}
		correction = correction.Add(settled.Adjustment)
	}

	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`UPDATE stock_adjustment_line SET value = $3
			 WHERE adjustment_id = $1 AND variant_id = $2`,
			adjID, l.variantID, l.value); err != nil {
			return err
		}
	}

	var country, number string
	if err := tx.QueryRow(ctx, `
		SELECT c.country, a.adjustment_no
		FROM stock_adjustment a JOIN company c ON c.id = a.company_id
		WHERE a.id = $1`, adjID).Scan(&country, &number); err != nil {
		return err
	}

	entryID, err := s.postNet(ctx, tx, scope, adjID, country, when, net,
		memoFor(kind, number))
	if err != nil {
		return err
	}
	if entryID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE stock_adjustment SET journal_entry_id = $2 WHERE id = $1`,
			adjID, *entryID); err != nil {
			return err
		}
	}

	// The correction to earlier sales, as its own entry rather than folded into
	// the voucher's. Two different facts: what this voucher moved, and what an
	// earlier sale was mis-costed by.
	return s.postCostCorrection(ctx, tx, scope, adjID, country, when, correction,
		"Cost correction on "+number)
}

// postNet books the value the voucher moved, in whichever direction it moved,
// and hands back the entry it made so the caller can attach it in the same
// statement that freezes the voucher.
func (s *Service) postNet(
	ctx context.Context, tx pgx.Tx, scope Scope, adjID uuid.UUID,
	country string, when time.Time, net decimal.Decimal, memo string,
) (*uuid.UUID, error) {
	if net.IsZero() {
		// The lines cancelled out, or a count came back exact. The company is
		// no poorer and no richer, and an entry for zero would be noise in a
		// ledger somebody has to read.
		return nil, nil
	}

	// Rule 10 destroys value; rule 10a finds it. Two rules rather than one
	// signed amount, for the reason 0052 and 0025 both give: a negative debit
	// where a credit belongs is unreadable in a trial balance.
	ruleKey := "inventory.writeon"
	if net.IsNegative() {
		ruleKey = "inventory.writeoff"
	}

	result, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: when, SourceType: "stock_adjustment", SourceID: adjID,
		RuleKey: ruleKey, PostedBy: &scope.UserID, Memo: memo,
	}, country, accounting.Transaction{
		Amounts: accounting.Amounts{"value": net.Abs()},
	})
	if err != nil {
		return nil, err
	}
	entryID := result.EntryID
	return &entryID, nil
}

// markPosted is the single statement that freezes a voucher. Everything it
// records has already happened; after it, 0079's trigger refuses any further
// change to the row.
func markPosted(
	ctx context.Context, tx pgx.Tx, adjID, userID uuid.UUID, when time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE stock_adjustment
		SET status = 'posted', posted_by = $2, posted_at = $3
		WHERE id = $1`, adjID, userID, when)
	return err
}

// postCostCorrection books what found stock put right on earlier sales that
// went below zero, exactly as a goods receipt does.
func (s *Service) postCostCorrection(
	ctx context.Context, tx pgx.Tx, scope Scope, adjID uuid.UUID,
	country string, when time.Time, value decimal.Decimal, memo string,
) error {
	if value.IsZero() {
		return nil
	}
	ruleKey := "inventory.variance"
	if value.IsNegative() {
		ruleKey = "inventory.variance_favourable"
	}
	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: when, SourceType: "stock_adjustment", SourceID: adjID,
		RuleKey: ruleKey, PostedBy: &scope.UserID, Memo: memo,
	}, country, accounting.Transaction{
		Amounts: accounting.Amounts{"variance": value.Abs()},
	})
	return err
}

// estimateUnitCost is what a unit that appeared out of nowhere is worth.
//
// The answer `ConsumeFIFO` already gives for units it does not have: the most
// recent known cost. Under weighted average that is the pool's own average,
// which is exactly the figure the next sale would be charged at; under FIFO and
// standard costing it is the newest open layer.
//
// The fallbacks descend deliberately. This warehouse's stock, then the
// company's — the same product in the back room cost what it cost — then the
// variant's standard cost, which is a stated intention rather than a
// measurement but is better than nothing. Zero is the last resort and is the
// dangerous answer: it books found stock at no value, which flatters the margin
// on the day it eventually sells.
func estimateUnitCost(
	ctx context.Context, tx pgx.Tx, companyID, variantID, warehouseID uuid.UUID,
) (decimal.Decimal, error) {
	var cost decimal.Decimal

	// The pool, if this company averages.
	var method string
	if err := tx.QueryRow(ctx,
		`SELECT costing_method::text FROM company WHERE id = $1`, companyID).
		Scan(&method); err != nil {
		return decimal.Zero, err
	}

	if inventory.Method(method) == inventory.MethodWAC {
		// coalesce rather than a CASE returning NULL: an empty pool is the
		// ordinary case here — this whole function exists because the stock is
		// not there — and a NULL would have to be handled as a scan failure
		// rather than as the answer "nothing known", which it is.
		err := tx.QueryRow(ctx, `
			SELECT coalesce(
			         CASE WHEN qty_on_hand > 0 THEN total_value / qty_on_hand END,
			         0)
			FROM stock_valuation
			WHERE variant_id = $1 AND warehouse_id = $2`,
			variantID, warehouseID).Scan(&cost)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, err
		}
		if err == nil && cost.IsPositive() {
			return cost.Round(4), nil
		}
	}

	// The newest open layer here, then anywhere in the company.
	//
	// `received_at`, not `created_at`: the column has been called `received_at`
	// since the table was created, and `cost_layer_fifo_idx` is on
	// (variant_id, warehouse_id, received_at) precisely so the first of these
	// two reads an index. The wrong name made every one-step adjustment fail
	// with a 500 whenever it had to fall back this far to find a cost — which
	// is exactly the case a shop hits first, because a variant with no receipt
	// yet has no layer to short-circuit on.
	// Each query carries its OWN arguments. Sharing one three-argument list
	// across both meant the first — which has two placeholders — was sent an
	// extra parameter, and pgx refuses that with "expected 2 arguments, got 3"
	// before the query ever reaches the database.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT unit_cost FROM cost_layer
		  WHERE variant_id = $1 AND warehouse_id = $2 AND qty_remaining > 0
		  ORDER BY received_at DESC LIMIT 1`,
			[]any{variantID, warehouseID}},
		{`SELECT unit_cost FROM cost_layer
		  WHERE variant_id = $1 AND company_id = $2
		  ORDER BY received_at DESC LIMIT 1`,
			[]any{variantID, companyID}},
	} {
		err := tx.QueryRow(ctx, q.sql, q.args...).Scan(&cost)
		if err == nil && cost.IsPositive() {
			return cost.Round(4), nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, err
		}
	}

	var standard *decimal.Decimal
	if err := tx.QueryRow(ctx,
		`SELECT cost_standard FROM variant WHERE id = $1`, variantID).
		Scan(&standard); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, errs.New(errs.CodeNotFound,
				"That product is not in this business's catalogue.")
		}
		return decimal.Zero, err
	}
	if standard != nil && standard.IsPositive() {
		return standard.Round(4), nil
	}
	return decimal.Zero, nil
}

func claimAdjustmentNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n string
	err := tx.QueryRow(ctx, `SELECT claim_stock_adjustment_no($1)`, companyID).Scan(&n)
	return n, err
}

func memoFor(kind, number string) string {
	switch kind {
	case KindWastage:
		return "Stock written off " + number
	case KindCount:
		return "Stock count " + number
	default:
		return "Stock adjustment " + number
	}
}

func sorted(in []string) []string {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
	return in
}
