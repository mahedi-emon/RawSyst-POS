//go:build integration

// Every entry in the trail names who made it.
//
// `actor_label` is denormalised on purpose: it has to survive the user row
// being deleted, so the trail does not become a list of missing people. The
// audit package's own doc comment says that is why it exists, and says the
// defect it was written to stop — "the third one already omitted actor_label".
//
// Four entries in `internal/identity` were doing exactly that: signing in,
// changing a password, a Super Admin resetting one, and a refresh token
// presented twice. The last two are the security events an incident review
// reads first, and both arrived with the name blank.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestTheTrailNamesWhoActed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := context.Background()

	// Signing in is the entry every account produces, so it is the one that
	// would be missing a name on every row in the table.
	resp := h.do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email":    f.email,
		"password": testPassword,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign in answered %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var labelled, total int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FILTER (
			         WHERE btrim(coalesce(actor_label, '')) <> ''),
			       count(*)
			FROM audit_log
			WHERE action = 'login' AND actor_id = $1`, f.userID).
			Scan(&labelled, &total)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}

	if total == 0 {
		t.Fatal("signing in wrote nothing to the audit log")
	}
	if labelled != total {
		t.Errorf("%d of %d sign-in entries name nobody. `actor_label` is "+
			"denormalised precisely so the trail survives the user row being "+
			"deleted, and a log with the name missing on some rows is a log "+
			"somebody has to apologise for in an audit", total-labelled, total)
	}
}

// A password change is the entry an incident review looks for, and it was
// arriving blank.
func TestChangingAPasswordNamesTheActor(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := context.Background()

	resp := h.do(t, http.MethodPost, "/api/v1/auth/change-password", f.token,
		map[string]any{
			"current_password": testPassword,
			"new_password":     "Str0ng-Enough-For-Now!2026",
		})
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("change password answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}

	var label string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT coalesce(actor_label, '') FROM audit_log
			WHERE action = 'password_changed' AND actor_id = $1
			ORDER BY occurred_at DESC LIMIT 1`, f.userID).Scan(&label)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if label == "" {
		t.Error("the password-change entry names nobody")
	}
}
