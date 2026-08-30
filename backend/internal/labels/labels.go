// Package labels is the barcode engine and the label studio (blueprint B3).
//
// # A meaningful code and a scannable code are two different things
//
// B3 wants `M-WIN-BLK-XL` — a code a person can read off a shelf edge and know
// what it is. It also wants Code128, EAN-13 and QR, which are encodings of a
// value. Those are not in conflict, they are two layers: the shop's scheme
// builds the human-readable string, and the symbology says how it is printed
// so a scanner can read it back.
//
// The exception is worth naming, because getting it wrong produces labels that
// scan as the wrong product. EAN-13 and its relatives are FIXED-LENGTH DIGIT
// codes with a check digit; "M-WIN-BLK-XL" cannot be encoded as one. So a
// company whose symbology is EAN-13 gets a generated NUMBER, and the readable
// string is printed beside it rather than inside it.
//
// # This package computes; it does not draw
//
// It produces the code, and the data every label needs. Turning that into a
// PDF or a thermal printer's own language happens in the browser, where the
// printer actually is — a server that rendered PDFs would still leave the
// browser to send them to a USB device it cannot see, and B3's thermal
// printers (Xprinter, Zebra, TSC) are attached to the till.
package labels

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// The symbologies B3 lists.
const (
	Code128    = "code128"
	EAN13      = "ean13"
	EAN8       = "ean8"
	UPCA       = "upca"
	QR         = "qr"
	DataMatrix = "datamatrix"
)

// The kinds of label B3 asks for.
const (
	KindHangTag     = "hang_tag"
	KindThermal     = "thermal"
	KindA4Sheet     = "a4_sheet"
	KindLoyaltyCard = "loyalty_card"
)

// Service builds codes and serves label data.
type Service struct {
	pool *db.Pool
	// rules resolves the VAT rate a tag prints. Held rather than queried,
	// because there is exactly one accessor for that rate in this product and a
	// label has to agree with what the till charges.
	rules *registry.Service
}

// NewService builds the service.
func NewService(pool *db.Pool, rules *registry.Service) *Service {
	return &Service{pool: pool, rules: rules}
}

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Scheme is how a company builds a meaningful barcode.
type Scheme struct {
	Parts      []string `json:"parts"`
	Separator  string   `json:"separator"`
	Symbology  string   `json:"symbology"`
	PartLength int      `json:"part_length"`
	Prefix     string   `json:"prefix,omitempty"`
	// Example is the scheme applied to made-up values, so somebody changing it
	// can see what they will get before they generate a thousand of them.
	Example string `json:"example"`
}

// Template is one label layout.
type Template struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Width     string    `json:"width_mm"`
	Height    string    `json:"height_mm"`
	Columns   *int      `json:"columns,omitempty"`
	Rows      *int      `json:"rows,omitempty"`
	Margin    string    `json:"margin_mm"`
	Gap       string    `json:"gap_mm"`
	Fields    []Field   `json:"fields"`
	IsDefault bool      `json:"is_default"`
	// PerSheet is columns × rows, which is the number a shopkeeper actually
	// buys labels by — "a sheet of 24" — rather than a grid they have to
	// multiply in their head.
	PerSheet int `json:"per_sheet,omitempty"`
}

// Field is one thing printed on a label.
type Field struct {
	Field  string `json:"field"`
	Size   int    `json:"size,omitempty"`
	Height int    `json:"height,omitempty"`
	Bold   bool   `json:"bold,omitempty"`
	RTL    bool   `json:"rtl,omitempty"`
}

// Label is everything one printed label needs.
//
// Resolved server-side rather than left to the browser to assemble from four
// endpoints: a price that came from one request and a barcode from another can
// disagree, and a label is the one place in this product where a wrong price
// is physically attached to a garment.
type Label struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Barcode   string    `json:"barcode"`
	Symbology string    `json:"symbology"`
	// Readable is the meaningful string, which for a digit symbology is not
	// the same as the barcode and is printed beside it.
	Readable string `json:"readable,omitempty"`

	Name       string `json:"name"`
	NameAr     string `json:"name_ar,omitempty"`
	Attributes string `json:"attributes,omitempty"`
	Category   string `json:"category,omitempty"`
	Brand      string `json:"brand,omitempty"`
	Season     string `json:"season,omitempty"`

	// Price is VAT-INCLUSIVE, which is what B3 asks for and what a customer
	// reading a tag in Saudi Arabia expects: the shelf price is the price paid.
	Price    string `json:"price"`
	Currency string `json:"currency"`
	TaxRate  string `json:"tax_rate"`
}

