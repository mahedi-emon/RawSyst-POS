// Package registry resolves legal values — tax rates, thresholds, deadlines,
// file formats — from the Regulatory Rule Registry.
//
// Blueprint E8, verbatim: "No legal figure, deadline, threshold, rate, or file
// format may be hard-coded anywhere in the codebase."
//
// # Resolution is always dated
//
// Every lookup takes an explicit as-of date and there is no "current value"
// helper. That omission is deliberate. A convenience accessor defaulting to
// time.Now() would eventually be called from inside a historical report, and
// the result would be a March VAT return computed at June's rate — the exact
// failure E8.1 warns about, and one that surfaces during an audit rather than
// during testing.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Well-known rule keys. Declaring them as constants keeps typos out of call
// sites; the VALUES behind them still live entirely in the database.
const (
	KeyVATStandardRate       = "SA.VAT.STANDARD_RATE"
	KeyVATMandatoryThreshold = "SA.VAT.MANDATORY_REGISTRATION_THRESHOLD"
	KeyVATVoluntaryThreshold = "SA.VAT.VOLUNTARY_REGISTRATION_THRESHOLD"
	KeyVATMonthlyThreshold   = "SA.VAT.MONTHLY_FILING_THRESHOLD"
	KeyVATRecordRetention    = "SA.VAT.RECORD_RETENTION"
	KeyVATTaxTreatments      = "SA.VAT.TAX_TREATMENTS"

	KeyZATCASchemaVersion    = "SA.ZATCA.XML_SCHEMA_VERSION"
	KeyZATCAQRFields         = "SA.ZATCA.QR_TLV_FIELDS"
	KeyZATCAHashAlgorithm    = "SA.ZATCA.HASH_ALGORITHM"
	KeyZATCAReportingWindow  = "SA.ZATCA.REPORTING_WINDOW_HOURS"
	KeyZATCAOfflineTolerance = "SA.ZATCA.STANDARD_OFFLINE_TOLERANCE"
	KeyZATCACSIDRenewal      = "SA.ZATCA.CSID_RENEWAL_DAYS"

	// Onboarding: turning the nine captured CSR inputs into a CSID. Recorded by
	// 0045. The subject layout and the request format are release blockers, so
	// resolving either refuses in production until it has been read from the
	// official Fatoora SDK and Swagger files.
	KeyZATCACSRKeyParameters    = "SA.ZATCA.CSR_KEY_PARAMETERS"
	KeyZATCACSRCertTemplate     = "SA.ZATCA.CSR_CERTIFICATE_TEMPLATE"
	KeyZATCACSRSubjectLayout    = "SA.ZATCA.CSR_SUBJECT_LAYOUT"
	KeyZATCAOnboardingEndpoints = "SA.ZATCA.ONBOARDING_ENDPOINTS"
	KeyZATCAOnboardingRequest   = "SA.ZATCA.ONBOARDING_REQUEST_FORMAT"
	KeyZATCAOnboardingOTP       = "SA.ZATCA.ONBOARDING_OTP"

	// Split out of KeyZATCAQRFields by 0046. The QR framing and its nine tags are
	// verified; how tags 6 to 9 encode their values is answered two different
	// ways by the standard, so it stays a blocker.
	KeyZATCAQRTagValueEncoding = "SA.ZATCA.QR_TAG_VALUE_ENCODING"

	KeyPDPLDSRResponseDays = "SA.PDPL.DSR_RESPONSE_DAYS"
	KeyPDPLBreachHours     = "SA.PDPL.BREACH_NOTIFICATION_HOURS"

	KeyGOSIRates         = "SA.GOSI.RATES"
	KeyWPSWageFileFormat = "SA.WPS.WAGE_FILE_FORMAT"
)

// Placeholder marks a value that has never been verified against its official
// source. Seeded rules carry it so an unverified figure can never be silently
// mistaken for a real one.
const Placeholder = "__VERIFY__"

// Rule is a resolved regulatory value.
type Rule struct {
	Key        string
	Country    string
	Payload    json.RawMessage
	VerifiedOn *time.Time
	SourceDoc  string
	IsOverride bool
}

