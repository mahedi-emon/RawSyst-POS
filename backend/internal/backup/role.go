// The role that takes the backup, created rather than described.
//
// # Why this file exists
//
// `canReadEverything` says, correctly and at length, that the application's
// role cannot dump this database and must never be given the attribute that
// would let it. `grantBackupRole` puts the grants back after a restore. Between
// those two there was a hole: nothing CREATED the role. The four statements
// lived in deploy/server/BACKUP.md, to be pasted into psql by hand, on a server
// somebody is setting up for the first time and probably at speed.
//
// A setup step that exists only as prose in a document is a setup step that
// gets skipped, and the way it announces itself is the first nightly backup
// failing at 03:30 — silently, because nobody watches a timer that has never
// failed before.
//
// # What it will not do
//
// It will not give the application's role BYPASSRLS, and it refuses if asked to
// operate on that role at all. It checks the application role and fails if
// somebody has already done it by hand, because a server where the app role can
// see past row-level security is a server with no tenant isolation, and finding
// that out here is better than not finding it out.
//
// # Idempotent, and it says what it changed
//
// Run it on every deploy. An existing role is repaired rather than replaced:
// the attribute is set if it is missing, the grants are reapplied, and the
// password is left alone unless one was supplied. Then it connects AS the role
// and runs the same precondition check `backup run` runs, so the answer is
// "this role can take a backup" rather than "the statements did not error".
package backup

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// DefaultBackupRole is the name deploy/server/BACKUP.md uses throughout.
const DefaultBackupRole = "rawsyst_backup"

// RoleOptions is what to make, and where.
type RoleOptions struct {
	// AdminDSN is a connection as a role that may CREATE ROLE and GRANT. On
	// the compose stack it points at the `postgres` database; the grants have
	// to be applied inside the live one, so the database in this string is
	// replaced with LiveDatabase before connecting.
	AdminDSN string

	// AppDSN is the application's own connection. Two things are read from it
	// and nothing else: which database is live, and which role owns it.
	AppDSN string

	// Role is the name to create. Empty means DefaultBackupRole.
	Role string

	// Password is used only when the role is being created, or when a rotation
	// was explicitly asked for. Never logged, never returned in the report.
	Password string

	// VerifyDSN is the connection string the backup will actually use, so the
	// check at the end tests what the deployment configured rather than
	// something this code assembled. Empty skips that last step and the report
	// says so.
	VerifyDSN string

	// DryRun reports what would change and changes nothing.
	DryRun bool
}

// RoleReport is what happened, in the terms an operator would check.
type RoleReport struct {
	Role     string `json:"role"`
	Database string `json:"database"`
	Owner    string `json:"owner"`

	Existed bool `json:"existed"`
	Created bool `json:"created"`

	// GrantedBypass is true when this run added BYPASSRLS to a role that did
	// not have it, which is the one attribute that makes a dump possible.
	GrantedBypass bool `json:"granted_bypassrls"`
	PasswordSet   bool `json:"password_set"`

	// Statements are what ran, with any password removed. Safe to log, and
	// meant to be: an operator reading a deploy log should be able to see
	// exactly what was done to their database.
	Statements []string `json:"statements"`

	// Verified is the answer to the only question that matters. Unset when
	// VerifyDSN was empty.
	Verified   *bool  `json:"verified,omitempty"`
	VerifyNote string `json:"verify_note,omitempty"`

	DryRun bool `json:"dry_run"`
}

// databaseOf is the database a connection string names.
func databaseOf(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// validRoleName keeps an identifier out of the SQL that is not one.
//
// The name comes from the environment rather than from a request, so this is
// belt and braces — but the statements below are string-built, because CREATE
// ROLE takes no parameters, and a string-built statement with an unvalidated
// identifier in it is the shape of the bug whatever the source.
func validRoleName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case (r >= '0' && r <= '9') && i > 0:
		default:
			return false
		}
	}
	return true
}

