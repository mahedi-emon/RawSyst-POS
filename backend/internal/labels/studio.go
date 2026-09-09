package labels

// The templates, and the data a print run needs (blueprint B3).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Templates lists the layouts, defaults first within each kind.
func (s *Service) Templates(ctx context.Context, scope Scope) ([]Template, error) {
	out := []Template{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, templateSelect+`
			WHERE t.company_id = $1
			ORDER BY t.kind, t.is_default DESC, t.name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			tpl, e := scanTemplate(rows)
			if e != nil {
				return e
			}
			out = append(out, tpl)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveTemplate creates or replaces a layout.
func (s *Service) SaveTemplate(
	ctx context.Context, scope Scope, id *uuid.UUID, in Template,
) (Template, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Template{}, errs.Validation("Give the label a name.").
			WithField("name", "It is what the print button will offer.")
	}
	if !knownKind(in.Kind) {
		return Template{}, errs.Newf(errs.CodeInvalidInput,
			"There is no kind of label called %q.", in.Kind)
	}
	width, err := decimal.NewFromString(strings.TrimSpace(in.Width))
	if err != nil || !width.IsPositive() {
		return Template{}, errs.Validation("Say how wide the label is.").
			WithField("width_mm", "In millimetres, as printed on the roll.")
	}
	height, err := decimal.NewFromString(strings.TrimSpace(in.Height))
	if err != nil || !height.IsPositive() {
		return Template{}, errs.Validation("Say how tall the label is.").
			WithField("height_mm", "In millimetres, as printed on the roll.")
	}
	if in.Kind == KindA4Sheet && (in.Columns == nil || in.Rows == nil) {
		return Template{}, errs.Validation(
			"An A4 sheet needs to say how the labels sit on it.").
			WithField("columns", "Three across and eight down is a sheet of 24.")
	}
	if in.Kind != KindA4Sheet {
		// A single label carrying a grid would leave the renderer choosing
		// between two contradictory layouts. The database refuses it too.
		in.Columns, in.Rows = nil, nil
	}
	if err := checkFields(in.Fields); err != nil {
		return Template{}, err
	}
	if len(in.Fields) == 0 {
		return Template{}, errs.Validation(
			"A label with nothing on it is a blank sticker.").
			WithField("fields", "Choose at least one thing to print.")
	}

	fields, err := json.Marshal(in.Fields)
	if err != nil {
		return Template{}, err
	}
	margin := amountOr(in.Margin)
	gap := amountOr(in.Gap)

	var saved uuid.UUID
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The default is cleared first. The unique index would otherwise refuse
		// the write, and "this label cannot be saved" is a worse answer than
		// moving a default the person plainly intended to move.
		if in.IsDefault {
			if _, e := tx.Exec(ctx, `
				UPDATE label_template SET is_default = false
				WHERE company_id = $1 AND kind = $2 AND is_default`,
				scope.CompanyID, in.Kind); e != nil {
				return e
			}
		}

		if id != nil {
			tag, e := tx.Exec(ctx, `
				UPDATE label_template
				SET name = $3, kind = $4, width_mm = $5, height_mm = $6,
				    columns = $7, rows = $8, margin_mm = $9, gap_mm = $10,
				    fields = $11::jsonb, is_default = $12
				WHERE id = $1 AND company_id = $2`,
				*id, scope.CompanyID, in.Name, in.Kind, width, height,
				in.Columns, in.Rows, margin, gap, string(fields), in.IsDefault)
			if e != nil {
				return db.Translate(e, "That label could not be saved.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound, "That label was not found.")
			}
			saved = *id
			return auditTemplate(ctx, tx, scope, "label_template_changed", saved,
				nil, describeTemplate(in, string(fields)))
		}

		if e := db.Translate(tx.QueryRow(ctx, `
			INSERT INTO label_template
			  (tenant_id, company_id, name, kind, width_mm, height_mm, columns,
			   rows, margin_mm, gap_mm, fields, is_default, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.Name, in.Kind, width, height,
			in.Columns, in.Rows, margin, gap, string(fields), in.IsDefault,
			scope.UserID).Scan(&saved),
			"A label with that name already exists."); e != nil {
			return e
		}
		return auditTemplate(ctx, tx, scope, "label_template_created", saved,
			nil, describeTemplate(in, string(fields)))
	})
	if err != nil {
		return Template{}, db.Translate(err, "")
	}
	return s.Template(ctx, scope, saved)
}

