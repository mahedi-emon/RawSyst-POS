// Package sync applies work a POS terminal produced offline.
//
// # Three layers of idempotency
//
// A terminal that loses its connection mid-push cannot tell whether the server
// received the batch. It must retry, and retrying must be harmless. Three
// independent mechanisms make that true, and any one of them alone would do —
// which is the point, because the consequence of a double-posted sale is
// duplicated revenue in a tax return.
//
//  1. The batch idempotency key. A retried batch returns its original outcome
//     without reprocessing anything.
//  2. (device_id, seq) unique. The same item from the same device collides.
//  3. entity_uuid unique. The same sale cannot be created twice by any route.
//
// The journal adds a fourth at the accounting layer: a unique index on
// (source_type, source_id, rule_key) means even a bug here cannot post a sale
// twice.
//
// # Order matters, but only within a chain
//
// Invoices must reach ZATCA in ICV order, so an item that would leave a gap is
// held rather than applied. Independent work — a customer edit, a stock count —
// has no such constraint and proceeds regardless. Treating the whole batch as
// ordered would let one bad customer record stall a day of invoices.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Item is one piece of work from a terminal.
type Item struct {
	Seq        int64           `json:"seq"`
	EntityUUID uuid.UUID       `json:"entity_uuid"`
	EntityType string          `json:"entity_type"`
	Payload    json.RawMessage `json:"payload"`
}

