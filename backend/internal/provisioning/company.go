// Company settings after setup, blueprint I1.
//
// The wizard in onboarding.go creates a company once and never looks at it
// again. Until this file there was no second look available to anybody:
// `UPDATE company` appeared nowhere in the product outside a receipt-number
// counter, and `store` was written only by CommitStores reading the wizard's
// own scratch JSONB. A business that mistyped its legal name during setup, got
// a new commercial registration, moved premises or opened a second branch had
// no way to say so, in any screen, ever.
//
// That is not a cosmetic gap. Three consequences were live before this file:
//
//   - The storefront disclosure (E5) reports `cr_number` among the things a
//     shop is missing, and nothing in the product could supply one. The
//     compliance dashboard pointed at a field with no way in.
//   - A branch's National Address is what sales/document.go puts on every
//     invoice, and refuses to issue one without (BR-KSA-09, -37, -66). A shop
//     that finished setup with a wrong postal code could not invoice, and could
//     not correct it.
//   - A second branch could not be opened at all. POST /onboarding/stores reads
//     the wizard's scratch state, not a request, and re-validates every branch
//     the shop has ever had against the Saudi address rules.
//
// # What may not be changed, and why the screen is told rather than the save
//
// Some of this record decided how figures that already exist were computed.
// Changing it would not correct history, it would silently restate it. Those
// fields are reported as SETTLED, with the sentence saying why, so the screen
// can render them fixed rather than let somebody type into a box that will
// refuse. The refusal still exists — a settled field named in a request is an
// error, because a screen is not a security boundary — but nobody should meet
// it by using the product normally.
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

// Business is the company record a settings screen shows.
//
// The next_* document counters are deliberately absent. They are the product's
// own bookkeeping, a caller has no business setting one, and showing them would
// invite somebody to try.
type Business struct {
	ID          uuid.UUID `json:"id"`
	LegalName   string    `json:"legal_name"`
	LegalNameAr string    `json:"legal_name_ar"`
	TradeName   string    `json:"trade_name"`

	// Country is the company's own; Market is the tenant's, set by the platform
	// operator. CommitBusinessInfo requires them to agree, so they are shown
	// together — a screen that displayed only one could not explain why the
	// field is fixed.
	Country      string `json:"country"`
	Market       string `json:"market"`
	MarketName   string `json:"market_name"`
	BaseCurrency string `json:"base_currency"`
	Timezone     string `json:"timezone"`

	CRNumber      string `json:"cr_number"`
	VATRegistered bool   `json:"vat_registered"`
	VATNumber     string `json:"vat_number"`

	ZATCAWave     string `json:"zatca_wave"`
	ZATCADeadline string `json:"zatca_deadline"`
	ZATCAStatus   string `json:"zatca_status"`

	B2BOfflinePolicy     string `json:"b2b_offline_policy"`
	NegativeStockPolicy  string `json:"negative_stock_policy"`
	CostingMethod        string `json:"costing_method"`
	FiscalYearStartMonth int    `json:"fiscal_year_start_month"`

	// Decimals as strings, never as JSON numbers. A tolerance decides whether a
	// bill matches, and float64 is not a thing to decide that with.
	MatchTolerancePct    string `json:"match_tolerance_pct"`
	MatchToleranceAmount string `json:"match_tolerance_amount"`

	WPSBankSarieID     string `json:"wps_bank_sarie_id"`
	WPSEstablishmentID string `json:"wps_establishment_id"`
	WPSBankAccount     string `json:"wps_bank_account"`
	MOLEstablishmentID string `json:"mol_establishment_id"`

	// Settled maps a field name to the reason it can no longer be changed. A
	// field absent from this map may be edited.
	Settled map[string]string `json:"settled"`
}

