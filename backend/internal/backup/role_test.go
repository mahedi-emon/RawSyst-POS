// What the backup role setup is allowed to do, and what it must refuse.
//
// The interesting cases here are the refusals, and they are deliberately the
// ones that need no database: a command that CREATES A ROLE WITH BYPASSRLS is
// one typo away from ending tenant isolation on a server, so the guards in
// front of it are worth holding to a test that runs everywhere rather than one
// that needs a superuser to be configured.
//
// The half that does need a real Postgres — creating the role, applying the
// grants, and proving `pg_dump` then succeeds where it failed — is proved by
// running it against a database with forced row-level security and an
// unprivileged owner. `deploy/server/BACKUP.md` says how, and the same shape is
// what CI builds for the isolation tests.
package backup

import (
	"context"
	"strings"
	"testing"
)

const (
	appDSN   = "postgres://rawsyst:pw@127.0.0.1:5432/rawsyst_prod?sslmode=disable"
	adminDSN = "postgres://postgres:pw@127.0.0.1:5432/postgres?sslmode=disable"
)

func TestRoleNameMustBeAnIdentifier(t *testing.T) {
	// The name reaches a string-built CREATE ROLE, because that statement takes
	// no parameters. Anything that is not a plain lower-case identifier is
	// refused before it gets there rather than quoted and hoped for.
	for _, bad := range []string{
		"", "Biz1core", "biz1core backup", "biz1core-backup", `biz1core"backup`,
		"9biz1core", "biz1core;DROP", "biz1core'--", strings.Repeat("a", 64),
	} {
		if validRoleName(bad) {
			t.Errorf("%q was accepted as a role name and must not be", bad)
		}
	}
	for _, ok := range []string{
		"rawsyst_backup", "_backup", "b2", strings.Repeat("a", 63),
	} {
		if !validRoleName(ok) {
			t.Errorf("%q is a legal identifier and was refused", ok)
		}
	}
}

func TestRefusesToTouchTheApplicationRole(t *testing.T) {
	// The whole point of a separate role. Asking for the application's own one
	// has to fail before any statement runs, and the message has to say why
	// rather than only that it will not.
	_, err := EnsureRole(context.Background(), RoleOptions{
		AdminDSN: adminDSN, AppDSN: appDSN, Role: "rawsyst",
		Password: "x", DryRun: true,
	})
	if err == nil {
		t.Fatal("setting the application's own role up as the backup role was allowed")
	}
	if !strings.Contains(err.Error(), "tenant isolation") {
		t.Errorf("the refusal does not explain the consequence: %v", err)
	}
}

func TestRefusesAnIllegalRoleName(t *testing.T) {
	_, err := EnsureRole(context.Background(), RoleOptions{
		AdminDSN: adminDSN, AppDSN: appDSN, Role: `x"; ALTER ROLE biz1core BYPASSRLS; --`,
		Password: "x", DryRun: true,
	})
	if err == nil {
		t.Fatal("an injection-shaped role name was accepted")
	}
}

func TestRefusesWithoutTheConnectionsItNeeds(t *testing.T) {
	ctx := context.Background()

	if _, err := EnsureRole(ctx, RoleOptions{
		AdminDSN: adminDSN,
		AppDSN:   "postgres://rawsyst:pw@127.0.0.1:5432/?sslmode=disable",
		Password: "x", DryRun: true,
	}); err == nil {
		t.Error("an application DSN naming no database was accepted")
	}

	if _, err := EnsureRole(ctx, RoleOptions{
		AppDSN: appDSN, Password: "x", DryRun: true,
	}); err == nil {
		t.Error("creating a role with no administrative connection was accepted")
	}
}

func TestDatabaseAndOwnerAreReadFromTheDSN(t *testing.T) {
	if got := databaseOf(appDSN); got != "rawsyst_prod" {
		t.Errorf("database: got %q, want rawsyst_prod", got)
	}
	if got := userOf(appDSN); got != "rawsyst" {
		t.Errorf("owner: got %q, want biz1core", got)
	}
	if got := databaseOf("not a url at all %%%"); got != "" {
		t.Errorf("an unparseable DSN named a database: %q", got)
	}
}

// A password reaches the database and must reach nothing else. The report is
// printed to a terminal and, on a server, into a deploy log.
func TestThePasswordIsNeverInTheReport(t *testing.T) {
	const secret = "Sup3rSecret!Value"

	for _, stmt := range []string{
		`CREATE ROLE "rawsyst_backup" LOGIN PASSWORD '` + secret + `' BYPASSRLS NOSUPERUSER`,
		`ALTER ROLE "rawsyst_backup" PASSWORD '` + secret + `'`,
		`alter role "rawsyst_backup" password '` + secret + `'`,
	} {
		got := redactPassword(stmt)
		if strings.Contains(got, secret) {
			t.Errorf("the password survived redaction: %s", got)
		}
		if !strings.Contains(got, "<redacted>") {
			t.Errorf("nothing was redacted in: %s", got)
		}
	}

	// A password containing a quote is escaped by doubling, and the redaction
	// has to find the real end of the literal rather than the first quote in it.
	stmt := `ALTER ROLE "r" PASSWORD 'it''s a secret' NOSUPERUSER`
	got := redactPassword(stmt)
	if strings.Contains(got, "secret") {
		t.Errorf("a quoted password survived redaction: %s", got)
	}
	if !strings.Contains(got, "NOSUPERUSER") {
		t.Errorf("redaction ate the rest of the statement: %s", got)
	}

	// A statement with no password is returned untouched.
	plain := `GRANT SELECT ON ALL TABLES IN SCHEMA public TO "rawsyst_backup"`
	if redactPassword(plain) != plain {
		t.Errorf("a statement with no password was altered: %s", redactPassword(plain))
	}
}

func TestQuotingIsNotOptional(t *testing.T) {
	if got := quoteIdent(`we"ird`); got != `"we""ird"` {
		t.Errorf("identifier quoting: got %s", got)
	}
	if got := quoteLiteral(`it's`); got != `'it''s'` {
		t.Errorf("literal quoting: got %s", got)
	}
}
