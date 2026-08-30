package integration

// API keys (blueprint H6).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// keyPrefix marks a RawSyst key wherever it turns up.
//
// Worth the seven characters: a key pasted into a support ticket, a log or a
// public repository is recognisable as a credential belonging to this product,
// which is what makes automated secret scanning able to find one and tell the
// shop before somebody else does.
const keyPrefix = "rsk_live"

// Mint creates a key and returns it, once.
//
// `allowed` is what the CALLER holds. The key's permissions are intersected
// with it rather than validated against it and refused: a person ticking a box
// they do not themselves have should get a key that does the rest, not an
// error page — but they must not get the box they ticked.
func (s *Service) Mint(
	ctx context.Context, scope Scope, name string,
	wanted []string, allowed map[string]bool, expires *time.Time,
) (Minted, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Minted{}, errs.Validation("Give the key a name.").
			WithField("name",
				"It is the only way to tell which integration to revoke.")
	}

	granted := make([]string, 0, len(wanted))
	for _, p := range wanted {
		p = strings.TrimSpace(p)
		if p != "" && allowed[p] {
			granted = append(granted, p)
		}
	}
	if len(granted) == 0 {
		return Minted{}, errs.Validation(
			"A key that may do nothing would do nothing.").
			WithField("permissions",
				"Choose at least one thing this key may do, from what you "+
					"hold yourself.")
	}

	// 32 bytes from crypto/rand. Base64url so the key survives being pasted
	// into a header, a query string or a YAML file without escaping.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Minted{}, err
	}
	secret := keyPrefix + "_" + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(sum[:])

	encoded, err := json.Marshal(granted)
	if err != nil {
		return Minted{}, err
	}

	// Enough to recognise a key in a list, not enough to reconstruct one: the
	// prefix plus six characters of a 43-character random tail.
	display := secret[:len(keyPrefix)+7]

	var id uuid.UUID
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO api_key
			  (tenant_id, company_id, name, key_hash, key_prefix, permissions,
			   expires_at, created_by)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, name, hash, display,
			string(encoded), expires, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That key could not be created.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "api_key_created",
			EntityType: "api_key", EntityID: &id,
			// The permissions, never the key. An audit trail that recorded the
			// secret would be a second place it could leak from.
			After: map[string]any{
				"name": name, "prefix": display, "permissions": granted,
			},
		})
	})
	if err != nil {
		return Minted{}, err
	}

	key, err := s.Key(ctx, scope, id)
	if err != nil {
		return Minted{}, err
	}
	return Minted{Key: key, Secret: secret}, nil
}

// Keys lists what exists, without any of them being readable.
func (s *Service) Keys(
	ctx context.Context, scope Scope, includeRevoked bool,
) ([]Key, error) {
	out := []Key{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, keySelect+`
			WHERE k.company_id = $1 AND ($2 OR k.revoked_at IS NULL)
			ORDER BY k.created_at DESC`, scope.CompanyID, includeRevoked)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			k, e := scanKey(rows)
			if e != nil {
				return e
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Key reads one.
func (s *Service) Key(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Key, error) {
	var out Key
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, keySelect+`
			WHERE k.id = $1 AND k.company_id = $2`, id, scope.CompanyID)
		k, e := scanKey(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That key was not found.")
		}
		out = k
		return e
	})
	return out, db.Translate(err, "")
}

// Revoke stops a key working, permanently.
//
// There is no un-revoke. A key that was revoked was revoked because somebody
// believed it had leaked, and a product that could bring it back would let one
// mistaken click undo the only response available to that.
func (s *Service) Revoke(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE api_key
				SET revoked_at = now(), revoked_by = $3
				WHERE id = $1 AND company_id = $2 AND revoked_at IS NULL`,
				id, scope.CompanyID, scope.UserID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That key was not found, or was already revoked.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "api_key_revoked",
				EntityType: "api_key", EntityID: &id,
			})
		}), "")
}

// Authenticate resolves a presented key to what it may do.
//
// Runs on the platform plane because the caller has not established a tenant
// yet — the key is what says which tenant they are. The lookup is by hash, so a
// key that does not exist and a key that is wrong are the same query and take
// the same time.
func (s *Service) Authenticate(
	ctx context.Context, presented string,
) (Identity, error) {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return Identity{}, errs.New(errs.CodeUnauthenticated, "No API key was sent.")
	}
	sum := sha256.Sum256([]byte(presented))
	hash := hex.EncodeToString(sum[:])

	var out Identity
	var storedHash string
	var permissions string
	var revoked, expires *time.Time

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT id, tenant_id, company_id, key_hash, permissions::text,
			       revoked_at, expires_at
			FROM api_key WHERE key_hash = $1`, hash).
			Scan(&out.KeyID, &out.TenantID, &out.CompanyID, &storedHash,
				&permissions, &revoked, &expires)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeUnauthenticated, "That API key is not valid.")
		}
		return e
	})
	if err != nil {
		return Identity{}, err
	}

	// Constant time, even though the lookup was by an indexed hash. It costs
	// nothing and it means no future refactor that loosens the query into a
	// prefix search turns this into a timing oracle.
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return Identity{}, errs.New(errs.CodeUnauthenticated,
			"That API key is not valid.")
	}
	if revoked != nil {
		return Identity{}, errs.New(errs.CodeUnauthenticated,
			"That API key has been revoked.")
	}
	if expires != nil && expires.Before(time.Now()) {
		return Identity{}, errs.New(errs.CodeUnauthenticated,
			"That API key has expired.")
	}
	if err := json.Unmarshal([]byte(permissions), &out.Permissions); err != nil {
		return Identity{}, err
	}

	// Recorded on its own transaction, after the decision. A failed write here
	// must not refuse a valid key: "when was this last used" is useful, and it
	// is not worth an outage.
	_ = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE api_key SET last_used_at = now() WHERE id = $1`, out.KeyID)
		return e
	})
	return out, nil
}

// Identity is who a presented key turns out to be.
type Identity struct {
	KeyID       uuid.UUID
	TenantID    uuid.UUID
	CompanyID   uuid.UUID
	Permissions []string
}

// Can says whether the key may do something.
func (i Identity) Can(permission string) bool {
	for _, p := range i.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

const keySelect = `
	SELECT k.id, k.name, k.key_prefix, k.permissions::text, k.last_used_at,
	       k.expires_at, k.revoked_at, k.created_at, coalesce(u.full_name, '')
	FROM api_key k
	LEFT JOIN app_user u ON u.id = k.created_by`

func scanKey(row scanner) (Key, error) {
	var k Key
	var permissions string
	var lastUsed, expires, revoked *time.Time
	var created time.Time
	if err := row.Scan(&k.ID, &k.Name, &k.Prefix, &permissions, &lastUsed,
		&expires, &revoked, &created, &k.CreatedBy); err != nil {
		return Key{}, err
	}
	if err := json.Unmarshal([]byte(permissions), &k.Permissions); err != nil {
		return Key{}, err
	}
	if lastUsed != nil {
		k.LastUsed = lastUsed.UTC().Format(time.RFC3339)
	}
	if expires != nil {
		k.ExpiresAt = expires.UTC().Format(time.RFC3339)
	}
	if revoked != nil {
		k.RevokedAt = revoked.UTC().Format(time.RFC3339)
	}
	k.CreatedAt = created.UTC().Format(time.RFC3339)
	k.Live = revoked == nil && (expires == nil || expires.After(time.Now()))
	return k, nil
}
