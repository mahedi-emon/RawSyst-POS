package sales

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/market"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
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
		var e error
		out, e = s.FinalizeInTx(ctx, tx, tenantID, deviceID, sale, warehouseID)
		return e
	})
	return out, err
}

// FinalizeInTx is the one authoritative way a sale becomes an invoice.
//
// Both callers reach it: the HTTP handler, which wraps it in its own
// transaction, and the sync engine, which replays an offline sale inside the
// transaction it is already holding for the batch.
//
// That is deliberate and it is the whole point of this function existing. If
// the sync worker built invoices, chain positions, stock movements and journal
// entries itself, there would be two implementations of the most consequential
// operation in the system — and the offline one, which handles the sales nobody
// watched happen, would be the less exercised of the two. Every guarantee
// proved for an online sale holds for a replayed one because it is the same
// code, not because someone kept two copies in step.
func (s *Service) FinalizeInTx(
	ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID,
	sale Sale, warehouseID uuid.UUID,
) (Finalized, error) {
	// The till does not get to name a reserved settlement method, for the same
	// reason it does not get to name the VAT rate or the currency below. This is
	// the choke point for both callers, so a replayed offline sale is refused on
	// the same grounds as a live one.
	if err := checkMethodsAreOfferable(tenderMethods(sale.Tenders)); err != nil {
		return Finalized{}, err
	}

	term, profile, err := resolveTerminal(ctx, tx, tenantID, deviceID, warehouseID)
	if err != nil {
		return Finalized{}, err
	}
	if err := s.applyTaxProfile(ctx, tx, &sale, profile, tenantID); err != nil {
		return Finalized{}, err
	}
	return s.Finalize(ctx, tx, term, sale)
}

// companyProfile is what a sale needs to know about the legal entity behind the
// till.
type companyProfile struct {
	country      string
	baseCurrency string

	// storeID is which shop is selling. Needed because a market that taxes by
	// jurisdiction resolves the rate from the SHOP's jurisdiction, and a
	// company with branches in two cities is taxed differently in each.
	storeID uuid.UUID

	// stockPolicy decides whether a till may sell past what is on hand. It
	// belongs to the COMPANY, not to the request: a till that named its own
	// policy could sell below zero at a shop that had deliberately forbidden
	// it, and the column defaults to block precisely because that is the safe
	// answer for serialised or high-value goods.
	stockPolicy inventory.NegativeStockPolicy
}

