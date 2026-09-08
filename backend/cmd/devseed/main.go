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

	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/assets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/labels"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/orders"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/people"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/stockops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/treasury"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/workflow"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

func main() {
	email := flag.String("email", "owner@example.test", "the owner's sign-in email")
	name := flag.String("name", "Demo Retail", "the trading name")
	// Optional, and there for one specific reason: reproducing an email that
	// belongs to two businesses with the SAME password, which is the case the
	// tenant picker exists for and which cannot be set up otherwise.
	password := flag.String("password", "", "reuse a known password instead of generating one")
	// The platform workspace is unreachable without one of these, and there is
	// no screen that can create one: a platform operator is a user with no
	// tenant, and every route that could make one sits behind the guard it
	// would be needed to pass. So the seeder makes it, or nobody in
	// development ever sees Platform Admin at all.
	operator := flag.String("platform-email", "",
		"also create a platform operator with this email (no tenant, super admin)")
	flag.Parse()

	if err := run(*email, *name, *password, *operator); err != nil {
		fmt.Fprintf(os.Stderr, "devseed: %v\n", err)
		os.Exit(1)
	}
}

func run(email, name, password, operator string) error {
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
		// Matches the company seedShop creates below, which is Saudi. The two
		// have to agree: onboarding refuses a company whose country is not the
		// tenant's market, and a seed that cannot complete setup is not a seed.
		Market:     "sa",
		OwnerEmail: email,
		OwnerName:  "Demo Owner",
	})
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}

	if err := seedShop(ctx, pool, out.TenantID, name); err != nil {
		return fmt.Errorf("seed shop: %w", err)
	}

	if err := seedDocuments(ctx, pool, out.TenantID, out.OwnerUserID); err != nil {
		return fmt.Errorf("seed documents: %w", err)
	}

	if err := seedPeople(ctx, pool, out.TenantID, out.OwnerUserID); err != nil {
		return fmt.Errorf("seed people: %w", err)
	}

	if err := seedTrading(ctx, pool, out.TenantID, out.OwnerUserID); err != nil {
		return fmt.Errorf("seed trading: %w", err)
	}

	if err := seedConfiguration(ctx, pool, out.TenantID, out.OwnerUserID); err != nil {
		return fmt.Errorf("seed configuration: %w", err)
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

	if operator != "" {
		pw, oErr := seedOperator(ctx, pool, operator, password)
		if oErr != nil {
			return fmt.Errorf("seed platform operator: %w", oErr)
		}
		fmt.Printf("  Platform operator\n\n")
		fmt.Printf("    email     %s\n", operator)
		fmt.Printf("    password  %s\n\n", pw)
	}
	return nil
}

