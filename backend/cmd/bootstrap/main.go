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
//	RAWSYST_DB_DSN=... go run ./cmd/bootstrap -email you@example.com
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

func main() {
	// The address is configuration rather than a constant, and the flag is
	// still the last word. A container runs this with no arguments at all --
	// there is nowhere to type one -- so without an environment variable the
	// only way to bootstrap a composed stack was to override the entrypoint.
	email := flag.String("email", os.Getenv("RAWSYST_PLATFORM_EMAIL"),
		"the first platform operator's sign-in email; defaults to $RAWSYST_PLATFORM_EMAIL")
	name := flag.String("name", "Platform Operator", "their name, as it appears in the audit log")
	flag.Parse()

	if err := run(strings.TrimSpace(strings.ToLower(*email)), *name); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		os.Exit(1)
	}
}

func run(email, name string) error {
	if email == "" {
		return errors.New("give the operator an email: -email you@example.com")
	}
	if !strings.Contains(email, "@") {
		return fmt.Errorf("%q is not an email address", email)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

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
		_, e := tx.Exec(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash,
			   must_change_password, status)
			VALUES (NULL, $1, $2, $3, true, 'active')`,
			email, name, hash)
		return e
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
