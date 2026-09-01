// Package privacy is the PDPL module (blueprint E4) and the storefront
// disclosures Saudi e-commerce law requires (E5).
//
// # The deadlines come from the registry and then stop moving
//
// Two clocks run here: a data-subject request is due in 30 days, extendable
// once by 30 more, and a breach must reach SDAIA within 72 hours of the
// controller becoming aware. Both figures are registry rules — E8 is explicit
// that "every legal parameter is versioned data, never code" — and both are
// resolved ONCE, when the request or the incident is opened, and written to the
// row.
//
// Resolving them on every read would be the more obvious design and it is
// wrong. A rule that changes next March would silently move the due date of a
// request opened in February, and an auditor asking why a request was due on
// the 14th would get an answer that depends on when they asked.
//
// # Withdrawal is a stamp, not a delete
//
// A consent row is never removed. E4.1's most-enforced violation is marketing
// without provable agreement, and the proof a shop needs is not "there is no
// row saying they agreed" — it is "here is the row, here is the date, here is
// the channel, and here is the date they withdrew and we stopped". A deleted
// grant and a grant that never existed look identical, which is the wrong side
// of an audit to be on.
//
// # An erasure request meets the tax law and says so
//
// E4.1 and E2.4 disagree in a specific, foreseeable case: a customer asks to be
// erased and their name is on invoices the Zakat authority requires for six
// years. `Fulfil` refuses to pretend. It checks for a legal hold, and when one
// applies the request closes as partially_fulfilled WITH the reason recorded —
// which is what the regulation actually asks a controller to do, and is the
// answer a shop can defend to both the customer and the auditor.
package privacy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service carries the privacy register.
type Service struct {
	pool     *db.Pool
	registry *registry.Service
}

// NewService builds the service.
//
// The registry is required rather than optional: a data-subject request whose
// deadline was guessed is worse than one that could not be opened, because the
// first ships and the second is noticed.
func NewService(pool *db.Pool, reg *registry.Service) *Service {
	return &Service{pool: pool, registry: reg}
}

// Scope is who is asking and on whose books.
//
// No country field: the country is the company's, it is read inside the
// transaction that needs it, and the registry lookup is handed that same
// transaction. A rule resolved on a second connection while holding the first
// is the pool deadlock registry.Query.Tx exists to prevent.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// countryOf reads the company's country inside the caller's transaction.
func countryOf(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var country string
	err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, companyID).Scan(&country)
	if err == pgx.ErrNoRows {
		return "", errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return country, err
}

// ---------------------------------------------------------------------------
// Consent
// ---------------------------------------------------------------------------

// Consent is one recorded agreement.
type Consent struct {
	ID          uuid.UUID `json:"id"`
	SubjectType string    `json:"subject_type"`
	SubjectID   uuid.UUID `json:"subject_id"`
	SubjectName string    `json:"subject_name,omitempty"`

	LawfulBasis string `json:"lawful_basis"`
	Purpose     string `json:"purpose"`
	Channel     string `json:"channel"`

	Granted     bool   `json:"granted"`
	GrantedAt   string `json:"granted_at"`
	WithdrawnAt string `json:"withdrawn_at,omitempty"`
	Proof       string `json:"proof"`
	RecordedBy  string `json:"recorded_by,omitempty"`
}

// NewConsent records an agreement.
type NewConsent struct {
	SubjectType string
	SubjectID   uuid.UUID
	LawfulBasis string
	Purpose     string
	Channel     string
	Proof       string
}

