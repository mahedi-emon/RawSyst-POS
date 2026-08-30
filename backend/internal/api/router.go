// Package api wires HTTP routes to handlers.
//
// # The route registry
//
// Every route is declared in Routes() as data before it is mounted. That is not
// bookkeeping: it makes two properties testable that are otherwise only
// conventions.
//
//   - Every tenant route names the permission it requires. A route added
//     without one fails TestEveryRouteDeclaresAPermission, so QA gate M7 cannot
//     be bypassed by forgetting rather than by deciding.
//   - The full permission surface is enumerable, so the seeded roles can be
//     checked against it and a typo like "sales.refnud" is caught by a test
//     rather than by a cashier who cannot issue a refund.
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/assets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fiscal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/integration"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/loyalty"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/ops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/orders"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/people"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portability"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/settlement"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/stockops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/treasury"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/workflow"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// Access says who may reach a route.
type Access int

const (
	// AccessPublic is reachable without a token. Deliberately rare: sign-in,
	// token refresh, and health checks only.
	AccessPublic Access = iota

	// AccessAuthenticated needs a valid token but no particular permission.
	// Used only for "about the caller themselves" routes, where the identity in
	// the token IS the authorization.
	AccessAuthenticated

	// AccessPermission needs a named permission.
	AccessPermission

	// AccessSuperAdmin is the platform control plane.
	AccessSuperAdmin
)

// Route is one endpoint.
type Route struct {
	Method     string
	Pattern    string
	Access     Access
	Permission string // required when Access is AccessPermission
	Handler    http.HandlerFunc

	// Why records the reason a route is public or permission-free. Required for
	// AccessPublic and AccessAuthenticated, so widening access is a deliberate,
	// reviewed act rather than a default someone drifted into.
	Why string
}

// Server holds the handler dependencies.
type Server struct {
	auth         *identity.Service
	mw           *identity.Middleware
	authz        *identity.Authorizer
	provisioning *provisioning.Service
	sales        *sales.Service
	reports      *reports.Service
	vat          *vat.Service
	catalog      *catalog.Service
	sync         *sync.Engine
	purchasing   *purchasing.Service
	receivables  *receivables.Service
	devices      *devices.Service
	egs          *egs.Service
	branding     *branding.Service
	shift        *shift.Service
	settlement   *settlement.Service
	expenses     *expenses.Service
	stock        *stockops.Service
	fiscal       *fiscal.Service
	treasury     *treasury.Service
	assets       *assets.Service
	promotions   *promotions.Service
	orders       *orders.Service
	loyalty      *loyalty.Service
	wallet       *wallet.Service
	workflow     *workflow.Service
	notify       *notify.Service
	integrations *integration.Service
	portability  *portability.Service
	ops          *ops.Service
	aftersales   *aftersales.Service
	people       *people.Service
	audit        *audit.Service
	health       func() error
	version      string

	// queue is how a recovery code reaches a mailbox: the send is a
	// `notify.send` job rather than I/O inside the request, so a mail provider
	// being down cannot make the reset endpoint unavailable.
	//
	// Optional. An installation with no queue wired still serves every other
	// route; the recovery ones report that they cannot send rather than
	// panicking, which is the honest failure for a feature that needs
	// infrastructure somebody has not set up.
	queue identity.Enqueuer

	// recoveryLimit caps the two PUBLIC recovery routes per caller. See
	// recovery_handlers.go for why it is in memory and what that costs.
	recoveryLimit *recoveryLimiter

	// onboarding is optional: an installation with no data encryption key
	// cannot hold a ZATCA credential, and the routes report that rather than
	// failing to start.
	onboarding *zatca.Onboarding

	// secureCookies marks the session cookies Secure, so a browser will only
	// send them over TLS. False only in development, where the browser talks
	// to the API over plain HTTP on localhost and would silently DROP a Secure
	// cookie -- which presents as a sign-in that appears to work and then has
	// no session, the least diagnosable failure available here.
	secureCookies bool
}

// WithSecureCookies marks session cookies Secure. Call it for any deployment
// served over TLS, which is every deployment except a developer's laptop.
func (s *Server) WithSecureCookies(secure bool) *Server {
	s.secureCookies = secure
	return s
}

// WithOnboarding enables the ZATCA onboarding routes.
func (s *Server) WithOnboarding(o *zatca.Onboarding) *Server {
	s.onboarding = o
	return s
}

func NewServer(
	auth *identity.Service,
	mw *identity.Middleware,
	authz *identity.Authorizer,
	prov *provisioning.Service,
	salesSvc *sales.Service,
	reportSvc *reports.Service,
	vatSvc *vat.Service,
	catalogSvc *catalog.Service,
	syncEngine *sync.Engine,
	purchasingSvc *purchasing.Service,
	receivablesSvc *receivables.Service,
	devicesSvc *devices.Service,
	egsSvc *egs.Service,
	brandingSvc *branding.Service,
	shiftSvc *shift.Service,
	settlementSvc *settlement.Service,
	expenseSvc *expenses.Service,
	stockSvc *stockops.Service,
	fiscalSvc *fiscal.Service,
	treasurySvc *treasury.Service,
	assetSvc *assets.Service,
	promotionSvc *promotions.Service,
	orderSvc *orders.Service,
	loyaltySvc *loyalty.Service,
	walletSvc *wallet.Service,
	workflowSvc *workflow.Service,
	notifySvc *notify.Service,
	integrationSvc *integration.Service,
	portabilitySvc *portability.Service,
	opsSvc *ops.Service,
	aftersalesSvc *aftersales.Service,
	peopleSvc *people.Service,
	auditSvc *audit.Service,
	health func() error,
	version string,
) *Server {
	return &Server{
		auth:         auth,
		mw:           mw,
		authz:        authz,
		provisioning: prov,
		sales:        salesSvc,
		reports:      reportSvc,
		vat:          vatSvc,
		catalog:      catalogSvc,
		sync:         syncEngine,
		purchasing:   purchasingSvc,
		receivables:  receivablesSvc,
		devices:      devicesSvc,
		egs:          egsSvc,
		branding:     brandingSvc,
		shift:        shiftSvc,
		settlement:   settlementSvc,
		expenses:     expenseSvc,
		stock:        stockSvc,
		fiscal:       fiscalSvc,
		treasury:     treasurySvc,
		assets:       assetSvc,
		promotions:   promotionSvc,
		orders:       orderSvc,
		loyalty:      loyaltySvc,
		wallet:       walletSvc,
		workflow:     workflowSvc,
		notify:       notifySvc,
		integrations: integrationSvc,
		portability:  portabilitySvc,
		ops:          opsSvc,
		aftersales:   aftersalesSvc,
		people:       peopleSvc,
		audit:        auditSvc,
		health:       health,
		version:      version,

		// Ten attempts a quarter hour from one caller. Generous for somebody
		// mistyping a code off a screen, useless for walking an address list.
		recoveryLimit: newRecoveryLimiter(10, 15*time.Minute),
	}
}

// WithQueue supplies the background queue, which is how a recovery code reaches
// a mailbox.
//
// Optional rather than a constructor argument: every other route works without
// it, and an installation that has not set up a mail provider should still
// start and serve. The recovery routes report that they cannot send.
func (s *Server) WithQueue(q identity.Enqueuer) *Server {
	s.queue = q
	return s
}

