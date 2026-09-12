//go:build integration

// The three levels the product separates, tested where they meet.
//
// Phase 4's question is not "is there authorization" — there is, and a lot of
// it. It is whether the boundaries hold when somebody pushes on them from a
// real, legitimately signed-in account, using curl rather than the interface.
//
// # What was already covered, and is not repeated here
//
// A great deal, and it is worth naming so this file does not duplicate it:
//
//   - `route_authz_test.go` walks EVERY permission-guarded route with a
//     signed-in cashier who lacks the permission, and demands 403.
//   - `cross_tenant_walk_test.go` walks every route carrying an id placeholder
//     as the OWNER of one tenant, using real ids belonging to another. That is
//     the whole IDOR matrix — products, customers, orders, invoices, stock,
//     suppliers, staff — done by enumeration rather than by list.
//   - `access_test.go` walks every route unauthenticated, and refuses a super
//     admin on tenant routes.
//   - `isolation_test.go` proves row-level security at the connection, by
//     explicit id, on write, and across transactions.
//
// So what follows is the coverage those leave out.
//
// # The three gaps this file fills
//
// **A mutation behind a reading permission.** The route walk skips any route
// whose permission the cashier HOLDS. A route that changes data while gated on
// a `.view` permission is therefore invisible to it: the cashier holds the
// view, so the route is treated as legitimately allowed and never called. Five
// such routes exist, every one of them deliberate and reasoned. Nothing pinned
// the set, so a sixth could be added by accident.
//
// **A disabled account already holding a token.** Nothing tested it, and it did
// not work. See `TestADisabledEmployeeStopsWorking`.
//
// **The owner as attacker.** The platform-plane tests use a cashier, the
// weakest account. The owner is the interesting one: they hold every permission
// their business has, so if any route confuses "holds everything here" with
// "may do anything", the owner is who finds it.
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/identity"
)

// --- 1. a mutation must not hide behind a reading permission --------------

// mutationsOnAReadingPermission are the routes that change something while
// gated on a `.view` permission.
//
// Each is deliberate, each carries its reasoning in the route table, and each
// is listed here so that the sixth one is a decision somebody makes rather than
// a line somebody copies.
//
// Two of them genuinely create a record, and that is the product's considered
// answer rather than an oversight: asking for leave is not granting it, and the
// counter that can see repair jobs is the counter that books them in. The other
// three compute and store nothing.
//
// This is the same shape as `TestPublicRoutesAreOnlyTheExpectedOnes`: a short,
// reviewed list, where the test is the review.
var mutationsOnAReadingPermission = map[string]string{
	"POST /api/v1/installments/quote":            "previews a schedule and creates nothing",
	"POST /api/v1/promotions/quote":              "prices a cart while it is built; nothing is redeemed until the sale is finalised",
	"POST /api/v1/pos/sales/{invoiceID}/reprint": "reprinting is looking a sale up again; TestReprintCarriesTheSamePermissionAsLookingASaleUp says so deliberately",

	// The two that write.
	"POST /api/v1/leave":        "asking for time off is not granting it; the decision is hr.manage",
	"POST /api/v1/service-jobs": "a counter that can see repair jobs can book one in, because it is the same conversation with the customer",
}

// A route that changes data must not be gated on a permission to read.
//
// This is the hole `TestEveryGuardedRouteRefusesAUserWithoutThePermission`
// cannot see. That test skips every route whose permission the cashier holds —
// correctly, because those are legitimately allowed — which means a DELETE
// guarded on `catalog.view` would be skipped for anybody holding `catalog.view`
// and never called at all.
//
// A view-only member of staff is the case the product sells on: somebody who
// may look at the takings and must not touch them. If a mutation sits behind a
// reading verb, that person can perform it, and no amount of hiding the button
// changes it.
func TestNoMutationHidesBehindAReadingPermission(t *testing.T) {
	s := &Server{}
	seen := map[string]bool{}
	checked := 0

	for _, rt := range s.Routes() {
		if rt.Access != AccessPermission {
			continue
		}
		switch rt.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		checked++

		// The verb half of `<module>.<verb>`. `TestPermissionNamesAreWellFormed`
		// guarantees the shape.
		_, verb, _ := strings.Cut(rt.Permission, ".")
		if verb != "view" {
			continue
		}

		key := rt.Method + " " + rt.Pattern
		seen[key] = true
		if _, allowed := mutationsOnAReadingPermission[key]; allowed {
			continue
		}

		t.Errorf("%s changes something and is gated on %q, a permission to "+
			"READ. Anybody with view-only access to that module can perform "+
			"it, whatever the interface shows them. Either gate it on a "+
			"writing verb, or add it to mutationsOnAReadingPermission with "+
			"the reason it is safe.", key, rt.Permission)
	}

	// The list must not outlive the routes. An entry for a route that no longer
	// exists is an exemption nobody is checking, and the next route to take
	// that path inherits it silently.
	for key := range mutationsOnAReadingPermission {
		if !seen[key] {
			t.Errorf("%s is exempted from this rule but is no longer a "+
				"mutating route gated on a reading permission. Remove it.", key)
		}
	}

	if checked < 100 {
		t.Errorf("only %d mutating routes were examined; this check has fallen "+
			"off the route table", checked)
	}
	t.Logf("%d mutating routes examined, %d deliberate exceptions",
		checked, len(mutationsOnAReadingPermission))
}

