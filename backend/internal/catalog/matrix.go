package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The variant matrix.
//
// Blueprint B2: a fashion retailer sets up one Abaya and needs the size by
// colour grid to exist as sellable variants, each with its own SKU, barcode and
// stock. Typing thirty rows by hand is how a shop ends up with two SKUs for the
// same thing.
//
// # Generating is idempotent, and that is the whole design
//
// A grid is not created once. A shop adds a colour in March and two sizes in
// June, and each time it regenerates the SAME matrix with one more axis value.
// So generation adds what is missing and leaves everything else alone — it
// never rewrites a variant that already exists, because that variant may have
// been sold a thousand times and carries its own price, barcode and stock.
//
// # Nothing is ever deleted
//
// Shrinking an axis does not remove variants. A variant that has been sold is
// referenced by invoice lines, stock movements and cost layers, all of which
// are immutable history — and a variant that has NOT been sold is still a
// barcode someone may have printed. Removal is deactivation, and the caller
// asks for it explicitly.

// Axis is one dimension of the grid: "size" with values S, M, L.
type Axis struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// MatrixRequest asks for a grid.
type MatrixRequest struct {
	ProductID uuid.UUID
	Axes      []Axis

	// BasePrice applies to every generated variant. Per-combination prices are
	// set afterwards by editing the variant, because a grid of thirty prices
	// supplied up front is thirty chances to mistype one.
	BasePrice    decimal.Decimal
	PriceFloor   *decimal.Decimal
	CostStandard *decimal.Decimal
}

// MatrixResult reports what generation did.
type MatrixResult struct {
	Created []VariantSummary `json:"created"`

	// Existing names combinations that were already there and left untouched.
	// Reported rather than silently skipped: a shop regenerating a grid needs
	// to see that its March colours survived.
	Existing []VariantSummary `json:"existing"`

	Combinations int `json:"combinations"`
}

// VariantSummary is one cell of the grid.
type VariantSummary struct {
	ID         uuid.UUID         `json:"id"`
	SKU        string            `json:"sku"`
	Attributes map[string]string `json:"attributes"`
	Price      string            `json:"price"`
	IsActive   bool              `json:"is_active"`
}

// GenerateMatrix creates the missing cells of a product's grid.
func (s *Service) GenerateMatrix(
	ctx context.Context, tenantID uuid.UUID, req MatrixRequest,
) (MatrixResult, error) {
	combos, err := expand(req.Axes)
	if err != nil {
		return MatrixResult{}, err
	}

	out := MatrixResult{
		Created: []VariantSummary{}, Existing: []VariantSummary{},
		Combinations: len(combos),
	}

	err = s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var companyID uuid.UUID
		var productSKU string
		e := tx.QueryRow(ctx,
			`SELECT company_id, sku FROM product WHERE id = $1`, req.ProductID).
			Scan(&companyID, &productSKU)
		if e != nil {
			return db.Translate(e, "That product was not found.")
		}

		// Existing cells, keyed by their attribute signature so a combination is
		// recognised however the JSON happens to be ordered.
		existing, e := readGrid(ctx, tx, req.ProductID)
		if e != nil {
			return e
		}

		for _, combo := range combos {
			key := signature(combo)
			if v, found := existing[key]; found {
				out.Existing = append(out.Existing, v)
				continue
			}

			attrs, e := json.Marshal(combo)
			if e != nil {
				return e
			}
			sku := deriveSKU(productSKU, req.Axes, combo)

			var v VariantSummary
			e = tx.QueryRow(ctx, `
				INSERT INTO variant
				  (tenant_id, company_id, product_id, sku, attributes,
				   price_retail, price_floor, cost_standard)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				RETURNING id, sku, is_active`,
				tenantID, companyID, req.ProductID, sku, attrs,
				req.BasePrice, req.PriceFloor, req.CostStandard).
				Scan(&v.ID, &v.SKU, &v.IsActive)
			if e != nil {
				return db.Translate(e, fmt.Sprintf(
					"The variant %q could not be created. A product cannot have two "+
						"variants with the same code.", sku))
			}
			v.Attributes = combo
			v.Price = req.BasePrice.String()
			out.Created = append(out.Created, v)
		}
		return nil
	})
	return out, err
}

// ReadMatrix returns the whole grid, generated or hand-made.
func (s *Service) ReadMatrix(
	ctx context.Context, tenantID, productID uuid.UUID,
) ([]VariantSummary, error) {
	out := []VariantSummary{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		grid, e := readGrid(ctx, tx, productID)
		if e != nil {
			return e
		}
		for _, v := range grid {
			out = append(out, v)
		}
		// Sorted by SKU so a grid reads the same way twice. Map iteration order
		// in Go is deliberately random, and a product page whose rows shuffle on
		// every refresh is unusable.
		sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
		return nil
	})
	return out, err
}

