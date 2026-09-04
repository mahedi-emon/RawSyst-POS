// Per-terminal configuration (blueprint I5).
//
// I5: "Per-terminal configuration: default warehouse, linked
// printer/scanner/drawer, receipt template, keyboard shortcuts, default
// discount rule, whether customer selection is required before checkout."
//
// Migration 0009 built the table for this — `terminal_setting`, with row-level
// security, a touch trigger and eight columns carrying exactly those settings.
// Nothing in the product ever read or wrote it. A jeweller who wanted the
// customer recorded on every sale, or a shop with two counters and one printer,
// had a column describing their situation and no way to reach it.
//
// # Why these belong to the terminal and not the shop
//
// Two counters in the same shop can have different printers, different
// scanners, and one with a cash drawer and one without. A shop-level setting
// would make the quieter counter's configuration a lie, which is why 0009 keyed
// the table on the device rather than the store.
//
// # What is stored and what is not
//
// The receipt template and the discount rule are named here, not defined here:
// the template lives in `document_template` and the discount rule in the
// promotions engine, and a terminal points at one. Keyboard shortcuts are the
// one item on I5's list that never reaches the server — they are a property of
// the till application on that machine, and a round trip to store them would be
// a round trip to store something only that machine can act on.
package devices

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// TerminalSettings is how one counter is set up.
type TerminalSettings struct {
	DeviceID uuid.UUID `json:"device_id"`

	DefaultWarehouseID *uuid.UUID `json:"default_warehouse_id,omitempty"`
	ReceiptTemplate    string     `json:"receipt_template,omitempty"`
	PrinterName        string     `json:"printer_name,omitempty"`
	ScannerPrefix      string     `json:"scanner_prefix,omitempty"`
	DrawerEnabled      bool       `json:"drawer_enabled"`

	// RequireCustomer is I5's "whether customer selection is required before
	// checkout". A jeweller wants it; a grocery does not.
	RequireCustomer bool `json:"require_customer"`

	MaxHeldCarts        int    `json:"max_held_carts"`
	DefaultDiscountRule string `json:"default_discount_rule,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

// SettingsChange is an amendment. Every field is optional: a screen that
// changes the printer should not have to restate the discount rule, and a
// partial write that silently blanked the rest is how a counter loses its
// configuration.
type SettingsChange struct {
	DefaultWarehouseID  *uuid.UUID
	ClearWarehouse      bool
	ReceiptTemplate     *string
	PrinterName         *string
	ScannerPrefix       *string
	DrawerEnabled       *bool
	RequireCustomer     *bool
	MaxHeldCarts        *int
	DefaultDiscountRule *string
}

// Settings reads a terminal's configuration, defaults included.
//
// A terminal with no row is not an error: it has never been configured, and the
// answer is what the defaults say it would do. Returning a 404 would make a
// screen handle "not configured" separately from "configured as standard",
// which are the same thing to the person at the counter.
func (s *Service) Settings(
	ctx context.Context, scope Scope, deviceID uuid.UUID,
) (TerminalSettings, error) {
	var out TerminalSettings
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireDevice(ctx, tx, scope.CompanyID, deviceID); e != nil {
			return e
		}
		var e error
		out, e = readSettings(ctx, tx, deviceID)
		return e
	})
	return out, db.Translate(err, "")
}

// SaveSettings amends a terminal's configuration.
func (s *Service) SaveSettings(
	ctx context.Context, scope Scope, deviceID uuid.UUID, in SettingsChange,
) (TerminalSettings, error) {
	if in.MaxHeldCarts != nil && *in.MaxHeldCarts <= 0 {
		return TerminalSettings{}, errs.Validation(
			"A terminal has to be able to hold at least one cart.").
			WithField("max_held_carts",
				"A held cart is a customer who stepped away from the counter.")
	}

	var out TerminalSettings
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireDevice(ctx, tx, scope.CompanyID, deviceID); e != nil {
			return e
		}
		// The warehouse is caller-supplied, so it is checked against this
		// company: another company's warehouse is in the same tenant, where
		// row-level security sees nothing wrong with it.
		if in.DefaultWarehouseID != nil {
			var ok bool
			if e := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM warehouse
				               WHERE id = $1 AND company_id = $2)`,
				*in.DefaultWarehouseID, scope.CompanyID).Scan(&ok); e != nil {
				return e
			}
			if !ok {
				return errs.New(errs.CodeNotFound,
					"That stock location was not found.")
			}
		}

		before, e := readSettings(ctx, tx, deviceID)
		if e != nil {
			return e
		}

		// COALESCE against the incoming value: a field left out keeps what the
		// terminal already had. The row is created on first save, which is why
		// this is an upsert rather than an update.
		if _, e := tx.Exec(ctx, `
			INSERT INTO terminal_setting
			  (device_id, tenant_id, default_warehouse_id, receipt_template,
			   printer_name, scanner_prefix, drawer_enabled, require_customer,
			   max_held_carts, default_discount_rule)
			VALUES ($1,$2,$3,$4,$5,$6,
			        coalesce($7, true), coalesce($8, false),
			        coalesce($9, 10), $10)
			ON CONFLICT (device_id) DO UPDATE SET
			  default_warehouse_id = CASE WHEN $11 THEN NULL
			    ELSE coalesce($3, terminal_setting.default_warehouse_id) END,
			  receipt_template      = coalesce($4, terminal_setting.receipt_template),
			  printer_name          = coalesce($5, terminal_setting.printer_name),
			  scanner_prefix        = coalesce($6, terminal_setting.scanner_prefix),
			  drawer_enabled        = coalesce($7, terminal_setting.drawer_enabled),
			  require_customer      = coalesce($8, terminal_setting.require_customer),
			  max_held_carts        = coalesce($9, terminal_setting.max_held_carts),
			  default_discount_rule = coalesce($10, terminal_setting.default_discount_rule)`,
			deviceID, scope.TenantID, in.DefaultWarehouseID,
			trimmed(in.ReceiptTemplate), trimmed(in.PrinterName),
			trimmed(in.ScannerPrefix), in.DrawerEnabled, in.RequireCustomer,
			in.MaxHeldCarts, trimmed(in.DefaultDiscountRule),
			in.ClearWarehouse); e != nil {
			return db.Translate(e, "Those settings could not be saved.")
		}

		out, e = readSettings(ctx, tx, deviceID)
		if e != nil {
			return e
		}

		// Audited: how a counter is configured decides whether a customer is
		// recorded on a sale and which warehouse the stock came out of, and
		// both are questions somebody asks afterwards.
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "terminal_settings_changed",
			EntityType: "terminal_setting", EntityID: &deviceID,
			Before: map[string]any{
				"require_customer": before.RequireCustomer,
				"drawer_enabled":   before.DrawerEnabled,
				"printer_name":     before.PrinterName,
				"max_held_carts":   before.MaxHeldCarts,
			},
			After: map[string]any{
				"require_customer": out.RequireCustomer,
				"drawer_enabled":   out.DrawerEnabled,
				"printer_name":     out.PrinterName,
				"max_held_carts":   out.MaxHeldCarts,
			},
		})
	})
	return out, db.Translate(err, "")
}

