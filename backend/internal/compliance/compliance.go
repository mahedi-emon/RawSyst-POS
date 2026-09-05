// Package compliance is the monitoring dashboard (blueprint E7).
//
// # It reads; it never writes
//
// E7 asks one question — "am I legally exposed right now?" — and every answer
// on the screen already exists somewhere else in this product. Nothing here
// owns a table. That is the design: a compliance dashboard that kept its own
// copy of "invoices pending submission" would drift from the truth, and the
// first time anybody noticed would be during an inspection.
//
// So each panel is a query against the module that owns the fact, and each one
// degrades to a stated "not configured" rather than a zero. There is a real
// difference between a shop with no failed submissions and a shop that has not
// begun onboarding, and reporting both as 0 would be the most reassuring
// possible way to be wrong.
//
// # It does not decide whether a shop is compliant
//
// Every panel reports a FACT and, where the fact has a deadline, how long is
// left. It does not compute a score or a traffic light for the shop as a whole.
// A single green tick over nine unrelated obligations is a claim this product
// cannot make and a shop should not rely on, and the blueprint never asks for
// one: E7's list is nine specific readings, and that is what this returns.
package compliance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service answers E7's question.
type Service struct {
	pool     *db.Pool
	registry *registry.Service
}

// NewService builds the service.
func NewService(pool *db.Pool, reg *registry.Service) *Service {
	return &Service{pool: pool, registry: reg}
}

// Scope is who is asking and on whose books.
//
// The country is not carried in: it is the company's, and it is read once
// inside the report's transaction so every registry lookup is handed that same
// transaction rather than reaching for a second connection.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID

	// country is filled in by Read. Unexported so a caller cannot set it to
	// something the company is not.
	country string
}

// Report is the whole screen.
type Report struct {
	Invoicing  Invoicing  `json:"invoicing"`
	VAT        VATPosture `json:"vat"`
	Privacy    Privacy    `json:"privacy"`
	Storefront Storefront `json:"storefront"`
	Payroll    Payroll    `json:"payroll"`
	People     People     `json:"people"`
	Records    Records    `json:"records"`
	// Registry is E8's own health: a rule the product depends on that nobody
	// has verified against the official source is a compliance exposure in
	// itself, and E8.4 names three that block release.
	UnverifiedRules int `json:"unverified_rules"`
	BlockingRules   int `json:"blocking_rules"`
}

// Invoicing is the e-invoicing readings.
//
// Read-only over what the e-invoicing module already records. This package
// neither submits nor retries anything; it reports what that module has done.
type Invoicing struct {
	// Started is false for a shop that has not begun onboarding at all, which
	// is a different state from "onboarded with nothing pending".
	Started bool   `json:"started"`
	Status  string `json:"status"`

	Devices      int `json:"devices"`
	DevicesReady int `json:"devices_ready"`

	Pending  int `json:"pending"`
	Failed   int `json:"failed"`
	Rejected int `json:"rejected"`
}

// VATPosture is the tax readings.
type VATPosture struct {
	Registered bool   `json:"registered"`
	Number     string `json:"vat_number,omitempty"`
	// Rate as a percentage, from the registry, so the screen shows what is
	// actually in force rather than what somebody remembers.
	StandardRate string `json:"standard_rate,omitempty"`

	// The next filing deadline and the countdown to it. Absent for a shop that
	// is not registered, because there is nothing to file.
	NextFilingDue string `json:"next_filing_due,omitempty"`
	DaysToFiling  *int   `json:"days_to_filing,omitempty"`

	// A period whose last day has passed and which is still open for posting.
	// Not a violation on its own — a shop closes its books after the month
	// ends, not on the last night of it — but it is the reason a return
	// prepared today can still change, and the owner should know before they
	// file from it.
	OpenEndedPeriods int `json:"open_ended_periods"`
}

