// The parts of the privacy module that are configuration rather than events:
// the record of processing activities, retention policy, legal holds, the
// destruction log, the DPO designation, and the storefront disclosures E5
// requires an online shop to publish.

package privacy

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// ---------------------------------------------------------------------------
// Record of Processing Activities
// ---------------------------------------------------------------------------

// Activity is one entry in the RoPA.
type Activity struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Purpose     string    `json:"purpose"`
	LawfulBasis string    `json:"lawful_basis"`

	DataCategories    string `json:"data_categories"`
	SubjectCategories string `json:"subject_categories"`
	Recipients        string `json:"recipients,omitempty"`

	CrossBorder        bool   `json:"cross_border"`
	DestinationCountry string `json:"destination_country,omitempty"`
	TransferSafeguard  string `json:"transfer_safeguard,omitempty"`

	RetentionNote string `json:"retention_note,omitempty"`
	SystemName    string `json:"system_name,omitempty"`
	OwnerName     string `json:"owner_name,omitempty"`
	ReviewedOn    string `json:"reviewed_on,omitempty"`
}

// Activities returns the register.
func (s *Service) Activities(
	ctx context.Context, scope Scope,
) ([]Activity, error) {
	out := []Activity{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, name, purpose, lawful_basis, data_categories,
			       subject_categories, coalesce(recipients, ''), cross_border,
			       coalesce(destination_country, ''),
			       coalesce(transfer_safeguard, ''),
			       coalesce(retention_note, ''), coalesce(system_name, ''),
			       coalesce(owner_name, ''), reviewed_on
			FROM processing_activity
			WHERE company_id = $1
			ORDER BY name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a Activity
			var reviewed *time.Time
			if e := rows.Scan(&a.ID, &a.Name, &a.Purpose, &a.LawfulBasis,
				&a.DataCategories, &a.SubjectCategories, &a.Recipients,
				&a.CrossBorder, &a.DestinationCountry, &a.TransferSafeguard,
				&a.RetentionNote, &a.SystemName, &a.OwnerName,
				&reviewed); e != nil {
				return e
			}
			if reviewed != nil {
				a.ReviewedOn = reviewed.Format("2006-01-02")
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveActivity creates or updates one entry.
//
// Upserted on the name rather than the id, because the RoPA is a list a shop
// edits rather than a ledger it appends to, and an owner correcting a typo in
// the purpose should not end up with two entries for the same processing.
func (s *Service) SaveActivity(
	ctx context.Context, scope Scope, in Activity,
) (Activity, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Activity{}, errs.New(errs.CodeInvalidInput,
			"Name the processing activity.")
	}
	if in.CrossBorder && (strings.TrimSpace(in.DestinationCountry) == "" ||
		strings.TrimSpace(in.TransferSafeguard) == "") {
		return Activity{}, errs.New(errs.CodeInvalidInput,
			"A transfer outside the Kingdom has to name where the data goes "+
				"and what protects it.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO processing_activity (
			  tenant_id, company_id, name, purpose, lawful_basis,
			  data_categories, subject_categories, recipients, cross_border,
			  destination_country, transfer_safeguard, retention_note,
			  system_name, owner_name, reviewed_on)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,current_date)
			ON CONFLICT (company_id, lower(name)) DO UPDATE SET
			  purpose = excluded.purpose,
			  lawful_basis = excluded.lawful_basis,
			  data_categories = excluded.data_categories,
			  subject_categories = excluded.subject_categories,
			  recipients = excluded.recipients,
			  cross_border = excluded.cross_border,
			  destination_country = excluded.destination_country,
			  transfer_safeguard = excluded.transfer_safeguard,
			  retention_note = excluded.retention_note,
			  system_name = excluded.system_name,
			  owner_name = excluded.owner_name,
			  reviewed_on = current_date
			RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(in.Name),
			in.Purpose, in.LawfulBasis, in.DataCategories,
			in.SubjectCategories, nullIfBlank(in.Recipients), in.CrossBorder,
			nullIfBlank(in.DestinationCountry),
			nullIfBlank(in.TransferSafeguard), nullIfBlank(in.RetentionNote),
			nullIfBlank(in.SystemName), nullIfBlank(in.OwnerName),
		).Scan(&id); e != nil {
			return db.Translate(e, "That activity could not be saved.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "processing_activity_saved",
			EntityType: "processing_activity", EntityID: &id,
			After: map[string]any{"name": in.Name},
		})
	})
	if err != nil {
		return Activity{}, err
	}
	in.ID = id
	in.ReviewedOn = time.Now().UTC().Format("2006-01-02")
	return in, nil
}

