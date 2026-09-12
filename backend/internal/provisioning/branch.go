// Branches after setup, blueprint I1.
//
// A branch could be created exactly once, by the setup wizard, and never
// touched again. CommitStores reads the wizard's scratch JSONB rather than a
// request, carries only the seven Saudi National Address fields, and knows
// nothing of a branch's Arabic name, its phone or whether it is still trading.
// So a shop could not open a second branch, correct a postal code it mistyped,
// record that it had moved, or close a branch it no longer ran.
//
// The postal code matters more than it sounds. sales/document.go refuses to
// issue an invoice when the selling branch's address is incomplete or
// malformed — BR-KSA-09 for street, district and city, BR-KSA-37 for the
// four-digit building number, BR-KSA-66 for the five-digit postal code. A shop
// that finished setup with a typo could not invoice at all, and had nowhere to
// go and fix it.
//
// # Closing, not deleting
//
// A branch is deactivated, never removed. Its name is on every invoice it ever
// issued, its code is inside every document number those invoices carry, and
// the ZATCA chain for its terminals hangs off it. `is_active` is what "we do
// not trade here any more" means; the row stays.
package provisioning

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Branch is one of a company's trading locations, as a settings screen sees it.
//
// CanInvoice is not stored: it is the same question sales/document.go asks
// before it will issue anything, answered here so the branch list can say which
// branches cannot yet trade instead of leaving somebody to discover it at the
// till. Incomplete says which fields are the reason.
type Branch struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar"`
	Phone    string    `json:"phone"`
	IsActive bool      `json:"is_active"`

	Street           string `json:"street"`
	BuildingNumber   string `json:"building_number"`
	AdditionalNumber string `json:"additional_number"`
	District         string `json:"district"`
	City             string `json:"city"`
	PostalCode       string `json:"postal_code"`
	CountryCode      string `json:"country_code"`

	// EffectiveCountryCode is what actually reaches BT-40 on an invoice: the
	// branch's own country when it has set one, and the company's when it has
	// not. Kept apart from CountryCode so a form shows the field as stored —
	// blank is blank — while the screen can still say which country will be
	// printed. Filling the input with the fallback would persist it on the next
	// save, quietly turning an inherited value into a stated one.
	EffectiveCountryCode string `json:"effective_country_code"`

	CanInvoice bool     `json:"can_invoice"`
	Incomplete []string `json:"incomplete"`
}

// invoiceReadiness answers the same question sales/document.go asks of a
// seller, against the columns a branch form writes.
//
// Deliberately a second reading of the same rules rather than a call into
// sales: that package builds a UBL seller out of a posted invoice and has no
// shape for "a branch nobody has sold from yet". The rules it duplicates are
// three named ZATCA business rules that do not move, and a test asserts both
// agree.
//
// The country fallback is part of that agreement, and was missing when this was
// first written. sales/document.go takes the COMPANY's country when the branch
// has set none, so a branch with a blank country_code invoices perfectly well —
// and a readiness check without the fallback told every such branch it could
// not trade. The seeded development branch is exactly that shape, which is how
// the mistake surfaced: the screen said "cannot invoice" about a branch with a
// hundred invoices already behind it.
func (b *Branch) invoiceReadiness(companyCountry string) {
	b.EffectiveCountryCode = strings.ToUpper(strings.TrimSpace(b.CountryCode))
	if b.EffectiveCountryCode == "" {
		b.EffectiveCountryCode = strings.ToUpper(strings.TrimSpace(companyCountry))
	}

	b.Incomplete = []string{}
	for _, f := range []struct{ field, value string }{
		{"street", b.Street},
		{"district", b.District},
		{"city", b.City},
		{"country_code", b.EffectiveCountryCode},
	} {
		if strings.TrimSpace(f.value) == "" {
			b.Incomplete = append(b.Incomplete, f.field)
		}
	}
	if !fourDigitNumber.MatchString(strings.TrimSpace(b.BuildingNumber)) {
		b.Incomplete = append(b.Incomplete, "building_number")
	}
	if !fiveDigitNumber.MatchString(strings.TrimSpace(b.PostalCode)) {
		b.Incomplete = append(b.Incomplete, "postal_code")
	}
	b.CanInvoice = len(b.Incomplete) == 0
}

