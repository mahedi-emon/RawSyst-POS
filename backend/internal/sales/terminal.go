package sales

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// WithPool gives the service a connection pool so it can own the transaction
// boundary.
//
// The boundary belongs here rather than in an HTTP handler. A sale is atomic
// across stock, chain, invoice and journal (C9.1), and if handlers opened
// transactions then every future caller — a sync worker, a scheduled job, an
// import — would have to remember to wrap the call correctly. One of them
// eventually would not.
func (s *Service) WithPool(pool *db.Pool) *Service {
	s.pool = pool
	return s
}

// RingUp finalises a sale in its own transaction.
//
// The tax rate and the currency are resolved HERE, not sent by the till. Two
// reasons, and both are correctness rather than tidiness.
//
// A till that stated its own VAT rate would be the system's authority on a
// legal value, which is exactly what the Regulatory Rule Registry exists to
// prevent — and a terminal running last year's build would quietly keep
// charging last year's rate. The registry resolves the rate at the TRANSACTION
// date (E8.1), so an offline sale that syncs a week later still uses the rate
// that applied when it was rung up.
//
// The currency comes from the company because the books are kept in it. A till
// naming a currency the company does not trade in would produce a journal that
// balances in one currency and not the other.
func (s *Service) RingUp(
	ctx context.Context, tenantID, deviceID uuid.UUID, sale Sale, warehouseID uuid.UUID,
) (Finalized, error) {
	if s.pool == nil {
		return Finalized{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	var out Finalized
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		term, profile, e := resolveTerminal(ctx, tx, tenantID, deviceID, warehouseID)
		if e != nil {
			return e
		}
		if e = s.applyTaxProfile(ctx, &sale, profile, tenantID); e != nil {
			return e
		}
		out, e = s.Finalize(ctx, tx, term, sale)
		return e
	})
	return out, err
}

// companyProfile is what a sale needs to know about the legal entity behind the
// till.
type companyProfile struct {
	country      string
	baseCurrency string
}

// applyTaxProfile fills in the values the till is not allowed to choose.
func (s *Service) applyTaxProfile(
	ctx context.Context, sale *Sale, profile companyProfile, tenantID uuid.UUID,
) error {
	if s.rules == nil {
		return errs.New(errs.CodeInternal,
			"The sales service was built without the regulatory rule registry.")
	}

	rules, err := catalog.TaxRulesFor(ctx, s.rules, profile.country, sale.IssuedAt, tenantID)
	if err != nil {
		return err
	}
	rate, err := s.rules.VATRate(ctx, profile.country, sale.IssuedAt, tenantID)
	if err != nil {
		return err
	}

	sale.Input.Rules = rules
	sale.Input.TaxRate = rate
	sale.Currency = profile.baseCurrency
	return nil
}

// Refund processes a return in its own transaction.
func (s *Service) Refund(
	ctx context.Context, tenantID, deviceID uuid.UUID, ret Return, warehouseID uuid.UUID,
) (Refunded, error) {
	if s.pool == nil {
		return Refunded{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	var out Refunded
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		term, _, e := resolveTerminal(ctx, tx, tenantID, deviceID, warehouseID)
		if e != nil {
			return e
		}
		out, e = s.ProcessReturn(ctx, tx, term, ret)
		return e
	})
	return out, err
}