// seedOperator creates the platform's own login.
//
// A NULL tenant_id is the whole model: identity.Login sets IsSuperAdmin for a
// user who belongs to no tenant, and migration 0006 bounds what that reaches.
// There is no role to grant and no permission to seed -- /auth/me answers an
// empty permission list for one of these, which is correct, because the
// platform routes are gated on the claim rather than on the catalogue.
//
// Idempotent by email, like the rest of this seeder, and it reuses the owner's
// password when one was given so a developer has one password to remember.
func seedOperator(
	ctx context.Context, pool *db.Pool, email, password string,
) (string, error) {
	var err error
	if password == "" {
		password, err = identity.GenerateTemporaryPassword()
		if err != nil {
			return "", err
		}
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return "", err
	}
	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash,
			   must_change_password, status)
			VALUES (NULL, $1, 'Platform Operator', $2, false, 'active')
			ON CONFLICT (email) WHERE tenant_id IS NULL
			DO UPDATE SET password_hash = EXCLUDED.password_hash,
			              must_change_password = false,
			              status = 'active'`, email, hash)
		return e
	})
	return password, err
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

		// A second till, already paired.
		//
		// The one above is deliberately `pending`: a device earns its status by
		// being paired, and an active device with no credential is a till that
		// looks ready and cannot authenticate. But `GET /pos/counters` lists
		// only ACTIVE session-bound devices, so a seed carrying nothing but a
		// pending till answers an empty list -- and `verify:api` could not
		// check the counter row shape at all against a fresh database. It only
		// ever passed because the development database had accumulated a
		// paired till by hand, which is a verification nobody can reproduce.
		// Two tills: one to pair, one already paired.
		var counterEGS uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit
			  (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1, $2, $3, 'till-2', 'smart_pos') RETURNING id`,
			tenantID, companyID, storeID).Scan(&counterEGS); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO device
			  (tenant_id, company_id, store_id, terminal_label, status,
			   binding, egs_unit_id)
			VALUES ($1, $2, $3, 'Till 2', 'active', 'session', $4)`,
			tenantID, companyID, storeID, counterEGS); err != nil {
			return err
		}

		// How the catalogue is arranged.
		//
		// Seeded because the routes that read these are new, and a shop with no
		// departments cannot demonstrate a commission scheme scoped to one nor
		// a product form with anything in its pickers.
		departments := []struct{ name, arabic string }{
			{"Womenswear", "\u0645\u0644\u0627\u0628\u0633 \u0646\u0633\u0627\u0626\u064a\u0629"},
			{"Menswear", "\u0645\u0644\u0627\u0628\u0633 \u0631\u062c\u0627\u0644\u064a\u0629"},
			{"Accessories", "\u0625\u0643\u0633\u0633\u0648\u0627\u0631\u0627\u062a"},
		}
		categoryIDs := make([]uuid.UUID, 0, len(departments))
		for i, d := range departments {
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO category
				  (tenant_id, company_id, name, translations, path, depth, sort_order)
				VALUES ($1,$2,$3, jsonb_build_object('ar', $4::text), '{}', 0, $5)
				RETURNING id`,
				tenantID, companyID, d.name, d.arabic, i).Scan(&id); err != nil {
				return err
			}
			categoryIDs = append(categoryIDs, id)
		}

		var brandID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO brand (tenant_id, company_id, name, translations)
			VALUES ($1,$2,'Al Anaqah', jsonb_build_object('ar', $3::text))
			RETURNING id`, tenantID, companyID,
			"\u0627\u0644\u0623\u0646\u0627\u0642\u0629").Scan(&brandID); err != nil {
			return err
		}

		var pieceID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO unit_of_measure
			  (tenant_id, company_id, code, name, translations, allows_fraction)
			VALUES ($1,$2,'PC','Piece', jsonb_build_object('ar', $3::text), false)
			RETURNING id`, tenantID, companyID,
			"\u0642\u0637\u0639\u0629").Scan(&pieceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO unit_of_measure
			  (tenant_id, company_id, code, name, translations, allows_fraction)
			VALUES ($1,$2,'M','Metre', jsonb_build_object('ar', $3::text), true)`,
			tenantID, companyID, "\u0645\u062a\u0631"); err != nil {
			return err
		}

		// A handful of products, so the order form's item search has something
		// to find. Named plainly rather than with lorem ipsum: somebody looking
		// at this screen should be able to tell at a glance that it is demo data.
		items := []struct {
			sku, product string
			category     int
		}{
			{"ABAYA-BLK-M", "Abaya, Black", 0},
			{"ABAYA-BLK-L", "Abaya, Black", 0},
			{"SCARF-SLK", "Silk Scarf", 2},
			{"THOBE-WHT-L", "Thobe, White", 1},
		}
		for i, item := range items {
			var productID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO product
				  (tenant_id, company_id, sku, name, tax_treatment,
				   category_id, brand_id, unit_id)
				VALUES ($1,$2,$3,$4,'standard',$5,$6,$7) RETURNING id`,
				tenantID, companyID, item.sku, item.product,
				categoryIDs[item.category], brandID, pieceID).
				Scan(&productID); err != nil {
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

		// A customer and a supplier.
		//
		// Both were absent, so a fresh database answered `GET /customers` and
		// `GET /purchasing/suppliers` with empty lists and `verify:api` could
		// check neither row shape. The verification passed only against a
		// database somebody had typed records into, which is the difference
		// between a check and a memory of one.
		if _, err := tx.Exec(ctx, `
			INSERT INTO customer
			  (tenant_id, company_id, code, name, name_ar, customer_type,
			   phone, payment_terms_days, credit_limit)
			VALUES ($1,$2,'C-0001','Al Noor Trading',$3,
			        'wholesale','+966500000001',30,5000)`,
			tenantID, companyID,
			"\u0627\u0644\u0646\u0648\u0631 \u0644\u0644\u062a\u062c\u0627\u0631\u0629"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO supplier
			  (tenant_id, company_id, code, legal_name, name_ar, contact_name,
			   phone, country, payment_terms_days)
			VALUES ($1,$2,'S-0001','Riyadh Textiles Co.',$3,
			        'Faisal','+966500000002','sa',30)`,
			tenantID, companyID,
			"\u0634\u0631\u0643\u0629 \u0627\u0644\u0631\u064a\u0627\u0636 \u0644\u0644\u0646\u0633\u064a\u062c"); err != nil {
			return err
		}

		// Somewhere for the money to be.
		//
		// The chart has a Cash and a Bank account; a `money_account` is the
		// operational account that points at one. Without them the treasury
		// screens are empty and nothing can record a payment.
		for _, a := range []struct{ kind, name, arabic, code string }{
			{"cash", "Till Float", "\u0639\u0647\u062f\u0629 \u0627\u0644\u0635\u0646\u062f\u0648\u0642", "1100"},
			{"bank", "Al Rajhi Current", "\u0627\u0644\u0631\u0627\u062c\u062d\u064a \u0627\u0644\u062c\u0627\u0631\u064a", "1110"},
		} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO money_account
				  (tenant_id, company_id, account_id, kind, name, name_ar, currency,
				   bank_name)
				SELECT $1, $2, a.id, $3, $4, $5, 'SAR',
				       CASE WHEN $3 = 'bank' THEN 'Al Rajhi Bank' END
				FROM account a
				WHERE a.company_id = $2 AND a.code = $6`,
				tenantID, companyID, a.kind, a.name, a.arabic, a.code); err != nil {
				return err
			}
		}

		return nil
	})
}

