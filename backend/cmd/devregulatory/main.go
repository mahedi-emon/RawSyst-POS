// Lets a developer run the calculations that a legal value gates.
//
// # The problem
//
// `SA.EOSB.ENTITLEMENT` and its neighbours are seeded as `__VERIFY__`, which
// refuses at the point of use. That is correct for a deployment and useless for
// a developer: nobody can work on end-of-service accrual, or see the screen
// that reports it, without a figure — and the only honest way to get one is for
// somebody to read the Labour Law, which is not a thing a developer does before
// every test run.
//
// So the calculation had no local path at all. The band arithmetic, the wage
// basis and the posting were exercised only by the integration suite, which
// stages its own override and tears it down.
//
// # Why this is not a back door
//
// It writes PER-TENANT overrides, not registry rules. Three things follow, and
// together they are the whole safety argument:
//
//   - `regulatory_rule_override` is row-level-security scoped to one tenant, so
//     this cannot change what any other tenant computes.
//   - An override resolves with `verified_on = NULL`. A deployment that
//     requires verification refuses it exactly as it refuses a placeholder, so
//     this cannot make a production system compute from these numbers.
//   - It refuses outright when `RAWSYST_ENV` is production, before it opens a
//     transaction.
//
// # Why the figures are deliberately absurd
//
// Ten days and forty, a third and two thirds — chosen so that nobody can
// mistake them for the statute, and so a screenshot taken during development
// cannot be read as a legal answer. A developer needs the code path to RUN;
// they do not need it to be right, and making it look right is how a
// development value ends up quoted at somebody.
//
//	RAWSYST_DB_DSN=... go run ./cmd/devregulatory
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/config"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
)

// staged is one development figure set, and the rule it stands in for.
type staged struct {
	key     string
	country string
	payload string
	why     string
}

// The development values. Not the law, and visibly not the law.
var stages = []staged{
	{
		key:     "SA.EOSB.ENTITLEMENT",
		country: "sa",
		// The fractions were 0, 0.3333 and 0.6667, which are a third and two
		// thirds and therefore look exactly like a real reading of Article 85.
		// This file's whole claim is that its figures are visibly not the law,
		// and two of them were not visibly anything of the sort. A descending
		// half, quarter, fifth, tenth is unmistakable.
		//
		// The fourth band arrived with 0132. Article 85 bands the fraction by
		// length of service and 0092 recorded three, so service beyond ten
		// years had none to apply.
		payload: `{"wage_basis":"basic_plus_housing",
		           "days_per_year_first_five":"10",
		           "days_per_year_after_five":"40",
		           "resignation_fraction_under_two_years":"0.5",
		           "resignation_fraction_two_to_five_years":"0.25",
		           "resignation_fraction_five_to_ten_years":"0.2",
		           "resignation_fraction_over_ten_years":"0.1"}`,
		why: "end-of-service accrual, the positions screen and the settlement",
	},
	{
		key:     "SA.WPS.SUBMISSION_TIMING",
		country: "sa",
		payload: `{"lead_time_business_days":"3","payment_window_days":"10"}`,
		why:     "the wage-file timing checks",
	},
}

// devFrom is the date the development figures come into force.
//
// Earlier than any period a development database holds, because a rule is
// resolved at the date of the document being processed: a September accrual
// asks what was in force in September, so a window opened today governs
// nothing anybody runs.
var devFrom = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

func main() {
	remove := flag.Bool("remove", false, "take the development figures back out")
	flag.Parse()

	if err := run(*remove); err != nil {
		fmt.Fprintf(os.Stderr, "devregulatory: %v\n", err)
		os.Exit(1)
	}
}