// resolveTerminal works out where a sale is happening from the DEVICE, never
// from the request body.
//
// This is an authorization boundary, not a convenience. If a till could name
// its own company, store, EGS unit or warehouse, then a compromised or merely
// misconfigured terminal could sell another branch's stock, post into another
// company's books, or — worst — take a position on another terminal's ZATCA
// chain. Row-level security would not catch any of it, because all of those
// rows belong to the same tenant.
//
// So the only thing the caller may choose is which of ITS OWN store's
// warehouses to sell from, and even that is checked.
func resolveTerminal(
	ctx context.Context, tx pgx.Tx, tenantID, deviceID, warehouseID uuid.UUID,
) (Terminal, companyProfile, error) {
	if deviceID == uuid.Nil {
		return Terminal{}, companyProfile{}, errs.New(errs.CodeForbidden,
			"A sale must be rung up on a registered terminal. This session is not "+
				"bound to one.")
	}

	term := Terminal{TenantID: tenantID, DeviceID: &deviceID}
	var egsUnitID *uuid.UUID
	var status string

	var profile companyProfile
	err := tx.QueryRow(ctx, `
		SELECT d.company_id, d.store_id, d.egs_unit_id, d.status::text,
		       c.country, c.base_currency
		FROM device d JOIN company c ON c.id = d.company_id
		WHERE d.id = $1`, deviceID).
		Scan(&term.CompanyID, &term.StoreID, &egsUnitID, &status,
			&profile.country, &profile.baseCurrency)

	if errors.Is(err, pgx.ErrNoRows) {
		// Another tenant's device reads as absent under row-level security,
		// which is the right answer: its existence is not this caller's
		// business.
		return Terminal{}, companyProfile{}, errs.New(errs.CodeNotFound,
			"That terminal is not registered.")
	}
	if err != nil {
		return Terminal{}, companyProfile{}, err
	}

	if status != "active" {
		return Terminal{}, companyProfile{}, errs.Newf(errs.CodeForbidden,
			"This terminal is %s, so it cannot ring up sales. An owner can "+
				"activate it under Settings > Terminals.", status)
	}

	if egsUnitID == nil {
		// Without an EGS unit there is no counter and no hash chain, so the
		// invoice could not be a legal document. Better to refuse the sale than
		// to issue something that cannot be reported.
		return Terminal{}, companyProfile{}, errs.New(errs.CodeComplianceBlocked,
			"This terminal has not been onboarded for e-invoicing yet, so it "+
				"cannot issue invoices. Complete onboarding under Settings > "+
				"E-invoicing.")
	}
	term.EGSUnitID = *egsUnitID

	term.WarehouseID, err = resolveWarehouse(ctx, tx, term.StoreID, warehouseID)
	if err != nil {
		return Terminal{}, companyProfile{}, err
	}

	// Every sale belongs to a shift. A till with no open session has no drawer
	// anyone has counted into, so its takings could never be reconciled — and a
	// cash difference discovered later could not be attributed to anyone.
	var sessionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM cash_session WHERE device_id = $1 AND state = 'open'`,
		deviceID).Scan(&sessionID)

	if errors.Is(err, pgx.ErrNoRows) {
		return Terminal{}, companyProfile{}, errs.New(errs.CodeConflict,
			"This till has no open session. Count the drawer and open a session "+
				"before ringing up sales.")
	}
	if err != nil {
		return Terminal{}, companyProfile{}, err
	}
	term.CashSessionID = &sessionID

	return term, profile, nil
}

// resolveWarehouse picks the stock this till sells from.
//
// A named warehouse is verified against the device's own store. A shop with one
// stockroom should not have to configure anything, so a single warehouse is
// chosen automatically; more than one is ambiguous and the till must say which,
// because guessing would silently sell from the wrong place.
func resolveWarehouse(
	ctx context.Context, tx pgx.Tx, storeID, requested uuid.UUID,
) (uuid.UUID, error) {
	if requested != uuid.Nil {
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM warehouse
				WHERE id = $1 AND is_active AND (store_id = $2 OR store_id IS NULL))`,
			requested, storeID).Scan(&ok); err != nil {
			return uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, errs.New(errs.CodeNotFound,
				"That stock location does not belong to this branch.")
		}
		return requested, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT id FROM warehouse
		WHERE is_active AND (store_id = $1 OR store_id IS NULL)
		ORDER BY store_id NULLS LAST, code`, storeID)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()

	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, err
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}

	switch len(found) {
	case 0:
		return uuid.Nil, errs.New(errs.CodeConflict,
			"This branch has no stock location set up, so there is nothing to "+
				"sell from. An owner can add one under Settings > Stock locations.")
	case 1:
		return found[0], nil
	default:
		return uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This branch has more than one stock location, so the sale must say "+
				"which one it is selling from.")
	}
}
