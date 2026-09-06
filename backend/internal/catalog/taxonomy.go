package catalog

// Categories, brands and units of measure — the three tables a product hangs
// off, and the three that nothing in the product could reach.
//
// # How this was found
//
// `POST /catalog/products` accepts `category_id`, `brand_id` and `unit_id`,
// and the payroll engine filters a commission scheme's takings by
// `category_id` and `brand_id`. Both read ids that NOTHING could produce:
// there was no route to list a category, none to create one, and no screen
// anywhere. `devseed` writes ten categories, which is why the columns looked
// populated in development and were unreachable in a real business.
//
// So a shop could not put its products into departments, could not record a
// brand at all, and could not write a commission scheme for one — not because
// the feature was missing, but because the only way to name a category was to
// already know a UUID.
//
// # Retired, not deleted
//
// `product_category_id_fkey` is ON DELETE RESTRICT, so a category anything has
// ever been filed under cannot be removed. That is the right constraint, and
// it makes "delete" a button that works until the shop has used the thing.
// `is_active` is what somebody pressing delete actually wants — stop offering
// it — and it leaves every historical product still explicable.
//
// # Categories nest, and the nesting is maintained here
//
// `path` and `depth` are derived from `parent_id`: the path is the ancestors
// in order, the depth is how many there are. Both are stored because a
// category tree is read far more often than it is written, and recomputing an
// ancestry on every product list would be a recursive query per row. They are
// computed on write, never accepted from the caller, so the two cannot
// disagree with `parent_id`.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// maxCategoryDepth stops a tree nobody can read, and stops a cycle turning a
// path computation into an infinite loop. Five is deeper than any retail
// hierarchy that stays navigable.
const maxCategoryDepth = 5

// Category is one department in the product tree.
type Category struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	NameAr   string     `json:"name_ar,omitempty"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	// Depth is how many ancestors it has, so a screen can indent without
	// walking the tree itself.
	Depth     int  `json:"depth"`
	SortOrder int  `json:"sort_order"`
	IsActive  bool `json:"is_active"`
	// Products is how many are filed under it. On the list because it is the
	// answer to "may I retire this", asked at the moment somebody wants to.
	Products int `json:"product_count"`
}

// Brand is a maker, for reporting and for a commission scheme scoped to one.
type Brand struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar,omitempty"`
	IsActive bool      `json:"is_active"`
	Products int       `json:"product_count"`
}

// Unit is how a product is counted or measured.
type Unit struct {
	ID     uuid.UUID `json:"id"`
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	NameAr string    `json:"name_ar,omitempty"`
	// AllowsFraction decides whether half of one may be sold. A metre of cloth
	// may be cut; a shirt may not.
	AllowsFraction bool `json:"allows_fraction"`
	IsActive       bool `json:"is_active"`
	Products       int  `json:"product_count"`
}

// arabicOf reads the Arabic name out of the `translations` object.
//
// One column rather than a `name_ar` beside every `name`: the three tables
// already carried `translations jsonb`, and adding a second place to put the
// same fact would let the two disagree.
const arabicOf = `coalesce(t.translations ->> 'ar', '')`

// --- categories ----------------------------------------------------------

