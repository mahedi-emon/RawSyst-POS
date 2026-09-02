package identity

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The custom role builder (blueprint A6.2), and a person's own session list
// (H1).
//
// # Nobody can build a role more powerful than themselves
//
// The single rule this file exists to enforce. `AssignRoleTo` already refuses
// to hand somebody a role carrying permissions the granter does not hold; a
// builder without the same rule would be a way straight past it — write a role
// with every permission, assign it to yourself, and the assignment check passes
// because by then you hold them.
//
// So `SaveRole` checks the permissions being PUT INTO the role against what the
// author holds, and refuses the ones they do not. Same rule, one step earlier.
//
// # A system role is read-only
//
// The seeded roles are the product's, not a tenant's: they are what a new shop
// gets on day one and what the migrations extend when a module is added. A
// tenant editing one would silently diverge from every future migration, so a
// tenant that wants a different Cashier clones it and edits the clone —
// which is what `cloned_from` has been for since 0003.

// PermissionOption is one permission as the builder shows it.
type PermissionOption struct {
	Permission string `json:"permission"`
	Section    string `json:"section"`
	Label      string `json:"label"`
	LabelAr    string `json:"label_ar,omitempty"`
	LabelBn    string `json:"label_bn,omitempty"`
	// Caution is the sentence beside the tick box for a permission that
	// changes money, reveals pay, or grants further permissions.
	Caution string `json:"caution,omitempty"`

	// Holds is false when the CALLER does not hold this permission themselves.
	// Sent rather than filtered out: an owner who cannot see why a box is
	// disabled will assume the screen is broken.
	Holds bool `json:"holds"`
}

