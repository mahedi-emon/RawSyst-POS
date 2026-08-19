package devices

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// What an Owner can do to a terminal after it exists.
//
// H3 names them: activate, deactivate, revoke, rename, reassign to a different
// store, and view health. They are separated here by what they cost to undo.
// Renaming and reassigning are corrections. Deactivating is a pause. Revoking
// is the end of that terminal, and is treated accordingly.

// terminalSelect is shared so the list and the single read cannot drift into
// reporting different states for the same till.
//
// The CSID columns come from the EGS unit the terminal signs under, never from
// `device`. 0013 moved the CSID to the unit — it is only the same thing as a
// device in the smart-POS architecture — and left `device.csid_serial` in place
// as a deprecated column that nothing writes. Reading it here meant this screen
// reported an empty CSID for every terminal in the other two architectures,
// including ones that were properly onboarded.
const terminalSelect = `
	SELECT d.id, d.store_id, s.name, d.terminal_label, d.status::text,
	       coalesce(d.os, ''), coalesce(d.app_version, ''),
	       coalesce(to_char(d.last_sync_at,   'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(to_char(d.last_active_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(to_char(d.enrolled_at,    'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(to_char(d.revoked_at,     'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(d.revoked_reason, ''),
	       coalesce(d.egs_unit_id::text, ''),
	       coalesce(u.label, ''),
	       coalesce(u.csid_status, ''),
	       coalesce(u.csid_serial, ''),
	       coalesce(to_char(u.csid_expires_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       (e.id IS NOT NULL) AS pending_code,
	       coalesce(to_char(e.expires_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), '')
	FROM device d
	JOIN store s ON s.id = d.store_id
	LEFT JOIN egs_unit u ON u.id = d.egs_unit_id
	LEFT JOIN device_enrolment e
	       ON e.device_id = d.id AND e.redeemed_at IS NULL AND e.expires_at > now()`

type scanner interface{ Scan(dest ...any) error }

func scanTerminal(row scanner) (Terminal, error) {
	var t Terminal
	if err := row.Scan(&t.ID, &t.StoreID, &t.Store, &t.Label, &t.Status,
		&t.OS, &t.AppVersion, &t.LastSyncAt, &t.LastActiveAt, &t.EnrolledAt,
		&t.RevokedAt, &t.RevokedReason,
		&t.EGSUnitID, &t.EGSUnit, &t.CSIDStatus, &t.CSIDSerial, &t.CSIDExpiresAt,
		&t.PendingCode, &t.CodeExpiresAt); err != nil {
		return Terminal{}, err
	}
	return t, nil
}

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, scope Scope, deviceID uuid.UUID,
) (Terminal, error) {
	row := tx.QueryRow(ctx, terminalSelect+`
		WHERE d.id = $1 AND d.company_id = $2`, deviceID, scope.CompanyID)
	t, err := scanTerminal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// Under row-level security another tenant's terminal reads as absent,
		// which is the right answer: its existence is not this caller's
		// business.
		return Terminal{}, errs.New(errs.CodeNotFound, "That terminal was not found.")
	}
	return t, err
}

// Read is one terminal, for the detail panel.
func (s *Service) Read(
	ctx context.Context, scope Scope, deviceID uuid.UUID,
) (Terminal, error) {
	var out Terminal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		t, e := s.read(ctx, tx, scope, deviceID)
		out = t
		return e
	})
	return out, err
}