const branchColumns = `
	id, code, name, coalesce(name_ar, ''), coalesce(phone, ''), is_active,
	coalesce(street, ''), coalesce(building_number, ''),
	coalesce(additional_number, ''), coalesce(district, ''),
	coalesce(city, ''), coalesce(postal_code, ''), coalesce(country_code, '')`

func scanBranch(row pgx.Row, companyCountry string) (Branch, error) {
	var b Branch
	err := row.Scan(&b.ID, &b.Code, &b.Name, &b.NameAr, &b.Phone, &b.IsActive,
		&b.Street, &b.BuildingNumber, &b.AdditionalNumber, &b.District,
		&b.City, &b.PostalCode, &b.CountryCode)
	if err != nil {
		return Branch{}, err
	}
	b.invoiceReadiness(companyCountry)
	return b, nil
}

// companyCountryOf reads the fallback country once per call rather than joining
// it onto every branch row: it is one value for the whole list, and the
// fallback is needed whether or not any branch has left its own country blank.
func companyCountryOf(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var country string
	err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, companyID).Scan(&country)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return country, err
}

// Branches lists a company's branches, open ones first.
//
// Closed branches are listed rather than filtered, for the reason GET /stores
// lists them: a shift worked in a branch that has since closed still happened
// there, and a settings screen that hid it could not reopen it.
func (s *Service) Branches(
	ctx context.Context, tenantID, companyID uuid.UUID,
) ([]Branch, error) {
	out := []Branch{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		country, err := companyCountryOf(ctx, tx, companyID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT`+branchColumns+`
			 FROM store WHERE company_id = $1
			 ORDER BY is_active DESC, name`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBranch(rows, country)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// BranchChange is a branch as a form submits it.
//
// Pointers for the same reason BusinessChange uses them: a blank phone is
// "remove the phone" and an absent phone is "I was not editing that".
type BranchChange struct {
	Code     *string `json:"code"`
	Name     *string `json:"name"`
	NameAr   *string `json:"name_ar"`
	Phone    *string `json:"phone"`
	IsActive *bool   `json:"is_active"`

	Street           *string `json:"street"`
	BuildingNumber   *string `json:"building_number"`
	AdditionalNumber *string `json:"additional_number"`
	District         *string `json:"district"`
	City             *string `json:"city"`
	PostalCode       *string `json:"postal_code"`
	CountryCode      *string `json:"country_code"`
}

// validateBranch checks the fields present, in the vocabulary of the form.
//
// The National Address fields are validated when SUPPLIED but not required
// here, which is the difference between this and CommitStores. A shop opening a
// branch next week knows its name before it knows its postal code, and refusing
// to record the branch until the address is complete would leave them unable to
// start. What the address gates is invoicing, and Branch.CanInvoice says so on
// every read — so the branch exists, the list shows it cannot yet trade, and
// the till refuses with a sentence rather than the settings screen refusing
// with a form error.
func validateBranch(ch BranchChange, creating bool) error {
	e := errs.New(errs.CodeInvalidInput, "That branch could not be saved.")

	if creating && ch.Code == nil {
		e.WithField("code",
			"A branch needs a short code; it appears in every document number it issues.")
	}
	if ch.Code != nil {
		v := strings.ToUpper(strings.TrimSpace(*ch.Code))
		switch {
		case v == "":
			e.WithField("code",
				"A branch needs a short code; it appears in every document number it issues.")
		case len(v) > 16:
			e.WithField("code", "A branch code is at most 16 characters.")
		case !branchCodePattern.MatchString(v):
			e.WithField("code", "A branch code is letters, digits and dashes.")
		}
	}
	if creating && ch.Name == nil {
		e.WithField("name", "A branch needs a name.")
	}
	if ch.Name != nil && strings.TrimSpace(*ch.Name) == "" {
		e.WithField("name", "A branch needs a name.")
	}

	// Supplied-but-wrong is reported; supplied-as-blank is accepted as
	// "not known yet" and shows up as an incomplete address instead.
	if v := trimPtr(ch.BuildingNumber); v != "" && !fourDigitNumber.MatchString(v) {
		e.WithField("building_number",
			"The building number is exactly 4 digits, for example 2322.")
	}
	if v := trimPtr(ch.PostalCode); v != "" && !fiveDigitNumber.MatchString(v) {
		e.WithField("postal_code",
			"The postal code is exactly 5 digits, for example 23333.")
	}
	if v := trimPtr(ch.AdditionalNumber); v != "" && !fourDigitNumber.MatchString(v) {
		e.WithField("additional_number",
			"The additional number is 4 digits, or leave it empty.")
	}
	if v := trimPtr(ch.CountryCode); v != "" && !twoLetterCode.MatchString(v) {
		e.WithField("country_code", "Use the two-letter country code, such as SA.")
	}

	if len(e.Fields) > 0 {
		return e
	}
	return nil
}

// A branch code goes into every document number the branch issues, so it is
// restricted to characters that survive a document number, a filename and a
// ZATCA XML attribute unchanged.
var branchCodePattern = regexp.MustCompile(`^[A-Z0-9-]+$`)

func trimPtr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// OpenBranch adds a branch to a company.
func (s *Service) OpenBranch(
	ctx context.Context, tenantID, companyID uuid.UUID, ch BranchChange,
) (Branch, error) {
	if err := validateBranch(ch, true); err != nil {
		return Branch{}, err
	}

	var out Branch
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// The plan ceiling, counted here rather than trusted from the client.
		// Unlike CommitStores this is one branch, so the arithmetic is a
		// comparison rather than a sum, and it cannot mistake an amendment for
		// an addition.
		var existing, ceiling int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM store WHERE company_id = $1`, companyID).
			Scan(&existing); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT max_stores FROM tenant_limit WHERE tenant_id = $1`, tenantID).
			Scan(&ceiling); err != nil {
			return err
		}
		if existing >= ceiling {
			return errs.Newf(errs.CodeLimitReached,
				"Your plan allows %d branches and you already have %d.",
				ceiling, existing)
		}

		var taken bool
		if err := tx.QueryRow(ctx,
			`SELECT exists(SELECT 1 FROM store WHERE company_id = $1 AND code = upper($2))`,
			companyID, trimPtr(ch.Code)).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return errs.Newf(errs.CodeConflict,
				"A branch with the code %s already exists.", strings.ToUpper(trimPtr(ch.Code)))
		}

		country, err := companyCountryOf(ctx, tx, companyID)
		if err != nil {
			return err
		}

		out, err = scanBranch(tx.QueryRow(ctx, `
			INSERT INTO store
			  (tenant_id, company_id, code, name, name_ar, phone, is_active,
			   street, building_number, additional_number, district, city,
			   postal_code, country_code)
			VALUES ($1, $2, upper($3), $4, $5, $6, coalesce($7, true),
			        $8, $9, $10, $11, $12, $13,
			        coalesce(nullif(upper($14), ''),
			                 upper((SELECT country FROM company WHERE id = $2))))
			RETURNING`+branchColumns,
			tenantID, companyID, trimPtr(ch.Code), trimPtr(ch.Name),
			nullIfBlank(trimPtr(ch.NameAr)), nullIfBlank(trimPtr(ch.Phone)),
			ch.IsActive,
			nullIfBlank(trimPtr(ch.Street)), nullIfBlank(trimPtr(ch.BuildingNumber)),
			nullIfBlank(trimPtr(ch.AdditionalNumber)), nullIfBlank(trimPtr(ch.District)),
			nullIfBlank(trimPtr(ch.City)), nullIfBlank(trimPtr(ch.PostalCode)),
			trimPtr(ch.CountryCode)), country)
		return err
	})
	if err != nil {
		if errs.As(err) != nil {
			return Branch{}, err
		}
		return Branch{}, db.Translate(err, "")
	}
	return out, nil
}

// AmendBranch changes a branch that already exists, including closing and
// reopening it.
func (s *Service) AmendBranch(
	ctx context.Context, tenantID, companyID, branchID uuid.UUID, ch BranchChange,
) (Branch, error) {
	if err := validateBranch(ch, false); err != nil {
		return Branch{}, err
	}

	var out Branch
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Confined to the company named in the request as well as to the
		// tenant. RLS keeps another tenant out; this keeps a caller from
		// amending a branch of a sister company they can see but did not ask
		// about.
		var found bool
		if err := tx.QueryRow(ctx,
			`SELECT exists(SELECT 1 FROM store WHERE id = $1 AND company_id = $2)`,
			branchID, companyID).Scan(&found); err != nil {
			return err
		}
		if !found {
			return errs.New(errs.CodeNotFound, "That branch was not found.")
		}

		if ch.Code != nil {
			var taken bool
			if err := tx.QueryRow(ctx, `
				SELECT exists(SELECT 1 FROM store
				              WHERE company_id = $1 AND code = upper($2) AND id <> $3)`,
				companyID, trimPtr(ch.Code), branchID).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return errs.Newf(errs.CodeConflict,
					"A branch with the code %s already exists.",
					strings.ToUpper(trimPtr(ch.Code)))
			}
		}

		set := map[string]any{}
		if ch.Code != nil {
			set["code"] = strings.ToUpper(trimPtr(ch.Code))
		}
		if ch.Name != nil {
			set["name"] = trimPtr(ch.Name)
		}
		if ch.NameAr != nil {
			set["name_ar"] = nullIfBlank(trimPtr(ch.NameAr))
		}
		if ch.Phone != nil {
			set["phone"] = nullIfBlank(trimPtr(ch.Phone))
		}
		if ch.IsActive != nil {
			set["is_active"] = *ch.IsActive
		}
		for _, f := range []struct {
			col string
			ptr *string
		}{
			{"street", ch.Street},
			{"building_number", ch.BuildingNumber},
			{"additional_number", ch.AdditionalNumber},
			{"district", ch.District},
			{"city", ch.City},
			{"postal_code", ch.PostalCode},
		} {
			if f.ptr != nil {
				set[f.col] = nullIfBlank(trimPtr(f.ptr))
			}
		}
		if ch.CountryCode != nil {
			set["country_code"] = nullIfBlank(strings.ToUpper(trimPtr(ch.CountryCode)))
		}

		if len(set) > 0 {
			cols := make([]string, 0, len(set))
			args := make([]any, 0, len(set)+1)
			for c, v := range set {
				args = append(args, v)
				cols = append(cols, c+" = $"+strconv.Itoa(len(args)))
			}
			args = append(args, branchID)
			sql := "UPDATE store SET " + strings.Join(cols, ", ") +
				", updated_at = now() WHERE id = $" + strconv.Itoa(len(args))
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				return err
			}
		}

		country, err := companyCountryOf(ctx, tx, companyID)
		if err != nil {
			return err
		}
		out, err = scanBranch(tx.QueryRow(ctx,
			`SELECT`+branchColumns+` FROM store WHERE id = $1`, branchID), country)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That branch was not found.")
		}
		return err
	})
	if err != nil {
		if errs.As(err) != nil {
			return Branch{}, err
		}
		return Branch{}, db.Translate(err, "")
	}
	return out, nil
}