// Verified reports whether a human has confirmed this value against its Tier 1
// source. Unverified rules are usable in development and blocked at release.
func (r Rule) Verified() bool { return r.VerifiedOn != nil }

// Query is a single lookup.
type Query struct {
	Key      string
	Country  string
	AsOf     time.Time // the TRANSACTION date, never "now"
	TenantID uuid.UUID // optional: enables a tenant override

	// Tx is the transaction the CALLER is already inside, when there is one.
	//
	// This is not an optimisation. A caller holding a transaction holds a
	// connection, and resolving a rule on a second connection while holding the
	// first is a pool deadlock waiting for enough concurrency to happen: with
	// N sales in flight and a pool of N, every connection is held by a sale and
	// every sale is waiting for a connection that will never be free. Nothing
	// times out, because acquiring from the pool has no deadline — the till
	// simply stops, and so does every other till in the shop.
	//
	// It is reachable in production, not only under test: the cache makes it
	// rare rather than impossible, and the moment it is missed is exactly the
	// moment traffic is highest — a fresh deployment, a cache invalidated by a
	// registry write, or the first sale of a new month, all of which land on
	// many tills at once.
	//
	// So a caller inside a transaction passes it here and the read joins that
	// transaction instead of asking the pool for another connection. Callers
	// with no transaction leave it nil and the pool is used as before.
	//
	// Reading registry rows inside the caller's transaction is safe: they are
	// effective-dated reference data that this transaction never writes, so
	// there is no lock to take and nothing to deadlock against in the database.
	Tx pgx.Tx
}

// Service resolves rules, with a small cache.
type Service struct {
	pool *db.Pool

	// Rules change rarely and are read constantly. The cache key includes the
	// as-of month so a rate change mid-period cannot serve a stale value to the
	// following period.
	mu    sync.RWMutex
	cache map[string]Rule

	// requireVerified is true in production: an unverified rule then fails the
	// request rather than quietly computing tax from a placeholder.
	requireVerified bool
}

// New builds the service. Pass requireVerified=true in staging and production.
func New(pool *db.Pool, requireVerified bool) *Service {
	return &Service{
		pool:            pool,
		cache:           make(map[string]Rule, 64),
		requireVerified: requireVerified,
	}
}

func cacheKey(q Query) string {
	// Month granularity: rules are effective-dated by day, but a change within
	// a month is rare enough that a per-month key is a safe trade for a much
	// smaller cache. Invalidate() clears everything on any write.
	return fmt.Sprintf("%s|%s|%s|%s",
		q.Key, q.Country, q.AsOf.Format("2006-01"), q.TenantID)
}

// Invalidate clears the cache. Called after any registry write.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = make(map[string]Rule, 64)
	s.mu.Unlock()
}