// Categories lists the tree, parents before children.
func (s *Service) Categories(
	ctx context.Context, tenantID, companyID uuid.UUID, includeRetired bool,
) ([]Category, error) {
	out := []Category{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT t.id, t.name, `+arabicOf+`, t.parent_id, t.depth,
			       t.sort_order, t.is_active,
			       (SELECT count(*) FROM product p WHERE p.category_id = t.id)
			FROM category t
			WHERE t.company_id = $1 AND ($2 OR t.is_active)
			ORDER BY t.path, t.sort_order, t.name`, companyID, includeRetired)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var c Category
			if e := rows.Scan(&c.ID, &c.Name, &c.NameAr, &c.ParentID, &c.Depth,
				&c.SortOrder, &c.IsActive, &c.Products); e != nil {
				return e
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "Those categories could not be read.")
}

// NewCategory is a department being created or amended.
type NewCategory struct {
	Name      string
	NameAr    string
	ParentID  *uuid.UUID
	SortOrder int
}

// CreateCategory files a new department under an optional parent.
func (s *Service) CreateCategory(
	ctx context.Context, tenantID, companyID, userID uuid.UUID, in NewCategory,
) (Category, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Category{}, errs.Validation("Give the category a name.").
			WithField("name", "It is what staff will look for the product under.")
	}

	var out Category
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		path, depth, e := ancestryOf(ctx, tx, companyID, in.ParentID)
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO category
			  (tenant_id, company_id, parent_id, name, translations, path,
			   depth, sort_order)
			VALUES ($1,$2,$3,$4, jsonb_build_object('ar', $5::text), $6, $7, $8)
			RETURNING id`,
			tenantID, companyID, in.ParentID, strings.TrimSpace(in.Name),
			strings.TrimSpace(in.NameAr), path, depth, in.SortOrder,
		).Scan(&id); e != nil {
			return db.Translate(e, "That category could not be saved.")
		}
		read, e := s.readCategory(ctx, tx, companyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// UpdateCategory renames a department and may move it.
//
// Moving one rewrites the stored ancestry of everything beneath it, which is
// the cost of storing the path. Done in the same transaction as the move, so a
// failure cannot leave half the tree claiming an ancestor it no longer has.
func (s *Service) UpdateCategory(
	ctx context.Context, tenantID, companyID, id uuid.UUID, in NewCategory,
) (Category, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Category{}, errs.Validation("Give the category a name.").
			WithField("name", "It is what staff will look for the product under.")
	}
	if in.ParentID != nil && *in.ParentID == id {
		return Category{}, errs.Validation(
			"A category cannot be filed under itself.").
			WithField("parent_id", "Choose a different parent, or none.")
	}

	var out Category
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// A category cannot move under its own descendant: the tree would
		// become a ring, and every path computation on it would run forever.
		if in.ParentID != nil {
			var wouldLoop bool
			if e := tx.QueryRow(ctx, `
				SELECT $1 = ANY(path) FROM category
				WHERE id = $2 AND company_id = $3`,
				id, *in.ParentID, companyID).Scan(&wouldLoop); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.New(errs.CodeNotFound,
						"That parent category was not found.")
				}
				return e
			}
			if wouldLoop {
				return errs.Validation(
					"A category cannot be filed under one of its own subcategories.").
					WithField("parent_id", "Choose a parent outside this branch.")
			}
		}

		path, depth, e := ancestryOf(ctx, tx, companyID, in.ParentID)
		if e != nil {
			return e
		}

		tag, e := tx.Exec(ctx, `
			UPDATE category
			SET name = $4,
			    translations = jsonb_set(translations, '{ar}', to_jsonb($5::text)),
			    parent_id = $6, path = $7, depth = $8, sort_order = $9
			WHERE id = $1 AND company_id = $2 AND tenant_id = $3`,
			id, companyID, tenantID, strings.TrimSpace(in.Name),
			strings.TrimSpace(in.NameAr), in.ParentID, path, depth, in.SortOrder)
		if e != nil {
			return db.Translate(e, "That category could not be saved.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That category was not found.")
		}

		// Everything beneath it now sits somewhere else. `path` is the
		// ancestry in order, so a descendant's new path is this one's path,
		// plus this one, plus whatever of its own path came after this one.
		if _, e := tx.Exec(ctx, `
			UPDATE category d
			SET path  = $4::uuid[] || $1::uuid
			          || d.path[array_position(d.path, $1) + 1 : array_length(d.path, 1)],
			    depth = $5 + 1 + (array_length(d.path, 1) - array_position(d.path, $1))
			WHERE d.company_id = $2 AND d.tenant_id = $3 AND $1 = ANY(d.path)`,
			id, companyID, tenantID, path, depth); e != nil {
			return db.Translate(e, "The categories beneath it could not be moved.")
		}

		read, e := s.readCategory(ctx, tx, companyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// SetCategoryActive retires a department or brings it back.
//
// Retiring one retires everything under it: a subcategory of a department
// nobody may file under any more is not a department anybody may file under
// either, and leaving the children on offer would let somebody put a product
// into a branch of a tree that has been closed.
func (s *Service) SetCategoryActive(
	ctx context.Context, tenantID, companyID, id uuid.UUID, active bool,
) error {
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE category SET is_active = $4
			WHERE company_id = $2 AND tenant_id = $3
			  AND (id = $1 OR (NOT $4 AND $1 = ANY(path)))`,
			id, companyID, tenantID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That category was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// ancestryOf answers the path and depth a child of `parentID` would have.
func ancestryOf(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, parentID *uuid.UUID,
) ([]uuid.UUID, int, error) {
	if parentID == nil {
		return []uuid.UUID{}, 0, nil
	}
	var path []uuid.UUID
	var depth int
	if err := tx.QueryRow(ctx,
		`SELECT path, depth FROM category WHERE id = $1 AND company_id = $2`,
		*parentID, companyID).Scan(&path, &depth); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, errs.New(errs.CodeNotFound,
				"That parent category was not found.")
		}
		return nil, 0, err
	}
	if depth+1 > maxCategoryDepth {
		return nil, 0, errs.Newf(errs.CodeInvalidInput,
			"Categories can be nested %d deep, and this would be one deeper.",
			maxCategoryDepth).
			WithField("parent_id", "Choose a parent nearer the top.")
	}
	return append(append([]uuid.UUID{}, path...), *parentID), depth + 1, nil
}

func (s *Service) readCategory(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Category, error) {
	var c Category
	err := tx.QueryRow(ctx, `
		SELECT t.id, t.name, `+arabicOf+`, t.parent_id, t.depth, t.sort_order,
		       t.is_active,
		       (SELECT count(*) FROM product p WHERE p.category_id = t.id)
		FROM category t WHERE t.id = $1 AND t.company_id = $2`, id, companyID).
		Scan(&c.ID, &c.Name, &c.NameAr, &c.ParentID, &c.Depth, &c.SortOrder,
			&c.IsActive, &c.Products)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, errs.New(errs.CodeNotFound, "That category was not found.")
	}
	return c, err
}

// --- brands --------------------------------------------------------------

// Brands lists the makers this company stocks.
func (s *Service) Brands(
	ctx context.Context, tenantID, companyID uuid.UUID, includeRetired bool,
) ([]Brand, error) {
	out := []Brand{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT t.id, t.name, `+arabicOf+`, t.is_active,
			       (SELECT count(*) FROM product p WHERE p.brand_id = t.id)
			FROM brand t
			WHERE t.company_id = $1 AND ($2 OR t.is_active)
			ORDER BY t.name`, companyID, includeRetired)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var b Brand
			if e := rows.Scan(&b.ID, &b.Name, &b.NameAr, &b.IsActive,
				&b.Products); e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "Those brands could not be read.")
}

