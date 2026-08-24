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

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/settlement"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
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
	health       func() error
	version      string

	// onboarding is optional: an installation with no data encryption key
	// cannot hold a ZATCA credential, and the routes report that rather than
	// failing to start.
	onboarding *zatca.Onboarding
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
		health:       health,
		version:      version,
	}
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
		{http.MethodGet, "/api/v1/dashboard/stock", AccessPermission, "inventory.view",
			s.handleStockDetail,
			"variants low or out of stock, summed across the company's warehouses"},
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