// EnsureRole creates or repairs the backup role and proves it can read.
func EnsureRole(ctx context.Context, opts RoleOptions) (*RoleReport, error) {
	role := strings.TrimSpace(opts.Role)
	if role == "" {
		role = DefaultBackupRole
	}
	if !validRoleName(role) {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"%q is not a name this will create a role under. Lower case "+
				"letters, digits and underscores, starting with a letter or "+
				"an underscore.", role)
	}

	owner := userOf(opts.AppDSN)
	live := databaseOf(opts.AppDSN)
	if live == "" {
		return nil, errs.New(errs.CodeInvalidInput,
			"The application's connection string does not name a database, so "+
				"there is nothing to grant on. Set RAWSYST_DB_DSN.")
	}
	if strings.TrimSpace(opts.AdminDSN) == "" {
		return nil, errs.New(errs.CodeInvalidInput,
			"Creating a role needs a connection that may create one. Set "+
				"RAWSYST_BACKUP_ADMIN_DSN to a superuser or to the database "+
				"owner.")
	}
	if role == owner {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"%q is the application's own role. Giving it BYPASSRLS would end "+
				"tenant isolation on this server: every business would be able "+
				"to read every other business's books, and no policy in the "+
				"database would stop it. The backup needs its OWN role.", role)
	}

	report := &RoleReport{
		Role: role, Database: live, Owner: owner, DryRun: opts.DryRun,
	}

	// Connected to the LIVE database, not to whichever one the admin string
	// happens to name: GRANT is per database, and grants applied in `postgres`
	// would leave the next backup failing with the message this exists to
	// prevent.
	conn, err := pgx.Connect(ctx, dsnFor(opts.AdminDSN, live))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeUnavailable,
			"Could not open "+live+" as an administrator to set the backup "+
				"role up.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// The check that is worth failing the command over. If somebody has already
	// given the application's role BYPASSRLS to "make the backup work", this
	// server has no tenant isolation and saying so is more urgent than
	// finishing the job.
	if owner != "" {
		var appBypass, appSuper bool
		err := conn.QueryRow(ctx,
			`SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = $1`,
			owner).Scan(&appBypass, &appSuper)
		if err != nil && err != pgx.ErrNoRows {
			return nil, errs.Wrap(err, errs.CodeInternal,
				"The database would not say what the application's role may do.")
		}
		if appBypass || appSuper {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"The application's role %q can already see past row-level "+
					"security (bypassrls=%t, superuser=%t). Row-level security "+
					"is forced on this database precisely so it cannot, and "+
					"while that is true one business can read another's books. "+
					"Fix that before setting up a backup role: ALTER ROLE %s "+
					"NOBYPASSRLS NOSUPERUSER. On the compose stack the "+
					"container's POSTGRES_USER is a superuser by default, "+
					"which is why deploy/server/BACKUP.md says not to run a "+
					"real deployment that way.",
				owner, appBypass, appSuper, owner)
		}
	}

	var existed, hasBypass bool
	err = conn.QueryRow(ctx,
		`SELECT true, rolbypassrls FROM pg_roles WHERE rolname = $1`, role).
		Scan(&existed, &hasBypass)
	if err != nil && err != pgx.ErrNoRows {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"The database would not say whether "+role+" exists.")
	}
	report.Existed = existed

	if !existed && strings.TrimSpace(opts.Password) == "" {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"%q does not exist and no password was given for it. Set "+
				"RAWSYST_BACKUP_ROLE_PASSWORD to a generated value, then put "+
				"the same value in the RAWSYST_BACKUP_DSN the backup uses.",
			role)
	}

	// Built rather than parameterised because CREATE ROLE and ALTER ROLE take
	// no parameters. The identifier is validated above; the password is quoted
	// by the driver's literal quoter and is stripped from the reported
	// statement, so it reaches the database and neither the report nor a log.
	//
	// It does still reach the server's own statement log if the deployment set
	// `log_statement = all`, which is why BACKUP.md says not to and says to
	// rotate if it did.
	var stmts []string
	switch {
	case !existed:
		stmts = append(stmts, fmt.Sprintf(
			`CREATE ROLE %s LOGIN PASSWORD %s BYPASSRLS `+
				`NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION`,
			quoteIdent(role), quoteLiteral(opts.Password)))
		report.Created = true
		report.PasswordSet = true
		report.GrantedBypass = true
	default:
		if !hasBypass {
			stmts = append(stmts,
				`ALTER ROLE `+quoteIdent(role)+` BYPASSRLS`)
			report.GrantedBypass = true
		}
		// Never silently: an existing role keeps its password unless one was
		// deliberately supplied, so re-running this on every deploy does not
		// rotate a credential the deployment is still using.
		if strings.TrimSpace(opts.Password) != "" {
			stmts = append(stmts, fmt.Sprintf(`ALTER ROLE %s PASSWORD %s`,
				quoteIdent(role), quoteLiteral(opts.Password)))
			report.PasswordSet = true
		}
		// A role that exists must still not be able to do more than read.
		stmts = append(stmts, `ALTER ROLE `+quoteIdent(role)+
			` NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION`)
	}

	// The same five the restore path reapplies, and for the same reason: the
	// ALTER DEFAULT PRIVILEGES pair is what keeps the grant true for tables a
	// migration adds next month.
	stmts = append(stmts,
		`GRANT CONNECT ON DATABASE `+quoteIdent(live)+` TO `+quoteIdent(role),
		`GRANT USAGE ON SCHEMA public TO `+quoteIdent(role),
		`GRANT SELECT ON ALL TABLES IN SCHEMA public TO `+quoteIdent(role),
		`GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO `+quoteIdent(role),
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO `+quoteIdent(role),
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO `+quoteIdent(role),
	)

	for _, s := range stmts {
		report.Statements = append(report.Statements, redactPassword(s))
	}
	if opts.DryRun {
		return report, nil
	}

	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return report, errs.Wrap(err, errs.CodeInternal,
				"Setting the backup role up failed at: "+redactPassword(s))
		}
	}

	// ALTER DEFAULT PRIVILEGES applies to objects created by the role that ran
	// it. The application owns its tables, so the statements above only hold
	// for tables the ADMIN creates unless the admin IS the owner. Said here
	// rather than discovered later.
	if owner != "" && userOf(opts.AdminDSN) != owner {
		report.VerifyNote = "Default privileges were set as " +
			userOf(opts.AdminDSN) + ", not as the table owner " + owner +
			". Tables a future migration creates as " + owner +
			" will not inherit the grant; `backup run` names them before it " +
			"starts, and running this command again reapplies them."
	}

	if strings.TrimSpace(opts.VerifyDSN) == "" {
		return report, nil
	}

	ok := false
	report.Verified = &ok
	vconn, err := pgx.Connect(ctx, opts.VerifyDSN)
	if err != nil {
		return report, errs.Wrap(err, errs.CodeUnavailable,
			"The role was set up, but connecting with RAWSYST_BACKUP_DSN "+
				"failed. The password in that connection string and the one "+
				"just set have to be the same value.")
	}
	defer vconn.Close(context.WithoutCancel(ctx))

	if err := canReadEverything(ctx, vconn); err != nil {
		return report, err
	}
	ok = true
	return report, nil
}

// quoteIdent is the SQL identifier form of an already-validated name.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteLiteral is a single-quoted SQL string.
func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// redactPassword removes the secret from a statement before it is reported.
//
// Matched on the keyword rather than on the value, so a password that happens
// to look like SQL is still removed, and so a statement this file gains later
// is covered without anybody remembering to add it here.
func redactPassword(stmt string) string {
	upper := strings.ToUpper(stmt)
	i := strings.Index(upper, "PASSWORD ")
	if i < 0 {
		return stmt
	}
	rest := stmt[i+len("PASSWORD "):]
	if !strings.HasPrefix(rest, "'") {
		return stmt
	}
	// To the closing quote, doubled quotes included.
	j := 1
	for j < len(rest) {
		if rest[j] == '\'' {
			if j+1 < len(rest) && rest[j+1] == '\'' {
				j += 2
				continue
			}
			break
		}
		j++
	}
	if j >= len(rest) {
		return stmt[:i] + "PASSWORD '<redacted>'"
	}
	return stmt[:i] + "PASSWORD '<redacted>'" + rest[j+1:]
}
