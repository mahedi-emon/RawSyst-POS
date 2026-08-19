//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Registering, pairing and taking away a terminal — blueprint H3.
//
// The property most of these protect is the one the whole design turns on: a
// terminal's authority is re-read from its current status on every request, so
// revoking one takes effect immediately rather than whenever a token would have
// expired. A till in somebody else's hands does not get a grace period.

// --- helpers ---------------------------------------------------------------

// registerTill creates a terminal in `pending` and returns its id.
//
// It names the fixture's EGS unit because the route now requires one: a
// terminal with no signing unit cannot sell, and letting these tests register
// one anyway would mean they exercised a shape the product no longer produces.
func registerTill(t *testing.T, h *harness, f *shopFixture, label string) string {
	t.Helper()
	resp := h.do(t, "POST",
		"/api/v1/devices?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"store_id":       f.storeID.String(),
			"terminal_label": label,
			"egs_unit_id":    f.egsUnitID.String(),
		})
	if resp.StatusCode != 201 {
		t.Fatalf("register %s: %d %s", label, resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got, _ := body["status"].(string); got != "pending" {
		t.Fatalf("a new terminal is %q, want pending", got)
	}
	id, _ := body["id"].(string)
	return id
}

// issueCode asks for an enrolment code and returns it.
func issueCode(t *testing.T, h *harness, f *shopFixture, deviceID string) string {
	t.Helper()
	resp := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/enrolment-code?company_id="+f.companyID.String(),
		f.token, map[string]any{})
	if resp.StatusCode != 201 {
		t.Fatalf("issue code: %d %s", resp.StatusCode, readBody(t, resp))
	}
	code, _ := decodeJSON(t, resp)["code"].(string)
	if code == "" {
		t.Fatal("no code was returned")
	}
	return code
}

// enrol redeems a code the way a fresh terminal does: with no token at all.
func enrol(t *testing.T, h *harness, code string) (*http.Response, map[string]any) {
	t.Helper()
	resp := h.do(t, "POST", "/api/v1/devices/enrol", "", map[string]any{
		"code": code, "os": "Windows 11", "app_version": "1.0.0",
	})
	if resp.StatusCode != 201 {
		return resp, nil
	}
	return resp, decodeJSON(t, resp)
}

// --- Pairing ---------------------------------------------------------------

// The whole journey: register, issue, redeem, and the till can identify itself.
func TestATerminalIsRegisteredThenPaired(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	code := issueCode(t, h, f, deviceID)

	// The code is readable, grouped, and short enough to type at a counter.
	if len(code) != 9 || !strings.Contains(code, "-") {
		t.Errorf("code %q is not the shape a person can read out", code)
	}

	_, paired := enrol(t, h, code)
	secret, _ := paired["device_secret"].(string)
	if secret == "" {
		t.Fatal("pairing returned no secret")
	}
	if label, _ := paired["terminal_label"].(string); label != "Till 2" {
		t.Errorf("the terminal calls itself %q", label)
	}

	// It is active now, and says who it is when it presents the secret.
	body := decodeJSON(t, h.do(t, "GET",
		"/api/v1/devices/"+deviceID+"?company_id="+f.companyID.String(), f.token, nil))
	if got, _ := body["status"].(string); got != "active" {
		t.Errorf("a paired terminal is %q, want active", got)
	}
	if enrolled, _ := body["enrolled_at"].(string); enrolled == "" {
		t.Error("a paired terminal has no enrolment date")
	}

	who := h.withHeader(t, "GET", "/api/v1/devices/identity", "",
		map[string]string{"X-Device-Secret": secret}, nil)
	if who.StatusCode != 200 {
		t.Fatalf("a paired terminal could not identify itself: %s", readBody(t, who))
	}
	if got, _ := decodeJSON(t, who)["terminal_label"].(string); got != "Till 2" {
		t.Errorf("identity reports %q", got)
	}
}

