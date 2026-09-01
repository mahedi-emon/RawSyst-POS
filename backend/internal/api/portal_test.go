//go:build integration

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
)

// The portals are the only place in this product where somebody who does not
// work for the shop holds a session, so the guarantees are worth stating as
// tests rather than as comments.
//
// Three things have to hold:
//
//   - a portal session is not a staff session, and cannot be used as one;
//   - a portal session for one shop reads nothing from another;
//   - asking for a sign-in code answers the same way whether or not the number
//     is on file, so the portal cannot be used to ask a shop who its customers
//     are.

// portalURL builds a portal path with the tenant and company the portal is
// against, which the routes take in the query string.
func portalURL(f *shopFixture, path string) string {
	return path + "?tenant_id=" + f.tenantID.String() +
		"&company_id=" + f.companyID.String()
}

// signInAsCustomer gets a portal token for a customer of this shop.
//
// The code is never returned by the API — it goes to the queue — so the test
// reads the hash's row and re-issues a known code directly. That is the only
// way to sign in without a message provider, and it exercises the exchange
// exactly as a customer's browser would.
func signInAsCustomer(
	t *testing.T, h *harness, f *shopFixture, phone string,
) string {
	t.Helper()
	ctx := context.Background()

	// A code with a hash the test knows the plaintext of.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		hash, e := hashForTest("424242")
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `
			INSERT INTO customer_portal_code (
			  tenant_id, company_id, phone, code_hash, expires_at)
			VALUES ($1,$2,$3,$4, now() + interval '10 minutes')`,
			f.tenantID, f.companyID, phone, hash)
		return e
	}); err != nil {
		t.Fatalf("seeding a portal code: %v", err)
	}

	resp := h.do(t, http.MethodPost,
		portalURL(f, "/api/v1/portal/session"), "",
		map[string]any{"phone": phone, "code": "424242"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("portal sign-in: %s", readBody(t, resp))
	}
	token, _ := decodeJSON(t, resp)["token"].(string)
	if token == "" {
		t.Fatal("portal sign-in returned no token")
	}
	return token
}

func TestAPortalSessionIsNotAStaffSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	phone := "0500001234"
	seedPortalCustomer(t, h, f, phone, "Portal Customer")
	token := signInAsCustomer(t, h, f, phone)

	// Every one of these is an ordinary back-office read that a staff token
	// opens. A portal token must open none of them: the middleware that reads
	// a staff token does not know how to read a portal one, so the token is
	// simply not a credential here.
	for _, path := range []string{
		"/api/v1/customers?company_id=" + f.companyID.String(),
		"/api/v1/catalog/products?company_id=" + f.companyID.String(),
		"/api/v1/reports/trial-balance?company_id=" + f.companyID.String() +
			"&as_of=2026-08-31",
		"/api/v1/people?company_id=" + f.companyID.String(),
	} {
		resp := h.do(t, http.MethodGet, path, token, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s answered %d to a portal token; expected 401",
				path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestAPortalSessionReadsNothingFromAnotherShop(t *testing.T) {
	h := newHarness(t)

	mine := h.seedShop(t, "owner")
	other := h.seedShop(t, "owner")

	phone := "0500005678"
	seedPortalCustomer(t, h, mine, phone, "My Customer")
	token := signInAsCustomer(t, h, mine, phone)

	// The same token, aimed at the other shop. The session was issued against
	// one company and Authenticate only matches a session whose portal user
	// belongs to the company being asked about, so naming another produces no
	// session rather than somebody else's data.
	for _, path := range []string{
		"/api/v1/portal/me",
		"/api/v1/portal/invoices",
		"/api/v1/portal/orders",
		"/api/v1/portal/addresses",
		"/api/v1/portal/returns",
	} {
		resp := h.do(t, http.MethodGet, portalURL(other, path), token, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s aimed at another shop answered %d; expected 401",
				path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// And the positive control, so a portal that refused EVERYTHING would not
	// pass the test above by being broken.
	resp := h.do(t, http.MethodGet, portalURL(mine, "/api/v1/portal/me"),
		token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the caller's own portal home: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	me, _ := body["me"].(map[string]any)
	if me == nil || me["name"] != "My Customer" {
		t.Fatalf("the portal did not return the caller's own account: %v", body)
	}
}

func TestAskingForACodeDoesNotSayWhoIsACustomer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedPortalCustomer(t, h, f, "0500009999", "Known Customer")

	// A number the shop has, and one it does not. The two answers must be
	// indistinguishable: anything else is a way to ask a shop whether a
	// particular person shops there.
	known := h.do(t, http.MethodPost, portalURL(f, "/api/v1/portal/code"), "",
		map[string]any{"phone": "0500009999"})
	knownBody := readBody(t, known)

	unknown := h.do(t, http.MethodPost, portalURL(f, "/api/v1/portal/code"), "",
		map[string]any{"phone": "0500000000"})
	unknownBody := readBody(t, unknown)

	if known.StatusCode != unknown.StatusCode {
		t.Errorf("a known number answered %d and an unknown one %d",
			known.StatusCode, unknown.StatusCode)
	}
	if knownBody != unknownBody {
		t.Errorf("the two answers differ:\n known: %s\n other: %s",
			knownBody, unknownBody)
	}
}

func TestAWrongCodeCountsAgainstTheAttemptsEvenThoughItFails(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := context.Background()

	phone := "0500007777"
	seedPortalCustomer(t, h, f, phone, "Guessed At")

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		hash, e := hashForTest("111111")
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `
			INSERT INTO customer_portal_code (
			  tenant_id, company_id, phone, code_hash, expires_at)
			VALUES ($1,$2,$3,$4, now() + interval '10 minutes')`,
			f.tenantID, f.companyID, phone, hash)
		return e
	}); err != nil {
		t.Fatalf("seeding a portal code: %v", err)
	}

	// A wrong guess. The exchange fails, and the attempt must survive the
	// failure: a guesser who could roll back their own attempts by guessing
	// wrong would have unlimited guesses.
	resp := h.do(t, http.MethodPost, portalURL(f, "/api/v1/portal/session"), "",
		map[string]any{"phone": phone, "code": "999999"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong code answered %d; expected 401", resp.StatusCode)
	}
	resp.Body.Close()

	var attempts int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT attempts FROM customer_portal_code
			 WHERE company_id = $1 AND phone = $2`,
			f.companyID, phone).Scan(&attempts)
	}); err != nil {
		t.Fatalf("reading the attempt counter: %v", err)
	}
	if attempts != 1 {
		t.Errorf("the wrong guess left %d attempts recorded; expected 1",
			attempts)
	}
}

// seedPortalCustomer puts a customer with a known phone number on the books.
func seedPortalCustomer(
	t *testing.T, h *harness, f *shopFixture, phone, name string,
) {
	t.Helper()
	ctx := context.Background()
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO customer (tenant_id, company_id, code, name, phone)
			VALUES ($1, $2, 'PORTAL-' || substr(md5(random()::text), 1, 8),
			        $3, $4)`,
			f.tenantID, f.companyID, name, phone)
		return e
	}); err != nil {
		t.Fatalf("seeding a portal customer: %v", err)
	}
}

// hashForTest hashes a code the way the portal does, so a test can plant one.
func hashForTest(code string) (string, error) {
	return identity.HashSecret(code)
}
