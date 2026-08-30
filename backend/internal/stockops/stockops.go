// Package stockops is what a person does to stock: count it, correct it, write
// it off, move it between rooms, and decide which rooms exist at all.
//
// # Why this is not in `inventory`
//
// `inventory` owns the costing engine and the movement primitives — Receive,
// Consume, Restore — and it deliberately posts nothing. Its own documentation
// says why: "a stock package that wrote journal lines would put the posting
// rules in two places, and the two would drift." Every primitive there returns
// the figure to post and leaves the posting to its caller.
//
// This package is that caller. It knows both halves — how stock moves and what
// the movement means to the books — which is exactly the knowledge `inventory`
// refuses to hold. Keeping them apart is what stops the costing engine growing
// an opinion about accounts.
//
// # The blocker this package opens with
//
// Before migration 0078 nothing in the product had ever created a warehouse.
// The only INSERTs against that table in the repository were in test fixtures,
// so a tenant taken through the whole A5 wizard finished with stores, staff, a
// chart of accounts, a paired till, a tax profile — and every sale refused,
// because `sales.resolveWarehouse` found nowhere to sell from. Its error even
// named a screen that did not exist. Locations come first here for that reason:
// the rest of B4 is a capability, and this part was a shop that could not open.
//
// # What a movement means to the books
//
// Only some of these post. The rule is not arbitrary and it is worth stating
// once, because "why did that not create a journal entry" is the commonest
// question about a stock module:
//
//   - A write-off, wastage or a count shortfall DESTROYS value. Inventory falls
//     and an expense rises. Rule 10, `inventory.writeoff`.
//   - A count surplus is the same event in reverse and posts the mirror of it,
//     against the same account — never as income. See `adjust.go`.
//   - A transfer between two of the company's own rooms moves nothing in or out
//     of the business. The Inventory account is unchanged and there is nothing
//     to post. See `transfer.go` for why in-transit stock still has to live
//     somewhere real for C13's tie-out to survive the journey.
package stockops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service performs stock operations.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// --- stock locations ------------------------------------------------------

// Location is a place stock can be.
type Location struct {
	ID     uuid.UUID `json:"id"`
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Store  string    `json:"store,omitempty"`
	Active bool      `json:"is_active"`

	// HoldsStock reports whether anything is on hand here. The screen needs it
	// to explain, before the press rather than after, why a location cannot be
	// retired — retiring a room full of stock would hide the stock rather than
	// remove it, and C13's valuation would still be counting it.
	HoldsStock bool `json:"holds_stock"`
}

// Kinds a person may create. `transit` is missing on purpose: it is
// system-owned, one per company, and holding stock in it is not a thing anybody
// does deliberately — see transfer.go.
var creatableKinds = map[string]bool{
	KindShopFloor: true,
	KindStoreRoom: true,
	KindCentral:   true,
}

// The four kinds, matching the CHECK constraint in 0078.
const (
	KindShopFloor = "shop_floor"
	KindStoreRoom = "store_room"
	KindCentral   = "central"
	KindTransit   = "transit"
)

// NewLocation is a place being added.
type NewLocation struct {
	Code string
	Name string
	Kind string

	// StoreID is the branch this room is at. Required for a shop floor or a
	// store room, refused for a central warehouse — a central warehouse serves
	// every branch, which is the whole of what makes it central.
	StoreID *uuid.UUID
}

// Branch is a store a location can belong to.
type Branch struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

// Places is the locations and the branches together.
//
// One reply rather than two requests, because the form that adds a location
// needs both and neither is useful without the other. It also keeps the branch
// list behind `inventory.view`: the only other list of stores in the product is
// on the devices route, and asking a storeman to hold `devices.view` so they
// can name the room they work in would be a permission granted for a screen it
// has nothing to do with.
type Places struct {
	Data     []Location `json:"data"`
	Branches []Branch   `json:"branches"`
}

