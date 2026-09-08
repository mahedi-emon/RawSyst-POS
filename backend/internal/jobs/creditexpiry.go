// Expiring store credit and gift-card balances (blueprint B16).
//
// `store_credit_entry.expires_on` exists so a shop's credit liability does not
// grow for ever, and `wallet.ExpireCredit` was written to act on it: it finds
// every balance past its date, writes the remainder back out and posts the
// release through the accounting rule. `POST /store-credit/expire` exposes it.
//
// Nothing ever called any of that. No screen, no handler, no schedule — the
// same shape of defect as the reservation sweep beside it, and with the same
// consequence: the deadline was recorded and never arrived.
//
// What that means for a shop is money. A credit note issued with a twelve-month
// expiry stays spendable in year three, and the liability on the balance sheet
// is a number nobody can retire — so the books say the shop owes customers
// more than it does, for ever, and the figure only ever goes up.
package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
)

// KindCreditExpirySweep is the scheduled release of lapsed store credit.
const KindCreditExpirySweep = "wallet.credit_expiry_sweep"

// CreditExpirySweeper retires balances whose date has passed.
//
// In tenant context, like the low-stock, batch-expiry and reservation sweeps,
// and for the same reason: row-level security is what stops one tenant's sweep
// touching another's balances, and a job running as the platform would give
// that up.
type CreditExpirySweeper struct {
	pool   *db.Pool
	wallet *wallet.Service
}

// NewCreditExpirySweeper builds the handler.
func NewCreditExpirySweeper(pool *db.Pool, w *wallet.Service) *CreditExpirySweeper {
	return &CreditExpirySweeper{pool: pool, wallet: w}
}

// Run retires lapsed credit for every company in the tenant.
//
// Per company rather than in one statement, because expiring credit POSTS —
// the liability is released to income through the accounting rule — and a bulk
// UPDATE here would be a second implementation of a rule that already has one.
func (s *CreditExpirySweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This credit-expiry sweep names no tenant.")}
	}
	tenantID := *j.TenantID

	var companies []uuid.UUID
	if err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id FROM company`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				return e
			}
			companies = append(companies, id)
		}
		return rows.Err()
	}); err != nil {
		return db.Translate(err, "")
	}

	for _, companyID := range companies {
		if _, err := s.wallet.ExpireCredit(ctx, wallet.Scope{
			TenantID: tenantID, CompanyID: companyID,
		}); err != nil {
			return err
		}
	}
	return nil
}
