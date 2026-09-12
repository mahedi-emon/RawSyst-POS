// Creates the FIRST platform operator, and nothing else.
//
// # Why this has to exist
//
// A fresh deployment migrates cleanly, comes up healthy, and nobody can sign
// in. A platform operator is a user with no tenant, and every route that could
// create one sits behind the guard it would be needed to pass — so the product
// came up correct and unusable, and the only thing that could make an operator
// was `cmd/devseed`, which refuses in production and would invent a demo shop
// if it did not.
//
// That is the whole gap this closes: the first actor in the model. Everything
// after it is already a product workflow — the operator creates the business,
// the business owner creates their staff.
//
// # Why it is safe to ship
//
// It refuses if ANY platform operator already exists. That is the difference
// between a bootstrap and a back door: this can be run once on an empty
// deployment and never again, so a copy of the binary on a live server is not
// a way to mint yourself an administrator.
//
// It creates one user with no tenant, prints a generated password once, and
// requires it to be changed at first sign-in. It writes no business data.
//
// # And the way back in when the only operator is locked out
//
// `-recover` issues a new one-time password for an operator who ALREADY exists,
// and refuses to create one. A deployment with a single administrator who has
// lost their password had no remedy at all: this refuses, the forgotten-password
// flow needs mail a fresh deployment may not have, and the reset route needs
// the Super Admin session that has been lost. See recoverOperator.
//
//	RAWSYST_DB_DSN=... go run ./cmd/bootstrap -email you@example.com
//	RAWSYST_DB_DSN=... go run ./cmd/bootstrap -recover -email you@example.com
package bootstrap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/identity"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/audit"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/config"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
)

func Main() {
	// The address is configuration rather than a constant, and the flag is
	// still the last word. A container runs this with no arguments at all --
	// there is nowhere to type one -- so without an environment variable the
	// only way to bootstrap a composed stack was to override the entrypoint.
	email := flag.String("email", os.Getenv("RAWSYST_PLATFORM_EMAIL"),
		"the first platform operator's sign-in email; defaults to $RAWSYST_PLATFORM_EMAIL")
	name := flag.String("name", "Platform Operator", "their name, as it appears in the audit log")
	recover_ := flag.Bool("recover", false,
		"issue a new one-time password for an operator who already exists")
	flag.Parse()

	address := strings.TrimSpace(strings.ToLower(*email))
	var err error
	if *recover_ {
		err = recoverOperator(address)
	} else {
		err = run(address, *name)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		os.Exit(1)
	}
}

// recoverOperator issues a new one-time password for an operator who exists.
//
// # The gap this closes
//
// A deployment with ONE platform operator who has lost their password has, up
// to now, no way back in at all. `bootstrap` refuses because an operator
// already exists — which is the refusal that makes it safe to ship. The
// forgotten-password flow needs mail delivery a fresh deployment may not have.
// `POST /platform/users/{id}/reset-password` needs a signed-in Super Admin,
// which is the thing that has been lost. So the only remedy was hand-written
// SQL against production, which is exactly the operations task E8 set out to
// abolish.
//
// # Why this is not a back door either
//
// It adds no authority. Whoever can run it already holds the database
// credentials, and anybody holding those can insert an operator by hand; what
// this changes is that the legitimate act no longer requires improvising an
// UPDATE against a live table at three in the morning.
//
// It refuses to CREATE anybody — an address that is not already a platform
// operator is refused by name — so it cannot be used to mint an administrator,
// which is the property that separates it from a back door.
//
// Every session the account holds is revoked, because "I have lost my
// password" and "somebody else has my password" arrive looking identical.
func recoverOperator(email string) error {
	pool, err := open()
	if err != nil {
		return err
	}
	defer pool.Close()
	return recoverIn(context.Background(), pool, email)
}