// SaveBrand creates a maker, or renames one when id is not nil.
func (s *Service) SaveBrand(
	ctx context.Context, tenantID, companyID uuid.UUID, id *uuid.UUID,
	name, nameAr string,
) (Brand, error) {
	if strings.TrimSpace(name) == "" {
		return Brand{}, errs.Validation("Give the brand a name.").
			WithField("name", "It is what a report will group by.")
	}

	var out Brand
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var saved uuid.UUID
		if id == nil {
			// `brand_name_uq` is on lower(name), so a second "Adidas" is
			// refused by the database. db.Translate turns that into a sentence
			// rather than a constraint name.
			if e := tx.QueryRow(ctx, `
				INSERT INTO brand (tenant_id, company_id, name, translations)
				VALUES ($1,$2,$3, jsonb_build_object('ar', $4::text))
				RETURNING id`,
				tenantID, companyID, strings.TrimSpace(name),
				strings.TrimSpace(nameAr)).Scan(&saved); e != nil {
				return db.Translate(e,
					"That brand could not be saved. A brand with that name "+
						"may already exist.")
			}
		} else {
			saved = *id
			tag, e := tx.Exec(ctx, `
				UPDATE brand
				SET name = $4,
				    translations = jsonb_set(translations, '{ar}', to_jsonb($5::text))
				WHERE id = $1 AND company_id = $2 AND tenant_id = $3`,
				saved, companyID, tenantID, strings.TrimSpace(name),
				strings.TrimSpace(nameAr))
			if e != nil {
				return db.Translate(e,
					"That brand could not be saved. A brand with that name "+
						"may already exist.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound, "That brand was not found.")
			}
		}

		var b Brand
		if e := tx.QueryRow(ctx, `
			SELECT t.id, t.name, `+arabicOf+`, t.is_active,
			       (SELECT count(*) FROM product p WHERE p.brand_id = t.id)
			FROM brand t WHERE t.id = $1 AND t.company_id = $2`,
			saved, companyID).
			Scan(&b.ID, &b.Name, &b.NameAr, &b.IsActive, &b.Products); e != nil {
			return e
		}
		out = b
		return nil
	})
	return out, db.Translate(err, "")
}

