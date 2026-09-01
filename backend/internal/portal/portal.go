// Package portal is the customer self-service portal and the supplier portal
// (blueprint F2, F3).
//
// # A portal identity is not a staff identity
//
// Neither a customer nor a supplier contact is an `app_user`. They hold no
// role, carry no permission, and their token has its own audience. The
// middleware that reads a staff token does not know how to read a portal one
// and vice versa, so a portal session cannot be exchanged for staff access by
// any confusion of code — the two never meet.
//
// # What a portal may see is fixed in the query, not granted
//
// Every read here puts the signed-in customer's or supplier's id in its WHERE
// clause, and no portal route takes a parameter that could name somebody else.
// That is deliberate and stronger than a permission: a permission implies that
// reading another customer's invoices is a thing that could be granted, and
// there is no code path in this package that would serve it.
//
// # Signing in must not say who is a customer
//
// Asking for a code always answers the same way, whether or not the number is
// on file. A portal that said "no such customer" would be a way to ask a shop
// whether a particular person shops there, which is a disclosure of personal
// data to anybody with a phone.
package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// codeTTL is how long a one-time code lives.
//
// Ten minutes, matching the staff recovery code in 0076. Long enough for a
// message to arrive on a slow network and for somebody to switch apps; short
// enough that an intercepted code is usually already dead.
const codeTTL = 10 * time.Minute

// sessionTTL is how long a portal session lasts.
//
// Thirty days for a customer, because the alternative is asking them to receive
// a text message every time they want to look at a receipt, and that is the
// friction that makes a self-service portal unused. A supplier's is shorter:
// they act on behalf of a business from a shared desk.
const (
	customerSessionTTL = 30 * 24 * time.Hour
	supplierSessionTTL = 12 * time.Hour
)

// maxAttempts caps guesses against one code before it is dead.
const maxAttempts = 5

// Service carries both portals.
type Service struct {
	pool *db.Pool

	// queue is how a code reaches a phone. Optional: an installation with no
	// message provider still serves every other portal route, and asking for a
	// code reports that it cannot be sent rather than issuing one nobody will
	// receive.
	queue identity.Enqueuer
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// WithQueue supplies the message queue.
func (s *Service) WithQueue(q identity.Enqueuer) *Service {
	s.queue = q
	return s
}

// Scope names the shop a portal request is against.
//
// A portal is per company: a customer of one branch of a group is not
// automatically a customer of another, and the sign-in page belongs to a shop.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
}

// Caller is a signed-in portal user, as the request context carries them.
type Caller struct {
	Scope
	PortalUserID uuid.UUID
	// Exactly one of these is set, and which one says which portal.
	CustomerID *uuid.UUID
	SupplierID *uuid.UUID
	Name       string
}

// ---------------------------------------------------------------------------
// Customer sign-in
// ---------------------------------------------------------------------------