// Privacy is the PDPL readings E7 asks for.
type Privacy struct {
	// Consent coverage: how many customers the shop holds marketing consent
	// for, against how many it could contact. Not a compliance score — a shop
	// that never markets needs no consent at all — but the number an owner
	// needs before they press send.
	Customers        int `json:"customers"`
	MarketingConsent int `json:"marketing_consent"`

	OpenRequests    int  `json:"open_requests"`
	OverdueRequests int  `json:"overdue_requests"`
	SoonestDueDays  *int `json:"soonest_due_days,omitempty"`

	OpenIncidents       int  `json:"open_incidents"`
	IncidentHoursLeft   *int `json:"incident_hours_left,omitempty"`
	IncidentsUnnotified int  `json:"incidents_unnotified"`

	RetentionPolicies int    `json:"retention_policies"`
	RetentionLastRun  string `json:"retention_last_run,omitempty"`
	ActivitiesLogged  int    `json:"processing_activities"`
	DPOAppointed      bool   `json:"dpo_appointed"`
	LiveHolds         int    `json:"legal_holds"`
}

// Storefront is E5's disclosure completeness.
type Storefront struct {
	// Missing is the list of required disclosures that are not filled in, by
	// field name. Empty means complete.
	Missing []string `json:"missing"`
}

// Payroll is the wage-protection and social-insurance readings.
type Payroll struct {
	LastRunPeriod string `json:"last_run_period,omitempty"`
	// Runs that are approved but whose wage file has not been submitted. The
	// number that matters: an unpaid, unfiled month is the exposure.
	UnsubmittedRuns int `json:"unsubmitted_runs"`

	// DeadlineKnown is false while SA.WPS.SUBMISSION_TIMING is still one of
	// E8.4's unverified rules. The screen says so rather than showing a date
	// this product invented — a wage-file deadline a shop plans around and
	// misses is worse than no date at all.
	DeadlineKnown  bool   `json:"deadline_known"`
	NextDeadline   string `json:"next_deadline,omitempty"`
	DaysToDeadline *int   `json:"days_to_deadline,omitempty"`
}

// People is the document-expiry reading.
type People struct {
	// Documents that expire within the window, and ones that already have.
	// E7 names Iqama and work permits specifically; anything with an expiry
	// date counts, because a lapsed supplier VAT certificate is the same kind
	// of problem.
	//
	// Both totals span TWO sources: the uploaded document shelf, and the
	// residency permit recorded against each employee. Counting only the first
	// is what this did, and it meant a dashboard whose own comment named Iqamas
	// answered "nothing expiring" to a business whose cashier could not legally
	// work next month -- while `GET /employees/expiring` listed them by name.
	ExpiringSoon int `json:"expiring_soon"`
	Expired      int `json:"expired"`

	// Of those, the ones that are somebody's permit rather than a filed
	// document. Two different screens fix them, so a dashboard that lumps them
	// together tells an owner a number and not where to go.
	StaffExpiringSoon int `json:"staff_expiring_soon"`
	StaffExpired      int `json:"staff_expired"`
}

// Records is the archive-health reading.
type Records struct {
	RetentionYears int    `json:"retention_years"`
	OldestInvoice  string `json:"oldest_invoice,omitempty"`
	// Whether a verified backup exists at all, and how old it is. E7 asks
	// whether the archive is "intact and retrievable", and the only honest
	// answer this product can give is whether anybody has proved a restore.
	LastVerifiedBackup string `json:"last_verified_backup,omitempty"`
	BackupAgeDays      *int   `json:"backup_age_days,omitempty"`
}

// Read builds the whole report.
//
// One transaction, so every panel is a reading of the same instant. A report
// assembled from nine separate connections could show a request as open in one
// panel and closed in another, which is the kind of inconsistency that makes
// somebody stop trusting a compliance screen.
func (s *Service) Read(ctx context.Context, scope Scope) (Report, error) {
	var out Report
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&scope.country); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That company was not found.")
			}
			return e
		}
		for _, part := range []func(context.Context, pgx.Tx, Scope, *Report) error{
			s.invoicing, s.vat, s.privacy, s.storefront,
			s.payroll, s.people, s.records, s.rules,
		} {
			if e := part(ctx, tx, scope, &out); e != nil {
				return e
			}
		}
		return nil
	})
	return out, db.Translate(err, "")
}