// Routes returns every route as data.
func (s *Server) Routes() []Route {
	return []Route{
		// --- public ---
		{http.MethodGet, "/healthz", AccessPublic, "", s.handleLive,
			"liveness probe; must answer before any dependency is reachable"},
		{http.MethodGet, "/readyz", AccessPublic, "", s.handleReady,
			"readiness probe for the load balancer"},
		{http.MethodGet, "/api/v1/meta/version", AccessPublic, "", s.handleVersion,
			"build version, so a support call can establish what the client is running"},

		{http.MethodPost, "/api/v1/auth/login", AccessPublic, "", s.handleLogin,
			"sign-in is how a caller obtains a token; it cannot require one"},
		{http.MethodPost, "/api/v1/auth/refresh", AccessPublic, "", s.handleRefresh,
			"the access token is expired by definition; the refresh token authenticates"},

		// --- authenticated, no permission ---
		{http.MethodPost, "/api/v1/auth/logout", AccessAuthenticated, "", s.handleLogout,
			"ends the caller's own session; the token is the authorization"},
		{http.MethodGet, "/api/v1/auth/me", AccessAuthenticated, "", s.handleMe,
			"returns the caller's own identity and permissions, so the client can shape its UI"},
		{http.MethodPost, "/api/v1/auth/change-password", AccessAuthenticated, "", s.handleChangePassword,
			"changes the caller's own password; the current password is re-verified"},

		// --- self-service recovery (blueprint A4.2) ---
		//
		// Public, necessarily: the caller has lost the way to get a token. That
		// is safe because neither route says whether an account exists, and
		// guessing is bounded per code, per account and per caller.
		{http.MethodPost, "/api/v1/auth/forgot-password", AccessPublic, "",
			s.handleForgotPassword,
			"asks for a reset code. Answers 204 whether or not the address is on an account, because an endpoint that distinguishes them confirms which of a leaked address list are customers"},
		{http.MethodPost, "/api/v1/auth/reset-password", AccessPublic, "",
			s.handleResetPassword,
			"exchanges a code for a new password. One refusal for a wrong, expired, spent or unknown code, for the same reason"},

		// --- staff (blueprint A5, A6) ---
		//
		// Three verbs, not one. Reading the list, keeping it current, and
		// deciding what somebody may do are separately dangerous: an office
		// manager can reasonably keep a staff list up to date without also
		// being able to hand somebody the bank ledger.
		//
		// `identity.manage_roles` additionally carries the subset rule — a
		// role may only be assigned by somebody who holds everything in it —
		// so delegation cannot become escalation.
		{http.MethodGet, "/api/v1/people", AccessPermission, "identity.view",
			s.handleListPeople,
			"everybody in the business and the roles they hold"},
		{http.MethodGet, "/api/v1/people/roles", AccessPermission, "identity.view",
			s.handleListRoles,
			"the roles this tenant can assign, marked with which the caller may hand over"},
		{http.MethodPost, "/api/v1/people", AccessPermission, "identity.create",
			s.handleCreatePerson,
			"adds a member of staff and issues their one-time password; also needs identity.manage_roles, because adding somebody means deciding what they may do"},
		{http.MethodPut, "/api/v1/people/{userID}", AccessPermission, "identity.create",
			s.handleUpdatePerson,
			"changes a name, phone or sign-in address"},
		{http.MethodPost, "/api/v1/people/{userID}/active", AccessPermission, "identity.create",
			s.handleSetPersonActive,
			"suspends or restores somebody; never deletes, because their name is on the invoices they rang up"},
		{http.MethodPost, "/api/v1/people/{userID}/reset-password", AccessPermission, "identity.create",
			s.handleResetPersonPassword,
			"issues a new one-time password for a member of staff; the Owner-level twin of A4.2"},
		{http.MethodPost, "/api/v1/people/{userID}/roles", AccessPermission, "identity.manage_roles",
			s.handleAssignRole,
			"gives somebody a role, scoped by company, store, warehouse, amount and time window"},
		{http.MethodDelete, "/api/v1/people/roles/{assignmentID}", AccessPermission, "identity.manage_roles",
			s.handleRemoveAssignment,
			"takes a role away; refuses your own last one, which would leave you signed in and unable to act"},
		{http.MethodGet, "/api/v1/catalog/snapshot", AccessPermission, "catalog.view", s.handleCatalogSnapshot,
			"the sellable catalogue a till caches to scan offline; cursored so later pulls are deltas"},
		{http.MethodGet, "/api/v1/meta/ping", AccessAuthenticated, "", s.handlePing,
			"a terminal asking whether it can sync; the token IS the answer, so no permission applies"},

		// --- onboarding (blueprint A5) ---
		//
		// Gated on identity.view rather than a dedicated permission: setup is
		// the Owner's job, and the Owner role already holds it. Inventing an
		// onboarding-only permission would mean every tenant's custom roles
		// need updating before anyone can finish setup.
		{http.MethodGet, "/api/v1/onboarding", AccessPermission, "identity.view",
			s.handleOnboardingProgress, ""},
		{http.MethodPut, "/api/v1/onboarding/steps/{step}", AccessPermission, "identity.edit",
			s.handleOnboardingSaveStep, ""},
		{http.MethodPost, "/api/v1/onboarding/steps/{step}/complete", AccessPermission, "identity.edit",
			s.handleOnboardingCompleteStep, ""},
		{http.MethodPost, "/api/v1/onboarding/company", AccessPermission, "identity.edit",
			s.handleOnboardingCommitCompany, ""},
		{http.MethodPost, "/api/v1/onboarding/stores", AccessPermission, "identity.edit",
			s.handleOnboardingCommitStores,
			"creates the branches the wizard collected; nothing did before, so a shop " +
				"finished setup with no store and could not record a sale against one"},

		// --- catalogue ---
		//
		// Cost price and margin never cross this boundary. A Cashier holds
		// catalog.view and is deliberately denied catalog.view_cost_price, so a
		// payload carrying cost would defeat the masking the permission exists
		// for — correctly gated, and leaking anyway.
		{http.MethodPost, "/api/v1/catalog/products", AccessPermission, "catalog.create",
			s.handleCreateProduct, ""},
		{http.MethodGet, "/api/v1/catalog/products", AccessPermission, "catalog.view",
			s.handleListProducts, ""},
		{http.MethodPost, "/api/v1/catalog/products/{productID}/matrix", AccessPermission, "catalog.create",
			s.handleGenerateMatrix, ""},
		{http.MethodGet, "/api/v1/catalog/products/{productID}/matrix", AccessPermission, "catalog.view",
			s.handleReadMatrix, ""},
		// Withdrawing needs catalog.delete even though nothing is deleted: it is
		// the destructive-intent permission, and a variant off sale is as
		// disruptive to a shop as one removed would be.
		{http.MethodDelete, "/api/v1/catalog/variants/{variantID}", AccessPermission, "catalog.delete",
			s.handleWithdrawVariant, ""},
		{http.MethodGet, "/api/v1/catalog/scan", AccessPermission, "catalog.view",
			s.handleScanBarcode, ""},

		// --- point of sale ---
		//
		// Refunding is its own permission, not a consequence of being able to
		// sell. The seeded Cashier happens to hold both, but a tenant can build
		// a role that sells and cannot reverse — which matters because refund
		// fraud is among the commonest forms of till theft, and C14 treats a
		// return as its own act for exactly that reason.
		{http.MethodPost, "/api/v1/pos/sales", AccessPermission, "sales.create",
			s.handleCreateSale, ""},
		{http.MethodPost, "/api/v1/pos/returns", AccessPermission, "sales.refund",
			s.handleCreateReturn, ""},
		{http.MethodGet, "/api/v1/pos/sales/{invoiceID}", AccessPermission, "sales.view",
			s.handleGetSale, ""},
		// Turns what is printed on a receipt into the invoice it names.
		//
		// Design 11 §7: "the original invoice is always scanned or linked,
		// never re-typed". The till had no way to obey that. It generates the
		// document UUID, prints a prefix of it, and never learns the
		// sales_invoice.id that every other route here takes — so a returns
		// screen sending what the cashier scanned got a malformed-UUID error or
		// a 404, on every sale. Gated on sales.refund: finding a customer's
		// sale from their receipt is the first act of giving money back.
		{http.MethodGet, "/api/v1/pos/sales/lookup", AccessPermission, "sales.refund",
			s.handleLookupSale,
			"a receipt carries the document UUID, not the invoice id; without " +
				"this no sale made at a till could be found to return"},
		{http.MethodPost, "/api/v1/pos/exchanges", AccessPermission, "sales.exchange",
			s.handleCreateExchange,
			"a credit note and a replacement invoice in one transaction; the server states the difference"},
		// UI spec §5: reprint is available and logged, and reprinting is not
		// reissuing — no new document, no new number, no new ICV. Gated on
		// sales.view per design 11 §10: looking a sale up and handing the
		// customer another copy of it are the same privilege.
		{http.MethodPost, "/api/v1/pos/sales/{invoiceID}/reprint", AccessPermission, "sales.view",
			s.handleReprintSale, ""},

		// The shop's own stationery, for the receipt a customer walks out with.
		// Device-resolved like the catalogue snapshot: a till that could name
		// its own company could print another company's letterhead, and both
		// belong to the same tenant so RLS would not notice. Gated on
		// sales.create because printing a receipt is part of making a sale.
		{http.MethodGet, "/api/v1/pos/stationery", AccessPermission, "sales.create",
			s.handleTillStationery,
			"the words a till prints a receipt on; cached so it still prints offline"},
		{http.MethodGet, "/api/v1/pos/sales/{invoiceID}/returnable", AccessPermission, "sales.refund",
			s.handleReturnableLines,
			"what is still owed back on an invoice; a till must never work this out itself"},
		// The terminal hands back what it signed locally. Gated on sales.create
		// because it completes a sale rather than reading one, and refused
		// outright for a session with no terminal behind it.
		{http.MethodPut, "/api/v1/pos/sales/{invoiceID}/signed-document",
			AccessPermission, "sales.create", s.handleUploadSignedDocument, ""},

		// --- shift and cash drawer (C8, design 11 §9) ---
		//
		// Opening and closing a till are the same act to blueprint A6.1, which
		// gives the Cashier "shift open/close" as one capability, so both carry
		// the permission design 11 §10 names for the close half. A till never
		// says which device it is: the terminal comes from the token, for the
		// reason the POS block above gives.
		{http.MethodPost, "/api/v1/shifts", AccessPermission, "sales.receive_payment",
			s.handleOpenShift, ""},
		{http.MethodGet, "/api/v1/shifts/current", AccessPermission, "sales.receive_payment",
			s.handleCurrentShift,
			"a till restarted mid-shift has to find the session it is already in; " +
				"without this the id from the open response is the only copy"},
		{http.MethodGet, "/api/v1/shifts/{sessionID}", AccessPermission, "sales.receive_payment",
			s.handlePeekShift,
			"the cashier's own view of the shift before counting; withholds the " +
				"expected figure on a blind-close till"},

		// The supervisor's report, and the ONLY route that reveals the expected
		// drawer before a count is committed. Gated on report.view rather than
		// sales.receive_payment on purpose: the Cashier holds the latter, and a
		// cashier who can read the expected figure can make the drawer agree
		// with it, which would leave the variance reading zero on every shift
		// and defeat the blind close blueprint B7 requires.
		{http.MethodGet, "/api/v1/shifts/{sessionID}/x-report", AccessPermission, "report.view",
			s.handleXReport, ""},

		{http.MethodPost, "/api/v1/shifts/{sessionID}/cash-drop",
			AccessPermission, "sales.receive_payment", s.handleCashDrop,
			"moving cash to the vault mid-shift is part of running the drawer (C8)"},
		{http.MethodPost, "/api/v1/shifts/{sessionID}/close",
			AccessPermission, "sales.receive_payment", s.handleCloseShift, ""},

		// --- sync ---
		//
		// The device comes from the token, never the body. A terminal that could
		// name its own device id would push another till's sales onto another
		// till's ZATCA chain, and both tills belong to the same tenant so
		// row-level security would not notice.
		{http.MethodPost, "/api/v1/sync/push", AccessPermission, "sales.create",
			s.handleSyncPush, ""},
		{http.MethodGet, "/api/v1/sync/health", AccessPermission, "sales.view",
			s.handleSyncHealth, ""},

		// --- financial statements ---
		//
		// Gated on accounting.view, not sales.view. A statement exposes the whole
		// company position — margin, cash, what is owed — which is precisely the
		// information a cashier is deliberately kept away from.
		// --- Purchasing (B5) ---
		//
		// Gated on purchasing permissions rather than catalog.view, because
		// every payload here is a cost document: a Cashier who holds
		// catalog.view and is denied catalog.view_cost_price must not reach
		// them.
		{http.MethodGet, "/api/v1/purchasing/suppliers", AccessPermission, "purchasing.view",
			s.handleListSuppliers, "suppliers, with what is owed to each"},
		{http.MethodPost, "/api/v1/purchasing/suppliers", AccessPermission, "purchasing.manage_suppliers",
			s.handleCreateSupplier, ""},

		{http.MethodPut, "/api/v1/purchasing/suppliers/{supplierID}", AccessPermission, "purchasing.manage_suppliers",
			s.handleUpdateSupplier,
			"the code is not editable: it is on orders already issued"},
		{http.MethodPost, "/api/v1/purchasing/suppliers/{supplierID}/active", AccessPermission, "purchasing.manage_suppliers",
			s.handleSetSupplierActive,
			"hides a supplier from the pickers; never a delete, and refused while money is owed"},

		{http.MethodGet, "/api/v1/purchasing/orders", AccessPermission, "purchasing.view",
			s.handleListOrders, ""},
		{http.MethodPost, "/api/v1/purchasing/orders", AccessPermission, "purchasing.create_order",
			s.handleCreateOrder, ""},
		{http.MethodGet, "/api/v1/purchasing/orders/{poID}", AccessPermission, "purchasing.view",
			s.handleReadOrder, ""},
		{http.MethodPost, "/api/v1/purchasing/orders/{poID}/issue", AccessPermission, "purchasing.issue_order",
			s.handleIssueOrder,
			"freezing an order and committing the shop to it is its own permission"},
		{http.MethodPut, "/api/v1/purchasing/orders/{poID}", AccessPermission, "purchasing.create_order",
			s.handleUpdateOrder,
			"rewrites a DRAFT order; an issued one is a commitment and cannot be edited"},
		{http.MethodGet, "/api/v1/purchasing/warehouses", AccessPermission, "purchasing.view",
			s.handleListWarehouses, "where an order can be delivered"},
		{http.MethodGet, "/api/v1/purchasing/orders/{poID}/receipts", AccessPermission, "purchasing.view",
			s.handleListReceipts, ""},

		{http.MethodPost, "/api/v1/purchasing/receipts", AccessPermission, "purchasing.receive_goods",
			s.handleReceiveGoods,
			"the ONLY route that increases stock through a purchase; B5 forbids a PO doing it"},

		{http.MethodGet, "/api/v1/purchasing/bills", AccessPermission, "purchasing.view",
			s.handleListBills, ""},
		{http.MethodPost, "/api/v1/purchasing/bills", AccessPermission, "purchasing.record_bill",
			s.handleRecordBill, "records the bill and runs the three-way match"},
		{http.MethodGet, "/api/v1/purchasing/bills/{billID}", AccessPermission, "purchasing.view",
			s.handleReadBill, ""},
		{http.MethodPost, "/api/v1/purchasing/bills/{billID}/approve", AccessPermission, "purchasing.approve_bill",
			s.handleApproveBill,
			"separate from recording, so the person accepting a discrepancy need not be the one who entered it"},

		{http.MethodPost, "/api/v1/purchasing/payments", AccessPermission, "purchasing.pay_supplier",
			s.handlePaySupplier, ""},
		{http.MethodPost, "/api/v1/purchasing/payments/{paymentID}/reverse", AccessPermission, "purchasing.pay_supplier",
			s.handleReverseSupplierPayment,
			"a new payment that undoes one; the original is not edited. Idempotent on a client-assigned uuid"},
		{http.MethodGet, "/api/v1/purchasing/ageing", AccessPermission, "accounting.view",
			s.handleSupplierAgeing, "what is owed to whom, aged from the due date"},

		// --- Sourcing: requisition, RFQ, quotes, award (B5, B5.1) ---
		//
		// Four permissions rather than one, because the control B5.1 describes
		// depends on separating them. Anybody trusted with stock may ASK for it;
		// approving somebody else's request is a manager's act; running a
		// comparison is the buyer's job; and awarding it commits the business to
		// a supplier. "Who chose this supplier, and why" is the question the
		// whole module exists to answer, and it is worth less if the person who
		// ran the comparison also signed it off.
		{http.MethodGet, "/api/v1/purchasing/requisitions", AccessPermission, "purchasing.view",
			s.handleListRequisitions, "requests for stock, newest first"},
		{http.MethodPost, "/api/v1/purchasing/requisitions", AccessPermission, "purchasing.request",
			s.handleRaiseRequisition,
			"asks for stock. B5 puts this in reach of any authorised staff, so it carries no cost and needs no buying permission"},
		{http.MethodGet, "/api/v1/purchasing/requisitions/{requisitionID}", AccessPermission, "purchasing.view",
			s.handleReadRequisition, "one request and its lines"},
		{http.MethodPost, "/api/v1/purchasing/requisitions/{requisitionID}/decision", AccessPermission, "purchasing.approve_request",
			s.handleDecideRequisition,
			"approves or turns down somebody else's request; a refusal must say why"},

		{http.MethodGet, "/api/v1/purchasing/rfqs", AccessPermission, "purchasing.view",
			s.handleListRFQs, "requests for quotation"},
		{http.MethodPost, "/api/v1/purchasing/rfqs", AccessPermission, "purchasing.manage_rfq",
			s.handleRaiseRFQ, "asks several suppliers to price the same list"},
		{http.MethodGet, "/api/v1/purchasing/rfqs/{rfqID}/comparison", AccessPermission, "purchasing.view",
			s.handleCompareRFQ,
			"B5.1's side-by-side: price, total, VAT, lead time, terms and quality per supplier"},
		{http.MethodPost, "/api/v1/purchasing/rfqs/{rfqID}/quotes", AccessPermission, "purchasing.manage_rfq",
			s.handleRecordQuote,
			"files a supplier's reply; a second reply is a revision that supersedes the first, never an overwrite"},
		{http.MethodPost, "/api/v1/purchasing/rfqs/{rfqID}/declines", AccessPermission, "purchasing.manage_rfq",
			s.handleDeclineToQuote,
			"records that a supplier was asked and said no, which a missing quote cannot tell you"},
		{http.MethodPost, "/api/v1/purchasing/rfqs/{rfqID}/award", AccessPermission, "purchasing.award_rfq",
			s.handleAwardRFQ,
			"picks the winner, with a mandatory reason, and raises the purchase order from that quote"},
		{http.MethodPost, "/api/v1/purchasing/rfqs/{rfqID}/cancel", AccessPermission, "purchasing.manage_rfq",
			s.handleCancelRFQ, "abandons a request without awarding it"},

		{http.MethodGet, "/api/v1/purchasing/suppliers/{supplierID}/quotes", AccessPermission, "purchasing.view",
			s.handleSupplierQuoteHistory,
			"B5.1's archive: what this supplier has quoted before, won or lost"},

		// --- Customers and receivables (B16, C9.3) ---
		//
		// customers.view is deliberately held by a Cashier: a till has to be able
		// to find the customer standing in front of it. Everything that changes a
		// balance needs more.
		// --- Terminals (H3) ---
		//
		// Enrolment is PUBLIC, and has to be: a terminal being paired has no
		// credential yet. What makes it safe is the code — single use, fifteen
		// minutes, attempt-limited — not a token.
		{http.MethodPost, "/api/v1/devices/enrol", AccessPublic, "", s.handleEnrol,
			"a terminal claims itself with a code an owner issued; returns its secret once"},
		{http.MethodGet, "/api/v1/devices/identity", AccessPublic, "",
			s.handleTerminalIdentity,
			"a paired terminal asks who it is, presenting its secret; also how it learns it was revoked"},

		{http.MethodGet, "/api/v1/devices/stores", AccessPermission, "devices.view",
			s.handleListDeviceStores, "the branches a terminal can be registered in"},
		{http.MethodGet, "/api/v1/devices", AccessPermission, "devices.view",
			s.handleListTerminals, "the tills in this business, most in need of attention first"},
		{http.MethodPost, "/api/v1/devices", AccessPermission, "devices.manage",
			s.handleRegisterTerminal, "registers a terminal in `pending`; it can do nothing until paired"},
		{http.MethodGet, "/api/v1/devices/{deviceID}", AccessPermission, "devices.view",
			s.handleReadTerminal, ""},
		{http.MethodPut, "/api/v1/devices/{deviceID}", AccessPermission, "devices.manage",
			s.handleAmendTerminal,
			"rename or move between stores of the SAME company; the ZATCA chain continues unbroken"},
		{http.MethodPost, "/api/v1/devices/{deviceID}/enrolment-code", AccessPermission, "devices.manage",
			s.handleIssueEnrolmentCode,
			"shows the code once and supersedes any outstanding one"},
		{http.MethodPost, "/api/v1/devices/{deviceID}/active", AccessPermission, "devices.manage",
			s.handleSetTerminalActive, "pauses or resumes a till; takes effect on its next request"},
		{http.MethodPost, "/api/v1/devices/{deviceID}/revoke", AccessPermission, "devices.manage",
			s.handleRevokeTerminal,
			"permanent: clears the secret and ends the terminal, leaving its chain intact and archived"},

		// --- e-invoicing units ---
		//
		// A unit owns one ICV/PIH chain and carries the VAT registration that
		// chain hangs from, so managing one is not the same act as pairing a
		// till and does not use devices.manage. There is deliberately no route
		// A CSID is never SET by a client. It is obtained from ZATCA by the
		// onboarding routes below and written server-side; the columns remain
		// read-only to every request that carries one.
		{http.MethodGet, "/api/v1/einvoicing/units", AccessPermission, "einvoicing.view",
			s.handleListEGSUnits,
			"the signing units in this business, and how many tills and invoices each carries"},
		{http.MethodPost, "/api/v1/einvoicing/units", AccessPermission, "einvoicing.manage",
			s.handleCreateEGSUnit,
			"creates a unit and captures the nine CSR fields; asserts no CSID"},
		{http.MethodGet, "/api/v1/einvoicing/units/{unitID}", AccessPermission, "einvoicing.view",
			s.handleReadEGSUnit, ""},
		{http.MethodPut, "/api/v1/einvoicing/units/{unitID}", AccessPermission, "einvoicing.manage",
			s.handleAmendEGSUnit,
			"corrects the name, branch and CSR details; the architecture is fixed at creation"},

		// Onboarding. Status is einvoicing.VIEW, because a store manager whose
		// till stopped selling needs to see that its certificate expired;
		// requesting a CSID is einvoicing.ONBOARD, which only the Owner holds,
		// because it binds the business's tax identity and consumes a one-time
		// password the taxpayer had to fetch themselves.
		{http.MethodGet, "/api/v1/einvoicing/units/{unitID}/onboarding",
			AccessPermission, "einvoicing.view",
			s.handleZATCAOnboardingStatus,
			"where this till has got to with ZATCA, and what to do next"},
		{http.MethodPost, "/api/v1/einvoicing/units/{unitID}/onboarding/compliance",
			AccessPermission, "einvoicing.onboard",
			s.handleZATCARequestComplianceCSID,
			"exchanges a certificate request and the Fatoora one-time password " +
				"for a compliance CSID; the password is never stored"},
		{http.MethodPost, "/api/v1/einvoicing/units/{unitID}/onboarding/production",
			AccessPermission, "einvoicing.onboard",
			s.handleZATCARequestProductionCSID,
			"promotes a compliance CSID to a production one; no password, because " +
				"the compliance credential is itself the proof one was given"},
		{http.MethodPost, "/api/v1/einvoicing/units/{unitID}/onboarding/renew",
			AccessPermission, "einvoicing.onboard",
			s.handleZATCARenewCSID,
			"replaces a certificate near expiry; the old one is superseded and " +
				"kept, because invoices reported under it outlive it"},

		{http.MethodGet, "/api/v1/customers", AccessPermission, "customers.view",
			s.handleListCustomers, "customers, with what each owes and what is left of their limit"},
		{http.MethodGet, "/api/v1/customers/snapshot", AccessPermission, "customers.view",
			s.handleCustomerSnapshot,
			"the delta a till caches so it can attach a sale to somebody with the network down"},
		{http.MethodPost, "/api/v1/customers", AccessPermission, "customers.manage",
			s.handleCreateCustomer, ""},
		{http.MethodGet, "/api/v1/customers/{customerID}", AccessPermission, "customers.view",
			s.handleReadCustomer, ""},
		{http.MethodPut, "/api/v1/customers/{customerID}", AccessPermission, "customers.manage",
			s.handleUpdateCustomer,
			"the code is not editable: it is on invoices already issued and signed"},
		{http.MethodPost, "/api/v1/customers/{customerID}/credit-limit", AccessPermission, "customers.set_credit_limit",
			s.handleSetCreditLimit,
			"deciding how much a customer may owe is its own permission, not part of managing their details"},
		{http.MethodPost, "/api/v1/customers/{customerID}/active", AccessPermission, "customers.manage",
			s.handleSetCustomerActive,
			"hides a customer from the pickers; never a delete, and refused while they owe money"},

		{http.MethodGet, "/api/v1/customers/{customerID}/ledger", AccessPermission, "customers.view",
			s.handleCustomerLedger, "the khata: every charge and receipt with a running balance"},
		{http.MethodGet, "/api/v1/customers/{customerID}/open-invoices", AccessPermission, "customers.view",
			s.handleCustomerOpenInvoices, "what a receipt can be allocated against"},

		{http.MethodPost, "/api/v1/receivables/receipts", AccessPermission, "sales.receive_payment",
			s.handleTakeCustomerPayment,
			"taking money in is separate from managing the customer record; idempotent on a client-assigned uuid"},
		{http.MethodPost, "/api/v1/receivables/receipts/{receiptID}/reverse", AccessPermission, "sales.receive_payment",
			s.handleReverseCustomerPayment,
			"a new receipt that undoes one; the original is not edited. Idempotent on a client-assigned uuid"},
		{http.MethodGet, "/api/v1/receivables/ageing", AccessPermission, "accounting.view",
			s.handleCustomerAgeing, "who owes what, aged from the due date"},

		// Card settlement (P15, C12). Reading what has not yet cleared is an
		// accounting question, so accounting.view; recording a deposit changes
		// the ledger, so accounting.create. Deliberately NOT
		// sales.receive_payment: taking money at a counter and reconciling a
		// bank statement are different jobs done by different people.
		{http.MethodGet, "/api/v1/settlement/pending", AccessPermission, "accounting.view",
			s.handlePendingSettlement, ""},
		{http.MethodPost, "/api/v1/settlement/batches", AccessPermission, "accounting.create",
			s.handleRecordSettlement, ""},
		{http.MethodGet, "/api/v1/settlement/batches/{batchID}", AccessPermission, "accounting.view",
			s.handleReadSettlement, ""},

		{http.MethodGet, "/api/v1/companies", AccessAuthenticated, "", s.handleListCompanies,
			"every signed-in user needs to know which companies they are in before asking about one; scoped by RLS and the token"},

		// --- branding (I2) ---
		//
		// Setting a logo is a settings change, so it carries the permission the
		// rest of company setup carries. There is deliberately no branding.*
		// verb: a new permission would have to be granted to every tenant's
		// cloned roles before anybody could use the feature, which is the trap
		// 0032 and 0033 fell into.
		{http.MethodGet, "/api/v1/companies/{companyID}/logo",
			AccessPermission, "identity.view", s.handleGetLogo,
			"what is set, without the bytes; a settings screen does not need the image to say one exists"},
		{http.MethodPut, "/api/v1/companies/{companyID}/logo",
			AccessPermission, "identity.edit", s.handlePutLogo, ""},
		{http.MethodDelete, "/api/v1/companies/{companyID}/logo",
			AccessPermission, "identity.edit", s.handleDeleteLogo, ""},

		// The file itself, merely authenticated. A logo is the shop's public
		// mark and is destined for every receipt it prints, so a till that
		// could not read it could not print one — and gating it behind a
		// settings permission would put that gate in the wrong place. RLS is
		// what confines it to the caller's own tenant.
		{http.MethodGet, "/api/v1/companies/{companyID}/logo/image",
			AccessAuthenticated, "", s.handleGetLogoImage,
			"the shop's own mark, destined for its receipts; scoped to the caller's tenant by RLS"},

		// --- document templates (I2 / P35) ---
		//
		// What a client writes on their own documents: header, footer, return
		// policy, payment terms, and whether the logo and tax numbers appear.
		// Presentation only — nothing here can reach a figure, a party or a
		// date, which is what makes it safe to change after a document has
		// been issued.
		//
		// Reading is merely authenticated, like the logo image and for the same
		// reason: every document surface needs the stationery, so a cashier
		// looking at an invoice must be able to fetch it. Writing carries the
		// settings permission the rest of company configuration carries.
		{http.MethodGet, "/api/v1/companies/{companyID}/templates",
			AccessAuthenticated, "", s.handleListTemplates,
			"the shop's own stationery, needed by every surface that renders a document"},
		{http.MethodPut, "/api/v1/companies/{companyID}/templates/{docType}",
			AccessPermission, "identity.edit", s.handleSaveTemplate, ""},
		{http.MethodDelete, "/api/v1/companies/{companyID}/templates/{docType}",
			AccessPermission, "identity.edit", s.handleResetTemplate, ""},
		{http.MethodGet, "/api/v1/dashboard/overview", AccessPermission, "accounting.view",
			s.handleDashboardOverview,
			"the Owner Dashboard in one call; every figure computed from the journal"},
		{http.MethodGet, "/api/v1/dashboard/sales", AccessPermission, "sales.view",
			s.handleSalesDetail,
			"the invoices behind the sales tile; A8 requires every figure to open"},
		{http.MethodGet, "/api/v1/dashboard/expenses", AccessPermission, "accounting.view",
			s.handleExpensesDetail,
			"the postings behind the expenses tile, optionally one account"},
		{http.MethodGet, "/api/v1/dashboard/compliance", AccessPermission, "accounting.view",
			s.handleComplianceQueue,
			"invoices that have not finished reporting; states the P1 gate honestly"},

		// --- cash expenses (C3, design 02 rule 5) ---
		//
		// Three verbs, split by the SIZE of the decision, which is the argument
		// 0065 makes. Recording what the electricity cost is clerical; deciding
		// that fuel VAT cannot be reclaimed is a tax position that overstates
		// every future return if it is wrong.
		{http.MethodGet, "/api/v1/expenses", AccessPermission, "expense.view",
			s.handleListExpenses, ""},
		{http.MethodPost, "/api/v1/expenses", AccessPermission, "expense.record",
			s.handleRecordExpense, ""},
		{http.MethodGet, "/api/v1/expenses/{expenseID}", AccessPermission, "expense.view",
			s.handleReadExpense, ""},
		{http.MethodGet, "/api/v1/expenses/heads", AccessPermission, "expense.view",
			s.handleListExpenseHeads,
			"recording an expense needs the list to choose from, so view rather " +
				"than manage_heads gates reading it"},
		{http.MethodPost, "/api/v1/expenses/heads", AccessPermission, "expense.manage_heads",
			s.handleCreateExpenseHead, ""},
		{http.MethodPut, "/api/v1/expenses/heads/{headID}", AccessPermission, "expense.manage_heads",
			s.handleUpdateExpenseHead, ""},
		{http.MethodPost, "/api/v1/expenses/heads/{headID}/active", AccessPermission, "expense.manage_heads",
			s.handleSetExpenseHeadActive, ""},
		{http.MethodGet, "/api/v1/expenses/accounts", AccessPermission, "expense.manage_heads",
			s.handleListExpenseAccounts,
			"the chart accounts a category may post to; only useful to whoever " +
				"may point one at an account"},
		// --- stock operations (B4) ---
		//
		// Two verbs seeded in 0005 and unreachable ever since, plus one added by
		// 0079. The split is by what the act can hide:
		//
		//   inventory.view              — reading levels and the movement
		//                                 ledger. Every role that touches stock
		//                                 has it.
		//   inventory.adjust_stock      — writing stock off, and counting.
		//                                 Whoever holds this can make a theft
		//                                 disappear, which is why the reason and
		//                                 the note are mandatory and the voucher
		//                                 is frozen once posted.
		//   inventory.transfer_stock    — asking for stock, sending it, and
		//                                 confirming it arrived.
		//   inventory.approve_transfer  — letting it go. Deliberately NOT held
		//                                 by the Inventory Keeper, because a
		//                                 control the doer can sign off is not a
		//                                 control.
		//
		// Stock locations are managed under adjust_stock rather than a verb of
		// their own: the two acts a location supports — retiring one and adding
		// one — are the same kind of decision as correcting a shelf, and a
		// fifth verb for two routes would be a permission nobody can explain.
		{http.MethodGet, "/api/v1/stock/locations", AccessPermission, "inventory.view",
			s.handleListStockLocations,
			"every stock screen needs somewhere to point at, and a till's " +
				"branch resolves through this list"},
		{http.MethodPost, "/api/v1/stock/locations", AccessPermission, "inventory.adjust_stock",
			s.handleCreateStockLocation, ""},
		{http.MethodPut, "/api/v1/stock/locations/{locationID}", AccessPermission, "inventory.adjust_stock",
			s.handleRenameStockLocation, ""},
		{http.MethodPost, "/api/v1/stock/locations/{locationID}/active", AccessPermission, "inventory.adjust_stock",
			s.handleSetStockLocationActive, ""},

		{http.MethodGet, "/api/v1/stock/on-hand", AccessPermission, "inventory.view",
			s.handleStockOnHand, ""},
		{http.MethodGet, "/api/v1/stock/movements", AccessPermission, "inventory.view",
			s.handleStockMovements, ""},

		{http.MethodGet, "/api/v1/stock/adjustments", AccessPermission, "inventory.view",
			s.handleListStockAdjustments,
			"reading what was written off is how a manager notices somebody " +
				"writing too much off; gating it behind the verb that DOES the " +
				"writing would hide it from exactly the person checking"},
		{http.MethodPost, "/api/v1/stock/adjustments", AccessPermission, "inventory.adjust_stock",
			s.handleRecordStockAdjustment, ""},
		{http.MethodGet, "/api/v1/stock/adjustments/{adjustmentID}", AccessPermission, "inventory.view",
			s.handleGetStockAdjustment, ""},

		{http.MethodPost, "/api/v1/stock/counts", AccessPermission, "inventory.adjust_stock",
			s.handleOpenStockCount, ""},
		{http.MethodPut, "/api/v1/stock/counts/{adjustmentID}", AccessPermission, "inventory.adjust_stock",
			s.handleSaveStockCount, ""},
		{http.MethodPost, "/api/v1/stock/counts/{adjustmentID}/post", AccessPermission, "inventory.adjust_stock",
			s.handlePostStockCount, ""},
		{http.MethodPost, "/api/v1/stock/counts/{adjustmentID}/cancel", AccessPermission, "inventory.adjust_stock",
			s.handleCancelStockCount, ""},

		{http.MethodGet, "/api/v1/stock/transfers", AccessPermission, "inventory.view",
			s.handleListStockTransfers, ""},
		{http.MethodPost, "/api/v1/stock/transfers", AccessPermission, "inventory.transfer_stock",
			s.handleRequestStockTransfer, ""},
		{http.MethodGet, "/api/v1/stock/transfers/{transferID}", AccessPermission, "inventory.view",
			s.handleGetStockTransfer, ""},
		{http.MethodPost, "/api/v1/stock/transfers/{transferID}/approve", AccessPermission, "inventory.approve_transfer",
			s.handleApproveStockTransfer, ""},
		{http.MethodPost, "/api/v1/stock/transfers/{transferID}/dispatch", AccessPermission, "inventory.transfer_stock",
			s.handleDispatchStockTransfer, ""},
		{http.MethodPost, "/api/v1/stock/transfers/{transferID}/receive", AccessPermission, "inventory.transfer_stock",
			s.handleReceiveStockTransfer, ""},
		{http.MethodPost, "/api/v1/stock/transfers/{transferID}/cancel", AccessPermission, "inventory.transfer_stock",
			s.handleCancelStockTransfer, ""},

		{http.MethodGet, "/api/v1/dashboard/stock", AccessPermission, "inventory.view",
			s.handleStockDetail,
			"variants low or out of stock, summed across the company's warehouses"},
		// --- the accounting calendar (C10) ---
		//
		// Two verbs seeded in 0005 and unreachable ever since -- the route audit
		// listed both as "awaited", and 0080 says what that actually meant:
		// nothing had ever created a fiscal period either, so a journal entry had
		// nowhere to go and no company provisioned through this product could
		// post anything at all.
		//
		// Closing needs `accounting.close_period`; reopening needs
		// `accounting.reopen_period`, which C10 puts at Owner level because
		// reopening changes figures somebody has already reported.
		{http.MethodGet, "/api/v1/accounting/periods", AccessPermission, "accounting.view",
			s.handleFiscalCalendar,
			"anybody who may read the accounts needs to know which months are open; " +
				"a figure means something different in a closed month"},
		{http.MethodPost, "/api/v1/accounting/periods", AccessPermission, "accounting.close_period",
			s.handleOpenFiscalYear,
			"opening a year is the same authority as closing a month: it decides " +
				"which dates the books will accept"},
		{http.MethodPost, "/api/v1/accounting/periods/{periodID}/close", AccessPermission, "accounting.close_period",
			s.handleClosePeriod, ""},
		{http.MethodPost, "/api/v1/accounting/periods/{periodID}/reopen", AccessPermission, "accounting.reopen_period",
			s.handleReopenPeriod, ""},
		{http.MethodPost, "/api/v1/accounting/year-end", AccessPermission, "accounting.reopen_period",
			s.handleCloseYear,
			"closing a YEAR empties revenue and expense into retained earnings and " +
				"locks every month in it beyond reopening, so it takes the more " +
				"restricted of the two period permissions rather than the one that " +
				"closes a month"},

		// --- cash and bank (C2), and the reconciliation (C11) ---
		//
		// The chart has carried one Cash account and one Bank account since
		// provisioning was written, and that was all a company could ever have.
		// Posting rule 9, `transfer.internal`, has been seeded since 0025 and had
		// never once been called, because nothing knew what the two ends of a
		// transfer were.
		//
		// `accounting.reconcile` is its own verb rather than folded into
		// `accounting.create`, because reconciling asserts that the books agree
		// with an outside party -- which is the assertion an auditor relies on,
		// and a different thing from being allowed to post an entry.
		{http.MethodGet, "/api/v1/treasury/accounts", AccessPermission, "accounting.view",
			s.handleListMoneyAccounts, ""},
		{http.MethodPost, "/api/v1/treasury/accounts", AccessPermission, "accounting.manage_accounts",
			s.handleCreateMoneyAccount, ""},
		{http.MethodPost, "/api/v1/treasury/accounts/{accountID}/active", AccessPermission, "accounting.manage_accounts",
			s.handleSetMoneyAccountActive, ""},

		{http.MethodGet, "/api/v1/treasury/transfers", AccessPermission, "accounting.view",
			s.handleListMoneyTransfers, ""},
		{http.MethodPost, "/api/v1/treasury/transfers", AccessPermission, "accounting.create",
			s.handleMoveMoney,
			"a transfer between the company's own accounts IS a journal entry, so it " +
				"is gated with the rest of them"},

		{http.MethodGet, "/api/v1/treasury/statements", AccessPermission, "accounting.reconcile",
			s.handleListStatements, ""},
		{http.MethodPost, "/api/v1/treasury/statements", AccessPermission, "accounting.reconcile",
			s.handleImportStatement, ""},
		{http.MethodGet, "/api/v1/treasury/statements/{statementID}", AccessPermission, "accounting.reconcile",
			s.handleGetStatement, ""},
		{http.MethodPost, "/api/v1/treasury/statements/{statementID}/reconcile", AccessPermission, "accounting.reconcile",
			s.handleReconcileStatement, ""},
		{http.MethodPost, "/api/v1/treasury/lines/{lineID}/match", AccessPermission, "accounting.reconcile",
			s.handleMatchStatementLine,
			"an empty journal line undoes a match, because pointing a line at an " +
				"entry and pointing it at nothing are the same edit"},

		// --- fixed assets (C7) and investors (C3.2) ---
		//
		// Four verbs. Reading a register and changing it are different acts, and
		// depreciation is behind `asset.manage` because running it posts to the
		// ledger -- it is not a report.
		//
		// Neither register reaches the Store Manager. 0005 describes that role as
		// unable to see "bank ledgers or true net profit", and an investor
		// register is a statement of who owns the business.
		{http.MethodGet, "/api/v1/assets", AccessPermission, "asset.view",
			s.handleAssetRegister, ""},
		{http.MethodPost, "/api/v1/assets", AccessPermission, "asset.manage",
			s.handleAddAsset, ""},
		{http.MethodPost, "/api/v1/assets/depreciate", AccessPermission, "asset.manage",
			s.handleDepreciate,
			"a depreciation run posts to the ledger, so it takes the verb that " +
				"changes the register rather than the one that reads it"},
		{http.MethodPost, "/api/v1/assets/{assetID}/dispose", AccessPermission, "asset.manage",
			s.handleDisposeAsset, ""},

		{http.MethodGet, "/api/v1/investors", AccessPermission, "investor.view",
			s.handleListInvestors, ""},
		{http.MethodPost, "/api/v1/investors", AccessPermission, "investor.manage",
			s.handleAddInvestor, ""},
		{http.MethodPost, "/api/v1/investors/movements", AccessPermission, "investor.manage",
			s.handleRecordInvestment, ""},
		{http.MethodGet, "/api/v1/investors/{investorID}/statement", AccessPermission, "investor.view",
			s.handleInvestorStatement,
			"C3.2 lets an investor read their OWN history, so the permission is the " +
				"reading one and the service checks whose statement it is"},

		// --- promotions and the pricing engine (B9) ---
		//
		// A cashier holds `promotion.view`, which reaches the quote route: a cart
		// being built has to be priced repeatedly while items are scanned.
		// Setting a campaign UP needs `promotion.manage`, which decides what
		// every till in every branch will charge -- and B9 puts manager
		// authorisation around discounts far smaller than that.
		{http.MethodGet, "/api/v1/promotions", AccessPermission, "promotion.view",
			s.handleListPromotions, ""},
		{http.MethodPost, "/api/v1/promotions", AccessPermission, "promotion.manage",
			s.handleCreatePromotion, ""},
		{http.MethodPost, "/api/v1/promotions/{promotionID}/active", AccessPermission, "promotion.manage",
			s.handleSetPromotionActive,
			"a campaign is stopped rather than deleted: its redemptions are what the " +
				"campaign figures are drawn from, and deleting one would take a " +
				"month of discount history with it"},
		{http.MethodPost, "/api/v1/promotions/quote", AccessPermission, "promotion.view",
			s.handleQuotePromotions,
			"a till prices a cart many times while it is built, so this is the " +
				"reading verb; nothing is redeemed until the sale is finalised"},

		// --- quotations, orders and delivery documents (B11, B12) ---
		//
		// `order.view` reaches the list, one order and the three printable
		// documents: a picker and a driver both need those, and neither should be
		// able to change a price. `order.manage` raises, advances, cancels and
		// records what was picked and delivered.
		//
		// 0005 describes the Sales Executive as handling "quotations, orders and
		// their own customer list" and gave that role no permission that could
		// reach one. 0085 gives it these, plus the catalogue and customer reads an
		// order is built from -- the same widow 0033 found for the Purchase
		// Manager.
		{http.MethodGet, "/api/v1/orders", AccessPermission, "order.view",
			s.handleListSalesOrders, ""},
		{http.MethodPost, "/api/v1/orders", AccessPermission, "order.manage",
			s.handleRaiseOrder,
			"always raised as a quotation: confirming is the customer's decision, " +
				"and a route that could skip it would put \"the customer agreed\" " +
				"in the hands of whoever typed the order"},
		{http.MethodGet, "/api/v1/orders/{orderID}", AccessPermission, "order.view",
			s.handleGetOrder, ""},
		{http.MethodPost, "/api/v1/orders/{orderID}/advance", AccessPermission, "order.manage",
			s.handleAdvanceOrder, ""},
		{http.MethodPost, "/api/v1/orders/{orderID}/cancel", AccessPermission, "order.manage",
			s.handleCancelOrder, ""},
		{http.MethodPost, "/api/v1/orders/{orderID}/pick", AccessPermission, "order.manage",
			s.handlePickOrder, ""},
		{http.MethodPost, "/api/v1/orders/{orderID}/deliver", AccessPermission, "order.manage",
			s.handleDeliverOrder, ""},
		{http.MethodGet, "/api/v1/orders/{orderID}/documents/{kind}", AccessPermission, "order.view",
			s.handleOrderDocument,
			"B11's delivery note is itemised WITHOUT pricing, and the type it is " +
				"built from has no price fields at all so no screen can put them back"},

		// --- loyalty, store credit and gift cards (B16) ---
		//
		// `store_credit` and `loyalty_points` have been accepted TENDERS since
		// 0018 with no balance anywhere behind them: a till could settle a sale
		// with credit a customer had never been given, and the liability would go
		// negative with nobody told. 0086 gives that money somewhere to live and
		// the sale path now draws it down.
		//
		// The reading verbs sit with a cashier, who has to be able to say what is
		// on a card before taking it in payment. The managing verbs create money
		// out of nothing from the shop's point of view, which is why they sit
		// with the people who can also set a credit limit.
		{http.MethodGet, "/api/v1/loyalty/program", AccessPermission, "loyalty.view",
			s.handleGetLoyaltyProgram, ""},
		{http.MethodPut, "/api/v1/loyalty/program", AccessPermission, "loyalty.manage",
			s.handleSetLoyaltyProgram,
			"changing the rate does not revalue points already earned: those were " +
				"posted at the value in force when they were earned, and repricing " +
				"them would move a liability a closed month has already reported"},
		{http.MethodGet, "/api/v1/loyalty/members", AccessPermission, "loyalty.view",
			s.handleListLoyaltyMembers, ""},
		{http.MethodGet, "/api/v1/loyalty/members/{customerID}", AccessPermission, "loyalty.view",
			s.handleGetLoyaltyCard, ""},
		{http.MethodPost, "/api/v1/loyalty/members/{customerID}/adjust", AccessPermission, "loyalty.manage",
			s.handleAdjustPoints, ""},
		{http.MethodPost, "/api/v1/loyalty/expire", AccessPermission, "loyalty.manage",
			s.handleExpirePoints,
			"run on demand rather than by a timer: a background job that quietly " +
				"changed a liability on a Saturday would be a change nobody could " +
				"attribute"},

		{http.MethodGet, "/api/v1/wallets/{customerID}", AccessPermission, "wallet.view",
			s.handleGetWallet, ""},
		{http.MethodPost, "/api/v1/wallets/{customerID}/credit", AccessPermission, "wallet.manage",
			s.handleGiveStoreCredit, ""},
		{http.MethodGet, "/api/v1/gift-cards", AccessPermission, "wallet.view",
			s.handleListGiftCards, ""},
		{http.MethodPost, "/api/v1/gift-cards", AccessPermission, "wallet.manage",
			s.handleIssueGiftCard,
			"selling a gift card is not a sale: no revenue and no VAT until it is " +
				"spent, or the month it was sold in is overstated and the tax is " +
				"charged twice"},
		{http.MethodGet, "/api/v1/gift-cards/{cardID}", AccessPermission, "wallet.view",
			s.handleGetGiftCard, ""},
		{http.MethodPost, "/api/v1/gift-cards/{cardID}/void", AccessPermission, "wallet.manage",
			s.handleVoidGiftCard, ""},
		{http.MethodGet, "/api/v1/gift-cards/by-code/{code}", AccessPermission, "wallet.view",
			s.handleLookUpGiftCard,
			"what a till calls when a cashier types a card number; a GET because " +
				"checking a balance changes nothing"},
		{http.MethodPost, "/api/v1/store-credit/expire", AccessPermission, "wallet.manage",
			s.handleExpireStoreCredit, ""},

		// Fitting history is behind the CUSTOMER permissions rather than a pair
		// of its own: a size is part of the customer record, and inventing
		// `size.view` would be a permission an administrator has to grant before
		// the feature works at all.
		{http.MethodGet, "/api/v1/customers/{customerID}/sizes", AccessPermission, "customers.view",
			s.handleListCustomerSizes, ""},
		{http.MethodPut, "/api/v1/customers/{customerID}/sizes", AccessPermission, "customers.manage",
			s.handleRecordCustomerSize,
			"an upsert on the garment, so a customer who has gone up a size has a " +
				"corrected row rather than two rows leaving staff to guess"},
		{http.MethodDelete, "/api/v1/customers/{customerID}/sizes/{sizeID}", AccessPermission, "customers.manage",
			s.handleForgetCustomerSize, ""},

		// --- Delivery and stock reservation (B13) ---
		//
		// The list and read routes are gated on delivery.view, which a driver
		// holds — and both narrow to the caller's OWN runs unless they also
		// hold delivery.manage. A6.1 gives Delivery Staff "assigned delivery
		// orders only", and an unnarrowed list would hand a driver the
		// company's customer address book.
		{http.MethodGet, "/api/v1/deliveries", AccessPermission, "delivery.view",
			s.handleListDeliveries,
			"consignments; a driver sees only their own"},
		{http.MethodPost, "/api/v1/deliveries", AccessPermission, "delivery.manage",
			s.handleBookDelivery, "books a consignment against a confirmed order"},
		{http.MethodGet, "/api/v1/deliveries/{deliveryID}", AccessPermission, "delivery.view",
			s.handleReadDelivery, "one consignment and every step it has been through"},
		{http.MethodPost, "/api/v1/deliveries/{deliveryID}/advance", AccessPermission, "delivery.deliver",
			s.handleAdvanceDelivery,
			"moves a consignment along B13's pipeline; the transition is checked, so a delivery cannot be marked arrived without having left"},

		{http.MethodPost, "/api/v1/stock/reservations", AccessPermission, "order.manage",
			s.handleReserveStock,
			"holds stock against an order so a second channel cannot sell the same unit"},
		{http.MethodDelete, "/api/v1/stock/reservations/{orderID}", AccessPermission, "order.manage",
			s.handleReleaseReservation, "lets go of what an order was holding"},
		{http.MethodGet, "/api/v1/stock/availability", AccessPermission, "inventory.view",
			s.handleStockAvailability,
			"on hand, reserved, and what is actually free to sell"},

		// --- Serial numbers and warranty (B15) ---
		{http.MethodGet, "/api/v1/serials", AccessPermission, "serial.view",
			s.handleListSerials, "tracked units"},
		{http.MethodPost, "/api/v1/serials", AccessPermission, "serial.manage",
			s.handleReceiveSerials, "records serial numbers arriving with a delivery"},
		{http.MethodGet, "/api/v1/serials/{serialNo}", AccessPermission, "serial.view",
			s.handleLookupSerial,
			"the warranty desk's question: what is this, whose is it, is it still covered"},

		// --- Service and repair (B15) ---
		{http.MethodGet, "/api/v1/service-jobs", AccessPermission, "service.view",
			s.handleListRepairs, "repairs in progress"},
		{http.MethodPost, "/api/v1/service-jobs", AccessPermission, "service.view",
			s.handleBookInRepair,
			"takes something in for repair; a counter that can see jobs can book one in, because that is the same conversation with the customer"},
		{http.MethodGet, "/api/v1/service-jobs/{jobID}", AccessPermission, "service.view",
			s.handleReadRepair, "one job, its diagnosis and the parts fitted"},
		{http.MethodPost, "/api/v1/service-jobs/{jobID}", AccessPermission, "service.manage",
			s.handleUpdateRepair,
			"records progress, and the replacement serial when a unit was swapped rather than fixed"},
		{http.MethodPost, "/api/v1/service-jobs/{jobID}/parts", AccessPermission, "service.manage",
			s.handleIssueServicePart,
			"fits a part: stock leaves, and on a warranty job the shop absorbs the cost"},

		// --- Instalments (B14) ---
		//
		// Quoting is separate from opening, and deliberately reachable by a
		// cashier holding installment.view: a customer at the counter asking
		// what twelve months would cost must get an answer without anybody
		// being committed to a plan.
		{http.MethodPost, "/api/v1/installments/quote", AccessPermission, "installment.view",
			s.handleQuoteInstalments,
			"previews a schedule without creating one"},
		{http.MethodGet, "/api/v1/installments", AccessPermission, "installment.view",
			s.handleListPlans, "instalment agreements"},
		{http.MethodPost, "/api/v1/installments", AccessPermission, "installment.manage",
			s.handleOpenPlan,
			"turns a credit invoice into a schedule; the sale already posted, so only the finance charge does here"},
		{http.MethodGet, "/api/v1/installments/{planID}", AccessPermission, "installment.view",
			s.handleReadPlan, "one agreement and every instalment on it"},
		{http.MethodPost, "/api/v1/installments/{planID}/collect", AccessPermission, "installment.manage",
			s.handleCollectInstalment,
			"marks a customer receipt against the schedule, oldest instalment first"},
		{http.MethodPost, "/api/v1/installments/{planID}/cancel", AccessPermission, "installment.manage",
			s.handleCancelPlan, "unwinds an agreement, with a reason"},
		{http.MethodPost, "/api/v1/installments/accrue", AccessPermission, "installment.manage",
			s.handleAccrueFinanceIncome,
			"earns the finance income on instalments now due and charges late fees"},

		// --- Employees, attendance and leave (C5) ---
		//
		// `hr.view` is the directory; `hr.view_pay` is what somebody earns.
		// A6.2 requires staff to be blockable from "other employees'
		// salaries", so a Store Manager holds the first and not the second:
		// they roster their branch without learning what the branch is paid.
		// The split is applied in the service by omitting the fields, not by a
		// screen choosing not to render them.
		{http.MethodGet, "/api/v1/employees", AccessPermission, "hr.view",
			s.handleListEmployees, "the staff directory"},
		{http.MethodPost, "/api/v1/employees", AccessPermission, "hr.manage",
			s.handleHireEmployee,
			"adds somebody to the payroll; setting pay additionally needs hr.view_pay"},
		{http.MethodGet, "/api/v1/employees/expiring", AccessPermission, "hr.view",
			s.handleExpiringDocuments,
			"C5's Iqama and ID expiry alert: an expired document stops somebody working"},
		{http.MethodGet, "/api/v1/employees/{employeeID}", AccessPermission, "hr.view",
			s.handleReadEmployee, "one member of staff"},
		{http.MethodPut, "/api/v1/employees/{employeeID}", AccessPermission, "hr.manage",
			s.handleUpdateEmployee, "amends a record; changing pay needs hr.view_pay"},
		{http.MethodPost, "/api/v1/employees/{employeeID}/leaving", AccessPermission, "hr.manage",
			s.handleEmployeeLeaves,
			"records a departure; the record stays, because their name is on the payslips they were paid"},

		{http.MethodGet, "/api/v1/attendance", AccessPermission, "hr.view",
			s.handleReadAttendance, "who was in, and when"},
		{http.MethodPost, "/api/v1/attendance", AccessPermission, "hr.manage",
			s.handleRecordAttendance,
			"records or corrects days; one row per person per day, because two would double-count the hours payroll reads"},

		{http.MethodGet, "/api/v1/leave", AccessPermission, "hr.view",
			s.handleListLeave, "time off, asked for and granted"},
		{http.MethodPost, "/api/v1/leave", AccessPermission, "hr.view",
			s.handleRequestLeave,
			"asks for time off; anybody who can see the directory can ask, because asking is not granting"},
		{http.MethodPost, "/api/v1/leave/{leaveID}/decision", AccessPermission, "hr.manage",
			s.handleDecideLeave,
			"grants or refuses; approved leave becomes attendance, so payroll reads one source"},

		// --- Advances (C5) ---
		{http.MethodGet, "/api/v1/advances", AccessPermission, "hr.view_pay",
			s.handleListAdvances, "money lent against future wages"},
		{http.MethodPost, "/api/v1/advances", AccessPermission, "payroll.run",
			s.handleIssueAdvance,
			"lends against future wages: a loan, not a cost, recovered automatically by the next run"},

		// --- Payroll (C6) ---
		//
		// Preparing and approving are separate permissions because C6 makes
		// them separate acts: a draft is a calculation somebody checks while
		// it can still be corrected, and approving it is what commits the
		// business to paying those figures.
		{http.MethodGet, "/api/v1/payroll", AccessPermission, "payroll.view",
			s.handleListPayrollRuns, "payroll runs"},
		{http.MethodPost, "/api/v1/payroll", AccessPermission, "payroll.run",
			s.handlePreparePayroll,
			"computes a draft run for a month; posts nothing"},
		{http.MethodGet, "/api/v1/payroll/{runID}", AccessPermission, "payroll.view",
			s.handleReadPayrollRun, "one run and its payslips"},
		{http.MethodPost, "/api/v1/payroll/{runID}/approve", AccessPermission, "payroll.approve",
			s.handleApprovePayroll,
			"signs the run off and posts what it owes; the wage is earned here, not paid"},
		{http.MethodPost, "/api/v1/payroll/{runID}/pay", AccessPermission, "payroll.approve",
			s.handlePayPayroll, "settles an approved run from a cash or bank account"},
		{http.MethodPost, "/api/v1/payroll/{runID}/wage-file", AccessPermission, "payroll.approve",
			s.handleGenerateWageFile,
			"builds the WPS submission; refuses while the Mudad layout is unverified, because a file in the wrong layout is rejected rather than partly right"},

		// --- End of service and commission (E6, C6) ---
		{http.MethodGet, "/api/v1/eosb", AccessPermission, "payroll.view",
			s.handleEOSBPositions,
			"what the business owes each person if they left today"},
		{http.MethodPost, "/api/v1/eosb/accrue", AccessPermission, "payroll.run",
			s.handleAccrueEOSB,
			"charges one month's end-of-service benefit; monthly, so the liability is never discovered at termination"},
		{http.MethodGet, "/api/v1/commission-rules", AccessPermission, "payroll.view",
			s.handleListCommissionRules, "the commission schemes in force"},
		{http.MethodPost, "/api/v1/commission-rules", AccessPermission, "payroll.run",
			s.handleSetCommissionRule,
			"creates a scheme, flat or tiered, by employee or store"},

		// --- the Approval Centre (D5) and the approval engine (F1) ---
		//
		// Watching and deciding are separate verbs. A requester needs to follow
		// what they asked for without being able to grant it, which is the whole
		// point of an approval; one permission covering both would make every
		// Purchase Manager their own signatory.
		//
		// There is no route that PERFORMS an approved action. The engine gates,
		// it does not execute: a module calls Evaluate before it commits and
		// either proceeds or stops, and a granted request is the module's cue to
		// be retried by the person who asked. Letting the Centre reach back into
		// whatever it approved would put the engine inside every module's
		// transaction and let a rule change break a posting path it knows
		// nothing about.
		{http.MethodGet, "/api/v1/approvals", AccessPermission, "approval.view",
			s.handleListApprovals,
			"everything waiting for somebody, oldest first"},
		{http.MethodGet, "/api/v1/approvals/mine", AccessPermission, "approval.view",
			s.handleMyApprovals,
			"what the caller themselves asked for; a separate list because " +
				"\"what happened to my request\" should not be read past " +
				"everybody else's"},
		{http.MethodGet, "/api/v1/approvals/{requestID}", AccessPermission, "approval.view",
			s.handleGetApproval, "one request and every decision taken on it"},
		{http.MethodPost, "/api/v1/approvals/{requestID}/decide", AccessPermission, "approval.decide",
			s.handleDecideApproval,
			"a multi-step rule is granted only at its LAST step, so an Owner's " +
				"signature never lands on something the Accountant never saw"},
		{http.MethodPost, "/api/v1/approvals/escalate", AccessPermission, "approval.decide",
			s.handleEscalateApprovals,
			"moves what has waited too long; on demand rather than by a timer, " +
				"so an escalation is always attributable to somebody"},

		{http.MethodGet, "/api/v1/approval-rules", AccessPermission, "approval.manage_rules",
			s.handleListApprovalRules, ""},
		{http.MethodPost, "/api/v1/approval-rules", AccessPermission, "approval.manage_rules",
			s.handleSaveApprovalRule,
			"F1's argument for a configurable engine: one codebase serves a " +
				"three-person shop and a three-hundred-person chain"},
		{http.MethodPost, "/api/v1/approval-rules/{ruleID}/active", AccessPermission, "approval.manage_rules",
			s.handleSetApprovalRuleActive,
			"a rule is switched off rather than deleted: the requests it raised " +
				"name it, and deleting one would leave a decision nobody can explain"},

		{http.MethodGet, "/api/v1/approval-delegations", AccessPermission, "approval.view",
			s.handleListDelegations, "who is covering for whom"},
		{http.MethodPost, "/api/v1/approval-delegations", AccessPermission, "approval.decide",
			s.handleDelegate,
			"handing your approvals on requires being able to give them: " +
				"approval.decide, not approval.view"},

		// --- the notification centre (D3) ---
		//
		// AccessAuthenticated, with no permission on any of them. Every query here
		// names the CALLER's own id and there is no parameter that could name
		// somebody else, so a permission would suggest that reading another
		// person's inbox is a thing that can be granted. It is not: there is no
		// code path that would serve it.
		{http.MethodGet, "/api/v1/notifications", AccessAuthenticated, "",
			s.handleListNotifications,
			"the caller's own; the unread count travels with the list so the " +
				"bell never shows a number a second request has not caught up with"},
		{http.MethodGet, "/api/v1/notifications/unread", AccessAuthenticated, "",
			s.handleUnreadCount,
			"its own query, because the bell is read on every screen and the " +
				"list is not"},
		{http.MethodPost, "/api/v1/notifications/read", AccessAuthenticated, "",
			s.handleMarkAllNotificationsRead,
			"clears the caller's own bell and nobody else's; the UPDATE names " +
				"their user id and no parameter could name another"},
		{http.MethodPost, "/api/v1/notifications/{notificationID}/read", AccessAuthenticated, "",
			s.handleMarkNotificationRead,
			"marking your own notification read is about the caller themselves, " +
				"and a notification that is not theirs is refused by the query"},
		{http.MethodPost, "/api/v1/notifications/announce", AccessPermission, "notification.manage",
			s.handleAnnounce,
			"a notice a manager sends to the whole company; the one write here " +
				"that reaches somebody else's inbox, which is why it is the one " +
				"that carries a permission"},
		{http.MethodGet, "/api/v1/notifications/preferences", AccessAuthenticated, "",
			s.handleGetNotificationPreferences,
			"every trigger, not only the ones already stored: a screen showing " +
				"three settings because three rows exist would hide the eleven " +
				"somebody most needs to switch on"},
		{http.MethodPut, "/api/v1/notifications/preferences", AccessAuthenticated, "",
			s.handleSetNotificationPreference,
			"in-app cannot be switched off, whatever the request says: the " +
				"centre is where a shop discovers why a submission failed"},

		// --- webhooks and API keys (H6) ---
		//
		// Owner-level. A webhook sends a shop's sales somewhere, and an API key
		// is a credential that acts on their behalf: both are decisions about
		// who else gets to see the business, which is not a thing a store
		// manager decides.
		//
		// A key is readable exactly once, in the response to the route that
		// creates it, and from nowhere else. That is not an omission to fix
		// later: a product that can show a key a second time is a product where
		// the key is recoverable from the database.
		{http.MethodGet, "/api/v1/webhooks", AccessPermission, "integration.view",
			s.handleListWebhooks,
			"the event vocabulary travels with the list, so a screen cannot " +
				"offer an event the server would refuse"},
		{http.MethodPost, "/api/v1/webhooks", AccessPermission, "integration.manage",
			s.handleCreateWebhook,
			"https only, enforced by the database: plain HTTP would put a " +
				"shop's sales over the wire in clear"},
		{http.MethodPost, "/api/v1/webhooks/{endpointID}/active", AccessPermission, "integration.manage",
			s.handleSetWebhookActive,
			"switched off rather than deleted, so the delivery history that " +
				"explains a missing week survives"},
		{http.MethodGet, "/api/v1/webhooks/{endpointID}/deliveries", AccessPermission, "integration.view",
			s.handleWebhookDeliveries, ""},

		{http.MethodGet, "/api/v1/api-keys", AccessPermission, "integration.view",
			s.handleListAPIKeys,
			"never the keys themselves: a prefix, and what each may do"},
		{http.MethodPost, "/api/v1/api-keys", AccessPermission, "integration.manage",
			s.handleMintAPIKey,
			"the only place a key is ever readable; its permissions are " +
				"intersected with what the caller holds, so a key can never be " +
				"an escalation"},
		{http.MethodDelete, "/api/v1/api-keys/{keyID}", AccessPermission, "integration.manage",
			s.handleRevokeAPIKey,
			"there is no un-revoke: a key is revoked because somebody believes " +
				"it leaked, and undoing that would undo the only response available"},

		// --- migration and export (H7) ---
		//
		// Three requests to import, not one. H7's flow puts Validation and
		// Preview between the file and the write, and a single "import this"
		// route would remove the step where a shop finds out what is about to
		// happen to their master data.
		{http.MethodGet, "/api/v1/imports/shapes", AccessPermission, "data.import",
			s.handleImportShapes,
			"what each kind of import needs, so a mapping screen can be built " +
				"from the server's own answer rather than a copy of it"},
		{http.MethodGet, "/api/v1/imports", AccessPermission, "data.import",
			s.handleListImports, ""},
		{http.MethodPost, "/api/v1/imports", AccessPermission, "data.import",
			s.handleUploadImport,
			"stages and checks; writes nothing"},
		{http.MethodGet, "/api/v1/imports/{importID}", AccessPermission, "data.import",
			s.handleGetImport,
			"H7's Error Report: refused rows first, each with the reason"},
		{http.MethodPost, "/api/v1/imports/{importID}/validate", AccessPermission, "data.import",
			s.handleValidateImport,
			"re-checkable after a mapping is corrected, without re-uploading"},
		{http.MethodPost, "/api/v1/imports/{importID}/commit", AccessPermission, "data.import",
			s.handleCommitImport,
			"one transaction for the whole batch: every valid row lands or none " +
				"does, because a half-finished import is worse than none"},
		{http.MethodPost, "/api/v1/imports/{importID}/cancel", AccessPermission, "data.import",
			s.handleCancelImport, ""},

		{http.MethodGet, "/api/v1/exports/{kind}", AccessPermission, "data.export",
			s.handleExport,
			"streams CSV with a filename, because what happens to it next is " +
				"that somebody opens it in Excel"},

		// --- backups (H4) ---
		//
		// This product records backups; it does not take them. Taking a dump is
		// the operator's job, and pretending otherwise would be a product that
		// claims a guarantee it cannot keep.
		//
		// What it does own is the distinction H4 insists on: a backup that ran
		// is not a backup that restores, and the health route answers the second
		// question rather than the first.
		{http.MethodGet, "/api/v1/backups/health", AccessPermission, "backup.view",
			s.handleBackupHealth,
			"reads the last VERIFIED backup, not the last successful run: the " +
				"second is a more comforting number and a less true one"},
		{http.MethodGet, "/api/v1/backups", AccessPermission, "backup.view",
			s.handleListBackups, ""},
		{http.MethodPost, "/api/v1/backups", AccessPermission, "backup.run",
			s.handleStartBackup,
			"opens the record before the work, so a backup that dies halfway " +
				"leaves a row somebody can see"},
		{http.MethodPost, "/api/v1/backups/{backupID}/finish", AccessPermission, "backup.run",
			s.handleFinishBackup, ""},
		{http.MethodPost, "/api/v1/backups/{backupID}/verify", AccessPermission, "backup.run",
			s.handleVerifyBackup,
			"a failed verification does not stamp verified_at, or the dashboard " +
				"would read a broken backup as protection"},

		// --- support tickets (H10) ---
		//
		// The one conversation that crosses the tenant boundary, which is why
		// its tables are among the few the platform may read. A ticket carries a
		// subject and a description, never business records.
		{http.MethodGet, "/api/v1/support/tickets", AccessPermission, "support.raise",
			s.handleListTickets,
			"reading your own tickets and raising one are the same act: there " +
				"is nothing to see that you did not write"},
		{http.MethodPost, "/api/v1/support/tickets", AccessPermission, "support.raise",
			s.handleRaiseTicket, ""},
		{http.MethodGet, "/api/v1/support/tickets/{ticketID}", AccessPermission, "support.raise",
			s.handleGetTicket, ""},
		{http.MethodPost, "/api/v1/support/tickets/{ticketID}/reply", AccessPermission, "support.raise",
			s.handleReplyToTicket,
			"the status follows the conversation rather than waiting for " +
				"somebody to move it, because a hand-maintained status is wrong " +
				"within a week"},
		{http.MethodPost, "/api/v1/support/tickets/{ticketID}/close", AccessPermission, "support.raise",
			s.handleCloseTicket, ""},

		// --- the audit trail (D4) ---
		//
		// Read-only, and there is no write route by design: an audit row is
		// written inside the transaction of the thing it records, never by
		// somebody calling an endpoint. 0003 puts a trigger on the table that
		// refuses every UPDATE and DELETE, Owner included.
		{http.MethodGet, "/api/v1/audit", AccessPermission, "accounting.view",
			s.handleAuditTrail,
			"the trail is evidence about the books, so it is gated with the books " +
				"rather than behind a verb of its own that nobody holds"},

		{http.MethodGet, "/api/v1/reports/trial-balance", AccessPermission, "accounting.view",
			s.handleTrialBalance, ""},
		{http.MethodGet, "/api/v1/reports/profit-and-loss", AccessPermission, "accounting.view",
			s.handleProfitAndLoss, ""},
		{http.MethodGet, "/api/v1/reports/balance-sheet", AccessPermission, "accounting.view",
			s.handleBalanceSheet, ""},
		{http.MethodGet, "/api/v1/reports/cash-flow", AccessPermission, "accounting.view",
			s.handleCashFlow, ""},

		// Preparation, never filing. The official form layout is a regulatory
		// value and no verified rule for it exists, so these totals are not
		// mapped onto numbered boxes.
		{http.MethodGet, "/api/v1/reports/vat-return", AccessPermission, "accounting.view",
			s.handleVATReturn, ""},

		// --- platform control plane ---
		{http.MethodPost, "/api/v1/platform/tenants", AccessSuperAdmin, "",
			s.handleCreateTenant, ""},
		{http.MethodPost, "/api/v1/platform/users/{userID}/reset-password",
			AccessSuperAdmin, "", s.handleAdminResetPassword, ""},
	}
}

// Handler builds the mounted router.
func (s *Server) Handler(mws ...func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	for _, mw := range mws {
		r.Use(mw)
	}

	for _, rt := range s.Routes() {
		switch rt.Access {
		case AccessPublic:
			r.Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessAuthenticated:
			r.With(s.mw.Authenticate).Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessPermission:
			r.With(s.mw.Authenticate, s.mw.Require(rt.Permission)).
				Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessSuperAdmin:
			r.With(s.mw.Authenticate, s.mw.RequireSuperAdmin).
				Method(rt.Method, rt.Pattern, rt.Handler)
		}
	}

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeNotFound(w, r)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeNotFound(w, r)
	})

	return r
}