// settledFields decides what this company may no longer change.
//
// Each answer is a fact about work already done, read in the same transaction
// as the record itself so the screen cannot be told "editable" about a field
// that a concurrent posting has just settled.
func settledFields(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, zatcaStatus string,
) (map[string]string, error) {
	out := map[string]string{}

	// The market is the platform operator's decision (0103), and the tax engine
	// reads company.country on every sale. A shop cannot move country: its
	// invoices, its VAT and its regulatory obligations were all decided by the
	// old one.
	out["country"] = "The market is set when your account is created. Ask your " +
		"Biz1core contact to change it — every sale is taxed by the rules that " +
		"follow from it."

	var posted bool
	if err := tx.QueryRow(ctx,
		`SELECT exists(SELECT 1 FROM journal_entry WHERE company_id = $1)`,
		companyID).Scan(&posted); err != nil {
		return nil, err
	}
	if posted {
		out["base_currency"] = "Every figure in your books is already recorded " +
			"in this currency. Changing it would restate them rather than convert them."
		out["fiscal_year_start_month"] = "Your books already have entries in " +
			"them, and the accounting months are counted from this date."
	}

	var moved bool
	if err := tx.QueryRow(ctx,
		`SELECT exists(SELECT 1 FROM stock_movement WHERE company_id = $1)`,
		companyID).Scan(&moved); err != nil {
		return nil, err
	}
	if moved {
		out["costing_method"] = "Stock has already moved, and its cost was " +
			"calculated this way. Changing it would not recalculate what is on the shelf."
	}

	// Once ZATCA has issued anything, the VAT number is inside the certificate.
	// Changing it here would leave the shop signing invoices with a credential
	// that names a different taxpayer.
	if zatcaStatus != "not_started" {
		reason := "ZATCA has issued a certificate against this VAT number. " +
			"Changing it here would not change the certificate."
		out["vat_number"] = reason
		out["vat_registered"] = reason
	}

	return out, nil
}

// ReadBusiness answers the company record and what may be done to it.
func (s *Service) ReadBusiness(
	ctx context.Context, tenantID, companyID uuid.UUID,
) (Business, error) {
	var b Business
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT c.id, c.legal_name, coalesce(c.legal_name_ar, ''),
			       coalesce(c.trade_name, ''), c.country, t.market,
			       c.base_currency, c.timezone, coalesce(c.cr_number, ''),
			       c.vat_registered, coalesce(c.vat_number, ''),
			       coalesce(c.zatca_wave, ''),
			       coalesce(to_char(c.zatca_deadline, 'YYYY-MM-DD'), ''),
			       c.zatca_status::text, c.b2b_offline_policy,
			       c.negative_stock_policy::text, c.costing_method::text,
			       c.fiscal_year_start_month,
			       c.match_tolerance_pct::text, c.match_tolerance_amount::text,
			       coalesce(c.wps_bank_sarie_id, ''),
			       coalesce(c.wps_establishment_id, ''),
			       coalesce(c.wps_bank_account, ''),
			       coalesce(c.mol_establishment_id, '')
			FROM company c JOIN tenant t ON t.id = c.tenant_id
			WHERE c.id = $1`, companyID).
			Scan(&b.ID, &b.LegalName, &b.LegalNameAr, &b.TradeName,
				&b.Country, &b.Market, &b.BaseCurrency, &b.Timezone, &b.CRNumber,
				&b.VATRegistered, &b.VATNumber, &b.ZATCAWave, &b.ZATCADeadline,
				&b.ZATCAStatus, &b.B2BOfflinePolicy, &b.NegativeStockPolicy,
				&b.CostingMethod, &b.FiscalYearStartMonth,
				&b.MatchTolerancePct, &b.MatchToleranceAmount,
				&b.WPSBankSarieID, &b.WPSEstablishmentID, &b.WPSBankAccount,
				&b.MOLEstablishmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That company was not found.")
		}
		if err != nil {
			return err
		}
		b.MarketName = supportedCountries[b.Market]

		b.Settled, err = settledFields(ctx, tx, companyID, b.ZATCAStatus)
		return err
	})
	if err != nil {
		if errs.As(err) != nil {
			return Business{}, err
		}
		return Business{}, db.Translate(err, "")
	}
	return b, nil
}

// BusinessChange is a partial amendment. Every field is a pointer because a
// blank string is an answer — "clear the trade name" — and an absent field is
// not. An empty string is a value, not an absence; the distinction has to
// survive the wire, or a screen saving one tab wipes the fields on the others.
type BusinessChange struct {
	LegalName   *string `json:"legal_name"`
	LegalNameAr *string `json:"legal_name_ar"`
	TradeName   *string `json:"trade_name"`
	Timezone    *string `json:"timezone"`

	CRNumber      *string `json:"cr_number"`
	VATRegistered *bool   `json:"vat_registered"`
	VATNumber     *string `json:"vat_number"`

	ZATCAWave     *string `json:"zatca_wave"`
	ZATCADeadline *string `json:"zatca_deadline"`

	B2BOfflinePolicy     *string `json:"b2b_offline_policy"`
	NegativeStockPolicy  *string `json:"negative_stock_policy"`
	CostingMethod        *string `json:"costing_method"`
	FiscalYearStartMonth *int    `json:"fiscal_year_start_month"`
	BaseCurrency         *string `json:"base_currency"`

	MatchTolerancePct    *string `json:"match_tolerance_pct"`
	MatchToleranceAmount *string `json:"match_tolerance_amount"`

	WPSBankSarieID     *string `json:"wps_bank_sarie_id"`
	WPSEstablishmentID *string `json:"wps_establishment_id"`
	WPSBankAccount     *string `json:"wps_bank_account"`
	MOLEstablishmentID *string `json:"mol_establishment_id"`

	// Country is accepted so that naming it can be REFUSED with the settled
	// reason rather than ignored in silence. A caller that sends it believes it
	// will take effect.
	Country *string `json:"country"`
}

// oneOf reports whether v is among the values a column accepts. Named apart
// from `offered`, which formats a map of choices for a message rather than
// testing one.
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

var (
	decimalPattern    = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,4})?$`)
	sarieCode         = regexp.MustCompile(`^[A-Z]{4}$`)
	digitsUpTo10      = regexp.MustCompile(`^[0-9]{1,10}$`)
	digits2To15       = regexp.MustCompile(`^[0-9]{2,15}$`)
	bankAccountFormat = regexp.MustCompile(`^[A-Za-z0-9]{1,24}$`)
	isoDatePattern    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// AmendBusiness applies a partial change to the company record and answers the