// auditTemplate records a change to a layout.
//
// The tag carries the shop's price, and changing what is on it changes every
// ticket printed from then on. The bulk barcode generator beside it has been
// audited since 0095 and this was not, so a template whose price line had been
// taken off named nobody.
func auditTemplate(
	ctx context.Context, tx pgx.Tx, scope Scope, action string,
	id uuid.UUID, before, after map[string]any,
) error {
	return audit.Write(ctx, tx, audit.Entry{
		TenantID: &scope.TenantID, ActorID: &scope.UserID,
		ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
		Action:     action,
		EntityType: "label_template", EntityID: &id,
		Before: before, After: after,
	})
}

// Template reads one.
func (s *Service) Template(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Template, error) {
	var out Template
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, templateSelect+`
			WHERE t.id = $1 AND t.company_id = $2`, id, scope.CompanyID)
		tpl, e := scanTemplate(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That label was not found.")
		}
		out = tpl
		return e
	})
	return out, db.Translate(err, "")
}

// DeleteTemplate removes a layout.
func (s *Service) DeleteTemplate(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			// Read before removing, so the trail says WHICH layout went. A row
			// naming a uuid that no longer resolves tells an investigation
			// nothing at all.
			var name, kind string
			var wasDefault bool
			e := tx.QueryRow(ctx, `
				SELECT name, kind, is_default FROM label_template
				WHERE id = $1 AND company_id = $2`, id, scope.CompanyID).
				Scan(&name, &kind, &wasDefault)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That label was not found.")
			}
			if e != nil {
				return e
			}

			if _, e := tx.Exec(ctx, `
				DELETE FROM label_template WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID); e != nil {
				return db.Translate(e,
					"That label cannot be removed while something still uses it.")
			}
			return auditTemplate(ctx, tx, scope, "label_template_removed", id,
				map[string]any{
					"name": name, "kind": kind, "was_default": wasDefault,
				}, nil)
		}), "")
}

// describeTemplate is what an audit entry records about a layout.
//
// The field list is written out rather than counted: "6 fields" says nothing,
// and the change worth catching is a price line quietly taken off a shelf
// ticket.
func describeTemplate(in Template, fields string) map[string]any {
	return map[string]any{
		"name": in.Name, "kind": in.Kind,
		"width_mm": in.Width, "height_mm": in.Height,
		"is_default": in.IsDefault,
		"fields":     fields,
	}
}

// Sheet is what one print run needs: the layout, and a label per item.
type Sheet struct {
	Template Template `json:"template"`
	Labels   []Label  `json:"labels"`
}

// Query says which labels to produce.
//
// B3 asks for bulk generation by variant, by category and by brand — "a factory
// delivers 1,000 pieces → generate all 1,000 in one click" — and for a quantity
// per item, because a delivery of thirty of one shirt needs thirty tags.
type Query struct {
	TemplateID *uuid.UUID
	Kind       string
	VariantIDs []uuid.UUID
	CategoryID *uuid.UUID
	BrandID    *uuid.UUID
	Search     string
	// Copies repeats each label. Held here rather than left to the browser to
	// duplicate, so what is printed and what was asked for cannot diverge.
	Copies int
}

// Build assembles a print run.
func (s *Service) Build(ctx context.Context, scope Scope, q Query) (Sheet, error) {
	if q.Copies <= 0 {
		q.Copies = 1
	}
	if q.Copies > 100 {
		return Sheet{}, errs.New(errs.CodeInvalidInput,
			"A hundred copies of one label is the most this will print at once.")
	}

	var out Sheet
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tpl, e := s.pickTemplate(ctx, tx, scope, q)
		if e != nil {
			return e
		}
		out.Template = tpl

		scheme, e := readScheme(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			return e
		}

		// Resolved once for the run, on the transaction the caller already
		// holds. Asking the pool for a second connection while holding this one
		// is the deadlock that froze the tills; registry.Query.Tx exists so it
		// cannot happen again.
		rate, e := s.rules.VATRate(ctx, tx, country, time.Now().UTC(),
			scope.TenantID)
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx, labelSelect+`
			WHERE v.company_id = $1 AND v.is_active
			  AND ($2::uuid[] IS NULL OR v.id = ANY($2))
			  AND ($3::uuid IS NULL OR p.category_id = $3)
			  AND ($4::uuid IS NULL OR p.brand_id = $4)
			  AND ($5 = '' OR p.name ILIKE '%' || $5 || '%'
			                OR v.sku ILIKE '%' || $5 || '%')
			ORDER BY p.name, v.sku
			LIMIT 2000`,
			scope.CompanyID, nullIDs(q.VariantIDs), q.CategoryID, q.BrandID,
			strings.TrimSpace(q.Search))
		if e != nil {
			return e
		}
		defer rows.Close()

		out.Labels = []Label{}
		for rows.Next() {
			label, e := scanLabel(rows, scheme, currency, rate)
			if e != nil {
				return e
			}
			for i := 0; i < q.Copies; i++ {
				out.Labels = append(out.Labels, label)
			}
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// pickTemplate resolves which layout to use.
func (s *Service) pickTemplate(
	ctx context.Context, tx pgx.Tx, scope Scope, q Query,
) (Template, error) {
	if q.TemplateID != nil {
		row := tx.QueryRow(ctx, templateSelect+`
			WHERE t.id = $1 AND t.company_id = $2`, *q.TemplateID, scope.CompanyID)
		tpl, err := scanTemplate(row)
		if err == pgx.ErrNoRows {
			return Template{}, errs.New(errs.CodeNotFound,
				"That label was not found.")
		}
		return tpl, err
	}

	kind := q.Kind
	if kind == "" {
		kind = KindThermal
	}
	row := tx.QueryRow(ctx, templateSelect+`
		WHERE t.company_id = $1 AND t.kind = $2
		ORDER BY t.is_default DESC, t.name LIMIT 1`, scope.CompanyID, kind)
	tpl, err := scanTemplate(row)
	if err == pgx.ErrNoRows {
		return Template{}, errs.Newf(errs.CodeNotFound,
			"This company has no %s label set up.", strings.ReplaceAll(kind, "_", " "))
	}
	return tpl, err
}

const templateSelect = `
	SELECT t.id, t.name, t.kind, t.width_mm, t.height_mm, t.columns, t.rows,
	       t.margin_mm, t.gap_mm, t.fields::text, t.is_default
	FROM label_template t`

// labelSelect reads everything a label prints, except the rate.
//
// The treatment travels with the row and the RATE is resolved once per print
// run through registry.VATRate — the single accessor every tax computation in
// this product goes through. Reading the registry table directly here would be
// a second definition of the rate, free to disagree with what the till charges,
// and a shelf ticket that disagrees with the till is the one error on this
// screen a customer notices and a regulator cares about.
const labelSelect = `
	SELECT v.id, v.sku, coalesce(v.barcode, ''), p.name,
	       coalesce(p.translations->>'ar', ''),
	       v.attributes::text,
	       coalesce(c.name, ''), coalesce(b.name, ''), coalesce(p.season, ''),
	       v.price_retail, p.tax_treatment
	FROM variant v
	JOIN product p ON p.id = v.product_id
	LEFT JOIN category c ON c.id = p.category_id
	LEFT JOIN brand b ON b.id = p.brand_id`

type scanner interface{ Scan(dest ...any) error }

func scanTemplate(row scanner) (Template, error) {
	var t Template
	var width, height, margin, gap decimal.Decimal
	var fields string
	if err := row.Scan(&t.ID, &t.Name, &t.Kind, &width, &height, &t.Columns,
		&t.Rows, &margin, &gap, &fields, &t.IsDefault); err != nil {
		return Template{}, err
	}
	if err := json.Unmarshal([]byte(fields), &t.Fields); err != nil {
		return Template{}, err
	}
	t.Width = width.String()
	t.Height = height.String()
	t.Margin = margin.String()
	t.Gap = gap.String()
	if t.Columns != nil && t.Rows != nil {
		t.PerSheet = *t.Columns * *t.Rows
	}
	return t, nil
}

func scanLabel(
	row scanner, scheme Scheme, currency string, standardRate decimal.Decimal,
) (Label, error) {
	var l Label
	var attributes, treatment string
	var price decimal.Decimal
	if err := row.Scan(&l.VariantID, &l.SKU, &l.Barcode, &l.Name, &l.NameAr,
		&attributes, &l.Category, &l.Brand, &l.Season, &price,
		&treatment); err != nil {
		return Label{}, err
	}

	// Only a standard-rated product carries the rate. A zero-rated or exempt
	// garment priced as though it were standard would put a tag on the rail
	// charging fifteen per cent tax the shop must not collect.
	rate := decimal.Zero
	if treatment == "standard" {
		rate = standardRate
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(attributes), &parsed); err != nil {
		return Label{}, err
	}
	l.Attributes = describe(parsed)

	l.Symbology = scheme.Symbology
	l.Readable = buildReadable(scheme, l.Category, l.Brand, l.Season, parsed, l.SKU)
	if l.Barcode == "" {
		// Nothing has been generated for this variant yet. The SKU is printed
		// rather than an empty barcode, because a tag with a blank where the
		// bars should be is a tag somebody puts on a garment and then cannot
		// scan — and the SKU at least identifies it by eye.
		l.Barcode = l.SKU
	}

	// The shelf price, printed as it is held.
	//
	// `price_retail` is ALREADY VAT-inclusive in this product: the till prices
	// with PricesIncludeTax defaulting to true, so a 115.00 abaya rings up at
	// 115.00 and the 15.00 of tax inside it is derived by division. Adding the
	// rate here would print 132.25 on the tag of an item that sells for 115.00
	// — a wrong price physically attached to a garment, which is the one error
	// on this screen a customer notices at the counter.
	//
	// The rate travels alongside so a tag can say "VAT included", which B3
	// asks for and which is a caption rather than a calculation.
	l.Price = money(price.Round(2))
	l.Currency = currency
	l.TaxRate = rate.String()
	return l, nil
}

// describe turns a variant's attributes into the line under the name.
//
// Values only — "Black · XL" rather than "colour: Black · size: XL" — because
// the labels are what fits on 25 millimetres and a person looking at a black
// extra-large shirt does not need to be told which is which.
func describe(attributes map[string]string) string {
	// Colour and size first, in that order, because that is how clothing is
	// described out loud. Map order would put them differently on every label
	// printed, which is how a shelf of tags ends up unreadable.
	var parts []string
	for _, key := range []string{"colour", "color", "size"} {
		if v := strings.TrimSpace(attributes[key]); v != "" {
			parts = append(parts, v)
		}
	}
	for key, value := range attributes {
		switch key {
		case "colour", "color", "size":
			continue
		}
		if v := strings.TrimSpace(value); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " · ")
}

// PrintableFields are the things a label can carry, and the ONLY ones.
//
// Not a suggestion list: this is exactly the set the studio's renderer has a
// case for and the print run has data for. A template naming anything else
// produces a label with a silently missing line — the layout is right, the
// price is absent, and nobody discovers it until nine hundred tags are on a
// rail.
//
// So an unknown field is refused here rather than dropped, and the message
// names what there is. Adding a seventh means adding it to `Label`, to the
// print run's query, and to the renderer; a screen cannot introduce one by
// sending a word.
var PrintableFields = []string{
	"logo",          // the shop's mark
	"name",          // the product, as the catalogue holds it
	"name_ar",       // and its Arabic name, printed right to left
	"attributes",    // colour, size — the variant's own axes
	"price",         // VAT-inclusive, which is what a shelf ticket means
	"barcode",       // the bars, plus the human-readable string beneath
	"customer_name", // a loyalty card, which carries a person rather than a garment
}

func printable(field string) bool {
	for _, f := range PrintableFields {
		if f == field {
			return true
		}
	}
	return false
}

// checkFields refuses a field the printer cannot fill.
func checkFields(fields []Field) error {
	for i, f := range fields {
		name := strings.TrimSpace(f.Field)
		if printable(name) {
			continue
		}
		return errs.Validation("That label asks for something it cannot print.").
			WithField("fields",
				fmt.Sprintf("Line %d says %q. A label can carry: %s.",
					i+1, f.Field, strings.Join(PrintableFields, ", ")))
	}
	return nil
}

func knownKind(kind string) bool {
	switch kind {
	case KindHangTag, KindThermal, KindA4Sheet, KindLoyaltyCard:
		return true
	}
	return false
}

func amountOr(s string) decimal.Decimal {
	v, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return decimal.Zero
	}
	return v
}