// Locations lists where stock can be, and the branches one can belong to.
func (s *Service) Locations(
	ctx context.Context, scope Scope, includeRetired bool,
) (Places, error) {
	out := Places{Data: []Location{}, Branches: []Branch{}}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT w.id, w.code, w.name, w.kind,
			       coalesce(st.name, ''), w.is_active,
			       EXISTS (
			         SELECT 1 FROM stock_movement m
			         WHERE m.warehouse_id = w.id
			         GROUP BY m.variant_id
			         HAVING sum(m.delta) <> 0)
			FROM warehouse w
			LEFT JOIN store st ON st.id = w.store_id
			WHERE w.company_id = $1
			  AND ($2 OR w.is_active)
			  AND w.kind <> 'transit'
			ORDER BY w.kind, w.code`,
			scope.CompanyID, includeRetired)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var l Location
			if err := rows.Scan(&l.ID, &l.Code, &l.Name, &l.Kind,
				&l.Store, &l.Active, &l.HoldsStock); err != nil {
				return err
			}
			out.Data = append(out.Data, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		branches, err := tx.Query(ctx, `
			SELECT id, code, name FROM store
			WHERE company_id = $1 AND is_active
			ORDER BY code`, scope.CompanyID)
		if err != nil {
			return err
		}
		defer branches.Close()

		for branches.Next() {
			var b Branch
			if err := branches.Scan(&b.ID, &b.Code, &b.Name); err != nil {
				return err
			}
			out.Branches = append(out.Branches, b)
		}
		return branches.Err()
	})
	return out, err
}

// CreateLocation adds a place stock can be.
func (s *Service) CreateLocation(
	ctx context.Context, scope Scope, in NewLocation,
) (Location, error) {
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	name := strings.TrimSpace(in.Name)
	kind := strings.TrimSpace(in.Kind)

	if code == "" {
		return Location{}, errs.Validation("Give the location a short code.").
			WithField("code", "Letters, digits and dashes, up to sixteen.")
	}
	if name == "" {
		return Location{}, errs.Validation("Give the location a name.").
			WithField("name", "What the people who work there call it.")
	}
	if !creatableKinds[kind] {
		return Location{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a kind of stock location. Choose a shop floor, a store "+
				"room or a central warehouse.", kind)
	}

	// The two halves of 0078's `warehouse_kind_agrees_with_scope`, refused here
	// in words rather than as a constraint violation.
	if kind == KindCentral && in.StoreID != nil {
		return Location{}, errs.New(errs.CodeInvalidInput,
			"A central warehouse serves every branch, so it is not at one of them.")
	}
	if kind != KindCentral && in.StoreID == nil {
		return Location{}, errs.New(errs.CodeInvalidInput,
			"Say which branch this location is at.")
	}

	var out Location
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.StoreID != nil {
			var ok bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM store WHERE id = $1 AND company_id = $2)`,
				*in.StoreID, scope.CompanyID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return errs.New(errs.CodeNotFound,
					"That branch is not this business's.")
			}
		}

		err := tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name, kind)
			VALUES ($1,$2,$3,$4,$5,$6)
			RETURNING id, code, name, kind, is_active`,
			scope.TenantID, scope.CompanyID, in.StoreID, code, name, kind).
			Scan(&out.ID, &out.Code, &out.Name, &out.Kind, &out.Active)
		return err
	})
	if err != nil {
		if errs.As(err) != nil {
			return Location{}, err
		}
		return Location{}, db.Translate(err, "That stock location could not be created.")
	}
	return out, nil
}

// RenameLocation changes what a place is called. The code is not editable: it
// is what stock movements and transfer documents already say.
func (s *Service) RenameLocation(
	ctx context.Context, scope Scope, id uuid.UUID, name string,
) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errs.Validation("Give the location a name.").
			WithField("name", "What the people who work there call it.")
	}
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE warehouse SET name = $3
			WHERE id = $1 AND company_id = $2 AND kind <> 'transit'`,
			id, scope.CompanyID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That stock location is not this business's.")
		}
		return nil
	})
}

// SetLocationActive retires a location or brings it back.
//
// Retiring one that still holds stock is refused. Hiding a room does not empty
// it: the movements stay, C13's valuation keeps counting them, and the shop is
// left with stock the screens no longer show. Move it out first, which is what
// a transfer is for.
func (s *Service) SetLocationActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var name, kind string
		var storeID *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT name, kind, store_id FROM warehouse
			WHERE id = $1 AND company_id = $2 AND kind <> 'transit'`,
			id, scope.CompanyID).Scan(&name, &kind, &storeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That stock location is not this business's.")
		}
		if err != nil {
			return err
		}

		if !active {
			var held bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
				  SELECT 1 FROM stock_movement
				  WHERE warehouse_id = $1
				  GROUP BY variant_id HAVING sum(delta) <> 0)`, id).
				Scan(&held); err != nil {
				return err
			}
			if held {
				return errs.Newf(errs.CodeConflict,
					"%s still holds stock. Move it somewhere else first — "+
						"retiring the location would hide the stock rather "+
						"than remove it.", name)
			}

			// The last way into a branch. A store whose only location is
			// retired sells nothing, and the refusal a cashier would meet
			// names a screen rather than the cause, so it is refused here
			// where the cause is known.
			if storeID != nil {
				var others int
				if err := tx.QueryRow(ctx, `
					SELECT count(*) FROM warehouse
					WHERE company_id = $1 AND is_active AND id <> $2
					  AND kind <> 'transit'
					  AND (store_id = $3 OR store_id IS NULL)`,
					scope.CompanyID, id, *storeID).Scan(&others); err != nil {
					return err
				}
				if others == 0 {
					return errs.Newf(errs.CodeConflict,
						"%s is the only place that branch can sell from. "+
							"Add another location before retiring this one.", name)
				}
			}
		}

		_, err = tx.Exec(ctx,
			`UPDATE warehouse SET is_active = $3 WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, active)
		return err
	})
}

// --- shared helpers -------------------------------------------------------

// locationForWrite reads a location this company owns and may write stock to.
func locationForWrite(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (string, error) {
	var name string
	var active bool
	err := tx.QueryRow(ctx,
		`SELECT name, is_active FROM warehouse WHERE id = $1 AND company_id = $2`,
		id, companyID).Scan(&name, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound,
			"That stock location is not this business's.")
	}
	if err != nil {
		return "", err
	}
	if !active {
		return "", errs.Newf(errs.CodeConflict,
			"%s has been retired, so nothing new can be booked to it.", name)
	}
	return name, nil
}

// variantLabel names a variant for a memo or an error, so a person is told
// which product rather than which UUID.
func variantLabel(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var label string
	err := tx.QueryRow(ctx, `
		SELECT p.name || ' (' || v.sku || ')'
		FROM variant v JOIN product p ON p.id = v.product_id
		WHERE v.id = $1`, id).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.New(errs.CodeNotFound,
			"That product is not in this business's catalogue.")
	}
	return label, err
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

// countedLabel is "3 items" / "1 item", used in memos so a journal line reads
// like a sentence rather than a template.
func countedLabel(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}
