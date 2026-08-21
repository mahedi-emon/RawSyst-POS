package provisioning

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Every company gets a chart of accounts when it is created.
//
// This closes a gap that only showed itself once exchanges were built. Posting
// rules are data and name ACCOUNT ROLES rather than accounts — "debit cash",
// not "debit 1100" — and the role is resolved per company through
// account_role_map. That indirection is what lets one rule serve every company,
// and it means a company with no mapping cannot post at all.
//
// Nothing was creating those mappings. Tests seeded them by hand and real
// deployments were expected to, so the first sale in a newly onboarded company
// would have failed on a missing role. The failure was at least loud and named
// the role, but "loud" is not the same as "works".
//
// # These are a starting point, not a prescription
//
// The codes and names below are conventional retail defaults. An owner or their
// accountant is expected to rename, renumber and extend them — a chart of
// accounts belongs to the business, and no software should tell a company what
// its ledger looks like. What must not change is the ROLE each account is
// mapped to, because that is what the posting engine resolves.
//
// # Deliberately minimal
//
// Only the accounts the implemented modules actually post to. Seeding a full
// retail chart would leave an owner scrolling past forty empty accounts for
// modules that do not exist yet, and would make the ones that matter harder to
// find. Purchases, payroll and fixed assets bring their own when they arrive.
type seedAccount struct {
	code string
	name string
	kind string
	role string
	// control marks an account whose balance must reconcile to a subsidiary
	// ledger — inventory to the stock valuation, receivables to the customer
	// balances. The posting engine enforces the tie-out.
	control string
}

// The order is the order they appear in the chart: assets, liabilities,
// equity, revenue, expenses — the sequence every accountant expects.
var defaultChart = []seedAccount{
	{"1100", "Cash", "asset", "cash", ""},
	{"1110", "Bank", "asset", "bank", ""},
	// Card money that has been taken but not yet settled by the acquirer (C12).
	// Its own account because an owner asking "where is my money" needs to see
	// that it exists and is not yet theirs to spend.
	{"1150", "Card Settlement Clearing", "asset", "card_clearing", ""},
	{"1200", "Accounts Receivable", "asset", "accounts_receivable", "receivable"},
	{"1400", "Inventory", "asset", "inventory", "inventory"},

	{"2100", "Accounts Payable", "liability", "accounts_payable", "payable"},
	// Goods on the shelf that the supplier has not invoiced yet. Without it the
	// inventory valuation runs ahead of the Inventory control account for the
	// whole window between a delivery and its bill, which design 02 §6.6 says
	// must never happen.
	{"2150", "Goods Received Not Invoiced", "liability", "grni", ""},
	{"2200", "Output VAT Payable", "liability", "output_vat", ""},
	{"2210", "Input VAT Recoverable", "asset", "input_vat", ""},
	{"2300", "Store Credit Issued", "liability", "store_credit_liability", ""},
	// The offsetting half of an exchange. Zero between exchanges; a balance on
	// it means one settled half and not the other, which the atomic
	// transaction is supposed to make impossible.
	{"2350", "Exchange Clearing", "liability", "exchange_clearing", ""},
	{"2400", "Loyalty Points Liability", "liability", "loyalty_liability", ""},

	{"3100", "Owner's Equity", "equity", "owners_equity", ""},
	{"3200", "Retained Earnings", "equity", "retained_earnings", ""},

	{"4100", "Sales Revenue", "revenue", "sales_revenue", ""},
	{"4200", "Sales Discounts", "revenue", "sales_discounts", ""},

	{"5100", "Cost of Goods Sold", "expense", "cogs", ""},
	// Where a standard-costing difference lands, and where an allow_warn
	// shortfall's provisional cost is corrected (C13).
	//
	// The role is cost_variance because that is the name the posting rules
	// resolve — rules 11 and 11a, seeded by 0025 and 0026. This account was
	// mapped to inventory_variance until 0048, which meant every variance the
	// engine tried to post failed on an unmapped role in any company created
	// through the product. The tests that covered rule 11 mapped the role by
	// hand and so never saw it.
	{"5150", "Inventory Cost Variance", "expense", "cost_variance", ""},
	{"5200", "Stock Write-off", "expense", "stock_writeoff", ""},
	// Where a drawer that did not reconcile lands (C8, design 11 §9). Both
	// directions post here: an unexplained surplus is as much a control failure
	// as a shortfall, and sending an overage to Other Income would flatter the
	// month it happened in.
	{"5500", "Cash Over/Short", "expense", "cash_over_short", ""},
	{"5900", "Rounding Differences", "expense", "rounding", ""},
}

// SeedChartOfAccounts gives a new company the accounts its modules post to.
//
// Idempotent on both tables. A retried onboarding step must not produce a
// second Cash account, and a company that already has a chart — imported, or
// created before this existed — keeps it: ON CONFLICT DO NOTHING means an
// existing mapping is never repointed at a new account, which would silently
// split a balance across two accounts mid-year.
//
// Exported because a company can be created by more than the onboarding
// wizard — an import, a migration, admin tooling — and every one of those
// paths needs the same mappings or the company cannot post a sale.
func SeedChartOfAccounts(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
) error {
	for _, a := range defaultChart {
		var accountID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO account
			  (tenant_id, company_id, code, name, type, is_control, control_of)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (company_id, code) DO UPDATE SET code = EXCLUDED.code
			RETURNING id`,
			tenantID, companyID, a.code, a.name, a.kind,
			a.control != "", nullIfBlank(a.control)).Scan(&accountID)
		if err != nil {
			return db.Translate(err, "That chart of accounts could not be created.")
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (company_id, role) DO NOTHING`,
			tenantID, companyID, a.role, accountID); err != nil {
			return db.Translate(err, "That chart of accounts could not be created.")
		}
	}
	return nil
}
