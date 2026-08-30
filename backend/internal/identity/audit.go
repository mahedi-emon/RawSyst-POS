package identity

import (
	"context"
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// auditEntry and writeAudit are this package's names for `audit.Entry` and
// `audit.Write`.
//
// Kept as an alias rather than replaced at four call sites, because the shape
// was right and the only thing wrong with it was that three packages each had
// their own copy — including one that omitted `actor_label`, the field that
// exists so the trail survives a user being deleted.
type auditEntry = audit.Entry

func writeAudit(ctx context.Context, tx pgx.Tx, e auditEntry) error {
	return audit.Write(ctx, tx, e)
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
