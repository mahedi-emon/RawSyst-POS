// Package catalog owns products, variants, categories, brands and units.
package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// TaxModel distinguishes the two fundamentally different systems the product
// must serve.
//
// This is not a cosmetic difference. Under a VAT, tax is charged at each stage
// and the input tax paid on purchases is reclaimed against the output tax
// collected on sales. Under a US sales tax, tax is charged only at the final
// retail sale — there is no input tax and nothing to reclaim, so any report
// built on "output minus input" is meaningless.
//
// Code that assumes a VAT will produce confidently wrong numbers in the USA,
// which is why the model is explicit data rather than an assumption.
type TaxModel string

const (
	TaxModelVAT      TaxModel = "vat"
	TaxModelSalesTax TaxModel = "sales_tax"
)

// TaxRules is a country's tax shape, resolved from the registry.
type TaxRules struct {
	Country             string
	Model               TaxModel
	Treatments          []string
	InputTaxRecoverable bool
}

// Allows reports whether a treatment is valid in this country.
func (r TaxRules) Allows(treatment string) bool {
	for _, t := range r.Treatments {
		if t == treatment {
			return true
		}
	}
	return false
}

// RequiresJurisdiction reports whether a rate lookup needs more than a country.
//
// Saudi Arabia and Bangladesh set VAT nationally. US sales tax is set by state,
// county and city, so a rate resolved from the country alone would be wrong
// almost everywhere.
func (r TaxRules) RequiresJurisdiction() bool {
	return r.Model == TaxModelSalesTax
}

// TaxRulesFor resolves a country's tax shape as it stood on a given date.
//
// The date is required for the same reason it is required everywhere in this
// system: a product's treatment must be validated against the rules that
// applied when the product was priced, not the rules in force today.
//
// tx is the transaction the caller is already inside, or nil when there is
// none. Passing it keeps the lookup on the connection the caller already holds
// — see registry.Query.Tx, where a sale that asked the pool for a second
// connection while holding the first is a deadlock rather than a slow query.
func TaxRulesFor(
	ctx context.Context, rules *registry.Service, tx pgx.Tx,
	country string, asOf time.Time, tenantID uuid.UUID,
) (TaxRules, error) {
	country = strings.ToLower(country)

	key := treatmentKeyFor(country)
	var payload struct {
		Treatments          []string `json:"treatments"`
		Model               string   `json:"model"`
		InputTaxRecoverable bool     `json:"input_tax_recoverable"`
	}

	err := rules.Into(ctx, registry.Query{
		Key: key, Country: country, AsOf: asOf, TenantID: tenantID, Tx: tx,
	}, &payload)
	if err != nil {
		return TaxRules{}, err
	}

	if payload.Model == "" {
		// Migration 0011 makes this unrepresentable in the database. Reaching
		// here means a rule was inserted through some other path, and guessing
		// a model is exactly the mistake that constraint exists to prevent.
		return TaxRules{}, errs.Newf(errs.CodeUnverifiedRule,
			"The tax rules for %q do not say whether the country uses a VAT or a "+
				"sales tax, so tax cannot be calculated safely.", strings.ToUpper(country))
	}

	return TaxRules{
		Country:             country,
		Model:               TaxModel(payload.Model),
		Treatments:          payload.Treatments,
		InputTaxRecoverable: payload.InputTaxRecoverable,
	}, nil
}

// treatmentKeyFor maps a country to its treatment-list rule key.
//
// The key names the tax regime rather than the country's generic "tax", because
// the regimes are not interchangeable: SA.VAT and US.SALESTAX describe
// different systems and a shared key would hide that.
func treatmentKeyFor(country string) string {
	switch strings.ToLower(country) {
	case "us":
		return "US.SALESTAX.TAX_TREATMENTS"
	default:
		return fmt.Sprintf("%s.VAT.TAX_TREATMENTS", strings.ToUpper(country))
	}
}

// ValidateTreatment checks a product's tax treatment against its country.
//
// The error lists what IS allowed. A product manager who typed "zero_rated" in
// a US catalogue needs to know the US list says "non_taxable", not merely that
// they were wrong.
func ValidateTreatment(r TaxRules, treatment string) error {
	if treatment == "" {
		return errs.New(errs.CodeInvalidInput,
			"Choose a tax treatment for this product.")
	}
	if r.Allows(treatment) {
		return nil
	}

	allowed := append([]string(nil), r.Treatments...)
	sort.Strings(allowed)

	return errs.Newf(errs.CodeInvalidInput,
		"%q is not a tax treatment used in %s. Allowed values are: %s.",
		treatment, strings.ToUpper(r.Country), strings.Join(allowed, ", ")).
		WithField("tax_treatment", "Not valid for this country.")
}

// RequiresExemptionReason reports whether a treatment needs a reason code on
// the invoice.
//
// ZATCA requires the reason for any non-standard treatment to be identified on
// the invoice. US exempt sales need a resale or exemption certificate held
// against the customer, which serves the same evidentiary purpose, so both are
// treated as requiring a reason.
func RequiresExemptionReason(r TaxRules, treatment string) bool {
	switch r.Model {
	case TaxModelVAT:
		return treatment != "standard" && treatment != "reduced"
	case TaxModelSalesTax:
		return treatment == "exempt"
	default:
		return true
	}
}