// List is every terminal in the company, most in need of attention first.
//
// Ordered by status rather than by name: a shop with twelve tills opens this
// screen because one of them is wrong, and the pending and revoked ones are
// what they came to see.
func (s *Service) List(ctx context.Context, scope Scope) ([]Terminal, error) {
	out := []Terminal{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, terminalSelect+`
			WHERE d.company_id = $1
			ORDER BY
			  CASE d.status
			    WHEN 'pending'  THEN 0
			    WHEN 'inactive' THEN 1
			    WHEN 'active'   THEN 2
			    ELSE 3
			  END,
			  s.name, d.terminal_label`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			t, e := scanTerminal(rows)
			if e != nil {
				return e
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

// Amendment renames a terminal and optionally moves it to another store.
//
// One operation because it is one form. Reassignment does NOT start a new ZATCA
// chain: 04-identity §9 is explicit that the chain belongs to the device under
// its company's VAT registration and the ICV continues unbroken, and that only
// a genuinely new terminal starts at ICV 1 (E1.3 RULE 5). Moving a till between
// stores of one company is a change of address, not a new identity.
//
// Moving it to a store in a DIFFERENT company is refused, because that would
// change the VAT registration the chain hangs from — which is exactly the case
// RULE 5 says must be a new terminal.
type Amendment struct {
	Label   string
	StoreID uuid.UUID

	// EGSUnitID repoints the terminal at a different signing unit. Absent
	// leaves it as it is. This is the repair path for every terminal registered
	// before Register required one: without it they are permanently unable to
	// sell and the only fix is SQL.
	EGSUnitID uuid.UUID
}

func (s *Service) Amend(
	ctx context.Context, scope Scope, deviceID uuid.UUID, in Amendment,
) (Terminal, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		return Terminal{}, errs.New(errs.CodeInvalidInput,
			"Give the terminal a name you will recognise.")
	}

	var out Terminal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.StoreID != uuid.Nil {
			var exists bool
			if e := tx.QueryRow(ctx, `
				SELECT EXISTS (
				  SELECT 1 FROM store WHERE id = $1 AND company_id = $2
				)`, in.StoreID, scope.CompanyID).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				// Covers "no such store" and "a store in another company", and
				// says the same thing about each.
				return errs.New(errs.CodeNotFound,
					"That store was not found in this business. A terminal cannot "+
						"move to another company: its invoice chain belongs to the "+
						"VAT registration it was set up under.")
			}
		}

		current, e := s.read(ctx, tx, scope, deviceID)
		if e != nil {
			return e
		}

		// Only a genuine change of unit is checked, and it is checked against
		// the branch the terminal will END UP in, because the form submits both
		// at once. Leaving the unit alone leaves it alone, even across a move.
		if repointing := in.EGSUnitID != uuid.Nil &&
			in.EGSUnitID.String() != current.EGSUnitID; repointing {
			store := in.StoreID
			if store == uuid.Nil {
				store = current.StoreID
			}
			if e := bindable(ctx, tx, scope, in.EGSUnitID, store); e != nil {
				return e
			}

			// Repointing a till that has already traded would split its sales
			// across two chains with nothing recording where the seam is. The
			// old invoices stay on the old unit and remain valid — that is not
			// the problem. The problem is that nobody reading either chain
			// afterwards could tell which till wrote which part of it.
			//
			// A terminal that never had a unit has nothing to split, so giving
			// one to a terminal registered before 0043 is always allowed.
			if current.EGSUnitID != "" {
				var sold bool
				if e := tx.QueryRow(ctx, `
					SELECT EXISTS (SELECT 1 FROM sales_invoice WHERE device_id = $1)`,
					deviceID).Scan(&sold); e != nil {
					return e
				}
				if sold {
					return errs.New(errs.CodeImmutable,
						"This terminal has already issued invoices under its current "+
							"e-invoicing unit, so it cannot be moved to another one. "+
							"Revoke it and register a replacement if the unit is wrong.")
				}
			}
		}

		tag, e := tx.Exec(ctx, `
			UPDATE device
			SET terminal_label = $3,
			    store_id = coalesce($4, store_id),
			    egs_unit_id = coalesce($5, egs_unit_id)
			WHERE id = $1 AND company_id = $2`,
			deviceID, scope.CompanyID, label, nullUUID(in.StoreID),
			nullUUID(in.EGSUnitID))
		if e != nil {
			return db.Translate(e, "That terminal could not be saved.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That terminal was not found.")
		}

		t, e := s.read(ctx, tx, scope, deviceID)
		out = t
		return e
	})
	return out, err
}

