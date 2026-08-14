package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one versioned schema change.
type Migration struct {
	Version int
	Name    string
	SQL     string
	Hash    string
}

// LoadMigrations reads and orders the embedded migrations.
//
// Filenames are NNNN_snake_case_name.sql. The numeric prefix is the version
// and must be unique and gapless-friendly (gaps are tolerated, duplicates are
// not — two developers numbering the same is a merge conflict, not a silent
// reordering).
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var out []Migration
	seen := map[int]string{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		idx := strings.Index(base, "_")
		if idx <= 0 {
			return nil, fmt.Errorf("migration %q: expected NNNN_name.sql", e.Name())
		}
		version, err := strconv.Atoi(base[:idx])
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version prefix: %w", e.Name(), err)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %q and %q",
				version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)

		out = append(out, Migration{
			Version: version,
			Name:    base[idx+1:],
			SQL:     string(body),
			Hash:    hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

const migrationTableDDL = `
CREATE TABLE IF NOT EXISTS schema_migration (
  version     INTEGER     PRIMARY KEY,
  name        TEXT        NOT NULL,
  hash        TEXT        NOT NULL,
  applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  duration_ms INTEGER     NOT NULL
)`

// Migrate applies every pending migration in order.
//
// Each migration runs inside its own transaction, so a failure leaves the
// schema at the last complete version rather than half-applied. PostgreSQL
// supports transactional DDL, which is what makes this safe.
//
// Already-applied migrations are verified by hash. An edited migration is a
// hard error: the database and the source tree would otherwise silently
// disagree about what the schema is.
func (p *Pool) Migrate(ctx context.Context, log *slog.Logger) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}

	if _, err := p.pool.Exec(ctx, migrationTableDDL); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	applied := map[int]string{}
	rows, err := p.pool.Query(ctx, `SELECT version, hash FROM schema_migration`)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var h string
		if err := rows.Scan(&v, &h); err != nil {
			rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[v] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate applied migrations: %w", err)
	}

	pending := 0
	for _, m := range migrations {
		if prevHash, ok := applied[m.Version]; ok {
			if prevHash != m.Hash {
				return fmt.Errorf(
					"migration %04d_%s was modified after it was applied "+
						"(recorded %s, found %s). Add a new migration instead of editing history",
					m.Version, m.Name, prevHash[:12], m.Hash[:12])
			}
			continue
		}

		start := time.Now()
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %04d: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
		}
		elapsed := time.Since(start)
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migration (version, name, hash, duration_ms)
			 VALUES ($1, $2, $3, $4)`,
			m.Version, m.Name, m.Hash, elapsed.Milliseconds()); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return fmt.Errorf("record migration %04d: %w", m.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %04d: %w", m.Version, err)
		}

		pending++
		if log != nil {
			log.Info("migration applied",
				slog.Int("version", m.Version),
				slog.String("name", m.Name),
				slog.Duration("took", elapsed))
		}
	}

	if log != nil {
		if pending == 0 {
			log.Info("schema up to date", slog.Int("version", maxVersion(migrations)))
		} else {
			log.Info("migrations complete",
				slog.Int("applied", pending),
				slog.Int("version", maxVersion(migrations)))
		}
	}
	return nil
}

func maxVersion(ms []Migration) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].Version
}