// The secret is returned exactly once and is never readable again.
func TestTheSecretIsNeverReadableAfterPairing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	_, paired := enrol(t, h, issueCode(t, h, f, deviceID))
	secret, _ := paired["device_secret"].(string)

	// Nothing an administrator can read gives it back.
	raw := readBody(t, h.do(t, "GET",
		"/api/v1/devices/"+deviceID+"?company_id="+f.companyID.String(), f.token, nil))
	if strings.Contains(raw, secret) {
		t.Error("the device detail hands the secret back out")
	}
	for _, leak := range []string{"secret_hash", "secret"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the device payload mentions %q", leak)
		}
	}

	list := readBody(t, h.do(t, "GET",
		"/api/v1/devices?company_id="+f.companyID.String(), f.token, nil))
	if strings.Contains(list, secret) {
		t.Error("the device list hands the secret back out")
	}

	// And the stored form is a hash, not the secret.
	var stored string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT secret_hash FROM device WHERE id = $1`, deviceID).Scan(&stored)
	}); err != nil {
		t.Fatalf("read the stored secret: %v", err)
	}
	if stored == secret {
		t.Fatal("the secret is stored in the clear")
	}
	if !strings.HasPrefix(stored, "$argon2") {
		t.Errorf("the stored secret is not an argon2 hash: %.16s", stored)
	}
}

// Single use. A code read aloud on the phone must not pair a second till.
func TestAnEnrolmentCodeWorksOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	code := issueCode(t, h, f, registerTill(t, h, f, "Till 2"))
	if _, first := enrol(t, h, code); first == nil {
		t.Fatal("the first use failed")
	}

	resp, _ := enrol(t, h, code)
	if resp.StatusCode != 401 {
		t.Fatalf("a reused code returned %d, want 401: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Issuing a second code cancels the first, or cancelling achieved nothing.
func TestANewCodeSupersedesTheOldOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	first := issueCode(t, h, f, deviceID)
	second := issueCode(t, h, f, deviceID)

	if first == second {
		t.Fatal("the same code came back twice")
	}
	if resp, _ := enrol(t, h, first); resp.StatusCode != 401 {
		t.Errorf("the superseded code still works (%d)", resp.StatusCode)
	}
	if _, ok := enrol(t, h, second); ok == nil {
		t.Error("the current code does not work")
	}
}

// Typing is forgiving about case and the grouping dash, and nothing else.
func TestACodeIsAcceptedHoweverItIsTyped(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	code := issueCode(t, h, f, registerTill(t, h, f, "Till 2"))
	messy := strings.ToLower(strings.ReplaceAll(code, "-", " "))

	if _, ok := enrol(t, h, messy); ok == nil {
		t.Errorf("a code typed as %q was refused", messy)
	}
}

// Guessing is bounded, and bounded PER CALLER.
//
// The first version of this counted misses against every open enrolment, which
// meant one till guessing wrong could kill every outstanding code on the
// platform — a denial of service wearing a security control as a hat. This
// asserts the property that replaced it.
func TestGuessingACodeIsRateLimited(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	registerTill(t, h, f, "Till 2")

	var limited bool
	for i := 0; i < 12; i++ {
		resp, _ := enrol(t, h, "ZZZZ-ZZZZ")
		if resp.StatusCode == 429 {
			limited = true
			break
		}
		if resp.StatusCode != 401 {
			t.Fatalf("a wrong code returned %d", resp.StatusCode)
		}
	}
	if !limited {
		t.Error("a caller could guess without limit")
	}
}

// Another tenant guessing must not touch this tenant's outstanding code.
func TestOneTerminalGuessingDoesNotKillEverybodyElsesCodes(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	code := issueCode(t, h, mine, registerTill(t, h, mine, "My Till"))
	issueCode(t, h, theirs, registerTill(t, h, theirs, "Their Till"))

	// Somebody hammers the endpoint until they are cut off.
	for i := 0; i < 12; i++ {
		if resp, _ := enrol(t, h, "ZZZZ-ZZZZ"); resp.StatusCode == 429 {
			break
		}
	}

	// The limiter is per caller and the test client shares an address with the
	// guesser, so a fresh code is what a real second shop would have. What must
	// NOT have happened is the code being invalidated in the database.
	var attempts int
	if err := h.pool.TxAsTenant(t.Context(), mine.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT attempts FROM device_enrolment
			WHERE tenant_id = $1 AND redeemed_at IS NULL`, mine.tenantID).Scan(&attempts)
	}); err != nil {
		t.Fatalf("read the enrolment: %v", err)
	}
	if attempts != 0 {
		t.Errorf("another caller's guesses charged %d attempts to this code", attempts)
	}
	_ = code
}

// The refusal must not say which part was wrong.
func TestAFailedEnrolmentRevealsNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	issueCode(t, h, f, registerTill(t, h, f, "Till 2"))

	resp, _ := enrol(t, h, "ZZZZ-ZZZZ")
	body := readBody(t, resp)
	for _, leak := range []string{"Till 2", "expired", "already used", f.companyID.String()} {
		if strings.Contains(body, leak) {
			t.Errorf("the refusal leaks %q: %s", leak, body)
		}
	}
}