// SetActive pauses or resumes a terminal.
//
// A pause, not an end. A till switched off for the summer keeps its secret and
// its chain and comes back by being switched on again. Because Authenticate
// re-reads the status on every exchange, deactivating takes effect on the next
// call rather than whenever some already-issued token would have expired.
//
// Refused for a revoked terminal, and for one never paired: neither has
// anything to pause.
func (s *Service) SetActive(
	ctx context.Context, scope Scope, deviceID uuid.UUID, active bool,
) (Terminal, error) {
	var out Terminal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		var enrolled *string
		if e := tx.QueryRow(ctx, `
			SELECT status::text, to_char(enrolled_at, 'YYYY-MM-DD')
			FROM device WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			deviceID, scope.CompanyID).Scan(&status, &enrolled); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That terminal was not found.")
			}
			return e
		}
		if status == "revoked" {
			return errs.New(errs.CodeConflict,
				"That terminal was revoked. Revoking is permanent — register a "+
					"new terminal instead.")
		}
		if enrolled == nil {
			return errs.New(errs.CodeConflict,
				"That terminal has not been paired yet, so there is nothing to "+
					"switch on. Issue an enrolment code for it.")
		}

		next := "inactive"
		if active {
			next = "active"
		}
		if _, e := tx.Exec(ctx,
			`UPDATE device SET status = $2::device_status WHERE id = $1`,
			deviceID, next); e != nil {
			return e
		}

		t, e := s.read(ctx, tx, scope, deviceID)
		out = t
		return e
	})
	return out, err
}

// Revoke ends a terminal.
//
// The one lifecycle step that does not reverse. 01-invoice-zatca-engine.md §7
// pairs revocation with destroying the local CSID key on next start, so a
// revoked terminal cannot be brought back — it is replaced.
//
// The secret is CLEARED rather than merely marked: a database copy taken
// afterwards must not contain a working credential for a till that may be in
// somebody else's hands. The chain and every invoice on it stay exactly where
// they are, because §7 also says the chain "remains intact and archived" —
// revoking a terminal must never look like erasing its trading history.
func (s *Service) Revoke(
	ctx context.Context, scope Scope, deviceID uuid.UUID, reason string,
) (Terminal, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		// Required, because a revocation nobody can explain a month later is a
		// revocation somebody will undo.
		return Terminal{}, errs.New(errs.CodeInvalidInput,
			"Say why this terminal is being revoked. It cannot be undone, and "+
				"the reason is what the next person reads.")
	}

	var out Terminal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		if e := tx.QueryRow(ctx, `
			SELECT status::text FROM device
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			deviceID, scope.CompanyID).Scan(&status); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That terminal was not found.")
			}
			return e
		}
		if status == "revoked" {
			return errs.New(errs.CodeConflict, "That terminal is already revoked.")
		}

		if _, e := tx.Exec(ctx, `
			UPDATE device
			SET status = 'revoked', revoked_at = now(), revoked_by = $3,
			    revoked_reason = $4, secret_hash = NULL, secret_selector = NULL,
			    enrolled_at = NULL
			WHERE id = $1 AND company_id = $2`,
			deviceID, scope.CompanyID, scope.UserID, reason); e != nil {
			return e
		}

		// Any outstanding code dies with it, or a revoked terminal could be
		// paired again by whoever still held the code.
		if _, e := tx.Exec(ctx,
			`DELETE FROM device_enrolment WHERE device_id = $1 AND redeemed_at IS NULL`,
			deviceID); e != nil {
			return e
		}

		t, e := s.read(ctx, tx, scope, deviceID)
		out = t
		return e
	})
	return out, err
}