func (s *Service) invoicing(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	if e := tx.QueryRow(ctx, `
		SELECT zatca_status::text FROM company WHERE id = $1`,
		scope.CompanyID).Scan(&out.Invoicing.Status); e != nil {
		return e
	}
	out.Invoicing.Started = out.Invoicing.Status != "not_started"

	if e := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'active')
		FROM device WHERE company_id = $1`,
		scope.CompanyID).Scan(
		&out.Invoicing.Devices, &out.Invoicing.DevicesReady); e != nil {
		return e
	}

	// A shop that has not onboarded has no submissions and reporting 0 pending
	// would read as "nothing outstanding" rather than "nothing started". The
	// Started flag above is what distinguishes them on the screen.
	return tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE submitted_at IS NULL),
		       count(*) FILTER (WHERE submitted_at IS NOT NULL
		                          AND reject_reason IS NOT NULL),
		       count(*) FILTER (WHERE reject_reason IS NOT NULL)
		FROM zatca_invoice z
		JOIN sales_invoice i ON i.id = z.invoice_id
		WHERE i.company_id = $1`, scope.CompanyID).Scan(
		&out.Invoicing.Pending, &out.Invoicing.Failed, &out.Invoicing.Rejected)
}

func (s *Service) vat(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	var number *string
	if e := tx.QueryRow(ctx, `
		SELECT vat_registered, vat_number FROM company WHERE id = $1`,
		scope.CompanyID).Scan(&out.VAT.Registered, &number); e != nil {
		return e
	}
	if number != nil {
		out.VAT.Number = *number
	}

	rate, err := s.registry.VATRate(ctx, tx, scope.country,
		time.Now().UTC(), scope.TenantID)
	if err == nil {
		out.VAT.StandardRate = rate.Mul(hundred).StringFixed(2)
	}

	if e := tx.QueryRow(ctx, `
		SELECT count(*) FROM fiscal_period
		 WHERE company_id = $1 AND state = 'open' AND ends_on < current_date`,
		scope.CompanyID).Scan(&out.VAT.OpenEndedPeriods); e != nil {
		return e
	}

	// SA.VAT.FILING_DUE_RULE holds "last_day_of_month_following_period_end".
	// The rule is read rather than assumed, and a rule this product does not
	// recognise leaves the deadline blank instead of guessing a date an owner
	// might plan around.
	if !out.VAT.Registered {
		return nil
	}
	var rule struct {
		Rule string `json:"rule"`
	}
	if e := s.registry.Into(ctx, registry.Query{
		Key:      "SA.VAT.FILING_DUE_RULE",
		Country:  scope.country,
		AsOf:     time.Now().UTC(),
		TenantID: scope.TenantID,
		Tx:       tx,
	}, &rule); e != nil {
		return nil
	}
	if rule.Rule != "last_day_of_month_following_period_end" {
		return nil
	}
	// The last day of next month: the first of the month after next, less a
	// day. Written that way because month lengths differ and February exists.
	now := time.Now().UTC()
	due := time.Date(now.Year(), now.Month()+2, 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -1)
	out.VAT.NextFilingDue = due.Format("2006-01-02")
	days := int(time.Until(due).Hours() / 24)
	out.VAT.DaysToFiling = &days
	return nil
}

