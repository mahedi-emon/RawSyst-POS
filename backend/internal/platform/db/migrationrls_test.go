// A backfill that cannot see the rows it was written for.
//
// # The failure this exists to prevent
//
// Migration 0137 backfills a `subscription` row for every tenant that has
// none. It ran, it reported success, it was recorded in `schema_migration`,
// and it inserted nothing at all.
//
// `Pool.Migrate` runs each migration on an ordinary pool connection and sets no
// GUC, because the schema changes migrations usually carry do not need one.
// Row-level security on `tenant` is FORCED, and its policy is
//
//	USING (id = current_tenant_id() OR is_platform_admin())
//
// With neither `app.tenant_id` nor `app.platform_admin` set, both halves are
// false for every row. So `INSERT INTO ... SELECT ... FROM tenant` selected
// from an empty table.
//
// Nothing failed, because inserting zero rows is not an error. The schema
// version said 137 and the table it was written to fill was untouched.
//
// # Why this is a source check and not a behaviour test
//
// A migration runs once. By the time anything could observe the result, the
// version is recorded and the opportunity is gone -- and on a fresh database
// the same bad migration produces the same silent nothing, so a test that
// creates a tenant and looks for its subscription cannot tell a migration that
// worked from one that had no rows to work on.
//
// The whole-table invariant is not available either: tenants inserted directly
// by test fixtures never go through provisioning, so a test database holds
// hundreds of tenants with no subscription and always will. That is correct,
// and it makes "every tenant has one" a statement about fixtures rather than
// about the product.
//
// What IS checkable, cheaply and exactly, is the rule every other migration of
// this kind already follows.
package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// exemptFromPlatformFlag are migrations allowed to read `tenant` in a data
// statement without setting the flag.
//
// One entry, and it is the defect itself. 0137 is already applied everywhere,
// and `Pool.Migrate` hashes every migration and refuses to start if one changed
// after it was recorded -- "Add a new migration instead of editing history", in
// its own words. So it stays exactly as it is, wrong, and 0138 redoes its work
// properly.
//
// Nothing else belongs in here. A new migration that trips this check has the
// bug 0137 had.
var exemptFromPlatformFlag = map[string]string{
	"0137_a_subscription_nobody_wrote_down_is_not_a_subscription.sql": "the defect itself; redone by 0138 and left unedited because it is hashed and applied",
}

// dataStatement matches a write that reads rows rather than defining a table.
var dataStatement = regexp.MustCompile(`(?is)(INSERT\s+INTO|^\s*UPDATE\s|\sUPDATE\s)`)

// readsTenant matches a reference to the tenant table as a source of rows.
var readsTenant = regexp.MustCompile(`(?is)FROM\s+tenant\b`)

// A data migration that reads `tenant` must set the platform flag, or it reads
// an empty table and quietly does nothing.
func TestATenantBackfillSetsThePlatformFlag(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found, so this check is proving nothing")
	}

	checked := 0
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		name := filepath.Base(path)

		// Comments are prose and routinely discuss `FROM tenant` and the flag;
		// matching them would both mask a real miss and invent false ones.
		sql := withoutComments(string(body))
		if !dataStatement.MatchString(sql) || !readsTenant.MatchString(sql) {
			continue
		}
		checked++

		if strings.Contains(sql, "app.platform_admin") {
			continue
		}
		if why, ok := exemptFromPlatformFlag[name]; ok {
			t.Logf("%s is exempt: %s", name, why)
			continue
		}

		t.Errorf("%s reads rows from `tenant` in a data statement without "+
			"setting app.platform_admin. Row-level security on that table is "+
			"FORCED and migrations run with no GUC set, so the SELECT will "+
			"read an EMPTY table, write nothing, and report success. Wrap it "+
			"in a DO block that does `PERFORM set_config('app.platform_admin', "+
			"'on', true)` and clears it at the end, as 0042 and every "+
			"tenant-scoped backfill since has done.", name)
	}

	// The check has to be looking at something. A regex that quietly stopped
	// matching would leave this test passing for ever while guarding nothing.
	if checked < 20 {
		t.Errorf("only %d migrations were examined; this check has fallen off "+
			"the migrations it is meant to cover", checked)
	}
}

// withoutComments strips `--` lines so prose about the rule is not mistaken for
// the rule being followed, or for it being broken.
func withoutComments(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