// RemoveActivity deletes one entry.
func (s *Service) RemoveActivity(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				DELETE FROM processing_activity
				 WHERE id = $1 AND company_id = $2`, id, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That activity was not found.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "processing_activity_removed",
				EntityType: "processing_activity", EntityID: &id,
			})
		}), "")
}

// ---------------------------------------------------------------------------
// Retention, holds and the destruction log
// ---------------------------------------------------------------------------

// Retention is one configured policy.
type Retention struct {
	ID           uuid.UUID `json:"id"`
	DataCategory string    `json:"data_category"`
	RetainMonths int       `json:"retain_months"`
	Action       string    `json:"action"`
	LegalNote    string    `json:"legal_note,omitempty"`
	IsActive     bool      `json:"is_active"`
	LastRunAt    string    `json:"last_run_at,omitempty"`
}

// Retentions lists the policies.
func (s *Service) Retentions(
	ctx context.Context, scope Scope,
) ([]Retention, error) {
	out := []Retention{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, data_category, retain_months, action,
			       coalesce(legal_note, ''), is_active, last_run_at
			FROM retention_policy
			WHERE company_id = $1
			ORDER BY data_category`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r Retention
			var last *time.Time
			if e := rows.Scan(&r.ID, &r.DataCategory, &r.RetainMonths,
				&r.Action, &r.LegalNote, &r.IsActive, &last); e != nil {
				return e
			}
			if last != nil {
				r.LastRunAt = last.UTC().Format(time.RFC3339)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveRetention creates or updates a policy.
func (s *Service) SaveRetention(
	ctx context.Context, scope Scope, in Retention,
) (Retention, error) {
	if strings.TrimSpace(in.DataCategory) == "" {
		return Retention{}, errs.New(errs.CodeInvalidInput,
			"Name the category of data the policy covers.")
	}
	if in.RetainMonths < 1 || in.RetainMonths > 600 {
		return Retention{}, errs.New(errs.CodeInvalidInput,
			"A retention period runs from one month to fifty years.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The tax rule is a floor, not a suggestion. A shop setting a two-year
		// retention on invoices would be configuring a breach of the record
		// retention obligation, and the honest thing is to refuse rather than
		// to let the job discover it later.
		if months, ok := s.retentionFloor(
			ctx, tx, scope, in.DataCategory); ok && in.RetainMonths < months {
			return errs.New(errs.CodeComplianceBlocked,
				"Records of that kind must be kept for longer than that. Tax "+
					"and commercial-record obligations override a shorter "+
					"retention.")
		}

		if e := tx.QueryRow(ctx, `
			INSERT INTO retention_policy (
			  tenant_id, company_id, data_category, retain_months, action,
			  legal_note, is_active)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (company_id, lower(data_category)) DO UPDATE SET
			  retain_months = excluded.retain_months,
			  action = excluded.action,
			  legal_note = excluded.legal_note,
			  is_active = excluded.is_active
			RETURNING id`,
			scope.TenantID, scope.CompanyID,
			strings.TrimSpace(in.DataCategory), in.RetainMonths, in.Action,
			nullIfBlank(in.LegalNote), in.IsActive,
		).Scan(&id); e != nil {
			return db.Translate(e, "That policy could not be saved.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "retention_policy_saved",
			EntityType: "retention_policy", EntityID: &id,
			After: map[string]any{
				"data_category": in.DataCategory,
				"retain_months": in.RetainMonths,
				"action":        in.Action,
			},
		})
	})
	if err != nil {
		return Retention{}, err
	}
	in.ID = id
	return in, nil
}

// retentionFloor is how long the law says records of this kind must be kept.
//
// Only the categories the registry actually has a rule for. A shop naming its
// own category gets no floor, which is right: the product does not know what
// "workshop photographs" are or how long anybody must keep them.
func (s *Service) retentionFloor(
	ctx context.Context, tx pgx.Tx, scope Scope, category string,
) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "invoices", "tax records", "accounting records", "sales invoices":
	default:
		return 0, false
	}
	country, err := countryOf(ctx, tx, scope.CompanyID)
	if err != nil {
		return 0, false
	}
	years, err := s.registry.Int(ctx, registry.Query{
		Key:      "SA.VAT.RECORD_RETENTION",
		Country:  country,
		AsOf:     time.Now().UTC(),
		TenantID: scope.TenantID,
		Tx:       tx,
	}, "years")
	if err != nil {
		return 0, false
	}
	return int(years) * 12, true
}