// SetBrandActive retires a maker or brings it back.
func (s *Service) SetBrandActive(
	ctx context.Context, tenantID, companyID, id uuid.UUID, active bool,
) error {
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE brand SET is_active = $4
			WHERE id = $1 AND company_id = $2 AND tenant_id = $3`,
			id, companyID, tenantID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That brand was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// --- units of measure ----------------------------------------------------

// Units lists how this company counts and measures what it sells.
func (s *Service) Units(
	ctx context.Context, tenantID, companyID uuid.UUID, includeRetired bool,
) ([]Unit, error) {
	out := []Unit{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT t.id, t.code, t.name, `+arabicOf+`, t.allows_fraction,
			       t.is_active,
			       (SELECT count(*) FROM product p WHERE p.unit_id = t.id)
			FROM unit_of_measure t
			WHERE t.company_id = $1 AND ($2 OR t.is_active)
			ORDER BY t.code`, companyID, includeRetired)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var u Unit
			if e := rows.Scan(&u.ID, &u.Code, &u.Name, &u.NameAr,
				&u.AllowsFraction, &u.IsActive, &u.Products); e != nil {
				return e
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "Those units could not be read.")
}

// NewUnit is a unit of measure being created or amended.
type NewUnit struct {
	Code           string
	Name           string
	NameAr         string
	AllowsFraction bool
}