func (s *Service) privacy(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FROM customer WHERE company_id = $1`,
		scope.CompanyID).Scan(&out.Privacy.Customers); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx, `
		SELECT count(DISTINCT subject_id) FROM privacy_consent
		 WHERE company_id = $1 AND subject_type = 'customer'
		   AND purpose = 'marketing' AND granted`,
		scope.CompanyID).Scan(&out.Privacy.MarketingConsent); e != nil {
		return e
	}

	var soonest *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (
		         WHERE coalesce(extended_to, due_at) < now()),
		       min(coalesce(extended_to, due_at))
		FROM data_subject_request
		WHERE company_id = $1 AND closed_at IS NULL`,
		scope.CompanyID).Scan(&out.Privacy.OpenRequests,
		&out.Privacy.OverdueRequests, &soonest); e != nil {
		return e
	}
	if soonest != nil {
		days := int(time.Until(*soonest).Hours() / 24)
		out.Privacy.SoonestDueDays = &days
	}

	var nextNotify *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE sdaia_notified_at IS NULL),
		       min(notify_due_at) FILTER (WHERE sdaia_notified_at IS NULL)
		FROM privacy_incident
		WHERE company_id = $1 AND closed_at IS NULL`,
		scope.CompanyID).Scan(&out.Privacy.OpenIncidents,
		&out.Privacy.IncidentsUnnotified, &nextNotify); e != nil {
		return e
	}
	if nextNotify != nil {
		hours := int(time.Until(*nextNotify).Hours())
		out.Privacy.IncidentHoursLeft = &hours
	}

	var lastRun *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE is_active), max(last_run_at)
		FROM retention_policy WHERE company_id = $1`,
		scope.CompanyID).Scan(
		&out.Privacy.RetentionPolicies, &lastRun); e != nil {
		return e
	}
	if lastRun != nil {
		out.Privacy.RetentionLastRun = lastRun.UTC().Format(time.RFC3339)
	}

	if e := tx.QueryRow(ctx, `
		SELECT count(*) FROM processing_activity WHERE company_id = $1`,
		scope.CompanyID).Scan(&out.Privacy.ActivitiesLogged); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FROM legal_hold
		 WHERE company_id = $1 AND released_at IS NULL`,
		scope.CompanyID).Scan(&out.Privacy.LiveHolds); e != nil {
		return e
	}
	return tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM privacy_settings
		                WHERE company_id = $1
		                  AND btrim(coalesce(dpo_name, '')) <> '')`,
		scope.CompanyID).Scan(&out.Privacy.DPOAppointed)
}

func (s *Service) storefront(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	var cr, ref, returnAr, deliveryAr, phone, email *string
	var cooling *int
	if e := tx.QueryRow(ctx, `
		SELECT c.cr_number, d.registration_ref, d.return_policy_ar,
		       d.delivery_terms_ar, d.contact_phone, d.contact_email,
		       d.cooling_off_days
		FROM company c
		LEFT JOIN storefront_disclosure d ON d.company_id = c.id
		WHERE c.id = $1`, scope.CompanyID).Scan(
		&cr, &ref, &returnAr, &deliveryAr, &phone, &email,
		&cooling); e != nil {
		return e
	}

	out.Storefront.Missing = []string{}
	for _, c := range []struct {
		filled bool
		what   string
	}{
		{filled(cr), "cr_number"},
		{filled(ref), "registration_ref"},
		{filled(returnAr), "return_policy_ar"},
		{filled(deliveryAr), "delivery_terms_ar"},
		{filled(phone) || filled(email), "contact"},
		{cooling != nil, "cooling_off_days"},
	} {
		if !c.filled {
			out.Storefront.Missing = append(out.Storefront.Missing, c.what)
		}
	}
	return nil
}