// Hold is one legal hold.
type Hold struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Reason       string     `json:"reason"`
	SubjectType  string     `json:"subject_type,omitempty"`
	SubjectID    *uuid.UUID `json:"subject_id,omitempty"`
	DataCategory string     `json:"data_category,omitempty"`
	PlacedAt     string     `json:"placed_at"`
	ReleasedAt   string     `json:"released_at,omitempty"`
	PlacedBy     string     `json:"placed_by,omitempty"`
}

// Holds lists them, live ones first.
func (s *Service) Holds(ctx context.Context, scope Scope) ([]Hold, error) {
	out := []Hold{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT h.id, h.name, h.reason, coalesce(h.subject_type, ''),
			       h.subject_id, coalesce(h.data_category, ''), h.placed_at,
			       h.released_at, coalesce(u.full_name, '')
			FROM legal_hold h
			LEFT JOIN app_user u ON u.id = h.placed_by
			WHERE h.company_id = $1
			ORDER BY h.released_at IS NOT NULL, h.placed_at DESC`,
			scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var h Hold
			var placed time.Time
			var released *time.Time
			if e := rows.Scan(&h.ID, &h.Name, &h.Reason, &h.SubjectType,
				&h.SubjectID, &h.DataCategory, &placed, &released,
				&h.PlacedBy); e != nil {
				return e
			}
			h.PlacedAt = placed.UTC().Format(time.RFC3339)
			if released != nil {
				h.ReleasedAt = released.UTC().Format(time.RFC3339)
			}
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// PlaceHold records a hold that blocks routine deletion.
func (s *Service) PlaceHold(
	ctx context.Context, scope Scope, in Hold,
) (Hold, error) {
	if strings.TrimSpace(in.Name) == "" ||
		strings.TrimSpace(in.Reason) == "" {
		return Hold{}, errs.New(errs.CodeInvalidInput,
			"Name the hold and say why it is in place. A hold stops a person "+
				"being erased when they have asked to be.")
	}
	if in.SubjectID == nil && strings.TrimSpace(in.DataCategory) == "" {
		return Hold{}, errs.New(errs.CodeInvalidInput,
			"A hold covers either one named person or a category of records.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.SubjectID != nil {
			if e := s.subjectBelongsHere(ctx, tx, scope.CompanyID,
				in.SubjectType, *in.SubjectID); e != nil {
				return e
			}
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO legal_hold (
			  tenant_id, company_id, name, reason, subject_type, subject_id,
			  data_category, placed_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(in.Name),
			strings.TrimSpace(in.Reason), nullIfBlank(in.SubjectType),
			in.SubjectID, nullIfBlank(in.DataCategory), scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e, "That hold could not be recorded.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "legal_hold_placed",
			EntityType: "legal_hold", EntityID: &id,
			After: map[string]any{"name": in.Name, "reason": in.Reason},
		})
	})
	if err != nil {
		return Hold{}, err
	}
	in.ID = id
	in.PlacedAt = time.Now().UTC().Format(time.RFC3339)
	return in, nil
}

// ReleaseHold lifts one.
func (s *Service) ReleaseHold(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE legal_hold SET released_at = now()
				 WHERE id = $1 AND company_id = $2 AND released_at IS NULL`,
				id, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That hold was not found, or it is already released.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "legal_hold_released",
				EntityType: "legal_hold", EntityID: &id,
			})
		}), "")
}

