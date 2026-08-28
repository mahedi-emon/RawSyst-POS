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

	// The role is owner_capital because that is the name rule 12 resolves, and
	// the label is the one design 12 §1 gives account 3100. This was seeded as
	// "Owner's Equity" with a matching role until 0053, which meant every
	// capital contribution the engine tried to post would have failed on an
	// unmapped role — the same shape of defect as the cost_variance mapping
	// 0048 had to correct, caught this time while the module is still dormant.
	{"3100", "Owner Capital", "equity", "owner_capital", ""},
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
	// 5400, not 5200, because design 12 §1 puts Inventory Write-off there and
	// 5200 is Rent. This was seeded at 5200 until 0071, which relabelled it —
	// the account keeps its id, so nothing moved and no journal line changed.
	{"5400", "Stock Write-off", "expense", "stock_writeoff", ""},

	// The four heads design 12 §1 names, so a shop can record rent on the day
	// it installs the product rather than building a chart first. 0071 seeds an
	// expense head against each of them.
	//
	// The roles are here so the head seed can find the account by name rather
	// than by a code a shop may have changed. Nothing RESOLVES them today — an
	// expense head names its account, which is the whole point of the model —
	// but rule 6 will want expense_salaries when payroll is built.
	{"5200", "Rent", "expense", "expense_rent", ""},
	{"5210", "Utilities", "expense", "expense_utilities", ""},
	{"5220", "Salaries", "expense", "expense_salaries", ""},
	{"5230", "Marketing", "expense", "expense_marketing", ""},
	// What it costs to be paid by card. Design 12 §1 gives it 5300. Separate
	// from the clearing account on purpose: the clearing account is money owed
	// to the shop, this is money the shop never receives, and merging them
	// would leave a residue in an account whose whole job is to reach zero.
	{"5300", "Bank & Card Charges", "expense", "bank_card_charges", ""},
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

	return seedExpenseHeads(ctx, tx, tenantID, companyID)
}

// defaultExpenseHeads are the categories design 12 §1's chart implies.
//
// Every one is `input_vat_recoverable`, and that is not a default anybody chose
// — it is what the documents say. Blueprint E2.3 and design 02 rule 5 restrict
// entertainment, some vehicles and fuel; none of these is on that list.
//
// Heads for the restricted categories are deliberately absent. Design 12's
// chart has no accounts for them, and inventing an "Entertainment" account so
// the recoverability flag had something to demonstrate itself on would be the
// same kind of invention P32 refused when it declined to pick one account for
// every cash expense.
var defaultExpenseHeads = []struct {
	code, name, nameAr, role string
}{
	{"RENT", "Rent", "الإيجار", "expense_rent"},
	{"UTILITIES", "Utilities", "المرافق", "expense_utilities"},
	{"SALARIES", "Salaries", "الرواتب", "expense_salaries"},
	{"MARKETING", "Marketing", "التسويق", "expense_marketing"},
	{"BANKFEES", "Bank charges", "رسوم بنكية", "bank_card_charges"},
}

// seedExpenseHeads gives a new company something to book an expense to.
//
// Seeded with the chart rather than beside it, because a head is meaningless
// without the account it posts to and the two drifting apart is exactly how a
// company ends up able to record an expense against nothing.
//
// Idempotent, like the chart: a retried onboarding step must not produce a
// second Rent, and a company that has renamed or repointed a head keeps its
// version.
func seedExpenseHeads(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
) error {
	for _, h := range defaultExpenseHeads {
		if _, err := tx.Exec(ctx, `
			INSERT INTO expense_head
			  (tenant_id, company_id, code, name, name_ar, account_id,
			   input_vat_recoverable)
			SELECT $1, $2, $3, $4, $5, m.account_id, true
			FROM account_role_map m
			WHERE m.company_id = $2 AND m.role = $6
			ON CONFLICT DO NOTHING`,
			tenantID, companyID, h.code, h.name, h.nameAr, h.role); err != nil {
			return db.Translate(err,
				"That company's expense categories could not be created.")
		}
	}
	return nil
}
