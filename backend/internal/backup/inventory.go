// What a database contains, in enough detail to prove a copy of it is the same.
//
// # Why a list of ten tables was not enough
//
// The first version of this counted `tenant`, `company`, `app_user`, `product`,
// `customer`, `supplier`, `invoice`, `journal_entry`, `regulatory_rule` and
// `audit_log`, on the reasoning that a dump which restores those is a dump
// somebody can trade on. That reasoning is wrong in a way that matters: it can
// only ever find the failures somebody thought of. Payroll, stock movements,
// tax returns, wallet balances, delivery, approvals and every table added after
// the list was written are outside it, and a restore that lost one of them
// would pass with a green tick.
//
// So this asks the database what it contains, rather than being told. Every
// base table in `public`, counted. Every index, constraint, sequence, function,
// trigger, extension, view and policy, counted and named. The list needs no
// maintenance when a migration adds a table, which is the only property that
// makes it true a year from now.
//
// # One scan, three answers
//
// Counting rows is the expensive part: it reads the table. So a table that
// carries `tenant_id` is counted with a GROUP BY rather than a bare `count(*)`,
// which yields the table's total, the per-business breakdown and the
// per-company breakdown from the SAME scan. Doing it in three passes would read
// the database three times to learn what one pass already knew.
//
// # Why the per-business numbers exist at all
//
// This product is multi-tenant and a dump is every business at once. The
// failure that a total row count cannot see is a restore where one business
// came back and another did not, or where the same total is spread differently
// across them. That is not a hypothetical shape of bug — it is what a partial
// restore, an interrupted `pg_restore`, or a dump taken with row-level security
// still in force actually looks like, and the last of those is silent.
package backup

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Inventory is everything a verification compares.
//
// Deliberately made of counts and names, never of content. It travels in the
// manifest, which is stored beside the dump and is readable by anybody who can
// read the bucket, so a field here that carried a customer's name would be a
// data leak with a checksum on it.
type Inventory struct {
	Database string `json:"database"`
	Size     int64  `json:"size_bytes"`

	// ServerVersion is the Postgres the dump came out of. A restore into an
	// older major version fails in ways that are hard to read; knowing this
	// before starting turns that into one sentence.
	ServerVersion string `json:"postgres_version"`

	// SchemaVersion is from the migration ledger rather than a build constant:
	// what matters on restore is what the DATABASE is at.
	SchemaVersion int `json:"schema_version"`

	// Tables, named and counted. `Rows` has an entry for every name in
	// `Tables`, so a missing key is itself a finding.
	Tables []string         `json:"tables"`
	Rows   map[string]int64 `json:"row_counts"`

	// TenantRows and CompanyRows are totals across every table that carries the
	// column, keyed by id. A business that vanished shows up as a missing key
	// rather than as arithmetic somebody has to do.
	TenantRows  map[string]int64 `json:"tenant_row_totals"`
	CompanyRows map[string]int64 `json:"company_row_totals"`

	// Sequences and their positions. A restore that brought the rows back and
	// left the sequences at 1 is a database that issues a duplicate invoice
	// number on its first write, which is worse than an obvious failure because
	// it happens after everybody has gone home believing it worked.
	Sequences map[string]int64 `json:"sequence_positions"`

	// Extensions by name and version. `pgcrypto` missing means
	// `gen_random_uuid()` is missing, which means nothing can be inserted.
	Extensions map[string]string `json:"extensions"`

	// Named rather than counted, because a count that is one short does not say
	// which one, and "which one" is the whole of the next question.
	Policies []string `json:"rls_policies"`
	Views    []string `json:"views"`
	MatViews []string `json:"materialized_views"`

	Indexes     int `json:"index_count"`
	PrimaryKeys int `json:"primary_key_count"`
	ForeignKeys int `json:"foreign_key_count"`
	Uniques     int `json:"unique_constraint_count"`
	Checks      int `json:"check_constraint_count"`
	Functions   int `json:"function_count"`
	Triggers    int `json:"trigger_count"`

	// RLSEnabled and RLSForced are counts of base tables. Forced is the one
	// that matters: enabled without forced means the owning role bypasses it,
	// and this product's whole isolation guarantee rests on forced.
	RLSEnabled int `json:"rls_enabled_tables"`
	RLSForced  int `json:"rls_forced_tables"`

	// TookMS is how long the inventory itself took, so an operator watching a
	// backup get slower knows which half is getting slower.
	TookMS int64 `json:"inventory_ms"`
}

// TableCount is the number of base tables, which is what a manifest records.
func (i Inventory) TableCount() int { return len(i.Tables) }

