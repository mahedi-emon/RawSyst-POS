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
	"context"
	"encoding/json"
	"fmt"
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
	if q.TenantID != uuid.Nil {
		err = s.pool.TxAsTenant(ctx, q.TenantID, scan)
	} else {
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
	rule, err := s.Resolve(ctx, q)
	if err != nil {
		return decimal.Zero, err
	}
	var raw map[string]any
	if err := json.Unmarshal(rule.Payload, &raw); err != nil {
		return decimal.Zero, errs.Wrap(err, errs.CodeInternal,
			"A regulatory value could not be read.")
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

// Int resolves an integer field.
func (s *Service) Int(ctx context.Context, q Query, field string) (int64, error) {
	rule, err := s.Resolve(ctx, q)
	if err != nil {
		return 0, err
	}
	var raw map[string]any
	if err := json.Unmarshal(rule.Payload, &raw); err != nil {
		return 0, errs.Wrap(err, errs.CodeInternal, "A regulatory value could not be read.")
	}
	v, ok := raw[field]
	if !ok {
		return 0, errs.Newf(errs.CodeInternal, "Regulatory rule %q has no field %q.", q.Key, field)
	}
	f, ok := v.(float64) // encoding/json decodes numbers as float64
	if !ok {
		return 0, errs.Newf(errs.CodeUnverifiedRule,
			"Regulatory rule %q field %q is not yet a number — it may still be a placeholder.",
			q.Key, field)
	}
	return int64(f), nil
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
func (s *Service) VATRate(ctx context.Context, country string, asOf time.Time, tenantID uuid.UUID) (decimal.Decimal, error) {
	return s.Decimal(ctx, Query{
		Key:      KeyVATStandardRate,
		Country:  country,
		AsOf:     asOf,
		TenantID: tenantID,
	}, "rate")
}

// HealthReport summarises registry verification state for the Super Admin
// dashboard. Blueprint E8.3 calls staleness alerting "the operational
// mechanism that keeps the platform legally current instead of quietly
// drifting" — a passive flag nobody looks at achieves nothing.
type HealthReport struct {
	Verified        int      `json:"verified"`
	NeverVerified   int      `json:"never_verified"`
	StaleTaxPayroll int      `json:"stale_tax_payroll"` // > 6 months
	StaleOther      int      `json:"stale_other"`       // > 12 months
	BlockingRelease []string `json:"blocking_release"`  // release_blocker AND unverified
}

// Health computes the registry health report.
func (s *Service) Health(ctx context.Context) (HealthReport, error) {
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
		SELECT rule_key FROM regulatory_rule
		WHERE release_blocker AND verified_on IS NULL AND effective_to IS NULL
		ORDER BY rule_key`)
	if err != nil {
		return rep, db.Translate(err, "")
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return rep, db.Translate(err, "")
		}
		rep.BlockingRelease = append(rep.BlockingRelease, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, db.Translate(err, "")
	}
	return rep, tx.Commit(ctx)
}
