package provisioning

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The seven steps of blueprint A5, in order.
const (
	StepBusinessInfo    = "business_info"
	StepStores          = "stores"
	StepTax             = "tax"
	StepEmployees       = "employees"
	StepHardware        = "hardware"
	StepOpeningBalances = "opening_balances"
	StepFinished        = "finished"
)

var stepOrder = []string{
	StepBusinessInfo, StepStores, StepTax,
	StepEmployees, StepHardware, StepOpeningBalances, StepFinished,
}

// Progress is where a tenant has reached in setup.
type Progress struct {
	CurrentStep    string          `json:"current_step"`
	CompletedSteps []string        `json:"completed_steps"`
	StepData       json.RawMessage `json:"step_data"`
	Finished       bool            `json:"finished"`

	// NextStep is included so the client never has to know the order. Moving a
	// step or inserting one becomes a server change alone.
	NextStep string `json:"next_step,omitempty"`
}

// GetProgress returns the tenant's onboarding state.
func (s *Service) GetProgress(ctx context.Context) (Progress, error) {
	a := actor.From(ctx)
	var p Progress
	var completed []string
	var finishedAt *string

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_step::text, completed_steps::text[], step_data,
			       completed_at::text
			FROM onboarding_progress WHERE tenant_id = $1`, a.TenantID).
			Scan(&p.CurrentStep, &completed, &p.StepData, &finishedAt)
	})
	if err != nil {
		return Progress{}, db.Translate(err, "Setup has not been started for this business.")
	}

	p.CompletedSteps = completed
	p.Finished = finishedAt != nil
	p.NextStep = nextStep(p.CurrentStep)
	return p, nil
}

func nextStep(current string) string {
	for i, s := range stepOrder {
		if s == current && i+1 < len(stepOrder) {
			return stepOrder[i+1]
		}
	}
	return ""
}

// SaveStep records a step's answers without committing them to their real
// tables, so a half-finished step survives the Owner closing the browser.
//
// Validation happens at CompleteStep, not here. A store with no name yet must
// be resumable rather than rejected — an Owner filling in a form on a phone
// between customers should not lose their work to a validation error.
func (s *Service) SaveStep(ctx context.Context, step string, data json.RawMessage) error {
	if !isKnownStep(step) {
		return errs.Newf(errs.CodeInvalidInput, "%q is not a setup step.", step)
	}
	if step == StepFinished {
		return errs.New(errs.CodeInvalidInput,
			"The final step is completed, not saved.")
	}
	if !json.Valid(data) {
		return errs.New(errs.CodeInvalidInput, "That step's answers could not be read.")
	}

	a := actor.From(ctx)
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE onboarding_progress
			SET step_data = jsonb_set(step_data, ARRAY[$2], $3::jsonb, true)
			WHERE tenant_id = $1 AND completed_at IS NULL`,
			a.TenantID, step, data)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"Setup is already finished for this business.")
		}
		return nil
	})
	if err != nil {
		if errs.As(err) != nil {
			return err
		}
		return db.Translate(err, "")
	}
	return nil
}

