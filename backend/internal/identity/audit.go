package identity

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// auditEntry is one row for the append-only audit log.
//
// Blueprint D4 fixes the six fields exactly: who, what, when, where,
// before-value, after-value. "When" is the database default, so it cannot be
// back-dated by a caller.
type auditEntry struct {
	TenantID   *uuid.UUID
	ActorID    *uuid.UUID
	ActorLabel string
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	IP         string
	Device     string
	Before     map[string]any
	After      map[string]any
}

// writeAudit appends one entry inside the caller's transaction.
//
// Being in the same transaction is the point: an action that commits without
// its audit record, or an audit record for an action that rolled back, are both
// worse than no log at all, because either makes the log unreliable as
// evidence.
//
// Never log a credential. The After map is written by callers who must not put
// a password, token or key in it; the field masking in the logging package does
// not apply here, since this is a database row rather than a log line.
func writeAudit(ctx context.Context, tx pgx.Tx, e auditEntry) error {
	var before, after []byte
	var err error

	if e.Before != nil {
		if before, err = json.Marshal(e.Before); err != nil {
			return errs.Wrap(err, errs.CodeInternal, "Could not record this action.")
		}
	}
	if e.After != nil {
		if after, err = json.Marshal(e.After); err != nil {
			return errs.Wrap(err, errs.CodeInternal, "Could not record this action.")
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log
		  (tenant_id, actor_id, actor_label, action, entity_type, entity_id,
		   ip, device_label, before_value, after_value)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.TenantID, e.ActorID, nullIfEmpty(e.ActorLabel), e.Action,
		e.EntityType, e.EntityID, nullIfEmpty(e.IP), nullIfEmpty(e.Device),
		nullOrJSON(before), nullOrJSON(after))
	return err
}

func nullOrJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// temporaryPasswordAlphabet excludes characters that are misread when a
// password is spoken over the phone or copied from a screen: 0/O, 1/l/I, 5/S,
// 8/B. Blueprint A4.2's recovery flow ends with a Super Admin reading this to
// an Owner, so ambiguity is a real support cost.
const temporaryPasswordAlphabet = "ACDEFGHJKMNPQRTUVWXYZacdefghjkmnpqrtuvwxyz234679"

// temporaryPasswordLen is above MinPasswordLen so a generated password always
// satisfies the policy it will be checked against.
const temporaryPasswordLen = 16

// GenerateTemporaryPassword produces a one-time password for account recovery.
//
// crypto/rand, not math/rand. A predictable recovery password would let anyone
// who knows when a reset happened guess their way into the account, which is
// precisely the account this flow exists to protect.
func GenerateTemporaryPassword() (string, error) {
	var sb strings.Builder
	sb.Grow(temporaryPasswordLen + 3)

	max := big.NewInt(int64(len(temporaryPasswordAlphabet)))
	for i := 0; i < temporaryPasswordLen; i++ {
		// Group into fours so it can be read aloud reliably.
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", errs.Wrap(err, errs.CodeInternal,
				"Could not generate a temporary password.")
		}
		sb.WriteByte(temporaryPasswordAlphabet[n.Int64()])
	}
	return sb.String(), nil
}