// --- 2. a disabled account stops working ---------------------------------

// disableUser marks somebody disabled the way the product does, without going
// through the staff routes — so the test is about what the ACCESS layer does
// with a disabled account rather than about who may disable one.
func (h *harness) disableUser(t *testing.T, email string) {
	t.Helper()
	ctx := context.Background()
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx,
			`UPDATE app_user SET status = 'disabled' WHERE email = $1`, email)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("disabling %s changed %d rows", email, tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatalf("disabling %s: %v", email, err)
	}
}

// A member of staff who is disabled stops being able to work, now.
//
// # What used to happen
//
// They kept working for up to fifteen minutes.
//
// An access token is a signed statement about the past. `TokenService.Verify`
// checks the signature, the issuer, the expiry and the shape of the claims, and
// deliberately touches no database at all — so a token minted for somebody in
// good standing stays cryptographically perfect after they are dismissed.
//
// Disabling somebody does revoke their sessions, so they cannot refresh and
// cannot sign in again. But the token already in their browser kept working
// until it expired, and the default lifetime is fifteen minutes. A quarter of
// an hour is long enough to ring up sales, move stock, or read the books.
//
// # Why the fix went where it did
//
// `Authorizer.Resolve` is the one thing that already reads the database on
// every request, and `grantsCacheTTL` already argues for why: a revocation
// "must take effect now", which is the reason permissions are resolved per
// request rather than baked into the token.
//
// A revoked PERMISSION took effect in five seconds. A revoked ACCOUNT took up
// to fifteen minutes. That was not a decision anybody made — the account was
// simply never looked at.
func TestADisabledEmployeeStopsWorking(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "cashier")
	token := h.login(t, email)

	// The token works, so the refusal below is about the disabling and not
	// about a token that was never any good.
	before := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("the token did not work before disabling: status %d",
			before.StatusCode)
	}

	h.disableUser(t, email)
	h.authz.Invalidate(userIDOf(t, h, email))

	// The same token, unchanged and not yet expired.
	after := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a disabled employee's existing token still works: status "+
			"%d, want 401. They cannot sign in and cannot refresh, but the "+
			"token already in their browser is good until it expires — up to "+
			"fifteen minutes of ringing up sales after being dismissed.",
			after.StatusCode)
	}

	// And they cannot sign in again to get a fresh one.
	relogin := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": email, "password": testPassword})
	relogin.Body.Close()
	if relogin.StatusCode == http.StatusOK {
		t.Error("a disabled employee signed in")
	}
}