// Resolve returns the rule in force on the given date.
func (s *Service) Resolve(ctx context.Context, q Query) (Rule, error) {
	if q.AsOf.IsZero() {
		return Rule{}, errs.New(errs.CodeInternal,
			"A date is required to resolve a regulatory rule.")
	}
	if q.Country == "" {
		return Rule{}, errs.New(errs.CodeInternal,
			"A country is required to resolve a regulatory rule.")
	}

	ck := cacheKey(q)
	s.mu.RLock()
	cached, hit := s.cache[ck]
	s.mu.RUnlock()
	if hit {
		return s.gate(cached)
	}

	var rule Rule
	rule.Key = q.Key
	rule.Country = q.Country

	scan := func(tx pgx.Tx) error {
		var tenantArg any
		if q.TenantID != uuid.Nil {
			tenantArg = q.TenantID
		}
		row := tx.QueryRow(ctx,
			`SELECT payload, verified_on, source_doc, is_override
			   FROM resolve_regulatory_rule($1, $2, $3, $4)`,
			q.Key, q.Country, q.AsOf, tenantArg)
		return row.Scan(&rule.Payload, &rule.VerifiedOn, &rule.SourceDoc, &rule.IsOverride)
	}

	var err error
	switch {
	// Inside the caller's transaction: read on the connection they already
	// hold. See Query.Tx for why asking the pool for a second one deadlocks.
	//
	// The tenant GUC is already set on this connection — TxAsTenant set it when
	// the caller opened the transaction — so the row-level security predicate a
	// tenant override relies on is in force without setting it again.
	case q.Tx != nil:
		err = scan(q.Tx)

	case q.TenantID != uuid.Nil:
		err = s.pool.TxAsTenant(ctx, q.TenantID, scan)

	default:
		// Platform-scoped read: registry rows are not tenant-scoped, since the
		// VAT rate in Saudi Arabia is the same fact for every tenant.
		err = func() error {
			tx, txErr := s.pool.Raw().Begin(ctx)
			if txErr != nil {
				return txErr
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			if scanErr := scan(tx); scanErr != nil {
				return scanErr
			}
			return tx.Commit(ctx)
		}()
	}

	if err != nil {
		return Rule{}, db.Translate(err, fmt.Sprintf(
			"No regulatory rule %q is on record for %s on %s. "+
				"A Super Admin must add it before this operation can run.",
			q.Key, q.Country, q.AsOf.Format("2 Jan 2006")))
	}

	s.mu.Lock()
	s.cache[ck] = rule
	s.mu.Unlock()

	return s.gate(rule)
}

// gate blocks unverified rules where verification is required.
func (s *Service) gate(r Rule) (Rule, error) {
	if s.requireVerified && !r.Verified() {
		return Rule{}, errs.Newf(errs.CodeUnverifiedRule,
			"The legal value %q has not been verified against its official source (%s), "+
				"so this operation cannot proceed. Verification is recorded in "+
				"Super Admin > Regulatory Registry.",
			r.Key, r.SourceDoc)
	}
	return r, nil
}

// --- typed accessors ---------------------------------------------------

// Decimal resolves a rule whose payload holds a single decimal under `field`.
// Money and rates are decimal throughout: binary floating point cannot
// represent 0.15 exactly, and a VAT calculation that is wrong in the last
// hallala is wrong on a tax return.
func (s *Service) Decimal(ctx context.Context, q Query, field string) (decimal.Decimal, error) {
	raw, rule, err := s.decodePayload(ctx, q)
	if err != nil {
		return decimal.Zero, err
	}
	v, ok := raw[field]
	if !ok {
		return decimal.Zero, errs.Newf(errs.CodeInternal,
			"Regulatory rule %q has no field %q.", q.Key, field)
	}
	str, ok := v.(string)
	if !ok {
		return decimal.Zero, errs.Newf(errs.CodeInternal,
			"Regulatory rule %q field %q is not a decimal string.", q.Key, field)
	}
	if str == Placeholder {
		return decimal.Zero, errs.Newf(errs.CodeUnverifiedRule,
			"The legal value %q is still a placeholder and must be verified "+
				"against %s before it can be used.", q.Key, rule.SourceDoc)
	}
	d, err := decimal.NewFromString(str)
	if err != nil {
		return decimal.Zero, errs.Wrap(err, errs.CodeInternal,
			fmt.Sprintf("Regulatory rule %q field %q is not a valid number.", q.Key, field))
	}
	return d, nil
}

// Int resolves an integer field, such as a deadline in days or hours.
//
// Decoding uses json.Number rather than the default any-typed decode, which
// would land on float64. No legal value passes through binary floating point in
// this system, even one that looks safely small: the habit is what matters, and
// the same helper shape is used for values where it would not be safe.
func (s *Service) Int(ctx context.Context, q Query, field string) (int64, error) {
	raw, rule, err := s.decodePayload(ctx, q)
	if err != nil {
		return 0, err
	}
	v, ok := raw[field]
	if !ok {
		return 0, errs.Newf(errs.CodeInternal, "Regulatory rule %q has no field %q.", q.Key, field)
	}
	num, ok := v.(json.Number)
	if !ok {
		if s, isStr := v.(string); isStr && s == Placeholder {
			return 0, errs.Newf(errs.CodeUnverifiedRule,
				"The legal value %q is still a placeholder and must be verified "+
					"against %s before it can be used.", q.Key, rule.SourceDoc)
		}
		return 0, errs.Newf(errs.CodeInternal,
			"Regulatory rule %q field %q is not a number.", q.Key, field)
	}
	n, err := num.Int64()
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternal,
			fmt.Sprintf("Regulatory rule %q field %q is not a whole number.", q.Key, field))
	}
	return n, nil
}

