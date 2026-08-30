// Package integration is outbound webhooks and API keys (blueprint H6).
//
// # A key is a credential, so it is stored the way a credential is stored
//
// The key itself is shown exactly once, at creation, and never again. What is
// kept is a SHA-256 of it and the first few characters so a person can tell two
// keys apart in a list. A readable key column would be a breach that hands over
// every integration a shop has, and "we can show you your key again" is the
// feature that makes it one.
//
// SHA-256 rather than bcrypt, unlike a password. A password is short, chosen by
// a person, and has to survive an offline guessing attack, which is what a slow
// hash buys. An API key here is 32 bytes from crypto/rand; there is nothing to
// guess, and a slow hash on the authentication path of a machine integration
// would cost real latency for no security.
//
// # A key can never be an escalation
//
// Its permissions are checked against what its creator holds at the moment of
// creation. Somebody cannot mint a key that does more than they can, and a key
// whose creator later loses a permission is checked against the key's own list
// — narrowing a person must not silently widen the machine acting for them,
// which is why the list is stored rather than resolved through the creator.
//
// # A webhook secret is encrypted, and the URL must be HTTPS
//
// The receiver verifies an HMAC over the body to know a delivery is genuinely
// from here rather than from anybody who learned the URL. The database refuses
// a plain-HTTP endpoint outright — there is no configuration option for it,
// because the alternative is a shop's sales going over the wire in clear.
package integration

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
)

// Events a webhook can subscribe to.
//
// A closed list, checked on save. An endpoint subscribed to "sale.complete"
// when the product emits "sale.completed" is an integration that silently never
// fires, and the shop finds out weeks later when a report does not reconcile.
var Events = []string{
	"sale.completed",
	"sale.returned",
	"invoice.issued",
	"order.confirmed",
	"order.delivered",
	"stock.low",
	"stock.received",
	"purchase.received",
	"payment.received",
	"payment.made",
	"customer.created",
	"product.created",
}

// Service manages endpoints, keys and the delivery queue.
type Service struct {
	pool   *db.Pool
	cipher *secrets.Cipher
}

// NewService builds the service.
//
// The cipher may be nil in development, where there is no keyring. Creating an
// endpoint then fails with a sentence saying so rather than storing a secret in
// clear, which is the failure a developer can act on.
func NewService(pool *db.Pool, cipher *secrets.Cipher) *Service {
	return &Service{pool: pool, cipher: cipher}
}

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Endpoint is one place events are sent.
type Endpoint struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Active    bool      `json:"is_active"`
	Events    []string  `json:"events"`
	Created   string    `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`

	// How it has been behaving. An endpoint list without these is a list of
	// URLs somebody has to test one at a time to find the broken one.
	Queued    int    `json:"queued"`
	Failed    int    `json:"failed"`
	LastSent  string `json:"last_delivered_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// Delivery is one attempt to send one event to one endpoint.
type Delivery struct {
	ID          uuid.UUID `json:"id"`
	Event       string    `json:"event"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	Response    *int      `json:"response_status,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	NextAttempt string    `json:"next_attempt_at,omitempty"`
	Delivered   string    `json:"delivered_at,omitempty"`
	CreatedAt   string    `json:"created_at"`
}

// Key is an API key as a list shows it — never the key itself.
type Key struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Prefix      string    `json:"key_prefix"`
	Permissions []string  `json:"permissions"`
	LastUsed    string    `json:"last_used_at,omitempty"`
	ExpiresAt   string    `json:"expires_at,omitempty"`
	RevokedAt   string    `json:"revoked_at,omitempty"`
	CreatedAt   string    `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
	// Live folds "not revoked and not expired" into one answer, so a screen
	// does not leave somebody comparing a date against today in their head.
	Live bool `json:"is_live"`
}

// Minted is a key at the one moment it can be read.
type Minted struct {
	Key
	// Secret is the key itself. Returned once, on creation, and never
	// retrievable: what the database keeps is a hash.
	Secret string `json:"secret"`
}