// ReadScheme returns the company's barcode scheme.
func (s *Service) ReadScheme(ctx context.Context, scope Scope) (Scheme, error) {
	var out Scheme
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		sc, e := readScheme(ctx, tx, scope.CompanyID)
		out = sc
		return e
	})
	return out, db.Translate(err, "")
}

// SaveScheme replaces it.
//
// Existing barcodes are not regenerated. A code that is printed on nine hundred
// hang tags in a stockroom does not change because somebody edited a setting,
// and a product whose barcode moved would scan as nothing at the till.
func (s *Service) SaveScheme(
	ctx context.Context, scope Scope, in Scheme,
) (Scheme, error) {
	if len(in.Parts) == 0 {
		return Scheme{}, errs.Validation(
			"A barcode scheme needs at least one part.").
			WithField("parts",
				"Category, colour and size are what most shops use.")
	}
	for _, p := range in.Parts {
		if !knownPart(p) {
			return Scheme{}, errs.Newf(errs.CodeInvalidInput,
				"There is nothing called %q to put in a barcode.", p)
		}
	}
	if !knownSymbology(in.Symbology) {
		return Scheme{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a barcode format this product prints.", in.Symbology)
	}
	if in.PartLength < 1 || in.PartLength > 8 {
		return Scheme{}, errs.New(errs.CodeInvalidInput,
			"Each part has to be between one and eight characters.")
	}
	if in.Separator == "" {
		in.Separator = "-"
	}

	parts, err := json.Marshal(in.Parts)
	if err != nil {
		return Scheme{}, err
	}

	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO barcode_scheme
			  (tenant_id, company_id, parts, separator, symbology, part_length,
			   prefix, updated_by)
			VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8)
			ON CONFLICT (company_id) DO UPDATE SET
			  parts = EXCLUDED.parts,
			  separator = EXCLUDED.separator,
			  symbology = EXCLUDED.symbology,
			  part_length = EXCLUDED.part_length,
			  prefix = EXCLUDED.prefix,
			  updated_by = EXCLUDED.updated_by`,
			scope.TenantID, scope.CompanyID, string(parts), in.Separator,
			in.Symbology, in.PartLength, nullText(in.Prefix), scope.UserID)
		return db.Translate(e, "That scheme could not be saved.")
	})
	if err != nil {
		return Scheme{}, err
	}
	return s.ReadScheme(ctx, scope)
}

// Generate assigns barcodes to variants that have none.
//
// B3's bulk generator: a factory delivers a thousand pieces and the shop wants
// a thousand codes in one press.
//
// A variant that ALREADY has a barcode keeps it unless `overwrite` says
// otherwise, and overwrite is a decision somebody makes rather than the
// default: regenerating a code that is printed on tags already in the
// stockroom turns those tags into stickers that scan as nothing.
func (s *Service) Generate(
	ctx context.Context, scope Scope, variantIDs []uuid.UUID, overwrite bool,
) (Generated, error) {
	var out Generated
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		scheme, e := readScheme(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx, sourceSelect+`
			WHERE v.company_id = $1
			  AND ($2::uuid[] IS NULL OR v.id = ANY($2))
			  AND ($3 OR v.barcode IS NULL)
			ORDER BY p.name, v.sku
			LIMIT 5000`, scope.CompanyID, nullIDs(variantIDs), overwrite)
		if e != nil {
			return e
		}
		type source struct {
			id         uuid.UUID
			sku        string
			category   string
			brand      string
			season     string
			attributes map[string]string
		}
		var sources []source
		for rows.Next() {
			var src source
			var attributes string
			if e := rows.Scan(&src.id, &src.sku, &src.category, &src.brand,
				&src.season, &attributes); e != nil {
				rows.Close()
				return e
			}
			if e := json.Unmarshal([]byte(attributes), &src.attributes); e != nil {
				rows.Close()
				return e
			}
			sources = append(sources, src)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		// The codes are checked against every other barcode in the company as
		// they are built, not only against what was there when the batch
		// started: two variants that differ only in a part the scheme does not
		// include would otherwise both be given the same code, and a till
		// scanning it would pick one of them arbitrarily.
		taken, e := existingCodes(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		for _, src := range sources {
			readable := buildReadable(scheme, src.category, src.brand,
				src.season, src.attributes, src.sku)
			code, e := encodeFor(scheme, readable, taken)
			if e != nil {
				return e
			}
			taken[code] = true

			if _, e := tx.Exec(ctx,
				`UPDATE variant SET barcode = $2 WHERE id = $1`,
				src.id, code); e != nil {
				return db.Translate(e, "That barcode could not be assigned.")
			}
			out.Assigned = append(out.Assigned, Assignment{
				VariantID: src.id, SKU: src.sku,
				Barcode: code, Readable: readable,
			})
		}
		out.Count = len(out.Assigned)

		if out.Count == 0 {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "barcodes_generated",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"count": out.Count, "overwrote_existing": overwrite,
			},
		})
	})
	return out, db.Translate(err, "")
}

// Generated is what a bulk run did.
type Generated struct {
	Count    int          `json:"count"`
	Assigned []Assignment `json:"assigned"`
}

// Assignment is one variant and the code it was given.
type Assignment struct {
	VariantID uuid.UUID `json:"variant_id"`
	SKU       string    `json:"sku"`
	Barcode   string    `json:"barcode"`
	Readable  string    `json:"readable"`
}

// SetBarcode is B3's manual override, for a product that has to carry a code
// somebody else assigned.
func (s *Service) SetBarcode(
	ctx context.Context, scope Scope, variantID uuid.UUID, barcode string,
) error {
	barcode = strings.TrimSpace(barcode)
	if barcode == "" {
		return errs.New(errs.CodeInvalidInput, "Type the barcode.")
	}
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE variant SET barcode = $3
				WHERE id = $1 AND company_id = $2`,
				variantID, scope.CompanyID, barcode)
			if e != nil {
				return db.Translate(e,
					"Another product already carries that barcode.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That product is not on this company's books.")
			}
			return nil
		}), "")
}