// Permissions lists every permission the product enforces, described.
//
// The catalogue supplies the words; the ROUTE REGISTRY supplies the list. A
// permission with no catalogue row still appears, labelled by its own key and
// put in an "other" section — the honest outcome for one somebody forgot to
// describe, rather than one that quietly cannot be granted.
func (s *Service) Permissions(
	ctx context.Context, scope PeopleScope, known []string,
) ([]PermissionOption, error) {
	described := map[string]PermissionOption{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT permission, section, label, coalesce(label_ar, ''),
			       coalesce(label_bn, ''), coalesce(caution, ''), sort_order
			FROM permission_catalogue
			ORDER BY section, sort_order, label`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p PermissionOption
			var order int
			if e := rows.Scan(&p.Permission, &p.Section, &p.Label, &p.LabelAr,
				&p.LabelBn, &p.Caution, &order); e != nil {
				return e
			}
			described[p.Permission] = p
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}

	out := make([]PermissionOption, 0, len(known))
	for _, perm := range known {
		p, ok := described[perm]
		if !ok {
			p = PermissionOption{
				Permission: perm, Section: "other", Label: perm,
			}
		}
		// `Holds` is the caller's own set, resolved for this request by the
		// same grants the middleware checked. Reading it again from the
		// database would be a second answer to a question already answered.
		p.Holds = scope.Holds[perm]
		out = append(out, p)
	}
	return out, nil
}

// CustomRole is a role a tenant built.
type CustomRole struct {
	ID          uuid.UUID `json:"id,omitempty"`
	Key         string    `json:"key,omitempty"`
	Name        string    `json:"name"`
	NameAr      string    `json:"name_ar,omitempty"`
	Description string    `json:"description,omitempty"`
	Permissions []string  `json:"permissions"`

	// IsSystem marks the seeded roles, which cannot be edited. Set by the
	// server on the way out and ignored on the way in.
	IsSystem bool `json:"is_system"`
	// ClonedFrom names the system role this was copied from, when it was.
	ClonedFrom *uuid.UUID `json:"cloned_from,omitempty"`
	// InUse counts the people currently holding it, so a screen can say why a
	// role cannot be deleted before somebody presses delete.
	InUse int `json:"in_use"`
}

// SaveRole creates or edits a tenant's own role.
func (s *Service) SaveRole(
	ctx context.Context, scope PeopleScope, in CustomRole,
) (CustomRole, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return CustomRole{}, errs.New(errs.CodeInvalidInput,
			"Give the role a name.")
	}
	if len(in.Permissions) == 0 {
		return CustomRole{}, errs.New(errs.CodeInvalidInput,
			"A role that can do nothing is a role nobody can use. Tick at "+
				"least one thing.")
	}

	// Nobody builds a role more powerful than themselves. See the file note:
	// without this, the assignment check next door is walked straight past.
	//
	// The same `withheld` helper `checkRoleIsGrantable` uses, against the same
	// resolved set, so the two rules cannot drift into disagreeing about what
	// somebody holds.
	if missing := withheld(in.Permissions, scope.Holds); len(missing) > 0 {
		return CustomRole{}, errs.Newf(errs.CodeForbidden,
			"You cannot put something into a role that you do not have "+
				"yourself: %s.", strings.Join(missing, ", "))
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.ID != uuid.Nil {
			// Editing. A system role is not the tenant's to change.
			var isSystem bool
			var owner *uuid.UUID
			e := tx.QueryRow(ctx,
				`SELECT is_system, tenant_id FROM role WHERE id = $1`,
				in.ID).Scan(&isSystem, &owner)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That role was not found.")
			}
			if e != nil {
				return e
			}
			if isSystem || owner == nil {
				return errs.New(errs.CodeForbidden,
					"That is one of the built-in roles. Copy it and edit the "+
						"copy, so it keeps working when the product adds a "+
						"module.")
			}
			id = in.ID

			if _, e := tx.Exec(ctx, `
				UPDATE role SET name = $2, name_ar = nullif($3,''),
				                description = nullif($4,'')
				 WHERE id = $1 AND tenant_id = $5`,
				id, name, in.NameAr, in.Description, scope.TenantID); e != nil {
				return e
			}
		} else {
			// Creating. The plan's ceiling applies: A3 is explicit that
			// "unlimited" is a marketing word.
			var limit, used int
			if e := tx.QueryRow(ctx, `
				SELECT coalesce(l.max_custom_roles, 0),
				       (SELECT count(*) FROM role
				         WHERE tenant_id = $1 AND NOT is_system)
				FROM tenant_limit l WHERE l.tenant_id = $1`,
				scope.TenantID).Scan(&limit, &used); e != nil && e != pgx.ErrNoRows {
				return e
			}
			if used >= limit {
				return errs.Newf(errs.CodeLimitReached,
					"Your plan allows %d roles of your own and you have %d. "+
						"Edit one, or ask about a larger plan.", limit, used)
			}

			// The key is derived from the name and made unique within the
			// tenant. It is what the permission tests and the migrations join
			// on, and a person should not have to invent one.
			key := slugify(name)
			if key == "" {
				key = "role"
			}
			if e := tx.QueryRow(ctx, `
				INSERT INTO role
				  (tenant_id, key, name, name_ar, description, is_system,
				   cloned_from)
				VALUES ($1, $2 || '_' || substr(md5(random()::text), 1, 6),
				        $3, nullif($4,''), nullif($5,''), false, $6)
				RETURNING id`,
				scope.TenantID, key, name, in.NameAr, in.Description,
				in.ClonedFrom).Scan(&id); e != nil {
				return db.Translate(e, "That role could not be created.")
			}
		}

		// The permissions, replaced wholesale. A diff would be the same three
		// statements with more ways to be wrong.
		if _, e := tx.Exec(ctx,
			`DELETE FROM role_permission WHERE role_id = $1`, id); e != nil {
			return e
		}
		for _, p := range in.Permissions {
			if _, e := tx.Exec(ctx, `
				INSERT INTO role_permission (role_id, permission)
				VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, p); e != nil {
				return e
			}
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.ActorID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.ActorID),
			Action:     "role_saved",
			EntityType: "role", EntityID: &id,
			After: map[string]any{
				"name": name, "permissions": in.Permissions,
			},
		})
	})
	if err != nil {
		return CustomRole{}, db.Translate(err, "")
	}
	return s.Role(ctx, scope, id)
}