// The whole sequence, in order, for somebody who is dismissed mid-shift.
//
// The individual refusals are asserted above and elsewhere. This exists because
// the ORDER is the thing that matters to the person being locked out: they have
// a token in a tab, a cookie in the browser, and a URL in their history, and
// every one of those has to stop working. Proving each separately leaves open
// the possibility that one of them lets the others back in.
//
// It also pins the cache window, which is the honest limit of this design and
// the number that belongs in a test rather than only in a document.
func TestEveryDoorShutsOnADismissedEmployee(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "cashier")

	// Sign in the way a browser does, so there is a real refresh cookie and a
	// real CSRF pair to try afterwards.
	signIn := signInRaw(t, h, email, false)
	refreshCookie := cookieNamed(signIn, refreshCookieName)
	csrfCookie := cookieNamed(signIn, csrfCookieName)
	token, _ := decodeLogin(t, signIn)["access_token"].(string)
	signIn.Body.Close()

	if token == "" || refreshCookie == nil || csrfCookie == nil {
		t.Fatal("signing in did not produce a token and the session cookies")
	}

	// 1. The token works, so every refusal below is about the disabling.
	if got := h.statusWithToken(t, "/api/v1/auth/me", token); got != http.StatusOK {
		t.Fatalf("the token did not work before disabling: %d", got)
	}

	// 2. Dismissed.
	h.disableUser(t, email)

	// 3. The cache is the only thing between them and the door, and its window
	// is real: for up to `grantsCacheTTL` the old grants answer. Rather than
	// sleeping five seconds in every CI run, the cache is dropped the way the
	// product would if anything called Invalidate — which nothing does, so the
	// five-second window below is the honest figure and is asserted as such.
	h.authz.Invalidate(userIDOf(t, h, email))

	// 4. The access token they are already holding.
	if got := h.statusWithToken(t, "/api/v1/auth/me", token); got != http.StatusUnauthorized {
		t.Errorf("the old access token still works: %d, want 401", got)
	}

	// 5. A protected route that reads real data, not just their own identity.
	if got := h.statusWithToken(t, "/api/v1/catalog/products", token); got != http.StatusUnauthorized {
		t.Errorf("the old token still reads the catalogue: %d, want 401", got)
	}

	// 6. The refresh cookie, with its CSRF echo, which is the one thing that
	// could mint a NEW token and undo all of the above.
	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(refreshCookie)
	req.AddCookie(csrfCookie)
	req.Header.Set(csrfHeaderName, csrfCookie.Value)
	refreshed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	defer refreshed.Body.Close()
	if refreshed.StatusCode < 400 {
		t.Errorf("a dismissed employee refreshed their session: %d. Every "+
			"other refusal is undone by this one.", refreshed.StatusCode)
	}

	// 7. Signing in again from scratch.
	again := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": email, "password": testPassword})
	again.Body.Close()
	if again.StatusCode == http.StatusOK {
		t.Error("a dismissed employee signed in again")
	}

	// 8. Typing a URL. The API is the same surface either way — the browser
	// sends the same request the screen would — so a "direct URL" reaches
	// exactly this, and there is no separate door to shut.
	if got := h.statusWithToken(t, "/api/v1/dashboard/overview", token); got == http.StatusOK {
		t.Error("a screen's own data still loads for a dismissed employee")
	}
}

// statusWithToken calls a path and reports only the status.
func (h *harness) statusWithToken(t *testing.T, path, token string) int {
	t.Helper()
	resp := h.do(t, http.MethodGet, path, token, nil)
	defer resp.Body.Close()
	return resp.StatusCode
}

// The cache window is five seconds, and that is the limit of this design.
//
// Asserted rather than described, because it is the number somebody will want
// when they ask how long a dismissed employee can keep working, and a figure
// that lives only in a comment drifts away from the code under it.
func TestRevocationIsBoundedByTheGrantsCache(t *testing.T) {
	if identity.GrantsCacheTTL > 5*time.Second {
		t.Errorf("the grants cache is %v. Every revocation — a permission, a "+
			"role, a disabled account — takes that long to bite, because "+
			"nothing in the product calls Invalidate. Anything longer needs "+
			"deciding rather than drifting.", identity.GrantsCacheTTL)
	}
}

// A disabled PLATFORM OPERATOR stops working too.
//
// The higher-stakes half of the same gap, and it was worse: `Resolve` returned
// immediately for a super admin without reading anything, so the account behind
// the highest-privilege token in the product was never consulted at all.
func TestADisabledPlatformOperatorStopsWorking(t *testing.T) {
	h := newHarness(t)
	email := h.seedSuperAdmin(t)
	token := h.login(t, email)

	before := h.do(t, http.MethodGet, "/api/v1/platform/health", token, nil)
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("the operator's token did not work before disabling: status %d",
			before.StatusCode)
	}

	h.disableUser(t, email)
	h.authz.Invalidate(userIDOf(t, h, email))

	after := h.do(t, http.MethodGet, "/api/v1/platform/health", token, nil)
	defer after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("a disabled platform operator still reads the control plane: "+
			"status %d, want 401", after.StatusCode)
	}
}

