package provisioning

// The label studio a new company starts with (blueprint B3).
//
// # Seeded, not left empty
//
// 0095 gave every EXISTING company a starting set of templates and a barcode
// scheme. A company created afterwards would have had neither, and the label
// studio would have opened on an empty list with a print button that answered
// "this company has no thermal label set up" — the same shape of defect as a
// company with no fiscal period or no stock location: a layer that works, with
// no path through it.
//
// # These are not preferences
//
// 50×25 and 38×25 are what the label rolls come in, and a sheet of 24 is what
// the box of A4 labels says on it. A shop opening the studio to nothing would
// have to measure a sticker before they could print one. Everything here is
// editable; none of it is a guess about what the shop wants, only about what
// the hardware is.

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedLabel is one starting template.
type seedLabel struct {
	name      string
	kind      string
	width     string
	height    string
	columns   *int
	rows      *int
	margin    string
	gap       string
	fields    string
	isDefault bool
}

func grid(n int) *int { return &n }

// defaultLabels is the same set 0095 seeded, kept here so the two paths into a
// company — a migration and the onboarding wizard — produce the same shop.
var defaultLabels = []seedLabel{
	{
		name: "Hang tag", kind: "hang_tag", width: "50", height: "80",
		margin: "3", gap: "0", isDefault: true,
		fields: `[{"field":"logo"},{"field":"name","size":10,"bold":true},
		          {"field":"name_ar","size":10,"rtl":true},
		          {"field":"attributes","size":8},
		          {"field":"price","size":13,"bold":true},
		          {"field":"barcode","height":14}]`,
	},
	{
		name: "Thermal 50x25", kind: "thermal", width: "50", height: "25",
		margin: "1", gap: "0", isDefault: true,
		fields: `[{"field":"name","size":7},{"field":"price","size":9,"bold":true},
		          {"field":"barcode","height":10}]`,
	},
	{
		name: "Thermal 38x25", kind: "thermal", width: "38", height: "25",
		margin: "1", gap: "0",
		fields: `[{"field":"name","size":6},{"field":"price","size":8,"bold":true},
		          {"field":"barcode","height":9}]`,
	},
	{
		name: "A4 sheet of 24", kind: "a4_sheet", width: "63.5", height: "33.9",
		columns: grid(3), rows: grid(8), margin: "8", gap: "2.5",
		isDefault: true,
		fields: `[{"field":"name","size":7},{"field":"price","size":9,"bold":true},
		          {"field":"barcode","height":10}]`,
	},
	{
		name: "A4 sheet of 30", kind: "a4_sheet", width: "63.5", height: "25.4",
		columns: grid(3), rows: grid(10), margin: "8", gap: "0",
		fields: `[{"field":"name","size":6},{"field":"price","size":8,"bold":true},
		          {"field":"barcode","height":9}]`,
	},
	{
		name: "Loyalty card", kind: "loyalty_card", width: "85.6",
		height: "53.98", margin: "4", gap: "0", isDefault: true,
		fields: `[{"field":"logo"},{"field":"customer_name","size":11,"bold":true},
		          {"field":"tier","size":9},{"field":"barcode","height":12}]`,
	},
}

// SeedLabelStudio gives a new company its label templates and barcode scheme.
//
// Idempotent, like the chart of accounts and for the same reason: a retried
// onboarding step must not produce two of everything, and a company that
// already has templates keeps the ones it edited.
//
// Exported because a company can be created by more than the onboarding wizard,
// and every one of those paths needs this or the studio opens on nothing.
func SeedLabelStudio(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
) error {
	for _, l := range defaultLabels {
		if _, err := tx.Exec(ctx, `
			INSERT INTO label_template
			  (tenant_id, company_id, name, kind, width_mm, height_mm, columns,
			   rows, margin_mm, gap_mm, fields, is_default)
			VALUES ($1,$2,$3,$4,$5::numeric,$6::numeric,$7,$8,$9::numeric,
			        $10::numeric,$11::jsonb,$12)
			ON CONFLICT DO NOTHING`,
			tenantID, companyID, l.name, l.kind, l.width, l.height, l.columns,
			l.rows, l.margin, l.gap, l.fields, l.isDefault); err != nil {
			return err
		}
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO barcode_scheme (tenant_id, company_id)
		VALUES ($1,$2) ON CONFLICT (company_id) DO NOTHING`,
		tenantID, companyID)
	return err
}