// seedDocuments puts one of each transactional document into the shop.
//
// # Why this is not more INSERT statements
//
// A stock adjustment moves the Inventory account and a quotation reserves
// nothing until it is confirmed. Writing either as a raw row would produce a
// document the ledger disagrees with, which is exactly the sort of fixture
// that makes a green test meaningless. Both go through the services the API
// calls, so the seeded documents are posted the way real ones are.
//
// # Why they are seeded at all
//
// `verify:api` reads the first row of each list to check the shape a screen
// depends on, and answers "no payload" when the list is empty. Against a fresh
// database `GET /stock/adjustments` and `GET /orders` were both empty, so two
// screen contracts went unchecked -- and the run only ever passed because the
// development database had accumulated documents by hand. A verification that
// depends on what somebody happened to type last week is a memory, not a check.
func seedDocuments(
	ctx context.Context, pool *db.Pool, tenantID, ownerID uuid.UUID,
) error {
	var companyID, warehouseID, storeID, variantID, customerID, supplierID uuid.UUID
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT id FROM company LIMIT 1`).Scan(&companyID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM warehouse WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&warehouseID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM store WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&storeID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM variant WHERE company_id = $1 ORDER BY sku LIMIT 1`,
			companyID).Scan(&variantID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM customer WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&customerID); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT id FROM supplier WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&supplierID)
	}); err != nil {
		return err
	}

	// Opening stock, recorded as an adjustment: twenty found on the shelf.
	//
	// A shop with no stock cannot sell, so this is not only there to give the
	// list a row -- it is what makes the seeded till able to ring anything up.
	stock := stockops.NewService(pool)
	if _, err := stock.RecordAdjustment(ctx, stockops.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, stockops.NewAdjustment{
		UUID:        uuid.New(),
		WarehouseID: warehouseID,
		Kind:        stockops.KindAdjustment,
		Reason:      "found",
		Note:        "Opening stock for the demo shop, counted onto the shelf.",
		Lines: []stockops.NewAdjustmentLine{
			{VariantID: variantID, Delta: decimal.NewFromInt(20)},
		},
	}); err != nil {
		return fmt.Errorf("opening stock: %w", err)
	}

	// A quotation, which is what Raise always creates: confirming one is the
	// customer's decision, and a seeder that skipped that step would put "the
	// customer agreed" into a fixture.
	order := orders.NewService(pool)
	if _, err := order.Raise(ctx, orders.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, orders.NewOrder{
		CustomerID: &customerID,
		StoreID:    &storeID,
		Channel:    "store",
		Notes:      "Demo quotation.",
		Lines: []orders.NewLine{{
			VariantID: variantID,
			Qty:       decimal.NewFromInt(2),
			UnitPrice: decimal.NewFromInt(100),
		}},
	}); err != nil {
		return fmt.Errorf("demo quotation: %w", err)
	}

	// A supplier bill, recorded without a purchase order.
	//
	// `POID` is optional on purpose -- the electricity bill arrives without one
	// -- so this is a real shape rather than a shortcut round the three-way
	// match. The match is exercised by the tests; what the seed owes is a row
	// for `GET /purchasing/bills` to describe, which was the last screen
	// contract `verify:api` could not check against a fresh database.
	// WithRules, because every bill line is priced through the regulatory rule
	// registry: the service refuses outright without it rather than guessing a
	// tax rate, which is the correct refusal and the reason this is wired here
	// exactly as cmd/api wires it.
	buying := purchasing.NewService(pool).WithRules(registry.New(pool, false))
	if _, err := buying.RecordBill(ctx, purchasing.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, purchasing.NewBill{
		UUID:        uuid.New(),
		SupplierID:  supplierID,
		SupplierRef: "INV-2026-0001",
		BillDate:    time.Now().UTC(),
		Lines: []purchasing.BillLine{{
			VariantID:    &variantID,
			Description:  "Opening purchase",
			Qty:          decimal.NewFromInt(10),
			UnitCost:     decimal.NewFromInt(60),
			TaxTreatment: "standard",
			TaxRate:      decimal.RequireFromString("0.15"),
		}},
	}); err != nil {
		return fmt.Errorf("demo supplier bill: %w", err)
	}

	// A purchase order, issued and therefore open for receiving.
	//
	// The last list `verify:api` could not describe against a fresh database.
	// `GET /purchasing/orders` answered an empty page, so the seven fields the
	// buying list reads off an order went unchecked, and so did the receiving
	// screen behind it -- both only ever passed because the development
	// database had accumulated orders by hand.
	//
	// Issued rather than left a draft, because a draft is open for editing and
	// not for receiving: `status=issued` and `status=receiving` are the two
	// queries the receiving screen makes, and a draft answers neither. The
	// quantity is deliberately larger than the bill above so the order stays
	// partially outstanding instead of closing itself the moment it is made.
	po, err := buying.CreateOrder(ctx, purchasing.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, purchasing.NewOrder{
		SupplierID:  supplierID,
		WarehouseID: warehouseID,
		Notes:       "Demo purchase order, open for receiving.",
		Lines: []purchasing.OrderLine{{
			VariantID:    variantID,
			Description:  "Restock",
			Qty:          decimal.NewFromInt(25),
			UnitCost:     decimal.NewFromInt(60),
			TaxTreatment: "standard",
			TaxRate:      decimal.RequireFromString("0.15"),
		}},
	})
	if err != nil {
		return fmt.Errorf("demo purchase order: %w", err)
	}
	if _, err := buying.IssueOrder(ctx, purchasing.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, po.ID); err != nil {
		return fmt.Errorf("issue demo purchase order: %w", err)
	}

	return nil
}

