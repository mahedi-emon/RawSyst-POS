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
	return strings.EqualFold(strings.TrimSpace(country), "sa")
}
