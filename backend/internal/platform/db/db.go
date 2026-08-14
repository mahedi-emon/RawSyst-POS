// Package db owns the PostgreSQL connection pool and the tenant-scoping
// contract that makes row-level security work.
//
// # The isolation contract
//
// Blueprint A3 requires tenant identity to be enforced "at the database query
// layer, not just the frontend". Every tenant-scoped table therefore carries
// tenant_id and an RLS policy of the form:
//
//	USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
//
// That policy only works if app.tenant_id is actually set. This package is the
// single place that sets it, which is why all query paths run through Tx or
// Query below rather than touching the pool directly.
//
// A query that forgets its tenant filter returns zero rows instead of another
// tenant's data. That is the difference between isolation enforced by the
// engine and isolation enforced by developer discipline.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Pool wraps pgxpool with the tenant-scoping contract.
type Pool struct {
	pool *pgxpool.Pool
}

// Open connects and verifies the connection.
func Open(ctx context.Context, cfg config.DB) (*Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database dsn: %w", err)
	}
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Pool{pool: pool}, nil
}

// Close releases every connection.
func (p *Pool) Close() { p.pool.Close() }

// Health reports whether the database is reachable.
func (p *Pool) Health(ctx context.Context) error { return p.pool.Ping(ctx) }

// Raw exposes the underlying pool. Reserved for migrations and platform
// maintenance that legitimately operate outside any tenant. Business code must
// not use it: doing so bypasses row-level security.
func (p *Pool) Raw() *pgxpool.Pool { return p.pool }

// Tx runs fn inside a transaction with app.tenant_id bound to the caller's
// tenant, then commits. Any error rolls back.
//
// The setting is transaction-scoped (set_config's third argument is true), so
// it is discarded when the transaction ends and cannot leak to the next
// borrower of the same pooled connection.
func (p *Pool) Tx(ctx context.Context, fn func(pgx.Tx) error) error {
	a := actor.From(ctx)
	if !a.IsAuthenticated() {
		return errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}
	// A Super Admin operates on the platform control plane, which is not
	// tenant-scoped. Business tables remain unreachable to them because their
	// tenant id is nil and no row carries a nil tenant.
	return p.txWithTenant(ctx, a.TenantID, fn)
}

// TxAsTenant runs fn scoped to an explicit tenant. It exists for background
// workers, which act on behalf of a tenant without a signed-in user.
//
// It must never be reachable from an HTTP handler: that would let a caller
// choose their own tenant, which is precisely the attack QA gate M8 tests.
func (p *Pool) TxAsTenant(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return errs.New(errs.CodeInternal, "A tenant is required for this operation.")
	}
	return p.txWithTenant(ctx, tenantID, fn)
}

func (p *Pool) txWithTenant(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable, "The database is not reachable right now.")
	}
	defer func() {
		// Rollback after a successful commit is a no-op, so this is safe
		// unconditionally and guarantees no transaction is ever left open.
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return errs.Wrap(err, errs.CodeInternal, "Could not establish the tenant context.")
	}

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(err, errs.CodeInternal, "Could not save your changes.")
	}
	return nil
}

// TenantIDOf reads back the tenant currently bound to a transaction. Used by
// tests to prove the scoping contract holds.
func TenantIDOf(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	var raw string
	if err := tx.QueryRow(ctx,
		`SELECT current_setting('app.tenant_id', true)`).Scan(&raw); err != nil {
		return uuid.Nil, err
	}
	if raw == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(raw)
}

// --- error translation -------------------------------------------------

// PostgreSQL error codes we translate into domain errors.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
	pgExclusionViolation  = "23P01"
	pgRaiseException      = "P0001" // our own RAISE EXCEPTION from triggers
)

// Translate converts a driver error into an application error.
//
// Constraint violations are not incidental here: immutability, balance
// enforcement and non-overlapping regulatory rules are all implemented as
// database constraints, so their violations carry real domain meaning.
func Translate(err error, notFoundMsg string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if notFoundMsg == "" {
			notFoundMsg = "That record does not exist."
		}
		return errs.Wrap(err, errs.CodeNotFound, notFoundMsg)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return errs.Wrap(err, errs.CodeConflict, "That record already exists.")
		case pgForeignKeyViolation:
			return errs.Wrap(err, errs.CodeInvalidInput, "A referenced record does not exist.")
		case pgExclusionViolation:
			return errs.Wrap(err, errs.CodeConflict,
				"That would overlap an existing record covering the same period.")
		case pgCheckViolation:
			return errs.Wrap(err, errs.CodeInvalidInput, "That value is not allowed.")
		case pgRaiseException:
			// Triggers raise these deliberately with a message written for the
			// user — immutability and period locks both surface this way.
			return errs.Wrap(err, classifyRaise(pgErr.Message), pgErr.Message)
		}
	}
	return errs.Wrap(err, errs.CodeInternal, "Something went wrong on our side.")
}

func classifyRaise(msg string) errs.Code {
	switch {
	case contains(msg, "immutable"), contains(msg, "cannot be modified"),
		contains(msg, "cannot be deleted"):
		return errs.CodeImmutable
	case contains(msg, "closed period"), contains(msg, "locked period"):
		return errs.CodePeriodClosed
	default:
		return errs.CodeConflict
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0
}

func indexFold(s, substr string) int {
	if substr == "" {
		return 0
	}
	n := len(substr)
	for i := 0; i+n <= len(s); i++ {
		if equalFold(s[i:i+n], substr) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
