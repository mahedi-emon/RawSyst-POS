//go:build integration

package api

// A shift belongs to the day the SHOP was having, not the day UTC was having.
//
// The register bounded its default fortnight with instants computed in UTC and
// then compared them against a date the database resolved in another timezone.
// The two disagreed for the last hours of every local day: in Riyadh a session
// opened after nine in the evening was missing from the register that exists
// to review it, and in Dhaka anything after six. A supervisor arriving in the
// morning would find last night's till simply absent.
//
// This runs the same assertion in four timezones spread across the world, so
// no wall clock the suite happens to run at can make it pass by luck.

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAShiftOpenedNowAppearsInEveryTimezone(t *testing.T) {
	for _, tz := range []string{
		"Pacific/Kiritimati", // +14, the furthest ahead there is
		"Asia/Riyadh",        // +3, the product's home market
		"UTC",
		"America/Los_Angeles", // -8, the other side
	} {
		t.Run(tz, func(t *testing.T) {
			h := newHarness(t)
			f := h.seedShop(t, "owner")

			ctx := context.Background()
			if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
				_, e := tx.Exec(ctx,
					`UPDATE company SET timezone = $2 WHERE id = $1`,
					f.companyID, tz)
				return e
			}); err != nil {
				t.Fatalf("set the shop's timezone: %v", err)
			}

			// No from or to: the default window is the one that was wrong.
			resp := h.do(t, http.MethodGet,
				"/api/v1/shifts?company_id="+f.companyID.String(), f.token, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the shift register answered %d: %s",
					resp.StatusCode, readBody(t, resp))
			}
			rows, _ := decodeJSONFrom(t, resp)["data"].([]any)
			if len(rows) == 0 {
				t.Fatalf("a till in %s opened a session a moment ago and the "+
					"register reports none: the window is being computed in a "+
					"timezone that is not the shop's", tz)
			}
		})
	}
}

func TestAShiftRegisterWindowStillNarrows(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// A window that ended before the shop opened its drawer excludes it. The
	// fix must not have turned the bounds into decoration.
	resp := h.do(t, http.MethodGet,
		"/api/v1/shifts?company_id="+f.companyID.String()+
			"&from=2020-01-01&to=2020-01-31", f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shift register answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSONFrom(t, resp)["data"].([]any)
	if len(rows) != 0 {
		t.Errorf("a window in 2020 returned %d shift(s) opened today", len(rows))
	}

	// And an end before the start is still refused rather than quietly
	// returning nothing.
	bad := h.do(t, http.MethodGet,
		"/api/v1/shifts?company_id="+f.companyID.String()+
			"&from=2026-02-01&to=2026-01-01", f.token, nil)
	defer bad.Body.Close()
	if bad.StatusCode < 400 || bad.StatusCode >= 500 {
		t.Errorf("a period ending before it starts = %d, want a refusal",
			bad.StatusCode)
	}
}