func run(remove bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Before anything else, and before a transaction is opened.
	if cfg.Env == "production" {
		return errors.New(
			"refusing: RAWSYST_ENV is production. These are development " +
				"figures and they are not the law")
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	var tenants []uuid.UUID
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT id FROM tenant WHERE status <> 'deactivated' ORDER BY created_at`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				return e
			}
			tenants = append(tenants, id)
		}
		return rows.Err()
	}); err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	if len(tenants) == 0 {
		return errors.New("this database has no tenants; run cmd/devseed first")
	}

	// Somebody has to be named as having approved an override, and the schema
	// requires a real user. The owner of each tenant is the honest choice: on a
	// development machine they are the person running this.
	for _, tenantID := range tenants {
		if err := apply(ctx, pool, tenantID, remove); err != nil {
			return fmt.Errorf("tenant %s: %w", tenantID, err)
		}
	}

	if remove {
		fmt.Printf("\n  Development figures removed from %d tenant(s).\n\n", len(tenants))
		return nil
	}

	fmt.Printf("\n  Development figures staged for %d tenant(s):\n\n", len(tenants))
	for _, st := range stages {
		fmt.Printf("    %-28s %s\n", st.key, st.why)
	}
	fmt.Printf("\n  These are NOT the law. They are per-tenant overrides that\n")
	fmt.Printf("  resolve unverified, so a deployment requiring verification\n")
	fmt.Printf("  refuses them exactly as it refuses a placeholder.\n\n")
	// The registry caches resolved rules inside the API process and clears
	// that cache only on a registry write it made itself. This writes
	// straight to the database, so an API already running keeps answering
	// from the placeholder it read earlier -- which looks exactly like this
	// command having done nothing.
	fmt.Printf("  Restart any API or worker already running: they cache\n")
	fmt.Printf("  resolved rules in process and will not see these otherwise.\n\n")
	fmt.Printf("  Remove them with: go run ./cmd/devregulatory -remove\n\n")
	return nil
}

func apply(ctx context.Context, pool *db.Pool, tenantID uuid.UUID, remove bool) error {
	return pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if remove {
			// `regulatory_rule_override` carries a reject_delete trigger, so
			// removing means closing the window rather than deleting the row —
			// which is also the honest record: these figures WERE in force on
			// this machine for a while.
			for _, st := range stages {
				if _, e := tx.Exec(ctx, `
					UPDATE regulatory_rule_override
					SET effective_to = current_date
					WHERE tenant_id = $1 AND rule_key = $2
					  AND (effective_to IS NULL OR effective_to > current_date)`,
					tenantID, st.key); e != nil {
					return e
				}
			}
			return nil
		}

		// Any user who is not disabled, not only an active one. A tenant
		// provisioned and never signed into has exactly one user and their
		// status is `invited` -- which is the normal state of a business the
		// platform has just created, and the state this refused on.
		var approver uuid.UUID
		if e := tx.QueryRow(ctx, `
			SELECT id FROM app_user
			WHERE tenant_id = $1 AND status <> 'disabled'
			ORDER BY created_at LIMIT 1`, tenantID).Scan(&approver); e != nil {
			return fmt.Errorf("find somebody to record the override against: %w", e)
		}

		for _, st := range stages {
			// Back-dated on purpose, and this is the whole of why it works.
			//
			// A rule resolves AT THE DATE OF THE DOCUMENT BEING PROCESSED, not
			// at today: an accrual run resolves at the start of the period it
			// is accruing. A window opened today covers none of that, which is
			// how the first version of this staged its figures, reported
			// success, and left every calculation refusing exactly as before.
			//
			// `devFrom` is earlier than any period a development database
			// holds, so the figures are in force for whatever gets run.
			//
			// Idempotent by adopting the open window rather than opening a
			// second one: `regulatory_rule_override_no_overlap` refuses the
			// second, and running this twice in a morning is the ordinary case
			// on a development machine.
			tag, e := tx.Exec(ctx, `
				UPDATE regulatory_rule_override
				SET payload = $3::jsonb, approved_by = $4,
				    effective_from = $5, effective_to = NULL
				WHERE tenant_id = $1 AND rule_key = $2
				  AND (effective_to IS NULL OR effective_to > current_date)`,
				tenantID, st.key, st.payload, approver, devFrom)
			if e != nil {
				return e
			}
			if tag.RowsAffected() > 0 {
				continue
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO regulatory_rule_override
				  (tenant_id, rule_key, country, payload, effective_from,
				   justification, approved_by)
				VALUES ($1, $2, $3, $4::jsonb, $6,
				        'Development figures so the calculation runs locally. '
				        'Not the law, deliberately not plausible as the law, '
				        'and unverified so any deployment requiring '
				        'verification refuses them.', $5)`,
				tenantID, st.key, st.country, st.payload, approver, devFrom); e != nil {
				return e
			}
		}
		return nil
	})
}