// CompleteStep validates a step and advances.
//
// Steps must be completed in order. Allowing a jump would let opening balances
// be entered before the chart of accounts exists, and the resulting failure
// would surface as a confusing error rather than a missing prerequisite.
func (s *Service) CompleteStep(ctx context.Context, step string) (Progress, error) {
	if !isKnownStep(step) {
		return Progress{}, errs.Newf(errs.CodeInvalidInput, "%q is not a setup step.", step)
	}

	current, err := s.GetProgress(ctx)
	if err != nil {
		return Progress{}, err
	}
	if current.Finished {
		return Progress{}, errs.New(errs.CodeConflict,
			"Setup is already finished for this business.")
	}
	if step != current.CurrentStep {
		return Progress{}, errs.Newf(errs.CodeConflict,
			"The next step is %q, not %q.", current.CurrentStep, step)
	}

	if err := s.validateStep(step, current.StepData); err != nil {
		return Progress{}, err
	}

	next := nextStep(step)
	a := actor.From(ctx)

	err = s.pool.Tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE onboarding_progress
			SET current_step = $2::onboarding_step,
			    completed_steps = array_append(completed_steps, $3::onboarding_step),
			    completed_at = CASE WHEN $2 = 'finished' THEN now() ELSE completed_at END
			WHERE tenant_id = $1`, a.TenantID, next, step)
		return err
	})
	if err != nil {
		return Progress{}, db.Translate(err, "")
	}

	return s.GetProgress(ctx)
}

// validateStep checks a step's answers before allowing progress.
//
// Only the fields that would break something downstream are required. An
// over-strict wizard is abandoned; blueprint A5's whole point is that a
// non-technical shop owner completes it alone.
func (s *Service) validateStep(step string, all json.RawMessage) error {
	var payload map[string]json.RawMessage
	if len(all) > 0 {
		_ = json.Unmarshal(all, &payload)
	}

	raw, present := payload[step]
	if !present && step != StepHardware && step != StepOpeningBalances {
		return errs.Newf(errs.CodeInvalidInput,
			"Fill in the %s step before continuing.", humanStep(step))
	}

	switch step {
	case StepBusinessInfo:
		var v struct {
			LegalName     string `json:"legal_name"`
			Country       string `json:"country"`
			BaseCurrency  string `json:"base_currency"`
			VATRegistered bool   `json:"vat_registered"`
			VATNumber     string `json:"vat_number"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return errs.New(errs.CodeInvalidInput, "Those business details could not be read.")
		}
		e := errs.Validation("Some business details are missing.")
		bad := false
		if strings.TrimSpace(v.LegalName) == "" {
			e.WithField("legal_name", "Enter the registered legal name of the business.")
			bad = true
		}
		if len(v.Country) != 2 {
			e.WithField("country", "Choose the country the business operates in.")
			bad = true
		}
		if len(v.BaseCurrency) != 3 {
			e.WithField("base_currency", "Choose the currency you keep your books in.")
			bad = true
		}
		// Mirrors the database constraint, so the Owner sees a helpful message
		// here rather than a constraint violation later.
		if v.VATRegistered && strings.TrimSpace(v.VATNumber) == "" {
			e.WithField("vat_number",
				"Enter your VAT registration number. It appears on every tax invoice you issue.")
			bad = true
		}
		if bad {
			return e
		}

	case StepStores:
		var v struct {
			Stores []StoreAnswer `json:"stores"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return errs.New(errs.CodeInvalidInput, "Those store details could not be read.")
		}
		if len(v.Stores) == 0 {
			return errs.New(errs.CodeInvalidInput,
				"Add at least one store. Every sale is recorded against a store.")
		}
		seen := map[string]bool{}
		for i, st := range v.Stores {
			if strings.TrimSpace(st.Name) == "" {
				return errs.Newf(errs.CodeInvalidInput, "Store %d has no name.", i+1)
			}
			code := strings.ToUpper(strings.TrimSpace(st.Code))
			if code == "" {
				return errs.Newf(errs.CodeInvalidInput,
					"Store %d has no short code. It appears in invoice numbers, "+
						"for example INV-%s-000001.", i+1, "RYD")
			}
			if seen[code] {
				return errs.Newf(errs.CodeInvalidInput,
					"Two stores share the code %q. Codes must be unique because "+
						"they identify the store in every document number.", code)
			}
			seen[code] = true

			if err := st.validateAddress(i + 1); err != nil {
				return err
			}
		}

	case StepTax:
		// Saudi tenants have their tax configuration loaded from the regulatory
		// registry rather than typed in, so there is nothing to require here.
		// Confirming the step is the Owner acknowledging what was loaded.

	case StepEmployees:
		// Optional. A single-person shop is a real customer, and the Owner
		// already exists.

	case StepHardware, StepOpeningBalances:
		// Optional. Hardware can be paired later from the terminal, and a new
		// business legitimately starts with no opening balances.
	}

	return nil
}

func isKnownStep(s string) bool {
	for _, k := range stepOrder {
		if k == s {
			return true
		}
	}
	return false
}

func humanStep(s string) string {
	switch s {
	case StepBusinessInfo:
		return "business information"
	case StepStores:
		return "store setup"
	case StepTax:
		return "tax configuration"
	case StepEmployees:
		return "employees"
	case StepHardware:
		return "hardware setup"
	case StepOpeningBalances:
		return "opening balances"
	default:
		return s
	}
}

// CommitBusinessInfo creates the company record from the wizard's answers.
//
// Separate from CompleteStep because it writes to real tables: the wizard's
// JSONB is scratch space, and this is the point where the answers become the
// company that owns the books, the VAT registration and the ZATCA sequence.
func (s *Service) CommitBusinessInfo(ctx context.Context) (uuid.UUID, error) {
	progress, err := s.GetProgress(ctx)
	if err != nil {
		return uuid.Nil, err
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(progress.StepData, &payload); err != nil {
		return uuid.Nil, errs.New(errs.CodeInvalidInput, "Setup answers could not be read.")
	}
	raw, ok := payload[StepBusinessInfo]
	if !ok {
		return uuid.Nil, errs.New(errs.CodeInvalidInput,
			"Fill in the business information step first.")
	}

	var v struct {
		LegalName     string `json:"legal_name"`
		LegalNameAr   string `json:"legal_name_ar"`
		TradeName     string `json:"trade_name"`
		Country       string `json:"country"`
		BaseCurrency  string `json:"base_currency"`
		Timezone      string `json:"timezone"`
		CRNumber      string `json:"cr_number"`
		VATRegistered bool   `json:"vat_registered"`
		VATNumber     string `json:"vat_number"`
		ZATCAWave     string `json:"zatca_wave"`
		ZATCADeadline string `json:"zatca_deadline"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return uuid.Nil, errs.New(errs.CodeInvalidInput,
			"Those business details could not be read.")
	}
	if v.Timezone == "" {
		v.Timezone = "Asia/Riyadh"
	}

	// The ZATCA obligation belongs to the Tax step, which is where UI spec §6
	// puts it and where an Owner expects to be asked. It is read from there in
	// preference to the business step, which still answers so that answers
	// recorded before the wizard existed are not dropped on the floor.
	//
	// `zatca_deadline` had no home at all until now: 0002 has carried the
	// column since the beginning and this commit never wrote it, so a shop
	// could be asked for the date ZATCA gave them and have it silently
	// discarded — which is worse than not asking, because they would believe
	// the product knew.
	if tax, ok := payload[StepTax]; ok {
		var t struct {
			ZATCAWave     string `json:"zatca_wave"`
			ZATCADeadline string `json:"zatca_deadline"`
		}
		if err := json.Unmarshal(tax, &t); err == nil {
			if strings.TrimSpace(t.ZATCAWave) != "" {
				v.ZATCAWave = t.ZATCAWave
			}
			if strings.TrimSpace(t.ZATCADeadline) != "" {
				v.ZATCADeadline = t.ZATCADeadline
			}
		}
	}

	a := actor.From(ctx)
	var companyID uuid.UUID

	err = s.pool.Tx(ctx, func(tx pgx.Tx) error {
		// The plan ceiling is checked here rather than trusted from the client,
		// which cannot be relied on to know it.
		var existing, ceiling int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM company WHERE tenant_id = $1`, a.TenantID).
			Scan(&existing); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT max_companies FROM tenant_limit WHERE tenant_id = $1`, a.TenantID).
			Scan(&ceiling); err != nil {
			return err
		}
		if existing >= ceiling {
			return errs.Newf(errs.CodeLimitReached,
				"Your plan allows %d companies and you already have %d.", ceiling, existing)
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO company
			  (tenant_id, legal_name, legal_name_ar, trade_name, country,
			   base_currency, timezone, cr_number, vat_registered, vat_number,
			   zatca_wave, zatca_deadline)
			VALUES ($1,$2,$3,$4,lower($5),upper($6),$7,$8,$9,$10,$11,$12::date)
			RETURNING id`,
			a.TenantID, v.LegalName, nullIfBlank(v.LegalNameAr), nullIfBlank(v.TradeName),
			v.Country, v.BaseCurrency, v.Timezone, nullIfBlank(v.CRNumber),
			v.VATRegistered, nullIfBlank(v.VATNumber),
			nullIfBlank(v.ZATCAWave), nullIfBlank(v.ZATCADeadline)).
			Scan(&companyID); err != nil {
			return err
		}

		return SeedChartOfAccounts(ctx, tx, a.TenantID, companyID)
	})
	if err != nil {
		if errs.As(err) != nil {
			return uuid.Nil, err
		}
		return uuid.Nil, db.Translate(err, "")
	}
	return companyID, nil
}

// StoreAnswer is one branch as the wizard collects it.
//
// The address is the Saudi National Address, because that is what ZATCA asks
// for on the face of every invoice — BR-KSA-09 names all six parts and links to
// https://splonline.com.sa/en/national-address-1/ directly. Collecting it here
// rather than later is deliberate: a shop that finishes setup without it can
// take money and cannot issue a compliant invoice for it, which is the worst
// order to discover the problem in.
type StoreAnswer struct {
	Code string `json:"code"`
	Name string `json:"name"`

	Street           string `json:"street"`
	BuildingNumber   string `json:"building_number"`
	AdditionalNumber string `json:"additional_number"`
	District         string `json:"district"`
	City             string `json:"city"`
	PostalCode       string `json:"postal_code"`
	CountryCode      string `json:"country_code"`
}

var (
	fourDigitNumber = regexp.MustCompile(`^[0-9]{4}$`)
	fiveDigitNumber = regexp.MustCompile(`^[0-9]{5}$`)
	twoLetterCode   = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

// validateAddress applies BR-KSA-09, BR-KSA-37 and BR-KSA-66.
//
// Reported per field, and each sentence says what the value is FOR. "Invalid
// building number" tells somebody nothing; "must be exactly 4 digits, from your
// National Address" tells them where to look it up.
func (a StoreAnswer) validateAddress(position int) error {
	e := errs.Newf(errs.CodeInvalidInput,
		"Store %d needs its National Address before it can issue invoices.", position)

	for _, f := range []struct{ field, value, why string }{
		{"street", a.Street, "Street name, as it appears on your National Address."},
		{"district", a.District, "District, as it appears on your National Address."},
		{"city", a.City, "City, as it appears on your National Address."},
	} {
		if strings.TrimSpace(f.value) == "" {
			e.WithField(f.field, f.why)
		}
	}
	if !fourDigitNumber.MatchString(strings.TrimSpace(a.BuildingNumber)) {
		e.WithField("building_number",
			"The building number is exactly 4 digits, for example 2322.")
	}
	if !fiveDigitNumber.MatchString(strings.TrimSpace(a.PostalCode)) {
		e.WithField("postal_code",
			"The postal code is exactly 5 digits, for example 23333.")
	}
	// Optional, but wrong is worse than absent.
	if v := strings.TrimSpace(a.AdditionalNumber); v != "" && !fourDigitNumber.MatchString(v) {
		e.WithField("additional_number",
			"The additional number is 4 digits, or leave it empty.")
	}
	if v := strings.TrimSpace(a.CountryCode); v != "" && !twoLetterCode.MatchString(v) {
		e.WithField("country_code", "Use the two-letter country code, such as SA.")
	}

	if len(e.Fields) > 0 {
		return e
	}
	return nil
}

// CommitStores creates the branches the wizard collected.
//
// Nothing did this before. The wizard asked for stores, wrote the answers into
// its scratch JSONB, and stopped — so a shop finished setup with no store at
// all, and since every sale is recorded against one, it could not trade. The
// only stores that ever existed came from the development seeder.
//
// Idempotent by code within the company, so a retried request does not create a
// second branch with the same code — the code appears in every document number
// and a duplicate would corrupt the numbering.
func (s *Service) CommitStores(ctx context.Context, companyID uuid.UUID) ([]uuid.UUID, error) {
	progress, err := s.GetProgress(ctx)
	if err != nil {
		return nil, err
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(progress.StepData, &payload); err != nil {
		return nil, errs.New(errs.CodeInvalidInput, "Setup answers could not be read.")
	}
	raw, ok := payload[StepStores]
	if !ok {
		return nil, errs.New(errs.CodeInvalidInput, "Fill in the store step first.")
	}

	var v struct {
		Stores []StoreAnswer `json:"stores"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errs.New(errs.CodeInvalidInput, "Those store details could not be read.")
	}
	if len(v.Stores) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"Add at least one store. Every sale is recorded against a store.")
	}
	for i, st := range v.Stores {
		if err := st.validateAddress(i + 1); err != nil {
			return nil, err
		}
	}

	a := actor.From(ctx)
	var created []uuid.UUID

	err = s.pool.Tx(ctx, func(tx pgx.Tx) error {
		// The plan ceiling, checked here rather than trusted from the client.
		var existing, ceiling int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM store WHERE company_id = $1`, companyID).
			Scan(&existing); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT max_stores FROM tenant_limit WHERE tenant_id = $1`, a.TenantID).
			Scan(&ceiling); err != nil {
			return err
		}
		if existing+len(v.Stores) > ceiling {
			return errs.Newf(errs.CodeLimitReached,
				"Your plan allows %d stores and this would make %d.",
				ceiling, existing+len(v.Stores))
		}

		created = created[:0]
		for _, st := range v.Stores {
			country := strings.ToUpper(strings.TrimSpace(st.CountryCode))
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO store
				  (tenant_id, company_id, code, name,
				   street, building_number, additional_number,
				   district, city, postal_code, country_code)
				VALUES ($1,$2,upper($3),$4,$5,$6,$7,$8,$9,$10,
				        coalesce(nullif($11,''), upper((SELECT country FROM company WHERE id = $2))))
				ON CONFLICT (company_id, code) DO UPDATE
				  SET name              = excluded.name,
				      street            = excluded.street,
				      building_number   = excluded.building_number,
				      additional_number = excluded.additional_number,
				      district          = excluded.district,
				      city              = excluded.city,
				      postal_code       = excluded.postal_code,
				      country_code      = excluded.country_code
				RETURNING id`,
				a.TenantID, companyID,
				strings.TrimSpace(st.Code), strings.TrimSpace(st.Name),
				strings.TrimSpace(st.Street),
				strings.TrimSpace(st.BuildingNumber),
				nullIfBlank(st.AdditionalNumber),
				strings.TrimSpace(st.District),
				strings.TrimSpace(st.City),
				strings.TrimSpace(st.PostalCode),
				country).Scan(&id); err != nil {
				return err
			}
			created = append(created, id)
		}
		return nil
	})
	if err != nil {
		if errs.As(err) != nil {
			return nil, err
		}
		return nil, db.Translate(err, "Those stores could not be created.")
	}
	return created, nil
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