// decodePayload decodes a rule payload with numbers preserved as json.Number,
// so no value is silently widened to float64 on the way through.
func (s *Service) decodePayload(ctx context.Context, q Query) (map[string]any, Rule, error) {
	rule, err := s.Resolve(ctx, q)
	if err != nil {
		return nil, Rule{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(rule.Payload))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, Rule{}, errs.Wrap(err, errs.CodeInternal,
			"A regulatory value could not be read.")
	}
	return raw, rule, nil
}

// Into unmarshals the whole payload into a caller-supplied struct, for rules
// with structured shapes such as the GOSI rate matrix or the Mudad wage-file
// specification.
func (s *Service) Into(ctx context.Context, q Query, dst any) error {
	rule, err := s.Resolve(ctx, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(rule.Payload, dst); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			fmt.Sprintf("Regulatory rule %q has an unexpected shape.", q.Key))
	}
	return nil
}

// VATRate is the single accessor for the standard rate. Every tax computation
// goes through it, so there is exactly one place where the rate enters the
// system, and it is always dated.
// tx is the transaction the caller already holds, or nil. See Query.Tx.
// vatRateKeyFor names the standard-rate rule for a country.
//
// It used to be the constant KeyVATStandardRate — "SA.VAT.STANDARD_RATE" — for
// every caller, while the COUNTRY filter beside it was passed faithfully. So a
// Bangladeshi sale asked for Saudi Arabia's rate rule filtered to Bangladesh,
// matched nothing, and the till refused the sale with a registry miss. The
// country was plumbed all the way through and the key was not.
//
// Same shape as catalog.treatmentKeyFor, and named for the same reason: the key
// says which tax REGIME the value belongs to, so a country whose regime is not
// a VAT does not get a VAT key by default. The US is deliberately absent — its
// sales tax is set per state, county and city, so a national rate is not a
// thing that exists and returning one would be inventing it. A US sale
// therefore still misses the registry, which is the honest answer until
// jurisdiction-based resolution is built.
func vatRateKeyFor(country string) string {
	return strings.ToUpper(strings.TrimSpace(country)) + ".VAT.STANDARD_RATE"
}

func (s *Service) VATRate(
	ctx context.Context, tx pgx.Tx, country string, asOf time.Time,
	tenantID uuid.UUID,
) (decimal.Decimal, error) {
	return s.Decimal(ctx, Query{
		Key:      vatRateKeyFor(country),
		Country:  country,
		AsOf:     asOf,
		TenantID: tenantID,
		Tx:       tx,
	}, "rate")
}

// rateKeyFor names the rule holding the rate for one treatment in one country.
//
// A key per treatment rather than one rule holding several rates, because each
// is separately dated, separately sourced and separately verified: a country
// can change its reduced rate without touching its standard one, and the
// evidence recorded against each has to be the evidence for that rate.
//
// Only treatments that CHARGE need a key. Zero-rated, exempt and out-of-scope
// are answered from the treatment list itself — there is no rate to record and
// no source to cite for "this is not taxed".
func rateKeyFor(country, treatment string) (string, bool) {
	cc := strings.ToUpper(strings.TrimSpace(country))
	switch strings.ToLower(strings.TrimSpace(treatment)) {
	case "standard":
		return cc + ".VAT.STANDARD_RATE", true
	case "reduced":
		return cc + ".VAT.REDUCED_RATE", true
	default:
		// Includes "taxable", the US sales-tax treatment. Deliberately NOT
		// given a country-level key: US tax is set by state, county and city,
		// so a national rate does not exist, and minting a key for one would
		// invite somebody to fill it in. See TaxRate for what is said instead.
		return "", false
	}
}