const sourceSelect = `
	SELECT v.id, v.sku, coalesce(c.name, ''), coalesce(b.name, ''),
	       coalesce(p.season, ''), v.attributes::text
	FROM variant v
	JOIN product p ON p.id = v.product_id
	LEFT JOIN category c ON c.id = p.category_id
	LEFT JOIN brand b ON b.id = p.brand_id`

func readScheme(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (Scheme, error) {
	var sc Scheme
	var parts string
	var prefix *string
	err := tx.QueryRow(ctx, `
		SELECT parts::text, separator, symbology, part_length, prefix
		FROM barcode_scheme WHERE company_id = $1`, companyID).
		Scan(&parts, &sc.Separator, &sc.Symbology, &sc.PartLength, &prefix)
	if err == pgx.ErrNoRows {
		// A company provisioned before 0095 has no row. The defaults are what
		// the migration seeds, so the answer is the same either way rather
		// than an error a screen has to special-case.
		sc = Scheme{
			Parts: []string{"category", "colour", "size"}, Separator: "-",
			Symbology: Code128, PartLength: 3,
		}
	} else if err != nil {
		return Scheme{}, err
	} else if err := json.Unmarshal([]byte(parts), &sc.Parts); err != nil {
		return Scheme{}, err
	}
	if prefix != nil {
		sc.Prefix = *prefix
	}

	sc.Example = buildReadable(sc, "Menswear", "Nassar", "Winter",
		map[string]string{"colour": "Black", "size": "XL"}, "SAMPLE")
	return sc, nil
}

// buildReadable assembles B3's meaningful string.
//
// A part the product does not have is SKIPPED rather than left as an empty
// slot, so a shirt with no season reads `MEN-BLK-XL` and not `MEN--BLK-XL`.
// The alternative — a placeholder — would put a character on a shelf edge that
// means "we did not know", which is not information anybody can act on.
func buildReadable(
	sc Scheme, category, brand, season string,
	attributes map[string]string, sku string,
) string {
	var out []string
	if sc.Prefix != "" {
		out = append(out, strings.ToUpper(sc.Prefix))
	}
	for _, part := range sc.Parts {
		value := ""
		switch part {
		case "category":
			value = category
		case "brand":
			value = brand
		case "season":
			value = season
		case "sku":
			value = sku
		default:
			// Everything else is a variant attribute — colour, size, width,
			// length — read by the name the shop gave it.
			value = attributes[part]
			if value == "" {
				value = attributes[strings.ToLower(part)]
			}
		}
		if trimmed := shorten(value, sc.PartLength); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return strings.ToUpper(sku)
	}
	return strings.Join(out, sc.Separator)
}

// shorten upper-cases a part and cuts it to length, keeping only characters a
// scanner and a person will both read the same way.
func shorten(value string, length int) string {
	var kept []rune
	for _, r := range strings.ToUpper(strings.TrimSpace(value)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			kept = append(kept, r)
		}
		if len(kept) == length {
			break
		}
	}
	return string(kept)
}