// --- webhooks -------------------------------------------------------------

// SaveEndpoint registers a place to send events.
func (s *Service) SaveEndpoint(
	ctx context.Context, scope Scope, name, url string, events []string,
) (Endpoint, error) {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return Endpoint{}, errs.Validation("Give the endpoint a name.").
			WithField("name", "It is how you will tell two of them apart.")
	}
	if !strings.HasPrefix(url, "https://") {
		return Endpoint{}, errs.Validation(
			"A webhook must be an https:// address.").
			WithField("url",
				"Plain HTTP would put this shop's sales over the wire in clear.")
	}
	for _, e := range events {
		if !knownEvent(e) {
			return Endpoint{}, errs.Newf(errs.CodeInvalidInput,
				"This product does not send an event called %q.", e)
		}
	}
	if len(events) == 0 {
		return Endpoint{}, errs.Validation(
			"Say which events this endpoint wants.").
			WithField("events",
				"An endpoint subscribed to nothing would never be called.")
	}
	if s.cipher == nil {
		return Endpoint{}, errs.New(errs.CodeUnavailable,
			"This installation has no data encryption key, so a webhook "+
				"signing secret cannot be stored safely. Set one first.")
	}

	// The secret the receiver verifies against. Generated here rather than
	// accepted from the request: a secret somebody typed is a secret they have
	// used somewhere else.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Endpoint{}, err
	}
	sealed, err := s.cipher.Seal([]byte(hex.EncodeToString(secret)))
	if err != nil {
		return Endpoint{}, err
	}

	encoded, err := json.Marshal(events)
	if err != nil {
		return Endpoint{}, err
	}

	var id uuid.UUID
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO webhook_endpoint
			  (tenant_id, company_id, name, url, events, secret_enc, created_by)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, name, url, string(encoded),
			sealed, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That endpoint could not be saved.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "webhook_endpoint_created",
			EntityType: "webhook_endpoint", EntityID: &id,
			After: map[string]any{"name": name, "url": url, "events": events},
		})
	})
	if err != nil {
		return Endpoint{}, err
	}
	return s.Endpoint(ctx, scope, id)
}