// TaxRate resolves the rate for one tax treatment in one market at a date.
//
// This is what the sale path asks now. VATRate answered "the rate for this
// country", which is a question with no answer in two of the three markets this
// product serves: Bangladesh charges several rates, and the United States sets
// tax by jurisdiction rather than nationally.
//
// A missing rule is reported as a missing rule, naming what has to be recorded.
// Nothing is defaulted, because every default here would be an invented legal
// value.
func (s *Service) TaxRate(
	ctx context.Context, tx pgx.Tx, country, treatment string, asOf time.Time,
	tenantID uuid.UUID,
) (decimal.Decimal, error) {
	key, ok := rateKeyFor(country, treatment)
	if !ok {
		return decimal.Zero, errs.Newf(errs.CodeUnverifiedRule,
			"Tax for %q in %s is not set nationally, so it cannot be resolved "+
				"from a country and a date alone. It needs a tax jurisdiction "+
				"— state, county, city — with rates recorded against it.",
			treatment, strings.ToUpper(strings.TrimSpace(country)))
	}

	return s.Decimal(ctx, Query{
		Key:      key,
		Country:  country,
		AsOf:     asOf,
		TenantID: tenantID,
		Tx:       tx,
	}, "rate")
}

// HealthReport summarises registry verification state for the Super Admin
// dashboard. Blueprint E8.3 calls staleness alerting "the operational
// mechanism that keeps the platform legally current instead of quietly
// drifting" — a passive flag nobody looks at achieves nothing.
type HealthReport struct {
	Verified        int `json:"verified"`
	NeverVerified   int `json:"never_verified"`
	StaleTaxPayroll int `json:"stale_tax_payroll"` // > 6 months
	StaleOther      int `json:"stale_other"`       // > 12 months

	// BlockingRelease is the unverified release-blockers that belong to a market
	// this deployment actually serves. This is the set that refuses a
	// production start.
	BlockingRelease []string `json:"blocking_release"`

	// ServedMarkets is the countries this deployment's tenants trade in, read
	// from `tenant.market` (0103). Empty on a deployment with no tenants yet.
	ServedMarkets []string `json:"served_markets"`

	// DeferredBlockers is the unverified release-blockers for markets nobody
	// here trades in — reported, never blocking.
	//
	// Named "deferred" rather than "ignored" on purpose: nothing about them has
	// been resolved, and the moment a tenant is created in one of their markets
	// they become blocking. Kept in the report so the operator can see what
	// verification the platform still owes before selling into that market.
	DeferredBlockers []string `json:"deferred_blockers"`
}

// Health computes the registry health report for this deployment.
func (s *Service) Health(ctx context.Context) (HealthReport, error) {
	served, err := s.servedMarkets(ctx)
	if err != nil {
		return HealthReport{}, err
	}
	return s.healthFor(ctx, served)
}