// seedPeople gives the shop staff, and the records staff generate.
//
// # Why
//
// Seven screen contracts went unchecked against a fresh database — the
// employee row, the expiring-document alert, an attendance day, a leave
// request, an advance, a payroll run and an end-of-service position — because
// the demo shop employed nobody. `verify:api` reported each of them as "not
// exercised" and moved on, so the fields those screens read were only ever
// confirmed against a development database somebody had typed staff into
// months earlier. A verification that depends on that is a memory, not a check.
//
// Everything here goes through the same services the API calls, so a seeded
// employee is hired the way a real one is: numbered, posted where posting
// applies, and refused by the same validation.
func seedPeople(
	ctx context.Context, pool *db.Pool, tenantID, ownerID uuid.UUID,
) error {
	var companyID, storeID, cashAccountID uuid.UUID
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT id FROM company LIMIT 1`).Scan(&companyID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM store WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&storeID); e != nil {
			return e
		}
		// An advance is money leaving a real account, so it resolves a
		// money_account rather than a chart account -- the same distinction
		// that cost a screen an afternoon, recorded here so the seed cannot
		// quietly reintroduce it.
		return tx.QueryRow(ctx, `
			SELECT id FROM money_account
			WHERE company_id = $1 AND kind = 'cash'
			ORDER BY created_at LIMIT 1`, companyID).Scan(&cashAccountID)
	}); err != nil {
		return err
	}

	staff := people.NewService(pool, registry.New(pool, false))
	scope := people.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
		MaySeePay: true,
	}

	now := time.Now().UTC()

	// Two people, and the second one's residency permit expires inside the
	// window C5's alert asks about. Without somebody close to expiry the alert
	// list is empty and its row shape is never described -- and that list is
	// the one that stops a cashier turning up to work unable to work legally.
	soon := now.AddDate(0, 0, 21)
	longAgo := now.AddDate(-6, -2, 0)

	supervisor, err := staff.Hire(ctx, scope, people.NewEmployee{
		FullName: "Nadia Haddad", NameAr: "نادية حداد",
		Position: "Store supervisor", Department: "Retail",
		StoreID:  &storeID,
		JoinedOn: longAgo,
		IsSaudi:  true, Nationality: "SA",
		NationalID: "1098765432", GOSINumber: "1234567890",
		IBAN:    "SA0380000000608010167519",
		Basic:   decimal.NewFromInt(7000),
		Housing: decimal.NewFromInt(1750),
	})
	if err != nil {
		return fmt.Errorf("hire the supervisor: %w", err)
	}

	cashier, err := staff.Hire(ctx, scope, people.NewEmployee{
		FullName: "Imran Qureshi", NameAr: "عمران قريشي",
		Position: "Cashier", Department: "Retail",
		StoreID:  &storeID,
		JoinedOn: now.AddDate(-1, -3, 0),
		IsSaudi:  false, Nationality: "PK",
		IqamaNo: "2345678901", IDExpiresOn: &soon,
		GOSINumber: "2234567890",
		IBAN:       "SA4420000001234567891234",
		Basic:      decimal.NewFromInt(4000),
		Housing:    decimal.NewFromInt(1000),
		Transport:  decimal.NewFromInt(400),
	})
	if err != nil {
		return fmt.Errorf("hire the cashier: %w", err)
	}

	// A worked day each, so the attendance grid has something in it. Recorded
	// for yesterday rather than today: a day still in progress is the one case
	// where an empty grid is correct.
	yesterday := now.AddDate(0, 0, -1)
	if _, err := staff.RecordAttendance(ctx, scope, []people.NewAttendance{
		{
			EmployeeID: supervisor.ID, OnDate: yesterday, Status: "present",
			Hours: decimal.NewFromInt(8),
		},
		{
			EmployeeID: cashier.ID, OnDate: yesterday, Status: "present",
			Hours: decimal.NewFromInt(8), Overtime: decimal.NewFromInt(2),
			LateMins: 15,
		},
	}); err != nil {
		return fmt.Errorf("record attendance: %w", err)
	}

	// A leave request, left undecided: the queue a manager opens is the
	// pending one, and a request already approved would leave it empty.
	if _, err := staff.RequestLeave(ctx, scope, cashier.ID, "annual", true,
		now.AddDate(0, 1, 0), now.AddDate(0, 1, 4), decimal.Zero,
		"Family visit."); err != nil {
		return fmt.Errorf("request leave: %w", err)
	}

	if _, err := staff.IssueAdvance(ctx, scope, cashier.ID, cashAccountID,
		decimal.NewFromInt(1000), 4, "Advance against wages."); err != nil {
		return fmt.Errorf("issue an advance: %w", err)
	}

	// Prepared and left unapproved. A run in progress is the state the payroll
	// screen is for -- approving and paying it are decisions a person makes,
	// and a seeder that made them would put "the owner approved this" into a
	// fixture.
	if _, err := staff.Prepare(ctx, scope, now,
		"Seeded run, prepared and awaiting approval."); err != nil {
		return fmt.Errorf("prepare payroll: %w", err)
	}

	return nil
}

// seedTrading takes the demo shop's quotation all the way to money.
//
// # Why
//
// A shop that has quoted and never sold has an empty ledger, an empty ageing,
// no sale on the dashboard and no receipt to collect an instalment against.
// Six screen contracts went unchecked against a fresh database for want of one
// completed sale, and `verify:api` said so on each of them.
//
// The order is walked forward one state at a time rather than written as
// `completed`, because the ladder is the product: confirming reserves stock,
// delivering releases the hold, and invoicing is what completes it. A fixture
// that set the end state would describe an order the rest of the system does
// not believe in.
//
// It is invoiced ON ACCOUNT — no tenders — so the customer owes it. That is
// what puts a row in the ageing, the ledger and the open-invoice list, and it
// is the shape a wholesale shop's books are actually in.
func seedTrading(
	ctx context.Context, pool *db.Pool, tenantID, ownerID uuid.UUID,
) error {
	var companyID, orderID, customerID uuid.UUID
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT id FROM company LIMIT 1`).Scan(&companyID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT id, customer_id FROM sales_order
			WHERE company_id = $1 AND state = 'quotation'
			ORDER BY created_at LIMIT 1`, companyID).Scan(&orderID, &customerID)
	}); err != nil {
		return err
	}

	rules := registry.New(pool, false)
	salesSvc := sales.NewService(zatca.NewChain(pool, zatca.StandardHasher{})).
		WithPool(pool).WithRegistry(rules).
		WithPromotions(promotions.NewService(pool))
	order := orders.NewService(pool).WithSales(salesSvc)
	scope := orders.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}

	// quotation -> confirmed -> processing -> packed -> delivered.
	for range 4 {
		if _, err := order.Advance(ctx, scope, orderID); err != nil {
			return fmt.Errorf("advance the demo order: %w", err)
		}
	}

	invoiced, err := order.Invoice(ctx, scope, orderID, orders.InvoiceRequest{
		UUID: uuid.New(),
	})
	if err != nil {
		return fmt.Errorf("invoice the demo order: %w", err)
	}

	// Part-paid, deliberately. A fully settled invoice leaves nothing in the
	// ageing and nothing for a collection screen to be about, and a receipt
	// with money still on it is what the instalment picker offers.
	money := receivables.NewService(pool)
	if _, err := money.TakePayment(ctx, receivables.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, receivables.NewReceipt{
		UUID:       uuid.New(),
		CustomerID: customerID,
		Method:     "cash",
		Reference:  "Demo part payment.",
		ReceivedOn: time.Now().UTC(),
		Allocations: []receivables.Allocation{{
			InvoiceID: invoiced.InvoiceID,
			Amount:    decimal.NewFromInt(50),
		}},
	}); err != nil {
		return fmt.Errorf("take a part payment: %w", err)
	}

	return nil
}

// seedConfiguration writes the settings a shop configures once, and the money
// records the back office reads.
//
// # Why
//
// Every list below answered an empty page against a fresh database, so
// verify:api could describe none of their rows: a promotion, an approval rule,
// a commission scheme, a nested department, a label layout, an exchange rate,
// a bank account, a transfer between accounts, an expense, a standing cost, an
// asset and an investor.
//
// These are configuration rather than trade, which is exactly why a demo shop
// never grows them by accident: nobody sells anything that causes a label
// layout to exist.
func seedConfiguration(
	ctx context.Context, pool *db.Pool, tenantID, ownerID uuid.UUID,
) error {
	var companyID, storeID, supplierID, cashAccountID uuid.UUID
	var currency string
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT id, base_currency FROM company LIMIT 1`).
			Scan(&companyID, &currency); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM store WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&storeID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM supplier WHERE company_id = $1 LIMIT 1`,
			companyID).Scan(&supplierID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT id FROM money_account
			WHERE company_id = $1 AND kind = 'cash'
			ORDER BY created_at LIMIT 1`, companyID).Scan(&cashAccountID)
	}); err != nil {
		return err
	}

	rules := registry.New(pool, false)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// --- The catalogue's own shape ---------------------------------------

	// A department with a department inside it. A flat list never exercises
	// the indentation, and the tree is the part of a category that can be
	// wrong: path and depth are computed on every write.
	cat := catalog.NewService(pool, rules)
	parent, err := cat.CreateCategory(ctx, tenantID, companyID, ownerID,
		catalog.NewCategory{Name: "Beverages", NameAr: "مشروبات"})
	if err != nil {
		return fmt.Errorf("create a category: %w", err)
	}
	if _, err := cat.CreateCategory(ctx, tenantID, companyID, ownerID,
		catalog.NewCategory{
			Name: "Hot drinks", NameAr: "مشروبات ساخنة", ParentID: &parent.ID,
		}); err != nil {
		return fmt.Errorf("nest a category: %w", err)
	}

	// A shelf-edge label, so the studio has a layout to print with.
	sheetCols, sheetRows := 3, 8
	if _, err := labels.NewService(pool, rules).SaveTemplate(ctx, labels.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, nil, labels.Template{
		Name: "Shelf edge, A4", Kind: labels.KindA4Sheet,
		Width: "63.5", Height: "38.1", Margin: "5", Gap: "2",
		Columns: &sheetCols, Rows: &sheetRows,
		Fields: []labels.Field{
			{Field: "name", Size: 10, Bold: true},
			{Field: "price", Size: 12, Bold: true},
			{Field: "barcode", Height: 12},
		},
		IsDefault: true,
	}); err != nil {
		return fmt.Errorf("save a label layout: %w", err)
	}

	// A campaign, running now so the till can quote it.
	ends := now.AddDate(0, 1, 0)
	if _, err := promotions.NewService(pool).Create(ctx, promotions.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, promotions.NewPromotion{
		Code: "WELCOME10", Name: "Ten per cent off", NameAr: "خصم عشرة بالمئة",
		Kind: "percentage", Value: decimal.NewFromInt(10),
		StartsOn: &monthStart, EndsOn: &ends,
	}); err != nil {
		return fmt.Errorf("create a promotion: %w", err)
	}

	// --- Who signs things off ---------------------------------------------

	flow := workflow.NewService(pool)
	if _, err := flow.SaveRule(ctx, workflow.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}, workflow.Rule{
		Name:      "Expenses over 5,000 need the owner",
		Subject:   "expense",
		Condition: `{"amount_over": "5000"}`,
		Action:    "require_approval",
		Steps:     `[{"role": "owner"}]`,
		IsActive:  true,
	}); err != nil {
		return fmt.Errorf("save an approval rule: %w", err)
	}

	// --- What people are paid on top of wages ------------------------------

	if _, err := people.NewService(pool, rules).SetCommissionRule(ctx,
		people.Scope{
			TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
			MaySeePay: true,
		},
		"Counter staff, 2% of revenue", "revenue",
		people.CommissionScope{StoreID: &storeID},
		decimal.RequireFromString("0.02"), "",
		monthStart, nil); err != nil {
		return fmt.Errorf("set a commission scheme: %w", err)
	}

	// --- Money ------------------------------------------------------------

	// A rate, so a foreign-currency figure has something to be converted at.
	other := "USD"
	if currency == "USD" {
		other = "SAR"
	}
	if _, err := fx.New(pool).Record(ctx, fx.Scope{
		TenantID: tenantID, UserID: ownerID,
	}, other, currency, decimal.RequireFromString("3.75"), now,
		"seed", "Indicative rate for development."); err != nil {
		return fmt.Errorf("record an exchange rate: %w", err)
	}

	cash := treasury.NewService(pool)
	trScope := treasury.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}

	// The bank account money is moved INTO. Provisioning already opens one
	// beside the cash drawer -- one money_account per chart account, which is
	// what money_account_ledger_uq holds -- so this finds it rather than
	// opening a second one against the same ledger account.
	var bankAccountID uuid.UUID
	if err := pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id FROM money_account
			WHERE company_id = $1 AND kind = 'bank'
			ORDER BY created_at LIMIT 1`, companyID).Scan(&bankAccountID)
	}); err != nil {
		return fmt.Errorf("find the bank account: %w", err)
	}

	if _, err := cash.Move(ctx, trScope, treasury.NewTransfer{
		UUID:          uuid.New(),
		FromAccountID: cashAccountID,
		ToAccountID:   bankAccountID,
		Amount:        decimal.NewFromInt(500),
		MovedOn:       now,
		Reference:     "Banking the float.",
	}); err != nil {
		return fmt.Errorf("bank the takings: %w", err)
	}

	// --- What the shop spends ---------------------------------------------

	spend := expenses.NewService(pool, rules)
	exScope := expenses.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}
	heads, err := spend.Heads(ctx, exScope, false)
	if err != nil {
		return fmt.Errorf("read the expense heads: %w", err)
	}
	if len(heads) == 0 {
		return fmt.Errorf("no expense heads are seeded, so nothing can be spent")
	}
	head := heads[0]

	if _, err := spend.Record(ctx, exScope, expenses.NewExpense{
		UUID: uuid.New(), Date: now, StoreID: &storeID,
		SupplierID: &supplierID,
		Reference:  "UTIL-0001", Description: "Electricity for the month.",
		PaidFrom: "cash",
		Lines: []expenses.NewLine{{
			HeadID: head.ID, Description: "Electricity",
			Net: decimal.NewFromInt(400), TaxTreatment: "standard",
		}},
	}); err != nil {
		return fmt.Errorf("record an expense: %w", err)
	}

	if _, err := spend.CreateRecurring(ctx, exScope, expenses.NewRecurring{
		Name: "Shop rent", HeadID: head.ID, StoreID: &storeID,
		Amount: decimal.NewFromInt(9000), PaidFrom: "bank",
		Description: "Monthly rent on the shop.",
		Frequency:   "monthly", Interval: 1,
		StartsOn: monthStart,
	}); err != nil {
		return fmt.Errorf("create a standing cost: %w", err)
	}

	// --- What the shop owns, and who put money in --------------------------

	fixed := assets.NewService(pool)
	asScope := assets.Scope{
		TenantID: tenantID, CompanyID: companyID, UserID: ownerID,
	}
	if _, err := fixed.Add(ctx, asScope, assets.NewAsset{
		Name: "Chiller cabinet", NameAr: "ثلاجة عرض", Category: "equipment",
		StoreID: &storeID, SerialNumber: "CHL-2026-001",
		AcquiredOn: now.AddDate(0, -6, 0),
		Cost:       decimal.NewFromInt(18000),
		Residual:   decimal.NewFromInt(1800),
		LifeMonths: 60,
	}); err != nil {
		return fmt.Errorf("register an asset: %w", err)
	}

	if _, err := fixed.AddInvestor(ctx, asScope, assets.NewInvestor{
		Name: "Demo Retail Holdings", NameAr: "القابضة", Kind: "owner",
		Email: "holdings@example.test",
		Note:  "The founding shareholder.",
	}); err != nil {
		return fmt.Errorf("add an investor: %w", err)
	}

	return nil
}
