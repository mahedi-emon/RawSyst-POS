//go:build integration

// Store credit stops being spendable when it lapses (blueprint B16).
//
// `store_credit_entry.expires_on` exists so a shop's credit liability does not
// grow for ever, and `wallet.ExpireCredit` was written to act on it. Nothing
// ever called it: no screen, no handler, no schedule — the same defect as the
// reservation sweep beside it, and the same consequence. The deadline was
// recorded and never arrived.
//
// So a credit note issued with a twelve-month expiry stayed spendable in year
// three, and the liability on the balance sheet was a figure nobody could
// retire: the books said the shop owed its customers more than it did, for
// ever, and the number only went up.
package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
)

// seedChart gives the shop the chart of accounts a posting needs.
func seedChart(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(
			t.Context(), tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed chart: %v", err)
	}
}

// creditWithExpiry gives a customer credit that lapses on a given day.
//
// `days` is negative for credit that has already lapsed, which is the case
// under test: credit expiring next year must survive the sweep.
func creditWithExpiry(
	t *testing.T, h *harness, f *shopFixture, name, amount string, days int,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO customer (tenant_id, company_id, code, name)
			VALUES ($1,$2,$3,$4) RETURNING id`,
			f.tenantID, f.companyID, "C"+uuid.NewString()[:8], name).
			Scan(&id); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			INSERT INTO store_credit_entry
			  (tenant_id, company_id, customer_id, amount, currency, reason,
			   expires_on)
			VALUES ($1,$2,$3,$4,
			        (SELECT base_currency FROM company WHERE id = $2),
			        'issued', current_date + ($5::int))`,
			f.tenantID, f.companyID, id, decimal.RequireFromString(amount), days)
		return e
	}); err != nil {
		t.Fatalf("credit %s: %v", name, err)
	}
	return id
}

// balanceOf is what the customer can still spend.
func balanceOf(t *testing.T, h *harness, f *shopFixture, customerID uuid.UUID) decimal.Decimal {
	t.Helper()
	var out decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(amount), 0) FROM store_credit_entry
			WHERE customer_id = $1`, customerID).Scan(&out)
	}); err != nil {
		t.Fatalf("read the balance: %v", err)
	}
	return out
}

// Lapsed credit is retired; credit still in date is left alone.
func TestTheSweepRetiresLapsedCreditAndLeavesTheRestAlone(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	// The full chart, for its Store Credit Liability account: expiring credit
	// RELEASES a liability to income, so a shop with no chart cannot expire
	// anything and the refusal says exactly that.
	seedChart(t, h, f)

	lapsed := creditWithExpiry(t, h, f, "Lapsed", "120.00", -30)
	live := creditWithExpiry(t, h, f, "Still good", "80.00", 200)

	tenant := f.tenantID
	sweeper := jobs.NewCreditExpirySweeper(h.pool, wallet.NewService(h.pool))
	if err := sweeper.Run(context.Background(), jobs.Job{
		TenantID: &tenant, Kind: jobs.KindCreditExpirySweep,
	}); err != nil {
		t.Fatalf("credit expiry sweep: %v", err)
	}

	if got := balanceOf(t, h, f, lapsed); !got.IsZero() {
		t.Errorf("lapsed credit still spendable: %s left, want 0", got)
	}
	if got := balanceOf(t, h, f, live); !got.Equal(decimal.RequireFromString("80")) {
		t.Errorf("credit in date was retired too: %s left, want 80", got)
	}
}

// Running it twice retires nothing a second time.
//
// The sweep is scheduled daily, so it runs against the same lapsed credit
// again tomorrow. A second write-back would post the release twice and take
// the balance negative.
func TestSweepingTwiceRetiresTheCreditOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedChart(t, h, f)
	lapsed := creditWithExpiry(t, h, f, "Lapsed", "50.00", -1)

	tenant := f.tenantID
	sweeper := jobs.NewCreditExpirySweeper(h.pool, wallet.NewService(h.pool))
	for i := range 2 {
		if err := sweeper.Run(context.Background(), jobs.Job{
			TenantID: &tenant, Kind: jobs.KindCreditExpirySweep,
		}); err != nil {
			t.Fatalf("sweep %d: %v", i+1, err)
		}
	}

	if got := balanceOf(t, h, f, lapsed); !got.IsZero() {
		t.Errorf("after two sweeps the balance is %s, want 0 — a second "+
			"write-back would take it negative", got)
	}
}

// A sweep with no tenant is a permanent failure, not a retry forever.
func TestACreditSweepWithNoTenantDoesNotRetry(t *testing.T) {
	h := newHarness(t)
	sweeper := jobs.NewCreditExpirySweeper(h.pool, wallet.NewService(h.pool))
	err := sweeper.Run(context.Background(), jobs.Job{Kind: jobs.KindCreditExpirySweep})
	if err == nil {
		t.Fatal("a sweep naming no tenant succeeded")
	}
	if _, ok := err.(jobs.Permanent); !ok {
		t.Errorf("error is %T, want a jobs.Permanent so it is not retried: %v",
			err, err)
	}
}