// Destruction is one entry in the permanent log.
type Destruction struct {
	ID           uuid.UUID `json:"id"`
	DataCategory string    `json:"data_category"`
	EntityType   string    `json:"entity_type,omitempty"`
	Action       string    `json:"action"`
	RowCount     int       `json:"row_count"`
	Reason       string    `json:"reason"`
	ExecutedAt   string    `json:"executed_at"`
	ExecutedBy   string    `json:"executed_by,omitempty"`
}

// Destructions returns the proof of what has been deleted and when.
func (s *Service) Destructions(
	ctx context.Context, scope Scope,
) ([]Destruction, error) {
	out := []Destruction{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT d.id, d.data_category, coalesce(d.entity_type, ''),
			       d.action, d.row_count, d.reason, d.executed_at,
			       coalesce(u.full_name, '')
			FROM destruction_log d
			LEFT JOIN app_user u ON u.id = d.executed_by
			WHERE d.company_id = $1
			ORDER BY d.executed_at DESC
			LIMIT 500`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d Destruction
			var at time.Time
			if e := rows.Scan(&d.ID, &d.DataCategory, &d.EntityType, &d.Action,
				&d.RowCount, &d.Reason, &at, &d.ExecutedBy); e != nil {
				return e
			}
			d.ExecutedAt = at.UTC().Format(time.RFC3339)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ---------------------------------------------------------------------------
// Settings: the DPO, the SDAIA registration, and the storefront disclosures
// ---------------------------------------------------------------------------

// Settings is a company's privacy posture.
type Settings struct {
	DPOName     string `json:"dpo_name,omitempty"`
	DPOEmail    string `json:"dpo_email,omitempty"`
	DPOPhone    string `json:"dpo_phone,omitempty"`
	DPOExternal bool   `json:"dpo_external"`

	SDAIARegistrationRef   string `json:"sdaia_registration_ref,omitempty"`
	ControllerRegisteredOn string `json:"controller_registered_on,omitempty"`
	PrivacyNoticeURL       string `json:"privacy_notice_url,omitempty"`

	// DataRegion is the tenant's, not the company's — E4.2 puts the region on
	// the tenant because it decides which database the rows are in, and a
	// group's companies cannot be in two places at once.
	DataRegion string `json:"data_region"`
}

// Settings reads them.
func (s *Service) Settings(ctx context.Context, scope Scope) (Settings, error) {
	var out Settings
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var registered *time.Time
		e := tx.QueryRow(ctx, `
			SELECT coalesce(p.dpo_name, ''), coalesce(p.dpo_email, ''),
			       coalesce(p.dpo_phone, ''), p.dpo_external,
			       coalesce(p.sdaia_registration_ref, ''),
			       p.controller_registered_on,
			       coalesce(p.privacy_notice_url, ''), t.data_region::text
			FROM privacy_settings p
			JOIN tenant t ON t.id = p.tenant_id
			WHERE p.company_id = $1`, scope.CompanyID).Scan(
			&out.DPOName, &out.DPOEmail, &out.DPOPhone, &out.DPOExternal,
			&out.SDAIARegistrationRef, &registered, &out.PrivacyNoticeURL,
			&out.DataRegion)
		if e == pgx.ErrNoRows {
			// A company provisioned before 0096 has no row. Reporting the
			// tenant's region and empty settings is truer than a 404: nothing
			// has been configured, which is exactly what the screen should say.
			return tx.QueryRow(ctx, `
				SELECT t.data_region::text FROM company c
				JOIN tenant t ON t.id = c.tenant_id
				WHERE c.id = $1`, scope.CompanyID).Scan(&out.DataRegion)
		}
		if registered != nil {
			out.ControllerRegisteredOn = registered.Format("2006-01-02")
		}
		return e
	})
	return out, db.Translate(err, "")
}

// SaveSettings writes them.
func (s *Service) SaveSettings(
	ctx context.Context, scope Scope, in Settings,
) (Settings, error) {
	var registered *time.Time
	if in.ControllerRegisteredOn != "" {
		d, err := time.Parse("2006-01-02", in.ControllerRegisteredOn)
		if err != nil {
			return Settings{}, errs.New(errs.CodeInvalidInput,
				"That registration date is not a date.")
		}
		registered = &d
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			INSERT INTO privacy_settings (
			  company_id, tenant_id, dpo_name, dpo_email, dpo_phone,
			  dpo_external, sdaia_registration_ref, controller_registered_on,
			  privacy_notice_url, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (company_id) DO UPDATE SET
			  dpo_name = excluded.dpo_name,
			  dpo_email = excluded.dpo_email,
			  dpo_phone = excluded.dpo_phone,
			  dpo_external = excluded.dpo_external,
			  sdaia_registration_ref = excluded.sdaia_registration_ref,
			  controller_registered_on = excluded.controller_registered_on,
			  privacy_notice_url = excluded.privacy_notice_url,
			  updated_by = excluded.updated_by`,
			scope.CompanyID, scope.TenantID, nullIfBlank(in.DPOName),
			nullIfBlank(in.DPOEmail), nullIfBlank(in.DPOPhone), in.DPOExternal,
			nullIfBlank(in.SDAIARegistrationRef), registered,
			nullIfBlank(in.PrivacyNoticeURL), scope.UserID); e != nil {
			return db.Translate(e, "Those settings could not be saved.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "privacy_settings_saved",
			EntityType: "privacy_settings", EntityID: &scope.CompanyID,
		})
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx, scope)
}