// Endpoints lists what is registered, with how each has been behaving.
func (s *Service) Endpoints(ctx context.Context, scope Scope) ([]Endpoint, error) {
	out := []Endpoint{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, endpointSelect+`
			WHERE w.company_id = $1
			ORDER BY w.name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			ep, e := scanEndpoint(rows)
			if e != nil {
				return e
			}
			out = append(out, ep)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Endpoint reads one.
func (s *Service) Endpoint(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Endpoint, error) {
	var out Endpoint
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, endpointSelect+`
			WHERE w.id = $1 AND w.company_id = $2`, id, scope.CompanyID)
		ep, e := scanEndpoint(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That endpoint was not found.")
		}
		out = ep
		return e
	})
	return out, db.Translate(err, "")
}

// SetEndpointActive turns an endpoint on or off.
//
// Off rather than deleted, so the delivery history that names it survives: a
// shop asking why its accounting system missed a week of sales needs to see the
// failures, and deleting the endpoint would take them with it.
func (s *Service) SetEndpointActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE webhook_endpoint SET is_active = $3
				WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID, active)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound, "That endpoint was not found.")
			}
			return nil
		}), "")
}

// Deliveries is the history for one endpoint, newest first.
func (s *Service) Deliveries(
	ctx context.Context, scope Scope, endpointID uuid.UUID,
) ([]Delivery, error) {
	out := []Delivery{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Asked about first, so an endpoint on another shop's books is refused
		// rather than answered with an empty history. "This endpoint has never
		// been called" and "this endpoint is not yours" are different
		// sentences, and only one of them is true.
		var exists bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM webhook_endpoint
			               WHERE id = $1 AND company_id = $2)`,
			endpointID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return errs.New(errs.CodeNotFound, "That endpoint was not found.")
		}

		rows, e := tx.Query(ctx, `
			SELECT d.id, d.event, d.status, d.attempts, d.response_status,
			       coalesce(d.last_error, ''), d.next_attempt_at,
			       d.delivered_at, d.created_at
			FROM webhook_delivery d
			JOIN webhook_endpoint w ON w.id = d.endpoint_id
			WHERE d.endpoint_id = $1 AND w.company_id = $2
			ORDER BY d.created_at DESC
			LIMIT 200`, endpointID, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d Delivery
			var next, delivered *time.Time
			var created time.Time
			if e := rows.Scan(&d.ID, &d.Event, &d.Status, &d.Attempts,
				&d.Response, &d.LastError, &next, &delivered,
				&created); e != nil {
				return e
			}
			if next != nil {
				d.NextAttempt = next.UTC().Format(time.RFC3339)
			}
			if delivered != nil {
				d.Delivered = delivered.UTC().Format(time.RFC3339)
			}
			d.CreatedAt = created.UTC().Format(time.RFC3339)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Emit queues an event for every endpoint that asked for it.
//
// Takes a tx so it runs inside the transaction of whatever happened: a sale
// that committed and a webhook that did not is an integration silently missing
// a record, and a webhook queued for a sale that rolled back is worse — it
// tells somebody's accounting system about a sale that never happened.
//
// Queued, never sent inline. An HTTP call inside a sale's transaction would
// hold a database connection open for as long as somebody else's server takes
// to answer, and a slow receiver would become a slow till.
func Emit(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID uuid.UUID, event string, payload any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO webhook_delivery
		  (tenant_id, endpoint_id, event, payload, next_attempt_at)
		SELECT $1, w.id, $3, $4::jsonb, now()
		FROM webhook_endpoint w
		WHERE w.company_id = $2 AND w.is_active
		  AND w.events @> to_jsonb($3::text)`,
		tenantID, companyID, event, string(body))
	return db.Translate(err, "That event could not be queued.")
}

// Sign is the signature a receiver checks.
//
// HMAC-SHA256 over the exact bytes sent, hex encoded. Exported because the
// dispatcher and the tests both need it, and because it is the one part of this
// module a receiver has to reimplement.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

const endpointSelect = `
	SELECT w.id, w.name, w.url, w.is_active, w.events::text, w.created_at,
	       coalesce(u.full_name, ''),
	       coalesce((SELECT count(*) FROM webhook_delivery d
	                 WHERE d.endpoint_id = w.id AND d.status = 'queued'), 0)::int,
	       coalesce((SELECT count(*) FROM webhook_delivery d
	                 WHERE d.endpoint_id = w.id
	                   AND d.status IN ('failed', 'abandoned')), 0)::int,
	       (SELECT max(d.delivered_at) FROM webhook_delivery d
	        WHERE d.endpoint_id = w.id),
	       coalesce((SELECT d.last_error FROM webhook_delivery d
	                 WHERE d.endpoint_id = w.id AND d.last_error IS NOT NULL
	                 ORDER BY d.created_at DESC LIMIT 1), '')
	FROM webhook_endpoint w
	LEFT JOIN app_user u ON u.id = w.created_by`

type scanner interface{ Scan(dest ...any) error }

func scanEndpoint(row scanner) (Endpoint, error) {
	var e Endpoint
	var events string
	var created time.Time
	var last *time.Time
	if err := row.Scan(&e.ID, &e.Name, &e.URL, &e.Active, &events, &created,
		&e.CreatedBy, &e.Queued, &e.Failed, &last, &e.LastError); err != nil {
		return Endpoint{}, err
	}
	if err := json.Unmarshal([]byte(events), &e.Events); err != nil {
		return Endpoint{}, err
	}
	e.Created = created.UTC().Format(time.RFC3339)
	if last != nil {
		e.LastSent = last.UTC().Format(time.RFC3339)
	}
	return e, nil
}

func knownEvent(name string) bool {
	for _, e := range Events {
		if e == name {
			return true
		}
	}
	return false
}