// Deactivate withdraws a variant from sale without removing it.
//
// Never a delete. A variant that has been sold is referenced by invoice lines,
// stock movements and cost layers — all immutable history — so removing it
// would either fail on a foreign key or, worse, orphan a figure on a tax
// invoice. A variant that has never been sold is still a barcode somebody may
// have printed and stuck on a shelf.
func (s *Service) Deactivate(
	ctx context.Context, tenantID, variantID uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx,
			`UPDATE variant SET is_active = false WHERE id = $1`, variantID)
		if e != nil {
			return db.Translate(e, "That variant could not be withdrawn.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That variant was not found.")
		}
		return nil
	})
}

// readGrid loads a product's variants keyed by attribute signature.
func readGrid(
	ctx context.Context, tx pgx.Tx, productID uuid.UUID,
) (map[string]VariantSummary, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, sku, attributes, price_retail, is_active
		FROM variant WHERE product_id = $1`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]VariantSummary{}
	for rows.Next() {
		var v VariantSummary
		var attrs []byte
		var price decimal.Decimal
		if err := rows.Scan(&v.ID, &v.SKU, &attrs, &price, &v.IsActive); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attrs, &v.Attributes); err != nil {
			return nil, err
		}
		v.Price = price.String()
		out[signature(v.Attributes)] = v
	}
	return out, rows.Err()
}

// expand turns axes into every combination, in a stable order.
func expand(axes []Axis) ([]map[string]string, error) {
	if len(axes) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"A variant grid needs at least one axis, such as size or colour.")
	}

	total := 1
	for _, a := range axes {
		if strings.TrimSpace(a.Name) == "" {
			return nil, errs.New(errs.CodeInvalidInput,
				"Every axis of a variant grid needs a name.")
		}
		if len(a.Values) == 0 {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"The %q axis has no values, so it would produce no variants.", a.Name)
		}
		if dup := firstDuplicate(a.Values); dup != "" {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"The %q axis lists %q twice.", a.Name, dup)
		}
		total *= len(a.Values)

		// A grid is a convenience, not a bulk import. Three axes of ten values
		// is already a thousand variants, each with a barcode somebody has to
		// print, and a shop that means it can generate in stages.
		if total > 500 {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"These axes would produce more than 500 variants. Generate the "+
					"grid in stages, or check the axis values are right.")
		}
	}

	combos := []map[string]string{{}}
	for _, a := range axes {
		next := make([]map[string]string, 0, len(combos)*len(a.Values))
		for _, base := range combos {
			for _, value := range a.Values {
				merged := make(map[string]string, len(base)+1)
				for k, v := range base {
					merged[k] = v
				}
				merged[a.Name] = value
				next = append(next, merged)
			}
		}
		combos = next
	}
	return combos, nil
}

func firstDuplicate(values []string) string {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		key := strings.ToLower(strings.TrimSpace(v))
		if seen[key] {
			return v
		}
		seen[key] = true
	}
	return ""
}

// signature identifies a combination independently of key order.
//
// Two variants with the same attributes are the same cell of the grid whatever
// order the JSON stored them in, and Postgres does not preserve jsonb key
// order. Comparing raw JSON would make regeneration create duplicates.
func signature(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		for orig, v := range attrs {
			if strings.EqualFold(orig, k) {
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(strings.ToLower(v))
				b.WriteByte(';')
				break
			}
		}
	}
	return b.String()
}

// deriveSKU builds a variant code from the product's, in axis order.
//
// Axis ORDER, not map order, so ABAYA-L-BLK is stable across regenerations. A
// SKU that changed between runs would be printed on two different labels for
// the same garment.
func deriveSKU(productSKU string, axes []Axis, combo map[string]string) string {
	parts := make([]string, 0, len(axes)+1)
	parts = append(parts, productSKU)
	for _, a := range axes {
		parts = append(parts, skuToken(combo[a.Name]))
	}
	return strings.Join(parts, "-")
}

// skuToken reduces a value to something a barcode label and a scanner both
// tolerate. The product SKU format constraint allows only these characters, and
// a variant SKU must satisfy it too.
func skuToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(value)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			// Collapsed away rather than kept: "Navy Blue" becomes NAVYBLUE, so
			// a scanner never has to deal with a space.
		}
	}
	token := b.String()
	if token == "" {
		// A value written entirely in Arabic reduces to nothing under this rule,
		// which is a real case in these markets. Falling back to a stable short
		// hash keeps the SKU unique and scannable; the readable name is on the
		// variant's attributes either way.
		return fmt.Sprintf("X%X", stableHash(value)%0xFFFF)
	}
	if len(token) > 12 {
		return token[:12]
	}
	return token
}

// unmarshalAttributes fills a summary's attribute map.
func unmarshalAttributes(raw []byte, v *VariantSummary) error {
	return json.Unmarshal(raw, &v.Attributes)
}

func stableHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