// Batch is what a terminal pushes.
type Batch struct {
	DeviceID       uuid.UUID `json:"device_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	Items          []Item    `json:"items"`
}

// ItemResult is the outcome for one item.
type ItemResult struct {
	Seq        int64     `json:"seq"`
	EntityUUID uuid.UUID `json:"entity_uuid"`
	State      string    `json:"state"`
	Error      string    `json:"error,omitempty"`
}

// Result is what the terminal gets back.
//
// Per-item rather than a single status, so the device knows exactly what to
// retry. A batch-level "failed" would force it to resend everything, and
// resending applied work is how duplicates happen in systems that get this
// wrong.
type Result struct {
	BatchID    uuid.UUID    `json:"batch_id"`
	Replayed   bool         `json:"replayed"`
	Applied    int          `json:"applied"`
	Duplicates int          `json:"duplicates"`
	Failed     int          `json:"failed"`
	Blocked    int          `json:"blocked"`
	Items      []ItemResult `json:"items"`

	// The highest contiguous sequence the server holds. The device may discard
	// everything up to here; anything after it is still outstanding.
	Cursor int64 `json:"cursor"`
}

// Applier applies one item to its domain.
//
// Implemented per entity type. Returning ErrAlreadyApplied rather than an error
// lets the engine record a duplicate without treating it as a failure — a
// distinction the device depends on, since duplicates are the expected result
// of a healthy retry.
type Applier interface {
	Apply(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, item Item) error

	// Ordered reports whether this entity type must be applied in sequence.
	// Invoices are; a customer profile edit is not.
	Ordered() bool
}

// ErrAlreadyApplied signals that an item's effect is already present.
var ErrAlreadyApplied = errs.New(errs.CodeConflict, "already applied")

// Engine applies batches.
type Engine struct {
	pool     *db.Pool
	appliers map[string]Applier
}

func NewEngine(pool *db.Pool) *Engine {
	return &Engine{pool: pool, appliers: make(map[string]Applier, 8)}
}

// Register wires an applier to an entity type.
func (e *Engine) Register(entityType string, a Applier) {
	e.appliers[entityType] = a
}

// Push applies a batch and reports the outcome per item.
func (e *Engine) Push(ctx context.Context, tenantID uuid.UUID, batch Batch) (Result, error) {
	if batch.DeviceID == uuid.Nil {
		return Result{}, errs.New(errs.CodeInvalidInput, "The batch does not say which terminal it came from.")
	}
	if batch.IdempotencyKey == "" {
		return Result{}, errs.New(errs.CodeInvalidInput,
			"The batch has no idempotency key, so a retry could not be recognised.")
	}

	// Replay check first. A device that timed out mid-push retries with the
	// same key and must get its original answer, not a second application.
	if prior, found, err := e.priorResult(ctx, tenantID, batch); err != nil {
		return Result{}, err
	} else if found {
		prior.Replayed = true
		return prior, nil
	}

	// Sequence order, whatever order the device serialised them in.
	items := append([]Item(nil), batch.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })

	result := Result{Items: make([]ItemResult, 0, len(items))}
	// Chains that have stalled in this batch. Once an ordered entity type
	// fails, everything after it on the same chain is held rather than applied,
	// because applying it would leave a gap.
	stalled := make(map[string]bool, 2)

	err := e.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var batchID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO sync_batch (tenant_id, device_id, idempotency_key, item_count)
			VALUES ($1,$2,$3,$4) RETURNING id`,
			tenantID, batch.DeviceID, batch.IdempotencyKey, len(items)).Scan(&batchID); err != nil {
			return err
		}
		result.BatchID = batchID

		for _, item := range items {
			r := e.applyOne(ctx, tx, tenantID, batchID, batch.DeviceID, item, stalled)
			result.Items = append(result.Items, r)

			switch r.State {
			case "applied":
				result.Applied++
			case "duplicate":
				result.Duplicates++
			case "blocked":
				result.Blocked++
			default:
				result.Failed++
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE sync_batch
			SET applied = $2, duplicates = $3, failed = $4, completed_at = now()
			WHERE id = $1`,
			batchID, result.Applied, result.Duplicates,
			result.Failed+result.Blocked); err != nil {
			return err
		}

		cursor, err := advanceCursor(ctx, tx, tenantID, batch.DeviceID)
		if err != nil {
			return err
		}
		result.Cursor = cursor
		return nil
	})
	if err != nil {
		return Result{}, db.Translate(err, "")
	}
	return result, nil
}

// applyOne applies a single item, recording its outcome.
//
// Each item runs in a savepoint so one failure does not abort the batch. A
// terminal that produced 500 invoices and one malformed customer edit must land
// the 500.
func (e *Engine) applyOne(
	ctx context.Context, tx pgx.Tx,
	tenantID, batchID, deviceID uuid.UUID, item Item, stalled map[string]bool,
) ItemResult {
	res := ItemResult{Seq: item.Seq, EntityUUID: item.EntityUUID}

	applier, known := e.appliers[item.EntityType]
	if !known {
		res.State = "failed"
		res.Error = fmt.Sprintf(
			"This terminal sent a %q, which this server version does not understand. "+
				"It may be running a newer app version than the server.", item.EntityType)
		recordOrFail(ctx, e, tx, tenantID, batchID, deviceID, item, &res)
		return res
	}

	// An ordered entity type whose chain has already stalled in this batch must
	// wait: applying it would leave a gap, and a gap is the exact signal ZATCA's
	// tamper detection looks for.
	if applier.Ordered() && stalled[item.EntityType] {
		res.State = "blocked"
		res.Error = "An earlier item of this type has not been applied. " +
			"Applying this one would leave a gap in the sequence."
		recordOrFail(ctx, e, tx, tenantID, batchID, deviceID, item, &res)
		return res
	}

	// A savepoint, so a failure rolls back only this item.
	spName := fmt.Sprintf("sp_%d", item.Seq)
	if _, err := tx.Exec(ctx, "SAVEPOINT "+spName); err != nil {
		res.State = "failed"
		res.Error = "Could not isolate this item for processing."
		return res
	}

	err := applier.Apply(ctx, tx, tenantID, deviceID, item)
	switch {
	case err == nil:
		if _, e2 := tx.Exec(ctx, "RELEASE SAVEPOINT "+spName); e2 != nil {
			res.State = "failed"
			res.Error = "Could not finalise this item."
			return res
		}
		res.State = "applied"

	case isAlreadyApplied(err):
		// The expected outcome of a healthy retry, not a problem. Releasing
		// rather than rolling back keeps any bookkeeping the applier did.
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+spName)
		res.State = "duplicate"

	default:
		if _, e2 := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+spName); e2 != nil {
			res.State = "failed"
			res.Error = "Could not roll back a failed item."
			return res
		}
		res.State = "failed"
		res.Error = userMessage(err)
		if applier.Ordered() {
			stalled[item.EntityType] = true
		}
	}

	recordOrFail(ctx, e, tx, tenantID, batchID, deviceID, item, &res)
	return res
}

// recordOrFail writes an item's verdict and downgrades it if the write fails.
//
// A verdict the queue did not keep is worse than a plain failure: the device
// would be told its sale landed while nothing recorded that it had, and the
// cursor would move past an item the server cannot account for.
func recordOrFail(
	ctx context.Context, e *Engine, tx pgx.Tx,
	tenantID, batchID, deviceID uuid.UUID, item Item, res *ItemResult,
) {
	if err := e.recordItem(ctx, tx, tenantID, batchID, deviceID, item, *res); err != nil {
		res.State = "failed"
		res.Error = "This item was processed but its outcome could not be " +
			"recorded, so it has been left outstanding."
	}
}

// recordItem writes the audit row for an item.
//
// A conflict on (device_id, seq) or entity_uuid means this item was already
// recorded by an earlier batch, so the outcome becomes "duplicate". That is the
// second idempotency layer catching what the first did not: a device that
// resent old items under a NEW batch key.
func (e *Engine) recordItem(
	ctx context.Context, tx pgx.Tx,
	tenantID, batchID, deviceID uuid.UUID, item Item, res ItemResult,
) error {
	// A settled row is history and must never be rewritten: once an item is
	// applied or recognised as a duplicate, that verdict is the idempotency
	// record and a later batch cannot overturn it.
	//
	// An UNSETTLED row is a different matter. This used to be DO NOTHING, so an
	// item that failed or was blocked kept that state forever — even after a
	// corrected retry applied it successfully. The sale landed, the queue row
	// still said failed, the cursor never advanced past it, and the terminal
	// would have retried the same sale for the rest of its life while the
	// server quietly recognised it as a duplicate every time.
	_, err := tx.Exec(ctx, `
		INSERT INTO sync_item
		  (tenant_id, batch_id, device_id, seq, entity_uuid, entity_type,
		   payload, state, error, applied_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
		        CASE WHEN $8 = 'applied' THEN now() ELSE NULL END)
		ON CONFLICT (device_id, seq) DO UPDATE SET
		  batch_id   = EXCLUDED.batch_id,
		  entity_uuid = EXCLUDED.entity_uuid,
		  payload    = EXCLUDED.payload,
		  state      = EXCLUDED.state,
		  error      = EXCLUDED.error,
		  applied_at = EXCLUDED.applied_at
		WHERE sync_item.state IN ('pending', 'blocked', 'failed')`,
		tenantID, batchID, deviceID, item.Seq, item.EntityUUID,
		item.EntityType, item.Payload, res.State, nullIfEmpty(res.Error))

	// Returned rather than swallowed. A write that fails here leaves the
	// engine's in-memory verdict disagreeing with what the queue records, and
	// the device is told an outcome the server did not keep.
	return err
}

// priorResult returns a previous batch's outcome if this key has been seen.
func (e *Engine) priorResult(
	ctx context.Context, tenantID uuid.UUID, batch Batch,
) (Result, bool, error) {
	var result Result
	found := false

	err := e.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var batchID uuid.UUID
		var applied, duplicates, failed int
		err := tx.QueryRow(ctx, `
			SELECT id, applied, duplicates, failed FROM sync_batch
			WHERE device_id = $1 AND idempotency_key = $2`,
			batch.DeviceID, batch.IdempotencyKey).
			Scan(&batchID, &applied, &duplicates, &failed)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil
			}
			return err
		}
		found = true
		result = Result{
			BatchID: batchID, Applied: applied,
			Duplicates: duplicates, Failed: failed,
		}

		rows, err := tx.Query(ctx, `
			SELECT seq, entity_uuid, state, coalesce(error, '')
			FROM sync_item WHERE batch_id = $1 ORDER BY seq`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ItemResult
			if err := rows.Scan(&r.Seq, &r.EntityUUID, &r.State, &r.Error); err != nil {
				return err
			}
			result.Items = append(result.Items, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		return tx.QueryRow(ctx,
			`SELECT coalesce(last_applied_seq, 0) FROM device_sync_cursor WHERE device_id = $1`,
			batch.DeviceID).Scan(&result.Cursor)
	})
	if err != nil && err != pgx.ErrNoRows {
		return Result{}, false, db.Translate(err, "")
	}
	return result, found, nil
}

// advanceCursor moves the device cursor to the highest CONTIGUOUS applied
// sequence.
//
// Contiguous, not highest. If 1-3 and 5 landed but 4 did not, the cursor is 3:
// the device must keep 4 and 5 and resend them. Reporting 5 would tell the
// terminal it may discard 4, and that invoice would be lost — which QA gate M3
// tests for directly.
func advanceCursor(
	ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID,
) (int64, error) {
	var contiguous, highest int64

	// DISTINCT is load-bearing. A seq can settle more than once — a resent
	// batch records the same sequence again as a duplicate — and without it the
	// row numbering shifts against the sequence, so `seq = rn` stops matching
	// and the cursor freezes at the first repeated number. The device would
	// then never be told it may discard anything.
	err := tx.QueryRow(ctx, `
		WITH settled AS (
		  SELECT DISTINCT seq FROM sync_item
		  WHERE device_id = $1 AND state IN ('applied', 'duplicate')
		),
		numbered AS (
		  SELECT seq, row_number() OVER (ORDER BY seq) AS rn FROM settled
		)
		SELECT coalesce(max(seq) FILTER (WHERE seq = rn), 0)
		FROM numbered`, deviceID).Scan(&contiguous)
	if err != nil {
		return 0, err
	}

	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(seq), 0) FROM sync_item WHERE device_id = $1`,
		deviceID).Scan(&highest); err != nil {
		return 0, err
	}

	// The outstanding counts are computed HERE rather than left to the trigger
	// on sync_item.
	//
	// That trigger updates the cursor row, and on a device's very first batch
	// there is no cursor row to update: it is created below, after every item
	// has already been recorded. So the first batch's counts were silently lost
	// and a terminal was told it had nothing outstanding when its whole queue
	// had failed — which is the one moment a cashier most needs the truth.
	_, err = tx.Exec(ctx, `
		INSERT INTO device_sync_cursor
		  (device_id, tenant_id, last_applied_seq, highest_seen_seq, last_sync_at,
		   pending_count, blocked_count, failed_count, oldest_unsettled_at)
		SELECT $1, $2, $3, $4, now(),
		       coalesce(count(*) FILTER (WHERE state = 'pending'), 0),
		       coalesce(count(*) FILTER (WHERE state = 'blocked'), 0),
		       coalesce(count(*) FILTER (WHERE state = 'failed'), 0),
		       min(created_at) FILTER (WHERE state IN ('pending','blocked','failed'))
		FROM sync_item WHERE device_id = $1
		ON CONFLICT (device_id) DO UPDATE SET
		  last_applied_seq = GREATEST(device_sync_cursor.last_applied_seq, EXCLUDED.last_applied_seq),
		  highest_seen_seq = GREATEST(device_sync_cursor.highest_seen_seq, EXCLUDED.highest_seen_seq),
		  last_sync_at = now(),
		  pending_count = EXCLUDED.pending_count,
		  blocked_count = EXCLUDED.blocked_count,
		  failed_count  = EXCLUDED.failed_count,
		  oldest_unsettled_at = EXCLUDED.oldest_unsettled_at`,
		deviceID, tenantID, contiguous, highest)
	if err != nil {
		return 0, err
	}
	return contiguous, nil
}