// Disclosure is what E5 requires an online shop to publish.
type Disclosure struct {
	RegistrationRef      string `json:"registration_ref,omitempty"`
	RegistrationChannel  string `json:"registration_channel,omitempty"`
	VerificationBadgeURL string `json:"verification_badge_url,omitempty"`

	ReturnPolicy    string `json:"return_policy,omitempty"`
	ReturnPolicyAr  string `json:"return_policy_ar,omitempty"`
	DeliveryTerms   string `json:"delivery_terms,omitempty"`
	DeliveryTermsAr string `json:"delivery_terms_ar,omitempty"`

	ContactEmail string `json:"contact_email,omitempty"`
	ContactPhone string `json:"contact_phone,omitempty"`
	SupportHours string `json:"support_hours,omitempty"`

	CoolingOffDays *int `json:"cooling_off_days,omitempty"`

	// Read-only, from `company`. Repeated here because the storefront screen
	// has to show whether the CR and VAT numbers are present, and they are the
	// two disclosures most often missing.
	CRNumber  string `json:"cr_number,omitempty"`
	VATNumber string `json:"vat_number,omitempty"`

	// Missing names the disclosures E5 requires that are not filled in. Empty
	// means the storefront is compliant on this axis.
	Missing []string `json:"missing"`
}

