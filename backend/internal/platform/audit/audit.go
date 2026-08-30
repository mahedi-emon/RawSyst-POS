// Package audit is the append-only record of who did what (blueprint D4).
//
// D4 fixes six fields and this package writes exactly those: who, what, when,
// where, before, after. "When" is the database default so a caller cannot
// back-date one, and migration 0003 puts a trigger on the table that refuses
// every UPDATE and DELETE — including from an Owner, which D4 is explicit
// about, "to preserve evidentiary integrity".
//
// # Why it is one package rather than a helper in each caller
//
// Three packages were writing this table with three hand-written INSERTs. They
// agreed today; the third one already omitted `actor_label`, which is the field
// that exists so the trail survives a user being deleted, and a log with the
// name missing on some rows is a log somebody has to apologise for in an audit.
//
// # Writing happens inside the caller's transaction, always
//
// An action that commits without its audit record, and an audit record for an
// action that rolled back, are both worse than no log at all: either one makes
// the log unreliable as evidence, and unreliable evidence is what a defendant's
// counsel is looking for.
//
// So `Write` takes a `pgx.Tx` and never opens its own.
package audit

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Entry is one thing that happened.
type Entry struct {
	TenantID *uuid.UUID
	ActorID  *uuid.UUID

	// ActorLabel is denormalised on purpose: it has to survive the user row
	// being deleted, so the trail does not become a list of missing people.
	// Callers that have only an id should use `LabelFor`.
	ActorLabel string

	// Action is a verb in the product's own vocabulary — `period_closed`,
	// `person_suspended`, `tenant_provisioned`. Snake case, past tense, because
	// every row in this table is something that already happened.
	Action string

	EntityType string
	EntityID   *uuid.UUID

	IP     string
	Device string

	Before map[string]any
	After  map[string]any
}

// Write appends one entry inside the caller's transaction.
//
// Never log a credential. `Before` and `After` are written by callers who must
// not put a password, token or key in them; the field masking in the logging
// package does not apply here, because this is a database row rather than a log
// line and nothing redacts it on the way out.
func Write(ctx context.Context, tx pgx.Tx, e Entry) error {
	before, err := marshal(e.Before)
	if err != nil {
		return err
	}
	after, err := marshal(e.After)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log
		  (tenant_id, actor_id, actor_label, action, entity_type, entity_id,
		   ip, device_label, before_value, after_value)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.TenantID, e.ActorID, nullIfEmpty(e.ActorLabel), e.Action,
		e.EntityType, e.EntityID, nullIfEmpty(e.IP), nullIfEmpty(e.Device),
		nullOrJSON(before), nullOrJSON(after))
	return err
}

// LabelFor reads the name to record against an action.
//
// Falls back to the email, and then to nothing rather than to an error: a
// missing label is a worse trail, and a failed audit write would roll back the
// action it was recording, which is very much worse.
func LabelFor(ctx context.Context, tx pgx.Tx, userID uuid.UUID) string {
	var label string
	_ = tx.QueryRow(ctx, `
		SELECT coalesce(nullif(btrim(full_name), ''), email)
		FROM app_user WHERE id = $1`, userID).Scan(&label)
	return label
}

// --- reading --------------------------------------------------------------

// Record is one entry as a screen reads it.
type Record struct {
	At     string `json:"occurred_at"`
	Actor  string `json:"actor,omitempty"`
	Action string `json:"action"`

	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id,omitempty"`

	IP     string `json:"ip,omitempty"`
	Device string `json:"device,omitempty"`

	// Before and After are the raw JSON the writer recorded. Handed to the
	// screen as-is: this is evidence, and summarising it here would mean the
	// reader sees a rendering of the record rather than the record.
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// Query narrows the trail.
type Query struct {
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	ActorID    *uuid.UUID
	From, To   *time.Time
	Limit      int
}

// Read returns the trail, newest first.
//
// Scoped by row-level security to the caller's tenant, like everything else —
// there is no company dimension on `audit_log`, deliberately: an action such as
// creating a company does not belong to one.
func Read(ctx context.Context, tx pgx.Tx, q Query) ([]Record, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := tx.Query(ctx, `
		SELECT to_char(occurred_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
		       coalesce(actor_label, ''), action, entity_type,
		       coalesce(entity_id::text, ''),
		       coalesce(host(ip), ''), coalesce(device_label, ''),
		       before_value, after_value
		FROM audit_log
		WHERE ($1 = '' OR action = $1)
		  AND ($2 = '' OR entity_type = $2)
		  AND ($3::uuid IS NULL OR entity_id = $3)
		  AND ($4::uuid IS NULL OR actor_id = $4)
		  AND ($5::timestamptz IS NULL OR occurred_at >= $5)
		  AND ($6::timestamptz IS NULL OR occurred_at < $6)
		ORDER BY occurred_at DESC, id DESC
		LIMIT $7`,
		q.Action, q.EntityType, q.EntityID, q.ActorID, q.From, q.To, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var r Record
		var before, after []byte
		if err := rows.Scan(&r.At, &r.Actor, &r.Action, &r.EntityType,
			&r.EntityID, &r.IP, &r.Device, &before, &after); err != nil {
			return nil, err
		}
		r.Before = json.RawMessage(before)
		r.After = json.RawMessage(after)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Actions lists the verbs that actually appear in this tenant's trail, so a
// filter offers what is there rather than a fixed list that goes stale every
// time a module is added.
func Actions(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- the trail as a service -----------------------------------------------

// Service reads the trail.
//
// Deliberately read-only. Writing goes through `Write` inside whichever
// transaction is doing the thing being recorded, because an audit row that
// commits separately from the action it describes is a row that can exist
// without its action, or fail to exist with it.
type Service struct {
	pool *db.Pool
}

// NewService builds the reader.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Trail returns the records a query matches, and the verbs that appear in this
// tenant's log at all.
//
// The second is not a convenience: a filter offering a fixed list of actions
// goes stale the day a module is added, and one offering nothing makes a person
// guess at the vocabulary.
func (s *Service) Trail(
	ctx context.Context, tenantID uuid.UUID, q Query,
) ([]Record, []string, error) {
	var records []Record
	var actions []string
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if records, e = Read(ctx, tx, q); e != nil {
			return e
		}
		actions, e = Actions(ctx, tx)
		return e
	})
	return records, actions, err
}

// --- plumbing -------------------------------------------------------------

func marshal(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal, "Could not record this action.")
	}
	return b, nil
}

func nullOrJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