// Somebody holding a one-time password can still reach the screen that changes
// it.
//
// The guard above refuses anything that is not `active` or `invited`, and
// `invited` is exactly the state a newly provisioned owner is in. Refusing them
// would make a new business impossible to activate — the account would be
// created, handed over, and unable to do the one thing it is required to do
// first.
func TestAnInvitedOwnerCanStillChangeTheirPassword(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, resp := h.createBusiness(t, admin, nil)
	if made.TenantID == "" {
		t.Fatalf("creating the business: status %d", resp.StatusCode)
	}

	owner := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{
			"email": made.OwnerEmail, "password": made.TemporaryPassword,
		})
	defer owner.Body.Close()
	if owner.StatusCode != http.StatusOK {
		t.Fatalf("the new owner could not sign in: status %d — %s",
			owner.StatusCode, readBody(t, owner))
	}
	body := decodeJSON(t, owner)
	if body["must_change_password"] != true {
		t.Error("a one-time password did not require changing")
	}
	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatal("no token was issued")
	}

	// The account is `invited`, and it has to be able to act.
	me := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Errorf("an invited owner cannot reach an authenticated route: "+
			"status %d. They would be unable to change the one-time password "+
			"they are required to change before anything else.", me.StatusCode)
	}
}

// userIDOf reads an account's id.
func userIDOf(t *testing.T, h *harness, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	ctx := context.Background()
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM app_user WHERE email = $1`, email).Scan(&id)
	}); err != nil {
		t.Fatalf("reading the id of %s: %v", email, err)
	}
	return id
}

// --- 3. a reading permission does not act for other people ----------------

// Asking for time off is asking for your OWN time off.
//
// `POST /leave` is gated on `hr.view`, deliberately: asking is not granting,
// and granting is `hr.manage`. The route's own note says "anybody who can see
// the directory can ask", and asking for yourself is what that meant.
//
// What it did was take the employee from the REQUEST BODY and check nothing, so
// anybody who could see the staff directory could file leave in anybody else's
// name — including the owner's. `requested_by` recorded who really asked, and
// the request lands as `requested` and cannot become attendance without a
// manager deciding it, so this was never a way to take unapproved time off. It
// was a way to put rows in somebody else's record that they did not put there.
//
// This is the case the guarded-route walk cannot reach: the caller HOLDS the
// permission the route asks for, so it is skipped as legitimately allowed. The
// authorization that was missing is about the argument, not the route.
func TestAskingForLeaveMeansYourOwnLeave(t *testing.T) {
	h := newHarness(t)

	// One shop, two people: the owner keeps the records, the other does not.
	shop := h.seedShop(t, "owner")
	clerkEmail, clerkUserID := h.newUserInTenant(t, shop.tenantID, "accountant")
	clerk := h.login(t, clerkEmail)

	scoped := func(path string) string {
		return path + "?company_id=" + shop.companyID.String()
	}

	// An employee record for the clerk, linked to their login, and one for a
	// colleague who has none.
	mine := h.newEmployee(t, shop, scoped("/api/v1/employees"), clerkUserID.String())
	colleague := h.newEmployee(t, shop, scoped("/api/v1/employees"), "")

	leave := func(employeeID string) map[string]any {
		return map[string]any{
			"employee_id": employeeID, "kind": "annual", "is_paid": true,
			"starts_on": "2026-11-02", "ends_on": "2026-11-06",
		}
	}

	// Their own: allowed. This is the whole reason the route is gated on a
	// reading permission rather than on `hr.manage`, and the fix must not
	// cost it.
	own := h.do(t, http.MethodPost, scoped("/api/v1/leave"), clerk, leave(mine))
	defer own.Body.Close()
	if own.StatusCode != http.StatusCreated {
		t.Fatalf("somebody could not ask for their own time off: %d — %s",
			own.StatusCode, readBody(t, own))
	}

	// A colleague's: refused.
	theirs := h.do(t, http.MethodPost, scoped("/api/v1/leave"), clerk,
		leave(colleague))
	defer theirs.Body.Close()
	if theirs.StatusCode != http.StatusForbidden {
		t.Errorf("an employee filed leave in a colleague's name: %d, want 403. "+
			"Holding hr.view is permission to SEE the directory, not to act "+
			"for the people in it.", theirs.StatusCode)
	}

	// And the owner still can, because entering leave for somebody with no
	// login of their own is what keeping the records means.
	byOwner := h.do(t, http.MethodPost, scoped("/api/v1/leave"), shop.token,
		leave(colleague))
	defer byOwner.Body.Close()
	if byOwner.StatusCode != http.StatusCreated {
		t.Errorf("a manager could not enter leave for their staff: %d — %s",
			byOwner.StatusCode, readBody(t, byOwner))
	}
}

// newEmployee adds a member of staff and returns their id. `userID` empty is
// somebody with no login — a cleaner paid in cash, somebody on a paper rota.
func (h *harness) newEmployee(
	t *testing.T, shop *shopFixture, path, userID string,
) string {
	t.Helper()
	body := map[string]any{
		"full_name": "Staff " + randomSuffix(),
		"position":  "Assistant",
		// Required: length of service decides the end-of-service benefit.
		"joined_on": "2025-01-06",
	}
	if userID != "" {
		body["user_id"] = userID
	}
	resp := h.do(t, http.MethodPost, path, shop.token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating an employee: %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	out := decodeJSON(t, resp)
	if id, ok := out["id"].(string); ok && id != "" {
		return id
	}
	// Some create routes answer {"data": {...}}.
	if data, ok := out["data"].(map[string]any); ok {
		if id, ok := data["id"].(string); ok {
			return id
		}
	}
	t.Fatalf("the created employee has no id: %v", out)
	return ""
}

// --- 4. the owner is not a platform operator ------------------------------

// An owner holds everything their business has, and nothing of the platform's.
//
// The existing platform-boundary test uses a cashier. The owner is the
// interesting caller: they hold every permission the product seeds, so a route
// that confused "holds everything in this tenant" with "may do anything" would
// be found by them and not by a cashier.
//
// 404 and not 403, on every one of them. A 403 confirms the route exists, which
// tells somebody probing exactly where to keep pushing.
func TestAnOwnerHoldsNothingOnThePlatformPlane(t *testing.T) {
	h := newHarness(t)
	owner := h.login(t, h.seedUserWithRole(t, "owner"))

	s := &Server{}
	checked := 0
	for _, rt := range s.Routes() {
		if !strings.HasPrefix(rt.Pattern, "/api/v1/platform/") {
			continue
		}
		checked++

		var body any
		if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
			body = map[string]string{}
		}
		resp := h.do(t, rt.Method, fillPathParams(rt.Pattern), owner, body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("an owner reached %s %s and got %d, want 404. They hold "+
				"every permission their own business has, which is not a "+
				"claim on the platform's.",
				rt.Method, rt.Pattern, resp.StatusCode)
		}
	}

	if checked == 0 {
		t.Fatal("no platform route was exercised")
	}
	t.Logf("%d platform routes refused an owner", checked)
}

// An owner cannot promote themselves by saying so.
//
// Every field a client might hope grants platform authority, sent to the routes
// that shape an account. The claim comes from a signed token and nothing in a
// request body reaches it; `TestUnknownFieldsAreRejected` means an invented
// field is refused outright rather than ignored, which is the stronger of the
// two behaviours — an ignored field leaves the caller believing it worked.
func TestAnOwnerCannotDeclareThemselvesSuperAdmin(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")
	owner := h.login(t, email)

	for _, body := range []map[string]any{
		{"is_super_admin": true},
		{"sa": true},
		{"tenant_id": nil},
		{"role": "super_admin"},
		{"permissions": []string{"platform.admin"}},
	} {
		// Through the account's own settings, which is the request an owner
		// can legitimately make and the obvious place to try smuggling a field.
		resp := h.do(t, http.MethodPut, "/api/v1/auth/me", owner, body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("PUT /auth/me accepted %v", body)
		}
	}

	// Whatever happened above, the session is still a tenant session.
	me := h.do(t, http.MethodGet, "/api/v1/auth/me", owner, nil)
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatalf("reading identity: status %d", me.StatusCode)
	}
	if decodeJSON(t, me)["is_super_admin"] == true {
		t.Fatal("an owner became a super admin by asking")
	}

	// And the platform plane still refuses them.
	plat := h.do(t, http.MethodGet, "/api/v1/platform/health", owner, nil)
	plat.Body.Close()
	if plat.StatusCode != http.StatusNotFound {
		t.Errorf("after the attempts above, the platform answered %d",
			plat.StatusCode)
	}
}
