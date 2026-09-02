// Package market answers which country-specific obligations apply to a
// business.
//
// # Why this is its own package
//
// It exists so that `country == "sa"` appears in one place instead of in every
// module that has to care. Before it, the Saudi e-invoicing obligation was not
// expressed as a rule at all — it was assumed. `sales.resolveTerminal` refused
// any terminal with no EGS unit and `devices.Register` refused to create one
// without naming a unit, in every market, because both were written when Saudi
// Arabia was the only market. The result was that a Bangladeshi or American
// shop could be provisioned, set up, stocked and staffed, and then could not
// register a till or ring up a single item.
//
// The fix is not a flag on a company that somebody remembers to set. It is a
// function, named for the obligation rather than for the country, that the
// modules ask. When a second market legislates for e-invoicing — several are —
// this is the line that changes, and the compiler finds every caller.
//
// # What does NOT belong here
//
// Dated legal VALUES: a VAT rate, a filing threshold, a document format. Those
// live in the regulatory registry, resolved at a transaction's date, with
// evidence recorded against each one. The difference is that a registry value
// changes under a product that keeps working the same way, whereas the
// questions here decide which code path exists at all. A sale that took a
// different shape because somebody edited a registry row would be far harder to
// reason about than one function an engineer can read.
package market

import "strings"

// EInvoicingApplies reports whether a market obliges every invoice onto a
// signed, gapless, per-terminal chain — in practice, whether a terminal needs
// an EGS unit before it can sell.
//
// Saudi Arabia only, today. ZATCA requires each terminal to carry its own
// non-resetting counter and hash chain, which is why an EGS unit is a hard
// prerequisite of selling there and is meaningless everywhere else: its columns
// are a ZATCA CSR, and nothing outside the Kingdom has one to fill in.
//
// Case-insensitive because `company.country` carries a CHECK that it is stored
// lowercase, and a caller holding the same value from a JSON body may not have
// normalised it yet. Being strict here would make the obligation depend on how
// somebody happened to type it.
func EInvoicingApplies(country string) bool {
	return isSaudi(country)
}

// SocialInsuranceApplies reports whether this market runs the social-insurance
// scheme this product knows how to compute — GOSI.
//
// Saudi Arabia only. Other markets have their own schemes with their own rates,
// bases and eligibility, and this product knows none of them. Answering "yes"
// anywhere else would mean computing a Saudi contribution for a foreign
// employee, which is worse than computing none: a wrong deduction leaves the
// payroll balanced and the employee short.
//
// Where it does not apply, payroll runs WITHOUT a social-insurance line rather
// than failing. It used to fail: `SA.GOSI.RATES` was resolved for whatever
// country the company was in, found nothing, and the registry's "no regulatory
// rule is on record" error killed the whole run — so a Bangladeshi shop could
// not pay anybody.
func SocialInsuranceApplies(country string) bool {
	return isSaudi(country)
}

// WageProtectionApplies reports whether wages must be filed to a state wage
// protection system — Saudi Arabia's WPS/Mudad.
//
// The file FORMAT is a legal specification, per market. Producing a Saudi
// wage file for a Bangladeshi payroll would generate a document no authority
// asked for and none would accept.
func WageProtectionApplies(country string) bool {
	return isSaudi(country)
}

// EndOfServiceApplies reports whether this product can compute the statutory
// end-of-service benefit for a market — Saudi Arabia's EOSB.
//
// Most markets have some form of gratuity or severance and each computes it
// differently, from different service bands on different pay definitions.
// This product has the Saudi entitlement rule and no other, so it declines to
// compute one elsewhere rather than applying Saudi bands to a foreign contract.
func EndOfServiceApplies(country string) bool {
	return isSaudi(country)
}

// PrivacyRegimeApplies reports whether the privacy obligations this product
// implements — Saudi Arabia's PDPL — govern a market.
//
// Bangladesh and the United States have their own data-protection law, and it
// is not PDPL. Reporting a PDPL deadline to a market it does not govern would
// be stating a legal obligation that does not exist; the honest answer is that
// this product has no regime on file for that market.
func PrivacyRegimeApplies(country string) bool {
	return isSaudi(country)
}

func isSaudi(country string) bool {
	return strings.EqualFold(strings.TrimSpace(country), "sa")
}