// SaveUnit creates a unit, or amends one when id is not nil.
//
// The code does not move once set: it is what appears on an invoice line, and
// changing it would rewrite the meaning of every document already issued.
func (s *Service) SaveUnit(
	ctx context.Context, tenantID, companyID uuid.UUID, id *uuid.UUID, in NewUnit,
) (Unit, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Unit{}, errs.Validation("Give the unit a name.").
			WithField("name", "\"Piece\", \"Kilogram\", \"Metre\".")
	}
	if id == nil && strings.TrimSpace(in.Code) == "" {
		return Unit{}, errs.Validation("Give the unit a short code.").
			WithField("code", "It is what appears on an invoice line: PC, KG, M.")
	}

	var out Unit
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var saved uuid.UUID
		if id == nil {
			if e := tx.QueryRow(ctx, `
				INSERT INTO unit_of_measure
				  (tenant_id, company_id, code, name, translations, allows_fraction)
				VALUES ($1,$2,upper($3),$4,
				        jsonb_build_object('ar', $5::text), $6)
				RETURNING id`,
				tenantID, companyID, strings.TrimSpace(in.Code),
				strings.TrimSpace(in.Name), strings.TrimSpace(in.NameAr),
				in.AllowsFraction).Scan(&saved); e != nil {
				return db.Translate(e,
					"That unit could not be saved. A unit with that code may "+
						"already exist.")
			}
		} else {
			saved = *id
			tag, e := tx.Exec(ctx, `
				UPDATE unit_of_measure
				SET name = $4,
				    translations = jsonb_set(translations, '{ar}', to_jsonb($5::text)),
				    allows_fraction = $6
				WHERE id = $1 AND company_id = $2 AND tenant_id = $3`,
				saved, companyID, tenantID, strings.TrimSpace(in.Name),
				strings.TrimSpace(in.NameAr), in.AllowsFraction)
			if e != nil {
				return db.Translate(e, "That unit could not be saved.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound, "That unit was not found.")
			}
		}

		var u Unit
		if e := tx.QueryRow(ctx, `
			SELECT t.id, t.code, t.name, `+arabicOf+`, t.allows_fraction,
			       t.is_active,
			       (SELECT count(*) FROM product p WHERE p.unit_id = t.id)
			FROM unit_of_measure t WHERE t.id = $1 AND t.company_id = $2`,
			saved, companyID).
			Scan(&u.ID, &u.Code, &u.Name, &u.NameAr, &u.AllowsFraction,
				&u.IsActive, &u.Products); e != nil {
			return e
		}
		out = u
		return nil
	})
	return out, db.Translate(err, "")
}

// SetUnitActive retires a unit or brings it back.
func (s *Service) SetUnitActive(
	ctx context.Context, tenantID, companyID, id uuid.UUID, active bool,
) error {
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE unit_of_measure SET is_active = $4
			WHERE id = $1 AND company_id = $2 AND tenant_id = $3`,
			id, companyID, tenantID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That unit was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// --- what tax treatments this company may use ----------------------------

// Treatment is one tax treatment a product may carry, and what it implies.
type Treatment struct {
	Code string `json:"code"`
	// NeedsReason is true when choosing it obliges the product to carry an
	// exemption reason code. ZATCA requires the reason for any non-standard
	// treatment to be identified on the invoice; a US exempt sale needs a
	// certificate held against the customer, which serves the same purpose.
	NeedsReason bool `json:"needs_reason"`
}

// TreatmentOptions is the answer to "what may I choose here".
type TreatmentOptions struct {
	Country string `json:"country"`
	// Model is "vat" or "sales_tax". The two are not interchangeable and a
	// screen written for one produces confidently wrong words in the other.
	Model      string      `json:"model"`
	Treatments []Treatment `json:"treatments"`
}

// TreatmentsFor answers which tax treatments a company's country allows.
//
// A product form that offered every treatment the product has ever heard of
// would let somebody choose "zero_rated" in a US catalogue and be refused on
// save with a list they should have been shown in the first place. The
// registry already knows the answer per country and per date; this is the only
// route that asks it on a screen's behalf.
//
// Resolved as of today rather than as of a date the caller names: a form is
// being filled in now, and validating what it sends is the write path's job
// against the date the product is actually priced on.
func (s *Service) TreatmentsFor(
	ctx context.Context, tenantID, companyID uuid.UUID,
) (TreatmentOptions, error) {
	var out TreatmentOptions
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, companyID).
			Scan(&country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		rules, e := TaxRulesFor(ctx, s.rules, tx, country, time.Now().UTC(), tenantID)
		if e != nil {
			return e
		}

		codes := append([]string(nil), rules.Treatments...)
		sort.Strings(codes)
		out = TreatmentOptions{
			Country:    strings.ToUpper(rules.Country),
			Model:      string(rules.Model),
			Treatments: make([]Treatment, 0, len(codes)),
		}
		for _, code := range codes {
			out.Treatments = append(out.Treatments, Treatment{
				Code:        code,
				NeedsReason: RequiresExemptionReason(rules, code),
			})
		}
		return nil
	})
	return out, db.Translate(err, "")
}