// healthFor is Health with the served markets supplied rather than read.
//
// Split out so the classification can be tested against the REAL registry for a
// named set of markets. The alternative — asserting on whatever tenants happen
// to exist in a shared test database — would be a test whose meaning changed
// every time another test provisioned a tenant.
func (s *Service) healthFor(ctx context.Context, served []string) (HealthReport, error) {
	var rep HealthReport
	tx, err := s.pool.Raw().Begin(ctx)
	if err != nil {
		return rep, db.Translate(err, "")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	err = tx.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE verified_on IS NOT NULL),
		  count(*) FILTER (WHERE verified_on IS NULL),
		  count(*) FILTER (WHERE verified_on IS NOT NULL
		                     AND source_authority IN ('zatca','gosi','mhrsd')
		                     AND verified_on < current_date - INTERVAL '6 months'),
		  count(*) FILTER (WHERE verified_on IS NOT NULL
		                     AND source_authority NOT IN ('zatca','gosi','mhrsd')
		                     AND verified_on < current_date - INTERVAL '12 months')
		FROM regulatory_rule
		WHERE effective_to IS NULL`).
		Scan(&rep.Verified, &rep.NeverVerified, &rep.StaleTaxPayroll, &rep.StaleOther)
	if err != nil {
		return rep, db.Translate(err, "")
	}

	rows, err := tx.Query(ctx, `
		SELECT rule_key, country FROM regulatory_rule
		WHERE release_blocker AND verified_on IS NULL AND effective_to IS NULL
		ORDER BY rule_key`)
	if err != nil {
		return rep, db.Translate(err, "")
	}
	type blocker struct{ key, country string }
	var blockers []blocker
	for rows.Next() {
		var b blocker
		if err := rows.Scan(&b.key, &b.country); err != nil {
			rows.Close()
			return rep, db.Translate(err, "")
		}
		blockers = append(blockers, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, db.Translate(err, "")
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, db.Translate(err, "")
	}

	rep.ServedMarkets = served

	inService := make(map[string]bool, len(served))
	for _, m := range served {
		inService[m] = true
	}
	for _, b := range blockers {
		if inService[strings.ToLower(strings.TrimSpace(b.country))] {
			rep.BlockingRelease = append(rep.BlockingRelease, b.key)
		} else {
			rep.DeferredBlockers = append(rep.DeferredBlockers, b.key)
		}
	}
	return rep, nil
}

// RequiresVerification reports whether this deployment refuses unverified legal
// values. True in staging and production.
//
// Exposed so a caller can ask the question the registry was configured with,
// rather than deciding for itself what "strict" means. Provisioning uses it: a
// development machine may create a tenant in any market, and a production
// deployment may not create one whose legal values are still placeholders.
func (s *Service) RequiresVerification() bool { return s.requireVerified }

// UnverifiedBlockersFor lists the release-blocking rules for one market that
// have never been verified against their official source.
//
// The provisioning-time half of the boot gate. `Health` answers "may this
// process start given the tenants it already has"; this answers "may a tenant
// be created in this market at all", which is the moment the boot answer would
// otherwise change underneath a process already running.
func (s *Service) UnverifiedBlockersFor(
	ctx context.Context, market string,
) ([]string, error) {
	market = strings.ToLower(strings.TrimSpace(market))
	if market == "" {
		return nil, nil
	}

	var out []string
	tx, err := s.pool.Raw().Begin(ctx)
	if err != nil {
		return nil, db.Translate(err, "")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `
		SELECT rule_key FROM regulatory_rule
		WHERE release_blocker
		  AND blocks = 'onboarding'
		  AND verified_on IS NULL
		  AND effective_to IS NULL
		  AND lower(country) = $1
		ORDER BY rule_key`, market)
	if err != nil {
		return nil, db.Translate(err, "")
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, db.Translate(err, "")
		}
		out = append(out, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, db.Translate(err, "")
	}
	return out, tx.Commit(ctx)
}

// servedMarkets is the set of countries this deployment's tenants trade in.
//
// # Why this is read from data rather than configured
//
// A deployment's markets are a FACT about the tenants it holds, not a policy
// somebody sets. A config flag would be a second copy of that fact, maintained
// by hand, and the failure mode is silent in the worst direction: an operator
// onboards a Saudi client, forgets the flag, and the process keeps booting
// while Saudi legal values are still placeholders. `tenant.market` (0103) is
// written by the platform operator at provisioning and cannot drift from the
// tenants that exist.
//
// # Why the platform plane
//
// `tenant` is ENABLE + FORCE row-level security, so an unscoped connection sees
// zero rows — silently, which would read here as "this deployment serves no
// markets" and quietly disable the gate entirely. That is exactly the failure
// this project has hit before (a chain verifier that used an unscoped
// connection reported every chain intact because RLS hid every row). Migration
// 0006 grants the platform plane a predicate on `tenant`, and this is a
// platform-level question about the deployment rather than about any one
// tenant, so it is asked there.
//
// # An empty result is honest, not a bypass
//
// A deployment with no tenants serves no markets and has no legal figure to
// compute, so nothing blocks. The real protection against a placeholder is not
// this gate but `gate()`, which refuses EVERY unverified rule at the point of
// use whenever requireVerified is set — so a Saudi payroll run on a deployment
// that booted refuses on `SA.GOSI.RATES` regardless of what happened at start.
// This check is an early, loud warning; it is not the thing standing between a
// placeholder and a tax return.
func (s *Service) servedMarkets(ctx context.Context) ([]string, error) {
	var out []string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT DISTINCT lower(market) FROM tenant
			WHERE status <> 'deactivated'
			ORDER BY 1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m string
			if err := rows.Scan(&m); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}