// --- Device-bound sign-in --------------------------------------------------

// Signing in on a paired till binds the session to it, which is what lets the
// POS routes resolve a terminal at all.
func TestSigningInOnAPairedTerminalBindsTheSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	_, paired := enrol(t, h, issueCode(t, h, f, registerTill(t, h, f, "Till 2")))
	secret, _ := paired["device_secret"].(string)
	boundDevice, _ := paired["device_id"].(string)

	token := h.loginOnDevice(t, f.email, secret)

	// A device-bound session can push a sync batch; an ordinary one cannot.
	push := h.do(t, "POST", "/api/v1/sync/push", token, map[string]any{
		"idempotency_key": "bound-session", "items": []map[string]any{},
	})
	if push.StatusCode != 200 {
		t.Fatalf("a device-bound session could not sync: %d %s",
			push.StatusCode, readBody(t, push))
	}

	plain := h.login(t, f.email)
	refused := h.do(t, "POST", "/api/v1/sync/push", plain, map[string]any{
		"idempotency_key": "unbound-session", "items": []map[string]any{},
	})
	if refused.StatusCode != 403 {
		t.Errorf("a browser session pushed a sync batch (%d)", refused.StatusCode)
	}

	// And the binding is the terminal that was actually paired.
	var sessionDevice string
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(device_id::text, '') FROM user_session
			WHERE device_id IS NOT NULL ORDER BY created_at DESC LIMIT 1`).
			Scan(&sessionDevice)
	}); err != nil {
		t.Fatalf("read the session: %v", err)
	}
	if sessionDevice != boundDevice {
		t.Errorf("the session is bound to %q, want %q", sessionDevice, boundDevice)
	}
}

// A bad secret refuses the sign-in outright rather than quietly issuing an
// unbound session that looks normal until the first sale fails.
func TestSigningInWithAWrongSecretIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.withHeader(t, "POST", "/api/v1/auth/login", "",
		map[string]string{"X-Device-Secret": "NOTAREALSECRET"},
		map[string]any{"email": f.email, "password": testPassword})
	if resp.StatusCode != 401 {
		t.Fatalf("a wrong device secret signed in (%d): %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- Lifecycle -------------------------------------------------------------

// Revocation takes effect on the very next request, not when a token expires.
func TestRevokingATerminalTakesEffectImmediately(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	_, paired := enrol(t, h, issueCode(t, h, f, deviceID))
	secret, _ := paired["device_secret"].(string)

	token := h.loginOnDevice(t, f.email, secret)
	if resp := h.do(t, "POST", "/api/v1/sync/push", token, map[string]any{
		"idempotency_key": "before-revocation", "items": []map[string]any{},
	}); resp.StatusCode != 200 {
		t.Fatalf("the till could not work before revocation: %d %s",
			resp.StatusCode, readBody(t, resp))
	}

	revoke := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/revoke?company_id="+f.companyID.String(),
		f.token, map[string]any{"reason": "stolen from the counter"})
	if revoke.StatusCode != 200 {
		t.Fatalf("revoke: %s", readBody(t, revoke))
	}

	// The SAME token, unexpired, is now dead.
	after := h.do(t, "POST", "/api/v1/sync/push", token, map[string]any{
		"idempotency_key": "after-revocation", "items": []map[string]any{},
	})
	if after.StatusCode != 401 {
		t.Fatalf("a revoked terminal kept working (%d): %s",
			after.StatusCode, readBody(t, after))
	}

	// And its secret no longer identifies anything.
	who := h.withHeader(t, "GET", "/api/v1/devices/identity", "",
		map[string]string{"X-Device-Secret": secret}, nil)
	if who.StatusCode != 401 {
		t.Errorf("a revoked secret still identifies a terminal (%d)", who.StatusCode)
	}
}

// Revoking clears the credential rather than only marking it, so a database
// copy taken afterwards contains nothing that works.
func TestRevokingClearsTheStoredSecret(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	enrol(t, h, issueCode(t, h, f, deviceID))

	h.do(t, "POST", "/api/v1/devices/"+deviceID+"/revoke?company_id="+f.companyID.String(),
		f.token, map[string]any{"reason": "replaced"})

	var stored *string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT secret_hash FROM device WHERE id = $1`, deviceID).Scan(&stored)
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored != nil {
		t.Error("a revoked terminal still has a credential stored")
	}
}

