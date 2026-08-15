package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service manages the catalogue.
type Service struct {
	pool  *db.Pool
	rules *registry.Service
}

func NewService(pool *db.Pool, rules *registry.Service) *Service {
	return &Service{pool: pool, rules: rules}
}

// NewProduct is a product being created.
type NewProduct struct {
	CompanyID uuid.UUID

	SKU         string
	Name        string
	NameAr      string
	Description string

	CategoryID *uuid.UUID
	BrandID    *uuid.UUID
	UnitID     *uuid.UUID

	// TaxTreatment is validated against the country's list from the regulatory
	// registry. "standard" is meaningful in Saudi Arabia and meaningless in the
	// USA, where the equivalent is "taxable", so the valid set cannot be a
	// constant in this file.
	TaxTreatment       string
	TaxExemptionReason string
	TrackSerial        bool
	TrackBatch         bool
	WarrantyMonths     *int
	CreatedBy          *uuid.UUID
}

// Product is a stored product.
type Product struct {
	ID           uuid.UUID `json:"id"`
	SKU          string    `json:"sku"`
	Name         string    `json:"name"`
	TaxTreatment string    `json:"tax_treatment"`
	Lifecycle    string    `json:"lifecycle"`
	VariantCount int       `json:"variant_count"`
}

// CreateProduct adds a product after checking its tax treatment is one this
// country recognises.
func (s *Service) CreateProduct(
	ctx context.Context, tenantID uuid.UUID, in NewProduct,
) (Product, error) {
	if in.SKU == "" || in.Name == "" {
		return Product{}, errs.New(errs.CodeInvalidInput,
			"A product needs a code and a name.")
	}

	var out Product
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		country, e := companyCountry(ctx, tx, in.CompanyID)
		if e != nil {
			return e
		}

		if e := s.checkTreatment(ctx, country, tenantID, in); e != nil {
			return e
		}

		translations := map[string]string{}
		if in.NameAr != "" {
			translations["ar"] = in.NameAr
		}

		e = tx.QueryRow(ctx, `
			INSERT INTO product
			  (tenant_id, company_id, sku, name, translations, description,
			   category_id, brand_id, unit_id, tax_treatment,
			   tax_exemption_reason_code, track_serial, track_batch,
			   warranty_months, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING id, sku, name, tax_treatment, lifecycle::text`,
			tenantID, in.CompanyID, in.SKU, in.Name, translations,
			nullText(in.Description), in.CategoryID, in.BrandID, in.UnitID,
			in.TaxTreatment, nullText(in.TaxExemptionReason),
			in.TrackSerial, in.TrackBatch, in.WarrantyMonths, in.CreatedBy).
			Scan(&out.ID, &out.SKU, &out.Name, &out.TaxTreatment, &out.Lifecycle)

		return db.Translate(e,
			"That product could not be created. A product code must be unique "+
				"within the company.")
	})
	return out, err
}

// checkTreatment refuses a treatment the country does not recognise, and
// insists on a reason where one is required.
//
// Both come from the registry rather than from a list in code. ZATCA requires
// the reason for any non-standard treatment to appear on the invoice, and an
// invoice missing it is rejected — so the moment to catch it is when the
// product is set up, not when a customer is waiting at the till.
func (s *Service) checkTreatment(
	ctx context.Context, country string, tenantID uuid.UUID, in NewProduct,
) error {
	rules, err := TaxRulesFor(ctx, s.rules, country, time.Now().UTC(), tenantID)
	if err != nil {
		return err
	}
	if err := ValidateTreatment(rules, in.TaxTreatment); err != nil {
		return err
	}
	if RequiresExemptionReason(rules, in.TaxTreatment) && in.TaxExemptionReason == "" {
		return errs.Newf(errs.CodeInvalidInput,
			"A %q product needs an exemption reason code, because it has to "+
				"appear on every invoice that sells it.", in.TaxTreatment)
	}
	return nil
}

// ListProducts returns a company's products, newest first.
//
// Cursored rather than offset-paged. An offset walks and discards every row
// before the page, so page 400 of a large catalogue costs four hundred times
// page one — and rows shift under an offset while someone is reading.
func (s *Service) ListProducts(
	ctx context.Context, tenantID, companyID uuid.UUID, search string, limit int, after *uuid.UUID,
) ([]Product, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	out := []Product{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT p.id, p.sku, p.name, p.tax_treatment, p.lifecycle::text,
			       (SELECT count(*) FROM variant v WHERE v.product_id = p.id)
			FROM product p
			WHERE p.company_id = $1
			  AND ($2::text IS NULL
			       OR p.name ILIKE '%' || $2 || '%'
			       OR p.sku  ILIKE '%' || $2 || '%')
			  AND ($3::uuid IS NULL OR p.id > $3::uuid)
			ORDER BY p.id
			LIMIT $4`,
			companyID, nullText(search), after, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var p Product
			if e := rows.Scan(&p.ID, &p.SKU, &p.Name, &p.TaxTreatment,
				&p.Lifecycle, &p.VariantCount); e != nil {
				return e
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// FindByBarcode is the till's lookup: one scan, one variant.
func (s *Service) FindByBarcode(
	ctx context.Context, tenantID, companyID uuid.UUID, barcode string,
) (VariantSummary, error) {
	var out VariantSummary
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var attrs []byte
		var price string
		e := tx.QueryRow(ctx, `
			SELECT v.id, v.sku, v.attributes, v.price_retail::text, v.is_active
			FROM variant v
			WHERE v.company_id = $1 AND v.barcode = $2`,
			companyID, barcode).
			Scan(&out.ID, &out.SKU, &attrs, &price, &out.IsActive)

		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"Nothing in this catalogue carries that barcode.")
		}
		if e != nil {
			return e
		}
		out.Price = price
		return unmarshalAttributes(attrs, &out)
	})
	return out, err
}

func companyCountry(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (string, error) {
	var country string
	err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, companyID).Scan(&country)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return country, err
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