// Disclosure reads a company's storefront settings.
func (s *Service) Disclosure(
	ctx context.Context, scope Scope,
) (Disclosure, error) {
	var out Disclosure
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT coalesce(d.registration_ref, ''),
			       coalesce(d.registration_channel, ''),
			       coalesce(d.verification_badge_url, ''),
			       coalesce(d.return_policy, ''),
			       coalesce(d.return_policy_ar, ''),
			       coalesce(d.delivery_terms, ''),
			       coalesce(d.delivery_terms_ar, ''),
			       coalesce(d.contact_email, ''),
			       coalesce(d.contact_phone, ''),
			       coalesce(d.support_hours, ''),
			       d.cooling_off_days,
			       coalesce(c.cr_number, ''), coalesce(c.vat_number, '')
			FROM company c
			LEFT JOIN storefront_disclosure d ON d.company_id = c.id
			WHERE c.id = $1`, scope.CompanyID).Scan(
			&out.RegistrationRef, &out.RegistrationChannel,
			&out.VerificationBadgeURL, &out.ReturnPolicy, &out.ReturnPolicyAr,
			&out.DeliveryTerms, &out.DeliveryTermsAr, &out.ContactEmail,
			&out.ContactPhone, &out.SupportHours, &out.CoolingOffDays,
			&out.CRNumber, &out.VATNumber)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That company was not found.")
		}
		return e
	})
	if err != nil {
		return Disclosure{}, db.Translate(err, "")
	}
	out.Missing = missingDisclosures(out)
	return out, nil
}

// missingDisclosures is E5's mandatory list, checked.
//
// The Arabic copies are required and the English ones are not: E5 says the
// disclosures must render in Arabic, and a shop that has written its return
// policy only in English has not met the requirement even though the screen
// looks full.
func missingDisclosures(d Disclosure) []string {
	missing := []string{}
	for _, c := range []struct {
		filled bool
		what   string
	}{
		{d.CRNumber != "", "cr_number"},
		{d.RegistrationRef != "", "registration_ref"},
		{d.ReturnPolicyAr != "", "return_policy_ar"},
		{d.DeliveryTermsAr != "", "delivery_terms_ar"},
		{d.ContactPhone != "" || d.ContactEmail != "", "contact"},
		{d.CoolingOffDays != nil, "cooling_off_days"},
	} {
		if !c.filled {
			missing = append(missing, c.what)
		}
	}
	return missing
}

// SaveDisclosure writes them.
//
// The cooling-off period is validated against the registry floor rather than
// against a constant: SA.ECOMMERCE.COOLING_OFF_DAYS is 14 today, and a shop may
// be MORE generous but not less.
func (s *Service) SaveDisclosure(
	ctx context.Context, scope Scope, in Disclosure,
) (Disclosure, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.CoolingOffDays != nil {
			country, e := countryOf(ctx, tx, scope.CompanyID)
			if e != nil {
				return e
			}
			floor, e := s.registry.Int(ctx, registry.Query{
				Key:      "SA.ECOMMERCE.COOLING_OFF_DAYS",
				Country:  country,
				AsOf:     time.Now().UTC(),
				TenantID: scope.TenantID,
				Tx:       tx,
			}, "days")
			// A rule that cannot be resolved leaves the shop's own figure
			// alone. Refusing the save because the registry is unverified
			// would block a shop from recording a MORE generous window than
			// the law requires, which is not what the rule is protecting.
			if e == nil && *in.CoolingOffDays < int(floor) {
				return errs.New(errs.CodeComplianceBlocked,
					"The return window cannot be shorter than the law "+
						"allows. A longer one is your choice to offer.")
			}
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO storefront_disclosure (
			  company_id, tenant_id, registration_ref, registration_channel,
			  verification_badge_url, return_policy, return_policy_ar,
			  delivery_terms, delivery_terms_ar, contact_email, contact_phone,
			  support_hours, cooling_off_days, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (company_id) DO UPDATE SET
			  registration_ref = excluded.registration_ref,
			  registration_channel = excluded.registration_channel,
			  verification_badge_url = excluded.verification_badge_url,
			  return_policy = excluded.return_policy,
			  return_policy_ar = excluded.return_policy_ar,
			  delivery_terms = excluded.delivery_terms,
			  delivery_terms_ar = excluded.delivery_terms_ar,
			  contact_email = excluded.contact_email,
			  contact_phone = excluded.contact_phone,
			  support_hours = excluded.support_hours,
			  cooling_off_days = excluded.cooling_off_days,
			  updated_by = excluded.updated_by`,
			scope.CompanyID, scope.TenantID, nullIfBlank(in.RegistrationRef),
			nullIfBlank(in.RegistrationChannel),
			nullIfBlank(in.VerificationBadgeURL), nullIfBlank(in.ReturnPolicy),
			nullIfBlank(in.ReturnPolicyAr), nullIfBlank(in.DeliveryTerms),
			nullIfBlank(in.DeliveryTermsAr), nullIfBlank(in.ContactEmail),
			nullIfBlank(in.ContactPhone), nullIfBlank(in.SupportHours),
			in.CoolingOffDays, scope.UserID); e != nil {
			return db.Translate(e, "Those disclosures could not be saved.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "storefront_disclosure_saved",
			EntityType: "storefront_disclosure", EntityID: &scope.CompanyID,
		})
	})
	if err != nil {
		return Disclosure{}, err
	}
	return s.Disclosure(ctx, scope)
}