// RecordConsent writes a grant.
//
// Re-granting after a withdrawal is a new row, which the partial unique index
// allows: the history is the point. Granting twice without withdrawing in
// between is a conflict, because two live grants for the same purpose and
// channel would make "did they agree" ambiguous.
func (s *Service) RecordConsent(
	ctx context.Context, scope Scope, in NewConsent,
) (Consent, error) {
	if strings.TrimSpace(in.Proof) == "" {
		return Consent{}, errs.New(errs.CodeInvalidInput,
			"Record how the agreement was obtained. An unevidenced consent is "+
				"the violation, not the record of it.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := s.subjectBelongsHere(
			ctx, tx, scope.CompanyID, in.SubjectType, in.SubjectID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO privacy_consent (
			  tenant_id, company_id, subject_type, subject_id, lawful_basis,
			  purpose, channel, proof, recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.SubjectType, in.SubjectID,
			in.LawfulBasis, in.Purpose, in.Channel,
			strings.TrimSpace(in.Proof), scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e,
				"That agreement is already recorded. Withdraw it first if it "+
					"is being given again.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "consent_recorded",
			EntityType: "privacy_consent", EntityID: &id,
			After: map[string]any{
				"purpose": in.Purpose, "channel": in.Channel,
				"lawful_basis": in.LawfulBasis,
			},
		})
	})
	if err != nil {
		return Consent{}, err
	}
	return s.consent(ctx, scope, id)
}

// WithdrawConsent stamps a grant as withdrawn.
func (s *Service) WithdrawConsent(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Consent, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE privacy_consent
			   SET granted = false, withdrawn_at = now()
			 WHERE id = $1 AND company_id = $2 AND granted`,
			id, scope.CompanyID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That agreement was not found, or it has already been "+
					"withdrawn.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "consent_withdrawn",
			EntityType: "privacy_consent", EntityID: &id,
		})
	})
	if err != nil {
		return Consent{}, db.Translate(err, "")
	}
	return s.consent(ctx, scope, id)
}

// Consents lists what is recorded, newest first, optionally for one subject.
func (s *Service) Consents(
	ctx context.Context, scope Scope, subjectType string, subjectID *uuid.UUID,
	liveOnly bool,
) ([]Consent, error) {
	out := []Consent{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, consentSelect+`
			WHERE c.company_id = $1
			  AND ($2 = '' OR c.subject_type = $2)
			  AND ($3::uuid IS NULL OR c.subject_id = $3)
			  AND (NOT $4::boolean OR c.granted)
			ORDER BY c.granted_at DESC
			LIMIT 500`,
			scope.CompanyID, subjectType, subjectID, liveOnly)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			c, e := scanConsent(rows)
			if e != nil {
				return e
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// MayContact answers the question a marketing send actually asks.
//
// Used by the notification centre before a marketing message goes out. It is
// deliberately a positive check: no row means no, because E4.1 puts the burden
// of proof on the controller and "we had no record either way" is not a
// defence.
func (s *Service) MayContact(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	subjectType string, subjectID uuid.UUID, channel string,
) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT true FROM privacy_consent
		 WHERE company_id = $1 AND subject_type = $2 AND subject_id = $3
		   AND purpose = 'marketing' AND granted
		   AND channel IN ($4, 'any')
		 LIMIT 1`, companyID, subjectType, subjectID, channel).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return ok, err
}

const consentSelect = `
	SELECT c.id, c.subject_type, c.subject_id,
	       coalesce(cust.name, emp.full_name, ''),
	       c.lawful_basis, c.purpose, c.channel, c.granted, c.granted_at,
	       c.withdrawn_at, c.proof, coalesce(u.full_name, '')
	FROM privacy_consent c
	LEFT JOIN customer  cust ON cust.id = c.subject_id
	                        AND c.subject_type = 'customer'
	LEFT JOIN employee  emp  ON emp.id  = c.subject_id
	                        AND c.subject_type = 'employee'
	LEFT JOIN app_user  u    ON u.id    = c.recorded_by`

func (s *Service) consent(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Consent, error) {
	var out Consent
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, consentSelect+`
			WHERE c.id = $1 AND c.company_id = $2`, id, scope.CompanyID)
		c, e := scanConsent(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That record was not found.")
		}
		out = c
		return e
	})
	return out, db.Translate(err, "")
}

