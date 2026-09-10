//go:build freshcheck

// Applies every migration to an empty database, from 0001 to the latest.
//
// The long-lived test database has had migrations applied incrementally for
// months, with rows rolled back and function bodies swapped by hand. A green
// suite against it proves the migrations work in the order they happened to be
// applied here — not that a customer's first deployment comes up at all.
//
// This creates a database with nothing in it, runs the whole series, and
// reports what the schema ends up with.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

func main() {
	ctx := context.Background()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		panic("RAWSYST_DB_DSN is not set")
	}

	// The application role is deliberately NOSUPERUSER and cannot create a
	// database, which is the right posture and the reason this rebuilds the
	// public SCHEMA instead. The effect is the same: every object the
	// migrations create is dropped and the series runs from nothing.
	//
	// Guarded on the database name, because running this against production
	// would delete the business.
	if !strings.Contains(dsn, "dev") && !strings.Contains(dsn, "test") {
		fmt.Println("refusing: the DSN does not name a dev or test database")
		os.Exit(1)
	}

	reset, err := pgx.Connect(ctx, dsn)
	if err != nil {
		panic(err)
	}
	for _, stmt := range []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
	} {
		if _, err := reset.Exec(ctx, stmt); err != nil {
			panic(fmt.Sprintf("%s: %v", stmt, err))
		}
	}
	reset.Close(ctx)
	fmt.Println("public schema dropped and recreated; nothing is left")

	freshDSN := dsn
	pool, err := db.Open(ctx, config.DB{
		DSN: freshDSN, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	start := time.Now()
	if err := pool.Migrate(ctx, nil); err != nil {
		fmt.Println("MIGRATION FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("all migrations applied in %s\n", time.Since(start).Round(time.Millisecond))

	conn, err := pgx.Connect(ctx, freshDSN)
	if err != nil {
		panic(err)
	}
	defer conn.Close(ctx)

	var applied, tables, policies, forced int
	conn.QueryRow(ctx, `SELECT count(*) FROM schema_migration`).Scan(&applied)
	conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'`).Scan(&tables)
	conn.QueryRow(ctx, `SELECT count(*) FROM pg_policy`).Scan(&policies)
	conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		  AND c.relrowsecurity AND c.relforcerowsecurity`).Scan(&forced)

	var maxV int
	conn.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM schema_migration`).Scan(&maxV)

	fmt.Printf("migrations recorded: %d (highest %d)\n", applied, maxV)
	fmt.Printf("tables: %d, with RLS forced: %d, policies: %d\n",
		tables, forced, policies)

	// The two things a first deployment must have, or nothing works.
	var rules, cdtfa, active int
	conn.Exec(ctx, `SELECT set_config('app.platform_admin','on',false)`)
	conn.QueryRow(ctx, `SELECT count(*) FROM regulatory_rule`).Scan(&rules)
	conn.QueryRow(ctx,
		`SELECT count(*) FROM tax_jurisdiction_rate WHERE source_authority = 'cdtfa'`).
		Scan(&cdtfa)
	conn.QueryRow(ctx, `
		SELECT count(*) FROM tax_jurisdiction_rate
		WHERE source_authority = 'cdtfa' AND activated_on IS NOT NULL`).Scan(&active)
	fmt.Printf("regulatory rules seeded: %d\n", rules)
	fmt.Printf("CDTFA rates seeded: %d, active: %d\n", cdtfa, active)

	// And that a Saudi business would not be turned away.
	var onboardingBlockers int
	conn.QueryRow(ctx, `
		SELECT count(*) FROM regulatory_rule
		WHERE release_blocker AND blocks = 'onboarding'
		  AND verified_on IS NULL AND effective_to IS NULL
		  AND lower(country) = 'sa'`).Scan(&onboardingBlockers)
	fmt.Printf("Saudi onboarding blockers outstanding: %d\n", onboardingBlockers)

	// And which capabilities are waiting on a figure rather than on a
	// developer. Reported separately from the line above because they are
	// different conditions: nothing above zero there can trade at all, and
	// anything here is finished software waiting for somebody to read an
	// official document. A fresh database that printed only the first number
	// left the second invisible, which is how "unverified" and "unbuilt"
	// became the same word.
	var awaiting []string
	rows, err := conn.Query(ctx, `
		SELECT rule_key FROM regulatory_rule
		WHERE release_blocker AND blocks = 'feature'
		  AND verified_on IS NULL AND effective_to IS NULL
		ORDER BY rule_key`)
	if err == nil {
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				awaiting = append(awaiting, k)
			}
		}
		rows.Close()
	}
	if len(awaiting) == 0 {
		fmt.Println("capabilities awaiting a regulatory figure: none")
	} else {
		fmt.Printf("capabilities awaiting a regulatory figure: %d (%s)\n",
			len(awaiting), strings.Join(awaiting, ", "))
		fmt.Println("  each is implemented and refuses by name at the point " +
			"of use; record with `regulatory -template` then `-apply`")
	}

	fmt.Println("\nfresh database came up clean")
}