func recoverIn(ctx context.Context, pool *db.Pool, email string) error {
	if email == "" {
		return errors.New("say whose password to reset: -recover -email you@example.com")
	}

	password, err := identity.GenerateTemporaryPassword()
	if err != nil {
		return err
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return err
	}

	var name string
	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var id uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id, full_name FROM app_user
			WHERE tenant_id IS NULL AND lower(email) = $1`, email).
			Scan(&id, &name)
		if e == pgx.ErrNoRows {
			// Named rather than vague. This runs on a machine whose operator
			// already has the database credentials, so there is nobody to
			// protect by being coy, and "no such operator" is what tells them
			// they have typed the wrong address.
			return fmt.Errorf(
				"%s is not a platform operator on this deployment. This "+
					"resets an existing operator's password and will not "+
					"create one; `bootstrap` with no -recover creates the "+
					"first, and refuses once there is one", email)
		}
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE app_user
			SET password_hash = $2, must_change_password = true,
			    failed_attempts = 0, locked_until = NULL
			WHERE id = $1`, id, hash); e != nil {
			return e
		}
		// A lost password and a stolen one look the same from here.
		if _, e := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(),
			    revoked_reason = 'platform operator password recovered'
			WHERE user_id = $1 AND revoked_at IS NULL`, id); e != nil {
			return e
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &id,
			ActorLabel: name,
			Action:     "platform_operator_password_recovered",
			EntityType: "app_user",
			EntityID:   &id,
			After: map[string]any{
				"email":                email,
				"must_change_password": true,
				"sessions_revoked":     true,
				"how": "cmd/bootstrap -recover, run against this " +
					"installation's database",
			},
		})
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n  A new password for %s.\n\n", name)
	fmt.Printf("    email     %s\n", email)
	fmt.Printf("    password  %s\n\n", password)
	fmt.Printf("  Shown once. It must be changed at first sign-in, and every\n")
	fmt.Printf("  session that account held has been ended.\n\n")
	return nil
}

func run(email, name string) error {
	pool, err := open()
	if err != nil {
		return err
	}
	defer pool.Close()
	return create(context.Background(), pool, email, name)
}

// open is the database, and nothing else.
//
// Split out so the two acts below take a pool rather than reaching for the
// environment. A command whose logic can only run inside `main` is a command
// with no tests, and both of these change who can administer a deployment.
func open() (*db.Pool, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return db.Open(context.Background(), cfg.DB)
}

// create makes the first platform operator, and refuses if there is one.
func create(ctx context.Context, pool *db.Pool, email, name string) error {
	if email == "" {
		return errors.New("give the operator an email: -email you@example.com")
	}
	if !strings.Contains(email, "@") {
		return fmt.Errorf("%q is not an email address", email)
	}

	password, err := identity.GenerateTemporaryPassword()
	if err != nil {
		return err
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return err
	}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// The refusal that makes this safe to leave on a server.
		//
		// Counted inside the same transaction that inserts, so two people
		// running it at once cannot both find none and both create one.
		var existing int
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM app_user WHERE tenant_id IS NULL`).
			Scan(&existing); e != nil {
			return e
		}
		if existing > 0 {
			return fmt.Errorf(
				"this deployment already has %d platform operator(s); "+
					"bootstrap creates the FIRST one only. Use Super Admin "+
					"to add another, or reset a password there", existing)
		}

		// `must_change_password` is true, unlike the development seeder's:
		// this password is printed to a terminal and may be in a shell
		// history, a CI log or somebody's screen recording.
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash,
			   must_change_password, status)
			VALUES (NULL, $1, $2, $3, true, 'active')
			RETURNING id`,
			email, name, hash).Scan(&id); e != nil {
			return e
		}

		// The first row in the trail, and it has to be there.
		//
		// Everything else a platform operator does is audited, and the act
		// that CREATED the operator was not: a deployment's log began with
		// somebody already administering it, and no record said who that was
		// or when they appeared. That is precisely the entry an incident
		// review looks for first.
		//
		// The operator is recorded as their own actor. Nobody else exists to
		// name, and the honest statement is that this account came into being
		// by bootstrap rather than by a grant from someone already trusted.
		// Never the password, generated or otherwise.
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &id,
			ActorLabel: name,
			Action:     "platform_operator_bootstrapped",
			EntityType: "app_user",
			EntityID:   &id,
			After: map[string]any{
				"email":                email,
				"full_name":            name,
				"must_change_password": true,
				"how": "cmd/bootstrap on an installation that held no " +
					"platform operator",
			},
		})
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n  Platform operator created.\n\n")
	fmt.Printf("    email     %s\n", email)
	fmt.Printf("    password  %s\n\n", password)
	fmt.Printf("  Shown once. It must be changed at first sign-in, and this\n")
	fmt.Printf("  command will refuse to run again on this deployment.\n\n")
	return nil
}