// readSettings reads the row, or the defaults a terminal would run on.
func readSettings(
	ctx context.Context, tx pgx.Tx, deviceID uuid.UUID,
) (TerminalSettings, error) {
	out := TerminalSettings{
		DeviceID: deviceID, DrawerEnabled: true, MaxHeldCarts: 10,
	}
	var template, printer, scanner, discount *string
	var updated *string
	err := tx.QueryRow(ctx, `
		SELECT default_warehouse_id, receipt_template, printer_name,
		       scanner_prefix, drawer_enabled, require_customer,
		       max_held_carts, default_discount_rule,
		       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSZ')
		FROM terminal_setting WHERE device_id = $1`, deviceID).
		Scan(&out.DefaultWarehouseID, &template, &printer, &scanner,
			&out.DrawerEnabled, &out.RequireCustomer, &out.MaxHeldCarts,
			&discount, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return TerminalSettings{}, err
	}
	for target, src := range map[*string]*string{
		&out.ReceiptTemplate: template, &out.PrinterName: printer,
		&out.ScannerPrefix: scanner, &out.DefaultDiscountRule: discount,
		&out.UpdatedAt: updated,
	} {
		if src != nil {
			*target = *src
		}
	}
	return out, nil
}

// requireDevice confirms the terminal belongs to this company.
//
// Without it a caller could read or set the configuration of a counter in
// another company: the device id is theirs to choose, and row-level security
// scopes to the tenant rather than to the company inside it.
func requireDevice(
	ctx context.Context, tx pgx.Tx, companyID, deviceID uuid.UUID,
) error {
	var ok bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM device d
		  JOIN store s ON s.id = d.store_id
		  WHERE d.id = $1 AND s.company_id = $2)`,
		deviceID, companyID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errs.New(errs.CodeNotFound, "That terminal was not found.")
	}
	return nil
}

// trimmed turns an optional string into what should be stored: nil when the
// caller did not mention it, and NULL when they cleared it.
func trimmed(v *string) any {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return t
}