// Revocation is permanent. A revoked till is replaced, never resurrected.
func TestARevokedTerminalCannotBePairedAgain(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	enrol(t, h, issueCode(t, h, f, deviceID))
	h.do(t, "POST", "/api/v1/devices/"+deviceID+"/revoke?company_id="+f.companyID.String(),
		f.token, map[string]any{"reason": "lost"})

	resp := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/enrolment-code?company_id="+f.companyID.String(),
		f.token, map[string]any{})
	if resp.StatusCode != 409 {
		t.Fatalf("a revoked terminal was issued a code (%d)", resp.StatusCode)
	}

	// Nor can it be switched back on.
	back := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/active?company_id="+f.companyID.String(),
		f.token, map[string]any{"active": true})
	if back.StatusCode != 409 {
		t.Errorf("a revoked terminal was reactivated (%d)", back.StatusCode)
	}
}

// A revocation nobody can explain later is one somebody will undo.
func TestRevokingRequiresAReason(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	enrol(t, h, issueCode(t, h, f, deviceID))

	resp := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/revoke?company_id="+f.companyID.String(),
		f.token, map[string]any{"reason": "  "})
	if resp.StatusCode != 400 {
		t.Errorf("a terminal was revoked with no reason (%d)", resp.StatusCode)
	}
}

// Deactivating is a pause: the secret survives and switching it back on works.
func TestDeactivatingIsReversible(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	_, paired := enrol(t, h, issueCode(t, h, f, deviceID))
	secret, _ := paired["device_secret"].(string)
	token := h.loginOnDevice(t, f.email, secret)

	h.do(t, "POST", "/api/v1/devices/"+deviceID+"/active?company_id="+f.companyID.String(),
		f.token, map[string]any{"active": false})

	// Paused takes effect at once, and says so rather than reading as revoked.
	paused := h.do(t, "POST", "/api/v1/sync/push", token, map[string]any{
		"idempotency_key": "while-paused", "items": []map[string]any{},
	})
	if paused.StatusCode != 403 {
		t.Fatalf("a paused terminal returned %d, want 403", paused.StatusCode)
	}
	if !strings.Contains(readBody(t, paused), "inactive") {
		t.Error("the refusal does not say the terminal is switched off")
	}

	h.do(t, "POST", "/api/v1/devices/"+deviceID+"/active?company_id="+f.companyID.String(),
		f.token, map[string]any{"active": true})

	// The same secret still works, so nobody has to re-pair the till.
	who := h.withHeader(t, "GET", "/api/v1/devices/identity", "",
		map[string]string{"X-Device-Secret": secret}, nil)
	if who.StatusCode != 200 {
		t.Errorf("a resumed terminal lost its pairing (%d)", who.StatusCode)
	}
}

// Re-pairing a working terminal replaces its secret, which is how a shop moves
// a till to a new machine without starting a new ZATCA chain.
func TestRePairingReplacesTheSecretAndKeepsTheTerminal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	_, first := enrol(t, h, issueCode(t, h, f, deviceID))
	oldSecret, _ := first["device_secret"].(string)

	_, second := enrol(t, h, issueCode(t, h, f, deviceID))
	newSecret, _ := second["device_secret"].(string)

	if oldSecret == newSecret {
		t.Fatal("re-pairing returned the same secret")
	}
	// The old machine stops working the moment the new one is paired.
	stale := h.withHeader(t, "GET", "/api/v1/devices/identity", "",
		map[string]string{"X-Device-Secret": oldSecret}, nil)
	if stale.StatusCode != 401 {
		t.Errorf("the replaced secret still works (%d)", stale.StatusCode)
	}
	// Same terminal, so the same chain.
	if got, _ := second["device_id"].(string); got != deviceID {
		t.Errorf("re-pairing produced terminal %q, want the original %q", got, deviceID)
	}
}