func scanConsent(row scanner) (Consent, error) {
	var c Consent
	var granted time.Time
	var withdrawn *time.Time
	if err := row.Scan(&c.ID, &c.SubjectType, &c.SubjectID, &c.SubjectName,
		&c.LawfulBasis, &c.Purpose, &c.Channel, &c.Granted, &granted,
		&withdrawn, &c.Proof, &c.RecordedBy); err != nil {
		return Consent{}, err
	}
	c.GrantedAt = granted.UTC().Format(time.RFC3339)
	if withdrawn != nil {
		c.WithdrawnAt = withdrawn.UTC().Format(time.RFC3339)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Data subject requests
// ---------------------------------------------------------------------------

// Request is one person exercising a right.
type Request struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"request_no"`
	Kind   string    `json:"kind"`
	Status string    `json:"status"`

	SubjectType    string     `json:"subject_type"`
	SubjectID      *uuid.UUID `json:"subject_id,omitempty"`
	SubjectName    string     `json:"subject_name"`
	SubjectContact string     `json:"subject_contact"`

	ReceivedAt      string `json:"received_at"`
	DueAt           string `json:"due_at"`
	ExtendedTo      string `json:"extended_to,omitempty"`
	ExtensionReason string `json:"extension_reason,omitempty"`

	// DaysLeft counts down to whichever deadline is in force, and goes negative
	// once it has passed. The warning E4.1 asks for is this number.
	DaysLeft int `json:"days_left"`

	ClosedAt    string `json:"closed_at,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	OutcomeNote string `json:"outcome_note,omitempty"`
	LegalHold   bool   `json:"legal_hold_applied"`

	HandledBy string `json:"handled_by,omitempty"`
}

// NewRequest opens one.
type NewRequest struct {
	Kind           string
	SubjectType    string
	SubjectID      *uuid.UUID
	SubjectName    string
	SubjectContact string
}

// OpenRequest records a request and starts the clock.
func (s *Service) OpenRequest(
	ctx context.Context, scope Scope, in NewRequest,
) (Request, error) {
	if strings.TrimSpace(in.SubjectName) == "" {
		return Request{}, errs.New(errs.CodeInvalidInput,
			"Record who is asking.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		days, e := s.responseDays(ctx, tx, scope)
		if e != nil {
			return e
		}
		if in.SubjectID != nil {
			if e := s.subjectBelongsHere(ctx, tx, scope.CompanyID,
				in.SubjectType, *in.SubjectID); e != nil {
				return e
			}
		}

		var n int64
		if e := tx.QueryRow(ctx,
			`SELECT nextval('data_subject_request_seq')`).Scan(&n); e != nil {
			return e
		}
		number := fmt.Sprintf("DSR-%06d", n)

		if e := tx.QueryRow(ctx, `
			INSERT INTO data_subject_request (
			  tenant_id, company_id, request_no, subject_type, subject_id,
			  subject_name, subject_contact, kind, due_at, raised_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(days => $9),
			        $10)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.SubjectType,
			in.SubjectID, strings.TrimSpace(in.SubjectName),
			strings.TrimSpace(in.SubjectContact), in.Kind, days, scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e, "That request could not be recorded.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "dsr_opened",
			EntityType: "data_subject_request", EntityID: &id,
			After: map[string]any{
				"request_no": number, "kind": in.Kind,
				"response_days": days,
			},
		})
	})
	if err != nil {
		return Request{}, err
	}
	return s.Request(ctx, scope, id)
}

// responseDays resolves SA.PDPL.DSR_RESPONSE_DAYS.
func (s *Service) responseDays(
	ctx context.Context, tx pgx.Tx, scope Scope,
) (int, error) {
	country, err := countryOf(ctx, tx, scope.CompanyID)
	if err != nil {
		return 0, err
	}
	n, err := s.registry.Int(ctx, registry.Query{
		Key:      "SA.PDPL.DSR_RESPONSE_DAYS",
		Country:  country,
		AsOf:     time.Now().UTC(),
		TenantID: scope.TenantID,
		Tx:       tx,
	}, "days")
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ExtendRequest takes the single further period the regulation allows.
func (s *Service) ExtendRequest(
	ctx context.Context, scope Scope, id uuid.UUID, reason string,
) (Request, error) {
	if strings.TrimSpace(reason) == "" {
		return Request{}, errs.New(errs.CodeInvalidInput,
			"Say why the request needs longer. The regulation allows the "+
				"extension for unusual effort, not by default.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		country, e := countryOf(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		extra, e := s.registry.Int(ctx, registry.Query{
			Key:      "SA.PDPL.DSR_RESPONSE_DAYS",
			Country:  country,
			AsOf:     time.Now().UTC(),
			TenantID: scope.TenantID,
			Tx:       tx,
		}, "extension_days")
		if e != nil {
			return e
		}

		tag, e := tx.Exec(ctx, `
			UPDATE data_subject_request
			   SET extended_to = due_at + make_interval(days => $3),
			       extension_reason = $4,
			       status = 'extended'
			 WHERE id = $1 AND company_id = $2
			   AND closed_at IS NULL AND extended_to IS NULL`,
			id, scope.CompanyID, int(extra), strings.TrimSpace(reason))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That request is closed, or it has already been extended "+
					"once. The regulation allows one extension.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "dsr_extended",
			EntityType: "data_subject_request", EntityID: &id,
			After: map[string]any{"reason": reason, "extra_days": extra},
		})
	})
	if err != nil {
		return Request{}, db.Translate(err, "")
	}
	return s.Request(ctx, scope, id)
}

// CloseRequest records the outcome.
//
// An erasure that ran into a legal hold closes as partially_fulfilled with the
// hold named. See the package note: this is the conflict E4.1 and E2.4 create,
// and answering it honestly is the whole job.
func (s *Service) CloseRequest(
	ctx context.Context, scope Scope, id uuid.UUID, outcome, note string,
) (Request, error) {
	switch outcome {
	case "fulfilled", "partially_fulfilled", "refused":
	default:
		return Request{}, errs.New(errs.CodeInvalidInput,
			"Say whether the request was fulfilled, partly fulfilled, or "+
				"refused.")
	}
	if outcome != "fulfilled" && strings.TrimSpace(note) == "" {
		return Request{}, errs.New(errs.CodeInvalidInput,
			"A request that was not fulfilled in full needs a reason the "+
				"person can be given.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var kind string
		var subjectType *string
		var subjectID *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT kind, subject_type, subject_id FROM data_subject_request
			 WHERE id = $1 AND company_id = $2 AND closed_at IS NULL`,
			id, scope.CompanyID).Scan(&kind, &subjectType, &subjectID)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound,
				"That request was not found, or it is already closed.")
		}
		if e != nil {
			return e
		}

		held := false
		if subjectID != nil {
			held, e = s.underLegalHold(
				ctx, tx, scope.CompanyID, *subjectType, *subjectID)
			if e != nil {
				return e
			}
		}

		status := "fulfilled"
		if outcome == "refused" {
			status = "refused"
		}

		if _, e := tx.Exec(ctx, `
			UPDATE data_subject_request
			   SET status = $3, outcome = $4, outcome_note = $5,
			       legal_hold_applied = $6, closed_at = now(),
			       handled_by = $7
			 WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, status, outcome, nullIfBlank(note),
			held, scope.UserID); e != nil {
			return e
		}

		// An erasure that actually ran leaves a permanent record of what went.
		if kind == "deletion" && outcome != "refused" {
			if _, e := tx.Exec(ctx, `
				INSERT INTO destruction_log (
				  tenant_id, company_id, data_category, entity_type, entity_id,
				  action, row_count, reason, request_id, executed_by)
				VALUES ($1,$2,$3,$4,$5,'anonymize',1,$6,$7,$8)`,
				scope.TenantID, scope.CompanyID,
				"personal data held about a data subject",
				subjectType, subjectID,
				"Erasure request "+id.String(), id, scope.UserID); e != nil {
				return e
			}
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "dsr_closed",
			EntityType: "data_subject_request", EntityID: &id,
			After: map[string]any{
				"outcome": outcome, "legal_hold_applied": held,
			},
		})
	})
	if err != nil {
		return Request{}, db.Translate(err, "")
	}
	return s.Request(ctx, scope, id)
}

// underLegalHold says whether erasing this subject would break a hold.
func (s *Service) underLegalHold(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	subjectType string, subjectID uuid.UUID,
) (bool, error) {
	var held bool
	err := tx.QueryRow(ctx, `
		SELECT true FROM legal_hold
		 WHERE company_id = $1 AND released_at IS NULL
		   AND ((subject_type = $2 AND subject_id = $3)
		        OR subject_id IS NULL)
		 LIMIT 1`, companyID, subjectType, subjectID).Scan(&held)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return held, err
}

// Requests lists the queue.
func (s *Service) Requests(
	ctx context.Context, scope Scope, openOnly bool,
) ([]Request, error) {
	out := []Request{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, requestSelect+`
			WHERE r.company_id = $1
			  AND (NOT $2::boolean OR r.closed_at IS NULL)
			ORDER BY r.closed_at IS NOT NULL,
			         coalesce(r.extended_to, r.due_at)
			LIMIT 500`, scope.CompanyID, openOnly)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanRequest(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Request reads one.
func (s *Service) Request(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Request, error) {
	var out Request
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, requestSelect+`
			WHERE r.id = $1 AND r.company_id = $2`, id, scope.CompanyID)
		r, e := scanRequest(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That request was not found.")
		}
		out = r
		return e
	})
	return out, db.Translate(err, "")
}

const requestSelect = `
	SELECT r.id, r.request_no, r.kind, r.status, r.subject_type, r.subject_id,
	       r.subject_name, r.subject_contact, r.received_at, r.due_at,
	       r.extended_to, coalesce(r.extension_reason, ''), r.closed_at,
	       coalesce(r.outcome, ''), coalesce(r.outcome_note, ''),
	       r.legal_hold_applied, coalesce(u.full_name, '')
	FROM data_subject_request r
	LEFT JOIN app_user u ON u.id = r.handled_by`

func scanRequest(row scanner) (Request, error) {
	var r Request
	var received, due time.Time
	var extended, closed *time.Time
	if err := row.Scan(&r.ID, &r.Number, &r.Kind, &r.Status, &r.SubjectType,
		&r.SubjectID, &r.SubjectName, &r.SubjectContact, &received, &due,
		&extended, &r.ExtensionReason, &closed, &r.Outcome, &r.OutcomeNote,
		&r.LegalHold, &r.HandledBy); err != nil {
		return Request{}, err
	}
	r.ReceivedAt = received.UTC().Format(time.RFC3339)
	r.DueAt = due.UTC().Format(time.RFC3339)
	deadline := due
	if extended != nil {
		r.ExtendedTo = extended.UTC().Format(time.RFC3339)
		deadline = *extended
	}
	if closed != nil {
		r.ClosedAt = closed.UTC().Format(time.RFC3339)
	}
	r.DaysLeft = int(time.Until(deadline).Hours() / 24)
	return r, nil
}

// ---------------------------------------------------------------------------
// Incidents
// ---------------------------------------------------------------------------

// Incident is one breach or suspected breach.
type Incident struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"incident_no"`
	Title    string    `json:"title"`
	Severity string    `json:"severity"`
	Status   string    `json:"status"`

	WhatHappened   string `json:"what_happened"`
	DataCategories string `json:"data_categories"`
	Subjects       *int   `json:"subjects_affected,omitempty"`
	Consequences   string `json:"consequences,omitempty"`
	Containment    string `json:"containment,omitempty"`

	DiscoveredAt string `json:"discovered_at"`
	NotifyDueAt  string `json:"notify_due_at"`
	// HoursLeft is the countdown E4.1 asks for, negative once the window has
	// closed. Hours rather than days because 72 of them is the whole window.
	HoursLeft int `json:"hours_left"`

	SDAIANotifiedAt    string `json:"sdaia_notified_at,omitempty"`
	SubjectsNotifiedAt string `json:"subjects_notified_at,omitempty"`
	ClosedAt           string `json:"closed_at,omitempty"`
	LoggedBy           string `json:"logged_by,omitempty"`
}

// NewIncident logs one.
type NewIncident struct {
	Title          string
	WhatHappened   string
	DataCategories string
	Subjects       *int
	Consequences   string
	Containment    string
	Severity       string
	DiscoveredAt   time.Time
}

// LogIncident records a breach and starts the 72-hour countdown.
func (s *Service) LogIncident(
	ctx context.Context, scope Scope, in NewIncident,
) (Incident, error) {
	if strings.TrimSpace(in.Title) == "" ||
		strings.TrimSpace(in.WhatHappened) == "" {
		return Incident{}, errs.New(errs.CodeInvalidInput,
			"Record what happened. The notification has to describe the "+
				"incident and how it occurred.")
	}
	if in.DiscoveredAt.IsZero() {
		return Incident{}, errs.New(errs.CodeInvalidInput,
			"Record when the incident was discovered. The deadline runs from "+
				"becoming aware, not from now.")
	}

	severity := in.Severity
	if severity == "" {
		severity = "medium"
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		country, e := countryOf(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		hours, e := s.registry.Int(ctx, registry.Query{
			Key:      "SA.PDPL.BREACH_NOTIFICATION_HOURS",
			Country:  country,
			AsOf:     time.Now().UTC(),
			TenantID: scope.TenantID,
			Tx:       tx,
		}, "hours")
		if e != nil {
			return e
		}

		var n int64
		if e := tx.QueryRow(ctx,
			`SELECT nextval('privacy_incident_seq')`).Scan(&n); e != nil {
			return e
		}
		number := fmt.Sprintf("INC-%06d", n)

		if e := tx.QueryRow(ctx, `
			INSERT INTO privacy_incident (
			  tenant_id, company_id, incident_no, title, what_happened,
			  data_categories, subjects_affected, consequences, containment,
			  severity, discovered_at, notify_due_at, logged_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			        $11::timestamptz + make_interval(hours => $12), $13)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number,
			strings.TrimSpace(in.Title), strings.TrimSpace(in.WhatHappened),
			strings.TrimSpace(in.DataCategories), in.Subjects,
			nullIfBlank(in.Consequences), nullIfBlank(in.Containment),
			severity, in.DiscoveredAt.UTC(), int(hours), scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e, "That incident could not be recorded.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "privacy_incident_logged",
			EntityType: "privacy_incident", EntityID: &id,
			After: map[string]any{
				"incident_no": number, "severity": severity,
				"notification_hours": hours,
			},
		})
	})
	if err != nil {
		return Incident{}, err
	}
	return s.Incident(ctx, scope, id)
}

// MarkNotified stamps who has been told.
func (s *Service) MarkNotified(
	ctx context.Context, scope Scope, id uuid.UUID, who string,
) (Incident, error) {
	column := ""
	switch who {
	case "sdaia":
		column = "sdaia_notified_at"
	case "subjects":
		column = "subjects_notified_at"
	default:
		return Incident{}, errs.New(errs.CodeInvalidInput,
			"Say whether the authority or the affected people were notified.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The column name is chosen from a fixed set above, never from the
		// caller's string.
		tag, e := tx.Exec(ctx, `
			UPDATE privacy_incident
			   SET `+column+` = now(),
			       status = CASE WHEN status = 'open' THEN 'notified'
			                     ELSE status END
			 WHERE id = $1 AND company_id = $2 AND `+column+` IS NULL`,
			id, scope.CompanyID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That incident was not found, or that notification is "+
					"already recorded.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "privacy_incident_notified",
			EntityType: "privacy_incident", EntityID: &id,
			After: map[string]any{"notified": who},
		})
	})
	if err != nil {
		return Incident{}, db.Translate(err, "")
	}
	return s.Incident(ctx, scope, id)
}

// CloseIncident records containment and shuts it.
func (s *Service) CloseIncident(
	ctx context.Context, scope Scope, id uuid.UUID, containment string,
) (Incident, error) {
	if strings.TrimSpace(containment) == "" {
		return Incident{}, errs.New(errs.CodeInvalidInput,
			"Record the containment and remediation measures taken. The "+
				"notification has to state them.")
	}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE privacy_incident
			   SET containment = $3, status = 'closed', closed_at = now()
			 WHERE id = $1 AND company_id = $2 AND closed_at IS NULL`,
			id, scope.CompanyID, strings.TrimSpace(containment))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That incident was not found, or it is already closed.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "privacy_incident_closed",
			EntityType: "privacy_incident", EntityID: &id,
		})
	})
	if err != nil {
		return Incident{}, db.Translate(err, "")
	}
	return s.Incident(ctx, scope, id)
}