// querier is a connection or a transaction.
//
// The inventory is taken inside the transaction that exported the snapshot
// `pg_dump` is using, and on a plain connection when a restored copy is being
// checked. Both satisfy this; naming it here is what lets the same code do
// both without either caller having to pretend to be the other.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// takeInventory reads everything about a database, on the caller's connection.
//
// On the CALLER's connection deliberately, and on purpose inside the caller's
// transaction where there is one: `Run` exports a repeatable-read snapshot,
// hands it to `pg_dump`, and takes the inventory in the same transaction, so
// the numbers recorded in the manifest describe exactly the bytes in the dump.
// Counting on a second connection afterwards would count a database that had
// moved on, and every sale rung up during the backup would read as a
// verification failure.
func takeInventory(ctx context.Context, conn querier) (Inventory, error) {
	started := time.Now()
	inv := Inventory{
		Rows:        map[string]int64{},
		TenantRows:  map[string]int64{},
		CompanyRows: map[string]int64{},
		Sequences:   map[string]int64{},
		Extensions:  map[string]string{},
	}

	if err := conn.QueryRow(ctx, `
		SELECT current_database(), pg_database_size(current_database()),
		       current_setting('server_version')`).
		Scan(&inv.Database, &inv.Size, &inv.ServerVersion); err != nil {
		return inv, errs.Wrap(err, errs.CodeInternal,
			"The database could not describe itself.")
	}

	// The migration ledger. A database with no `schema_migration` is not this
	// product's database, and saying so here is kinder than a restore that
	// half-works.
	if err := conn.QueryRow(ctx, `
		SELECT coalesce(max(version), 0) FROM schema_migration`).
		Scan(&inv.SchemaVersion); err != nil {
		return inv, errs.Wrap(err, errs.CodeInternal,
			"The schema version could not be read. Has cmd/migrate run "+
				"against this database?")
	}

	// Which tables exist, and which of them carry the columns that make a row
	// belong to somebody. Read once, up front, so the counting loop below is a
	// loop over facts rather than a loop of catalogue queries.
	type shape struct {
		name            string
		tenant, company bool
	}
	rows, err := conn.Query(ctx, `
		SELECT c.relname,
		       bool_or(a.attname = 'tenant_id'),
		       bool_or(a.attname = 'company_id')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attribute a
		  ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		GROUP BY c.relname
		ORDER BY c.relname`)
	if err != nil {
		return inv, errs.Wrap(err, errs.CodeInternal,
			"The table list could not be read.")
	}
	var shapes []shape
	for rows.Next() {
		var s shape
		if err := rows.Scan(&s.name, &s.tenant, &s.company); err != nil {
			rows.Close()
			return inv, errs.Wrap(err, errs.CodeInternal,
				"The table list could not be read.")
		}
		shapes = append(shapes, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return inv, errs.Wrap(err, errs.CodeInternal,
			"The table list could not be read.")
	}
	if len(shapes) == 0 {
		return inv, errs.New(errs.CodeInvalidInput,
			"The database has no tables in `public`. There is nothing here to "+
				"back up, and recording an empty backup would be worse than "+
				"recording none.")
	}

	for _, s := range shapes {
		inv.Tables = append(inv.Tables, s.name)
		if err := countTable(ctx, conn, s.name, s.tenant, s.company, &inv); err != nil {
			return inv, err
		}
	}
	sort.Strings(inv.Tables)

	if err := countObjects(ctx, conn, &inv); err != nil {
		return inv, err
	}

	inv.TookMS = time.Since(started).Milliseconds()
	return inv, nil
}

// countTable reads one table once and attributes what it finds.
//
// The table name comes from the catalogue and never from input, and it is
// quoted regardless — a product that adds a table called `order` should get a
// working backup rather than a syntax error at three in the morning.
func countTable(
	ctx context.Context, conn querier,
	table string, hasTenant, hasCompany bool, inv *Inventory,
) error {
	quoted := `"` + table + `"`

	switch {
	case hasTenant && hasCompany:
		return scanGrouped(ctx, conn, `
			SELECT coalesce(tenant_id::text, ''), coalesce(company_id::text, ''),
			       count(*) FROM `+quoted+` GROUP BY 1, 2`, table, inv)

	case hasTenant:
		return scanGrouped(ctx, conn, `
			SELECT coalesce(tenant_id::text, ''), '', count(*)
			FROM `+quoted+` GROUP BY 1`, table, inv)

	case hasCompany:
		return scanGrouped(ctx, conn, `
			SELECT '', coalesce(company_id::text, ''), count(*)
			FROM `+quoted+` GROUP BY 2`, table, inv)

	default:
		var n int64
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM `+quoted).Scan(&n); err != nil {
			return errs.Wrap(err, errs.CodeInternal,
				table+" could not be counted.")
		}
		inv.Rows[table] = n
		return nil
	}
}

func scanGrouped(
	ctx context.Context, conn querier, query, table string, inv *Inventory,
) error {
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			table+" could not be counted.")
	}
	defer rows.Close()

	var total int64
	for rows.Next() {
		var tenant, company string
		var n int64
		if err := rows.Scan(&tenant, &company, &n); err != nil {
			return errs.Wrap(err, errs.CodeInternal,
				table+" could not be counted.")
		}
		total += n
		// The empty key is a row that belongs to no business — a platform
		// record, a shared reference table. Kept out of the per-business
		// totals rather than filed under "", which would create a business
		// with no id that a restore would then have to match.
		if tenant != "" {
			inv.TenantRows[tenant] += n
		}
		if company != "" {
			inv.CompanyRows[company] += n
		}
	}
	if err := rows.Err(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			table+" could not be counted.")
	}
	inv.Rows[table] = total
	return nil
}

// countObjects reads everything that is not a row.
//
// A dump that restores every row into a schema with no foreign keys is a
// database that will accept an order for a customer who does not exist, and it
// will do it quietly. These are the checks that say the SHAPE came back, and
// they are cheap: all of them are catalogue reads.
func countObjects(ctx context.Context, conn querier, inv *Inventory) error {
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM pg_indexes WHERE schemaname = 'public'),
		  (SELECT count(*) FROM pg_proc p
		     JOIN pg_namespace n ON n.oid = p.pronamespace
		    WHERE n.nspname = 'public'),
		  (SELECT count(*) FROM pg_trigger t
		     JOIN pg_class c ON c.oid = t.tgrelid
		     JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = 'public' AND NOT t.tgisinternal),
		  (SELECT count(*) FROM pg_class c
		     JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = 'public' AND c.relkind = 'r'
		      AND c.relrowsecurity),
		  (SELECT count(*) FROM pg_class c
		     JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = 'public' AND c.relkind = 'r'
		      AND c.relforcerowsecurity)`).
		Scan(&inv.Indexes, &inv.Functions, &inv.Triggers,
			&inv.RLSEnabled, &inv.RLSForced); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The database objects could not be counted.")
	}

	// Constraints, split by kind. One query and a switch, rather than five
	// queries that would each pay the same join.
	rows, err := conn.Query(ctx, `
		SELECT c.contype::text, count(*)
		FROM pg_constraint c
		JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = 'public'
		GROUP BY c.contype`)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The constraints could not be counted.")
	}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return errs.Wrap(err, errs.CodeInternal,
				"The constraints could not be counted.")
		}
		switch kind {
		case "p":
			inv.PrimaryKeys = n
		case "f":
			inv.ForeignKeys = n
		case "u":
			inv.Uniques = n
		case "c":
			inv.Checks = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The constraints could not be counted.")
	}

	// Sequences and where they stand. `last_value` is NULL until a sequence has
	// been used, which is different from zero and is recorded as zero here
	// only because an unused sequence and a sequence at position zero are the
	// same thing to a restore.
	seqRows, err := conn.Query(ctx, `
		SELECT sequencename, coalesce(last_value, 0)
		FROM pg_sequences WHERE schemaname = 'public'`)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The sequences could not be read.")
	}
	for seqRows.Next() {
		var name string
		var last int64
		if err := seqRows.Scan(&name, &last); err != nil {
			seqRows.Close()
			return errs.Wrap(err, errs.CodeInternal,
				"The sequences could not be read.")
		}
		inv.Sequences[name] = last
	}
	seqRows.Close()
	if err := seqRows.Err(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The sequences could not be read.")
	}

	extRows, err := conn.Query(ctx,
		`SELECT extname, extversion FROM pg_extension ORDER BY extname`)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The extensions could not be read.")
	}
	for extRows.Next() {
		var name, version string
		if err := extRows.Scan(&name, &version); err != nil {
			extRows.Close()
			return errs.Wrap(err, errs.CodeInternal,
				"The extensions could not be read.")
		}
		inv.Extensions[name] = version
	}
	extRows.Close()
	if err := extRows.Err(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The extensions could not be read.")
	}

	// Policies, views and materialized views, by name. Three list queries in
	// one round trip each; naming them is what lets a verification say WHICH
	// policy is missing, and a missing policy is a business reading another
	// business's books.
	if inv.Policies, err = names(ctx, conn, `
		SELECT tablename || '.' || policyname FROM pg_policies
		WHERE schemaname = 'public' ORDER BY 1`); err != nil {
		return err
	}
	if inv.Views, err = names(ctx, conn, `
		SELECT table_name FROM information_schema.views
		WHERE table_schema = 'public' ORDER BY 1`); err != nil {
		return err
	}
	if inv.MatViews, err = names(ctx, conn, `
		SELECT matviewname FROM pg_matviews
		WHERE schemaname = 'public' ORDER BY 1`); err != nil {
		return err
	}
	return nil
}

func names(ctx context.Context, conn querier, query string) ([]string, error) {
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"A list of database objects could not be read.")
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternal,
				"A list of database objects could not be read.")
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"A list of database objects could not be read.")
	}
	return out, nil
}
