// Puts a working shop into a development database.
//
// A back office cannot be looked at without a tenant, an owner, a company with
// a chart of accounts, a warehouse and something to buy — and building all of
// that through the UI is impossible, because the first screen needs a login
// that does not exist yet. So this creates it, using the SAME provisioning and
// chart-of-accounts code the product uses rather than a parallel set of INSERT
// statements that could drift from it.
//
// # It refuses to run against production
//
// Loudly, on RAWSYST_ENV, because the one thing a seeder must never do is
// invent a tenant in a real deployment. The check is the reason this is a
// separate binary rather than a flag on the API.
//
// It is idempotent by email: running it twice reports the existing owner rather
// than failing or creating a second shop.

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

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

func main() {
	email := flag.String("email", "owner@example.test", "the owner's sign-in email")
	name := flag.String("name", "Demo Retail", "the trading name")
	// Optional, and there for one specific reason: reproducing an email that
	// belongs to two businesses with the SAME password, which is the case the
	// tenant picker exists for and which cannot be set up otherwise.
	password := flag.String("password", "", "reuse a known password instead of generating one")
	flag.Parse()

	if err := run(*email, *name, *password); err != nil {
		fmt.Fprintf(os.Stderr, "devseed: %v\n", err)
		os.Exit(1)
	}
}

func run(email, name, password string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The guard that justifies this binary existing at all.
	if cfg.Env == "production" {
		return errors.New(
			"refusing to seed a production database; this creates a tenant and an owner account")
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	// A platform administrator, because CreateTenant requires one and correctly
	// refuses anybody else. Nothing is written under this identity beyond the
	// tenant itself.
	ctx = actor.Into(ctx, actor.Actor{
		UserID: uuid.New(), IsSuperAdmin: true,
	})

	prov := provisioning.NewService(pool)
	out, err := prov.CreateTenant(ctx, provisioning.NewTenant{
		Name:       name,
		DataRegion: cfg.DataRegion,
		PlanTier:   "business",
		OwnerEmail: email,
		OwnerName:  "Demo Owner",
	})
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}

	if err := seedShop(ctx, pool, out.TenantID, name); err != nil {
		return fmt.Errorf("seed shop: %w", err)
	}

	// Overwritten only when asked, and only outside production, which the check
	// above has already established.
	if password != "" {
		hash, hErr := identity.HashPassword(password)
		if hErr != nil {
			return hErr
		}
		if uErr := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `
				UPDATE app_user SET password_hash = $2, must_change_password = false
				WHERE id = $1`, out.OwnerUserID, hash)
			return e
		}); uErr != nil {
			return uErr
		}
		out.TemporaryPassword = password
	}

	fmt.Printf("\n  Seeded %q\n\n", name)
	fmt.Printf("    email     %s\n", out.OwnerEmail)
	fmt.Printf("    password  %s\n", out.TemporaryPassword)
	fmt.Printf("    tenant    %s\n\n", out.TenantID)
	return nil
}

// seedShop gives the tenant a company that can actually trade: a chart of
// accounts to post to, a store, a warehouse to receive into, and a few products
// to order.
func seedShop(
	ctx context.Context, pool *db.Pool, tenantID uuid.UUID, name string,
) error {
	return pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// A distinct registration number per seeded shop, because the column is
		// unique across the whole platform — two demo companies sharing one
		// would fail the second time this is run. Structurally shaped like a
		// Saudi VAT number and deliberately not a real one; nothing here is a
		// verified regulatory value.
		vatNumber := fmt.Sprintf("3%013d3", time.Now().UnixNano()%1e13)

		var companyID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO company
			  (tenant_id, legal_name, trade_name, country, base_currency,
			   timezone, vat_registered, vat_number)
			VALUES ($1, $2, $2, 'sa', 'SAR', 'Asia/Riyadh', true, $3)
			RETURNING id`, tenantID, name, vatNumber).Scan(&companyID); err != nil {
			return err
		}

		// The same seeder the onboarding wizard runs, so a seeded company and a
		// real one have identical books.
		if err := provisioning.SeedChartOfAccounts(ctx, tx, tenantID, companyID); err != nil {
			return err
		}

		// Twelve open months for the current year.
		//
		// Without these the company looks complete and cannot post a single
		// transaction: the engine refuses with "no accounting period covers
		// that date", which is correct and which a browser check found the
		// hard way when approving a bill. A seeded shop that cannot take money
		// is not a seeded shop.
		year := time.Now().UTC().Year()
		for month := 1; month <= 12; month++ {
			starts := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
			ends := starts.AddDate(0, 1, -1)
			if _, err := tx.Exec(ctx, `
				INSERT INTO fiscal_period
				  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				tenantID, companyID, year, month, starts, ends); err != nil {
				return err
			}
		}

		var storeID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1, $2, 'MAIN', 'Main Branch') RETURNING id`,
			tenantID, companyID).Scan(&storeID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1, $2, $3, 'MAIN-WH', 'Main Stockroom')`,
			tenantID, companyID, storeID); err != nil {
			return err
		}

		// A till, so a developer can ring up a sale rather than only look at
		// screens that report sales. Without one the POS routes have no terminal
		// to resolve, and the whole sell-on-account-then-collect journey cannot
		// be exercised outside the test suite.
		//
		// The EGS unit owns the ZATCA counter and hash chain, which E1.3 puts on
		// the device itself — so a device without one could not take a sale at
		// all, and the two are seeded together.
		var egsUnitID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit
			  (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1, $2, $3, 'till-1', 'smart_pos') RETURNING id`,
			tenantID, companyID, storeID).Scan(&egsUnitID); err != nil {
			return err
		}
		// PENDING, not active. Since 0037 a terminal earns its status by being
		// paired: an active device with no credential is a till that looks ready
		// and cannot authenticate, which is worse than one that plainly needs
		// setting up. Pair it from Devices in the back office.
		if _, err := tx.Exec(ctx, `
			INSERT INTO device
			  (tenant_id, company_id, store_id, terminal_label, status, egs_unit_id)
			VALUES ($1, $2, $3, 'Till 1', 'pending', $4)`,
			tenantID, companyID, storeID, egsUnitID); err != nil {
			return err
		}

		// A handful of products, so the order form's item search has something
		// to find. Named plainly rather than with lorem ipsum: somebody looking
		// at this screen should be able to tell at a glance that it is demo data.
		items := []struct{ sku, product string }{
			{"ABAYA-BLK-M", "Abaya, Black"},
			{"ABAYA-BLK-L", "Abaya, Black"},
			{"SCARF-SLK", "Silk Scarf"},
			{"THOBE-WHT-L", "Thobe, White"},
		}
		for i, item := range items {
			var productID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO product (tenant_id, company_id, sku, name, tax_treatment)
				VALUES ($1,$2,$3,$4,'standard') RETURNING id`,
				tenantID, companyID, item.sku, item.product).Scan(&productID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO variant
				  (tenant_id, company_id, product_id, sku, barcode,
				   price_retail, reorder_level, is_active)
				VALUES ($1,$2,$3,$4,$5,$6,10,true)`,
				tenantID, companyID, productID, item.sku,
				fmt.Sprintf("628100000%04d", i+1),
				100+(i*25)); err != nil {
				return err
			}
		}

		return nil
	})
}