// Role reads one.
func (s *Service) Role(
	ctx context.Context, scope PeopleScope, id uuid.UUID,
) (CustomRole, error) {
	var out CustomRole
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT r.id, r.key, r.name, coalesce(r.name_ar, ''),
			       coalesce(r.description, ''), r.is_system, r.cloned_from,
			       (SELECT count(*) FROM user_role_assignment a
			         WHERE a.role_id = r.id)
			FROM role r WHERE r.id = $1`, id).Scan(
			&out.ID, &out.Key, &out.Name, &out.NameAr, &out.Description,
			&out.IsSystem, &out.ClonedFrom, &out.InUse)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That role was not found.")
		}
		if e != nil {
			return e
		}

		rows, e := tx.Query(ctx,
			`SELECT permission FROM role_permission WHERE role_id = $1
			  ORDER BY permission`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		out.Permissions = []string{}
		for rows.Next() {
			var p string
			if e := rows.Scan(&p); e != nil {
				return e
			}
			out.Permissions = append(out.Permissions, p)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// RemoveRole deletes a tenant's own role.
//
// Refused while anybody holds it. Cascading the assignment away would silently
// strip somebody of everything they could do, and they would find out at the
// till.
func (s *Service) RemoveRole(
	ctx context.Context, scope PeopleScope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			var isSystem bool
			var inUse int
			e := tx.QueryRow(ctx, `
				SELECT r.is_system,
				       (SELECT count(*) FROM user_role_assignment a
				         WHERE a.role_id = r.id)
				FROM role r WHERE r.id = $1 AND r.tenant_id = $2`,
				id, scope.TenantID).Scan(&isSystem, &inUse)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That role was not found.")
			}
			if e != nil {
				return e
			}
			if isSystem {
				return errs.New(errs.CodeForbidden,
					"The built-in roles cannot be removed.")
			}
			if inUse > 0 {
				return errs.Newf(errs.CodeConflict,
					"%d people still hold that role. Move them to another "+
						"one first.", inUse)
			}

			if _, e := tx.Exec(ctx,
				`DELETE FROM role WHERE id = $1 AND tenant_id = $2`,
				id, scope.TenantID); e != nil {
				return e
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.ActorID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.ActorID),
				Action:     "role_removed",
				EntityType: "role", EntityID: &id,
			})
		}), "")
}

// slugify turns a role name into a key.
//
// Underscores rather than hyphens, and it must start with a letter: the column
// carries CHECK (key ~ '^[a-z][a-z0-9_]*$') and has since 0003. A name that is
// entirely digits or punctuation — "2024", "—" — yields nothing usable, and
// the caller supplies "role" in that case rather than writing a key the
// constraint refuses.
func slugify(name string) string {
	var b strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastUnderscore = false
		case r >= '0' && r <= '9':
			// Not as the first character: the constraint wants a letter there.
			if b.Len() == 0 {
				continue
			}
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore && b.Len() > 0:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// ---------------------------------------------------------------------------
// A person's own sessions (H1)
// ---------------------------------------------------------------------------

// ActiveSession is one place the caller is signed in.
type ActiveSession struct {
	ID          uuid.UUID `json:"id"`
	DeviceLabel string    `json:"device_label,omitempty"`
	IP          string    `json:"ip,omitempty"`
	UserAgent   string    `json:"user_agent,omitempty"`
	CreatedAt   string    `json:"created_at"`
	LastSeenAt  string    `json:"last_seen_at,omitempty"`
	ExpiresAt   string    `json:"expires_at"`
	// Current marks the session making the request, so the screen can label it
	// rather than inviting somebody to sign themselves out by accident.
	Current bool `json:"current"`
}

// MySessions lists where the caller is signed in.
//
// Scoped to the CALLER's own user id, taken from their token. There is no
// parameter naming a user, so this cannot be pointed at anybody else.
func (s *Service) MySessions(
	ctx context.Context, userID, currentSessionID uuid.UUID,
) ([]ActiveSession, error) {
	out := []ActiveSession{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, coalesce(device_label, ''), coalesce(host(ip), ''),
			       coalesce(user_agent, ''), created_at, last_seen_at,
			       expires_at
			FROM user_session
			WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
			ORDER BY coalesce(last_seen_at, created_at) DESC`, userID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a ActiveSession
			var created, expires time.Time
			var lastSeen *time.Time
			if e := rows.Scan(&a.ID, &a.DeviceLabel, &a.IP, &a.UserAgent,
				&created, &lastSeen, &expires); e != nil {
				return e
			}
			a.CreatedAt = created.UTC().Format(time.RFC3339)
			a.ExpiresAt = expires.UTC().Format(time.RFC3339)
			if lastSeen != nil {
				a.LastSeenAt = lastSeen.UTC().Format(time.RFC3339)
			}
			a.Current = a.ID == currentSessionID
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// RevokeMySession signs the caller out of one of their own sessions.
//
// The user id is in the WHERE clause, so a session id belonging to somebody
// else finds nothing — the same shape of guarantee the portals use.
func (s *Service) RevokeMySession(
	ctx context.Context, userID, sessionID uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE user_session
			   SET revoked_at = now(), revoked_reason = 'signed out by the user'
			 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			sessionID, userID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That session was not found, or it has already ended.")
		}
		// The refresh chain goes with it. Leaving it alive would let the
		// signed-out device mint a new access token on its next refresh.
		_, e = tx.Exec(ctx, `
			UPDATE session_refresh_token SET used_at = now()
			 WHERE session_id = $1 AND used_at IS NULL`, sessionID)
		return e
	}), "")
}