// Subprocessor is one vendor the platform uses on every tenant's behalf.
type Subprocessor struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Purpose        string    `json:"purpose"`
	Country        string    `json:"country"`
	DataCategories string    `json:"data_categories"`
	Safeguard      string    `json:"safeguard,omitempty"`
	DPASignedOn    string    `json:"dpa_signed_on,omitempty"`
	IsActive       bool      `json:"is_active"`
}

// Subprocessors is the platform's own disclosure, readable by any tenant.
//
// A tenant's RoPA has to name who else touches the data, and the answer is not
// theirs to compile: it is the platform operator's, and E4.1's last bullet
// makes it the platform's obligation to keep.
func (s *Service) Subprocessors(
	ctx context.Context, scope Scope,
) ([]Subprocessor, error) {
	out := []Subprocessor{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, name, purpose, country, data_categories,
			       coalesce(safeguard, ''), dpa_signed_on, is_active
			FROM subprocessor
			WHERE is_active
			ORDER BY name`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p Subprocessor
			var signed *time.Time
			if e := rows.Scan(&p.ID, &p.Name, &p.Purpose, &p.Country,
				&p.DataCategories, &p.Safeguard, &signed,
				&p.IsActive); e != nil {
				return e
			}
			if signed != nil {
				p.DPASignedOn = signed.Format("2006-01-02")
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveSubprocessor is the platform owner's write.
func (s *Service) SaveSubprocessor(
	ctx context.Context, in Subprocessor,
) (Subprocessor, error) {
	if strings.TrimSpace(in.Name) == "" ||
		strings.TrimSpace(in.Purpose) == "" {
		return Subprocessor{}, errs.New(errs.CodeInvalidInput,
			"Name the vendor and what they are used for.")
	}
	var signed *time.Time
	if in.DPASignedOn != "" {
		d, err := time.Parse("2006-01-02", in.DPASignedOn)
		if err != nil {
			return Subprocessor{}, errs.New(errs.CodeInvalidInput,
				"That agreement date is not a date.")
		}
		signed = &d
	}

	var id uuid.UUID
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO subprocessor (
			  name, purpose, country, data_categories, safeguard,
			  dpa_signed_on, is_active)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (lower(name)) DO UPDATE SET
			  purpose = excluded.purpose,
			  country = excluded.country,
			  data_categories = excluded.data_categories,
			  safeguard = excluded.safeguard,
			  dpa_signed_on = excluded.dpa_signed_on,
			  is_active = excluded.is_active
			RETURNING id`,
			strings.TrimSpace(in.Name), strings.TrimSpace(in.Purpose),
			strings.ToLower(in.Country), in.DataCategories,
			nullIfBlank(in.Safeguard), signed, in.IsActive).Scan(&id)
	})
	if err != nil {
		return Subprocessor{}, db.Translate(err,
			"That sub-processor could not be saved.")
	}
	in.ID = id
	return in, nil
}