func isAlreadyApplied(err error) bool {
	if e := errs.As(err); e != nil {
		return e.Message == ErrAlreadyApplied.Message
	}
	return false
}

// userMessage extracts a message safe to return to the terminal.
func userMessage(err error) string {
	if e := errs.As(err); e != nil {
		return e.Message
	}
	// An unrecognised failure must not leak internals to a device, which may be
	// in a shop with staff reading the screen.
	return "This item could not be applied. It has been kept and can be retried."
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Health reports what a device still owes the server.
type Health struct {
	Pending         int64      `json:"pending"`
	Blocked         int64      `json:"blocked"`
	Failed          int64      `json:"failed"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
	GapSize         int64      `json:"gap_size"`
}

// HealthFor reads a device's outstanding sync state.
func (e *Engine) HealthFor(
	ctx context.Context, tenantID, deviceID uuid.UUID,
) (Health, error) {
	var h Health
	// Reads device_sync_depth rather than counting sync_item directly. The
	// depth view is backed by the cursor, which carries no business content, so
	// the same call serves the Super Admin compliance watch without giving the
	// platform operator a route to what a tenant sold.
	err := e.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT pending, blocked, failed, oldest_unsettled_at, gap_size
			FROM device_sync_depth($1)`, deviceID).
			Scan(&h.Pending, &h.Blocked, &h.Failed, &h.OldestPendingAt, &h.GapSize)
	})
	if err != nil {
		return Health{}, db.Translate(err, "")
	}
	return h, nil
}
