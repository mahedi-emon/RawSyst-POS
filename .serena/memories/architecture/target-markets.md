# Target markets — Saudi Arabia, Bangladesh, USA

Stated by the founder 2026-08-15. Saudi is the first and most detailed configuration, **not** the architecture. Blueprint A2 #4: "Nothing is hard-coded to Saudi."

## The three markets differ in ways that reach the schema

### Tax models are not the same shape
| Market | Model | Consequence |
|---|---|---|
| **Saudi** | VAT, single national rate, 7 treatments, input VAT reclaimable | ZATCA e-invoicing, clearance vs reporting |
| **Bangladesh** | VAT/NBR, multiple rates, own invoicing rules (Mushak) | Different treatment list, different filing cadence |
| **USA** | **Sales tax — NOT a VAT** | Charged only at final retail sale; **no input tax to reclaim**; rate varies by state/county/city; origin vs destination sourcing; exemption certificates per customer |

**The USA case is the one that breaks naive designs.** A schema that assumes "output tax minus input tax = payable" cannot express US sales tax. So:

- `tax_treatment` on a product is **TEXT validated against a country-scoped registry list**, never a global PostgreSQL enum. An enum would need a migration per country.
- Tax rate resolution takes **country + jurisdiction + date**, not just date. Saudi ignores jurisdiction; the USA cannot.
- Input-tax recoverability is a **per-country capability flag**, not an assumption.

### Other divergences already known
- **Barcode**: EAN-13 dominant internationally, **UPC-A in North America**. Both already supported.
- **Units**: metric for SA/BD, **imperial common in the USA**. Unit of measure is per-tenant data, not a fixed list.
- **Languages**: Arabic (RTL), English, Bengali at launch. Product names need more than a fixed `name` + `name_ar` pair — see below.
- **Currency**: SAR, BDT, USD. Already per-company with FX gain/loss.
- **Address shape**: differs enough that address is stored as structured JSONB rather than fixed columns.

### Product naming
`name` + `name_ar` (as sketched in design doc 10) is a **Saudi-shaped shortcut**. For three markets it becomes `name` (default) plus a `translations jsonb` map keyed by language, so adding Bengali or a fourth language is data, not a migration. Blueprint G3 requires a translation-management system precisely so languages ship without a code release.

## What this does NOT change
- ZATCA, PDPL, WPS/Mudad stay Saudi-only, activated by the country configuration. They are not made generic — they are made **optional per country**.
- The Regulatory Rule Registry already keys every rule by `(rule_key, country, effective_from)`, so it holds all three markets without change.

## Related
[[design/index]] · [[architecture/decisions]] · [[blueprint/part-e-saudi-compliance]]