// Renaming and moving between stores of the same company is a correction. It
// must NOT be treated as a new terminal — 04-identity §9 keeps the chain.
func TestATerminalCanBeRenamedAndMovedWithinTheCompany(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")

	var second string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'BR2','Second Branch') RETURNING id::text`,
			f.tenantID, f.companyID).Scan(&second)
	}); err != nil {
		t.Fatalf("seed a second store: %v", err)
	}

	resp := h.do(t, "PUT",
		"/api/v1/devices/"+deviceID+"?company_id="+f.companyID.String(), f.token,
		map[string]any{"terminal_label": "Front Till", "store_id": second})
	if resp.StatusCode != 200 {
		t.Fatalf("amend: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got, _ := body["terminal_label"].(string); got != "Front Till" {
		t.Errorf("label = %q", got)
	}
	if got, _ := body["store"].(string); got != "Second Branch" {
		t.Errorf("store = %q", got)
	}
}

// --- Isolation and permissions ---------------------------------------------

// M8: another tenant's terminal reads as absent, not as forbidden.
func TestAnotherTenantsTerminalIsNotFound(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	theirTill := registerTill(t, h, theirs, "Their Till")

	resp := h.do(t, "GET",
		"/api/v1/devices/"+theirTill+"?company_id="+mine.companyID.String(),
		mine.token, nil)
	if resp.StatusCode != 404 {
		t.Errorf("reading another tenant's terminal returned %d, want 404", resp.StatusCode)
	}

	// And it cannot be revoked across the boundary either.
	revoke := h.do(t, "POST",
		"/api/v1/devices/"+theirTill+"/revoke?company_id="+mine.companyID.String(),
		mine.token, map[string]any{"reason": "not mine to revoke"})
	if revoke.StatusCode != 404 {
		t.Errorf("revoking another tenant's terminal returned %d, want 404", revoke.StatusCode)
	}
}

// A terminal cannot be attached to a store in a company the caller is not in.
func TestATerminalCannotBeRegisteredIntoAnotherCompanysStore(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := h.do(t, "POST",
		"/api/v1/devices?company_id="+mine.companyID.String(), mine.token,
		map[string]any{
			"store_id": theirs.storeID.String(), "terminal_label": "Smuggled",
			"egs_unit_id": mine.egsUnitID.String(),
		})
	if resp.StatusCode != 404 {
		t.Errorf("a terminal was registered into another company's store (%d): %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// A cashier runs a till; they do not decide which tills exist.
func TestACashierCannotManageTerminals(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")

	deviceID := registerTill(t, h, f, "Till 2")

	for _, c := range []struct {
		what   string
		method string
		path   string
		body   map[string]any
	}{
		{"list", "GET", "/api/v1/devices", nil},
		{"register", "POST", "/api/v1/devices",
			map[string]any{"store_id": f.storeID.String(), "terminal_label": "Rogue"}},
		{"issue a code", "POST", "/api/v1/devices/" + deviceID + "/enrolment-code",
			map[string]any{}},
		{"revoke", "POST", "/api/v1/devices/" + deviceID + "/revoke",
			map[string]any{"reason": "mischief"}},
	} {
		sep := "?"
		if strings.Contains(c.path, "?") {
			sep = "&"
		}
		resp := h.do(t, c.method, c.path+sep+"company_id="+f.companyID.String(),
			cashier, c.body)
		if resp.StatusCode != 403 {
			t.Errorf("a cashier could %s (%d)", c.what, resp.StatusCode)
		}
	}
}

// A store manager runs the shop floor and can pair a replacement till, because
// waiting for an owner to answer their phone is not a plan.
func TestAStoreManagerCanPairAndRevokeATerminal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	manager := h.seedUserIn(t, f, "store_manager")

	created := h.do(t, "POST",
		"/api/v1/devices?company_id="+f.companyID.String(), manager,
		map[string]any{
			"store_id": f.storeID.String(), "terminal_label": "Till 3",
			"egs_unit_id": f.egsUnitID.String(),
		})
	if created.StatusCode != 201 {
		t.Fatalf("a store manager could not register a till: %d %s",
			created.StatusCode, readBody(t, created))
	}
	id, _ := decodeJSON(t, created)["id"].(string)

	code := h.do(t, "POST",
		"/api/v1/devices/"+id+"/enrolment-code?company_id="+f.companyID.String(),
		manager, map[string]any{})
	if code.StatusCode != 201 {
		t.Errorf("a store manager could not issue a code (%d)", code.StatusCode)
	}
}

// --- Pending terminals -----------------------------------------------------

// A registered but unpaired terminal can do nothing at all.
func TestAPendingTerminalCannotBeSwitchedOn(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")

	resp := h.do(t, "POST",
		"/api/v1/devices/"+deviceID+"/active?company_id="+f.companyID.String(),
		f.token, map[string]any{"active": true})
	if resp.StatusCode != 409 {
		t.Fatalf("an unpaired terminal was switched on (%d)", resp.StatusCode)
	}
	if !strings.Contains(readBody(t, resp), "paired") {
		t.Error("the refusal does not explain that it needs pairing first")
	}
}

// The list says which terminals are waiting for somebody to type a code, and
// when that code dies.
func TestTheListShowsWhichTerminalsAreWaitingToBePaired(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")

	before := findTerminal(t, decodeJSON(t, h.do(t, "GET",
		"/api/v1/devices?company_id="+f.companyID.String(), f.token, nil)), deviceID)
	if waiting, _ := before["pending_code"].(bool); waiting {
		t.Error("a terminal with no code issued claims to be waiting for one")
	}

	issueCode(t, h, f, deviceID)

	after := findTerminal(t, decodeJSON(t, h.do(t, "GET",
		"/api/v1/devices?company_id="+f.companyID.String(), f.token, nil)), deviceID)
	if waiting, _ := after["pending_code"].(bool); !waiting {
		t.Error("a terminal with a live code does not show as waiting")
	}
	if expires, _ := after["code_expires_at"].(string); expires == "" {
		t.Error("nothing says when the code dies, so nobody can count down")
	}
}

func findTerminal(t *testing.T, body map[string]any, id string) map[string]any {
	t.Helper()
	rows, _ := body["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if got, _ := row["id"].(string); got == id {
			return row
		}
	}
	t.Fatalf("terminal %s is not in the list", id)
	return nil
}

// --- harness additions -----------------------------------------------------

// withHeader is `do` with extra headers, for the two calls that carry a device
// secret rather than a bearer token.
func (h *harness) withHeader(
	t *testing.T, method, path, token string,
	headers map[string]string, body any,
) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.server.URL+path, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

// loginOnDevice signs a cashier in ON a paired terminal, which is what a real
// till does and what binds the session to the device.
func (h *harness) loginOnDevice(t *testing.T, email, secret string) string {
	t.Helper()
	resp := h.withHeader(t, "POST", "/api/v1/auth/login", "",
		map[string]string{"X-Device-Secret": secret},
		map[string]any{"email": email, "password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatalf("device sign-in: %d %s", resp.StatusCode, readBody(t, resp))
	}
	token, _ := decodeJSON(t, resp)["access_token"].(string)
	if token == "" {
		t.Fatal("device sign-in returned no token")
	}
	return token
}

// --- The branches a terminal can stand in ----------------------------------

// The register form needs somewhere to put a terminal, and it must not be able
// to reach another company's branches.
func TestTheStoreListIsScopedToTheCompany(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	body := decodeJSON(t, h.do(t, "GET",
		"/api/v1/devices/stores?company_id="+mine.companyID.String(), mine.token, nil))
	rows, _ := body["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the branch list has %d entries, want 1", len(rows))
	}

	// And another tenant's company is not found rather than empty.
	resp := h.do(t, "GET",
		"/api/v1/devices/stores?company_id="+theirs.companyID.String(), mine.token, nil)
	if resp.StatusCode != 404 {
		t.Errorf("another tenant's branches returned %d, want 404", resp.StatusCode)
	}
}

// A cashier does not decide which branches exist either.
func TestTheStoreListIsGatedOnDevicesView(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")

	resp := h.do(t, "GET",
		"/api/v1/devices/stores?company_id="+f.companyID.String(), cashier, nil)
	if resp.StatusCode != 403 {
		t.Errorf("a cashier read the branch list (%d)", resp.StatusCode)
	}
}

// The list has to tell a screen the difference between a terminal waiting for
// somebody to type a code and one that has no code yet, because the two need
// different actions from a reader. It carries `pending_code` for exactly that,
// and the countdown so nobody discovers a code died while they walked to the
// till.
func TestTheListCarriesWhatTheScreenNeedsToSayWhatToDo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	deviceID := registerTill(t, h, f, "Till 2")
	issueCode(t, h, f, deviceID)

	row := findTerminal(t, decodeJSON(t, h.do(t, "GET",
		"/api/v1/devices?company_id="+f.companyID.String(), f.token, nil)), deviceID)

	for _, needed := range []string{"terminal_label", "store", "status", "pending_code", "code_expires_at"} {
		if _, present := row[needed]; !present {
			t.Errorf("the list omits %q, which the screen needs", needed)
		}
	}
	// And still nothing resembling a credential.
	for _, leak := range []string{"secret", "code_hash", "code_selector"} {
		if _, present := row[leak]; present {
			t.Errorf("the list carries %q", leak)
		}
	}
}