// record as it now stands, so a screen never has to guess what it saved.
func (s *Service) AmendBusiness(
	ctx context.Context, tenantID, companyID uuid.UUID, ch BusinessChange,
) (Business, error) {
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var zatcaStatus string
		if err := tx.QueryRow(ctx,
			`SELECT zatca_status::text FROM company WHERE id = $1`, companyID).
			Scan(&zatcaStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return err
		}
		settled, err := settledFields(ctx, tx, companyID, zatcaStatus)
		if err != nil {
			return err
		}

		// A settled field named in the request is refused with the reason,
		// never applied and never dropped quietly.
		refused := errs.New(errs.CodeImmutable,
			"Some of those settings can no longer be changed.")
		for _, f := range []struct {
			field string
			named bool
		}{
			{"country", ch.Country != nil},
			{"base_currency", ch.BaseCurrency != nil},
			{"fiscal_year_start_month", ch.FiscalYearStartMonth != nil},
			{"costing_method", ch.CostingMethod != nil},
			{"vat_number", ch.VATNumber != nil},
			{"vat_registered", ch.VATRegistered != nil},
		} {
			if !f.named {
				continue
			}
			if why, ok := settled[f.field]; ok {
				refused.WithField(f.field, why)
			}
		}
		if len(refused.Fields) > 0 {
			return refused
		}

		bad := errs.New(errs.CodeInvalidInput,
			"Those business details could not be saved.")
		set := map[string]any{}

		if ch.LegalName != nil {
			v := strings.TrimSpace(*ch.LegalName)
			if v == "" {
				bad.WithField("legal_name",
					"A business needs a legal name; it appears on every invoice.")
			}
			set["legal_name"] = v
		}
		if ch.LegalNameAr != nil {
			set["legal_name_ar"] = nullIfBlank(*ch.LegalNameAr)
		}
		if ch.TradeName != nil {
			set["trade_name"] = nullIfBlank(*ch.TradeName)
		}
		if ch.Timezone != nil {
			v := strings.TrimSpace(*ch.Timezone)
			if v == "" {
				bad.WithField("timezone",
					"A timezone decides which day a sale falls on, so it cannot be empty.")
			} else {
				var known bool
				if err := tx.QueryRow(ctx,
					`SELECT exists(SELECT 1 FROM pg_timezone_names WHERE name = $1)`,
					v).Scan(&known); err != nil {
					return err
				}
				if !known {
					bad.WithField("timezone",
						"That is not a timezone name. Use a name such as Asia/Riyadh or Asia/Dhaka.")
				}
			}
			set["timezone"] = v
		}
		if ch.CRNumber != nil {
			set["cr_number"] = nullIfBlank(*ch.CRNumber)
		}
		if ch.VATNumber != nil {
			set["vat_number"] = nullIfBlank(*ch.VATNumber)
		}
		if ch.VATRegistered != nil {
			set["vat_registered"] = *ch.VATRegistered
		}
		if ch.ZATCAWave != nil {
			set["zatca_wave"] = nullIfBlank(*ch.ZATCAWave)
		}
		if ch.ZATCADeadline != nil {
			v := strings.TrimSpace(*ch.ZATCADeadline)
			if v != "" && !isoDatePattern.MatchString(v) {
				bad.WithField("zatca_deadline", "Write the date as YYYY-MM-DD.")
			}
			set["zatca_deadline"] = nullIfBlank(v)
		}
		if ch.B2BOfflinePolicy != nil {
			v := strings.TrimSpace(*ch.B2BOfflinePolicy)
			if !oneOf(v, "block", "draft_hold", "convert_simplified", "uncleared_invoice") {
				bad.WithField("b2b_offline_policy",
					"Choose one of the offered ways to handle a B2B sale made offline.")
			}
			set["b2b_offline_policy"] = v
		}
		if ch.NegativeStockPolicy != nil {
			v := strings.TrimSpace(*ch.NegativeStockPolicy)
			if !oneOf(v, "block", "allow_warn") {
				bad.WithField("negative_stock_policy",
					"Choose whether selling below zero stock is blocked or allowed with a warning.")
			}
			set["negative_stock_policy"] = v
		}
		if ch.CostingMethod != nil {
			v := strings.TrimSpace(*ch.CostingMethod)
			if !oneOf(v, "wac", "fifo", "standard") {
				bad.WithField("costing_method",
					"Choose weighted average, FIFO or standard cost.")
			}
			set["costing_method"] = v
		}
		if ch.BaseCurrency != nil {
			v := strings.ToUpper(strings.TrimSpace(*ch.BaseCurrency))
			if _, ok := supportedCurrencies[v]; !ok {
				bad.WithField("base_currency",
					"That is not a currency this product keeps books in.")
			}
			set["base_currency"] = v
		}
		if ch.FiscalYearStartMonth != nil {
			v := *ch.FiscalYearStartMonth
			if v < 1 || v > 12 {
				bad.WithField("fiscal_year_start_month",
					"The financial year starts in one of the twelve months.")
			}
			set["fiscal_year_start_month"] = v
		}

		for _, d := range []struct {
			field string
			ptr   *string
			why   string
		}{
			{"match_tolerance_pct", ch.MatchTolerancePct,
				"A tolerance is a number such as 2 or 2.5."},
			{"match_tolerance_amount", ch.MatchToleranceAmount,
				"A tolerance is an amount such as 50 or 50.00."},
		} {
			if d.ptr == nil {
				continue
			}
			v := strings.TrimSpace(*d.ptr)
			if !decimalPattern.MatchString(v) {
				bad.WithField(d.field, d.why)
				continue
			}
			if d.field == "match_tolerance_pct" {
				// The column is a percentage and 0..100 is what the constraint
				// allows; say so rather than let a check constraint answer.
				var within bool
				if err := tx.QueryRow(ctx,
					`SELECT $1::numeric BETWEEN 0 AND 100`, v).Scan(&within); err != nil {
					return err
				}
				if !within {
					bad.WithField(d.field, "A percentage is between 0 and 100.")
					continue
				}
			}
			set[d.field] = v
		}

		for _, f := range []struct {
			field string
			ptr   *string
			re    *regexp.Regexp
			why   string
		}{
			{"wps_bank_sarie_id", ch.WPSBankSarieID, sarieCode,
				"The bank's SARIE code is four capital letters, such as RJHI."},
			{"wps_establishment_id", ch.WPSEstablishmentID, digitsUpTo10,
				"The WPS establishment number is up to 10 digits."},
			{"wps_bank_account", ch.WPSBankAccount, bankAccountFormat,
				"The account number is up to 24 letters and digits."},
			{"mol_establishment_id", ch.MOLEstablishmentID, digits2To15,
				"The Ministry of Labour establishment number is 2 to 15 digits."},
		} {
			if f.ptr == nil {
				continue
			}
			v := strings.TrimSpace(*f.ptr)
			if v != "" && !f.re.MatchString(v) {
				bad.WithField(f.field, f.why)
				continue
			}
			set[f.field] = nullIfBlank(v)
		}

		if len(bad.Fields) > 0 {
			return bad
		}
		if len(set) == 0 {
			return nil
		}

		// Assembled rather than written out, because the caller decides which
		// columns are in play. The names are ours and never the client's: every
		// map key above is a literal in this file.
		cols := make([]string, 0, len(set))
		args := make([]any, 0, len(set)+1)
		for c, v := range set {
			args = append(args, v)
			cols = append(cols, c+" = $"+strconv.Itoa(len(args)))
		}
		args = append(args, companyID)
		sql := "UPDATE company SET " + strings.Join(cols, ", ") +
			", updated_at = now() WHERE id = $" + strconv.Itoa(len(args))
		_, err = tx.Exec(ctx, sql, args...)
		return err
	})
	if err != nil {
		if errs.As(err) != nil {
			return Business{}, err
		}
		return Business{}, db.Translate(err, "")
	}
	return s.ReadBusiness(ctx, tenantID, companyID)
}