// RequestCode sends a one-time code to a phone.
//
// Always reports success. See the package note: an answer that differed for a
// number that is on file would turn the portal into a way of asking a shop who
// its customers are.
//
// The code is queued for sending INSIDE the transaction that issues it, the
// same way the staff recovery code is: a code that exists and a message that
// will be sent commit together or not at all.
func (s *Service) RequestCode(
	ctx context.Context, scope Scope, phone string,
) error {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return errs.New(errs.CodeInvalidInput,
			"Enter the phone number the shop has for you.")
	}
	if s.queue == nil {
		return errs.New(errs.CodeUnavailable,
			"This shop cannot send codes at the moment. Ask them to look up "+
				"your receipts for you.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Is there a customer with this number? Looked up, but the answer
		// never leaves this function.
		var customerID uuid.UUID
		var name string
		e := tx.QueryRow(ctx, `
			SELECT id, name FROM customer
			 WHERE company_id = $1 AND phone = $2 AND is_active
			 LIMIT 1`, scope.CompanyID, phone).Scan(&customerID, &name)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}

		generated, e := sixDigits()
		if e != nil {
			return e
		}
		// HashSecret rather than HashPassword: the strength policy is about
		// what a PERSON chooses, and this was chosen by a random number
		// generator. Running six digits through the policy would refuse them.
		hash, e := identity.HashSecret(generated)
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO customer_portal_code (
			  tenant_id, company_id, phone, code_hash, expires_at)
			VALUES ($1,$2,$3,$4, now() + make_interval(mins => $5))`,
			scope.TenantID, scope.CompanyID, phone, hash,
			int(codeTTL.Minutes())); e != nil {
			return e
		}

		return s.queue.QueueNotification(ctx, tx, identity.NotifyPayload{
			Kind: NotifyKindPortalCode, Email: phone, FullName: name,
			Code: generated, ExpiresInMinutes: int(codeTTL.Minutes()),
		})
	})
	return db.Translate(err, "")
}

// NotifyKindPortalCode is the `notify.send` payload kind for a portal code.
//
// `Email` carries the PHONE here, which is the one place the shared payload
// shape is bent: the queue's field is named for the staff case and adding a
// second address field would mean every existing sender had to learn which one
// to read. The kind says which it is.
const NotifyKindPortalCode = "customer_portal_code"

// sixDigits is the code a person reads off a message and types.
//
// Six digits rather than a longer random string: it is typed by hand, from a
// phone, sometimes by somebody who is not comfortable with letters and cases.
// The rate limit and the ten-minute life are what make six digits enough, not
// the length.
func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// Exchange turns a phone and a code into a session token.
func (s *Service) Exchange(
	ctx context.Context, scope Scope, phone, code, agent, ip string,
) (string, Caller, error) {
	phone = strings.TrimSpace(phone)
	code = strings.TrimSpace(code)
	if phone == "" || code == "" {
		return "", Caller{}, errs.New(errs.CodeUnauthenticated,
			"That code is not right, or it has expired.")
	}

	var token string
	var out Caller
	// Set when the code was wrong. The transaction returns nil in that case so
	// the attempt counter COMMITS: returning an error would roll it back, and a
	// guesser who could roll back their own attempts has unlimited guesses.
	wrong := false

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var codeID uuid.UUID
		var hash string
		var attempts int
		e := tx.QueryRow(ctx, `
			SELECT id, code_hash, attempts FROM customer_portal_code
			 WHERE company_id = $1 AND phone = $2
			   AND used_at IS NULL AND expires_at > now()
			 ORDER BY requested_at DESC
			 LIMIT 1
			 FOR UPDATE`, scope.CompanyID, phone).Scan(
			&codeID, &hash, &attempts)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeUnauthenticated,
				"That code is not right, or it has expired.")
		}
		if e != nil {
			return e
		}
		if attempts >= maxAttempts {
			return errs.New(errs.CodeUnauthenticated,
				"That code has been tried too many times. Ask for a new one.")
		}

		ok, e := identity.VerifyPassword(hash, code)
		if e != nil {
			return e
		}
		if !ok {
			if _, e := tx.Exec(ctx,
				`UPDATE customer_portal_code SET attempts = attempts + 1
				  WHERE id = $1`, codeID); e != nil {
				return e
			}
			wrong = true
			return nil
		}

		if _, e := tx.Exec(ctx,
			`UPDATE customer_portal_code SET used_at = now() WHERE id = $1`,
			codeID); e != nil {
			return e
		}

		var customerID uuid.UUID
		var name string
		if e := tx.QueryRow(ctx, `
			SELECT id, name FROM customer
			 WHERE company_id = $1 AND phone = $2 AND is_active
			 LIMIT 1`, scope.CompanyID, phone).Scan(&customerID, &name); e != nil {
			return errs.New(errs.CodeUnauthenticated,
				"That code is not right, or it has expired.")
		}

		// The portal account is created on first successful sign-in. See the
		// migration note: rows for people who have never used the portal would
		// be personal data held for no purpose.
		var portalUserID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO customer_portal_user (
			  tenant_id, company_id, customer_id, phone, last_seen_at)
			VALUES ($1,$2,$3,$4, now())
			ON CONFLICT (customer_id) DO UPDATE SET
			  last_seen_at = now(), phone = excluded.phone, is_active = true
			RETURNING id`,
			scope.TenantID, scope.CompanyID, customerID, phone).
			Scan(&portalUserID); e != nil {
			return e
		}

		issued, e := s.issue(ctx, tx, scope.TenantID, portalUserID,
			"customer_portal_session", customerSessionTTL, agent, ip)
		if e != nil {
			return e
		}
		token = issued
		out = Caller{
			Scope: scope, PortalUserID: portalUserID,
			CustomerID: &customerID, Name: name,
		}
		return nil
	})

	if err != nil {
		return "", Caller{}, db.Translate(err, "")
	}
	if wrong {
		return "", Caller{}, errs.New(errs.CodeUnauthenticated,
			"That code is not right, or it has expired.")
	}
	return token, out, nil
}

// ---------------------------------------------------------------------------
// Supplier sign-in
// ---------------------------------------------------------------------------