// encodeFor turns the readable string into the value that is actually printed.
//
// For Code128, QR and Data Matrix that is the string itself: all three encode
// arbitrary text, which is the whole reason B3 can ask for a readable code at
// all. For the digit symbologies it cannot be, so a numeric code is allocated
// and the readable string is printed beside the bars instead.
func encodeFor(sc Scheme, readable string, taken map[string]bool) (string, error) {
	switch sc.Symbology {
	case Code128, QR, DataMatrix:
		return unique(readable, taken), nil
	case EAN13:
		return allocateDigits(12, taken)
	case UPCA:
		return allocateDigits(11, taken)
	case EAN8:
		return allocateDigits(7, taken)
	}
	return "", errs.Newf(errs.CodeInternal,
		"%q is not a barcode format this product prints.", sc.Symbology)
}

// unique makes a readable code distinct by appending a counter.
//
// Two variants that differ only in something the scheme does not include —
// two lengths of the same black XL trouser — would otherwise be given the same
// code, and a till scanning it would pick one of them arbitrarily.
func unique(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
}

// allocateDigits builds the next free numeric code with its check digit.
//
// In-store codes, deliberately: the leading 2 is the GS1 range reserved for
// internal use, so a shop's generated EAN-13 can never collide with a real
// manufacturer's code on a product they also stock.
func allocateDigits(bodyLength int, taken map[string]bool) (string, error) {
	for n := 1; n < 1_000_000; n++ {
		body := "2" + fmt.Sprintf("%0*d", bodyLength-1, n)
		code := body + string(rune('0'+checkDigit(body)))
		if !taken[code] {
			return code, nil
		}
	}
	return "", errs.New(errs.CodeConflict,
		"This company has run out of in-store barcode numbers.")
}

// checkDigit is the modulo-10 digit EAN and UPC carry.
//
// Weights alternate 3 and 1 from the RIGHT, which is what makes the same
// function correct for EAN-8, UPC-A and EAN-13 despite their different
// lengths. Computing it left to right with a fixed starting weight is the
// classic way to get an EAN-8 wrong while EAN-13 still passes.
func checkDigit(body string) int {
	sum := 0
	weight := 3
	for i := len(body) - 1; i >= 0; i-- {
		digit, err := strconv.Atoi(string(body[i]))
		if err != nil {
			return 0
		}
		sum += digit * weight
		if weight == 3 {
			weight = 1
		} else {
			weight = 3
		}
	}
	return (10 - sum%10) % 10
}

func existingCodes(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT barcode FROM variant
		WHERE company_id = $1 AND barcode IS NOT NULL`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taken := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		taken[code] = true
	}
	return taken, rows.Err()
}

func knownPart(part string) bool {
	// Category, brand, season and the SKU are columns; anything else is a
	// variant attribute, which a shop names itself, so the list cannot be
	// closed without stopping a shop using the attribute they actually have.
	return strings.TrimSpace(part) != "" && len(part) <= 40
}

func knownSymbology(s string) bool {
	switch s {
	case Code128, EAN13, EAN8, UPCA, QR, DataMatrix:
		return true
	}
	return false
}

func nullIDs(ids []uuid.UUID) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

func money(d decimal.Decimal) string { return d.StringFixed(2) }