// nullUUID keeps "not supplied" distinct from "the zero uuid" in SQL, so
// coalesce can leave a column alone rather than writing a nil id into it.
func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// Active reports whether a paired terminal may still act.
//
// Called on EVERY request that carries a device-bound token, for exactly the
// reason 04-identity gives for resolving permissions per request rather than
// embedding them in the JWT: "A permission revoked at 10:00 must not remain
// effective until a 15-minute token expires." A revoked terminal is the same
// problem with higher stakes — it may be in somebody else's hands — so it gets
// the same treatment rather than a weaker one.
func (s *Service) Active(ctx context.Context, deviceID uuid.UUID) error {
	var status, label string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status::text, terminal_label FROM device WHERE id = $1`,
			deviceID).Scan(&status, &label)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeUnauthenticated,
			"This terminal is no longer registered.")
	}
	if err != nil {
		return err
	}

	switch status {
	case "active":
		return nil
	case "revoked":
		return errs.Newf(errs.CodeUnauthenticated,
			"%s has been revoked and can no longer be used.", label)
	default:
		return errs.Newf(errs.CodeForbidden,
			"%s is %s. An owner can switch it back on under Devices.", label, status)
	}
}

// Store is somewhere a terminal can stand.
type Store struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

// StoresFor lists the branches of this company, for the register form.
//
// Its own small route rather than reusing purchasing's warehouse list: a
// terminal stands in a STORE, stock sits in a warehouse, and the two are
// different things that happen to be one-to-one in a small shop. Borrowing the
// wrong list would also gate the Devices screen on a purchasing permission,
// which has nothing to do with who may set up a till.
func (s *Service) StoresFor(ctx context.Context, scope Scope) ([]Store, error) {
	out := []Store{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `
			SELECT id, code, name FROM store
			WHERE company_id = $1 ORDER BY name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var st Store
			if e := rows.Scan(&st.ID, &st.Code, &st.Name); e != nil {
				return e
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	return out, err
}

// bindable refuses an EGS unit this terminal must not sign under.
//
// Company ownership is checked in the query rather than trusted from row
// security alone, so a unit id copied from another business is a "not found"
// and not a silent cross-registration binding. That one is a security
// property and applies always.
//
// The branch rule is softer, and only applies to the choice being MADE. A
// centralized unit serves the whole taxpayer, so any branch may point at it; a
// branch server "signs for the devices in one branch" and a smart POS signs
// for itself, so picking one from another branch is a mistake worth refusing.
// It is deliberately NOT re-checked when a terminal merely moves between
// branches: 04-identity §9 says that move keeps the chain unbroken, and turning
// a documented correction into a refusal because of a rule invented here would
// be the wrong trade.
func bindable(
	ctx context.Context, tx pgx.Tx, scope Scope, unitID, storeID uuid.UUID,
) error {
	var architecture string
	var unitStore *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT architecture, store_id FROM egs_unit
		WHERE id = $1 AND company_id = $2`, unitID, scope.CompanyID).
		Scan(&architecture, &unitStore)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound,
			"That e-invoicing unit was not found in this business.")
	}
	if err != nil {
		return err
	}

	if unitStore != nil && *unitStore != storeID {
		return errs.New(errs.CodeInvalidInput,
			"That e-invoicing unit signs for a different branch. Choose one in "+
				"this terminal's branch, or a central unit that covers the whole "+
				"business.").
			WithField("egs_unit_id", "This unit belongs to another branch.")
	}
	return nil
}

// requireCompany refuses a company this caller has no business in.
//
// The token check in the handler is not enough on its own: an unscoped user has
// an empty company list, which means "every company in MY tenant", so a company
// id belonging to a DIFFERENT tenant passes it. Row-level security then hides
// every row and the caller gets an empty list — which reads as a business with
// no terminals rather than one that is none of their concern.
//
// The same guard purchasing and receivables carry, for the same reason.
func requireCompany(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM company WHERE id = $1)`, companyID).
		Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return nil
}