func (s *Service) payroll(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	// Formatted in SQL, not scanned as text. `payroll_run.period` is a DATE,
	// and pgx refuses to put one into a *string in binary format -- so this
	// route answered 500 for every business, whatever was in the table. It had
	// no screen, so nothing ever asked it.
	//
	// The month, not the day: a run is a month, the rest of the product speaks
	// of it as "2026-08", and a compliance dashboard saying 2026-08-01 invites
	// somebody to wonder what happened on the first.
	var period *string
	if e := tx.QueryRow(ctx, `
		SELECT to_char(max(period), 'YYYY-MM')
		FROM payroll_run WHERE company_id = $1`,
		scope.CompanyID).Scan(&period); e != nil {
		return e
	}
	if period != nil {
		out.Payroll.LastRunPeriod = *period
	}

	// A run that is approved and whose wage file has not been submitted is the
	// exposure. A draft run is not late; it has not been decided yet.
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FROM payroll_run r
		 WHERE r.company_id = $1 AND r.status = 'approved'
		   AND NOT EXISTS (
		     SELECT 1 FROM wps_file f
		      WHERE f.run_id = r.id AND f.submitted_at IS NOT NULL)`,
		scope.CompanyID).Scan(&out.Payroll.UnsubmittedRuns); e != nil {
		return e
	}

	// The wage-protection window is a registry rule and is one of the three
	// E8.4 flags as needing verification before release. Until somebody has
	// verified it, `Int` refuses to answer and the screen says the deadline is
	// not known. See the field comment.
	days, err := s.registry.Int(ctx, registry.Query{
		Key:      "SA.WPS.SUBMISSION_TIMING",
		Country:  scope.country,
		AsOf:     time.Now().UTC(),
		TenantID: scope.TenantID,
		Tx:       tx,
	}, "payment_window_days")
	if err != nil || period == nil {
		return nil
	}
	end, perr := time.Parse("2006-01", *period)
	if perr != nil {
		return nil
	}
	due := end.AddDate(0, 1, int(days)-1)
	out.Payroll.DeadlineKnown = true
	out.Payroll.NextDeadline = due.Format("2006-01-02")
	left := int(time.Until(due).Hours() / 24)
	out.Payroll.DaysToDeadline = &left
	return nil
}

func (s *Service) people(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (
		         WHERE expires_on >= current_date
		           AND expires_on <= current_date + 60),
		       count(*) FILTER (WHERE expires_on < current_date)
		FROM document
		WHERE company_id = $1 AND expires_on IS NOT NULL`,
		scope.CompanyID).Scan(
		&out.People.ExpiringSoon, &out.People.Expired); e != nil {
		return e
	}

	// And the residency permits, which are the ones E7 actually names. They
	// live on the employee rather than on the document shelf, and leaving them
	// out made this reading blind to the case it exists for. Somebody who has
	// left is excluded: their permit lapsing is not this business's exposure.
	//
	// Sixty days, the same window as the document count above and as
	// `GET /employees/expiring` defaults to. Two windows would let the
	// dashboard and the staff screen disagree about the same person.
	if e := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (
		         WHERE id_expires_on >= current_date
		           AND id_expires_on <= current_date + 60),
		       count(*) FILTER (WHERE id_expires_on < current_date)
		FROM employee
		WHERE company_id = $1 AND id_expires_on IS NOT NULL
		  AND left_on IS NULL`,
		scope.CompanyID).Scan(
		&out.People.StaffExpiringSoon, &out.People.StaffExpired); e != nil {
		return e
	}
	out.People.ExpiringSoon += out.People.StaffExpiringSoon
	out.People.Expired += out.People.StaffExpired
	return nil
}

func (s *Service) records(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	years, err := s.registry.Int(ctx, registry.Query{
		Key:      "SA.VAT.RECORD_RETENTION",
		Country:  scope.country,
		AsOf:     time.Now().UTC(),
		TenantID: scope.TenantID,
		Tx:       tx,
	}, "years")
	if err == nil {
		out.Records.RetentionYears = int(years)
	}

	var oldest *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT min(issued_at) FROM sales_invoice WHERE company_id = $1`,
		scope.CompanyID).Scan(&oldest); e != nil {
		return e
	}
	if oldest != nil {
		out.Records.OldestInvoice = oldest.Format("2006-01-02")
	}

	var verified *time.Time
	if e := tx.QueryRow(ctx, `
		SELECT max(verified_at) FROM backup_record
		 WHERE tenant_id = $1 AND verified_at IS NOT NULL`,
		scope.TenantID).Scan(&verified); e != nil {
		return e
	}
	if verified != nil {
		out.Records.LastVerifiedBackup = verified.UTC().Format(time.RFC3339)
		age := int(time.Since(*verified).Hours() / 24)
		out.Records.BackupAgeDays = &age
	}
	return nil
}

// rules is E8's own health, which is a compliance exposure of a different kind:
// a rate the product is charging that nobody has checked against the official
// source.
func (s *Service) rules(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Report,
) error {
	return tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE verified_on IS NULL),
		       count(*) FILTER (WHERE verified_on IS NULL AND release_blocker)
		FROM regulatory_rule
		WHERE country = $1 AND effective_to IS NULL`,
		scope.country).Scan(&out.UnverifiedRules, &out.BlockingRules)
}

// hundred turns the registry's fractional rate into the percentage a screen
// shows. 0.15 is what the rule holds; 15.00 is what an owner reads.
var hundred = decimal.NewFromInt(100)

func filled(s *string) bool {
	return s != nil && *s != ""
}