// Incidents lists them, open ones first and most urgent at the top.
func (s *Service) Incidents(
	ctx context.Context, scope Scope, openOnly bool,
) ([]Incident, error) {
	out := []Incident{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, incidentSelect+`
			WHERE i.company_id = $1
			  AND (NOT $2::boolean OR i.closed_at IS NULL)
			ORDER BY i.closed_at IS NOT NULL, i.notify_due_at
			LIMIT 500`, scope.CompanyID, openOnly)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			i, e := scanIncident(rows)
			if e != nil {
				return e
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Incident reads one.
func (s *Service) Incident(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Incident, error) {
	var out Incident
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, incidentSelect+`
			WHERE i.id = $1 AND i.company_id = $2`, id, scope.CompanyID)
		i, e := scanIncident(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That incident was not found.")
		}
		out = i
		return e
	})
	return out, db.Translate(err, "")
}

const incidentSelect = `
	SELECT i.id, i.incident_no, i.title, i.severity, i.status, i.what_happened,
	       i.data_categories, i.subjects_affected, coalesce(i.consequences, ''),
	       coalesce(i.containment, ''), i.discovered_at, i.notify_due_at,
	       i.sdaia_notified_at, i.subjects_notified_at, i.closed_at,
	       coalesce(u.full_name, '')
	FROM privacy_incident i
	LEFT JOIN app_user u ON u.id = i.logged_by`