// resolveRates finds a rate for every treatment this sale actually uses.
//
// # Only the treatments present
//
// A sale of three standard-rated shirts asks the registry for one rate. It does
// not ask for the reduced rate, so a market that has never recorded one can
// still sell everything it sells today — which is the difference between an
// architecture that supports several rates and one that demands every rate
// exist before anything can be sold.
//
// # A treatment with no rate is refused, not defaulted
//
// `taxable()` accepts `reduced`, and until now a single rate was applied to
// every taxable line — so a reduced-rate line was charged the STANDARD rate.
// That silently overcharges the customer and overstates the tax return. The
// honest answer where no reduced rate has been recorded is to refuse the sale
// and say so, because the alternative is inventing a legal value.
//
// Bangladesh is the live example: its treatment list includes `reduced`, and
// the rate behind it has never been established against the NBR. Selling such a
// line at 15% would be a guess presented as a legal figure.
func (s *Service) resolveRates(
	ctx context.Context, tx pgx.Tx, sale *Sale, rules catalog.TaxRules,
	country string, tenantID, storeID uuid.UUID,
) (map[string]decimal.Decimal, map[string]registry.CombinedRate, error) {
	out := make(map[string]decimal.Decimal, 2)
	// Only treatments resolved through a jurisdiction have shares. A national
	// VAT has one authority and nothing to apportion.
	shares := make(map[string]registry.CombinedRate, 2)

	for _, l := range sale.Input.Lines {
		treatment := l.TaxTreatment
		if _, done := out[treatment]; done {
			continue
		}
		// Not taxable at all: no lookup, and nothing for a market to record.
		// Pricing answers these from the rules rather than from a rate.
		if !taxable(rules, treatment) {
			continue
		}

		rate, err := s.rules.TaxRate(ctx, tx, country, treatment, sale.IssuedAt, tenantID)
		if errs.CodeOf(err) == errs.CodeUnverifiedRule && err != nil {
			// The market does not set this tax nationally — the United States
			// levies it by state, county and city — so it is resolved through
			// the shop's jurisdiction instead. `TaxRate` says so rather than
			// inventing a national rate, and this is where that answer is
			// acted on.
			combined, jErr := s.jurisdictionRate(ctx, tx, storeID, treatment, sale.IssuedAt)
			if jErr != nil {
				// The ORIGINAL refusal is returned when there is no
				// jurisdiction at all: "this tax needs a jurisdiction" tells a
				// shop what to do, where "that jurisdiction is not on file"
				// would be answering a question they did not ask.
				if errs.CodeOf(jErr) == errs.CodeInvalidInput {
					return nil, nil, err
				}
				return nil, nil, jErr
			}
			out[treatment] = combined.Total
			shares[treatment] = combined
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		out[treatment] = rate
	}
	return out, shares, nil
}

// applyTaxProfile fills in the values the till is not allowed to choose.
//
// tx is the sale's own transaction, and the rule lookups below run inside it.
// They must: this function is called with a connection already held, and a
// registry read that asked the pool for a second one would deadlock every till
// in the shop as soon as concurrent sales reached the pool size. See
// registry.Query.Tx.
func (s *Service) applyTaxProfile(
	ctx context.Context, tx pgx.Tx, sale *Sale, profile companyProfile,
	tenantID uuid.UUID,
) error {
	if s.rules == nil {
		return errs.New(errs.CodeInternal,
			"The sales service was built without the regulatory rule registry.")
	}

	rules, err := catalog.TaxRulesFor(ctx, s.rules, tx, profile.country, sale.IssuedAt, tenantID)
	if err != nil {
		return err
	}
	rates, shares, err := s.resolveRates(ctx, tx, sale, rules, profile.country,
		tenantID, profile.storeID)
	if err != nil {
		return err
	}

	sale.Input.Rules = rules
	sale.Input.TaxRates = rates
	sale.TaxShares = shares
	sale.Currency = profile.baseCurrency

	// Read from the company, never from the request. The handler cannot supply
	// this and a till cannot ask for it: a terminal that named its own policy
	// could sell below zero at a shop that had forbidden it.
	sale.StockPolicy = profile.stockPolicy
	return nil
}

// Refund processes a return in its own transaction.
func (s *Service) Refund(
	ctx context.Context, tenantID, deviceID uuid.UUID, ret Return, warehouseID uuid.UUID,
) (Refunded, error) {
	// As on a sale, and before the database is needed: a request that names a
	// method it may not name is refused on its own terms. ProcessExchange
	// reaches ProcessReturn directly with the clearing leg it built itself, so
	// this refuses the method for every return a client can ask for without
	// standing in the way of the one that is real.
	if err := checkMethodsAreOfferable(refundMethods(ret.Refunds)); err != nil {
		return Refunded{}, err
	}

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
		       c.country, c.base_currency, c.negative_stock_policy::text
		FROM device d JOIN company c ON c.id = d.company_id
		WHERE d.id = $1`, deviceID).
		Scan(&term.CompanyID, &term.StoreID, &egsUnitID, &status,
			&profile.country, &profile.baseCurrency, &profile.stockPolicy)

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

	// The chain is a Saudi obligation, not a property of selling.
	//
	// This used to refuse ANY terminal with no EGS unit, in every market. The
	// unit is a ZATCA object — its columns are a CSR: organization identifier,
	// invoice type, industry — and nothing outside Saudi Arabia has one or
	// could fill one in. The effect was that a Bangladeshi or American shop
	// could be provisioned, set up, stocked and paired, and then could not ring
	// up a single item, refused by a message telling it to complete an
	// onboarding that does not apply to it.
	//
	// So the requirement is now asked of the market rather than of every
	// terminal. Where e-invoicing applies the refusal is unchanged and still
	// happens before an ICV is consumed. Where it does not, the terminal sells
	// with `EGSUnitID` left as uuid.Nil and `OnAChain` false, and the whole
	// chain — reserve, document, hash, record, submission queue — is skipped
	// rather than faked. A sale off a chain writes no `zatca_invoice` row,
	// which is exactly what "this invoice is not on a chain" should look like
	// in the data.
	if egsUnitID != nil {
		term.EGSUnitID = *egsUnitID
	} else if market.EInvoicingApplies(profile.country) {
		return Terminal{}, companyProfile{}, errs.New(errs.CodeComplianceBlocked,
			"This terminal has not been onboarded for e-invoicing yet, so it "+
				"cannot issue invoices. Complete onboarding under Settings > "+
				"E-invoicing.")
	}
	term.Country = profile.country
	profile.storeID = term.StoreID

	term.WarehouseID, err = resolveWarehouse(ctx, tx, term.StoreID, warehouseID)
	if err != nil {
		return Terminal{}, companyProfile{}, err
	}

	// Every sale belongs to a shift. A till with no open session has no drawer
	// anyone has counted into, so its takings could never be reconciled — and a
	// cash difference discovered later could not be attributed to anyone.
	//
	// FOR SHARE because "there is an open session" has to still be true when
	// this transaction commits, not merely when it was asked.
	//
	// The Z report takes FOR UPDATE, computes the expected cash and freezes it
	// atomically. Without a lock here a sale already in flight — a slow card
	// authorisation, a batch of offline sales being replayed by sync — reads
	// 'open' from its own snapshot, commits after the close, and attaches its
	// cash tender to a session whose expected figure was fixed without it. The
	// Z report then shows the sale in its takings and not in its expected cash,
	// and the drawer reads over by exactly that sale.
	//
	// FOR SHARE conflicts with FOR UPDATE and with nothing else, so concurrent
	// sales on one till still proceed together. Either the sale commits first
	// and the close counts it, or the close commits first and this row no
	// longer satisfies `state = 'open'` — PostgreSQL re-checks the qualifier
	// against the committed version before granting the lock, so the sale is
	// refused rather than misfiled.
	var sessionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM cash_session
		WHERE device_id = $1 AND state = 'open' FOR SHARE`,
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

// jurisdictionRate resolves tax through the shop's jurisdiction.
//
// For a market that does not set tax nationally — the United States levies it
// by state, county and city, and several of those apply to one sale — the rate
// is the sum of the authorities above the shop. See registry.JurisdictionRate.
//
// The shop's OWN jurisdiction is used: that is the origin, which is the right
// answer for a customer standing at the counter. Destination-based sourcing,
// where the delivery address decides, needs the sourcing rule per state, and
// `tax_jurisdiction.is_origin_based` records that fact without anything reading
// it yet. Choosing one silently would be inventing a rule.
func (s *Service) jurisdictionRate(
	ctx context.Context, tx pgx.Tx, storeID uuid.UUID, treatment string,
	asOf time.Time,
) (registry.CombinedRate, error) {
	var jurisdictionID *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT tax_jurisdiction_id FROM store WHERE id = $1`, storeID).
		Scan(&jurisdictionID); err != nil {
		return registry.CombinedRate{}, err
	}
	if jurisdictionID == nil {
		// Reported as invalid input rather than as a missing rate: the shop has
		// not been told where it is, which is a setup step somebody can take,
		// not a legal value nobody has verified.
		return registry.CombinedRate{}, errs.New(errs.CodeInvalidInput,
			"This shop has no tax jurisdiction set, and its market taxes by "+
				"jurisdiction rather than nationally.")
	}

	// Returned whole, not summed. The total is what the customer pays; the
	// shares are what each authority is owed, and 0111 keeps them because a
	// total that cannot be broken down cannot be filed.
	return s.rules.JurisdictionRate(ctx, tx, *jurisdictionID, treatment, asOf)
}