// SupplierSignIn exchanges an email and a password for a session.
func (s *Service) SupplierSignIn(
	ctx context.Context, scope Scope, email, password, agent, ip string,
) (string, Caller, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return "", Caller{}, errs.New(errs.CodeUnauthenticated,
			"That email address and password do not match.")
	}

	var token string
	var out Caller
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var portalUserID, supplierID uuid.UUID
		var hash, name string
		e := tx.QueryRow(ctx, `
			SELECT id, supplier_id, password_hash, full_name
			  FROM supplier_portal_user
			 WHERE company_id = $1 AND email = $2 AND is_active`,
			scope.CompanyID, email).Scan(
			&portalUserID, &supplierID, &hash, &name)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeUnauthenticated,
				"That email address and password do not match.")
		}
		if e != nil {
			return e
		}

		ok, e := identity.VerifyPassword(hash, password)
		if e != nil {
			return e
		}
		if !ok {
			return errs.New(errs.CodeUnauthenticated,
				"That email address and password do not match.")
		}

		if _, e := tx.Exec(ctx,
			`UPDATE supplier_portal_user SET last_seen_at = now()
			  WHERE id = $1`, portalUserID); e != nil {
			return e
		}

		issued, e := s.issue(ctx, tx, scope.TenantID, portalUserID,
			"supplier_portal_session", supplierSessionTTL, agent, ip)
		if e != nil {
			return e
		}
		token = issued
		out = Caller{
			Scope: scope, PortalUserID: portalUserID,
			SupplierID: &supplierID, Name: name,
		}
		return nil
	})
	return token, out, db.Translate(err, "")
}

// InviteSupplier creates a portal login for a supplier contact.
//
// The staff side of F3. The password is set by the person inviting and handed
// over out of band, which is how a small shop actually gives a supplier access
// — an invitation email would need a mail provider a shop may not have.
func (s *Service) InviteSupplier(
	ctx context.Context, scope Scope, actorID, supplierID uuid.UUID,
	name, email, password string,
) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if strings.TrimSpace(name) == "" || email == "" {
		return errs.New(errs.CodeInvalidInput,
			"Name the contact and give their email address.")
	}
	if len(password) < 12 {
		return errs.New(errs.CodeInvalidInput,
			"A supplier password needs at least twelve characters. They are "+
				"accepting orders that commit their business.")
	}

	// The full policy here: a supplier password is chosen by a person and
	// protects the act of committing their business to an order.
	hash, err := identity.HashPassword(password)
	if err != nil {
		return errs.New(errs.CodeInvalidInput, err.Error())
	}

	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			var exists bool
			if e := tx.QueryRow(ctx,
				`SELECT true FROM supplier WHERE id = $1 AND company_id = $2`,
				supplierID, scope.CompanyID).Scan(&exists); e != nil {
				if e == pgx.ErrNoRows {
					return errs.New(errs.CodeNotFound,
						"That supplier was not found.")
				}
				return e
			}

			_, e := tx.Exec(ctx, `
				INSERT INTO supplier_portal_user (
				  tenant_id, company_id, supplier_id, full_name, email,
				  password_hash, invited_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (company_id, email) DO UPDATE SET
				  full_name = excluded.full_name,
				  password_hash = excluded.password_hash,
				  is_active = true`,
				scope.TenantID, scope.CompanyID, supplierID,
				strings.TrimSpace(name), email, hash, actorID)
			return db.Translate(e, "That contact could not be invited.")
		}), "")
}