func scanIncident(row scanner) (Incident, error) {
	var i Incident
	var discovered, due time.Time
	var sdaiaAt, subjectsAt, closed *time.Time
	if err := row.Scan(&i.ID, &i.Number, &i.Title, &i.Severity, &i.Status,
		&i.WhatHappened, &i.DataCategories, &i.Subjects, &i.Consequences,
		&i.Containment, &discovered, &due, &sdaiaAt, &subjectsAt, &closed,
		&i.LoggedBy); err != nil {
		return Incident{}, err
	}
	i.DiscoveredAt = discovered.UTC().Format(time.RFC3339)
	i.NotifyDueAt = due.UTC().Format(time.RFC3339)
	i.HoursLeft = int(time.Until(due).Hours())
	for _, p := range []struct {
		t   *time.Time
		dst *string
	}{{sdaiaAt, &i.SDAIANotifiedAt}, {subjectsAt, &i.SubjectsNotifiedAt},
		{closed, &i.ClosedAt}} {
		if p.t != nil {
			*p.dst = p.t.UTC().Format(time.RFC3339)
		}
	}
	return i, nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

type scanner interface {
	Scan(dst ...any) error
}

var subjectTables = map[string]string{
	"customer": "customer",
	"employee": "employee",
}

// subjectBelongsHere refuses a consent or a request aimed at another company's
// person, for the same reason docs refuses an attachment aimed at one.
func (s *Service) subjectBelongsHere(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	subjectType string, subjectID uuid.UUID,
) error {
	table, ok := subjectTables[subjectType]
	if !ok {
		// supplier_contact and unknown have no row of their own to check
		// against; the free-text name is all there is, and it is not a
		// reference that could cross a tenant.
		return nil
	}
	var exists bool
	e := tx.QueryRow(ctx,
		`SELECT true FROM `+table+` WHERE id = $1 AND company_id = $2`,
		subjectID, companyID).Scan(&exists)
	if e == pgx.ErrNoRows {
		return errs.New(errs.CodeNotFound, "That person was not found.")
	}
	return e
}

func nullIfBlank(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