// RevokeSupplier turns a supplier contact off and ends their sessions.
func (s *Service) RevokeSupplier(
	ctx context.Context, scope Scope, portalUserID uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE supplier_portal_user SET is_active = false
				 WHERE id = $1 AND company_id = $2`,
				portalUserID, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That contact was not found.")
			}
			// Turning the account off is not enough on its own: a session
			// already issued would keep working until it expired.
			_, e = tx.Exec(ctx, `
				UPDATE supplier_portal_session SET revoked_at = now()
				 WHERE portal_user_id = $1 AND revoked_at IS NULL`,
				portalUserID)
			return e
		}), "")
}

// SupplierContacts lists a company's supplier logins, for the staff screen.
type SupplierContact struct {
	ID           uuid.UUID `json:"id"`
	SupplierID   uuid.UUID `json:"supplier_id"`
	SupplierName string    `json:"supplier_name"`
	FullName     string    `json:"full_name"`
	Email        string    `json:"email"`
	IsActive     bool      `json:"is_active"`
	InvitedAt    string    `json:"invited_at"`
	LastSeenAt   string    `json:"last_seen_at,omitempty"`
}

// SupplierContacts returns them.
func (s *Service) SupplierContacts(
	ctx context.Context, scope Scope,
) ([]SupplierContact, error) {
	out := []SupplierContact{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT u.id, u.supplier_id, s.legal_name, u.full_name, u.email,
			       u.is_active, u.invited_at, u.last_seen_at
			FROM supplier_portal_user u
			JOIN supplier s ON s.id = u.supplier_id
			WHERE u.company_id = $1
			ORDER BY s.legal_name, u.full_name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var c SupplierContact
			var invited time.Time
			var seen *time.Time
			if e := rows.Scan(&c.ID, &c.SupplierID, &c.SupplierName,
				&c.FullName, &c.Email, &c.IsActive, &invited, &seen); e != nil {
				return e
			}
			c.InvitedAt = invited.UTC().Format(time.RFC3339)
			if seen != nil {
				c.LastSeenAt = seen.UTC().Format(time.RFC3339)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// issue mints a token and records its hash.
//
// The table name is one of two constants chosen by the caller, never input.
func (s *Service) issue(
	ctx context.Context, tx pgx.Tx, tenantID, portalUserID uuid.UUID,
	table string, ttl time.Duration, agent, ip string,
) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	_, err := tx.Exec(ctx, `
		INSERT INTO `+table+` (
		  tenant_id, portal_user_id, token_hash, expires_at, user_agent, ip)
		VALUES ($1,$2,$3, now() + make_interval(mins => $4), nullif($5,''),
		        nullif($6,'')::inet)`,
		tenantID, portalUserID, hashToken(token), int(ttl.Minutes()),
		agent, ip)
	if err != nil {
		return "", err
	}
	return token, nil
}

// hashToken is SHA-256, not a password hash.
//
// A session token is 32 bytes of entropy from a CSPRNG; there is nothing to
// guess and nothing to slow down. Argon2 here would cost a hundred milliseconds
// on every single request for no gain at all — which is a different judgement
// from the code and the password above, where the secret is short or chosen by
// a person.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Authenticate resolves a portal token to its caller.
//
// Both tables are tried, and the one that matches says which portal the token
// belongs to. A token cannot be in both: they are 32 random bytes.
func (s *Service) Authenticate(
	ctx context.Context, scope Scope, token string,
) (Caller, error) {
	if strings.TrimSpace(token) == "" {
		return Caller{}, errs.New(errs.CodeUnauthenticated,
			"Sign in to see this.")
	}
	hash := hashToken(token)

	var out Caller
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var portalUserID, customerID uuid.UUID
		var name string
		e := tx.QueryRow(ctx, `
			SELECT s.portal_user_id, u.customer_id, c.name
			FROM customer_portal_session s
			JOIN customer_portal_user u ON u.id = s.portal_user_id
			JOIN customer c ON c.id = u.customer_id
			WHERE s.token_hash = $1 AND s.revoked_at IS NULL
			  AND s.expires_at > now() AND u.is_active
			  AND u.company_id = $2`,
			hash, scope.CompanyID).Scan(&portalUserID, &customerID, &name)
		if e == nil {
			out = Caller{
				Scope: scope, PortalUserID: portalUserID,
				CustomerID: &customerID, Name: name,
			}
			_, e = tx.Exec(ctx,
				`UPDATE customer_portal_session SET last_used_at = now()
				  WHERE token_hash = $1`, hash)
			return e
		}
		if e != pgx.ErrNoRows {
			return e
		}

		var supplierID uuid.UUID
		e = tx.QueryRow(ctx, `
			SELECT s.portal_user_id, u.supplier_id, u.full_name
			FROM supplier_portal_session s
			JOIN supplier_portal_user u ON u.id = s.portal_user_id
			WHERE s.token_hash = $1 AND s.revoked_at IS NULL
			  AND s.expires_at > now() AND u.is_active
			  AND u.company_id = $2`,
			hash, scope.CompanyID).Scan(&portalUserID, &supplierID, &name)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeUnauthenticated,
				"That session has expired. Sign in again.")
		}
		if e != nil {
			return e
		}
		out = Caller{
			Scope: scope, PortalUserID: portalUserID,
			SupplierID: &supplierID, Name: name,
		}
		_, e = tx.Exec(ctx,
			`UPDATE supplier_portal_session SET last_used_at = now()
			  WHERE token_hash = $1`, hash)
		return e
	})
	return out, db.Translate(err, "")
}

// SignOut ends one session.
func (s *Service) SignOut(
	ctx context.Context, scope Scope, token string,
) error {
	hash := hashToken(token)
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			for _, table := range []string{
				"customer_portal_session", "supplier_portal_session",
			} {
				if _, e := tx.Exec(ctx,
					`UPDATE `+table+` SET revoked_at = now()
					  WHERE token_hash = $1 AND revoked_at IS NULL`,
					hash); e != nil {
					return e
				}
			}
			return nil
		}), "")
}
